package config

import (
	"fmt"
	"hash/fnv"
	"log"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"

	"github.com/fsnotify/fsnotify"
)

// ============== 核心类型定义 ==============

// UpstreamConfig 上游配置
type UpstreamConfig struct {
	BaseURL            string            `json:"baseUrl"`
	BaseURLs           []string          `json:"baseUrls,omitempty"` // 多 BaseURL 支持（failover 模式）
	APIKeys            []string          `json:"apiKeys"`
	HistoricalAPIKeys  []string          `json:"historicalApiKeys,omitempty"` // 历史 API Key（用于统计聚合，换 Key 后保留旧 Key 的统计数据）
	DisabledAPIKeys    []DisabledKeyInfo `json:"disabledApiKeys,omitempty"`   // 被拉黑的 API Key（持久化，需手动恢复）
	ServiceType        string            `json:"serviceType"`                 // gemini, openai, claude
	Name               string            `json:"name,omitempty"`
	Description        string            `json:"description,omitempty"`
	Website            string            `json:"website,omitempty"`
	InsecureSkipVerify bool              `json:"insecureSkipVerify,omitempty"`
	ModelMapping       map[string]string `json:"modelMapping,omitempty"`
	ReasoningMapping   map[string]string `json:"reasoningMapping,omitempty"`
	TextVerbosity      string            `json:"textVerbosity,omitempty"`
	FastMode           bool              `json:"fastMode,omitempty"`
	// 多渠道调度相关字段
	Priority       int        `json:"priority"`                 // 渠道优先级（数字越小优先级越高，默认按索引）
	Status         string     `json:"status"`                   // 渠道状态：active（正常）, suspended（暂停）, disabled（备用池）
	PromotionUntil *time.Time `json:"promotionUntil,omitempty"` // 促销期截止时间，在此期间内优先使用此渠道（忽略trace亲和）
	LowQuality     bool       `json:"lowQuality,omitempty"`     // 低质量渠道标记：启用后强制本地估算 token，偏差>5%时使用本地值
	RPM            int        `json:"rpm"`                      // 能力测试发送速率（每分钟请求数，仅影响能力测试）
	// 自动拉黑开关
	AutoBlacklistBalance *bool `json:"autoBlacklistBalance,omitempty"` // 余额不足时自动拉黑 Key（默认 true）
	// metadata.user_id 规范化开关
	NormalizeMetadataUserID *bool `json:"normalizeMetadataUserId,omitempty"` // 规范化 metadata.user_id（默认 true）
	// Gemini 特定配置
	InjectDummyThoughtSignature bool `json:"injectDummyThoughtSignature,omitempty"` // 给空 thought_signature 注入 dummy 值（兼容 x666.me 等要求必须有该字段的 API）
	StripThoughtSignature       bool `json:"stripThoughtSignature,omitempty"`       // 移除 thought_signature 字段（兼容旧版 Gemini API）
	// 自定义请求头
	CustomHeaders map[string]string `json:"customHeaders,omitempty"` // 自定义请求头（覆盖或添加到上游请求）
	// 渠道级代理
	ProxyURL string `json:"proxyUrl,omitempty"` // HTTP/HTTPS/SOCKS5 代理地址
	// 模型白名单
	SupportedModels []string `json:"supportedModels,omitempty"` // 支持的模型白名单（空=全部），支持通配符如 gpt-4*
	// /v1/models 返回模式
	ModelsResponseMode string   `json:"modelsResponseMode,omitempty"` // upstream=请求上游；manual=返回手工模型列表
	ManualModels       []string `json:"manualModels,omitempty"`       // 手工维护的模型列表，仅在 modelsResponseMode=manual 时生效
	// 路由前缀
	RoutePrefix string `json:"routePrefix,omitempty"` // 路由前缀（如 "kimi"），客户端可通过 /:routePrefix/v1/messages 访问
	// Claude 流式转发模式：true=直接透传，false=走本地流事件处理链
	StreamPassthroughEnabled  *bool `json:"streamPassthroughEnabled,omitempty"`
	Sub2APIPassthroughEnabled *bool `json:"sub2apiPassthroughEnabled,omitempty"`
	KeyAffinityEnabled        *bool `json:"keyAffinityEnabled,omitempty"`
	// Claude 严格请求透传：true=请求体原样转发；false=继续执行本地兼容预处理
	StrictRequestPassthroughEnabled *bool `json:"strictRequestPassthroughEnabled,omitempty"`
	// Key 健康巡检（可选）：周期请求 /v1/models（Gemini 为 /v1beta/models），非 200 自动拉黑
	ModelsHealthCheckEnabled         *bool `json:"modelsHealthCheckEnabled,omitempty"`
	ModelsHealthCheckIntervalMinutes *int  `json:"modelsHealthCheckIntervalMinutes,omitempty"`
	// 渠道级故障规则：按状态码/错误码/关键词命中后执行冷却或拉黑
	FailoverRules []FailoverRule `json:"failoverRules,omitempty"`
}

// DisabledKeyInfo 被拉黑的 API Key 信息
type DisabledKeyInfo struct {
	Key        string `json:"key"`
	Reason     string `json:"reason"`     // "authentication_error" / "permission_error" / "insufficient_balance"
	Message    string `json:"message"`    // 原始错误信息
	DisabledAt string `json:"disabledAt"` // ISO8601 时间戳
	RecoverAt  string `json:"recoverAt,omitempty"`
}

func IsAutoRecoverableDisabledReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch reason {
	case "insufficient_balance", "insufficient_quota", "billing_error", "quota":
		return true
	default:
		return false
	}
}

// CooldownKeyInfo 处于冷却期的 API Key 信息（运行时）
type CooldownKeyInfo struct {
	Key                  string `json:"key"`
	FailureCount         int    `json:"failureCount"`
	CooldownUntil        string `json:"cooldownUntil"`
	RemainingSeconds     int64  `json:"remainingSeconds"`
	FixedDurationSeconds int64  `json:"fixedDurationSeconds,omitempty"`
}

// IsAutoBlacklistBalanceEnabled 检查余额不足自动拉黑是否启用（默认 true）
func (u *UpstreamConfig) IsAutoBlacklistBalanceEnabled() bool {
	if u.AutoBlacklistBalance == nil {
		return true
	}
	return *u.AutoBlacklistBalance
}

// IsNormalizeMetadataUserIDEnabled 检查 metadata.user_id 规范化是否启用（默认 true）
func (u *UpstreamConfig) IsNormalizeMetadataUserIDEnabled() bool {
	if u.NormalizeMetadataUserID == nil {
		return true
	}
	return *u.NormalizeMetadataUserID
}

// IsStreamPassthroughEnabled 检查流式是否直接透传（默认 true）
func (u *UpstreamConfig) IsStreamPassthroughEnabled() bool {
	if u.StreamPassthroughEnabled == nil {
		return true
	}
	return *u.StreamPassthroughEnabled
}

func (u *UpstreamConfig) IsSub2APIPassthroughEnabled() bool {
	if u.Sub2APIPassthroughEnabled == nil {
		return false
	}
	return *u.Sub2APIPassthroughEnabled
}

func (u *UpstreamConfig) IsKeyAffinityEnabled() bool {
	if u.KeyAffinityEnabled == nil {
		return false
	}
	return *u.KeyAffinityEnabled
}

func (u *UpstreamConfig) IsModelsHealthCheckEnabled() bool {
	if u.ModelsHealthCheckEnabled == nil {
		return false
	}
	return *u.ModelsHealthCheckEnabled
}

func (u *UpstreamConfig) GetModelsHealthCheckIntervalMinutes() int {
	if u.ModelsHealthCheckIntervalMinutes == nil || *u.ModelsHealthCheckIntervalMinutes <= 0 {
		return 60
	}
	return *u.ModelsHealthCheckIntervalMinutes
}

func (u *UpstreamConfig) NormalizeModelsHealthCheckOptions() {
	if u == nil {
		return
	}
	if u.ModelsHealthCheckIntervalMinutes != nil && *u.ModelsHealthCheckIntervalMinutes <= 0 {
		defaultInterval := 60
		u.ModelsHealthCheckIntervalMinutes = &defaultInterval
	}
}

func (u *UpstreamConfig) NormalizeClaudePassthroughMode() {
	if u == nil || !strings.EqualFold(u.ServiceType, "claude") {
		return
	}
	if u.Sub2APIPassthroughEnabled != nil && *u.Sub2APIPassthroughEnabled {
		disabled := false
		u.StreamPassthroughEnabled = &disabled
	}
}

func (u *UpstreamConfig) GetModelsResponseMode() string {
	if u == nil {
		return "upstream"
	}
	if strings.EqualFold(strings.TrimSpace(u.ModelsResponseMode), "manual") {
		return "manual"
	}
	return "upstream"
}

func (u *UpstreamConfig) UsesManualModels() bool {
	return u.GetModelsResponseMode() == "manual"
}

func (u *UpstreamConfig) NormalizeModelsResponseMode() {
	if u == nil {
		return
	}

	u.ModelsResponseMode = u.GetModelsResponseMode()

	normalized := make([]string, 0, len(u.ManualModels))
	seen := make(map[string]struct{}, len(u.ManualModels))
	for _, model := range u.ManualModels {
		modelID := strings.TrimSpace(model)
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		normalized = append(normalized, modelID)
	}
	u.ManualModels = normalized
}

// IsStrictRequestPassthroughEnabled 检查是否启用严格请求透传（默认 true）
func (u *UpstreamConfig) IsStrictRequestPassthroughEnabled() bool {
	if u.StrictRequestPassthroughEnabled == nil {
		return true
	}
	return *u.StrictRequestPassthroughEnabled
}

// GetEffectiveFailoverRules 获取渠道级故障规则（Claude 默认规则可覆盖）
func (u *UpstreamConfig) GetEffectiveFailoverRules() []FailoverRule {
	if len(u.FailoverRules) > 0 {
		if strings.EqualFold(u.ServiceType, "claude") && IsLegacyClaudeDefaultFailoverRules(u.FailoverRules) {
			return DefaultClaudeFailoverRules()
		}
		return CloneFailoverRules(u.FailoverRules)
	}

	if strings.EqualFold(u.ServiceType, "claude") {
		return DefaultClaudeFailoverRules()
	}

	return nil
}

func IsLegacyClaudeDefaultFailoverRules(rules []FailoverRule) bool {
	if len(rules) != 2 {
		return false
	}

	has429Cooldown := false
	has400401Blacklist := false

	for _, rule := range rules {
		action := strings.ToLower(strings.TrimSpace(rule.Action))
		hasErrorCodes := len(rule.ErrorCodes) > 0
		hasKeywords := len(rule.Keywords) > 0

		if action == "cooldown" && !hasErrorCodes && !hasKeywords &&
			len(rule.StatusCodes) == 1 && rule.StatusCodes[0] == 429 &&
			rule.DurationMinutes == 60 {
			has429Cooldown = true
			continue
		}

		if action == "blacklist" && !hasErrorCodes && !hasKeywords &&
			len(rule.StatusCodes) == 2 &&
			((rule.StatusCodes[0] == 400 && rule.StatusCodes[1] == 401) ||
				(rule.StatusCodes[0] == 401 && rule.StatusCodes[1] == 400)) {
			has400401Blacklist = true
			continue
		}

		return false
	}

	return has429Cooldown && has400401Blacklist
}

// FailoverRule 渠道级故障规则
type FailoverRule struct {
	Description     string   `json:"description,omitempty"`
	Action          string   `json:"action"` // "cooldown" | "blacklist"
	StatusCodes     []int    `json:"statusCodes,omitempty"`
	ErrorCodes      []string `json:"errorCodes,omitempty"`
	Keywords        []string `json:"keywords,omitempty"`
	DurationMinutes int      `json:"durationMinutes,omitempty"` // action=cooldown 时生效
}

// DefaultClaudeFailoverRules Claude 默认故障规则
func DefaultClaudeFailoverRules() []FailoverRule {
	return []FailoverRule{
		{
			Description: "401 无效 key 拉黑",
			Action:      "blacklist",
			StatusCodes: []int{401},
			ErrorCodes:  []string{"authentication_error", "1000"},
			Keywords:    []string{"invalid x-api-key", "身份验证失败"},
		},
		{
			Description:     "400 用量上限冷却",
			Action:          "cooldown",
			StatusCodes:     []int{400},
			ErrorCodes:      []string{"invalid_request_error"},
			Keywords:        []string{"reached your specified api usage limits", "regain access on"},
			DurationMinutes: 720,
		},
		{
			Description: "400 余额不足拉黑",
			Action:      "blacklist",
			StatusCodes: []int{400},
			ErrorCodes:  []string{"invalid_request_error"},
			Keywords:    []string{"credit balance is too low", "plans & billing", "purchase credits"},
		},
		{
			Description: "402 会员权益校验失败拉黑",
			Action:      "blacklist",
			StatusCodes: []int{402},
			ErrorCodes:  []string{"invalid_request_error"},
			Keywords:    []string{"unable to verify your membership benefits"},
		},
		{
			Description: "400 api_error 请求失败拉黑",
			Action:      "blacklist",
			StatusCodes: []int{400},
			ErrorCodes:  []string{"api_error"},
			Keywords:    []string{"request failed. please check your input and try again"},
		},
		{
			Description:     "429 冷却 60 分钟",
			Action:          "cooldown",
			StatusCodes:     []int{429},
			DurationMinutes: 60,
		},
	}
}

// CloneFailoverRules 深拷贝规则切片
func CloneFailoverRules(rules []FailoverRule) []FailoverRule {
	if len(rules) == 0 {
		return nil
	}
	cloned := make([]FailoverRule, len(rules))
	for i := range rules {
		cloned[i] = rules[i]
		if len(rules[i].StatusCodes) > 0 {
			cloned[i].StatusCodes = append([]int(nil), rules[i].StatusCodes...)
		}
		if len(rules[i].ErrorCodes) > 0 {
			cloned[i].ErrorCodes = append([]string(nil), rules[i].ErrorCodes...)
		}
		if len(rules[i].Keywords) > 0 {
			cloned[i].Keywords = append([]string(nil), rules[i].Keywords...)
		}
	}
	return cloned
}

// UpstreamUpdate 用于部分更新 UpstreamConfig
type UpstreamUpdate struct {
	Name               *string           `json:"name"`
	ServiceType        *string           `json:"serviceType"`
	BaseURL            *string           `json:"baseUrl"`
	BaseURLs           []string          `json:"baseUrls"`
	APIKeys            []string          `json:"apiKeys"`
	Description        *string           `json:"description"`
	Website            *string           `json:"website"`
	InsecureSkipVerify *bool             `json:"insecureSkipVerify"`
	ModelMapping       map[string]string `json:"modelMapping"`
	ReasoningMapping   map[string]string `json:"reasoningMapping"`
	TextVerbosity      *string           `json:"textVerbosity"`
	FastMode           *bool             `json:"fastMode"`
	// 多渠道调度相关字段
	Priority                         *int           `json:"priority"`
	Status                           *string        `json:"status"`
	PromotionUntil                   *time.Time     `json:"promotionUntil"`
	LowQuality                       *bool          `json:"lowQuality"`
	RPM                              *int           `json:"rpm"`
	AutoBlacklistBalance             *bool          `json:"autoBlacklistBalance"`
	NormalizeMetadataUserID          *bool          `json:"normalizeMetadataUserId"`
	StreamPassthroughEnabled         *bool          `json:"streamPassthroughEnabled"`
	Sub2APIPassthroughEnabled        *bool          `json:"sub2apiPassthroughEnabled"`
	KeyAffinityEnabled               *bool          `json:"keyAffinityEnabled"`
	StrictRequestPassthroughEnabled  *bool          `json:"strictRequestPassthroughEnabled"`
	ModelsHealthCheckEnabled         *bool          `json:"modelsHealthCheckEnabled"`
	ModelsHealthCheckIntervalMinutes *int           `json:"modelsHealthCheckIntervalMinutes"`
	FailoverRules                    []FailoverRule `json:"failoverRules"`
	// Gemini 特定配置
	InjectDummyThoughtSignature *bool `json:"injectDummyThoughtSignature"`
	StripThoughtSignature       *bool `json:"stripThoughtSignature"`
	// 自定义请求头
	CustomHeaders map[string]string `json:"customHeaders"`
	// 渠道级代理
	ProxyURL *string `json:"proxyUrl"`
	// 模型白名单
	SupportedModels []string `json:"supportedModels"` // 支持的模型白名单（空=全部），支持通配符如 gpt-4*
	// /v1/models 返回模式
	ModelsResponseMode *string  `json:"modelsResponseMode"`
	ManualModels       []string `json:"manualModels"`
	// 路由前缀
	RoutePrefix *string `json:"routePrefix"` // 路由前缀（如 "kimi"）
}

// Config 配置结构
type Config struct {
	Upstream        []UpstreamConfig `json:"upstream"`
	CurrentUpstream int              `json:"currentUpstream,omitempty"` // 已废弃：旧格式兼容用

	// Responses 接口专用配置（独立于 /v1/messages）
	ResponsesUpstream        []UpstreamConfig `json:"responsesUpstream"`
	CurrentResponsesUpstream int              `json:"currentResponsesUpstream,omitempty"` // 已废弃：旧格式兼容用

	// Gemini 接口专用配置（独立于 /v1/messages 和 /v1/responses）
	GeminiUpstream []UpstreamConfig `json:"geminiUpstream"`

	// Chat Completions 接口专用配置（OpenAI /v1/chat/completions 兼容）
	ChatUpstream []UpstreamConfig `json:"chatUpstream,omitempty"`

	// Images 接口专用配置；完整 CRUD 由 images/config lane 接入。
	ImagesUpstream []UpstreamConfig `json:"imagesUpstream,omitempty"`

	// Fuzzy 模式：启用时模糊处理错误，所有非 2xx 错误都尝试 failover
	FuzzyModeEnabled bool `json:"fuzzyModeEnabled"`

	// 移除计费头中的 cch= 参数：启用时自动从 system 数组中移除 cch=xxx; 部分
	StripBillingHeader bool `json:"stripBillingHeader"`

	// 全局暂停规则：HTTP 状态码 + 关键词匹配 → 自定义暂停时间
	PauseRules []PauseRule `json:"pauseRules,omitempty"`
}

// PauseRule 全局暂停规则：HTTP 状态码 + 关键词匹配 → 自定义暂停时间
type PauseRule struct {
	Description     string   `json:"description"`
	ErrorCode       int      `json:"error_code"`
	Keywords        []string `json:"keywords"`
	DurationMinutes int      `json:"duration_minutes"`
}

// FailedKey 失败密钥记录
type FailedKey struct {
	Timestamp     time.Time
	FailureCount  int
	FixedDuration time.Duration // 非零时使用固定冷却时间，忽略指数退避
}

// ConfigManager 配置管理器
type ConfigManager struct {
	mu                  sync.RWMutex
	config              Config
	configFile          string
	watcher             *fsnotify.Watcher
	failedKeysCache     map[string]*FailedKey
	keyBackoffDurations []time.Duration    // 各档冷却时间
	roundRobinCounters  map[string]*uint64 // upstream.Name → 轮询计数器
	stopChan            chan struct{}      // 用于通知 goroutine 停止
	closeOnce           sync.Once          // 确保 Close 只执行一次
}

// failedKeyCacheKey 构造 FailedKeysCache 的复合键（apiType:apiKey）
func failedKeyCacheKey(apiType, apiKey string) string {
	return apiType + ":" + apiKey
}

// backoffDuration 根据失败次数返回对应冷却时间（指数退避，超出档位取最后一档）
func (cm *ConfigManager) backoffDuration(failureCount int) time.Duration {
	idx := failureCount - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cm.keyBackoffDurations) {
		idx = len(cm.keyBackoffDurations) - 1
	}
	return cm.keyBackoffDurations[idx]
}

// getOrCreateCounter 获取或创建 upstream 的轮询计数器
func (cm *ConfigManager) getOrCreateCounter(upstreamName string) *uint64 {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if c, ok := cm.roundRobinCounters[upstreamName]; ok {
		return c
	}
	var c uint64
	cm.roundRobinCounters[upstreamName] = &c
	return &c
}

// ============== 核心共享方法 ==============

// GetConfig 获取配置（返回深拷贝，确保并发安全）
func (cm *ConfigManager) GetConfig() Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// 深拷贝整个 Config 结构体
	cloned := cm.config

	// 深拷贝 Upstream slice
	if cm.config.Upstream != nil {
		cloned.Upstream = make([]UpstreamConfig, len(cm.config.Upstream))
		for i := range cm.config.Upstream {
			cloned.Upstream[i] = *cm.config.Upstream[i].Clone()
		}
	}

	// 深拷贝 ResponsesUpstream slice
	if cm.config.ResponsesUpstream != nil {
		cloned.ResponsesUpstream = make([]UpstreamConfig, len(cm.config.ResponsesUpstream))
		for i := range cm.config.ResponsesUpstream {
			cloned.ResponsesUpstream[i] = *cm.config.ResponsesUpstream[i].Clone()
		}
	}

	// 深拷贝 GeminiUpstream slice
	if cm.config.GeminiUpstream != nil {
		cloned.GeminiUpstream = make([]UpstreamConfig, len(cm.config.GeminiUpstream))
		for i := range cm.config.GeminiUpstream {
			cloned.GeminiUpstream[i] = *cm.config.GeminiUpstream[i].Clone()
		}
	}

	// 深拷贝 ChatUpstream slice
	if len(cm.config.ChatUpstream) > 0 {
		cloned.ChatUpstream = make([]UpstreamConfig, len(cm.config.ChatUpstream))
		for i := range cm.config.ChatUpstream {
			cloned.ChatUpstream[i] = *cm.config.ChatUpstream[i].Clone()
		}
	}

	if len(cm.config.ImagesUpstream) > 0 {
		cloned.ImagesUpstream = make([]UpstreamConfig, len(cm.config.ImagesUpstream))
		for i := range cm.config.ImagesUpstream {
			cloned.ImagesUpstream[i] = *cm.config.ImagesUpstream[i].Clone()
		}
	}

	return cloned
}

// GetNextAPIKey 获取下一个 API 密钥（纯 failover 模式）
// apiType: 接口类型（Messages/Responses/Gemini），用于日志标签前缀
func (cm *ConfigManager) GetNextAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool, apiType string) (string, error) {
	if len(upstream.APIKeys) == 0 {
		return "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
	}

	// 单 Key 直接返回
	if len(upstream.APIKeys) == 1 {
		return upstream.APIKeys[0], nil
	}

	// 筛选可用密钥：排除临时失败密钥和内存中的失败密钥
	availableKeys := cm.getAvailableKeys(upstream, failedKeys, apiType)

	if len(availableKeys) == 0 {
		oldestFailedKey := cm.selectOldestFailedKey(upstream, failedKeys, apiType)
		if oldestFailedKey != "" {
			log.Printf("[%s-Key] 警告: 所有密钥都失效，尝试最早失败的密钥: %s", apiType, utils.MaskAPIKey(oldestFailedKey))
			return oldestFailedKey, nil
		}

		return "", fmt.Errorf("上游 %s 的所有API密钥都暂时不可用", upstream.Name)
	}

	// 轮询：按计数器均匀分配，每次取下一个可用 key
	counter := cm.getOrCreateCounter(upstream.Name)
	idx := int(atomic.AddUint64(counter, 1)-1) % len(availableKeys)
	selectedKey := availableKeys[idx]
	// 获取该密钥在原始列表中的索引
	keyIndex := 0
	for i, key := range upstream.APIKeys {
		if key == selectedKey {
			keyIndex = i + 1
			break
		}
	}
	log.Printf("[%s-Key] 轮询选择密钥 %s (%d/%d)", apiType, utils.MaskAPIKey(selectedKey), keyIndex, len(upstream.APIKeys))
	return selectedKey, nil
}

func (cm *ConfigManager) GetNextAPIKeyForUser(upstream *UpstreamConfig, failedKeys map[string]bool, apiType string, userID string) (string, error) {
	if upstream == nil {
		return "", fmt.Errorf("upstream 为空")
	}
	normalizedUserID := strings.TrimSpace(userID)
	if !upstream.IsKeyAffinityEnabled() {
		return cm.GetNextAPIKey(upstream, failedKeys, apiType)
	}

	if len(upstream.APIKeys) == 0 {
		return "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
	}
	if len(upstream.APIKeys) == 1 {
		return upstream.APIKeys[0], nil
	}

	availableKeys := cm.getAvailableKeys(upstream, failedKeys, apiType)
	if len(availableKeys) == 0 {
		oldestFailedKey := cm.selectOldestFailedKey(upstream, failedKeys, apiType)
		if oldestFailedKey != "" {
			log.Printf("[%s-Key] 警告: 亲和选择无可用密钥，尝试最早失败密钥: %s", apiType, utils.MaskAPIKey(oldestFailedKey))
			return oldestFailedKey, nil
		}
		return "", fmt.Errorf("上游 %s 的所有API密钥都暂时不可用", upstream.Name)
	}

	if normalizedUserID == "" {
		selectedKey := availableKeys[0]
		keyIndex := 0
		for i, key := range upstream.APIKeys {
			if key == selectedKey {
				keyIndex = i + 1
				break
			}
		}
		log.Printf("[%s-Key] 亲和选择密钥 %s (%d/%d, user=empty)", apiType, utils.MaskAPIKey(selectedKey), keyIndex, len(upstream.APIKeys))
		return selectedKey, nil
	}

	hash := fnv.New32a()
	_, _ = hash.Write([]byte(normalizedUserID))
	idx := int(hash.Sum32() % uint32(len(availableKeys)))
	selectedKey := availableKeys[idx]

	keyIndex := 0
	for i, key := range upstream.APIKeys {
		if key == selectedKey {
			keyIndex = i + 1
			break
		}
	}
	log.Printf("[%s-Key] 亲和选择密钥 %s (%d/%d, user=%s)", apiType, utils.MaskAPIKey(selectedKey), keyIndex, len(upstream.APIKeys), maskUserIDForLog(normalizedUserID))
	return selectedKey, nil
}

func (cm *ConfigManager) getAvailableKeys(upstream *UpstreamConfig, failedKeys map[string]bool, apiType string) []string {
	availableKeys := make([]string, 0, len(upstream.APIKeys))
	for _, key := range upstream.APIKeys {
		if failedKeys[key] {
			continue
		}
		if cm.isKeyFailed(key, apiType) {
			continue
		}
		availableKeys = append(availableKeys, key)
	}
	return availableKeys
}

func (cm *ConfigManager) selectOldestFailedKey(upstream *UpstreamConfig, failedKeys map[string]bool, apiType string) string {
	var oldestFailedKey string
	oldestTime := time.Now()

	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, key := range upstream.APIKeys {
		if failedKeys[key] { // 排除本次请求已经尝试过的密钥
			continue
		}
		cacheKey := failedKeyCacheKey(apiType, key)
		if failure, exists := cm.failedKeysCache[cacheKey]; exists {
			if failure.Timestamp.Before(oldestTime) {
				oldestTime = failure.Timestamp
				oldestFailedKey = key
			}
		}
	}
	return oldestFailedKey
}

func maskUserIDForLog(userID string) string {
	if userID == "" {
		return ""
	}
	if len(userID) <= 8 {
		return userID
	}
	return userID[:4] + "***" + userID[len(userID)-2:]
}

// GetAdminAPIKey 获取管理/探测场景下的 API 密钥。
// 优先使用活跃 APIKeys；若活跃密钥不可用，则临时借用 DisabledAPIKeys 中的密钥。
// 返回值 fallback=true 表示本次借用了已拉黑密钥。
func (cm *ConfigManager) GetAdminAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool, apiType string) (apiKey string, fallback bool, err error) {
	apiKey, err = cm.GetNextAPIKey(upstream, failedKeys, apiType)
	if err == nil {
		return apiKey, false, nil
	}

	for _, disabledKey := range upstream.DisabledAPIKeys {
		if failedKeys[disabledKey.Key] {
			continue
		}
		log.Printf("[%s-Key] 警告: 活跃密钥不可用，临时借用已拉黑密钥用于管理操作: %s", apiType, utils.MaskAPIKey(disabledKey.Key))
		return disabledKey.Key, true, nil
	}

	return "", false, err
}

// MarkKeyAsFailed 标记密钥失败
// apiType: 接口类型（Messages/Responses/Gemini/Chat），用于日志标签前缀和缓存键隔离
func (cm *ConfigManager) MarkKeyAsFailed(apiKey string, apiType string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cacheKey := failedKeyCacheKey(apiType, apiKey)
	if failure, exists := cm.failedKeysCache[cacheKey]; exists {
		failure.FailureCount++
		failure.Timestamp = time.Now()
		// 普通失败回退为指数退避语义，清除暂停规则写入的固定冷却时长。
		failure.FixedDuration = 0
	} else {
		cm.failedKeysCache[cacheKey] = &FailedKey{
			Timestamp:    time.Now(),
			FailureCount: 1,
		}
	}

	failure := cm.failedKeysCache[cacheKey]
	recoveryTime := cm.backoffDuration(failure.FailureCount)

	log.Printf("[%s-Key] 标记API密钥失败: %s (失败次数: %d, 恢复时间: %v)",
		apiType, utils.MaskAPIKey(apiKey), failure.FailureCount, recoveryTime)
}

// isKeyFailed 检查密钥是否失败
func (cm *ConfigManager) isKeyFailed(apiKey, apiType string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	cacheKey := failedKeyCacheKey(apiType, apiKey)
	failure, exists := cm.failedKeysCache[cacheKey]
	if !exists {
		return false
	}

	// 优先使用固定冷却时间（暂停规则触发），否则使用指数退避
	var recoveryTime time.Duration
	if failure.FixedDuration > 0 {
		recoveryTime = failure.FixedDuration
	} else {
		recoveryTime = cm.backoffDuration(failure.FailureCount)
	}

	return time.Since(failure.Timestamp) < recoveryTime
}

// IsKeyFailed 检查 Key 是否在冷却期（公开方法）
func (cm *ConfigManager) IsKeyFailed(apiKey, apiType string) bool {
	return cm.isKeyFailed(apiKey, apiType)
}

// GetCooldownKeys 获取指定渠道当前处于冷却期的 Key 列表
// 仅返回当前 active APIKeys 中仍在冷却窗口内的 key
func (cm *ConfigManager) GetCooldownKeys(apiType string, channelIndex int) []CooldownKeyInfo {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return nil
	}

	upstream := &(*upstreams)[channelIndex]
	if len(upstream.APIKeys) == 0 || len(cm.failedKeysCache) == 0 {
		return nil
	}

	now := time.Now()
	result := make([]CooldownKeyInfo, 0, len(upstream.APIKeys))
	for _, apiKey := range upstream.APIKeys {
		cacheKey := failedKeyCacheKey(apiType, apiKey)
		failure, exists := cm.failedKeysCache[cacheKey]
		if !exists || failure == nil {
			continue
		}

		cooldownDuration := failure.FixedDuration
		if cooldownDuration <= 0 {
			cooldownDuration = cm.backoffDuration(failure.FailureCount)
		}
		if cooldownDuration <= 0 {
			continue
		}

		cooldownUntil := failure.Timestamp.Add(cooldownDuration)
		remaining := cooldownUntil.Sub(now)
		if remaining <= 0 {
			continue
		}

		remainingSeconds := int64(remaining / time.Second)
		if remaining%time.Second != 0 {
			remainingSeconds++
		}
		if remainingSeconds <= 0 {
			continue
		}

		item := CooldownKeyInfo{
			Key:              apiKey,
			FailureCount:     failure.FailureCount,
			CooldownUntil:    cooldownUntil.Format(time.RFC3339),
			RemainingSeconds: remainingSeconds,
		}
		if failure.FixedDuration > 0 {
			item.FixedDurationSeconds = int64(failure.FixedDuration / time.Second)
		}
		result = append(result, item)
	}

	slices.SortFunc(result, func(a, b CooldownKeyInfo) int {
		if a.RemainingSeconds == b.RemainingSeconds {
			return strings.Compare(a.Key, b.Key)
		}
		if a.RemainingSeconds > b.RemainingSeconds {
			return -1
		}
		return 1
	})

	return result
}

// MarkKeyAsFailedWithDuration 标记密钥失败，使用固定冷却时间（暂停规则触发）
func (cm *ConfigManager) MarkKeyAsFailedWithDuration(apiKey, apiType string, duration time.Duration) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cacheKey := failedKeyCacheKey(apiType, apiKey)
	cm.failedKeysCache[cacheKey] = &FailedKey{
		Timestamp:     time.Now(),
		FailureCount:  1,
		FixedDuration: duration,
	}

	log.Printf("[%s-PauseRule] 暂停 API 密钥: %s (时长: %v)",
		apiType, utils.MaskAPIKey(apiKey), duration)
}

// GetPauseRules 获取全局暂停规则
func (cm *ConfigManager) GetPauseRules() []PauseRule {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.PauseRules
}

// MatchPauseRule 根据 HTTP 状态码和响应体匹配暂停规则
// 返回匹配的规则，未匹配返回 nil
func (cm *ConfigManager) MatchPauseRule(statusCode int, body []byte) *PauseRule {
	rules := cm.GetPauseRules()
	if len(rules) == 0 {
		return nil
	}
	bodyLower := strings.ToLower(string(body))
	for i := range rules {
		rule := &rules[i]
		if rule.ErrorCode != statusCode {
			continue
		}
		// 无关键词要求时，仅状态码匹配即可
		if len(rule.Keywords) == 0 {
			return rule
		}
		// 任一关键词命中即匹配
		for _, kw := range rule.Keywords {
			if strings.Contains(bodyLower, strings.ToLower(kw)) {
				return rule
			}
		}
	}
	return nil
}

// clearFailedKeysForUpstream 清理指定渠道的所有失败 key 记录
// 当渠道被删除时调用，避免内存泄漏和冷却状态残留
// apiType: 接口类型（Messages/Responses/Gemini/Chat），用于日志标签前缀和缓存键隔离
func (cm *ConfigManager) clearFailedKeysForUpstream(upstream *UpstreamConfig, apiType string) {
	for _, key := range upstream.APIKeys {
		cacheKey := failedKeyCacheKey(apiType, key)
		if _, exists := cm.failedKeysCache[cacheKey]; exists {
			delete(cm.failedKeysCache, cacheKey)
			log.Printf("[%s-Key] 已清理被删除渠道 %s 的失败密钥记录: %s", apiType, upstream.Name, utils.MaskAPIKey(key))
		}
	}
}

// cleanupExpiredFailures 清理过期的失败记录
func (cm *ConfigManager) cleanupExpiredFailures() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-cm.stopChan:
			return
		case <-ticker.C:
			cm.mu.Lock()
			now := time.Now()
			for key, failure := range cm.failedKeysCache {
				recoveryTime := failure.FixedDuration
				if recoveryTime <= 0 {
					recoveryTime = cm.backoffDuration(failure.FailureCount)
				}

				if now.Sub(failure.Timestamp) > recoveryTime {
					delete(cm.failedKeysCache, key)
					log.Printf("[Config-Key] API密钥 %s 已从失败列表中恢复", utils.MaskAPIKey(key))
				}
			}
			cm.mu.Unlock()
		}
	}
}

// ============== Fuzzy 模式相关方法 ==============

// GetFuzzyModeEnabled 获取 Fuzzy 模式状态
func (cm *ConfigManager) GetFuzzyModeEnabled() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.FuzzyModeEnabled
}

// SetFuzzyModeEnabled 设置 Fuzzy 模式状态
func (cm *ConfigManager) SetFuzzyModeEnabled(enabled bool) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.config.FuzzyModeEnabled = enabled

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	status := "关闭"
	if enabled {
		status = "启用"
	}
	log.Printf("[Config-FuzzyMode] Fuzzy 模式已%s", status)
	return nil
}

// ============== StripBillingHeader 相关方法 ==============

// GetStripBillingHeader 获取移除计费头状态
func (cm *ConfigManager) GetStripBillingHeader() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.StripBillingHeader
}

// SetStripBillingHeader 设置移除计费头状态
func (cm *ConfigManager) SetStripBillingHeader(enabled bool) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.config.StripBillingHeader = enabled

	if err := cm.saveConfigLocked(cm.config); err != nil {
		return err
	}

	status := "关闭"
	if enabled {
		status = "启用"
	}
	log.Printf("[Config-StripBillingHeader] 移除计费头已%s", status)
	return nil
}

// ============== API Key 拉黑相关方法 ==============

// BlacklistKey 将指定 Key 从活跃列表移到拉黑列表（持久化）
// apiType: Messages/Responses/Gemini/Chat，用于定位 upstream slice
// channelIndex: 渠道在 upstream slice 中的索引
func (cm *ConfigManager) BlacklistKey(apiType string, channelIndex int, apiKey string, reason string, message string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]

	// 检查 key 是否在活跃列表中
	keyIdx := -1
	for i, k := range upstream.APIKeys {
		if k == apiKey {
			keyIdx = i
			break
		}
	}
	if keyIdx == -1 {
		return nil // key 不在活跃列表，可能已被拉黑，忽略
	}

	// 从 APIKeys 中移除
	upstream.APIKeys = append(upstream.APIKeys[:keyIdx], upstream.APIKeys[keyIdx+1:]...)

	// 添加到 DisabledAPIKeys
	disabledAt := time.Now()
	recoverAt := ""
	if IsAutoRecoverableDisabledReason(reason) {
		recoverAt = disabledAt.Add(time.Hour).Format(time.RFC3339)
	}
	upstream.DisabledAPIKeys = append(upstream.DisabledAPIKeys, DisabledKeyInfo{
		Key:        apiKey,
		Reason:     reason,
		Message:    message,
		DisabledAt: disabledAt.Format(time.RFC3339),
		RecoverAt:  recoverAt,
	})

	// 同时添加到 HistoricalAPIKeys（保留统计数据）
	if !slices.Contains(upstream.HistoricalAPIKeys, apiKey) {
		upstream.HistoricalAPIKeys = append(upstream.HistoricalAPIKeys, apiKey)
	}

	log.Printf("[%s-Blacklist] Key %s 已被拉黑 (原因: %s, 渠道: %s, 剩余Key: %d)",
		apiType, utils.MaskAPIKey(apiKey), reason, upstream.Name, len(upstream.APIKeys))

	if len(upstream.APIKeys) == 0 {
		log.Printf("[%s-Blacklist] 警告: 渠道 %s 的所有 Key 都已被拉黑！", apiType, upstream.Name)
	}

	return cm.saveConfigLocked(cm.config)
}

// RestoreKey 将指定 Key 从拉黑列表恢复到活跃列表（持久化）
func (cm *ConfigManager) RestoreKey(apiType string, channelIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]

	// 查找并移除
	disabledIdx := -1
	for i, dk := range upstream.DisabledAPIKeys {
		if dk.Key == apiKey {
			disabledIdx = i
			break
		}
	}
	if disabledIdx == -1 {
		return fmt.Errorf("Key %s 不在拉黑列表中", utils.MaskAPIKey(apiKey))
	}

	upstream.DisabledAPIKeys = append(upstream.DisabledAPIKeys[:disabledIdx], upstream.DisabledAPIKeys[disabledIdx+1:]...)
	if !slices.Contains(upstream.APIKeys, apiKey) {
		upstream.APIKeys = append(upstream.APIKeys, apiKey)
	}

	// 从 HistoricalAPIKeys 移除，避免 active∩historical 重复导致统计重复计数
	upstream.HistoricalAPIKeys = slices.DeleteFunc(upstream.HistoricalAPIKeys, func(k string) bool {
		return k == apiKey
	})

	// 清除内存中的失败记录
	cacheKey := failedKeyCacheKey(apiType, apiKey)
	delete(cm.failedKeysCache, cacheKey)

	log.Printf("[%s-Blacklist] Key %s 已恢复 (渠道: %s)", apiType, utils.MaskAPIKey(apiKey), upstream.Name)

	return cm.saveConfigLocked(cm.config)
}

// RestoreAllKeys 恢复指定渠道所有被拉黑的 Key（持久化）
// 返回恢复的 Key 数量
func (cm *ConfigManager) RestoreAllKeys(apiType string, channelIndex int) (int, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return 0, fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]
	restoredCount := len(upstream.DisabledAPIKeys)
	if restoredCount == 0 {
		return 0, nil
	}

	// 将所有被拉黑的 Key 移回活跃列表
	for _, dk := range upstream.DisabledAPIKeys {
		if !slices.Contains(upstream.APIKeys, dk.Key) {
			upstream.APIKeys = append(upstream.APIKeys, dk.Key)
		}
		// 从 HistoricalAPIKeys 移除，避免 active∩historical 重复
		upstream.HistoricalAPIKeys = slices.DeleteFunc(upstream.HistoricalAPIKeys, func(k string) bool {
			return k == dk.Key
		})
		// 清除内存中的失败记录
		cacheKey := failedKeyCacheKey(apiType, dk.Key)
		delete(cm.failedKeysCache, cacheKey)
	}

	log.Printf("[%s-Blacklist] 渠道 [%d] %s 的 %d 个 Key 已全部恢复", apiType, channelIndex, upstream.Name, restoredCount)
	upstream.DisabledAPIKeys = nil

	return restoredCount, cm.saveConfigLocked(cm.config)
}

func (cm *ConfigManager) RestoreDisabledKeys(apiType string, channelIndex int, keys []string) ([]string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return nil, fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}
	if len(keys) == 0 {
		return nil, nil
	}

	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key != "" {
			keySet[key] = struct{}{}
		}
	}
	if len(keySet) == 0 {
		return nil, nil
	}

	upstream := &(*upstreams)[channelIndex]
	restored := make([]string, 0, len(keySet))
	newDisabled := make([]DisabledKeyInfo, 0, len(upstream.DisabledAPIKeys))
	for _, dk := range upstream.DisabledAPIKeys {
		if _, ok := keySet[dk.Key]; !ok {
			newDisabled = append(newDisabled, dk)
			continue
		}
		if !slices.Contains(upstream.APIKeys, dk.Key) {
			upstream.APIKeys = append(upstream.APIKeys, dk.Key)
		}
		upstream.HistoricalAPIKeys = slices.DeleteFunc(upstream.HistoricalAPIKeys, func(k string) bool {
			return k == dk.Key
		})
		delete(cm.failedKeysCache, failedKeyCacheKey(apiType, dk.Key))
		restored = append(restored, dk.Key)
	}
	if len(restored) == 0 {
		return nil, nil
	}

	upstream.DisabledAPIKeys = newDisabled
	log.Printf("[%s-Blacklist] 渠道 [%d] %s 自动恢复了 %d 个 Key", apiType, channelIndex, upstream.Name, len(restored))
	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}
	return restored, nil
}

// getUpstreamSliceLocked 根据 apiType 获取对应的 upstream slice 指针（调用方需持有锁）
func (cm *ConfigManager) getUpstreamSliceLocked(apiType string) *[]UpstreamConfig {
	switch apiType {
	case "Messages":
		return &cm.config.Upstream
	case "Responses":
		return &cm.config.ResponsesUpstream
	case "Gemini":
		return &cm.config.GeminiUpstream
	case "Chat":
		return &cm.config.ChatUpstream
	case "Images":
		return &cm.config.ImagesUpstream
	default:
		return nil
	}
}
