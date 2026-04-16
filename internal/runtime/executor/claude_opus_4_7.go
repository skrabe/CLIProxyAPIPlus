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
// the request body when the target is Claude Opus 4.7 or later. Opus 4.7's
// Messages API returns a 400 error when any of these sampling parameters are
// set to a non-default value; the safe migration path per Anthropic's release
// notes is to omit them entirely.
// See https://docs.anthropic.com/en/docs/about-claude/models/whats-new-claude-4-7#sampling-parameters-removed
func stripSamplingParamsForOpus47(body []byte, baseModel string) []byte {
	if !isOpus47OrLater(baseModel) {
		return body
	}
	for _, path := range []string{"temperature", "top_p", "top_k"} {
		body, _ = sjson.DeleteBytes(body, path)
	}
	return body
}

// isOpus47OrLater reports whether the given base model id matches
// claude-opus-4-<n> for n >= 7. Unknown or older Opus families return false.
func isOpus47OrLater(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	const prefix = "claude-opus-4-"
	if !strings.HasPrefix(lower, prefix) {
		return false
	}
	rest := lower[len(prefix):]
	var digits strings.Builder
	for _, c := range rest {
		if c < '0' || c > '9' {
			break
		}
		digits.WriteRune(c)
	}
	if digits.Len() == 0 {
		return false
	}
	n, err := strconv.Atoi(digits.String())
	if err != nil {
		return false
	}
	return n >= 7
}
