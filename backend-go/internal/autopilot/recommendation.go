package autopilot

import "sort"

// ── 渠道推荐（Phase 4 Item 4）──
//
// 只读只建议：不做任何自动切换渠道的动作。复用 task_domain.go 的 DomainStrength +
// ChannelProfile 聚合出的 QualityTier/StabilityTier，为每个 (channel, domain) 算一个
// 综合分；结合 UsagePatternAccumulator 的用量画像，找出"用户常用领域里，有渠道评分显著
// 更高但用户很少/从未使用"的情况，生成推荐条目。

// RecommendationOptions 推荐算法可调参数（均有合理默认值）。
type RecommendationOptions struct {
	// WindowDays 用量统计滚动窗口天数，默认 7。
	WindowDays int
	// TopDomainLimit 只对用量最高的前 N 个 TaskDomain 生成推荐，默认 2。
	TopDomainLimit int
	// ScoreDeltaThreshold 推荐渠道综合分需比当前主用渠道高出的最小差值，默认 0.15。
	ScoreDeltaThreshold float64
	// RareUsageThreshold 候选渠道在该 domain 下的用量占比阈值（低于此比例视为"很少使用"），默认 0.05。
	RareUsageThreshold float64
}

// defaultRecommendationOptions 返回默认参数。
func defaultRecommendationOptions() RecommendationOptions {
	return RecommendationOptions{
		WindowDays:          7,
		TopDomainLimit:      2,
		ScoreDeltaThreshold: 0.15,
		RareUsageThreshold:  0.05,
	}
}

// normalizeRecommendationOptions 填充零值字段为默认值。
func normalizeRecommendationOptions(opts RecommendationOptions) RecommendationOptions {
	def := defaultRecommendationOptions()
	if opts.WindowDays <= 0 {
		opts.WindowDays = def.WindowDays
	}
	if opts.TopDomainLimit <= 0 {
		opts.TopDomainLimit = def.TopDomainLimit
	}
	if opts.ScoreDeltaThreshold <= 0 {
		opts.ScoreDeltaThreshold = def.ScoreDeltaThreshold
	}
	if opts.RareUsageThreshold <= 0 {
		opts.RareUsageThreshold = def.RareUsageThreshold
	}
	return opts
}

// ChannelDomainScore 计算指定渠道在给定 TaskDomain 下的综合分（0.0~1.0，越高越好）。
// 加权：域匹配度（modelProfiles 中最佳 DomainStrength）0.5 + QualityTier 0.3 + StabilityTier 0.2。
// modelProfiles 为空时域匹配度回退中性值 0.5（与 DomainStrength 的中性默认保持一致）。
func ChannelDomainScore(cp ChannelProfile, domain TaskDomain, modelProfiles []ModelProfile) float64 {
	qualityScore := float64(qualityTierRank(cp.QualityTier))
	if qualityScore < 0 {
		// avoid 档 rank 为负，钳到 0 防止渠道综合分被拉成负值
		qualityScore = 0
	}
	qualityScore /= 3.0
	stabilityScore := float64(stabilityTierRank(cp.StabilityTier)) / 2.0

	domainScore := 0.5
	if len(modelProfiles) > 0 {
		best := -1.0
		for i := range modelProfiles {
			s := DomainStrength(&modelProfiles[i], domain)
			if s > best {
				best = s
			}
		}
		if best >= 0 {
			domainScore = best
		}
	}

	return domainScore*0.5 + qualityScore*0.3 + stabilityScore*0.2
}

// ChannelRecommendation 单条渠道推荐（只读展示）。
type ChannelRecommendation struct {
	ProxyKeyMask          string     `json:"proxyKeyMask"`
	Domain                TaskDomain `json:"domain"`
	DomainUsageCount      int        `json:"domainUsageCount"`
	CurrentChannelUID     string     `json:"currentChannelUid"`
	CurrentScore          float64    `json:"currentScore"`
	RecommendedChannelUID string     `json:"recommendedChannelUid"`
	RecommendedScore      float64    `json:"recommendedScore"`
	ScoreDelta            float64    `json:"scoreDelta"`
	Reason                string     `json:"reason"`
}

// channelScoreLookup 提供渠道 -> domain -> score 的查询能力，供 BuildRecommendations 使用。
type channelScoreLookup struct {
	scores  map[string]map[TaskDomain]float64
	healthy map[string]bool // channelUID -> 是否健康（dead/misconfigured 视为不健康，不参与推荐）
}

// BuildRecommendationsForUser 为单个 proxyKeyMask 生成渠道推荐列表。
// acc 为用量画像累积器；lookup 为渠道-领域评分表（由调用方基于 ChannelProfile + ModelProfile 预计算）。
// 纯函数（除读取 acc 外无副作用），便于单测。
func BuildRecommendationsForUser(
	proxyKeyMask string,
	acc *UsagePatternAccumulator,
	lookup channelScoreLookup,
	opts RecommendationOptions,
) []ChannelRecommendation {
	if acc == nil || proxyKeyMask == "" {
		return nil
	}
	opts = normalizeRecommendationOptions(opts)

	domainStats := acc.DomainDistribution(proxyKeyMask, opts.WindowDays)
	if len(domainStats) == 0 {
		return nil
	}
	if len(domainStats) > opts.TopDomainLimit {
		domainStats = domainStats[:opts.TopDomainLimit]
	}

	var recommendations []ChannelRecommendation
	for _, ds := range domainStats {
		channelStats := acc.ChannelDistribution(proxyKeyMask, ds.Domain, opts.WindowDays)
		if len(channelStats) == 0 {
			continue
		}

		// 当前主用渠道 = 用量最高的渠道（ChannelDistribution 已按数量降序排列）。
		currentChannelUID := channelStats[0].ChannelUID
		currentScore, ok := lookup.scores[currentChannelUID][ds.Domain]
		if !ok {
			currentScore = 0.5 // 无画像数据时按中性分处理，避免除零/误判
		}

		usageByChannel := make(map[string]int, len(channelStats))
		for _, cs := range channelStats {
			usageByChannel[cs.ChannelUID] = cs.Count
		}

		// 在该 domain 综合分表中寻找显著更优、且用户很少/从未使用的候选渠道。
		var best string
		var bestScore float64
		for channelUID, domainScores := range lookup.scores {
			if channelUID == currentChannelUID {
				continue
			}
			if lookup.healthy != nil && !lookup.healthy[channelUID] {
				continue // 排除不健康渠道，不推荐用户切到一个当前有问题的渠道
			}
			score, ok := domainScores[ds.Domain]
			if !ok {
				continue
			}
			usage := usageByChannel[channelUID]
			usageShare := 0.0
			if ds.Count > 0 {
				usageShare = float64(usage) / float64(ds.Count)
			}
			if usageShare > opts.RareUsageThreshold {
				continue // 用户已经比较常用这个渠道，不算"值得尝试的新选项"
			}
			if score-currentScore < opts.ScoreDeltaThreshold {
				continue
			}
			if best == "" || score > bestScore {
				best = channelUID
				bestScore = score
			}
		}

		if best == "" {
			continue
		}

		recommendations = append(recommendations, ChannelRecommendation{
			ProxyKeyMask:          proxyKeyMask,
			Domain:                ds.Domain,
			DomainUsageCount:      ds.Count,
			CurrentChannelUID:     currentChannelUID,
			CurrentScore:          currentScore,
			RecommendedChannelUID: best,
			RecommendedScore:      bestScore,
			ScoreDelta:            bestScore - currentScore,
			Reason:                "high_domain_usage_better_channel_available",
		})
	}

	sort.Slice(recommendations, func(i, j int) bool {
		return recommendations[i].ScoreDelta > recommendations[j].ScoreDelta
	})
	return recommendations
}
