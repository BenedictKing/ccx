package siteaccess

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// ChannelBindingRequest 将已有渠道绑定到已接入账号，不改写渠道中的 Key。
type ChannelBindingRequest struct {
	AccountUID   string `json:"accountUid"`
	Protocol     string `json:"protocol"`
	ChannelIndex int    `json:"channelIndex"`
	Group        string `json:"group"`
}

// BindExistingChannel 校验站点、协议、分组和 Key 指纹后建立账号归属。
func BindExistingChannel(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, adapter *NewApiAdapter, req ChannelBindingRequest) error {
	store.operations.Lock()
	defer store.operations.Unlock()

	req.Protocol = strings.ToLower(strings.TrimSpace(req.Protocol))
	req.Group = strings.TrimSpace(req.Group)
	apiType, _, err := provisionProtocolInfo(req.Protocol)
	if err != nil {
		return err
	}
	account, found, err := store.Get(req.AccountUID)
	if err != nil || !found {
		return fmt.Errorf("账号不存在")
	}
	channels := protocolChannels(cfgManager, apiType)
	if req.ChannelIndex < 0 || req.ChannelIndex >= len(channels) {
		return fmt.Errorf("渠道不存在")
	}
	channel := channels[req.ChannelIndex]
	if channel.SiteAccountUID != "" && channel.SiteAccountUID != account.UID {
		return fmt.Errorf("渠道已绑定其他账号，请先解绑")
	}
	if NormalizeSiteURL(channel.BaseURL) != account.BaseURL {
		return fmt.Errorf("渠道站点地址与账号不一致")
	}

	var meta *ProvisionedKeyMeta
	for i := range account.ProvisionedKeys {
		candidate := &account.ProvisionedKeys[i]
		if candidate.Protocol == req.Protocol && candidate.Group == req.Group {
			meta = candidate
			break
		}
	}
	if meta == nil || meta.KeyFingerprint == "" {
		return fmt.Errorf("账号在该协议和分组下没有可核对的 Key，请先完成站点接入")
	}
	matchedKey := ""
	for _, key := range channel.APIKeys {
		if keyFingerprint(key) == meta.KeyFingerprint {
			matchedKey = key
			break
		}
	}
	if matchedKey == "" {
		return fmt.Errorf("渠道未包含该账号分组的 Key，不能绑定")
	}
	if channel.SiteAccountUID == account.UID && channel.SiteGroup == req.Group {
		return nil
	}

	configs := append([]config.APIKeyConfig(nil), channel.APIKeyConfigs...)
	updates := config.UpstreamUpdate{
		SiteAccountUID:       &account.UID,
		SiteGroup:            &req.Group,
		ExpectedSiteSnapshot: &channel,
	}
	if account.CredentialMode != "key" {
		accessToken, err := account.plaintextAccessToken()
		if err != nil {
			return err
		}
		ratios, err := adapterForAccount(account, adapter).FetchGroups(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode)
		if err != nil {
			return fmt.Errorf("读取分组倍率失败: %w", err)
		}
		ratio, ok := ratios[req.Group]
		if !ok || !validNewApiGroupRatio(ratio) {
			return fmt.Errorf("远端分组 %q 不存在或倍率无效", req.Group)
		}
		limit := DefaultNewApiMaxGroupMultiplier
		for _, plan := range account.Plans {
			if plan.Protocol == req.Protocol && plan.MaxMultiplier != nil {
				limit = *plan.MaxMultiplier
				break
			}
		}
		if ratio > limit {
			return fmt.Errorf("分组 %q 的倍率 %.4g 超过上限 %.4g", req.Group, ratio, limit)
		}
		updates.MaxGroupMultiplier = config.OptionalMultiplier{Present: true, Value: &limit}
		updates.APIKeyConfigs = upsertBoundKeyConfig(configs, matchedKey, account, *meta, ratio)
	}
	if _, err := updateProtocolChannel(cfgManager, apiType, req.ChannelIndex, updates); err != nil {
		return err
	}
	RecordConfigured(store, account.UID, req.Protocol, req.Group)
	return nil
}

func upsertBoundKeyConfig(configs []config.APIKeyConfig, key string, account SiteAccount, meta ProvisionedKeyMeta, ratio float64) []config.APIKeyConfig {
	for i := range configs {
		if configs[i].Key != key {
			continue
		}
		configs[i].QuotaGroup = account.UID + "/" + meta.Group
		configs[i].GroupMultiplier = &ratio
		configs[i].MultiplierSource = account.SiteType
		configs[i].MultiplierUpdatedAt = time.Now().UTC().Format(time.RFC3339)
		configs[i].SourceAccountUID = account.UID
		configs[i].SourceRemoteTokenID = meta.TokenID
		return configs
	}
	return append(configs, config.APIKeyConfig{
		Key: key, QuotaGroup: account.UID + "/" + meta.Group, GroupMultiplier: &ratio,
		MultiplierSource: account.SiteType, MultiplierUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		SourceAccountUID: account.UID, SourceRemoteTokenID: meta.TokenID,
	})
}

// UnbindExistingChannel 只解除账号归属，保留渠道及全部 Key。
func UnbindExistingChannel(cfgManager *config.ConfigManager, req ChannelBindingRequest) error {
	apiType, _, err := provisionProtocolInfo(strings.ToLower(strings.TrimSpace(req.Protocol)))
	if err != nil {
		return err
	}
	channels := protocolChannels(cfgManager, apiType)
	if req.ChannelIndex < 0 || req.ChannelIndex >= len(channels) {
		return fmt.Errorf("渠道不存在")
	}
	channel := channels[req.ChannelIndex]
	if channel.SiteAccountUID == "" {
		return nil
	}
	empty := ""
	configs := append([]config.APIKeyConfig(nil), channel.APIKeyConfigs...)
	for i := range configs {
		if configs[i].SourceAccountUID != channel.SiteAccountUID {
			continue
		}
		configs[i].SourceAccountUID = ""
		configs[i].SourceRemoteTokenID = 0
		configs[i].RemoteStatus = ""
		configs[i].MultiplierUpdatedAt = ""
		if configs[i].GroupMultiplier != nil {
			configs[i].MultiplierSource = "manual"
		}
		if configs[i].QuotaGroup == channel.SiteAccountUID+"/"+channel.SiteGroup {
			configs[i].QuotaGroup = ""
		}
	}
	_, err = updateProtocolChannel(cfgManager, apiType, req.ChannelIndex, config.UpstreamUpdate{
		SiteAccountUID: &empty, SiteGroup: &empty, APIKeyConfigs: configs, ExpectedSiteSnapshot: &channel,
	})
	return err
}
