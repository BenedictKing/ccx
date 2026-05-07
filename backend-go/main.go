package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers"
	"github.com/BenedictKing/ccx/internal/handlers/chat"
	"github.com/BenedictKing/ccx/internal/handlers/gemini"
	"github.com/BenedictKing/ccx/internal/handlers/images"
	"github.com/BenedictKing/ccx/internal/handlers/messages"
	"github.com/BenedictKing/ccx/internal/handlers/responses"
	"github.com/BenedictKing/ccx/internal/logger"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/pricing"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/usage"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

//go:embed all:frontend/dist
var frontendFS embed.FS

func main() {
	// 加载环境变量
	if err := godotenv.Load(); err != nil {
		log.Println("没有找到 .env 文件，使用环境变量或默认值")
	}

	// 设置版本信息到 handlers 包
	handlers.SetVersionInfo(Version, BuildTime, GitCommit)

	// 初始化配置管理器
	envCfg := config.NewEnvConfig()

	// 初始化日志系统（必须在其他初始化之前）
	logCfg := &logger.Config{
		LogDir:     envCfg.LogDir,
		LogFile:    envCfg.LogFile,
		MaxSize:    envCfg.LogMaxSize,
		MaxBackups: envCfg.LogMaxBackups,
		MaxAge:     envCfg.LogMaxAge,
		Compress:   envCfg.LogCompress,
		Console:    envCfg.LogToConsole,
	}
	if err := logger.Setup(logCfg); err != nil {
		log.Fatalf("初始化日志系统失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(".config/config.json")
	if err != nil {
		log.Fatalf("初始化配置管理器失败: %v", err)
	}
	defer func() { _ = cfgManager.Close() }()

	// 初始化会话管理器（Responses API 专用）
	sessionManager := session.NewSessionManager(
		24*time.Hour, // 24小时过期
		100,          // 最多100条消息
		100000,       // 最多100k tokens
	)
	log.Printf("[Session-Init] 会话管理器已初始化")

	// 初始化指标持久化存储（可选）
	var metricsStore *metrics.SQLiteStore
	if envCfg.MetricsPersistenceEnabled {
		var err error
		metricsStore, err = metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{
			DBPath:        ".config/metrics.db",
			RetentionDays: envCfg.MetricsRetentionDays,
		})
		if err != nil {
			log.Printf("[Metrics-Init] 警告: 初始化指标持久化存储失败: %v，将使用纯内存模式", err)
			metricsStore = nil
		}
	} else {
		log.Printf("[Metrics-Init] 指标持久化已禁用，使用纯内存模式")
	}

	// 初始化多渠道调度器（Messages、Responses、Gemini 和 Chat 使用独立的指标管理器）
	var messagesMetricsManager, responsesMetricsManager, geminiMetricsManager, chatMetricsManager, imagesMetricsManager *metrics.MetricsManager
	if metricsStore != nil {
		messagesMetricsManager = metrics.NewMetricsManagerWithPersistence(
			envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold, metricsStore, "messages")
		responsesMetricsManager = metrics.NewMetricsManagerWithPersistence(
			envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold, metricsStore, "responses")
		geminiMetricsManager = metrics.NewMetricsManagerWithPersistence(
			envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold, metricsStore, "gemini")
		chatMetricsManager = metrics.NewMetricsManagerWithPersistence(
			envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold, metricsStore, "chat")
		imagesMetricsManager = metrics.NewMetricsManagerWithPersistence(
			envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold, metricsStore, "images")
	} else {
		messagesMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
		responsesMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
		geminiMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
		chatMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
		imagesMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
	}
	traceAffinityManager := session.NewTraceAffinityManager()

	// 初始化 URL 管理器（非阻塞，动态排序）
	urlManager := warmup.NewURLManager(30*time.Second, 3) // 30秒冷却期，连续3次失败后移到末尾
	log.Printf("[URLManager-Init] URL管理器已初始化 (冷却期: 30秒, 最大连续失败: 3)")

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, messagesMetricsManager, responsesMetricsManager, geminiMetricsManager, chatMetricsManager, imagesMetricsManager, traceAffinityManager, urlManager)
	log.Printf("[Scheduler-Init] 多渠道调度器已初始化 (失败率阈值: %.0f%%, 滑动窗口: %d)",
		messagesMetricsManager.GetFailureThreshold()*100, messagesMetricsManager.GetWindowSize())

	// 初始化 pricing / usage（PR3 T8a 起 messages handler 经 pipeline.Process
	// 路径调用 wire.LBOutboundAdapter.Finalize，需要 pricing.Loader + usage.Store）。
	priceLoader, err := pricing.NewLoaderFromEnv()
	if err != nil {
		log.Fatalf("[Main-Pricing] 初始化价格表失败: %v", err)
	}
	usageStore, err := usage.NewNDJSONStore(usage.DefaultConfig())
	if err != nil {
		log.Fatalf("[Main-Usage] 初始化 usage store 失败: %v", err)
	}
	defer func() { _ = usageStore.Close() }()
	messages.SetGlobalDependencies(usageStore, priceLoader)
	chat.SetGlobalDependencies(usageStore, priceLoader)
	log.Printf("[Main-Wiring] messages 模块依赖注入完成 (price_version=%s)", priceLoader.Version())

	// 设置 Gin 模式
	if envCfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	// 创建路由器（使用自定义 Logger，根据 QUIET_POLLING_LOGS 配置过滤轮询日志）
	r := gin.New()
	r.Use(middleware.FilteredLogger(envCfg))
	r.Use(gin.Recovery())

	// 配置 CORS
	r.Use(middleware.CORSMiddleware(envCfg))

	// 静态资源 Gzip 压缩（排除 API 端点）
	r.Use(middleware.GzipMiddleware())

	// Web UI 访问控制中间件
	r.Use(middleware.WebAuthMiddleware(envCfg, cfgManager))

	// 健康检查端点（固定路径 /health，与 Dockerfile HEALTHCHECK 保持一致）
	healthHandler := handlers.HealthCheck(envCfg, cfgManager)
	r.GET("/health", healthHandler)
	r.GET("/:routePrefix/health", healthHandler)

	// 配置保存端点
	r.POST("/admin/config/save", handlers.SaveConfigHandler(cfgManager))

	// 开发信息端点
	if envCfg.IsDevelopment() {
		r.GET("/admin/dev/info", handlers.DevInfo(envCfg, cfgManager))
	}

	// Web 管理界面 API 路由
	apiGroup := r.Group("/api")
	{
		// Messages 渠道管理
		apiGroup.GET("/messages/channels", messages.GetUpstreams(cfgManager))
		apiGroup.POST("/messages/channels", messages.AddUpstream(cfgManager))
		apiGroup.PUT("/messages/channels/:id", messages.UpdateUpstream(cfgManager, channelScheduler))
		apiGroup.DELETE("/messages/channels/:id", messages.DeleteUpstream(cfgManager, channelScheduler))
		apiGroup.POST("/messages/channels/:id/keys", messages.AddApiKey(cfgManager))
		apiGroup.DELETE("/messages/channels/:id/keys/:apiKey", messages.DeleteApiKey(cfgManager))
		apiGroup.POST("/messages/channels/:id/keys/:apiKey/top", messages.MoveApiKeyToTop(cfgManager))
		apiGroup.POST("/messages/channels/:id/keys/:apiKey/bottom", messages.MoveApiKeyToBottom(cfgManager))
		apiGroup.POST("/messages/channels/:id/keys/restore", handlers.RestoreBlacklistedKey(cfgManager, "Messages"))

		// Messages 多渠道调度 API
		apiGroup.POST("/messages/channels/reorder", messages.ReorderChannels(cfgManager))
		apiGroup.PATCH("/messages/channels/:id/status", messages.SetChannelStatus(cfgManager))
		apiGroup.POST("/messages/channels/:id/resume", handlers.ResumeChannel(channelScheduler, cfgManager, false))
		apiGroup.POST("/messages/channels/:id/promotion", messages.SetChannelPromotion(cfgManager))
		apiGroup.GET("/messages/channels/metrics", handlers.GetChannelMetricsWithConfig(messagesMetricsManager, cfgManager, false))
		apiGroup.GET("/messages/channels/metrics/history", handlers.GetChannelMetricsHistory(messagesMetricsManager, cfgManager, false))
		apiGroup.GET("/messages/channels/:id/keys/metrics/history", handlers.GetChannelKeyMetricsHistory(messagesMetricsManager, cfgManager, false))
		apiGroup.GET("/messages/channels/scheduler/stats", handlers.GetSchedulerStats(channelScheduler))
		apiGroup.GET("/messages/global/stats/history", handlers.GetGlobalStatsHistory(messagesMetricsManager))
		apiGroup.GET("/messages/channels/dashboard", handlers.GetChannelDashboard(cfgManager, channelScheduler)) // 统一 dashboard 端点，支持 ?type=messages|responses|chat|gemini
		apiGroup.GET("/messages/ping/:id", messages.PingChannel(cfgManager))
		apiGroup.GET("/messages/ping", messages.PingAllChannels(cfgManager))
		apiGroup.POST("/messages/channels/:id/models", messages.GetChannelModels(cfgManager))
		apiGroup.GET("/messages/models/stats/history", handlers.GetModelStatsHistory(messagesMetricsManager))
		apiGroup.GET("/messages/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages)))
		apiGroup.POST("/messages/channels/:id/capability-test", handlers.TestChannelCapability(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages), "messages"))
		apiGroup.GET("/messages/channels/:id/capability-snapshot", handlers.GetCapabilitySnapshot(cfgManager, "messages"))
		apiGroup.GET("/messages/channels/:id/capability-test/:jobId", handlers.GetCapabilityTestJobStatus(cfgManager, "messages"))
		apiGroup.DELETE("/messages/channels/:id/capability-test/:jobId", handlers.CancelCapabilityTestJob(cfgManager, "messages"))
		apiGroup.POST("/messages/channels/:id/capability-test/:jobId/retry", handlers.RetryCapabilityTestModel(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages), "messages"))

		// Responses 渠道管理
		apiGroup.GET("/responses/channels", responses.GetUpstreams(cfgManager))
		apiGroup.POST("/responses/channels", responses.AddUpstream(cfgManager))
		apiGroup.PUT("/responses/channels/:id", responses.UpdateUpstream(cfgManager, channelScheduler))
		apiGroup.DELETE("/responses/channels/:id", responses.DeleteUpstream(cfgManager, channelScheduler))
		apiGroup.POST("/responses/channels/:id/keys", responses.AddApiKey(cfgManager))
		apiGroup.DELETE("/responses/channels/:id/keys/:apiKey", responses.DeleteApiKey(cfgManager))
		apiGroup.POST("/responses/channels/:id/keys/:apiKey/top", responses.MoveApiKeyToTop(cfgManager))
		apiGroup.POST("/responses/channels/:id/keys/:apiKey/bottom", responses.MoveApiKeyToBottom(cfgManager))
		apiGroup.POST("/responses/channels/:id/keys/restore", handlers.RestoreBlacklistedKey(cfgManager, "Responses"))

		// Responses 多渠道调度 API
		apiGroup.POST("/responses/channels/reorder", responses.ReorderChannels(cfgManager))
		apiGroup.PATCH("/responses/channels/:id/status", responses.SetChannelStatus(cfgManager))
		apiGroup.POST("/responses/channels/:id/resume", handlers.ResumeChannel(channelScheduler, cfgManager, true))
		apiGroup.POST("/responses/channels/:id/promotion", handlers.SetResponsesChannelPromotion(cfgManager))
		apiGroup.GET("/responses/channels/metrics", handlers.GetChannelMetricsWithConfig(responsesMetricsManager, cfgManager, true))
		apiGroup.GET("/responses/channels/metrics/history", handlers.GetChannelMetricsHistory(responsesMetricsManager, cfgManager, true))
		apiGroup.GET("/responses/channels/:id/keys/metrics/history", handlers.GetChannelKeyMetricsHistory(responsesMetricsManager, cfgManager, true))
		apiGroup.GET("/responses/global/stats/history", handlers.GetGlobalStatsHistory(responsesMetricsManager))
		apiGroup.GET("/responses/ping/:id", responses.PingChannel(cfgManager))
		apiGroup.GET("/responses/ping", responses.PingAllChannels(cfgManager))
		apiGroup.POST("/responses/channels/:id/models", responses.GetChannelModels(cfgManager))
		apiGroup.GET("/responses/models/stats/history", handlers.GetModelStatsHistory(responsesMetricsManager))
		apiGroup.GET("/responses/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses)))
		apiGroup.POST("/responses/channels/:id/capability-test", handlers.TestChannelCapability(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses), "responses"))
		apiGroup.GET("/responses/channels/:id/capability-snapshot", handlers.GetCapabilitySnapshot(cfgManager, "responses"))
		apiGroup.GET("/responses/channels/:id/capability-test/:jobId", handlers.GetCapabilityTestJobStatus(cfgManager, "responses"))
		apiGroup.DELETE("/responses/channels/:id/capability-test/:jobId", handlers.CancelCapabilityTestJob(cfgManager, "responses"))
		apiGroup.POST("/responses/channels/:id/capability-test/:jobId/retry", handlers.RetryCapabilityTestModel(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses), "responses"))

		// Gemini 渠道管理
		apiGroup.GET("/gemini/channels", gemini.GetUpstreams(cfgManager))
		apiGroup.POST("/gemini/channels", gemini.AddUpstream(cfgManager))
		apiGroup.PUT("/gemini/channels/:id", gemini.UpdateUpstream(cfgManager, channelScheduler))
		apiGroup.DELETE("/gemini/channels/:id", gemini.DeleteUpstream(cfgManager, channelScheduler))
		apiGroup.POST("/gemini/channels/:id/keys", gemini.AddApiKey(cfgManager))
		apiGroup.DELETE("/gemini/channels/:id/keys/:apiKey", gemini.DeleteApiKey(cfgManager))
		apiGroup.POST("/gemini/channels/:id/keys/:apiKey/top", gemini.MoveApiKeyToTop(cfgManager))
		apiGroup.POST("/gemini/channels/:id/keys/:apiKey/bottom", gemini.MoveApiKeyToBottom(cfgManager))
		apiGroup.POST("/gemini/channels/:id/keys/restore", handlers.RestoreBlacklistedKey(cfgManager, "Gemini"))

		// Gemini 多渠道调度 API
		apiGroup.POST("/gemini/channels/reorder", gemini.ReorderChannels(cfgManager))
		apiGroup.PATCH("/gemini/channels/:id/status", gemini.SetChannelStatus(cfgManager))
		apiGroup.POST("/gemini/channels/:id/resume", handlers.ResumeChannelWithKind(channelScheduler, cfgManager, scheduler.ChannelKindGemini))
		apiGroup.POST("/gemini/channels/:id/promotion", gemini.SetChannelPromotion(cfgManager))
		apiGroup.GET("/gemini/channels/metrics", handlers.GetGeminiChannelMetrics(geminiMetricsManager, cfgManager))
		apiGroup.GET("/gemini/channels/metrics/history", handlers.GetGeminiChannelMetricsHistory(geminiMetricsManager, cfgManager))
		apiGroup.GET("/gemini/channels/:id/keys/metrics/history", handlers.GetGeminiChannelKeyMetricsHistory(geminiMetricsManager, cfgManager))
		apiGroup.GET("/gemini/global/stats/history", handlers.GetGlobalStatsHistory(geminiMetricsManager))
		apiGroup.GET("/gemini/ping/:id", gemini.PingChannel(cfgManager))
		apiGroup.GET("/gemini/ping", gemini.PingAllChannels(cfgManager))
		apiGroup.POST("/gemini/channels/:id/models", gemini.GetChannelModels(cfgManager))
		apiGroup.GET("/gemini/models/stats/history", handlers.GetModelStatsHistory(geminiMetricsManager))
		apiGroup.GET("/gemini/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindGemini)))
		apiGroup.POST("/gemini/channels/:id/capability-test", handlers.TestChannelCapability(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindGemini), "gemini"))
		apiGroup.GET("/gemini/channels/:id/capability-snapshot", handlers.GetCapabilitySnapshot(cfgManager, "gemini"))
		apiGroup.GET("/gemini/channels/:id/capability-test/:jobId", handlers.GetCapabilityTestJobStatus(cfgManager, "gemini"))
		apiGroup.DELETE("/gemini/channels/:id/capability-test/:jobId", handlers.CancelCapabilityTestJob(cfgManager, "gemini"))
		apiGroup.POST("/gemini/channels/:id/capability-test/:jobId/retry", handlers.RetryCapabilityTestModel(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindGemini), "gemini"))

		// Chat 渠道管理
		apiGroup.GET("/chat/channels", chat.GetUpstreams(cfgManager))
		apiGroup.POST("/chat/channels", chat.AddUpstream(cfgManager))
		apiGroup.PUT("/chat/channels/:id", chat.UpdateUpstream(cfgManager, channelScheduler))
		apiGroup.DELETE("/chat/channels/:id", chat.DeleteUpstream(cfgManager, channelScheduler))
		apiGroup.POST("/chat/channels/:id/keys", chat.AddApiKey(cfgManager))
		apiGroup.DELETE("/chat/channels/:id/keys/:apiKey", chat.DeleteApiKey(cfgManager))
		apiGroup.POST("/chat/channels/:id/keys/:apiKey/top", chat.MoveApiKeyToTop(cfgManager))
		apiGroup.POST("/chat/channels/:id/keys/:apiKey/bottom", chat.MoveApiKeyToBottom(cfgManager))
		apiGroup.POST("/chat/channels/:id/keys/restore", handlers.RestoreBlacklistedKey(cfgManager, "Chat"))

		// Chat 多渠道调度 API
		apiGroup.POST("/chat/channels/reorder", chat.ReorderChannels(cfgManager))
		apiGroup.PATCH("/chat/channels/:id/status", chat.SetChannelStatus(cfgManager))
		apiGroup.POST("/chat/channels/:id/resume", handlers.ResumeChannelWithKind(channelScheduler, cfgManager, scheduler.ChannelKindChat))
		apiGroup.POST("/chat/channels/:id/promotion", chat.SetChannelPromotion(cfgManager))
		apiGroup.GET("/chat/channels/metrics", handlers.GetChatChannelMetrics(chatMetricsManager, cfgManager))
		apiGroup.GET("/chat/channels/metrics/history", handlers.GetChatChannelMetricsHistory(chatMetricsManager, cfgManager))
		apiGroup.GET("/chat/channels/:id/keys/metrics/history", handlers.GetChatChannelKeyMetricsHistory(chatMetricsManager, cfgManager))
		apiGroup.GET("/chat/global/stats/history", handlers.GetGlobalStatsHistory(chatMetricsManager))
		apiGroup.GET("/chat/ping/:id", chat.PingChannel(cfgManager))
		apiGroup.GET("/chat/ping", chat.PingAllChannels(cfgManager))
		apiGroup.POST("/chat/channels/:id/models", chat.GetChannelModels(cfgManager))
		apiGroup.GET("/chat/models/stats/history", handlers.GetModelStatsHistory(chatMetricsManager))
		apiGroup.GET("/chat/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat)))
		apiGroup.POST("/chat/channels/:id/capability-test", handlers.TestChannelCapability(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat), "chat"))
		apiGroup.GET("/chat/channels/:id/capability-snapshot", handlers.GetCapabilitySnapshot(cfgManager, "chat"))
		apiGroup.GET("/chat/channels/:id/capability-test/:jobId", handlers.GetCapabilityTestJobStatus(cfgManager, "chat"))
		apiGroup.DELETE("/chat/channels/:id/capability-test/:jobId", handlers.CancelCapabilityTestJob(cfgManager, "chat"))
		apiGroup.POST("/chat/channels/:id/capability-test/:jobId/retry", handlers.RetryCapabilityTestModel(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat), "chat"))
		apiGroup.GET("/chat/channels/scheduler/stats", handlers.GetSchedulerStats(channelScheduler))

		registerImagesAdminRoutes(apiGroup, cfgManager, channelScheduler, imagesMetricsManager)

		// Fuzzy 模式设置
		apiGroup.GET("/settings/fuzzy-mode", handlers.GetFuzzyMode(cfgManager))
		apiGroup.PUT("/settings/fuzzy-mode", handlers.SetFuzzyMode(cfgManager))

		// 移除计费头设置
		apiGroup.GET("/settings/strip-billing-header", handlers.GetStripBillingHeader(cfgManager))
		apiGroup.PUT("/settings/strip-billing-header", handlers.SetStripBillingHeader(cfgManager))
	}

	// 代理端点 - Messages API
	messagesHandler := messages.Handler(envCfg, cfgManager, channelScheduler)
	r.POST("/v1/messages", messagesHandler)
	r.POST("/:routePrefix/v1/messages", messagesHandler)

	countTokensHandler := messages.CountTokensHandler(envCfg, cfgManager, channelScheduler)
	r.POST("/v1/messages/count_tokens", countTokensHandler)
	r.POST("/:routePrefix/v1/messages/count_tokens", countTokensHandler)

	// 代理端点 - Models API（转发到上游）
	modelsHandler := messages.ModelsHandler(envCfg, cfgManager, channelScheduler)
	r.GET("/v1/models", modelsHandler)
	r.GET("/:routePrefix/v1/models", modelsHandler)

	modelsDetailHandler := messages.ModelsDetailHandler(envCfg, cfgManager, channelScheduler)
	r.GET("/v1/models/:model", modelsDetailHandler)
	r.GET("/:routePrefix/v1/models/:model", modelsDetailHandler)

	// 代理端点 - Responses API
	responsesHandler := responses.Handler(envCfg, cfgManager, sessionManager, channelScheduler)
	r.POST("/v1/responses", responsesHandler)
	r.POST("/:routePrefix/v1/responses", responsesHandler)

	compactHandler := responses.CompactHandler(envCfg, cfgManager, sessionManager, channelScheduler)
	r.POST("/v1/responses/compact", compactHandler)
	r.POST("/:routePrefix/v1/responses/compact", compactHandler)

	// 代理端点 - Gemini API (原生协议)
	// 使用通配符捕获 model:action 格式，如 gemini-pro:generateContent
	// 路径格式：/v1beta/models/{model}:generateContent (Gemini 原生格式)
	geminiHandler := gemini.Handler(envCfg, cfgManager, channelScheduler)
	r.POST("/v1beta/models/*modelAction", geminiHandler)
	r.POST("/:routePrefix/v1beta/models/*modelAction", geminiHandler)

	// 代理端点 - Chat Completions API (OpenAI 兼容)
	chatHandler := chat.Handler(envCfg, cfgManager, channelScheduler)
	r.POST("/v1/chat/completions", chatHandler)
	r.POST("/:routePrefix/v1/chat/completions", chatHandler)

	imagesHandler := images.Handler(envCfg, cfgManager, channelScheduler)
	r.POST("/v1/images/generations", imagesHandler)
	r.POST("/:routePrefix/v1/images/generations", imagesHandler)
	r.POST("/v1/images/edits", imagesHandler)
	r.POST("/:routePrefix/v1/images/edits", imagesHandler)
	r.POST("/v1/images/variations", imagesHandler)
	r.POST("/:routePrefix/v1/images/variations", imagesHandler)

	// 静态文件服务 (嵌入的前端)
	if envCfg.EnableWebUI {
		handlers.ServeFrontend(r, frontendFS, envCfg)
	} else {
		// 纯 API 模式
		r.GET("/", func(c *gin.Context) {
			c.JSON(200, gin.H{
				"name":    "CCX API Proxy",
				"mode":    "API Only",
				"version": "1.0.0",
				"endpoints": gin.H{
					"health": "/health",
					"proxy":  "/v1/messages",
					"config": "/admin/config/save",
				},
				"message": "Web界面已禁用，此服务器运行在纯API模式下",
			})
		})
	}

	// 启动服务器
	addr := fmt.Sprintf(":%d", envCfg.Port)
	fmt.Printf("\n[Server-Startup] CCX API代理服务器已启动\n")
	fmt.Printf("[Server-Info] 版本: %s\n", Version)
	if BuildTime != "unknown" {
		fmt.Printf("[Server-Info] 构建时间: %s\n", BuildTime)
	}
	if GitCommit != "unknown" {
		fmt.Printf("[Server-Info] Git提交: %s\n", GitCommit)
	}
	fmt.Printf("[Server-Info] 管理界面: http://localhost:%d\n", envCfg.Port)
	fmt.Printf("[Server-Info] API 地址: http://localhost:%d/v1\n", envCfg.Port)
	fmt.Printf("[Server-Info] Claude Messages: POST /v1/messages\n")
	fmt.Printf("[Server-Info] Codex Responses: POST /v1/responses\n")
	fmt.Printf("[Server-Info] Gemini API: POST /v1beta/models/{model}:generateContent\n")
	fmt.Printf("[Server-Info] Gemini API: POST /v1beta/models/{model}:streamGenerateContent\n")
	fmt.Printf("[Server-Info] Chat Completions: POST /v1/chat/completions\n")
	fmt.Printf("[Server-Info] Images Generations: POST /v1/images/generations\n")
	fmt.Printf("[Server-Info] Images Edits: POST /v1/images/edits\n")
	fmt.Printf("[Server-Info] Images Variations: POST /v1/images/variations\n")
	fmt.Printf("[Server-Info] 健康检查: GET /health\n")
	fmt.Printf("[Server-Info] 环境: %s\n", envCfg.Env)
	// 生产环境检查：必须设置有效的访问密钥
	if envCfg.IsProduction() && envCfg.ProxyAccessKey == "your-proxy-access-key" {
		log.Fatal("[Server-Fatal] 生产环境必须设置 PROXY_ACCESS_KEY，禁止使用默认值")
	}
	// 检查是否使用默认密码，给予提示
	if envCfg.ProxyAccessKey == "your-proxy-access-key" {
		fmt.Printf("[Server-Warn] 访问密钥: your-proxy-access-key (默认值，建议通过 .env 文件修改)\n")
	}
	// 提示管理密钥配置状态
	if envCfg.AdminAccessKey != "" {
		fmt.Printf("[Server-Info] 管理密钥: 已配置独立 ADMIN_ACCESS_KEY\n")
	}
	fmt.Printf("\n")

	// 创建 HTTP 服务器
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Duration(envCfg.ServerReadTimeout) * time.Millisecond, // 仅控制服务端读取入站请求，避免与上游请求超时耦合
		IdleTimeout:       120 * time.Second,
	}

	// 用于传递关闭结果
	shutdownDone := make(chan struct{})

	// 优雅关闭：监听系统信号
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		signal.Stop(sigChan) // 停止信号监听，避免资源泄漏

		log.Println("[Server-Shutdown] 收到关闭信号，正在优雅关闭服务器...")

		// 创建超时上下文
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("[Server-Shutdown] 警告: 服务器关闭时发生错误: %v", err)
		} else {
			log.Println("[Server-Shutdown] 服务器已安全关闭")
		}

		// 关闭指标持久化存储
		if metricsStore != nil {
			if err := metricsStore.Close(); err != nil {
				log.Printf("[Metrics-Shutdown] 警告: 关闭指标存储时发生错误: %v", err)
			} else {
				log.Println("[Metrics-Shutdown] 指标存储已安全关闭")
			}
		}

		close(shutdownDone)
	}()

	// 启动服务器（阻塞直到关闭）
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务器启动失败: %v", err)
	}

	// 等待关闭完成（带超时保护，避免死锁）
	select {
	case <-shutdownDone:
		// 正常关闭完成
	case <-time.After(15 * time.Second):
		log.Println("[Server-Shutdown] 警告: 等待关闭超时")
	}
}

func registerImagesAdminRoutes(apiGroup *gin.RouterGroup, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, imagesMetricsManager *metrics.MetricsManager) {
	apiGroup.GET("/images/channels", images.GetUpstreams(cfgManager))
	apiGroup.POST("/images/channels", images.AddUpstream(cfgManager))
	apiGroup.PUT("/images/channels/:id", images.UpdateUpstream(cfgManager, channelScheduler))
	apiGroup.DELETE("/images/channels/:id", images.DeleteUpstream(cfgManager, channelScheduler))
	apiGroup.POST("/images/channels/:id/keys", images.AddApiKey(cfgManager))
	apiGroup.DELETE("/images/channels/:id/keys/:apiKey", images.DeleteApiKey(cfgManager))
	apiGroup.POST("/images/channels/:id/keys/:apiKey/top", images.MoveApiKeyToTop(cfgManager))
	apiGroup.POST("/images/channels/:id/keys/:apiKey/bottom", images.MoveApiKeyToBottom(cfgManager))
	apiGroup.POST("/images/channels/:id/keys/restore", handlers.RestoreBlacklistedKey(cfgManager, "Images"))
	apiGroup.POST("/images/channels/reorder", images.ReorderChannels(cfgManager))
	apiGroup.PATCH("/images/channels/:id/status", images.SetChannelStatus(cfgManager))
	apiGroup.POST("/images/channels/:id/resume", handlers.ResumeChannelWithKind(channelScheduler, cfgManager, scheduler.ChannelKindImages))
	apiGroup.POST("/images/channels/:id/promotion", images.SetChannelPromotion(cfgManager))
	apiGroup.GET("/images/channels/metrics", handlers.GetImagesChannelMetrics(imagesMetricsManager, cfgManager))
	apiGroup.GET("/images/channels/metrics/history", handlers.GetImagesChannelMetricsHistory(imagesMetricsManager, cfgManager))
	apiGroup.GET("/images/channels/:id/keys/metrics/history", handlers.GetImagesChannelKeyMetricsHistory(imagesMetricsManager, cfgManager))
	apiGroup.GET("/images/global/stats/history", handlers.GetGlobalStatsHistory(imagesMetricsManager))
	apiGroup.GET("/images/ping/:id", images.PingChannel(cfgManager))
	apiGroup.GET("/images/ping", images.PingAllChannels(cfgManager))
	apiGroup.POST("/images/channels/:id/models", images.GetChannelModels(cfgManager))
	apiGroup.GET("/images/models/stats/history", handlers.GetModelStatsHistory(imagesMetricsManager))
	apiGroup.GET("/images/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindImages)))
}
