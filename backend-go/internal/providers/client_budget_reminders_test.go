package providers

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestIsCCBudgetReminderText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{"plain number", "<total_tokens>14963377 tokens left</total_tokens>", true},
		{"comma formatted", "<total_tokens>14,963,377 tokens left</total_tokens>", true},
		{"fresh budget with style suffix", "<total_tokens>15000000 tokens left</total_tokens>\n\nengineer-professional output style is active. Remember to follow the specific guidelines for this style.", true},
		{"mid-text mention not stripped", "You have <total_tokens>123 tokens left</total_tokens> per the docs.", false},
		{"style reminder alone", "engineer-professional output style is active. Remember to follow the specific guidelines for this style.", false},
		{"normal system", "You are a helpful assistant.", false},
	}
	for _, tt := range tests {
		if got := isCCBudgetReminderText(tt.text); got != tt.expected {
			t.Errorf("%s: isCCBudgetReminderText = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestIsCodexBudgetReminderText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{"bare text numeric", "You have 710 tokens left in this context window.", true},
		{"bare text unknown", "You have unknown tokens left in this context window.", true},
		{"legacy wrapper", "<token_budget>\nYou have 710 tokens left in this context window.\n</token_budget>", true},
		{"window identity kept", "<context_window>\nAgent name: main\nFirst context window id: 018f2a4e-1f2b-7c3d-9e4a-5b6c7d8e9f0a\n</context_window>", false},
		{"window guidance kept", "<context_window_guidance>\nSwitch windows proactively when budget is low.\n</context_window_guidance>", false},
		{"mid-text mention", "Context: you have 710 tokens left in this context window. per earlier note", false},
		{"other reminder text", "Context low. Consider wrapping up.", false},
	}
	for _, tt := range tests {
		if got := isCodexBudgetReminderText(tt.text); got != tt.expected {
			t.Errorf("%s: isCodexBudgetReminderText = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestStripCCBudgetRemindersFromClaudeSystem(t *testing.T) {
	reminder := map[string]interface{}{"type": "text", "text": "<total_tokens>1000 tokens left</total_tokens>"}
	cacheBlock := map[string]interface{}{
		"type":          "text",
		"text":          "persistent instructions",
		"cache_control": map[string]interface{}{"type": "ephemeral"},
	}

	t.Run("array form strips reminder keeps others", func(t *testing.T) {
		system := []interface{}{reminder, cacheBlock}
		got, changed := StripCCBudgetRemindersFromClaudeSystem(system)
		if !changed {
			t.Fatal("expected changed")
		}
		arr, ok := got.([]interface{})
		if !ok || len(arr) != 1 {
			t.Fatalf("expected 1 remaining block, got %v", got)
		}
		if _, has := arr[0].(map[string]interface{})["cache_control"]; !has {
			t.Error("cache_control block must survive")
		}
	})

	t.Run("array form all stripped returns nil", func(t *testing.T) {
		got, changed := StripCCBudgetRemindersFromClaudeSystem([]interface{}{reminder})
		if !changed || got != nil {
			t.Fatalf("expected (nil, true), got (%v, %v)", got, changed)
		}
	})

	t.Run("string form stripped", func(t *testing.T) {
		got, changed := StripCCBudgetRemindersFromClaudeSystem("<total_tokens>5 tokens left</total_tokens>\n\nstyle note")
		if !changed || got != nil {
			t.Fatalf("expected (nil, true), got (%v, %v)", got, changed)
		}
	})

	t.Run("no match returns original unchanged", func(t *testing.T) {
		system := []interface{}{cacheBlock}
		got, changed := StripCCBudgetRemindersFromClaudeSystem(system)
		if changed {
			t.Error("expected unchanged")
		}
		if !reflect.DeepEqual(got.([]interface{})[0], cacheBlock) {
			t.Error("original slice value must be returned as-is")
		}
	})

	t.Run("non text block kept", func(t *testing.T) {
		toolUse := map[string]interface{}{"type": "tool_use", "id": "t1"}
		got, changed := StripCCBudgetRemindersFromClaudeSystem([]interface{}{reminder, toolUse})
		if !changed {
			t.Fatal("expected changed")
		}
		if len(got.([]interface{})) != 1 {
			t.Error("tool_use block must survive")
		}
	})
}

func TestStripCodexBudgetRemindersFromResponsesInput(t *testing.T) {
	reminderItem := map[string]interface{}{
		"type": "message",
		"role": "developer",
		"content": []interface{}{
			map[string]interface{}{"type": "input_text", "text": "You have 710 tokens left in this context window."},
		},
	}
	windowItem := map[string]interface{}{
		"type": "message",
		"role": "developer",
		"content": []interface{}{
			map[string]interface{}{"type": "input_text", "text": "<context_window>\nAgent name: main\nCurrent context window id: 018f2a4e-1f2b-7c3d-9e4a-5b6c7d8e9f0a\n</context_window>"},
		},
	}
	userItem := map[string]interface{}{
		"type":    "message",
		"role":    "user",
		"content": "You have 710 tokens left in this context window.",
	}

	t.Run("strips developer reminder keeps window identity", func(t *testing.T) {
		input := []interface{}{reminderItem, windowItem, userItem}
		got, changed := StripCodexBudgetRemindersFromResponsesInput(input)
		if !changed {
			t.Fatal("expected changed")
		}
		arr := got.([]interface{})
		if len(arr) != 2 {
			t.Fatalf("expected 2 remaining items, got %d", len(arr))
		}
		if !reflect.DeepEqual(arr[0], windowItem) || !reflect.DeepEqual(arr[1], userItem) {
			t.Error("wrong items retained")
		}
	})

	t.Run("no match returns original unchanged", func(t *testing.T) {
		input := []interface{}{windowItem, userItem}
		got, changed := StripCodexBudgetRemindersFromResponsesInput(input)
		if changed {
			t.Error("expected unchanged")
		}
		if !reflect.DeepEqual(got.([]interface{})[0], windowItem) {
			t.Error("original slice must be returned as-is")
		}
	})

	t.Run("string content shorthand stripped", func(t *testing.T) {
		item := map[string]interface{}{
			"type":    "message",
			"role":    "developer",
			"content": "You have unknown tokens left in this context window.",
		}
		got, changed := StripCodexBudgetRemindersFromResponsesInput([]interface{}{item})
		if !changed {
			t.Fatal("expected changed")
		}
		if arr := got.([]interface{}); len(arr) != 0 {
			t.Errorf("expected empty, got %v", arr)
		}
	})

	t.Run("mixed content message kept", func(t *testing.T) {
		item := map[string]interface{}{
			"type": "message",
			"role": "developer",
			"content": []interface{}{
				map[string]interface{}{"type": "input_text", "text": "You have 710 tokens left in this context window."},
				map[string]interface{}{"type": "input_text", "text": "Additional deployment note."},
			},
		}
		_, changed := StripCodexBudgetRemindersFromResponsesInput([]interface{}{item})
		if changed {
			t.Error("multi-purpose developer message must be kept")
		}
	})

	t.Run("non-array input unchanged", func(t *testing.T) {
		got, changed := StripCodexBudgetRemindersFromResponsesInput("just a string input")
		if changed || got != "just a string input" {
			t.Error("non-array input must pass through")
		}
	})
}

func TestStripCCBudgetRemindersFromBody(t *testing.T) {
	t.Run("system key removed when fully stripped", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-5","system":[{"type":"text","text":"<total_tokens>10 tokens left</total_tokens>"}],"messages":[{"role":"user","content":"hi"}]}`)
		got := StripCCBudgetRemindersFromBody(body)
		var m map[string]interface{}
		if err := json.Unmarshal(got, &m); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if _, has := m["system"]; has {
			t.Error("system key must be deleted, not null")
		}
		if m["model"] != "claude-sonnet-5" {
			t.Error("model must be untouched")
		}
	})

	t.Run("no reminder returns identical bytes", func(t *testing.T) {
		body := []byte(`{"model":"m","system":[{"type":"text","text":"real prompt"}],"messages":[]}`)
		if got := StripCCBudgetRemindersFromBody(body); string(got) != string(body) {
			t.Error("body without reminders must be returned as-is")
		}
	})

	t.Run("invalid json passthrough", func(t *testing.T) {
		body := []byte(`{not json`)
		if got := StripCCBudgetRemindersFromBody(body); string(got) != string(body) {
			t.Error("invalid json must pass through")
		}
	})
}

func TestStripCodexBudgetRemindersFromResponsesBody(t *testing.T) {
	t.Run("reminder item removed", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.6","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"You have 710 tokens left in this context window."}]},{"type":"message","role":"user","content":"hi"}],"stream":true}`)
		got := StripCodexBudgetRemindersFromResponsesBody(body)
		if strings.Contains(string(got), "tokens left") {
			t.Errorf("reminder must be stripped, got: %s", got)
		}
		if !strings.Contains(string(got), `"stream":true`) {
			t.Error("other fields must survive")
		}
	})

	t.Run("no reminder returns identical bytes", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.6","input":[{"type":"message","role":"user","content":"hi"}]}`)
		if got := StripCodexBudgetRemindersFromResponsesBody(body); string(got) != string(body) {
			t.Error("body without reminders must be returned as-is")
		}
	})
}

func TestIsClaudeCodeSystemHeader_RegressionAfterSplit(t *testing.T) {
	// 拆分后合并判定必须保持 1c63bbb2 行为：预算提醒块在转换路径仍被识别
	if !isClaudeCodeSystemHeader("<total_tokens>14963377 tokens left</total_tokens>\n\nengineer-professional output style is active.") {
		t.Error("budget reminder must still be detected as CC header")
	}
	if !isClaudeCodeSystemHeader("You are Claude Code, Anthropic's official CLI for Claude.") {
		t.Error("identity header must still be detected")
	}
	if isClaudeCodeSystemHeader("normal system prompt") {
		t.Error("normal prompt must not be detected")
	}
}
