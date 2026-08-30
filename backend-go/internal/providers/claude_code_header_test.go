package providers

import (
	"strings"
	"testing"
)

func TestIsClaudeCodeSystemHeader(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		// Claude Code billing header
		{
			name:     "billing header",
			text:     "x-anthropic-billing-header: cc_version=2.1.216.046; cc_entrypoint=cli;",
			expected: true,
		},
		{
			name:     "billing header with cch",
			text:     "x-anthropic-billing-header: cc_version=2.1.216.046; cc_entrypoint=cli; cch=abc123;",
			expected: true,
		},
		// Claude Code 每轮注入的上下文余量提醒
		{
			name:     "token budget reminder with style suffix",
			text:     "<total_tokens>14963377 tokens left</total_tokens>\n\nengineer-professional output style is active. Remember to follow the specific guidelines for this style.",
			expected: true,
		},
		{
			name:     "token budget reminder fresh budget",
			text:     "<total_tokens>15000000 tokens left</total_tokens>\n\nengineer-professional output style is active. Remember to follow the specific guidelines for this style.",
			expected: true,
		},
		{
			name:     "token budget reminder with comma formatted number",
			text:     "<total_tokens>14,963,377 tokens left</total_tokens>",
			expected: true,
		},
		// 无 tokens 标签的 output style 提醒不在此模式处理范围
		{
			name:     "style reminder without token tag",
			text:     "engineer-professional output style is active. Remember to follow the specific guidelines for this style.",
			expected: false,
		},
		{
			name:     "token tag mentioned mid-text should not filter",
			text:     "You have <total_tokens>123 tokens left</total_tokens> per the docs.",
			expected: false,
		},
		// Claude Code identity
		{
			name:     "claude code identity",
			text:     "You are Claude Code, Anthropic's official CLI for Claude.",
			expected: true,
		},
		// Subagent identity
		{
			name:     "claude agent sdk identity",
			text:     "You are a Claude agent, built on Anthropic's Claude Agent SDK.",
			expected: true,
		},
		// Subagent role
		{
			name:     "file search specialist",
			text:     "You are a file search specialist for Claude Code, Anthropic's official CLI for Claude.",
			expected: true,
		},
		{
			name:     "explore agent",
			text:     "You are an Explore agent for Claude Code, Anthropic's official CLI for Claude.",
			expected: true,
		},
		// 真实 system prompt（不应被过滤）
		{
			name:     "real system prompt",
			text:     "You are a helpful assistant.",
			expected: false,
		},
		{
			name:     "custom instructions",
			text:     "Always respond in Chinese.",
			expected: false,
		},
		{
			name:     "partial match should not filter",
			text:     "I am Claude Code, not you.",
			expected: false,
		},
		{
			name:     "empty string",
			text:     "",
			expected: false,
		},
		{
			name:     "whitespace only",
			text:     "   ",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isClaudeCodeSystemHeader(tt.text)
			if result != tt.expected {
				t.Errorf("isClaudeCodeSystemHeader(%q) = %v, want %v", tt.text, result, tt.expected)
			}
		})
	}
}

func TestExtractSystemTextBlocks_FiltersClaudeCodeHeaders(t *testing.T) {
	system := []interface{}{
		map[string]interface{}{
			"type": "text",
			"text": "x-anthropic-billing-header: cc_version=2.1.216.046; cc_entrypoint=cli;",
		},
		map[string]interface{}{
			"type": "text",
			"text": "You are Claude Code, Anthropic's official CLI for Claude.",
		},
		map[string]interface{}{
			"type": "text",
			"text": "You are a helpful assistant.",
		},
	}

	result := extractSystemTextBlocks(system, 0)

	// 应只保留真实的 system prompt
	if strings.Contains(result, "x-anthropic-billing-header") {
		t.Errorf("extractSystemTextBlocks should filter billing header, got: %s", result)
	}
	if strings.Contains(result, "You are Claude Code") {
		t.Errorf("extractSystemTextBlocks should filter CC identity, got: %s", result)
	}
	if !strings.Contains(result, "You are a helpful assistant.") {
		t.Errorf("extractSystemTextBlocks should keep real system prompt, got: %s", result)
	}
}

func TestExtractSystemTextBlocks_FiltersTokenBudgetReminders(t *testing.T) {
	// 复刻真实 Claude Code 请求：同一会话多轮累积的 tokens-left 提醒块
	system := []interface{}{
		map[string]interface{}{
			"type": "text",
			"text": "x-anthropic-billing-header: cc_version=2.1.241.bc7; cc_entrypoint=cli;",
		},
		map[string]interface{}{
			"type": "text",
			"text": "You are Claude Code, Anthropic's official CLI for Claude.",
		},
		map[string]interface{}{
			"type": "text",
			"text": "You are a helpful assistant.",
		},
		map[string]interface{}{
			"type": "text",
			"text": "<total_tokens>14963377 tokens left</total_tokens>\n\nengineer-professional output style is active. Remember to follow the specific guidelines for this style.",
		},
		map[string]interface{}{
			"type": "text",
			"text": "<total_tokens>14940872 tokens left</total_tokens>\n\nengineer-professional output style is active. Remember to follow the specific guidelines for this style.",
		},
		map[string]interface{}{
			"type": "text",
			"text": "<total_tokens>15000000 tokens left</total_tokens>\n\nengineer-professional output style is active. Remember to follow the specific guidelines for this style.",
		},
		map[string]interface{}{
			"type": "text",
			"text": "Always respond in Chinese.",
		},
	}

	result := extractSystemTextBlocks(system, 0)

	if strings.Contains(result, "total_tokens") || strings.Contains(result, "tokens left") {
		t.Errorf("extractSystemTextBlocks should filter token budget reminders, got: %s", result)
	}
	if !strings.Contains(result, "You are a helpful assistant.") {
		t.Errorf("extractSystemTextBlocks should keep real system prompt, got: %s", result)
	}
	if !strings.Contains(result, "Always respond in Chinese.") {
		t.Errorf("extractSystemTextBlocks should keep trailing real block, got: %s", result)
	}
}

func TestExtractSystemTextBlocks_PreservesNonCCHeaders(t *testing.T) {
	system := []interface{}{
		map[string]interface{}{
			"type": "text",
			"text": "You are a helpful assistant.",
		},
		map[string]interface{}{
			"type": "text",
			"text": "Always respond in Chinese.",
		},
	}

	result := extractSystemTextBlocks(system, 0)

	if !strings.Contains(result, "You are a helpful assistant.") {
		t.Errorf("extractSystemTextBlocks should keep first block, got: %s", result)
	}
	if !strings.Contains(result, "Always respond in Chinese.") {
		t.Errorf("extractSystemTextBlocks should keep second block, got: %s", result)
	}
}

func TestExtractSystemTextBlocks_StringSystem(t *testing.T) {
	// 字符串形式的 system 不应受影响
	result := extractSystemTextBlocks("You are a helpful assistant.", 0)
	if result != "You are a helpful assistant." {
		t.Errorf("extractSystemTextBlocks should return string as-is, got: %s", result)
	}
}

func TestExtractSystemTextBlocks_SubagentHeaders(t *testing.T) {
	system := []interface{}{
		map[string]interface{}{
			"type": "text",
			"text": "x-anthropic-billing-header: cc_version=2.1.216.9fa; cc_entrypoint=cli; cc_is_subagent=true;",
		},
		map[string]interface{}{
			"type": "text",
			"text": "You are a Claude agent, built on Anthropic's Claude Agent SDK.",
		},
		map[string]interface{}{
			"type": "text",
			"text": "You are a file search specialist for Claude Code, Anthropic's official CLI for Claude. You excel at thoroughly navigating and exploring codebases.",
		},
	}

	result := extractSystemTextBlocks(system, 0)

	// 所有 subagent header 都应被过滤
	if strings.Contains(result, "x-anthropic-billing-header") {
		t.Errorf("should filter billing header, got: %s", result)
	}
	if strings.Contains(result, "Claude Agent SDK") {
		t.Errorf("should filter agent SDK identity, got: %s", result)
	}
	if strings.Contains(result, "file search specialist") {
		t.Errorf("should filter specialist role, got: %s", result)
	}
	// 过滤后应为空
	if result != "" {
		t.Errorf("all subagent headers should be filtered, got: %s", result)
	}
}
