package converters

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertResponsesToOpenAIChatRequest(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		model    string
		stream   bool
		validate func(t *testing.T, result []byte)
	}{
		{
			name:  "关闭推理别名转换为 none",
			input: `{"model":"gpt-5","input":"hello","reasoning":{"effort":"off"}}`,
			model: "gpt-5",
			validate: func(t *testing.T, result []byte) {
				if got := gjson.GetBytes(result, "reasoning_effort").String(); got != "none" {
					t.Fatalf("reasoning_effort = %q, want none", got)
				}
			},
		},
		{
			name: "基本文本输入",
			input: `{
				"model": "gpt-4",
				"input": "Hello, world!",
				"instructions": "You are a helpful assistant."
			}`,
			model:  "gpt-4o",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				if root.Get("model").String() != "gpt-4o" {
					t.Errorf("model should be gpt-4o, got %s", root.Get("model").String())
				}
				if root.Get("stream").Bool() != false {
					t.Error("stream should be false")
				}
				messages := root.Get("messages").Array()
				if len(messages) != 2 {
					t.Errorf("should have 2 messages (system + user), got %d", len(messages))
				}
				if messages[0].Get("role").String() != "system" {
					t.Error("first message should be system")
				}
				if messages[1].Get("role").String() != "user" {
					t.Error("second message should be user")
				}
			},
		},
		{
			name: "带 tools 的请求",
			input: `{
				"model": "gpt-4",
				"input": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "What's the weather?"}]}],
				"tools": [
					{
						"name": "get_weather",
						"description": "Get weather info",
						"parameters": {"type": "object", "properties": {"location": {"type": "string"}}}
					}
				]
			}`,
			model:  "gpt-4o",
			stream: true,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				if root.Get("stream").Bool() != true {
					t.Error("stream should be true")
				}
				tools := root.Get("tools").Array()
				if len(tools) != 1 {
					t.Errorf("should have 1 tool, got %d", len(tools))
				}
				if tools[0].Get("function.name").String() != "get_weather" {
					t.Error("tool name should be get_weather")
				}
			},
		},
		{
			name: "Codex 兼容开启时转换 tool_search",
			input: `{
				"model": "gpt-4",
				"input": "Find a tool",
				"transformer_metadata": {"codex_tool_compat_enabled": true},
				"tools": [
					{
						"type": "tool_search",
						"execution": "client",
						"description": "Search deferred tools",
						"parameters": {
							"type": "object",
							"properties": {
								"query": {"type": "string"}
							},
							"required": ["query"]
						}
					},
					{
						"type": "function",
						"name": "get_weather",
						"parameters": {"type": "object", "properties": {}}
					}
				]
			}`,
			model:  "gpt-4o",
			stream: true,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				tools := root.Get("tools").Array()
				if len(tools) != 2 {
					t.Fatalf("should have 2 tools, got %d: %s", len(tools), root.Get("tools").Raw)
				}
				if tools[0].Get("function.name").String() != "tool_search" {
					t.Fatalf("first tool should be tool_search, got %s", tools[0].Raw)
				}
				if !tools[0].Get("function.parameters.properties.query").Exists() {
					t.Fatalf("tool_search should preserve query schema, got %s", tools[0].Raw)
				}
				if tools[0].Get("function.parameters.properties.input").Exists() {
					t.Fatalf("tool_search should not use generic input proxy schema, got %s", tools[0].Raw)
				}
				if tools[1].Get("function.name").String() != "get_weather" {
					t.Fatalf("second tool should be get_weather, got %s", tools[1].Raw)
				}
			},
		},
		{
			name: "Codex 兼容开启时保留懒加载子代理函数",
			input: `{
				"model": "gpt-4",
				"input": "Try a sub-agent",
				"transformer_metadata": {"codex_tool_compat_enabled": true},
				"tools": [
					{
						"type": "tool_search",
						"execution": "client",
						"description": "Search deferred tools",
						"parameters": {
							"type": "object",
							"properties": {
								"query": {"type": "string"}
							},
							"required": ["query"]
						}
					},
					{
						"type": "function",
						"name": "spawn_agent",
						"description": "Spawn and manage sub-agents.",
						"parameters": {
							"type": "object",
							"properties": {
								"agent_type": {"type": "string"},
								"message": {"type": "string"}
							},
							"required": ["agent_type", "message"]
						}
					},
					{
						"type": "function",
						"name": "wait_agent",
						"description": "Wait for sub-agent completion.",
						"parameters": {
							"type": "object",
							"properties": {
								"targets": {"type": "array", "items": {"type": "string"}},
								"timeout_ms": {"type": "integer"}
							},
							"required": ["targets"]
						}
					}
				]
			}`,
			model:  "gpt-4o",
			stream: true,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				tools := root.Get("tools").Array()
				if len(tools) != 3 {
					t.Fatalf("should have 3 tools, got %d: %s", len(tools), root.Get("tools").Raw)
				}
				names := map[string]bool{}
				for _, tool := range tools {
					if tool.Get("type").String() != "function" {
						t.Fatalf("chat tool should be function, got %s", tool.Raw)
					}
					names[tool.Get("function.name").String()] = true
				}
				for _, name := range []string{"tool_search", "spawn_agent", "wait_agent"} {
					if !names[name] {
						t.Fatalf("missing chat tool %q in %s", name, root.Get("tools").Raw)
					}
				}
				if !tools[1].Get("function.parameters.properties.agent_type").Exists() {
					t.Fatalf("spawn_agent schema should be preserved, got %s", tools[1].Raw)
				}
				if !tools[2].Get("function.parameters.properties.targets").Exists() {
					t.Fatalf("wait_agent schema should be preserved, got %s", tools[2].Raw)
				}
			},
		},
		{
			name: "function_call 和 function_call_output",
			input: `{
				"model": "gpt-4",
				"input": [
					{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "What's the weather in NYC?"}]},
					{"type": "function_call", "call_id": "call_123", "name": "get_weather", "arguments": "{\"location\": \"NYC\"}"},
					{"type": "function_call_output", "call_id": "call_123", "output": "Sunny, 72°F"}
				]
			}`,
			model:  "gpt-4o",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				messages := root.Get("messages").Array()
				if len(messages) != 3 {
					t.Errorf("should have 3 messages, got %d", len(messages))
				}
				// 第二条消息应该是 assistant with tool_calls
				if messages[1].Get("role").String() != "assistant" {
					t.Error("second message should be assistant")
				}
				if !messages[1].Get("tool_calls").Exists() {
					t.Error("assistant message should have tool_calls")
				}
				// 第三条消息应该是 tool
				if messages[2].Get("role").String() != "tool" {
					t.Error("third message should be tool")
				}
			},
		},
		{
			name: "多模态图片输入保留为 Chat content array",
			input: `{
				"model": "mimo-v2.5-pro",
				"input": [{"type": "message", "role": "user", "content": [
					{"type": "input_text", "text": "描述这张图片"},
					{"type": "input_image", "image_url": "data:image/png;base64,abc", "detail": "high"}
				]}]
			}`,
			model:  "mimo-v2.5-pro",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				content := root.Get("messages.0.content")
				if !content.IsArray() {
					t.Fatalf("content should be array, got %s", content.Raw)
				}
				if content.Get("0.type").String() != "text" || content.Get("0.text").String() != "描述这张图片" {
					t.Fatalf("text block mismatch: %s", content.Get("0").Raw)
				}
				if content.Get("1.type").String() != "image_url" {
					t.Fatalf("image block type mismatch: %s", content.Get("1").Raw)
				}
				if content.Get("1.image_url.url").String() != "data:image/png;base64,abc" {
					t.Fatalf("image url mismatch: %s", content.Get("1").Raw)
				}
				if content.Get("1.image_url.detail").String() != "high" {
					t.Fatalf("image detail mismatch: %s", content.Get("1").Raw)
				}
			},
		},
		{
			name: "tool_choice 无 tools 时不透传",
			input: `{
				"model": "gpt-4",
				"input": "Call a tool",
				"tool_choice": {"type": "function", "function": {"name": "get_weather"}}
			}`,
			model:  "gpt-4o",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				if root.Get("tool_choice").Exists() {
					t.Fatalf("tool_choice should not exist when tools are absent, got %s", root.Get("tool_choice").Raw)
				}
			},
		},

		{
			name: "tools 缺失 required 字段时自动补齐 []",
			input: `{
				"model": "gpt-5-codex",
				"input": "list mcp resources",
				"tools": [
					{
						"type": "function",
						"name": "list_mcp_resources",
						"description": "Lists resources provided by MCP servers.",
						"strict": false,
						"parameters": {
							"type": "object",
							"properties": {
								"cursor": {"type": "string"},
								"server": {"type": "string"}
							},
							"additionalProperties": false
						}
					}
				]
			}`,
			model:  "gpt-5-codex",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				tools := root.Get("tools").Array()
				if len(tools) != 1 {
					t.Fatalf("should have 1 tool, got %d", len(tools))
				}
				tool := tools[0]
				if tool.Get("function.name").String() != "list_mcp_resources" {
					t.Fatalf("tool name mismatch: %s", tool.Raw)
				}
				params := tool.Get("function.parameters")
				if params.Get("type").String() != "object" {
					t.Fatalf("parameters.type should be object: %s", params.Raw)
				}
				required := params.Get("required")
				if !required.Exists() || !required.IsArray() {
					t.Fatalf("parameters.required should exist and be array, got %s", params.Raw)
				}
				if params.Get("additionalProperties").Bool() != false {
					t.Fatalf("additionalProperties should be preserved: %s", params.Raw)
				}
			},
		},
		{
			name: "非 function 类型的工具应被跳过",
			input: `{
				"model": "gpt-5-codex",
				"input": "search the web",
				"tools": [
					{"type": "web_search"},
					{"type": "custom", "name": "grep"},
					{"type": "function", "name": "do_thing", "parameters": {"type": "object", "properties": {}}}
				]
			}`,
			model:  "gpt-5-codex",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				tools := root.Get("tools").Array()
				if len(tools) != 1 {
					t.Fatalf("expected 1 tool after filtering, got %d (%s)", len(tools), root.Get("tools").Raw)
				}
				if tools[0].Get("function.name").String() != "do_thing" {
					t.Fatalf("should keep only function tool, got %s", tools[0].Raw)
				}
			},
		},
		{
			name: "reasoning effort 转换",
			input: `{
				"model": "o1-mini",
				"input": "Think about this",
				"reasoning": {"effort": "high"}
			}`,
			model:  "o1-mini",
			stream: false,
			validate: func(t *testing.T, result []byte) {
				root := gjson.ParseBytes(result)
				if root.Get("reasoning_effort").String() != "high" {
					t.Errorf("reasoning_effort should be high, got %s", root.Get("reasoning_effort").String())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertResponsesToOpenAIChatRequest(tt.model, []byte(tt.input), tt.stream)
			tt.validate(t, result)
		})
	}
}

func TestConvertResponsesToOpenAIChatRequest_ImageSourceVariants(t *testing.T) {
	tests := []struct {
		name       string
		imageBlock string
		wantURL    string
		wantDetail string
	}{
		{
			name:       "base64 source",
			imageBlock: `{"type":"input_image","source":{"type":"base64","media_type":"image/png","data":"abc"},"detail":"high"}`,
			wantURL:    "data:image/png;base64,abc",
			wantDetail: "high",
		},
		{
			name:       "url source",
			imageBlock: `{"type":"input_image","source":{"type":"url","url":"https://example.com/a.png"},"detail":"low"}`,
			wantURL:    "https://example.com/a.png",
			wantDetail: "low",
		},
		{
			name:       "empty image_url falls back to source",
			imageBlock: `{"type":"input_image","image_url":"","source":{"type":"base64","media_type":"image/jpeg","data":"xyz"}}`,
			wantURL:    "data:image/jpeg;base64,xyz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := `{"model":"gpt-4o","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"describe"},` + tt.imageBlock + `]}]}`
			result := ConvertResponsesToOpenAIChatRequest("gpt-4o", []byte(input), false)
			content := gjson.ParseBytes(result).Get("messages.0.content")
			if !content.IsArray() {
				t.Fatalf("content should be array, got %s", content.Raw)
			}
			image := content.Get("1")
			if image.Get("type").String() != "image_url" {
				t.Fatalf("image block type mismatch: %s", image.Raw)
			}
			if got := image.Get("image_url.url").String(); got != tt.wantURL {
				t.Fatalf("image_url.url = %q, want %q; body=%s", got, tt.wantURL, result)
			}
			if got := image.Get("image_url.detail").String(); got != tt.wantDetail {
				t.Fatalf("image_url.detail = %q, want %q; body=%s", got, tt.wantDetail, result)
			}
		})
	}
}

func TestNormalizeResponsesImageURL_SourceVariants(t *testing.T) {
	tests := []struct {
		name       string
		block      map[string]interface{}
		wantURL    string
		wantDetail string
	}{
		{
			name: "base64 source",
			block: map[string]interface{}{
				"source": map[string]interface{}{"type": "base64", "media_type": "image/png", "data": "abc"},
				"detail": "high",
			},
			wantURL:    "data:image/png;base64,abc",
			wantDetail: "high",
		},
		{
			name: "url source",
			block: map[string]interface{}{
				"source": map[string]interface{}{"type": "url", "url": "https://example.com/a.png"},
			},
			wantURL: "https://example.com/a.png",
		},
		{
			name: "empty image_url falls back to source",
			block: map[string]interface{}{
				"image_url": "",
				"source":    map[string]interface{}{"type": "base64", "media_type": "image/jpeg", "data": "xyz"},
			},
			wantURL: "data:image/jpeg;base64,xyz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeResponsesImageURL(tt.block)
			if result == nil {
				t.Fatalf("normalizeResponsesImageURL returned nil")
			}
			if got := result["url"]; got != tt.wantURL {
				t.Fatalf("url = %v, want %s", got, tt.wantURL)
			}
			if tt.wantDetail != "" && result["detail"] != tt.wantDetail {
				t.Fatalf("detail = %v, want %s", result["detail"], tt.wantDetail)
			}
		})
	}
}
