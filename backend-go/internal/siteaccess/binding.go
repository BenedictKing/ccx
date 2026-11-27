package siteaccess

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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
	if channel.SiteGroup != "" && channel.SiteGroup != req.Group {
		return fmt.Errorf("远端分组与当前渠道不同，请建立独立渠道")
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
		ratio, err := fetchBindingRatio(ctx, adapterForAccount(account, adapter), account, accessToken, req.Group)
		if err != nil {
			return err
		}
		updates.GroupMultiplier = config.OptionalMultiplier{Present: true, Value: &ratio}
		updates.APIKeyConfigs = upsertBoundKeyConfig(configs, matchedKey, account, *meta)
	}
	if _, err := updateProtocolChannel(cfgManager, apiType, req.ChannelIndex, updates); err != nil {
		return err
	}
	RecordConfigured(store, account.UID, req.Protocol, req.Group)
	return nil
}

func upsertBoundKeyConfig(configs []config.APIKeyConfig, key string, account SiteAccount, meta ProvisionedKeyMeta) []config.APIKeyConfig {
	for i := range configs {
		if configs[i].Key != key {
			continue
		}
		configs[i].QuotaGroup = account.UID + "/" + meta.Group
		configs[i].SourceAccountUID = account.UID
		configs[i].SourceRemoteTokenID = meta.TokenID
		configs[i].RemoteStatus = ""
		return configs
	}
	return append(configs, config.APIKeyConfig{
		Key: key, QuotaGroup: account.UID + "/" + meta.Group,
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
		if configs[i].QuotaGroup == channel.SiteAccountUID+"/"+channel.SiteGroup {
			configs[i].QuotaGroup = ""
		}
	}
	_, err = updateProtocolChannel(cfgManager, apiType, req.ChannelIndex, config.UpstreamUpdate{
		SiteAccountUID: &empty, SiteGroup: &empty, APIKeyConfigs: configs, ExpectedSiteSnapshot: &channel,
	})
	return err
}

// KeyBindingRequest 绑定渠道中的单个 Key 到远端账号令牌。
type KeyBindingRequest struct {
	AccountUID    string `json:"accountUid"`
	Protocol      string `json:"protocol"`
	ChannelIndex  int    `json:"channelIndex"`
	APIKey        string `json:"apiKey"`
	Group         string `json:"group"`
	RemoteTokenID int    `json:"remoteTokenId"`
}

// ChannelGroupCopyRequest 以现有渠道为模板创建指定账号分组的独立副本。
type ChannelGroupCopyRequest struct {
	KeyBindingRequest
}

// ChannelGroupCopyResult 返回分组副本位置；Reused 表示同账号同分组渠道已存在。
type ChannelGroupCopyResult struct {
	ChannelIndex int    `json:"channelIndex"`
	ChannelName  string `json:"channelName"`
	AccountUID   string `json:"accountUid"`
	Group        string `json:"group"`
	Reused       bool   `json:"reused"`
}

// BindExistingKey 校验远端令牌明文与本地 Key 一致后，仅更新该 Key 的账号归属。
func BindExistingKey(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, adapter *NewApiAdapter, req KeyBindingRequest) error {
	store.operations.Lock()
	defer store.operations.Unlock()

	req.Protocol = strings.ToLower(strings.TrimSpace(req.Protocol))
	req.Group = strings.TrimSpace(req.Group)
	apiType, _, err := provisionProtocolInfo(req.Protocol)
	if err != nil {
		return err
	}
	account, channel, _, err := bindingContext(store, cfgManager, apiType, req.AccountUID, req.ChannelIndex, req.APIKey)
	if err != nil {
		return err
	}
	if account.CredentialMode == "key" {
		return fmt.Errorf("该账号只有推理 Key，请先在渠道绑定窗口补充站点管理令牌")
	}
	accessToken, err := account.plaintextAccessToken()
	if err != nil {
		return err
	}
	site := adapterForAccount(account, adapter)
	remote, plain, err := remoteTokenPlaintext(ctx, site, account, accessToken, req.RemoteTokenID)
	if err != nil {
		return err
	}
	if remote.Group != req.Group || (remote.Status != 0 && remote.Status != 1) {
		return fmt.Errorf("远端 Key 的分组或状态不符合绑定条件")
	}
	if !newApiTokenSupportsModels(remote, channelRemoteModels(channel)) {
		return fmt.Errorf("远端 Key 未授权当前渠道的目标模型")
	}
	if plain != req.APIKey {
		return fmt.Errorf("所选远端 Key 与当前本地 Key 不一致")
	}
	ratio, err := fetchBindingRatio(ctx, site, account, accessToken, req.Group)
	if err != nil {
		return err
	}
	if channel.SiteAccountUID != "" && (channel.SiteGroup != req.Group ||
		(channel.SiteAccountUID != account.UID && channel.BillingMultiplier() != ratio)) {
		return fmt.Errorf("远端分组或倍率与当前渠道不同，请建立独立渠道")
	}
	meta := ProvisionedKeyMeta{Name: remote.Name, Group: remote.Group, TokenID: remote.ID, Protocol: req.Protocol, KeyFingerprint: keyFingerprint(plain), Imported: true}
	configs := upsertBoundKeyConfig(append([]config.APIKeyConfig(nil), channel.APIKeyConfigs...), req.APIKey, account, meta)
	updates := config.UpstreamUpdate{APIKeyConfigs: configs, ExpectedSiteSnapshot: &channel}
	if channel.SiteAccountUID == "" {
		updates.SiteAccountUID = &account.UID
		updates.SiteGroup = &req.Group
	}
	if channel.SiteAccountUID == "" || channel.SiteAccountUID == account.UID {
		updates.GroupMultiplier = config.OptionalMultiplier{Present: true, Value: &ratio}
	}
	if _, err := updateProtocolChannel(cfgManager, apiType, req.ChannelIndex, updates); err != nil {
		return err
	}
	if err := rememberBoundKey(store, account.UID, meta); err != nil {
		return err
	}
	RecordConfigured(store, account.UID, req.Protocol, req.Group)
	return nil
}

// UnbindExistingKey 只解除一个 Key 的来源账号，其他账号的 Key 继续参与对账。
func UnbindExistingKey(store *AccountStore, cfgManager *config.ConfigManager, req KeyBindingRequest) error {
	store.operations.Lock()
	defer store.operations.Unlock()
	apiType, _, err := provisionProtocolInfo(strings.ToLower(strings.TrimSpace(req.Protocol)))
	if err != nil {
		return err
	}
	channels := protocolChannels(cfgManager, apiType)
	if req.ChannelIndex < 0 || req.ChannelIndex >= len(channels) {
		return fmt.Errorf("渠道不存在")
	}
	channel := channels[req.ChannelIndex]
	configs := append([]config.APIKeyConfig(nil), channel.APIKeyConfigs...)
	sourceAccount := ""
	found := false
	for i := range configs {
		if configs[i].Key != req.APIKey || configs[i].SourceAccountUID == "" {
			continue
		}
		found = true
		sourceAccount = configs[i].SourceAccountUID
		configs[i].SourceAccountUID = ""
		configs[i].SourceRemoteTokenID = 0
		configs[i].RemoteStatus = ""
		if strings.HasPrefix(configs[i].QuotaGroup, sourceAccount+"/") {
			configs[i].QuotaGroup = ""
		}
	}
	if !found {
		return nil
	}
	updates := config.UpstreamUpdate{APIKeyConfigs: configs, ExpectedSiteSnapshot: &channel}
	if channel.SiteAccountUID == sourceAccount {
		empty := ""
		updates.SiteAccountUID = &empty
		updates.SiteGroup = &empty
		for _, cfg := range configs {
			if cfg.SourceAccountUID == "" {
				continue
			}
			group := strings.TrimPrefix(cfg.QuotaGroup, cfg.SourceAccountUID+"/")
			updates.SiteAccountUID = &cfg.SourceAccountUID
			updates.SiteGroup = &group
			break
		}
	}
	if _, err = updateProtocolChannel(cfgManager, apiType, req.ChannelIndex, updates); err != nil {
		return err
	}
	fingerprint := keyFingerprint(req.APIKey)
	stillUsed := false
	for _, channel := range protocolChannels(cfgManager, apiType) {
		for _, cfg := range channel.APIKeyConfigs {
			if cfg.SourceAccountUID == sourceAccount && keyFingerprint(cfg.Key) == fingerprint {
				stillUsed = true
				break
			}
		}
	}
	if !stillUsed {
		return store.Patch(sourceAccount, func(account *SiteAccount) error {
			keys := account.ProvisionedKeys[:0]
			for _, meta := range account.ProvisionedKeys {
				if meta.Protocol == req.Protocol && meta.TokenID > 0 && meta.KeyFingerprint == fingerprint {
					continue
				}
				keys = append(keys, meta)
			}
			account.ProvisionedKeys = keys
			return nil
		})
	}
	return nil
}

// CopyChannelToGroup 创建只包含目标分组 Key 的渠道副本；原渠道和 Key 不变。
func CopyChannelToGroup(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, adapter *NewApiAdapter, req ChannelGroupCopyRequest) (*ChannelGroupCopyResult, error) {
	store.operations.Lock()
	defer store.operations.Unlock()

	req.Protocol = strings.ToLower(strings.TrimSpace(req.Protocol))
	req.Group = strings.TrimSpace(req.Group)
	apiType, _, err := provisionProtocolInfo(req.Protocol)
	if err != nil {
		return nil, err
	}
	account, channel, _, err := bindingContext(store, cfgManager, apiType, req.AccountUID, req.ChannelIndex, req.APIKey)
	if err != nil {
		return nil, err
	}
	for i, existing := range protocolChannels(cfgManager, apiType) {
		if existing.SiteAccountUID == account.UID && existing.SiteGroup == req.Group {
			return &ChannelGroupCopyResult{ChannelIndex: i, ChannelName: existing.Name, AccountUID: account.UID, Group: req.Group, Reused: true}, nil
		}
	}
	if account.CredentialMode == "key" {
		return nil, fmt.Errorf("该账号只有推理 Key，请先在渠道绑定窗口补充站点管理令牌")
	}
	accessToken, err := account.plaintextAccessToken()
	if err != nil {
		return nil, err
	}
	site := adapterForAccount(account, adapter)
	ratio, err := fetchBindingRatio(ctx, site, account, accessToken, req.Group)
	if err != nil {
		return nil, err
	}
	models := channelRemoteModels(channel)
	groupModels, modelsErr := site.FetchGroupModels(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode, req.Group)
	if modelsErr != nil && !errors.Is(modelsErr, ErrGroupModelsUnknown) {
		return nil, fmt.Errorf("查询分组模型失败: %w", modelsErr)
	}
	availableModels := map[string]bool{}
	for _, model := range groupModels {
		availableModels[model] = true
	}
	if modelsErr == nil && len(groupModels) == 0 {
		return nil, fmt.Errorf("目标分组没有可用模型")
	}
	if modelsErr == nil && len(models) > 0 {
		filtered := models[:0]
		for _, model := range models {
			if availableModels[model] {
				filtered = append(filtered, model)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("目标分组不含当前渠道的目标模型")
		}
		models = filtered
	}
	tokenID, plain, reused, finalName, err := provisionOrImportKey(ctx, site, account, accessToken, req.RemoteTokenID, NewApiProvisionOptions{
		Name: provisionedKeyName(req.Protocol, req.Group, models), Group: req.Group, Models: models,
	})
	if err != nil {
		return nil, err
	}

	clone := channel.Clone()
	if modelsErr == nil && len(models) > 0 {
		allowed := make(map[string]bool, len(models))
		for _, model := range models {
			allowed[model] = true
		}
		for alias, target := range clone.ModelMapping {
			if !allowed[target] {
				delete(clone.ModelMapping, alias)
			} else {
				allowed[alias] = true
			}
		}
		if clone.SupportedModels != nil {
			supported := clone.SupportedModels[:0]
			for _, model := range clone.SupportedModels {
				if allowed[model] {
					supported = append(supported, model)
				}
			}
			clone.SupportedModels = supported
		}
	}
	clone.ChannelUID = ""
	clone.Name = uniqueChannelCopyName(protocolChannels(cfgManager, apiType), channel.Name, req.Group)
	clone.SiteAccountUID = account.UID
	clone.SiteGroup = req.Group
	clone.GroupMultiplier = &ratio
	clone.APIKeys = []string{plain}
	clone.HistoricalAPIKeys = nil
	clone.DisabledAPIKeys = nil
	clone.DisabledKeyModels = nil
	clone.DisabledGroupModels = nil
	clone.PromotionUntil = nil
	clone.Status = "active"
	meta := ProvisionedKeyMeta{Name: finalName, Group: req.Group, TokenID: tokenID, Protocol: req.Protocol, KeyFingerprint: keyFingerprint(plain), Imported: req.RemoteTokenID > 0}
	keyConfig := config.APIKeyConfig{Key: plain}
	for _, cfg := range channel.APIKeyConfigs {
		if cfg.Key == req.APIKey {
			keyConfig = cfg
			keyConfig.Key = plain
			keyConfig.Enabled = nil
			break
		}
	}
	keyConfig.RemoteStatus = ""
	clone.APIKeyConfigs = upsertBoundKeyConfig([]config.APIKeyConfig{keyConfig}, plain, account, meta)
	if err := addProtocolChannel(cfgManager, apiType, *clone); err != nil {
		if !reused {
			if deleteErr := site.DeleteToken(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode, tokenID); deleteErr != nil {
				return nil, fmt.Errorf("创建渠道副本失败: %v；新建远端 Key 回收失败: %v", err, deleteErr)
			}
		}
		return nil, err
	}
	if err := rememberBoundKey(store, account.UID, meta); err != nil {
		return nil, err
	}
	RecordConfigured(store, account.UID, req.Protocol, req.Group)
	return &ChannelGroupCopyResult{ChannelIndex: 0, ChannelName: clone.Name, AccountUID: account.UID, Group: req.Group}, nil
}

func bindingContext(store *AccountStore, cfgManager *config.ConfigManager, apiType, accountUID string, channelIndex int, apiKey string) (SiteAccount, config.UpstreamConfig, int, error) {
	account, found, err := store.Get(accountUID)
	if err != nil || !found {
		return SiteAccount{}, config.UpstreamConfig{}, 0, fmt.Errorf("账号不存在")
	}
	channels := protocolChannels(cfgManager, apiType)
	if channelIndex < 0 || channelIndex >= len(channels) {
		return SiteAccount{}, config.UpstreamConfig{}, 0, fmt.Errorf("渠道不存在")
	}
	channel := channels[channelIndex]
	if NormalizeSiteURL(channel.BaseURL) != account.BaseURL {
		return SiteAccount{}, config.UpstreamConfig{}, 0, fmt.Errorf("渠道站点地址与账号不一致")
	}
	for _, key := range channel.APIKeys {
		if key == apiKey {
			return account, channel, channelIndex, nil
		}
	}
	return SiteAccount{}, config.UpstreamConfig{}, 0, fmt.Errorf("当前渠道中不存在该 Key")
}

func remoteTokenPlaintext(ctx context.Context, site siteAdapter, account SiteAccount, accessToken string, tokenID int) (NewApiToken, string, error) {
	if tokenID <= 0 {
		return NewApiToken{}, "", fmt.Errorf("绑定当前 Key 时必须选择对应的远端 Key")
	}
	items, err := site.ListAllTokens(ctx, account, accessToken)
	if err != nil {
		return NewApiToken{}, "", err
	}
	for _, item := range items {
		if item.ID != tokenID {
			continue
		}
		plain := item.Key
		if plain == "" || IsMaskedNewApiKey(plain) {
			plain, err = site.GetTokenKey(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode, item.ID)
		}
		if err != nil {
			return NewApiToken{}, "", err
		}
		if account.SiteType == "newapi" {
			plain = normalizeNewApiPlaintextKey(plain)
		}
		if plain == "" || IsMaskedNewApiKey(plain) {
			return NewApiToken{}, "", fmt.Errorf("远端未返回完整 Key")
		}
		return item, plain, nil
	}
	return NewApiToken{}, "", fmt.Errorf("所选远端 Key 不存在")
}

func fetchBindingRatio(ctx context.Context, site siteAdapter, account SiteAccount, accessToken, group string) (float64, error) {
	ratios, err := site.FetchGroups(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode)
	if err != nil {
		return 0, fmt.Errorf("读取分组倍率失败: %w", err)
	}
	ratio, ok := ratios[group]
	if !ok || !validNewApiGroupRatio(ratio) {
		return 0, fmt.Errorf("远端分组 %q 不存在或倍率无效", group)
	}
	return ratio, nil
}

func rememberBoundKey(store *AccountStore, accountUID string, meta ProvisionedKeyMeta) error {
	return store.Patch(accountUID, func(account *SiteAccount) error {
		for i := range account.ProvisionedKeys {
			existing := &account.ProvisionedKeys[i]
			if existing.Protocol == meta.Protocol && existing.Group == meta.Group && existing.TokenID == meta.TokenID {
				*existing = meta
				return nil
			}
		}
		account.ProvisionedKeys = append(account.ProvisionedKeys, meta)
		return nil
	})
}

func channelRemoteModels(channel config.UpstreamConfig) []string {
	seen := map[string]bool{}
	models := make([]string, 0, len(channel.ModelMapping))
	for _, model := range channel.ModelMapping {
		model = strings.TrimSpace(model)
		if model != "" && !seen[model] {
			seen[model] = true
			models = append(models, model)
		}
	}
	sort.Strings(models)
	return models
}

func uniqueChannelCopyName(channels []config.UpstreamConfig, original, group string) string {
	base := strings.TrimSpace(original) + " / " + group
	name := base
	for suffix := 2; ; suffix++ {
		exists := false
		for _, channel := range channels {
			if channel.Name == name {
				exists = true
				break
			}
		}
		if !exists {
			return name
		}
		name = fmt.Sprintf("%s / %d", base, suffix)
	}
}

func addProtocolChannel(cfgManager *config.ConfigManager, apiType string, channel config.UpstreamConfig) error {
	switch apiType {
	case "Messages":
		return cfgManager.AddUpstream(channel)
	case "Chat":
		return cfgManager.AddChatUpstream(channel)
	case "Responses":
		return cfgManager.AddResponsesUpstream(channel)
	case "Gemini":
		return cfgManager.AddGeminiUpstream(channel)
	default:
		return fmt.Errorf("不支持的 apiType: %s", apiType)
	}
}
