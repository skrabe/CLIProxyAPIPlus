package thinking_test

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/thinking/provider/claude"
	"github.com/tidwall/gjson"
)

// Claude Fable 5 is adaptive-only: thinking is always on, an explicit
// thinking.type="disabled" or manual budget_tokens returns a 400 upstream.
// ApplyThinking must normalize every client-sent thinking config into something
// Fable 5 accepts (adaptive, optionally with an effort level). These cases mirror
// the configs older clients persist for Opus 4.5/4.6 conversations.
func TestApplyThinking_Fable5NormalizesToAdaptive(t *testing.T) {
	for _, model := range []string{"claude-fable-5", "claude-fable-5-1"} {
		t.Run(model, func(t *testing.T) { testFableNormalizesToAdaptive(t, model) })
	}
}

func testFableNormalizesToAdaptive(t *testing.T, model string) {
	t.Helper()

	cases := []struct {
		name       string
		body       string
		wantEffort string // "" means effort must be absent
	}{
		{
			name: "disabled becomes adaptive",
			body: `{"thinking":{"type":"disabled"}}`,
		},
		{
			name: "manual budget becomes adaptive effort",
			body: `{"thinking":{"type":"enabled","budget_tokens":10000}}`,
			// budget 10000 maps to the "high" level band.
			wantEffort: "high",
		},
		{
			name: "enabled without budget becomes adaptive",
			body: `{"thinking":{"type":"enabled"}}`,
		},
		{
			name:       "adaptive with effort is preserved",
			body:       `{"thinking":{"type":"adaptive"},"output_config":{"effort":"xhigh"}}`,
			wantEffort: "xhigh",
		},
		{
			name: "adaptive without effort is preserved",
			body: `{"thinking":{"type":"adaptive"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := thinking.ApplyThinking([]byte(tc.body), model, "claude", "claude", "claude")
			if err != nil {
				t.Fatalf("ApplyThinking returned error: %v", err)
			}

			if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
				t.Fatalf("thinking.type = %q, want \"adaptive\" (body: %s)", got, string(out))
			}
			if gjson.GetBytes(out, "thinking.budget_tokens").Exists() {
				t.Fatalf("thinking.budget_tokens must not be sent to Fable 5: %s", string(out))
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

// A model-name thinking suffix must also normalize to adaptive on Fable 5.
func TestApplyThinking_Fable5SuffixNone(t *testing.T) {
	out, err := thinking.ApplyThinking([]byte(`{}`), "claude-fable-5(none)", "claude", "claude", "claude")
	if err != nil {
		t.Fatalf("ApplyThinking returned error: %v", err)
	}
	if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, want \"adaptive\": %s", got, string(out))
	}
	if gjson.GetBytes(out, "thinking.budget_tokens").Exists() {
		t.Fatalf("thinking.budget_tokens must not be sent to Fable 5: %s", string(out))
	}
}
