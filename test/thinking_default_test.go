package test

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"

	// Import provider packages to trigger init() registration of ProviderAppliers
	_ "github.com/router-for-me/CLIProxyAPI/v6/internal/thinking/provider/codex"

	"github.com/tidwall/gjson"
)

// TestApplyThinking_GPT55BareDefaultsToNone verifies that a bare gpt-5.5
// request with no thinking suffix and no body thinking config results in
// reasoning.effort=none (the registered Default).
func TestApplyThinking_GPT55BareDefaultsToNone(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[]}`)

	out, err := thinking.ApplyThinking(body, "gpt-5.5", "codex", "codex", "codex")
	if err != nil {
		t.Fatalf("ApplyThinking() error = %v", err)
	}

	got := gjson.GetBytes(out, "reasoning.effort").String()
	if got != "none" {
		t.Fatalf("reasoning.effort = %q, want %q", got, "none")
	}
}

// TestApplyThinking_GPT55SuffixOverridesDefault verifies that a thinking suffix
// (e.g., gpt-5.5(medium)) takes priority over the registered Default.
func TestApplyThinking_GPT55SuffixOverridesDefault(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[]}`)

	out, err := thinking.ApplyThinking(body, "gpt-5.5(medium)", "codex", "codex", "codex")
	if err != nil {
		t.Fatalf("ApplyThinking() error = %v", err)
	}

	got := gjson.GetBytes(out, "reasoning.effort").String()
	if got != "medium" {
		t.Fatalf("reasoning.effort = %q, want %q", got, "medium")
	}
}

// TestApplyThinking_GPT55BodyConfigOverridesDefault verifies that an explicit
// reasoning.effort in the request body takes priority over the registered Default.
func TestApplyThinking_GPT55BodyConfigOverridesDefault(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","reasoning":{"effort":"high"},"input":[]}`)

	out, err := thinking.ApplyThinking(body, "gpt-5.5", "codex", "codex", "codex")
	if err != nil {
		t.Fatalf("ApplyThinking() error = %v", err)
	}

	got := gjson.GetBytes(out, "reasoning.effort").String()
	if got != "high" {
		t.Fatalf("reasoning.effort = %q, want %q", got, "high")
	}
}

// TestApplyThinking_NoDefaultStillPassesThrough verifies that models without
// a Default (e.g., gpt-5.4) preserve the existing passthrough behavior when
// no thinking config is provided.
func TestApplyThinking_NoDefaultStillPassesThrough(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":[]}`)

	out, err := thinking.ApplyThinking(body, "gpt-5.4", "codex", "codex", "codex")
	if err != nil {
		t.Fatalf("ApplyThinking() error = %v", err)
	}

	if gjson.GetBytes(out, "reasoning.effort").Exists() {
		t.Fatalf("reasoning.effort should not be set for gpt-5.4 with no config, got %q",
			gjson.GetBytes(out, "reasoning.effort").String())
	}
}

// TestThinkingSupportDefault_RegistryRoundTrip verifies the Default field
// survives the registry's clone path used by LookupModelInfo.
func TestThinkingSupportDefault_RegistryRoundTrip(t *testing.T) {
	cloned := registry.LookupModelInfo("gpt-5.5")
	if cloned == nil {
		t.Fatal("expected gpt-5.5 to be discoverable via LookupModelInfo")
	}
	if cloned.Thinking == nil {
		t.Fatal("gpt-5.5 missing thinking support")
	}
	if cloned.Thinking.Default != "none" {
		t.Fatalf("gpt-5.5 thinking default = %q, want \"none\"", cloned.Thinking.Default)
	}
}
