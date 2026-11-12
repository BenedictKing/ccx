package utils

import (
	"encoding/json"
	"unicode"
)

// EstimateTokens 估算文本的 token 数量
// 使用字符估算法：
// - 中文/日文/韩文：约 1.5 字符/token
// - 英文及其他：约 3.5 字符/token
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	cjkCount := 0
	otherCount := 0

	for _, r := range text {
		if isCJK(r) {
			cjkCount++
		} else if !unicode.IsSpace(r) {
			otherCount++
		}
	}

	// CJK: ~1.5 字符/token, 其他: ~3.5 字符/token
	cjkTokens := float64(cjkCount) / 1.5
	otherTokens := float64(otherCount) / 3.5

	return int(cjkTokens + otherTokens + 0.5) // 四舍五入
}

// EstimateMessagesTokens 估算消息数组的 token 数量。
//
// 算法（marshal 后按字符估算 + 每条消息约 4 token + 图片 token）与
// EstimateRequestTokens 处理 messages 字段的逻辑一致——EstimateRequestTokens 直接复用本函数，
// 二者不再各持一份实现，避免漂移（DRY）。
// 对已剥离 base64 的输入复用本函数不会重复计图，但成本因 schema 而异：
//   - OpenAI data URL：整个 url 值连同 ";base64," 一起被替换成 "<image>"，"base64" 特征消失，
//     extractImageTokensAndStripBytes 直接短路，近乎零成本。
//   - Anthropic：仅 source.data 被替换，"type":"base64" 仍残留，不短路、会再做一次 gjson 全量解析；
//     但 data 已是占位符 "<image>"，imagePayloadFromBlock 跳过，必返回 0 图片 token，不重复计图。
func EstimateMessagesTokens(messages interface{}) int {
	if messages == nil {
		return 0
	}

	// 序列化为 JSON 后估算
	data, err := json.Marshal(messages)
	if err != nil {
		return 0
	}

	// 用 gjson 提取图片 token 并把 base64 字段替换成占位符，避免按字符数高估
	cleaned, imageTokens := extractMediaTokensAndStripBytes(data)

	// 每条消息额外开销约 4 tokens
	msgCount := 0
	if arr, ok := messages.([]interface{}); ok {
		msgCount = len(arr)
	}

	return EstimateTokens(string(cleaned)) + msgCount*4 + imageTokens
}

// EstimateRequestTokens 从请求体估算输入 token。
//
// 注意：这是「路由用的保守上界估算」，非计费精度。结果在上游不回 usage 时会回填给客户端，
// 而图片按 Qwen3-VL 16384 上界估算，对 OpenAI/Anthropic 实际计费可能偏高（详见 image_tokens.go）。
func EstimateRequestTokens(bodyBytes []byte) int {
	if len(bodyBytes) == 0 {
		return 0
	}

	// 提取图片 token 并把 base64 字段替换成占位符，后续按 cleaned 估算文本，
	// 图片 token 在此处一次性计入；下方 messages 复用 EstimateMessagesTokens，
	// 其对已剥离的 cleaned 会短路、返回 0 图片 token，不会重复计图。
	cleaned, imageTokens := extractMediaTokensAndStripBytes(bodyBytes)

	var req map[string]interface{}
	if err := json.Unmarshal(cleaned, &req); err != nil {
		return EstimateTokens(string(cleaned)) + imageTokens
	}

	total := imageTokens

	// system prompt
	if system, ok := req["system"]; ok {
		if str, ok := system.(string); ok {
			total += EstimateTokens(str)
		} else if arr, ok := system.([]interface{}); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]interface{}); ok {
					if text, ok := m["text"].(string); ok {
						total += EstimateTokens(text)
					}
				}
			}
		}
	}

	// messages：cleaned 里的 base64 已剥离，复用 EstimateMessagesTokens 统一算法。
	if messages, ok := req["messages"].([]interface{}); ok {
		total += EstimateMessagesTokens(messages)
	}

	// tools (每个工具约 100-200 tokens)
	if tools, ok := req["tools"].([]interface{}); ok {
		total += estimateToolsTokens(tools)
	}

	return total
}

// EstimateGeminiRequestTokens 从 Gemini 请求体估算输入 token。
//
// Gemini 内联图在 contents[].parts[].inlineData(.data) 下，需先经
// extractImageTokensAndStripBytes 把 base64 剥离、按真实尺寸计图，
// 否则大图会被当文本字符数高估而撞穿 scheduler 阈值导致 503（与其它三种 schema 同一类 bug）。
// 同样是「路由用的保守上界估算」，非计费精度（详见 image_tokens.go）。
//
// 与 chat/messages 路径不同，这里刻意只算「剥离后文本 + 图片 token」，不叠加每条消息约 4 token
// 的结构开销：Gemini 输入主体是图片与长文本，结构开销占比极小，省略它既不影响路由判断
// （保守上界 + 上层另计 thinkingBudget），也避免为不同 schema 维护各自的开销系数。
func EstimateGeminiRequestTokens(bodyBytes []byte) int {
	if len(bodyBytes) == 0 {
		return 0
	}
	cleaned, imageTokens := extractMediaTokensAndStripBytes(bodyBytes)
	return EstimateTokens(string(cleaned)) + imageTokens
}

// EstimateResponseTokens 从响应内容估算输出 token
func EstimateResponseTokens(content interface{}) int {
	if content == nil {
		return 0
	}

	// 字符串内容
	if str, ok := content.(string); ok {
		return EstimateTokens(str)
	}

	// 内容数组
	if arr, ok := content.([]interface{}); ok {
		total := 0
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					total += EstimateTokens(text)
				}
				// tool_use 的 input 也计入
				if input, ok := m["input"]; ok {
					data, _ := json.Marshal(input)
					total += EstimateTokens(string(data))
				}
			}
		}
		return total
	}

	// 其他情况序列化后估算
	data, err := json.Marshal(content)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(data))
}

// isCJK 判断是否为中日韩字符
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// ============== Responses API Token 估算 ==============

// EstimateResponsesRequestTokens 从 Responses API 请求体估算输入 token
// 支持 instructions、input (string 或 []item) 格式
//
// 注意：这是「路由用的保守上界估算」，非计费精度。结果在上游不回 usage 时会回填给客户端，
// 而图片按 Qwen3-VL 16384 上界估算，对 OpenAI/Anthropic 实际计费可能偏高（详见 image_tokens.go）。
func EstimateResponsesRequestTokens(bodyBytes []byte) int {
	if len(bodyBytes) == 0 {
		return 0
	}

	// 先用 gjson 提取图片 token 并把 base64 字段清空
	cleaned, imageTokens := extractMediaTokensAndStripBytes(bodyBytes)

	var req map[string]interface{}
	if err := json.Unmarshal(cleaned, &req); err != nil {
		return EstimateTokens(string(cleaned)) + imageTokens
	}

	total := imageTokens

	// instructions (系统指令)
	if instructions, ok := req["instructions"].(string); ok {
		total += EstimateTokens(instructions)
	}

	// input 字段处理
	if input := req["input"]; input != nil {
		total += estimateResponsesInputTokens(input)
	}

	// tools (每个工具约 100-200 tokens)
	if tools, ok := req["tools"].([]interface{}); ok {
		total += estimateToolsTokens(tools)
	}

	return total
}

// estimateResponsesInputTokens 估算 Responses input 字段的 token
func estimateResponsesInputTokens(input interface{}) int {
	if text, ok := input.(string); ok {
		return EstimateTokens(text)
	}
	data, err := json.Marshal(input)
	if err != nil {
		return 0
	}
	var items []map[string]interface{}
	if json.Unmarshal(data, &items) != nil {
		return EstimateTokens(string(data))
	}
	total := 0
	for _, item := range items {
		// 保留未知 item 的所有字段，避免先转窄类型而丢掉新协议负载。
		total += 4 + estimateResponseItemMapTokens(item)
	}
	return total
}

// estimateResponseItemMapTokens 对密文单独估算，其他字段按实际负载计数。
func estimateResponseItemMapTokens(item map[string]interface{}) int {
	total := 0
	plain := make(map[string]interface{}, len(item))
	for key, value := range item {
		switch key {
		case "encrypted_content":
			if text, ok := value.(string); ok {
				total += estimateOpaqueStringTokens(text)
				continue
			}
		case "encrypted_function_args":
			if segments, ok := value.([]interface{}); ok {
				for _, segment := range segments {
					if text, ok := segment.(string); ok {
						total += estimateOpaqueStringTokens(text)
					}
				}
				continue
			}
		}
		plain[key] = value
	}
	data, _ := json.Marshal(plain)
	return total + EstimateTokens(string(data))
}

func estimateToolsTokens(tools []interface{}) int {
	total := 0
	for _, tool := range tools {
		data, err := json.Marshal(tool)
		if err == nil {
			total += 8 + EstimateTokens(string(data))
		}
	}
	return total
}

func estimateOpaqueStringTokens(text string) int {
	count := 0
	for _, r := range text {
		if !unicode.IsSpace(r) {
			count++
		}
	}
	return (count*2 + 2) / 3
}

// EstimateResponsesOutputTokens 从 Responses API 响应估算输出 token
// 支持 []ResponsesItem 格式
func EstimateResponsesOutputTokens(output interface{}) int {
	if output == nil {
		return 0
	}
	data, err := json.Marshal(output)
	if err != nil {
		return 0
	}
	var items []map[string]interface{}
	if json.Unmarshal(data, &items) != nil {
		return EstimateTokens(string(data))
	}
	total := 0
	for _, item := range items {
		total += estimateResponseItemMapTokens(item)
	}
	return total
}
