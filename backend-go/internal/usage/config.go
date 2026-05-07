package usage

import (
	"os"
	"strconv"
	"time"
)

// 默认配置常量。
const (
	// DefaultDir NDJSON 输出目录（相对于工作目录或绝对路径均可）。
	DefaultDir = "logs/usage"

	// DefaultRetentionDays 保留 N 天前的 NDJSON 文件，启动时清理超期文件。
	DefaultRetentionDays = 30

	// DefaultFlushInterval 后台 flush 周期，亦用于检查日期切分。
	DefaultFlushInterval = 1 * time.Second

	// EnvRetentionDays 覆盖保留期的环境变量。
	EnvRetentionDays = "CCX_USAGE_RETENTION_DAYS"

	// EnvDir 覆盖输出目录的环境变量。
	EnvDir = "CCX_USAGE_DIR"
)

// Config NDJSONStore 配置参数。
type Config struct {
	// Dir NDJSON 输出目录。空时落 DefaultDir。
	Dir string

	// RetentionDays 保留天数；<= 0 视为不清理（仅在测试中有用，生产建议 30）。
	RetentionDays int

	// FlushInterval 后台 flush 周期。<= 0 时回退 DefaultFlushInterval。
	FlushInterval time.Duration
}

// DefaultConfig 返回默认配置；从环境变量读取覆盖项。
//
// 当前支持的覆盖：
//   - CCX_USAGE_DIR             覆盖输出目录
//   - CCX_USAGE_RETENTION_DAYS  覆盖保留天数（必须为正整数，否则忽略）
func DefaultConfig() Config {
	cfg := Config{
		Dir:           DefaultDir,
		RetentionDays: DefaultRetentionDays,
		FlushInterval: DefaultFlushInterval,
	}
	if v := os.Getenv(EnvDir); v != "" {
		cfg.Dir = v
	}
	if v := os.Getenv(EnvRetentionDays); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.RetentionDays = n
		}
	}
	return cfg
}
