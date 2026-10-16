package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestModelCatalogSourcesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	source := filepath.Join(t.TempDir(), "models.json")
	raw := []byte(fmt.Sprintf("models:\n  catalog: %q\ntls-handshake-timeout-seconds: 15\n", source))
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models.Catalog != source || cfg.TLSHandshakeTimeout() != 15*time.Second {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.SourceHash != ComputeSourceHash(raw) {
		t.Fatal("source hash does not describe parsed bytes")
	}
	if err := SaveConfigPreserveComments(path, cfg); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models.Catalog != source {
		t.Fatal("catalog source lost on save")
	}
	cfg.Models.Catalog = ""
	if err := SaveConfigPreserveComments(path, cfg); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models.Catalog != "" {
		t.Fatal("cleared source retained on save")
	}
}

func TestModelCatalogSourcesValidation(t *testing.T) {
	for _, source := range []string{"relative.json", "./models.json", "~/models.json", "http://example.com/models", "ftp://example.com/models", "file:///tmp/models.json", "https:///models"} {
		t.Run(source, func(t *testing.T) {
			raw := []byte(fmt.Sprintf("models:\n  catalog: %q\n", source))
			if _, err := ParseConfigBytes(raw); err == nil {
				t.Fatal("parser accepted invalid source")
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("loader accepted invalid source")
			}
		})
	}
	for _, raw := range []string{"models: {}", "models: {catalog: ''}", "models: {catalog: 'https://example.com/models.json'}"} {
		if _, err := ParseConfigBytes([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := ParseConfigBytes([]byte("tls-handshake-timeout-seconds: -1"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLSHandshakeTimeout() != 0 || cfg.TLSHandshakeTimeoutSeconds != 0 {
		t.Fatal("negative timeout was not normalized")
	}
}
