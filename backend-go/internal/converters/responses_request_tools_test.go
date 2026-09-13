package converters

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestResponsesToGeminiRequest_PreservesTools(t *testing.T) {
	req := &types.ResponsesRequest{
		Model: "gpt-5",
		Input: "hello",
		Tools: []map[string]interface{}{
			{
				"type":        "function",
				"name":        "get_weather",
				"description": "Get weather",
				"parameters": map[string]interface{}{
					"type": "object",
				},
			},
		},
	}

	geminiReq, err := ResponsesToGeminiRequest(&session.Session{}, req, "gemini-2.5-pro")
	assert.NoError(t, err)
	assert.Len(t, geminiReq.Tools, 1)
	assert.Len(t, geminiReq.Tools[0].FunctionDeclarations, 1)
	assert.Equal(t, "get_weather", geminiReq.Tools[0].FunctionDeclarations[0].Name)
}

func TestOpenAIChatConverter_ToProviderRequest_PreservesTools(t *testing.T) {
	parallelToolCalls := true
	req := &types.ResponsesRequest{
		Model:             "gpt-5",
		Input:             "hello",
		ToolChoice:        "auto",
		ParallelToolCalls: &parallelToolCalls,
		RawTools: []interface{}{
			map[string]interface{}{
				"type":        "function",
				"name":        "get_weather",
				"description": "Get weather",
				"parameters": map[string]interface{}{
					"type": "object",
				},
			},
		},
	}

	converted, err := (&OpenAIChatConverter{}).ToProviderRequest(&session.Session{}, req)
	assert.NoError(t, err)

	requestMap, ok := converted.(map[string]interface{})
	assert.True(t, ok)
	tools, ok := requestMap["tools"].([]map[string]interface{})
	assert.True(t, ok)
	assert.Len(t, tools, 1)
	assert.Equal(t, "function", tools[0]["type"])
	assert.Equal(t, "auto", requestMap["tool_choice"])
	assert.Equal(t, true, requestMap["parallel_tool_calls"])
}

func TestClaudeConverter_ToProviderRequest_PreservesTools(t *testing.T) {
	req := &types.ResponsesRequest{
		Model: "claude-3-5-sonnet",
		Input: "hello",
		Tools: []map[string]interface{}{
			{
				"type":        "function",
				"name":        "get_weather",
				"description": "Get weather",
				"parameters": map[string]interface{}{
					"type": "object",
				},
			},
		},
	}

	converted, err := (&ClaudeConverter{}).ToProviderRequest(&session.Session{}, req)
	assert.NoError(t, err)

	requestMap, ok := converted.(map[string]interface{})
	assert.True(t, ok)
	tools, ok := requestMap["tools"].([]map[string]interface{})
	assert.True(t, ok)
	assert.Len(t, tools, 1)
	assert.Equal(t, "get_weather", tools[0]["name"])
	assert.NotNil(t, tools[0]["input_schema"])
}

func TestClaudeConverter_ToProviderRequest_MapsUserToMetadataUserID(t *testing.T) {
	req := &types.ResponsesRequest{
		Model: "claude-3-5-sonnet",
		Input: "hello",
		User:  "deepseek_user_123",
	}

	converted, err := (&ClaudeConverter{}).ToProviderRequest(&session.Session{}, req)
	assert.NoError(t, err)

	requestMap, ok := converted.(map[string]interface{})
	assert.True(t, ok)
	metadata, ok := requestMap["metadata"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "deepseek_user_123", metadata["user_id"])
}

func TestOpenAIChatConverter_ToolRequiredField_FilledWhenMissing(t *testing.T) {
	req := &types.ResponsesRequest{
		Model: "gpt-5.5",
		Input: "list resources",
		RawTools: []interface{}{
			map[string]interface{}{
				"type":        "function",
				"name":        "list_mcp_resources",
				"description": "Lists resources",
				"parameters": map[string]interface{}{
					"type":                 "object",
					"properties":           map[string]interface{}{},
					"additionalProperties": false,
				},
			},
		},
	}

	converted, err := (&OpenAIChatConverter{}).ToProviderRequest(&session.Session{}, req)
	assert.NoError(t, err)
	requestMap := converted.(map[string]interface{})
	tools := requestMap["tools"].([]map[string]interface{})

	params, ok := tools[0]["function"].(map[string]interface{})["parameters"].(map[string]interface{})
	assert.True(t, ok)
	required, exists := params["required"]
	assert.True(t, exists, "required 字段必须存在")
	_, isArray := required.([]interface{})
	assert.True(t, isArray, "required 字段必须是数组")
	assert.False(t, exists && !isArray, "required 不应为 None 或非数组")
}

func TestOpenAIChatConverter_SkipsNonFunctionTools(t *testing.T) {
	req := &types.ResponsesRequest{
		Model: "gpt-5.5",
		Input: "search",
		RawTools: []interface{}{
			map[string]interface{}{"type": "web_search", "search_content_types": []interface{}{"text"}},
			map[string]interface{}{"type": "custom", "name": "apply_patch", "format": map[string]interface{}{"type": "grammar"}},
			map[string]interface{}{
				"type":       "function",
				"name":       "do_thing",
				"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			},
		},
	}

	converted, err := (&OpenAIChatConverter{}).ToProviderRequest(&session.Session{}, req)
	assert.NoError(t, err)
	requestMap := converted.(map[string]interface{})
	tools := requestMap["tools"].([]map[string]interface{})
	assert.Len(t, tools, 1, "只应保留 function 类型工具")
	assert.Equal(t, "do_thing", tools[0]["function"].(map[string]interface{})["name"])
}

func TestOpenAIChatConverter_ConvertsToolSearchWhenCodexCompatEnabled(t *testing.T) {
	req := &types.ResponsesRequest{
		Model: "gpt-5.5",
		Input: "search",
		RawTools: []interface{}{
			map[string]interface{}{
				"type":        "tool_search",
				"execution":   "client",
				"description": "Search over deferred tool metadata",
				"parameters": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
			map[string]interface{}{
				"type":       "function",
				"name":       "do_thing",
				"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			},
		},
		TransformerMetadata: map[string]interface{}{
			"codex_tool_compat_enabled": true,
		},
	}

	converted, err := (&OpenAIChatConverter{}).ToProviderRequest(&session.Session{}, req)
	assert.NoError(t, err)
	requestMap := converted.(map[string]interface{})
	tools := requestMap["tools"].([]map[string]interface{})
	assert.Len(t, tools, 2)
	assert.Equal(t, "tool_search", tools[0]["function"].(map[string]interface{})["name"])
	assert.Equal(t, "do_thing", tools[1]["function"].(map[string]interface{})["name"])
}

func TestOpenAIChatConverter_MergesToolSearchOutputToolsWhenCodexCompatEnabled(t *testing.T) {
	req := &types.ResponsesRequest{
		Model: "gpt-5.5",
		Input: []types.ResponsesItem{
			{
				Type: "tool_search_output",
				Tools: []interface{}{
					map[string]interface{}{
						"type":        "namespace",
						"name":        "multi_agent_v1",
						"description": "Multi-agent tools",
						"tools": []interface{}{
							map[string]interface{}{
								"type":        "function",
								"name":        "spawn_agent",
								"description": "Spawn an agent",
								"parameters":  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
							},
							map[string]interface{}{
								"type":        "function",
								"name":        "wait_agent",
								"description": "Wait for an agent",
								"parameters":  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
							},
						},
					},
				},
			},
			{Type: "message", Role: "user", Content: "试试子代理"},
		},
		RawTools: []interface{}{
			map[string]interface{}{
				"type":        "tool_search",
				"execution":   "client",
				"description": "Search over deferred tool metadata",
				"parameters": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		TransformerMetadata: map[string]interface{}{
			"codex_tool_compat_enabled": true,
		},
	}

	converted, err := (&OpenAIChatConverter{}).ToProviderRequest(&session.Session{}, req)
	assert.NoError(t, err)

	requestMap := converted.(map[string]interface{})
	tools := requestMap["tools"].([]map[string]interface{})
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool["function"].(map[string]interface{})["name"].(string))
	}

	assert.Contains(t, names, "tool_search")
	assert.Contains(t, names, "multi_agent_v1__spawn_agent")
	assert.Contains(t, names, "multi_agent_v1__wait_agent")

	ctx, ok := req.TransformerMetadata["codex_tool_context"].(CodexToolContext)
	assert.True(t, ok)
	assert.True(t, ctx.HasNamespaceTools)
	name, namespace := ctx.OpenAINameForFunctionTool("multi_agent_v1__spawn_agent")
	assert.Equal(t, "spawn_agent", name)
	assert.Equal(t, "multi_agent_v1", namespace)
}

func TestOpenAIChatConverter_PreservesStringToolsWhenCodexCompatDisabled(t *testing.T) {
	req := &types.ResponsesRequest{
		Model:    "gpt-5.5",
		Input:    "search",
		RawTools: []interface{}{"web_search_preview"},
	}

	converted, err := (&OpenAIChatConverter{}).ToProviderRequest(&session.Session{}, req)
	assert.NoError(t, err)
	requestMap := converted.(map[string]interface{})
	tools := requestMap["tools"].([]map[string]interface{})
	assert.Len(t, tools, 1)
	assert.Equal(t, "web_search_preview", tools[0]["function"].(map[string]interface{})["name"])
}

func TestOpenAIChatResponseToResponses_ToolCalls(t *testing.T) {
	openaiResp := map[string]interface{}{
		"model": "gpt-4o",
		"choices": []interface{}{
			map[string]interface{}{
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "I'll call a tool.",
					"tool_calls": []interface{}{
						map[string]interface{}{
							"id":   "call_123",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "get_weather",
								"arguments": `{"location":"NYC"}`,
							},
						},
					},
				},
			},
		},
	}

	resp, err := OpenAIChatResponseToResponses(openaiResp, "sess_test")
	assert.NoError(t, err)
	assert.Len(t, resp.Output, 2)
	assert.Equal(t, "message", resp.Output[0].Type)
	assert.Equal(t, "function_call", resp.Output[1].Type)
	assert.Equal(t, "call_123", resp.Output[1].CallID)
	assert.Equal(t, "get_weather", resp.Output[1].Name)
}

// additional_tools 提升场景（codex 0.153+）：providers 层已把 input[] 的
// additional_tools 收割提升为顶层扁平 tools，提升前（namespace/custom 形态）的
// ctx 与原始工具数组经 TransformerMetadata 传入。转换器必须复用该 ctx（而非按
// 提升后的扁平 rawTools 重建），否则 custom 代理语义丢失、custom_tool_call 无法还原。
func TestOpenAIChatConverter_HoistedAdditionalToolsReusesPreHoistCtx(t *testing.T) {
	preHoistRaw := []interface{}{
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
	}
	preHoistCtx := BuildCodexToolContextFromRaw(preHoistRaw)

	req := &types.ResponsesRequest{
		Model: "gpt-6-astra",
		Input: "run cat probe.txt",
		RawTools: []interface{}{
			map[string]interface{}{"type": "function", "name": "functions__exec", "parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"input": map[string]interface{}{"type": "string"}}}},
			map[string]interface{}{"type": "function", "name": "functions__wait", "parameters": map[string]interface{}{"type": "object"}},
		},
		TransformerMetadata: map[string]interface{}{
			"codex_tool_compat_enabled": true,
			"codex_tool_context":        preHoistCtx,
			"codex_merged_raw_tools":    preHoistRaw,
		},
	}

	converted, err := (&OpenAIChatConverter{}).ToProviderRequest(&session.Session{}, req)
	assert.NoError(t, err)

	requestMap := converted.(map[string]interface{})
	tools := requestMap["tools"].([]map[string]interface{})
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool["function"].(map[string]interface{})["name"].(string))
	}
	assert.Contains(t, names, "functions__exec")
	assert.Contains(t, names, "functions__wait")

	// ctx 必须保持提升前形态（custom 代理语义），不被提升后扁平 rawTools 覆盖
	ctx, ok := req.TransformerMetadata["codex_tool_context"].(CodexToolContext)
	assert.True(t, ok)
	assert.True(t, ctx.HasCustomTools)
	spec, ok := ctx.CustomTools["functions__exec"]
	assert.True(t, ok)
	assert.Equal(t, "exec", spec.OpenAIName)
	assert.Equal(t, CodexCustomToolExec, spec.Kind)

	// merged raw tools 必须保持提升前 namespace/custom 形态
	merged, ok := req.TransformerMetadata["codex_merged_raw_tools"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, merged, 1)
	assert.Equal(t, "namespace", merged[0].(map[string]interface{})["type"])
}
