package siteaccess

import (
	"context"
	"fmt"

	"github.com/BenedictKing/ccx/internal/config"
)

// ReconcileAction 对账产生的本地动作记录。
type ReconcileAction struct {
	Protocol    string `json:"protocol"`
	Group       string `json:"group"`
	KeyName     string `json:"keyName"`
	ChannelName string `json:"channelName"`
	Action      string `json:"action"` // "disabled" / "missing_local" / "error"
	Detail      string `json:"detail"`
}

// ReconcileAccount 对账单个站点账号：逐个核对已建远端 Key 是否仍存在，
// 远端已删除的 Key 在本地对应渠道中停用（拉黑，需手动恢复）。
// 只停用有明确证据的 Key；查询失败一律跳过，不做任何变更。
func ReconcileAccount(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, adapter *NewApiAdapter, accountUID string) ([]ReconcileAction, error) {
	account, found, err := store.Get(accountUID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("[SiteAccess-Reconcile] 账号不存在: %s", accountUID)
	}
	accessToken, err := account.plaintextAccessToken()
	if err != nil {
		return nil, err
	}

	var actions []ReconcileAction
	for _, meta := range account.ProvisionedKeys {
		action := ReconcileAction{
			Protocol: meta.Protocol,
			Group:    meta.Group,
			KeyName:  meta.Name,
		}
		remote, err := adapter.FindTokenByName(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode, meta.Name)
		if err != nil {
			action.Action = "error"
			action.Detail = fmt.Sprintf("远端查询失败，跳过: %v", err)
			actions = append(actions, action)
			continue
		}
		if remote != nil {
			continue // 远端仍存在，无需动作
		}

		apiType, _, err := provisionProtocolInfo(meta.Protocol)
		if err != nil {
			action.Action = "error"
			action.Detail = err.Error()
			actions = append(actions, action)
			continue
		}
		action.ChannelName = ProvisionedChannelName(account.BaseURL, meta.Protocol, meta.Group)
		disabled, detail := disableLocalProvisionedKey(cfgManager, apiType, action.ChannelName, meta.KeyFingerprint)
		action.Action = disabled
		action.Detail = detail
		actions = append(actions, action)
	}
	return actions, nil
}

// disableLocalProvisionedKey 在指定渠道中按指纹定位并拉黑远端已删的 Key。
// 找不到渠道或 Key 时返回 missing_local（渠道可能已被用户手动删除/换 Key）。
func disableLocalProvisionedKey(cfgManager *config.ConfigManager, apiType, channelName, fingerprint string) (string, string) {
	channels := protocolChannels(cfgManager, apiType)
	for i := range channels {
		if channels[i].Name != channelName {
			continue
		}
		for _, key := range channels[i].APIKeys {
			if fingerprint != "" && keyFingerprint(key) != fingerprint {
				continue
			}
			if err := cfgManager.BlacklistKey(apiType, i, key, "remote_token_deleted", "远端站点已删除该 Key，对账自动停用"); err != nil {
				return "error", fmt.Sprintf("本地停用失败: %v", err)
			}
			return "disabled", "远端已删除，本地 Key 已停用"
		}
		return "missing_local", "渠道中未找到对应 Key（可能已被手动调整）"
	}
	return "missing_local", "渠道不存在（可能已被手动删除）"
}

// ReconcileAll 对全部站点账号执行对账；单账号失败不中断其他账号。
func ReconcileAll(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, adapter *NewApiAdapter) (map[string][]ReconcileAction, error) {
	accounts, err := store.List()
	if err != nil {
		return nil, err
	}
	results := make(map[string][]ReconcileAction)
	for _, account := range accounts {
		actions, err := ReconcileAccount(ctx, store, cfgManager, adapter, account.UID)
		if err != nil {
			results[account.UID] = []ReconcileAction{{Action: "error", Detail: err.Error()}}
			continue
		}
		if len(actions) > 0 {
			results[account.UID] = actions
		}
	}
	return results, nil
}
