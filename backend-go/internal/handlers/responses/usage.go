package responses

import (
	"encoding/json"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
)

func checkResponsesEventUsage(event string, enableLog bool) (bool, bool, responsesStreamUsage) {
	return checkResponsesEventUsageWithLogTag(event, enableLog, "")
}

func checkResponsesEventUsageWithLogTag(event string, enableLog bool, logTag string) (bool, bool, responsesStreamUsage) {
	lines := strings.Split(event, "\n")
	for _, line := range lines {
		// 支持 "data:" 和 "data: " 两种格式（有些上游不带空格）
		var jsonStr string
		if strings.HasPrefix(line, "data:") {
			jsonStr = strings.TrimPrefix(line, "data:")
			jsonStr = strings.TrimPrefix(jsonStr, " ") // 移除可能的前导空格
		} else {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)

		// 检查 response.completed 事件中的 usage
		if eventType == "response.completed" {
			if response, ok := data["response"].(map[string]interface{}); ok {
				if usage, ok := response["usage"].(map[string]interface{}); ok {
					usageData := extractResponsesUsageFromMap(usage)
					needPatch := usageData.InputTokens <= 1 || usageData.OutputTokens <= 1

					// 仅当检测到 Claude 原生缓存字段时，才跳过 input_tokens 补全
					// OpenAI 的 input_tokens_details.cached_tokens 不应阻止补全
					if usageData.HasClaudeCache && usageData.InputTokens <= 1 {
						needPatch = usageData.OutputTokens <= 1 // 有 Claude 缓存时只检查 output
					}

					// 检查 total_tokens 是否需要补全（有效 input/output 但 total=0）
					if !needPatch && usageData.TotalTokens == 0 && (usageData.InputTokens > 0 || usageData.OutputTokens > 0) {
						needPatch = true
					}

					if enableLog {
						common.LogWithTag(logTag, "[Responses-Stream-Token] response.completed: InputTokens=%d, OutputTokens=%d, TotalTokens=%d, CacheCreation=%d, CacheRead=%d, HasClaudeCache=%v, 需补全=%v",
							usageData.InputTokens, usageData.OutputTokens, usageData.TotalTokens, usageData.CacheCreationInputTokens, usageData.CacheReadInputTokens, usageData.HasClaudeCache, needPatch)
					}
					return true, needPatch, usageData
				} else if enableLog {
					common.LogWithTag(logTag, "[Responses-Stream-Token] response.completed 事件中无 usage 字段")
				}
			} else if enableLog {
				common.LogWithTag(logTag, "[Responses-Stream-Token] response.completed 事件中无 response 字段")
			}
		}
	}
	return false, false, responsesStreamUsage{}
}

// extractResponsesUsageFromMap 从 usage map 中提取数据
func extractResponsesUsageFromMap(usage map[string]interface{}) responsesStreamUsage {
	var data responsesStreamUsage

	if v, ok := usage["input_tokens"].(float64); ok {
		data.InputTokens = int(v)
	}
	if v, ok := usage["output_tokens"].(float64); ok {
		data.OutputTokens = int(v)
	}
	if v, ok := usage["total_tokens"].(float64); ok {
		data.TotalTokens = int(v)
	}
	if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
		data.CacheCreationInputTokens = int(v)
		if v > 0 {
			data.HasClaudeCache = true
		}
	}
	if v, ok := usage["cache_read_input_tokens"].(float64); ok {
		data.CacheReadInputTokens = int(v)
		if v > 0 {
			data.HasClaudeCache = true
		}
	}
	if v, ok := usage["cache_creation_5m_input_tokens"].(float64); ok {
		data.CacheCreation5mInputTokens = int(v)
		if v > 0 {
			data.HasClaudeCache = true
		}
	}
	if v, ok := usage["cache_creation_1h_input_tokens"].(float64); ok {
		data.CacheCreation1hInputTokens = int(v)
		if v > 0 {
			data.HasClaudeCache = true
		}
	}

	// 检查 input_tokens_details.cached_tokens (OpenAI 格式，不设置 HasClaudeCache)
	if details, ok := usage["input_tokens_details"].(map[string]interface{}); ok {
		if cached, ok := details["cached_tokens"].(float64); ok && cached > 0 {
			// 仅当 CacheReadInputTokens 未被设置时才使用 OpenAI 的 cached_tokens
			if data.CacheReadInputTokens == 0 {
				data.CacheReadInputTokens = int(cached)
			}
			// 注意：不设置 HasClaudeCache，因为这是 OpenAI 格式
		}
	}

	// 设置 CacheTTL
	var has5m, has1h bool
	if data.CacheCreation5mInputTokens > 0 {
		has5m = true
	}
	if data.CacheCreation1hInputTokens > 0 {
		has1h = true
	}
	if has5m && has1h {
		data.CacheTTL = "mixed"
	} else if has1h {
		data.CacheTTL = "1h"
	} else if has5m {
		data.CacheTTL = "5m"
	}

	return data
}

// updateResponsesStreamUsage 更新收集的 usage 数据
func updateResponsesStreamUsage(collected *responsesStreamUsage, usageData responsesStreamUsage) {
	if usageData.InputTokens > collected.InputTokens {
		collected.InputTokens = usageData.InputTokens
	}
	if usageData.OutputTokens > collected.OutputTokens {
		collected.OutputTokens = usageData.OutputTokens
	}
	if usageData.TotalTokens > collected.TotalTokens {
		collected.TotalTokens = usageData.TotalTokens
	}
	if usageData.CacheCreationInputTokens > 0 {
		collected.CacheCreationInputTokens = usageData.CacheCreationInputTokens
	}
	if usageData.CacheReadInputTokens > 0 {
		collected.CacheReadInputTokens = usageData.CacheReadInputTokens
	}
	if usageData.CacheCreation5mInputTokens > 0 {
		collected.CacheCreation5mInputTokens = usageData.CacheCreation5mInputTokens
	}
	if usageData.CacheCreation1hInputTokens > 0 {
		collected.CacheCreation1hInputTokens = usageData.CacheCreation1hInputTokens
	}
	if usageData.CacheTTL != "" {
		collected.CacheTTL = usageData.CacheTTL
	}
	// 传播 HasClaudeCache 标志
	if usageData.HasClaudeCache {
		collected.HasClaudeCache = true
	}
}

// isResponsesCompletedEvent 检测是否为 response.completed 事件
func isResponsesCompletedEvent(event string) bool {
	return strings.Contains(event, `"type":"response.completed"`) ||
		strings.Contains(event, `"type": "response.completed"`)
}

// isClientDisconnectError 判断是否为客户端断开连接错误
func isClientDisconnectError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "context canceled")
}

func effectiveCacheCreationTokens(cacheCreation, cacheCreation5m, cacheCreation1h int) int {
	if cacheCreation > 0 {
		return cacheCreation
	}
	return cacheCreation5m + cacheCreation1h
}

// openAIInclusiveCacheAddend 汇总缓存读 + 有效缓存写 token。
// 协议转换器内部按"不含缓存"口径输出 input_tokens，而官方 OpenAI Responses 语义要求
// input_tokens 已包含缓存命中部分，出口归一时把这部分加回。
func openAIInclusiveCacheAddend(cacheRead, cacheCreation, cacheCreation5m, cacheCreation1h int) int {
	addend := cacheRead + effectiveCacheCreationTokens(cacheCreation, cacheCreation5m, cacheCreation1h)
	if addend < 0 {
		return 0
	}
	return addend
}

// normalizeResponsesUsageToOpenAISemantics 把非流式 Responses usage 从内部"不含缓存"口径
// 归一为官方 OpenAI 口径（input_tokens 含缓存、total_tokens = input + output）。
// 仅用于协议转换路径；透传路径的上游本来就是官方口径，不得调用（会重复加回）。
func normalizeResponsesUsageToOpenAISemantics(usage *types.ResponsesUsage) {
	if usage == nil {
		return
	}
	cacheRead := usage.CacheReadInputTokens
	if cacheRead == 0 && usage.InputTokensDetails != nil {
		cacheRead = usage.InputTokensDetails.CachedTokens
	}
	usage.InputTokens += openAIInclusiveCacheAddend(cacheRead, usage.CacheCreationInputTokens, usage.CacheCreation5mInputTokens, usage.CacheCreation1hInputTokens)
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
}

func jsonIntFromMap(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

// normalizeCompletedEventUsageToOpenAISemantics 在流式 response.completed 事件出口处
// 把 usage 归一为官方 OpenAI 口径，避免按官方语义计算"未命中输入 = input_tokens - cached_tokens"
// 的客户端在缓存占比过半时得到负数（#274）。仅用于协议转换路径。
func normalizeCompletedEventUsageToOpenAISemantics(event string) string {
	if !isResponsesCompletedEvent(event) {
		return event
	}
	lines := strings.Split(event, "\n")
	var result strings.Builder
	for i, line := range lines {
		result.WriteString(normalizeCompletedUsageDataLine(line))
		if i < len(lines)-1 {
			result.WriteString("\n")
		}
	}
	return result.String()
}

// normalizeCompletedUsageDataLine 改写单个 data: 行中的 completed usage；
// 非 data 行、非 completed 事件、无 usage 时原样返回。
func normalizeCompletedUsageDataLine(line string) string {
	if !strings.HasPrefix(line, "data:") {
		return line
	}
	jsonStr := strings.TrimPrefix(line, "data:")
	jsonStr = strings.TrimPrefix(jsonStr, " ")

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return line
	}
	if data["type"] != "response.completed" {
		return line
	}
	response, ok := data["response"].(map[string]interface{})
	if !ok {
		return line
	}
	usage, ok := response["usage"].(map[string]interface{})
	if !ok {
		return line
	}

	inputTokens := jsonIntFromMap(usage, "input_tokens")
	outputTokens := jsonIntFromMap(usage, "output_tokens")
	cacheRead := jsonIntFromMap(usage, "cache_read_input_tokens")
	if cacheRead == 0 {
		if details, ok := usage["input_tokens_details"].(map[string]interface{}); ok {
			cacheRead = jsonIntFromMap(details, "cached_tokens")
		}
	}
	cacheCreation := jsonIntFromMap(usage, "cache_creation_input_tokens")
	if cacheCreation == 0 {
		cacheCreation = jsonIntFromMap(usage, "cache_creation_5m_input_tokens") + jsonIntFromMap(usage, "cache_creation_1h_input_tokens")
	}
	addend := openAIInclusiveCacheAddend(cacheRead, cacheCreation, 0, 0)
	if addend > 0 {
		usage["input_tokens"] = float64(inputTokens + addend)
	}
	usage["total_tokens"] = float64(inputTokens + addend + outputTokens)

	patchedJSON, err := json.Marshal(data)
	if err != nil {
		return line
	}
	return "data: " + string(patchedJSON)
}

func calculateTotalTokensWithCache(inputTokens, outputTokens, cacheRead, cacheCreation, cacheCreation5m, cacheCreation1h int) int {
	return inputTokens + outputTokens + cacheRead + effectiveCacheCreationTokens(cacheCreation, cacheCreation5m, cacheCreation1h)
}

// injectResponsesUsageToCompletedEvent 向 response.completed 事件注入 usage
// 返回: 修改后的事件字符串, 估算的 inputTokens, 估算的 outputTokens

func injectResponsesUsageToCompletedEventWithLogTag(event string, requestBody []byte, outputText string, envCfg *config.EnvConfig, logTag string) (string, int, int) {
	inputTokens := utils.EstimateResponsesRequestTokens(requestBody)
	outputTokens := utils.EstimateTokens(outputText)
	totalTokens := calculateTotalTokensWithCache(inputTokens, outputTokens, 0, 0, 0, 0)

	// 调试日志：记录估算开始
	if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
		common.LogWithTag(logTag, "[Responses-Stream-Token] injectUsage 开始: inputTokens=%d, outputTokens=%d, event长度=%d",
			inputTokens, outputTokens, len(event))
	}

	var result strings.Builder
	lines := strings.Split(event, "\n")
	injected := false

	for _, line := range lines {
		// 跳过 event: 行，但保留它
		if strings.HasPrefix(line, "event:") {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		// 支持 "data:" 和 "data: " 两种格式（有些上游不带空格）
		var jsonStr string
		if strings.HasPrefix(line, "data:") {
			jsonStr = strings.TrimPrefix(line, "data:")
			jsonStr = strings.TrimPrefix(jsonStr, " ") // 移除可能的前导空格
		} else {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			// 调试日志：JSON 解析失败
			if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
				common.LogWithTag(logTag, "[Responses-Stream-Token] JSON解析失败: %v, 内容前200字符: %.200s", err, jsonStr)
			}
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		eventType, _ := data["type"].(string)

		if eventType == "response.completed" {
			response, ok := data["response"].(map[string]interface{})
			if !ok {
				// response 字段缺失或类型错误，创建一个新的
				if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
					common.LogWithTag(logTag, "[Responses-Stream-Token] response字段缺失, 创建新的response对象")
				}
				response = make(map[string]interface{})
				data["response"] = response
			}

			response["usage"] = map[string]interface{}{
				"input_tokens":  inputTokens,
				"output_tokens": outputTokens,
				"total_tokens":  totalTokens,
			}
			injected = true

			patchedJSON, err := json.Marshal(data)
			if err != nil {
				if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
					common.LogWithTag(logTag, "[Responses-Stream-Token] JSON序列化失败: %v", err)
				}
				result.WriteString(line)
				result.WriteString("\n")
				continue
			}

			if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
				common.LogWithTag(logTag, "[Responses-Stream-Token] 注入本地估算成功: InputTokens=%d, OutputTokens=%d, TotalTokens=%d",
					inputTokens, outputTokens, totalTokens)
			}

			result.WriteString("data: ")
			result.Write(patchedJSON)
			result.WriteString("\n")
		} else {
			result.WriteString(line)
			result.WriteString("\n")
		}
	}

	// 如果没有成功注入，可能是 SSE 格式不同，尝试直接在整个 event 中查找并替换
	if !injected {
		if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
			common.LogWithTag(logTag, "[Responses-Stream-Token] 逐行解析未找到, 尝试整体解析 event")
		}

		// 尝试从 event 中提取 JSON 部分（可能是多行格式）
		var jsonStart, jsonEnd int
		for i, line := range lines {
			if strings.HasPrefix(line, "data:") {
				jsonStart = i
				break
			}
		}

		// 合并所有 data: 行（支持 "data:" 和 "data: " 两种格式）
		var jsonBuilder strings.Builder
		for i := jsonStart; i < len(lines); i++ {
			line := lines[i]
			if strings.HasPrefix(line, "data:") {
				jsonData := strings.TrimPrefix(line, "data:")
				jsonData = strings.TrimPrefix(jsonData, " ") // 移除可能的前导空格
				jsonBuilder.WriteString(jsonData)
			} else if line == "" {
				jsonEnd = i
				break
			}
		}

		fullJSON := jsonBuilder.String()
		if fullJSON != "" {
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(fullJSON), &data); err == nil {
				eventType, _ := data["type"].(string)
				if eventType == "response.completed" {
					response, ok := data["response"].(map[string]interface{})
					if !ok {
						response = make(map[string]interface{})
						data["response"] = response
					}

					response["usage"] = map[string]interface{}{
						"input_tokens":  inputTokens,
						"output_tokens": outputTokens,
						"total_tokens":  totalTokens,
					}

					patchedJSON, err := json.Marshal(data)
					if err == nil {
						injected = true
						// 重建 event
						result.Reset()
						for i := 0; i < jsonStart; i++ {
							result.WriteString(lines[i])
							result.WriteString("\n")
						}
						result.WriteString("data: ")
						result.Write(patchedJSON)
						result.WriteString("\n")
						for i := jsonEnd; i < len(lines); i++ {
							result.WriteString(lines[i])
							result.WriteString("\n")
						}

						if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
							common.LogWithTag(logTag, "[Responses-Stream-Token] 整体解析注入成功: InputTokens=%d, OutputTokens=%d",
								inputTokens, outputTokens)
						}
					}
				}
			}
		}
	}

	// 如果仍然没有成功注入，记录警告并打印 event 内容
	if !injected {
		if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") {
			// 打印 event 的前500个字符帮助调试
			eventPreview := event
			if len(eventPreview) > 500 {
				eventPreview = eventPreview[:500] + "..."
			}
			common.LogWithTag(logTag, "[Responses-Stream-Token] 警告: 未找到 response.completed 事件进行注入, event内容: %s", eventPreview)
		}
		return event, inputTokens, outputTokens
	}

	return result.String(), inputTokens, outputTokens
}

// patchResponsesCompletedEventUsage 修补 response.completed 事件中的 usage

func patchResponsesCompletedEventUsageWithLogTag(event string, requestBody []byte, outputText string, collected *responsesStreamUsage, envCfg *config.EnvConfig, logTag string) string {
	var result strings.Builder
	lines := strings.Split(event, "\n")

	for _, line := range lines {
		// 支持 "data:" 和 "data: " 两种格式（有些上游不带空格）
		var jsonStr string
		if strings.HasPrefix(line, "data:") {
			jsonStr = strings.TrimPrefix(line, "data:")
			jsonStr = strings.TrimPrefix(jsonStr, " ") // 移除可能的前导空格
		} else {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		if data["type"] == "response.completed" {
			if response, ok := data["response"].(map[string]interface{}); ok {
				if usage, ok := response["usage"].(map[string]interface{}); ok {
					originalInput := collected.InputTokens
					originalOutput := collected.OutputTokens
					patched := false

					// 修补 input_tokens（仅当没有 Claude 原生缓存时）
					// OpenAI 的 cached_tokens 不应阻止 input_tokens 补全
					if collected.InputTokens <= 1 && !collected.HasClaudeCache {
						estimatedInput := utils.EstimateResponsesRequestTokens(requestBody)
						usage["input_tokens"] = estimatedInput
						collected.InputTokens = estimatedInput
						patched = true
					}

					// 修补 output_tokens
					if collected.OutputTokens <= 1 {
						estimatedOutput := utils.EstimateTokens(outputText)
						usage["output_tokens"] = estimatedOutput
						collected.OutputTokens = estimatedOutput
						patched = true
					}

					// 重新计算 total_tokens（修补时或 total_tokens 为 0 但 input/output 有效时）
					currentTotal := 0
					if t, ok := usage["total_tokens"].(float64); ok {
						currentTotal = int(t)
					}
					if patched || (currentTotal == 0 && (collected.InputTokens > 0 || collected.OutputTokens > 0)) {
						usage["total_tokens"] = calculateTotalTokensWithCache(
							collected.InputTokens,
							collected.OutputTokens,
							collected.CacheReadInputTokens,
							collected.CacheCreationInputTokens,
							collected.CacheCreation5mInputTokens,
							collected.CacheCreation1hInputTokens,
						)
					}

					if envCfg.EnableResponseLogs && envCfg.ShouldLog("debug") && patched {
						common.LogWithTag(logTag, "[Responses-Stream-Token] 虚假值修补: InputTokens=%d->%d, OutputTokens=%d->%d",
							originalInput, collected.InputTokens, originalOutput, collected.OutputTokens)
					}
				}
			}

			patchedJSON, err := json.Marshal(data)
			if err != nil {
				result.WriteString(line)
				result.WriteString("\n")
				continue
			}

			result.WriteString("data: ")
			result.Write(patchedJSON)
			result.WriteString("\n")
		} else {
			result.WriteString(line)
			result.WriteString("\n")
		}
	}

	return result.String()
}

// patchResponsesCompletedEventModel 改写 response.completed 事件中的 response.model 字段
// 当 REWRITE_RESPONSE_MODEL 启用且上游为 Responses 直通（needConvert==false）时使用
func patchResponsesCompletedEventModel(event string, requestModel string, logTag string) string {
	var result strings.Builder
	lines := strings.Split(event, "\n")

	for _, line := range lines {
		var jsonStr string
		if strings.HasPrefix(line, "data:") {
			jsonStr = strings.TrimPrefix(line, "data:")
			jsonStr = strings.TrimPrefix(jsonStr, " ")
		} else {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		if data["type"] == "response.completed" {
			if response, ok := data["response"].(map[string]interface{}); ok {
				if responseModel, _ := response["model"].(string); responseModel != "" && requestModel != "" && responseModel != requestModel {
					response["model"] = requestModel
					common.LogWithTag(logTag, "[Responses-Stream-Patch] 改写 response.model: %s -> %s", responseModel, requestModel)
					patchedJSON, err := json.Marshal(data)
					if err != nil {
						result.WriteString(line)
						result.WriteString("\n")
						continue
					}
					result.WriteString("data: ")
					result.Write(patchedJSON)
					result.WriteString("\n")
					continue
				}
			}
		}
		result.WriteString(line)
		result.WriteString("\n")
	}

	return result.String()
}
