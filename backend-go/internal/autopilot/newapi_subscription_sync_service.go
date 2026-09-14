package autopilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/quota"
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

	// quota 余额结果接入配额真相体系（configured 级；nil = 不接入）。
	quotaMu sync.RWMutex
	quota   *quota.Manager

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
// SetQuotaManager 注入配额管理器（main.go 在创建后、Start 前调用）。
// 余额同步成功后经 writeConfiguredQuota 以 configured 级写入。
func (s *NewApiSubscriptionSyncService) SetQuotaManager(qm *quota.Manager) {
	if s == nil {
		return
	}
	s.quotaMu.Lock()
	s.quota = qm
	s.quotaMu.Unlock()
}

// writeConfiguredQuota 把指定订阅的最新画像余额写入配额管理器（configured 级）。
// fail-open：未注入管理器、画像缺失或配置不可读时静默跳过。
func (s *NewApiSubscriptionSyncService) writeConfiguredQuota(uid string) {
	s.quotaMu.RLock()
	qm := s.quota
	s.quotaMu.RUnlock()
	if qm == nil || s.cfgManager == nil {
		return
	}
	profile := s.store.Get(uid)
	if profile == nil {
		return
	}
	SyncSubscriptionQuotaAsConfigured(qm, profile, LiveChannelUIDSet(s.cfgManager.GetConfig()))
}

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
	if s.locks == nil {
		s.locks = make(map[string]*sync.Mutex)
	}
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
	if strings.TrimSpace(profile.BaseURL) == "" {
		return result, fmt.Errorf("new_api 订阅缺少 baseUrl")
	}
	// 主账号已删除（账号平权模型）：跳过站点级同步，仅同步各子账号，不视为失败。
	if strings.TrimSpace(profile.AccessToken) == "" {
		if len(profile.Accounts) > 0 {
			result.Keys = s.syncAccounts(ctx, profile, s.now())
		}
		return result, nil
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

	statuses, desired := s.buildNewApiDesired(profile, groups, now)
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

	// 余额落盘成功：把订阅画像的额度声明接入配额真相体系（configured 级）。
	// 只写配置中仍存在的关联渠道；渠道配置在下方 reconcile 更新后再全量
	// 重放一次（RegisterOnConfigChange 出口），这里做的是余额侧增量。
	s.writeConfiguredQuota(uid)

	changedChannels, conflict, updateErr := s.reconcileChannels(profile, desired)
	if updateErr != nil {
		return s.handleHardFailure(profile, result, newApiSyncStatusSyncError, fmt.Errorf("更新渠道失败: %w", updateErr))
	}
	if conflict {
		return s.handleHardFailure(profile, result, newApiSyncStatusRelinkRequired, fmt.Errorf("key ownership 冲突，需要重新关联"))
	}
	// 常规同步只更新已存在 config；渠道侧被误删的自动接入 key 在此自愈找回。
	s.healMissingProvisionedKeys(ctx, profile, adapter, profile.BaseURL, profile.AccessToken, userID, mode, desired, "")
	// 兜底补建：「自动接入全部合格分组」的订阅，远端新增合格分组、或接入时因 0 模型
	// 被跳过的分组补上模型后，在此自动补建 key 并注入渠道（幂等，失败下轮 sweep 重试）。
	s.catchUpUncoveredGroups(ctx, profile, adapter, userID, mode, groups)

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

	statuses, desired := buildDesiredForKeys(profile.SubscriptionUID, account.ProvisionedKeys, groups, s.linkedChannelMaxGroupMultiplier(profile), now)

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
			merged, conflict := reconcileNewApiConfigs(channel.APIKeyConfigs, desired, profile.SubscriptionUID, channel.MaxGroupMultiplier)
			if conflict {
				continue
			}
			if !newApiConfigsEqual(channel.APIKeyConfigs, merged) {
				_, _ = updateChannelForKind(s.cfgManager, kind, index, config.UpstreamUpdate{APIKeyConfigs: merged})
			}
		}
		// 账号侧被误删的自动接入 key 同样自愈找回（凭证走该账号自己的 accessToken）。
		s.healMissingProvisionedKeys(ctx, profile, adapter, profile.BaseURL, account.AccessToken, userID, mode, desired, account.AccountUID)
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
	for _, k := range account.ProvisionedKeys {
		removed[int64(k.TokenID)] = struct{}{}
	}
	s.removeTokenKeysFromChannels(profile, removed)
	return removed
}

// removeTokenKeysFromChannels 按 tokenID 集合从订阅关联渠道剔除 key 配置与对应明文 key，
// 返回发生剔除的渠道数。
func (s *NewApiSubscriptionSyncService) removeTokenKeysFromChannels(profile *SubscriptionProfile, tokenIDs map[int64]struct{}) int {
	if s.cfgManager == nil || len(tokenIDs) == 0 {
		return 0
	}
	changedChannels := 0
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
			if _, err := updateChannelForKind(s.cfgManager, kind, index, config.UpstreamUpdate{APIKeys: keptKeys, APIKeyConfigs: keptConfigs}); err == nil {
				changedChannels++
			}
		}
	}
	return changedChannels
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
			MaxGroupMultiplier:  derefFloat(s.linkedChannelMaxGroupMultiplier(profile)),
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
	ratio                               float64
	updatedAt                           time.Time
	expiresAt                           *time.Time
}

// linkedChannelMaxGroupMultiplier 返回订阅关联渠道的渠道级分组倍率上限（展示与同步状态用）。
// 找不到已配置上限的关联渠道时回退订阅 profile 记录的接入初始值。
// 多渠道上限不同的极端场景取第一个命中渠道；调度闸门本身在 evaluator 内
// 按 per-channel 的 UpstreamConfig.MaxGroupMultiplier 实时判定，不受此处影响。
func (s *NewApiSubscriptionSyncService) linkedChannelMaxGroupMultiplier(profile *SubscriptionProfile) *float64 {
	if s != nil && s.cfgManager != nil {
		for _, uid := range profile.LinkedChannelUIDs {
			if _, _, channel, ok := findNewApiChannel(s.cfgManager, uid); ok && channel.MaxGroupMultiplier != nil {
				return channel.MaxGroupMultiplier
			}
		}
	}
	return profile.MaxGroupMultiplier
}

func (s *NewApiSubscriptionSyncService) buildNewApiDesired(profile *SubscriptionProfile, groups map[string]float64, now time.Time) ([]NewApiKeyStatus, []newApiDesiredKey) {
	return buildDesiredForKeys(profile.SubscriptionUID, profile.ProvisionedKeys, groups, s.linkedChannelMaxGroupMultiplier(profile), now)
}

// buildDesiredForKeys 为任意一组 ProvisionedKeys 构造同步期望与状态。
// 主账号传 profile.ProvisionedKeys，额外账号传 account.ProvisionedKeys + 该账号自己的分组倍率；
// 这样不同账号即使同名分组倍率不同也能各自正确取 ratio。
// limitForDisplay 仅用于响应展示与同步状态预判；渠道配置落库时的 over_limit
// 以各渠道自己的 MaxGroupMultiplier 为准（reconcileNewApiConfigs）。
func buildDesiredForKeys(subscriptionUID string, keys []NewApiProvisionedKey, groups map[string]float64, limitForDisplay *float64, now time.Time) ([]NewApiKeyStatus, []newApiDesiredKey) {
	statuses := make([]NewApiKeyStatus, 0, len(keys))
	desired := make([]newApiDesiredKey, 0, len(keys))
	limit := derefFloat(limitForDisplay)
	for _, owned := range keys {
		keyUID := StableKeyUID(subscriptionUID, int64(owned.TokenID))
		ratio, exists := groups[owned.Group]
		status, reason := newApiSyncStatusFresh, ""
		var expires *time.Time
		if !exists {
			ratio, status, reason = owned.GroupMultiplier, newApiSyncStatusRemoteMissing, "远端分组已消失"
		} else if limitForDisplay != nil && ratio > *limitForDisplay {
			status, reason = newApiSyncStatusOverLimit, fmt.Sprintf("远端倍率 %.4g 超过上限 %.4g", ratio, limit)
		} else {
			expiry := now.Add(newApiSyncTTL)
			expires = &expiry
		}
		d := newApiDesiredKey{keyUID: keyUID, name: owned.Name, group: owned.Group, tokenID: int64(owned.TokenID), ratio: ratio, status: status, reason: reason, updatedAt: now, expiresAt: expires}
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
		merged, conflict := reconcileNewApiConfigs(channel.APIKeyConfigs, desired, profile.SubscriptionUID, channel.MaxGroupMultiplier)
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

// reconcileNewApiConfigs 把 desired key 合并进渠道现有 key 配置。
// channelMax 是该渠道的渠道级分组倍率上限：超过上限的 key 状态落为 over_limit
// （不参与调度），key 级不再持久化上限字段（上限唯一真源是渠道级）。
func reconcileNewApiConfigs(existing []config.APIKeyConfig, desired []newApiDesiredKey, subscriptionUID string, channelMax *float64) ([]config.APIKeyConfig, bool) {
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
		status, reason := d.status, d.reason
		var expires *time.Time = d.expiresAt
		if channelMax != nil && d.ratio > *channelMax {
			status, reason, expires = newApiSyncStatusOverLimit, fmt.Sprintf("远端倍率 %.4g 超过上限 %.4g", d.ratio, *channelMax), nil
		}
		cfg.KeyUID = d.keyUID
		cfg.MultiplierSource = newApiSyncSourceNewAPI
		cfg.SourceSubscriptionUID = subscriptionUID
		cfg.SourceRemoteTokenID = d.tokenID
		cfg.QuotaGroup = d.group
		cfg.GroupMultiplier = floatPtr(d.ratio)
		cfg.MaxGroupMultiplier = nil
		cfg.MultiplierUpdatedAt = timePtr(d.updatedAt)
		cfg.MultiplierExpiresAt = expires
		cfg.MultiplierSyncStatus = status
		cfg.MultiplierSyncError = reason
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
		results = append(results, NewApiKeyStatus{KeyUID: StableKeyUID(profile.SubscriptionUID, int64(owned.TokenID)), Name: owned.Name, Group: owned.Group, GroupMultiplier: owned.GroupMultiplier, MaxGroupMultiplier: derefFloat(s.linkedChannelMaxGroupMultiplier(profile)), SourceRemoteTokenID: int64(owned.TokenID), SyncStatus: status, Reason: reason})
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
			cfg.QuotaGroup, cfg.GroupMultiplier = d.group, floatPtr(d.ratio)
			cfg.MaxGroupMultiplier = nil
			cfg.MultiplierUpdatedAt = timePtr(d.updatedAt)
			status, reason, expires := d.status, d.reason, d.expiresAt
			if channel.MaxGroupMultiplier != nil && d.ratio > *channel.MaxGroupMultiplier {
				status, reason, expires = newApiSyncStatusOverLimit, fmt.Sprintf("远端倍率 %.4g 超过上限 %.4g", d.ratio, *channel.MaxGroupMultiplier), nil
			}
			cfg.MultiplierExpiresAt = expires
			cfg.MultiplierSyncStatus, cfg.MultiplierSyncError = status, reason
		}
		// 注入的明文 key 须并入渠道 APIKeys：调度与 keypool 候选只遍历 APIKeys，
		// 仅写 configs 的 key 不参与调用（此前添加账号/自愈注入的 key 实际不可调度）。
		mergedKeys := append([]string(nil), channel.APIKeys...)
		known := make(map[string]struct{}, len(mergedKeys))
		for _, k := range mergedKeys {
			known[k] = struct{}{}
		}
		for _, d := range desired {
			key := plaintextByToken[d.tokenID]
			if key == "" {
				continue
			}
			if _, exists := known[key]; !exists {
				known[key] = struct{}{}
				mergedKeys = append(mergedKeys, key)
			}
		}
		if _, err := updateChannelForKind(s.cfgManager, kind, index, config.UpstreamUpdate{APIKeys: mergedKeys, APIKeyConfigs: configs}); err != nil {
			return err
		}
	}
	return nil
}

// newApiTokenHealer 是自愈所需的远端 token 能力；*NewApiAdapter 天然满足。
// 测试 fake 未实现时自愈自动跳过，不破坏既有 mock。
type newApiTokenHealer interface {
	ListTokens(ctx context.Context, baseURL, accessToken, userID, authTokenMode string, page, size int) ([]NewApiToken, error)
	GetTokenKey(ctx context.Context, baseURL, accessToken, userID, authTokenMode string, tokenID int) (string, error)
}

// newApiGroupCatchUpper 是同步兜底补建 key 所需的适配器能力；*NewApiAdapter 天然满足。
// 测试 fake 未实现时兜底自动跳过，不破坏既有 mock。
type newApiGroupCatchUpper interface {
	newApiGroupModelCounter
	ProvisionKey(ctx context.Context, baseURL, accessToken, userID, authTokenMode string, opts NewApiProvisionOptions) (tokenID int, keyPlainText string, reused bool, finalName string, err error)
}

// catchUpUncoveredGroups 同步兜底：为「自动接入全部合格分组」的订阅补建缺失分组的 key。
// 覆盖接入之后的两类变化：站点新增合格分组；接入时因 0 可用模型被跳过的分组补上了模型。
// 显式单分组接入（ProvisionAllEligible=false）与存量订阅不扩组，避免后台静默建 key。
// best-effort：任何失败只记日志、不影响本次同步结果；ProvisionKey 查重幂等，下轮 sweep 自动重试。
func (s *NewApiSubscriptionSyncService) catchUpUncoveredGroups(ctx context.Context, profile *SubscriptionProfile, adapter NewApiSyncAdapter, userID, authTokenMode string, groups map[string]float64) {
	if s.cfgManager == nil || s.store == nil || !profile.ProvisionAllEligible || len(profile.LinkedChannelUIDs) == 0 {
		return
	}
	catcher, ok := adapter.(newApiGroupCatchUpper)
	if !ok {
		return
	}
	// 渠道级上限是唯一真源；未配置时回退订阅记录的接入初始值，再回退全局默认。
	limit := DefaultNewApiMaxGroupMultiplier
	if configured := s.linkedChannelMaxGroupMultiplier(profile); configured != nil && finiteNonNegative(*configured) {
		limit = *configured
	}
	covered := make(map[string]struct{}, len(profile.ProvisionedKeys))
	for _, key := range profile.ProvisionedKeys {
		covered[key.Group] = struct{}{}
	}
	missing := make([]newApiResolvedGroup, 0)
	for name, ratio := range groups {
		if strings.TrimSpace(name) == "" || !finiteNonNegative(ratio) || ratio > limit {
			continue
		}
		if _, ok := covered[name]; ok {
			continue
		}
		missing = append(missing, newApiResolvedGroup{Name: name, Ratio: ratio})
	}
	if len(missing) == 0 {
		return
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Name < missing[j].Name })

	names := make([]string, len(missing))
	for i, group := range missing {
		names[i] = group.Name
	}
	counts := fetchNewApiGroupModelCounts(ctx, catcher, profile.BaseURL, profile.AccessToken, userID, authTokenMode, names)

	// 与 provision 共用站点级锁：避免与进行中的接入/加账号流程并发建 key。
	siteLock := lockForKeyFrom(&newAPIProvisionSiteLocksMu, newAPIProvisionSiteLocks, newAPIProvisionSiteKey(profile.BaseURL, userID, profile.AccessToken))
	siteLock.Lock()
	defer siteLock.Unlock()

	now := s.now()
	expiry := now.Add(newApiSyncTTL)
	provisioned := make([]newApiProvisionedKey, 0, len(missing))
	for _, group := range missing {
		if count, known := counts[group.Name]; known && count <= 0 {
			continue // 依旧没有可用模型，下轮 sweep 再看
		}
		tokenID, keyPlain, reused, finalName, err := catcher.ProvisionKey(ctx, profile.BaseURL, profile.AccessToken, userID, authTokenMode, NewApiProvisionOptions{
			Name:   defaultNewApiProvisionKeyNameForGroup(group.Name),
			Group:  group.Name,
			Models: profile.ProvisionModels,
		})
		if err != nil {
			log.Printf("[NewApi-Sync] 补建分组 key 失败 subscription=%s group=%s: %v", profile.SubscriptionUID, group.Name, err)
			continue
		}
		if keyPlain == "" || IsMaskedNewApiKey(keyPlain) {
			log.Printf("[NewApi-Sync] 补建分组 key 未取得明文，跳过 subscription=%s group=%s token=%d", profile.SubscriptionUID, group.Name, tokenID)
			continue
		}
		provisioned = append(provisioned, newApiProvisionedKey{
			NewApiProvisionedKey: NewApiProvisionedKey{
				Name:            finalName,
				Group:           group.Name,
				GroupMultiplier: group.Ratio,
				TokenID:         tokenID,
				KeyUID:          StableKeyUID(profile.SubscriptionUID, int64(tokenID)),
			},
			Key:    keyPlain,
			Reused: reused,
		})
	}
	if len(provisioned) == 0 {
		return
	}

	if err := s.store.Patch(profile.SubscriptionUID, nil, func(p *SubscriptionProfile) error {
		existing := make(map[int]struct{}, len(p.ProvisionedKeys))
		for _, key := range p.ProvisionedKeys {
			existing[key.TokenID] = struct{}{}
		}
		for _, key := range provisioned {
			if _, dup := existing[key.TokenID]; dup {
				continue
			}
			p.ProvisionedKeys = append(p.ProvisionedKeys, key.NewApiProvisionedKey)
		}
		p.UpdatedAt = now
		return nil
	}); err != nil {
		log.Printf("[NewApi-Sync] 补建 key 落库失败 subscription=%s: %v", profile.SubscriptionUID, err)
		return
	}

	desired := make([]newApiDesiredKey, 0, len(provisioned))
	plaintextByToken := make(map[int64]string, len(provisioned))
	for _, key := range provisioned {
		desired = append(desired, newApiDesiredKey{
			keyUID:    key.KeyUID,
			name:      key.Name,
			group:     key.Group,
			tokenID:   int64(key.TokenID),
			ratio:     key.GroupMultiplier,
			status:    newApiSyncStatusFresh,
			updatedAt: now,
			expiresAt: &expiry,
		})
		plaintextByToken[int64(key.TokenID)] = key.Key
	}
	if err := s.injectProvisionedKeys(profile, desired, plaintextByToken); err != nil {
		log.Printf("[NewApi-Sync] 补建 key 注入渠道失败 subscription=%s: %v", profile.SubscriptionUID, err)
		return
	}
	caughtGroups := make([]string, 0, len(provisioned))
	for _, key := range provisioned {
		caughtGroups = append(caughtGroups, key.Group)
	}
	log.Printf("[NewApi-Sync] 已补建合格分组 key subscription=%s groups=%v", profile.SubscriptionUID, caughtGroups)

	// 新 key 注入后触发 Discovery 拉取各分组模型。
	if s.runner != nil {
		for _, uid := range profile.LinkedChannelUIDs {
			_, _, channel, ok := findNewApiChannel(s.cfgManager, uid)
			if !ok {
				continue
			}
			ch := channel
			s.runner.TriggerDiscovery(uid, &ch, s.cfgManager)
		}
	}
}

// healMissingProvisionedKeys 补齐订阅期望、但关联渠道缺失的自动接入 key：
// 用户误删渠道 key 后，常规同步只更新已存在 config、无法找回；此处按 desired 的
// tokenID 从远端 token 列表取回明文（掩码经揭示端点换回），经 injectProvisionedKeys
// 重建 config 并入 APIKeys。远端 token 也已删除的项跳过，绝不注入空 key。
// healMissingProvisionedKeys 对账关联渠道与本订阅拥有的自动接入 key：
// 正向——渠道缺失的 key 按 tokenID 从远端列表找回并重新注入（掩码 key 经揭示端点换明文）；
// 反向——远端已删除的 token 同步从渠道与 profile 清理（ownerAccountUID 为空表示订阅级凭证）。
// 反向清理仅在远端列表成功拉全且非空时判定，防止接口异常导致误删。
func (s *NewApiSubscriptionSyncService) healMissingProvisionedKeys(ctx context.Context, profile *SubscriptionProfile, adapter NewApiSyncAdapter, baseURL, accessToken, userID, authTokenMode string, desired []newApiDesiredKey, ownerAccountUID string) {
	if s.cfgManager == nil || len(profile.LinkedChannelUIDs) == 0 || len(desired) == 0 {
		return
	}
	healer, ok := adapter.(newApiTokenHealer)
	if !ok {
		return
	}

	// 任一关联渠道缺失即触发补齐（injectProvisionedKeys 会写所有关联渠道）。
	missing := make([]newApiDesiredKey, 0, len(desired))
	for _, uid := range profile.LinkedChannelUIDs {
		_, _, channel, found := findNewApiChannel(s.cfgManager, uid)
		if !found {
			continue
		}
		byToken := make(map[int64]struct{})
		byUID := make(map[string]struct{})
		hasOwnedConfig := false
		for _, cfg := range channel.APIKeyConfigs {
			if cfg.SourceRemoteTokenID > 0 {
				byToken[int64(cfg.SourceRemoteTokenID)] = struct{}{}
			}
			if cfg.KeyUID != "" {
				byUID[cfg.KeyUID] = struct{}{}
			}
			if cfg.SourceSubscriptionUID == profile.SubscriptionUID {
				hasOwnedConfig = true
			}
		}
		for _, d := range desired {
			if _, has := byToken[d.tokenID]; has {
				continue
			}
			if _, has := byUID[d.keyUID]; has {
				continue
			}
			missing = append(missing, d)
		}
		// 渠道里没有本订阅的任何 key 且无缺失时无需远端交互，避免空转请求。
		if !hasOwnedConfig && len(missing) == 0 {
			return
		}
		break
	}

	need := make(map[int64]struct{}, len(missing))
	for _, d := range missing {
		need[d.tokenID] = struct{}{}
	}
	remote := make(map[int64]string, len(missing))
	remoteIDs := make(map[int64]struct{})
	listComplete := false
	const pageSize = 100
	for page := 1; ; page++ {
		tokens, err := healer.ListTokens(ctx, baseURL, accessToken, userID, authTokenMode, page, pageSize)
		if err != nil {
			log.Printf("[NewApi-Sync] 对账拉取远端 token 列表失败 subscription=%s: %v", profile.SubscriptionUID, err)
			return
		}
		for i := range tokens {
			id := int64(tokens[i].ID)
			remoteIDs[id] = struct{}{}
			if _, wanted := need[id]; !wanted {
				continue
			}
			key := tokens[i].Key
			if IsMaskedNewApiKey(key) {
				revealed, rErr := healer.GetTokenKey(ctx, baseURL, accessToken, userID, authTokenMode, tokens[i].ID)
				if rErr != nil {
					log.Printf("[NewApi-Sync] 自愈揭示 key 明文失败 subscription=%s token=%d: %v", profile.SubscriptionUID, tokens[i].ID, rErr)
					continue
				}
				key = revealed
			}
			remote[id] = normalizeNewApiPlaintextKey(key)
			delete(need, id)
		}
		if len(tokens) < pageSize {
			listComplete = true
			break
		}
	}

	// 正向：远端仍在的缺失 key 重新注入渠道。
	if len(remote) > 0 {
		healed := make([]newApiDesiredKey, 0, len(remote))
		plaintextByToken := make(map[int64]string, len(remote))
		for _, d := range desired {
			if key, found := remote[d.tokenID]; found {
				healed = append(healed, d)
				plaintextByToken[d.tokenID] = key
			}
		}
		if len(healed) > 0 {
			if err := s.injectProvisionedKeys(profile, healed, plaintextByToken); err != nil {
				log.Printf("[NewApi-Sync] 自愈注入渠道失败 subscription=%s: %v", profile.SubscriptionUID, err)
			} else {
				log.Printf("[NewApi-Sync] 自愈找回自动接入 key subscription=%s count=%d tokens=%v", profile.SubscriptionUID, len(healed), needKeys(remote))
			}
		}
	}

	// 反向：远端已删除的 token 从渠道与 profile 清理。
	if listComplete && len(remoteIDs) > 0 {
		stale := make([]newApiDesiredKey, 0)
		for _, d := range desired {
			if _, exists := remoteIDs[d.tokenID]; !exists {
				stale = append(stale, d)
			}
		}
		if len(stale) > 0 {
			s.pruneRemoteDeletedKeys(profile, stale, ownerAccountUID)
		}
	}
}

// pruneRemoteDeletedKeys 把远端已删除的 token 从关联渠道与订阅画像中移除。
func (s *NewApiSubscriptionSyncService) pruneRemoteDeletedKeys(profile *SubscriptionProfile, stale []newApiDesiredKey, ownerAccountUID string) {
	tokenIDs := make(map[int64]struct{}, len(stale))
	staleIDs := make([]int64, 0, len(stale))
	for _, d := range stale {
		tokenIDs[d.tokenID] = struct{}{}
		staleIDs = append(staleIDs, d.tokenID)
	}
	removed := s.removeTokenKeysFromChannels(profile, tokenIDs)
	if s.store != nil {
		_ = s.store.Patch(profile.SubscriptionUID, nil, func(p *SubscriptionProfile) error {
			if ownerAccountUID == "" {
				p.ProvisionedKeys = dropProvisionedKeysByTokenIDs(p.ProvisionedKeys, tokenIDs)
				return nil
			}
			for i := range p.Accounts {
				if p.Accounts[i].AccountUID == ownerAccountUID {
					p.Accounts[i].ProvisionedKeys = dropProvisionedKeysByTokenIDs(p.Accounts[i].ProvisionedKeys, tokenIDs)
				}
			}
			return nil
		})
	}
	log.Printf("[NewApi-Sync] 远端已删除的自动接入 key 已清理 subscription=%s account=%s tokens=%v channels=%d", profile.SubscriptionUID, ownerAccountUID, staleIDs, removed)
}

func dropProvisionedKeysByTokenIDs(keys []NewApiProvisionedKey, tokenIDs map[int64]struct{}) []NewApiProvisionedKey {
	kept := keys[:0]
	for _, k := range keys {
		if _, drop := tokenIDs[int64(k.TokenID)]; drop {
			continue
		}
		kept = append(kept, k)
	}
	return kept
}

func needKeys(remote map[int64]string) []int64 {
	out := make([]int64, 0, len(remote))
	for id := range remote {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s *NewApiSubscriptionSyncService) ReconcileProvisioned(profile *SubscriptionProfile, plaintextByToken map[int64]string) error {
	if profile == nil || s.cfgManager == nil {
		return nil
	}
	now := s.now()
	_, desired := s.buildNewApiDesired(profile, profile.GroupMultipliers, now)
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
	_, desired := buildDesiredForKeys(profile.SubscriptionUID, account.ProvisionedKeys, groups, s.linkedChannelMaxGroupMultiplier(profile), now)
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
