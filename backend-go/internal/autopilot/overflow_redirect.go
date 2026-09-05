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

	// 显式有序协议切片替换 map 遍历：请求协议优先，其后按固定协议顺序，
	// 保证对同一配置重复运行的结果顺序一致（map 遍历顺序随机，会让同名候选
	// 的取舍受哈希布局影响）。
	protocolOrder := []scheduler.ChannelKind{
		scheduler.ChannelKindResponses,
		scheduler.ChannelKindChat,
		scheduler.ChannelKindMessages,
		scheduler.ChannelKindGemini,
	}
	orderedKinds := make([]scheduler.ChannelKind, 0, len(protocolOrder))
	for _, k := range protocolOrder {
		if k == requestKind {
			orderedKinds = append(orderedKinds, k)
		}
	}
	for _, k := range protocolOrder {
		if k != requestKind {
			orderedKinds = append(orderedKinds, k)
		}
	}

	type protocolPool struct {
		kind     scheduler.ChannelKind
		upstream config.UpstreamConfig
		index    int
	}
	var pools []protocolPool
	for _, kind := range orderedKinds {
		var upstreams []config.UpstreamConfig
		switch kind {
		case scheduler.ChannelKindResponses:
			upstreams = cfg.ResponsesUpstream
		case scheduler.ChannelKindChat:
			upstreams = cfg.ChatUpstream
		case scheduler.ChannelKindMessages:
			upstreams = cfg.Upstream
		case scheduler.ChannelKindGemini:
			upstreams = cfg.GeminiUpstream
		}
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
	// 候选唯一键 = 物理路由 + 模型：同一物理渠道的同一画像只保留一条，
	// 同一模型在不同物理渠道上各保留一条（物理渠道间可用性/Key 独立，
	// 按协议+模型去重会丢掉可用渠道，最终可用性由 fanout 上限裁剪）。
	type candidateKey struct {
		route scheduler.ChannelRouteKey
		model string
	}
	best := make([]redirectCandidate, 0, overflowRedirectFanout)
	seen := make(map[candidateKey]bool)
	for _, pool := range pools {
		for _, p := range m.modelProfileStore.ListActiveByChannel(pool.upstream.ChannelUID) {
			if !p.ProbeSuccess {
				continue
			}
			// 同名模型刚被证明装不下，重定向到它没有意义
			if p.ModelID == requestModel {
				continue
			}
			// 协议画像匹配：ListActiveByChannel 返回渠道下全部协议的画像，
			// 执行路由按 pool.kind 构造，窗口按画像自身协议查询，两者必须一致，
			// 否则会出现"以 chat 路由执行 responses 窗口口径"的错配候选。
			if p.ChannelKind != string(pool.kind) {
				continue
			}
			window := effectiveContextWindow(p.ChannelUID, p.ChannelKind, p.ModelID, p.ContextTokens)
			if window < inputTokens {
				continue
			}
			route := scheduler.ChannelRouteRef{Kind: string(pool.kind), Index: pool.index, ChannelUID: pool.upstream.ChannelUID}
			key := candidateKey{route: route.Key(), model: p.ModelID}
			if seen[key] {
				continue
			}
			// 全部检查通过后才写 seen：窗口不足等被拒的组合不得占用去重名额，
			// 否则第一个窗口不足的渠道会把其他渠道的同名模型也挡在门外。
			seen[key] = true
			best = append(best, redirectCandidate{
				info: scheduler.ChannelInfo{
					Route:            route,
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

	// 同协议优先，其后质量档降序、稳定兜底键（名称、路由身份、模型）
	sort.SliceStable(best, func(i, j int) bool {
		if best[i].native != best[j].native {
			return best[i].native
		}
		if qualityTierRank(best[i].tier) != qualityTierRank(best[j].tier) {
			return qualityTierRank(best[i].tier) > qualityTierRank(best[j].tier)
		}
		if best[i].info.Name != best[j].info.Name {
			return best[i].info.Name < best[j].info.Name
		}
		if best[i].info.Route.Kind != best[j].info.Route.Kind {
			return best[i].info.Route.Kind < best[j].info.Route.Kind
		}
		if best[i].info.Route.Index != best[j].info.Route.Index {
			return best[i].info.Route.Index < best[j].info.Route.Index
		}
		if best[i].info.Route.ChannelUID != best[j].info.Route.ChannelUID {
			return best[i].info.Route.ChannelUID < best[j].info.Route.ChannelUID
		}
		return best[i].info.ActualModel < best[j].info.ActualModel
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
