package util

import "strings"

// StripCodexServiceTierSuffix detects "(fast)" or "(default)" tokens in a Codex
// model name, strips them, and returns the cleaned name plus the override:
// "priority" for (fast), "default" to explicitly clear service_tier, or ""
// when no suffix is present. Used by routing (provider lookup) and by the
// Codex executor (request-body injection).
func StripCodexServiceTierSuffix(model string) (string, string) {
	const (
		fastTok    = "(fast)"
		defaultTok = "(default)"
	)
	if strings.Contains(model, fastTok) {
		return strings.ReplaceAll(model, fastTok, ""), "priority"
	}
	if strings.Contains(model, defaultTok) {
		return strings.ReplaceAll(model, defaultTok, ""), "default"
	}
	return model, ""
}
