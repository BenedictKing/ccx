package autopilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
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
	newApiSyncTTL                  = 35 * time.Minute
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
	UsedQuota          int64             `json:"usedQuota,omitempty"`
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

	// 周期性自动刷新
	cancel        func()
	wg            sync.WaitGroup
	ticker        *time.Ticker
	sweeping      atomic.Bool
	sem           chan struct{}
	quietLogs     bool
	enabled       func() bool
	perUIDTimeout time.Duration
}

type NewApiSubscriptionSyncServiceDeps struct {
	Store         *SubscriptionStore
	CfgManager    *config.ConfigManager
	Runner        *AutoDiscoveryRunner
	Adapter       NewApiSyncAdapter
	Now           func() time.Time
	QuietLogs     bool
	Enabled       func() bool
	PerUIDTimeout time.Duration
}

const (
	newApiSyncDefaultInterval   = 30 * time.Minute
	newApiSyncDefaultSemSize    = 4
	newApiSyncDefaultUIDTimeout = 25 * time.Second
)

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
	if deps.Enabled == nil {
		deps.Enabled = func() bool { return true }
	}
	if deps.PerUIDTimeout <= 0 {
		deps.PerUIDTimeout = newApiSyncDefaultUIDTimeout
	}
	return &NewApiSubscriptionSyncService{
		store:         deps.Store,
		cfgManager:    deps.CfgManager,
		runner:        deps.Runner,
		adapter:       deps.Adapter,
		now:           deps.Now,
		locks:         make(map[string]*sync.Mutex),
		sem:           make(chan struct{}, newApiSyncDefaultSemSize),
		quietLogs:     deps.QuietLogs,
		enabled:       deps.Enabled,
		perUIDTimeout: deps.PerUIDTimeout,
	}
}

// adapterForProfile 返回该订阅适用的适配器：生效代理为空时用共享适配器（零开销），
// 配置了代理时按代理设置（含直连优先回退）构造。
func (s *NewApiSubscriptionSyncService) adapterForProfile(profile *SubscriptionProfile) NewApiSyncAdapter {
	proxyURL, preferDirect := s.effectiveProxyFor(profile)
	return s.adapterFor(proxyURL, preferDirect)
}

// adapterFor 按代理设置选择适配器；proxyURL 为空时回退到注入的共享适配器。
func (s *NewApiSubscriptionSyncService) adapterFor(proxyURL string, preferDirect bool) NewApiSyncAdapter {
	if strings.TrimSpace(proxyURL) == "" {
		return s.adapter
	}
	return NewApiAdapterForProxy(proxyURL, preferDirect)
}

// effectiveProxyFor 返回订阅管理面访问（同步/余额刷新/账号校验）生效的代理设置：
// 渠道的"代理通道"是唯一事实源（绑定后管理面应跟随渠道配置），关联渠道均未配置时
// 回退订阅级存量设置。
func (s *NewApiSubscriptionSyncService) effectiveProxyFor(profile *SubscriptionProfile) (string, bool) {
	if s.cfgManager != nil {
		for _, uid := range profile.LinkedChannelUIDs {
			if _, _, channel, ok := findNewApiChannel(s.cfgManager, uid); ok && strings.TrimSpace(channel.ProxyURL) != "" {
				return channel.ProxyURL, channel.ProxyPreferDirect
			}
		}
	}
	return profile.ProxyURL, profile.ProxyPreferDirect
}

// Start 启动周期性余额/倍率同步循环。
func (s *NewApiSubscriptionSyncService) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.ticker = time.NewTicker(newApiSyncDefaultInterval)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if !s.quietLogs {
			log.Printf("[NewApiSubscriptionSyncService-Start] 周期性同步已启动 (interval=%s, uidTimeout=%s, concurrency=%d)", newApiSyncDefaultInterval, s.perUIDTimeout, cap(s.sem))
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.ticker.C:
				s.SweepAll(ctx)
			}
		}
	}()
}

// Stop 优雅停止后台同步循环。
func (s *NewApiSubscriptionSyncService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.ticker != nil {
		s.ticker.Stop()
	}
	s.wg.Wait()
	if !s.quietLogs {
		log.Println("[NewApiSubscriptionSyncService-Stop] 周期性同步已停止")
	}
}

// SweepAll 扫描所有 new_api 订阅并并发刷新。
func (s *NewApiSubscriptionSyncService) SweepAll(ctx context.Context) {
	if !s.enabled() {
		if !s.quietLogs {
			log.Println("[NewApiSubscriptionSyncService-Sweep] 全局开关关闭，跳过")
		}
		return
	}
	if !s.sweeping.CompareAndSwap(false, true) {
		if !s.quietLogs {
			log.Println("[NewApiSubscriptionSyncService-Sweep] 上一轮同步尚未结束，跳过重叠执行")
		}
		return
	}
	defer s.sweeping.Store(false)

	all := s.store.ListAll()
	var wg sync.WaitGroup
	for _, profile := range all {
		if profile.Provider != "new_api" {
			continue
		}
		uid := profile.SubscriptionUID
		wg.Add(1)
		s.sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-s.sem }()
			ctxUID, cancel := context.WithTimeout(ctx, s.perUIDTimeout)
			defer cancel()
			result, err := s.SyncNow(ctxUID, uid)
			if !s.quietLogs {
				if err != nil {
					log.Printf("[NewApiSubscriptionSyncService-Sweep] uid=%s error=%v", uid, err)
				} else {
					log.Printf("[NewApiSubscriptionSyncService-Sweep] %s", result.LogSummary())
				}
			}
		}()
	}
	wg.Wait()
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

// WithSubscriptionLock 在 subscription uid 的 per-UID 锁内执行 fn。
// 保证 provision 与 SyncNow 交叉互斥，避免同时修改同一订阅的渠道配置。
func (s *NewApiSubscriptionSyncService) WithSubscriptionLock(uid string, fn func()) {
	lock := s.lockForUID(uid)
	lock.Lock()
	defer lock.Unlock()
	fn()
}

// WithSubscriptionLockHandle 返回 uid 的 per-UID 锁，供调用方按需手动 lock/unlock。
// handleAddSubscriptionAccount 等场景需要在锁内做多次异步操作，不适合 callback 模式。
func (s *NewApiSubscriptionSyncService) WithSubscriptionLockHandle(uid string) *sync.Mutex {
	return s.lockForUID(uid)
}

// LockForUID 导出 lockForUID，供其他写路径（如添加订阅账号）复用同一把 per-UID 锁，
// 保证与 provision / SyncNow 三者对同一订阅交叉互斥。返回的 Mutex 即可 Lock/Unlock。
func (s *NewApiSubscriptionSyncService) LockForUID(uid string) *sync.Mutex {
	return s.lockForUID(uid)
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
	adapter := s.adapterForProfile(profile)

	self, userID, err := adapter.VerifyWithFallback(ctx, profile.BaseURL, profile.AccessToken, profile.UserID, mode)
	if err != nil {
		return s.handleRemoteFailure(profile, result, err)
	}
	groups, err := adapter.FetchGroups(ctx, profile.BaseURL, profile.AccessToken, userID, mode)
	if err != nil {
		return s.handleRemoteFailure(profile, result, err)
	}
	for group, ratio := range groups {
		if !finiteNonNegative(ratio) {
			err = fmt.Errorf("分组 %q 返回非法倍率 %v", group, ratio)
			return s.handleHardFailure(profile, result, newApiSyncStatusSyncError, err)
		}
	}
	models, err := adapter.FetchModels(ctx, profile.BaseURL, profile.AccessToken, userID, mode)
	if err != nil {
		return s.handleRemoteFailure(profile, result, err)
	}
	now := s.now()
	oldHash := hashModelList(profile.AvailableModels)
	newHash := hashModelList(models)
	result.Balance, result.UsedQuota, result.Models, result.ModelsHash = float64(self.Quota), self.UsedQuota, models, newHash
	result.ModelsHashChanged = oldHash != "" && oldHash != newHash

	statuses, desired := buildNewApiDesired(profile, groups, now)
	result.Keys = statuses
	if err := s.store.Patch(uid, nil, func(p *SubscriptionProfile) error {
		p.Balance = result.Balance
		p.UsedQuota = result.UsedQuota
		p.GroupMultipliers = cloneRatios(groups)
		p.AvailableModels = append([]string(nil), models...)
		p.UserID, p.AuthTokenMode = userID, mode
		// 存量订阅未存用户名时顺带回填（verify 响应自带）
		if p.Username == "" {
			p.Username = self.Username
		}
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

	// 额外账号：各自验证/拉分组/reconcile 自己的 ProvisionedKeys。单账号失败隔离，不影响主账号结果。
	if len(profile.Accounts) > 0 {
		accountKeys := s.syncAccounts(ctx, profile, now)
		result.Keys = append(result.Keys, accountKeys...)
	}
	return result, nil
}

// syncAccounts 遍历订阅下的额外账号，分别同步其余额与各自 ProvisionedKeys 的分组倍率/渠道注入。
// 任何单个账号失败只标记该账号，不返回错误，确保主账号与其他账号不受影响。
func (s *NewApiSubscriptionSyncService) syncAccounts(ctx context.Context, profile *SubscriptionProfile, now time.Time) []NewApiKeyStatus {
	all := make([]NewApiKeyStatus, 0)
	for _, account := range profile.Accounts {
		statuses, _ := s.syncOneAccount(ctx, profile, account, now)
		all = append(all, statuses...)
	}
	return all
}

func (s *NewApiSubscriptionSyncService) syncOneAccount(ctx context.Context, profile *SubscriptionProfile, account NewApiAccount, now time.Time) ([]NewApiKeyStatus, error) {
	mode := account.AuthTokenMode
	if mode == "" {
		mode = NewApiAuthModeBearer
	}
	markErr := func(err error) []NewApiKeyStatus {
		// 失败：把该账号的 key 标记为 sync_error，并记录到账号上。
		if s.cfgManager != nil {
			s.markAccountKeys(profile, account, newApiSyncStatusSyncError, err.Error(), now)
		}
		_ = s.store.Patch(profile.SubscriptionUID, nil, func(p *SubscriptionProfile) error {
			for i := range p.Accounts {
				if p.Accounts[i].AccountUID == account.AccountUID {
					p.Accounts[i].Status = "error"
					p.Accounts[i].LastSyncError = err.Error()
					p.Accounts[i].LastCheckedAt = now
				}
			}
			return nil
		})
		return s.accountKeyStatuses(profile, account, newApiSyncStatusSyncError, err.Error(), now)
	}

	// 账号级代理优先，缺省继承渠道/订阅级生效代理；无代理时回退注入的共享适配器
	fallbackProxy, fallbackPreferDirect := s.effectiveProxyFor(profile)
	adapter := s.adapterFor(resolveNewApiAccountProxy(account, fallbackProxy, fallbackPreferDirect))

	self, userID, err := adapter.VerifyWithFallback(ctx, profile.BaseURL, account.AccessToken, account.UserID, mode)
	if err != nil {
		return markErr(err), err
	}
	groups, err := adapter.FetchGroups(ctx, profile.BaseURL, account.AccessToken, userID, mode)
	if err != nil {
		return markErr(err), err
	}
	for _, ratio := range groups {
		if !finiteNonNegative(ratio) {
			return markErr(fmt.Errorf("分组返回非法倍率 %v", ratio)), fmt.Errorf("分组返回非法倍率")
		}
	}

	statuses, desired := buildDesiredForKeys(profile.SubscriptionUID, account.ProvisionedKeys, groups, profile.MaxGroupMultiplier, now)

	// 更新账号余额/状态/KeyUID/倍率。
	_ = s.store.Patch(profile.SubscriptionUID, nil, func(p *SubscriptionProfile) error {
		for i := range p.Accounts {
			if p.Accounts[i].AccountUID != account.AccountUID {
				continue
			}
			p.Accounts[i].Balance = float64(self.Quota)
			p.Accounts[i].Status = "active"
			p.Accounts[i].LastSyncError = ""
			p.Accounts[i].LastCheckedAt = now
			p.Accounts[i].UserID = userID
			for ki := range p.Accounts[i].ProvisionedKeys {
				p.Accounts[i].ProvisionedKeys[ki].KeyUID = StableKeyUID(profile.SubscriptionUID, int64(p.Accounts[i].ProvisionedKeys[ki].TokenID))
				if ratio, ok := groups[p.Accounts[i].ProvisionedKeys[ki].Group]; ok && finiteNonNegative(ratio) {
					p.Accounts[i].ProvisionedKeys[ki].GroupMultiplier = ratio
				}
			}
		}
		return nil
	})

	// 注入/更新渠道（只更新已存在的 key 元数据；新增 key 走添加账号流程的 ReconcileAccountProvisioned）。
	if s.cfgManager != nil {
		for _, uid := range profile.LinkedChannelUIDs {
			kind, index, channel, ok := findNewApiChannel(s.cfgManager, uid)
			if !ok {
				continue
			}
			merged, conflict := reconcileNewApiConfigs(channel.APIKeyConfigs, desired, profile.SubscriptionUID)
			if conflict {
				continue
			}
			if !newApiConfigsEqual(channel.APIKeyConfigs, merged) {
				_, _ = updateChannelForKind(s.cfgManager, kind, index, config.UpstreamUpdate{APIKeyConfigs: merged})
			}
		}
	}
	return statuses, nil
}

// markAccountKeys 把指定账号在渠道中的 key 标记为给定状态（故障隔离的最小单元）。
func (s *NewApiSubscriptionSyncService) markAccountKeys(profile *SubscriptionProfile, account NewApiAccount, status, reason string, now time.Time) {
	tokenIDs := make(map[int64]struct{}, len(account.ProvisionedKeys))
	for _, k := range account.ProvisionedKeys {
		tokenIDs[int64(k.TokenID)] = struct{}{}
	}
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
			if _, owned := tokenIDs[cfg.SourceRemoteTokenID]; !owned {
				continue
			}
			if cfg.MultiplierSyncStatus != status || cfg.MultiplierSyncError != reason {
				cfg.MultiplierSyncStatus, cfg.MultiplierSyncError = status, reason
				changed = true
			}
		}
		if changed {
			_, _ = updateChannelForKind(s.cfgManager, kind, index, config.UpstreamUpdate{APIKeyConfigs: updated})
		}
	}
}

// RemoveAccountKeysFromChannels 在删除账号时，从订阅关联的所有渠道剔除该账号 tokenID 对应的 key 配置，
// 同时从 APIKeys 列表移除对应明文 key。返回被移除的 tokenID 集合，供调用方回收远端 key。
func (s *NewApiSubscriptionSyncService) RemoveAccountKeysFromChannels(profile *SubscriptionProfile, account NewApiAccount) map[int64]struct{} {
	removed := make(map[int64]struct{}, len(account.ProvisionedKeys))
	if s.cfgManager == nil {
		return removed
	}
	tokenIDs := make(map[int64]struct{}, len(account.ProvisionedKeys))
	for _, k := range account.ProvisionedKeys {
		tokenIDs[int64(k.TokenID)] = struct{}{}
	}
	for _, uid := range profile.LinkedChannelUIDs {
		kind, index, channel, ok := findNewApiChannel(s.cfgManager, uid)
		if !ok {
			continue
		}
		removedKeys := make(map[string]struct{})
		keptConfigs := make([]config.APIKeyConfig, 0, len(channel.APIKeyConfigs))
		for _, cfg := range channel.APIKeyConfigs {
			if cfg.SourceSubscriptionUID == profile.SubscriptionUID {
				if _, owned := tokenIDs[cfg.SourceRemoteTokenID]; owned {
					removed[cfg.SourceRemoteTokenID] = struct{}{}
					if cfg.Key != "" {
						removedKeys[cfg.Key] = struct{}{}
					}
					continue
				}
			}
			keptConfigs = append(keptConfigs, cfg)
		}
		keptKeys := make([]string, 0, len(channel.APIKeys))
		for _, k := range channel.APIKeys {
			if _, drop := removedKeys[k]; drop {
				continue
			}
			keptKeys = append(keptKeys, k)
		}
		if len(keptConfigs) != len(channel.APIKeyConfigs) || len(keptKeys) != len(channel.APIKeys) {
			_, _ = updateChannelForKind(s.cfgManager, kind, index, config.UpstreamUpdate{APIKeys: keptKeys, APIKeyConfigs: keptConfigs})
		}
	}
	return removed
}

// accountKeyStatuses 生成指定账号 key 的状态条目。
func (s *NewApiSubscriptionSyncService) accountKeyStatuses(profile *SubscriptionProfile, account NewApiAccount, status, reason string, now time.Time) []NewApiKeyStatus {
	out := make([]NewApiKeyStatus, 0, len(account.ProvisionedKeys))
	for _, k := range account.ProvisionedKeys {
		out = append(out, NewApiKeyStatus{
			KeyUID:              StableKeyUID(profile.SubscriptionUID, int64(k.TokenID)),
			Name:                k.Name,
			Group:               k.Group,
			GroupMultiplier:     k.GroupMultiplier,
			MaxGroupMultiplier:  derefFloat(profile.MaxGroupMultiplier),
			SourceRemoteTokenID: int64(k.TokenID),
			SyncStatus:          status,
			Reason:              reason,
			UpdatedAt:           now.UTC().Format(time.RFC3339),
		})
	}
	return out
}

type newApiDesiredKey struct {
	keyUID, name, group, status, reason string
	tokenID                             int64
	ratio, limit                        float64
	updatedAt                           time.Time
	expiresAt                           *time.Time
}

func buildNewApiDesired(profile *SubscriptionProfile, groups map[string]float64, now time.Time) ([]NewApiKeyStatus, []newApiDesiredKey) {
	return buildDesiredForKeys(profile.SubscriptionUID, profile.ProvisionedKeys, groups, profile.MaxGroupMultiplier, now)
}

// buildDesiredForKeys 为任意一组 ProvisionedKeys 构造同步期望与状态。
// 主账号传 profile.ProvisionedKeys，额外账号传 account.ProvisionedKeys + 该账号自己的分组倍率；
// 这样不同账号即使同名分组倍率不同也能各自正确取 ratio。
func buildDesiredForKeys(subscriptionUID string, keys []NewApiProvisionedKey, groups map[string]float64, maxGroupMultiplier *float64, now time.Time) ([]NewApiKeyStatus, []newApiDesiredKey) {
	statuses := make([]NewApiKeyStatus, 0, len(keys))
	desired := make([]newApiDesiredKey, 0, len(keys))
	limit := derefFloat(maxGroupMultiplier)
	for _, owned := range keys {
		keyUID := StableKeyUID(subscriptionUID, int64(owned.TokenID))
		ratio, exists := groups[owned.Group]
		status, reason := newApiSyncStatusFresh, ""
		var expires *time.Time
		if !exists {
			ratio, status, reason = owned.GroupMultiplier, newApiSyncStatusRemoteMissing, "远端分组已消失"
		} else if maxGroupMultiplier != nil && ratio > *maxGroupMultiplier {
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

// injectProvisionedKeys 把一组 desired key 按 tokenID 注入 profile 关联的所有渠道。
// 已存在的 config 更新元数据；缺失的按明文追加。明文 key ownership 冲突时报错。
func (s *NewApiSubscriptionSyncService) injectProvisionedKeys(profile *SubscriptionProfile, desired []newApiDesiredKey, plaintextByToken map[int64]string) error {
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
	return nil
}

func (s *NewApiSubscriptionSyncService) ReconcileProvisioned(profile *SubscriptionProfile, plaintextByToken map[int64]string) error {
	if profile == nil || s.cfgManager == nil {
		return nil
	}
	now := s.now()
	_, desired := buildNewApiDesired(profile, profile.GroupMultipliers, now)
	if err := s.injectProvisionedKeys(profile, desired, plaintextByToken); err != nil {
		return err
	}
	return s.store.Patch(profile.SubscriptionUID, nil, func(p *SubscriptionProfile) error {
		for i := range p.ProvisionedKeys {
			p.ProvisionedKeys[i].KeyUID = StableKeyUID(p.SubscriptionUID, int64(p.ProvisionedKeys[i].TokenID))
		}
		return nil
	})
}

// ReconcileAccountProvisioned 把某个额外账号新建/复用的 key 注入关联渠道，并回填其 KeyUID。
// 与主账号 ReconcileProvisioned 的区别在于数据源是 account.ProvisionedKeys + 该账号自己的分组倍率 groups。
func (s *NewApiSubscriptionSyncService) ReconcileAccountProvisioned(profile *SubscriptionProfile, accountUID string, groups map[string]float64, plaintextByToken map[int64]string) error {
	if profile == nil || s.cfgManager == nil {
		return nil
	}
	var account *NewApiAccount
	for i := range profile.Accounts {
		if profile.Accounts[i].AccountUID == accountUID {
			account = &profile.Accounts[i]
			break
		}
	}
	if account == nil {
		return fmt.Errorf("account_uid=%s 不存在", accountUID)
	}
	now := s.now()
	_, desired := buildDesiredForKeys(profile.SubscriptionUID, account.ProvisionedKeys, groups, profile.MaxGroupMultiplier, now)
	if err := s.injectProvisionedKeys(profile, desired, plaintextByToken); err != nil {
		return err
	}
	return s.store.Patch(profile.SubscriptionUID, nil, func(p *SubscriptionProfile) error {
		for ai := range p.Accounts {
			if p.Accounts[ai].AccountUID != accountUID {
				continue
			}
			for ki := range p.Accounts[ai].ProvisionedKeys {
				p.Accounts[ai].ProvisionedKeys[ki].KeyUID = StableKeyUID(p.SubscriptionUID, int64(p.Accounts[ai].ProvisionedKeys[ki].TokenID))
			}
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
