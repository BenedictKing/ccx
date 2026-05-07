package middleware

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/pipeline"
	"github.com/BenedictKing/ccx/internal/utils"
)

// pauseRuleConfig 抽象 ConfigManager 中 CCXPauseRuleMiddleware 实际依赖的方法集，
// 便于测试注入 mock；*config.ConfigManager 自动满足。
type pauseRuleConfig interface {
	MatchPauseRule(statusCode int, body []byte) *config.PauseRule
	MarkKeyAsFailedWithDuration(apiKey, apiType string, duration time.Duration)
}

// CCXPauseRuleMiddleware 把全局 PauseRule（status + keyword 关键词匹配）落到
// RawResponse hook，等价替换 upstream_failover.go:360-374 行的处理段。
//
// 注：ccx 现有 PauseRule 的实际作用对象是 KEY 而非 channel（命中后调用
// MarkKeyAsFailedWithDuration 在 failedKeysCache 上写入 FixedDuration），
// 与 PRD 中 "PauseChannel" 的措辞略有出入；这里保持现有 KEY 级语义，避免
// 与既有 config_pause_rules_test.go 等测试相互冲突。
//
// 注册顺序建议：注册在 CCXKeyFailureMiddleware **之前**，与既有 handlers
// 顺序一致（PauseRule 优先于 channel.failoverRule）。
//
// resp.Body 契约：与 CCXKeyFailureMiddleware 一致 —— 读完后通过
// readAndRestoreBody 回填，下游可继续读取已解压的字节。
type CCXPauseRuleMiddleware struct {
	pipeline.BaseMiddleware
	cfg pauseRuleConfig
}

// NewCCXPauseRuleMiddleware 构造 middleware；cfg 不可为 nil。
func NewCCXPauseRuleMiddleware(cfg pauseRuleConfig) *CCXPauseRuleMiddleware {
	if cfg == nil {
		panic("middleware.NewCCXPauseRuleMiddleware: cfg must not be nil")
	}
	return &CCXPauseRuleMiddleware{cfg: cfg}
}

// RawResponse 实现 pipeline.Middleware.RawResponse。
func (m *CCXPauseRuleMiddleware) RawResponse(ctx context.Context, resp *http.Response) (*http.Response, error) {
	if resp == nil {
		return resp, nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}

	info := AttemptInfoFromContext(ctx)
	if info == nil || info.APIKey == "" {
		return resp, nil
	}

	body := readAndRestoreBody(resp)

	rule := m.cfg.MatchPauseRule(resp.StatusCode, body)
	if rule == nil {
		return resp, nil
	}

	duration := time.Duration(rule.DurationMinutes) * time.Minute
	if duration <= 0 {
		duration = 60 * time.Minute
	}
	m.cfg.MarkKeyAsFailedWithDuration(info.APIKey, info.APIType, duration)

	log.Printf("[%s-Middleware-PauseRule] 命中暂停规则: channel=[%d] key=%s status=%d duration=%s desc=%s",
		info.APIType, info.ChannelIndex, utils.MaskAPIKey(info.APIKey), resp.StatusCode, duration, rule.Description)

	return resp, ErrRetryNextKey
}
