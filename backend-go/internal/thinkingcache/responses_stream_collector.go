package thinkingcache

import "strings"

// ResponsesStreamCollector 从 Responses 协议 SSE 流中收集 reasoning item 的
// encrypted_content，供后续续接重放使用。与 ClaudeStreamCollector 互补：
// 后者收集 Claude thinking 纯文本，前者收集 Responses reasoning 加密快照。
type ResponsesStreamCollector struct {
	reasoningItems []responsesReasoningEntry
}

type responsesReasoningEntry struct {
	itemID           string
	encryptedContent string
}

// NewResponsesStreamCollector 创建一个 Responses reasoning 收集器。
func NewResponsesStreamCollector() *ResponsesStreamCollector {
	return &ResponsesStreamCollector{}
}

// ProcessEvent 解析单个 Responses SSE 事件，从 response.output_item.done 中
// 提取 type=="reasoning" 且携带 encrypted_content 的 item。
func (c *ResponsesStreamCollector) ProcessEvent(event string) {
	if c == nil {
		return
	}
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}
		data, ok := decodeEventObject(jsonStr)
		if !ok {
			continue
		}
		eventType, _ := data["type"].(string)
		if eventType != "response.output_item.done" {
			continue
		}
		item, ok := data["item"].(map[string]interface{})
		if !ok {
			continue
		}
		itemType, _ := item["type"].(string)
		if itemType != "reasoning" {
			continue
		}
		encryptedContent, _ := item["encrypted_content"].(string)
		if strings.TrimSpace(encryptedContent) == "" {
			continue
		}
		itemID, _ := item["id"].(string)
		if itemID == "" {
			// 无 id 的 reasoning item 无法被后续按 id 取回，跳过
			continue
		}
		c.reasoningItems = append(c.reasoningItems, responsesReasoningEntry{
			itemID:           itemID,
			encryptedContent: encryptedContent,
		})
	}
}

// Store 将收集到的 reasoning encrypted_content 写入 thinkingcache，
// 按 sessionID + itemID 索引。返回成功存储的条目数。
func (c *ResponsesStreamCollector) Store(sessionID string) int {
	if c == nil || strings.TrimSpace(sessionID) == "" {
		return 0
	}
	stored := 0
	for _, entry := range c.reasoningItems {
		if StoreResponsesReasoning(sessionID, entry.itemID, entry.encryptedContent) {
			stored++
		}
	}
	return stored
}

// ReasoningCount 返回已收集的 reasoning 条目数（主要用于测试与日志）。
func (c *ResponsesStreamCollector) ReasoningCount() int {
	if c == nil {
		return 0
	}
	return len(c.reasoningItems)
}
