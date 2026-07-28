package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ChannelCompatStatePath 渠道兼容性能力记忆的默认落盘位置。
// 与 deprecated_params.json 同级，属于内部运行时状态，不进 config.json，用户无需感知或配置。
const ChannelCompatStatePath = ".config/channel_compat.json"

// channelCompatTTL 兼容性记忆的有效期。
// 与 DeprecatedParamCache / SystemHeaderFilterCache 保持一致：上游能力变化后自动重新学习。
const channelCompatTTL = 24 * time.Hour

// CompatTrait 一个可自动学习的渠道兼容性事实。
// 取字符串而非 iota，便于落盘 JSON 与日志直接可读。
type CompatTrait string

const (
	// TraitDowngradeDeveloperRole 上游不识别 OpenAI developer role，需降级为 system
	TraitDowngradeDeveloperRole CompatTrait = "downgrade_developer_role"
	// TraitStripImageGenTool 上游未开通图片生成，需剥离 image_generation / Codex image_gen 工具
	TraitStripImageGenTool CompatTrait = "strip_image_generation_tool"
	// TraitStripCodexClientTools 上游不接受 Codex 客户端专属工具，需剥离
	TraitStripCodexClientTools CompatTrait = "strip_codex_client_tools"
	// TraitPassbackThinkingBlocks 上游严格要求历史 content[].thinking 回传
	TraitPassbackThinkingBlocks CompatTrait = "passback_thinking_blocks"
	// TraitPassbackReasoningContent 上游要求 OpenAI 风格 reasoning_content 回传
	TraitPassbackReasoningContent CompatTrait = "passback_reasoning_content"
	// TraitStripEmptyTextBlocks 上游严格校验空 text content block，需转发前剥离
	TraitStripEmptyTextBlocks CompatTrait = "strip_empty_text_blocks"
	// TraitNormalizeNonstandardChatRoles 上游只接受标准 Chat role，需把非标准 role 降为 user
	TraitNormalizeNonstandardChatRoles CompatTrait = "normalize_nonstandard_chat_roles"
	// TraitCodexNativeToolPassthrough 上游需要 Codex 原生工具转为 OpenAI function 格式
	TraitCodexNativeToolPassthrough CompatTrait = "codex_native_tool_passthrough"
)

// AllCompatTraits 全部可学习兼容项，供配置迁移与诊断遍历。
func AllCompatTraits() []CompatTrait {
	return []CompatTrait{
		TraitDowngradeDeveloperRole,
		TraitStripImageGenTool,
		TraitStripCodexClientTools,
		TraitPassbackThinkingBlocks,
		TraitPassbackReasoningContent,
		TraitStripEmptyTextBlocks,
		TraitNormalizeNonstandardChatRoles,
		TraitCodexNativeToolPassthrough,
	}
}

// 学习来源。error_signal 来自上游明确报错（强证据）；probe 来自主动探测（弱证据）。
const (
	CompatSourceErrorSignal = "error_signal"
	CompatSourceProbe       = "probe"
)

// compatEvidenceMaxLen 证据摘要落盘上限，避免上游长错误体把状态文件撑大。
const compatEvidenceMaxLen = 200

// truncateCompatEvidence 截断证据摘要，按 rune 边界切分避免产生非法 UTF-8。
func truncateCompatEvidence(evidence string) string {
	runes := []rune(evidence)
	if len(runes) <= compatEvidenceMaxLen {
		return evidence
	}
	return string(runes[:compatEvidenceMaxLen]) + "..."
}

// CompatTraitState 单个兼容性事实的学习结论。
type CompatTraitState struct {
	Enabled    bool      `json:"enabled"`     // 学到的结论：该兼容改写是否应生效
	Source     string    `json:"source"`      // CompatSourceErrorSignal / CompatSourceProbe
	Evidence   string    `json:"evidence"`    // 触发时的错误/探测摘要（截断）
	LearnedAt  time.Time `json:"learned_at"`  // 首次学到的时间
	ApplyCount int       `json:"apply_count"` // 命中记忆并主动改写的次数
}

// 上下文上限的两种证据来源，强弱不同，合成规则也不同（见 RecordContextLimit）。
const (
	// CompatSourceUpstreamDeclared 上游报错里明确写出了它接受的最大 token 数（强证据，直接采信）
	CompatSourceUpstreamDeclared = "upstream_declared"
	// CompatSourceRejectedEstimate 上游只说"太长"没给数值，只能由本次请求估算值反推上界（弱证据，取更小值）
	CompatSourceRejectedEstimate = "rejected_estimate"
)

// ContextLimitState 该 渠道-Key-模型 组合实测的上下文输入上限。
//
// 存在意义：模型注册表登记的是"该模型公开的窗口"，但个别渠道对某个模型的实际窗口更短
// （中转商自行截断、上游按套餐限制等）。这类事实只能由真实请求被拒绝后学到，
// 且不能外溢到同渠道的其他 Key 或其他模型——同一渠道不同套餐的 Key 上限可能不同。
type ContextLimitState struct {
	// MaxInputTokens 该组合可接受的输入 token 上限（保守值，宁小勿大）
	MaxInputTokens int `json:"max_input_tokens"`
	// Source CompatSourceUpstreamDeclared / CompatSourceRejectedEstimate
	Source string `json:"source"`
	// Evidence 触发学习时的上游错误摘要（截断）
	Evidence string `json:"evidence"`
	// LearnedAt 该结论的学习时间。独立于 ChannelCompatEntry.DetectedAt 计算 TTL：
	// trait 侧的 MarkApplied 会刷新 DetectedAt，若共用会让上下文上限被无限续期。
	LearnedAt time.Time `json:"learned_at"`
	// RejectedTokens 触发学习时本次请求的估算输入 token，仅用于事后追溯
	RejectedTokens int `json:"rejected_tokens"`
}

// ChannelCompatEntry 记录单个 渠道-Key-模型 组合已学习到的全部兼容性事实。
type ChannelCompatEntry struct {
	Traits map[CompatTrait]CompatTraitState `json:"traits"`
	// ContextLimit 实测上下文上限；nil 表示未学习过（不做任何限制）。
	ContextLimit *ContextLimitState `json:"context_limit,omitempty"`
	DetectedAt   time.Time          `json:"detected_at"` // 最近一次学习/命中时间
}

// ChannelCompatCache 按 渠道-Key-模型 记忆上游的兼容性能力事实。
// 首次遇到上游明确拒绝（或主动探测得出结论）后写入，后续同组合请求在构造上游请求时直接应用，
// 避免每次都消耗一次失败往返，也避免要求用户手工勾选兼容性开关。
type ChannelCompatCache struct {
	cache map[string]*ChannelCompatEntry
	mu    sync.RWMutex
	// path 为空表示纯内存模式（测试与未启用持久化时）。
	path string
	// dirty 标记自上次落盘后是否有新增记忆，避免无变化时重复写盘。
	dirty bool
}

// NewChannelCompatCache 创建纯内存缓存实例（不落盘）。
func NewChannelCompatCache() *ChannelCompatCache {
	return &ChannelCompatCache{cache: make(map[string]*ChannelCompatEntry)}
}

// NewChannelCompatCacheWithPersistence 创建带落盘的缓存实例，并立即加载已有记忆。
// 加载失败（文件缺失/损坏）时退化为空缓存并继续运行：记忆丢失只意味着重新学习一次，
// 不应阻断代理服务启动。
func NewChannelCompatCacheWithPersistence(path string) *ChannelCompatCache {
	c := &ChannelCompatCache{
		cache: make(map[string]*ChannelCompatEntry),
		path:  path,
	}
	if err := c.load(); err != nil {
		log.Printf("[ChannelCompat-Load] 加载渠道兼容性记忆失败，从空状态开始: %v", err)
	} else if n := c.Size(); n > 0 {
		log.Printf("[ChannelCompat-Load] 已加载 %d 条渠道兼容性记忆", n)
	}
	return c
}

// load 从磁盘读取记忆，跳过已过期条目。
func (c *ChannelCompatCache) load() error {
	if c.path == "" {
		return nil
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	var stored map[string]*ChannelCompatEntry
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range stored {
		if entry == nil {
			continue
		}
		// 只有 ContextLimit 没有 traits 的条目同样有效，不能因 Traits 为 nil 丢弃
		if entry.Traits == nil && entry.ContextLimit == nil {
			continue
		}
		if entry.Traits == nil {
			entry.Traits = make(map[CompatTrait]CompatTraitState)
		}
		// 过期条目不加载，等价于重新学习
		if time.Since(entry.DetectedAt) > channelCompatTTL {
			continue
		}
		// 上下文上限按自己的 LearnedAt 判定过期：trait 命中会刷新 DetectedAt，
		// 共用会让实测上限被无限续期，上游放宽窗口后永远学不回来。
		if entry.ContextLimit != nil && time.Since(entry.ContextLimit.LearnedAt) > channelCompatTTL {
			entry.ContextLimit = nil
			if len(entry.Traits) == 0 {
				continue
			}
		}
		c.cache[key] = entry
	}
	return nil
}

// Flush 将当前记忆原子落盘（tmp + rename）。无变化时为空操作。
func (c *ChannelCompatCache) Flush() error {
	c.mu.Lock()
	if c.path == "" || !c.dirty {
		c.mu.Unlock()
		return nil
	}
	// 仅序列化未过期条目，顺带完成落盘时的清理
	snapshot := make(map[string]*ChannelCompatEntry, len(c.cache))
	for key, entry := range c.cache {
		if time.Since(entry.DetectedAt) <= channelCompatTTL {
			snapshot[key] = entry
		}
	}
	path := c.path
	c.dirty = false
	data, err := json.MarshalIndent(snapshot, "", "  ")
	c.mu.Unlock()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Record 记录一条学习到的兼容性事实。返回该 trait 是否为新增结论（此前未记录或结论翻转）。
// 仅在返回 true 时调用方才应触发同 Key 重试，避免记忆已生效后死循环。
// 新增时立即落盘：单次学习代价是一条上游 400，值得同步持久化。
func (c *ChannelCompatCache) Record(channelUID, keyHash, model string, trait CompatTrait, enabled bool, source, evidence string) bool {
	if trait == "" {
		return false
	}

	c.mu.Lock()
	key := GenerateCacheKey(channelUID, keyHash, model)
	entry, ok := c.cache[key]
	if !ok || time.Since(entry.DetectedAt) > channelCompatTTL {
		entry = &ChannelCompatEntry{Traits: make(map[CompatTrait]CompatTraitState)}
		c.cache[key] = entry
	}
	if entry.Traits == nil {
		entry.Traits = make(map[CompatTrait]CompatTraitState)
	}
	entry.DetectedAt = time.Now()

	prev, exists := entry.Traits[trait]
	// 已有相同结论时不重复记录；结论翻转（如探测后被真实报错纠正）则覆盖并视为新增。
	isNew := !exists || prev.Enabled != enabled
	if isNew {
		entry.Traits[trait] = CompatTraitState{
			Enabled:   enabled,
			Source:    source,
			Evidence:  truncateCompatEvidence(evidence),
			LearnedAt: time.Now(),
		}
		c.dirty = true
	}
	c.mu.Unlock()

	if !isNew {
		return false
	}
	// 锁外做 IO，避免阻塞并发请求路径
	if err := c.Flush(); err != nil {
		log.Printf("[ChannelCompat-Flush] 落盘渠道兼容性记忆失败: %v", err)
	}
	return true
}

// Trait 返回该组合上某个兼容性事实的学习结论。条目过期或未学习过时第二个返回值为 false。
func (c *ChannelCompatCache) Trait(channelUID, keyHash, model string, trait CompatTrait) (CompatTraitState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.cache[GenerateCacheKey(channelUID, keyHash, model)]
	if !ok || time.Since(entry.DetectedAt) > channelCompatTTL {
		return CompatTraitState{}, false
	}
	state, exists := entry.Traits[trait]
	return state, exists
}

// HasAnyTrait 判断该组合是否已有任何学习记录，用于决定是否需要触发一次主动探测。
func (c *ChannelCompatCache) HasAnyTrait(channelUID, keyHash, model string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.cache[GenerateCacheKey(channelUID, keyHash, model)]
	if !ok || time.Since(entry.DetectedAt) > channelCompatTTL {
		return false
	}
	return len(entry.Traits) > 0
}

// MarkApplied 记录一次基于记忆的主动改写，并刷新有效期。
func (c *ChannelCompatCache) MarkApplied(channelUID, keyHash, model string, trait CompatTrait) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.cache[GenerateCacheKey(channelUID, keyHash, model)]
	if !ok {
		return
	}
	state, exists := entry.Traits[trait]
	if !exists {
		return
	}
	state.ApplyCount++
	entry.Traits[trait] = state
	entry.DetectedAt = time.Now()
}

// Traits 返回该组合已学习的全部事实（按 trait 名字母序，便于日志稳定输出）。
func (c *ChannelCompatCache) Traits(channelUID, keyHash, model string) map[CompatTrait]CompatTraitState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.cache[GenerateCacheKey(channelUID, keyHash, model)]
	if !ok || time.Since(entry.DetectedAt) > channelCompatTTL {
		return nil
	}
	out := make(map[CompatTrait]CompatTraitState, len(entry.Traits))
	for trait, state := range entry.Traits {
		out[trait] = state
	}
	return out
}

// EnabledTraitNames 返回该组合上结论为"应启用"的 trait 名列表（字母序），用于日志输出。
func (c *ChannelCompatCache) EnabledTraitNames(channelUID, keyHash, model string) []string {
	names := make([]string, 0, 4)
	for trait, state := range c.Traits(channelUID, keyHash, model) {
		if state.Enabled {
			names = append(names, string(trait))
		}
	}
	sort.Strings(names)
	return names
}

// Clear 清除所有缓存。
func (c *ChannelCompatCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*ChannelCompatEntry)
}

// Size 返回缓存条目数量。
func (c *ChannelCompatCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.cache)
}

// minLearnableContextLimit 可学习上限的下界。
// 上游报错反推出的上限低于此值时不予采信：正常对话请求不会只有几百 token 还被判超长，
// 这种情况更可能是把无关 400 误判成上下文超限，学进去会直接把渠道永久排除。
const minLearnableContextLimit = 4096

// RecordContextLimit 记录该 渠道-Key-模型 组合实测的上下文输入上限。
// 返回是否写入了更严格的新结论（调用方据此决定是否输出日志）。
//
// 合成规则「宁小勿大」：多次学习之间取最小值。上游明确声明的窗口值（upstream_declared）
// 是精确事实，直接采信；只能反推的估算上界（rejected_estimate）取 rejectedTokens 的
// 保守折扣值，因为真实上限一定小于被拒绝的这次请求量，但具体小多少无从得知。
//
// 不做扩大：已学到 200k 后又出现一次 500k 被拒，不能推翻 200k 结论。上限只在遇到
// 更严格的证据时收紧，放宽只能靠 TTL 到期后重新学习。
func (c *ChannelCompatCache) RecordContextLimit(channelUID, keyHash, model string, limit int, source, evidence string, rejectedTokens int) bool {
	if limit < minLearnableContextLimit {
		return false
	}

	c.mu.Lock()
	key := GenerateCacheKey(channelUID, keyHash, model)
	entry, ok := c.cache[key]
	if !ok || time.Since(entry.DetectedAt) > channelCompatTTL {
		entry = &ChannelCompatEntry{Traits: make(map[CompatTrait]CompatTraitState)}
		c.cache[key] = entry
	}
	if entry.Traits == nil {
		entry.Traits = make(map[CompatTrait]CompatTraitState)
	}

	// 已有更严格（或相等）的上限时不覆盖，避免来回抖动
	if entry.ContextLimit != nil &&
		time.Since(entry.ContextLimit.LearnedAt) <= channelCompatTTL &&
		entry.ContextLimit.MaxInputTokens <= limit {
		c.mu.Unlock()
		return false
	}

	entry.ContextLimit = &ContextLimitState{
		MaxInputTokens: limit,
		Source:         source,
		Evidence:       truncateCompatEvidence(evidence),
		LearnedAt:      time.Now(),
		RejectedTokens: rejectedTokens,
	}
	entry.DetectedAt = time.Now()
	c.dirty = true
	c.mu.Unlock()

	// 锁外做 IO，避免阻塞并发请求路径
	if err := c.Flush(); err != nil {
		log.Printf("[ChannelCompat-Flush] 落盘渠道上下文上限失败: %v", err)
	}
	return true
}

// ContextLimit 返回该组合实测的上下文输入上限；未学习过或已过期时第二个返回值为 false。
// fail-open：无记忆时返回 false，调用方应沿用模型注册表窗口，不做额外限制。
func (c *ChannelCompatCache) ContextLimit(channelUID, keyHash, model string) (ContextLimitState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.cache[GenerateCacheKey(channelUID, keyHash, model)]
	if !ok || entry.ContextLimit == nil {
		return ContextLimitState{}, false
	}
	if time.Since(entry.ContextLimit.LearnedAt) > channelCompatTTL {
		return ContextLimitState{}, false
	}
	return *entry.ContextLimit, true
}

// MinContextLimitForChannelModel 返回该渠道-模型在所有已知 Key 上实测到的最小上下文上限。
//
// 用途：路由决策发生在选定具体 Key 之前，此时只知道渠道和目标模型。取最小值是保守的：
// 宁可放过一个窗口更大的 Key，也不要把请求送进已知会 400 的组合。
// 第二个返回值为 false 表示该渠道-模型无任何 Key 学到过上限，调用方沿用注册表窗口。
func (c *ChannelCompatCache) MinContextLimitForChannelModel(channelUID, model string) (int, bool) {
	if channelUID == "" || model == "" {
		return 0, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	minLimit := 0
	for key, entry := range c.cache {
		if entry == nil || entry.ContextLimit == nil {
			continue
		}
		// 键形如 channelUID:keyHash:model。模型名本身可能含冒号（如 Gemini 的
		// models/x:generateContent 形态），因此按前两个冒号切分后精确比对，
		// 不能用 HasPrefix/HasSuffix，否则会跨模型误命中。
		parts := strings.SplitN(key, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] != channelUID || !strings.EqualFold(parts[2], model) {
			continue
		}
		if time.Since(entry.ContextLimit.LearnedAt) > channelCompatTTL {
			continue
		}
		if minLimit == 0 || entry.ContextLimit.MaxInputTokens < minLimit {
			minLimit = entry.ContextLimit.MaxInputTokens
		}
	}
	return minLimit, minLimit > 0
}
