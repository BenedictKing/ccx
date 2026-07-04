package thinkingcache

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestClaudeStreamCollectorStoresThinkingByToolUseFingerprint(t *testing.T) {
	ResetForTest()

	collector := NewClaudeStreamCollector()
	events := []string{
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"cached reasoning\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_123\",\"name\":\"Bash\",\"input\":{}}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\":\\\"ls\\\"}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
	}
	for _, event := range events {
		collector.ProcessEvent(event)
	}

	if !collector.Store("session-1") {
		t.Fatal("expected thinking cache store to succeed")
	}

	requestContent := []interface{}{map[string]interface{}{
		"type":  "tool_use",
		"id":    "toolu_123",
		"name":  "Bash",
		"input": map[string]interface{}{"command": "ls"},
	}}
	got, ok := LookupClaudeThinkingForContent("session-1", requestContent)
	if !ok {
		t.Fatal("expected thinking cache lookup to hit")
	}
	if got != "cached reasoning" {
		t.Fatalf("cached thinking = %q, want cached reasoning", got)
	}
}

func TestInjectCachedClaudeThinkingOnlyWhenDeepSeekThinkingRequestHitsCache(t *testing.T) {
	ResetForTest()

	content := []interface{}{map[string]interface{}{
		"type":  "tool_use",
		"id":    "toolu_123",
		"name":  "Bash",
		"input": map[string]interface{}{"command": "ls"},
	}}
	if !StoreClaudeThinkingForContent("session-1", content, "cached reasoning") {
		t.Fatal("expected store to succeed")
	}

	body := []byte(`{
		"model": "deepseek-v4-pro",
		"thinking": {"type": "adaptive"},
		"messages": [
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_123", "name": "Bash", "input": {"command": "ls"}}
			]}
		]
	}`)
	upstream := &config.UpstreamConfig{
		BaseURL:     "https://api.deepseek.com",
		ServiceType: "claude",
	}

	got, injected := InjectCachedClaudeThinking(body, "session-1", upstream)
	if injected != 1 {
		t.Fatalf("injected = %d, want 1; body=%s", injected, string(got))
	}
	if !containsAll(string(got), `"type":"thinking"`, `"thinking":"cached reasoning"`, `"type":"tool_use"`) {
		t.Fatalf("injected body missing thinking block: %s", string(got))
	}
}

func TestInjectCachedClaudeThinkingFallsBackToToolUseSignature(t *testing.T) {
	ResetForTest()

	storedContent := []interface{}{map[string]interface{}{
		"type":  "tool_use",
		"id":    "toolu_123",
		"name":  "Bash",
		"input": map[string]interface{}{"command": "git status --short"},
	}}
	if !StoreClaudeThinkingForContent("session-1", storedContent, "signature cached reasoning") {
		t.Fatal("expected store to succeed")
	}

	body := []byte(`{
		"model": "deepseek-v4-pro",
		"thinking": {"type": "adaptive"},
		"messages": [
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_123", "name": "Bash", "input": {"command": "git status --short", "description": "查看状态"}}
			]}
		]
	}`)
	upstream := &config.UpstreamConfig{
		BaseURL:     "https://api.deepseek.com",
		ServiceType: "claude",
	}

	got, injected := InjectCachedClaudeThinking(body, "session-1", upstream)
	if injected != 1 {
		t.Fatalf("injected = %d, want 1; body=%s", injected, string(got))
	}
	if !containsAll(string(got), `"type":"thinking"`, `"thinking":"signature cached reasoning"`, `"type":"tool_use"`) {
		t.Fatalf("injected body missing signature cached thinking block: %s", string(got))
	}
}

func TestInjectCachedClaudeThinkingFallsBackToLatestToolUseAssistant(t *testing.T) {
	ResetForTest()

	storedContent := []interface{}{map[string]interface{}{
		"type": "text",
		"text": "provider-normalized content differs from client echo",
	}}
	if !StoreClaudeThinkingForContent("session-1", storedContent, "latest cached reasoning") {
		t.Fatal("expected store to succeed")
	}

	body := []byte(`{
		"model": "deepseek-v4-pro",
		"thinking": {"type": "adaptive"},
		"messages": [
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_latest", "name": "Bash", "input": {"command": "git status --porcelain"}}
			]}
		]
	}`)
	upstream := &config.UpstreamConfig{
		BaseURL:     "https://api.deepseek.com",
		ServiceType: "claude",
	}

	got, injected := InjectCachedClaudeThinking(body, "session-1", upstream)
	if injected != 1 {
		t.Fatalf("injected = %d, want 1; body=%s", injected, string(got))
	}
	if !containsAll(string(got), `"type":"thinking"`, `"thinking":"latest cached reasoning"`, `"type":"tool_use"`) {
		t.Fatalf("injected body missing latest cached thinking block: %s", string(got))
	}
}

func TestInjectCachedClaudeThinkingMissLeavesBodyUnchanged(t *testing.T) {
	ResetForTest()

	body := []byte(`{"model":"deepseek-v4-pro","thinking":{"type":"adaptive"},"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_missing","name":"Bash","input":{"command":"pwd"}}]}]}`)
	upstream := &config.UpstreamConfig{
		BaseURL:     "https://api.deepseek.com",
		ServiceType: "claude",
	}

	got, injected := InjectCachedClaudeThinking(body, "session-1", upstream)
	if injected != 0 {
		t.Fatalf("injected = %d, want 0", injected)
	}
	if string(got) != string(body) {
		t.Fatalf("body changed on cache miss:\ngot:  %s\nwant: %s", string(got), string(body))
	}
}

func TestSQLitePersistenceSurvivesReset(t *testing.T) {
	ResetForTest()
	dbPath := filepath.Join(t.TempDir(), "thinking_cache.db")

	if err := Configure(Config{DBPath: dbPath, TTL: time.Hour}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}

	content := []interface{}{map[string]interface{}{
		"type":  "tool_use",
		"id":    "toolu_persist",
		"name":  "Bash",
		"input": map[string]interface{}{"command": "pwd"},
	}}
	if !StoreClaudeThinkingForContent("session-persist", content, "persisted reasoning") {
		t.Fatal("expected store to succeed")
	}

	ResetForTest()
	if err := Configure(Config{DBPath: dbPath, TTL: time.Hour}); err != nil {
		t.Fatalf("Configure() after reset error = %v", err)
	}

	got, ok := LookupClaudeThinkingForContent("session-persist", content)
	if !ok {
		t.Fatal("expected persisted thinking cache lookup to hit")
	}
	if got != "persisted reasoning" {
		t.Fatalf("persisted thinking = %q, want persisted reasoning", got)
	}
}

func containsAll(text string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false
		}
	}
	return true
}
