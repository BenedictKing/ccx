package utils

import (
	"encoding/json"
	"unicode"

	"github.com/BenedictKing/ccx/internal/types"
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
//     但 data 已是占位符 "<image>"，mediaPayloadFromBlock 跳过，必返回 0 图片 token，不重复计图。
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
	cleaned, imageTokens := extractImageTokensAndStripBytes(data)

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
	cleaned, imageTokens := extractImageTokensAndStripBytes(bodyBytes)

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

	// tools (按实际 schema 估算)
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
	cleaned, imageTokens := extractImageTokensAndStripBytes(bodyBytes)
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
	cleaned, imageTokens := extractImageTokensAndStripBytes(bodyBytes)

	var req map[string]interface{}
	if err := json.Unmarshal(cleaned, &req); err != nil {
		return EstimateTokens(string(cleaned)) + imageTokens
	}

	total := imageTokens

	// instructions (系统指令)
	if instructions, ok := req["instructions"].(string); ok {
		total += EstimateTokens(instructions)
	}

	// input 字段处理：先通过 ParseResponsesInput 归一化，再统一走 typed item 计数器
	if input := req["input"]; input != nil {
		// 直接解析 input，而非整个 request
		items, err := types.ParseResponsesInput(input)
		if err == nil && len(items) > 0 {
			total += estimateResponsesItemsTokens(items, true)
		} else {
			// 归一化失败：退回到保守方案
			data, _ := json.Marshal(input)
			total += EstimateTokens(string(data))
		}
	}

	// tools (按实际 schema 估算)
	if tools, ok := req["tools"].([]interface{}); ok {
		total += estimateToolsTokens(tools)
	}

	return total
}

// EstimateResponsesOutputTokens 从 Responses API 响应估算输出 token
// 支持 []ResponsesItem 格式
func EstimateResponsesOutputTokens(output interface{}) int {
	if output == nil {
		return 0
	}

	// 处理 []types.ResponsesItem 类型
	if items, ok := output.([]types.ResponsesItem); ok {
		return estimateResponsesItemsTokens(items, false)
	}

	// 处理 []interface{} 类型：先归一化再走统一计数器
	if arr, ok := output.([]interface{}); ok {
		items, err := types.ParseResponsesInput(arr)
		if err == nil && len(items) > 0 {
			return estimateResponsesItemsTokens(items, false)
		}
		// 归一化失败：退回到保守方案
		total := 0
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if content := m["content"]; content != nil {
					total += estimateContentTokens(content)
				}
				if toolUse, ok := m["tool_use"].(map[string]interface{}); ok {
					data, _ := json.Marshal(toolUse)
					total += EstimateTokens(string(data))
				}
			}
		}
		return total
	}

	// 其他情况序列化后估算
	data, err := json.Marshal(output)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(data))
}

// ============== 内部辅助函数 ==============

// estimateResponsesItemsTokens 估算 ResponsesItem 数组的 token
// isRequest 表示是否用于 request side（会加 item framing）
func estimateResponsesItemsTokens(items []types.ResponsesItem, isRequest bool) int {
	total := 0
	for _, item := range items {
		total += estimateResponsesItemTokens(item, isRequest)
	}
	return total
}

// estimateResponsesItemTokens 估算单个 ResponsesItem 的 token 数
// 注意：通过 ParseResponsesInput/NormalizeResponsesItem 保证已知 item 字段唯一归属，无重复计数。
// 不同时计 canonical 字段与 raw/marshal（除 unknown type fallback）。
func estimateResponsesItemTokens(item types.ResponsesItem, isRequest bool) int {
	total := 0

	// 结构开销（仅用于 request 路由估算，让不同表示开销一致）
	itemOverhead := 0
	if isRequest {
		itemOverhead = 4
	}
	total += itemOverhead

	// 按类型明确字段归属，优先计 canonical 字段而非整项 marshal
	switch item.Type {
	case "message", "text":
		// message/text：计 Content
		if item.Content != nil {
			total += estimateContentTokens(item.Content)
		}
		// 不重复计 ToolUse（NormalizeResponsesItem 会把 tool_call/tool_use 统一为 function_call，
		// 且清空 ToolUse/Content 避免重复）
		if item.ToolUse != nil {
			data, _ := json.Marshal(item.ToolUse)
			total += EstimateTokens(string(data))
		}

	case "reasoning", "compaction":
		// reasoning/compaction：计 Summary、EncryptedContent
		if item.Summary != nil {
			if s, ok := item.Summary.(string); ok {
				total += EstimateTokens(s)
			} else {
				data, _ := json.Marshal(item.Summary)
				total += EstimateTokens(string(data))
			}
		}
		if item.EncryptedContent != "" {
			total += estimateOpaqueStringTokens(item.EncryptedContent)
		}
		// 即使有 Content/ToolUse 也不计（避免与 legacy/双重表示重复）

	case "function_call", "tool_call":
		// function_call：计 Name、Arguments、CallID
		if item.Name != "" {
			total += EstimateTokens(item.Name) + 2 // 函数名 + 小开销
		}
		if item.Arguments != "" {
			total += EstimateTokens(item.Arguments)
		}
		if item.CallID != "" {
			total += EstimateTokens(item.CallID)
		}
		// 不重复计 Content/ToolUse（NormalizeResponsesItem 会清空它们）
		if item.Content != nil {
			// 但兼容：如果确实只有 Content 没有 Arguments，补计 Content
			if item.Name == "" && item.Arguments == "" {
				total += estimateContentTokens(item.Content)
			}
		}
		if item.ToolUse != nil {
			// 兼容：如果确实只有 ToolUse 没有 Name/Arguments，补计 ToolUse
			if item.Name == "" && item.Arguments == "" {
				data, _ := json.Marshal(item.ToolUse)
				total += EstimateTokens(string(data))
			}
		}

	case "function_call_output", "tool_result":
		// function_call_output：计 Output、CallID
		if item.Output != nil {
			if s, ok := item.Output.(string); ok {
				total += EstimateTokens(s)
			} else {
				data, _ := json.Marshal(item.Output)
				total += EstimateTokens(string(data))
			}
		}
		if item.CallID != "" {
			total += EstimateTokens(item.CallID)
		}
		// 不重复计 Content

	case "custom_tool_call", "custom_tool_result":
		// custom_tool_call：计 Name、Input、Namespace、CallID
		if item.Name != "" {
			total += EstimateTokens(item.Name) + 2
		}
		if item.Input != "" {
			total += EstimateTokens(item.Input)
		}
		if item.Namespace != "" {
			total += EstimateTokens(item.Namespace)
		}
		if item.CallID != "" {
			total += EstimateTokens(item.CallID)
		}
		// custom_tool_result：计 Output
		if item.Output != nil {
			if s, ok := item.Output.(string); ok {
				total += EstimateTokens(s)
			} else {
				data, _ := json.Marshal(item.Output)
				total += EstimateTokens(string(data))
			}
		}

	case "tool_search_call", "tool_search_result":
		// tool_search_call：计 Arguments、CallID
		if item.Arguments != "" {
			total += EstimateTokens(item.Arguments)
		}
		if item.CallID != "" {
			total += EstimateTokens(item.CallID)
		}
		// tool_search_result：计 Tools
		if len(item.Tools) > 0 {
			total += estimateToolsTokens(item.Tools)
		}

	case "computer_call", "computer_call_output",
		"local_shell_call", "local_shell_call_output",
		"web_search_call", "web_search_call_output":
		// computer/local_shell/web：计 Name、Arguments、Input、Output、CallID 等
		if item.Name != "" {
			total += EstimateTokens(item.Name) + 2
		}
		if item.Arguments != "" {
			total += EstimateTokens(item.Arguments)
		}
		if item.Input != "" {
			total += EstimateTokens(item.Input)
		}
		if item.Output != nil {
			if s, ok := item.Output.(string); ok {
				total += EstimateTokens(s)
			} else {
				data, _ := json.Marshal(item.Output)
				total += EstimateTokens(string(data))
			}
		}
		if item.CallID != "" {
			total += EstimateTokens(item.CallID)
		}
		if item.Status != "" {
			total += EstimateTokens(item.Status)
		}
		if item.Execution != "" {
			total += EstimateTokens(item.Execution)
		}

	default:
		// 未知类型：保守方案——整项 marshal（不与已知字段重复，前面已只加 itemOverhead）
		data, _ := json.Marshal(item)
		total += EstimateTokens(string(data))
	}

	// 不再用旧逻辑：旧逻辑先计 Content/ToolUse，total == 0 才整项 marshal。
	// 现在明确：已知类型只计对应字段，未知类型整项 marshal。

	return total
}

// estimateContentTokens 估算 content 字段的 token
func estimateContentTokens(content interface{}) int {
	if content == nil {
		return 0
	}
	switch v := content.(type) {
	case string:
		return EstimateTokens(v)
	case []interface{}:
		total := 0
		for _, block := range v {
			if b, ok := block.(map[string]interface{}); ok {
				hasText := false
				if textVal, ok := b["text"].(string); ok {
					total += EstimateTokens(textVal)
					hasText = true
				}
				// 其他 block 类型整体估算（但图片已在外层剥离并单独计过）
				if !hasText {
					data, _ := json.Marshal(b)
					total += EstimateTokens(string(data))
				}
			}
		}
		return total
	case []types.ContentBlock:
		total := 0
		for _, block := range v {
			if block.Text != "" {
				total += EstimateTokens(block.Text)
			}
		}
		return total
	default:
		data, err := json.Marshal(content)
		if err != nil {
			return 0
		}
		return EstimateTokens(string(data))
	}
}

// estimateToolsTokens 估算工具 schema 的 token
// 不再用固定 150 tokens/tool，按实际内容估算
func estimateToolsTokens(tools []interface{}) int {
	if len(tools) == 0 {
		return 0
	}
	total := 0
	for _, tool := range tools {
		data, err := json.Marshal(tool)
		if err != nil {
			continue
		}
		total += EstimateTokens(string(data)) + 8 // schema + 小开销
	}
	return total
}

// estimateOpaqueStringTokens 估算 opaque/encrypted 字符串的 token
// 加密/高熵内容不按普通英文比率低估，用更保守的算法
func estimateOpaqueStringTokens(s string) int {
	if s == "" {
		return 0
	}
	// 保守策略：对非空白字符统一用 ~1.5 字符/token（不区分 CJK/英文）
	nonSpace := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			nonSpace++
		}
	}
	tokens := float64(nonSpace) / 1.5
	return int(tokens + 0.5)
}
