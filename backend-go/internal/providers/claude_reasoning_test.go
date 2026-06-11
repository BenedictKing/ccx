package providers

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestStripEmptyTextBlocksFromBody(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantJSON map[string]interface{}
	}{
		{
			name: "移除 assistant tool_use 前的裸空 text block",
			input: `{
				"messages": [
					{"role": "assistant", "content": [
						{"type": "text", "text": ""},
						{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": {"command": "pwd"}}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role": "assistant",
						"content": []interface{}{
							map[string]interface{}{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]interface{}{"command": "pwd"}},
						},
					},
				},
			},
		},
		{
			name: "保留非空 text block",
			input: `{
				"messages": [
					{"role": "assistant", "content": [
						{"type": "text", "text": "hello"},
						{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": {"command": "pwd"}}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role": "assistant",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "hello"},
							map[string]interface{}{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]interface{}{"command": "pwd"}},
						},
					},
				},
			},
		},
		{
			name: "保留带附加字段的空 text block",
			input: `{
				"messages": [
					{"role": "assistant", "content": [
						{"type": "text", "text": "", "cache_control": {"type": "ephemeral"}},
						{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": {"command": "pwd"}}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{
						"role": "assistant",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "", "cache_control": map[string]interface{}{"type": "ephemeral"}},
							map[string]interface{}{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]interface{}{"command": "pwd"}},
						},
					},
				},
			},
		},
		{
			name: "不处理 system 空 text",
			input: `{
				"system": [{"type": "text", "text": ""}],
				"messages": [
					{"role": "user", "content": [{"type": "text", "text": "hello"}]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"system": []interface{}{map[string]interface{}{"type": "text", "text": ""}},
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "user",
						"content": []interface{}{map[string]interface{}{"type": "text", "text": "hello"}},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripEmptyTextBlocksFromBody([]byte(tt.input))

			var got map[string]interface{}
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}

			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.wantJSON)

			if string(gotJSON) != string(wantJSON) {
				t.Errorf("stripEmptyTextBlocksFromBody() mismatch:\ngot:  %s\nwant: %s", string(gotJSON), string(wantJSON))
			}
		})
	}
}

func TestStripThinkingBlocksFromBody(t *testing.T) {
	input := `{
		"messages": [
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "drop", "signature": "foreign_sig"},
				{"type": "redacted_thinking", "data": "opaque"},
				{"type": "text", "text": "answer"},
				{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": {"command": "pwd"}}
			]},
			{"role": "user", "content": [{"type": "text", "text": "hello"}]}
		]
	}`

	result := stripThinkingBlocksFromBody([]byte(input))

	var got map[string]interface{}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	messages, _ := got["messages"].([]interface{})
	assistantMsg, _ := messages[0].(map[string]interface{})
	content, _ := assistantMsg["content"].([]interface{})
	if len(content) != 2 {
		t.Fatalf("assistant content len = %d, want 2", len(content))
	}

	firstBlock, _ := content[0].(map[string]interface{})
	if firstBlock["type"] != "text" {
		t.Fatalf("first remaining block type = %v, want text", firstBlock["type"])
	}
	secondBlock, _ := content[1].(map[string]interface{})
	if secondBlock["type"] != "tool_use" {
		t.Fatalf("second remaining block type = %v, want tool_use", secondBlock["type"])
	}
}

// TestConvertThinkingToReasoningContent 测试 assistant 历史消息补顶层 reasoning_content，并清理 content thinking
func TestConvertThinkingToReasoningContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantJSON map[string]interface{}
	}{
		{
			name: "已有 thinking 块的 assistant 消息转为 reasoning_content",
			input: `{
				"model": "mimo-v2.5-pro",
				"messages": [
					{"role": "user", "content": [{"type": "text", "text": "hello"}]},
					{"role": "assistant", "content": [
						{"type": "thinking", "thinking": "let me think", "signature": "sig_123"},
						{"type": "text", "text": "response"}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "hello"}}},
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": "let me think",
						"content": []interface{}{
							map[string]interface{}{"type": "thinking", "thinking": "let me think", "signature": "sig_123"},
							map[string]interface{}{"type": "text", "text": "response"},
						},
					},
				},
			},
		},
		{
			name: "无真实 thinking 块的 assistant 消息补空 reasoning_content",
			input: `{
				"model": "mimo-v2.5-pro",
				"messages": [
					{"role": "user", "content": [{"type": "text", "text": "hello"}]},
					{"role": "assistant", "content": [
						{"type": "text", "text": "answer"},
						{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": {"command": "pwd"}}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "hello"}}},
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": legacyThinkingPlaceholder,
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "answer"},
							map[string]interface{}{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]interface{}{"command": "pwd"}, "reasoning_content": legacyThinkingPlaceholder},
						},
					},
				},
			},
		},
		{
			name: "历史占位 thinking 块会被移除并补空 reasoning_content",
			input: `{
				"model": "mimo-v2.5-pro",
				"messages": [
					{"role": "user", "content": [{"type": "text", "text": "hello"}]},
					{"role": "assistant", "content": [
						{"type": "thinking", "thinking": "(no prior reasoning recorded)"},
						{"type": "text", "text": "answer"}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "hello"}}},
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": legacyThinkingPlaceholder,
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "answer"},
						},
					},
				},
			},
		},
		{
			name: "只有历史占位 thinking 的 assistant 消息补空 reasoning_content 并转为非空 text",
			input: `{
				"model": "mimo-v2.5-pro",
				"messages": [
					{"role": "user", "content": [{"type": "text", "text": "hello"}]},
					{"role": "assistant", "content": [
						{"type": "thinking", "thinking": "(no prior reasoning recorded)"}
					]},
					{"role": "user", "content": [{"type": "text", "text": "next"}]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "hello"}}},
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": legacyThinkingPlaceholder,
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": missingAssistantResponseText},
						},
					},
					map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "next"}}},
				},
			},
		},
		{
			name: "assistant string content 补空 reasoning_content",
			input: `{
				"model": "mimo-v2.5-pro",
				"messages": [
					{"role": "assistant", "content": "plain answer"}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": legacyThinkingPlaceholder,
						"content":           "plain answer",
					},
				},
			},
		},
		{
			name: "thinking block type 带空白仍会被识别",
			input: `{
				"model": "mimo-v2.5-pro",
				"messages": [
					{"role": "assistant", "content": [
						{"type": " thinking ", "thinking": "  nested reasoning  "},
						{"type": "text", "text": "answer"}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": "  nested reasoning  ",
						"content": []interface{}{
							map[string]interface{}{"type": " thinking ", "thinking": "  nested reasoning  "},
							map[string]interface{}{"type": "text", "text": "answer"},
						},
					},
				},
			},
		},
		{
			name: "content block 内 reasoning_content 会提升到顶层",
			input: `{
				"model": "mimo-v2.5-pro",
				"messages": [
					{"role": "assistant", "content": [
						{"type": "text", "reasoning_content": "block reasoning", "text": "answer"}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": "block reasoning",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "answer"},
						},
					},
				},
			},
		},
		{
			name: "assistant 缺失 content 补空 reasoning_content",
			input: `{
				"model": "mimo-v2.5-pro",
				"messages": [
					{"role": "assistant"}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": legacyThinkingPlaceholder,
					},
				},
			},
		},
		{
			name: "assistant 空 content 数组补空 reasoning_content 并转为非空 text",
			input: `{
				"model": "mimo-v2.5-pro",
				"messages": [
					{"role": "assistant", "content": []}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": legacyThinkingPlaceholder,
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": missingAssistantResponseText},
						},
					},
				},
			},
		},
		{
			name:  "已有顶层 reasoning_content 的 assistant string content 保持原值",
			input: `{"model": "mimo-v2.5-pro", "messages": [{"role": "assistant", "reasoning_content": "prior reasoning", "content": "plain answer"}]}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": "prior reasoning",
						"content":           "plain answer",
					},
				},
			},
		},
		{
			name:  "assistant 仅 tool_use 且无 thinking 时补非空 reasoning_content",
			input: `{"model": "mimo-v2.5-pro", "messages": [{"role": "assistant", "content": [{"type": "tool_use", "id": "call_x", "name": "Edit", "input": {"file_path": "a.go"}}]}]}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": legacyThinkingPlaceholder,
						"content": []interface{}{
							map[string]interface{}{"type": "tool_use", "id": "call_x", "name": "Edit", "input": map[string]interface{}{"file_path": "a.go"}, "reasoning_content": legacyThinkingPlaceholder},
						},
					},
				},
			},
		},
		{
			name:  "assistant 顶层 reasoning_content 为空串时回填占位",
			input: `{"model": "mimo-v2.5-pro", "messages": [{"role": "assistant", "reasoning_content": "", "content": [{"type": "tool_use", "id": "call_y", "name": "Edit", "input": {"file_path": "b.go"}}]}]}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": legacyThinkingPlaceholder,
						"content": []interface{}{
							map[string]interface{}{"type": "tool_use", "id": "call_y", "name": "Edit", "input": map[string]interface{}{"file_path": "b.go"}, "reasoning_content": legacyThinkingPlaceholder},
						},
					},
				},
			},
		},
		{
			name:  "已有顶层 reasoning_content 时不覆盖原值",
			input: `{"model": "mimo-v2.5-pro", "messages": [{"role": "assistant", "reasoning_content": "exact-passthrough", "content": [{"type": "thinking", "thinking": "new thinking that should not override"}, {"type": "text", "text": "plain answer"}]}]}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": "exact-passthrough",
						"content": []interface{}{
							map[string]interface{}{"type": "thinking", "thinking": "new thinking that should not override"},
							map[string]interface{}{"type": "text", "text": "plain answer"},
						},
					},
				},
			},
		},
		{
			name: "user 消息不受影响",
			input: `{
				"model": "mimo-v2.5-pro",
				"messages": [
					{"role": "user", "content": [{"type": "text", "text": "hello"}]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "mimo-v2.5-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role":    "user",
						"content": []interface{}{map[string]interface{}{"type": "text", "text": "hello"}},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertThinkingToReasoningContent([]byte(tt.input))

			var got map[string]interface{}
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}

			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.wantJSON)

			if string(gotJSON) != string(wantJSON) {
				t.Errorf("convertThinkingToReasoningContent() mismatch:\ngot:  %s\nwant: %s", string(gotJSON), string(wantJSON))
			}
		})
	}
}

// TestConvertReasoningContentToThinking 测试 reasoning_content → thinking 块转换
func TestConvertReasoningContentToThinking(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantJSON map[string]interface{}
	}{
		{
			name: "顶层 reasoning_content 转为 thinking 块",
			input: `{
				"id": "msg_123",
				"type": "message",
				"role": "assistant",
				"reasoning_content": "internal reasoning",
				"content": [{"type": "text", "text": "answer"}],
				"stop_reason": "end_turn"
			}`,
			wantJSON: map[string]interface{}{
				"id":          "msg_123",
				"type":        "message",
				"role":        "assistant",
				"content":     []interface{}{map[string]interface{}{"type": "thinking", "thinking": "internal reasoning"}, map[string]interface{}{"type": "text", "text": "answer"}},
				"stop_reason": "end_turn",
			},
		},
		{
			name: "content 块中的 reasoning_content 转为 thinking",
			input: `{
				"id": "msg_456",
				"type": "message",
				"role": "assistant",
				"content": [
					{"type": "text", "reasoning_content": "block reasoning", "text": "answer"}
				],
				"stop_reason": "end_turn"
			}`,
			wantJSON: map[string]interface{}{
				"id":   "msg_456",
				"type": "message",
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "thinking", "thinking": "block reasoning", "text": "answer"},
				},
				"stop_reason": "end_turn",
			},
		},
		{
			name: "无 reasoning_content 不修改",
			input: `{
				"id": "msg_789",
				"type": "message",
				"role": "assistant",
				"content": [{"type": "text", "text": "answer"}],
				"stop_reason": "end_turn"
			}`,
			wantJSON: map[string]interface{}{
				"id":          "msg_789",
				"type":        "message",
				"role":        "assistant",
				"content":     []interface{}{map[string]interface{}{"type": "text", "text": "answer"}},
				"stop_reason": "end_turn",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertReasoningContentToThinking([]byte(tt.input))

			var got map[string]interface{}
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}

			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.wantJSON)

			if string(gotJSON) != string(wantJSON) {
				t.Errorf("convertReasoningContentToThinking() mismatch:\ngot:  %s\nwant: %s", string(gotJSON), string(wantJSON))
			}
		})
	}
}

// TestClaudeProvider_ConvertToClaudeResponse_WithReasoningContent 测试响应转换
func TestClaudeProvider_ConvertToClaudeResponse_WithReasoningContent(t *testing.T) {
	provider := &ClaudeProvider{}

	t.Run("reasoning_content 转为 thinking 块", func(t *testing.T) {
		providerResp := &types.ProviderResponse{
			StatusCode: 200,
			Body: []byte(`{
				"id": "msg_mimo",
				"type": "message",
				"role": "assistant",
				"reasoning_content": "mimo reasoning",
				"content": [{"type": "text", "text": "mimo answer"}],
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 10, "output_tokens": 20}
			}`),
		}

		claudeResp, err := provider.ConvertToClaudeResponse(providerResp)
		if err != nil {
			t.Fatalf("ConvertToClaudeResponse() error = %v", err)
		}

		if len(claudeResp.Content) != 2 {
			t.Fatalf("content length = %d, want 2", len(claudeResp.Content))
		}

		if claudeResp.Content[0].Type != "thinking" {
			t.Errorf("content[0].type = %q, want thinking", claudeResp.Content[0].Type)
		}
		if claudeResp.Content[0].Thinking != "mimo reasoning" {
			t.Errorf("content[0].thinking = %q, want mimo reasoning", claudeResp.Content[0].Thinking)
		}
		if claudeResp.Content[1].Type != "text" {
			t.Errorf("content[1].type = %q, want text", claudeResp.Content[1].Type)
		}
	})

	t.Run("无 reasoning_content 正常解析", func(t *testing.T) {
		providerResp := &types.ProviderResponse{
			StatusCode: 200,
			Body: []byte(`{
				"id": "msg_normal",
				"type": "message",
				"role": "assistant",
				"content": [{"type": "text", "text": "normal answer"}],
				"stop_reason": "end_turn",
				"usage": {"input_tokens": 5, "output_tokens": 10}
			}`),
		}

		claudeResp, err := provider.ConvertToClaudeResponse(providerResp)
		if err != nil {
			t.Fatalf("ConvertToClaudeResponse() error = %v", err)
		}

		if len(claudeResp.Content) != 1 {
			t.Fatalf("content length = %d, want 1", len(claudeResp.Content))
		}
		if claudeResp.Content[0].Type != "text" {
			t.Errorf("content[0].type = %q, want text", claudeResp.Content[0].Type)
		}
	})
}

// TestClaudeProvider_HandleStreamResponse_WithReasoningContent 测试流式响应转换
func TestClaudeProvider_HandleStreamResponse_WithReasoningContent(t *testing.T) {
	provider := &ClaudeProvider{}

	streamBody := `event: message_start
data: {"type":"message_start","message":{"id":"msg_stream","type":"message","role":"assistant","content":[]}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","reasoning_content":"stream reasoning"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"stream answer"}}

event: message_stop
data: {"type":"message_stop"}

`

	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(streamBody)))
	if err != nil {
		t.Fatalf("HandleStreamResponse() error = %v", err)
	}

	var events []string
	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				goto done
			}
			events = append(events, event)
		case err := <-errChan:
			if err != nil {
				t.Fatalf("stream error: %v", err)
			}
		}
	}
done:

	joined := strings.Join(events, "")

	// 验证 reasoning_content 被转换为 thinking_delta
	if !strings.Contains(joined, `"type":"thinking_delta"`) {
		t.Errorf("expected thinking_delta in stream, got: %s", joined)
	}
	if !strings.Contains(joined, `"thinking":"stream reasoning"`) {
		t.Errorf("expected thinking field in stream, got: %s", joined)
	}
	if strings.Contains(joined, `"reasoning_content"`) {
		t.Errorf("reasoning_content should be removed from stream, got: %s", joined)
	}
}

func TestConvertThinkingToReasoningContent_ChannelSwitch(t *testing.T) {
	// 模拟 MiMo → GPT → MiMo 切换场景
	// assistant[1] 来自 MiMo 阶段：已有 reasoning_content + thinking blocks
	// assistant[3] 来自 GPT 阶段：Claude Code 空 thinking block + text
	input := `{
		"model": "mimo-v2.5-pro",
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "hello"}]},
			{"role": "assistant", "reasoning_content": "real thinking", "content": [
				{"type": "thinking", "thinking": "real thinking"},
				{"type": "text", "text": "mimo answer"}
			]},
			{"role": "user", "content": [{"type": "text", "text": "next"}]},
			{"role": "assistant", "content": [
				{"type": "thinking"},
				{"type": "text", "text": "gpt channel answer"}
			]}
		]
	}`

	result := convertThinkingToReasoningContent([]byte(input))

	var got map[string]interface{}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	messages := got["messages"].([]interface{})
	for i, m := range messages {
		msg := m.(map[string]interface{})
		if role, _ := msg["role"].(string); role != "assistant" {
			continue
		}
		rc, hasRC := msg["reasoning_content"].(string)
		if !hasRC || strings.TrimSpace(rc) == "" {
			t.Errorf("assistant[%d] missing/empty reasoning_content: has=%v rc=%q", i, hasRC, rc)
		}
	}
}

func TestConvertReasoningContentToThinkingBlocks(t *testing.T) {
	tests := []struct {
		name                  string
		keepTopLevelReasoning bool
		input                 string
		wantJSON              map[string]interface{}
	}{
		{
			name:                  "严格 thinking 回传清理占位并保留真实 thinking",
			keepTopLevelReasoning: true,
			input: `{
				"model": "deepseek-v4-pro",
				"messages": [
					{"role": "user", "content": [{"type": "text", "text": "hello"}]},
					{"role": "assistant", "reasoning_content": "(no prior reasoning recorded)", "content": [
						{"type": "text", "text": "old answer"},
						{"type": "tool_use", "id": "call_1", "name": "Read", "input": {"file_path": "a.go"}}
					]},
					{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "call_1", "content": "ok"}]},
					{"role": "assistant", "reasoning_content": "real reasoning", "content": [
						{"type": "thinking", "thinking": "real reasoning"},
						{"type": "text", "text": "new answer"}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "deepseek-v4-pro",
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "text", "text": "hello"}}},
					map[string]interface{}{
						"role": "assistant",
						"content": []interface{}{
							map[string]interface{}{"type": "text", "text": "old answer"},
							map[string]interface{}{"type": "tool_use", "id": "call_1", "name": "Read", "input": map[string]interface{}{"file_path": "a.go"}},
						},
					},
					map[string]interface{}{"role": "user", "content": []interface{}{map[string]interface{}{"type": "tool_result", "tool_use_id": "call_1", "content": "ok"}}},
					map[string]interface{}{
						"role":              "assistant",
						"reasoning_content": "real reasoning",
						"content": []interface{}{
							map[string]interface{}{"type": "thinking", "thinking": "real reasoning"},
							map[string]interface{}{"type": "text", "text": "new answer"},
						},
					},
				},
			},
		},
		{
			name:                  "真实顶层 reasoning_content 注入 thinking block",
			keepTopLevelReasoning: false,
			input: `{
				"model": "deepseek-v4-pro",
				"messages": [
					{"role": "assistant", "reasoning_content": "top reasoning", "content": [
						{"type": "text", "text": "answer"}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "deepseek-v4-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role": "assistant",
						"content": []interface{}{
							map[string]interface{}{"type": "thinking", "thinking": "top reasoning"},
							map[string]interface{}{"type": "text", "text": "answer"},
						},
					},
				},
			},
		},
		{
			name:                  "content block 内 reasoning_content 注入 thinking block",
			keepTopLevelReasoning: false,
			input: `{
				"model": "deepseek-v4-pro",
				"messages": [
					{"role": "assistant", "content": [
						{"type": "text", "reasoning_content": "block reasoning", "text": "answer"}
					]}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "deepseek-v4-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role": "assistant",
						"content": []interface{}{
							map[string]interface{}{"type": "thinking", "thinking": "block reasoning"},
							map[string]interface{}{"type": "text", "text": "answer"},
						},
					},
				},
			},
		},
		{
			name:                  "string content 有真实 reasoning 时转为 Claude content 数组",
			keepTopLevelReasoning: false,
			input: `{
				"model": "deepseek-v4-pro",
				"messages": [
					{"role": "assistant", "reasoning_content": "plain reasoning", "content": "plain answer"}
				]
			}`,
			wantJSON: map[string]interface{}{
				"model": "deepseek-v4-pro",
				"messages": []interface{}{
					map[string]interface{}{
						"role": "assistant",
						"content": []interface{}{
							map[string]interface{}{"type": "thinking", "thinking": "plain reasoning"},
							map[string]interface{}{"type": "text", "text": "plain answer"},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertReasoningContentToThinkingBlocks([]byte(tt.input), tt.keepTopLevelReasoning)

			var got map[string]interface{}
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}

			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.wantJSON)

			if string(gotJSON) != string(wantJSON) {
				t.Errorf("convertReasoningContentToThinkingBlocks() mismatch:\ngot:  %s\nwant: %s", string(gotJSON), string(wantJSON))
			}
		})
	}
}
