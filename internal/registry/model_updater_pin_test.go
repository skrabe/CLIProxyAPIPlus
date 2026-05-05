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
