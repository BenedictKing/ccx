package siteaccess

import (
	"context"
	"github.com/BenedictKing/ccx/internal/config"
)

func rememberPlan(store *AccountStore, uid string, req ProvisionRequest) error {
	return store.Patch(uid, func(account *SiteAccount) error {
		for i := range account.Plans {
			if account.Plans[i].Protocol == req.Protocol {
				account.Plans[i] = req
				return nil
			}
		}
		account.Plans = append(account.Plans, req)
		return nil
	})
}

// catchUpGroups 只补接用户启用自动维护后新出现的合格组；已有归属即使被删也不重建。
func catchUpGroups(ctx context.Context, store *AccountStore, cm *config.ConfigManager, adapter siteAdapter, account SiteAccount, ratios map[string]float64) []ReconcileAction {
	var actions []ReconcileAction
	for _, plan := range account.Plans {
		if !plan.AutoSyncGroups {
			continue
		}
		apiType, serviceType, err := provisionProtocolInfo(plan.Protocol)
		if err != nil {
			continue
		}
		covered := map[string]bool{}
		for _, meta := range account.ProvisionedKeys {
			if meta.Protocol == plan.Protocol {
				covered[meta.Group] = true
			}
		}
		for _, group := range plan.ExcludeGroups {
			covered[group] = true
		}
		for name, ratio := range ratios {
			if covered[name] || !validNewApiGroupRatio(ratio) || ratio > provisionMaxMultiplier(plan.MaxMultiplier) {
				continue
			}
			token, err := account.plaintextAccessToken()
			if err != nil {
				continue
			}
			if _, err := adapter.FetchGroupModels(ctx, account.BaseURL, token, account.UserID, account.AuthTokenMode, name); err != nil {
				continue
			}
			plan.AccountUID = account.UID
			result := aProvisionGroup(ctx, store, cm, adapter, account, apiType, serviceType, plan, newApiResolvedGroup{Name: name, Ratio: ratio, MaxMultiplier: provisionMaxMultiplier(plan.MaxMultiplier)})
			action := "created"
			detail := "已接入符合原模型和倍率要求的新分组"
			if result.Skipped != "" {
				action = "skipped"
				detail = result.Skipped
			}
			actions = append(actions, ReconcileAction{Protocol: plan.Protocol, Group: name, KeyName: result.KeyName, ChannelName: result.ChannelName, Action: action, Detail: detail})
		}
	}
	return actions
}
