package converters

import (
	"encoding/json"
	"strings"
)

// OpenRouter 的 Responses API 实现与 OpenAI 规范存在两处系统性偏差：
//  1. 非流式：reasoning 输出条目把思考文本放在非标准 content[]（part 类型 reasoning_text）里，
//     而规范位置 summary[] 留空；
//  2. 流式：思考增量用非标准事件 response.reasoning_text.delta/.done，
//     且 content_part.added/.done 携带 reasoning_text part，
//     而非规范的 response.reasoning_summary_text.* / response.reasoning_summary_part.* 与 summary_text part。
//
// 按 OpenAI 规范实现的客户端（Codex CLI 等）只读 summary / summary 系事件，
// 导致经 OpenRouter 中转的推理模型思考内容不可见。本文件将上述形态归一化为规范形态。
//
// 所有改写均按数据形状触发：仅当事件名精确命中或 summary 为空且 content 全部为
// reasoning_text 时才改写，标准上游的响应不受影响，重复调用幂等。

// openRouterReasoningEventRenames 需要改名的非标准流式事件 -> 规范事件名。
var openRouterReasoningEventRenames = map[string]string{
	"response.reasoning_text.delta": "response.reasoning_summary_text.delta",
	"response.reasoning_text.done":  "response.reasoning_summary_text.done",
}

// NormalizeOpenRouterReasoningEvent 就地归一化单条 Responses SSE 事件，
// 返回是否发生改写。标准事件原样保留。
func NormalizeOpenRouterReasoningEvent(event map[string]interface{}) bool {
	eventType, _ := event["type"].(string)
	if eventType == "" {
		return false
	}

	switch eventType {
	case "response.output_item.added", "response.output_item.done":
		item, _ := event["item"].(map[string]interface{})
		if item == nil {
			return false
		}
		return normalizeResponsesReasoningItem(item)
	case "response.content_part.added", "response.content_part.done":
		part, ok := event["part"].(map[string]interface{})
		if !ok {
			return false
		}
		if partType, _ := part["type"].(string); partType != "reasoning_text" {
			return false
		}
		suffix := strings.TrimPrefix(eventType, "response.content_part.")
		part["type"] = "summary_text"
		event["type"] = "response.reasoning_summary_part." + suffix
		renameContentIndexToSummaryIndex(event)
		return true
	default:
		renamed, ok := openRouterReasoningEventRenames[eventType]
		if !ok {
			return false
		}
		event["type"] = renamed
		renameContentIndexToSummaryIndex(event)
		return true
	}
}

// NormalizeOpenRouterReasoningResponseBody 就地归一化 Responses 非流式响应体中
// reasoning 条目的非标准 content 形态，返回是否发生改写。
func NormalizeOpenRouterReasoningResponseBody(body map[string]interface{}) bool {
	output, ok := body["output"].([]interface{})
	if !ok {
		return false
	}
	changed := false
	for _, rawItem := range output {
		if item, ok := rawItem.(map[string]interface{}); ok && normalizeResponsesReasoningItem(item) {
			changed = true
		}
	}
	return changed
}

// NormalizeOpenRouterReasoningSSELine 对透传中继路径的单行 SSE 做归一化。
// 仅 data 行且负载含 reasoning 字样时才解析 JSON，其余行原样返回以保持零开销。
func NormalizeOpenRouterReasoningSSELine(line string) string {
	payload, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
	if !ok || !strings.Contains(payload, "reasoning") {
		return line
	}
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &event); err != nil {
		return line
	}
	if !NormalizeOpenRouterReasoningEvent(event) {
		return line
	}
	normalized, err := json.Marshal(event)
	if err != nil {
		return line
	}
	return "data: " + string(normalized)
}

// normalizeResponsesReasoningItem 将 reasoning 条目中非标准的 content[reasoning_text]
// 迁移为规范的 summary[summary_text]，返回是否发生改写。
// summary 已有内容、content 含未知 part 类型时保守跳过，避免破坏异构上游的自定义扩展。
func normalizeResponsesReasoningItem(item map[string]interface{}) bool {
	if itemType, _ := item["type"].(string); itemType != "reasoning" {
		return false
	}
	if summary, ok := item["summary"].([]interface{}); ok && len(summary) > 0 {
		return false
	}
	content, ok := item["content"].([]interface{})
	if !ok || len(content) == 0 {
		return false
	}
	parts := make([]interface{}, 0, len(content))
	for _, rawPart := range content {
		part, ok := rawPart.(map[string]interface{})
		if !ok {
			return false
		}
		if partType, _ := part["type"].(string); partType != "reasoning_text" {
			return false
		}
		parts = append(parts, map[string]interface{}{"type": "summary_text", "text": part["text"]})
	}
	item["summary"] = parts
	delete(item, "content")
	return true
}

// renameContentIndexToSummaryIndex 规范的 summary 系事件用 summary_index 标记分片序号，
// OpenRouter 复用了 content_index；语义相同，改名即可。
func renameContentIndexToSummaryIndex(event map[string]interface{}) {
	if _, has := event["summary_index"]; has {
		return
	}
	if idx, ok := event["content_index"]; ok {
		event["summary_index"] = idx
		delete(event, "content_index")
	}
}
