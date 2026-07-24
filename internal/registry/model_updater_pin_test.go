package registry

import "testing"

// TestMergeEmbeddedAdditions_PinsGPT55 verifies that gpt-5.5 from the embedded
// catalog overrides the remote definition. Without the pin, periodic refresh
// from router-for-me/models would silently revert local additions like the
// thinking.default field.
func TestMergeEmbeddedAdditions_PinsGPT55(t *testing.T) {
	// Simulate a remote catalog that has gpt-5.5 without the Default field
	// (this is the actual upstream state).
	remoteGPT55 := &ModelInfo{
		ID:          "gpt-5.5",
		Object:      "model",
		OwnedBy:     "openai",
		DisplayName: "GPT 5.5",
		Thinking: &ThinkingSupport{
			Levels:  []string{"low", "medium", "high", "xhigh"},
			Default: "", // <- remote does NOT have this
		},
	}
	remote := &staticModelsJSON{
		CodexFree: []*ModelInfo{remoteGPT55},
		CodexTeam: []*ModelInfo{remoteGPT55},
		CodexPlus: []*ModelInfo{remoteGPT55},
		CodexPro:  []*ModelInfo{remoteGPT55},
	}

	merged := mergeEmbeddedAdditions(remote)

	for tier, list := range map[string][]*ModelInfo{
		"free": merged.CodexFree,
		"team": merged.CodexTeam,
		"plus": merged.CodexPlus,
		"pro":  merged.CodexPro,
	} {
		var found *ModelInfo
		for _, m := range list {
			if m != nil && m.ID == "gpt-5.5" {
				found = m
				break
			}
		}
		if found == nil {
			t.Fatalf("%s tier: gpt-5.5 missing after merge", tier)
		}
		if found.Thinking == nil {
			t.Fatalf("%s tier: gpt-5.5 missing thinking support after merge", tier)
		}
		if found.Thinking.Default != "none" {
			t.Errorf("%s tier: gpt-5.5 Default = %q, want %q (embedded pin should override remote)",
				tier, found.Thinking.Default, "none")
		}
	}
}

// TestMergeEmbeddedAdditions_PinsClaudeOpus5 verifies that claude-opus-5 from the
// embedded catalog overrides the remote definition. The shared catalog ships Opus
// entries with a min/max budget range; without the pin a periodic refresh would
// turn our level-only definition hybrid again and let thinking.budget_tokens reach
// Anthropic, which rejects manual extended thinking on Opus 5 with a 400.
func TestMergeEmbeddedAdditions_PinsClaudeOpus5(t *testing.T) {
	// Simulate a remote catalog that ships claude-opus-5 with a budget range.
	remote := &staticModelsJSON{
		Claude: []*ModelInfo{{
			ID:          "claude-opus-5",
			Object:      "model",
			OwnedBy:     "anthropic",
			DisplayName: "Claude Opus 5",
			Thinking: &ThinkingSupport{
				Min:            1024, // <- remote budget range we must not inherit
				Max:            128000,
				DynamicAllowed: true,
				Levels:         []string{"low", "medium", "high", "xhigh", "max"},
			},
		}},
	}

	merged := mergeEmbeddedAdditions(remote)

	var found *ModelInfo
	for _, m := range merged.Claude {
		if m != nil && m.ID == "claude-opus-5" {
			found = m
			break
		}
	}
	if found == nil {
		t.Fatal("claude-opus-5 missing after merge")
	}
	if found.Thinking == nil {
		t.Fatal("claude-opus-5 missing thinking support after merge")
	}
	if found.Thinking.Min != 0 || found.Thinking.Max != 0 {
		t.Errorf("claude-opus-5 budget range = [%d,%d], want [0,0] (embedded pin should override remote)",
			found.Thinking.Min, found.Thinking.Max)
	}
	if !found.Thinking.ZeroAllowed {
		t.Errorf("claude-opus-5 ZeroAllowed = false, want true (embedded pin should override remote)")
	}
}
