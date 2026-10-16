package auth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	codexauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestFileTokenStorePreservesMetadataAndRefreshedTokens(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "account.json")
			if err := os.WriteFile(path, []byte(`{"access_token":"old-test-token","refresh_token":"old-test-refresh","id_token":"old-test-id","disabled":true,"prefix":"personal","websockets":false,"note":"keep","nested":{"key":"keep"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			record := &coreauth.Auth{ID: "account.json", FileName: "account.json", Provider: provider, Metadata: map[string]any{"type": provider, "access_token": "new-test-token"}}
			if provider == "claude" {
				record.Storage = &claudeauth.ClaudeTokenStorage{AccessToken: "new-test-token"}
			} else {
				record.Storage = &codexauth.CodexTokenStorage{AccessToken: "new-test-token"}
			}
			var existing map[string]any
			seed, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(seed, &existing); err != nil {
				t.Fatal(err)
			}
			coreauth.MergeExistingAuthMetadata(record, existing)
			store := NewFileTokenStore()
			store.SetBaseDir(dir)
			if _, err := store.Save(context.Background(), record); err != nil {
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
			if saved["access_token"] != "new-test-token" || saved["refresh_token"] == "old-test-refresh" || saved["id_token"] == "old-test-id" {
				t.Fatal("old token lifecycle metadata retained")
			}
			if saved["note"] != "keep" || saved["prefix"] != "personal" || saved["websockets"] != false || saved["nested"].(map[string]any)["key"] != "keep" {
				t.Fatal("custom metadata lost")
			}
			record.Metadata["access_token"] = "refreshed-test-token"
			record.Disabled = false
			record.Prefix = ""
			record.ProxyURL = ""
			delete(record.Metadata, "prefix")
			delete(record.Metadata, "proxy_url")
			delete(record.Metadata, "note")
			if _, err := store.Save(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			raw, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &saved); err != nil {
				t.Fatal(err)
			}
			saved = nil
			if err := json.Unmarshal(raw, &saved); err != nil {
				t.Fatal(err)
			}
			if _, exists := saved["note"]; exists {
				t.Fatal("deleted note restored from old file")
			}
			if _, exists := saved["prefix"]; exists {
				t.Fatal("cleared prefix restored from old file")
			}
			if saved["access_token"] != "refreshed-test-token" || saved["disabled"] != false {
				t.Fatal("metadata token refresh or explicit enable lost")
			}
		})
	}
}

func TestTokenSerializationErrorDoesNotTruncateCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	original := []byte(`{"access_token":"existing-test-token"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	storage := &claudeauth.ClaudeTokenStorage{AccessToken: "new-test-token", Metadata: map[string]any{"invalid": make(chan int)}}
	if err := storage.SaveTokenToFile(path); err == nil {
		t.Fatal("expected serialization error")
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(original) {
		t.Fatal("serialization error truncated existing credentials")
	}
}
