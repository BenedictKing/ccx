package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

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
	return convertThinkingToReasoningContentWithOptions(bodyBytes, false)
}

func convertThinkingToReasoningContentWithOptions(bodyBytes []byte, synthesizeThinkingForToolUse bool) []byte {
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
		toolUseBlocks := make([]map[string]interface{}, 0, 1)
		hasThinkingBlock := false
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				filteredContent = append(filteredContent, block)
				continue
			}

			blockType, _ := blockMap["type"].(string)
			blockType = strings.TrimSpace(blockType)
			if isAssistantToolUseBlock(blockType) {
				toolUseBlocks = append(toolUseBlocks, blockMap)
			}
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
				hasThinkingBlock = true
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
		finalReasoning, _ := msgMap["reasoning_content"].(string)
		if strings.TrimSpace(finalReasoning) != "" {
			for _, blockMap := range toolUseBlocks {
				if blockReasoning, _ := blockMap["reasoning_content"].(string); blockReasoning == finalReasoning {
					continue
				}
				blockMap["reasoning_content"] = finalReasoning
				modified = true
				contentModified = true
			}
		}
		if synthesizeThinkingForToolUse && len(toolUseBlocks) > 0 && !hasThinkingBlock {
			thinking := finalReasoning
			if strings.TrimSpace(thinking) == "" {
				thinking = legacyThinkingPlaceholder
			}
			filteredContent = append([]interface{}{map[string]interface{}{
				"type":     "thinking",
				"thinking": thinking,
			}}, filteredContent...)
			modified = true
			contentModified = true
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

func isAssistantToolUseBlock(blockType string) bool {
	switch strings.TrimSpace(blockType) {
	case "tool_use", "server_tool_use":
		return true
	default:
		return false
	}
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

	// 模型重定向：仅修改 model 字段，保持其他内容不变
	if len(upstream.ModelMapping) > 0 {
		bodyBytes = redirectModelInBody(bodyBytes, upstream)
	}

	passbackReasoningContent := shouldPassbackReasoningContent(upstream)
	if passbackReasoningContent {
		bodyBytes = convertThinkingToReasoningContentWithOptions(bodyBytes, shouldSynthesizeThinkingForToolUse(upstream))
	}
	if upstream.PassbackThinkingBlocks {
		bodyBytes = convertReasoningContentToThinkingBlocks(bodyBytes, passbackReasoningContent)
	}
	if upstream.StripEmptyTextBlocks {
		bodyBytes = stripEmptyTextBlocksFromBody(bodyBytes)
		if !passbackReasoningContent && !upstream.PassbackThinkingBlocks {
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
	utils.SetAuthenticationHeader(req.Header, apiKey)
	utils.EnsureCompatibleUserAgent(req.Header, "claude")

	return req, bodyBytes, nil
}

func shouldSynthesizeThinkingForToolUse(upstream *config.UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	if upstream.PassbackThinkingBlocks {
		return true
	}
	baseURL := strings.ToLower(strings.TrimSpace(upstream.BaseURL))
	name := strings.ToLower(strings.TrimSpace(upstream.Name))
	return strings.Contains(baseURL, "api.kimi.com/coding") || (strings.Contains(name, "kimi") && strings.Contains(baseURL, "/coding"))
}

func shouldPassbackReasoningContent(upstream *config.UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	if upstream.PassbackReasoningContent {
		return true
	}
	if isKimiCodingUpstream(upstream) {
		return false
	}
	switch strings.TrimSpace(upstream.ReasoningParamStyle) {
	case "reasoning", "thinking":
		return true
	default:
		return false
	}
}

func isKimiCodingUpstream(upstream *config.UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	baseURL := strings.ToLower(strings.TrimSpace(upstream.BaseURL))
	return strings.Contains(baseURL, "api.kimi.com/coding")
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
					return &claudeResp, nil
				}
			}
		}
	}

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

		toolUseStopEmitted := false

		// 注意：为了让下游的 token 注入/修补逻辑保持正确，这里必须按「完整 SSE 事件」转发。
		// 上游以空行分隔事件：event/data/id/retry/... + "\n"，空行 => 事件结束。
		var eventBuf strings.Builder

		flushEvent := func() {
			if eventBuf.Len() == 0 {
				return
			}
			eventChan <- eventBuf.String()
			eventBuf.Reset()
		}

		for scanner.Scan() {
			line := normalizeSSEFieldLine(scanner.Text())

			// 检测是否发送了 tool_use 相关的 stop_reason（通常在 data 行中）
			if strings.Contains(line, `"stop_reason":"tool_use"`) ||
				strings.Contains(line, `"stop_reason": "tool_use"`) {
				toolUseStopEmitted = true
			}

			// 转换流式响应中的 reasoning_content → thinking（兼容 mimo 等上游）
			if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"reasoning_content"`) {
				dataJSON := strings.TrimPrefix(line, "data: ")
				if dataJSON != "[DONE]" {
					var eventData map[string]interface{}
					if err := json.Unmarshal([]byte(dataJSON), &eventData); err == nil {
						// 检查是否包含 reasoning_content
						if delta, ok := eventData["delta"].(map[string]interface{}); ok {
							if reasoningContent, exists := delta["reasoning_content"].(string); exists && reasoningContent != "" {
								// 将 reasoning_content 转为 thinking_delta
								delta["type"] = "thinking_delta"
								delta["thinking"] = reasoningContent
								delete(delta, "reasoning_content")

								// 重新序列化
								if newJSON, err := json.Marshal(eventData); err == nil {
									line = "data: " + string(newJSON)
								}
							}
						}
					}
				}
			}

			// 透传所有 SSE 字段（包括注释、id、retry 等）
			eventBuf.WriteString(line)
			eventBuf.WriteString("\n")

			// 空行表示一个 SSE event 结束
			if line == "" {
				flushEvent()
			}
		}

		// 若上游未以空行结尾，仍尝试把最后的残留事件发出去
		flushEvent()

		if err := scanner.Err(); err != nil {
			// 在 tool_use 场景下，客户端主动断开是正常行为
			// 如果已经发送了 tool_use stop 事件，并且错误是连接断开相关的，则忽略该错误
			errMsg := err.Error()
			if toolUseStopEmitted && (strings.Contains(errMsg, "broken pipe") ||
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
