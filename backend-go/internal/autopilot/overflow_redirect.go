package autopilot

import (
	"context"

	"github.com/BenedictKing/ccx/internal/config"
)

// 溢出跨模型重定向（逃生阀兜底层）。
//
// 触发条件：请求输入超过所有同模型候选的已知窗口（含试探档），选路以
// ContextCapacityError 失败。此前该请求只会得到 400 context_length_exceeded；
// 现在 failover 外壳会先询问本 provider：池内是否存在有效窗口能承载的替代模型。
//
// 选型口径（用户拍板"全池按质量档自动选"）：在请求协议的渠道池内枚举全部
// 画像，按有效窗口 ≥ 输入 + 探测成功过滤，质量档降序取最优。协议转换惩罚
// 与最终渠道选择仍由既有评分链路完成——这里只决定"改成哪个模型"。
// 替代模型发送时剥离历史 encrypted_content（跨模型不可解密），由发送层统一处理。

// OverflowRedirectModel 返回可承载 inputTokens 的替代模型（全池按质量档）。
// 返回 ok=false 表示池内无合适替代（调用方回退到 400 语义）。
func (m *Manager) OverflowRedirectModel(ctx context.Context, channelKind, requestModel string, inputTokens int) (string, bool) {
	if m == nil || m.modelProfileStore == nil || inputTokens <= 0 {
		return "", false
	}
	upstreams := upstreamsForKind(m.cfgManager, channelKind)
	if len(upstreams) == 0 {
		return "", false
	}

	type redirectCandidate struct {
		model string
		tier  QualityTier
	}
	best := redirectCandidate{}
	seen := make(map[string]bool)
	for _, upstream := range upstreams {
		if upstream.ChannelUID == "" {
			continue
		}
		for _, p := range m.modelProfileStore.ListActiveByChannel(upstream.ChannelUID) {
			if p.ChannelKind != channelKind || !p.ProbeSuccess || seen[p.ModelID] {
				continue
			}
			// 同名模型刚被证明装不下，重定向到它没有意义
			if p.ModelID == requestModel {
				continue
			}
			seen[p.ModelID] = true
			window := effectiveContextWindow(p.ChannelUID, p.ChannelKind, p.ModelID, p.ContextTokens)
			if window < inputTokens {
				continue
			}
			if best.model == "" || qualityTierRank(p.QualityTier) > qualityTierRank(best.tier) {
				best = redirectCandidate{model: p.ModelID, tier: p.QualityTier}
			}
		}
	}
	if best.model == "" {
		return "", false
	}
	return best.model, true
}

// upstreamsForKind 取该协议的渠道配置列表。
func upstreamsForKind(cfgManager *config.ConfigManager, channelKind string) []config.UpstreamConfig {
	if cfgManager == nil {
		return nil
	}
	cfg := cfgManager.GetConfig()
	switch channelKind {
	case "responses":
		return cfg.ResponsesUpstream
	case "chat":
		return cfg.ChatUpstream
	case "messages":
		return cfg.Upstream
	case "gemini":
		return cfg.GeminiUpstream
	default:
		return nil
	}
}
