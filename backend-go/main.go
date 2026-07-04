package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
	"github.com/BenedictKing/ccx/internal/handlers"
	"github.com/BenedictKing/ccx/internal/handlers/chat"
	"github.com/BenedictKing/ccx/internal/handlers/copilot"
	"github.com/BenedictKing/ccx/internal/handlers/gemini"
	"github.com/BenedictKing/ccx/internal/handlers/images"
	"github.com/BenedictKing/ccx/internal/handlers/messages"
	"github.com/BenedictKing/ccx/internal/handlers/responses"
	"github.com/BenedictKing/ccx/internal/handlers/vectors"
	"github.com/BenedictKing/ccx/internal/logger"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/thinkingcache"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

//go:embed all:frontend/dist
var frontendFS embed.FS

type cliAction int

const (
	cliActionRun cliAction = iota
	cliActionHelp
	cliActionVersion
)

const (
	defaultConfigPath              = ".config/config.json"
	defaultStateDir                = ".config"
	metricsDBFile                  = "metrics.db"
	thinkingCacheDBFile            = "thinking_cache.db"
	conversationStateFile          = "conversation_state.json"
	scheduledRecoveryStateFileName = "scheduled_recovery_state.json"
)

type cliOptions struct {
	Action     cliAction
	ConfigPath string
	StateDir   string
	LogDir     string
	BackupDir  string
}

type runtimePaths struct {
	ConfigPath                 string
	StateDir                   string
	MetricsDBPath              string
	ThinkingCacheDBPath        string
	ConversationStatePath      string
	ScheduledRecoveryStatePath string
	LogDir                     string
	BackupDir                  string
}

func parseCLIArgs(args []string) (cliOptions, error) {
	opts := cliOptions{Action: cliActionRun}
	if len(args) == 1 && args[0] == "version" {
		opts.Action = cliActionVersion
		return opts, nil
	}

	fs := flag.NewFlagSet("ccx", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	showHelp := false
	showVersion := false
	fs.BoolVar(&showHelp, "help", false, "显示帮助")
	fs.BoolVar(&showVersion, "version", false, "显示版本")
	fs.BoolVar(&showVersion, "v", false, "显示版本")
	fs.StringVar(&opts.ConfigPath, "config", "", "指定配置文件路径")
	fs.StringVar(&opts.StateDir, "statedir", "", "指定运行时状态目录")
	fs.StringVar(&opts.LogDir, "logdir", "", "指定日志目录")
	fs.StringVar(&opts.BackupDir, "backupdir", "", "指定配置备份目录")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			opts.Action = cliActionHelp
			return opts, nil
		}
		return opts, err
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("未知参数: %s", strings.Join(fs.Args(), " "))
	}
	if showHelp {
		opts.Action = cliActionHelp
		return opts, nil
	}
	if showVersion {
		opts.Action = cliActionVersion
		return opts, nil
	}
	return opts, nil
}

func writeCLIHelp(out io.Writer) {
	fmt.Fprint(out, `用法:
  ccx [options]
  ccx version

选项:
  --help, -h          显示帮助并退出
  --version, -v       显示版本信息并退出
  --config PATH       指定运行时配置文件路径，默认 .config/config.json
  --statedir DIR      指定运行时状态目录，默认 .config
  --logdir DIR        指定日志目录，优先级高于 LOG_DIR，默认 logs
                      使用 none 或 null 禁用日志文件写入（仅输出到控制台）
	  --backupdir DIR     指定配置备份目录，默认 配置文件同级目录下的 backups

示例:
  ccx --config ~/.config/ccx/config.json --statedir ~/.local/state/ccx --logdir ~/.local/state/ccx/logs --backupdir ~/.local/state/ccx/backups
  ccx --logdir none   # 禁用日志文件，仅输出到控制台

说明:
  --config 只改变配置文件位置。
  --statedir 会让 metrics.db、thinking_cache.db、conversation_state.json、scheduled_recovery_state.json
  写入指定目录；不指定时保持默认 .config。
  --logdir 只影响日志目录。使用 none 或 null 可禁用日志文件写入，适合 systemd/journald 等环境。
	  --backupdir 只影响配置备份目录，不指定时默认为配置文件同级目录下的 backups。
`)
}

func printVersion(out io.Writer) {
	fmt.Fprintf(out, "ccx %s\n", Version)
	if BuildTime != "unknown" {
		fmt.Fprintf(out, "build time: %s\n", BuildTime)
	}
	if GitCommit != "unknown" {
		fmt.Fprintf(out, "git commit: %s\n", GitCommit)
	}
}

func expandUserPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("获取用户主目录失败: %w", err)
		}
		if path == "~" {
			return filepath.Clean(homeDir), nil
		}
		return filepath.Clean(filepath.Join(homeDir, path[2:])), nil
	}
	if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("不支持 ~otheruser 形式: %s", path)
	}
	return filepath.Clean(path), nil
}

func resolveRuntimePaths(opts cliOptions, envCfg *config.EnvConfig) (runtimePaths, error) {
	configPath := defaultConfigPath
	if opts.ConfigPath != "" {
		expandedConfigPath, err := expandUserPath(opts.ConfigPath)
		if err != nil {
			return runtimePaths{}, fmt.Errorf("解析配置文件路径失败: %w", err)
		}
		configPath = expandedConfigPath
	}

	stateDir := defaultStateDir
	if opts.StateDir != "" {
		expandedStateDir, err := expandUserPath(opts.StateDir)
		if err != nil {
			return runtimePaths{}, fmt.Errorf("解析运行时状态目录失败: %w", err)
		}
		stateDir = expandedStateDir
	}

	logDir := envCfg.LogDir
	if opts.LogDir != "" {
		logDir = opts.LogDir
	}

	// 禁用日志文件 sentinel 归一化（none/null 不区分大小写）
	if logger.IsLogDisabled(logDir) {
		logDir = "none"
	} else if opts.LogDir != "" {
		// 非禁用值才需要展开路径
		expandedLogDir, err := expandUserPath(opts.LogDir)
		if err != nil {
			return runtimePaths{}, fmt.Errorf("解析日志目录失败: %w", err)
		}
		logDir = expandedLogDir
	}

	// 备份目录：CLI > 默认（配置文件同级 backups）
	backupDir := filepath.Join(filepath.Dir(configPath), "backups")
	if opts.BackupDir != "" {
		expandedBackupDir, err := expandUserPath(opts.BackupDir)
		if err != nil {
			return runtimePaths{}, fmt.Errorf("解析配置备份目录失败: %w", err)
		}
		backupDir = expandedBackupDir
	}

	return runtimePaths{
		ConfigPath:                 configPath,
		StateDir:                   stateDir,
		MetricsDBPath:              filepath.Join(stateDir, metricsDBFile),
		ThinkingCacheDBPath:        filepath.Join(stateDir, thinkingCacheDBFile),
		ConversationStatePath:      filepath.Join(stateDir, conversationStateFile),
		ScheduledRecoveryStatePath: filepath.Join(stateDir, scheduledRecoveryStateFileName),
		LogDir:                     logDir,
		BackupDir:                  backupDir,
	}, nil
}

func main() {
	cliOpts, err := parseCLIArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "参数错误: %v\n\n", err)
		writeCLIHelp(os.Stderr)
		os.Exit(2)
	}
	switch cliOpts.Action {
	case cliActionHelp:
		writeCLIHelp(os.Stdout)
		os.Exit(0)
	case cliActionVersion:
		printVersion(os.Stdout)
		os.Exit(0)
	}

	// 加载环境变量
	if err := godotenv.Load(); err != nil {
		log.Println("没有找到 .env 文件，使用环境变量或默认值")
	}

	// 设置版本信息到 handlers 包
	handlers.SetVersionInfo(Version, BuildTime, GitCommit)

	// 初始化环境配置，并应用命令行运行时路径覆盖
	envCfg := config.NewEnvConfig()
	paths, err := resolveRuntimePaths(cliOpts, envCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析运行时路径失败: %v\n", err)
		os.Exit(2)
	}

	// 初始化日志系统（必须在其他初始化之前）
	logCfg := &logger.Config{
		LogDir:     paths.LogDir,
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

	cfgManager, err := config.NewConfigManager(paths.ConfigPath, paths.BackupDir)
	if err != nil {
		log.Fatalf("初始化配置管理器失败: %v", err)
	}
	defer cfgManager.Close()

	applyThinkingCacheConfig := func(cfg config.Config) {
		if err := thinkingcache.Configure(thinkingcache.Config{
			DBPath: paths.ThinkingCacheDBPath,
			TTL:    cfg.ThinkingCache.EffectiveTTL(),
		}); err != nil {
			log.Printf("[ThinkingCache-Init] 警告: 初始化 Claude thinking 缓存失败: %v，将使用内存缓存", err)
		}
	}
	applyThinkingCacheConfig(cfgManager.GetConfig())
	cfgManager.RegisterOnConfigChange(applyThinkingCacheConfig)
	defer thinkingcache.Close()

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
			DBPath:        paths.MetricsDBPath,
			RetentionDays: envCfg.MetricsRetentionDays,
		})
		if err != nil {
			log.Printf("[Metrics-Init] 警告: 初始化指标持久化存储失败: %v，将使用纯内存模式", err)
			metricsStore = nil
		}
	} else {
		log.Printf("[Metrics-Init] 指标持久化已禁用，使用纯内存模式")
	}

	// 初始化多渠道调度器（Messages、Responses、Gemini、Chat、Images 和 Vectors 使用独立的指标管理器）
	var messagesMetricsManager, responsesMetricsManager, geminiMetricsManager, chatMetricsManager, imagesMetricsManager, vectorsMetricsManager *metrics.MetricsManager
	if metricsStore != nil {
		if err := metricsStore.MigrateMetricsKeysToIdentity(cfgManager.GetConfig()); err != nil {
			log.Fatalf("[Metrics-Migration] metrics key 迁移失败: %v", err)
		}
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
		vectorsMetricsManager = metrics.NewMetricsManagerWithPersistence(
			envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold, metricsStore, "vectors")
	} else {
		messagesMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
		responsesMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
		geminiMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
		chatMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
		imagesMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
		vectorsMetricsManager = metrics.NewMetricsManagerWithConfig(envCfg.MetricsWindowSize, envCfg.MetricsFailureThreshold)
	}
	traceAffinityManager := session.NewTraceAffinityManager()

	applyCircuitBreakerConfig := func(cfg config.Config) {
		requestTimeoutMs := envCfg.RequestTimeout
		responseHeaderTimeoutMs := envCfg.ResponseHeaderTimeout * 1000
		params := metrics.CircuitBreakerParams{
			WindowSize:                   envCfg.MetricsWindowSize,
			FailureThreshold:             envCfg.MetricsFailureThreshold,
			ConsecutiveFailuresThreshold: 3,
			StreamFirstContentTimeoutMs:  30000,
			StreamInactivityTimeoutMs:    20000,
			StreamToolCallIdleTimeoutMs:  120000,
		}
		if cfg.CircuitBreaker != nil {
			if cfg.CircuitBreaker.WindowSize != nil {
				params.WindowSize = *cfg.CircuitBreaker.WindowSize
			}
			if cfg.CircuitBreaker.FailureThreshold != nil {
				params.FailureThreshold = *cfg.CircuitBreaker.FailureThreshold
			}
			if cfg.CircuitBreaker.ConsecutiveFailuresThreshold != nil {
				params.ConsecutiveFailuresThreshold = int64(*cfg.CircuitBreaker.ConsecutiveFailuresThreshold)
			}
			if cfg.CircuitBreaker.RequestTimeoutMs != nil {
				requestTimeoutMs = *cfg.CircuitBreaker.RequestTimeoutMs
			}
			if cfg.CircuitBreaker.ResponseHeaderTimeoutMs != nil {
				responseHeaderTimeoutMs = *cfg.CircuitBreaker.ResponseHeaderTimeoutMs
			}
			if cfg.CircuitBreaker.StreamFirstContentTimeoutMs != nil {
				params.StreamFirstContentTimeoutMs = *cfg.CircuitBreaker.StreamFirstContentTimeoutMs
			}
			if cfg.CircuitBreaker.StreamInactivityTimeoutMs != nil {
				params.StreamInactivityTimeoutMs = *cfg.CircuitBreaker.StreamInactivityTimeoutMs
			}
			if cfg.CircuitBreaker.StreamToolCallIdleTimeoutMs != nil {
				params.StreamToolCallIdleTimeoutMs = *cfg.CircuitBreaker.StreamToolCallIdleTimeoutMs
			}
		}
		config.SetRuntimeTimeouts(requestTimeoutMs, responseHeaderTimeoutMs)
		messagesMetricsManager.UpdateCircuitBreakerConfig(params)
		responsesMetricsManager.UpdateCircuitBreakerConfig(params)
		geminiMetricsManager.UpdateCircuitBreakerConfig(params)
		chatMetricsManager.UpdateCircuitBreakerConfig(params)
		imagesMetricsManager.UpdateCircuitBreakerConfig(params)
		vectorsMetricsManager.UpdateCircuitBreakerConfig(params)
	}
	applyCircuitBreakerConfig(cfgManager.GetConfig())
	cfgManager.RegisterOnConfigChange(applyCircuitBreakerConfig)

	// 初始化主动限速管理器（渠道级令牌桶 + 并发控制 + 429 动态 cooldown）
	rateLimitManager := ratelimit.NewManager()
	applyRateLimitConfig := func(cfg config.Config) {
		channelTypes := []struct {
			apiType   string
			upstreams []config.UpstreamConfig
		}{
			{"Messages", cfg.Upstream},
			{"Chat", cfg.ChatUpstream},
			{"Responses", cfg.ResponsesUpstream},
			{"Gemini", cfg.GeminiUpstream},
			{"Images", cfg.ImagesUpstream},
			{"Vectors", cfg.VectorsUpstream},
		}
		for _, ct := range channelTypes {
			for idx, upstream := range ct.upstreams {
				autoFromHeaders := upstream.RateLimitAutoFromHeaders != nil && *upstream.RateLimitAutoFromHeaders
				rateLimitManager.GetOrCreate(ct.apiType, idx, ratelimit.Config{
					RPM:             upstream.RateLimitRPM,
					WindowSeconds:   config.RateLimitWindowSeconds(upstream.RateLimitWindowMinutes),
					MaxConcurrent:   upstream.RateLimitMaxConcurrent,
					AutoFromHeaders: autoFromHeaders,
				})
			}
		}
	}
	applyRateLimitConfig(cfgManager.GetConfig())
	cfgManager.RegisterOnConfigChange(applyRateLimitConfig)
	log.Printf("[RateLimit-Init] 主动限速管理器已初始化")

	// 初始化 URL 管理器（非阻塞，动态排序）
	urlManager := warmup.NewURLManager(30*time.Second, 3) // 30秒冷却期，连续3次失败后移到末尾
	log.Printf("[URLManager-Init] URL管理器已初始化 (冷却期: 30秒, 最大连续失败: 3)")

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, messagesMetricsManager, responsesMetricsManager, geminiMetricsManager, chatMetricsManager, imagesMetricsManager, traceAffinityManager, urlManager, vectorsMetricsManager)
	channelScheduler.SetRateLimitManager(rateLimitManager)
	log.Printf("[Scheduler-Init] 多渠道调度器已初始化 (失败率阈值: %.0f%%, 滑动窗口: %d, 连续失败阈值: %d)",
		messagesMetricsManager.GetFailureThreshold()*100, messagesMetricsManager.GetWindowSize(), messagesMetricsManager.GetConsecutiveRetryableFailuresThreshold())

	// 初始化对话追踪器和覆盖管理器
	conversationTracker := conversation.NewConversationTracker(1*time.Hour, 24*time.Hour, paths.ConversationStatePath)

	// 获取 override TTL：优先使用配置文件中的值，否则使用环境变量
	cfg := cfgManager.GetConfig()
	overrideTTLMinutes := cfg.OverrideTTLMinutes
	if overrideTTLMinutes == 0 {
		overrideTTLMinutes = envCfg.OverrideTTLMinutes
	}

	var overrideTTL time.Duration
	if overrideTTLMinutes == -1 {
		overrideTTL = -1 // 永不过期
		log.Printf("[Conversation-Init] 对话追踪器和覆盖管理器已初始化 (idle: 1h, expire: 2h, override TTL: 永不恢复)")
	} else {
		overrideTTL = time.Duration(overrideTTLMinutes) * time.Minute
		log.Printf("[Conversation-Init] 对话追踪器和覆盖管理器已初始化 (idle: 1h, expire: 2h, override TTL: %dm)", overrideTTLMinutes)
	}

	overrideManager := conversation.NewOverrideManager(overrideTTL)
	channelScheduler.SetConversationComponents(conversationTracker, overrideManager)

	// 启动 loadShed 后台 reaper（30s 推进到期状态）
	channelScheduler.Start()

	scheduledRecoveryStop := make(chan struct{})
	go func() {
		runScheduledRecovery := func(now time.Time, missedSlot time.Time) bool {
			effectiveTime := now.UTC()
			if !missedSlot.IsZero() {
				effectiveTime = missedSlot.UTC()
				log.Printf("[Scheduler-Recovery] 检测到错过 UTC 恢复槽位 %s，立即补跑", missedSlot.Format(time.RFC3339))
			}
			results, err := channelScheduler.RunScheduledRecoveries(effectiveTime)
			if err != nil {
				log.Printf("[Scheduler-Recovery] 警告: 自动恢复执行失败: %v", err)
				return false
			}
			if len(results) == 0 {
				log.Printf("[Scheduler-Recovery] UTC 自动恢复完成，本轮无可恢复 key")
				return true
			}
			restoredKeys := 0
			activatedChannels := 0
			for _, result := range results {
				restoredKeys += len(result.RestoredKeys)
				if result.ActivatedChannel {
					activatedChannels++
				}
			}
			log.Printf("[Scheduler-Recovery] UTC 自动恢复完成：恢复 %d 个 key，激活 %d 个渠道", restoredKeys, activatedChannels)
			return true
		}

		recordRecoveryCheck := func(checkedAt time.Time) {
			if err := saveScheduledRecoveryLastCheck(paths.ScheduledRecoveryStatePath, checkedAt); err != nil {
				log.Printf("[Scheduler-Recovery] 警告: 持久化恢复检查时间失败: %v", err)
			}
		}

		lastRecoveryCheck, err := loadScheduledRecoveryLastCheck(paths.ScheduledRecoveryStatePath)
		if err != nil {
			log.Printf("[Scheduler-Recovery] 警告: 读取恢复检查时间失败: %v", err)
			lastRecoveryCheck = time.Time{}
		}
		commitRecoveryCheck := func(checkedAt time.Time, attempted bool, succeeded bool) {
			if attempted && !succeeded {
				log.Printf("[Scheduler-Recovery] 警告: 本轮恢复失败，保留检查点 %s 以便后续重试", lastRecoveryCheck.Format(time.RFC3339))
				return
			}
			lastRecoveryCheck = checkedAt
			recordRecoveryCheck(lastRecoveryCheck)
		}

		startupNow := time.Now().UTC()
		if !lastRecoveryCheck.IsZero() {
			if missedSlot, ok := scheduler.MissedScheduledRecoveryTimeUTC(lastRecoveryCheck, startupNow); ok {
				commitRecoveryCheck(startupNow, true, runScheduledRecovery(startupNow, missedSlot))
			} else {
				commitRecoveryCheck(startupNow, false, true)
			}
		} else {
			commitRecoveryCheck(startupNow, false, true)
		}

		recoveryFallbackTicker := time.NewTicker(1 * time.Minute)
		defer recoveryFallbackTicker.Stop()

		for {
			next := scheduler.NextScheduledRecoveryTimeUTC(time.Now())
			wait := time.Until(next)
			if wait < 0 {
				wait = 0
			}
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				now := time.Now().UTC()
				scheduledAt := next.UTC()
				if now.After(scheduledAt.Add(time.Second)) {
					if missedSlot, ok := scheduler.MissedScheduledRecoveryTimeUTC(lastRecoveryCheck, now); ok {
						commitRecoveryCheck(now, true, runScheduledRecovery(now, missedSlot))
					} else {
						commitRecoveryCheck(now, true, runScheduledRecovery(scheduledAt, time.Time{}))
					}
				} else {
					commitRecoveryCheck(now, true, runScheduledRecovery(scheduledAt, time.Time{}))
				}
			case tickAt := <-recoveryFallbackTicker.C:
				now := tickAt.UTC()
				if missedSlot, ok := scheduler.MissedScheduledRecoveryTimeUTC(lastRecoveryCheck, now); ok {
					commitRecoveryCheck(now, true, runScheduledRecovery(now, missedSlot))
				} else {
					commitRecoveryCheck(now, false, true)
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-scheduledRecoveryStop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
		}
	}()

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
		apiGroup.POST("/copilot/oauth/device/code", copilot.RequestDeviceCode())
		apiGroup.POST("/copilot/oauth/token", copilot.PollAccessToken())
		apiGroup.POST("/copilot/oauth/verify", copilot.VerifyToken())

		apiGroup.POST("/responses/channels/:id/copilot/diagnose", responses.DiagnoseCopilotChannel(cfgManager))

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
		apiGroup.PUT("/messages/channels/:id/mappings", messages.UpdateModelMapping(cfgManager))

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
		apiGroup.GET("/messages/channels/dashboard", handlers.GetChannelDashboard(cfgManager, channelScheduler)) // 统一 dashboard 端点，支持 ?type=messages|responses|chat|gemini|images|vectors
		apiGroup.GET("/messages/ping/:id", messages.PingChannel(cfgManager))
		apiGroup.GET("/messages/ping", messages.PingAllChannels(cfgManager))
		apiGroup.POST("/messages/channels/:id/models", messages.GetChannelModels(cfgManager))
		apiGroup.GET("/messages/models/stats/history", handlers.GetModelStatsHistory(messagesMetricsManager))
		apiGroup.GET("/messages/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages), cfgManager, scheduler.ChannelKindMessages))
		apiGroup.GET("/messages/channels/:id/capability-snapshot", handlers.GetCapabilitySnapshot(cfgManager, "messages"))
		apiGroup.POST("/messages/channels/:id/capability-test", handlers.TestChannelCapability(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages), "messages"))
		apiGroup.GET("/messages/channels/:id/capability-test/:jobId", handlers.GetCapabilityTestJobStatus(cfgManager, "messages"))
		apiGroup.DELETE("/messages/channels/:id/capability-test/:jobId", handlers.CancelCapabilityTestJob(cfgManager, "messages"))
		apiGroup.POST("/messages/channels/:id/capability-test/:jobId/retry", handlers.RetryCapabilityTestModel(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages), "messages"))
		apiGroup.POST("/messages/channels/:id/compat-diagnose", handlers.DiagnoseChannelCompat(cfgManager, "messages"))

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
		apiGroup.PUT("/responses/channels/:id/mappings", responses.UpdateModelMapping(cfgManager))

		// Responses 多渠道调度 API
		apiGroup.POST("/responses/channels/reorder", responses.ReorderChannels(cfgManager))
		apiGroup.PATCH("/responses/channels/:id/status", responses.SetChannelStatus(cfgManager))
		apiGroup.POST("/responses/channels/:id/resume", handlers.ResumeChannel(channelScheduler, cfgManager, true))
		apiGroup.POST("/responses/channels/:id/promotion", responses.SetChannelPromotion(cfgManager))
		apiGroup.GET("/responses/channels/metrics", handlers.GetChannelMetricsWithConfig(responsesMetricsManager, cfgManager, true))
		apiGroup.GET("/responses/channels/metrics/history", handlers.GetChannelMetricsHistory(responsesMetricsManager, cfgManager, true))
		apiGroup.GET("/responses/channels/:id/keys/metrics/history", handlers.GetChannelKeyMetricsHistory(responsesMetricsManager, cfgManager, true))
		apiGroup.GET("/responses/global/stats/history", handlers.GetGlobalStatsHistory(responsesMetricsManager))
		apiGroup.GET("/responses/ping/:id", responses.PingChannel(cfgManager))
		apiGroup.GET("/responses/ping", responses.PingAllChannels(cfgManager))
		apiGroup.POST("/responses/channels/:id/models", responses.GetChannelModels(cfgManager))
		apiGroup.GET("/responses/models/stats/history", handlers.GetModelStatsHistory(responsesMetricsManager))
		apiGroup.GET("/responses/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses), cfgManager, scheduler.ChannelKindResponses))
		apiGroup.GET("/responses/channels/:id/capability-snapshot", handlers.GetCapabilitySnapshot(cfgManager, "responses"))
		apiGroup.POST("/responses/channels/:id/capability-test", handlers.TestChannelCapability(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses), "responses"))
		apiGroup.GET("/responses/channels/:id/capability-test/:jobId", handlers.GetCapabilityTestJobStatus(cfgManager, "responses"))
		apiGroup.DELETE("/responses/channels/:id/capability-test/:jobId", handlers.CancelCapabilityTestJob(cfgManager, "responses"))
		apiGroup.POST("/responses/channels/:id/capability-test/:jobId/retry", handlers.RetryCapabilityTestModel(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses), "responses"))
		apiGroup.POST("/responses/channels/:id/compat-diagnose", handlers.DiagnoseChannelCompat(cfgManager, "responses"))

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
		apiGroup.PUT("/gemini/channels/:id/mappings", gemini.UpdateModelMapping(cfgManager))

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
		apiGroup.GET("/gemini/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindGemini), cfgManager, scheduler.ChannelKindGemini))
		apiGroup.GET("/gemini/channels/:id/capability-snapshot", handlers.GetCapabilitySnapshot(cfgManager, "gemini"))
		apiGroup.POST("/gemini/channels/:id/capability-test", handlers.TestChannelCapability(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindGemini), "gemini"))
		apiGroup.GET("/gemini/channels/:id/capability-test/:jobId", handlers.GetCapabilityTestJobStatus(cfgManager, "gemini"))
		apiGroup.DELETE("/gemini/channels/:id/capability-test/:jobId", handlers.CancelCapabilityTestJob(cfgManager, "gemini"))
		apiGroup.POST("/gemini/channels/:id/capability-test/:jobId/retry", handlers.RetryCapabilityTestModel(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindGemini), "gemini"))
		apiGroup.POST("/gemini/channels/:id/compat-diagnose", handlers.DiagnoseChannelCompat(cfgManager, "gemini"))

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
		apiGroup.PUT("/chat/channels/:id/mappings", chat.UpdateModelMapping(cfgManager))

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
		apiGroup.GET("/chat/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat), cfgManager, scheduler.ChannelKindChat))
		apiGroup.GET("/chat/channels/:id/capability-snapshot", handlers.GetCapabilitySnapshot(cfgManager, "chat"))
		apiGroup.POST("/chat/channels/:id/capability-test", handlers.TestChannelCapability(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat), "chat"))
		apiGroup.GET("/chat/channels/:id/capability-test/:jobId", handlers.GetCapabilityTestJobStatus(cfgManager, "chat"))
		apiGroup.DELETE("/chat/channels/:id/capability-test/:jobId", handlers.CancelCapabilityTestJob(cfgManager, "chat"))
		apiGroup.POST("/chat/channels/:id/capability-test/:jobId/retry", handlers.RetryCapabilityTestModel(cfgManager, channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat), "chat"))
		apiGroup.POST("/chat/channels/:id/compat-diagnose", handlers.DiagnoseChannelCompat(cfgManager, "chat"))
		apiGroup.GET("/chat/channels/scheduler/stats", handlers.GetSchedulerStats(channelScheduler))

		// Images 渠道管理
		apiGroup.GET("/images/channels", images.GetUpstreams(cfgManager))
		apiGroup.POST("/images/channels", images.AddUpstream(cfgManager))
		apiGroup.PUT("/images/channels/:id", images.UpdateUpstream(cfgManager, channelScheduler))
		apiGroup.DELETE("/images/channels/:id", images.DeleteUpstream(cfgManager, channelScheduler))
		apiGroup.POST("/images/channels/:id/keys", images.AddApiKey(cfgManager))
		apiGroup.DELETE("/images/channels/:id/keys/:apiKey", images.DeleteApiKey(cfgManager))
		apiGroup.POST("/images/channels/:id/keys/:apiKey/top", images.MoveApiKeyToTop(cfgManager))
		apiGroup.POST("/images/channels/:id/keys/:apiKey/bottom", images.MoveApiKeyToBottom(cfgManager))
		apiGroup.POST("/images/channels/:id/keys/restore", handlers.RestoreBlacklistedKey(cfgManager, "Images"))
		apiGroup.PUT("/images/channels/:id/mappings", images.UpdateModelMapping(cfgManager))

		// Images 多渠道调度 API
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
		apiGroup.GET("/images/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindImages), cfgManager, scheduler.ChannelKindImages))

		// Vectors 渠道管理
		apiGroup.GET("/vectors/channels", vectors.GetUpstreams(cfgManager))
		apiGroup.POST("/vectors/channels", vectors.AddUpstream(cfgManager))
		apiGroup.PUT("/vectors/channels/:id", vectors.UpdateUpstream(cfgManager, channelScheduler))
		apiGroup.DELETE("/vectors/channels/:id", vectors.DeleteUpstream(cfgManager, channelScheduler))
		apiGroup.POST("/vectors/channels/:id/keys", vectors.AddApiKey(cfgManager))
		apiGroup.DELETE("/vectors/channels/:id/keys/:apiKey", vectors.DeleteApiKey(cfgManager))
		apiGroup.POST("/vectors/channels/:id/keys/:apiKey/top", vectors.MoveApiKeyToTop(cfgManager))
		apiGroup.POST("/vectors/channels/:id/keys/:apiKey/bottom", vectors.MoveApiKeyToBottom(cfgManager))
		apiGroup.POST("/vectors/channels/:id/keys/restore", handlers.RestoreBlacklistedKey(cfgManager, "Vectors"))
		apiGroup.PUT("/vectors/channels/:id/mappings", vectors.UpdateModelMapping(cfgManager))

		// Vectors 多渠道调度 API
		apiGroup.POST("/vectors/channels/reorder", vectors.ReorderChannels(cfgManager))
		apiGroup.PATCH("/vectors/channels/:id/status", vectors.SetChannelStatus(cfgManager))
		apiGroup.POST("/vectors/channels/:id/resume", handlers.ResumeChannelWithKind(channelScheduler, cfgManager, scheduler.ChannelKindVectors))
		apiGroup.POST("/vectors/channels/:id/promotion", vectors.SetChannelPromotion(cfgManager))
		apiGroup.GET("/vectors/channels/metrics", handlers.GetVectorsChannelMetrics(vectorsMetricsManager, cfgManager))
		apiGroup.GET("/vectors/channels/metrics/history", handlers.GetVectorsChannelMetricsHistory(vectorsMetricsManager, cfgManager))
		apiGroup.GET("/vectors/channels/:id/keys/metrics/history", handlers.GetVectorsChannelKeyMetricsHistory(vectorsMetricsManager, cfgManager))
		apiGroup.GET("/vectors/global/stats/history", handlers.GetGlobalStatsHistory(vectorsMetricsManager))
		apiGroup.GET("/vectors/ping/:id", vectors.PingChannel(cfgManager))
		apiGroup.GET("/vectors/ping", vectors.PingAllChannels(cfgManager))
		apiGroup.POST("/vectors/channels/:id/models", vectors.GetChannelModels(cfgManager))
		apiGroup.GET("/vectors/models/stats/history", handlers.GetModelStatsHistory(vectorsMetricsManager))
		apiGroup.GET("/vectors/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindVectors), cfgManager, scheduler.ChannelKindVectors))

		// Fuzzy 模式设置
		apiGroup.GET("/settings/fuzzy-mode", handlers.GetFuzzyMode(cfgManager))
		apiGroup.PUT("/settings/fuzzy-mode", handlers.SetFuzzyMode(cfgManager))

		// 熔断器运行时设置
		apiGroup.GET("/settings/circuit-breaker", handlers.GetCircuitBreaker(messagesMetricsManager.GetCircuitBreakerConfig, envCfg))
		apiGroup.PUT("/settings/circuit-breaker", handlers.SetCircuitBreaker(cfgManager))

		// 会话调度看板 API
		convDeps := &handlers.ConversationHandlerDeps{
			Tracker:          conversationTracker,
			OverrideManager:  overrideManager,
			ChannelScheduler: channelScheduler,
			ConfigManager:    cfgManager,
		}
		apiGroup.GET("/conversations", handlers.GetConversations(convDeps))
		apiGroup.POST("/conversations/:id/override", handlers.SetConversationOverride(convDeps))
		apiGroup.DELETE("/conversations/:id/override", handlers.RemoveConversationOverride(convDeps))
		apiGroup.GET("/conversations/settings", handlers.GetConversationSettings(convDeps))
		apiGroup.PUT("/conversations/settings", handlers.UpdateConversationSettings(convDeps))
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

	// Responses WebSocket: 支持 Codex 原生 response.create over WebSocket。
	responsesWebSocketHandler := responses.WebSocketHandler(envCfg, cfgManager, sessionManager, channelScheduler)
	r.GET("/v1/responses", responsesWebSocketHandler)
	r.GET("/:routePrefix/v1/responses", responsesWebSocketHandler)

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

	// 代理端点 - Images API (OpenAI Images 兼容)
	imagesHandler := images.Handler(envCfg, cfgManager, channelScheduler)
	r.POST("/v1/images/generations", imagesHandler)
	r.POST("/:routePrefix/v1/images/generations", imagesHandler)
	r.POST("/v1/images/edits", imagesHandler)
	r.POST("/:routePrefix/v1/images/edits", imagesHandler)
	r.POST("/v1/images/variations", imagesHandler)
	r.POST("/:routePrefix/v1/images/variations", imagesHandler)

	// 代理端点 - Embeddings API (OpenAI Embeddings 兼容)
	vectorsHandler := vectors.Handler(envCfg, cfgManager, channelScheduler)
	r.POST("/v1/embeddings", vectorsHandler)
	r.POST("/:routePrefix/v1/embeddings", vectorsHandler)

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
	addr := listenAddressForEnv(envCfg)
	endpoint := endpointForEnv(envCfg)

	// 创建 HTTP 服务器
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Duration(envCfg.ServerReadTimeout) * time.Millisecond, // 仅控制服务端读取入站请求，避免与上游请求超时耦合
		IdleTimeout:       120 * time.Second,
	}
	if err := configureServerTLS(srv, envCfg); err != nil {
		log.Fatalf("[Server-Fatal] HTTPS 配置无效: %v", err)
	}

	fmt.Printf("\n[Server-Startup] CCX API代理服务器已启动\n")
	fmt.Printf("[Server-Info] 版本: %s\n", Version)
	if BuildTime != "unknown" {
		fmt.Printf("[Server-Info] 构建时间: %s\n", BuildTime)
	}
	if GitCommit != "unknown" {
		fmt.Printf("[Server-Info] Git提交: %s\n", GitCommit)
	}
	fmt.Printf("\n")
	fmt.Printf("[Server-Info] 协议: %s\n", strings.ToUpper(endpoint.Scheme))
	fmt.Printf("[Server-Info] 监听地址: %s\n", addr)
	fmt.Printf("[Server-Info] 管理界面: %s\n", endpoint.URL(""))
	fmt.Printf("[Server-Info] API 地址: %s\n", endpoint.URL("/v1"))
	if envCfg.EnableHTTPS {
		if envCfg.TLSCertFile == "" {
			fmt.Printf("[Server-Info] HTTPS 证书: 自动生成 localhost 自签名证书（仅建议本地使用）\n")
		} else {
			fmt.Printf("[Server-Info] HTTPS 证书: %s\n", envCfg.TLSCertFile)
		}
		fmt.Printf("[Server-Info] HTTP 兼容: 已启用（同端口同时接受 HTTP/HTTPS）\n")
	}
	fmt.Printf("\n")
	fmt.Printf("[Server-Info] Claude Messages: POST /v1/messages\n")
	fmt.Printf("[Server-Info] Codex Responses: POST /v1/responses\n")
	fmt.Printf("[Server-Info] Gemini API: POST /v1beta/models/{model}:generateContent\n")
	fmt.Printf("[Server-Info] Gemini API: POST /v1beta/models/{model}:streamGenerateContent\n")
	fmt.Printf("[Server-Info] Chat Completions: POST /v1/chat/completions\n")
	fmt.Printf("[Server-Info] Images Generations: POST /v1/images/generations\n")
	fmt.Printf("[Server-Info] Images Edits: POST /v1/images/edits\n")
	fmt.Printf("[Server-Info] Images Variations: POST /v1/images/variations\n")
	fmt.Printf("[Server-Info] Embeddings: POST /v1/embeddings\n")
	fmt.Printf("[Server-Info] 健康检查: GET /health\n")
	fmt.Printf("\n")
	fmt.Printf("[Server-Info] 环境: %s\n", envCfg.Env)
	fmt.Printf("[Server-Info] 配置文件: %s\n", paths.ConfigPath)
	if paths.LogDir == "none" {
		fmt.Printf("[Server-Info] 日志文件输出: 已禁用（仅控制台）\n")
	} else {
		fmt.Printf("[Server-Info] 日志目录: %s\n", paths.LogDir)
	}
	// 生产环境检查：必须设置有效的访问密钥
	if envCfg.IsProduction() && envCfg.ProxyAccessKey == "your-proxy-access-key" {
		log.Fatal("[Server-Fatal] 生产环境必须设置 PROXY_ACCESS_KEY，禁止使用默认值")
	}
	if err := envCfg.ValidateAccessKeys(); err != nil {
		log.Fatalf("[Server-Fatal] 访问密钥配置无效: %v", err)
	}
	// 打印访问控制密钥的脱密内容和设置情况，避免用户混淆
	fmt.Printf("[Server-Info] 代理访问密钥 (PROXY_ACCESS_KEY): %s\n", maskKey(envCfg.ProxyAccessKey))
	if envCfg.HasExtraProxyAccessKeys() {
		fmt.Printf("[Server-Info] 额外代理访问密钥 (EXTRA_PROXY_ACCESS_KEYS): %d 个已启用\n", len(envCfg.ExtraProxyAccessKeys))
	}
	if envCfg.AdminAccessKey != "" {
		fmt.Printf("[Server-Info] 管理 API 密钥 (ADMIN_ACCESS_KEY): %s (已启用独立管理密钥)\n", maskKey(envCfg.AdminAccessKey))
	} else {
		fmt.Printf("[Server-Info] 管理 API 密钥 (ADMIN_ACCESS_KEY): 未设置 (回退到 PROXY_ACCESS_KEY)\n")
	}
	fmt.Printf("\n")

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

		// 关闭对话追踪器（flush 持久化状态）
		conversationTracker.Stop()
		log.Println("[Conversation-Shutdown] 对话追踪器已安全关闭")

		// 停止调度器后台 reaper
		channelScheduler.Stop()

		// 停止限速器后台清理协程
		rateLimitManager.Stop()

		close(scheduledRecoveryStop)
		close(shutdownDone)
	}()

	// 启动服务器（阻塞直到关闭）
	if err := startHTTPServer(srv, envCfg); err != nil && err != http.ErrServerClosed {
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

// maskKey 对密钥进行脱密处理，保留首尾部分字符，中间用 * 遮蔽
func maskKey(key string) string {
	if key == "" {
		return "未设置"
	}
	if key == "your-proxy-access-key" {
		return "your-proxy-access-key (默认值，不安全)"
	}
	n := len(key)
	var masked string
	if n <= 3 {
		masked = key[:1] + "****"
	} else if n <= 4 {
		masked = key[:1] + "****" + key[n-1:]
	} else if n <= 8 {
		masked = key[:1] + "****" + key[n-1:]
	} else {
		masked = key[:2] + "****" + key[n-2:]
	}
	return masked + " (已脱敏，不可直接复制)"
}
