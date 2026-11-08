package siteaccess

import (
	"context"
	"log"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// MinReconcileInterval 定期对账的频率硬下限，防止过于频繁地打点上游管理面。
const MinReconcileInterval = 30 * time.Minute

// StartReconcileScheduler 启动定期对账：间隔低于硬下限时收敛到下限。
// 返回后立即在后台运行，ctx 取消即停止。同步状态和倍率；
// 仅在账号已显式启用 AutoSyncGroups 时创建符合原策略的新分组 Key。
func StartReconcileScheduler(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, adapter *NewApiAdapter, interval time.Duration) {
	if interval < MinReconcileInterval {
		interval = MinReconcileInterval
	}
	go reconcileLoop(ctx, store, cfgManager, adapter, interval)
}

func reconcileLoop(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, adapter *NewApiAdapter, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			results, err := ReconcileAll(tickCtx, store, cfgManager, adapter)
			cancel()
			if err != nil {
				log.Printf("[SiteAccess-Reconcile] 定期对账失败: %v", err)
				continue
			}
			for uid, actions := range results {
				for _, action := range actions {
					log.Printf("[SiteAccess-Reconcile] 账号 %s: %s/%s %s %s", uid, action.Protocol, action.Group, action.Action, action.Detail)
				}
			}
		}
	}
}
