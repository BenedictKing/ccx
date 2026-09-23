package common

import (
	"github.com/BenedictKing/ccx/internal/config"
)

// HandleBalanceClassKeyFailure 处理余额/配额类 Key 失败的组合级降级。
//
// 余额/配额错误（402、余额文案等）可能是按模型分池计费导致的单模型不可用，
// 不能仅凭一次失败就整 Key 拉黑：先写入 (Key, 模型) 组合级限制（1h 自动恢复），
// 保留该 Key 对其他模型的调度；仅当限制已覆盖该渠道全部模型（精确白名单判定）
// 或累计不同模型数达到阈值时，才升级为整 Key 拉黑（沿用跨协议级联与自动恢复）。
//
// 返回是否已升级为整 Key 拉黑。调用方需自行记录日志；上游给出的 recoverAt
// 仅在升级拉黑时透传（组合限制固定 1h 恢复，与 DisableKeyModel 语义一致）。
func HandleBalanceClassKeyFailure(cfgManager *config.ConfigManager, upstream *config.UpstreamConfig,
	apiType string, channelIndex int, apiKey, model, reason, message, recoverAt string) (escalated bool) {

	if cfgManager == nil || upstream == nil || apiKey == "" || model == "" {
		return false
	}
	// 开关关闭=余额类不做任何自动处置，语义与旧的"不自动拉黑"保持一致。
	if !upstream.IsAutoBlacklistBalanceEnabled() {
		return false
	}
	if err := cfgManager.DisableKeyModel(apiType, channelIndex, apiKey, model, reason, message); err != nil {
		return false
	}
	if !cfgManager.ShouldEscalateBalanceKeyBlacklist(apiType, channelIndex, apiKey) {
		return false
	}
	if err := cfgManager.BlacklistKeyWithRecoverAt(apiType, channelIndex, apiKey, reason, message, recoverAt); err != nil {
		return false
	}
	return true
}
