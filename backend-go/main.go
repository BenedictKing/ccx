package main

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/guardrails"
	"github.com/BenedictKing/ccx/internal/conversation"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/eventbus"
	"github.com/BenedictKing/ccx/internal/handlers"
	channelsv2 "github.com/BenedictKing/ccx/internal/handlers/channels"
	"github.com/BenedictKing/ccx/internal/handlers/chat"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/handlers/copilot"
	"github.com/BenedictKing/ccx/internal/handlers/gemini"
	"github.com/BenedictKing/ccx/internal/handlers/images"
	"github.com/BenedictKing/ccx/internal/handlers/logicalchannels"
	"github.com/BenedictKing/ccx/internal/handlers/messages"
	"github.com/BenedictKing/ccx/internal/handlers/responses"
	"github.com/BenedictKing/ccx/internal/handlers/vectors"
	"github.com/BenedictKing/ccx/internal/healthcheck"
	"github.com/BenedictKing/ccx/internal/logger"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/presetstore"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/thinkingcache"
	"github.com/BenedictKing/ccx/internal/upstreamprobe"
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
	autopilotDBFile                = "autopilot.db"
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
	AutopilotDBPath            string
	PresetCacheDir             string
	LogDir                     string
	BackupDir                  string
}

func buildChannelDiscoveryModelFetchers(cfgManager *config.ConfigManager) handlers.ChannelDiscoveryModelFetchers {
	return handlers.ChannelDiscoveryModelFetchers{
		"messages":  channelModelsHandlerFetcher(messages.GetChannelModels(cfgManager)),
		"responses": channelModelsHandlerFetcher(responses.GetChannelModels(cfgManager)),
		"chat":      channelModelsHandlerFetcher(chat.GetChannelModels(cfgManager)),
		"gemini":    channelModelsHandlerFetcher(gemini.GetChannelModels(cfgManager)),
	}
}

func channelModelsHandlerFetcher(handler gin.HandlerFunc) handlers.ChannelDiscoveryModelFetcher {
	return func(ctx context.Context, req handlers.DiscoveryModelsFetchRequest) (handlers.DiscoveryModelsFetchResponse, error) {
		body, err := json.Marshal(map[string]any{
			"key":                      req.APIKey,
			"baseUrl":                  req.BaseURL,
			"baseUrls":                 req.BaseURLs,
			"serviceType":              req.ServiceType,
			"proxyUrl":                 req.ProxyURL,
			"proxyPreferDirect":        req.ProxyPreferDirect,
			"insecureSkipVerify":       req.InsecureSkipVerify,
			"customHeaders":            req.CustomHeaders,
			"authHeader":               req.AuthHeader,
			"learnedClientFingerprint": req.LearnedClientFingerprint,
		})
		if err != nil {
			return handlers.DiscoveryModelsFetchResponse{}, err
		}

		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		httpReq := httptest.NewRequest(http.MethodPost, "/internal/channel-discovery/models", bytes.NewReader(body)).WithContext(ctx)
		httpReq.Header.Set("Content-Type", "application/json")
		ginCtx.Request = httpReq
		ginCtx.Params = gin.Params{{Key: "id", Value: "0"}}

		handler(ginCtx)

		return handlers.DiscoveryModelsFetchResponse{
			StatusCode: recorder.Code,
			Body:       append([]byte(nil), recorder.Body.Bytes()...),
		}, nil
	}
}

// healthCheckL1Fetcher 将渠道 GetChannelModels handler 适配为保活验证的 L1Fetcher
// （与 channelModelsHandlerFetcher 同一包装路径，响应归一化在 healthcheck 包内完成）。
// 火山 Agent/Coding Plan 官方入口不走通用 /v1/models（套餐 Key 无法通过该接口探测），
// 改用共享数据面探针 + 内置 manifest 模型清单，并标记 RealCallVerified 跳过同周期等价 L2。
func healthCheckL1Fetcher(handler gin.HandlerFunc) healthcheck.L1Fetcher {
	fetcher := channelModelsHandlerFetcher(handler)
	return func(ctx context.Context, req healthcheck.L1Request) (healthcheck.L1Response, error) {
		if upstreamprobe.IsVolcenginePlanBaseURL(req.BaseURL) {
			// 使用内置 manifest 模型清单作为候选，让 L1 探针动态选择最便宜可用模型
			candidates := volcenginePlanCandidates(req.BaseURL, req.ServiceType)
			sc, body, model, err := upstreamprobe.VolcenginePlanL1Probe(ctx, req.ServiceType, req.BaseURL, req.APIKey, req.AuthHeader, candidates, upstreamprobe.ProbeOptions{
				ProxyURL:           req.ProxyURL,
				ProxyPreferDirect:  req.ProxyPreferDirect,
				CustomHeaders:      req.CustomHeaders,
				InsecureSkipVerify: req.InsecureSkipVerify,
			})
			if err != nil {
				return healthcheck.L1Response{RealCallVerified: true, Model: model}, err
			}
			return healthcheck.L1Response{StatusCode: sc, Body: body, RealCallVerified: true, Model: model}, nil
		}
		resp, err := fetcher(ctx, handlers.DiscoveryModelsFetchRequest{
			ServiceType:              req.ServiceType,
			BaseURL:                  req.BaseURL,
			APIKey:                   req.APIKey,
			AuthHeader:               req.AuthHeader,
			CustomHeaders:            req.CustomHeaders,
			ProxyURL:                 req.ProxyURL,
			ProxyPreferDirect:        req.ProxyPreferDirect,
			InsecureSkipVerify:       req.InsecureSkipVerify,
			LearnedClientFingerprint: req.LearnedClientFingerprint,
		})
		if err != nil {
			return healthcheck.L1Response{}, err
		}
		return healthcheck.L1Response{StatusCode: resp.StatusCode, Body: resp.Body}, nil
	}
}

// volcenginePlanCandidates 返回火山套餐入口对应的内置候选模型清单。
// 用于 healthcheck L1 动态选择探针模型；无 manifest 时返回 nil，由上游探针回退常量。
func volcenginePlanCandidates(baseURL, serviceType string) []string {
	manifest, ok := config.LookupBuiltinManifest(baseURL, volcengineManifestServiceType(serviceType))
	if !ok {
		return nil
	}
	return manifest.ModelIDs
}

// volcengineManifestServiceType 把 healthcheck serviceType 归一化为 manifest 查找口径。
func volcengineManifestServiceType(serviceType string) string {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "messages":
		return "messages"
	case "openai":
		return "openai"
	default:
		return strings.ToLower(strings.TrimSpace(serviceType))
	}
}

// healthCheckAPIType 保活验证渠道类型到 ConfigManager.BlacklistKeyWithRecoverAt apiType 的映射
func healthCheckAPIType(channelType string) string {
	switch channelType {
	case "messages":
		return "Messages"
	case "responses":
		return "Responses"
	case "gemini":
		return "Gemini"
	case "chat":
		return "Chat"
	case "images":
		return "Images"
	case "vectors":
		return "Vectors"
	}
	return channelType
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
	_, _ = fmt.Fprint(out, `用法:
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
	_, _ = fmt.Fprintf(out, "ccx %s\n", Version)
	if BuildTime != "unknown" {
		_, _ = fmt.Fprintf(out, "build time: %s\n", BuildTime)
	}
	if GitCommit != "unknown" {
		_, _ = fmt.Fprintf(out, "git commit: %s\n", GitCommit)
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
		AutopilotDBPath:            filepath.Join(stateDir, autopilotDBFile),
		PresetCacheDir:             filepath.Join(stateDir, "presets"),
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

	// 注册渠道兼容性探测：让无上游报错信号的兼容项（reasoning_content 回传、空 text block 剥离）
	// 在首次遇到渠道-Key-模型组合时自动探测一次并记忆，无需用户手工点诊断按钮
	handlers.RegisterCompatProbeHook()

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
	defer errutil.IgnoreDeferred(cfgManager.Close)

	// 初始化 Guardrails 注册表：credential-masker（最小集）
	// fail-open：guardrail 异常仅记日志放行，绝不阻断流量
	credentialMasker := guardrails.NewCredentialMasker()
	// 注入网关自身密钥
	gatewayKeys := []string{envCfg.ProxyAccessKey, envCfg.GetAdminAccessKey()}
	gatewayKeys = append(gatewayKeys, envCfg.ExtraProxyAccessKeys...)
	credentialMasker.SetStaticKeys(gatewayKeys)
	// 注入渠道 key 前缀指纹
	initCfg := cfgManager.GetConfig()
	var channelKeyPrefixes []string
	for _, upstream := range initCfg.Upstream {
		for _, key := range upstream.APIKeys {
			if len(key) >= 8 {
				channelKeyPrefixes = append(channelKeyPrefixes, key[:8])
			}
		}
	}
	credentialMasker.SetChannelKeyPrefixes(channelKeyPrefixes)
	guardrails.DefaultRegistry().Register(credentialMasker)
	log.Printf("[Guardrails-Init] 已注册 credential-masker，渠道 key 前缀 %d 个", len(channelKeyPrefixes))

	// 配置热重载时同步更新 credential-masker 的渠道 key 前缀
	cfgManager.RegisterOnConfigChange(func(cfg config.Config) {
		var prefixes []string
		seen := make(map[string]struct{})
		for _, upstream := range cfg.Upstream {
			for _, key := range upstream.APIKeys {
				if len(key) >= 8 {
					p := key[:8]
					if _, ok := seen[p]; !ok {
						seen[p] = struct{}{}
						prefixes = append(prefixes, p)
					}
				}
			}
		}
		credentialMasker.SetChannelKeyPrefixes(prefixes)
	})

	// 远程预置更新：启动时先尝试恢复已校验磁盘缓存，再由后台 worker 异步检查文档站。
	// 网络/缓存失败不阻断服务，始终保留编译期 embedded fallback。
	presetUpdater := presetstore.NewPresetUpdater(presetstore.Default(), presetstore.UpdaterConfig{
		Enabled:  envCfg.PresetUpdateEnabled,
		IndexURL: envCfg.PresetUpdateURL,
		Interval: time.Duration(envCfg.PresetUpdateIntervalMinutes) * time.Minute,
		CacheDir: paths.PresetCacheDir,
	})
	if err := presetUpdater.LoadCacheAtStartup(); err != nil && !os.IsNotExist(err) {
		log.Printf("[PresetUpdater-Cache] 缓存不可用，继续使用内置预置: %v", err)
	}
	presetUpdater.Start(context.Background())

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
	defer errutil.IgnoreDeferred(thinkingcache.Close)

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
			ConsecutiveFailuresThreshold: 5,
			StreamFirstContentTimeoutMs:  90000,
			StreamInactivityTimeoutMs:    90000,
			StreamToolCallIdleTimeoutMs:  300000,
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
		entries := make([]ratelimit.ChannelConfig, 0)
		for _, ct := range channelTypes {
			for idx, upstream := range ct.upstreams {
				autoFromHeaders := upstream.RateLimitAutoFromHeaders != nil && *upstream.RateLimitAutoFromHeaders
				entries = append(entries, ratelimit.ChannelConfig{
					APIType:      ct.apiType,
					ChannelIndex: idx,
					ChannelUID:   upstream.ChannelUID,
					Config: ratelimit.Config{
						RPM:             upstream.RateLimitRPM,
						WindowSeconds:   config.RateLimitWindowSeconds(upstream.RateLimitWindowMinutes),
						MaxConcurrent:   upstream.RateLimitMaxConcurrent,
						AutoFromHeaders: autoFromHeaders,
					},
				})
			}
		}
		rateLimitManager.ReconcileChannelConfigs(entries)
	}
	applyRateLimitConfig(cfgManager.GetConfig())
	cfgManager.RegisterOnConfigChange(applyRateLimitConfig)
	log.Printf("[RateLimit-Init] 主动限速管理器已初始化")

	// 初始化 URL 管理器（非阻塞，动态排序）
	urlManager := warmup.NewURLManager(30*time.Second, 3) // 30秒冷却期，连续3次失败后移到末尾
	log.Printf("[URLManager-Init] URL管理器已初始化 (冷却期: 30秒, 最大连续失败: 3)")

	// 初始化 Autopilot 健康中心（Phase 1 shadow/read-only）
	var autopilotManager *autopilot.Manager
	var autopilotDB *sql.DB // Phase B.1: 暴露给 StateEventStore 复用
	var autoDiscoveryRunner *autopilot.AutoDiscoveryRunner
	var newApiSyncService *autopilot.NewApiSubscriptionSyncService
	{
		autopilotStore, apErr := autopilot.NewProfileStore(paths.AutopilotDBPath)
		if apErr != nil {
			log.Printf("[Autopilot-Init] 警告: 初始化 ProfileStore 失败: %v (健康中心将不可用)", apErr)
		} else {
			metricsAdapters := map[string]autopilot.MetricsProvider{
				"messages":  autopilot.NewMetricsManagerAdapter(messagesMetricsManager),
				"responses": autopilot.NewMetricsManagerAdapter(responsesMetricsManager),
				"gemini":    autopilot.NewMetricsManagerAdapter(geminiMetricsManager),
				"chat":      autopilot.NewMetricsManagerAdapter(chatMetricsManager),
				"images":    autopilot.NewMetricsManagerAdapter(imagesMetricsManager),
				"vectors":   autopilot.NewMetricsManagerAdapter(vectorsMetricsManager),
			}
			autopilotMetrics := autopilot.NewMetricsAdapterManager(metricsAdapters)
			mgr, mgrErr := autopilot.NewManager(autopilotStore, autopilotMetrics, cfgManager, autopilot.ManagerConfig{
				WorkerInterval: 5 * time.Minute,
				QuietLogs:      envCfg.QuietPollingLogs,
			})
			if mgrErr != nil {
				log.Printf("[Autopilot-Init] 警告: 初始化 Manager 失败: %v (健康中心将不可用)", mgrErr)
			} else {
				autopilotManager = mgr
				autopilotDB = autopilotStore.DB() // Phase B.1: 复用 SQLite 连接

				// Phase 2: 创建 TraceStore（内存环形 + 可选 SQLite 落盘）
				traceStore, tsErr := autopilot.NewTraceStoreWithDB(autopilotStore.DB())
				if tsErr != nil {
					log.Printf("[Autopilot-Init] 警告: 初始化 TraceStore 失败: %v (路由追踪将不可用)", tsErr)
				} else {
					autopilotManager.SetTraceStore(traceStore)
					log.Printf("[Autopilot-Init] TraceStore 已初始化")
				}

				// 创建始终自动运行的 SmartRouter
				smartRouter := autopilot.NewSmartRouter(
					autopilotStore,
					autopilotManager.ManualIntentStore(),
					traceStore,
					cfgManager,
				)
				autopilotManager.SetSmartRouter(smartRouter)

				// Phase 2: 将 Advisor + LocalRuntimeStore 注入 SmartRouter
				autopilotManager.WireSmartRouter()
				log.Printf("[Autopilot-Init] SmartRouter advisor + localRuntimeStore 已注入")

				log.Printf("[Autopilot-Init] SmartRouter 已初始化 (Autopilot 自动运行)")

				// 注册限速信号回调：上游响应 → autopilot 限速发现器 + 时间桶
				// endpointUID 和 metricsKey 由 upstream_failover.go 在请求上下文中计算后传入
				// reason 携带 429 细分原因（如 account_rate_limit_exceeded），由
				// upstream_failover.go 读完 body 分类后传入，确保同一次 429 只通知一次
				ratelimit.SetUpstreamSignalCallback(func(endpointUID, metricsKey, serviceType, channelName string, isStream bool, latencyMs int64, headers http.Header, statusCode int, reason string) {
					if autopilotManager.ObserveRateLimitSignal(endpointUID, 0, metricsKey, serviceType, channelName, isStream, latencyMs, headers, statusCode, reason) {
						autopilotManager.RequestRateLimitApply()
					}
				})

				// 限速发现器配置接线：从 AutopilotRouting.RateLimitDiscovery 映射到 Discoverer 参数。
				// 配置热重载时同步刷新；PassiveAimdEnabled=false 时仍展示 header 信号但不执行 AIMD。
				applyRateLimitDiscoveryConfig := func() {
					disc := autopilotManager.RateLimitDiscoverer()
					if disc == nil {
						return
					}
					rlCfg := cfgManager.GetAutopilotRouting().RateLimitDiscovery
					discCfg := autopilot.RateLimitDiscovererConfig{
						PassiveAimdEnabled: rlCfg.PassiveAimdEnabled,
					}
					if rlCfg.MinRpm > 0 {
						discCfg.MinRPM = rlCfg.MinRpm
					}
					if rlCfg.MaxAutoRpm > 0 {
						discCfg.MaxAutoRPM = rlCfg.MaxAutoRpm
					}
					if rlCfg.MaxAutoConcurrent > 0 {
						discCfg.MaxAutoConcurrent = rlCfg.MaxAutoConcurrent
					}
					if rlCfg.ConfidenceThreshold > 0 {
						discCfg.ConfidenceThreshold = rlCfg.ConfidenceThreshold
					}
					if rlCfg.IncreaseIntervalMinutes > 0 {
						discCfg.AIMDIncreaseInterval = time.Duration(rlCfg.IncreaseIntervalMinutes) * time.Minute
					}
					if rlCfg.IncreaseStepPercent > 0 {
						discCfg.AIMDIncreasePercent = float64(rlCfg.IncreaseStepPercent)
					}
					disc.UpdateConfig(discCfg)
				}
				applyRateLimitDiscoveryConfig()
				cfgManager.RegisterOnConfigChange(func(_ config.Config) {
					applyRateLimitDiscoveryConfig()
				})

				// Phase 4 Item 8: A/B 测试（低比例统计抽样双发）
				// 默认关闭（Enabled=false），需显式 opt-in。
				// 主请求路径完全不变：影子请求在主响应返回后异步发起。
				abTestStore, abErr := autopilot.NewABTestStoreWithDB(autopilotStore.DB())
				if abErr != nil {
					log.Printf("[Autopilot-Init] 警告: 初始化 ABTestStore 失败: %v (A/B 测试将不可用)", abErr)
				} else {
					autopilotManager.SetABTestStore(abTestStore)
					abTestSampler := autopilot.NewABTestSampler(abTestStore, func() autopilot.ABTestSamplerConfig {
						abCfg := cfgManager.GetAutopilotRouting().ABTest
						return autopilot.ABTestSamplerConfig{
							Enabled:                  abCfg.Enabled,
							SampleRatio:              abCfg.SampleRatio,
							MaxShadowRequestsPerHour: abCfg.MaxShadowRequestsPerHour,
							ShadowCandidateCount:     abCfg.ShadowCandidateCount,
						}
					})
					autopilotManager.SetABTestSampler(abTestSampler)
					// 将 SmartRouter 候选排名回调连接到 A/B 测试缓存
					if smartRouter != nil {
						smartRouter.SetOnCandidatesRanked(abTestSampler.OnCandidatesRanked())
					}
					// 注册代理成功后回调：主响应已返回客户端后异步发起影子请求
					// 影子请求在独立 goroutine 中执行，不阻塞主请求路径
					common.SetPostSuccessfulProxyHook(func(channelKind, model, channelUID string, statusCode int, latencyMs int64, bodyBytes []byte) {
						// 获取全局 KillSwitch 状态
						routingCfg := cfgManager.GetAutopilotRouting()
						killSwitchActive := routingCfg.KillSwitch
						if envKillSwitch := os.Getenv("AUTOPILOT_KILL_SWITCH"); envKillSwitch == "true" || envKillSwitch == "1" {
							killSwitchActive = true
						}
						if !abTestSampler.ShouldSample(killSwitchActive) {
							return
						}
						abTestSampler.ExecuteShadowRequest(
							context.Background(),
							cfgManager,
							bodyBytes,
							model,
							channelKind,
							channelUID,
							statusCode,
							latencyMs,
						)
					})
					log.Printf("[Autopilot-Init] ABTestSampler 已初始化 (默认关闭, 抽样率: 0.01)")
				}
			}
		}
	}

	channelScheduler := scheduler.NewChannelScheduler(cfgManager, messagesMetricsManager, responsesMetricsManager, geminiMetricsManager, chatMetricsManager, imagesMetricsManager, traceAffinityManager, urlManager, vectorsMetricsManager)
	channelScheduler.SetRateLimitManager(rateLimitManager)
	log.Printf("[Scheduler-Init] 多渠道调度器已初始化 (失败率阈值: %.0f%%, 滑动窗口: %d, 连续失败阈值: %d)",
		messagesMetricsManager.GetFailureThreshold()*100, messagesMetricsManager.GetWindowSize(), messagesMetricsManager.GetConsecutiveRetryableFailuresThreshold())

	// Phase B.1：创建跨模块事件总线并注入 ConfigManager + 6 个 MetricsManager + autopilot Manager。
	// 可选依赖：未注入时所有 publish no-op，系统行为不变。
	var stateEventBus *eventbus.Bus
	{
		stateEventBus = eventbus.NewBus()
		cfgManager.SetEventBus(stateEventBus)
		messagesMetricsManager.SetEventBus(stateEventBus)
		responsesMetricsManager.SetEventBus(stateEventBus)
		geminiMetricsManager.SetEventBus(stateEventBus)
		chatMetricsManager.SetEventBus(stateEventBus)
		imagesMetricsManager.SetEventBus(stateEventBus)
		vectorsMetricsManager.SetEventBus(stateEventBus)
		presetstore.Default().SetEventBus(stateEventBus)
		handlers.SetCapabilityTestEventBus(stateEventBus)
		if autopilotManager != nil {
			var stateStore *autopilot.StateEventStore
			if autopilotDB != nil {
				var storeErr error
				stateStore, storeErr = autopilot.NewStateEventStoreWithDB(autopilotDB)
				if storeErr != nil {
					log.Printf("[EventBus] 警告: 初始化 StateEventStore 失败: %v", storeErr)
					stateStore = nil
				}
			}
			autopilotManager.WireEventBus(stateEventBus, stateStore)
			if stateStore != nil {
				log.Printf("[EventBus] 跨模块事件总线已注入 (config + 6 metrics + autopilot manager + StateEventStore)")
			} else {
				log.Printf("[EventBus] 跨模块事件总线已注入 (config + 6 metrics + autopilot manager)")
			}
		}
	}

	// 通过 CandidateFilter 回调注入 Autopilot SmartRouter 的渠道级过滤与重排逻辑。
	// Kill Switch 启用时不注入 filter，恢复调度器默认行为。
	if autopilotManager != nil && autopilotManager.SmartRouter() != nil {
		sr := autopilotManager.SmartRouter()
		channelScheduler.SetCandidateFilterProvider(func(ctx context.Context, kind scheduler.ChannelKind, model string) (scheduler.CandidateFilterFunc, scheduler.CandidateSelectionObserver) {
			profile, ok := autopilot.RequestProfileFromContext(ctx)
			if !ok {
				profile = autopilot.BuildRequestProfile(autopilot.RequestProfileFeatures{
					Model:       model,
					ChannelKind: string(kind),
					Operation:   "completion",
				})
			}
			profile.Model = model
			profile.ChannelKind = string(kind)
			return sr.CandidateFilterForWithActual(&profile)
		})
		log.Printf("[Scheduler-Init] SmartRouter Autopilot filter 已注册")
	}

	// EndpointAttemptPolicy 注入 + FastDecay 通知 + L2 探测 + 限速应用。
	// 注册 endpoint policy hook：handlers 层 TryUpstreamWithAllKeys 调用时自动获取 policy；
	// Kill Switch 启用时返回 nil，不注入策略。
	if autopilotManager != nil && autopilotManager.SmartRouter() != nil {
		sr := autopilotManager.SmartRouter()
		profileStore := sr.ProfileStore()
		traceStore := sr.TraceStore()
		fastDecayScorer := autopilotManager.FastDecayScorer()
		if traceStore != nil {
			common.SetRoutingOutcomeRecorderHook(func(traceUID string, outcome autopilot.RoutingOutcome) {
				if err := traceStore.RecordOutcome(traceUID, outcome); err != nil {
					log.Printf("[Autopilot-Outcome] 警告: trace=%s 终态记录失败: %v", traceUID, err)
				}
			})
			// endpoint 尝试摘要记录器：每次上游尝试向 trace 追加安全摘要
			common.SetAttemptRecorderHook(func(traceUID string, attempt autopilot.EndpointAttemptSummary) {
				traceStore.AppendEndpointAttempt(traceUID, attempt)
			})
			// scheduler 裁决记录器：选择完成后把过滤阶段/跳过明细附加到 trace
			common.SetSchedulerDecisionHook(func(traceUID string, trace *scheduler.SelectionTrace) {
				traceStore.AttachSchedulerDecision(traceUID, autopilot.NormalizeSelectionTrace(trace))
			})
		}

		// 场景预设配置读取器：请求画像构建时解析 X-Routing-Scenario 头与全局场景模式
		common.SetScenarioConfigProvider(func() config.ScenarioRoutingConfig {
			return cfgManager.GetAutopilotRouting().Scenario
		})

		// endpoint policy hook：为每个请求构建 EndpointAttemptPolicy
		common.SetEndpointPolicyProviderHook(func(c *gin.Context, model string, upstream *config.UpstreamConfig) *autopilot.EndpointAttemptPolicy {
			autopilotCfg := cfgManager.GetAutopilotRouting()
			if autopilotCfg.KillSwitch {
				return nil
			}
			mode := autopilot.RoutingModeAuto
			req := autopilot.BuildRequestProfile(autopilot.RequestProfileFeatures{Model: model})
			if c != nil && c.Request != nil {
				if profile, ok := autopilot.RequestProfileFromContext(c.Request.Context()); ok {
					req = profile
					req.Model = model
				}
			}
			deps := autopilot.EndpointPolicyDeps{
				ProfileStore:  profileStore,
				FastDecay:     fastDecayScorer,
				TraceStore:    traceStore,
				ModelResolver: autopilotManager.ModelResolver(),
				GetRoutingCfg: func() config.AutopilotRoutingConfig { return cfgManager.GetAutopilotRouting() },
				APIKeyConfigs: config.NormalizeAPIKeyConfigsForView(*upstream),
			}
			return autopilot.BuildEndpointPolicy(deps, &req, mode)
		})

		// FastDecay 通知 hook：请求成功/失败时实时更新 FastDecayScorer
		common.SetNotifyEndpointResultHook(func(endpointUID string, success bool) {
			if fastDecayScorer != nil && endpointUID != "" {
				fastDecayScorer.RecordResult(endpointUID, success)
			}
		})
		log.Printf("[Autopilot-Init] EndpointAttemptPolicy hook + FastDecay notify hook 已注册")
	}

	// RateLimitApplier：将发现的限速建议应用到运行态 limiter
	if autopilotManager != nil && rateLimitManager != nil {
		rlApplier := autopilot.NewRateLimitApplier(
			autopilotManager.RateLimitDiscoverer(),
			rateLimitManager,
			func() config.AutopilotRoutingConfig { return cfgManager.GetAutopilotRouting() },
			envCfg.QuietPollingLogs,
		)
		autopilotManager.SetRateLimitApplier(rlApplier)
		autopilotManager.RefreshRateLimitMappings()
		// 配置变更时同步刷新 endpoint mapping，并立即唤醒 apply worker。
		// 这会使 kill switch、Enabled=false 或模式切换到 shadow/off 立即清理
		// 已注入 discovered RPM，而不等待 30 秒 ticker 或 5 分钟 collectAll。
		cfgManager.RegisterOnConfigChange(func(_ config.Config) {
			autopilotManager.RefreshRateLimitMappings()
			autopilotManager.RequestRateLimitApply()
		})
	}

	// Phase 4 Item 4: 用量画像记录 hook（渠道推荐用）。
	// 请求成功完成后（主响应已返回客户端之后）记录 proxyKeyMask -> channelUID 归因，
	// 纯观测性累积，不参与任何调度/候选过滤决策。
	if autopilotManager != nil {
		common.SetUsagePatternRecorderHook(func(proxyKeyMask, channelKind, channelUID, model string) {
			autopilotManager.RecordUsagePattern(proxyKeyMask, channelKind, channelUID, model)
		})
	}

	// L2 ProbeWorker：按配置门控启动（默认关闭）
	if autopilotManager != nil {
		autopilotCfg := cfgManager.GetAutopilotRouting()
		if autopilotCfg.HealthCheck.L2ProbeEnabled {
			probeWorker := autopilot.NewProbeWorker(
				autopilotManager.ProfileStore(),
				autopilot.ProbeWorkerConfig{
					QuietLogs:              envCfg.QuietPollingLogs,
					ProbeRecoveryThreshold: autopilotCfg.HealthCheck.ProbeRecoveryThreshold,
				},
			)
			probeWorker.SetAPIKeyResolver(autopilotManager.ResolveAPIKey)
			autopilotManager.SetProbeWorker(probeWorker)
			log.Printf("[Autopilot-Init] L2 ProbeWorker 已创建 (将在 StartWorker 时启动)")
		}
	}

	// Phase 4 Item 6: SubscriptionRefreshWorker：按配置门控启动（默认关闭）
	if autopilotManager != nil {
		autopilotCfg := cfgManager.GetAutopilotRouting()
		if autopilotCfg.SubscriptionAutoRefresh.Enabled {
			refreshWorker := autopilot.NewSubscriptionRefreshWorker(
				autopilotManager.SubscriptionStore(),
				nil, // 使用默认 fetcher 注册表（OpenAI/Anthropic/Google）
				autopilot.SubscriptionRefreshWorkerConfig{
					RefreshInterval: time.Duration(autopilotCfg.SubscriptionAutoRefresh.RefreshIntervalHours) * time.Hour,
					DailyBudget:     autopilotCfg.SubscriptionAutoRefresh.DailyBudget,
					RefreshTimeout:  time.Duration(autopilotCfg.SubscriptionAutoRefresh.RequestTimeoutSeconds) * time.Second,
					QuietLogs:       envCfg.QuietPollingLogs,
				},
				func() bool { return cfgManager.GetAutopilotRouting().SubscriptionAutoRefresh.Enabled },
			)
			autopilotManager.SetSubscriptionRefreshWorker(refreshWorker)
			log.Printf("[Autopilot-Init] SubscriptionRefreshWorker 已创建 (将在 StartWorker 时启动)")
		}
	}

	if autopilotManager != nil {
		autopilotManager.StartWorker(context.Background())
		log.Printf("[Autopilot-Init] 健康中心已初始化 (DB: %s, 间隔: 5分钟)", paths.AutopilotDBPath)
	}

	// Phase 3B-2: ModelSupportResolver 注入（无条件注册，安全门控在 ResolveModelSupport 内部）。
	// 调度器候选筛选时调用，AutoManaged 渠道 + 三条件门控通过才走 ModelResolver，否则回退 ExplainModelSupport。
	if autopilotManager != nil {
		channelScheduler.SetModelSupportResolverProvider(func(ctx context.Context, kind scheduler.ChannelKind, upstream *config.UpstreamConfig, model string) (bool, string, string, string) {
			if profile, ok := autopilot.RequestProfileFromContext(ctx); ok {
				profile.Model = model
				profile.ChannelKind = string(kind)
				return autopilotManager.ResolveModelSupportWithFloor(
					string(kind),
					upstream,
					model,
					autopilot.BuildCapabilityFloorFromRequestProfile(&profile),
				)
			}
			return autopilotManager.ResolveModelSupport(string(kind), upstream, model)
		})
		log.Printf("[Autopilot-Init] ModelSupportResolver 已注册到调度器")
	}

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

	// 渠道保活验证（L1 带 key 拉上游模型列表）：依赖指标持久化存储 key_health 表
	var healthCheckManager *healthcheck.Manager
	if metricsStore != nil {
		healthCheckManager = healthcheck.NewManager(
			func() config.Config { return cfgManager.GetConfig() },
			metricsStore,
			// 鉴权失败拉黑：交 ConfigManager.BlacklistKeyWithRecoverAt
			func(channelType string, channelIndex int, apiKey, reason, message, recoverAt string) {
				if err := cfgManager.BlacklistKeyWithRecoverAt(healthCheckAPIType(channelType), channelIndex, apiKey, reason, message, recoverAt); err != nil {
					log.Printf("[HealthCheck-Blacklist] 警告: 拉黑 Key 失败: %v", err)
				}
			},
			// 失败喂熔断：按渠道类型喂对应指标管理器，并写入渠道日志（保活验证失败此前对渠道日志不可见）
			func(channelType string, channelIndex int, baseURL, apiKey, serviceType, model, detail string) {
				kind := scheduler.ChannelKind(channelType)
				normalizedServiceType := scheduler.NormalizedMetricsServiceType(kind, serviceType)
				channelScheduler.RecordFailure(baseURL, apiKey, normalizedServiceType, kind)

				channelName := ""
				cfg := cfgManager.GetConfig()
				if upstreams := healthcheck.UpstreamsFor(&cfg, channelType); channelIndex >= 0 && channelIndex < len(upstreams) {
					channelName = upstreams[channelIndex].Name
				}
				metricsKey := metrics.GenerateMetricsIdentityKey(baseURL, apiKey, normalizedServiceType)
				common.RecordChannelLogWithSource(
					channelScheduler.GetChannelLogStore(kind),
					metricsKey,
					channelIndex,
					model, "", 0, 0, false,
					apiKey, baseURL, detail, channelType,
					false,
					metrics.RequestSourceHealthCheck,
					channelName,
				)
			},
			healthcheck.Options{},
		)
		healthCheckManager.SetModelCircuitLookup(func(channelType string) *metrics.ModelCircuitTracker {
			var manager *metrics.MetricsManager
			switch channelType {
			case "messages":
				manager = channelScheduler.GetMessagesMetricsManager()
			case "chat":
				manager = channelScheduler.GetChatMetricsManager()
			case "responses":
				manager = channelScheduler.GetResponsesMetricsManager()
			case "gemini":
				manager = channelScheduler.GetGeminiMetricsManager()
			case "images":
				manager = channelScheduler.GetImagesMetricsManager()
			case "vectors":
				manager = channelScheduler.GetVectorsMetricsManager()
			}
			if manager == nil {
				return nil
			}
			return manager.ModelCircuit()
		})
		// 注册六类渠道的 L1 fetcher（复用各渠道 GetChannelModels handler 的薄包装）
		healthCheckManager.RegisterL1Fetcher("messages", healthCheckL1Fetcher(messages.GetChannelModels(cfgManager)))
		healthCheckManager.RegisterL1Fetcher("chat", healthCheckL1Fetcher(chat.GetChannelModels(cfgManager)))
		healthCheckManager.RegisterL1Fetcher("responses", healthCheckL1Fetcher(responses.GetChannelModels(cfgManager)))
		healthCheckManager.RegisterL1Fetcher("gemini", healthCheckL1Fetcher(gemini.GetChannelModels(cfgManager)))
		healthCheckManager.RegisterL1Fetcher("images", healthCheckL1Fetcher(images.GetChannelModels(cfgManager)))
		healthCheckManager.RegisterL1Fetcher("vectors", healthCheckL1Fetcher(vectors.GetChannelModels(cfgManager)))
		// 注入火山套餐 AFP 余额查询器，稀疏 L2 预算按剩余额度比例收紧，避免探测蚕食生产额度
		healthCheckManager.SetProbeUsageResolver(cfgManager)
		healthCheckManager.Start()
	} else {
		log.Printf("[HealthCheck-Init] 指标持久化不可用，渠道保活验证未启动")
	}

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
			restoredKeyModels := 0
			activatedChannels := 0
			for _, result := range results {
				restoredKeys += len(result.RestoredKeys)
				restoredKeyModels += len(result.RestoredKeyModels)
				if result.ActivatedChannel {
					activatedChannels++
				}
			}
			log.Printf("[Scheduler-Recovery] UTC 自动恢复完成：恢复 %d 个 key，%d 个 (key,模型) 组合，激活 %d 个渠道", restoredKeys, restoredKeyModels, activatedChannels)
			return true
		}
		runDueRecovery := func(now time.Time) {
			results, err := channelScheduler.RunDueRecoveries(now.UTC())
			if err != nil {
				log.Printf("[Scheduler-Recovery] 警告: 到期恢复执行失败: %v", err)
				return
			}
			if len(results) == 0 {
				return
			}
			restoredKeys := 0
			restoredKeyModels := 0
			activatedChannels := 0
			for _, result := range results {
				restoredKeys += len(result.RestoredKeys)
				restoredKeyModels += len(result.RestoredKeyModels)
				if result.ActivatedChannel {
					activatedChannels++
				}
			}
			log.Printf("[Scheduler-Recovery] 到期恢复完成：恢复 %d 个 key，%d 个 (key,模型) 组合，激活 %d 个渠道", restoredKeys, restoredKeyModels, activatedChannels)
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
		runDueRecovery(startupNow)
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
				runDueRecovery(now)
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
	discoveryModelFetchers := buildChannelDiscoveryModelFetchers(cfgManager)
	apiGroup := r.Group("/api")
	{
		apiGroup.POST("/copilot/oauth/device/code", copilot.RequestDeviceCode())
		apiGroup.POST("/copilot/oauth/token", copilot.PollAccessToken())
		apiGroup.POST("/copilot/oauth/verify", copilot.VerifyToken())

		// 预置数据（订阅来源分类等），前端表单选项来源；独立于 autopilot 开关。
		apiGroup.GET("/presets", presetstore.Handler(presetstore.Default()))
		apiGroup.GET("/presets/status", presetstore.StatusHandler(presetUpdater))

		apiGroup.POST("/channel-discovery", handlers.ChannelDiscoveryWithModelFetchers(cfgManager, discoveryModelFetchers))
		// 快速探活：仅探一个真实模型以定 primaryKind，不做全量协议/能力探测。
		apiGroup.POST("/channel-discovery-fast", handlers.ChannelDiscoveryFast(cfgManager))

		// 渠道保活验证管理 API（六类渠道，挂在各渠道 ping 路由附近）
		registerChannelHealthRoutes := func(channelType string) {
			base := "/" + channelType + "/channels/:id/health"
			if healthCheckManager == nil {
				unavailable := func(c *gin.Context) {
					c.JSON(http.StatusServiceUnavailable, gin.H{"error": "渠道保活验证未启用（指标持久化不可用）"})
				}
				apiGroup.GET(base, unavailable)
				apiGroup.POST(base+"/check", unavailable)
				return
			}
			apiGroup.GET(base, healthCheckManager.ChannelHealthHandler(channelType))
			apiGroup.POST(base+"/check", healthCheckManager.TriggerChannelCheckHandler(channelType))
		}

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
		apiGroup.POST("/messages/channels/:id/keys/restore-model", handlers.RestoreKeyModel(cfgManager, "Messages"))
		apiGroup.POST("/messages/channels/:id/keys/group-model/disable", handlers.DisableGroupModel(cfgManager, "Messages"))
		apiGroup.POST("/messages/channels/:id/keys/group-model/restore", handlers.RestoreGroupModel(cfgManager, "Messages"))
		apiGroup.POST("/messages/channels/:id/keys/suspend", handlers.SuspendAPIKey(cfgManager, "Messages"))
		apiGroup.POST("/messages/channels/:id/keys/resume", handlers.ResumeAPIKey(cfgManager, "Messages"))
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
		apiGroup.POST("/messages/channels/scheduler/diagnose", handlers.DiagnoseSchedulerSelection(channelScheduler, scheduler.ChannelKindMessages))
		apiGroup.GET("/messages/global/stats/history", handlers.GetGlobalStatsHistory(messagesMetricsManager))
		apiGroup.GET("/messages/channels/dashboard", handlers.GetChannelDashboard(cfgManager, channelScheduler)) // 统一 dashboard 端点，支持 ?type=messages|responses|chat|gemini|images|vectors
		apiGroup.GET("/messages/ping/:id", messages.PingChannel(cfgManager))
		apiGroup.GET("/messages/ping", messages.PingAllChannels(cfgManager))
		apiGroup.POST("/messages/channels/:id/models", messages.GetChannelModels(cfgManager))
		registerChannelHealthRoutes("messages")
		apiGroup.GET("/messages/models/stats/history", handlers.GetModelStatsHistory(messagesMetricsManager))
		apiGroup.GET("/messages/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages), cfgManager, scheduler.ChannelKindMessages, channelScheduler.GetMessagesMetricsManager()))
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
		apiGroup.POST("/responses/channels/:id/keys/restore-model", handlers.RestoreKeyModel(cfgManager, "Responses"))
		apiGroup.POST("/responses/channels/:id/keys/group-model/disable", handlers.DisableGroupModel(cfgManager, "Responses"))
		apiGroup.POST("/responses/channels/:id/keys/group-model/restore", handlers.RestoreGroupModel(cfgManager, "Responses"))
		apiGroup.POST("/responses/channels/:id/keys/suspend", handlers.SuspendAPIKey(cfgManager, "Responses"))
		apiGroup.POST("/responses/channels/:id/keys/resume", handlers.ResumeAPIKey(cfgManager, "Responses"))
		apiGroup.PUT("/responses/channels/:id/mappings", responses.UpdateModelMapping(cfgManager))

		// Responses 多渠道调度 API
		apiGroup.POST("/responses/channels/reorder", responses.ReorderChannels(cfgManager))
		apiGroup.PATCH("/responses/channels/:id/status", responses.SetChannelStatus(cfgManager))
		apiGroup.POST("/responses/channels/:id/resume", handlers.ResumeChannel(channelScheduler, cfgManager, true))
		apiGroup.POST("/responses/channels/:id/promotion", responses.SetChannelPromotion(cfgManager))
		apiGroup.GET("/responses/channels/metrics", handlers.GetChannelMetricsWithConfig(responsesMetricsManager, cfgManager, true))
		apiGroup.GET("/responses/channels/metrics/history", handlers.GetChannelMetricsHistory(responsesMetricsManager, cfgManager, true))
		apiGroup.GET("/responses/channels/:id/keys/metrics/history", handlers.GetChannelKeyMetricsHistory(responsesMetricsManager, cfgManager, true))
		apiGroup.POST("/responses/channels/scheduler/diagnose", handlers.DiagnoseSchedulerSelection(channelScheduler, scheduler.ChannelKindResponses))
		apiGroup.GET("/responses/global/stats/history", handlers.GetGlobalStatsHistory(responsesMetricsManager))
		apiGroup.GET("/responses/ping/:id", responses.PingChannel(cfgManager))
		apiGroup.GET("/responses/ping", responses.PingAllChannels(cfgManager))
		apiGroup.POST("/responses/channels/:id/models", responses.GetChannelModels(cfgManager))
		registerChannelHealthRoutes("responses")
		apiGroup.GET("/responses/models/stats/history", handlers.GetModelStatsHistory(responsesMetricsManager))
		apiGroup.GET("/responses/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses), cfgManager, scheduler.ChannelKindResponses, channelScheduler.GetResponsesMetricsManager()))
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
		apiGroup.POST("/gemini/channels/:id/keys/restore-model", handlers.RestoreKeyModel(cfgManager, "Gemini"))
		apiGroup.POST("/gemini/channels/:id/keys/group-model/disable", handlers.DisableGroupModel(cfgManager, "Gemini"))
		apiGroup.POST("/gemini/channels/:id/keys/group-model/restore", handlers.RestoreGroupModel(cfgManager, "Gemini"))
		apiGroup.POST("/gemini/channels/:id/keys/suspend", handlers.SuspendAPIKey(cfgManager, "Gemini"))
		apiGroup.POST("/gemini/channels/:id/keys/resume", handlers.ResumeAPIKey(cfgManager, "Gemini"))
		apiGroup.PUT("/gemini/channels/:id/mappings", gemini.UpdateModelMapping(cfgManager))

		// Gemini 多渠道调度 API
		apiGroup.POST("/gemini/channels/reorder", gemini.ReorderChannels(cfgManager))
		apiGroup.PATCH("/gemini/channels/:id/status", gemini.SetChannelStatus(cfgManager))
		apiGroup.POST("/gemini/channels/:id/resume", handlers.ResumeChannelWithKind(channelScheduler, cfgManager, scheduler.ChannelKindGemini))
		apiGroup.POST("/gemini/channels/:id/promotion", gemini.SetChannelPromotion(cfgManager))
		apiGroup.GET("/gemini/channels/metrics", handlers.GetGeminiChannelMetrics(geminiMetricsManager, cfgManager))
		apiGroup.GET("/gemini/channels/metrics/history", handlers.GetGeminiChannelMetricsHistory(geminiMetricsManager, cfgManager))
		apiGroup.GET("/gemini/channels/:id/keys/metrics/history", handlers.GetGeminiChannelKeyMetricsHistory(geminiMetricsManager, cfgManager))
		apiGroup.POST("/gemini/channels/scheduler/diagnose", handlers.DiagnoseSchedulerSelection(channelScheduler, scheduler.ChannelKindGemini))
		apiGroup.GET("/gemini/global/stats/history", handlers.GetGlobalStatsHistory(geminiMetricsManager))
		apiGroup.GET("/gemini/ping/:id", gemini.PingChannel(cfgManager))
		apiGroup.GET("/gemini/ping", gemini.PingAllChannels(cfgManager))
		apiGroup.POST("/gemini/channels/:id/models", gemini.GetChannelModels(cfgManager))
		registerChannelHealthRoutes("gemini")
		apiGroup.GET("/gemini/models/stats/history", handlers.GetModelStatsHistory(geminiMetricsManager))
		apiGroup.GET("/gemini/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindGemini), cfgManager, scheduler.ChannelKindGemini, channelScheduler.GetGeminiMetricsManager()))
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
		apiGroup.POST("/chat/channels/:id/keys/restore-model", handlers.RestoreKeyModel(cfgManager, "Chat"))
		apiGroup.POST("/chat/channels/:id/keys/group-model/disable", handlers.DisableGroupModel(cfgManager, "Chat"))
		apiGroup.POST("/chat/channels/:id/keys/group-model/restore", handlers.RestoreGroupModel(cfgManager, "Chat"))
		apiGroup.POST("/chat/channels/:id/keys/suspend", handlers.SuspendAPIKey(cfgManager, "Chat"))
		apiGroup.POST("/chat/channels/:id/keys/resume", handlers.ResumeAPIKey(cfgManager, "Chat"))
		apiGroup.PUT("/chat/channels/:id/mappings", chat.UpdateModelMapping(cfgManager))

		// Chat 多渠道调度 API
		apiGroup.POST("/chat/channels/reorder", chat.ReorderChannels(cfgManager))
		apiGroup.PATCH("/chat/channels/:id/status", chat.SetChannelStatus(cfgManager))
		apiGroup.POST("/chat/channels/:id/resume", handlers.ResumeChannelWithKind(channelScheduler, cfgManager, scheduler.ChannelKindChat))
		apiGroup.POST("/chat/channels/:id/promotion", chat.SetChannelPromotion(cfgManager))
		apiGroup.GET("/chat/channels/metrics", handlers.GetChatChannelMetrics(chatMetricsManager, cfgManager))
		apiGroup.GET("/chat/channels/metrics/history", handlers.GetChatChannelMetricsHistory(chatMetricsManager, cfgManager))
		apiGroup.GET("/chat/channels/:id/keys/metrics/history", handlers.GetChatChannelKeyMetricsHistory(chatMetricsManager, cfgManager))
		apiGroup.POST("/chat/channels/scheduler/diagnose", handlers.DiagnoseSchedulerSelection(channelScheduler, scheduler.ChannelKindChat))
		apiGroup.GET("/chat/global/stats/history", handlers.GetGlobalStatsHistory(chatMetricsManager))
		apiGroup.GET("/chat/ping/:id", chat.PingChannel(cfgManager))
		apiGroup.GET("/chat/ping", chat.PingAllChannels(cfgManager))
		apiGroup.POST("/chat/channels/:id/models", chat.GetChannelModels(cfgManager))
		registerChannelHealthRoutes("chat")
		apiGroup.GET("/chat/models/stats/history", handlers.GetModelStatsHistory(chatMetricsManager))
		apiGroup.GET("/chat/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat), cfgManager, scheduler.ChannelKindChat, channelScheduler.GetChatMetricsManager()))
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
		apiGroup.POST("/images/channels/:id/keys/restore-model", handlers.RestoreKeyModel(cfgManager, "Images"))
		apiGroup.POST("/images/channels/:id/keys/group-model/disable", handlers.DisableGroupModel(cfgManager, "Images"))
		apiGroup.POST("/images/channels/:id/keys/group-model/restore", handlers.RestoreGroupModel(cfgManager, "Images"))
		apiGroup.POST("/images/channels/:id/keys/suspend", handlers.SuspendAPIKey(cfgManager, "Images"))
		apiGroup.POST("/images/channels/:id/keys/resume", handlers.ResumeAPIKey(cfgManager, "Images"))
		apiGroup.PUT("/images/channels/:id/mappings", images.UpdateModelMapping(cfgManager))

		// Images 多渠道调度 API
		apiGroup.POST("/images/channels/reorder", images.ReorderChannels(cfgManager))
		apiGroup.PATCH("/images/channels/:id/status", images.SetChannelStatus(cfgManager))
		apiGroup.POST("/images/channels/:id/resume", handlers.ResumeChannelWithKind(channelScheduler, cfgManager, scheduler.ChannelKindImages))
		apiGroup.POST("/images/channels/:id/promotion", images.SetChannelPromotion(cfgManager))
		apiGroup.GET("/images/channels/metrics", handlers.GetImagesChannelMetrics(imagesMetricsManager, cfgManager))
		apiGroup.GET("/images/channels/metrics/history", handlers.GetImagesChannelMetricsHistory(imagesMetricsManager, cfgManager))
		apiGroup.GET("/images/channels/:id/keys/metrics/history", handlers.GetImagesChannelKeyMetricsHistory(imagesMetricsManager, cfgManager))
		apiGroup.POST("/images/channels/scheduler/diagnose", handlers.DiagnoseSchedulerSelection(channelScheduler, scheduler.ChannelKindImages))
		apiGroup.GET("/images/global/stats/history", handlers.GetGlobalStatsHistory(imagesMetricsManager))
		apiGroup.GET("/images/ping/:id", images.PingChannel(cfgManager))
		apiGroup.GET("/images/ping", images.PingAllChannels(cfgManager))
		apiGroup.POST("/images/channels/:id/models", images.GetChannelModels(cfgManager))
		registerChannelHealthRoutes("images")
		apiGroup.GET("/images/models/stats/history", handlers.GetModelStatsHistory(imagesMetricsManager))
		apiGroup.GET("/images/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindImages), cfgManager, scheduler.ChannelKindImages, channelScheduler.GetImagesMetricsManager()))

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
		apiGroup.POST("/vectors/channels/:id/keys/restore-model", handlers.RestoreKeyModel(cfgManager, "Vectors"))
		apiGroup.POST("/vectors/channels/:id/keys/group-model/disable", handlers.DisableGroupModel(cfgManager, "Vectors"))
		apiGroup.POST("/vectors/channels/:id/keys/group-model/restore", handlers.RestoreGroupModel(cfgManager, "Vectors"))
		apiGroup.POST("/vectors/channels/:id/keys/suspend", handlers.SuspendAPIKey(cfgManager, "Vectors"))
		apiGroup.POST("/vectors/channels/:id/keys/resume", handlers.ResumeAPIKey(cfgManager, "Vectors"))
		apiGroup.PUT("/vectors/channels/:id/mappings", vectors.UpdateModelMapping(cfgManager))

		// Vectors 多渠道调度 API
		apiGroup.POST("/vectors/channels/reorder", vectors.ReorderChannels(cfgManager))
		apiGroup.PATCH("/vectors/channels/:id/status", vectors.SetChannelStatus(cfgManager))
		apiGroup.POST("/vectors/channels/:id/resume", handlers.ResumeChannelWithKind(channelScheduler, cfgManager, scheduler.ChannelKindVectors))
		apiGroup.POST("/vectors/channels/:id/promotion", vectors.SetChannelPromotion(cfgManager))
		apiGroup.GET("/vectors/channels/metrics", handlers.GetVectorsChannelMetrics(vectorsMetricsManager, cfgManager))
		apiGroup.GET("/vectors/channels/metrics/history", handlers.GetVectorsChannelMetricsHistory(vectorsMetricsManager, cfgManager))
		apiGroup.GET("/vectors/channels/:id/keys/metrics/history", handlers.GetVectorsChannelKeyMetricsHistory(vectorsMetricsManager, cfgManager))
		apiGroup.POST("/vectors/channels/scheduler/diagnose", handlers.DiagnoseSchedulerSelection(channelScheduler, scheduler.ChannelKindVectors))
		apiGroup.GET("/vectors/global/stats/history", handlers.GetGlobalStatsHistory(vectorsMetricsManager))
		apiGroup.GET("/vectors/ping/:id", vectors.PingChannel(cfgManager))
		apiGroup.GET("/vectors/ping", vectors.PingAllChannels(cfgManager))
		apiGroup.POST("/vectors/channels/:id/models", vectors.GetChannelModels(cfgManager))
		registerChannelHealthRoutes("vectors")
		apiGroup.GET("/vectors/models/stats/history", handlers.GetModelStatsHistory(vectorsMetricsManager))
		apiGroup.GET("/vectors/channels/:id/logs", handlers.GetChannelLogs(channelScheduler.GetChannelLogStore(scheduler.ChannelKindVectors), cfgManager, scheduler.ChannelKindVectors, channelScheduler.GetVectorsMetricsManager()))

		// 逻辑渠道管理 API（任务 #11）
		logicalchannels.RegisterRoutes(apiGroup, cfgManager, channelScheduler)

		// 渠道 v2 统一 API（Channel Data Model v2：渠道→key→endpoint→模型 + 跨账号共享能力 + 统一写端点）
		channelsv2.RegisterRoutes(apiGroup, cfgManager, channelScheduler)

		// 健康中心 API（Phase 1 shadow/read-only）
		if autopilotManager != nil {
			autopilot.RegisterRoutes(apiGroup, autopilotManager)
			// 画像覆盖率诊断只读 API（Task 7 覆盖率门槛，手工映射表退役前验证用）
			autopilot.RegisterProfileCoverageRoutes(apiGroup, autopilotManager)

			// Phase 2 第三批：自动托管发现执行器。
			// 提前到这里构造（原在 Phase 2 自动托管 API 注册处），因为 §8.5.1 new-api
			// 订阅集成的 provision 端点也需要复用同一个 runner 实例来触发 Discovery。
			autoDiscoveryRunner = autopilot.NewAutoDiscoveryRunner(autopilotManager.ProfileStore(), autopilotManager.EventHub())
			// Phase 3B-2：注入 ModelProfileStore，使自动发现时同步写入 model_profiles
			if mps := autopilotManager.ModelProfileStore(); mps != nil {
				autoDiscoveryRunner.ModelProfileStore = mps
			}
			// 注入共享事件总线；事件为只读观测，不影响调度。
			autoDiscoveryRunner.SetEventBus(stateEventBus)
			// 后台 discovery 任务落盘 + 断点续传：复用 ProfileStore 的 SQLite 连接，
			// 注入 taskStore 启用端点级 checkpoint；并在接受请求前恢复未完成 discovery。
			if ps := autopilotManager.ProfileStore(); ps != nil && ps.DB() != nil {
				discoveryTaskStore, err := autopilot.NewDiscoveryTaskStoreWithDB(ps.DB())
				if err != nil {
					log.Printf("[Autopilot-DiscoveryTaskStore] 初始化失败，降级为纯内存 discovery: %v", err)
				} else {
					autoDiscoveryRunner.SetTaskStore(discoveryTaskStore)
					autoDiscoveryRunner.ResumeIncompleteDiscoveries(cfgManager)
					autoDiscoveryRunner.StartModelRefreshLoop(cfgManager)
				}
			}

			newApiSyncService = autopilot.NewNewApiSubscriptionSyncService(autopilot.NewApiSubscriptionSyncServiceDeps{
				Store: autopilotManager.SubscriptionStore(), CfgManager: cfgManager, Runner: autoDiscoveryRunner,
				QuietLogs: envCfg.QuietPollingLogs,
				Enabled:   func() bool { return cfgManager.GetAutopilotRouting().SubscriptionAutoRefresh.Enabled },
			})
			newApiSyncService.SyncAllNewAPIAsync(context.Background())
			newApiSyncService.Start(context.Background())

			// 订阅中心 API
			autopilot.RegisterSubscriptionRoutes(apiGroup, autopilotManager.SubscriptionStore(), autopilotManager.SubscriptionRefreshWorker(), newApiSyncService)
			autopilot.RegisterKeyMultiplierRoutes(apiGroup, cfgManager)
			autopilot.RegisterCostRoutes(apiGroup, cfgManager)
			// §8.5.1：new-api 订阅集成 API（校验 + 完整 provision 流程）
			autopilot.RegisterNewApiSubscriptionRoutes(apiGroup, &autopilot.NewApiRouteDeps{
				Store:       autopilotManager.SubscriptionStore(),
				CfgManager:  cfgManager,
				Runner:      autoDiscoveryRunner,
				SyncService: newApiSyncService,
			})
			// new-api 多账号管理 API
			autopilot.RegisterSubscriptionAccountRoutes(apiGroup, &autopilot.NewApiRouteDeps{
				Store:       autopilotManager.SubscriptionStore(),
				CfgManager:  cfgManager,
				Runner:      autoDiscoveryRunner,
				SyncService: newApiSyncService,
			})
			// 本地 Runtime API
			autopilot.RegisterLocalRuntimeRoutes(apiGroup, autopilotManager.LocalRuntimeStore())
			// 手动意图 API
			autopilot.RegisterManualIntentRoutes(apiGroup, autopilotManager.ManualIntentStore())
			// 本地任务模板 API
			autopilot.RegisterTaskTemplateRoutes(apiGroup, autopilotManager.TaskTemplateStore())
			// 驾驶舱只读聚合 API
			autopilot.RegisterCockpitRoutes(apiGroup, autopilotManager)
			// Advisor shadow 决策记录 API
			autopilot.RegisterAdvisorRoutes(apiGroup, autopilotManager.AdvisorDecisionStore())
			// Phase 4 Item 4: 渠道推荐只读 API
			autopilot.RegisterRecommendationRoutes(apiGroup, autopilotManager)

			// Phase 4 Item 8: A/B 测试结果 + 紧急停止 API
			if autopilotManager.ABTestSampler() != nil && autopilotManager.ABTestStore() != nil {
				autopilot.RegisterABTestRoutes(apiGroup, &autopilot.ABTestDeps{
					Sampler:    autopilotManager.ABTestSampler(),
					Store:      autopilotManager.ABTestStore(),
					CfgManager: cfgManager,
				})
			}
			// 路由追踪 API
			if autopilotManager.TraceStore() != nil {
				autopilot.RegisterTraceRoutes(apiGroup, autopilotManager.TraceStore())
			}
			// SmartRouter dry-run API
			if autopilotManager.SmartRouter() != nil {
				autopilot.RegisterDryRunRoutes(apiGroup, autopilotManager.SmartRouter())
			}

			// 渠道兼容性能力记忆查看/清除（工具调用、安全分类等自学习结论）
			handlers.RegisterCompatCacheRoutes(apiGroup)

			// Phase 2 第三批：自动托管 API（复用上方已构造的 autoDiscoveryRunner）
			autopilot.RegisterAutoManagedRoutes(apiGroup, &autopilot.AutoManagedDeps{
				CfgManager:          cfgManager,
				Runner:              autoDiscoveryRunner,
				RateLimitDiscoverer: autopilotManager.RateLimitDiscoverer(),
				ResetChannelMetrics: func(kind string, index int) {
					switch scheduler.ChannelKind(kind) {
					case scheduler.ChannelKindMessages, scheduler.ChannelKindChat, scheduler.ChannelKindResponses,
						scheduler.ChannelKindGemini, scheduler.ChannelKindImages, scheduler.ChannelKindVectors:
						channelScheduler.ResetChannelMetrics(index, scheduler.ChannelKind(kind))
					}
				},
			})

			// Phase 2 第三批：智能路由配置 API
			autopilot.RegisterRoutingConfigRoutes(apiGroup, &autopilot.RoutingConfigDeps{
				CfgManager: cfgManager,
				TraceStore: autopilotManager.TraceStore(),
			})
		}

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

		// Phase 4 Item 2: 成本报表 API（按 user/model/key 分组聚合）
		apiGroup.GET("/reports/cost", handlers.GetCostReport(&handlers.CostReportDeps{
			MetricsManagers: map[string]*metrics.MetricsManager{
				"messages":  messagesMetricsManager,
				"responses": responsesMetricsManager,
				"chat":      chatMetricsManager,
				"gemini":    geminiMetricsManager,
				"images":    imagesMetricsManager,
				"vectors":   vectorsMetricsManager,
			},
		}))
		// Phase 4 Item 5: 批量渠道管理 API（导入/导出/模板）
		apiGroup.POST("/channels/export", handlers.ExportChannels(envCfg, cfgManager))
		apiGroup.GET("/channels/export", handlers.ExportAllChannels(envCfg, cfgManager))
		apiGroup.POST("/channels/import", handlers.ImportChannels(cfgManager))
		apiGroup.POST("/channels/import/confirm", handlers.ImportChannelsConfirm(cfgManager))
		apiGroup.GET("/channels/templates", handlers.GetChannelTemplates())
		apiGroup.GET("/channels/provider-templates", handlers.GetProviderTemplates())
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
	// Phase 4 Item 7: 注入本地任务模板到 gin.Context（供 compact 层查询模板，nil 时使用默认提示词）
	compactWithTemplates := func(c *gin.Context) {
		if autopilotManager != nil && autopilotManager.TaskTemplateStore() != nil {
			autopilot.SetTaskTemplateStore(c, autopilotManager.TaskTemplateStore())
		}
		compactHandler(c)
	}
	r.POST("/v1/responses/compact", compactWithTemplates)
	r.POST("/:routePrefix/v1/responses/compact", compactWithTemplates)

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

		// 停止远程预置更新器（取消进行中的 HTTP 请求并等待 worker 退出）。
		presetUpdater.Stop()
		log.Println("[PresetUpdater-Shutdown] 预置更新器已安全关闭")

		// 停止渠道保活验证（等待 worker 池排空后再关指标存储）
		if healthCheckManager != nil {
			healthCheckManager.Stop()
			log.Println("[HealthCheck-Shutdown] 渠道保活验证已安全关闭")
		}

		// 停止后台 discovery runner：取消未完成任务并停止 GC。
		// 已持久化的 running 状态与 checkpoint 保留，下次启动经 ResumeIncompleteDiscoveries 续传。
		if autoDiscoveryRunner != nil {
			autoDiscoveryRunner.Stop()
			log.Println("[AutoDiscovery-Shutdown] 后台发现执行器已停止")
		}

		// 停止 new-api 订阅周期性余额/倍率同步。
		if newApiSyncService != nil {
			newApiSyncService.Stop()
			log.Println("[NewApiSubscriptionSync-Shutdown] 周期性同步已停止")
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

		// 停止 Autopilot 健康中心（flush 画像 + 关闭 SQLite）
		if autopilotManager != nil {
			if err := autopilotManager.Close(); err != nil {
				log.Printf("[Autopilot-Shutdown] 警告: 关闭健康中心时发生错误: %v", err)
			} else {
				log.Println("[Autopilot-Shutdown] 健康中心已安全关闭")
			}
		}

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
