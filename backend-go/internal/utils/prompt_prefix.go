package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

// ── 匿名请求的内容指纹亲和回退 ──
//
// 背景：Trace 亲和依赖统一会话标识（请求头 / body user 字段 / metadata），
// 完全匿名的客户端（如裸 OpenAI SDK 调用）拿不到标识，亲和长期失效。
// 对话类协议每轮重发全量历史，其中 system 提示与首条 user 消息在整个会话内
// 保持不变，构成的规范指纹天然是稳定会话 ID。
//
// 蓝本参考：GPT-Load v2 internal/dialect/prompt_affinity.go（prompt-prefix HMAC）。
// 指纹返回 "pp:<16hex>"，进入 ExtractUnifiedSessionID 的既有 userID 通路，
// 亲和复合键、会话跟踪与日志标记全部复用，无需各 handler 改动。

const (
	// promptPrefixRoleBytes 单角色（system/user）参与指纹的文本预算。
	promptPrefixRoleBytes = 4 << 10
	// promptPrefixHexChars 指纹十六进制截断长度（64bit 碰撞空间，万级会话足够）。
	promptPrefixHexChars = 16
	// PromptPrefixIDPrefix 合成 ID 前缀，日志中可辨识来源。
	PromptPrefixIDPrefix = "pp:"
)

// promptAffinityFallbackDisabled 反向存储内容指纹回退的关闭状态：
// atomic.Bool 零值为 false，反向语义使未初始化（如单测）时默认启用，
// 与 SchedulerConfig.PromptAffinityFallbackEnabled 的 nil→默认开保持一致。
var promptAffinityFallbackDisabled atomic.Bool

// SetPromptAffinityFallback 设置内容指纹回退开关（main.go 在启动与配置热重载时调用）。
func SetPromptAffinityFallback(enabled bool) {
	promptAffinityFallbackDisabled.Store(!enabled)
}

// PromptAffinityFallbackEnabled 返回内容指纹回退当前是否启用。
func PromptAffinityFallbackEnabled() bool {
	return !promptAffinityFallbackDisabled.Load()
}

// promptPrefixFingerprint 是参与哈希的规范形态。使用固定字段序的结构体序列化，
// 保证同内容跨请求得到相同字节串。
type promptPrefixFingerprint struct {
	Version    int      `json:"v"`
	SystemRole string   `json:"system_role,omitempty"`
	System     []string `json:"system,omitempty"`
	User       []string `json:"user"`
}

// DerivePromptPrefixID 从已解析的请求体提取稳定对话指纹。
// req 为空或缺少可识别的对话结构时返回空串（调用方保持匿名语义不变）。
// 已有显式会话标识的请求不会走到这里（ExtractUnifiedSessionID 已提前返回）。
func DerivePromptPrefixID(req map[string]interface{}) string {
	if promptAffinityFallbackDisabled.Load() || len(req) == 0 {
		return ""
	}

	systemRole, system, user := promptPrefixFromRequest(req)
	if len(user) == 0 {
		return ""
	}
	system = boundPromptText(system, promptPrefixRoleBytes)
	user = boundPromptText(user, promptPrefixRoleBytes)
	if len(user) == 0 {
		return ""
	}
	if len(system) == 0 {
		systemRole = ""
	}

	encoded, err := json.Marshal(promptPrefixFingerprint{
		Version: 1, SystemRole: systemRole, System: system, User: user,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return PromptPrefixIDPrefix + hex.EncodeToString(sum[:])[:promptPrefixHexChars]
}

// promptPrefixFromRequest 按协议家族提取 (system 角色, system 文本段, 首条含文本的 user 文本段)。
// 覆盖 CCX 六类入口的请求体形状：messages(chat/anthropic 共用 messages 数组)、
// responses(instructions+input)、gemini(systemInstruction+contents)、images(prompt)、vectors(input)。
// input 字段的分派按元素形状：对象数组走 Responses item 解析，字符串/字符串数组按
// vectors 语义取首元素（Responses 的裸字符串 input 与 vectors 单串同形，语义一致）。
func promptPrefixFromRequest(req map[string]interface{}) (string, []string, []string) {
	// Gemini：systemInstruction + contents
	if contents, ok := req["contents"].([]interface{}); ok {
		return geminiPromptPrefix(req, contents)
	}
	// Chat / Anthropic Messages：messages 数组（含顶层 system 字段的 Anthropic 形状）
	if messages, ok := req["messages"].([]interface{}); ok {
		return messageListPromptPrefix(req, messages)
	}
	// Images：prompt 即用户输入
	if prompt, ok := req["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
		return "", nil, []string{prompt}
	}
	// Responses / Vectors 共用的 input 字段
	if raw, ok := req["input"]; ok {
		switch typed := raw.(type) {
		case string:
			if strings.TrimSpace(typed) == "" {
				return "", nil, nil
			}
			systemRole := ""
			system := contentTextParts(req["instructions"])
			if len(system) > 0 {
				systemRole = "instructions"
			}
			return systemRole, system, []string{typed}
		case []interface{}:
			if len(typed) == 0 {
				return "", nil, nil
			}
			if _, isObj := typed[0].(map[string]interface{}); isObj {
				return responsesPromptPrefix(req)
			}
			// 字符串数组（vectors）：首元素即用户文本
			if first, isStr := typed[0].(string); isStr && strings.TrimSpace(first) != "" {
				return "", nil, []string{first}
			}
			return "", nil, nil
		}
	}
	return "", nil, nil
}

// messageListPromptPrefix 处理 chat 与 anthropic messages 的 messages 数组。
// 跳过无文本段的消息（如 tool_result-only 的 user 轮），取首个含文本的 system 与 user。
func messageListPromptPrefix(req map[string]interface{}, messages []interface{}) (string, []string, []string) {
	systemRole := ""
	var system []string
	if sysParts := anthropicSystemParts(req); len(sysParts) > 0 {
		systemRole = "system"
		system = sysParts
	}
	for _, item := range messages {
		msg, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringField(msg, "role")))
		parts := contentTextParts(msg["content"])
		if len(parts) == 0 {
			continue
		}
		switch role {
		case "system", "developer":
			if len(system) == 0 {
				systemRole = role
				system = parts
			}
		case "user":
			return systemRole, system, parts
		}
	}
	return systemRole, system, nil
}

// anthropicSystemParts 提取 Anthropic messages 的顶层 system 字段（string 或内容块数组）。
func anthropicSystemParts(req map[string]interface{}) []string {
	raw, ok := req["system"]
	if !ok {
		return nil
	}
	return contentTextParts(raw)
}

// responsesPromptPrefix 处理 Responses 形状：instructions + input（string 或 item 数组）。
func responsesPromptPrefix(req map[string]interface{}) (string, []string, []string) {
	systemRole := "instructions"
	system := contentTextParts(req["instructions"])

	if direct, ok := req["input"].(string); ok {
		if strings.TrimSpace(direct) != "" {
			return systemRole, system, []string{direct}
		}
		return systemRole, system, nil
	}
	items, ok := req["input"].([]interface{})
	if !ok {
		return systemRole, system, nil
	}
	for _, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringField(entry, "role")))
		parts := contentTextParts(entry["content"])
		if len(parts) == 0 {
			// Responses item 可能用顶层 text 字段（easy input message 形状）
			if text := strings.TrimSpace(stringField(entry, "text")); text != "" {
				parts = []string{text}
			}
		}
		if len(parts) == 0 {
			continue
		}
		switch role {
		case "system", "developer":
			if len(system) == 0 {
				systemRole = role
				system = parts
			}
		case "user":
			return systemRole, system, parts
		case "":
			itemType := normalizeResponsesItemType(stringField(entry, "type"))
			if itemType == "input_text" || itemType == "text" || itemType == "message" {
				return systemRole, system, parts
			}
		}
	}
	return systemRole, system, nil
}

// geminiPromptPrefix 处理 Gemini 形状：systemInstruction（camel/snake）+ contents。
func geminiPromptPrefix(req map[string]interface{}, contents []interface{}) (string, []string, []string) {
	systemInstruction, ok := req["systemInstruction"]
	if !ok {
		systemInstruction = req["system_instruction"]
	}
	systemRole := ""
	var system []string
	if sysParts := geminiContentParts(systemInstruction); len(sysParts) > 0 {
		systemRole = "system"
		system = sysParts
	}
	for _, item := range contents {
		content, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringField(content, "role")))
		if role != "" && role != "user" {
			continue
		}
		parts := contentTextParts(content["parts"])
		if len(parts) > 0 {
			return systemRole, system, parts
		}
	}
	return systemRole, system, nil
}

// geminiContentParts 提取 Gemini content 对象（{role, parts}）内的文本段。
func geminiContentParts(raw interface{}) []string {
	content, ok := raw.(map[string]interface{})
	if !ok {
		if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
			return []string{s}
		}
		return nil
	}
	return contentTextParts(content["parts"])
}

// contentTextParts 从 content 字段提取文本段，兼容三种形状：
// 纯字符串；字符串数组；内容块数组（取各块的 text 字段）。
func contentTextParts(raw interface{}) []string {
	switch typed := raw.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	case []interface{}:
		parts := make([]string, 0, len(typed))
		for _, block := range typed {
			switch b := block.(type) {
			case string:
				if strings.TrimSpace(b) != "" {
					parts = append(parts, b)
				}
			case map[string]interface{}:
				if text, ok := b["text"].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			}
		}
		if len(parts) == 0 {
			return nil
		}
		return parts
	default:
		return nil
	}
}

// boundPromptText 在总字节预算内截断文本段（UTF-8 安全），超预算的最后一段截断保留。
func boundPromptText(source []string, limit int) []string {
	if limit <= 0 || len(source) == 0 {
		return nil
	}
	remaining := limit
	result := make([]string, 0, len(source))
	for _, value := range source {
		if strings.TrimSpace(value) == "" || remaining == 0 {
			continue
		}
		if len(value) > remaining {
			value = truncateUTF8(value, remaining)
		}
		if value == "" {
			continue
		}
		result = append(result, value)
		remaining -= len(value)
	}
	return result
}

// truncateUTF8 按字节长度截断并保证 UTF-8 边界完整。
func truncateUTF8(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	if limit <= 0 {
		return ""
	}
	value = value[:limit]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func stringField(m map[string]interface{}, key string) string {
	s, _ := m[key].(string)
	return s
}

func normalizeResponsesItemType(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", ""))
}
