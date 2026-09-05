package autopilot

import (
	"log"
	"math"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/quota"
)

// ── configured 级配额真相生产接线（quota-truth-scheduling.md §7.3）──
//
// 数据源：订阅同步落盘的静态额度声明（SubscriptionProfile.Balance/UsedQuota，
// new-api user/self 或各 provider 账单接口产出）。SubscriptionRefreshWorker 已把
// BillingAPIKey 路径的实时余额接为 provider_api 级；new-api 同步的余额此前只落
// 画像不进配额体系——本文件补齐为 configured 级，两者经 MergeValues 的来源
// 优先级自然合流（provider_api 永不被 configured 降级覆盖）。
//
// 接线点（共同成功出口，避免在各 provider handler 重复写入）：
//   - NewApiSubscriptionSyncService.SyncNow 画像落盘成功后（单订阅增量）；
//   - ConfigManager.RegisterOnConfigChange（配置热更新 / new-api reconcile 落盘
//     / 手工配置变更，全量重放）。

// SyncSubscriptionQuotaAsConfigured 把单个订阅画像的余额声明写入 quota.Manager
// （configured 级）。accountUID 用 SubscriptionUID 做饱和桶聚合键。
// 返回实际写入的渠道数。
//
// 语义边界：
//   - 只写有限、非负且来源明确（LastBalanceRefreshAt 非 nil，即至少成功同步过
//     一次）的额度值；从未同步过的订阅不写，避免把画像默认零值误判为耗尽；
//   - 只写 Remaining（不臆造 Limit）：configured 级不参与 headroom 评分
//     （无 Limit 时中性分），但 Balance=0 提供明确的 exhausted 耗尽信号；
//   - 只写 liveChannelUIDs 中仍存在的渠道（channelUID 对齐，不用数组 index）；
//     已删除渠道不写也不清理，残留值会被更高优先级来源覆盖或随进程重启消失。
func SyncSubscriptionQuotaAsConfigured(qm *quota.Manager, profile *SubscriptionProfile, liveChannelUIDs map[string]bool) int {
	if qm == nil || profile == nil || len(profile.LinkedChannelUIDs) == 0 {
		return 0
	}
	if profile.LastBalanceRefreshAt == nil {
		return 0 // 从未成功同步过：Balance 的零值不代表真相
	}
	balance := profile.Balance
	if math.IsNaN(balance) || math.IsInf(balance, 0) || balance < 0 {
		return 0 // 只写有限、非负的额度值
	}
	used := float64(profile.UsedQuota)
	if math.IsNaN(used) || math.IsInf(used, 0) || used < 0 {
		used = 0
	}

	values := []quota.Value{{
		Dimension: quota.DimCurrency,
		Remaining: &balance,
		Used:      &used,
		Unit:      profile.Currency,
	}}
	written := 0
	for _, channelUID := range profile.LinkedChannelUIDs {
		if liveChannelUIDs != nil && !liveChannelUIDs[channelUID] {
			continue
		}
		// 来源标记由 UpdateChannelConfigured 统一强制为 configured。
		qm.UpdateChannelConfigured(channelUID, profile.SubscriptionUID, values)
		written++
	}
	return written
}

// SyncAllSubscriptionsQuotaAsConfigured 全量重放订阅画像的 configured 级配额
// （配置热更新回调与冷启动导入用）。返回写入的渠道数。
func SyncAllSubscriptionsQuotaAsConfigured(qm *quota.Manager, store *SubscriptionStore, cfg config.Config) int {
	if qm == nil || store == nil {
		return 0
	}
	profiles := store.ListAll()
	if len(profiles) == 0 {
		return 0
	}
	live := LiveChannelUIDSet(cfg)
	written := 0
	for _, profile := range profiles {
		written += SyncSubscriptionQuotaAsConfigured(qm, profile, live)
	}
	if written > 0 {
		log.Printf("[Quota-Configured] 已按 %d 个订阅画像写入 %d 个渠道的 configured 级配额", len(profiles), written)
	}
	return written
}

// LiveChannelUIDSet 返回当前配置中六类协议池全部渠道的 UID 集合。
func LiveChannelUIDSet(cfg config.Config) map[string]bool {
	set := make(map[string]bool)
	for _, list := range [][]config.UpstreamConfig{
		cfg.Upstream, cfg.ChatUpstream, cfg.ResponsesUpstream,
		cfg.GeminiUpstream, cfg.ImagesUpstream, cfg.VectorsUpstream,
	} {
		for i := range list {
			if list[i].ChannelUID != "" {
				set[list[i].ChannelUID] = true
			}
		}
	}
	return set
}
