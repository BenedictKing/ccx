package config

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// AccountChannel 是账号级管理 API 使用的渠道快照。
type AccountChannel struct {
	Kind     string
	Upstream UpstreamConfig
}

// AccountChannelUpdate 描述一次账号更新中单条协议渠道的新凭证绑定。
type AccountChannelUpdate struct {
	ChannelUID   string
	Name         string
	APIKeys      []string
	APIKeyConfig []APIKeyConfig
	BaseURLs     []string
}

// AccountChannelAddition 描述账号事务中需要新增的一条协议渠道。
type AccountChannelAddition struct {
	Kind     string
	Upstream UpstreamConfig
	// Placement 故障转移位置："front"（首位）| ""（默认末尾）
	Placement string
}

// GetAccountChannels 返回账号下全部协议渠道的深拷贝。
func (cm *ConfigManager) GetAccountChannels(accountUID string) []AccountChannel {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var result []AccountChannel
	visit := func(kind string, channels []UpstreamConfig) {
		for i := range channels {
			if channels[i].AccountUID != accountUID {
				continue
			}
			result = append(result, AccountChannel{Kind: kind, Upstream: *channels[i].Clone()})
		}
	}
	visit("messages", cm.config.Upstream)
	visit("chat", cm.config.ChatUpstream)
	visit("responses", cm.config.ResponsesUpstream)
	visit("gemini", cm.config.GeminiUpstream)
	visit("images", cm.config.ImagesUpstream)
	visit("vectors", cm.config.VectorsUpstream)
	return result
}

// GetManagedAccountCredential 返回账号凭证的副本。
func (cm *ConfigManager) GetManagedAccountCredential(accountUID, credentialUID string) (ManagedAccountCredential, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, account := range cm.config.ManagedAccounts {
		if account.AccountUID != accountUID {
			continue
		}
		for _, credential := range account.Credentials {
			if credential.CredentialUID == credentialUID {
				if credential.VolcengineAccessKey != nil {
					pair := *credential.VolcengineAccessKey
					credential.VolcengineAccessKey = &pair
				}
				if credential.MiMoConsole != nil {
					console := *credential.MiMoConsole
					credential.MiMoConsole = &console
				}
				if credential.CompshareConsole != nil {
					console := *credential.CompshareConsole
					credential.CompshareConsole = &console
				}
				credential.KimiConsole = cloneKimiConsoleCredential(credential.KimiConsole)
				return credential, true
			}
		}
	}
	return ManagedAccountCredential{}, false
}

func cloneKimiConsoleCredential(source *KimiConsoleCredential) *KimiConsoleCredential {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Usage.RateLimits = append([]KimiCodeRateLimit(nil), source.Usage.RateLimits...)
	clone.Usage.GiftBalances = append([]KimiCodeBalance(nil), source.Usage.GiftBalances...)
	if source.Usage.CodeFiveHour != nil {
		window := *source.Usage.CodeFiveHour
		clone.Usage.CodeFiveHour = &window
	}
	if source.Usage.CodeSevenDay != nil {
		window := *source.Usage.CodeSevenDay
		clone.Usage.CodeSevenDay = &window
	}
	if source.Usage.SubscriptionBalance != nil {
		balance := *source.Usage.SubscriptionBalance
		clone.Usage.SubscriptionBalance = &balance
	}
	return &clone
}

// BindManagedAccountKimiConsole 为 Kimi 托管凭证保存 Web 会话令牌和套餐快照。
func (cm *ConfigManager) BindManagedAccountKimiConsole(accountUID, credentialUID string, console KimiConsoleCredential) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for i := range cm.config.ManagedAccounts {
		account := &cm.config.ManagedAccounts[i]
		if account.AccountUID != accountUID {
			continue
		}
		if account.ProviderID != "kimi" {
			return fmt.Errorf("仅 Kimi 自动托管账号支持绑定控制台令牌")
		}
		for j := range account.Credentials {
			if account.Credentials[j].CredentialUID != credentialUID {
				continue
			}
			console.AccessToken = strings.TrimSpace(console.AccessToken)
			if console.AccessToken == "" {
				return fmt.Errorf("kimi 控制台令牌不能为空")
			}
			account.Credentials[j].KimiConsole = cloneKimiConsoleCredential(&console)
			return cm.saveConfigLocked(cm.config)
		}
		return fmt.Errorf("凭证 %s 不存在", credentialUID)
	}
	return fmt.Errorf("账号 %s 不存在", accountUID)
}

func (cm *ConfigManager) ClearManagedAccountKimiConsole(accountUID, credentialUID string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for i := range cm.config.ManagedAccounts {
		account := &cm.config.ManagedAccounts[i]
		if account.AccountUID != accountUID {
			continue
		}
		if account.ProviderID != "kimi" {
			return fmt.Errorf("仅 Kimi 自动托管账号支持绑定控制台令牌")
		}
		for j := range account.Credentials {
			if account.Credentials[j].CredentialUID == credentialUID {
				account.Credentials[j].KimiConsole = nil
				return cm.saveConfigLocked(cm.config)
			}
		}
		return fmt.Errorf("凭证 %s 不存在", credentialUID)
	}
	return fmt.Errorf("账号 %s 不存在", accountUID)
}

// BindManagedAccountMiMoConsole 绑定 MiMo 控制台 Cookie，并可原子采用 Cookie 所属的 Token Plan Key。
func (cm *ConfigManager) BindManagedAccountMiMoConsole(accountUID, credentialUID, replacementKey string, console MiMoConsoleCredential) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	var credential *ManagedAccountCredential
	for i := range cm.config.ManagedAccounts {
		account := &cm.config.ManagedAccounts[i]
		if account.AccountUID != accountUID {
			continue
		}
		if account.ProviderID != "mimo" {
			return fmt.Errorf("仅 MiMo 自动托管账号支持绑定控制台 Cookie")
		}
		for j := range account.Credentials {
			if account.Credentials[j].CredentialUID == credentialUID {
				credential = &account.Credentials[j]
				break
			}
		}
		if credential == nil {
			return fmt.Errorf("凭证 %s 不存在", credentialUID)
		}
		break
	}
	if credential == nil {
		return fmt.Errorf("账号 %s 不存在", accountUID)
	}
	replacementKey = strings.TrimSpace(replacementKey)
	oldKey := credential.APIKey
	if replacementKey != "" && replacementKey != oldKey {
		for _, account := range cm.config.ManagedAccounts {
			if account.AccountUID != accountUID {
				continue
			}
			for _, existing := range account.Credentials {
				if existing.CredentialUID != credentialUID && existing.APIKey == replacementKey {
					return fmt.Errorf("cookie 所属 Key 已存在于当前账号")
				}
			}
		}
		replaceKey := func(channels []UpstreamConfig) {
			for i := range channels {
				channel := &channels[i]
				if channel.AccountUID != accountUID {
					continue
				}
				replaced := false
				for j := range channel.APIKeys {
					if channel.APIKeys[j] == oldKey {
						channel.APIKeys[j] = replacementKey
						replaced = true
					}
				}
				for j := range channel.APIKeyConfigs {
					cfg := &channel.APIKeyConfigs[j]
					if cfg.CredentialUID == credentialUID || cfg.Key == oldKey {
						cfg.Key = replacementKey
						cfg.CredentialUID = credentialUID
						replaced = true
					}
				}
				if replaced && oldKey != "" && !accountContainsString(channel.HistoricalAPIKeys, oldKey) {
					channel.HistoricalAPIKeys = append(channel.HistoricalAPIKeys, oldKey)
				}
			}
		}
		replaceKey(cm.config.Upstream)
		replaceKey(cm.config.ChatUpstream)
		replaceKey(cm.config.ResponsesUpstream)
		replaceKey(cm.config.GeminiUpstream)
		replaceKey(cm.config.ImagesUpstream)
		replaceKey(cm.config.VectorsUpstream)
		credential.APIKey = replacementKey
	}
	console.Cookie = strings.TrimSpace(console.Cookie)
	credential.MiMoConsole = &console
	return cm.saveConfigLocked(cm.config)
}

func (cm *ConfigManager) ClearManagedAccountMiMoConsole(accountUID, credentialUID string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for i := range cm.config.ManagedAccounts {
		account := &cm.config.ManagedAccounts[i]
		if account.AccountUID != accountUID {
			continue
		}
		for j := range account.Credentials {
			if account.Credentials[j].CredentialUID == credentialUID {
				account.Credentials[j].MiMoConsole = nil
				return cm.saveConfigLocked(cm.config)
			}
		}
		return fmt.Errorf("凭证 %s 不存在", credentialUID)
	}
	return fmt.Errorf("账号 %s 不存在", accountUID)
}

// BindManagedAccountCompshareConsole 为优云智算托管凭证保存控制台 Cookie 和套餐快照，
// 并将套餐并发上限同步到该凭证在全部协议渠道中的 Key 级限速配置。
func (cm *ConfigManager) BindManagedAccountCompshareConsole(accountUID, credentialUID string, console CompshareConsoleCredential) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for i := range cm.config.ManagedAccounts {
		account := &cm.config.ManagedAccounts[i]
		if account.AccountUID != accountUID {
			continue
		}
		if account.ProviderID != "compshare" {
			return fmt.Errorf("仅优云智算自动托管账号支持绑定控制台 Cookie")
		}
		for j := range account.Credentials {
			if account.Credentials[j].CredentialUID != credentialUID {
				continue
			}
			if console.ConcurrencyLimit > 0 {
				maxConcurrent := int(console.ConcurrencyLimit)
				if int64(maxConcurrent) != console.ConcurrencyLimit {
					return fmt.Errorf("优云智算套餐并发上限超出支持范围")
				}
				if !cm.applyManagedCredentialMaxConcurrentLocked(accountUID, credentialUID, account.Credentials[j].APIKey, maxConcurrent) {
					return fmt.Errorf("凭证 %s 未绑定到任何渠道", credentialUID)
				}
			}
			console.Cookie = strings.TrimSpace(console.Cookie)
			account.Credentials[j].CompshareConsole = &console
			return cm.saveConfigLocked(cm.config)
		}
		return fmt.Errorf("凭证 %s 不存在", credentialUID)
	}
	return fmt.Errorf("账号 %s 不存在", accountUID)
}

// applyManagedCredentialMaxConcurrentLocked 更新账号全部协议渠道中的同一凭证。
// 调用方必须持有 cm.mu；返回值表示至少找到了一条对应的 Key 配置。
func (cm *ConfigManager) applyManagedCredentialMaxConcurrentLocked(accountUID, credentialUID, apiKey string, maxConcurrent int) bool {
	updated := false
	matchesCredential := func(keyConfig APIKeyConfig) bool {
		if keyConfig.CredentialUID != "" {
			return keyConfig.CredentialUID == credentialUID
		}
		return apiKey != "" && keyConfig.Key == apiKey
	}
	apply := func(channels []UpstreamConfig) {
		for i := range channels {
			channel := &channels[i]
			if channel.AccountUID != accountUID || channel.ProviderID != "compshare" {
				continue
			}
			for j := range channel.APIKeyConfigs {
				if matchesCredential(channel.APIKeyConfigs[j]) {
					channel.APIKeyConfigs[j].RateLimitMaxConcurrent = maxConcurrent
					updated = true
				}
			}
			for j := range channel.DisabledAPIKeys {
				keyConfig := channel.DisabledAPIKeys[j].Config
				if keyConfig != nil && matchesCredential(*keyConfig) {
					keyConfig.RateLimitMaxConcurrent = maxConcurrent
					updated = true
				}
			}
		}
	}
	apply(cm.config.Upstream)
	apply(cm.config.ChatUpstream)
	apply(cm.config.ResponsesUpstream)
	apply(cm.config.GeminiUpstream)
	apply(cm.config.ImagesUpstream)
	apply(cm.config.VectorsUpstream)
	return updated
}

func (cm *ConfigManager) ClearManagedAccountCompshareConsole(accountUID, credentialUID string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for i := range cm.config.ManagedAccounts {
		account := &cm.config.ManagedAccounts[i]
		if account.AccountUID != accountUID {
			continue
		}
		if account.ProviderID != "compshare" {
			return fmt.Errorf("仅优云智算自动托管账号支持绑定控制台 Cookie")
		}
		for j := range account.Credentials {
			if account.Credentials[j].CredentialUID == credentialUID {
				account.Credentials[j].CompshareConsole = nil
				return cm.saveConfigLocked(cm.config)
			}
		}
		return fmt.Errorf("凭证 %s 不存在", credentialUID)
	}
	return fmt.Errorf("账号 %s 不存在", accountUID)
}

func accountContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// SetManagedAccountVolcengineAccessKey 为一个推理 Key 绑定火山云签名凭证。
func (cm *ConfigManager) SetManagedAccountVolcengineAccessKey(accountUID, credentialUID, accessKeyID, secretAccessKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	accessKeyID = strings.TrimSpace(accessKeyID)
	secretAccessKey = strings.TrimSpace(secretAccessKey)
	if accessKeyID == "" || secretAccessKey == "" {
		return fmt.Errorf("access Key ID 和 Secret Access Key 均不能为空")
	}
	for i := range cm.config.ManagedAccounts {
		account := &cm.config.ManagedAccounts[i]
		if account.AccountUID != accountUID {
			continue
		}
		for j := range account.Credentials {
			if account.Credentials[j].CredentialUID != credentialUID {
				continue
			}
			account.Credentials[j].VolcengineAccessKey = &VolcengineAccessKeyPair{
				AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey,
			}
			return cm.saveConfigLocked(cm.config)
		}
		return fmt.Errorf("凭证 %s 不存在", credentialUID)
	}
	return fmt.Errorf("账号 %s 不存在", accountUID)
}

// ClearManagedAccountVolcengineAccessKey 删除推理 Key 绑定的火山云签名凭证。
func (cm *ConfigManager) ClearManagedAccountVolcengineAccessKey(accountUID, credentialUID string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for i := range cm.config.ManagedAccounts {
		account := &cm.config.ManagedAccounts[i]
		if account.AccountUID != accountUID {
			continue
		}
		for j := range account.Credentials {
			if account.Credentials[j].CredentialUID != credentialUID {
				continue
			}
			account.Credentials[j].VolcengineAccessKey = nil
			return cm.saveConfigLocked(cm.config)
		}
		return fmt.Errorf("凭证 %s 不存在", credentialUID)
	}
	return fmt.Errorf("账号 %s 不存在", accountUID)
}

// SetManagedAccountVolcenginePlan 保存由火山管控面自动识别出的套餐信息。
func (cm *ConfigManager) SetManagedAccountVolcenginePlan(accountUID, credentialUID, plan, tier, status string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for i := range cm.config.ManagedAccounts {
		account := &cm.config.ManagedAccounts[i]
		if account.AccountUID != accountUID {
			continue
		}
		for j := range account.Credentials {
			credential := &account.Credentials[j]
			if credential.CredentialUID != credentialUID || credential.VolcengineAccessKey == nil {
				continue
			}
			credential.VolcengineAccessKey.Plan = strings.TrimSpace(plan)
			credential.VolcengineAccessKey.PlanTier = strings.TrimSpace(tier)
			credential.VolcengineAccessKey.PlanStatus = strings.TrimSpace(status)
			return cm.saveConfigLocked(cm.config)
		}
		return fmt.Errorf("凭证 %s 不存在或未绑定火山 Access Key", credentialUID)
	}
	return fmt.Errorf("账号 %s 不存在", accountUID)
}

// SetManagedAccountVolcenginePlanUsage 保存火山管控面查询到的套餐用量快照。
func (cm *ConfigManager) SetManagedAccountVolcenginePlanUsage(accountUID, credentialUID string, usage *VolcenginePlanUsage) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for i := range cm.config.ManagedAccounts {
		account := &cm.config.ManagedAccounts[i]
		if account.AccountUID != accountUID {
			continue
		}
		for j := range account.Credentials {
			credential := &account.Credentials[j]
			if credential.CredentialUID != credentialUID || credential.VolcengineAccessKey == nil {
				continue
			}
			credential.VolcengineAccessKey.Usage = usage
			return cm.saveConfigLocked(cm.config)
		}
		return fmt.Errorf("凭证 %s 不存在或未绑定火山 Access Key", credentialUID)
	}
	return fmt.Errorf("账号 %s 不存在", accountUID)
}

// mergeManagedProviderAccounts 将同一官方 provider 的历史自动托管账号归并为一个凭证池。
// provider 模板已经描述完整协议 routes，因此账号身份应是 provider 级，而不是每个 Key 一份。
func (cm *ConfigManager) mergeManagedProviderAccounts() bool {
	canonicalUID := make(map[string]string)
	canonicalName := make(map[string]string)
	providerAccounts := make(map[string]map[string]bool)
	for _, account := range cm.config.ManagedAccounts {
		if account.ProviderID == "" || account.AccountUID == "" {
			continue
		}
		// 保留最后创建的账号身份和名称，符合用户最近一次添加时看到的名称。
		canonicalUID[account.ProviderID] = account.AccountUID
		canonicalName[account.ProviderID] = account.Name
		if providerAccounts[account.ProviderID] == nil {
			providerAccounts[account.ProviderID] = make(map[string]bool)
		}
		providerAccounts[account.ProviderID][account.AccountUID] = true
	}

	updated := false
	providerKinds := make(map[string]map[string]bool)
	collectKinds := func(channels []UpstreamConfig, kind string) {
		for i := range channels {
			channel := &channels[i]
			if channel.AutoManaged && channel.ProviderID != "" {
				if providerKinds[channel.ProviderID] == nil {
					providerKinds[channel.ProviderID] = make(map[string]bool)
				}
				providerKinds[channel.ProviderID][kind] = true
				if providerAccounts[channel.ProviderID] == nil {
					providerAccounts[channel.ProviderID] = make(map[string]bool)
				}
				if channel.AccountUID != "" {
					providerAccounts[channel.ProviderID][channel.AccountUID] = true
				}
			}
		}
	}
	collectKinds(cm.config.Upstream, "messages")
	collectKinds(cm.config.ChatUpstream, "chat")
	collectKinds(cm.config.ResponsesUpstream, "responses")
	collectKinds(cm.config.GeminiUpstream, "gemini")
	collectKinds(cm.config.ImagesUpstream, "images")
	collectKinds(cm.config.VectorsUpstream, "vectors")

	mergeKind := func(channels []UpstreamConfig, kind string) []UpstreamConfig {
		out := make([]UpstreamConfig, 0, len(channels))
		providerIndex := make(map[string]int)
		providerHasCanonicalRoute := make(map[string]bool)
		for i := range channels {
			channel := channels[i]
			if !channel.AutoManaged || channel.ProviderID == "" {
				out = append(out, channel)
				continue
			}
			providerID := channel.ProviderID
			if len(providerAccounts[providerID]) < 2 {
				out = append(out, channel)
				continue
			}
			originalAccountUID := channel.AccountUID
			uid := canonicalUID[providerID]
			if uid == "" {
				uid = channel.AccountUID
				if uid == "" {
					uid = GenerateAccountUID()
				}
				canonicalUID[providerID] = uid
			}
			baseName := canonicalName[providerID]
			if baseName == "" {
				baseName = managedAccountName(channel.Name)
				canonicalName[providerID] = baseName
			}
			targetName := baseName
			if len(providerKinds[providerID]) > 1 {
				targetName += accountChannelSuffix(kind)
			}
			if channel.AccountUID != uid || channel.Name != targetName {
				updated = true
			}
			channel.AccountUID = uid
			channel.Name = targetName
			channel.APIKeyConfigs = normalizeAPIKeyConfigs(channel.APIKeys, channel.APIKeyConfigs)
			for j := range channel.APIKeyConfigs {
				if strings.TrimSpace(channel.APIKeyConfigs[j].Key) != "" {
					channel.APIKeyConfigs[j].CredentialUID = GenerateCredentialUID(uid, channel.APIKeyConfigs[j].Key)
				}
			}

			idx, exists := providerIndex[providerID]
			if !exists {
				providerIndex[providerID] = len(out)
				providerHasCanonicalRoute[providerID] = originalAccountUID == uid
				out = append(out, channel)
				continue
			}

			merged := &out[idx]
			if originalAccountUID == uid && !providerHasCanonicalRoute[providerID] {
				previous := *merged
				*merged = channel
				channel = previous
				providerHasCanonicalRoute[providerID] = true
			}
			configs := make(map[string]APIKeyConfig, len(merged.APIKeyConfigs)+len(channel.APIKeyConfigs))
			uidOnlyConfigs := make(map[string]APIKeyConfig)
			collectConfig := func(cfg APIKeyConfig) {
				if strings.TrimSpace(cfg.Key) == "" {
					if cfg.CredentialUID != "" {
						uidOnlyConfigs[cfg.CredentialUID] = cfg
					}
					return
				}
				cfg.CredentialUID = GenerateCredentialUID(uid, cfg.Key)
				configs[cfg.Key] = cfg
				delete(uidOnlyConfigs, cfg.CredentialUID)
			}
			for _, cfg := range merged.APIKeyConfigs {
				collectConfig(cfg)
			}
			for _, cfg := range channel.APIKeyConfigs {
				collectConfig(cfg)
			}
			merged.APIKeys = deduplicateStrings(append(merged.APIKeys, channel.APIKeys...))
			merged.APIKeyConfigs = make([]APIKeyConfig, 0, len(merged.APIKeys)+len(uidOnlyConfigs))
			for _, key := range merged.APIKeys {
				cfg := configs[key]
				cfg.Key = key
				if strings.TrimSpace(key) != "" {
					cfg.CredentialUID = GenerateCredentialUID(uid, key)
					delete(uidOnlyConfigs, cfg.CredentialUID)
				}
				merged.APIKeyConfigs = append(merged.APIKeyConfigs, cfg)
			}
			for _, cfg := range uidOnlyConfigs {
				merged.APIKeyConfigs = append(merged.APIKeyConfigs, cfg)
			}
			incomingBaseURLs := append([]string(nil), channel.BaseURLs...)
			if channel.BaseURL != "" {
				incomingBaseURLs = append([]string{channel.BaseURL}, incomingBaseURLs...)
			}
			merged.BaseURLs = deduplicateBaseURLs(append(merged.BaseURLs, incomingBaseURLs...), merged.ServiceType)
			if merged.BaseURL != "" {
				merged.BaseURLs = deduplicateBaseURLs(append([]string{merged.BaseURL}, merged.BaseURLs...), merged.ServiceType)
			}
			if len(merged.BaseURLs) > 0 {
				merged.BaseURL = merged.BaseURLs[0]
			}
			// 合并状态时保持 status/source 成对一致；任一路由的人工暂停都优先于 active，
			// 防止被合并进来的 active 路由覆盖 canonical 的人工暂停。
			if (merged.Status == "suspended" && merged.SuspensionSource == SuspensionSourceManual) ||
				(channel.Status == "suspended" && channel.SuspensionSource == SuspensionSourceManual) {
				applyChannelStatusTransition(merged, "suspended", SuspensionSourceManual)
			} else if channel.Status == "active" {
				applyChannelStatusTransition(merged, "active", "")
			}
			// 兼容性种子随渠道一起合并：被合并掉的渠道若带有历史手工配置降级来的种子，
			// 不合并就会连同渠道一起丢弃。已存在的种子优先（canonical 渠道的观察更可信）。
			for trait, entry := range channel.CompatSeeds {
				if merged.CompatSeeds == nil {
					merged.CompatSeeds = make(map[string]CompatSeedEntry, len(channel.CompatSeeds))
				}
				if _, exists := merged.CompatSeeds[trait]; !exists {
					merged.CompatSeeds[trait] = entry
				}
			}
			updated = true
		}
		return out
	}

	cm.config.Upstream = mergeKind(cm.config.Upstream, "messages")
	cm.config.ChatUpstream = mergeKind(cm.config.ChatUpstream, "chat")
	cm.config.ResponsesUpstream = mergeKind(cm.config.ResponsesUpstream, "responses")
	cm.config.GeminiUpstream = mergeKind(cm.config.GeminiUpstream, "gemini")
	cm.config.ImagesUpstream = mergeKind(cm.config.ImagesUpstream, "images")
	cm.config.VectorsUpstream = mergeKind(cm.config.VectorsUpstream, "vectors")
	if updated {
		accounts := cm.config.ManagedAccounts[:0]
		mergedCredentials := make(map[string][]ManagedAccountCredential)
		credentialSeen := make(map[string]map[string]bool)
		for _, account := range cm.config.ManagedAccounts {
			canonical := canonicalUID[account.ProviderID]
			if canonical == "" {
				canonical = account.AccountUID
			}
			if credentialSeen[canonical] == nil {
				credentialSeen[canonical] = make(map[string]bool)
			}
			for _, credential := range account.Credentials {
				if credential.CredentialUID == "" || credentialSeen[canonical][credential.CredentialUID] {
					continue
				}
				mergedCredentials[canonical] = append(mergedCredentials[canonical], credential)
				credentialSeen[canonical][credential.CredentialUID] = true
			}
			if account.AccountUID != canonical {
				continue
			}
			account.Credentials = mergedCredentials[canonical]
			accounts = append(accounts, account)
		}
		cm.config.ManagedAccounts = accounts
		// 渠道带明文 Key 时按合并结果重建凭证池；持久化渠道只有 UID、Key 尚未补水时，
		// 直接同步会清空已保留的账号凭证，因此只在至少存在运行时 Key 时执行。
		hasRuntimeKeys := false
		for _, channels := range [][]UpstreamConfig{cm.config.Upstream, cm.config.ChatUpstream, cm.config.ResponsesUpstream, cm.config.GeminiUpstream, cm.config.ImagesUpstream, cm.config.VectorsUpstream} {
			for i := range channels {
				if channels[i].AutoManaged && len(channels[i].APIKeys) > 0 {
					hasRuntimeKeys = true
					break
				}
			}
		}
		if hasRuntimeKeys {
			cm.config.syncManagedAccountsFromChannels()
		}
		log.Printf("[Config-AccountMerge] 已按 provider 合并历史自动托管账号")
	}
	return updated
}

// UpdateAccountChannels 原子更新账号下所有协议渠道的 Key -> BaseURL 绑定。
func (cm *ConfigManager) UpdateAccountChannels(accountUID string, updates []AccountChannelUpdate) error {
	return cm.ApplyAccountChannelChanges(accountUID, updates, nil)
}

// ApplyAccountChannelChanges 在一次配置写入中更新现有渠道并新增缺失协议渠道。
func (cm *ConfigManager) ApplyAccountChannelChanges(accountUID string, updates []AccountChannelUpdate, additions []AccountChannelAddition) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	next := cm.config.deepCopy()
	if err := applyAccountChannelChanges(&next, accountUID, updates, additions); err != nil {
		return err
	}
	return cm.saveConfigLocked(next)
}

func applyAccountChannelChanges(cfg *Config, accountUID string, updates []AccountChannelUpdate, additions []AccountChannelAddition) error {
	if cfg == nil {
		return fmt.Errorf("配置为空")
	}
	accountUID = strings.TrimSpace(accountUID)
	if accountUID == "" {
		return fmt.Errorf("accountUID 不能为空")
	}
	byChannel := make(map[string]AccountChannelUpdate, len(updates))
	for _, update := range updates {
		if update.ChannelUID == "" {
			return fmt.Errorf("账号 %s 包含空 channelUID 更新", accountUID)
		}
		if _, exists := byChannel[update.ChannelUID]; exists {
			return fmt.Errorf("账号 %s 包含重复渠道更新: %s", accountUID, update.ChannelUID)
		}
		byChannel[update.ChannelUID] = update
	}
	known := 0
	total := 0
	providerID := ""
	providerKnown := false
	providerMismatch := false
	countKnown := func(channels []UpstreamConfig) {
		for i := range channels {
			if channels[i].AccountUID == accountUID {
				total++
				if !providerKnown {
					providerID = channels[i].ProviderID
					providerKnown = true
				} else if channels[i].ProviderID != providerID {
					providerMismatch = true
				}
				if _, ok := byChannel[channels[i].ChannelUID]; ok {
					known++
				}
			}
		}
	}
	countKnown(cfg.Upstream)
	countKnown(cfg.ChatUpstream)
	countKnown(cfg.ResponsesUpstream)
	countKnown(cfg.GeminiUpstream)
	countKnown(cfg.ImagesUpstream)
	countKnown(cfg.VectorsUpstream)
	if providerMismatch {
		return fmt.Errorf("账号 %s 包含不一致的 provider", accountUID)
	}
	for _, addition := range additions {
		additionProvider := strings.TrimSpace(addition.Upstream.ProviderID)
		if !providerKnown {
			providerID = additionProvider
			providerKnown = true
		} else if additionProvider != providerID {
			return fmt.Errorf("账号 %s 的新增渠道 provider 不一致", accountUID)
		}
	}
	if total == 0 && len(additions) == 0 {
		return fmt.Errorf("账号 %s 不存在或没有可更新渠道", accountUID)
	}
	if total == 0 && len(updates) != 0 {
		return fmt.Errorf("账号 %s 不存在，不能应用渠道更新", accountUID)
	}
	if total > 0 && (known != total || len(updates) != total) {
		return fmt.Errorf("账号 %s 渠道更新不完整: matched=%d total=%d updates=%d", accountUID, known, total, len(updates))
	}

	matched := 0
	apply := func(channels []UpstreamConfig) {
		for i := range channels {
			channel := &channels[i]
			if channel.AccountUID != accountUID {
				continue
			}
			update, ok := byChannel[channel.ChannelUID]
			if !ok {
				continue
			}
			channel.Name = update.Name
			channel.APIKeys = deduplicateStrings(update.APIKeys)
			channel.APIKeyConfigs = normalizeAPIKeyConfigs(channel.APIKeys, update.APIKeyConfig)
			for j := range channel.APIKeyConfigs {
				if channel.APIKeyConfigs[j].CredentialUID == "" {
					channel.APIKeyConfigs[j].CredentialUID = GenerateCredentialUID(accountUID, channel.APIKeyConfigs[j].Key)
				}
			}
			channel.BaseURLs = deduplicateBaseURLs(update.BaseURLs, channel.ServiceType)
			if len(channel.BaseURLs) > 0 {
				channel.BaseURL = channel.BaseURLs[0]
			}
			resumeAutoNoKeysChannel(channel)
			matched++
		}
	}
	apply(cfg.Upstream)
	apply(cfg.ChatUpstream)
	apply(cfg.ResponsesUpstream)
	apply(cfg.GeminiUpstream)
	apply(cfg.ImagesUpstream)
	apply(cfg.VectorsUpstream)

	if matched != known {
		return fmt.Errorf("账号 %s 渠道更新计数异常: matched=%d known=%d", accountUID, matched, known)
	}
	for i := range cfg.ManagedAccounts {
		if cfg.ManagedAccounts[i].AccountUID == accountUID && len(updates) > 0 {
			cfg.ManagedAccounts[i].Name = managedAccountName(updates[0].Name)
		}
	}
	for _, addition := range additions {
		if err := appendAccountChannelAddition(cfg, accountUID, addition); err != nil {
			return err
		}
	}
	return nil
}

func appendAccountChannelAddition(cfg *Config, accountUID string, addition AccountChannelAddition) error {
	channels, fallback, err := accountChannelSlice(cfg, addition.Kind)
	if err != nil {
		return err
	}
	upstream := *addition.Upstream.Clone()
	if upstream.AccountUID != accountUID || !upstream.AutoManaged {
		return fmt.Errorf("新增渠道必须属于自动托管账号 %s", accountUID)
	}
	if strings.TrimSpace(upstream.ChannelUID) == "" || strings.TrimSpace(upstream.Name) == "" {
		return fmt.Errorf("新增 %s 渠道缺少 name 或 channelUID", addition.Kind)
	}
	if len(upstream.APIKeys) == 0 {
		return fmt.Errorf("新增 %s 渠道缺少 API Key", addition.Kind)
	}
	for _, existing := range *channels {
		if existing.Name == upstream.Name {
			return fmt.Errorf("渠道名称 '%s' 已存在", upstream.Name)
		}
	}
	if configHasChannelUID(cfg, upstream.ChannelUID) {
		return fmt.Errorf("channelUID %s 已存在", upstream.ChannelUID)
	}

	upstream.ServiceType = normalizeUpstreamServiceType(upstream.ServiceType, fallback)
	switch addition.Kind {
	case "images":
		upstream.ServiceType, err = normalizeImagesServiceType(upstream.ServiceType)
	case "vectors":
		upstream.ServiceType, err = normalizeVectorsServiceType(upstream.ServiceType)
	}
	if err != nil {
		return err
	}
	upstream.AuthHeader, err = applyAuthHeader(upstream.AuthHeader)
	if err != nil {
		return err
	}
	if err := validateRequestTimeoutMs(upstream.RequestTimeoutMs); err != nil {
		return err
	}
	if err := validateResponseHeaderTimeoutMs(upstream.ResponseHeaderTimeoutMs); err != nil {
		return err
	}
	if upstream.RateLimitRPM < 0 || upstream.RateLimitBurst < 0 || upstream.RateLimitMaxConcurrent < 0 {
		return fmt.Errorf("限速参数不能为负数")
	}
	if err := validateStreamTimeouts(upstream.StreamFirstContentTimeoutMs, upstream.StreamInactivityTimeoutMs, upstream.StreamToolCallIdleTimeoutMs); err != nil {
		return err
	}
	if upstream.Status == "" {
		upstream.Status = "active"
	}
	upstream.APIKeys = deduplicateStrings(upstream.APIKeys)
	upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)
	for i := range upstream.APIKeyConfigs {
		if upstream.APIKeyConfigs[i].CredentialUID == "" {
			upstream.APIKeyConfigs[i].CredentialUID = GenerateCredentialUID(accountUID, upstream.APIKeyConfigs[i].Key)
		}
	}
	upstream.BaseURL = utils.CanonicalBaseURL(upstream.BaseURL, upstream.ServiceType)
	upstream.BaseURLs = deduplicateBaseURLs(upstream.BaseURLs, upstream.ServiceType)
	applyDefaultBaseURL(&upstream)
	// 账号同步渠道默认追加到故障转移序列末尾，调用方可用 Placement 指定 "front"
	assignChannelPriority(*channels, &upstream, addition.Placement)
	*channels = append([]UpstreamConfig{upstream}, (*channels)...)
	return nil
}

func accountChannelSlice(cfg *Config, kind string) (*[]UpstreamConfig, string, error) {
	switch kind {
	case "messages":
		return &cfg.Upstream, "claude", nil
	case "chat":
		return &cfg.ChatUpstream, "openai", nil
	case "responses":
		return &cfg.ResponsesUpstream, "responses", nil
	case "gemini":
		return &cfg.GeminiUpstream, "gemini", nil
	case "images":
		return &cfg.ImagesUpstream, "openai", nil
	case "vectors":
		return &cfg.VectorsUpstream, "openai", nil
	default:
		return nil, "", fmt.Errorf("不支持的渠道类型: %s", kind)
	}
}

func configHasChannelUID(cfg *Config, channelUID string) bool {
	found := false
	visit := func(channels []UpstreamConfig) {
		for _, channel := range channels {
			if channel.ChannelUID == channelUID {
				found = true
				return
			}
		}
	}
	visit(cfg.Upstream)
	visit(cfg.ChatUpstream)
	visit(cfg.ResponsesUpstream)
	visit(cfg.GeminiUpstream)
	visit(cfg.ImagesUpstream)
	visit(cfg.VectorsUpstream)
	return found
}

// DeleteAccountChannels 原子删除账号下全部协议渠道，返回被删除的 channelUid。
func (cm *ConfigManager) DeleteAccountChannels(accountUID string) ([]string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	var removed []string
	filter := func(channels []UpstreamConfig) []UpstreamConfig {
		kept := channels[:0]
		for _, channel := range channels {
			if channel.AccountUID == accountUID {
				removed = append(removed, channel.ChannelUID)
				continue
			}
			kept = append(kept, channel)
		}
		return kept
	}
	cm.config.Upstream = filter(cm.config.Upstream)
	cm.config.ChatUpstream = filter(cm.config.ChatUpstream)
	cm.config.ResponsesUpstream = filter(cm.config.ResponsesUpstream)
	cm.config.GeminiUpstream = filter(cm.config.GeminiUpstream)
	cm.config.ImagesUpstream = filter(cm.config.ImagesUpstream)
	cm.config.VectorsUpstream = filter(cm.config.VectorsUpstream)
	if len(removed) == 0 {
		return nil, fmt.Errorf("账号 %s 不存在", accountUID)
	}
	accounts := cm.config.ManagedAccounts[:0]
	for _, account := range cm.config.ManagedAccounts {
		if account.AccountUID != accountUID {
			accounts = append(accounts, account)
		}
	}
	cm.config.ManagedAccounts = accounts
	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}
	return removed, nil
}

// RenameManagedAccount 原子重命名账号及其全部协议渠道。
func (cm *ConfigManager) RenameManagedAccount(accountUID, baseName string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	baseName = strings.TrimSpace(baseName)
	if baseName == "" {
		return fmt.Errorf("账号名称不能为空")
	}
	total := 0
	count := func(channels []UpstreamConfig) {
		for i := range channels {
			if channels[i].AccountUID == accountUID {
				total++
			}
		}
	}
	count(cm.config.Upstream)
	count(cm.config.ChatUpstream)
	count(cm.config.ResponsesUpstream)
	count(cm.config.GeminiUpstream)
	count(cm.config.ImagesUpstream)
	count(cm.config.VectorsUpstream)
	matched := 0
	rename := func(kind string, channels []UpstreamConfig) {
		for i := range channels {
			if channels[i].AccountUID == accountUID {
				channels[i].Name = baseName
				if total > 1 {
					channels[i].Name += accountChannelSuffix(kind)
				}
				matched++
			}
		}
	}
	rename("messages", cm.config.Upstream)
	rename("chat", cm.config.ChatUpstream)
	rename("responses", cm.config.ResponsesUpstream)
	rename("gemini", cm.config.GeminiUpstream)
	rename("images", cm.config.ImagesUpstream)
	rename("vectors", cm.config.VectorsUpstream)
	if matched == 0 {
		return fmt.Errorf("账号 %s 不存在", accountUID)
	}
	for i := range cm.config.ManagedAccounts {
		if cm.config.ManagedAccounts[i].AccountUID == accountUID {
			cm.config.ManagedAccounts[i].Name = baseName
		}
	}
	return cm.saveConfigLocked(cm.config)
}

func accountChannelSuffix(kind string) string {
	switch kind {
	case "messages":
		return "-claude"
	case "chat":
		return "-chat"
	case "responses":
		return "-codex"
	case "gemini":
		return "-gemini"
	default:
		return "-" + kind
	}
}

func (c *Config) syncManagedAccountsFromChannels() {
	existingOrder := append([]ManagedAccountConfig(nil), c.ManagedAccounts...)
	existingCredentials := make(map[string]map[string]ManagedAccountCredential, len(c.ManagedAccounts))
	accounts := make(map[string]ManagedAccountConfig, len(c.ManagedAccounts))
	for _, account := range c.ManagedAccounts {
		byUID := make(map[string]ManagedAccountCredential, len(account.Credentials))
		for _, credential := range account.Credentials {
			byUID[credential.CredentialUID] = credential
		}
		existingCredentials[account.AccountUID] = byUID
		account.Credentials = nil
		accounts[account.AccountUID] = account
	}
	credentialSeen := make(map[string]map[string]bool, len(accounts))
	visit := func(channels []UpstreamConfig) {
		for i := range channels {
			channel := &channels[i]
			if !channel.AutoManaged || channel.AccountUID == "" || channel.ProviderID == "" {
				continue
			}
			account := accounts[channel.AccountUID]
			account.AccountUID = channel.AccountUID
			account.ProviderID = channel.ProviderID
			if account.Name == "" {
				account.Name = managedAccountName(channel.Name)
			}
			seen := credentialSeen[channel.AccountUID]
			if seen == nil {
				seen = make(map[string]bool, len(channel.APIKeys)+len(channel.DisabledAPIKeys))
				credentialSeen[channel.AccountUID] = seen
			}
			addCredential := func(apiKey string, cfg *APIKeyConfig) {
				uid := ""
				if cfg != nil && cfg.CredentialUID != "" {
					uid = cfg.CredentialUID
				}
				if uid == "" {
					uid = channel.CredentialUIDForKey(apiKey)
				}
				if uid == "" || seen[uid] {
					return
				}
				credential := existingCredentials[channel.AccountUID][uid]
				credential.CredentialUID = uid
				if apiKey != "" {
					credential.APIKey = apiKey
				}
				account.Credentials = append(account.Credentials, credential)
				seen[uid] = true
			}
			for _, apiKey := range channel.APIKeys {
				addCredential(apiKey, nil)
			}
			// 持久化托管路由可能只有 CredentialUID；即使同一路由已混入新的运行时 Key，
			// 这些 legacy 绑定仍属于账号凭证池，不能在同步时被清空。
			for j := range channel.APIKeyConfigs {
				if channel.APIKeyConfigs[j].Key == "" && channel.APIKeyConfigs[j].CredentialUID != "" {
					addCredential("", &channel.APIKeyConfigs[j])
				}
			}
			// 被余额/限额不足拉黑的托管 Key 仍属于该账号凭证池，
			// 不能因暂时移出 APIKeys 就丢失其 Console token / AccessKey / 用量快照。
			// 不能因暂时移出 APIKeys 就丢失其 Console token / AccessKey / 用量快照。
			for _, dk := range channel.DisabledAPIKeys {
				if dk.Key != "" {
					addCredential(dk.Key, dk.Config)
				}
			}
			accounts[channel.AccountUID] = account
		}
	}
	visit(c.Upstream)
	visit(c.ChatUpstream)
	visit(c.ResponsesUpstream)
	visit(c.GeminiUpstream)
	visit(c.ImagesUpstream)
	visit(c.VectorsUpstream)
	c.ManagedAccounts = c.ManagedAccounts[:0]
	seen := make(map[string]bool, len(accounts))
	for _, existing := range existingOrder {
		if account, ok := accounts[existing.AccountUID]; ok {
			c.ManagedAccounts = append(c.ManagedAccounts, account)
			seen[existing.AccountUID] = true
		}
	}
	for uid, account := range accounts {
		if !seen[uid] {
			c.ManagedAccounts = append(c.ManagedAccounts, account)
		}
	}
}

func (c *Config) hydrateManagedAccountCredentials() bool {
	credentials := make(map[string]map[string]string, len(c.ManagedAccounts))
	orderedUIDs := make(map[string][]string, len(c.ManagedAccounts))
	for _, account := range c.ManagedAccounts {
		byUID := make(map[string]string, len(account.Credentials))
		for _, credential := range account.Credentials {
			byUID[credential.CredentialUID] = credential.APIKey
			orderedUIDs[account.AccountUID] = append(orderedUIDs[account.AccountUID], credential.CredentialUID)
		}
		credentials[account.AccountUID] = byUID
	}
	modified := false
	visit := func(channels []UpstreamConfig) {
		for i := range channels {
			channel := &channels[i]
			byUID := credentials[channel.AccountUID]
			if len(byUID) == 0 {
				continue
			}
			// 历史配置可能丢失单个路由的 APIKeyConfigs（其余路由仍有 credentialUid 绑定），
			// 该路由会退化为无 Key 可用且发现任务产出空端点。provider 托管渠道按账号
			// 凭证顺序回填绑定，恢复调度与发现能力。
			if channel.AutoManaged && channel.ProviderID != "" && len(channel.APIKeyConfigs) == 0 {
				for _, credentialUID := range orderedUIDs[channel.AccountUID] {
					channel.APIKeyConfigs = append(channel.APIKeyConfigs, APIKeyConfig{CredentialUID: credentialUID})
				}
				modified = true
			}
			previousKeys := append([]string(nil), channel.APIKeys...)
			channel.APIKeys = channel.APIKeys[:0]
			for j := range channel.APIKeyConfigs {
				if apiKey := strings.TrimSpace(byUID[channel.APIKeyConfigs[j].CredentialUID]); apiKey != "" {
					if channel.APIKeyConfigs[j].Key != apiKey {
						// 明文 Key 仅为运行时补水，保存时会再次剥离，不属于持久化结构迁移。
						channel.APIKeyConfigs[j].Key = apiKey
					}
					channel.APIKeys = append(channel.APIKeys, apiKey)
				}
			}
			_ = previousKeys // APIKeys 同样是运行时补水，不触发 load-save-strip 循环。
		}
	}
	visit(c.Upstream)
	visit(c.ChatUpstream)
	visit(c.ResponsesUpstream)
	visit(c.GeminiUpstream)
	visit(c.ImagesUpstream)
	visit(c.VectorsUpstream)
	return modified
}

func (c *Config) stripManagedChannelSecrets() {
	visit := func(channels []UpstreamConfig) {
		for i := range channels {
			channel := &channels[i]
			if !channel.AutoManaged || channel.AccountUID == "" || channel.ProviderID == "" {
				continue
			}
			channel.APIKeys = nil
			for j := range channel.APIKeyConfigs {
				channel.APIKeyConfigs[j].Key = ""
			}
		}
	}
	visit(c.Upstream)
	visit(c.ChatUpstream)
	visit(c.ResponsesUpstream)
	visit(c.GeminiUpstream)
	visit(c.ImagesUpstream)
	visit(c.VectorsUpstream)
}

func managedAccountName(channelName string) string {
	for _, suffix := range []string{"-claude", "-chat", "-codex", "-gemini"} {
		channelName = strings.TrimSuffix(channelName, suffix)
	}
	return channelName
}

// TryRestoreDisabledKeysByUsage 在套餐型 Provider 用量刷新后，检查因余额/限额不足
// 被禁用的 Key 是否已满足恢复条件（限额已重置或仍有剩余额度），是则自动恢复。
// 支持 Kimi、MiMo、优云智算(Compshare)、火山(Volcengine) 四类套餐凭证；
// 非套餐凭证或非余额/限额类拉黑原因不受影响。
func TryRestoreDisabledKeysByUsage(cm *ConfigManager, accountUID string, apiKey string, credentialUID string) {
	if cm == nil || accountUID == "" || apiKey == "" {
		return
	}
	credential, ok := cm.GetManagedAccountCredential(accountUID, credentialUID)
	if !ok {
		return
	}

	var canRecover func(dk DisabledKeyInfo, now time.Time) bool
	switch {
	case credential.KimiConsole != nil:
		usage := credential.KimiConsole.Usage
		canRecover = func(dk DisabledKeyInfo, now time.Time) bool {
			if !IsAutoRecoverableDisabledReason(dk.Reason) {
				return false
			}
			// 所有可用限额窗口均已重置且仍有余量时才恢复（AND 语义）。
			if usage.CodeFiveHour != nil && usage.CodeFiveHour.Enabled {
				if !kimiRatioWindowReset(usage.CodeFiveHour, now) {
					return false
				}
			}
			if usage.CodeSevenDay != nil && usage.CodeSevenDay.Enabled {
				if !kimiRatioWindowReset(usage.CodeSevenDay, now) {
					return false
				}
			}
			for _, rl := range usage.RateLimits {
				if rl.Usage.ResetTime != "" {
					rt, err := time.Parse(time.RFC3339Nano, rl.Usage.ResetTime)
					if err != nil || !now.After(rt) || rl.Usage.Remaining <= 0 {
						return false
					}
				}
			}
			if usage.WeeklyUsage.Limit > 0 && usage.WeeklyUsage.Remaining <= 0 {
				return false
			}
			if usage.SubscriptionBalance != nil && usage.SubscriptionBalance.AmountUsedRatio >= 1.0 {
				return false
			}
			return true
		}
	case credential.MiMoConsole != nil:
		usage := credential.MiMoConsole
		canRecover = func(dk DisabledKeyInfo, _ time.Time) bool {
			if !IsAutoRecoverableDisabledReason(dk.Reason) {
				return false
			}
			hasQuota := usage.CurrentUsage.Limit > 0 && usage.CurrentUsage.Used < usage.CurrentUsage.Limit
			return hasQuota && !usage.Expired
		}
	case credential.CompshareConsole != nil:
		usage := credential.CompshareConsole
		canRecover = func(dk DisabledKeyInfo, now time.Time) bool {
			if !IsAutoRecoverableDisabledReason(dk.Reason) {
				return false
			}
			// 所有可用窗口均有剩余额度时才恢复（AND 语义）。
			for _, w := range []CompsharePlanUsageWindow{usage.FiveHourUsage, usage.WeeklyUsage, usage.MonthlyUsage} {
				if w.Limit <= 0 {
					continue // 无限制窗口不阻止恢复
				}
				if w.Used >= w.Limit {
					return false
				}
			}
			return true
		}
	case credential.VolcengineAccessKey != nil && credential.VolcengineAccessKey.Usage != nil:
		usage := credential.VolcengineAccessKey.Usage
		canRecover = func(dk DisabledKeyInfo, now time.Time) bool {
			if !IsAutoRecoverableDisabledReason(dk.Reason) {
				return false
			}
			nowMs := now.UnixMilli()
			// 所有非 nil 窗口均有余量时才恢复（AND 语义）。
			for _, w := range []*VolcenginePlanUsageWindow{usage.FiveHour, usage.Daily, usage.Weekly, usage.Monthly} {
				if w == nil {
					continue
				}
				if w.Quota > 0 && w.Used >= w.Quota {
					return false
				}
				if w.UsedPercent != nil && *w.UsedPercent >= 100.0 {
					// Coding Plan 仅报告百分比。窗口仍耗尽且重置尚未到达（或未给出重置时间）时，
					// 不能恢复；有余量的周/月窗口不应阻止五小时窗口恢复。
					if w.ResetTime == 0 || nowMs < w.ResetTime {
						return false
					}
				}
			}
			return true
		}
	default:
		return
	}

	now := time.Now()
	cfg := cm.GetConfig()
	slices := []struct {
		kind     string
		channels []UpstreamConfig
	}{
		{"messages", cfg.Upstream},
		{"chat", cfg.ChatUpstream},
		{"responses", cfg.ResponsesUpstream},
		{"gemini", cfg.GeminiUpstream},
		{"images", cfg.ImagesUpstream},
		{"vectors", cfg.VectorsUpstream},
	}
	for _, s := range slices {
		for i := range s.channels {
			ch := &s.channels[i]
			if ch.AccountUID != accountUID {
				continue
			}
			restorable := make([]string, 0, 1)
			for _, dk := range ch.DisabledAPIKeys {
				if dk.Key == apiKey && canRecover(dk, now) {
					restorable = append(restorable, apiKey)
					break
				}
			}
			if len(restorable) == 0 {
				continue
			}
			if _, err := cm.RestoreDisabledKeys(kindToAPIType(s.kind), i, restorable); err != nil {
				log.Printf("[Provider-UsageRecover] 渠道 %s (kind=%s) 用量刷新后恢复 Key %s 失败: %v",
					ch.Name, s.kind, utils.MaskAPIKey(apiKey), err)
			} else {
				log.Printf("[Provider-UsageRecover] 渠道 %s (kind=%s) 用量刷新后自动恢复 Key %s",
					ch.Name, s.kind, utils.MaskAPIKey(apiKey))
			}
		}
	}
}

// kimiRatioWindowReset 判断 Kimi 比例限额窗口是否已重置且仍有余量。
func kimiRatioWindowReset(window *KimiCodeRatioWindow, now time.Time) bool {
	if window == nil || !window.Enabled || window.ResetTime == "" {
		return false
	}
	rt, err := time.Parse(time.RFC3339Nano, window.ResetTime)
	if err != nil {
		return false
	}
	return now.After(rt) && window.Ratio < 1.0
}

// kindToAPIType 将账号渠道 kind 映射为 ConfigManager 使用的 apiType。
func kindToAPIType(kind string) string {
	switch kind {
	case "chat":
		return "Chat"
	case "responses":
		return "Responses"
	case "gemini":
		return "Gemini"
	case "images":
		return "Images"
	case "vectors":
		return "Vectors"
	default:
		return "Messages"
	}
}
