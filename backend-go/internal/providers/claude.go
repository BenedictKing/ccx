package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/passthrough"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// ClaudeProvider Claude 提供商（直接透传）
type ClaudeProvider struct{}

// ConvertToProviderRequest 转换为 Claude 请求（实现真正的透传）
func claudeRequestPath(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}
	path := c.Request.URL.Path
	if routePrefix := c.Param("routePrefix"); routePrefix != "" {
		path = strings.TrimPrefix(path, "/"+routePrefix)
	}
	return path
}

func (p *ClaudeProvider) ConvertToProviderRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string) (*http.Request, []byte, error) {
	// 读取原始请求体
	bodyBytes, err := getRequestBodyBytes(c)
	if err != nil {
		return nil, nil, err
	}

	path := claudeRequestPath(c)

	// 模型重定向：仅修改 model 字段，保持其他内容不变
	if upstream.ModelMapping != nil && len(upstream.ModelMapping) > 0 {
		bodyBytes = passthrough.PatchTopLevelModel(bodyBytes, upstream)
	}

	// 构建目标URL
	// 智能拼接逻辑：
	// 1. 如果 baseURL 以 # 结尾，跳过自动添加 /v1
	// 2. 如果 baseURL 已包含版本号后缀（如 /v1, /v2, /v3），直接拼接端点路径
	// 3. 如果 baseURL 不包含版本号后缀，自动添加 /v1 再拼接端点路径
	// 先剥离 routePrefix（如 /glm），再剥离 /v1，得到纯端点路径（如 /messages）
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
	utils.EnsureCompatibleUserAgent(req.Header, "claude")
	utils.SetAuthenticationHeader(req.Header, apiKey)

	return req, bodyBytes, nil
}

// ConvertToClaudeResponse 转换为 Claude 响应（直接透传）
func (p *ClaudeProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	var claudeResp types.ClaudeResponse
	if err := json.Unmarshal(providerResp.Body, &claudeResp); err != nil {
		return nil, err
	}
	return &claudeResp, nil
}

// HandleStreamResponse 处理流式响应（直接透传）
func (p *ClaudeProvider) HandleStreamResponse(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	ctx = normalizeStreamContext(ctx)
	eventChan := make(chan string, 100)
	errChan := make(chan error, 1)

	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer closeStreamBodyOnCancel(ctx, body)()
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
			if !sendStreamEvent(ctx, eventChan, eventBuf.String()) {
				return
			}
			eventBuf.Reset()
		}

		for scanner.Scan() {
			if isStreamContextCanceled(ctx) {
				return
			}
			line := normalizeSSEFieldLine(scanner.Text())

			// 检测是否发送了 tool_use 相关的 stop_reason（通常在 data 行中）
			if strings.Contains(line, `"stop_reason":"tool_use"`) ||
				strings.Contains(line, `"stop_reason": "tool_use"`) {
				toolUseStopEmitted = true
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
			if isStreamContextCanceled(ctx) {
				return
			}
			// 在 tool_use 场景下，客户端主动断开是正常行为
			// 如果已经发送了 tool_use stop 事件，并且错误是连接断开相关的，则忽略该错误
			errMsg := err.Error()
			if toolUseStopEmitted && (strings.Contains(errMsg, "broken pipe") ||
				strings.Contains(errMsg, "connection reset") ||
				strings.Contains(errMsg, "EOF")) {
				// 这是预期的客户端行为，不报告错误
				return
			}
			sendStreamError(ctx, errChan, err)
		}
	}()

	return eventChan, errChan, nil
}
