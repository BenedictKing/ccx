package converters

import "strings"

// HoistCodexAdditionalTools 将 input 中的增量工具提升到顶层，并归一化调用历史。
// 返回原始工具定义供响应还原；无增量声明时保持请求不变。
func HoistCodexAdditionalTools(reqMap map[string]interface{}) ([]interface{}, CodexToolContext, bool) {
	input, ok := reqMap["input"].([]interface{})
	if !ok {
		return nil, CodexToolContext{}, false
	}

	existing, _ := reqMap["tools"].([]interface{})
	harvested := append([]interface{}(nil), existing...)
	found := false
	kept := make([]interface{}, 0, len(input))
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]interface{})
		if !ok || stringFromInterface(item["type"]) != "additional_tools" {
			kept = append(kept, rawItem)
			continue
		}
		if tools, ok := item["tools"].([]interface{}); ok {
			found = found || len(tools) > 0
			harvested = append(harvested, tools...)
		}
	}
	if !found {
		return nil, CodexToolContext{}, false
	}
	harvested = mergeAdditionalTools(harvested)
	ctx := BuildCodexToolContextFromRaw(harvested)
	hoisted := make([]interface{}, 0, len(harvested))
	for _, rawTool := range harvested {
		tool, ok := rawTool.(map[string]interface{})
		if !ok {
			hoisted = append(hoisted, rawTool)
			continue
		}
		switch stringFromInterface(tool["type"]) {
		case "namespace":
			for _, flat := range NamespaceToolsToResponsesFlat(tool, ctx) {
				hoisted = append(hoisted, flat)
			}
		case "function":
			hoisted = append(hoisted, tool)
		case "custom":
			name := stringFromInterface(tool["name"])
			if name == "" {
				continue
			}
			hoisted = append(hoisted, CustomProxyToolResponsesFlat(name, stringFromInterface(tool["description"])))
		default:
			hoisted = append(hoisted, tool)
		}
	}
	if len(hoisted) == 0 {
		return nil, CodexToolContext{}, false
	}

	for i, rawItem := range kept {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			continue
		}
		switch stringFromInterface(item["type"]) {
		case "custom_tool_call":
			upstreamName, argsJSON := BuildCustomToolCallHistoryArguments(ctx, stringFromInterface(item["name"]), stringFromInterface(item["input"]))
			item["type"] = "function_call"
			rewriteResponsesItemIDPrefix(item, "ctc_", "fc_")
			item["name"] = upstreamName
			delete(item, "input")
			item["arguments"] = argsJSON
			kept[i] = item
		case "custom_tool_call_output":
			item["type"] = "function_call_output"
			rewriteResponsesItemIDPrefix(item, "ctco_", "fco_")
			kept[i] = item
		case "function_call":
			namespace := stringFromInterface(item["namespace"])
			if namespace == "" {
				continue
			}
			if flat, ok := ctx.UpstreamNameForNamespaceCall(namespace, stringFromInterface(item["name"])); ok {
				item["name"] = flat
				delete(item, "namespace")
				kept[i] = item
			}
		}
	}

	reqMap["input"] = kept
	reqMap["tools"] = hoisted
	reqMap["tool_choice"] = ConvertToolChoiceForCodex(reqMap["tool_choice"], ctx)

	return harvested, ctx, true
}

func rewriteResponsesItemIDPrefix(item map[string]interface{}, oldPrefix, newPrefix string) {
	id, ok := item["id"].(string)
	if ok && strings.HasPrefix(id, oldPrefix) {
		item["id"] = newPrefix + strings.TrimPrefix(id, oldPrefix)
	}
}

// 增量声明可向已有 namespace 添加子工具，不能按 namespace 名丢弃整组。
func mergeAdditionalTools(tools []interface{}) []interface{} {
	merged := make([]interface{}, 0, len(tools))
	indices := make(map[string]int)
	for _, raw := range tools {
		key := codexToolIdentity(raw)
		if previous, ok := indices[key]; ok {
			tool, valid := raw.(map[string]interface{})
			if valid && tool["type"] == "namespace" {
				old, _ := merged[previous].(map[string]interface{})
				children, _ := old["tools"].([]interface{})
				more, _ := tool["tools"].([]interface{})
				combined := make(map[string]interface{}, len(old))
				for name, value := range old {
					combined[name] = value
				}
				combined["tools"] = MergeCodexToolSearchOutputTools(append(append([]interface{}(nil), children...), more...), nil, nil)
				merged[previous] = combined
			}
			continue
		}
		indices[key] = len(merged)
		merged = append(merged, raw)
	}
	return merged
}
