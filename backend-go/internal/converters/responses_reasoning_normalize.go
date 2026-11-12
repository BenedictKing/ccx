package converters

import (
	"encoding/json"
	"strings"
)

// 部分兼容上游使用 content/reasoning_text 承载思考，summary 留空。
//  1. 非流式：reasoning 输出条目把思考文本放在非标准 content[]（part 类型 reasoning_text）里，
//     而规范位置 summary[] 留空；
//  2. 流式：思考增量使用 response.reasoning_text.delta/.done，
//     且 content_part.added/.done 携带 reasoning_text part，
//     而非规范的 response.reasoning_summary_text.* / response.reasoning_summary_part.* 与 summary_text part。
//
// 仅消费 summary 系事件的客户端看不到上述思考内容，因此归一化为 summary 形态。
// reasoning_text 本身也是当前 OpenAI API 的事件类型，此处为客户端兼容转换。
//
// 所有改写均按数据形状触发：仅当事件名精确命中或 summary 为空且 content 全部为
// reasoning_text 时才改写，已有 summary 的响应保持不变，重复调用幂等。

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
	case "response.completed", "response.incomplete", "response.failed":
		response, _ := event["response"].(map[string]interface{})
		return NormalizeOpenRouterReasoningResponseBody(response)
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
	if event, ok := strings.CutPrefix(strings.TrimSpace(line), "event:"); ok {
		if renamed, found := openRouterReasoningEventRenames[strings.TrimSpace(event)]; found {
			return "event: " + renamed
		}
	}
	payload, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
	if !ok || !strings.Contains(payload, "reasoning") {
		return line
	}
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &event); err != nil {
		return line
	}
	originalType, _ := event["type"].(string)
	if !NormalizeOpenRouterReasoningEvent(event) {
		return line
	}
	normalized, err := json.Marshal(event)
	if err != nil {
		return line
	}
	if strings.HasPrefix(originalType, "response.content_part.") {
		// 同帧已有 content_part event 字段时，以后出现的 event 字段覆盖它（SSE 规则）。
		return "event: " + event["type"].(string) + "\ndata: " + string(normalized)
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
		converted := make(map[string]interface{}, len(part))
		for key, value := range part {
			converted[key] = value
		}
		converted["type"] = "summary_text"
		parts = append(parts, converted)
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
