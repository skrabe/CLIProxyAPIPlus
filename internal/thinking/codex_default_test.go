package thinking

import "testing"

// TestDefaultThinkingConfig_Mapping covers the helper directly. Located in the
// thinking package so the unexported helper is reachable.
func TestDefaultThinkingConfig_Mapping(t *testing.T) {
	tests := []struct {
		input string
		want  ThinkingConfig
	}{
		{"", ThinkingConfig{}},
		{"none", ThinkingConfig{Mode: ModeNone, Budget: 0}},
		{"NONE", ThinkingConfig{Mode: ModeNone, Budget: 0}},
		{"auto", ThinkingConfig{Mode: ModeAuto, Budget: -1}},
		{"-1", ThinkingConfig{Mode: ModeAuto, Budget: -1}},
		{"medium", ThinkingConfig{Mode: ModeLevel, Level: LevelMedium}},
		{"  HIGH  ", ThinkingConfig{Mode: ModeLevel, Level: LevelHigh}},
	}
	for _, tt := range tests {
		got := defaultThinkingConfig(tt.input)
		if got != tt.want {
			t.Errorf("defaultThinkingConfig(%q) = %+v, want %+v", tt.input, got, tt.want)
		}
	}
}
