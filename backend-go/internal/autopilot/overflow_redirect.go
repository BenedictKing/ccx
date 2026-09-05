package autopilot

import (
	"context"
	"sort"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// 溢出跨协议重定向（逃生阀兜底层，完全跨协议版）。
//
// 触发条件：请求输入超过请求协议内所有候选的已知窗口（含试探档），选路以
// ContextCapacityError 失败。调度器在容量错误分支询问本 provider，我们把
// 其他协议渠道上能承载该输入的模型作为候选注入（ChannelInfo 带 Route 与
// ActualModel），后续可用性/评分/优先级遍历与发送层协议转换全部复用既有机制
// （联邦 sibling 同款路径），实现 responses↔chat↔messages↔gemini 全池逃生。
//
// 选型口径（用户拍板"全池按质量档自动选"）：四类协议渠道池内枚举全部画像，
// 按 有效窗口 ≥ 输入 + 探测成功 过滤，同协议优先（避免不必要的转换损耗），
// 其后质量档降序。跨协议候选经发送层做协议转换，responses 直连的跨模型组合
// 由发送层统一剥离历史 encrypted_content。

// overflowRedirectFanout 注入候选数上限：够 failover 兜底即可，避免 trace 膨胀。
const overflowRedirectFanout = 4

// OverflowRedirectCandidates 返回可承载 inputTokens 的跨协议/跨模型重定向候选。
// 返回空切片表示无合适替代（调用方沿用容量错误语义）。
func (m *Manager) OverflowRedirectCandidates(ctx context.Context, requestKind scheduler.ChannelKind, requestModel string, inputTokens int) []scheduler.ChannelInfo {
	if m == nil || m.modelProfileStore == nil || inputTokens <= 0 || requestModel == "" {
		return nil
	}
	cfg := m.cfgManager.GetConfig()

	type protocolPool struct {
		kind     scheduler.ChannelKind
		upstream config.UpstreamConfig
		index    int
	}
	var pools []protocolPool
	for kind, upstreams := range map[scheduler.ChannelKind][]config.UpstreamConfig{
		scheduler.ChannelKindResponses: cfg.ResponsesUpstream,
		scheduler.ChannelKindChat:      cfg.ChatUpstream,
		scheduler.ChannelKindMessages:  cfg.Upstream,
		scheduler.ChannelKindGemini:    cfg.GeminiUpstream,
	} {
		for i := range upstreams {
			if upstreams[i].ChannelUID != "" {
				pools = append(pools, protocolPool{kind: kind, upstream: upstreams[i], index: i})
			}
		}
	}
	if len(pools) == 0 {
		return nil
	}

	type redirectCandidate struct {
		info   scheduler.ChannelInfo
		tier   QualityTier
		native bool
	}
	best := make([]redirectCandidate, 0, overflowRedirectFanout)
	seen := make(map[string]bool)
	for _, pool := range pools {
		for _, p := range m.modelProfileStore.ListActiveByChannel(pool.upstream.ChannelUID) {
			if !p.ProbeSuccess || seen[string(pool.kind)+"|"+p.ModelID] {
				continue
			}
			// 同名模型刚被证明装不下，重定向到它没有意义
			if p.ModelID == requestModel {
				continue
			}
			seen[string(pool.kind)+"|"+p.ModelID] = true
			window := effectiveContextWindow(p.ChannelUID, p.ChannelKind, p.ModelID, p.ContextTokens)
			if window < inputTokens {
				continue
			}
			best = append(best, redirectCandidate{
				info: scheduler.ChannelInfo{
					Route:            scheduler.ChannelRouteRef{Kind: string(pool.kind), Index: pool.index, ChannelUID: pool.upstream.ChannelUID},
					Index:            pool.index,
					Name:             pool.upstream.Name,
					Priority:         pool.index,
					Status:           pool.upstream.Status,
					ActualModel:      p.ModelID,
					OverflowRedirect: true,
				},
				tier:   p.QualityTier,
				native: pool.kind == requestKind,
			})
		}
	}
	if len(best) == 0 {
		return nil
	}

	// 同协议优先，其后质量档降序、名称稳定序兜底
	sort.SliceStable(best, func(i, j int) bool {
		if best[i].native != best[j].native {
			return best[i].native
		}
		if qualityTierRank(best[i].tier) != qualityTierRank(best[j].tier) {
			return qualityTierRank(best[i].tier) > qualityTierRank(best[j].tier)
		}
		return best[i].info.Name < best[j].info.Name
	})
	if len(best) > overflowRedirectFanout {
		best = best[:overflowRedirectFanout]
	}
	candidates := make([]scheduler.ChannelInfo, 0, len(best))
	for rank, c := range best {
		c.info.Priority = rank // 注入候选按选型名次排优先级
		candidates = append(candidates, c.info)
	}
	return candidates
}
