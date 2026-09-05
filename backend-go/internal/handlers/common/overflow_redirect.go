package common

import (
	"encoding/json"
)

// stripResponsesEncryptedContent 剥离 Responses 请求 input items 中 reasoning
// 项的 encrypted_content（保留 summary）。跨模型改写后上游无法解密其他模型
// 产生的密文；保留 summary 是语义损失最小的降级，与 chat 转换器的处理对齐。
// 解析失败时原样返回：剥离是尽力而为，不阻断发送。
func stripResponsesEncryptedContent(body []byte) []byte {
	var req struct {
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	var items []json.RawMessage
	if json.Unmarshal(req.Input, &items) != nil {
		// input 是字符串或其他非数组形态：没有 reasoning items 可剥
		return body
	}
	modified := false
	for i, item := range items {
		var m map[string]json.RawMessage
		if json.Unmarshal(item, &m) != nil {
			continue
		}
		var itemType string
		if raw, ok := m["type"]; ok {
			_ = json.Unmarshal(raw, &itemType)
		}
		if itemType != "reasoning" {
			continue
		}
		if _, has := m["encrypted_content"]; !has {
			continue
		}
		delete(m, "encrypted_content")
		rewritten, err := json.Marshal(m)
		if err != nil {
			continue
		}
		items[i] = rewritten
		modified = true
	}
	if !modified {
		return body
	}
	newItems, err := json.Marshal(items)
	if err != nil {
		return body
	}
	// 用偏移量替换 "input" 数组，避免整体序列化改动键序
	return replaceJSONField(body, "input", newItems)
}

// replaceJSONField 在 JSON 对象顶层替换指定键的值为新原始 JSON。
// 解析失败时返回原 body。
func replaceJSONField(body []byte, field string, value json.RawMessage) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	m[field] = value
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}
