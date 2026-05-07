// Package messages 提供 Claude Messages API 的处理器
package messages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/handlers/wire"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/pipeline"
	pipelinemw "github.com/BenedictKing/ccx/internal/pipeline/middleware"
	"github.com/BenedictKing/ccx/internal/pricing"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/usage"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// Handler Messages API 代理处理器
// 支持多渠道调度：当配置多个渠道时自动启用
func Handler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		// 先进行认证
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		startTime := time.Now()

		// 读取请求体
		bodyBytes, err := common.ReadRequestBody(c, envCfg.MaxRequestBodySize)
		if err != nil {
			return
		}
		rawBodyBytes := bodyBytes

		// 预处理：移除空 signature 字段，预防 400 错误
		// modified 表示请求体是否被修改，详细日志由 RemoveEmptySignatures 内部记录
		bodyBytes, modified := common.RemoveEmptySignatures(bodyBytes, envCfg.EnableRequestLogs, "Messages")
		_ = modified // 保留以便未来扩展（如需在 handler 层面做额外处理）

		// 预处理：清理历史 thinking 内容块/字段，预防上游参数校验 400
		bodyBytes, thinkingModified := common.SanitizeMalformedThinkingBlocks(bodyBytes, envCfg.EnableRequestLogs, "Messages")
		_ = thinkingModified // 保留以便未来扩展（如需在 handler 层面做额外处理）

		// 预处理：移除 system 中的 cch= 计费参数
		if cfgManager.GetStripBillingHeader() {
			bodyBytes, _ = common.RemoveBillingHeaders(bodyBytes, envCfg.EnableRequestLogs, "Messages")
		}

		// 预处理：规范化 metadata.user_id（兼容 Claude Code v2.1.78+ JSON 对象格式）
		bodyBytes = common.NormalizeMetadataUserID(bodyBytes)
		c.Set("requestBodyBytes", bodyBytes)

		// 解析请求
		var claudeReq types.ClaudeRequest
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &claudeReq); err != nil {
				c.JSON(400, gin.H{"error": fmt.Sprintf("Invalid request body: %v", err)})
				return
			}
		}

		// 提取 user_id 用于 Trace 亲和性
		userID := utils.ExtractUnifiedSessionID(c, bodyBytes)

		// 记录原始请求信息（仅在入口处记录一次）
		common.LogOriginalRequest(c, bodyBytes, envCfg, "Messages")

		// 检查是否为多渠道模式
		isMultiChannel := channelScheduler.IsMultiChannelMode(scheduler.ChannelKindMessages)

		if isMultiChannel {
			handleMultiChannel(c, envCfg, cfgManager, channelScheduler, rawBodyBytes, claudeReq, userID, startTime)
		} else {
			handleSingleChannel(c, envCfg, cfgManager, channelScheduler, rawBodyBytes, claudeReq, userID, startTime)
		}
	})
}

// handleMultiChannel 处理多渠道代理请求
//
// PR3 T8a：路由到 pipeline.Process（wire.LBOutboundAdapter 内部驱动渠道选择 +
// failover），不再走 TryUpstreamWithAllKeys。
func handleMultiChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	claudeReq types.ClaudeRequest,
	userID string,
	startTime time.Time,
) {
	processViaPipeline(c, envCfg, cfgManager, channelScheduler, &claudeReq, bodyBytes, userID, claudeReq.Model, "", startTime)
}

// handleSingleChannel 处理单渠道代理请求
//
// PR3 T8a：路由到 pipeline.Process。空渠道 / 空 key 的早退保留在此处，避免
// pipeline 主循环再做一次同等校验。
func handleSingleChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	claudeReq types.ClaudeRequest,
	userID string,
	startTime time.Time,
) {
	upstream, _, err := cfgManager.GetCurrentUpstreamWithIndex()
	if err != nil {
		c.JSON(503, gin.H{
			"error": "未配置任何渠道，请先在管理界面添加渠道",
			"code":  "NO_UPSTREAM",
		})
		return
	}
	if len(upstream.APIKeys) == 0 {
		c.JSON(503, gin.H{
			"error": fmt.Sprintf("当前渠道 \"%s\" 未配置API密钥", upstream.Name),
			"code":  "NO_API_KEYS",
		})
		return
	}
	if providers.GetProvider(upstream.ServiceType) == nil {
		c.JSON(400, gin.H{"error": "Unsupported service type"})
		return
	}

	processViaPipeline(c, envCfg, cfgManager, channelScheduler, &claudeReq, bodyBytes, userID, claudeReq.Model, "", startTime)
}

// CountTokensHandler 处理 /v1/messages/count_tokens 请求
func CountTokensHandler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, _ *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		// 使用统一的请求体读取函数，应用大小限制
		bodyBytes, err := common.ReadRequestBody(c, envCfg.MaxRequestBodySize)
		if err != nil {
			// ReadRequestBody 已经返回了错误响应
			return
		}
		c.Set("requestBodyBytes", bodyBytes)

		var req struct {
			Model    string      `json:"model"`
			System   interface{} `json:"system"`
			Messages interface{} `json:"messages"`
			Tools    interface{} `json:"tools"`
		}
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid JSON"})
			return
		}

		inputTokens := utils.EstimateRequestTokens(bodyBytes)

		c.JSON(200, gin.H{
			"input_tokens": inputTokens,
		})

		if envCfg.EnableResponseLogs {
			log.Printf("[Messages-Token] CountTokens本地估算: model=%s, input_tokens=%d", req.Model, inputTokens)
		}
	}
}

// ----------------------------------------------------------------------------
// PR3 T8a: pipeline 路由 helper
// ----------------------------------------------------------------------------

var (
	globalUsageStore  usage.UsageStore
	globalPriceLoader *pricing.Loader
)

// SetGlobalDependencies 由 main.go 在启动阶段调用，注入 usage/pricing。
// 测试可通过同一函数注入 noop 实现，避免 nil 依赖。
func SetGlobalDependencies(store usage.UsageStore, loader *pricing.Loader) {
	globalUsageStore, globalPriceLoader = store, loader
}

// ginInjectingInbound 装饰 messages.NewInbound：在 TransformRequest 阶段把
// gin.Context 写入 llm.Request.Metadata，并立即驱动 LBOutboundAdapter
// 选定首个 channel/key（pipeline 主循环只在错误后才会调 NextChannel）。
type ginInjectingInbound struct {
	Inner pipeline.Inbound
	Gin   *gin.Context
	LB    *wire.LBOutboundAdapter
}

func (g *ginInjectingInbound) Format() llm.Format { return g.Inner.Format() }

func (g *ginInjectingInbound) TransformRequest(ctx context.Context, req *http.Request, body []byte) (*llm.Request, error) {
	llmReq, err := g.Inner.TransformRequest(ctx, req, body)
	if err != nil {
		return nil, err
	}
	adapters.SetGinContext(llmReq, g.Gin)
	g.LB.Request = llmReq
	if e := g.LB.NextChannel(ctx); e != nil {
		return nil, e
	}
	return llmReq, nil
}

func (g *ginInjectingInbound) TransformResponse(ctx context.Context, resp *llm.Response) ([]byte, http.Header, error) {
	return g.Inner.TransformResponse(ctx, resp)
}

func (g *ginInjectingInbound) TransformStream(ctx context.Context, in llm.Stream[*llm.Response]) llm.Stream[*llm.StreamEvent] {
	return g.Inner.TransformStream(ctx, in)
}

// processViaPipeline 是 PR3 T8a 把 messages handler 切流量到 pipeline.Process
// 的入口；single-channel / multi-channel 共享同一份代码。
func processViaPipeline(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	claudeReq *types.ClaudeRequest,
	bodyBytes []byte,
	userID, parsedModel, routePrefix string,
	requestStart time.Time,
) {
	mm := channelScheduler.GetMessagesMetricsManager()
	common.SetLBAPIType(c, "messages")
	common.SetLBMetricsManager(c, mm)

	if requestStart.IsZero() {
		requestStart = time.Now()
	}
	attemptInfo := &pipelinemw.AttemptInfo{APIType: "Messages"}
	ctx := pipelinemw.WithAttemptInfo(c.Request.Context(), attemptInfo)

	lb := wire.NewLBOutboundAdapter(NewOutbound(), channelScheduler, scheduler.ChannelKindMessages, requestStart)
	lb.AttemptInfo = attemptInfo
	lb.LBMetricsMgr = mm
	lb.UsageStore = globalUsageStore
	lb.PriceLoader = globalPriceLoader
	lb.UserID = userID
	lb.Model = parsedModel
	lb.RoutePrefix = routePrefix
	outbound, _ := lb.Inner.(*outboundAdapter)

	inbound := &ginInjectingInbound{Inner: NewInbound(), Gin: c, LB: lb}

	exec := pipeline.ExecutorFunc(func(_ context.Context, req *http.Request) (*http.Response, error) {
		var upstream *config.UpstreamConfig
		if attemptInfo != nil {
			upstream = attemptInfo.Upstream
		}
		return common.SendRequest(req, upstream, envCfg, claudeReq.Stream, "Messages")
	})

	p := pipeline.NewFactory(exec).Pipeline(inbound, lb, wire.BuildPipelineOpts(cfgManager, lb)...)
	result, err := p.Process(ctx, c.Request, bodyBytes)

	durationMs := time.Since(requestStart).Milliseconds()
	var errCode string
	if err != nil {
		errCode = pipelineErrCode(err)
	}
	// llmUsage 用 defer-closure 捕获指针，使 stream 路径在事件流消费完成后
	// 通过 outbound.TakeStreamUsage 把累计的 SSE usage 注入。pipeline.Result.Response
	// 在 stream 路径下为 nil，是 PR3 T8a B2 既有限制——这里以"outbound 侧信道"
	// 补全，handler 主流程不动。
	var llmUsage llm.Usage
	if result != nil && result.Response != nil && result.Response.Usage != nil {
		llmUsage = *result.Response.Usage
	}
	defer func() {
		if outbound != nil {
			if streamUsage := outbound.TakeStreamUsage(); streamUsage != nil && llmUsage.IsZero() {
				llmUsage = *streamUsage
			}
		}
		lb.Finalize(ctx, err == nil, errCode, parsedModel, llmUsage, durationMs)
	}()

	if err != nil {
		writePipelineError(c, err)
		return
	}
	if result == nil {
		return
	}

	if result.Stream && result.EventStream != nil {
		writePipelineStream(c, result.EventStream)
		return
	}
	if result.Response != nil {
		writePipelineResponse(c, result.Response)
	}
}

// writePipelineResponse 把非流式 *llm.Response 写回客户端。
func writePipelineResponse(c *gin.Context, resp *llm.Response) {
	if resp == nil {
		return
	}
	if c.Writer.Written() {
		return
	}
	for k, v := range resp.Header {
		if len(v) == 0 {
			continue
		}
		c.Writer.Header().Set(k, v[0])
	}
	if resp.Header.Get("Content-Type") == "" {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	c.Writer.WriteHeader(status)
	_, _ = c.Writer.Write(resp.Body)
}

// writePipelineStream 把 SSE 事件流逐帧写回客户端。
func writePipelineStream(c *gin.Context, stream llm.Stream[*llm.StreamEvent]) {
	defer func() { _ = stream.Close() }()
	if !c.Writer.Written() {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.WriteHeader(http.StatusOK)
	}
	flusher, _ := c.Writer.(http.Flusher)
	for stream.Next() {
		ev := stream.Current()
		if ev == nil || len(ev.Data) == 0 {
			continue
		}
		if _, err := c.Writer.Write(ev.Data); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// writePipelineError 把 pipeline.Process 返回的错误回写客户端。
func writePipelineError(c *gin.Context, err error) {
	if c.Writer.Written() {
		return
	}
	if errors.Is(err, pipeline.ErrChannelExhausted) {
		c.JSON(http.StatusBadGateway, gin.H{"error": "all channels failed", "detail": err.Error()})
		return
	}
	c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
}

// pipelineErrCode 将 pipeline 错误归类为简短 error code，写入 UsageRecord。
func pipelineErrCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, pipeline.ErrChannelExhausted):
		return "channel_exhausted"
	case errors.Is(err, pipeline.ErrEmptyResponse):
		return "empty_response"
	case errors.Is(err, common.ErrInvalidResponseBody):
		return "invalid_response_body"
	default:
		return "upstream_error"
	}
}
