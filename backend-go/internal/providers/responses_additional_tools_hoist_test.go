package providers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/gin-gonic/gin"
)

func additionalToolsHoistFixture() map[string]interface{} {
	return map[string]interface{}{
		"model":       "kimi-k3",
		"tool_choice": "auto",
		"input": []interface{}{
			map[string]interface{}{
				"type": "additional_tools",
				"id":   "at_1",
				"role": "user",
				"tools": []interface{}{
					map[string]interface{}{
						"type": "namespace",
						"name": "functions",
						"tools": []interface{}{
							map[string]interface{}{
								"type":   "custom",
								"name":   "exec",
								"format": map[string]interface{}{"type": "grammar", "syntax": "lark", "definition": "start: .*"},
							},
							map[string]interface{}{
								"type":       "function",
								"name":       "wait",
								"parameters": map[string]interface{}{"type": "object"},
							},
						},
					},
				},
			},
			map[string]interface{}{"type": "message", "role": "user", "content": []interface{}{map[string]interface{}{"type": "input_text", "text": "run ls"}}},
		},
	}
}

func hoistTestGinContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

func TestHoistCodexAdditionalTools(t *testing.T) {
	t.Run("收割提升为顶层扁平 tools 并置位上下文", func(t *testing.T) {
		req := additionalToolsHoistFixture()
		c := hoistTestGinContext()
		hoistCodexAdditionalTools(c, req)

		tools, ok := req["tools"].([]interface{})
		if !ok || len(tools) != 2 {
			names := []string{}
			for _, tool := range tools {
				if m, ok := tool.(map[string]interface{}); ok {
					names = append(names, toString(m["name"]))
				}
			}
			t.Fatalf("tools = %v (names=%v), want 2 个扁平 function 工具", tools, names)
		}
		names := map[string]bool{}
		for _, tool := range tools {
			m := tool.(map[string]interface{})
			names[toString(m["name"])] = true
			if m["type"] != "function" {
				t.Fatalf("tool type = %v, want function", m["type"])
			}
		}
		if !names["functions__exec"] || !names["functions__wait"] {
			t.Fatalf("工具名 = %v, want functions__exec/functions__wait", names)
		}

		for _, rawItem := range req["input"].([]interface{}) {
			item := rawItem.(map[string]interface{})
			if toString(item["type"]) == "additional_tools" {
				t.Fatalf("additional_tools 条目应从 input 移除")
			}
		}

		if hoisted, _ := c.Get("codex_additional_tools_hoisted"); hoisted != true {
			t.Fatalf("codex_additional_tools_hoisted 未置位")
		}
		ctxVal, ok := c.Get("codex_tool_context")
		if !ok {
			t.Fatalf("codex_tool_context 未设置")
		}
		ctx := ctxVal.(converters.CodexToolContext)
		if !ctx.IsCustomToolProxy("functions__exec") {
			t.Fatalf("ctx 未注册 functions__exec custom 代理")
		}
	})

	t.Run("历史 custom_tool_call 与 namespace function_call 归一", func(t *testing.T) {
		req := additionalToolsHoistFixture()
		req["input"] = append(req["input"].([]interface{}),
			map[string]interface{}{
				"type":    "custom_tool_call",
				"id":      "ctc_1",
				"call_id": "call_1",
				"name":    "exec",
				"input":   "console.log(1)",
				"status":  "completed",
			},
			map[string]interface{}{
				"type":    "custom_tool_call_output",
				"call_id": "call_1",
				"output":  "1",
			},
			map[string]interface{}{
				"type":      "function_call",
				"id":        "fc_2",
				"call_id":   "call_2",
				"name":      "wait",
				"namespace": "functions",
				"arguments": `{"cell_id":"c1"}`,
			},
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "call_2",
				"output":  "done",
			},
		)
		c := hoistTestGinContext()
		hoistCodexAdditionalTools(c, req)

		input := req["input"].([]interface{})
		var customCall, customOutput, nsCall map[string]interface{}
		for _, rawItem := range input {
			item := rawItem.(map[string]interface{})
			switch toString(item["type"]) {
			case "function_call":
				if toString(item["call_id"]) == "call_1" {
					customCall = item
				} else if toString(item["call_id"]) == "call_2" {
					nsCall = item
				}
			case "function_call_output":
				if toString(item["call_id"]) == "call_1" {
					customOutput = item
				}
			case "custom_tool_call", "custom_tool_call_output":
				t.Fatalf("历史 %v 应已归一为 function_call 形态", item["type"])
			}
		}

		if customCall == nil || toString(customCall["name"]) != "functions__exec" {
			t.Fatalf("custom_tool_call 未归一: %#v", customCall)
		}
		if _, leaked := customCall["input"]; leaked {
			t.Fatalf("归一后不应残留 input 字段: %#v", customCall)
		}
		if args := toString(customCall["arguments"]); args != `{"input":"console.log(1)"}` {
			t.Fatalf("arguments = %q", args)
		}
		if customOutput == nil {
			t.Fatalf("custom_tool_call_output 未转为 function_call_output")
		}
		if nsCall == nil || toString(nsCall["name"]) != "functions__wait" {
			t.Fatalf("namespace function_call 未扁平化: %#v", nsCall)
		}
		if _, leaked := nsCall["namespace"]; leaked {
			t.Fatalf("扁平化后不应残留 namespace 字段: %#v", nsCall)
		}
	})

	t.Run("顶层 tools 非空时不触发", func(t *testing.T) {
		req := additionalToolsHoistFixture()
		req["tools"] = []interface{}{map[string]interface{}{"type": "function", "name": "existing"}}
		c := hoistTestGinContext()
		hoistCodexAdditionalTools(c, req)

		tools := req["tools"].([]interface{})
		if len(tools) != 1 || toString(tools[0].(map[string]interface{})["name"]) != "existing" {
			t.Fatalf("顶层 tools 不应被改动: %v", tools)
		}
		if _, ok := c.Get("codex_additional_tools_hoisted"); ok {
			t.Fatalf("未收割时不应置位 hoisted")
		}
	})

	t.Run("无 additional_tools 时不动", func(t *testing.T) {
		req := map[string]interface{}{
			"model": "kimi-k3",
			"input": []interface{}{
				map[string]interface{}{"type": "message", "role": "user"},
			},
		}
		c := hoistTestGinContext()
		hoistCodexAdditionalTools(c, req)
		if _, ok := req["tools"]; ok {
			t.Fatalf("无 additional_tools 不应生成 tools")
		}
		if _, ok := c.Get("codex_additional_tools_hoisted"); ok {
			t.Fatalf("不应置位 hoisted")
		}
	})

	t.Run("与 normalize 串联后 output 配对保留", func(t *testing.T) {
		req := additionalToolsHoistFixture()
		req["input"] = append(req["input"].([]interface{}),
			map[string]interface{}{
				"type":    "custom_tool_call",
				"call_id": "call_1",
				"name":    "exec",
				"input":   "ls",
			},
			map[string]interface{}{
				"type":    "custom_tool_call_output",
				"call_id": "call_1",
				"output":  "ok",
			},
		)
		c := hoistTestGinContext()
		hoistCodexAdditionalTools(c, req)
		normalizeResponsesInputForPassthrough(req)

		// 无状态配对检查必须看到转换后的 function_call，保留 function_call_output
		hasCall, hasOutput := false, false
		for _, rawItem := range req["input"].([]interface{}) {
			item := rawItem.(map[string]interface{})
			switch toString(item["type"]) {
			case "function_call":
				if toString(item["call_id"]) == "call_1" {
					hasCall = true
				}
			case "function_call_output":
				if toString(item["call_id"]) == "call_1" {
					hasOutput = true
				}
			}
		}
		if !hasCall || !hasOutput {
			t.Fatalf("normalize 后历史配对丢失: call=%v output=%v", hasCall, hasOutput)
		}
	})

	t.Run("序列化后形状合法", func(t *testing.T) {
		req := additionalToolsHoistFixture()
		c := hoistTestGinContext()
		hoistCodexAdditionalTools(c, req)
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("序列化失败: %v", err)
		}
		var roundTrip map[string]interface{}
		if err := json.Unmarshal(body, &roundTrip); err != nil {
			t.Fatalf("反序列化失败: %v", err)
		}
		if _, ok := roundTrip["tools"].([]interface{}); !ok {
			t.Fatalf("tools 序列化形状异常")
		}
	})
}
