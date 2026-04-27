package executor

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/tidwall/gjson"
)

func TestInjectCodexServiceTier_AddsPriorityWhenAbsent(t *testing.T) {
	cfg := &config.Config{}
	cfg.CodexServiceTier = "priority"
	body := []byte(`{"model":"gpt-5.3-codex","stream":true}`)

	out := injectCodexServiceTier(cfg, body)

	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want %q", got, "priority")
	}
}

func TestInjectCodexServiceTier_PreservesClientValue(t *testing.T) {
	cfg := &config.Config{}
	cfg.CodexServiceTier = "priority"
	body := []byte(`{"model":"gpt-5.3-codex","service_tier":"default"}`)

	out := injectCodexServiceTier(cfg, body)

	if got := gjson.GetBytes(out, "service_tier").String(); got != "default" {
		t.Fatalf("service_tier = %q, want client value %q", got, "default")
	}
}

func TestInjectCodexServiceTier_EmptyConfigIsNoop(t *testing.T) {
	cfg := &config.Config{}
	body := []byte(`{"model":"gpt-5.3-codex"}`)

	out := injectCodexServiceTier(cfg, body)

	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("expected no service_tier, got %s", gjson.GetBytes(out, "service_tier").Raw)
	}
}

func TestInjectCodexServiceTier_NilConfig(t *testing.T) {
	body := []byte(`{"model":"gpt-5.3-codex"}`)

	out := injectCodexServiceTier(nil, body)

	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("expected no service_tier with nil cfg, got %s", gjson.GetBytes(out, "service_tier").Raw)
	}
}
