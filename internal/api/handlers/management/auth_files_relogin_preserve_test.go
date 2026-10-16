package management

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestSaveTokenRecordReloginPreservesDisabledAndCustomMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-test.json")
	if err := os.WriteFile(path, []byte(`{"type":"claude","access_token":"old-test-token","disabled":true,"prefix":"personal","note":"keep","weight":3,"websockets":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: dir}, nil)
	h.tokenStore = sdkauth.NewFileTokenStore()
	record := &coreauth.Auth{ID: "claude-test.json", FileName: "claude-test.json", Provider: "claude", Storage: &claudeauth.ClaudeTokenStorage{AccessToken: "new-test-token"}, Metadata: map[string]any{"type": "claude", "access_token": "new-test-token"}}
	if _, err := h.saveTokenRecord(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["disabled"] != true || !record.Disabled || record.Prefix != "personal" || saved["note"] != "keep" || saved["weight"] != float64(3) || saved["websockets"] != false || saved["access_token"] != "new-test-token" {
		t.Fatal("management re-login lost operator configuration or fresh token")
	}
}
