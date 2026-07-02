package providers

import (
	"io"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestNormalizeSSEFieldLine(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: `data:{"x":1}`, want: `data: {"x":1}`},
		{in: `event:message_start`, want: `event: message_start`},
		{in: `id:123`, want: `id: 123`},
		{in: `retry:3000`, want: `retry: 3000`},
		{in: `data: {"x":1}`, want: `data: {"x":1}`},
	}

	for _, tt := range tests {
		if got := normalizeSSEFieldLine(tt.in); got != tt.want {
			t.Fatalf("normalizeSSEFieldLine(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResponsesProvider_HandleStreamResponse_AcceptsNoSpaceSSELines(t *testing.T) {
	body := strings.Join([]string{
		`event:response.output_item.added`,
		`data:{"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"Read"}}`,
		`event:response.function_call_arguments.delta`,
		`data:{"type":"response.function_call_arguments.delta","delta":"{\"file_path\":\"/tmp/x\"}"}`,
		`event:response.output_item.done`,
		`data:{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"/tmp/x\"}"}}`,
		`event:response.completed`,
		`data:{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
	}, "\n")

	provider := &ResponsesProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	foundToolUse := false
	for _, event := range events {
		if strings.Contains(event, `"type":"tool_use"`) || strings.Contains(event, `"type": "tool_use"`) {
			foundToolUse = true
			break
		}
	}
	if !foundToolUse {
		t.Fatalf("expected normalized no-space SSE lines to produce tool_use events, got %v", events)
	}
}

func TestOpenAIProvider_HandleStreamResponse_AcceptsNoSpaceDataLines(t *testing.T) {
	body := strings.Join([]string{
		`data:{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`,
		`data:{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data:[DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	foundTextDelta := false
	for _, event := range events {
		if strings.Contains(event, `"text":"hello"`) {
			foundTextDelta = true
			break
		}
	}
	if !foundTextDelta {
		t.Fatalf("expected normalized no-space data lines to produce text delta, got %v", events)
	}
}

func TestClaudeProvider_HandleStreamResponse_WrapsBareJSONLines(t *testing.T) {
	body := strings.Join([]string{
		`{}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &ClaudeProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	foundTextDelta := false
	for _, event := range events {
		if strings.Contains(event, `event: content_block_delta`) && strings.Contains(event, `"text":"hello"`) {
			foundTextDelta = true
			break
		}
	}
	if !foundTextDelta {
		t.Fatalf("expected bare JSON line to be wrapped as SSE data, got %v", events)
	}
}

func TestClaudeProvider_HandleStreamResponse_CompletesBareDeltaStream(t *testing.T) {
	body := strings.Join([]string{
		`{}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`data: {"service_tier":"default","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1}}`,
		`data: [DONE]`,
		`event: ping`,
		`data: {"type":"ping","cost":"0"}`,
		"",
	}, "\n")

	provider := &ClaudeProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	joined := strings.Join(events, "\n")
	for _, want := range []string{
		`event: message_start`,
		`"type":"message_start"`,
		`event: content_block_start`,
		`"type":"content_block_start"`,
		`event: content_block_delta`,
		`"text":"hello"`,
		`event: content_block_stop`,
		`event: message_delta`,
		`"stop_reason":"end_turn"`,
		`event: message_stop`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected completed Claude SSE to contain %q, got %v", want, events)
		}
	}
}

func TestOpenAIProvider_HandleStreamResponse_MapsReasoningContentToThinkingDelta(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-1","model":"deepseek-v4-pro","choices":[{"delta":{"reasoning_content":"think"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","model":"deepseek-v4-pro","choices":[{"delta":{"content":"answer"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","model":"deepseek-v4-pro","choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, `"type":"thinking"`) {
		t.Fatalf("expected thinking content block, got %v", events)
	}
	if !strings.Contains(joined, `"type":"thinking_delta"`) || !strings.Contains(joined, `"thinking":"think"`) {
		t.Fatalf("expected thinking_delta from reasoning_content, got %v", events)
	}
	if !strings.Contains(joined, `"type":"text_delta"`) || !strings.Contains(joined, `"text":"answer"`) {
		t.Fatalf("expected text delta after reasoning, got %v", events)
	}
}

// TestOpenAIProvider_HandleStreamResponse_EmptyToolCallsDoesNotSplitTextBlock 验证
// vLLM delta 中的空 tool_calls:[] 不会导致 text block 被拆分为多个独立块
func TestOpenAIProvider_HandleStreamResponse_EmptyToolCallsDoesNotSplitTextBlock(t *testing.T) {
	// 真实 vLLM 0.22.x 流式响应：每个 delta 都携带空 tool_calls:[]
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-ac0e1a6028c01fc8","object":"chat.completion.chunk","created":1782310028,"model":"glm-5.2","choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}],"prompt_token_ids":null,"prompt_text":null}`,
		`data: {"id":"chatcmpl-ac0e1a6028c01fc8","object":"chat.completion.chunk","created":1782310028,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"你好","tool_calls":[]},"logprobs":null,"finish_reason":null,"token_ids":null}]}`,
		`data: {"id":"chatcmpl-ac0e1a6028c01fc8","object":"chat.completion.chunk","created":1782310028,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"！有什么","tool_calls":[]},"logprobs":null,"finish_reason":null,"token_ids":null}]}`,
		`data: {"id":"chatcmpl-ac0e1a6028c01fc8","object":"chat.completion.chunk","created":1782310028,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"可以","tool_calls":[]},"logprobs":null,"finish_reason":null,"token_ids":null}]}`,
		`data: {"id":"chatcmpl-ac0e1a6028c01fc8","object":"chat.completion.chunk","created":1782310028,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"帮你的吗","tool_calls":[]},"logprobs":null,"finish_reason":null,"token_ids":null}]}`,
		`data: {"id":"chatcmpl-ac0e1a6028c01fc8","object":"chat.completion.chunk","created":1782310028,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"？😊","tool_calls":[]},"logprobs":null,"finish_reason":null,"token_ids":null}]}`,
		`data: {"id":"chatcmpl-ac0e1a6028c01fc8","object":"chat.completion.chunk","created":1782310028,"model":"glm-5.2","choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop","stop_reason":null,"token_ids":null}]}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	// 应该只有 1 个 content_block_start（type=text），不能被空 tool_calls 拆成多个
	startCount := 0
	for _, event := range events {
		if strings.Contains(event, `"type":"content_block_start"`) {
			startCount++
		}
	}
	if startCount != 1 {
		t.Fatalf("expected exactly 1 content_block_start for text, got %d.\nevents: %v", startCount, events)
	}
}

// TestOpenAIProvider_HandleStreamResponse_MapsVLLMReasoningToThinkingDelta 验证 vLLM 的
// reasoning 字段（非 reasoning_content）也能正确映射为 Claude thinking 事件
func TestOpenAIProvider_HandleStreamResponse_MapsVLLMReasoningToThinkingDelta(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-vllm","model":"glm-5.2","choices":[{"delta":{"reasoning":"让我思考"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-vllm","model":"glm-5.2","choices":[{"delta":{"reasoning":"一下"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-vllm","model":"glm-5.2","choices":[{"delta":{"content":"你好！"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-vllm","model":"glm-5.2","choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, `"type":"thinking"`) {
		t.Fatalf("expected thinking content block from vLLM reasoning field, got %v", events)
	}
	if !strings.Contains(joined, `"type":"thinking_delta"`) || !strings.Contains(joined, `"thinking":"让我思考"`) {
		t.Fatalf("expected thinking_delta from vLLM reasoning, got %v", events)
	}
	if !strings.Contains(joined, `"type":"text_delta"`) || !strings.Contains(joined, `"text":"你好！"`) {
		t.Fatalf("expected text delta after reasoning, got %v", events)
	}
}

func TestOpenAIProvider_ConvertToClaudeResponse_MapsVLLMReasoningToThinking(t *testing.T) {
	provider := &OpenAIProvider{}
	claudeResp, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{
		Body: []byte(`{
			"id": "chatcmpl-vllm",
			"choices": [{
				"message": {
					"role": "assistant",
					"reasoning": "vllm reasoning",
					"content": "final answer"
				},
				"finish_reason": "stop"
			}]
		}`),
	})
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse() error = %v", err)
	}
	if len(claudeResp.Content) != 2 {
		t.Fatalf("Content len = %d, want 2: %#v", len(claudeResp.Content), claudeResp.Content)
	}
	if claudeResp.Content[0].Type != "thinking" || claudeResp.Content[0].Thinking != "vllm reasoning" {
		t.Fatalf("expected vLLM reasoning mapped to thinking block, got %#v", claudeResp.Content[0])
	}
	if claudeResp.Content[1].Type != "text" || claudeResp.Content[1].Text != "final answer" {
		t.Fatalf("expected text block after thinking, got %#v", claudeResp.Content[1])
	}
}

func TestClaudeProvider_ConvertToClaudeResponse_FillsRequiredFields(t *testing.T) {
	provider := &ClaudeProvider{}
	claudeResp, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{
		Body: []byte(`{
			"role": "assistant",
			"content": [
				{"type": "text", "text": "ok"},
				{"type": "tool_use", "id": "chatcmpl-tool-x", "name": "Agent", "input": {"description": "x", "prompt": "p", "subagent_type": "Explore"}}
			],
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 1, "output_tokens": 2}
		}`),
	})
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse() error = %v", err)
	}
	if claudeResp.ID == "" {
		t.Fatal("expected generated id")
	}
	if claudeResp.Type != "message" {
		t.Fatalf("Type = %q, want message", claudeResp.Type)
	}
	if claudeResp.Role != "assistant" {
		t.Fatalf("Role = %q, want assistant", claudeResp.Role)
	}
	if len(claudeResp.Content) != 2 || claudeResp.Content[1].Type != "tool_use" {
		t.Fatalf("expected tool_use content to be preserved, got %#v", claudeResp.Content)
	}
}

func TestGeminiProvider_HandleStreamResponse_AcceptsNoSpaceDataLines(t *testing.T) {
	body := strings.Join([]string{
		`data:{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}]}`,
		`data:{"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":2}}`,
		"",
	}, "\n")

	provider := &GeminiProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	foundTextDelta := false
	for _, event := range events {
		if strings.Contains(event, `"text":"OK"`) {
			foundTextDelta = true
			break
		}
	}
	if !foundTextDelta {
		t.Fatalf("expected normalized no-space data lines to produce text delta, got %v", events)
	}
}
