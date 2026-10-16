package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/signature"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func sanitizeOpenAIResponsesReasoningEncryptedContent(ctx context.Context, provider string, body []byte) []byte {
	inputResult := gjson.GetBytes(body, "input")
	if !inputResult.Exists() || !inputResult.IsArray() {
		return body
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "openai responses upstream"
	}

	items := inputResult.Array()

	var rebuilt []byte
	itemsWritten := 0
	keep := func(raw string) {
		if rebuilt == nil {
			return
		}
		if itemsWritten > 0 {
			rebuilt = append(rebuilt, ',')
		}
		rebuilt = append(rebuilt, raw...)
		itemsWritten++
	}

	for index, item := range items {
		if strings.TrimSpace(item.Get("type").String()) != "reasoning" {
			keep(item.Raw)
			continue
		}

		encryptedContent := item.Get("encrypted_content")
		if !encryptedContent.Exists() {
			keep(item.Raw)
			continue
		}

		reason := ""
		switch encryptedContent.Type {
		case gjson.String:
			rawSignature := encryptedContent.String()
			if rawSignature != strings.TrimSpace(rawSignature) {
				reason = "encrypted_content has leading or trailing whitespace"
			} else if _, err := signature.InspectGPTReasoningSignature(rawSignature); err != nil {
				reason = err.Error()
			}
		case gjson.Null:
			reason = "encrypted_content is null"
		default:
			reason = fmt.Sprintf("encrypted_content must be a string, got %s", encryptedContent.Type.String())
		}
		if reason == "" {
			keep(item.Raw)
			continue
		}

		nextItem, err := sjson.Delete(item.Raw, "encrypted_content")
		if err != nil {
			helps.LogWithRequestID(ctx).Debugf("%s: failed to drop invalid reasoning encrypted_content at input[%d]: %v", provider, index, err)
			keep(item.Raw)
			continue
		}

		if rebuilt == nil {
			rebuilt = make([]byte, 0, len(inputResult.Raw))
			rebuilt = append(rebuilt, '[')
			for i := range index {
				keep(items[i].Raw)
			}
		}
		keep(nextItem)

		itemID := strings.TrimSpace(item.Get("id").String())
		if itemID == "" {
			itemID = fmt.Sprintf("input[%d]", index)
		}
		helps.LogWithRequestID(ctx).Debugf("%s: dropped invalid reasoning encrypted_content at input[%d] item_id=%q reason=%s", provider, index, itemID, reason)
	}

	if rebuilt == nil {
		return body
	}
	rebuilt = append(rebuilt, ']')

	updated, err := sjson.SetRawBytes(body, "input", rebuilt)
	if err != nil {
		helps.LogWithRequestID(ctx).Debugf("%s: failed to rebuild input array while sanitizing reasoning encrypted_content: %v", provider, err)
		return body
	}
	return updated
}
