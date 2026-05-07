// Package middleware 承载 ccx 自家 key 级失败处理逻辑（BlacklistKey /
// MarkKeyAsFailedWithDuration / MatchPauseRule / channel.failoverRule）的
// pipeline.Middleware 实现。
//
// PR3 T5 的工作单元：把 internal/handlers/common/upstream_failover.go
// 第 354-595 行的循环式 key 处理搬到 RawResponse hook 中，由 pipeline
// 主循环（pipeline.Process）统一驱动 retry / channel switch。
//
// 旧的 upstream_failover.go 在 T8 切流量后才会被删除，故这里不直接
// 复用旧文件的内部符号，而是通过 internal/handlers/common 暴露的
// ShouldBlacklistKey / ShouldRetryWithNextKey / MatchChannelFailoverRule
// 公开 API 调用。
package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/pipeline"
	"github.com/BenedictKing/ccx/internal/utils"
)

// ErrRetryNextKey 表示当前 attempt 失败但可由 pipeline 主循环
// 经由 Outbound.Retryable / ChannelRetryable 切换到下一个 key/channel。
//
// CCXKeyFailureMiddleware 与 CCXPauseRuleMiddleware 在做完冷却/拉黑标记
// 之后返回此错误，pipeline.Process 将之视为可重试错误。
var ErrRetryNextKey = errors.New("ccx middleware: retry with next key")

// AttemptInfo 携带本次 attempt 中 middleware 需要看到的 channel/key 关联信息。
//
// PR3 T5/T8 迁移期间的契约：
//   - handler（T8）在调用 pipeline.Process 之前通过 WithAttemptInfo 把 *AttemptInfo
//     注入 ctx；
//   - Outbound.NextChannel 在切换 channel/key 时，原地更新 *AttemptInfo 字段；
//   - 这里的 middleware 只对字段做读访问。
//
// 之所以使用 *AttemptInfo 而非按值传递：context 不可变，但每次 attempt 切换
// channel/key 时都需要更新；让 outbound 直接原地改写指针指向的结构体即可被
// 后续 RawResponse 调用看到最新值。
type AttemptInfo struct {
	// APIType：messages / chat / responses / gemini，用于日志标签和 cfg 缓存键隔离。
	APIType string
	// ChannelIndex：上游 channel 的索引（与现有 BlacklistKey 第 2 参数语义一致）。
	ChannelIndex int
	// APIKey：当前 attempt 选中的明文上游 API key。
	APIKey string
	// Upstream：当前 attempt 选中的 *config.UpstreamConfig，用于读取 failoverRule
	// 与 IsAutoBlacklistBalanceEnabled 等渠道级配置。
	Upstream *config.UpstreamConfig
}

type attemptInfoCtxKey struct{}

// WithAttemptInfo 把 *AttemptInfo 挂到 ctx 上；info 为 nil 时直接返回原 ctx。
func WithAttemptInfo(ctx context.Context, info *AttemptInfo) context.Context {
	if info == nil {
		return ctx
	}
	return context.WithValue(ctx, attemptInfoCtxKey{}, info)
}

// AttemptInfoFromContext 从 ctx 取出 *AttemptInfo；不存在或类型不匹配返回 nil。
func AttemptInfoFromContext(ctx context.Context) *AttemptInfo {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(attemptInfoCtxKey{}).(*AttemptInfo)
	return v
}

// keyFailureConfig 抽象 ConfigManager 中 CCXKeyFailureMiddleware 实际依赖的方法集。
//
// 用接口而非具体类型主要为了便于 unit test 注入 mock；*config.ConfigManager
// 通过结构性子类型自动满足该接口，无需外部适配。
type keyFailureConfig interface {
	MarkKeyAsFailedWithDuration(apiKey, apiType string, duration time.Duration)
	BlacklistKey(apiType string, channelIndex int, apiKey string, reason, message string) error
	GetFuzzyModeEnabled() bool
}

// CCXKeyFailureMiddleware 把 ccx 现有 key 级 failover 逻辑落到 RawResponse hook，
// 等价替换 upstream_failover.go:354-473 行的状态码处理段。
//
// 行为顺序（与既有 handlers 路径保持一致）：
//
//  1. status 在 [200,300) → 透传；
//  2. 429：解析 Retry-After 头，调用 MarkKeyAsFailedWithDuration 冷却当前 key，
//     返回 ErrRetryNextKey；
//  3. channel.failoverRule 命中（status_code/error_code/keyword）：
//     - cooldown action → MarkKeyAsFailedWithDuration(rule.Duration)
//     - blacklist action → BlacklistKey
//     - 都返回 ErrRetryNextKey；
//  4. ShouldBlacklistKey 命中（authentication/permission/insufficient_balance/rate_limit）→
//     BlacklistKey（其中 insufficient_balance 受 upstream.IsAutoBlacklistBalanceEnabled 门控），
//     再走 step 5；
//  5. ShouldRetryWithNextKey 判定 retry：是 → 返回 ErrRetryNextKey；否 → 透传给客户端。
//
// resp.Body 契约：读完后用 io.NopCloser(bytes.NewReader(body)) 回填，
// 并清除 Content-Encoding/Content-Length 头，使后续 middleware 与
// outbound transformer 能再次直接读取已解压的字节。
type CCXKeyFailureMiddleware struct {
	pipeline.BaseMiddleware
	cfg keyFailureConfig
}

// NewCCXKeyFailureMiddleware 构造 middleware；cfg 不可为 nil。
func NewCCXKeyFailureMiddleware(cfg keyFailureConfig) *CCXKeyFailureMiddleware {
	if cfg == nil {
		panic("middleware.NewCCXKeyFailureMiddleware: cfg must not be nil")
	}
	return &CCXKeyFailureMiddleware{cfg: cfg}
}

// RawResponse 实现 pipeline.Middleware.RawResponse。
func (m *CCXKeyFailureMiddleware) RawResponse(ctx context.Context, resp *http.Response) (*http.Response, error) {
	if resp == nil {
		return resp, nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}

	info := AttemptInfoFromContext(ctx)
	if info == nil || info.APIKey == "" {
		// 没有 attempt info 就无法定位 channel/key；保持透传，错误交给上层处理。
		return resp, nil
	}

	body := readAndRestoreBody(resp)

	// 1. 429 → Retry-After cooldown
	if resp.StatusCode == http.StatusTooManyRequests {
		duration := parseRetryAfter(resp.Header.Get("Retry-After"))
		if duration <= 0 {
			duration = 60 * time.Minute
		}
		m.cfg.MarkKeyAsFailedWithDuration(info.APIKey, info.APIType, duration)
		log.Printf("[%s-Middleware-RateLimit] 429 Retry-After 命中: channel=[%d] key=%s duration=%s",
			info.APIType, info.ChannelIndex, utils.MaskAPIKey(info.APIKey), duration)
		return resp, ErrRetryNextKey
	}

	// 2. channel.failoverRule
	if dec := common.MatchChannelFailoverRule(info.Upstream, resp.StatusCode, body, "", "", ""); dec.Matched {
		switch dec.Action {
		case common.FailoverActionBlacklist:
			if info.Upstream != nil {
				if err := m.cfg.BlacklistKey(info.APIType, info.ChannelIndex, info.APIKey, dec.Reason, dec.Message); err != nil {
					log.Printf("[%s-Middleware-Blacklist] 拉黑 key 失败: %v", info.APIType, err)
				}
			}
		case common.FailoverActionCooldown:
			duration := dec.Duration
			if duration <= 0 {
				duration = 60 * time.Minute
			}
			m.cfg.MarkKeyAsFailedWithDuration(info.APIKey, info.APIType, duration)
		}
		log.Printf("[%s-Middleware-Failover-Rule] 命中规则: channel=[%d] key=%s action=%s desc=%s",
			info.APIType, info.ChannelIndex, utils.MaskAPIKey(info.APIKey), dec.Action, dec.Description)
		return resp, ErrRetryNextKey
	}

	// 3. ShouldBlacklistKey 默认判定
	bl := common.ShouldBlacklistKey(resp.StatusCode, body)
	if bl.ShouldBlacklist {
		apply := true
		if bl.Reason == "insufficient_balance" {
			if info.Upstream == nil || !info.Upstream.IsAutoBlacklistBalanceEnabled() {
				apply = false
			}
		}
		// rate_limit 类型只冷却不拉黑（与 upstream_failover.go:546-548 对齐）
		if bl.Reason == "rate_limit" {
			apply = false
		}
		if apply && info.Upstream != nil {
			if err := m.cfg.BlacklistKey(info.APIType, info.ChannelIndex, info.APIKey, bl.Reason, bl.Message); err != nil {
				log.Printf("[%s-Middleware-Blacklist] 拉黑 key 失败: %v", info.APIType, err)
			}
		}
	}

	// 4. ShouldRetryWithNextKey 决定是否触发 retry
	shouldRetry, _ := common.ShouldRetryWithNextKey(resp.StatusCode, body, m.cfg.GetFuzzyModeEnabled(), info.APIType)
	if shouldRetry {
		log.Printf("[%s-Middleware-Key] 状态码 %d 触发换 key: channel=[%d] key=%s",
			info.APIType, resp.StatusCode, info.ChannelIndex, utils.MaskAPIKey(info.APIKey))
		return resp, ErrRetryNextKey
	}

	// 4xx 客户端错误且不应 retry → 透传给 inbound 写客户端。
	return resp, nil
}

// readAndRestoreBody 读完 resp.Body，按需 gzip 解压，再用 NopCloser 回填到
// resp.Body。同时清除 Content-Encoding 与 Content-Length，保证下一个 middleware
// 或 outbound transformer 直接拿到的是明文字节。
//
// 任何 IO 错误时返回空 body 并保留一个空 reader，避免 nil panic。
func readAndRestoreBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return nil
	}
	body := utils.DecompressGzipIfNeeded(resp, raw)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if resp.Header != nil {
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
	}
	resp.ContentLength = int64(len(body))
	return body
}

// parseRetryAfter 解析 HTTP Retry-After 头，支持秒数与 HTTP-date 两种形式。
//
// 解析失败 / 头缺失 / 负值 → 返回 0，由调用方决定 fallback。
func parseRetryAfter(value string) time.Duration {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
