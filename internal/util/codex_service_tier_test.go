package util

import "testing"

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
		gotModel, gotOverride := StripCodexServiceTierSuffix(tc.in)
		if gotModel != tc.wantModel || gotOverride != tc.wantOverride {
			t.Errorf("strip(%q) = (%q, %q), want (%q, %q)", tc.in, gotModel, gotOverride, tc.wantModel, tc.wantOverride)
		}
	}
}
