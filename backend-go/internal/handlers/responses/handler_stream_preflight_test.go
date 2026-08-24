package responses

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/handlers/common"
)

func TestHasResponsesSemanticContent(t *testing.T) {
	t.Run("function call arguments delta", func(t *testing.T) {
		event := "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"\"}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected function_call_arguments.delta to be treated as non-text content")
		}
	})

	t.Run("custom tool call input done", func(t *testing.T) {
		event := "event: response.custom_tool_call_input.done\ndata: {\"type\":\"response.custom_tool_call_input.done\",\"call_id\":\"call_1\",\"input\":\"{\\\"query\\\":\\\"sub-agent\\\"}\"}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected custom_tool_call_input.done to be treated as non-text content")
		}
	})

	t.Run("output item added function call", func(t *testing.T) {
		event := "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"Read\",\"call_id\":\"call_1\"}}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected function_call output_item to be treated as non-text content")
		}
	})

	t.Run("completed event with function call output", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"function_call\",\"name\":\"Read\",\"call_id\":\"call_1\",\"arguments\":\"{}\"}]}}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected completed event with function_call output to be treated as non-text content")
		}
	})

	t.Run("reasoning item added", func(t *testing.T) {
		event := "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_1\",\"status\":\"in_progress\",\"summary\":[]}}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected reasoning output_item to be treated as semantic content")
		}
	})

	t.Run("compaction item done", func(t *testing.T) {
		event := "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"encrypted_content\":\"summary payload\"}}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected compaction output_item to be treated as semantic content")
		}
	})

	t.Run("empty compaction item done", func(t *testing.T) {
		event := "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\"}}\n\n"
		if common.HasResponsesSemanticContent(event) {
			t.Fatal("did not expect compaction output_item without encrypted_content to be treated as semantic content")
		}
	})

	t.Run("plain empty completed", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"
		if common.HasResponsesSemanticContent(event) {
			t.Fatal("did not expect empty completed event to be treated as non-text content")
		}
	})

	t.Run("completed event with compaction output", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"compaction\",\"encrypted_content\":\"summary payload\"}]}}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected completed event with compaction output to be treated as semantic content")
		}
	})

	t.Run("unknown event type with semantic item", func(t *testing.T) {
		event := "event: response.web_search_item.added\ndata: {\"type\":\"response.web_search_item.added\",\"item\":{\"type\":\"web_search_call\",\"id\":\"ws_1\",\"status\":\"in_progress\"}}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected unknown event carrying _call item to be treated as semantic content")
		}
	})

	t.Run("unknown event type with call_id", func(t *testing.T) {
		event := "event: response.custom_tool.invoked\ndata: {\"type\":\"response.custom_tool.invoked\",\"call_id\":\"call_9\"}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected unknown event carrying call_id to be treated as semantic content")
		}
	})

	t.Run("unknown event type without content", func(t *testing.T) {
		event := "event: response.heartbeat\ndata: {\"type\":\"response.heartbeat\"}\n\n"
		if common.HasResponsesSemanticContent(event) {
			t.Fatal("did not expect content-free unknown event to be treated as semantic content")
		}
	})

	t.Run("output item done with _output suffix", func(t *testing.T) {
		event := "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"web_search_output\",\"id\":\"wso_1\"}}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected _output suffixed item to be treated as semantic content")
		}
	})

	t.Run("content part added output text", func(t *testing.T) {
		event := "event: response.content_part.added\ndata: {\"type\":\"response.content_part.added\",\"part\":{\"type\":\"output_text\",\"text\":\"part text\"}}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected content_part.added output_text to be treated as semantic content")
		}
	})

	t.Run("reasoning_text delta (OpenRouter non-standard)", func(t *testing.T) {
		event := "event: response.reasoning_text.delta\ndata: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"thinking...\",\"content_index\":0}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected reasoning_text.delta to be treated as semantic content")
		}
	})

	t.Run("reasoning_text done (OpenRouter non-standard)", func(t *testing.T) {
		event := "event: response.reasoning_text.done\ndata: {\"type\":\"response.reasoning_text.done\",\"content_index\":0}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected reasoning_text.done to be treated as semantic content")
		}
	})

	t.Run("reasoning_summary_text delta (standard)", func(t *testing.T) {
		event := "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking...\",\"summary_index\":0}\n\n"
		if !common.HasResponsesSemanticContent(event) {
			t.Fatal("expected reasoning_summary_text.delta to be treated as semantic content")
		}
	})
}

func TestExtractResponsesTextFromEventUnknownTypes(t *testing.T) {
	cases := []struct {
		name  string
		event string
		want  string
	}{
		{
			name:  "unknown delta type with delta field",
			event: "event: response.web_search_text.delta\ndata: {\"type\":\"response.web_search_text.delta\",\"delta\":\"search result\"}\n\n",
			want:  "search result",
		},
		{
			name:  "unknown done type with text field",
			event: "event: response.custom_summary.done\ndata: {\"type\":\"response.custom_summary.done\",\"text\":\"summary text\"}\n\n",
			want:  "summary text",
		},
		{
			name:  "output text done",
			event: "event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"text\":\"final text\"}\n\n",
			want:  "final text",
		},
		{
			name:  "content part done",
			event: "event: response.content_part.done\ndata: {\"type\":\"response.content_part.done\",\"part\":{\"type\":\"output_text\",\"text\":\"part text\"}}\n\n",
			want:  "part text",
		},
		{
			name:  "completed output text",
			event: "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"completed text\"}]}]}}\n\n",
			want:  "completed text",
		},
		{
			name:  "unknown non delta/done type ignored",
			event: "event: response.heartbeat\ndata: {\"type\":\"response.heartbeat\",\"text\":\"should not extract\"}\n\n",
			want:  "",
		},
		{
			name:  "non response prefix ignored",
			event: "event: custom.delta\ndata: {\"type\":\"custom.delta\",\"delta\":\"should not extract\"}\n\n",
			want:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			extractResponsesTextFromEvent(tc.event, &buf)
			if got := buf.String(); got != tc.want {
				t.Fatalf("extractResponsesTextFromEvent() buf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildResponsesPreflightRawLog(t *testing.T) {
	raw := buildResponsesPreflightRawLog([]string{
		"event: response.created",
		"data: {\"type\":\"response.created\"}\n",
	})
	if !strings.Contains(raw, "event: response.created\n") {
		t.Fatalf("raw log should preserve event line with newline, got %q", raw)
	}
	if !strings.Contains(raw, "data: {\"type\":\"response.created\"}\n") {
		t.Fatalf("raw log should preserve data line, got %q", raw)
	}
}

func TestIsResponsesEmptyContent(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		empty bool
	}{
		{name: "empty string", text: "", empty: true},
		{name: "opening brace only", text: "{", empty: true},
		{name: "whitespace brace", text: "  {  ", empty: true},
		{name: "json body", text: "{\"path\":\"/tmp/x\"}", empty: false},
		{name: "plain text", text: "hello", empty: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := common.IsEffectivelyEmptyStreamText(tc.text); got != tc.empty {
				t.Fatalf("IsEffectivelyEmptyStreamText(%q) = %v, want %v", tc.text, got, tc.empty)
			}
		})
	}
}

func TestBuildResponsesPreflightDiagnostic(t *testing.T) {
	if got := buildResponsesPreflightDiagnostic(false, false, false, false, "", ""); got == "" {
		t.Fatal("expected diagnostic for no-event case")
	}
	if got := buildResponsesPreflightDiagnostic(true, true, false, false, "", ""); got == "" {
		t.Fatal("expected diagnostic for completed-empty case")
	}
	if got := buildResponsesPreflightDiagnostic(true, false, false, true, "response.custom.delta", ""); got == "" || got == "收到了未识别的 Responses 事件类型，但没有文本或语义内容" {
		t.Fatal("expected diagnostic to mention unknown responses event type")
	}
}

func TestFirstUnknownResponsesEventType_AllowsResponsesLifecycleAndErrorTypes(t *testing.T) {
	eventTypes := []string{
		"response.created",
		"response.in_progress",
		"response.content_part.added",
		"response.content_part.done",
		"keepalive",
		"response.error",
		"response.failed",
		"error",
	}
	for _, eventType := range eventTypes {
		t.Run(eventType, func(t *testing.T) {
			event := "event: " + eventType + "\ndata: {\"type\":\"" + eventType + "\"}\n\n"
			if got, ok := firstUnknownResponsesEventType(event); ok {
				t.Fatalf("firstUnknownResponsesEventType() = %q, true; want known type", got)
			}
		})
	}
}

// 标准 OpenAI 流中高频出现的 content_part 与 done 系事件不应被判为未知类型，
// 否则空流诊断文案会被 seenUnknown 分支抢占（knownTypes 缺口回归测试）。
func TestFirstUnknownResponsesEventType_AllowsStandardContentPartAndDoneEvents(t *testing.T) {
	eventTypes := []string{
		"response.content_part.added",
		"response.content_part.delta",
		"response.content_part.done",
		"response.output_text.delta",
		"response.output_text.done",
		"response.output_json.done",
		"response.refusal.delta",
		"response.refusal.done",
		"response.audio.done",
		"response.audio_transcript.done",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.reasoning_text.delta",
		"response.reasoning_text.done",
		"response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done",
	}
	for _, eventType := range eventTypes {
		t.Run(eventType, func(t *testing.T) {
			event := "data: {\"type\":\"" + eventType + "\",\"output_index\":0}\n"
			if got, ok := firstUnknownResponsesEventType(event); ok {
				t.Fatalf("firstUnknownResponsesEventType() = %q, true; want known type", got)
			}
		})
	}
}
