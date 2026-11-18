package siteaccess

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// ReconcileAccount 按远端 ID 同步 Key 状态与倍率，并按用户授权补接新分组。
// 查询失败保留最近可信值；明确删除才拉黑，不把管理面失败当作推理不可用。
func ReconcileAccount(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, adapter *NewApiAdapter, accountUID string) (actions []ReconcileAction, retErr error) {
	store.operations.Lock()
	defer store.operations.Unlock()
	account, found, err := store.Get(accountUID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("账号不存在: %s", accountUID)
	}
	defer func() {
		_ = store.Patch(accountUID, func(current *SiteAccount) error {
			now := time.Now()
			current.LastSyncAt = &now
			current.LastSyncError = ""
			if retErr != nil {
				current.LastSyncError = retErr.Error()
			}
			for _, action := range actions {
				if action.Action == "error" {
					current.LastSyncError = action.Detail
					break
				}
			}
			return nil
		})
	}()
	managed := false
	for _, meta := range account.ProvisionedKeys {
		if meta.TokenID > 0 {
			managed = true
			break
		}
	}
	if !managed {
		return nil, nil
	}
	site := adapterForAccount(account, adapter)
	accessToken, err := account.plaintextAccessToken()
	if err != nil {
		return nil, err
	}
	tokens, err := site.ListAllTokens(ctx, account, accessToken)
	if err != nil {
		return nil, err
	}
	ratios, err := site.FetchGroups(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode)
	if err != nil {
		return nil, err
	}
	for _, meta := range account.ProvisionedKeys {
		if meta.TokenID <= 0 {
			continue
		}
		action := ReconcileAction{Protocol: meta.Protocol, Group: meta.Group, KeyName: meta.Name}
		var remote *NewApiToken
		for i := range tokens {
			if tokens[i].ID == meta.TokenID {
				remote = &tokens[i]
				break
			}
		}
		apiType, _, err := provisionProtocolInfo(meta.Protocol)
		if err != nil {
			action.Action, action.Detail = "error", err.Error()
			actions = append(actions, action)
			continue
		}
		foundLocal := false
		for i, channel := range protocolChannels(cfgManager, apiType) {
			if !matchesAccountChannel(channel, account, meta.Group, meta.KeyFingerprint) {
				continue
			}
			foundLocal = true
			action.ChannelName = channel.Name
			if remote == nil {
				action.Action, action.Detail = disableLocalProvisionedKey(cfgManager, apiType, channel.Name, meta.KeyFingerprint)
				actions = append(actions, action)
			} else {
				_, err := syncRemoteKey(cfgManager, apiType, i, channel, meta, *remote, ratios)
				if err != nil {
					action.Action, action.Detail = "error", err.Error()
					actions = append(actions, action)
				}
			}
		}
		if !foundLocal {
			action.Action, action.Detail = "missing_local", "本地渠道已删除，保留人工选择"
			actions = append(actions, action)
		}
	}
	actions = append(actions, catchUpGroups(ctx, store, cfgManager, site, account, ratios)...)
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
			if strings.TrimSpace(fingerprint) == "" || keyFingerprint(key) != fingerprint {
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
