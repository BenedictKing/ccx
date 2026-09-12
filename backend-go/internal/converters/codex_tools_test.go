package converters

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestBuildApplyPatchInput_AddFileDoesNotAddExtraBlankLine(t *testing.T) {
	got := BuildApplyPatchInput([]ApplyPatchOperation{{
		Type:    "add_file",
		Path:    "docs/test.md",
		Content: "# Test\n",
	}})
	want := "*** Begin Patch\n*** Add File: docs/test.md\n+# Test\n*** End Patch"
	if got != want {
		t.Fatalf("patch mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestRemapCustomToolCallsInResponseWritesInputField(t *testing.T) {
	ctx := BuildCodexToolContext([]map[string]interface{}{{
		"type": "custom",
		"name": "apply_patch",
	}})
	resp := &types.ResponsesResponse{
		Output: []types.ResponsesItem{{
			Type:      "function_call",
			CallID:    "call_1",
			Name:      "apply_patch_add_file",
			Arguments: `{"path":"docs/test.md","content":"# Test\n"}`,
		}},
	}

	ctx.RemapCustomToolCallsInResponse(resp)

	if len(resp.Output) != 1 {
		t.Fatalf("output len = %d", len(resp.Output))
	}
	item := resp.Output[0]
	if item.Type != "custom_tool_call" || item.Name != "apply_patch" {
		t.Fatalf("item = %#v", item)
	}
	if item.Input != "*** Begin Patch\n*** Add File: docs/test.md\n+# Test\n*** End Patch" {
		t.Fatalf("input = %q", item.Input)
	}
	if item.Output != nil {
		t.Fatalf("custom tool call should not use output field: %#v", item.Output)
	}

	b, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"input"`) || strings.Contains(string(b), `"output"`) {
		t.Fatalf("marshaled item = %s", b)
	}
}

func TestRemapCustomToolCallsInResponseSkipsWhenHasCustomToolsFalse(t *testing.T) {
	// Empty CodexToolContext (HasCustomTools=false) should skip remapping entirely.
	ctx := CodexToolContext{}
	resp := &types.ResponsesResponse{
		Output: []types.ResponsesItem{{
			Type:   "function_call",
			CallID: "call_1",
			Name:   "apply_patch_add_file",
			Arguments: `{"path":"docs/test.md","content":"# Test
"}`,
		}},
	}
	ctx.RemapCustomToolCallsInResponse(resp)

	// Output should be unchanged when HasCustomTools is false
	if len(resp.Output) != 1 {
		t.Fatalf("output len = %d, want 1", len(resp.Output))
	}
	if resp.Output[0].Type != "function_call" {
		t.Fatalf("item type = %s, want function_call", resp.Output[0].Type)
	}
}

func TestRemapCustomToolCallsInResponseToolSearchCall(t *testing.T) {
	ctx := BuildCodexToolContextFromRaw([]interface{}{
		map[string]interface{}{
			"type": "tool_search",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string"},
				},
			},
		},
	})
	resp := &types.ResponsesResponse{
		Output: []types.ResponsesItem{{
			Type:      "function_call",
			CallID:    "call_search",
			Name:      "tool_search",
			Arguments: `{"query":"sub-agent"}`,
		}},
	}

	ctx.RemapCustomToolCallsInResponse(resp)

	if len(resp.Output) != 1 {
		t.Fatalf("output len = %d", len(resp.Output))
	}
	item := resp.Output[0]
	if item.Type != "tool_search_call" {
		t.Fatalf("item type = %s, want tool_search_call", item.Type)
	}
	if item.Name != "" || item.Input != "" {
		t.Fatalf("tool_search_call should not use custom tool fields: %#v", item)
	}
	if item.Execution != "client" {
		t.Fatalf("execution = %q", item.Execution)
	}
	if item.Arguments != `{"query":"sub-agent"}` {
		t.Fatalf("arguments = %q", item.Arguments)
	}
}

// ========== Namespace tool tests ==========

func TestBuildCodexToolContext_NamespaceTool(t *testing.T) {
	tools := []map[string]interface{}{{
		"type": "namespace",
		"name": "mcp__vscode_mcp__",
		"tools": []interface{}{
			map[string]interface{}{
				"type": "function",
				"name": "execute_command",
				"parameters": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
	}}

	ctx := BuildCodexToolContext(tools)

	if !ctx.HasNamespaceTools {
		t.Fatal("HasNamespaceTools should be true")
	}
	spec, ok := ctx.FunctionTools["mcp__vscode_mcp__execute_command"]
	if !ok {
		t.Fatal("missing flat function tool entry")
	}
	if spec.Namespace != "mcp__vscode_mcp__" {
		t.Fatalf("namespace = %q, want %q", spec.Namespace, "mcp__vscode_mcp__")
	}
	if spec.Name != "execute_command" {
		t.Fatalf("name = %q, want %q", spec.Name, "execute_command")
	}
}

func TestFlattenNamespaceToolName(t *testing.T) {
	cases := []struct {
		namespace, name, want string
	}{
		{"mcp__vscode_mcp__", "execute_command", "mcp__vscode_mcp__execute_command"},
		{"mcp__", "foo", "mcp__foo"},
		{"mcp", "__foo", "mcp__foo"},
		{"mcp", "foo", "mcp__foo"},
		{"", "foo", "foo"},
		{"mcp", "", "mcp"},
	}

	for _, c := range cases {
		got := flattenNamespaceToolName(c.namespace, c.name)
		if got != c.want {
			t.Errorf("flattenNamespaceToolName(%q, %q) = %q, want %q", c.namespace, c.name, got, c.want)
		}
	}
}

func TestCombineNamespaceDescription(t *testing.T) {
	cases := []struct{ nsDesc, childDesc, want string }{
		{"VSCode MCP tools", "Execute VSCode command", "VSCode MCP tools\n\nExecute VSCode command"},
		{"", "Execute command", "Execute command"},
		{"VSCode MCP tools", "", "VSCode MCP tools"},
		{"", "", ""},
	}

	for _, c := range cases {
		got := combineNamespaceDescription(c.nsDesc, c.childDesc)
		if got != c.want {
			t.Errorf("combineNamespaceDescription(%q, %q) = %q, want %q", c.nsDesc, c.childDesc, got, c.want)
		}
	}
}

func TestNamespaceToolsToOpenAI(t *testing.T) {
	tools := []map[string]interface{}{{
		"type":        "namespace",
		"name":        "mcp__vscode_mcp__",
		"description": "VSCode MCP tools",
		"tools": []interface{}{
			map[string]interface{}{
				"type":        "function",
				"name":        "execute_command",
				"description": "Execute VSCode command",
				"parameters": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{"command": map[string]interface{}{"type": "string"}},
					"required":   []interface{}{"command"},
				},
			},
		},
	}}

	ctx := BuildCodexToolContext(tools)
	openaiTools := responsesToolsToOpenAIWithContext(tools, ctx)

	if len(openaiTools) != 1 {
		t.Fatalf("got %d tools, want 1", len(openaiTools))
	}
	fn, ok := openaiTools[0]["function"].(map[string]interface{})
	if !ok {
		t.Fatal("missing function field")
	}
	name, _ := fn["name"].(string)
	if name != "mcp__vscode_mcp__execute_command" {
		t.Fatalf("name = %q, want %q", name, "mcp__vscode_mcp__execute_command")
	}
	desc, _ := fn["description"].(string)
	if desc != "VSCode MCP tools\n\nExecute VSCode command" {
		t.Fatalf("desc = %q", desc)
	}
}

func TestRemapNamespaceFunctionCallsInResponse(t *testing.T) {
	ctx := BuildCodexToolContext([]map[string]interface{}{{
		"type": "namespace",
		"name": "mcp__vscode_mcp__",
		"tools": []interface{}{
			map[string]interface{}{
				"type":       "function",
				"name":       "execute_command",
				"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			},
		},
	}})

	resp := &types.ResponsesResponse{
		Output: []types.ResponsesItem{{
			Type:      "function_call",
			CallID:    "call_1",
			Name:      "mcp__vscode_mcp__execute_command",
			Arguments: `{"command":"workbench.action.files.saveAll"}`,
		}},
	}

	ctx.RemapNamespaceFunctionCallsInResponse(resp)

	item := resp.Output[0]
	if item.Type != "function_call" {
		t.Fatalf("type = %s, want function_call", item.Type)
	}
	if item.Name != "execute_command" {
		t.Fatalf("name = %q, want %q", item.Name, "execute_command")
	}
	if item.Namespace != "mcp__vscode_mcp__" {
		t.Fatalf("namespace = %q, want %q", item.Namespace, "mcp__vscode_mcp__")
	}
	if item.Arguments != `{"command":"workbench.action.files.saveAll"}` {
		t.Fatalf("arguments = %q", item.Arguments)
	}
}

func TestRemapNamespaceFunctionCallsSkipsNonNamespaceTools(t *testing.T) {
	ctx := BuildCodexToolContext([]map[string]interface{}{{
		"type": "namespace",
		"name": "mcp__vscode_mcp__",
		"tools": []interface{}{
			map[string]interface{}{"type": "function", "name": "execute_command",
				"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		},
	}})

	resp := &types.ResponsesResponse{
		Output: []types.ResponsesItem{{
			Type:      "function_call",
			CallID:    "call_2",
			Name:      "do_something",
			Arguments: `{}`,
		}},
	}

	ctx.RemapNamespaceFunctionCallsInResponse(resp)

	if resp.Output[0].Name != "do_something" {
		t.Fatalf("unrelated function_call was modified: name = %q", resp.Output[0].Name)
	}
}

func TestOpenAINameForFunctionTool(t *testing.T) {
	ctx := BuildCodexToolContext([]map[string]interface{}{
		{
			"type": "function",
			"name": "top_level_func",
		},
		{
			"type": "namespace",
			"name": "mcp__vscode_mcp__",
			"tools": []interface{}{
				map[string]interface{}{"type": "function", "name": "execute_command",
					"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
			},
		},
	})

	name, ns := ctx.OpenAINameForFunctionTool("mcp__vscode_mcp__execute_command")
	if name != "execute_command" || ns != "mcp__vscode_mcp__" {
		t.Fatalf("OpenAINameForFunctionTool = (%q, %q), want (%q, %q)", name, ns, "execute_command", "mcp__vscode_mcp__")
	}

	name2, ns2 := ctx.OpenAINameForFunctionTool("top_level_func")
	if name2 != "top_level_func" || ns2 != "" {
		t.Fatalf("top-level OpenAINameForFunctionTool = (%q, %q), want (%q, %q)", name2, ns2, "top_level_func", "")
	}

	name3, ns3 := ctx.OpenAINameForFunctionTool("unknown_func")
	if name3 != "unknown_func" || ns3 != "" {
		t.Fatalf("unknown OpenAINameForFunctionTool = (%q, %q), want (%q, %q)", name3, ns3, "unknown_func", "")
	}
}

func TestConvertToolChoiceForCodex_NamespaceFunction(t *testing.T) {
	ctx := BuildCodexToolContext([]map[string]interface{}{{
		"type": "namespace",
		"name": "mcp__vscode_mcp__",
		"tools": []interface{}{
			map[string]interface{}{"type": "function", "name": "execute_command",
				"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		},
	}})

	toolChoice := map[string]interface{}{
		"type":      "function",
		"namespace": "mcp__vscode_mcp__",
		"name":      "execute_command",
	}
	result := ConvertToolChoiceForCodex(toolChoice, ctx)

	rm, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("result is not a map")
	}
	fn, ok := rm["function"].(map[string]interface{})
	if !ok {
		t.Fatal("missing function field")
	}
	name, _ := fn["name"].(string)
	if name != "mcp__vscode_mcp__execute_command" {
		t.Fatalf("name = %q, want %q", name, "mcp__vscode_mcp__execute_command")
	}
}

func TestConvertToolChoiceForCodex_NormalFunctionUnchanged(t *testing.T) {
	ctx := CodexToolContext{}
	toolChoice := map[string]interface{}{
		"type": "function",
		"name": "do_something",
	}
	result := ConvertToolChoiceForCodex(toolChoice, ctx)

	if _, ok := result.(map[string]interface{}); !ok {
		t.Fatal("normal function tool choice should pass through as a map")
	}
}

func TestResponsesToolsToOpenAIWithContext_FallsBackWhenDisabled(t *testing.T) {
	tools := []map[string]interface{}{{
		"type": "function",
		"name": "foo",
		"parameters": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}}

	ctx := CodexToolContext{}
	got := responsesToolsToOpenAIWithContext(tools, ctx)
	if len(got) != 1 {
		t.Fatalf("got %d tools, want 1", len(got))
	}
	fn := got[0]["function"].(map[string]interface{})
	if fn["name"] != "foo" {
		t.Fatalf("name = %q, want %q", fn["name"], "foo")
	}
}

func TestBackwardCompatibility_ApplyPatchCustomToolStillWorks(t *testing.T) {
	// Verify that apply_patch custom tools are still handled
	tools := []map[string]interface{}{{
		"type": "custom",
		"name": "apply_patch",
	}}

	ctx := BuildCodexToolContext(tools)
	if !ctx.HasCustomTools {
		t.Fatal("HasCustomTools should be true for apply_patch")
	}
	if _, ok := ctx.CustomTools["apply_patch_add_file"]; !ok {
		t.Fatal("apply_patch_add_file proxy tool missing")
	}
	if _, ok := ctx.CustomTools["apply_patch_delete_file"]; !ok {
		t.Fatal("apply_patch_delete_file proxy tool missing")
	}
	if _, ok := ctx.CustomTools["apply_patch_update_file"]; !ok {
		t.Fatal("apply_patch_update_file proxy tool missing")
	}
	if _, ok := ctx.CustomTools["apply_patch_replace_file"]; !ok {
		t.Fatal("apply_patch_replace_file proxy tool missing")
	}
	if _, ok := ctx.CustomTools["apply_patch_batch"]; !ok {
		t.Fatal("apply_patch_batch proxy tool missing")
	}
}

// TestApplyPatchSchemaHunksHaveItems 锁定 hunks 数组的 items 字段：
// 部分严格校验的上游（如 OpenAI 官方）会对 "type":"array" 但缺少 items 的 schema
// 返回 400 invalid_function_parameters。
func TestApplyPatchSchemaHunksHaveItems(t *testing.T) {
	schemas := map[string]map[string]interface{}{
		"update_file": applyPatchUpdateFileSchema(),
		"batch_item":  extractBatchItemSchema(t, applyPatchBatchSchema()),
	}
	for name, schema := range schemas {
		props, _ := schema["properties"].(map[string]interface{})
		hunks, ok := props["hunks"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: hunks property missing", name)
		}
		if hunks["type"] != "array" {
			t.Fatalf("%s: hunks.type = %v, want array", name, hunks["type"])
		}
		items, ok := hunks["items"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: hunks.items missing or wrong type", name)
		}
		// 进一步验证嵌套 lines 数组也有 items
		hunkProps, _ := items["properties"].(map[string]interface{})
		lines, _ := hunkProps["lines"].(map[string]interface{})
		if lines["type"] != "array" {
			t.Fatalf("%s: hunks.items.properties.lines.type = %v, want array", name, lines["type"])
		}
		if _, ok := lines["items"]; !ok {
			t.Fatalf("%s: hunks.items.properties.lines.items missing", name)
		}
	}
}

// extractBatchItemSchema 从 batch schema 中提取 operations.items 子 schema。
func extractBatchItemSchema(t *testing.T, batch map[string]interface{}) map[string]interface{} {
	t.Helper()
	props, _ := batch["properties"].(map[string]interface{})
	ops, _ := props["operations"].(map[string]interface{})
	items, ok := ops["items"].(map[string]interface{})
	if !ok {
		t.Fatalf("batch schema: operations.items missing")
	}
	return items
}

func TestHistoryReplay_NamespaceFunctionCallFlattened(t *testing.T) {
	// Simulate a ResponsesItem with namespace from history, converted to OpenAI message.
	item := types.ResponsesItem{
		Type:      "function_call",
		CallID:    "call_1",
		Namespace: "mcp__vscode_mcp__",
		Name:      "execute_command",
		Arguments: `{"command":"workbench.action.files.saveAll"}`,
	}

	msg := responsesItemToOpenAIMessage(item)
	if msg == nil {
		t.Fatal("responsesItemToOpenAIMessage returned nil")
	}

	role, _ := msg["role"].(string)
	if role != "assistant" {
		t.Fatalf("role = %q, want assistant", role)
	}

	toolCalls, ok := msg["tool_calls"].([]map[string]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Fatal("missing tool_calls")
	}

	fn := toolCalls[0]["function"].(map[string]interface{})
	fnName, _ := fn["name"].(string)
	if fnName != "mcp__vscode_mcp__execute_command" {
		t.Fatalf("flattened name = %q, want %q", fnName, "mcp__vscode_mcp__execute_command")
	}
	args, _ := fn["arguments"].(string)
	if args != `{"command":"workbench.action.files.saveAll"}` {
		t.Fatalf("args = %q", args)
	}
}

func TestHistoryReplay_TopLevelFunctionCallNotModified(t *testing.T) {
	item := types.ResponsesItem{
		Type:      "function_call",
		CallID:    "call_2",
		Name:      "list_mcp_resources",
		Arguments: `{}`,
	}

	msg := responsesItemToOpenAIMessage(item)
	if msg == nil {
		t.Fatal("responsesItemToOpenAIMessage returned nil")
	}

	toolCalls, ok := msg["tool_calls"].([]map[string]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Fatal("missing tool_calls")
	}

	fn := toolCalls[0]["function"].(map[string]interface{})
	fnName, _ := fn["name"].(string)
	if fnName != "list_mcp_resources" {
		t.Fatalf("name = %q, want %q", fnName, "list_mcp_resources")
	}
}

func TestNamespaceAndCustomToolsCoexist(t *testing.T) {
	tools := []map[string]interface{}{
		{
			"type": "namespace",
			"name": "mcp__vscode_mcp__",
			"tools": []interface{}{
				map[string]interface{}{"type": "function", "name": "execute_command",
					"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
			},
		},
		{
			"type": "custom",
			"name": "apply_patch",
		},
	}

	ctx := BuildCodexToolContext(tools)

	if !ctx.HasCustomTools {
		t.Fatal("HasCustomTools should be true")
	}
	if !ctx.HasNamespaceTools {
		t.Fatal("HasNamespaceTools should be true")
	}

	// Verify namespace function in context
	spec, ok := ctx.FunctionTools["mcp__vscode_mcp__execute_command"]
	if !ok || spec.Name != "execute_command" {
		t.Fatal("namespace function tool missing")
	}

	// Verify custom proxies
	if _, ok := ctx.CustomTools["apply_patch_add_file"]; !ok {
		t.Fatal("custom proxy tools missing")
	}

	// Convert to OpenAI tools
	openaiTools := responsesToolsToOpenAIWithContext(tools, ctx)

	hasNamespaceTool := false
	hasCustomProxy := false
	for _, ot := range openaiTools {
		fn := ot["function"].(map[string]interface{})
		name := fn["name"].(string)
		if name == "mcp__vscode_mcp__execute_command" {
			hasNamespaceTool = true
		}
		if name == "apply_patch_batch" {
			hasCustomProxy = true
		}
	}
	if !hasNamespaceTool {
		t.Fatal("namespace tool missing from OpenAI output")
	}
	if !hasCustomProxy {
		t.Fatal("custom proxy tool missing from OpenAI output")
	}
}

func TestResponsesToolsToOpenAIWithContext_EmptyNamespaceName(t *testing.T) {
	// Namespace tool with empty name: flattened name should be just the child name.
	tools := []map[string]interface{}{{
		"type": "namespace",
		"tools": []interface{}{
			map[string]interface{}{
				"type":       "function",
				"name":       "func",
				"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			},
		},
	}}

	ctx := BuildCodexToolContext(tools)
	openaiTools := responsesToolsToOpenAIWithContext(tools, ctx)
	if len(openaiTools) != 1 {
		t.Fatalf("got %d tools, want 1", len(openaiTools))
	}
	fn := openaiTools[0]["function"].(map[string]interface{})
	name := fn["name"].(string)
	if name != "func" {
		t.Fatalf("unexpected name: %q, want func", name)
	}
}

func TestWrapOpenAIChatResponseToResponsesWithContext_NamespaceRemapping(t *testing.T) {
	ctx := BuildCodexToolContext([]map[string]interface{}{{
		"type": "namespace",
		"name": "mcp__vscode_mcp__",
		"tools": []interface{}{
			map[string]interface{}{"type": "function", "name": "execute_command",
				"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
		},
	}})

	openaiResp := map[string]interface{}{
		"id":    "chatcmpl-123",
		"model": "gpt-4",
		"choices": []interface{}{
			map[string]interface{}{
				"index": float64(0),
				"message": map[string]interface{}{
					"role": "assistant",
					"tool_calls": []interface{}{
						map[string]interface{}{
							"id":   "call_1",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "mcp__vscode_mcp__execute_command",
								"arguments": `{"command":"test"}`,
							},
						},
					},
				},
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     float64(10),
			"completion_tokens": float64(5),
			"total_tokens":      float64(15),
		},
	}

	resp, err := WrapOpenAIChatResponseToResponsesWithContext(openaiResp, "session_1", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("output len = %d, want 1", len(resp.Output))
	}
	item := resp.Output[0]
	if item.Type != "function_call" {
		t.Fatalf("type = %s, want function_call", item.Type)
	}
	if item.Name != "execute_command" {
		t.Fatalf("name = %q, want %q", item.Name, "execute_command")
	}
	if item.Namespace != "mcp__vscode_mcp__" {
		t.Fatalf("namespace = %q, want %q", item.Namespace, "mcp__vscode_mcp__")
	}
}

func TestWrapOpenAIChatResponseToResponsesWithContext_NamespaceStreaming(t *testing.T) {
	ctx := BuildCodexToolContext([]map[string]interface{}{{
		"type": "namespace",
		"name": "mcp__vscode_mcp__",
		"tools": []interface{}{
			map[string]interface{}{
				"type":       "function",
				"name":       "execute_command",
				"parameters": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			},
		},
	}})

	openaiResp := map[string]interface{}{
		"id":    "chatcmpl-123",
		"model": "gpt-4",
		"choices": []interface{}{
			map[string]interface{}{
				"index": float64(0),
				"message": map[string]interface{}{
					"role": "assistant",
					"tool_calls": []interface{}{
						map[string]interface{}{
							"id":   "call_1",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "mcp__vscode_mcp__execute_command",
								"arguments": `{"command":"test"}`,
							},
						},
					},
				},
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     float64(10),
			"completion_tokens": float64(5),
			"total_tokens":      float64(15),
		},
	}

	resp, err := WrapOpenAIChatResponseToResponsesWithContext(openaiResp, "session_1", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Output) != 1 {
		t.Fatalf("output len = %d, want 1", len(resp.Output))
	}
	item := resp.Output[0]
	if item.Type != "function_call" {
		t.Fatalf("type = %s, want function_call", item.Type)
	}
	if item.Name != "execute_command" {
		t.Fatalf("name = %q, want %q", item.Name, "execute_command")
	}
	if item.Namespace != "mcp__vscode_mcp__" {
		t.Fatalf("namespace = %q, want %q", item.Namespace, "mcp__vscode_mcp__")
	}
	if item.Arguments != `{"command":"test"}` {
		t.Fatalf("arguments = %q", item.Arguments)
	}
}

func TestRemapCustomToolCallsInResponse_StringToolsApplyPatch(t *testing.T) {
	// Codex sends tools as string array: ["apply_patch_add_file", "apply_patch_batch", "exec_command"]
	rawTools := []interface{}{"apply_patch_add_file", "apply_patch_batch", "exec_command"}
	ctx := BuildCodexToolContextFromRaw(rawTools)

	resp := &types.ResponsesResponse{
		Output: []types.ResponsesItem{{
			Type:      "function_call",
			CallID:    "call_1",
			Name:      "apply_patch_add_file",
			Arguments: `{"path":"/tmp/test.txt","content":"hello world"}`,
		}},
	}

	ctx.RemapCustomToolCallsInResponse(resp)

	if len(resp.Output) != 1 {
		t.Fatalf("output len = %d", len(resp.Output))
	}
	item := resp.Output[0]
	if item.Type != "custom_tool_call" {
		t.Fatalf("expected custom_tool_call, got %s", item.Type)
	}
	if item.Name != "apply_patch" {
		t.Fatalf("expected name=apply_patch, got %s", item.Name)
	}
	if !strings.Contains(item.Input, "*** Begin Patch") {
		t.Fatalf("expected patch format input, got %q", item.Input)
	}
}

// ========== tool_search tests ==========

func TestBuildCodexToolContext_ToolSearch(t *testing.T) {
	tools := []interface{}{
		map[string]interface{}{
			"type":        "tool_search",
			"execution":   "server",
			"description": "Search for tools",
			"parameters": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}

	ctx := BuildCodexToolContextFromRaw(tools)

	if !ctx.HasCustomTools {
		t.Fatal("tool_search should be tracked as a built-in proxy for Chat upstreams")
	}
	if !ctx.HasBuiltinFunctionTools {
		t.Fatal("HasBuiltinFunctionTools should be true when tool_search is present")
	}
	spec, ok := ctx.CustomTools["tool_search"]
	if !ok {
		t.Fatal("missing tool_search entry in CustomTools")
	}
	if spec.OpenAIName != "tool_search" {
		t.Fatalf("OpenAIName = %q, want %q", spec.OpenAIName, "tool_search")
	}
	if spec.Kind != CodexCustomToolBuiltIn {
		t.Fatalf("Kind = %q, want %q", spec.Kind, CodexCustomToolBuiltIn)
	}
	if !ctx.IsToolSearchProxy("tool_search") {
		t.Fatal("tool_search should be recognized as a native Responses tool_search proxy")
	}
}

func TestBuildCodexToolContext_StringToolSearch(t *testing.T) {
	ctx := BuildCodexToolContextFromRaw([]interface{}{"tool_search"})

	if !ctx.HasBuiltinFunctionTools {
		t.Fatal("string tool_search should be tracked as a built-in function proxy")
	}
	if !ctx.IsToolSearchProxy("tool_search") {
		t.Fatal("string tool_search should be recognized as a native Responses tool_search proxy")
	}
}

func TestToolSearchConvertsToOpenAIFunction(t *testing.T) {
	tools := []interface{}{
		map[string]interface{}{
			"type":        "tool_search",
			"execution":   "server",
			"description": "Search for tools by description",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"query"},
			},
		},
		map[string]interface{}{
			"type": "function",
			"name": "my_func",
			"parameters": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}

	ctx := BuildCodexToolContextFromRaw(tools)
	result := responsesRawToolsToOpenAIWithContext(tools, ctx)

	// tool_search and my_func should both produce function tools.
	if len(result) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result))
	}

	toolSearch := result[0]
	fn, _ := toolSearch["function"].(map[string]interface{})
	if fn["name"] != "tool_search" {
		t.Fatalf("tool name = %v, want tool_search", fn["name"])
	}
	desc, _ := fn["description"].(string)
	if !strings.Contains(desc, "Search for tools by description") {
		t.Fatalf("tool description = %q, should contain original description", desc)
	}
	params, _ := fn["parameters"].(map[string]interface{})
	if _, ok := params["properties"].(map[string]interface{})["query"]; !ok {
		t.Fatalf("tool_search should preserve query schema, got %#v", params)
	}
	if _, ok := params["properties"].(map[string]interface{})["input"]; ok {
		t.Fatalf("tool_search should preserve structured schema, got generic input schema: %#v", params)
	}
}

func TestToolSearchCustomHistoryPreservesArguments(t *testing.T) {
	ctx := BuildCodexToolContextFromRaw([]interface{}{
		map[string]interface{}{
			"type": "tool_search",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string"},
					"limit": map[string]interface{}{"type": "number"},
				},
			},
		},
	})

	input := `{"limit":8,"query":"multi-agent"}`
	if got := ReconstructCustomToolCallInput(ctx, "tool_search", input); got != input {
		t.Fatalf("ReconstructCustomToolCallInput() = %q, want %q", got, input)
	}

	name, args := BuildCustomToolCallHistoryArguments(ctx, "tool_search", input)
	if name != "tool_search" {
		t.Fatalf("name = %q, want tool_search", name)
	}
	if args != input {
		t.Fatalf("args = %q, want %q", args, input)
	}
	if strings.Contains(args, `"input"`) {
		t.Fatalf("tool_search history args should not be wrapped in input: %s", args)
	}

	wrappedInput := `{"input":"{\"limit\":8,\"query\":\"multi-agent\"}"}`
	if got := ReconstructCustomToolCallInput(ctx, "tool_search", wrappedInput); got != input {
		t.Fatalf("wrapped ReconstructCustomToolCallInput() = %q, want %q", got, input)
	}
	_, args = BuildCustomToolCallHistoryArguments(ctx, "tool_search", wrappedInput)
	if args != input {
		t.Fatalf("wrapped tool_search history args = %q, want %q", args, input)
	}
}

// ============== additional_tools 提升（codex 0.153+）==============

func additionalToolsNamespaceFixture() map[string]interface{} {
	return map[string]interface{}{
		"type":        "namespace",
		"name":        "functions",
		"description": "Unified exec namespace.",
		"tools": []interface{}{
			map[string]interface{}{
				"type":        "custom",
				"name":        "exec",
				"description": "Run JavaScript code to orchestrate tool calls.",
				"format": map[string]interface{}{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: .*",
				},
			},
			map[string]interface{}{
				"type":        "function",
				"name":        "wait",
				"description": "Waits on a yielded exec cell.",
				"parameters": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{"cell_id": map[string]interface{}{"type": "string"}},
				},
			},
		},
	}
}

func TestBuildCodexToolContext_NamespaceCustomChild(t *testing.T) {
	ctx := BuildCodexToolContextFromRaw([]interface{}{additionalToolsNamespaceFixture()})

	if !ctx.HasCustomTools || !ctx.HasNamespaceTools {
		t.Fatalf("ctx flags = custom:%v namespace:%v, want both true", ctx.HasCustomTools, ctx.HasNamespaceTools)
	}
	spec, ok := ctx.CustomTools["functions__exec"]
	if !ok {
		t.Fatalf("CustomTools missing functions__exec: %#v", ctx.CustomTools)
	}
	if spec.OpenAIName != "exec" || spec.Kind != CodexCustomToolExec {
		t.Fatalf("spec = %#v, want OpenAIName=exec Kind=exec", spec)
	}
	fn, ok := ctx.FunctionTools["functions__wait"]
	if !ok || fn.Namespace != "functions" || fn.Name != "wait" {
		t.Fatalf("FunctionTools[functions__wait] = %#v, ok=%v", fn, ok)
	}
}

func TestBuildCodexToolContext_NamespaceCustomApplyPatchDegradedToRaw(t *testing.T) {
	nsTool := map[string]interface{}{
		"type": "namespace",
		"name": "functions",
		"tools": []interface{}{
			map[string]interface{}{"type": "custom", "name": "apply_patch"},
		},
	}
	ctx := BuildCodexToolContextFromRaw([]interface{}{nsTool})
	spec, ok := ctx.CustomTools["functions__apply_patch"]
	if !ok {
		t.Fatalf("CustomTools missing functions__apply_patch")
	}
	if spec.Kind != CodexCustomToolRaw {
		t.Fatalf("namespace custom apply_patch Kind = %q, want raw（不做 action 拆分）", spec.Kind)
	}
	if _, exists := ctx.CustomTools["functions__apply_patch_batch"]; exists {
		t.Fatalf("namespace custom 不应注册 proxy 后缀变体")
	}
}

func TestBuildCustomToolCallHistoryArguments_ReverseLookupHoisted(t *testing.T) {
	ctx := BuildCodexToolContextFromRaw([]interface{}{additionalToolsNamespaceFixture()})

	name, args := BuildCustomToolCallHistoryArguments(ctx, "exec", "console.log(1)")
	if name != "functions__exec" {
		t.Fatalf("name = %q, want functions__exec", name)
	}
	if args != `{"input":"console.log(1)"}` {
		t.Fatalf("args = %q, want wrapped input", args)
	}
}

func TestBuildCustomToolCallHistoryArguments_TopLevelStillWorks(t *testing.T) {
	ctx := BuildCodexToolContextFromRaw([]interface{}{
		map[string]interface{}{"type": "custom", "name": "exec", "format": map[string]interface{}{"type": "grammar", "definition": "start: .*"}},
	})
	name, args := BuildCustomToolCallHistoryArguments(ctx, "exec", "ls")
	if name != "exec" {
		t.Fatalf("顶层 custom name = %q, want exec（行为不变）", name)
	}
	if args != `{"input":"ls"}` {
		t.Fatalf("args = %q", args)
	}
}

func TestNamespaceToolsToResponsesFlat(t *testing.T) {
	ctx := BuildCodexToolContextFromRaw([]interface{}{additionalToolsNamespaceFixture()})
	out := NamespaceToolsToResponsesFlat(additionalToolsNamespaceFixture(), ctx)
	if len(out) != 2 {
		t.Fatalf("flat tools 数量 = %d, want 2", len(out))
	}

	byName := map[string]map[string]interface{}{}
	for _, tool := range out {
		byName[tool["name"].(string)] = tool
		if tool["type"] != "function" {
			t.Fatalf("tool type = %v, want function", tool["type"])
		}
		if _, nested := tool["function"]; nested {
			t.Fatalf("提升输出应为 Responses 扁平格式，得到嵌套 function 字段: %#v", tool)
		}
	}

	wait := byName["functions__wait"]
	if wait == nil {
		t.Fatalf("缺少 functions__wait: %#v", byName)
	}
	if _, ok := wait["parameters"].(map[string]interface{}); !ok {
		t.Fatalf("functions__wait 缺 parameters: %#v", wait)
	}
	desc, _ := wait["description"].(string)
	if !strings.Contains(desc, "Unified exec namespace.") || !strings.Contains(desc, "Waits on a yielded exec cell.") {
		t.Fatalf("description 未合并 namespace 描述: %q", desc)
	}

	exec := byName["functions__exec"]
	if exec == nil {
		t.Fatalf("缺少 functions__exec freeform 代理")
	}
	params, _ := exec["parameters"].(map[string]interface{})
	props, _ := params["properties"].(map[string]interface{})
	if _, ok := props["input"]; !ok {
		t.Fatalf("custom 子工具应为 freeform input schema: %#v", params)
	}
	if desc, _ := exec["description"].(string); !strings.Contains(desc, "FREEFORM") {
		t.Fatalf("freeform 代理 description 缺提示: %q", desc)
	}
}

func TestUpstreamNameForNamespaceCall(t *testing.T) {
	ctx := BuildCodexToolContextFromRaw([]interface{}{additionalToolsNamespaceFixture()})
	flat, ok := ctx.UpstreamNameForNamespaceCall("functions", "wait")
	if !ok || flat != "functions__wait" {
		t.Fatalf("UpstreamNameForNamespaceCall = %q, %v", flat, ok)
	}
	if _, ok := ctx.UpstreamNameForNamespaceCall("functions", "nonexistent"); ok {
		t.Fatalf("未知工具不应命中")
	}
}
