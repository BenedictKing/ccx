package providers

import (
	"io"
	"strings"
	"testing"
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
	eventChan, errChan, err := provider.HandleStreamResponse(nil, io.NopCloser(strings.NewReader(body)))
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
	eventChan, errChan, err := provider.HandleStreamResponse(nil, io.NopCloser(strings.NewReader(body)))
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

func TestGeminiProvider_HandleStreamResponse_AcceptsNoSpaceDataLines(t *testing.T) {
	body := strings.Join([]string{
		`data:{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}]}`,
		`data:{"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":2}}`,
		"",
	}, "\n")

	provider := &GeminiProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(nil, io.NopCloser(strings.NewReader(body)))
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
