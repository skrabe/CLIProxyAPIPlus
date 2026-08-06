package executor

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// taskBudgetsBeta is the anthropic-beta header value that gates the
// output_config.task_budget feature introduced with Claude Opus 4.7.
// See https://docs.anthropic.com/en/docs/about-claude/models/whats-new-claude-4-7#task-budgets-beta
const taskBudgetsBeta = "task-budgets-2026-03-13"

// ensureTaskBudgetsBeta appends the task-budgets beta header when the request
// body carries an output_config.task_budget object. BYOK clients that cannot
// set custom headers can opt into task budgets by setting task_budget alone;
// the proxy adds the required beta automatically.
func ensureTaskBudgetsBeta(betas []string, body []byte) []string {
	if !gjson.GetBytes(body, "output_config.task_budget").Exists() {
		return betas
	}
	for _, b := range betas {
		if strings.EqualFold(strings.TrimSpace(b), taskBudgetsBeta) {
			return betas
		}
	}
	return append(betas, taskBudgetsBeta)
}

// stripSamplingParamsForOpus47 removes temperature, top_p, and top_k from
// the request body when the target rejects sampling parameters. Claude Opus 4.7
// and later, Sonnet 5 and later, and the Fable/Mythos 5 family return a 400 error
// when any of these sampling parameters are set to a non-default value; the safe
// migration path per Anthropic's release notes is to omit them entirely.
// See https://docs.anthropic.com/en/docs/about-claude/models/whats-new-claude-4-7#sampling-parameters-removed
func stripSamplingParamsForOpus47(body []byte, baseModel string) []byte {
	if !claudeRejectsSamplingParams(baseModel) {
		return body
	}
	for _, path := range []string{"temperature", "top_p", "top_k"} {
		body, _ = sjson.DeleteBytes(body, path)
	}
	return body
}

// claudeRejectsSamplingParams reports whether the target Claude model rejects
// temperature/top_p/top_k. True for Opus 4.7+, Sonnet 5+, and the Fable/Mythos
// 5 family.
func claudeRejectsSamplingParams(model string) bool {
	return isOpus47OrLater(model) || isSonnet5OrLater(model) || isFableOrMythosFamily(model)
}

// isFableOrMythosFamily reports whether the base model id is a Fable or Mythos
// model. These share Opus 4.7+'s adaptive-only thinking surface (sampling
// parameters and manual budget rejected) and additionally cannot disable
// thinking.
func isFableOrMythosFamily(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(lower, "claude-fable-") || strings.HasPrefix(lower, "claude-mythos-")
}

// isOpus47OrLater reports whether the given base model id is Claude Opus 4.7 or
// any later Opus release. This covers both the two-part ids used through the 4.x
// line (claude-opus-4-7, claude-opus-4-8) and the single-part ids introduced with
// Opus 5 (claude-opus-5). Unknown or older Opus families return false.
func isOpus47OrLater(model string) bool {
	major, minor, ok := parseClaudeVersion(model, "claude-opus-")
	if !ok {
		return false
	}
	if major > 4 {
		return true
	}
	return major == 4 && minor >= 7
}

// isSonnet5OrLater reports whether the given base model id is Claude Sonnet 5 or
// any later Sonnet release. Sonnet 5 deprecated the sampling parameters
// (`temperature` is deprecated for this model), while Sonnet 4.6 and earlier
// still accept them.
func isSonnet5OrLater(model string) bool {
	major, _, ok := parseClaudeVersion(model, "claude-sonnet-")
	return ok && major >= 5
}

// parseClaudeVersion extracts the major and minor version from a Claude base
// model id carrying the given family prefix. It accepts the two-part form
// (claude-opus-4-7 -> 4.7) and the single-part form (claude-opus-5 -> 5.0). A
// trailing segment of more than two digits is a dated snapshot rather than a
// minor version, so claude-opus-4-20250514 parses as 4.0 (the original Opus 4)
// and not as 4.20250514.
func parseClaudeVersion(model, prefix string) (major, minor int, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(model))
	if !strings.HasPrefix(lower, prefix) {
		return 0, 0, false
	}
	rest := lower[len(prefix):]
	major, width := leadingInt(rest)
	if width == 0 {
		return 0, 0, false
	}
	rest = rest[width:]
	if !strings.HasPrefix(rest, "-") {
		return major, 0, true
	}
	minor, width = leadingInt(rest[1:])
	if width == 0 || width > 2 {
		// No minor segment, or a dated snapshot such as -20250514.
		return major, 0, true
	}
	return major, minor, true
}

// leadingInt parses the leading decimal digits of s, returning the parsed value
// and the number of digits consumed. A zero width means s does not start with a
// digit.
func leadingInt(s string) (value, width int) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, 0
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, 0
	}
	return n, i
}
