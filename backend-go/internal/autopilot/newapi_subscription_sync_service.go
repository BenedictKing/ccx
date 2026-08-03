package autopilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

const (
	newApiSyncStatusFresh          = "fresh"
	newApiSyncStatusOverLimit      = "over_limit"
	newApiSyncStatusSyncError      = "sync_error"
	newApiSyncStatusRelinkRequired = "relink_required"
	newApiSyncStatusStale          = "stale"
	newApiSyncStatusRemoteMissing  = "remote_group_missing"
	newApiSyncSourceNewAPI         = "new_api"
	newApiSyncTTL                  = 15 * time.Minute
)

type NewApiKeyStatus struct {
	KeyUID              string  `json:"keyUid,omitempty"`
	Name                string  `json:"name"`
	Group               string  `json:"group"`
	GroupMultiplier     float64 `json:"groupMultiplier"`
	MaxGroupMultiplier  float64 `json:"maxGroupMultiplier"`
	SourceRemoteTokenID int64   `json:"sourceRemoteTokenId"`
	SyncStatus          string  `json:"syncStatus"`
	MultiplierExpiresAt string  `json:"multiplierExpiresAt,omitempty"`
	UpdatedAt           string  `json:"updatedAt,omitempty"`
	Reason              string  `json:"reason,omitempty"`
}

type NewApiSyncResult struct {
	SubscriptionUID    string            `json:"subscriptionUid"`
	Success            bool              `json:"success"`
	Balance            float64           `json:"balance,omitempty"`
	Models             []string          `json:"models,omitempty"`
	ModelsHash         string            `json:"modelsHash,omitempty"`
	ModelsHashChanged  bool              `json:"modelsHashChanged"`
	Keys               []NewApiKeyStatus `json:"keys"`
	DiscoveryTriggered bool              `json:"discoveryTriggered"`
	FailedReason       string            `json:"failedReason,omitempty"`
}

type NewApiSyncAdapter interface {
	VerifyWithFallback(context.Context, string, string, string, string) (*NewApiUserSelf, string, error)
	FetchGroups(context.Context, string, string, string, string) (map[string]float64, error)
	FetchModels(context.Context, string, string, string, string) ([]string, error)
}

type NewApiSubscriptionSyncService struct {
	store      *SubscriptionStore
	cfgManager *config.ConfigManager
	runner     *AutoDiscoveryRunner
	adapter    NewApiSyncAdapter
	now        func() time.Time
	locksMu    sync.Mutex
	locks      map[string]*sync.Mutex
}

type NewApiSubscriptionSyncServiceDeps struct {
	Store      *SubscriptionStore
	CfgManager *config.ConfigManager
	Runner     *AutoDiscoveryRunner
	Adapter    NewApiSyncAdapter
	Now        func() time.Time
}

func NewNewApiSubscriptionSyncService(deps NewApiSubscriptionSyncServiceDeps) *NewApiSubscriptionSyncService {
	if deps.Store == nil {
		panic("[NewApiSubscriptionSyncService-Init] Store 不能为空")
	}
	if deps.Adapter == nil {
		deps.Adapter = &NewApiAdapter{}
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &NewApiSubscriptionSyncService{store: deps.Store, cfgManager: deps.CfgManager, runner: deps.Runner, adapter: deps.Adapter, now: deps.Now, locks: make(map[string]*sync.Mutex)}
}

func (s *NewApiSubscriptionSyncService) lockForUID(uid string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	if lock := s.locks[uid]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	s.locks[uid] = lock
	return lock
}

func (s *NewApiSubscriptionSyncService) SyncNow(ctx context.Context, uid string) (NewApiSyncResult, error) {
	uid = strings.TrimSpace(uid)
	result := NewApiSyncResult{SubscriptionUID: uid, Keys: []NewApiKeyStatus{}}
	if uid == "" {
		return result, fmt.Errorf("subscriptionUID 不能为空")
	}
	lock := s.lockForUID(uid)
	lock.Lock()
	defer lock.Unlock()

	profile := s.store.Get(uid)
	if profile == nil {
		return result, fmt.Errorf("subscription_uid=%s 不存在", uid)
	}
	if profile.Provider != "new_api" {
		return result, fmt.Errorf("provider=%s 不是 new_api", profile.Provider)
	}
	if strings.TrimSpace(profile.BaseURL) == "" || strings.TrimSpace(profile.AccessToken) == "" {
		return result, fmt.Errorf("new_api 订阅缺少 baseUrl 或 accessToken")
	}
	mode := profile.AuthTokenMode
	if mode == "" {
		mode = NewApiAuthModeBearer
	}

	self, userID, err := s.adapter.VerifyWithFallback(ctx, profile.BaseURL, profile.AccessToken, profile.UserID, mode)
	if err != nil {
		return s.handleRemoteFailure(profile, result, err)
	}
	groups, err := s.adapter.FetchGroups(ctx, profile.BaseURL, profile.AccessToken, userID, mode)
	if err != nil {
		return s.handleRemoteFailure(profile, result, err)
	}
	for group, ratio := range groups {
		if !finiteNonNegative(ratio) {
			err = fmt.Errorf("分组 %q 返回非法倍率 %v", group, ratio)
			return s.handleHardFailure(profile, result, newApiSyncStatusSyncError, err)
		}
	}
	models, err := s.adapter.FetchModels(ctx, profile.BaseURL, profile.AccessToken, userID, mode)
	if err != nil {
		return s.handleRemoteFailure(profile, result, err)
	}
	now := s.now()
	oldHash := hashModelList(profile.AvailableModels)
	newHash := hashModelList(models)
	result.Balance, result.Models, result.ModelsHash = float64(self.Quota), models, newHash
	result.ModelsHashChanged = oldHash != "" && oldHash != newHash

	statuses, desired := buildNewApiDesired(profile, groups, now)
	result.Keys = statuses
	if err := s.store.Patch(uid, nil, func(p *SubscriptionProfile) error {
		p.Balance = result.Balance
		p.GroupMultipliers = cloneRatios(groups)
		p.AvailableModels = append([]string(nil), models...)
		p.UserID, p.AuthTokenMode = userID, mode
		p.LastBalanceRefreshAt = timePtr(now)
		p.LastBalanceRefreshError = ""
		for i := range p.ProvisionedKeys {
			p.ProvisionedKeys[i].KeyUID = StableKeyUID(uid, int64(p.ProvisionedKeys[i].TokenID))
			if ratio, ok := groups[p.ProvisionedKeys[i].Group]; ok && finiteNonNegative(ratio) {
				p.ProvisionedKeys[i].GroupMultiplier = ratio
			}
		}
		return nil
	}); err != nil {
		return result, err
	}

	changedChannels, conflict, updateErr := s.reconcileChannels(profile, desired)
	if updateErr != nil {
		return s.handleHardFailure(profile, result, newApiSyncStatusSyncError, fmt.Errorf("更新渠道失败: %w", updateErr))
	}
	if conflict {
		return s.handleHardFailure(profile, result, newApiSyncStatusRelinkRequired, fmt.Errorf("key ownership 冲突，需要重新关联"))
	}

	if result.ModelsHashChanged && s.runner != nil && s.cfgManager != nil {
		for _, channel := range changedChannels {
			ch := channel
			if s.runner.TriggerDiscovery(ch.ChannelUID, &ch, s.cfgManager) {
				result.DiscoveryTriggered = true
			}
		}
	}
	result.Success = true
	for _, status := range statuses {
		if status.SyncStatus != newApiSyncStatusFresh {
			result.Success = false
		}
	}
	return result, nil
}

type newApiDesiredKey struct {
	keyUID, name, group, status, reason string
	tokenID                             int64
	ratio, limit                        float64
	updatedAt                           time.Time
	expiresAt                           *time.Time
}

func buildNewApiDesired(profile *SubscriptionProfile, groups map[string]float64, now time.Time) ([]NewApiKeyStatus, []newApiDesiredKey) {
	statuses := make([]NewApiKeyStatus, 0, len(profile.ProvisionedKeys))
	desired := make([]newApiDesiredKey, 0, len(profile.ProvisionedKeys))
	limit := derefFloat(profile.MaxGroupMultiplier)
	for _, owned := range profile.ProvisionedKeys {
		keyUID := StableKeyUID(profile.SubscriptionUID, int64(owned.TokenID))
		ratio, exists := groups[owned.Group]
		status, reason := newApiSyncStatusFresh, ""
		var expires *time.Time
		if !exists {
			ratio, status, reason = owned.GroupMultiplier, newApiSyncStatusRemoteMissing, "远端分组已消失"
		} else if profile.MaxGroupMultiplier != nil && ratio > *profile.MaxGroupMultiplier {
			status, reason = newApiSyncStatusOverLimit, fmt.Sprintf("远端倍率 %.4g 超过上限 %.4g", ratio, limit)
		} else {
			expiry := now.Add(newApiSyncTTL)
			expires = &expiry
		}
		d := newApiDesiredKey{keyUID: keyUID, name: owned.Name, group: owned.Group, tokenID: int64(owned.TokenID), ratio: ratio, limit: limit, status: status, reason: reason, updatedAt: now, expiresAt: expires}
		desired = append(desired, d)
		item := NewApiKeyStatus{KeyUID: keyUID, Name: owned.Name, Group: owned.Group, GroupMultiplier: ratio, MaxGroupMultiplier: limit, SourceRemoteTokenID: int64(owned.TokenID), SyncStatus: status, UpdatedAt: now.UTC().Format(time.RFC3339), Reason: reason}
		if expires != nil {
			item.MultiplierExpiresAt = expires.UTC().Format(time.RFC3339)
		}
		statuses = append(statuses, item)
	}
	return statuses, desired
}

func (s *NewApiSubscriptionSyncService) reconcileChannels(profile *SubscriptionProfile, desired []newApiDesiredKey) ([]config.UpstreamConfig, bool, error) {
	if s.cfgManager == nil {
		return nil, false, nil
	}
	changed := make([]config.UpstreamConfig, 0, len(profile.LinkedChannelUIDs))
	for _, uid := range profile.LinkedChannelUIDs {
		kind, index, channel, ok := findNewApiChannel(s.cfgManager, uid)
		if !ok {
			continue
		}
		merged, conflict := reconcileNewApiConfigs(channel.APIKeyConfigs, desired, profile.SubscriptionUID)
		if conflict {
			return changed, true, nil
		}
		if !newApiConfigsEqual(channel.APIKeyConfigs, merged) {
			if _, err := updateChannelForKind(s.cfgManager, kind, index, config.UpstreamUpdate{APIKeyConfigs: merged}); err != nil {
				return changed, false, err
			}
			channel.APIKeyConfigs = merged
		}
		changed = append(changed, channel)
	}
	return changed, false, nil
}

func reconcileNewApiConfigs(existing []config.APIKeyConfig, desired []newApiDesiredKey, subscriptionUID string) ([]config.APIKeyConfig, bool) {
	out := append([]config.APIKeyConfig(nil), existing...)
	byToken := make(map[int64]int)
	byUID := make(map[string]int)
	for i, cfg := range out {
		if cfg.SourceRemoteTokenID > 0 {
			if previous, exists := byToken[cfg.SourceRemoteTokenID]; exists && (cfg.SourceSubscriptionUID == subscriptionUID || out[previous].SourceSubscriptionUID == subscriptionUID) {
				return out, true
			}
			if cfg.SourceSubscriptionUID == subscriptionUID {
				byToken[cfg.SourceRemoteTokenID] = i
			}
		}
		if cfg.KeyUID != "" {
			byUID[cfg.KeyUID] = i
		}
	}
	for _, d := range desired {
		index, found := byToken[d.tokenID]
		if !found {
			index, found = byUID[d.keyUID]
		}
		if !found {
			continue
		}
		cfg := out[index]
		if cfg.SourceSubscriptionUID != "" && cfg.SourceSubscriptionUID != subscriptionUID {
			return out, true
		}
		if cfg.SourceRemoteTokenID != 0 && cfg.SourceRemoteTokenID != d.tokenID {
			return out, true
		}
		cfg.KeyUID = d.keyUID
		cfg.MultiplierSource = newApiSyncSourceNewAPI
		cfg.SourceSubscriptionUID = subscriptionUID
		cfg.SourceRemoteTokenID = d.tokenID
		cfg.QuotaGroup = d.group
		cfg.GroupMultiplier = floatPtr(d.ratio)
		cfg.MaxGroupMultiplier = floatPtr(d.limit)
		cfg.MultiplierUpdatedAt = timePtr(d.updatedAt)
		cfg.MultiplierExpiresAt = d.expiresAt
		cfg.MultiplierSyncStatus = d.status
		cfg.MultiplierSyncError = d.reason
		out[index] = cfg
	}
	return out, false
}

func (s *NewApiSubscriptionSyncService) handleRemoteFailure(profile *SubscriptionProfile, result NewApiSyncResult, cause error) (NewApiSyncResult, error) {
	msg := strings.ToLower(cause.Error())
	if strings.Contains(msg, "401") || strings.Contains(msg, "403") || strings.Contains(msg, "unauthorized") || strings.Contains(msg, "forbidden") || strings.Contains(msg, "envelope") || strings.Contains(msg, "信封") {
		return s.handleHardFailure(profile, result, newApiSyncStatusSyncError, cause)
	}
	result.FailedReason = cause.Error()
	result.Keys = s.markTransientFailure(profile, cause.Error())
	_ = s.store.Patch(profile.SubscriptionUID, nil, func(p *SubscriptionProfile) error { p.LastBalanceRefreshError = cause.Error(); return nil })
	return result, cause
}

func (s *NewApiSubscriptionSyncService) handleHardFailure(profile *SubscriptionProfile, result NewApiSyncResult, status string, cause error) (NewApiSyncResult, error) {
	result.FailedReason = cause.Error()
	result.Keys = s.markAllOwned(profile, status, cause.Error(), true)
	_ = s.store.Patch(profile.SubscriptionUID, nil, func(p *SubscriptionProfile) error { p.LastBalanceRefreshError = cause.Error(); return nil })
	return result, cause
}

func (s *NewApiSubscriptionSyncService) markTransientFailure(profile *SubscriptionProfile, reason string) []NewApiKeyStatus {
	return s.markAllOwned(profile, newApiSyncStatusStale, reason, false)
}

func (s *NewApiSubscriptionSyncService) markAllOwned(profile *SubscriptionProfile, status, reason string, force bool) []NewApiKeyStatus {
	now := s.now()
	results := make([]NewApiKeyStatus, 0, len(profile.ProvisionedKeys))
	if s.cfgManager != nil {
		for _, uid := range profile.LinkedChannelUIDs {
			kind, index, channel, ok := findNewApiChannel(s.cfgManager, uid)
			if !ok {
				continue
			}
			updated := append([]config.APIKeyConfig(nil), channel.APIKeyConfigs...)
			changed := false
			for i := range updated {
				cfg := &updated[i]
				if cfg.SourceSubscriptionUID != profile.SubscriptionUID {
					continue
				}
				next := status
				if !force && (cfg.MultiplierExpiresAt == nil || cfg.MultiplierExpiresAt.After(now)) {
					next = cfg.MultiplierSyncStatus
				}
				if next != cfg.MultiplierSyncStatus || (force && cfg.MultiplierSyncError != reason) {
					cfg.MultiplierSyncStatus, cfg.MultiplierSyncError = next, reason
					changed = true
				}
			}
			if changed {
				_, _ = updateChannelForKind(s.cfgManager, kind, index, config.UpstreamUpdate{APIKeyConfigs: updated})
			}
		}
	}
	for _, owned := range profile.ProvisionedKeys {
		results = append(results, NewApiKeyStatus{KeyUID: StableKeyUID(profile.SubscriptionUID, int64(owned.TokenID)), Name: owned.Name, Group: owned.Group, GroupMultiplier: owned.GroupMultiplier, MaxGroupMultiplier: derefFloat(profile.MaxGroupMultiplier), SourceRemoteTokenID: int64(owned.TokenID), SyncStatus: status, Reason: reason})
	}
	return results
}

func findNewApiChannel(cm *config.ConfigManager, uid string) (string, int, config.UpstreamConfig, bool) {
	cfg := cm.GetConfig()
	for _, kind := range []string{"messages", "chat", "responses", "gemini", "images", "vectors"} {
		for i, channel := range getChannelSlice(cfg, kind) {
			if channel.ChannelUID == uid {
				return kind, i, channel, true
			}
		}
	}
	return "", -1, config.UpstreamConfig{}, false
}

func newApiConfigsEqual(a, b []config.APIKeyConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if fmt.Sprintf("%#v", a[i]) != fmt.Sprintf("%#v", b[i]) {
			return false
		}
	}
	return true
}

func StableKeyUID(subscriptionUID string, tokenID int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("newapi|%s|%d", subscriptionUID, tokenID)))
	return "kuid_" + hex.EncodeToString(sum[:8])
}

func finiteNonNegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
func derefFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
func floatPtr(v float64) *float64    { return &v }
func timePtr(v time.Time) *time.Time { return &v }
func cloneRatios(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *NewApiSubscriptionSyncService) ReconcileProvisioned(profile *SubscriptionProfile, plaintextByToken map[int64]string) error {
	if profile == nil || s.cfgManager == nil {
		return nil
	}
	now := s.now()
	_, desired := buildNewApiDesired(profile, profile.GroupMultipliers, now)
	for _, uid := range profile.LinkedChannelUIDs {
		kind, index, channel, ok := findNewApiChannel(s.cfgManager, uid)
		if !ok {
			continue
		}
		configs := append([]config.APIKeyConfig(nil), channel.APIKeyConfigs...)
		for _, d := range desired {
			key := plaintextByToken[d.tokenID]
			match := -1
			for i := range configs {
				if configs[i].SourceSubscriptionUID == profile.SubscriptionUID && configs[i].SourceRemoteTokenID == d.tokenID {
					match = i
					break
				}
				if key != "" && configs[i].Key == key {
					if configs[i].SourceSubscriptionUID != "" && configs[i].SourceSubscriptionUID != profile.SubscriptionUID {
						return fmt.Errorf("明文 key ownership 冲突")
					}
					match = i
				}
			}
			if match < 0 {
				configs = append(configs, config.APIKeyConfig{Key: key, Name: "new-api:" + d.group})
				match = len(configs) - 1
			}
			cfg := &configs[match]
			cfg.KeyUID, cfg.MultiplierSource = d.keyUID, newApiSyncSourceNewAPI
			cfg.SourceSubscriptionUID, cfg.SourceRemoteTokenID = profile.SubscriptionUID, d.tokenID
			cfg.QuotaGroup, cfg.GroupMultiplier, cfg.MaxGroupMultiplier = d.group, floatPtr(d.ratio), floatPtr(d.limit)
			cfg.MultiplierUpdatedAt, cfg.MultiplierExpiresAt = timePtr(d.updatedAt), d.expiresAt
			cfg.MultiplierSyncStatus, cfg.MultiplierSyncError = d.status, d.reason
		}
		if _, err := updateChannelForKind(s.cfgManager, kind, index, config.UpstreamUpdate{APIKeyConfigs: configs}); err != nil {
			return err
		}
	}
	return s.store.Patch(profile.SubscriptionUID, nil, func(p *SubscriptionProfile) error {
		for i := range p.ProvisionedKeys {
			p.ProvisionedKeys[i].KeyUID = StableKeyUID(p.SubscriptionUID, int64(p.ProvisionedKeys[i].TokenID))
		}
		return nil
	})
}

func (s *NewApiSubscriptionSyncService) SyncAllNewAPIAsync(ctx context.Context) {
	for _, profile := range s.store.ListAll() {
		if profile.Provider != "new_api" {
			continue
		}
		uid := profile.SubscriptionUID
		go func() { _, _ = s.SyncNow(ctx, uid) }()
	}
}

func (r NewApiSyncResult) LogSummary() string {
	return fmt.Sprintf("uid=%s success=%v owned=%d reason=%q", r.SubscriptionUID, r.Success, len(r.Keys), r.FailedReason)
}
