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

func TestRecordCodexAuthHealthPrunesMissingAuthFiles(t *testing.T) {
	dir := t.TempDir()
	authDir := filepath.Join(dir, "auths")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatalf("mkdir authDir: %v", err)
	}
	present := "codex-present.json"
	gone := "codex-gone.json"
	if err := os.WriteFile(filepath.Join(authDir, present), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write present: %v", err)
	}

	healthPath := filepath.Join(dir, "codex-auth-health.json")
	seed := codexAuthHealthFile{
		Version:   codexAuthHealthVersion,
		UpdatedAt: time.Now().Unix(),
		Auths: map[string]CodexAuthHealthState{
			gone: {Status: "dead", Source: "proxy_health", Reason: "token_invalidated", ObservedAt: time.Now().Unix() - 60},
		},
	}
	raw, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(healthPath, raw, 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	cfg := &config.Config{
		AuthDir:         authDir,
		CodexAuthHealth: config.CodexAuthHealthConfig{Enabled: true, Path: healthPath},
	}
	auth := &cliproxyauth.Auth{FileName: filepath.Join(authDir, present), Provider: "codex"}

	RecordCodexAuthHealthResponse(cfg, auth, "codex", http.StatusOK, nil, nil)

	data := readCodexAuthHealthFile(t, healthPath)
	if _, ok := data.Auths[gone]; ok {
		t.Fatalf("expected %q to be pruned (auth file gone)", gone)
	}
	if _, ok := data.Auths[present]; !ok {
		t.Fatalf("expected %q to be present (current observation)", present)
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
