package executor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestCodexAssemblesSplitCompletionInBothModes(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[stream], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\ndata: \"response\":{\"id\":\"resp_split\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
			}))
			defer server.Close()
			executor := NewCodexExecutor(&config.Config{})
			auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": server.URL, "api_key": "test"}}
			req := cliproxyexecutor.Request{Model: "gpt-5.5", Payload: []byte(`{"model":"gpt-5.5","input":"hello"}`)}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("codex"), Stream: stream}
			if !stream {
				result, err := executor.Execute(context.Background(), auth, req, opts)
				if err != nil {
					t.Fatal(err)
				}
				if gjson.GetBytes(result.Payload, "response.id").String() != "resp_split" {
					t.Fatalf("unexpected response: %s", result.Payload)
				}
				return
			}
			result, err := executor.ExecuteStream(context.Background(), auth, req, opts)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			for chunk := range result.Chunks {
				if chunk.Err != nil {
					t.Fatal(chunk.Err)
				}
				output.Write(chunk.Payload)
			}
			if !strings.Contains(output.String(), `"id":"resp_split"`) {
				t.Fatalf("missing completed event: %s", output.String())
			}
		})
	}
}

func TestCodexReportsPrematureEOFWithoutReplayingOutput(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "after output"}[partial], func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if partial {
					_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
				}
			}))
			defer server.Close()
			executor := NewCodexExecutor(&config.Config{})
			auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": server.URL, "api_key": "test"}}
			result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{Model: "gpt-5.5", Payload: []byte(`{"model":"gpt-5.5","input":"hello"}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("codex"), Stream: true})
			if err != nil {
				t.Fatal(err)
			}
			var streamErr error
			var output bytes.Buffer
			for chunk := range result.Chunks {
				if chunk.Err != nil {
					streamErr = chunk.Err
				}
				output.Write(chunk.Payload)
			}
			if streamErr == nil {
				t.Fatal("EOF reported as success")
			}
			if got := statusCodeFromTestError(t, streamErr); got != http.StatusBadGateway {
				t.Fatalf("status = %d, error = %v", got, streamErr)
			}
			if partial && strings.Count(output.String(), `"delta":"hello"`) != 1 {
				t.Fatalf("output was lost or replayed: %s", output.String())
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d", requests.Load())
			}
		})
	}
}

type terminalErrorBody struct {
	*strings.Reader
	closed atomic.Bool
}

func (b *terminalErrorBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}
func (b *terminalErrorBody) Close() error { b.closed.Store(true); return nil }

type reliabilityRoundTripper func(*http.Request) (*http.Response, error)

func (f reliabilityRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestClaudeCompletionIgnoresTrailingTransportFailure(t *testing.T) {
	for _, format := range []string{"claude", "openai"} {
		t.Run(format, func(t *testing.T) {
			body := &terminalErrorBody{Reader: strings.NewReader("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-5\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ctx = context.WithValue(ctx, "cliproxy.roundtripper", reliabilityRoundTripper(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
			}))
			executor := NewClaudeExecutor(&config.Config{})
			auth := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "test", "base_url": "https://api.anthropic.com"}}
			payload := []byte(`{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hello"}],"max_tokens":16}`)
			result, err := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "claude-sonnet-5", Payload: payload}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString(format), Stream: true})
			if err != nil {
				t.Fatal(err)
			}
			for chunk := range result.Chunks {
				if chunk.Err != nil {
					t.Fatalf("completed stream failed: %v", chunk.Err)
				}
			}
			if !body.closed.Load() {
				t.Fatal("upstream body leaked")
			}
		})
	}
}

func TestCodexManagerDoesNotReplayPartialOutput(t *testing.T) {
	for _, tc := range []struct {
		name       string
		partial    bool
		disabled   bool
		wantSecond int32
		wantError  bool
	}{
		{name: "partial with eligible backup", partial: true, wantError: true},
		{name: "empty with eligible backup", wantSecond: 1},
		{name: "empty with disabled backup", disabled: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var first, second atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if r.Header.Get("Authorization") == "Bearer first" {
					first.Add(1)
					if tc.partial {
						_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"once\"}\n\n")
					}
					return
				}
				second.Add(1)
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_backup\",\"output\":[]}}\n\n")
			}))
			defer server.Close()
			manager := cliproxyauth.NewManager(nil, &cliproxyauth.FillFirstSelector{}, nil)
			manager.SetRetryConfig(0, 0, 0)
			manager.RegisterExecutor(NewCodexExecutor(&config.Config{}))
			reg := registry.GetGlobalRegistry()
			for i, key := range []string{"first", "second"} {
				id := t.Name() + "-" + key
				auth := &cliproxyauth.Auth{ID: id, Provider: "codex", Status: cliproxyauth.StatusActive, Disabled: i == 1 && tc.disabled, Attributes: map[string]string{"base_url": server.URL, "api_key": key, "priority": map[bool]string{true: "10", false: "0"}[i == 0]}}
				reg.RegisterClient(id, "codex", []*registry.ModelInfo{{ID: "gpt-5.5"}})
				t.Cleanup(func() { reg.UnregisterClient(id) })
				if _, err := manager.Register(context.Background(), auth); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := manager.ExecuteStream(ctx, []string{"codex"}, cliproxyexecutor.Request{Model: "gpt-5.5", Payload: []byte(`{"model":"gpt-5.5","input":"hello"}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("codex"), Stream: true})
			var output bytes.Buffer
			if err == nil {
				for chunk := range result.Chunks {
					if chunk.Err != nil {
						err = chunk.Err
					}
					output.Write(chunk.Payload)
				}
			}
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
			if first.Load() != 1 || second.Load() != tc.wantSecond {
				t.Fatalf("requests first=%d second=%d, want second=%d", first.Load(), second.Load(), tc.wantSecond)
			}
			if tc.partial && strings.Count(output.String(), `"delta":"once"`) != 1 {
				t.Fatalf("partial output lost or replayed: %s", output.String())
			}
		})
	}
}
