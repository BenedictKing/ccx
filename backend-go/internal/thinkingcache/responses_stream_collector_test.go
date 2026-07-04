package thinkingcache

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreAndLookupResponsesReasoning(t *testing.T) {
	ResetForTest()

	if !StoreResponsesReasoning("sess-1", "rs_abc", "ENCRYPTED_BLOB_1") {
		t.Fatal("expected StoreResponsesReasoning to succeed")
	}

	got, ok := LookupResponsesReasoning("sess-1", "rs_abc")
	if !ok {
		t.Fatal("expected LookupResponsesReasoning to hit")
	}
	if got != "ENCRYPTED_BLOB_1" {
		t.Fatalf("cached encrypted_content = %q, want ENCRYPTED_BLOB_1", got)
	}
}

func TestStoreResponsesReasoningRejectsEmptyInputs(t *testing.T) {
	ResetForTest()

	cases := []struct {
		sessionID, itemID, enc string
	}{
		{"", "rs_1", "blob"},
		{"sess-1", "", "blob"},
		{"sess-1", "rs_1", ""},
		{"sess-1", "rs_1", "   "},
	}
	for _, c := range cases {
		if StoreResponsesReasoning(c.sessionID, c.itemID, c.enc) {
			t.Fatalf("expected StoreResponsesReasoning to reject empty input: %+v", c)
		}
	}
}

func TestResponsesReasoningCacheIsolatedFromClaudeThinking(t *testing.T) {
	ResetForTest()

	// 同一 sessionID 下，Claude thinking 缓存与 Responses reasoning 缓存应互不干扰
	StoreClaudeThinkingForContent("sess-1", []interface{}{map[string]interface{}{
		"type": "text",
		"text": "hello",
	}}, "claude-thinking-text")
	StoreResponsesReasoning("sess-1", "rs_abc", "ENCRYPTED_BLOB")

	// Claude thinking 查找应命中 thinking 文本
	gotThinking, ok := LookupClaudeThinkingForContent("sess-1", []interface{}{map[string]interface{}{
		"type": "text",
		"text": "hello",
	}})
	if !ok || gotThinking != "claude-thinking-text" {
		t.Fatalf("Claude thinking lookup = %q, %v, want claude-thinking-text", gotThinking, ok)
	}

	// Responses reasoning 查找应命中 encrypted_content
	gotEnc, ok := LookupResponsesReasoning("sess-1", "rs_abc")
	if !ok || gotEnc != "ENCRYPTED_BLOB" {
		t.Fatalf("Responses reasoning lookup = %q, %v, want ENCRYPTED_BLOB", gotEnc, ok)
	}
}

func TestResponsesStreamCollectorCollectsReasoningEncryptedContent(t *testing.T) {
	ResetForTest()

	collector := NewResponsesStreamCollector()
	events := []string{
		// reasoning item with encrypted_content
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_001\",\"encrypted_content\":\"BLOB_001\"}}\n\n",
		// reasoning item without encrypted_content (应被跳过)
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_002\",\"encrypted_content\":\"\"}}\n\n",
		// message item (应被跳过)
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":2,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n",
		// reasoning item without id (应被跳过)
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":3,\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"BLOB_NO_ID\"}}\n\n",
	}
	for _, event := range events {
		collector.ProcessEvent(event)
	}

	if collector.ReasoningCount() != 1 {
		t.Fatalf("ReasoningCount = %d, want 1 (仅带 id 和 encrypted_content 的 reasoning 应被收集)", collector.ReasoningCount())
	}

	stored := collector.Store("sess-stream-1")
	if stored != 1 {
		t.Fatalf("Store 返回 %d, want 1", stored)
	}

	got, ok := LookupResponsesReasoning("sess-stream-1", "rs_001")
	if !ok {
		t.Fatal("expected LookupResponsesReasoning to hit after Store")
	}
	if got != "BLOB_001" {
		t.Fatalf("cached encrypted_content = %q, want BLOB_001", got)
	}
}

func TestResponsesStreamCollectorHandlesMalformedEvents(t *testing.T) {
	ResetForTest()

	collector := NewResponsesStreamCollector()
	events := []string{
		"event: response.output_item.done\ndata: {not valid json}\n\n",
		"data: {\"type\":\"response.output_item.done\",\"item\":\"not-a-map\"}\n\n",
		"",
		strings.Repeat("x", 10),
	}
	for _, event := range events {
		collector.ProcessEvent(event)
	}
	if collector.ReasoningCount() != 0 {
		t.Fatalf("ReasoningCount = %d, want 0 for malformed events", collector.ReasoningCount())
	}
}

func TestResponsesReasoningSQLitePersistenceSurvivesReset(t *testing.T) {
	ResetForTest()
	dbPath := filepath.Join(t.TempDir(), "thinking_cache.db")

	if err := Configure(Config{DBPath: dbPath, TTL: time.Hour}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}

	if !StoreResponsesReasoning("session-persist", "rs_persist", "PERSISTED_BLOB") {
		t.Fatal("expected StoreResponsesReasoning to succeed")
	}

	// 重置内存缓存后重新加载 SQLite，验证 encrypted_content 持久化
	ResetForTest()
	if err := Configure(Config{DBPath: dbPath, TTL: time.Hour}); err != nil {
		t.Fatalf("Configure() after reset error = %v", err)
	}

	got, ok := LookupResponsesReasoning("session-persist", "rs_persist")
	if !ok {
		t.Fatal("expected persisted Responses reasoning lookup to hit")
	}
	if got != "PERSISTED_BLOB" {
		t.Fatalf("persisted encrypted_content = %q, want PERSISTED_BLOB", got)
	}
}
