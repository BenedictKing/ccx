package siteaccess

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// ListAllTokens 只有完整分页成功才返回列表，防止不完整响应被当作远端删除。
func (a *NewApiAdapter) ListAllTokens(ctx context.Context, account SiteAccount, token string) ([]NewApiToken, error) {
	return listAllTokens(ctx, a, account, token)
}

func listAllTokens(ctx context.Context, a siteAdapter, account SiteAccount, token string) ([]NewApiToken, error) {
	all := make([]NewApiToken, 0)
	seen := map[int]bool{}
	for page := 1; page <= 1000; page++ {
		items, err := a.ListTokens(ctx, account.BaseURL, token, account.UserID, account.AuthTokenMode, page, 100)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.ID <= 0 || seen[item.ID] {
				return nil, fmt.Errorf("Key 分页返回重复或无效 ID，停止同步")
			}
			seen[item.ID] = true
			all = append(all, item)
		}
		if len(items) < 100 {
			return all, nil
		}
	}
	return nil, fmt.Errorf("Key 列表分页超过限制，停止同步")
}

func syncRemoteKey(cm *config.ConfigManager, apiType string, index int, channel config.UpstreamConfig, meta ProvisionedKeyMeta, remote NewApiToken, ratios map[string]float64) (bool, error) {
	configs := append([]config.APIKeyConfig(nil), channel.APIKeyConfigs...)
	found := false
	for _, key := range channel.APIKeys {
		if keyFingerprint(key) != meta.KeyFingerprint {
			continue
		}
		pos := -1
		for j := range configs {
			if configs[j].Key == key {
				pos = j
				break
			}
		}
		if pos < 0 {
			configs = append(configs, config.APIKeyConfig{Key: key})
			pos = len(configs) - 1
		}
		cfg := &configs[pos]
		cfg.RemoteStatus = ""
		ratio, exists := ratios[remote.Group]
		if !exists {
			cfg.RemoteStatus = "group_missing"
		} else if !validNewApiGroupRatio(ratio) {
			return false, fmt.Errorf("远端分组倍率无效，保留最近可信配置")
		} else {
			cfg.GroupMultiplier = &ratio
			if cfg.MultiplierSource == "" {
				cfg.MultiplierSource = "newapi"
			}
			cfg.MultiplierUpdatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		if remote.Status != 0 && remote.Status != 1 {
			cfg.RemoteStatus = "disabled"
		}
		if remote.Group != meta.Group {
			cfg.RemoteStatus = "group_changed"
		}
		if remote.ExpiredTime > 0 && remote.ExpiredTime <= time.Now().Unix() {
			cfg.RemoteStatus = "expired"
		}
		if remote.UnlimitedQuota != nil && !*remote.UnlimitedQuota && remote.RemainQuota != nil && *remote.RemainQuota <= 0 {
			cfg.RemoteStatus = "quota_exhausted"
		}
		cfg.QuotaGroup = channel.SiteAccountUID + "/" + remote.Group
		cfg.SourceRemoteTokenID = remote.ID
		found = true
	}
	if !found {
		return false, nil
	}
	changed := !reflect.DeepEqual(channel.APIKeyConfigs, configs)
	if !changed {
		return false, nil
	}
	_, err := updateProtocolChannel(cm, apiType, index, config.UpstreamUpdate{APIKeyConfigs: configs, ExpectedSiteSnapshot: &channel})
	return true, err
}
