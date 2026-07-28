package providers

import (
	"encoding/json"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestStripCodexClientOnlyTools(t *testing.T) {
	t.Run("剥离字符串简写并保留 function 对象", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				"exec_command",
				"apply_patch",
				map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name": "lookup_user",
					},
				},
			},
			"tool_choice":         "auto",
			"parallel_tool_calls": true,
		}
		stripCodexClientOnlyTools(req)

		tools, ok := req["tools"].([]interface{})
		if !ok {
			t.Fatalf("tools 被误删，期望保留 function 条目")
		}
		if len(tools) != 1 {
			t.Fatalf("tools 长度=%d，期望 1", len(tools))
		}
		if _, ok := tools[0].(map[string]interface{}); !ok {
			t.Fatalf("剩余条目类型错误: %T", tools[0])
		}
		if req["tool_choice"] != "auto" {
			t.Fatalf("tool_choice 不应被删除")
		}
	})

	t.Run("全部剥离时同步清理 tool_choice 与 parallel_tool_calls", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				"exec_command",
				map[string]interface{}{"type": "namespace", "name": "mcp__chrome_devtools__"},
				map[string]interface{}{"type": "custom", "name": "apply_patch"},
			},
			"tool_choice":         "auto",
			"parallel_tool_calls": true,
		}
		stripCodexClientOnlyTools(req)

		if _, ok := req["tools"]; ok {
			t.Fatalf("tools 应当被删除")
		}
		if _, ok := req["tool_choice"]; ok {
			t.Fatalf("tool_choice 应当被删除")
		}
		if _, ok := req["parallel_tool_calls"]; ok {
			t.Fatalf("parallel_tool_calls 应当被删除")
		}
	})

	t.Run("web_search 工具应被保留（Codex v0.139.0+ 官方支持）", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "web_search"},
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
			},
			"tool_choice": "auto",
		}
		stripCodexClientOnlyTools(req)

		tools, ok := req["tools"].([]interface{})
		if !ok {
			t.Fatalf("tools 被误删")
		}
		if len(tools) != 2 {
			t.Fatalf("tools 长度=%d，期望 2（web_search + function）", len(tools))
		}
		if req["tool_choice"] != "auto" {
			t.Fatalf("tool_choice 不应被删除")
		}
	})

	t.Run("剥离 tool_search 并保留普通 function", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{
					"type":        "tool_search",
					"execution":   "client",
					"description": "Search deferred tools",
					"parameters":  map[string]interface{}{"type": "object"},
				},
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
			},
			"tool_choice": map[string]interface{}{"type": "tool_search", "name": "tool_search"},
		}
		stripCodexClientOnlyTools(req)

		tools, ok := req["tools"].([]interface{})
		if !ok {
			t.Fatalf("tools 被误删")
		}
		if len(tools) != 1 {
			t.Fatalf("tools 长度=%d，期望 1", len(tools))
		}
		if got := tools[0].(map[string]interface{})["type"]; got != "function" {
			t.Fatalf("保留工具类型=%v，期望 function", got)
		}
		if req["tool_choice"] != "auto" {
			t.Fatalf("tool_choice=%v，期望 auto", req["tool_choice"])
		}
	})

	t.Run("未知对象类型保守保留", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "something_new"},
			},
		}
		stripCodexClientOnlyTools(req)
		tools, ok := req["tools"].([]interface{})
		if !ok || len(tools) != 1 {
			t.Fatalf("未知类型应被保留，当前=%v", req["tools"])
		}
	})

	t.Run("无 tools 字段不报错", func(t *testing.T) {
		req := map[string]interface{}{"model": "gpt-5.5"}
		stripCodexClientOnlyTools(req)
		if _, ok := req["tools"]; ok {
			t.Fatalf("不应注入 tools")
		}
	})

	t.Run("部分剥离时修正指向已删除工具的 tool_choice", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "custom", "name": "apply_patch"},
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
			},
			"tool_choice": map[string]interface{}{"type": "custom", "name": "apply_patch"},
		}
		stripCodexClientOnlyTools(req)

		if req["tool_choice"] != "auto" {
			t.Fatalf("tool_choice=%v，期望 auto", req["tool_choice"])
		}
	})

	t.Run("部分剥离时保留仍有效的 tool_choice", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "custom", "name": "apply_patch"},
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
			},
			"tool_choice": map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
		}
		stripCodexClientOnlyTools(req)

		choice, ok := req["tool_choice"].(map[string]interface{})
		if !ok {
			t.Fatalf("tool_choice 应保持对象，当前=%v", req["tool_choice"])
		}
		if extractToolChoiceName(choice) != "lookup_user" {
			t.Fatalf("tool_choice 指向错误: %v", choice)
		}
	})
}

func TestStripCodexClientOnlyToolsFromBody(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","tools":["exec_command",{"type":"namespace","name":"mcp__chrome_devtools__"},{"type":"function","function":{"name":"lookup_user","parameters":{"type":"object","properties":{}}}}],"tool_choice":"auto"}`)
	updated := stripCodexClientOnlyToolsFromBody(body)

	var req map[string]interface{}
	if err := json.Unmarshal(updated, &req); err != nil {
		t.Fatalf("剥离后的 body 应保持合法 JSON: %v", err)
	}
	tools, ok := req["tools"].([]interface{})
	if !ok {
		t.Fatalf("应保留 function 工具，当前=%v", req["tools"])
	}
	if len(tools) != 1 {
		t.Fatalf("tools 长度=%d，期望 1", len(tools))
	}
	if req["tool_choice"] != "auto" {
		t.Fatalf("仍有 function 工具时不应删除 tool_choice")
	}
}

func TestNormalizeResponsesInputForPassthrough_StatelessToolHistory(t *testing.T) {
	req := map[string]interface{}{
		"input": []interface{}{
			map[string]interface{}{
				"type":      "function_call",
				"name":      "exec_command",
				"call_id":   "call_123",
				"arguments": `{"cmd":"pwd"}{"cmd":"ls"}`,
				"status":    "completed",
			},
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "call_123",
				"output":  "failed to parse function arguments: trailing characters",
			},
		},
	}

	normalizeResponsesInputForPassthrough(req)

	input := req["input"].([]interface{})
	if len(input) != 2 {
		t.Fatalf("input 长度=%d，期望 2", len(input))
	}
	// 配对的 function_call/function_call_output 应保留原样
	item0 := input[0].(map[string]interface{})
	if item0["type"] != "function_call" {
		t.Fatalf("配对的 function_call 应保留原样，当前=%v", item0["type"])
	}
	if _, ok := item0["status"]; ok {
		t.Fatalf("input[0] 不应保留 status")
	}
	item1 := input[1].(map[string]interface{})
	if item1["type"] != "function_call_output" {
		t.Fatalf("配对的 function_call_output 应保留原样，当前=%v", item1["type"])
	}
}

func TestNormalizeResponsesInputForPassthrough_OrphanedToolOutput(t *testing.T) {
	req := map[string]interface{}{
		"input": []interface{}{
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "orphan_call_456",
				"output":  "some stale output",
			},
		},
	}

	normalizeResponsesInputForPassthrough(req)

	input := req["input"].([]interface{})
	if len(input) != 1 {
		t.Fatalf("input 长度=%d，期望 1", len(input))
	}
	item := input[0].(map[string]interface{})
	if item["type"] != "message" {
		t.Fatalf("孤立 function_call_output 应降级为 message，当前=%v", item["type"])
	}
	if item["role"] != "user" {
		t.Fatalf("孤立 function_call_output 应降级为 user message，当前=%v", item["role"])
	}
}

func TestNormalizeResponsesInputForPassthrough_EmptyCallId(t *testing.T) {
	req := map[string]interface{}{
		"input": []interface{}{
			map[string]interface{}{
				"type":      "function_call",
				"name":      "exec_command",
				"call_id":   "",
				"arguments": `{}`,
			},
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "",
				"output":  "some output",
			},
		},
	}

	normalizeResponsesInputForPassthrough(req)

	input := req["input"].([]interface{})
	item0 := input[0].(map[string]interface{})
	if item0["type"] != "function_call" {
		t.Fatalf("empty call_id 的 function_call 仍应保留，当前=%v", item0["type"])
	}
	item1 := input[1].(map[string]interface{})
	if item1["type"] != "message" {
		t.Fatalf("empty call_id 对应的 output 应被判定为孤立并降级，当前=%v", item1["type"])
	}
}

func TestNormalizeResponsesInputForPassthrough_OutputBeforeCall(t *testing.T) {
	req := map[string]interface{}{
		"input": []interface{}{
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "call_abc",
				"output":  "result",
			},
			map[string]interface{}{
				"type":      "function_call",
				"name":      "exec_command",
				"call_id":   "call_abc",
				"arguments": `{}`,
			},
		},
	}

	normalizeResponsesInputForPassthrough(req)

	input := req["input"].([]interface{})
	item0 := input[0].(map[string]interface{})
	if item0["type"] != "function_call_output" {
		t.Fatalf("顺序颠倒但配对的 output 应保留，当前=%v", item0["type"])
	}
	item1 := input[1].(map[string]interface{})
	if item1["type"] != "function_call" {
		t.Fatalf("顺序颠倒的 function_call 应保留，当前=%v", item1["type"])
	}
}

func TestNormalizeResponsesInputForPassthrough_NonMapItems(t *testing.T) {
	req := map[string]interface{}{
		"input": []interface{}{
			"plain string item",
			42.0,
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "orphan",
				"output":  "stale",
			},
			true,
		},
	}

	normalizeResponsesInputForPassthrough(req)

	input := req["input"].([]interface{})
	if len(input) != 4 {
		t.Fatalf("input 长度=%d，期望 4", len(input))
	}
	if input[0] != "plain string item" {
		t.Fatalf("字符串条目应原样保留，当前=%v", input[0])
	}
	if input[1] != 42.0 {
		t.Fatalf("数值条目应原样保留，当前=%v", input[1])
	}
	if input[3] != true {
		t.Fatalf("布尔条目应原样保留，当前=%v", input[3])
	}
	// 孤立 output 仍应降级
	item2 := input[2].(map[string]interface{})
	if item2["type"] != "message" {
		t.Fatalf("孤立 output 应降级，当前=%v", item2["type"])
	}
}

func TestNormalizeResponsesInputForPassthrough_MultipleToolsPartiallyOrphaned(t *testing.T) {
	req := map[string]interface{}{
		"input": []interface{}{
			// pair A: 有对应 output
			map[string]interface{}{
				"type":      "function_call",
				"name":      "tool_a",
				"call_id":   "call_a",
				"arguments": `{}`,
			},
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "call_a",
				"output":  "result_a",
			},
			// pair B: 孤立 output（无对应 function_call）
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "call_b",
				"output":  "stale_b",
			},
			// pair C: 有对应 output
			map[string]interface{}{
				"type":      "function_call",
				"name":      "tool_c",
				"call_id":   "call_c",
				"arguments": `{}`,
			},
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "call_c",
				"output":  "result_c",
			},
		},
	}

	normalizeResponsesInputForPassthrough(req)

	input := req["input"].([]interface{})
	if len(input) != 5 {
		t.Fatalf("input 长度=%d，期望 5", len(input))
	}
	// pair A: 保留
	if input[0].(map[string]interface{})["type"] != "function_call" {
		t.Fatalf("pair A function_call 应保留")
	}
	if input[1].(map[string]interface{})["type"] != "function_call_output" {
		t.Fatalf("pair A output 应保留")
	}
	// pair B: 孤立，降级
	if input[2].(map[string]interface{})["type"] != "message" {
		t.Fatalf("pair B 孤立 output 应降级为 message，当前=%v", input[2].(map[string]interface{})["type"])
	}
	// pair C: 保留
	if input[3].(map[string]interface{})["type"] != "function_call" {
		t.Fatalf("pair C function_call 应保留")
	}
	if input[4].(map[string]interface{})["type"] != "function_call_output" {
		t.Fatalf("pair C output 应保留")
	}
}

func TestNormalizeResponsesInputForPassthrough_PreservesStatefulToolOutput(t *testing.T) {
	req := map[string]interface{}{
		"previous_response_id": "resp_123",
		"input": []interface{}{
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "call_123",
				"output":  "ok",
			},
		},
	}

	normalizeResponsesInputForPassthrough(req)

	input := req["input"].([]interface{})
	item := input[0].(map[string]interface{})
	if item["type"] != "function_call_output" {
		t.Fatalf("有 previous_response_id 时应保留 function_call_output，当前=%v", item)
	}
}

func TestNormalizeResponsesInputForPassthrough_StripsAdditionalTools(t *testing.T) {
	req := map[string]interface{}{
		"input": []interface{}{
			map[string]interface{}{
				"type": "message",
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "input_text", "text": "hello"},
				},
				"additional_tools": []interface{}{
					map[string]interface{}{
						"type":      "tool_search",
						"execution": "client",
					},
				},
			},
		},
	}

	normalizeResponsesInputForPassthrough(req)

	input := req["input"].([]interface{})
	item := input[0].(map[string]interface{})
	if _, ok := item["additional_tools"]; ok {
		t.Fatalf("additional_tools 应在 passthrough normalization 中被剥离: %#v", item)
	}
}

func TestConvertCodexToolsForPassthrough(t *testing.T) {
	t.Run("转换 custom apply_patch 为 5 个 function 工具", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{
					"type":        "custom",
					"name":        "apply_patch",
					"description": "Apply a patch",
					"schema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"patch": map[string]interface{}{"type": "string"},
						},
					},
				},
				map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name": "keep_me",
						"parameters": map[string]interface{}{
							"type":       "object",
							"properties": map[string]interface{}{},
						},
					},
				},
			},
			"tool_choice": "auto",
		}
		convertCodexToolsForPassthrough(req)

		tools, ok := req["tools"].([]interface{})
		if !ok {
			t.Fatalf("tools 应存在")
		}
		// apply_patch 拆为 5 个 + keep_me = 6
		if len(tools) != 6 {
			names := []string{}
			for _, tool := range tools {
				if m, ok := tool.(map[string]interface{}); ok {
					if fn, ok := m["function"].(map[string]interface{}); ok {
						names = append(names, fn["name"].(string))
					}
				}
			}
			t.Fatalf("tools 长度=%d，期望 6，工具名=%v", len(tools), names)
		}
	})

	t.Run("转换 namespace 工具为 function", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{
					"type": "namespace",
					"name": "mcp__server__",
					"tools": []interface{}{
						map[string]interface{}{
							"type":        "function",
							"name":        "list_files",
							"description": "List files",
							"parameters": map[string]interface{}{
								"type":       "object",
								"properties": map[string]interface{}{},
							},
						},
					},
				},
			},
		}
		convertCodexToolsForPassthrough(req)

		tools, ok := req["tools"].([]interface{})
		if !ok || len(tools) == 0 {
			t.Fatalf("namespace 工具应被转换，当前=%v", req["tools"])
		}
		first := tools[0].(map[string]interface{})
		fn := first["function"].(map[string]interface{})
		name := fn["name"].(string)
		if name != "mcp__server__list_files" {
			t.Fatalf("namespace 函数名=%s，期望 mcp__server__list_files", name)
		}
	})

	t.Run("转换 web_search/local_shell/computer_use 为 generic function", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "web_search", "name": "web"},
				map[string]interface{}{"type": "local_shell", "name": "shell"},
				map[string]interface{}{"type": "computer_use", "name": "cu"},
			},
		}
		convertCodexToolsForPassthrough(req)

		tools, ok := req["tools"].([]interface{})
		if !ok || len(tools) != 3 {
			t.Fatalf("应转换为 3 个 function，当前=%v", req["tools"])
		}
		for i, tool := range tools {
			m := tool.(map[string]interface{})
			if m["type"] != "function" {
				t.Fatalf("tools[%d] type=%v，期望 function", i, m["type"])
			}
		}
	})

	t.Run("无可转换工具时不修改", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name":       "lookup",
						"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
					},
				},
			},
			"tool_choice": "auto",
		}
		convertCodexToolsForPassthrough(req)

		tools := req["tools"].([]interface{})
		if len(tools) != 1 {
			t.Fatalf("纯 function 工具不应被修改，长度=%d", len(tools))
		}
	})

	t.Run("空 tools 不报错", func(t *testing.T) {
		req := map[string]interface{}{"model": "test"}
		convertCodexToolsForPassthrough(req)
		if _, ok := req["tools"]; ok {
			t.Fatalf("不应注入 tools")
		}
	})
}

func TestStripImageGenerationFromTools(t *testing.T) {
	t.Run("开关关闭时 image_generation 保留", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "image_generation", "output_format": "png"},
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
			},
		}
		// 不调用 stripImageGenerationFromTools，验证原始数据不变
		tools := req["tools"].([]interface{})
		if len(tools) != 2 {
			t.Fatalf("期望 2 个工具，实际 %d", len(tools))
		}
	})

	t.Run("开启后剥离 image_generation 对象", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "image_generation", "output_format": "png"},
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
			},
			"tool_choice":         "auto",
			"parallel_tool_calls": true,
		}
		stripImageGenerationFromTools(req)

		tools, ok := req["tools"].([]interface{})
		if !ok {
			t.Fatal("tools 被误删")
		}
		if len(tools) != 1 {
			t.Fatalf("tools 长度=%d，期望 1", len(tools))
		}
	})

	t.Run("开启后剥离 image_generation 字符串简写", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				"image_generation",
				"exec_command",
			},
		}
		stripImageGenerationFromTools(req)

		tools, ok := req["tools"].([]interface{})
		if !ok {
			t.Fatal("tools 被误删")
		}
		if len(tools) != 1 {
			t.Fatalf("tools 长度=%d，期望 1", len(tools))
		}
	})

	t.Run("开启后剥离 Codex image_gen namespace", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{
					"type": "namespace",
					"name": "image_gen",
					"tools": []interface{}{
						map[string]interface{}{"type": "function", "name": "imagegen"},
					},
				},
				map[string]interface{}{"type": "namespace", "name": "multi_agent_v2"},
			},
		}
		stripImageGenerationFromTools(req)

		tools, ok := req["tools"].([]interface{})
		if !ok || len(tools) != 1 {
			t.Fatalf("tools = %#v，期望仅保留非生图 namespace", req["tools"])
		}
		if got := tools[0].(map[string]interface{})["name"]; got != "multi_agent_v2" {
			t.Fatalf("保留的 namespace = %v", got)
		}
	})

	t.Run("开启后剥离 namespace 转换后的扁平 function", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{
					"type": "namespace",
					"name": "image_gen",
					"tools": []interface{}{
						map[string]interface{}{"type": "function", "name": "imagegen", "parameters": map[string]interface{}{"type": "object"}},
					},
				},
				map[string]interface{}{"type": "function", "name": "lookup_user", "parameters": map[string]interface{}{"type": "object"}},
			},
		}
		convertCodexToolsForPassthrough(req)
		stripImageGenerationFromTools(req)

		tools, ok := req["tools"].([]interface{})
		if !ok || len(tools) != 1 {
			t.Fatalf("tools = %#v，期望仅保留普通 function", req["tools"])
		}
		function := tools[0].(map[string]interface{})["function"].(map[string]interface{})
		if got := function["name"]; got != "lookup_user" {
			t.Fatalf("保留的 function = %v", got)
		}
	})

	t.Run("全部剥离后清理 tools/tool_choice/parallel_tool_calls", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "image_generation", "output_format": "png"},
			},
			"tool_choice":         "auto",
			"parallel_tool_calls": true,
		}
		stripImageGenerationFromTools(req)

		if _, ok := req["tools"]; ok {
			t.Fatal("tools 应被删除")
		}
		if _, ok := req["tool_choice"]; ok {
			t.Fatal("tool_choice 应被删除")
		}
		if _, ok := req["parallel_tool_calls"]; ok {
			t.Fatal("parallel_tool_calls 应被删除")
		}
	})

	t.Run("无 image_generation 时不修改", func(t *testing.T) {
		req := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
			},
			"tool_choice": "auto",
		}
		stripImageGenerationFromTools(req)

		tools, ok := req["tools"].([]interface{})
		if !ok || len(tools) != 1 {
			t.Fatalf("tools 不应被修改")
		}
		if req["tool_choice"] != "auto" {
			t.Fatal("tool_choice 不应被修改")
		}
	})

	t.Run("无 tools 字段不报错", func(t *testing.T) {
		req := map[string]interface{}{"model": "gpt-5.5"}
		stripImageGenerationFromTools(req)
		if _, ok := req["tools"]; ok {
			t.Fatal("不应注入 tools")
		}
	})
}

func TestResponsesProviderCodexToolCompatDiffersFromNativePassthroughForOpenAI(t *testing.T) {
	t.Run("OpenAI Chat 转换只由 codexToolCompat 启用 Codex proxy", func(t *testing.T) {
		compat := true
		upstream := &config.UpstreamConfig{
			ServiceType:     "openai",
			CodexToolCompat: &compat,
		}
		upstream.SetLearnedCompatTrait(config.TraitCodexNativeToolPassthrough, false)
		upstream.SetLearnedCompatTrait(config.TraitStripImageGenTool, false)
		upstream.SetLearnedCompatTrait(config.TraitNormalizeNonstandardChatRoles, false)
		body := []byte(`{
			"model": "gpt-5.5",
			"input": "search",
			"tools": [
				{"type": "tool_search", "execution": "client", "description": "Search tools", "parameters": {"type": "object", "properties": {}}},
				{"type": "function", "name": "do_thing", "parameters": {"type": "object", "properties": {}}}
			]
		}`)

		providerReq, _, err := (&ResponsesProvider{}).buildProviderRequestBody(nil, "/v1/responses", body, upstream)
		if err != nil {
			t.Fatalf("buildProviderRequestBody 失败: %v", err)
		}
		reqMap := providerReq.(map[string]interface{})
		tools := decodeToolMaps(t, reqMap["tools"])
		if len(tools) != 2 {
			t.Fatalf("工具数量=%d，期望 2", len(tools))
		}
		if got := tools[0]["function"].(map[string]interface{})["name"]; got != "tool_search" {
			t.Fatalf("第一个工具=%v，期望 tool_search", got)
		}
	})

	t.Run("codexNativeToolPassthrough 不等价于 Chat 转换兼容", func(t *testing.T) {
		compat := false
		upstream := &config.UpstreamConfig{
			ServiceType:     "openai",
			CodexToolCompat: &compat,
		}
		upstream.SetLearnedCompatTrait(config.TraitCodexNativeToolPassthrough, true)
		body := []byte(`{
			"model": "gpt-5.5",
			"input": "search",
			"tools": [
				{"type": "tool_search", "execution": "client", "description": "Search tools", "parameters": {"type": "object", "properties": {}}},
				{"type": "function", "name": "do_thing", "parameters": {"type": "object", "properties": {}}}
			]
		}`)

		providerReq, _, err := (&ResponsesProvider{}).buildProviderRequestBody(nil, "/v1/responses", body, upstream)
		if err != nil {
			t.Fatalf("buildProviderRequestBody 失败: %v", err)
		}
		reqMap := providerReq.(map[string]interface{})
		tools := decodeToolMaps(t, reqMap["tools"])
		if len(tools) != 1 {
			t.Fatalf("工具数量=%d，期望 1；codexNativeToolPassthrough 不应启用 Chat proxy", len(tools))
		}
		if got := tools[0]["function"].(map[string]interface{})["name"]; got != "do_thing" {
			t.Fatalf("保留工具=%v，期望 do_thing", got)
		}
	})
}

func TestResponsesProviderPassthroughCodexCompatStripsToolSearch(t *testing.T) {
	compat := true
	upstream := &config.UpstreamConfig{
		ServiceType:     "responses",
		CodexToolCompat: &compat,
	}
	upstream.SetLearnedCompatTrait(config.TraitCodexNativeToolPassthrough, false)
	body := []byte(`{
		"model": "gpt-5.5",
		"input": "search",
		"tools": [
			{"type": "tool_search", "execution": "client", "description": "Search tools", "parameters": {"type": "object", "properties": {}}},
			{"type": "function", "name": "do_thing", "parameters": {"type": "object", "properties": {}}}
		],
		"tool_choice": {"type": "tool_search", "name": "tool_search"}
	}`)

	providerReq, _, err := (&ResponsesProvider{}).buildProviderRequestBody(nil, "/v1/responses", body, upstream)
	if err != nil {
		t.Fatalf("buildProviderRequestBody 失败: %v", err)
	}
	reqMap := providerReq.(map[string]interface{})
	tools := decodeToolMaps(t, reqMap["tools"])
	if len(tools) != 1 {
		t.Fatalf("工具数量=%d，期望 1", len(tools))
	}
	if got := tools[0]["name"]; got != "do_thing" {
		t.Fatalf("保留工具=%v，期望 do_thing", got)
	}
	if reqMap["tool_choice"] != "auto" {
		t.Fatalf("tool_choice=%v，期望 auto", reqMap["tool_choice"])
	}
}

func decodeToolMaps(t *testing.T, raw interface{}) []map[string]interface{} {
	t.Helper()

	if tools, ok := raw.([]map[string]interface{}); ok {
		return tools
	}

	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("tools marshal err: %v", err)
	}
	var tools []map[string]interface{}
	if err := json.Unmarshal(b, &tools); err != nil {
		t.Fatalf("tools decode err: %v", err)
	}
	return tools
}
