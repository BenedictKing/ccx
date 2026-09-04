package autopilot

import (
	"fmt"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/keypool"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// ── 五元组候选身份：(渠道, 协议, key, 模型, effort) ──
//
// 旧粒度是 (渠道, 模型) 二元组（routingCandidateKey），key 与 effort 维被渠道级
// 聚合吞掉：key 级健康/成本差异取 min 或首个画像，effort 档锚定折叠回常规口径。
// 五元组行让评分、trace 与执行 pin 都落到真实执行单元上。

// routingCandidateIdentity 是调度候选行的五元组身份。
type routingCandidateIdentity struct {
	ChannelUID  string      // 物理渠道稳定 ID
	Protocol    string      // 执行协议（物理渠道单协议，= executionKind）
	KeyIdentity string      // KeyUID；手工 key 无 KeyUID 时用 "kh_"+keyHash；空 = 无 key 维度
	QuotaGroup  string      // key 分组（GroupIdentity 语义，展示与组级禁用判定用）
	Model       string      // 实际发送模型（归一化）
	Effort      EffortLevel // 思考档位；空 = passthrough（未决/模型不支持）
}

// routingCandidateSegment 编码身份段：空段用 "*" 占位，保证五段格式可逆向判别。
func routingCandidateSegment(s string) string {
	if s == "" {
		return "*"
	}
	return s
}

// Key 返回五段竖线身份串：channelUID|protocol|keyIdentity|model|effort。
// 旧二元组串是该格式的前两段形态；历史行读取不做反解析，前端按段数区分。
func (id routingCandidateIdentity) Key() string {
	return strings.Join([]string{
		routingCandidateSegment(id.ChannelUID),
		routingCandidateSegment(id.Protocol),
		routingCandidateSegment(id.KeyIdentity),
		routingCandidateSegment(normalizeRoutingModelID(id.Model)),
		routingCandidateSegment(string(id.Effort)),
	}, "|")
}

// routingKeyCandidate 是 key 维展开后的单 key 描述。
// APIKey 明文仅用于内存中的成本倍率匹配，严禁写入 trace/日志。
type routingKeyCandidate struct {
	APIKey      string
	KeyIdentity string
	KeyHash     string
	QuotaGroup  string
	Config      config.APIKeyConfig
}

// keyIdentityFor 计算 key 的稳定身份：KeyUID 优先（密钥轮换不变），
// 手工 key 无 KeyUID 时退到明文哈希前缀。
func keyIdentityFor(cfg config.APIKeyConfig, apiKey string) string {
	if uid := strings.TrimSpace(cfg.KeyUID); uid != "" {
		return uid
	}
	return "kh_" + KeyHashFromAPIKey(apiKey)
}

// ResolvePinnedAPIKey 把调度 pin 的 key 身份反查为渠道内明文 key。
// 未匹配（key 已被移除/轮换）返回空，调用方按无 pin 处理（fail-open）。
func ResolvePinnedAPIKey(upstream *config.UpstreamConfig, keyIdentity string) string {
	if upstream == nil || keyIdentity == "" {
		return ""
	}
	uid := strings.TrimSpace(keyIdentity)
	for _, cfg := range config.NormalizeAPIKeyConfigsForView(*upstream) {
		if cfg.Key == "" {
			continue
		}
		if strings.TrimSpace(cfg.KeyUID) == uid || "kh_"+KeyHashFromAPIKey(cfg.Key) == uid {
			return cfg.Key
		}
	}
	return ""
}

// channelKeyCandidates 全量枚举渠道在该模型上的可用 key。
// 复用 keypool 的运行期负信号过滤（key 禁用 / (key,模型) 限制 / 分组禁用 /
// 渠道-Key-模型熔断 / 倍率闸门），顺序为权重降序。
func (r *SmartRouter) channelKeyCandidates(upstream *config.UpstreamConfig, executionKind, model string) []routingKeyCandidate {
	if r == nil || upstream == nil || len(upstream.APIKeys) == 0 {
		return nil
	}
	var circuitProbe keypool.ModelCircuitChecker
	if r.modelCircuitProbe != nil {
		circuitProbe = func(channelUID, apiKey, model string) bool {
			return r.modelCircuitProbe(executionKind, channelUID, apiKey, model)
		}
	}
	cands := keypool.CandidatesForModelWeighted(upstream, nil, model, circuitProbe, nil)
	out := make([]routingKeyCandidate, 0, len(cands))
	for _, c := range cands {
		out = append(out, routingKeyCandidate{
			APIKey:      c.APIKey,
			KeyIdentity: keyIdentityFor(c.Config, c.APIKey),
			KeyHash:     KeyHashFromAPIKey(c.APIKey),
			QuotaGroup:  strings.TrimSpace(c.Config.QuotaGroup),
			Config:      c.Config,
		})
	}
	return out
}

// channelKeyProfiles 预取渠道下按 KeyHash 索引的 endpoint 画像（限执行协议）。
// 同一 key 多 baseURL 时保留首个（与旧聚合取 matchingProfiles[0] 的口径一致）。
func (r *SmartRouter) channelKeyProfiles(channelUID, channelKind string) map[string]*KeyEndpointProfile {
	if r == nil || r.profileStore == nil || channelUID == "" {
		return nil
	}
	profiles := r.profileStore.ListActiveByChannel(channelUID)
	m := make(map[string]*KeyEndpointProfile, len(profiles))
	for _, p := range profiles {
		if p == nil || p.ChannelKind != channelKind || p.KeyHash == "" {
			continue
		}
		if _, exists := m[p.KeyHash]; !exists {
			m[p.KeyHash] = p
		}
	}
	return m
}

// deriveEffortChain 推导候选行的 effort 档链：已决档 + 相邻降档（模型声明多档时）。
// 未决（passthrough）返回 nil，行身份 effort 段为空。
func deriveEffortChain(decided EffortLevel, effortDecided bool, supported []EffortLevel) []EffortLevel {
	if !effortDecided || decided == "" {
		return nil
	}
	chain := []EffortLevel{decided}
	if down := adjacentEffortDownshift(supported, decided); down != "" {
		chain = append(chain, down)
	}
	return chain
}

// adjacentEffortDownshift 在模型声明档位里找 decided 的序数相邻低档。
// off 视为非档（降档到 off 等于关闭思考，不属于档位 failover 语义）。
func adjacentEffortDownshift(supported []EffortLevel, decided EffortLevel) EffortLevel {
	ord := EffortLevelOrdinal(decided)
	best := EffortLevel("")
	bestOrd := -1
	for _, lv := range supported {
		if lv == "" || lv == EffortOff {
			continue
		}
		o := EffortLevelOrdinal(lv)
		if o < ord && o > bestOrd {
			best, bestOrd = lv, o
		}
	}
	return best
}

// routingCandidateRowsPerChannelLimit 单渠道五元组候选行硬顶。
// 模型 fanout(≤8) × 全量 key × effort(≤2) 的笛卡尔积截断，防 candidates 膨胀；
// 截断按（模型序 × key 权重序）保序截尾，即保留质量排序靠前的组合。
const routingCandidateRowsPerChannelLimit = 64

// expandChannelCandidates 把 (渠道, 模型) 解析行展开为 (渠道, 协议, key, 模型, effort)
// 五元组候选行，逐行构建评分输入并回填 costMap。
//
// 展开规则：
//   - key 维全量（keypool 运行期负信号过滤后的可用 key，权重序）；
//   - effort 维 ≤2 档（已决档 + 相邻降档；passthrough 模型单档）；
//   - 无可用 key 时回退单行无 key 维度（fail-open，等同旧粒度行为）；
//   - 单渠道行数超 routingCandidateRowsPerChannelLimit 时截断。
//
// 调用方负责设置 entry.Route 等渠道级字段前的短路路径（协议联邦）。
func (r *SmartRouter) expandChannelCandidates(
	ch scheduler.ChannelInfo,
	upstream *config.UpstreamConfig,
	executionKind string,
	route scheduler.ChannelRouteRef,
	resolutions []channelModelResolution,
	upstreamModelCapabilities map[string]config.UpstreamModelCapability,
	out []channelScoreEntry,
	costMap map[string]float64,
) []channelScoreEntry {
	if len(resolutions) == 0 {
		return out
	}
	channelUID := upstream.ChannelUID
	if channelUID == "" {
		channelUID = fmt.Sprintf("ch_%d", ch.Index)
	}
	keyProfiles := r.channelKeyProfiles(channelUID, executionKind)
	channelRows := 0

	for _, res := range resolutions {
		if !res.Supported {
			continue
		}
		keys := r.channelKeyCandidates(upstream, executionKind, res.ActualModel)
		efforts := res.EffortChain
		effortDecided := res.EffortDecided
		if len(efforts) == 0 {
			efforts = []EffortLevel{""}
			effortDecided = false
		}

		if len(keys) == 0 {
			// fail-open：渠道无可用 key 信息时保持单行无 key 维，不因 key 维丢失渠道。
			entry := r.buildChannelEntryForKey(ch, upstream, executionKind, res.ActualModel,
				upstreamModelCapabilities, nil, keyProfiles, efforts[0])
			applyResolutionIdentity(&entry, channelUID, executionKind, res, "", "", "", efforts[0], effortDecided)
			entry.Route = route
			entry.ProtocolFidelity = ch.ProtocolFidelity
			entry.ConversionPenalty = ch.ConversionPenalty
			r.applyModelQualityTier(&entry)
			channelRows++
			out = append(out, entry)
			if costMap != nil {
				costMap[entry.CandidateKey] = entry.EstimatedCost
			}
			continue
		}

		stop := false
		for i := range keys {
			key := keys[i]
			for _, effort := range efforts {
				if channelRows >= routingCandidateRowsPerChannelLimit {
					stop = true
					break
				}
				entry := r.buildChannelEntryForKey(ch, upstream, executionKind, res.ActualModel,
					upstreamModelCapabilities, &key, keyProfiles, effort)
				applyResolutionIdentity(&entry, channelUID, executionKind, res, key.KeyIdentity, key.KeyHash, key.QuotaGroup, effort, effortDecided)
				entry.Route = route
				entry.ProtocolFidelity = ch.ProtocolFidelity
				entry.ConversionPenalty = ch.ConversionPenalty
				r.applyModelQualityTier(&entry)
				channelRows++
				out = append(out, entry)
				if costMap != nil {
					costMap[entry.CandidateKey] = entry.EstimatedCost
				}
			}
			if stop {
				break
			}
		}
		if stop {
			break
		}
	}
	return out
}

// applyResolutionIdentity 把解析行身份 + key/effort 维写入候选行并生成五段 CandidateKey。
func applyResolutionIdentity(
	entry *channelScoreEntry,
	channelUID, executionKind string,
	res channelModelResolution,
	keyIdentity, keyHash, quotaGroup string,
	effort EffortLevel, effortDecided bool,
) {
	entry.MappedModel = res.MappedModel
	entry.MappingSource = res.MappingSource
	entry.MappingReason = res.MappingReason
	entry.KeyIdentity = keyIdentity
	entry.KeyHash = keyHash
	entry.QuotaGroup = quotaGroup
	entry.Effort = effort
	entry.EffortDecided = effortDecided
	entry.CandidateKey = routingCandidateIdentity{
		ChannelUID:  channelUID,
		Protocol:    executionKind,
		KeyIdentity: keyIdentity,
		QuotaGroup:  quotaGroup,
		Model:       res.ActualModel,
		Effort:      effort,
	}.Key()
}
