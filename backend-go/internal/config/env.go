package config

import (
	"crypto/subtle"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type EnvConfig struct {
	Port                 int
	BindHost             string
	Env                  string
	EnableHTTPS          bool
	TLSCertFile          string
	TLSKeyFile           string
	TLSAutoCert          bool
	EnableWebUI          bool
	UILanguage           string
	ProxyAccessKey       string
	ExtraProxyAccessKeys []string // 额外代理访问密钥（仅用于代理 API，不授予管理权限）
	AdminAccessKey       string   // 管理 API 独立密钥（可选，未设置时回退到 ProxyAccessKey）
	LogLevel             string
	EnableRequestLogs    bool
	EnableResponseLogs   bool
	QuietPollingLogs     bool   // 静默轮询端点日志
	RawLogOutput         bool   // 原始日志输出（不缩进、不截断、不重排序）
	SSEDebugLevel        string // SSE 调试级别: off, summary, full
	RewriteResponseModel bool   // 是否改写响应中的 model 字段为请求的 model（默认 false）
	ServerReadTimeout    int    // HTTP 服务端读取请求超时（毫秒），仅用于入站连接读取

	RequestTimeout     int
	MaxRequestBodySize int64 // 请求体最大大小 (字节)，由 MB 配置转换
	EnableCORS         bool
	CORSOrigin         string
	// 指标配置
	MetricsWindowSize       int     // 滑动窗口大小
	MetricsFailureThreshold float64 // 失败率阈值
	// 指标持久化配置
	MetricsPersistenceEnabled bool // 是否启用 SQLite 持久化
	MetricsRetentionDays      int  // 数据保留天数（3-366）
	// HTTP 客户端配置
	ResponseHeaderTimeout int // 等待响应头超时时间（秒）
	// 驾驶舱 override 默认有效期
	OverrideTTLMinutes int // 驾驶舱 override 默认有效期（分钟，1-1440，默认 30）
	// 日志文件相关配置
	LogDir        string
	LogFile       string
	LogMaxSize    int  // 单个日志文件最大大小 (MB)
	LogMaxBackups int  // 保留的旧日志文件最大数量
	LogMaxAge     int  // 保留的旧日志文件最大天数
	LogCompress   bool // 是否压缩旧日志文件
	LogToConsole  bool // 是否同时输出到控制台
}

// NewEnvConfig 创建环境配置
func NewEnvConfig() *EnvConfig {
	// 支持 ENV 和 NODE_ENV（向后兼容）
	env := getEnv("ENV", "")
	if env == "" {
		env = getEnv("NODE_ENV", "development")
	}

	return &EnvConfig{
		Port:                 getEnvAsInt("PORT", 3000),
		BindHost:             strings.TrimSpace(getEnv("BIND_HOST", "")),
		Env:                  env,
		EnableHTTPS:          getEnv("ENABLE_HTTPS", "false") == "true",
		TLSCertFile:          getEnv("TLS_CERT_FILE", ""),
		TLSKeyFile:           getEnv("TLS_KEY_FILE", ""),
		TLSAutoCert:          getEnv("TLS_AUTO_CERT", "true") != "false",
		EnableWebUI:          getEnv("ENABLE_WEB_UI", "true") != "false",
		UILanguage:           normalizeUILanguage(getEnv("APP_UI_LANGUAGE", "zh-CN")),
		ProxyAccessKey:       getEnv("PROXY_ACCESS_KEY", "your-proxy-access-key"),
		ExtraProxyAccessKeys: parseExtraProxyAccessKeys(getEnv("EXTRA_PROXY_ACCESS_KEYS", "")),
		AdminAccessKey:       getEnv("ADMIN_ACCESS_KEY", ""), // 空值时回退到 ProxyAccessKey
		LogLevel:             getEnv("LOG_LEVEL", "info"),
		EnableRequestLogs:    getEnv("ENABLE_REQUEST_LOGS", "true") != "false",
		EnableResponseLogs:   getEnv("ENABLE_RESPONSE_LOGS", "true") != "false",
		QuietPollingLogs:     getEnv("QUIET_POLLING_LOGS", "true") != "false",
		RawLogOutput:         getEnv("RAW_LOG_OUTPUT", "false") == "true",
		SSEDebugLevel:        getEnv("SSE_DEBUG_LEVEL", "off"),
		RewriteResponseModel: getEnv("REWRITE_RESPONSE_MODEL", "false") == "true",
		ServerReadTimeout:    clampInt(getEnvAsInt("SERVER_READ_TIMEOUT", 60000), 10000, 300000),

		RequestTimeout:     clampInt(getEnvAsInt("REQUEST_TIMEOUT", 120000), MinRequestTimeoutMs, MaxRequestTimeoutMs),
		MaxRequestBodySize: getEnvAsInt64("MAX_REQUEST_BODY_SIZE_MB", 50) * 1024 * 1024, // MB 转换为字节
		EnableCORS:         getEnv("ENABLE_CORS", "false") == "true",
		CORSOrigin:         getEnv("CORS_ORIGIN", "*"),
		// 指标配置
		MetricsWindowSize:       getEnvAsInt("METRICS_WINDOW_SIZE", 10),
		MetricsFailureThreshold: getEnvAsFloat("METRICS_FAILURE_THRESHOLD", 0.5),
		// 指标持久化配置
		MetricsPersistenceEnabled: getEnv("METRICS_PERSISTENCE_ENABLED", "true") != "false",
		MetricsRetentionDays:      clampInt(getEnvAsInt("METRICS_RETENTION_DAYS", 366), 3, 366),
		// HTTP 客户端配置
		ResponseHeaderTimeout: clampInt(getEnvAsInt("RESPONSE_HEADER_TIMEOUT", 60), 30, 120), // 30-120 秒
		// 驾驶舱 override 默认有效期
		OverrideTTLMinutes: clampInt(getEnvAsInt("OVERRIDE_TTL_MINUTES", 30), 1, 1440),
		// 日志文件配置
		LogDir:        getEnv("LOG_DIR", "logs"),
		LogFile:       getEnv("LOG_FILE", "app.log"),
		LogMaxSize:    getEnvAsInt("LOG_MAX_SIZE", 100),   // 默认 100MB
		LogMaxBackups: getEnvAsInt("LOG_MAX_BACKUPS", 10), // 默认保留 10 个
		LogMaxAge:     getEnvAsInt("LOG_MAX_AGE", 30),     // 默认保留 30 天
		LogCompress:   getEnv("LOG_COMPRESS", "true") != "false",
		LogToConsole:  getEnv("LOG_TO_CONSOLE", "true") != "false",
	}
}

func normalizeUILanguage(value string) string {
	switch value {
	case "en", "EN", "en-US", "en-us":
		return "en"
	case "id", "ID", "id-ID", "id-id":
		return "id"
	case "zh", "ZH", "zh-CN", "zh-cn":
		return "zh-CN"
	default:
		return "en"
	}
}

func parseExtraProxyAccessKeys(value string) []string {
	if value == "" {
		return nil
	}

	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		key := strings.TrimSpace(part)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}

	return keys
}

func secureCompare(a, b string) bool {
	if a == "" || b == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// IsDevelopment 是否为开发环境
func (c *EnvConfig) IsDevelopment() bool {
	return c.Env == "development"
}

// HasExtraProxyAccessKeys 是否配置了额外代理访问密钥
func (c *EnvConfig) HasExtraProxyAccessKeys() bool {
	return len(c.ExtraProxyAccessKeys) > 0
}

// ValidateAccessKeys 校验访问密钥配置的安全边界
func (c *EnvConfig) ValidateAccessKeys() error {
	if !c.HasExtraProxyAccessKeys() {
		return nil
	}

	if c.AdminAccessKey == "" {
		return fmt.Errorf("EXTRA_PROXY_ACCESS_KEYS requires ADMIN_ACCESS_KEY")
	}
	if secureCompare(c.AdminAccessKey, c.ProxyAccessKey) {
		return fmt.Errorf("ADMIN_ACCESS_KEY must differ from PROXY_ACCESS_KEY when EXTRA_PROXY_ACCESS_KEYS is set")
	}
	for _, key := range c.ExtraProxyAccessKeys {
		if key == "your-proxy-access-key" {
			return fmt.Errorf("EXTRA_PROXY_ACCESS_KEYS must not contain the default placeholder key")
		}
		if secureCompare(key, c.ProxyAccessKey) {
			return fmt.Errorf("EXTRA_PROXY_ACCESS_KEYS must differ from PROXY_ACCESS_KEY")
		}
		if secureCompare(c.AdminAccessKey, key) {
			return fmt.Errorf("ADMIN_ACCESS_KEY must differ from EXTRA_PROXY_ACCESS_KEYS when extra keys are set")
		}
	}

	return nil
}

// IsValidProxyAccessKey 判断是否为有效代理访问密钥
func (c *EnvConfig) IsValidProxyAccessKey(providedKey string) bool {
	if providedKey == "" {
		return false
	}
	if secureCompare(providedKey, c.ProxyAccessKey) {
		return true
	}
	for _, key := range c.ExtraProxyAccessKeys {
		if secureCompare(providedKey, key) {
			return true
		}
	}
	return false
}

// IsValidAdminAccessKey 判断是否为有效管理访问密钥
func (c *EnvConfig) IsValidAdminAccessKey(providedKey string) bool {
	if providedKey == "" {
		return false
	}
	if c.AdminAccessKey != "" {
		return secureCompare(providedKey, c.AdminAccessKey)
	}
	if c.HasExtraProxyAccessKeys() {
		return false
	}
	return secureCompare(providedKey, c.ProxyAccessKey)
}

// GetAdminAccessKey 获取显式或回退管理 API 密钥，认证判断请使用 IsValidAdminAccessKey
func (c *EnvConfig) GetAdminAccessKey() string {
	if c.AdminAccessKey != "" {
		return c.AdminAccessKey
	}
	return c.ProxyAccessKey
}

// IsProduction 是否为生产环境
func (c *EnvConfig) IsProduction() bool {
	return c.Env == "production"
}

// ShouldLog 是否应该记录日志
func (c *EnvConfig) ShouldLog(level string) bool {
	levels := map[string]int{
		"error": 0,
		"warn":  1,
		"info":  2,
		"debug": 3,
	}

	currentLevel, ok := levels[c.LogLevel]
	if !ok {
		currentLevel = 2 // 默认 info
	}

	requestLevel, ok := levels[level]
	if !ok {
		return false
	}

	return requestLevel <= currentLevel
}

// getEnv 获取环境变量，如果不存在则返回默认值
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt 获取环境变量并转换为整数
func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// getEnvAsInt64 获取环境变量并转换为 int64
func getEnvAsInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// getEnvAsFloat 获取环境变量并转换为浮点数
func getEnvAsFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
	}
	return defaultValue
}

// clampInt 将整数限制在指定范围内
func clampInt(value, minVal, maxVal int) int {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}
