package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/tidwall/gjson"
)

func sanitizerTestSignature() string {
	payload := make([]byte, 73)
	payload[0] = 0x80
	return base64.RawURLEncoding.EncodeToString(payload)
}

func TestReasoningSanitizerPreservesUnchangedRequest(t *testing.T) {
	body := []byte(fmt.Sprintf(`{ "model": "test", "input": [{"type":"reasoning","encrypted_content":%q},{"type":"message","content":"hello"}], "stream": true }`, sanitizerTestSignature()))
	got := sanitizeOpenAIResponsesReasoningEncryptedContent(context.Background(), "test", body)
	if !bytes.Equal(got, body) || &got[0] != &body[0] {
		t.Fatal("valid history should retain its original bytes and allocation")
	}
}

func TestReasoningSanitizerPreservesHistoryAndOtherFields(t *testing.T) {
	valid := sanitizerTestSignature()
	body := []byte(fmt.Sprintf(`{"model":"test","input":[{"type":"message","content":"before"},{"type":"reasoning","id":"bad","encrypted_content":null,"summary":[{"text":"keep me"}]},{"type":"reasoning","id":"valid","encrypted_content":%q},{"type":"function_call","call_id":"call.1","arguments":"{\"x\":1}"},{"type":"reasoning","id":"foreign","encrypted_content":"foreign","vendor":{"encrypted_content":"keep"}},{"type":"reasoning","id":"number","encrypted_content":42},{"type":"message","content":"after"}],"metadata":{"encrypted_content":"untouched"},"stream":true}`, valid))
	got := sanitizeOpenAIResponsesReasoningEncryptedContent(context.Background(), "test", body)
	if !json.Valid(got) {
		t.Fatalf("invalid JSON: %s", got)
	}
	items := gjson.GetBytes(got, "input").Array()
	if len(items) != 7 {
		t.Fatalf("history length = %d, want 7", len(items))
	}
	for _, index := range []int{1, 4, 5} {
		if items[index].Get("encrypted_content").Exists() {
			t.Fatalf("invalid signature at %d survived", index)
		}
	}
	if items[2].Get("encrypted_content").String() != valid || items[1].Get("summary.0.text").String() != "keep me" || items[4].Get("vendor.encrypted_content").String() != "keep" {
		t.Fatal("sanitizing changed valid reasoning or unrelated nested fields")
	}
	original := gjson.GetBytes(body, "input").Array()
	for _, index := range []int{0, 2, 3, 6} {
		if items[index].Raw != original[index].Raw {
			t.Fatalf("unchanged history item %d was rewritten", index)
		}
	}
	if gjson.GetBytes(got, "metadata.encrypted_content").String() != "untouched" || !gjson.GetBytes(got, "stream").Bool() {
		t.Fatal("request settings changed")
	}
}

func TestReasoningSanitizerLongHistory(t *testing.T) {
	items := make([]map[string]any, 2000)
	for index := range items {
		items[index] = map[string]any{"type": "reasoning", "id": fmt.Sprintf("rs_%d", index), "encrypted_content": "foreign", "summary": []string{"preserve"}}
	}
	body, err := json.Marshal(map[string]any{"input": items, "model": "test"})
	if err != nil {
		t.Fatal(err)
	}
	got := sanitizeOpenAIResponsesReasoningEncryptedContent(context.Background(), "test", body)
	result := gjson.GetBytes(got, "input").Array()
	if len(result) != len(items) {
		t.Fatalf("history length = %d, want %d", len(result), len(items))
	}
	for index, item := range result {
		if item.Get("encrypted_content").Exists() || item.Get("id").String() != fmt.Sprintf("rs_%d", index) || item.Get("summary.0").String() != "preserve" {
			t.Fatalf("history item %d lost data or retained its invalid signature", index)
		}
	}
}

func BenchmarkReasoningSanitizerLongHistory(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			item := []byte(`{"type":"reasoning","encrypted_content":null,"summary":[]}`)
			body := append([]byte(`{"input":[`), bytes.Repeat(append(item, ','), count)...)
			body[len(body)-1] = ']'
			body = append(body, '}')
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for b.Loop() {
				sanitizeOpenAIResponsesReasoningEncryptedContent(context.Background(), "test", body)
			}
		})
	}
}
