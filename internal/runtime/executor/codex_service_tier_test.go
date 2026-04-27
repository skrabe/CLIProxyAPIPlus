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

	out := injectCodexServiceTier(cfg, "", body)

	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want %q", got, "priority")
	}
}

func TestInjectCodexServiceTier_PreservesClientValue(t *testing.T) {
	cfg := &config.Config{}
	cfg.CodexServiceTier = "priority"
	body := []byte(`{"model":"gpt-5.3-codex","service_tier":"default"}`)

	out := injectCodexServiceTier(cfg, "", body)

	if got := gjson.GetBytes(out, "service_tier").String(); got != "default" {
		t.Fatalf("service_tier = %q, want client value %q", got, "default")
	}
}

func TestInjectCodexServiceTier_EmptyConfigIsNoop(t *testing.T) {
	cfg := &config.Config{}
	body := []byte(`{"model":"gpt-5.3-codex"}`)

	out := injectCodexServiceTier(cfg, "", body)

	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("expected no service_tier, got %s", gjson.GetBytes(out, "service_tier").Raw)
	}
}

func TestInjectCodexServiceTier_NilConfig(t *testing.T) {
	body := []byte(`{"model":"gpt-5.3-codex"}`)

	out := injectCodexServiceTier(nil, "", body)

	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("expected no service_tier with nil cfg, got %s", gjson.GetBytes(out, "service_tier").Raw)
	}
}

func TestInjectCodexServiceTier_OverrideBeatsConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.CodexServiceTier = ""
	body := []byte(`{"model":"gpt-5.5"}`)

	out := injectCodexServiceTier(cfg, "priority", body)

	if got := gjson.GetBytes(out, "service_tier").String(); got != "priority" {
		t.Fatalf("service_tier = %q, want priority", got)
	}
}

func TestInjectCodexServiceTier_DefaultOverrideClears(t *testing.T) {
	cfg := &config.Config{}
	cfg.CodexServiceTier = "priority"
	body := []byte(`{"model":"gpt-5.5"}`)

	out := injectCodexServiceTier(cfg, "default", body)

	if gjson.GetBytes(out, "service_tier").Exists() {
		t.Fatalf("expected service_tier cleared, got %s", gjson.GetBytes(out, "service_tier").Raw)
	}
}

func TestStripCodexServiceTierSuffix(t *testing.T) {
	cases := []struct {
		in           string
		wantModel    string
		wantOverride string
	}{
		{"gpt-5.5", "gpt-5.5", ""},
		{"gpt-5.5(fast)", "gpt-5.5", "priority"},
		{"gpt-5.5(medium)(fast)", "gpt-5.5(medium)", "priority"},
		{"gpt-5.5(fast)(high)", "gpt-5.5(high)", "priority"},
		{"gpt-5.5(default)", "gpt-5.5", "default"},
		{"gpt-5.3-codex(xhigh)", "gpt-5.3-codex(xhigh)", ""},
	}
	for _, tc := range cases {
		gotModel, gotOverride := stripCodexServiceTierSuffix(tc.in)
		if gotModel != tc.wantModel || gotOverride != tc.wantOverride {
			t.Errorf("strip(%q) = (%q, %q), want (%q, %q)", tc.in, gotModel, gotOverride, tc.wantModel, tc.wantOverride)
		}
	}
}
