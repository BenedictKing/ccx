package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// ClaudeProvider Claude 提供商（直接透传）
type ClaudeProvider struct{}

// redirectModelInBody 仅修改请求体中的 model 字段，保持其他内容不变
// 使用 map[string]interface{} 避免结构体字段丢失问题
func redirectModelInBody(bodyBytes []byte, upstream *config.UpstreamConfig) []byte {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber() // 保留数字精度

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes // 解析失败，返回原始数据
	}

	model, ok := data["model"].(string)
	if !ok {
		return bodyBytes // 没有 model 字段或类型不对
	}

	newModel := config.RedirectModel(model, upstream)
	if newModel == model {
		return bodyBytes // 模型未变，无需重编码
	}

	data["model"] = newModel

	// 使用 Encoder 并禁用 HTML 转义，保持原始格式
	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes // 编码失败，返回原始数据
	}
	return newBytes
}

// applyClaudeReasoningEffort 根据原始模型名将渠道级思考强度写入 Claude 请求体。
func applyClaudeReasoningEffort(bodyBytes []byte, upstream *config.UpstreamConfig) []byte {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes
	}

	model, ok := data["model"].(string)
	if !ok {
		return bodyBytes
	}

	effort := config.ResolveReasoningEffort(model, upstream)
	if effort == "" {
		return bodyBytes
	}

	applyClaudeThinkingEffort(data, effort)

	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes
	}
	return newBytes
}

func applyClaudeThinkingEffort(data map[string]interface{}, effort string) {
	delete(data, "reasoning")
	delete(data, "reasoning_effort")
	stripClaudeOutputConfigEffort(data)

	if effort == "off" || effort == "none" {
		data["thinking"] = map[string]interface{}{"type": "disabled"}
		return
	}

	thinking, _ := data["thinking"].(map[string]interface{})
	if thinking == nil {
		thinking = make(map[string]interface{})
	}
	thinking["type"] = "enabled"
	thinking["effort"] = effort
	delete(thinking, "budget_tokens")
	data["thinking"] = thinking
}

func stripClaudeOutputConfigEffort(data map[string]interface{}) {
	outputConfig, ok := data["output_config"].(map[string]interface{})
	if !ok {
		return
	}

	delete(outputConfig, "effort")
	if len(outputConfig) == 0 {
		delete(data, "output_config")
	}
}

const (
	legacyThinkingPlaceholder    = "(no prior reasoning recorded)"
	missingAssistantResponseText = "[prior assistant response unavailable]"
)

// convertThinkingToReasoningContent 将真实 thinking 回传为 reasoning_content，并清理历史版本注入的占位 thinking。
//
// 开启 thinking mode 的上游要求 assistant 历史都带顶层 reasoning_content：
// - 有真实 thinking/reasoning 时按原样回传；
// - 缺少真实 reasoning 时补非空占位（避免部分上游将空串视为“未回传”）。
//
// 注：函数名保留以维持向后兼容。
func convertThinkingToReasoningContent(bodyBytes []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes
	}

	messages, ok := data["messages"].([]interface{})
	if !ok {
		return bodyBytes
	}

	modified := false
	filteredMessages := make([]interface{}, 0, len(messages))
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			filteredMessages = append(filteredMessages, msg)
			continue
		}

		if role, _ := msgMap["role"].(string); role != "assistant" {
			filteredMessages = append(filteredMessages, msgMap)
			continue
		}

		content, hasContentArray := msgMap["content"].([]interface{})
		if !hasContentArray {
			modified = true
			if _, exists := msgMap["reasoning_content"]; !exists {
				msgMap["reasoning_content"] = legacyThinkingPlaceholder
			}
			filteredMessages = append(filteredMessages, msgMap)
			continue
		}

		filteredContent := make([]interface{}, 0, len(content))
		contentModified := false
		reasoningParts := make([]string, 0, 1)
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				filteredContent = append(filteredContent, block)
				continue
			}

			blockType, _ := blockMap["type"].(string)
			blockType = strings.TrimSpace(blockType)
			thinking, _ := blockMap["thinking"].(string)
			trimmedThinking := strings.TrimSpace(thinking)
			if blockType == "thinking" {
				// 历史占位/空 thinking 会污染回传语义，直接剔除。
				if trimmedThinking == "" || trimmedThinking == legacyThinkingPlaceholder {
					modified = true
					contentModified = true
					continue
				}
				// 为兼容上游思考回传校验，保留原始 thinking 文本，不做 trim 改写。
				reasoningParts = append(reasoningParts, thinking)
				// 保留真实 thinking block，避免部分上游按历史内容做一致性校验时报错。
				filteredContent = append(filteredContent, blockMap)
				continue
			}
			reasoning, reasoningExists := blockMap["reasoning_content"].(string)
			if reasoningExists {
				trimmedReasoning := strings.TrimSpace(reasoning)
				if trimmedReasoning != "" && trimmedReasoning != legacyThinkingPlaceholder {
					reasoningParts = append(reasoningParts, reasoning)
				}
				delete(blockMap, "reasoning_content")
				modified = true
				contentModified = true
			}

			filteredContent = append(filteredContent, blockMap)
		}

		existing, hasExisting := msgMap["reasoning_content"].(string)
		switch {
		case strings.TrimSpace(existing) != "":
			// 已有顶层 reasoning_content 时保持原样，避免改写导致上游回传校验失败。
		case len(reasoningParts) > 0:
			msgMap["reasoning_content"] = strings.Join(reasoningParts, "")
			modified = true
		case !hasExisting || strings.TrimSpace(existing) == "":
			modified = true
			msgMap["reasoning_content"] = legacyThinkingPlaceholder
		}

		if len(filteredContent) == 0 {
			modified = true
			contentModified = true
			filteredContent = []interface{}{map[string]interface{}{
				"type": "text",
				"text": missingAssistantResponseText,
			}}
		}
		if contentModified {
			msgMap["content"] = filteredContent
		}
		filteredMessages = append(filteredMessages, msgMap)
	}

	if !modified {
		return bodyBytes
	}
	data["messages"] = filteredMessages

	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes
	}
	return newBytes
}

// convertReasoningContentToThinkingBlocks 将真实 reasoning_content 投影为 Claude thinking 块。
//
// 严格 Claude thinking 上游会校验历史 assistant 的 content[].thinking，而不是顶层
// reasoning_content。该函数只回传真正存在的 reasoning；历史占位不会被伪造成 thinking。
func convertReasoningContentToThinkingBlocks(bodyBytes []byte, keepTopLevelReasoning bool) []byte {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes
	}

	messages, ok := data["messages"].([]interface{})
	if !ok {
		return bodyBytes
	}

	modified := false
	for _, rawMsg := range messages {
		msgMap, ok := rawMsg.(map[string]interface{})
		if !ok {
			continue
		}
		if role, _ := msgMap["role"].(string); role != "assistant" {
			continue
		}

		topLevelReasoning, _ := msgMap["reasoning_content"].(string)
		hasRealTopLevelReasoning := isRealReasoningText(topLevelReasoning)
		if !keepTopLevelReasoning && topLevelReasoning != "" {
			delete(msgMap, "reasoning_content")
			modified = true
		}
		if keepTopLevelReasoning && strings.TrimSpace(topLevelReasoning) == legacyThinkingPlaceholder {
			delete(msgMap, "reasoning_content")
			modified = true
		}

		content, hasContentArray := msgMap["content"].([]interface{})
		if !hasContentArray {
			if !hasRealTopLevelReasoning {
				continue
			}
			newContent := []interface{}{map[string]interface{}{
				"type":     "thinking",
				"thinking": topLevelReasoning,
			}}
			if text, ok := msgMap["content"].(string); ok && text != "" {
				newContent = append(newContent, map[string]interface{}{"type": "text", "text": text})
			}
			msgMap["content"] = newContent
			modified = true
			continue
		}

		filteredContent := make([]interface{}, 0, len(content)+1)
		hasRealThinkingBlock := false
		contentModified := false
		blockReasoningParts := make([]string, 0, 1)
		for _, rawBlock := range content {
			blockMap, ok := rawBlock.(map[string]interface{})
			if !ok {
				filteredContent = append(filteredContent, rawBlock)
				continue
			}

			blockType, _ := blockMap["type"].(string)
			blockType = strings.TrimSpace(blockType)
			if blockType == "thinking" {
				thinking, _ := blockMap["thinking"].(string)
				if !isRealReasoningText(thinking) {
					modified = true
					contentModified = true
					continue
				}
				hasRealThinkingBlock = true
				filteredContent = append(filteredContent, blockMap)
				continue
			}

			if reasoning, exists := blockMap["reasoning_content"].(string); exists {
				if isRealReasoningText(reasoning) {
					blockReasoningParts = append(blockReasoningParts, reasoning)
				}
				delete(blockMap, "reasoning_content")
				modified = true
				contentModified = true
			}
			filteredContent = append(filteredContent, blockMap)
		}

		reasoningForThinking := ""
		switch {
		case hasRealThinkingBlock:
		case hasRealTopLevelReasoning:
			reasoningForThinking = topLevelReasoning
		case len(blockReasoningParts) > 0:
			reasoningForThinking = strings.Join(blockReasoningParts, "")
		}
		if reasoningForThinking != "" {
			filteredContent = append([]interface{}{map[string]interface{}{
				"type":     "thinking",
				"thinking": reasoningForThinking,
			}}, filteredContent...)
			modified = true
			contentModified = true
		}

		if len(filteredContent) == 0 {
			filteredContent = []interface{}{map[string]interface{}{
				"type": "text",
				"text": missingAssistantResponseText,
			}}
			modified = true
			contentModified = true
		}
		if contentModified {
			msgMap["content"] = filteredContent
		}
	}

	if !modified {
		return bodyBytes
	}
	data["messages"] = messages

	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes
	}
	return newBytes
}

func isRealReasoningText(text string) bool {
	trimmed := strings.TrimSpace(text)
	return trimmed != "" && trimmed != legacyThinkingPlaceholder
}

func stripEmptyTextBlocksFromBody(bodyBytes []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes
	}

	messages, ok := data["messages"].([]interface{})
	if !ok {
		return bodyBytes
	}

	modified := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}

		content, ok := msgMap["content"].([]interface{})
		if !ok {
			continue
		}

		filtered := make([]interface{}, 0, len(content))
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				filtered = append(filtered, block)
				continue
			}
			if shouldStripEmptyTextBlock(blockMap) {
				modified = true
				continue
			}
			filtered = append(filtered, block)
		}

		msgMap["content"] = filtered
	}

	if !modified {
		return bodyBytes
	}

	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes
	}
	return newBytes
}

func shouldStripEmptyTextBlock(block map[string]interface{}) bool {
	if len(block) != 2 {
		return false
	}
	blockType, _ := block["type"].(string)
	text, _ := block["text"].(string)
	return blockType == "text" && text == ""
}

func stripThinkingBlocksFromBody(bodyBytes []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes
	}

	messages, ok := data["messages"].([]interface{})
	if !ok {
		return bodyBytes
	}

	modified := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}

		content, ok := msgMap["content"].([]interface{})
		if !ok {
			continue
		}

		filtered := make([]interface{}, 0, len(content))
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				filtered = append(filtered, block)
				continue
			}
			blockType, _ := blockMap["type"].(string)
			if blockType == "thinking" || blockType == "redacted_thinking" {
				modified = true
				continue
			}
			filtered = append(filtered, block)
		}

		msgMap["content"] = filtered
	}

	if !modified {
		return bodyBytes
	}

	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes
	}
	return newBytes
}

func transformRequestMap(reqMap map[string]interface{}, transform func([]byte) []byte) map[string]interface{} {
	marshaledReq, err := utils.MarshalJSONNoEscape(reqMap)
	if err != nil {
		return nil
	}
	normalizedBytes := transform(marshaledReq)
	var normalized map[string]interface{}
	if json.Unmarshal(normalizedBytes, &normalized) != nil {
		return nil
	}
	return normalized
}

func stripThinkingBlocksFromRequestMap(reqMap map[string]interface{}) map[string]interface{} {
	return transformRequestMap(reqMap, stripThinkingBlocksFromBody)
}

func stripEmptyTextBlocksFromRequestMap(reqMap map[string]interface{}) map[string]interface{} {
	return transformRequestMap(reqMap, stripEmptyTextBlocksFromBody)
}

func extractClaudeSystemText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if blockType, _ := block["type"].(string); blockType != "text" {
				continue
			}
			text, _ := block["text"].(string)
			text = strings.TrimSpace(text)
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func NormalizeSystemRoleToTopLevel(bodyBytes []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes
	}

	messages, ok := data["messages"].([]interface{})
	if !ok {
		return bodyBytes
	}

	systemTexts := make([]string, 0, 2)
	filteredMessages := make([]interface{}, 0, len(messages))
	modified := false

	for _, rawMsg := range messages {
		msgMap, ok := rawMsg.(map[string]interface{})
		if !ok {
			filteredMessages = append(filteredMessages, rawMsg)
			continue
		}
		role, _ := msgMap["role"].(string)
		if role != "system" {
			filteredMessages = append(filteredMessages, msgMap)
			continue
		}
		modified = true
		if text := extractClaudeSystemText(msgMap["content"]); text != "" {
			systemTexts = append(systemTexts, text)
		}
	}

	if !modified {
		return bodyBytes
	}

	existingTopLevel := ""
	switch s := data["system"].(type) {
	case string:
		existingTopLevel = strings.TrimSpace(s)
	case []interface{}:
		existingTopLevel = extractClaudeSystemText(s)
	}
	if existingTopLevel != "" {
		systemTexts = append([]string{existingTopLevel}, systemTexts...)
	}

	data["messages"] = filteredMessages
	if len(systemTexts) > 0 {
		data["system"] = strings.Join(systemTexts, "\n\n")
	}

	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes
	}
	return newBytes
}

// convertReasoningContentToThinking 将响应中的 reasoning_content 转为 Claude thinking 内容块
// 用于兼容 mimo 等返回 OpenAI 风格 reasoning_content 的 Claude 协议上游
func convertReasoningContentToThinking(bodyBytes []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes
	}

	modified := false

	// 处理顶层 reasoning_content（如果存在）
	if reasoningContent, ok := data["reasoning_content"].(string); ok && reasoningContent != "" {
		content, ok := data["content"].([]interface{})
		if !ok {
			content = []interface{}{}
		}

		// 在 content 数组开头插入 thinking 块
		thinkingBlock := map[string]interface{}{
			"type":     "thinking",
			"thinking": reasoningContent,
		}
		newContent := append([]interface{}{thinkingBlock}, content...)
		data["content"] = newContent
		delete(data, "reasoning_content")
		modified = true
	}

	// 处理 content 数组中的 reasoning_content（如果存在）
	if content, ok := data["content"].([]interface{}); ok {
		for i, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				continue
			}

			if reasoningContent, exists := blockMap["reasoning_content"].(string); exists && reasoningContent != "" {
				// 将 reasoning_content 转为 thinking 块
				blockMap["type"] = "thinking"
				blockMap["thinking"] = reasoningContent
				delete(blockMap, "reasoning_content")
				content[i] = blockMap
				modified = true
			}
		}
	}

	if !modified {
		return bodyBytes
	}

	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return bodyBytes
	}
	return newBytes
}

// ConvertToProviderRequest 转换为 Claude 请求（实现真正的透传）
func (p *ClaudeProvider) ConvertToProviderRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string) (*http.Request, []byte, error) {
	// 读取原始请求体
	bodyBytes, err := getRequestBodyBytes(c)
	if err != nil {
		return nil, nil, err
	}

	if len(upstream.ReasoningMapping) > 0 {
		bodyBytes = applyClaudeReasoningEffort(bodyBytes, upstream)
	}
	// 模型重定向：仅修改 model 字段，保持其他内容不变
	if len(upstream.ModelMapping) > 0 {
		bodyBytes = redirectModelInBody(bodyBytes, upstream)
	}

	if upstream.PassbackReasoningContent {
		bodyBytes = convertThinkingToReasoningContent(bodyBytes)
	}
	if upstream.PassbackThinkingBlocks {
		bodyBytes = convertReasoningContentToThinkingBlocks(bodyBytes, upstream.PassbackReasoningContent)
	}
	if upstream.StripEmptyTextBlocks {
		bodyBytes = stripEmptyTextBlocksFromBody(bodyBytes)
		if !upstream.PassbackReasoningContent && !upstream.PassbackThinkingBlocks {
			bodyBytes = stripThinkingBlocksFromBody(bodyBytes)
		}
	}

	// 注意：NormalizeSystemRoleToTopLevel（抽取 system 角色到顶层）已上移到
	// handlers/common 的 failover 统一入口处理，使所有上游类型均生效，此处不再重复执行。

	// 构建目标URL
	// 智能拼接逻辑：
	// 1. 如果 baseURL 以 # 结尾，跳过自动添加 /v1
	// 2. 如果 baseURL 已包含版本号后缀（如 /v1, /v2, /v3），直接拼接端点路径
	// 3. 如果 baseURL 不包含版本号后缀，自动添加 /v1 再拼接端点路径
	// 先剥离 routePrefix（如 /glm），再剥离 /v1，得到纯端点路径（如 /messages）
	path := c.Request.URL.Path
	if routePrefix := c.Param("routePrefix"); routePrefix != "" {
		path = strings.TrimPrefix(path, "/"+routePrefix)
	}
	endpoint := strings.TrimPrefix(path, "/v1")
	baseURL := upstream.GetEffectiveBaseURL()
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	// 使用正则表达式检测 baseURL 是否以版本号结尾（/v1, /v2, /v1beta, /v2alpha等）
	versionPattern := regexp.MustCompile(`/v\d+[a-z]*$`)

	var targetURL string
	if versionPattern.MatchString(baseURL) || skipVersionPrefix {
		// baseURL 已包含版本号或以#结尾，直接拼接
		targetURL = baseURL + endpoint
	} else {
		// baseURL 不包含版本号，添加 /v1
		targetURL = baseURL + "/v1" + endpoint
	}

	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}

	// 创建请求
	var req *http.Request
	if len(bodyBytes) > 0 {
		req, err = http.NewRequestWithContext(c.Request.Context(), c.Request.Method, targetURL, bytes.NewReader(bodyBytes))
	} else {
		// 如果 bodyBytes 为空（例如 GET 请求或原始请求体为空），则直接使用 nil Body
		req, err = http.NewRequestWithContext(c.Request.Context(), c.Request.Method, targetURL, nil)
	}
	if err != nil {
		return nil, nil, err
	}

	// 使用统一的头部处理逻辑
	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
	utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders) // 先应用自定义头，后覆盖认证（不可被自定义头覆盖）
	utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
	utils.EnsureCompatibleUserAgent(req.Header, "claude")

	return req, bodyBytes, nil
}

// ConvertToClaudeResponse 转换为 Claude 响应（直接透传）
func (p *ClaudeProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	var claudeResp types.ClaudeResponse
	if err := json.Unmarshal(providerResp.Body, &claudeResp); err != nil {
		return nil, err
	}

	// 检查响应中是否包含 reasoning_content（mimo 等上游可能返回此字段）
	// 如果存在，转换为 Claude thinking 内容块
	var rawResp map[string]interface{}
	if err := json.Unmarshal(providerResp.Body, &rawResp); err == nil {
		if content, ok := rawResp["content"].([]interface{}); ok {
			// 检查是否有 reasoning_content 需要转换
			hasReasoningContent := false
			for _, block := range content {
				if blockMap, ok := block.(map[string]interface{}); ok {
					if _, exists := blockMap["reasoning_content"]; exists {
						hasReasoningContent = true
						break
					}
				}
			}

			// 或者检查顶层是否有 reasoning_content
			if !hasReasoningContent {
				if _, exists := rawResp["reasoning_content"]; exists {
					hasReasoningContent = true
				}
			}

			if hasReasoningContent {
				convertedBody := convertReasoningContentToThinking(providerResp.Body)
				if err := json.Unmarshal(convertedBody, &claudeResp); err == nil {
					normalizeClaudeResponse(&claudeResp)
					return &claudeResp, nil
				}
			}
		}
	}

	normalizeClaudeResponse(&claudeResp)
	return &claudeResp, nil
}

// HandleStreamResponse 处理流式响应（直接透传）
func (p *ClaudeProvider) HandleStreamResponse(body io.ReadCloser) (<-chan string, <-chan error, error) {
	eventChan := make(chan string, 100)
	errChan := make(chan error, 1)

	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer body.Close()

		scanner := bufio.NewScanner(body)
		// 设置更大的 buffer (1MB) 以处理大 JSON chunk，避免默认 64KB 限制
		const maxScannerBufferSize = 1024 * 1024 // 1MB
		scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBufferSize)

		normalizer := newClaudeStreamNormalizer(eventChan)

		for scanner.Scan() {
			line := normalizeSSEFieldLine(scanner.Text())
			normalizer.handleLine(line)
		}

		// 若上游未以空行结尾，仍尝试把最后的残留事件发出去
		normalizer.finish()

		if err := scanner.Err(); err != nil {
			// 在 tool_use 场景下，客户端主动断开是正常行为
			// 如果已经发送了 tool_use stop 事件，并且错误是连接断开相关的，则忽略该错误
			errMsg := err.Error()
			if normalizer.toolUseStopEmitted && (strings.Contains(errMsg, "broken pipe") ||
				strings.Contains(errMsg, "connection reset") ||
				strings.Contains(errMsg, "EOF")) {
				// 这是预期的客户端行为，不报告错误
				return
			}
			errChan <- err
		}
	}()

	return eventChan, errChan, nil
}

func normalizeClaudeResponse(resp *types.ClaudeResponse) {
	if resp == nil {
		return
	}
	if resp.ID == "" {
		resp.ID = generateID()
	}
	if resp.Type == "" {
		resp.Type = "message"
	}
	if resp.Role == "" {
		resp.Role = "assistant"
	}
	for i := range resp.Content {
		if resp.Content[i].Type != "tool_use" {
			continue
		}
		resp.Content[i].ID = normalizeClaudeToolUseID(resp.Content[i].ID, i)
		if resp.Content[i].Input != nil {
			resp.Content[i].Input = sanitizeClaudeToolInput(resp.Content[i].Name, resp.Content[i].Input)
		}
	}
}

func normalizeClaudeToolUseID(id string, index int) string {
	if strings.HasPrefix(id, "toolu_") || strings.HasPrefix(id, "srvtoolu_") {
		return id
	}
	return fmt.Sprintf("toolu_%d_%d", time.Now().UnixNano(), index)
}

type claudeStreamNormalizer struct {
	eventChan          chan<- string
	eventName          string
	eventDataLines     []string
	seenMessageStart   bool
	seenMessageDelta   bool
	seenMessageStop    bool
	openTextBlock      map[int]bool
	openToolBlock      map[int]bool
	hasToolUse         bool
	toolUseStopEmitted bool
	finalUsage         map[string]interface{}
	finalStopReason    string
}

func newClaudeStreamNormalizer(eventChan chan<- string) *claudeStreamNormalizer {
	return &claudeStreamNormalizer{
		eventChan:     eventChan,
		openTextBlock: make(map[int]bool),
		openToolBlock: make(map[int]bool),
	}
}

func (n *claudeStreamNormalizer) handleLine(line string) {
	if line == "" {
		n.flushBufferedEvent()
		return
	}

	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "{") {
		n.handleDataJSON(strings.TrimSpace(trimmed))
		return
	}

	if strings.HasPrefix(line, "event: ") {
		n.eventName = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		return
	}
	if strings.HasPrefix(line, "data: ") {
		n.eventDataLines = append(n.eventDataLines, strings.TrimSpace(strings.TrimPrefix(line, "data: ")))
		return
	}
}

func (n *claudeStreamNormalizer) finish() {
	n.flushBufferedEvent()
	n.emitSyntheticEnd("end_turn")
}

func (n *claudeStreamNormalizer) flushBufferedEvent() {
	if len(n.eventDataLines) == 0 {
		n.eventName = ""
		return
	}

	for _, dataJSON := range n.eventDataLines {
		n.handleDataJSON(dataJSON)
	}
	n.eventName = ""
	n.eventDataLines = nil
}

func (n *claudeStreamNormalizer) handleDataJSON(dataJSON string) {
	if dataJSON == "[DONE]" {
		n.emitSyntheticEnd("end_turn")
		return
	}
	var eventData map[string]interface{}
	if err := json.Unmarshal([]byte(dataJSON), &eventData); err != nil {
		n.emitRawData(dataJSON)
		return
	}

	normalizedData := normalizeClaudeStreamData(eventData)
	eventType, _ := eventData["type"].(string)
	if eventType == "" {
		n.collectUntypedUsage(eventData)
		return
	}
	if eventType == "ping" {
		return
	}

	switch eventType {
	case "message_start":
		normalizeClaudeMessageStartEvent(eventData)
		normalizedData = normalizeClaudeStreamData(eventData)
		n.seenMessageStart = true
		n.emitDataEvent(n.eventName, normalizedData, eventType)
	case "content_block_start":
		n.ensureMessageStart("")
		normalizeClaudeContentBlockStart(eventData)
		normalizedData = normalizeClaudeStreamData(eventData)
		index := intFromMap(eventData, "index")
		block, _ := eventData["content_block"].(map[string]interface{})
		switch blockType, _ := block["type"].(string); blockType {
		case "text":
			n.openTextBlock[index] = true
		case "tool_use", "server_tool_use":
			n.hasToolUse = true
			n.openToolBlock[index] = true
		}
		n.emitDataEvent(n.eventName, normalizedData, eventType)
	case "content_block_delta":
		n.ensureMessageStart("")
		index := intFromMap(eventData, "index")
		delta, _ := eventData["delta"].(map[string]interface{})
		switch deltaType, _ := delta["type"].(string); deltaType {
		case "text_delta":
			n.ensureTextBlock(index)
		case "input_json_delta":
			n.hasToolUse = true
			n.ensureToolBlock(index)
		}
		n.emitDataEvent(n.eventName, normalizedData, eventType)
	case "content_block_stop":
		index := intFromMap(eventData, "index")
		n.emitDataEvent(n.eventName, normalizedData, eventType)
		delete(n.openTextBlock, index)
		delete(n.openToolBlock, index)
	case "message_delta":
		n.seenMessageDelta = true
		if usage, ok := eventData["usage"].(map[string]interface{}); ok {
			n.finalUsage = usage
		}
		if delta, ok := eventData["delta"].(map[string]interface{}); ok {
			if stopReason, _ := delta["stop_reason"].(string); stopReason != "" {
				n.finalStopReason = stopReason
			}
			if stopReason, _ := delta["stop_reason"].(string); stopReason == "tool_use" || stopReason == "server_tool_use" {
				n.hasToolUse = true
				n.toolUseStopEmitted = true
			}
		}
		n.emitDataEvent(n.eventName, normalizedData, eventType)
	case "message_stop":
		n.emitDataEvent(n.eventName, normalizedData, eventType)
		n.seenMessageStop = true
	default:
		n.emitDataEvent(n.eventName, normalizedData, eventType)
	}
}

func (n *claudeStreamNormalizer) ensureMessageStart(model string) {
	if n.seenMessageStart {
		return
	}
	n.emit("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":      generateID(),
			"type":    "message",
			"role":    "assistant",
			"model":   firstNonEmpty(model, "unknown"),
			"content": []interface{}{},
			"usage": map[string]int{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	})
	n.seenMessageStart = true
}

func (n *claudeStreamNormalizer) ensureTextBlock(index int) {
	if n.openTextBlock[index] {
		return
	}
	n.emit("content_block_start", map[string]interface{}{
		"type":          "content_block_start",
		"index":         index,
		"content_block": map[string]interface{}{"type": "text", "text": ""},
	})
	n.openTextBlock[index] = true
}

func (n *claudeStreamNormalizer) ensureToolBlock(index int) {
	if n.openToolBlock[index] {
		return
	}
	n.hasToolUse = true
	n.emit("content_block_start", map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type": "tool_use",
			"id":   fmt.Sprintf("toolu_%d_%d", time.Now().UnixNano(), index),
			"name": "unknown_function",
		},
	})
	n.openToolBlock[index] = true
}

func (n *claudeStreamNormalizer) emitSyntheticEnd(stopReason string) {
	if !n.seenMessageStart || n.seenMessageStop {
		return
	}

	for index := range n.openTextBlock {
		n.emit("content_block_stop", map[string]interface{}{
			"type":  "content_block_stop",
			"index": index,
		})
		delete(n.openTextBlock, index)
	}
	for index := range n.openToolBlock {
		n.emit("content_block_stop", map[string]interface{}{
			"type":  "content_block_stop",
			"index": index,
		})
		delete(n.openToolBlock, index)
	}

	if stopReason == "" {
		stopReason = n.finalStopReason
	}
	if stopReason == "" && n.hasToolUse {
		stopReason = "tool_use"
	}
	if stopReason == "" {
		stopReason = "end_turn"
	}
	if stopReason == "tool_use" || stopReason == "server_tool_use" {
		n.toolUseStopEmitted = true
	}
	usage := n.finalUsage
	if usage == nil {
		usage = map[string]interface{}{"input_tokens": 0, "output_tokens": 0}
	}
	if !n.seenMessageDelta {
		n.emit("message_delta", map[string]interface{}{
			"type": "message_delta",
			"delta": map[string]interface{}{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
				"stop_details":  nil,
			},
			"usage": usage,
		})
		n.seenMessageDelta = true
	}
	n.emit("message_stop", map[string]interface{}{
		"type": "message_stop",
	})
	n.seenMessageStop = true
}

func (n *claudeStreamNormalizer) collectUntypedUsage(eventData map[string]interface{}) {
	usage, _ := eventData["usage"].(map[string]interface{})
	if usage == nil {
		return
	}
	inputTokens := numericValue(usage["input_tokens"])
	if inputTokens == 0 {
		inputTokens = numericValue(usage["prompt_tokens"])
	}
	outputTokens := numericValue(usage["output_tokens"])
	if outputTokens == 0 {
		outputTokens = numericValue(usage["completion_tokens"])
	}
	if inputTokens == 0 && outputTokens == 0 {
		return
	}
	n.finalUsage = map[string]interface{}{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
	}
}

func (n *claudeStreamNormalizer) emit(event string, data map[string]interface{}) {
	payload, _ := json.Marshal(data)
	n.eventChan <- fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload)
}

func (n *claudeStreamNormalizer) emitDataEvent(eventName, dataLine, fallbackEventName string) {
	if dataLine == "" {
		return
	}
	if eventName == "" {
		eventName = fallbackEventName
	}
	if eventName == "" {
		n.eventChan <- dataLine + "\n\n"
		return
	}
	n.eventChan <- fmt.Sprintf("event: %s\n%s\n\n", eventName, dataLine)
}

func (n *claudeStreamNormalizer) emitRawData(dataJSON string) {
	if n.eventName == "" {
		n.eventChan <- "data: " + dataJSON + "\n\n"
		return
	}
	n.eventChan <- fmt.Sprintf("event: %s\ndata: %s\n\n", n.eventName, dataJSON)
}

func normalizeClaudeStreamData(eventData map[string]interface{}) string {
	if delta, ok := eventData["delta"].(map[string]interface{}); ok {
		if reasoningContent, exists := delta["reasoning_content"].(string); exists && reasoningContent != "" {
			delta["type"] = "thinking_delta"
			delta["thinking"] = reasoningContent
			delete(delta, "reasoning_content")
		}
	}

	payload, err := json.Marshal(eventData)
	if err != nil {
		return ""
	}
	return "data: " + string(payload)
}

func normalizeClaudeMessageStartEvent(eventData map[string]interface{}) {
	msg, ok := eventData["message"].(map[string]interface{})
	if !ok {
		return
	}
	if id, _ := msg["id"].(string); id == "" {
		msg["id"] = generateID()
	}
	if msgType, _ := msg["type"].(string); msgType == "" {
		msg["type"] = "message"
	}
	if role, _ := msg["role"].(string); role == "" {
		msg["role"] = "assistant"
	}
	if _, ok := msg["content"]; !ok {
		msg["content"] = []interface{}{}
	}
	if _, ok := msg["usage"]; !ok {
		msg["usage"] = map[string]int{"input_tokens": 0, "output_tokens": 0}
	}
}

func normalizeClaudeContentBlockStart(eventData map[string]interface{}) {
	index := intFromMap(eventData, "index")
	block, ok := eventData["content_block"].(map[string]interface{})
	if !ok {
		return
	}
	blockType, _ := block["type"].(string)
	if blockType != "tool_use" && blockType != "server_tool_use" {
		return
	}
	id, _ := block["id"].(string)
	block["id"] = normalizeClaudeToolUseID(id, index)
}

func intFromMap(data map[string]interface{}, key string) int {
	switch v := data[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func numericValue(value interface{}) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
