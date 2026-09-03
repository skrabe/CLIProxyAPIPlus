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

// Beta header values gating the Claude Fable 5.1 request features. BYOK clients
// that cannot set custom headers opt in by sending the feature alone; the proxy
// adds the required beta automatically, the same way task budgets work.
const (
	// thinkingDisplayUpdatesBeta gates thinking.display="updates", which returns
	// the model's between-tool-call progress notes while reasoning stays hidden.
	thinkingDisplayUpdatesBeta = "thinking-display-updates-2026-08-18"
	// thinkingBindingControlsBeta gates thinking.block_binding (preserved
	// thinking) and the input_transformations array in responses.
	thinkingBindingControlsBeta = "thinking-binding-controls-2026-08-01"
	// midConversationOutputConfigBeta gates per-message output_config.effort on a
	// role="system" message in the messages array.
	midConversationOutputConfigBeta = "mid-conversation-output-config-2026-07-01"
	// midConversationSystemClearAtBeta gates turn-scoped system messages
	// (clear_at="next_user_message").
	midConversationSystemClearAtBeta = "mid-conversation-system-clear-at-2026-08-21"
	// midConversationToolChangesBeta gates tool_addition/tool_removal blocks on a
	// role="system" message.
	midConversationToolChangesBeta = "mid-conversation-tool-changes-2026-07-01"
)

// appendBetaIfMissing appends value to betas unless it is already present
// (case-insensitive, ignoring surrounding whitespace).
func appendBetaIfMissing(betas []string, value string) []string {
	for _, b := range betas {
		if strings.EqualFold(strings.TrimSpace(b), value) {
			return betas
		}
	}
	return append(betas, value)
}

// ensureClaudeFeatureBetas adds the beta headers required by the request
// features present in the body. Anthropic gates several Fable 5.1 features
// behind beta headers that pass-through clients cannot always set, so the proxy
// derives them from the payload it is about to send.
func ensureClaudeFeatureBetas(betas []string, body []byte) []string {
	betas = ensureTaskBudgetsBeta(betas, body)

	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "thinking.display").String()), "updates") {
		betas = appendBetaIfMissing(betas, thinkingDisplayUpdatesBeta)
	}
	if gjson.GetBytes(body, "thinking.block_binding").Exists() {
		betas = appendBetaIfMissing(betas, thinkingBindingControlsBeta)
	}

	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return betas
	}
	messages.ForEach(func(_, message gjson.Result) bool {
		if !strings.EqualFold(strings.TrimSpace(message.Get("role").String()), "system") {
			return true
		}
		if message.Get("output_config").Exists() {
			betas = appendBetaIfMissing(betas, midConversationOutputConfigBeta)
		}
		if message.Get("clear_at").Exists() {
			betas = appendBetaIfMissing(betas, midConversationSystemClearAtBeta)
		}
		content := message.Get("content")
		if content.IsArray() {
			content.ForEach(func(_, block gjson.Result) bool {
				switch strings.TrimSpace(block.Get("type").String()) {
				case "tool_addition", "tool_removal":
					betas = appendBetaIfMissing(betas, midConversationToolChangesBeta)
				}
				return true
			})
		}
		return true
	})
	return betas
}

// isFableOrMythos51OrLater reports whether the base model id is Claude Fable 5.1
// or Mythos 5.1 (or any later release in those families). These models drop
// forced tool use and enforce preserved thinking, neither of which applies to
// Fable 5 / Mythos 5.
func isFableOrMythos51OrLater(model string) bool {
	for _, prefix := range []string{"claude-fable-", "claude-mythos-"} {
		major, minor, ok := parseClaudeVersion(model, prefix)
		if !ok {
			continue
		}
		return major > 5 || (major == 5 && minor >= 1)
	}
	return false
}

// normalizeForcedToolChoice rewrites a forced tool_choice into the auto form
// Claude Fable 5.1 accepts. Fable 5.1 and Mythos 5.1 reject tool_choice
// {"type":"any"} and {"type":"tool"} with a 400 (`tool_choice: type "tool" and
// "any" are not supported for this model.`) because thinking is always on and a
// forced call would skip it. Anthropic's documented migration is to keep
// tool_choice {"type":"auto"} and state in the prompt when the tool applies, so
// the instruction the forced choice expressed is carried into the conversation
// instead of being silently dropped. The same validation runs on the token
// counting endpoint, so this applies there too.
func normalizeForcedToolChoice(body []byte, baseModel string) []byte {
	if !isFableOrMythos51OrLater(baseModel) {
		return body
	}
	choiceType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "tool_choice.type").String()))
	if choiceType != "any" && choiceType != "tool" {
		return body
	}

	name := strings.TrimSpace(gjson.GetBytes(body, "tool_choice.name").String())
	instruction := "You must answer by calling one of the available tools rather than replying with plain text."
	if choiceType == "tool" && name != "" {
		instruction = "You must answer by calling the `" + name + "` tool rather than replying with plain text."
	}

	// disable_parallel_tool_use stays valid alongside "auto" and is preserved.
	body, _ = sjson.SetBytes(body, "tool_choice.type", "auto")
	body, _ = sjson.DeleteBytes(body, "tool_choice.name")
	return appendToolChoiceInstruction(body, instruction)
}

// appendToolChoiceInstruction adds instruction to the conversation with
// system-prompt authority. When the request ends with a user turn it is
// appended as a mid-conversation system message (supported on Fable 5.1 with no
// beta header), which leaves every earlier byte untouched. Otherwise it falls
// back to a text block on the last user message.
func appendToolChoiceInstruction(body []byte, instruction string) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	items := messages.Array()
	if len(items) == 0 {
		return body
	}

	if strings.EqualFold(strings.TrimSpace(items[len(items)-1].Get("role").String()), "user") {
		updated, err := sjson.SetBytes(body, "messages.-1", map[string]any{
			"role":    "system",
			"content": instruction,
		})
		if err == nil {
			return updated
		}
		return body
	}

	for i := len(items) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(items[i].Get("role").String()), "user") {
			continue
		}
		content := items[i].Get("content")
		path := "messages." + strconv.Itoa(i) + ".content"
		if content.IsArray() {
			updated, err := sjson.SetBytes(body, path+".-1", map[string]any{
				"type": "text",
				"text": instruction,
			})
			if err == nil {
				return updated
			}
			return body
		}
		if content.Type == gjson.String {
			updated, err := sjson.SetBytes(body, path, content.String()+"\n\n"+instruction)
			if err == nil {
				return updated
			}
		}
		return body
	}
	return body
}

// applyThinkingBlockBindingDefault opts a Fable 5.1 request that replays
// thinking blocks into "drop_block" handling for a preserved-thinking prefix
// mismatch, unless the client chose its own behavior. Fable 5.1 binds every
// thinking block to the exact system prompt, tool set, and message history that
// preceded it, and rejects a replayed block whose prefix changed with a 400 that
// no retry can clear. A proxy cannot guarantee its clients keep an append-only
// history — third-party clients routinely inject and then remove per-request
// reminders — so failing the whole request is the wrong trade. Dropping the
// affected blocks costs the model that reasoning, is unbilled, and keeps the
// session alive; the drop is reported back in input_transformations.
func applyThinkingBlockBindingDefault(body []byte, baseModel string) []byte {
	if !isFableOrMythos51OrLater(baseModel) {
		return body
	}
	if !gjson.GetBytes(body, "thinking.type").Exists() {
		return body
	}
	if gjson.GetBytes(body, "thinking.block_binding").Exists() {
		return body
	}
	if !replaysThinkingBlocks(body) {
		return body
	}
	body, _ = sjson.SetBytes(body, "thinking.block_binding.prefix_mismatch_behavior", "drop_block")
	return body
}

// replaysThinkingBlocks reports whether the request carries thinking blocks from
// an earlier assistant turn.
func replaysThinkingBlocks(body []byte) bool {
	found := false
	gjson.GetBytes(body, "messages").ForEach(func(_, message gjson.Result) bool {
		content := message.Get("content")
		if !content.IsArray() {
			return true
		}
		content.ForEach(func(_, block gjson.Result) bool {
			switch strings.TrimSpace(block.Get("type").String()) {
			case "thinking", "redacted_thinking":
				found = true
				return false
			}
			return true
		})
		return !found
	})
	return found
}
