package thinking_test

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/thinking/provider/claude"
	"github.com/tidwall/gjson"
)

// Claude Opus 5 thinks by default and accepts thinking.type="disabled", but it
// rejects manual extended thinking (thinking.type="enabled" + budget_tokens) with
// a 400. Its registry entry is therefore level-only, so every budget config must
// normalize to an adaptive effort level instead of a budget block.
func TestApplyThinking_Opus5NormalizesBudgetToAdaptiveEffort(t *testing.T) {
	out, err := thinking.ApplyThinking([]byte(`{"thinking":{"type":"enabled","budget_tokens":10000}}`), "claude-opus-5", "claude", "claude", "claude")
	if err != nil {
		t.Fatalf("ApplyThinking returned error: %v", err)
	}

	if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, want \"adaptive\": %s", got, string(out))
	}
	if gjson.GetBytes(out, "thinking.budget_tokens").Exists() {
		t.Fatalf("thinking.budget_tokens must not be sent to Opus 5: %s", string(out))
	}
	// budget 10000 maps to the "high" level band.
	if got := gjson.GetBytes(out, "output_config.effort").String(); got != "high" {
		t.Fatalf("output_config.effort = %q, want %q: %s", got, "high", string(out))
	}
}

// Opus 5 accepts thinking.type="disabled" only when effort is "high" or below;
// pairing it with "xhigh"/"max" returns a 400. The applier's ModeNone branch must
// therefore always drop output_config.effort alongside the disable.
func TestApplyThinking_Opus5DisabledDropsEffort(t *testing.T) {
	cases := []struct {
		name  string
		model string
		body  string
	}{
		{
			name:  "suffix none",
			model: "claude-opus-5(none)",
			body:  `{}`,
		},
		{
			name:  "body disabled with max effort",
			model: "claude-opus-5",
			body:  `{"thinking":{"type":"disabled"},"output_config":{"effort":"max"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := thinking.ApplyThinking([]byte(tc.body), tc.model, "claude", "claude", "claude")
			if err != nil {
				t.Fatalf("ApplyThinking returned error: %v", err)
			}

			if got := gjson.GetBytes(out, "thinking.type").String(); got != "disabled" {
				t.Fatalf("thinking.type = %q, want \"disabled\": %s", got, string(out))
			}
			if gjson.GetBytes(out, "thinking.budget_tokens").Exists() {
				t.Fatalf("thinking.budget_tokens must not be sent to Opus 5: %s", string(out))
			}
			if gotEffort := gjson.GetBytes(out, "output_config.effort"); gotEffort.Exists() {
				t.Fatalf("output_config.effort = %q, want absent (disabled + effort is rejected upstream): %s", gotEffort.String(), string(out))
			}
		})
	}
}

// Adaptive configs keep their effort level, and "auto" stays dynamic: Opus 5 has
// dynamic_allowed, so auto must not be downgraded to a mid-range level.
func TestApplyThinking_Opus5PreservesAdaptiveEffort(t *testing.T) {
	cases := []struct {
		name       string
		model      string
		body       string
		wantEffort string // "" means effort must be absent
	}{
		{
			name:       "suffix xhigh",
			model:      "claude-opus-5(xhigh)",
			body:       `{}`,
			wantEffort: "xhigh",
		},
		{
			name:       "suffix max",
			model:      "claude-opus-5(max)",
			body:       `{}`,
			wantEffort: "max",
		},
		{
			name:  "suffix auto stays dynamic",
			model: "claude-opus-5(auto)",
			body:  `{}`,
		},
		{
			name:       "adaptive with effort is preserved",
			model:      "claude-opus-5",
			body:       `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`,
			wantEffort: "high",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := thinking.ApplyThinking([]byte(tc.body), tc.model, "claude", "claude", "claude")
			if err != nil {
				t.Fatalf("ApplyThinking returned error: %v", err)
			}

			if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
				t.Fatalf("thinking.type = %q, want \"adaptive\": %s", got, string(out))
			}
			if gjson.GetBytes(out, "thinking.budget_tokens").Exists() {
				t.Fatalf("thinking.budget_tokens must not be sent to Opus 5: %s", string(out))
			}

			gotEffort := gjson.GetBytes(out, "output_config.effort")
			if tc.wantEffort == "" {
				if gotEffort.Exists() {
					t.Fatalf("output_config.effort = %q, want absent: %s", gotEffort.String(), string(out))
				}
			} else if gotEffort.String() != tc.wantEffort {
				t.Fatalf("output_config.effort = %q, want %q: %s", gotEffort.String(), tc.wantEffort, string(out))
			}
		})
	}
}
