package common

import "github.com/BenedictKing/ccx/internal/config"

// 公开常量与类型，供 internal/pipeline/middleware 复用 channel failover 规则匹配。
//
// 这些是对 failover_rules.go 中内部符号的别名导出，仅为新 middleware（PR3 T5）
// 引用而存在；不影响 handlers/common 既有调用，也不修改原函数签名。
const (
	// FailoverActionCooldown 是 failoverActionCooldown 的公开别名（值："cooldown"）。
	FailoverActionCooldown = failoverActionCooldown
	// FailoverActionBlacklist 是 failoverActionBlacklist 的公开别名（值："blacklist"）。
	FailoverActionBlacklist = failoverActionBlacklist
)

// ChannelFailoverDecision 是 channelFailoverDecision 的公开类型别名；
// 字段已是导出的（Matched/Action/Description/Duration/Reason/Message/IsQuotaRelated）。
type ChannelFailoverDecision = channelFailoverDecision

// MatchChannelFailoverRule 是 matchChannelFailoverRule 的公开包装；签名与语义保持一致。
//
// 提供给 internal/pipeline/middleware.CCXKeyFailureMiddleware 使用。
func MatchChannelFailoverRule(
	upstream *config.UpstreamConfig,
	statusCode int,
	body []byte,
	errCode string,
	errType string,
	errMessage string,
) ChannelFailoverDecision {
	return matchChannelFailoverRule(upstream, statusCode, body, errCode, errType, errMessage)
}
