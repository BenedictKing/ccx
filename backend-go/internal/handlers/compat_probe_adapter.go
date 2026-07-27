package handlers

import (
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
)

// compatProbeTraitMap 把渠道诊断的推荐项名映射到可学习的 trait。
// 只收录"没有上游报错信号、只能靠观察响应内容判断"的兼容项：
// 有明确 400 信号的项目走 compat_signal.go 的错误驱动路径，不需要消耗一次探测调用。
var compatProbeTraitMap = map[string]config.CompatTrait{
	"passbackReasoningContent": config.TraitPassbackReasoningContent,
	"stripEmptyTextBlocks":     config.TraitStripEmptyTextBlocks,
	"passbackThinkingBlocks":   config.TraitPassbackThinkingBlocks,
}

// RegisterCompatProbeHook 把渠道兼容性探测接到运行时自动学习链路。
// 复用 DiagnoseChannelCompat 已验证的探测与判定逻辑，只是触发方式从"用户点按钮"
// 改为"首次遇到该 渠道-Key-模型 组合时自动跑一次"。
func RegisterCompatProbeHook() {
	common.SetCompatProbeHook(func(upstream *config.UpstreamConfig, apiKey, baseURL, model string) map[config.CompatTrait]bool {
		if upstream == nil || apiKey == "" || baseURL == "" {
			return nil
		}

		channelKind := upstream.ServiceType
		if channelKind == "claude" {
			channelKind = "messages"
		}
		result := runCompatDiagnoseWithProbeModel(upstream, channelKind, apiKey, baseURL, model)
		if len(result.Recommendations) == 0 {
			return nil
		}

		traits := make(map[config.CompatTrait]bool, len(compatProbeTraitMap))
		for recName, trait := range compatProbeTraitMap {
			if enabled, ok := result.Recommendations[recName]; ok {
				traits[trait] = enabled
			}
		}
		return traits
	})
}
