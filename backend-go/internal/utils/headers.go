package utils

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

// PrepareUpstreamHeaders 准备上游请求头（统一头部处理逻辑）
// 保留原始请求头，移除代理相关头部，设置认证头
// 注意：此函数适用于Claude类型渠道，对于其他类型请使用 PrepareMinimalHeaders
// ExtractUnifiedSessionID 统一提取会话/缓存标识，供 Messages/Responses/Chat/Gemini 复用。
// 优先级: Conversation_id > Session_id > X-Claude-Code-Session-Id > user > user_id > prompt_cache_key > metadata.user_id > X-Gemini-Api-Privileged-User-Id > X-Client-Request-Id
// X-Client-Request-Id 通常是逐请求 ID，只作为最终兜底，避免把同一会话拆成多张驾驶舱卡片。
func ExtractUnifiedSessionID(c *gin.Context, bodyBytes []byte) string {
	if c != nil {
		if convID := c.GetHeader("Conversation_id"); convID != "" {
			return convID
		}

		if sessID := c.GetHeader("Session_id"); sessID != "" {
			return sessID
		}

		if claudeCodeSessionID := c.GetHeader("X-Claude-Code-Session-Id"); claudeCodeSessionID != "" {
			return claudeCodeSessionID
		}
	}

	var req map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return ""
	}

	if user, ok := req["user"].(string); ok && user != "" {
		return user
	}
	if userID, ok := req["user_id"].(string); ok && userID != "" {
		return userID
	}
	if promptCacheKey, ok := req["prompt_cache_key"].(string); ok && promptCacheKey != "" {
		return promptCacheKey
	}
	if metadata, ok := req["metadata"].(map[string]interface{}); ok {
		if userID, ok := metadata["user_id"].(string); ok && userID != "" {
			return userID
		}
		if flattened := flattenMetadataUserID(metadata["user_id"]); flattened != "" {
			return flattened
		}
	}

	if c != nil {
		if geminiUserID := c.GetHeader("X-Gemini-Api-Privileged-User-Id"); geminiUserID != "" {
			return geminiUserID
		}

		if clientRequestID := c.GetHeader("X-Client-Request-Id"); clientRequestID != "" {
			return clientRequestID
		}
	}

	return ""
}

func flattenMetadataUserID(raw interface{}) string {
	if raw == nil {
		return ""
	}

	parsed, ok := raw.(map[string]interface{})
	if !ok || len(parsed) == 0 {
		return ""
	}

	var parts []string
	if deviceID, ok := parsed["device_id"].(string); ok && deviceID != "" {
		parts = append(parts, "user_"+deviceID)
		if accountUUID, ok := parsed["account_uuid"].(string); ok && accountUUID != "" {
			parts = append(parts, "account_"+accountUUID)
		}
		if sessionID, ok := parsed["session_id"].(string); ok && sessionID != "" {
			parts = append(parts, "session_"+sessionID)
		}
	} else {
		keys := make([]string, 0, len(parsed))
		for k := range parsed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v, ok := parsed[k].(string); ok && v != "" {
				parts = append(parts, k+"_"+v)
			}
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "_")
}

// ExtractAgentContext 提取请求的代理上下文，用于 subagent 观测与角色路由。
// Codex Responses：通过 client_metadata 精确识别 subagent（exact）。
// Claude Code Messages：优先使用 X-Claude-Code-Agent-Id 精确识别 subagent；无该头时
// 再通过 metadata.user_id + 消息/工具数量弱识别。
func ExtractAgentContext(c *gin.Context, bodyBytes []byte) *types.AgentContext {
	ctx := &types.AgentContext{}

	var req map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return ctx
	}

	// Codex / Responses 精确识别
	if clientMeta, ok := req["client_metadata"].(map[string]interface{}); ok {
		subagentKind, _ := clientMeta["x-openai-subagent"].(string)
		parentThread, _ := clientMeta["x-codex-parent-thread-id"].(string)

		if subagentKind != "" {
			ctx.AgentRole = "subagent"
			ctx.AgentType = "codex_subagent"
			ctx.ParentThreadID = parentThread
			ctx.Confidence = "exact"
			return ctx
		}
		if parentThread != "" {
			ctx.AgentRole = "subagent"
			ctx.AgentType = "codex_subagent"
			ctx.ParentThreadID = parentThread
			ctx.Confidence = "exact"
			return ctx
		}
	}

	// Claude Code 弱识别（仅观测，不强制路由）
	if metadata, ok := req["metadata"].(map[string]interface{}); ok {
		if hasClaudeCodeSessionID(metadata) {
			if c != nil && strings.TrimSpace(c.GetHeader("X-Claude-Code-Agent-Id")) != "" {
				ctx.AgentRole = "subagent"
				ctx.AgentType = "claude_code_subagent"
				ctx.Confidence = "exact"
				return ctx
			}
			if isLikelyClaudeCodeSubagent(req) {
				ctx.AgentRole = "subagent"
				ctx.AgentType = "claude_code_subagent"
				ctx.Confidence = "heuristic"
				return ctx
			}
			// 带有 Claude Code session 标记但不符合 subagent 启发式 → 视为主对话
			ctx.AgentRole = "main"
			return ctx
		}
	}

	return ctx
}

// hasClaudeCodeSessionID 判断 metadata 是否为 Claude Code 风格（含 device_id/session_id 的 user_id 对象）
func hasClaudeCodeSessionID(metadata map[string]interface{}) bool {
	userID, ok := metadata["user_id"].(string)
	if !ok || userID == "" {
		return false
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(userID), &parsed); err != nil {
		return false
	}
	_, hasDevice := parsed["device_id"].(string)
	_, hasSession := parsed["session_id"].(string)
	return hasDevice || hasSession
}

// isLikelyClaudeCodeSubagent 弱识别 Claude Code subagent：
// subagent 通常携带大量工具定义但 user 消息极少（仅任务 prompt）。
func isLikelyClaudeCodeSubagent(req map[string]interface{}) bool {
	tools, _ := req["tools"].([]interface{})
	if len(tools) < 5 {
		return false
	}

	messages, _ := req["messages"].([]interface{})
	userMsgCount := 0
	for _, m := range messages {
		if msg, ok := m.(map[string]interface{}); ok {
			if role, _ := msg["role"].(string); role == "user" {
				userMsgCount++
			}
		}
	}
	return userMsgCount <= 2
}

func PrepareUpstreamHeaders(c *gin.Context, targetHost string) http.Header {
	headers := c.Request.Header.Clone()

	// 设置正确的Host头部
	headers.Set("Host", targetHost)

	// 移除代理相关头部，降低被识别为中转层的风险
	headers.Del("x-proxy-key")
	headers.Del("X-Forwarded-For")
	headers.Del("X-Forwarded-Host")
	headers.Del("X-Forwarded-Proto")
	headers.Del("X-Real-IP")
	headers.Del("Via")
	headers.Del("Forwarded")

	// 移除 Accept-Encoding，让 Go 的 http.Client 自动处理 gzip 压缩/解压缩
	// 这样可以避免在原始请求包含 Accept-Encoding 时 Go 不自动解压缩的问题
	headers.Del("Accept-Encoding")

	// 强制去重 Content-Type（部分客户端可能发送重复的 Content-Type 头）
	headers.Set("Content-Type", "application/json")

	return headers
}

// PrepareMinimalHeaders 准备最小化请求头（适用于非Claude渠道如OpenAI、Gemini等）
// 只保留必要的头部：Content-Type和Host，不包含任何Anthropic特定头部
// 注意：不设置Accept-Encoding，让Go的http.Client自动处理gzip压缩
func PrepareMinimalHeaders(targetHost string) http.Header {
	headers := http.Header{}

	// 只设置最基本的头部
	headers.Set("Host", targetHost)
	headers.Set("Content-Type", "application/json")
	// 不显式设置Accept-Encoding，让Go的http.Client自动添加并处理gzip解压

	return headers
}

// SetAuthenticationHeader 设置认证头部（根据密钥格式智能选择）
func SetAuthenticationHeader(headers http.Header, apiKey string) {
	// 移除旧的认证头
	headers.Del("authorization")
	headers.Del("x-api-key")
	headers.Del("x-goog-api-key")

	// Claude 官方密钥格式（sk-ant-api03-xxx）使用 x-api-key
	// 符合 Claude API 官方推荐的认证方式
	if strings.HasPrefix(apiKey, "sk-ant-") {
		headers.Set("x-api-key", apiKey)
	} else {
		// 其他格式密钥使用 Authorization: Bearer
		// 适用于 OpenAI、自定义密钥等
		headers.Set("Authorization", "Bearer "+apiKey)
	}
}

// SetAuthenticationHeaderWithOverride 设置认证头部，authHeader 为空或 auto 时保留原有智能选择逻辑。
func SetAuthenticationHeaderWithOverride(headers http.Header, apiKey, authHeader string) {
	switch strings.ToLower(strings.TrimSpace(authHeader)) {
	case "bearer":
		headers.Del("authorization")
		headers.Del("x-api-key")
		headers.Del("x-goog-api-key")
		headers.Set("Authorization", "Bearer "+apiKey)
	case "x-api-key":
		headers.Del("authorization")
		headers.Del("x-api-key")
		headers.Del("x-goog-api-key")
		headers.Set("x-api-key", apiKey)
	default:
		SetAuthenticationHeader(headers, apiKey)
	}
}

// HasAuthenticationHeaderOverride 判断是否配置了显式认证头覆盖。
func HasAuthenticationHeaderOverride(authHeader string) bool {
	switch strings.ToLower(strings.TrimSpace(authHeader)) {
	case "bearer", "x-api-key":
		return true
	default:
		return false
	}
}

// SetGeminiAuthenticationHeader 设置Gemini认证头部
func SetGeminiAuthenticationHeader(headers http.Header, apiKey string) {
	headers.Del("authorization")
	headers.Del("x-api-key")
	headers.Set("x-goog-api-key", apiKey)
}

// ApplyCustomHeaders 应用自定义请求头（覆盖或添加）
// 使用 http.Header.Set 会自动规范化 key 为 CanonicalHeaderKey 格式
// 跳过空白 key 或 value
func ApplyCustomHeaders(headers http.Header, customHeaders map[string]string) {
	for key, value := range customHeaders {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		headers.Set(key, value)
	}
}

// CopilotProtectedHeaders 是 Copilot 运行时依赖的敏感头，customHeaders 不得覆盖。
var CopilotProtectedHeaders = map[string]bool{
	"authorization":          true,
	"x-api-key":              true,
	"openai-organization":    true,
	"openai-intent":          true,
	"copilot-integration-id": true,
	"editor-version":         true,
	"editor-plugin-version":  true,
	"user-agent":             true,
}

// ApplyCustomHeadersProtected 应用自定义请求头，但跳过受保护的头（大小写不敏感匹配）。
func ApplyCustomHeadersProtected(headers http.Header, customHeaders map[string]string, protected map[string]bool) {
	for key, value := range customHeaders {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		if protected[strings.ToLower(key)] {
			continue
		}
		headers.Set(key, value)
	}
}

// EnsureCompatibleUserAgent 确保兼容的User-Agent（仅在必要时设置）
func EnsureCompatibleUserAgent(headers http.Header, serviceType string) {
	userAgent := headers.Get("User-Agent")

	// 仅在Claude服务类型且客户端未提供 User-Agent 时才设置默认值，有 UA 则透传
	if serviceType == "claude" {
		if userAgent == "" {
			headers.Set("User-Agent", "claude-cli/2.0.34 (external, cli)")
		}
	}
}

// ForwardResponseHeaders 转发上游响应头到客户端
// 作为透明代理，应该转发所有响应头，只过滤框架自动处理的头部
func ForwardResponseHeaders(upstreamHeaders http.Header, clientWriter http.ResponseWriter) {
	// 不应转发的头部列表（由框架或代理层自动处理）
	skipHeaders := map[string]bool{
		"transfer-encoding": true, // 由框架自动处理
		"content-length":    true, // 由框架自动处理
		"connection":        true, // 代理层控制
		"content-encoding":  true, // 如果已解压则不应转发
	}

	// 复制所有上游响应头到客户端
	for key, values := range upstreamHeaders {
		lowerKey := strings.ToLower(key)

		// 跳过不应转发的头部
		if skipHeaders[lowerKey] {
			continue
		}

		// 转发头部（可能有多个值）
		for _, value := range values {
			clientWriter.Header().Add(key, value)
		}
	}
}
