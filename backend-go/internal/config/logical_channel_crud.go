package config

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BenedictKing/ccx/internal/utils"
)

// remarkMaxRunes 单条渠道备注允许的最大字符数（按 rune 计）。
const remarkMaxRunes = 10

// remarkRuneCount 计算字符串的字符数（兼容多字节字符）。
func remarkRuneCount(s string) int {
	return utf8.RuneCountInString(s)
}

// CreateLogicalChannelInput 新建逻辑渠道的入参。
// 调用方负责收集并校验以下字段；CreateLogicalChannel 不会再做大幅度语义推断。
type CreateLogicalChannelInput struct {
	Name        string // 用户可见名（必填）
	Remark      string
	Description string
	Website     string
	ProviderID  string
	AccountUID  string
	Kind        LogicalChannelKind
	BaseURLs    []string // 必填，至少一个
	Tags        []string
	Protocols   []CreateLogicalChannelProtocol // 必填，至少一个；每个内部创建一条 UpstreamConfig
	Placement   string                         // "front" / "back"，仅在首个 protocol 时使用
}

// CreateLogicalChannelProtocol 单个新建协议的入参。
type CreateLogicalChannelProtocol struct {
	Kind              string // messages / chat / responses / gemini / images / vectors
	ServiceType       string // claude / openai / responses / gemini
	APIKeys           []string
	APIKeyConfigs     []APIKeyConfig
	BaseURLs          []string // 可选；缺省时回填到主 BaseURL
	BaseURL           string   // 可选；缺省时使用入参 BaseURLs[0]
	ModelMapping      map[string]string
	ReasoningMapping  map[string]string
	Priority          int
	Enabled           *bool  // 缺省 active
	Status            string // 缺省 active
	RoutePrefix       string
	SupportedModels   []string
	CustomHeaders     map[string]string
	ProxyURL          string
	ProxyPreferDirect bool
}

// CreateLogicalChannel 在事务内创建一个逻辑渠道及一组物理渠道。
// 内部使用 sync.Mutex (cm.mu) 保证原子性，失败时回滚已创建的物理渠道。
func (cm *ConfigManager) CreateLogicalChannel(in CreateLogicalChannelInput) (*LogicalChannel, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("name 不能为空")
	}
	if remarkRuneCount(strings.TrimSpace(in.Remark)) > remarkMaxRunes {
		return nil, fmt.Errorf("remark 不能超过 %d 个字符", remarkMaxRunes)
	}
	if len(in.BaseURLs) == 0 {
		return nil, fmt.Errorf("baseUrls 至少需要一个")
	}
	if len(in.Protocols) == 0 {
		return nil, fmt.Errorf("protocols 至少需要一个")
	}
	// 校验 kind 与 protocols 是否一致（至少 images/vectors 单数组专属）
	if in.Kind == LogicalChannelKindImages {
		for _, p := range in.Protocols {
			if p.Kind != "images" {
				return nil, fmt.Errorf("kind=images 的逻辑渠道只允许 images 协议")
			}
		}
	}
	if in.Kind == LogicalChannelKindEmbeddings {
		for _, p := range in.Protocols {
			if p.Kind != "vectors" {
				return nil, fmt.Errorf("kind=embeddings 的逻辑渠道只允许 vectors 协议")
			}
		}
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 重名检查：同 site + 同名不允许
	primary := strings.TrimSpace(in.BaseURLs[0])
	siteIdent := primarySiteIdentity(primary)
	for i := range cm.config.LogicalChannels {
		existing := &cm.config.LogicalChannels[i]
		if strings.EqualFold(existing.Name, in.Name) {
			return nil, fmt.Errorf("逻辑渠道名称 '%s' 已存在", in.Name)
		}
		if siteIdent != "" && existing.SiteIdentity == siteIdent {
			return nil, fmt.Errorf("站点 %s 已被逻辑渠道 '%s' 占用", siteIdent, existing.Name)
		}
	}

	uid := pickFreshUID(logicalUIDSet(cm.config))
	logical := &LogicalChannel{
		LogicalChannelUID: uid,
		AccountUID:        strings.TrimSpace(in.AccountUID),
		ProviderID:        strings.TrimSpace(in.ProviderID),
		Name:              strings.TrimSpace(in.Name),
		Remark:            in.Remark,
		Description:       in.Description,
		Website:           in.Website,
		Kind:              in.Kind,
		BaseURLs:          append([]string(nil), in.BaseURLs...),
		SiteIdentity:      siteIdent,
		Tags:              append([]string(nil), in.Tags...),
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	// 创建物理渠道
	created := make([]physicalChannelEntry, 0, len(in.Protocols))
	createdChannels := make([]UpstreamConfig, 0, len(in.Protocols))
	for _, p := range in.Protocols {
		up, err := cm.createPhysicalChannelForLogicalLocked(in, p, uid)
		if err != nil {
			// 回滚：删除已创建的物理渠道
			cm.rollbackCreatedChannelsLocked(createdChannels)
			return nil, fmt.Errorf("创建协议 %s 失败: %w", p.Kind, err)
		}
		created = append(created, physicalChannelEntry{slice: p.Kind, channel: up})
		createdChannels = append(createdChannels, up)
	}
	// 写回 logical.Protocols
	logical.Protocols = collectProtocolsFromEntries(created)

	// 更新内存 + 落盘
	cm.config.LogicalChannels = append(cm.config.LogicalChannels, *logical)
	if err := cm.saveConfigLocked(cm.config); err != nil {
		// 落盘失败也回滚
		cm.rollbackCreatedChannelsLocked(createdChannels)
		// 从 logical 列表中移除刚才追加的（删除切片最后一个元素）
		cm.config.LogicalChannels = cm.config.LogicalChannels[:len(cm.config.LogicalChannels)-1]
		return nil, err
	}
	log.Printf("[Config-LogicalChannel] 已创建逻辑渠道: uid=%s name=%s protocols=%d", uid, logical.Name, len(in.Protocols))
	return logical, nil
}

// createPhysicalChannelForLogicalLocked 在锁内创建一条物理渠道并写入对应数组。
// 成功返回插入后的 UpstreamConfig（已分配 ChannelUID 等）。
func (cm *ConfigManager) createPhysicalChannelForLogicalLocked(in CreateLogicalChannelInput, p CreateLogicalChannelProtocol, logicalUID string) (UpstreamConfig, error) {
	baseURLs := append([]string(nil), in.BaseURLs...)
	if len(p.BaseURLs) > 0 {
		baseURLs = append(baseURLs, p.BaseURLs...)
	}
	baseURL := strings.TrimSpace(p.BaseURL)
	if baseURL == "" && len(baseURLs) > 0 {
		baseURL = baseURLs[0]
	}
	up := UpstreamConfig{
		ChannelUID:        GenerateChannelUID(),
		AccountUID:        strings.TrimSpace(in.AccountUID),
		ProviderID:        strings.TrimSpace(in.ProviderID),
		Name:              derivePhysicalNameForLogical(in.Name, p.Kind, in.ProviderID),
		Remark:            strings.TrimSpace(in.Remark),
		ServiceType:       normalizeServiceTypeForKind(p.Kind, p.ServiceType),
		BaseURL:           baseURL,
		BaseURLs:          baseURLs,
		APIKeys:           append([]string(nil), p.APIKeys...),
		APIKeyConfigs:     append([]APIKeyConfig(nil), p.APIKeyConfigs...),
		ModelMapping:      p.ModelMapping,
		ReasoningMapping:  p.ReasoningMapping,
		Priority:          p.Priority,
		RoutePrefix:       p.RoutePrefix,
		SupportedModels:   append([]string(nil), p.SupportedModels...),
		CustomHeaders:     p.CustomHeaders,
		ProxyURL:          p.ProxyURL,
		ProxyPreferDirect: p.ProxyPreferDirect,
		LogicalChannelUID: logicalUID,
		LogicalName:       in.Name,
		Status:            defaultChannelStatus(p.Status, p.Enabled),
	}
	if up.Priority == 0 {
		up.Priority = 1
	}
	// 依据 kind 写入对应物理数组
	var err error
	switch p.Kind {
	case "messages":
		err = cm.appendUpstreamLocked(cm.config.Upstream, up, in.Placement)
		if err == nil {
			cm.config.Upstream = appendUpstream(cm.config.Upstream, up)
		}
	case "chat":
		err = cm.appendUpstreamLocked(cm.config.ChatUpstream, up, in.Placement)
		if err == nil {
			cm.config.ChatUpstream = appendUpstream(cm.config.ChatUpstream, up)
		}
	case "responses":
		err = cm.appendUpstreamLocked(cm.config.ResponsesUpstream, up, in.Placement)
		if err == nil {
			cm.config.ResponsesUpstream = appendUpstream(cm.config.ResponsesUpstream, up)
		}
	case "gemini":
		err = cm.appendUpstreamLocked(cm.config.GeminiUpstream, up, in.Placement)
		if err == nil {
			cm.config.GeminiUpstream = appendUpstream(cm.config.GeminiUpstream, up)
		}
	case "images":
		err = cm.appendUpstreamLocked(cm.config.ImagesUpstream, up, in.Placement)
		if err == nil {
			cm.config.ImagesUpstream = appendUpstream(cm.config.ImagesUpstream, up)
		}
	case "vectors":
		err = cm.appendUpstreamLocked(cm.config.VectorsUpstream, up, in.Placement)
		if err == nil {
			cm.config.VectorsUpstream = appendUpstream(cm.config.VectorsUpstream, up)
		}
	default:
		return up, fmt.Errorf("不支持的协议 kind: %s", p.Kind)
	}
	return up, err
}

// appendUpstreamLocked 在锁内完成名称去重、归一化、优先级分配等公共处理。
// 返回的错误直接终止上层的事务回滚。
func (cm *ConfigManager) appendUpstreamLocked(channels []UpstreamConfig, up UpstreamConfig, placement string) error {
	// 重名检查（同一数组）
	for i := range channels {
		if strings.EqualFold(channels[i].Name, up.Name) {
			return fmt.Errorf("渠道名称 '%s' 在 %s 已存在", up.Name, up.ServiceType)
		}
	}
	if strings.TrimSpace(up.ChannelUID) == "" {
		up.ChannelUID = GenerateChannelUID()
	}
	up.Status = defaultChannelStatus(up.Status, nil)
	// 复用现有分配逻辑（按 placement 决定 front/back）
	assignChannelPriority(channels, &up, placement)
	return nil
}

// appendUpstream 把新渠道按 placement 插入到切片。
func appendUpstream(channels []UpstreamConfig, up UpstreamConfig) []UpstreamConfig {
	out := make([]UpstreamConfig, 0, len(channels)+1)
	out = append(out, up)
	out = append(out, channels...)
	return out
}

// rollbackCreatedChannelsLocked 锁内回滚：根据 ChannelUID 把已成功写入内存的物理渠道移除。
// 注意：仅回滚内存，不写盘（上层决定是否继续）。
func (cm *ConfigManager) rollbackCreatedChannelsLocked(channels []UpstreamConfig) {
	if len(channels) == 0 {
		return
	}
	cm.config.Upstream = removeByChannelUID(cm.config.Upstream, channels)
	cm.config.ChatUpstream = removeByChannelUID(cm.config.ChatUpstream, channels)
	cm.config.ResponsesUpstream = removeByChannelUID(cm.config.ResponsesUpstream, channels)
	cm.config.GeminiUpstream = removeByChannelUID(cm.config.GeminiUpstream, channels)
	cm.config.ImagesUpstream = removeByChannelUID(cm.config.ImagesUpstream, channels)
	cm.config.VectorsUpstream = removeByChannelUID(cm.config.VectorsUpstream, channels)
}

func removeByChannelUID(channels []UpstreamConfig, removed []UpstreamConfig) []UpstreamConfig {
	rm := make(map[string]struct{}, len(removed))
	for _, r := range removed {
		rm[r.ChannelUID] = struct{}{}
	}
	out := make([]UpstreamConfig, 0, len(channels))
	for _, c := range channels {
		if _, ok := rm[c.ChannelUID]; ok {
			continue
		}
		out = append(out, c)
	}
	return out
}

// UpdateLogicalChannelInput 更新逻辑渠道的入参。
// 三个字段语义独立：Common 应用于所有协议；Protocols 按 kind 更新（不存在则新增）；Removals 列出要删除的 protocol kinds。
type UpdateLogicalChannelInput struct {
	LogicalChannelUID string
	Common            *UpdateLogicalChannelCommon
	Protocols         []UpdateLogicalChannelProtocol
	Removals          []string // 要删除的协议 kind 列表
	Placement         string
}

// UpdateLogicalChannelCommon 跨协议共享字段。
type UpdateLogicalChannelCommon struct {
	Name        *string
	Remark      *string
	Description *string
	Website     *string
	Tags        *[]string
	BaseURLs    *[]string
}

// UpdateLogicalChannelProtocol 更新或新增单协议。
type UpdateLogicalChannelProtocol struct {
	Kind              string
	ServiceType       string
	APIKeys           []string
	APIKeyConfigs     []APIKeyConfig
	BaseURLs          []string
	BaseURL           string
	ModelMapping      map[string]string
	ReasoningMapping  map[string]string
	Priority          int
	Enabled           *bool
	Status            string
	RoutePrefix       string
	SupportedModels   []string
	CustomHeaders     map[string]string
	ProxyURL          string
	ProxyPreferDirect bool
}

// UpdateLogicalChannel 事务内更新逻辑渠道（含 protocols 的增删改）。
// 失败时回滚所有改动。
func (cm *ConfigManager) UpdateLogicalChannel(in UpdateLogicalChannelInput) (*LogicalChannel, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	idx := findLogicalChannelIndexLocked(&cm.config, in.LogicalChannelUID)
	if idx < 0 {
		return nil, fmt.Errorf("逻辑渠道 %s 不存在", in.LogicalChannelUID)
	}
	logical := &cm.config.LogicalChannels[idx]
	// 记录原始快照以便回滚
	backupLogical := *logical
	backupSlices := backupAllUpstreamSlices(cm.config)
	backupManaged := append([]ManagedAccountConfig(nil), cm.config.ManagedAccounts...)

	// 1) 通用字段
	if in.Common != nil {
		if in.Common.Name != nil {
			newName := strings.TrimSpace(*in.Common.Name)
			if newName == "" {
				return nil, fmt.Errorf("name 不能为空")
			}
			if !strings.EqualFold(newName, logical.Name) {
				// 检查新名是否被其他 logical 占用
				for i := range cm.config.LogicalChannels {
					if i == idx {
						continue
					}
					if strings.EqualFold(cm.config.LogicalChannels[i].Name, newName) {
						return nil, fmt.Errorf("逻辑渠道名称 '%s' 已存在", newName)
					}
				}
			}
			logical.Name = newName
		}
		if in.Common.Remark != nil {
			r := strings.TrimSpace(*in.Common.Remark)
			if remarkRuneCount(r) > remarkMaxRunes {
				return nil, fmt.Errorf("remark 不能超过 %d 个字符", remarkMaxRunes)
			}
			logical.Remark = r
		}
		if in.Common.Description != nil {
			logical.Description = *in.Common.Description
		}
		if in.Common.Website != nil {
			logical.Website = *in.Common.Website
		}
		if in.Common.Tags != nil {
			logical.Tags = append([]string(nil), *in.Common.Tags...)
		}
		if in.Common.BaseURLs != nil {
			if len(*in.Common.BaseURLs) == 0 {
				return nil, fmt.Errorf("baseUrls 至少需要一个")
			}
			logical.BaseURLs = append([]string(nil), *in.Common.BaseURLs...)
			if ident := primarySiteIdentity(logical.BaseURLs[0]); ident != "" {
				logical.SiteIdentity = ident
			}
		}
	}

	// 2) 删除指定的协议
	if len(in.Removals) > 0 {
		kinds := make(map[string]struct{}, len(in.Removals))
		for _, k := range in.Removals {
			kinds[strings.ToLower(k)] = struct{}{}
		}
		// 不允许删到 0 个 protocol
		remaining := 0
		for _, p := range logical.Protocols {
			if _, drop := kinds[strings.ToLower(p.Kind)]; drop {
				continue
			}
			remaining++
		}
		for _, p := range in.Protocols {
			if _, exists := kinds[strings.ToLower(p.Kind)]; !exists {
				remaining++
			}
		}
		if remaining == 0 {
			return nil, fmt.Errorf("逻辑渠道至少需要保留一个协议")
		}
		newProtocols := make([]LogicalChannelProtocol, 0, len(logical.Protocols))
		for _, p := range logical.Protocols {
			if _, drop := kinds[strings.ToLower(p.Kind)]; drop {
				// 删除对应物理渠道
				if err := cm.removePhysicalChannelByUIDLocked(p.Kind, p.ChannelUID); err != nil {
					// 回滚
					cm.restoreAllUpstreamSlicesLocked(backupSlices)
					cm.config.ManagedAccounts = backupManaged
					cm.config.LogicalChannels[idx] = backupLogical
					return nil, err
				}
				continue
			}
			newProtocols = append(newProtocols, p)
		}
		logical.Protocols = newProtocols
	}

	// 3) 新增或更新 protocols
	for _, p := range in.Protocols {
		kind := strings.ToLower(p.Kind)
		if !isValidChannelKind(kind) {
			return nil, fmt.Errorf("不支持的协议 kind: %s", p.Kind)
		}
		existing := findProtocolByKind(logical.Protocols, kind)
		if existing != nil {
			// 找到已存在的 protocol：更新物理渠道字段
			if err := cm.updatePhysicalChannelForLogicalLocked(kind, existing.ChannelUID, p, logical); err != nil {
				cm.restoreAllUpstreamSlicesLocked(backupSlices)
				cm.config.ManagedAccounts = backupManaged
				cm.config.LogicalChannels[idx] = backupLogical
				return nil, err
			}
			// 同步 logical.Protocols entry
			entry := physicalProtocolEntry(kind, getChannelByUID(&cm.config, kind, existing.ChannelUID), p)
			replaceProtocolInLogical(logical, entry)
		} else {
			// 新增 protocol
			up, err := cm.createPhysicalChannelForLogicalLocked(CreateLogicalChannelInput{
				Name:       logical.Name,
				Remark:     logical.Remark,
				ProviderID: logical.ProviderID,
				AccountUID: logical.AccountUID,
				Kind:       logical.Kind,
				BaseURLs:   logical.BaseURLs,
				Tags:       logical.Tags,
			}, CreateLogicalChannelProtocol{
				Kind:              kind,
				ServiceType:       p.ServiceType,
				APIKeys:           p.APIKeys,
				APIKeyConfigs:     p.APIKeyConfigs,
				BaseURLs:          p.BaseURLs,
				BaseURL:           p.BaseURL,
				ModelMapping:      p.ModelMapping,
				ReasoningMapping:  p.ReasoningMapping,
				Priority:          p.Priority,
				Enabled:           p.Enabled,
				Status:            p.Status,
				RoutePrefix:       p.RoutePrefix,
				SupportedModels:   p.SupportedModels,
				CustomHeaders:     p.CustomHeaders,
				ProxyURL:          p.ProxyURL,
				ProxyPreferDirect: p.ProxyPreferDirect,
			}, logical.LogicalChannelUID)
			if err != nil {
				cm.restoreAllUpstreamSlicesLocked(backupSlices)
				cm.config.ManagedAccounts = backupManaged
				cm.config.LogicalChannels[idx] = backupLogical
				return nil, fmt.Errorf("新增协议 %s 失败: %w", kind, err)
			}
			// 同步 protocols 列表
			enabled := strings.EqualFold(strings.TrimSpace(up.Status), "active")
			entry := LogicalChannelProtocol{
				Kind:        kind,
				ChannelUID:  up.ChannelUID,
				ServiceType: up.ServiceType,
				Enabled:     enabled,
				Status:      up.Status,
				Priority:    up.Priority,
				RoutePrefix: up.RoutePrefix,
			}
			logical.Protocols = append(logical.Protocols, entry)
		}
	}

	logical.UpdatedAt = time.Now().UTC()
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.restoreAllUpstreamSlicesLocked(backupSlices)
		cm.config.ManagedAccounts = backupManaged
		cm.config.LogicalChannels[idx] = backupLogical
		return nil, err
	}
	log.Printf("[Config-LogicalChannel] 已更新逻辑渠道: uid=%s name=%s protocols=%d", logical.LogicalChannelUID, logical.Name, len(logical.Protocols))
	return logical, nil
}

// DeleteLogicalChannel 事务内删除逻辑渠道及全部下属物理渠道。
// 成功后返回已删除的物理渠道列表（用于上层清理 metrics/log）。
func (cm *ConfigManager) DeleteLogicalChannel(uid string) ([]UpstreamConfig, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	idx := findLogicalChannelIndexLocked(&cm.config, uid)
	if idx < 0 {
		return nil, fmt.Errorf("逻辑渠道 %s 不存在", uid)
	}
	logical := cm.config.LogicalChannels[idx]
	removed := make([]UpstreamConfig, 0, len(logical.Protocols))
	for _, p := range logical.Protocols {
		up := removePhysicalChannelByUIDInPlace(&cm.config, p.Kind, p.ChannelUID)
		if up != nil {
			up.LogicalChannelUID = ""
			up.LogicalName = ""
			removed = append(removed, *up)
		}
	}
	// 删除 logical
	cm.config.LogicalChannels = append(cm.config.LogicalChannels[:idx], cm.config.LogicalChannels[idx+1:]...)
	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}
	log.Printf("[Config-LogicalChannel] 已删除逻辑渠道: uid=%s name=%s 物理渠道=%d", uid, logical.Name, len(removed))
	return removed, nil
}

// ListLogicalChannels 返回所有逻辑渠道的副本。
func (cm *ConfigManager) ListLogicalChannels() []LogicalChannel {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	out := make([]LogicalChannel, 0, len(cm.config.LogicalChannels))
	out = append(out, cm.config.LogicalChannels...)
	return out
}

// ListLogicalChannelsWithKind 按 kind 过滤。
func (cm *ConfigManager) ListLogicalChannelsWithKind(kind LogicalChannelKind) []LogicalChannel {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	out := make([]LogicalChannel, 0, len(cm.config.LogicalChannels))
	for _, l := range cm.config.LogicalChannels {
		if l.Kind == kind {
			out = append(out, l)
		}
	}
	return out
}

// GetLogicalChannel 按 UID 取逻辑渠道。
func (cm *ConfigManager) GetLogicalChannel(uid string) *LogicalChannel {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for i := range cm.config.LogicalChannels {
		if cm.config.LogicalChannels[i].LogicalChannelUID == uid {
			out := cm.config.LogicalChannels[i]
			return &out
		}
	}
	return nil
}

// ListLogicalChannelsByKind 别名。
func (cm *ConfigManager) ListLogicalChannelsByKind(kind LogicalChannelKind) []LogicalChannel {
	return cm.ListLogicalChannelsWithKind(kind)
}

// ============== 锁内辅助 ==============

// removePhysicalChannelByUIDLocked 在锁内按 kind+channelUID 删除一条物理渠道。
func (cm *ConfigManager) removePhysicalChannelByUIDLocked(kind, channelUID string) error {
	up := removePhysicalChannelByUIDInPlace(&cm.config, kind, channelUID)
	if up == nil {
		return fmt.Errorf("物理渠道 %s/%s 不存在", kind, channelUID)
	}
	return nil
}

// removePhysicalChannelByUIDInPlace 真正从六个数组里删除，返回被删除的 *UpstreamConfig。
func removePhysicalChannelByUIDInPlace(cfg *Config, kind, channelUID string) *UpstreamConfig {
	if channelUID == "" {
		return nil
	}
	var target *UpstreamConfig
	switch kind {
	case "messages":
		for i := range cfg.Upstream {
			if cfg.Upstream[i].ChannelUID == channelUID {
				target = &cfg.Upstream[i]
				cfg.Upstream = append(cfg.Upstream[:i], cfg.Upstream[i+1:]...)
				break
			}
		}
	case "chat":
		for i := range cfg.ChatUpstream {
			if cfg.ChatUpstream[i].ChannelUID == channelUID {
				target = &cfg.ChatUpstream[i]
				cfg.ChatUpstream = append(cfg.ChatUpstream[:i], cfg.ChatUpstream[i+1:]...)
				break
			}
		}
	case "responses":
		for i := range cfg.ResponsesUpstream {
			if cfg.ResponsesUpstream[i].ChannelUID == channelUID {
				target = &cfg.ResponsesUpstream[i]
				cfg.ResponsesUpstream = append(cfg.ResponsesUpstream[:i], cfg.ResponsesUpstream[i+1:]...)
				break
			}
		}
	case "gemini":
		for i := range cfg.GeminiUpstream {
			if cfg.GeminiUpstream[i].ChannelUID == channelUID {
				target = &cfg.GeminiUpstream[i]
				cfg.GeminiUpstream = append(cfg.GeminiUpstream[:i], cfg.GeminiUpstream[i+1:]...)
				break
			}
		}
	case "images":
		for i := range cfg.ImagesUpstream {
			if cfg.ImagesUpstream[i].ChannelUID == channelUID {
				target = &cfg.ImagesUpstream[i]
				cfg.ImagesUpstream = append(cfg.ImagesUpstream[:i], cfg.ImagesUpstream[i+1:]...)
				break
			}
		}
	case "vectors":
		for i := range cfg.VectorsUpstream {
			if cfg.VectorsUpstream[i].ChannelUID == channelUID {
				target = &cfg.VectorsUpstream[i]
				cfg.VectorsUpstream = append(cfg.VectorsUpstream[:i], cfg.VectorsUpstream[i+1:]...)
				break
			}
		}
	}
	return target
}

// updatePhysicalChannelForLogicalLocked 在锁内更新单条物理渠道字段。
func (cm *ConfigManager) updatePhysicalChannelForLogicalLocked(kind, channelUID string, p UpdateLogicalChannelProtocol, logical *LogicalChannel) error {
	up := getChannelByUID(&cm.config, kind, channelUID)
	if up == nil {
		return fmt.Errorf("物理渠道 %s/%s 不存在", kind, channelUID)
	}
	// 同步 logical 级别的共享字段到每个物理渠道，确保名称/站点地址池一致
	up.Name = logical.Name
	up.LogicalName = logical.Name
	up.Remark = logical.Remark
	up.Website = logical.Website
	up.Description = logical.Description
	up.Tags = append([]string(nil), logical.Tags...)
	if len(logical.BaseURLs) > 0 {
		up.BaseURLs = append([]string(nil), logical.BaseURLs...)
		up.BaseURL = logical.BaseURLs[0]
	}

	if len(p.APIKeys) > 0 {
		up.APIKeys = append([]string(nil), p.APIKeys...)
	}
	if len(p.APIKeyConfigs) > 0 {
		up.APIKeyConfigs = append([]APIKeyConfig(nil), p.APIKeyConfigs...)
	}
	if len(p.BaseURLs) > 0 {
		up.BaseURLs = append([]string(nil), p.BaseURLs...)
		if up.BaseURL == "" && len(up.BaseURLs) > 0 {
			up.BaseURL = up.BaseURLs[0]
		}
	}
	if p.BaseURL != "" {
		up.BaseURL = p.BaseURL
	}
	if p.ServiceType != "" {
		up.ServiceType = p.ServiceType
	}
	if p.ModelMapping != nil {
		up.ModelMapping = p.ModelMapping
	}
	if p.ReasoningMapping != nil {
		up.ReasoningMapping = p.ReasoningMapping
	}
	if p.Priority > 0 {
		up.Priority = p.Priority
	}
	if p.Enabled != nil {
		if *p.Enabled {
			up.Status = "active"
		} else {
			up.Status = "suspended"
		}
	}
	if p.Status != "" {
		up.Status = p.Status
	}
	if p.RoutePrefix != "" {
		up.RoutePrefix = p.RoutePrefix
	}
	if len(p.SupportedModels) > 0 {
		up.SupportedModels = append([]string(nil), p.SupportedModels...)
	}
	if p.CustomHeaders != nil {
		up.CustomHeaders = p.CustomHeaders
	}
	if p.ProxyURL != "" {
		up.ProxyURL = p.ProxyURL
	}
	if p.ProxyPreferDirect {
		up.ProxyPreferDirect = true
	}
	return nil
}

// getChannelByUID 锁外调用，必须在 cm.mu 持有时调用。
// 返回指针以允许修改；如果不存在返回 nil。
func getChannelByUID(cfg *Config, kind, channelUID string) *UpstreamConfig {
	if channelUID == "" {
		return nil
	}
	switch kind {
	case "messages":
		for i := range cfg.Upstream {
			if cfg.Upstream[i].ChannelUID == channelUID {
				return &cfg.Upstream[i]
			}
		}
	case "chat":
		for i := range cfg.ChatUpstream {
			if cfg.ChatUpstream[i].ChannelUID == channelUID {
				return &cfg.ChatUpstream[i]
			}
		}
	case "responses":
		for i := range cfg.ResponsesUpstream {
			if cfg.ResponsesUpstream[i].ChannelUID == channelUID {
				return &cfg.ResponsesUpstream[i]
			}
		}
	case "gemini":
		for i := range cfg.GeminiUpstream {
			if cfg.GeminiUpstream[i].ChannelUID == channelUID {
				return &cfg.GeminiUpstream[i]
			}
		}
	case "images":
		for i := range cfg.ImagesUpstream {
			if cfg.ImagesUpstream[i].ChannelUID == channelUID {
				return &cfg.ImagesUpstream[i]
			}
		}
	case "vectors":
		for i := range cfg.VectorsUpstream {
			if cfg.VectorsUpstream[i].ChannelUID == channelUID {
				return &cfg.VectorsUpstream[i]
			}
		}
	}
	return nil
}

// ============== 工具方法 ==============

func findLogicalChannelIndexLocked(cfg *Config, uid string) int {
	if cfg == nil || uid == "" {
		return -1
	}
	for i := range cfg.LogicalChannels {
		if cfg.LogicalChannels[i].LogicalChannelUID == uid {
			return i
		}
	}
	return -1
}

func primarySiteIdentity(baseURL string) string {
	identities := siteIdentitiesForBaseURL(baseURL)
	if len(identities) == 0 {
		return ""
	}
	return identities[0]
}

func siteIdentitiesForBaseURL(baseURL string) []string {
	return baseURLSiteIdentitiesImport(baseURL)
}

// 在 logical_channel.go 中重写为 utils.BaseURLSiteIdentities（避免重复 import）。
// 这里我们走一个本地薄封装以分离命名空间。

func normalizeServiceTypeForKind(kind, serviceType string) string {
	if strings.TrimSpace(serviceType) != "" {
		return strings.ToLower(strings.TrimSpace(serviceType))
	}
	switch kind {
	case "messages":
		return "claude"
	case "chat", "images", "vectors":
		return "openai"
	case "responses":
		return "responses"
	case "gemini":
		return "gemini"
	}
	return serviceType
}

func defaultChannelStatus(status string, enabled *bool) string {
	if status != "" {
		return status
	}
	if enabled != nil {
		if *enabled {
			return "active"
		}
		return "suspended"
	}
	return "active"
}

func derivePhysicalNameForLogical(logicalName, kind, providerID string) string {
	if providerID != "" && (kind == "messages" || kind == "chat" || kind == "responses" || kind == "gemini") {
		return logicalName + " - " + accountRouteSuffixLikeAutoAdd(kind)
	}
	return logicalName
}

// accountRouteSuffixLikeAutoAdd 借鉴 autopilot.handlers_auto_managed 中的后缀规则，
// 避免在前端再误合并同站多协议。
func accountRouteSuffixLikeAutoAdd(kind string) string {
	switch kind {
	case "messages":
		return "claude"
	case "chat":
		return "chat"
	case "responses":
		return "codex"
	case "gemini":
		return "gemini"
	}
	return kind
}

func findProtocolByKind(protocols []LogicalChannelProtocol, kind string) *LogicalChannelProtocol {
	for i := range protocols {
		if strings.EqualFold(protocols[i].Kind, kind) {
			return &protocols[i]
		}
	}
	return nil
}

func replaceProtocolInLogical(l *LogicalChannel, entry LogicalChannelProtocol) {
	for i := range l.Protocols {
		if strings.EqualFold(l.Protocols[i].Kind, entry.Kind) {
			l.Protocols[i] = entry
			return
		}
	}
	l.Protocols = append(l.Protocols, entry)
}

func physicalProtocolEntry(kind string, up *UpstreamConfig, p UpdateLogicalChannelProtocol) LogicalChannelProtocol {
	if up == nil {
		return LogicalChannelProtocol{Kind: kind, ServiceType: p.ServiceType}
	}
	return LogicalChannelProtocol{
		Kind:        kind,
		ChannelUID:  up.ChannelUID,
		ServiceType: up.ServiceType,
		Enabled:     strings.EqualFold(strings.TrimSpace(up.Status), "active"),
		Status:      up.Status,
		Priority:    up.Priority,
		RoutePrefix: up.RoutePrefix,
	}
}

func isValidChannelKind(kind string) bool {
	switch kind {
	case "messages", "chat", "responses", "gemini", "images", "vectors":
		return true
	}
	return false
}

func logicalUIDSet(cfg Config) map[string]struct{} {
	out := make(map[string]struct{}, len(cfg.LogicalChannels))
	for _, l := range cfg.LogicalChannels {
		if l.LogicalChannelUID != "" {
			out[l.LogicalChannelUID] = struct{}{}
		}
	}
	return out
}

// backupAllUpstreamSlices 深拷贝六个 Upstream 切片（用于事务回滚）。
func backupAllUpstreamSlices(cfg Config) [6][]UpstreamConfig {
	up := append([]UpstreamConfig(nil), cfg.Upstream...)
	chat := append([]UpstreamConfig(nil), cfg.ChatUpstream...)
	res := append([]UpstreamConfig(nil), cfg.ResponsesUpstream...)
	gem := append([]UpstreamConfig(nil), cfg.GeminiUpstream...)
	img := append([]UpstreamConfig(nil), cfg.ImagesUpstream...)
	vec := append([]UpstreamConfig(nil), cfg.VectorsUpstream...)
	return [6][]UpstreamConfig{up, chat, res, gem, img, vec}
}

// restoreAllUpstreamSlicesLocked 把六个 Upstream 切片恢复为备份。
func (cm *ConfigManager) restoreAllUpstreamSlicesLocked(backup [6][]UpstreamConfig) {
	cm.config.Upstream = backup[0]
	cm.config.ChatUpstream = backup[1]
	cm.config.ResponsesUpstream = backup[2]
	cm.config.GeminiUpstream = backup[3]
	cm.config.ImagesUpstream = backup[4]
	cm.config.VectorsUpstream = backup[5]
}

// baseURLSiteIdentitiesImport 调用 utils.BaseURLSiteIdentities。
func baseURLSiteIdentitiesImport(rawURL string) []string {
	return utils.BaseURLSiteIdentities(rawURL)
}

// errors 占位避免未使用 import 警告（保留 errors 接口以备未来更细错误类型）
var _ = errors.New
