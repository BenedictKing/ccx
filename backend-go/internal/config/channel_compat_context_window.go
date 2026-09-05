package config

import (
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"
)

// contextWindowLearnedTTL 放宽方向证据（实测成功 / models API 自报）的有效期。
// 比 trait 的 24h 长：棘轮值只会被更强的新证据覆盖，长 TTL 意味着渠道扩容后
// 学到的更大窗口能稳定服务；到期回落注册表，等价于"重新学习一次"。
const contextWindowLearnedTTL = 7 * 24 * time.Hour

// ContextWindowLearnedState 渠道×协议×模型 粒度学习到的上下文窗口证据（放宽方向）。
//
// 与 ContextLimitState（渠道×Key×模型、由上游 400 学到的收紧上限）互补：
// 注册表登记的是模型公开窗口，但同一模型在不同渠道/协议上实际开放的窗口不同，
// 且会随供应商策略逐步放松（200K → 272K → 372K → 1M）。
// 这类"实际比注册表更大"的事实只能由成功请求（下界棘轮）或 /v1/models 元数据
// （声明值）学到——发前过滤若只信注册表，过期低窗口会把成功证据永远挡在门外。
type ContextWindowLearnedState struct {
	// ProvenInputTokens 成功请求实证的最小承载输入（下界，棘轮只升不降）。
	ProvenInputTokens int `json:"proven_input_tokens,omitempty"`
	// ProvenAt 最近一次实证时间，按 contextWindowLearnedTTL 判定新鲜度。
	ProvenAt time.Time `json:"proven_at,omitempty"`
	// ModelsAPIWindow /v1/models 元数据自报的输入窗口（声明值，最新覆盖）。
	ModelsAPIWindow int `json:"models_api_window,omitempty"`
	// ModelsAPIAt 最近一次声明时间。
	ModelsAPIAt time.Time `json:"models_api_at,omitempty"`
}

// contextWindowLearnedKey 学习窗口的存储键：渠道|协议|模型。
// 用 "|" 分隔，与 entries 的 ":" 键（channelUID:keyHash:model）空间天然隔离。
func contextWindowLearnedKey(channelUID, kind, model string) string {
	return channelUID + "|" + kind + "|" + model
}

func contextWindowLearnedFresh(state *ContextWindowLearnedState, now time.Time) bool {
	return state != nil && !state.ProvenAt.IsZero() && now.Sub(state.ProvenAt) <= contextWindowLearnedTTL
}

func modelsAPIWindowFresh(state *ContextWindowLearnedState, now time.Time) bool {
	return state != nil && !state.ModelsAPIAt.IsZero() && now.Sub(state.ModelsAPIAt) <= contextWindowLearnedTTL
}

// RecordContextWindowProven 记录一次成功请求实证的输入承载量。
// 下界棘轮：证据未过期时只在新值更大时更新（宁大勿小）；证据过期
// （ProvenAt 距今超过 contextWindowLearnedTTL）后允许以本次成功值重新起算，
// 否则 7 天后一个无法重新证明的历史高值会让放宽证据永久失效。
// 返回是否发生更新。调用方：handlers/common 的成功路径（handleSuccess 无错误
// 且 usage 有输入量）。
func (c *ChannelCompatCache) RecordContextWindowProven(channelUID, kind, model string, inputTokens int, now time.Time) bool {
	if channelUID == "" || model == "" || inputTokens <= 0 {
		return false
	}
	if kind == "" {
		kind = "unknown"
	}

	c.mu.Lock()
	key := contextWindowLearnedKey(channelUID, kind, model)
	state := c.contextWindows[key]
	if state == nil {
		state = &ContextWindowLearnedState{}
		c.contextWindows[key] = state
	}
	updated := false
	switch {
	case state.ProvenAt.IsZero() || !contextWindowLearnedFresh(state, now):
		// 首次记录或证据已过期：以本次成功值重建棘轮
		//（不保留一个已无法重新证明的历史高值）
		state.ProvenInputTokens = inputTokens
		state.ProvenAt = now
		updated = true
	case inputTokens > state.ProvenInputTokens:
		state.ProvenInputTokens = inputTokens
		state.ProvenAt = now
		updated = true
	}
	if updated {
		c.dirty = true
	}
	c.mu.Unlock()

	if updated {
		if err := c.Flush(); err != nil {
			log.Printf("[ChannelCompat-Flush] 落盘上下文窗口学习失败: %v", err)
		}
	}
	return updated
}

// RecordModelsAPIContextWindow 记录 /v1/models 元数据自报的输入窗口（声明值，最新覆盖）。
// 仅当声明值变化或过期被刷新时更新，返回是否发生更新。
func (c *ChannelCompatCache) RecordModelsAPIContextWindow(channelUID, kind, model string, window int, now time.Time) bool {
	if channelUID == "" || model == "" || window <= 0 {
		return false
	}
	if kind == "" {
		kind = "unknown"
	}

	c.mu.Lock()
	key := contextWindowLearnedKey(channelUID, kind, model)
	state := c.contextWindows[key]
	if state == nil {
		state = &ContextWindowLearnedState{}
		c.contextWindows[key] = state
	}
	updated := state.ModelsAPIWindow != window || !modelsAPIWindowFresh(state, now)
	if updated {
		state.ModelsAPIWindow = window
		state.ModelsAPIAt = now
		c.dirty = true
	}
	c.mu.Unlock()

	if updated {
		if err := c.Flush(); err != nil {
			log.Printf("[ChannelCompat-Flush] 落盘 models API 窗口声明失败: %v", err)
		}
	}
	return updated
}

// LearnedContextWindow 返回该渠道×协议×模型的放宽证据（都要求未过期）。
// proven/modelsAPI 为 0 表示无新鲜证据。
func (c *ChannelCompatCache) LearnedContextWindow(channelUID, kind, model string) (proven int, modelsAPI int) {
	if channelUID == "" || model == "" {
		return 0, 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	state := c.contextWindows[contextWindowLearnedKey(channelUID, kind, model)]
	now := time.Now()
	if contextWindowLearnedFresh(state, now) {
		proven = state.ProvenInputTokens
	}
	if modelsAPIWindowFresh(state, now) {
		modelsAPI = state.ModelsAPIWindow
	}
	return proven, modelsAPI
}

// EffectiveContextWindow 合成该渠道×协议×模型的有效输入窗口：
//
//		eff = min(declared上限, max(注册表窗口, 实证下界, models API 声明))
//
//	  - 无任何学习证据时等于注册表窗口（现行为，fail-open）；
//	  - 实证/声明顶开过期偏低的注册表（渐进扩容自动跟进）；
//	  - 实测 400 学到的收紧上限（渠道×模型，跨 Key 取最小）永远压得住放宽证据
//	    （渠道真降级时 declared 会重新学到，压过陈旧的 proven）。
func (c *ChannelCompatCache) EffectiveContextWindow(channelUID, kind, model string, registryWindow int) int {
	proven, modelsAPI := c.LearnedContextWindow(channelUID, kind, model)
	eff := registryWindow
	if proven > eff {
		eff = proven
	}
	if modelsAPI > eff {
		eff = modelsAPI
	}
	if declared, ok := c.MinContextLimitForChannelModel(channelUID, model); ok && declared > 0 && declared < eff {
		eff = declared
	}
	return eff
}

// ContextWindowLearnedEntries 返回全部学习窗口（诊断用，键为渠道|协议|模型）。
func (c *ChannelCompatCache) ContextWindowLearnedEntries() map[string]ContextWindowLearnedState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]ContextWindowLearnedState, len(c.contextWindows))
	for key, state := range c.contextWindows {
		if state != nil {
			out[key] = *state
		}
	}
	return out
}

// ContextWindowSnapshotEntry 管理端查看用的一条窗口学习记录视图。
// 复合键 channel|protocol|model 已拆开，禁止把原始键交给前端解析。
type ContextWindowSnapshotEntry struct {
	ChannelUID string `json:"channelUid"`
	Kind       string `json:"kind"`
	Model      string `json:"model"`
	// ProvenInputTokens/ProvenAt 成功请求实证（下界棘轮）及其时间与新鲜度。
	ProvenInputTokens int       `json:"provenInputTokens,omitempty"`
	ProvenAt          time.Time `json:"provenAt,omitempty"`
	ProvenFresh       bool      `json:"provenFresh"`
	// ModelsAPIWindow/ModelsAPIAt /v1/models 元数据声明及其时间与新鲜度。
	ModelsAPIWindow int       `json:"modelsApiWindow,omitempty"`
	ModelsAPIAt     time.Time `json:"modelsApiAt,omitempty"`
	ModelsAPIFresh  bool      `json:"modelsApiFresh"`
}

// ContextWindowSnapshot 返回全部上下文窗口学习记录的视图（管理端查看用，只读拷贝）。
func (c *ChannelCompatCache) ContextWindowSnapshot() []ContextWindowSnapshotEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	entries := make([]ContextWindowSnapshotEntry, 0, len(c.contextWindows))
	for key, state := range c.contextWindows {
		if state == nil {
			continue
		}
		parts := strings.SplitN(key, "|", 3)
		if len(parts) != 3 {
			continue
		}
		entries = append(entries, ContextWindowSnapshotEntry{
			ChannelUID:        parts[0],
			Kind:              parts[1],
			Model:             parts[2],
			ProvenInputTokens: state.ProvenInputTokens,
			ProvenAt:          state.ProvenAt,
			ProvenFresh:       contextWindowLearnedFresh(state, now),
			ModelsAPIWindow:   state.ModelsAPIWindow,
			ModelsAPIAt:       state.ModelsAPIAt,
			ModelsAPIFresh:    modelsAPIWindowFresh(state, now),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ChannelUID != entries[j].ChannelUID {
			return entries[i].ChannelUID < entries[j].ChannelUID
		}
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Model < entries[j].Model
	})
	return entries
}

// ClearContextWindows 只清除上下文窗口学习分区（traits/limits 保留），返回清除条目数。
// 清除后立即落盘，重启不复活。
func (c *ChannelCompatCache) ClearContextWindows() int {
	c.mu.Lock()

	removed := len(c.contextWindows)
	if removed == 0 {
		c.mu.Unlock()
		return 0
	}
	c.contextWindows = make(map[string]*ContextWindowLearnedState)
	c.dirty = true
	c.mu.Unlock()

	if err := c.Flush(); err != nil {
		log.Printf("[ChannelCompat-Flush] 清除窗口分区后落盘失败: %v", err)
	}
	return removed
}

// ParseModelsAPIContextWindows 从 OpenAI 兼容 /v1/models 响应体提取模型粒度的
// 上下文窗口元数据。常见字段：context_window / context_length / max_input_tokens /
// max_model_len（vLLM 系）与 top_provider.context_length（OpenRouter）。
// 大多数部署不提供，返回 nil 属常态，不构成错误。
func ParseModelsAPIContextWindows(body []byte) map[string]int {
	type modelsAPIModel struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		ContextWindow  int    `json:"context_window"`
		ContextLength  int    `json:"context_length"`
		MaxInputTokens int    `json:"max_input_tokens"`
		MaxModelLen    int    `json:"max_model_len"`
		TopProvider    struct {
			ContextLength int `json:"context_length"`
		} `json:"top_provider"`
	}
	var resp struct {
		Data   []modelsAPIModel `json:"data"`
		Models []modelsAPIModel `json:"models"`
	}
	// 解析失败静默返回：元数据收割是尽力而为，不阻断模型清单解析主路径。
	if json.Unmarshal(body, &resp) != nil {
		return nil
	}
	windows := make(map[string]int)
	record := func(m modelsAPIModel) {
		modelID := strings.TrimSpace(m.ID)
		if modelID == "" {
			modelID = strings.TrimSpace(m.Name)
			if index := strings.LastIndex(modelID, "/"); index >= 0 {
				modelID = modelID[index+1:]
			}
		}
		if modelID == "" {
			return
		}
		window := m.ContextWindow
		if m.ContextLength > window {
			window = m.ContextLength
		}
		if m.MaxInputTokens > window {
			window = m.MaxInputTokens
		}
		if m.MaxModelLen > window {
			window = m.MaxModelLen
		}
		if m.TopProvider.ContextLength > window {
			window = m.TopProvider.ContextLength
		}
		if window > 0 {
			windows[modelID] = window
		}
	}
	for _, m := range resp.Data {
		record(m)
	}
	for _, m := range resp.Models {
		record(m)
	}
	if len(windows) == 0 {
		return nil
	}
	return windows
}
