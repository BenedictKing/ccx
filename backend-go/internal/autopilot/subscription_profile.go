package autopilot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/BenedictKing/ccx/internal/errutil"
	"log"
	"sync"
	"time"
)

// SubscriptionProfile 描述渠道背后的套餐/余额/价格来源。
// Phase 1 仅手动维护，不做余额自动抓取。
type SubscriptionProfile struct {
	SubscriptionUID string `json:"subscriptionUid"`
	DisplayName     string `json:"displayName"`
	Provider        string `json:"provider"` // openai | anthropic | google | relay_x | community_x | custom
	OriginType      string `json:"originType"`
	OriginTier      string `json:"originTier"`

	BillingMode   string   `json:"billingMode"` // official_api | token_plan | prepaid_credit | shared_free | unknown
	Currency      string   `json:"currency,omitempty"`
	Balance       float64  `json:"balance,omitempty"`
	PaymentAmount *float64 `json:"paymentAmount,omitempty"`
	PaymentUnit   string   `json:"paymentUnit,omitempty"`
	CreditAmount  *float64 `json:"creditAmount,omitempty"`
	CreditUnit    string   `json:"creditUnit,omitempty"`
	Version       uint64   `json:"version"`

	// 套餐默认成本倍率；channel/key 可继续覆盖。
	GroupMultipliers   map[string]float64 `json:"groupMultipliers,omitempty"`
	RechargeMultiplier float64            `json:"rechargeMultiplier,omitempty"`

	LinkedChannelUIDs []string `json:"linkedChannelUids,omitempty"`
	Source            string   `json:"source"` // manual | imported | inferred
	Confidence        float64  `json:"confidence"`

	// ── Phase 4 Item 6：余额自动刷新 ──
	// BillingAPIKey 用于查询 provider 账单/余额的专用密钥（与推理 APIKeys 分离）。
	// 很多 provider 的账单 API 需要 admin/org 级密钥而非普通 API key，不能假设两者通用。
	// 未填写则该订阅不参与自动刷新，静默跳过。
	BillingAPIKey string `json:"billingApiKey,omitempty"`

	// AutoRefreshEnabled 单订阅级开关。即使全局 SubscriptionAutoRefresh.Enabled=true，
	// 该订阅也必须 AutoRefreshEnabled=true 且 BillingAPIKey 非空才纳入刷新队列。
	AutoRefreshEnabled bool `json:"autoRefreshEnabled,omitempty"`

	// LastBalanceRefreshAt 最近一次成功刷新余额的时间。
	LastBalanceRefreshAt *time.Time `json:"lastBalanceRefreshAt,omitempty"`

	// LastBalanceRefreshError 最近一次刷新失败的错误信息（成功后清空）。
	LastBalanceRefreshError string `json:"lastBalanceRefreshError,omitempty"`

	// ── 订阅级共享能力（§3.2.3，shadow 展示）──
	SharedCapability *SharedCapability `json:"sharedCapability,omitempty"` // 从同订阅 endpoint 聚合的共享能力

	// ── 订阅级用量窗口（§3.2.4）──
	UsageWindows []UsageWindow `json:"usageWindows,omitempty"` // 订阅级汇总用量窗口

	// ── §8.5.1：new-api 订阅集成（统一账号接入）──
	// BaseURL 是 new-api 站点地址，同时作为自动建渠道的上游 baseURL。
	BaseURL string `json:"baseUrl,omitempty"`
	// AccessToken 是 new-api 系统访问令牌，敏感数据。
	// 与 BillingAPIKey 同等级别处理：允许序列化进 profile_json 以便持久化（否则每次重启都要求用户重填），
	// 但 json tag 用小写 accessToken——toSubscriptionItem 转换 API 响应时必须脱敏（仅显示尾部 4 位），
	// 且任何日志打印都不得包含该字段。
	AccessToken string `json:"accessToken,omitempty"`
	// UserID 对应 New-API-User / User-id 请求头。
	UserID string `json:"userId,omitempty"`
	// AuthTokenMode: "bearer"(默认，Authorization: Bearer <token>) | "raw"（不带 Bearer 前缀，fork 兼容）
	AuthTokenMode string `json:"authTokenMode,omitempty"`
	// ProvisionKeyName 自动建 key 的名称模板，默认 "ccx-autopilot"。
	ProvisionKeyName string `json:"provisionKeyName,omitempty"`
	// ProvisionGroup 建 key 时指定分组，空=默认分组。
	ProvisionGroup string `json:"provisionGroup,omitempty"`
	// ProvisionGroupRatio 是建 key 时经服务端校验的分组倍率。
	ProvisionGroupRatio *float64 `json:"provisionGroupRatio,omitempty"`
	// MaxGroupMultiplier 是允许自动建 key 与调用的最高分组倍率。
	MaxGroupMultiplier *float64 `json:"maxGroupMultiplier,omitempty"`
	// ProvisionModels 建 key 时的 model_limits 白名单，空=不限制。
	ProvisionModels []string `json:"provisionModels,omitempty"`
	// ProvisionedTokenID 是自动建 key 后回填的 new-api 侧令牌 ID（只读展示）。
	ProvisionedTokenID int `json:"provisionedTokenId,omitempty"`
	// ProvisionedKeys 记录自动接入的全部安全分组 Key 元数据，不含明文 Key。
	// 旧的 Provision* 单值字段保留第一把 Key，以兼容已有 API 调用方。
	ProvisionedKeys []NewApiProvisionedKey `json:"provisionedKeys,omitempty"`
	// AvailableModels 是账号可用模型快照（GET /api/user/models），供渠道 supportedModels 参考。
	AvailableModels []string `json:"availableModels,omitempty"`

	// Accounts 是 new-api 订阅的多账号列表（Phase 5）。
	// 每个账号有独立的 accessToken，可分别创建渠道。
	Accounts []NewApiAccount `json:"accounts,omitempty"`

	Notes      string     `json:"notes,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
}

// NewApiProvisionedKey 是自动接入的单把 NewAPI Key 的非敏感元数据。
// Key 明文仅保存在渠道配置中，绝不写入订阅画像或订阅 API 响应。
type NewApiProvisionedKey struct {
	Name            string  `json:"name"`
	Group           string  `json:"group"`
	GroupMultiplier float64 `json:"groupMultiplier"`
	TokenID         int     `json:"tokenId"`
	KeyUID          string  `json:"keyUid,omitempty"`
}

// NewApiAccount 描述 new-api 订阅下的单个账号。
// 用于多账号管理：一个 new-api 订阅可关联多个 accessToken（多个账号）。
type NewApiAccount struct {
	AccountUID    string    `json:"accountUid"`
	AccessToken   string    `json:"accessToken,omitempty"` // 敏感，API 响应中脱敏
	UserID        string    `json:"userId,omitempty"`
	AuthTokenMode string    `json:"authTokenMode,omitempty"`
	DisplayName   string    `json:"displayName,omitempty"` // 用户备注名
	Balance       float64   `json:"balance,omitempty"`
	Status        string    `json:"status,omitempty"` // active | expired | error
	LastCheckedAt time.Time `json:"lastCheckedAt,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// SharedCapability 描述同订阅下所有 endpoint 共享的能力画像。
// 由 BuildSharedCapability 从 endpoint 画像聚合（多数派投票），存储在 SubscriptionProfile 中。
// endpoint 画像通过引用继承，无需重复探测。
type SharedCapability struct {
	// ── 模型列表 ──
	ModelListHash string   `json:"modelListHash"` // 模型列表哈希（排序后 SHA-256）
	ModelList     []string `json:"modelList"`     // 多数派模型列表快照

	// ── 能力标签（多数派投票结果）──
	SupportsVision    bool `json:"supportsVision"`
	SupportsToolCalls bool `json:"supportsToolCalls"`
	SupportsReasoning bool `json:"supportsReasoning"`

	// ── 协议兼容开关快照 ──
	SupportsStreaming  bool `json:"supportsStreaming"`  // 流式支持
	SupportsLongCtx    bool `json:"supportsLongCtx"`    // 长上下文支持
	SupportsMultiModal bool `json:"supportsMultiModal"` // 多模态支持

	// ── 统计 ──
	TotalEndpoints   int      `json:"totalEndpoints"`             // 参与聚合的 endpoint 总数
	ConsistentCount  int      `json:"consistentCount"`            // 与共享能力一致的 endpoint 数
	InconsistentKeys []string `json:"inconsistentKeys,omitempty"` // 与共享能力不一致的 endpointUID 列表

	ProbedAt time.Time `json:"probedAt"` // 最近一次聚合计算时间
}

// SubscriptionStore 管理 SubscriptionProfile 的内存缓存与 SQLite 持久化。
// 复用 ProfileStore 的 SQLite 连接模式：接收 *sql.DB，自建表，JSON 列存本体。
type SubscriptionStore struct {
	db     *sql.DB
	dbPath string // 自管连接时非空；外部传入时为空

	cache map[string]*SubscriptionProfile // key = subscriptionUID
	mu    sync.RWMutex
}

// NewSubscriptionStore 创建 SubscriptionStore，自行管理 SQLite 连接。
// dbPath 为数据库文件路径，启动时自动建表并 loadAll 回内存。
func NewSubscriptionStore(dbPath string) (*SubscriptionStore, error) {
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("[SubscriptionStore-Init] 打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store, err := newSubscriptionStoreFromDB(db, dbPath)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// NewSubscriptionStoreWithDB 使用外部 *sql.DB 创建 SubscriptionStore（便于测试/复用连接）。
// 调用方负责 db 的生命周期管理；Close() 不会关闭该 db。
func NewSubscriptionStoreWithDB(db *sql.DB) (*SubscriptionStore, error) {
	return newSubscriptionStoreFromDB(db, "")
}

func newSubscriptionStoreFromDB(db *sql.DB, dbPath string) (*SubscriptionStore, error) {
	if err := initSubscriptionStoreSchema(db); err != nil {
		return nil, fmt.Errorf("[SubscriptionStore-Init] 建表失败: %w", err)
	}

	store := &SubscriptionStore{
		db:     db,
		dbPath: dbPath,
		cache:  make(map[string]*SubscriptionProfile),
	}

	if err := store.loadAll(); err != nil {
		return nil, fmt.Errorf("[SubscriptionStore-Init] 加载订阅画像失败: %w", err)
	}

	log.Printf("[SubscriptionStore-Init] 初始化完成，已加载 %d 条订阅画像", len(store.cache))
	return store, nil
}

// initSubscriptionStoreSchema 建表迁移。
func initSubscriptionStoreSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS autopilot_subscriptions (
    subscription_uid  TEXT PRIMARY KEY,
    profile_json      TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);
`
	_, err := db.Exec(schema)
	return err
}

// loadAll 从 SQLite 加载全部订阅画像到内存缓存。
func (s *SubscriptionStore) loadAll() error {
	rows, err := s.db.Query("SELECT subscription_uid, profile_json FROM autopilot_subscriptions")
	if err != nil {
		return err
	}
	defer errutil.IgnoreDeferred(rows.Close)

	s.mu.Lock()
	defer s.mu.Unlock()

	for rows.Next() {
		var uid string
		var profileJSON string
		if err := rows.Scan(&uid, &profileJSON); err != nil {
			log.Printf("[SubscriptionStore-LoadAll] 跳过损坏行: %v", err)
			continue
		}
		var profile SubscriptionProfile
		if err := json.Unmarshal([]byte(profileJSON), &profile); err != nil {
			log.Printf("[SubscriptionStore-LoadAll] 反序列化失败 uid=%s: %v", uid, err)
			continue
		}
		s.cache[uid] = &profile
	}
	return rows.Err()
}

// ── CRUD ──

// Create 创建一条订阅画像。SubscriptionUID 不能为空且不能已存在。
func (s *SubscriptionStore) Create(profile *SubscriptionProfile) error {
	if profile == nil || profile.SubscriptionUID == "" {
		return fmt.Errorf("[SubscriptionStore-Create] subscription_uid 不能为空")
	}
	now := time.Now()
	next := cloneSubscriptionProfile(profile)
	next.CreatedAt, next.UpdatedAt = now, now
	if next.Version == 0 {
		next.Version = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.cache[next.SubscriptionUID]; exists {
		return fmt.Errorf("[SubscriptionStore-Create] subscription_uid=%s 已存在", next.SubscriptionUID)
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.cache[next.SubscriptionUID] = next
	*profile = *cloneSubscriptionProfile(next)
	return nil
}

// Get 按 subscriptionUID 从内存缓存获取画像。不存在返回 nil。
func (s *SubscriptionStore) Get(subscriptionUID string) *SubscriptionProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSubscriptionProfile(s.cache[subscriptionUID])
}

// Update 兼容整对象更新；新代码应使用 Patch 避免 last-writer-wins。
func (s *SubscriptionStore) Update(profile *SubscriptionProfile) error {
	if profile == nil || profile.SubscriptionUID == "" {
		return fmt.Errorf("[SubscriptionStore-Update] subscription_uid 不能为空")
	}
	return s.Patch(profile.SubscriptionUID, nil, func(current *SubscriptionProfile) error {
		createdAt, version := current.CreatedAt, current.Version
		*current = *cloneSubscriptionProfile(profile)
		current.CreatedAt, current.Version = createdAt, version
		return nil
	})
}

// Patch 对单个订阅执行原子读改写。持久化成功后才发布新缓存。
func (s *SubscriptionStore) Patch(subscriptionUID string, expectedVersion *uint64, mutate func(*SubscriptionProfile) error) error {
	if subscriptionUID == "" || mutate == nil {
		return fmt.Errorf("[SubscriptionStore-Patch] uid 和 mutate 不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.cache[subscriptionUID]
	if existing == nil {
		return fmt.Errorf("[SubscriptionStore-Patch] subscription_uid=%s 不存在", subscriptionUID)
	}
	if expectedVersion != nil && existing.Version != *expectedVersion {
		return fmt.Errorf("[SubscriptionStore-Patch] version 冲突: current=%d expected=%d", existing.Version, *expectedVersion)
	}
	next := cloneSubscriptionProfile(existing)
	if err := mutate(next); err != nil {
		return err
	}
	if next.SubscriptionUID != subscriptionUID {
		return fmt.Errorf("[SubscriptionStore-Patch] subscription_uid 不可修改")
	}
	next.CreatedAt, next.UpdatedAt, next.Version = existing.CreatedAt, time.Now(), existing.Version+1
	if err := s.persist(next); err != nil {
		return err
	}
	s.cache[subscriptionUID] = next
	return nil
}

// Delete 从内存和 SQLite 删除指定订阅画像。
func (s *SubscriptionStore) Delete(subscriptionUID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec("DELETE FROM autopilot_subscriptions WHERE subscription_uid = ?", subscriptionUID); err != nil {
		return fmt.Errorf("[SubscriptionStore-Delete] 删除失败 uid=%s: %w", subscriptionUID, err)
	}
	delete(s.cache, subscriptionUID)
	return nil
}

// ListAll 返回全部订阅画像副本。
func (s *SubscriptionStore) ListAll() []*SubscriptionProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*SubscriptionProfile, 0, len(s.cache))
	for _, profile := range s.cache {
		result = append(result, cloneSubscriptionProfile(profile))
	}
	return result
}

// LinkChannel 将 channelUID 关联到指定订阅。
func (s *SubscriptionStore) LinkChannel(subscriptionUID, channelUID string) error {
	return s.Patch(subscriptionUID, nil, func(p *SubscriptionProfile) error {
		for _, uid := range p.LinkedChannelUIDs {
			if uid == channelUID {
				return nil
			}
		}
		p.LinkedChannelUIDs = append(p.LinkedChannelUIDs, channelUID)
		return nil
	})
}

// UnlinkChannel 从指定订阅解绑 channelUID。
func (s *SubscriptionStore) UnlinkChannel(subscriptionUID, channelUID string) error {
	return s.Patch(subscriptionUID, nil, func(p *SubscriptionProfile) error {
		filtered := make([]string, 0, len(p.LinkedChannelUIDs))
		for _, uid := range p.LinkedChannelUIDs {
			if uid != channelUID {
				filtered = append(filtered, uid)
			}
		}
		p.LinkedChannelUIDs = filtered
		return nil
	})
}

// AddAccount 向指定订阅添加一个账号。
func (s *SubscriptionStore) AddAccount(subscriptionUID string, account NewApiAccount) error {
	return s.Patch(subscriptionUID, nil, func(p *SubscriptionProfile) error {
		for _, existing := range p.Accounts {
			if existing.AccountUID == account.AccountUID {
				return fmt.Errorf("[SubscriptionStore-AddAccount] account_uid=%s 已存在", account.AccountUID)
			}
		}
		p.Accounts = append(p.Accounts, account)
		return nil
	})
}

// RemoveAccount 从指定订阅删除一个账号。
func (s *SubscriptionStore) RemoveAccount(subscriptionUID, accountUID string) error {
	return s.Patch(subscriptionUID, nil, func(p *SubscriptionProfile) error {
		filtered := make([]NewApiAccount, 0, len(p.Accounts))
		for _, account := range p.Accounts {
			if account.AccountUID != accountUID {
				filtered = append(filtered, account)
			}
		}
		if len(filtered) == len(p.Accounts) {
			return fmt.Errorf("[SubscriptionStore-RemoveAccount] account_uid=%s 不存在", accountUID)
		}
		p.Accounts = filtered
		return nil
	})
}

func cloneSubscriptionProfile(profile *SubscriptionProfile) *SubscriptionProfile {
	if profile == nil {
		return nil
	}
	data, err := json.Marshal(profile)
	if err != nil {
		return nil
	}
	var cloned SubscriptionProfile
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil
	}
	return &cloned
}

// persist 将单条订阅画像写入 SQLite（upsert）。
func (s *SubscriptionStore) persist(profile *SubscriptionProfile) error {
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("[SubscriptionStore-Persist] 序列化失败 uid=%s: %w", profile.SubscriptionUID, err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`
INSERT INTO autopilot_subscriptions (subscription_uid, profile_json, created_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(subscription_uid) DO UPDATE SET
    profile_json = excluded.profile_json,
    updated_at = excluded.updated_at
`, profile.SubscriptionUID, string(profileJSON), now, now)

	if err != nil {
		return fmt.Errorf("[SubscriptionStore-Persist] 写入失败 uid=%s: %w", profile.SubscriptionUID, err)
	}
	return nil
}

// Close 关闭 SubscriptionStore。
// 仅自管连接（NewSubscriptionStore）会关闭 db；NewSubscriptionStoreWithDB 不关闭。
func (s *SubscriptionStore) Close() error {
	if s.dbPath != "" {
		if err := s.db.Close(); err != nil {
			return fmt.Errorf("[SubscriptionStore-Close] 关闭数据库失败: %w", err)
		}
	}
	return nil
}
