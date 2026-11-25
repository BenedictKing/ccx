package configservice

import (
	"sort"
	"strings"
)

func (s *Service) GetSavedProviderKeys() map[string]string {
	store := s.readProviderKeyStore()
	keys := map[string]string{}
	for name, key := range store.Keys {
		keys[name] = key
	}
	legacyCandidates := map[string]string{}
	assetKeys := make([]string, 0, len(store.Assets))
	for k := range store.Assets {
		assetKeys = append(assetKeys, k)
	}
	sort.Strings(assetKeys)
	for _, assetKey := range assetKeys {
		asset := store.Assets[assetKey]
		if strings.TrimSpace(asset.APIKey) == "" {
			continue
		}
		provider := asset.Provider
		planID := asset.PlanID
		switch provider {
		case ProviderDeepSeek, ProviderMiMo, ProviderXFyun, ProviderSenseNova:
			if planID != "" {
				keys[PlatformClaude+":"+provider+":"+planID] = asset.APIKey
				continue
			}
			keys["channel:"+provider] = asset.APIKey
			if legacyCandidates[PlatformClaude+":"+provider] == "" {
				legacyCandidates[PlatformClaude+":"+provider] = asset.APIKey
			}
		case ProviderOpenAI:
			if planID != "" {
				keys[PlatformCodex+":"+provider+":"+planID] = asset.APIKey
				continue
			}
			keys["channel:"+provider] = asset.APIKey
			if legacyCandidates[PlatformCodex+":"+provider] == "" {
				legacyCandidates[PlatformCodex+":"+provider] = asset.APIKey
			}
		default:
			if planID == "" {
				keys["channel:"+provider] = asset.APIKey
			}
		}
	}
	for k, v := range legacyCandidates {
		if keys[k] == "" {
			keys[k] = v
		}
	}
	return keys
}

func (s *Service) GetProviderKeyAssets() []ProviderKeyAsset {
	store := s.readProviderKeyStore()
	assets := make([]ProviderKeyAsset, 0, len(store.Assets))
	assetKeys := make([]string, 0, len(store.Assets))
	for assetKey := range store.Assets {
		assetKeys = append(assetKeys, assetKey)
	}
	sort.Strings(assetKeys)
	for _, assetKey := range assetKeys {
		asset := store.Assets[assetKey]
		if asset.Provider == "" || asset.APIKey == "" {
			continue
		}
		assets = append(assets, asset)
	}
	return assets
}

func (s *Service) SaveProviderKeyAsset(asset ProviderKeyAsset) error {
	provider := strings.TrimSpace(asset.Provider)
	key := strings.TrimSpace(asset.APIKey)
	if provider == "" || key == "" {
		return nil
	}
	store := s.readProviderKeyStore()
	store.Version = stateVersion
	asset.Provider = provider
	asset.APIKey = key
	asset.BaseURL = strings.TrimSpace(asset.BaseURL)
	asset.PlanID = strings.TrimSpace(asset.PlanID)
	asset.ProxyURL = strings.TrimSpace(asset.ProxyURL)
	assetKey := provider
	if asset.PlanID != "" {
		assetKey = provider + ":" + asset.PlanID
	}
	existing := store.Assets[assetKey]
	if existing.Usages != nil {
		asset.Usages = appendUniqueMany(existing.Usages, asset.Usages)
	}
	store.Assets[assetKey] = asset
	if asset.PlanID == "" {
		store.Keys["channel:"+provider] = key
		switch provider {
		case ProviderDeepSeek, ProviderMiMo, ProviderXFyun, ProviderSenseNova:
			store.Keys[PlatformClaude+":"+provider] = key
		case ProviderOpenAI:
			store.Keys[PlatformCodex+":"+provider] = key
		}
	}
	return writeJSONAtomic(s.providerKeysPath(), store)
}

func (s *Service) readProviderKeyStore() ProviderKeyStore {
	store := ProviderKeyStore{Version: stateVersion, Keys: map[string]string{}, Assets: map[string]ProviderKeyAsset{}}
	_ = readJSONFile(s.providerKeysPath(), &store)
	if store.Keys == nil {
		store.Keys = map[string]string{}
	}
	if store.Assets == nil {
		store.Assets = map[string]ProviderKeyAsset{}
	}
	for name, key := range store.Keys {
		provider := providerFromStoreKey(name)
		if provider == "" || strings.TrimSpace(key) == "" {
			continue
		}
		asset := store.Assets[provider]
		asset.Provider = provider
		if asset.APIKey == "" {
			asset.APIKey = key
		}
		asset.Usages = appendUnique(asset.Usages, usageFromStoreKey(name))
		store.Assets[provider] = asset
	}
	return store
}

func (s *Service) saveProviderKey(platform string, provider string, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	store := s.readProviderKeyStore()
	store.Version = stateVersion
	store.Keys[platform+":"+provider] = key
	asset := store.Assets[provider]
	asset.Provider = provider
	asset.APIKey = key
	asset.Usages = appendUnique(asset.Usages, usageFromStoreKey(platform+":"+provider))
	store.Assets[provider] = asset
	return writeJSONAtomic(s.providerKeysPath(), store)
}

func providerFromStoreKey(name string) string {
	parts := strings.SplitN(name, ":", 2)
	if len(parts) != 2 {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(parts[1])
}

func usageFromStoreKey(name string) string {
	parts := strings.SplitN(name, ":", 2)
	if len(parts) != 2 {
		return "manual"
	}
	switch parts[0] {
	case PlatformClaude:
		return "agent-direct"
	case PlatformCodex:
		return "codex-direct"
	default:
		return parts[0]
	}
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueMany(values []string, additions []string) []string {
	for _, value := range additions {
		values = appendUnique(values, value)
	}
	return values
}
