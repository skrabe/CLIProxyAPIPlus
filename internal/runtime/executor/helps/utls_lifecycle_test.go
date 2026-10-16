package helps

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

type lifecycleDialer struct {
	conn  net.Conn
	ready chan struct{}
}

func (d lifecycleDialer) Dial(network, addr string) (net.Conn, error) {
	return d.conn, nil
}

func (d lifecycleDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	close(d.ready)
	return d.conn, nil
}

func TestUTLSHandshakeCancellationAndBound(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller cancellation", true: "handshake bound"}[timeout], func(t *testing.T) {
			client, server := net.Pipe()
			defer server.Close()
			peerClosed := make(chan struct{})
			go func() {
				_, _ = io.Copy(io.Discard, server)
				close(peerClosed)
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ready := make(chan struct{})
			rt := &utlsRoundTripper{dialer: lifecycleDialer{conn: client, ready: ready}}
			if timeout {
				rt.handshakeTimeout = 30 * time.Millisecond
			}
			result := make(chan error, 1)
			go func() {
				_, err := rt.createConnection(ctx, "example.com", "example.com:443")
				result <- err
			}()
			<-ready
			if !timeout {
				cancel()
			}
			select {
			case err := <-result:
				want := context.Canceled
				if timeout {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("handshake did not stop")
			}
			select {
			case <-peerClosed:
			case <-time.After(time.Second):
				t.Fatal("connection leaked after handshake failure")
			}
		})
	}
}

type lifecycleBody struct {
	closed atomic.Int32
}

func (b *lifecycleBody) Read(p []byte) (int, error) { return 0, io.EOF }
func (b *lifecycleBody) Close() error               { b.closed.Add(1); return nil }

func TestUTLSBodyClosesConnectionExactlyOnce(t *testing.T) {
	body := &lifecycleBody{}
	var closed atomic.Int32
	wrapped := &closeConnectionBody{ReadCloser: body, closeConnection: func() error { closed.Add(1); return nil }}
	for range 3 {
		if err := wrapped.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if body.closed.Load() != 1 || closed.Load() != 1 {
		t.Fatalf("close counts body=%d connection=%d", body.closed.Load(), closed.Load())
	}
}

func TestUTLSHTTP2ConnectionClosesWithBody(t *testing.T) {
	for _, cancelBody := range []bool{false, true} {
		t.Run(map[bool]string{false: "body close", true: "canceled body"}[cancelBody], func(t *testing.T) {
			closed := make(chan struct{}, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 2 {
					t.Errorf("protocol = %s", r.Proto)
				}
				_, _ = io.WriteString(w, "hello")
				if cancelBody {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			server.EnableHTTP2 = true
			server.Config.ConnState = func(conn net.Conn, state http.ConnState) {
				if state == http.StateClosed {
					select {
					case closed <- struct{}{}:
					default:
					}
				}
			}
			server.StartTLS()
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			rt := &utlsRoundTripper{dialer: proxy.Direct, rootCAs: roots, handshakeTimeout: time.Second}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			if cancelBody {
				cancel()
			} else {
				data, err := io.ReadAll(resp.Body)
				if err != nil || string(data) != "hello" {
					t.Fatalf("body = %q, error = %v", data, err)
				}
			}
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP/2 connection leaked")
			}
		})
	}
}
