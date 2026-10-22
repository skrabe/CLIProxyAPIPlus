package executor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestStripAllCacheControlRemovesOnlyMarkers(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a"},{"name":"b","cache_control":{"type":"ephemeral"}}],"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"plain"},{"role":"user","content":[{"type":"text","text":"y"},{"type":"text","text":"z","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)

	got := stripAllCacheControl(body)

	if strings.Contains(string(got), "cache_control") {
		t.Fatalf("cache_control still present: %s", got)
	}
	for _, path := range []string{"tools.1.name", "system.0.text", "messages.0.content", "messages.1.content.1.text"} {
		if gjson.GetBytes(got, path).String() != gjson.GetBytes(body, path).String() {
			t.Fatalf("%s changed: %s", path, got)
		}
	}
}

func BenchmarkStripAllCacheControlLongConversation(b *testing.B) {
	var sb strings.Builder
	sb.WriteString(`{"messages":[`)
	for i := 0; i < 2000; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"role":"user","content":[{"type":"text","text":"%s"},{"type":"text","text":"b"}]}`, strings.Repeat("x", 200))
	}
	sb.WriteString(`]}`)
	body := []byte(sb.String())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stripAllCacheControl(body)
	}
}
