package helps

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRecordCodexAuthHealthResponseDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{CodexAuthHealth: config.CodexAuthHealthConfig{Path: filepath.Join(dir, "codex-auth-health.json")}}
	auth := &cliproxyauth.Auth{ID: "codex-test.json", Provider: "codex"}

	RecordCodexAuthHealthResponse(cfg, auth, "codex", http.StatusTooManyRequests, nil, []byte(`{"error":{"type":"usage_limit_reached"}}`))

	if _, err := os.Stat(cfg.CodexAuthHealth.Path); !os.IsNotExist(err) {
		t.Fatalf("expected no health file when disabled, stat err=%v", err)
	}
}

func TestRecordCodexAuthHealthResponseWritesQuotaState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex-auth-health.json")
	resetAt := time.Now().Add(time.Hour).Unix()
	headers := http.Header{}
	headers.Set("X-Codex-Primary-Used-Percent", "98")
	headers.Set("X-Codex-Primary-Reset-At", itoa64(resetAt))
	cfg := &config.Config{CodexAuthHealth: config.CodexAuthHealthConfig{Enabled: true, Path: path}}
	auth := &cliproxyauth.Auth{ID: "codex-test.json", Provider: "codex", Label: "user@example.com"}

	RecordCodexAuthHealthResponse(cfg, auth, "codex", http.StatusOK, headers, nil)

	data := readCodexAuthHealthFile(t, path)
	state := data.Auths["codex-test.json"]
	if state.Status != "quota" {
		t.Fatalf("status = %q, want quota", state.Status)
	}
	if state.Reason != "primary_98pct" {
		t.Fatalf("reason = %q, want primary_98pct", state.Reason)
	}
	if state.ExpiresAt != resetAt {
		t.Fatalf("expires_at = %d, want %d", state.ExpiresAt, resetAt)
	}
}

func TestRecordCodexAuthHealthResponseWritesDeadState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex-auth-health.json")
	cfg := &config.Config{CodexAuthHealth: config.CodexAuthHealthConfig{Enabled: true, Path: path}}
	auth := &cliproxyauth.Auth{FileName: "/tmp/codex-user.json", Provider: "codex", Label: "user@example.com"}

	RecordCodexAuthHealthResponse(cfg, auth, "codex", http.StatusPaymentRequired, nil, []byte(`{"error":{"message":"subscription expired"}}`))

	data := readCodexAuthHealthFile(t, path)
	state := data.Auths["codex-user.json"]
	if state.Status != "dead" {
		t.Fatalf("status = %q, want dead", state.Status)
	}
	if state.Reason != "subscription_expired" {
		t.Fatalf("reason = %q, want subscription_expired", state.Reason)
	}
}

func readCodexAuthHealthFile(t *testing.T, path string) codexAuthHealthFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read health file: %v", err)
	}
	var data codexAuthHealthFile
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal health file: %v", err)
	}
	return data
}

func itoa64(v int64) string {
	return strconv.FormatInt(v, 10)
}
