package autopilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/presetstore"
	"github.com/gin-gonic/gin"
)

// ─── new-api 订阅集成路由（§8.5.1）──────────────────────────────────────────

// newApiDefaults 返回 new-api 接入的建议预填值：优先取 presetstore，
// 缺失字段回退到编译期兜底（relay/second/token_plan）。
func newApiDefaults() presetstore.NewApiDefaults {
	d := presetstore.Default().Subscription().NewApiDefaults
	if d.OriginType == "" {
		d.OriginType = "relay"
	}
	if d.OriginTier == "" {
		d.OriginTier = "second"
	}
	if d.BillingMode == "" {
		d.BillingMode = "token_plan"
	}
	return d
}

// NewApiRouteDeps new-api 端点所需的依赖注入。
// Verify 端点只需要 SubscriptionStore；Provision 端点额外需要 CfgManager + Runner 来建渠道并触发 Discovery。
type NewApiRouteDeps struct {
	Store       *SubscriptionStore
	CfgManager  *config.ConfigManager
	Runner      *AutoDiscoveryRunner
	SyncService *NewApiSubscriptionSyncService
}

// NewAPI provision 是管理面低频操作。按 baseURL+user 的站点级锁串行化远端 key 的
// 查重/创建（FindTokenByName + Create），避免同一上游账号并发创建同名 key；
// 订阅画像与渠道写入则用与 sync service 共享的 per-订阅-UID 锁互斥（见 SyncNow）。
// 锁实例常驻 map（基数 = 上游账号数 / 订阅数，有界），不会无限增长。
var (
	newAPIProvisionSiteLocksMu sync.Mutex
	newAPIProvisionSiteLocks   = map[string]*sync.Mutex{}

	newAPIProvisionUIDLocksMu sync.Mutex
	newAPIProvisionUIDLocks   = map[string]*sync.Mutex{}
)

func lockForKeyFrom(locksMu *sync.Mutex, locks map[string]*sync.Mutex, key string) *sync.Mutex {
	locksMu.Lock()
	defer locksMu.Unlock()
	if lock := locks[key]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	locks[key] = lock
	return lock
}

// newAPIProvisionSiteKey 远端 key 查重/创建的互斥粒度：同一 new-api 站点 + 同一用户。
// userID 在请求里可能缺省（由 verify 派生），此时退回 accessToken 区分账号。
func newAPIProvisionSiteKey(baseURL, userID, accessToken string) string {
	identity := strings.TrimSpace(userID)
	if identity == "" {
		identity = "tok:" + strings.TrimSpace(accessToken)
	}
	return normalizeNewApiChannelURL(baseURL) + "|" + identity
}

// withNewAPIProvisionUIDLock 在订阅 uid 的 per-UID 锁内执行 fn。
// 优先复用 sync service 的锁 map，保证 provision 与 SyncNow 交叉互斥；
// SyncService 缺失（部分测试场景）时退回到本包级 per-UID 锁。
// uid 为空时不持锁直接执行。
func withNewAPIProvisionUIDLock(deps *NewApiRouteDeps, uid string, fn func()) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		fn()
		return
	}
	if deps != nil && deps.SyncService != nil {
		deps.SyncService.WithSubscriptionLock(uid, fn)
		return
	}
	lock := lockForKeyFrom(&newAPIProvisionUIDLocksMu, newAPIProvisionUIDLocks, uid)
	lock.Lock()
	defer lock.Unlock()
	fn()
}

// StableAccountUID 从 new-api 订阅 UID 派生稳定的托管账号身份。
// 同订阅下的多协议渠道共享该 AccountUID，使 RebuildLogicalChannels 按账号归组时
// 能把它们收敛到同一逻辑卡；重复 provision/同步后 AccountUID 保持不变。
// 注意：config 层（config_accounts.go deriveNewApiAccountUID，AutoManaged 渠道回填）
// 复制了同一段派生逻辑，两处算法必须保持一致。
func StableAccountUID(subscriptionUID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("newapi|account|%s", subscriptionUID)))
	return "newapi_" + hex.EncodeToString(sum[:8])
}

// RegisterNewApiSubscriptionRoutes 注册 new-api 集成的两个核心端点：
//
//	POST /subscriptions/newapi/verify    —— 校验令牌 + 预览账户/分组/模型（不落库）
//	POST /subscriptions/newapi/provision —— 完整流程：建 profile + 建 key + 建渠道 + 触发 Discovery
func RegisterNewApiSubscriptionRoutes(router gin.IRouter, deps *NewApiRouteDeps) {
	if deps == nil || deps.Store == nil {
		log.Printf("[NewApi-Routes] 依赖缺失，跳过注册")
		return
	}
	group := router.Group("/subscriptions")
	group.POST("/newapi/verify", handleNewApiVerify(deps))
	group.POST("/newapi/provision", handleNewApiProvision(deps))
}

// ─── 请求/响应类型 ───

// NewApiVerifyRequest POST /subscriptions/newapi/verify 请求体。
type NewApiVerifyRequest struct {
	BaseURL         string `json:"baseUrl" binding:"required"`
	AccessToken     string `json:"accessToken" binding:"required"`
	UserID          string `json:"userId"`
	AuthTokenMode   string `json:"authTokenMode,omitempty"`
	DisplayName     string `json:"displayName,omitempty"`
	SubscriptionUID string `json:"subscriptionUid,omitempty"`
	// ProxyURL 可选代理（HTTP/HTTPS/SOCKS5），用于直连被地域封锁的站点。
	ProxyURL string `json:"proxyUrl,omitempty"`
	// ProxyPreferDirect 配置了代理时先直连，失败自动回退代理。
	ProxyPreferDirect bool `json:"proxyPreferDirect,omitempty"`
}

// NewApiVerifyResponse POST /subscriptions/newapi/verify 响应体（不落库）。
type NewApiVerifyResponse struct {
	Username        string             `json:"username"`
	UserID          int                `json:"userId"`
	Quota           int64              `json:"quota"`
	UsedQuota       int64              `json:"usedQuota"`
	Groups          map[string]float64 `json:"groups"`
	GroupFetchError string             `json:"groupFetchError,omitempty"`
	AvailableModels []string           `json:"availableModels"`
	// 派生建议：前端可直接展示
	SuggestedOriginType string `json:"suggestedOriginType"`
	SuggestedOriginTier string `json:"suggestedOriginTier"`
	AccessTokenMasked   string `json:"accessTokenMasked"`
}

// NewApiProvisionRequest POST /subscriptions/newapi/provision 请求体。
type NewApiProvisionRequest struct {
	SubscriptionUID  string `json:"subscriptionUid" binding:"required"`
	DisplayName      string `json:"displayName" binding:"required"`
	BaseURL          string `json:"baseUrl" binding:"required"`
	AccessToken      string `json:"accessToken" binding:"required"`
	UserID           string `json:"userId"`
	AuthTokenMode    string `json:"authTokenMode,omitempty"`
	ChannelKind      string `json:"channelKind" binding:"required"` // messages/chat/responses/gemini
	ChannelName      string `json:"channelName,omitempty"`
	ProvisionKeyName string `json:"provisionKeyName,omitempty"`
	ProvisionGroup   string `json:"provisionGroup,omitempty"`
	// ProvisionAllEligibleGroups 明确启用“阈值内全部分组”的自动接入。
	// 空分组请求同样按上游分组计划处理；单分组模式必须显式传 ProvisionGroup。
	ProvisionAllEligibleGroups bool     `json:"provisionAllEligibleGroups,omitempty"`
	ProvisionModels            []string `json:"provisionModels,omitempty"`
	// MaxGroupMultiplier 限制自动建 Key 与调用允许使用的最高分组倍率；缺省时保守使用 1.0。
	MaxGroupMultiplier *float64 `json:"maxGroupMultiplier,omitempty"`
	Notes              string   `json:"notes,omitempty"`
	// ProxyURL 可选代理（HTTP/HTTPS/SOCKS5），绑定全流程经代理访问并写入所建渠道。
	ProxyURL string `json:"proxyUrl,omitempty"`
	// ProxyPreferDirect 配置了代理时先直连，失败自动回退代理；随 ProxyURL 一并写入渠道。
	ProxyPreferDirect bool `json:"proxyPreferDirect,omitempty"`
}

// NewApiProvisionResponse POST /subscriptions/newapi/provision 响应体。
type NewApiProvisionResponse struct {
	Subscription       SubscriptionItem               `json:"subscription"`
	ChannelUID         string                         `json:"channelUid"`
	ChannelIndex       int                            `json:"channelIndex"`
	ChannelName        string                         `json:"channelName,omitempty"`
	MergedChannel      bool                           `json:"mergedChannel,omitempty"` // true 表示同站点已有渠道，key 已并入而非新建
	ProvisionedKey     string                         `json:"provisionedKey"`          // 明文 key，仅此次返回，前端必须立即转给渠道；后续只展示脱敏/不回显
	ProvisionedTokenID int                            `json:"provisionedTokenId"`
	Reused             bool                           `json:"reused"` // true 表示全部 Key 都复用了已存在的同名 key
	ProvisionedKeys    []NewApiProvisionedKeyResponse `json:"provisionedKeys,omitempty"`
	DiscoveryStarted   bool                           `json:"discoveryStarted"`
}

// NewApiProvisionedKeyResponse 是一把自动接入 Key 的非敏感结果。
type NewApiProvisionedKeyResponse struct {
	Name            string  `json:"name"`
	Group           string  `json:"group"`
	GroupMultiplier float64 `json:"groupMultiplier"`
	TokenID         int     `json:"tokenId"`
	Reused          bool    `json:"reused"`
}

type newApiProvisionedKey struct {
	NewApiProvisionedKey
	Key    string
	Reused bool
}

type newApiProvisionConflictError struct {
	err error
}

func (e *newApiProvisionConflictError) Error() string {
	return e.err.Error()
}

// cleanupNewApiProvisionedKeys 仅回收本次请求新建、尚未绑定渠道的远端 Key。
// 回收失败不会覆盖主错误，但会留下明确日志供用户在上游面板手动处理。
func cleanupNewApiProvisionedKeys(ctx context.Context, adapter *NewApiAdapter, req NewApiProvisionRequest, userID string, keys []newApiProvisionedKey) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	for i := len(keys) - 1; i >= 0; i-- {
		key := keys[i]
		if key.Reused || key.TokenID <= 0 {
			continue
		}
		if err := adapter.DeleteToken(cleanupCtx, req.BaseURL, req.AccessToken, userID, req.AuthTokenMode, key.TokenID); err != nil {
			log.Printf("[NewApi-Provision] 回收未绑定 key 失败 name=%s group=%s tokenID=%d: %v", key.Name, key.Group, key.TokenID, err)
		}
	}
}

// provisionNewApiGroupKeys 为各分组创建/复用代理 Key。namePrefix 用于多账号场景给 key 名加账号前缀，
// 避免与主账号或其他账号在同站点下的同名 key 被 FindTokenByName 误复用；主账号调用传空串保持旧命名。
func provisionNewApiGroupKeys(
	ctx context.Context,
	adapter *NewApiAdapter,
	req NewApiProvisionRequest,
	userID string,
	groups []newApiResolvedGroup,
	namePrefix string,
) ([]newApiProvisionedKey, error) {
	if len(groups) == 0 {
		return nil, fmt.Errorf("没有可创建 key 的分组")
	}
	if req.ProvisionAllEligibleGroups && strings.TrimSpace(req.ProvisionKeyName) != "" {
		return nil, fmt.Errorf("自动接入全部合格分组时不支持 provisionKeyName")
	}

	names := make([]string, len(groups))
	nameGroups := make(map[string]string, len(groups))
	for i, group := range groups {
		name := strings.TrimSpace(req.ProvisionKeyName)
		if name == "" {
			name = defaultNewApiProvisionKeyNameForGroup(group.Name)
		}
		name = namePrefix + name
		if previousGroup, exists := nameGroups[name]; exists && previousGroup != group.Name {
			return nil, fmt.Errorf("分组 %q 与 %q 生成了相同的 key 名称 %q", previousGroup, group.Name, name)
		}
		names[i] = name
		nameGroups[name] = group.Name
	}

	provisioned := make([]newApiProvisionedKey, 0, len(groups))
	seenKeys := make(map[string]string, len(groups))
	// 站点级锁只覆盖远端 key 的查重/创建窗口，不覆盖订阅画像与渠道写入。
	siteLock := lockForKeyFrom(&newAPIProvisionSiteLocksMu, newAPIProvisionSiteLocks, newAPIProvisionSiteKey(req.BaseURL, userID, req.AccessToken))
	siteLock.Lock()
	defer siteLock.Unlock()
	rollback := func(extra ...newApiProvisionedKey) {
		keys := append([]newApiProvisionedKey(nil), provisioned...)
		keys = append(keys, extra...)
		cleanupNewApiProvisionedKeys(ctx, adapter, req, userID, keys)
	}
	for i, group := range groups {
		tokenID, keyPlain, reused, err := adapter.ProvisionKey(ctx, req.BaseURL, req.AccessToken, userID, req.AuthTokenMode, NewApiProvisionOptions{
			Name:   names[i],
			Group:  group.Name,
			Models: req.ProvisionModels,
		})
		if err != nil {
			if tokenID > 0 && !reused {
				rollback(newApiProvisionedKey{NewApiProvisionedKey: NewApiProvisionedKey{Name: names[i], Group: group.Name, GroupMultiplier: group.Ratio, TokenID: tokenID}})
			} else {
				rollback()
			}
			return nil, fmt.Errorf("分组 %q 建 key 失败: %w", group.Name, err)
		}
		current := newApiProvisionedKey{
			NewApiProvisionedKey: NewApiProvisionedKey{
				Name:            names[i],
				Group:           group.Name,
				GroupMultiplier: group.Ratio,
				TokenID:         tokenID,
			},
			Key:    keyPlain,
			Reused: reused,
		}
		if keyPlain == "" {
			rollback(current)
			return nil, &newApiProvisionConflictError{err: fmt.Errorf("分组 %q 的同名 key=%s 未返回明文，无法直接绑定，请删除后重试或手动填 key", group.Name, names[i])}
		}
		// 兜底：掩码 key 绝不能当明文绑定（会导致必然 403 并污染黑名单）。
		// 正常流程中适配器已通过揭示端点换回明文，此处拦截异常链路。
		if IsMaskedNewApiKey(keyPlain) {
			rollback(current)
			return nil, &newApiProvisionConflictError{err: fmt.Errorf("分组 %q 的 key=%s 仍为掩码形态，已阻止绑定，请手动填写 key", group.Name, names[i])}
		}
		if previousGroup, exists := seenKeys[keyPlain]; exists && previousGroup != group.Name {
			rollback(current)
			return nil, fmt.Errorf("分组 %q 与 %q 返回相同的 key，已阻止绑定", previousGroup, group.Name)
		}
		seenKeys[keyPlain] = group.Name
		provisioned = append(provisioned, current)
	}
	return provisioned, nil
}

// ─── Handler ───

// normalizeNewApiChannelURL 规范化 baseURL，用于同站点渠道匹配。
func normalizeNewApiChannelURL(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}

// findNewApiMergeTarget 在同 kind 渠道中查找与 baseURL 同站点的已有渠道作为合并目标：
// 同一站点的多个 new-api 订阅与已有纯 key 渠道应整合在同一个渠道中，而不是分散新建。
// 跳过带 providerId 的官方/已知 provider 渠道；多个命中时优先 active，再取列表中最靠前者。
// 找不到返回 (-1, false)。
func findNewApiMergeTarget(cfg config.Config, kind, baseURL string) (int, bool) {
	want := normalizeNewApiChannelURL(baseURL)
	if want == "" {
		return -1, false
	}
	channels := getChannelSlice(cfg, kind)
	match := func(ch config.UpstreamConfig) bool {
		if strings.TrimSpace(ch.ProviderID) != "" {
			return false
		}
		if strings.EqualFold(normalizeNewApiChannelURL(ch.BaseURL), want) {
			return true
		}
		for _, u := range ch.BaseURLs {
			if strings.EqualFold(normalizeNewApiChannelURL(u), want) {
				return true
			}
		}
		return false
	}
	fallback := -1
	for i, ch := range channels {
		if !match(ch) {
			continue
		}
		if ch.Status == "" || ch.Status == "active" {
			return i, true
		}
		if fallback < 0 {
			fallback = i
		}
	}
	if fallback >= 0 {
		return fallback, true
	}
	return -1, false
}

// updateChannelForKind 按渠道类型调用对应的 UpdateXxxUpstream。
func updateChannelForKind(cm *config.ConfigManager, kind string, index int, updates config.UpstreamUpdate) (bool, error) {
	switch kind {
	case "messages":
		return cm.UpdateUpstream(index, updates)
	case "chat":
		return cm.UpdateChatUpstream(index, updates)
	case "responses":
		return cm.UpdateResponsesUpstream(index, updates)
	case "gemini":
		return cm.UpdateGeminiUpstream(index, updates)
	case "images":
		return cm.UpdateImagesUpstream(index, updates)
	case "vectors":
		return cm.UpdateVectorsUpstream(index, updates)
	}
	return false, fmt.Errorf("不支持的渠道类型: %s", kind)
}

// handleNewApiVerify 校验 new-api 凭据 + 预览账户/分组/模型信息（不写入数据库）。
func handleNewApiVerify(deps *NewApiRouteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req NewApiVerifyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}
		if req.AccessToken == "" || req.BaseURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "baseUrl 和 accessToken 必填"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()

		adapter := NewApiAdapterForProxy(req.ProxyURL, req.ProxyPreferDirect)

		// 1) 校验 + 取用户信息（支持 New-API-User header 缺失回退）
		self, derivedUserID, err := adapter.VerifyWithFallback(ctx, req.BaseURL, req.AccessToken, req.UserID, req.AuthTokenMode)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("校验失败: %v", err)})
			return
		}

		// 2) 拉分组倍率（验证阶段不阻断，但把失败原因明确回传给界面）。
		groups, groupErr := adapter.FetchGroups(ctx, req.BaseURL, req.AccessToken, derivedUserID, req.AuthTokenMode)
		groupFetchError := ""
		if groupErr != nil {
			groupFetchError = groupErr.Error()
		}

		// 3) 拉可用模型（失败不阻断）
		models, _ := adapter.FetchModels(ctx, req.BaseURL, req.AccessToken, derivedUserID, req.AuthTokenMode)

		defaults := newApiDefaults()
		resp := NewApiVerifyResponse{
			Username:            self.Username,
			UserID:              self.ID,
			Quota:               self.Quota,
			UsedQuota:           self.UsedQuota,
			Groups:              groups,
			GroupFetchError:     groupFetchError,
			AvailableModels:     models,
			SuggestedOriginType: defaults.OriginType,
			SuggestedOriginTier: defaults.OriginTier,
			AccessTokenMasked:   maskAccessToken(req.AccessToken),
		}
		c.JSON(http.StatusOK, resp)
	}
}

// handleNewApiProvision 完整流程：建 profile + 建 key + 建渠道 + 触发 Discovery。
func handleNewApiProvision(deps *NewApiRouteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.CfgManager == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "配置管理器未就绪，无法建渠道"})
			return
		}

		var req NewApiProvisionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}
		if req.AccessToken == "" || req.BaseURL == "" || req.SubscriptionUID == "" || req.DisplayName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "subscriptionUid、displayName、baseUrl、accessToken、channelKind 必填"})
			return
		}
		if !validChannelKinds[req.ChannelKind] {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("不支持的渠道类型: %s", req.ChannelKind)})
			return
		}

		// 提前校验 subscriptionUid 唯一性，避免白白在 new-api 侧建 key 后才发现 profile 冲突。
		// Store.Create 原子兜底二次校验，并发同 uid 在临界区串行化。
		if deps.Store.Get(req.SubscriptionUID) != nil {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("subscriptionUid=%s 已存在", req.SubscriptionUID)})
			return
		}
		if req.ProvisionAllEligibleGroups && strings.TrimSpace(req.ProvisionKeyName) != "" {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "自动接入全部合格分组时不支持 provisionKeyName"})
			return
		}
		// 同站点已有渠道时把 key 并入该渠道而非新建：多 new-api 订阅与纯 key 渠道应整合为同一渠道。
		mergeIndex, mergeFound := findNewApiMergeTarget(deps.CfgManager.GetConfig(), req.ChannelKind, req.BaseURL)

		channelName := strings.TrimSpace(req.ChannelName)
		if channelName == "" {
			channelName = fmt.Sprintf("newapi-%s-%d", req.ChannelKind, time.Now().UnixMilli()%100000)
		}
		if !mergeFound {
			for _, existing := range getChannelSlice(deps.CfgManager.GetConfig(), req.ChannelKind) {
				if existing.Name == channelName {
					c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("渠道名称 %q 已存在", channelName)})
					return
				}
			}
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		adapter := NewApiAdapterForProxy(req.ProxyURL, req.ProxyPreferDirect)

		// 1) 校验 + 拉用户信息（支持 New-API-User header 缺失回退）
		self, derivedUserID, err := adapter.VerifyWithFallback(ctx, req.BaseURL, req.AccessToken, req.UserID, req.AuthTokenMode)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("校验失败: %v", err)})
			return
		}

		// 2) 拉分组倍率并强制校验。分组未知时不能安全决定要创建或调用哪一把 Key。
		groups, err := adapter.FetchGroups(ctx, req.BaseURL, req.AccessToken, derivedUserID, req.AuthTokenMode)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("无法获取分组倍率，已阻止自动建 key: %v", err)})
			return
		}
		resolvedGroups, err := resolveNewApiProvisionGroups(groups, req.ProvisionGroup, req.ProvisionAllEligibleGroups, req.MaxGroupMultiplier)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "分组倍率校验失败: " + err.Error()})
			return
		}

		// 模型清单只用于订阅画像和后续 Discovery，不影响分组安全闸门。
		models, _ := adapter.FetchModels(ctx, req.BaseURL, req.AccessToken, derivedUserID, req.AuthTokenMode)

		// 3) 为全部合格分组分别建/复用代理 Key。一个 Key 固定绑定一个上游分组，
		// 不会因为同渠道的其他分组而越过用户设置的倍率上限。
		provisioned, err := provisionNewApiGroupKeys(ctx, adapter, req, derivedUserID, resolvedGroups, "")
		if err != nil {
			var conflict *newApiProvisionConflictError
			if errors.As(err, &conflict) {
				c.JSON(http.StatusConflict, gin.H{"error": "建 key 失败: " + conflict.Error()})
				return
			}
			var keyConflict *NewApiProvisionKeyConflictError
			if errors.As(err, &keyConflict) {
				c.JSON(http.StatusConflict, gin.H{"error": "建 key 失败: " + keyConflict.Error()})
				return
			}
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("建 key 失败: %v", err)})
			return
		}

		// 4)~7) 建 profile + 写入渠道 + 关联订阅 + 触发 Discovery：
		// 全程持有订阅 uid 的 per-UID 锁（与 sync service 共享），串行化对同一订阅的 store/渠道写。
		// 抢到锁后先校验 uid 唯一性，挡住锁外 fast-path 与 Create 之间的并发同 uid 窗口。
		withNewAPIProvisionUIDLock(deps, req.SubscriptionUID, func() {
			if deps.Store.Get(req.SubscriptionUID) != nil {
				c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("subscriptionUid=%s 已存在", req.SubscriptionUID)})
				return
			}
			now := time.Now()
			defaults := newApiDefaults()
			primaryKey := provisioned[0]
			provisionGroupRatio := primaryKey.GroupMultiplier
			maxGroupMultiplier := resolvedGroups[0].MaxMultiplier
			profileKeys := make([]NewApiProvisionedKey, 0, len(provisioned))
			apiKeys := make([]string, 0, len(provisioned))
			apiKeyConfigs := make([]config.APIKeyConfig, 0, len(provisioned))
			allReused := true
			for _, key := range provisioned {
				profileKeys = append(profileKeys, key.NewApiProvisionedKey)
				apiKeys = append(apiKeys, key.Key)
				ratio := key.GroupMultiplier
				limit := maxGroupMultiplier
				apiKeyConfigs = append(apiKeyConfigs, config.APIKeyConfig{
					Key:                   key.Key,
					Name:                  "new-api:" + key.Group,
					QuotaGroup:            key.Group,
					GroupMultiplier:       &ratio,
					MaxGroupMultiplier:    &limit,
					SourceSubscriptionUID: req.SubscriptionUID,
				})
				if !key.Reused {
					allReused = false
				}
			}
			profile := &SubscriptionProfile{
				SubscriptionUID:   req.SubscriptionUID,
				DisplayName:       req.DisplayName,
				Provider:          "new_api",
				OriginType:        defaults.OriginType,
				OriginTier:        defaults.OriginTier,
				BillingMode:       defaults.BillingMode,
				Currency:          "quota",
				Balance:           float64(self.Quota),
				GroupMultipliers:  groups,
				LinkedChannelUIDs: []string{},
				Source:            "newapi_provision",
				Confidence:        0.95,
				Notes:             req.Notes,
				// §8.5.1
				BaseURL:             req.BaseURL,
				AccessToken:         req.AccessToken, // 持久化但不出 API 响应
				UserID:              derivedUserID,
				Username:            self.Username,
				AuthTokenMode:       req.AuthTokenMode,
				ProxyURL:            strings.TrimSpace(req.ProxyURL),
				ProxyPreferDirect:   req.ProxyPreferDirect,
				ProvisionKeyName:    primaryKey.Name,
				ProvisionGroup:      primaryKey.Group,
				ProvisionGroupRatio: &provisionGroupRatio,
				MaxGroupMultiplier:  &maxGroupMultiplier,
				ProvisionModels:     req.ProvisionModels,
				ProvisionedTokenID:  primaryKey.TokenID,
				ProvisionedKeys:     profileKeys,
				AvailableModels:     models,
				AutoRefreshEnabled:  false, // new-api 走 Verify，不直接接 SubscriptionBalanceFetcher
				CreatedAt:           now,
				UpdatedAt:           now,
			}
			if err := deps.Store.Create(profile); err != nil {
				cleanupNewApiProvisionedKeys(ctx, adapter, req, derivedUserID, provisioned)
				if strings.Contains(err.Error(), "已存在") {
					c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				} else {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				}
				return
			}

			// 5) 写入渠道：同站点已有渠道则并入，否则新建
			var channelUID string
			resolvedChannelName := channelName
			channelIndex := -1
			if mergeFound {
				channels := getChannelSlice(deps.CfgManager.GetConfig(), req.ChannelKind)
				if mergeIndex >= len(channels) {
					_ = deps.Store.Delete(req.SubscriptionUID)
					cleanupNewApiProvisionedKeys(ctx, adapter, req, derivedUserID, provisioned)
					c.JSON(http.StatusInternalServerError, gin.H{"error": "合并目标渠道已失效"})
					return
				}
				target := channels[mergeIndex]
				channelUID = target.ChannelUID
				if channelUID == "" {
					channelUID = config.GenerateChannelUID()
				}
				resolvedChannelName = target.Name

				// 已有纯 key 及其配置全部保留，新 key 按字符串去重后追加并补分组元数据
				existingKeys := make(map[string]struct{}, len(target.APIKeys)+len(target.APIKeyConfigs)+len(provisioned))
				mergedKeys := make([]string, 0, len(target.APIKeys)+len(provisioned))
				for _, k := range target.APIKeys {
					existingKeys[k] = struct{}{}
					mergedKeys = append(mergedKeys, k)
				}
				mergedConfigs := append([]config.APIKeyConfig(nil), target.APIKeyConfigs...)
				for _, kc := range target.APIKeyConfigs {
					existingKeys[kc.Key] = struct{}{}
				}
				for _, key := range provisioned {
					if _, exists := existingKeys[key.Key]; exists {
						continue
					}
					existingKeys[key.Key] = struct{}{}
					mergedKeys = append(mergedKeys, key.Key)
					ratio := key.GroupMultiplier
					limit := maxGroupMultiplier
					mergedConfigs = append(mergedConfigs, config.APIKeyConfig{
						Key:                key.Key,
						Name:               "new-api:" + key.Group,
						QuotaGroup:         key.Group,
						GroupMultiplier:    &ratio,
						MaxGroupMultiplier: &limit,
					})
				}
				autoManaged := true
				newApiKind := "new_api"
				updates := config.UpstreamUpdate{
					APIKeys:         mergedKeys,
					APIKeyConfigs:   mergedConfigs,
					AutoManaged:     &autoManaged,
					AutoManagedKind: &newApiKind,
				}
				// 绑定请求显式携带代理设置时，同步到合并目标渠道
				if trimmedProxy := strings.TrimSpace(req.ProxyURL); trimmedProxy != "" {
					updates.ProxyURL = &trimmedProxy
					updates.ProxyPreferDirect = &req.ProxyPreferDirect
				}
				if target.AutoManagedAt == nil {
					updates.AutoManagedAt = &now
				}
				if _, err = updateChannelForKind(deps.CfgManager, req.ChannelKind, mergeIndex, updates); err != nil {
					_ = deps.Store.Delete(req.SubscriptionUID)
					cleanupNewApiProvisionedKeys(ctx, adapter, req, derivedUserID, provisioned)
					c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("合并进已有渠道失败: %v", err)})
					return
				}
				channelIndex = mergeIndex
			} else {
				// 新建上游渠道
				serviceType := kindToDefaultServiceType(req.ChannelKind)
				channelUID = config.GenerateChannelUID()
				upstream := config.UpstreamConfig{
					Name:              channelName,
					ChannelUID:        channelUID,
					AccountUID:        StableAccountUID(req.SubscriptionUID),
					BaseURL:           strings.TrimRight(req.BaseURL, "/"),
					BaseURLs:          []string{strings.TrimRight(req.BaseURL, "/")},
					APIKeys:           apiKeys,
					APIKeyConfigs:     apiKeyConfigs,
					ServiceType:       serviceType,
					Status:            "active",
					AutoManaged:       true,
					AutoManagedAt:     &now,
					AutoManagedKind:   "new_api",
					OriginType:        "relay",
					OriginTier:        "second",
					ProxyURL:          strings.TrimSpace(req.ProxyURL),
					ProxyPreferDirect: req.ProxyPreferDirect,
				}

				switch req.ChannelKind {
				case "messages":
					err = deps.CfgManager.AddUpstream(upstream)
				case "chat":
					err = deps.CfgManager.AddChatUpstream(upstream)
				case "responses":
					err = deps.CfgManager.AddResponsesUpstream(upstream)
				case "gemini":
					err = deps.CfgManager.AddGeminiUpstream(upstream)
				case "images":
					err = deps.CfgManager.AddImagesUpstream(upstream)
				case "vectors":
					err = deps.CfgManager.AddVectorsUpstream(upstream)
				}
				if err != nil {
					// 渠道建失败：回滚 profile（最佳努力删除）
					_ = deps.Store.Delete(req.SubscriptionUID)
					cleanupNewApiProvisionedKeys(ctx, adapter, req, derivedUserID, provisioned)
					c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("建渠道失败: %v", err)})
					return
				}

				// 名称会按首个 baseURL 自动派生，使用稳定 ChannelUID 定位新建渠道。
				for i, ch := range getChannelSlice(deps.CfgManager.GetConfig(), req.ChannelKind) {
					if ch.ChannelUID == channelUID {
						channelIndex = i
						break
					}
				}
				if channelIndex < 0 {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "渠道已建但无法定位"})
					return
				}
			}

			// 6) 关联订阅
			if err := deps.Store.LinkChannel(req.SubscriptionUID, channelUID); err != nil {
				log.Printf("[NewApi-Provision] 关联渠道失败 subscription=%s channel=%s: %v", req.SubscriptionUID, channelUID, err)
			}

			// 7) 触发 Discovery（best-effort）
			discoveryStarted := false
			if deps.Runner != nil {
				channels := getChannelSlice(deps.CfgManager.GetConfig(), req.ChannelKind)
				if channelIndex >= 0 && channelIndex < len(channels) {
					ch := channels[channelIndex]
					discoveryStarted = deps.Runner.TriggerDiscovery(channelUID, &ch, deps.CfgManager)
				}
			}

			responseKeys := make([]NewApiProvisionedKeyResponse, 0, len(provisioned))
			for _, key := range provisioned {
				responseKeys = append(responseKeys, NewApiProvisionedKeyResponse{
					Name:            key.Name,
					Group:           key.Group,
					GroupMultiplier: key.GroupMultiplier,
					TokenID:         key.TokenID,
					Reused:          key.Reused,
				})
			}
			log.Printf("[NewApi-Provision] 完成 subscription=%s channelUID=%s merged=%v groups=%d maxRatio=%.4g allReused=%v discovery=%v",
				req.SubscriptionUID, channelUID, mergeFound, len(provisioned), maxGroupMultiplier, allReused, discoveryStarted)

			fresh := deps.Store.Get(req.SubscriptionUID)
			if fresh == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "订阅已建但无法回读"})
				return
			}
			if deps.SyncService != nil {
				plaintextByToken := make(map[int64]string, len(provisioned))
				for _, key := range provisioned {
					plaintextByToken[int64(key.TokenID)] = key.Key
				}
				if err := deps.SyncService.ReconcileProvisioned(fresh, plaintextByToken); err != nil {
					c.JSON(http.StatusConflict, gin.H{"error": "同步 key ownership 失败: " + err.Error()})
					return
				}
				fresh = deps.Store.Get(req.SubscriptionUID)
			}
			legacyProvisionedKey := ""
			if !req.ProvisionAllEligibleGroups {
				legacyProvisionedKey = primaryKey.Key
			}
			c.JSON(http.StatusCreated, NewApiProvisionResponse{
				Subscription:       toSubscriptionItem(fresh),
				ChannelUID:         channelUID,
				ChannelIndex:       channelIndex,
				ChannelName:        resolvedChannelName,
				MergedChannel:      mergeFound,
				ProvisionedKey:     legacyProvisionedKey,
				ProvisionedTokenID: primaryKey.TokenID,
				Reused:             allReused,
				ProvisionedKeys:    responseKeys,
				DiscoveryStarted:   discoveryStarted,
			})
		})
	}
}
