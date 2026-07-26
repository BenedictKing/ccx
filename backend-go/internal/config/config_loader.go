package config

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/fsnotify/fsnotify"
)

const (
	maxBackups      = 10
	keyRecoveryTime = 5 * time.Minute
	maxFailureCount = 3

	// configReloadDebounce 是 watcher 收到文件变更后的防抖窗口：
	// 在窗口内的连续事件合并为一次 loadConfig，避免编辑器原子保存或快速多次写入触发多次重载。
	configReloadDebounce = 100 * time.Millisecond
)

// NewConfigManager 创建配置管理器
func NewConfigManager(configFile string, backupDir string) (*ConfigManager, error) {
	cm := &ConfigManager{
		configFile:      configFile,
		backupDir:       backupDir,
		failedKeysCache: make(map[string]*FailedKey),
		keyRecoveryTime: keyRecoveryTime,
		maxFailureCount: maxFailureCount,
		stopChan:        make(chan struct{}),
		reloadCh:        make(chan struct{}, 1),
	}

	// 加载配置
	if err := cm.loadConfig(); err != nil {
		return nil, err
	}

	// 启动文件监听
	if err := cm.startWatcher(); err != nil {
		log.Printf("[Config-Watcher] 警告: 启动配置文件监听失败: %v", err)
	}

	// 启动定期清理
	cm.backgroundWG.Add(1)
	go func() {
		defer cm.backgroundWG.Done()
		cm.cleanupExpiredFailures()
	}()

	return cm, nil
}

// loadConfig 加载配置
// loadConfig 加载配置
func (cm *ConfigManager) loadConfig() error {
	cm.mu.Lock()

	// 如果配置文件不存在，创建默认配置
	if _, err := os.Stat(cm.configFile); os.IsNotExist(err) {
		err := cm.createDefaultConfig()
		cm.mu.Unlock()
		return err
	}

	// 读取配置文件
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		cm.mu.Unlock()
		return err
	}

	var loaded Config
	autopilotDecodeFallback := false
	if err := json.Unmarshal(data, &loaded); err != nil {
		fallback, fallbackErr := decodeConfigWithDefaultAutopilot(data)
		if fallbackErr != nil {
			cm.mu.Unlock()
			return err
		}
		loaded = fallback
		autopilotDecodeFallback = true
		log.Printf("[Config-Migration] 警告: autopilot 配置无法解析，已回退到默认值: %v", err)
	}
	cm.config = loaded
	cm.config.hydrateManagedAccountCredentials()

	// 兼容旧配置：检查 FuzzyModeEnabled 字段是否存在
	// 如果不存在，默认设为 true（新功能默认启用）
	needSaveDefaults := cm.applyConfigDefaults(data) || autopilotDecodeFallback
	// Autopilot 智能路由配置：旧版本升级、缺失值补齐与校验归一化
	if !autopilotDecodeFallback && cm.applyAutopilotDefaults(data) {
		needSaveDefaults = true
	}
	if cm.applyServiceTypeDefaults() {
		needSaveDefaults = true
	}
	if cm.applyCodexToolCompatMigration(data) {
		needSaveDefaults = true
	}
	if cm.migrateFableModelMapping() {
		needSaveDefaults = true
	}
	if cm.migrateFableReasoningMapping() {
		needSaveDefaults = true
	}
	if cm.migrateDeprecatedGrokModelMapping() {
		needSaveDefaults = true
	}
	if cm.ensureChannelUIDs() {
		needSaveDefaults = true
	}
	if cm.ensureAccountUIDs() {
		needSaveDefaults = true
	}
	if cm.mergeManagedProviderAccounts() {
		needSaveDefaults = true
	}
	if cm.ensureCredentialUIDs() {
		needSaveDefaults = true
	}
	if cm.ensureOriginBackfill() {
		needSaveDefaults = true
	}
	if cm.migrateDisabledKeyRecoveryTimes(time.Now()) {
		needSaveDefaults = true
	}

	// 兼容旧格式：检测是否需要迁移
	needMigration := cm.migrateOldFormat()

	// 如果有默认值迁移或格式迁移，保存配置
	if needSaveDefaults || needMigration {
		if err := cm.saveConfigLocked(cm.config); err != nil {
			log.Printf("[Config-Migration] 警告: 保存迁移后的配置失败: %v", err)
			cm.mu.Unlock()
			return err
		}
		if needMigration {
			log.Printf("[Config-Migration] 配置迁移完成")
		}
	}

	// 自检：没有配置 key 的渠道自动暂停
	if cm.validateChannelKeys() {
		if err := cm.saveConfigLocked(cm.config); err != nil {
			log.Printf("[Config-Validate] 警告: 保存自检后的配置失败: %v", err)
			cm.mu.Unlock()
			return err
		}
	}

	// 成功加载后通知回调（在锁内构造快照，释放锁后通知）
	cm.fireConfigChangeCallbacks()
	return nil
}

// decodeConfigWithDefaultAutopilot 仅忽略无法强类型解析的 autopilot 块。
// 若移除该块后仍解析失败，说明错误位于其他配置，调用方应保留原始错误。
func decodeConfigWithDefaultAutopilot(rawJSON []byte) (Config, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &root); err != nil {
		return Config{}, err
	}
	if _, exists := root["autopilot"]; !exists {
		return Config{}, fmt.Errorf("autopilot 配置块不存在")
	}
	root["autopilot"] = json.RawMessage("null")

	sanitized, err := json.Marshal(root)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(sanitized, &cfg); err != nil {
		return Config{}, err
	}
	cfg.AutopilotRouting = DefaultAutopilotRoutingConfig()
	return cfg, nil
}

// createDefaultConfig 创建默认配置
func (cm *ConfigManager) createDefaultConfig() error {
	defaultConfig := Config{
		Upstream:                 []UpstreamConfig{},
		CurrentUpstream:          0,
		ResponsesUpstream:        []UpstreamConfig{},
		CurrentResponsesUpstream: 0,
		GeminiUpstream:           []UpstreamConfig{},
		VectorsUpstream:          []UpstreamConfig{},
		FuzzyModeEnabled:         true, // 默认启用 Fuzzy 模式
		ThinkingCache: ThinkingCacheConfig{
			TTLHours: ThinkingCacheDefaultTTLHours,
		},
		AutopilotRouting: DefaultAutopilotRoutingConfig(),
		// StripBillingHeader 旧全局字段默认关闭；新语义已下沉到渠道级开关
	}

	if err := os.MkdirAll(filepath.Dir(cm.configFile), 0700); err != nil {
		return err
	}

	return cm.saveConfigLocked(defaultConfig)
}

// applyConfigDefaults 应用配置默认值
// rawJSON: 原始 JSON 数据，用于检测字段是否存在
// 返回: 是否有字段需要迁移（需要保存配置）
func (cm *ConfigManager) applyConfigDefaults(rawJSON []byte) bool {
	needSave := false

	// FuzzyModeEnabled 默认值处理：
	// 由于 bool 零值是 false，无法区分"用户设为 false"和"字段不存在"
	// 通过检查原始 JSON 是否包含该字段来判断
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &rawMap); err == nil {
		if _, exists := rawMap["fuzzyModeEnabled"]; !exists {
			// 字段不存在，设为默认值 true
			cm.config.FuzzyModeEnabled = true
			needSave = true
			log.Printf("[Config-Migration] FuzzyModeEnabled 字段不存在，设为默认值 true")
		}
		if _, exists := rawMap["thinkingCache"]; !exists {
			cm.config.ThinkingCache.TTLHours = ThinkingCacheDefaultTTLHours
			needSave = true
			log.Printf("[Config-Migration] thinkingCache 字段不存在，ttlHours 设为默认值 %d", ThinkingCacheDefaultTTLHours)
		} else {
			normalized := NormalizeThinkingCacheTTLHours(cm.config.ThinkingCache.TTLHours)
			if cm.config.ThinkingCache.TTLHours != normalized {
				cm.config.ThinkingCache.TTLHours = normalized
				needSave = true
				log.Printf("[Config-Migration] thinkingCache.ttlHours 已归一化为 %d", normalized)
			}
		}

		// 将旧全局 stripBillingHeader 迁移到已有 messages 渠道级字段
		// 仅当旧全局字段显式存在、且渠道级字段未显式设置时才迁移，避免覆盖用户显式配置
		if _, exists := rawMap["stripBillingHeader"]; exists {
			migrated := cm.migrateStripBillingHeaderToChannels(rawMap)
			if cm.config.StripBillingHeader {
				cm.config.StripBillingHeader = false
				needSave = true
				log.Printf("[Config-Migration] 旧全局 stripBillingHeader 开关已清理，后续仅使用渠道级配置")
			}
			needSave = migrated || needSave
		}
	}

	return needSave
}

// migrateDisabledKeyRecoveryTimes 用错误消息中的上游重置时间升级旧禁用记录。
// 仅接受未来时间，避免已过期文案延长禁用期。
func (cm *ConfigManager) migrateDisabledKeyRecoveryTimes(now time.Time) bool {
	channelGroups := []struct {
		apiType   string
		upstreams *[]UpstreamConfig
	}{
		{apiType: "Messages", upstreams: &cm.config.Upstream},
		{apiType: "Responses", upstreams: &cm.config.ResponsesUpstream},
		{apiType: "Chat", upstreams: &cm.config.ChatUpstream},
		{apiType: "Gemini", upstreams: &cm.config.GeminiUpstream},
		{apiType: "Images", upstreams: &cm.config.ImagesUpstream},
		{apiType: "Vectors", upstreams: &cm.config.VectorsUpstream},
	}

	changed := false
	for _, group := range channelGroups {
		for channelIndex := range *group.upstreams {
			upstream := &(*group.upstreams)[channelIndex]
			for disabledIndex := range upstream.DisabledAPIKeys {
				disabled := &upstream.DisabledAPIKeys[disabledIndex]
				if !IsAutoRecoverableDisabledReason(disabled.Reason) {
					continue
				}
				recordChanged := false
				if disabled.Reason == "insufficient_balance" && strings.Contains(strings.ToLower(disabled.Message), "usage quota") {
					disabled.Reason = "insufficient_quota"
					recordChanged = true
				}
				recoverAt := utils.ExtractQuotaRecoverAt(disabled.Message)
				parsed, err := time.Parse(time.RFC3339, recoverAt)
				if err == nil && parsed.After(now) && disabled.RecoverAt != recoverAt {
					disabled.RecoverAt = recoverAt
					recordChanged = true
				}
				if !recordChanged {
					continue
				}
				changed = true
				log.Printf("[Config-Migration] %s[%d] Key %s 的额度禁用记录已升级 (reason=%s, recoverAt=%s)",
					group.apiType, channelIndex, utils.MaskAPIKey(disabled.Key), disabled.Reason, disabled.RecoverAt)
			}
		}
	}
	return changed
}

// applyAutopilotDefaults 处理 AutopilotRouting 配置的版本升级、默认值与校验。
// 返回 true 表示配置被修改（需要保存）。
func (cm *ConfigManager) applyAutopilotDefaults(rawJSON []byte) bool {
	needSave := false

	// 缺失整个配置块时直接使用当前默认结构；已有旧配置则以当前默认值为基线，
	// 再精确覆盖旧 JSON 中显式存在的字段，保留 false、0、空数组和空 map。
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &rawMap); err == nil {
		rawAutopilot, exists := rawMap["autopilot"]
		if !exists || string(bytes.TrimSpace(rawAutopilot)) == "null" {
			cm.config.AutopilotRouting = DefaultAutopilotRoutingConfig()
			needSave = true
			log.Printf("[Config-Migration] autopilot 配置块不存在，使用默认值")
		} else {
			var metadata struct {
				SchemaVersion int `json:"schemaVersion"`
			}
			if err := json.Unmarshal(rawAutopilot, &metadata); err == nil &&
				metadata.SchemaVersion < currentAutopilotConfigSchemaVersion {
				upgraded := DefaultAutopilotRoutingConfig()
				if err := overlayJSONStruct(&upgraded, rawAutopilot); err != nil {
					cm.config.AutopilotRouting = DefaultAutopilotRoutingConfig()
					needSave = true
					log.Printf("[Config-Migration] 警告: autopilot 旧配置升级失败，已回退到默认值: %v", err)
				} else {
					upgraded.SchemaVersion = currentAutopilotConfigSchemaVersion
					cm.config.AutopilotRouting = upgraded
					needSave = true
					log.Printf("[Config-Migration] autopilot 配置已升级到 schemaVersion=%d", currentAutopilotConfigSchemaVersion)
				}
			}
		}
	}

	// 校验与归一化的结果也必须持久化，保证一次升级后配置文件与运行态一致。
	beforeValidation, beforeErr := json.Marshal(cm.config.AutopilotRouting)
	cm.config.AutopilotRouting.Validate()
	afterValidation, afterErr := json.Marshal(cm.config.AutopilotRouting)
	if beforeErr != nil || afterErr != nil || !bytes.Equal(beforeValidation, afterValidation) {
		needSave = true
		log.Printf("[Config-Migration] autopilot 配置已归一化")
	}

	return needSave
}

// overlayJSONStruct 将 JSON 中显式存在的字段覆盖到已填充默认值的结构体。
// struct 字段递归覆盖；map/slice/标量整体替换，从而保留显式空值语义。
func overlayJSONStruct(dst any, rawJSON []byte) error {
	value := reflect.ValueOf(dst)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("overlayJSONStruct 目标必须是非空结构体指针")
	}
	return overlayJSONStructValue(value.Elem(), rawJSON)
}

func overlayJSONStructValue(dst reflect.Value, rawJSON []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &fields); err != nil {
		return err
	}

	typ := dst.Type()
	for i := 0; i < typ.NumField(); i++ {
		structField := typ.Field(i)
		field := dst.Field(i)
		if !field.CanSet() {
			continue
		}

		jsonName := strings.Split(structField.Tag.Get("json"), ",")[0]
		if jsonName == "" {
			jsonName = structField.Name
		}
		if jsonName == "-" {
			continue
		}

		rawField, exists := fields[jsonName]
		if !exists {
			continue
		}

		trimmed := bytes.TrimSpace(rawField)
		if len(trimmed) > 0 && trimmed[0] == '{' {
			switch {
			case field.Kind() == reflect.Struct:
				if err := overlayJSONStructValue(field, rawField); err != nil {
					return fmt.Errorf("字段 %s: %w", jsonName, err)
				}
				continue
			case field.Kind() == reflect.Pointer && field.Type().Elem().Kind() == reflect.Struct:
				nested := reflect.New(field.Type().Elem())
				if !field.IsNil() {
					nested.Elem().Set(field.Elem())
				}
				if err := overlayJSONStructValue(nested.Elem(), rawField); err != nil {
					return fmt.Errorf("字段 %s: %w", jsonName, err)
				}
				field.Set(nested)
				continue
			}
		}

		replacement := reflect.New(field.Type())
		if err := json.Unmarshal(rawField, replacement.Interface()); err != nil {
			return fmt.Errorf("字段 %s: %w", jsonName, err)
		}
		field.Set(replacement.Elem())
	}
	return nil
}

// migrateStripBillingHeaderToChannels 将旧全局 StripBillingHeader 迁移到 messages 渠道级字段。
// 仅当渠道级字段未显式设置时才复制，避免覆盖用户显式配置。
func (cm *ConfigManager) migrateStripBillingHeaderToChannels(rawMap map[string]json.RawMessage) bool {
	updated := false
	apply := func(raw json.RawMessage, channels *[]UpstreamConfig, channelName string) {
		var rawChannels []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rawChannels); err != nil {
			return
		}
		for i := range *channels {
			if i >= len(rawChannels) {
				continue
			}
			if (*channels)[i].StripBillingHeader != nil {
				// 已显式设置，不覆盖
				continue
			}
			rawChannel := rawChannels[i]
			if _, exists := rawChannel["stripBillingHeader"]; exists {
				// JSON 中已存在渠道级字段，不迁移
				continue
			}
			v := cm.config.StripBillingHeader
			(*channels)[i].StripBillingHeader = &v
			updated = true
			log.Printf("[Config-Migration] %s 渠道 [%d] %s StripBillingHeader 已从全局迁移为 %v", channelName, i, (*channels)[i].Name, v)
		}
	}
	if raw, ok := rawMap["upstream"]; ok {
		apply(raw, &cm.config.Upstream, "Messages")
	}
	// 仅迁移 messages 渠道，其他渠道类型不涉及该功能
	return updated
}

func (cm *ConfigManager) applyCodexToolCompatMigration(rawJSON []byte) bool {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &rawMap); err != nil {
		return false
	}
	updated := false
	apply := func(raw json.RawMessage, channels *[]UpstreamConfig, channelName string) {
		var rawChannels []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rawChannels); err != nil {
			return
		}
		for i := range *channels {
			if i >= len(rawChannels) {
				continue
			}
			rawChannel := rawChannels[i]
			if (*channels)[i].CodexToolCompat != nil {
				continue
			}
			if rawCodexToolsCompat, ok := rawChannel["codexToolsCompat"]; ok {
				var v bool
				if err := json.Unmarshal(rawCodexToolsCompat, &v); err == nil {
					(*channels)[i].CodexToolCompat = &v
					updated = true
					log.Printf("[Config-Migration] %s 渠道 [%d] %s codexToolsCompat 已迁移为 codexToolCompat", channelName, i, (*channels)[i].Name)
				}
				continue
			}
			if rawStrip, ok := rawChannel["stripCodexClientTools"]; ok {
				var v bool
				if err := json.Unmarshal(rawStrip, &v); err == nil && v {
					(*channels)[i].CodexToolCompat = &v
					updated = true
					log.Printf("[Config-Migration] %s 渠道 [%d] %s stripCodexClientTools 已迁移为 codexToolCompat", channelName, i, (*channels)[i].Name)
				}
			}
		}
	}
	if raw, ok := rawMap["upstream"]; ok {
		apply(raw, &cm.config.Upstream, "Messages")
	}
	if raw, ok := rawMap["responsesUpstream"]; ok {
		apply(raw, &cm.config.ResponsesUpstream, "Responses")
	}
	if raw, ok := rawMap["geminiUpstream"]; ok {
		apply(raw, &cm.config.GeminiUpstream, "Gemini")
	}
	if raw, ok := rawMap["chatUpstream"]; ok {
		apply(raw, &cm.config.ChatUpstream, "Chat")
	}
	if raw, ok := rawMap["imagesUpstream"]; ok {
		apply(raw, &cm.config.ImagesUpstream, "Images")
	}
	if raw, ok := rawMap["vectorsUpstream"]; ok {
		apply(raw, &cm.config.VectorsUpstream, "Vectors")
	}
	return updated
}

// migrateFableModelMapping 自动为现有渠道补齐 fable 模型映射。
// 若渠道 modelMapping 中存在 "opus" 映射但缺少 "fable"，则将 "fable" 指向同一目标。
// 确保已有 opus 转发配置的渠道在升级后无需手动添加 fable 条目。
func (cm *ConfigManager) migrateFableModelMapping() bool {
	updated := false
	apply := func(channels []UpstreamConfig, channelName string) {
		for i := range channels {
			mm := channels[i].ModelMapping
			if mm == nil {
				continue
			}
			opusTarget, hasOpus := mm["opus"]
			_, hasFable := mm["fable"]
			if hasOpus && !hasFable {
				mm["fable"] = opusTarget
				updated = true
				log.Printf("[Config-Migration] %s 渠道 [%d] %s modelMapping 已自动补齐 fable -> %s（与 opus 一致）", channelName, i, channels[i].Name, opusTarget)
			}
		}
	}
	apply(cm.config.Upstream, "Messages")
	apply(cm.config.ResponsesUpstream, "Responses")
	apply(cm.config.GeminiUpstream, "Gemini")
	apply(cm.config.ChatUpstream, "Chat")
	apply(cm.config.ImagesUpstream, "Images")
	return updated
}

// migrateDeprecatedGrokModelMapping 清除历史遗留的 grok 精确模型映射
// （grok-4.1 -> grok-4.1-thinking，grok-4.2 -> grok-4.20-beta），
// 保证老配置文件里的渠道即使用户从不编辑，也会在下次启动时被清理。
func (cm *ConfigManager) migrateDeprecatedGrokModelMapping() bool {
	updated := false
	apply := func(channels []UpstreamConfig, channelName string) {
		for i := range channels {
			cleaned, changed := sanitizeDeprecatedGrokModelMapping(channels[i].ModelMapping)
			if changed {
				channels[i].ModelMapping = cleaned
				updated = true
				log.Printf("[Config-Migration] %s 渠道 [%d] %s 已清除过时的 grok modelMapping", channelName, i, channels[i].Name)
			}
		}
	}
	apply(cm.config.Upstream, "Messages")
	apply(cm.config.ResponsesUpstream, "Responses")
	apply(cm.config.GeminiUpstream, "Gemini")
	apply(cm.config.ChatUpstream, "Chat")
	apply(cm.config.ImagesUpstream, "Images")
	apply(cm.config.VectorsUpstream, "Vectors")
	return updated
}

// migrateFableReasoningMapping 自动为现有渠道补齐 fable 推理强度映射。
// 若渠道 reasoningMapping 中存在 "opus" 映射但缺少 "fable"，则将 "fable" 指向同一 effort。
// 确保已有 opus 思考强度配置的渠道在升级后自动继承到 fable。
func (cm *ConfigManager) migrateFableReasoningMapping() bool {
	updated := false
	apply := func(channels []UpstreamConfig, channelName string) {
		for i := range channels {
			rm := channels[i].ReasoningMapping
			if rm == nil {
				continue
			}
			opusEffort, hasOpus := rm["opus"]
			_, hasFable := rm["fable"]
			if hasOpus && !hasFable {
				rm["fable"] = opusEffort
				updated = true
				log.Printf("[Config-Migration] %s 渠道 [%d] %s reasoningMapping 已自动补齐 fable -> %s（与 opus 一致）", channelName, i, channels[i].Name, opusEffort)
			}
		}
	}
	apply(cm.config.Upstream, "Messages")
	apply(cm.config.ResponsesUpstream, "Responses")
	apply(cm.config.GeminiUpstream, "Gemini")
	apply(cm.config.ChatUpstream, "Chat")
	apply(cm.config.ImagesUpstream, "Images")
	return updated
}

// generateChannelUID 生成渠道稳定身份标识。
// 格式为 "ch_" + 12 位十六进制字符（6 字节随机数），提供 2^48 的碰撞空间。
func generateChannelUID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 在所有受支持的平台上不会失败；此处仅做防御性回退
		log.Printf("[Config-ChannelUID] 警告: crypto/rand 读取失败: %v，使用时间戳回退", err)
		return fmt.Sprintf("ch_%012x", time.Now().UnixNano())
	}
	return "ch_" + hex.EncodeToString(b)
}

// GenerateChannelUID 生成渠道稳定身份 ID（公开版，供 autopilot 等包在创建渠道时预分配）。
func GenerateChannelUID() string {
	return generateChannelUID()
}

// GenerateAccountUID 生成账号稳定身份 ID。
func GenerateAccountUID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		log.Printf("[Config-AccountUID] 警告: crypto/rand 读取失败: %v，使用时间戳回退", err)
		return fmt.Sprintf("acct_%012x", time.Now().UnixNano())
	}
	return "acct_" + hex.EncodeToString(b)
}

// GenerateCredentialUID 返回账号内稳定且不暴露明文 Key 的凭证 ID。
func GenerateCredentialUID(accountUID, apiKey string) string {
	sum := sha256.Sum256([]byte(accountUID + "\x00" + strings.TrimSpace(apiKey)))
	return "cred_" + hex.EncodeToString(sum[:8])
}

// ensureChannelUIDs 为所有缺失 ChannelUID 的渠道补齐稳定身份标识。
// 已有 ChannelUID 的渠道不会被修改，保证渠道重排、改名、改 baseURL 后身份不变。
// 覆盖全部六类渠道：Messages / Responses / Gemini / Chat / Images / Vectors。
// 返回 true 表示有新增 UID，需要持久化。
func (cm *ConfigManager) ensureChannelUIDs() bool {
	updated := false
	apply := func(channels []UpstreamConfig, channelKind string) {
		for i := range channels {
			if channels[i].ChannelUID == "" {
				channels[i].ChannelUID = generateChannelUID()
				updated = true
				log.Printf("[Config-ChannelUID] %s 渠道 [%d] %s 已分配 ChannelUID: %s", channelKind, i, channels[i].Name, channels[i].ChannelUID)
			}
		}
	}
	apply(cm.config.Upstream, "Messages")
	apply(cm.config.ResponsesUpstream, "Responses")
	apply(cm.config.GeminiUpstream, "Gemini")
	apply(cm.config.ChatUpstream, "Chat")
	apply(cm.config.ImagesUpstream, "Images")
	apply(cm.config.VectorsUpstream, "Vectors")
	return updated
}

// ensureAccountUIDs 为缺失账号身份的渠道补齐 accountUid。
// 旧 provider 自动托管渠道按 provider + 逻辑名称 + Key 集合恢复原有跨协议账号关系；
// 手动渠道和无法确认归属的渠道独立回填，避免错误合并。
func (cm *ConfigManager) ensureAccountUIDs() bool {
	updated := false
	managedGroups := make(map[string]string)
	apply := func(channels []UpstreamConfig, channelKind string) {
		for i := range channels {
			if channels[i].AccountUID != "" {
				continue
			}
			accountUID := ""
			if groupKey := legacyManagedAccountGroupKey(channels[i]); groupKey != "" {
				accountUID = managedGroups[groupKey]
				if accountUID == "" {
					accountUID = GenerateAccountUID()
					managedGroups[groupKey] = accountUID
				}
			}
			if accountUID == "" {
				accountUID = GenerateAccountUID()
			}
			channels[i].AccountUID = accountUID
			updated = true
			log.Printf("[Config-AccountUID] %s 渠道 [%d] %s 已分配 AccountUID: %s", channelKind, i, channels[i].Name, channels[i].AccountUID)
		}
	}
	apply(cm.config.Upstream, "Messages")
	apply(cm.config.ResponsesUpstream, "Responses")
	apply(cm.config.GeminiUpstream, "Gemini")
	apply(cm.config.ChatUpstream, "Chat")
	apply(cm.config.ImagesUpstream, "Images")
	apply(cm.config.VectorsUpstream, "Vectors")
	return updated
}

func legacyManagedAccountGroupKey(channel UpstreamConfig) string {
	if !channel.AutoManaged || channel.ProviderID == "" || len(channel.APIKeys) == 0 {
		return ""
	}
	keys := append([]string(nil), channel.APIKeys...)
	for i := range keys {
		keys[i] = strings.TrimSpace(keys[i])
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\x00")))
	return channel.ProviderID + "\x00" + managedAccountName(channel.Name) + "\x00" + hex.EncodeToString(sum[:8])
}

func (cm *ConfigManager) ensureCredentialUIDs() bool {
	updated := false
	apply := func(channels []UpstreamConfig) {
		for i := range channels {
			channel := &channels[i]
			if !channel.AutoManaged || channel.AccountUID == "" {
				continue
			}
			channel.APIKeyConfigs = normalizeAPIKeyConfigs(channel.APIKeys, channel.APIKeyConfigs)
			for j := range channel.APIKeyConfigs {
				if channel.APIKeyConfigs[j].CredentialUID == "" {
					channel.APIKeyConfigs[j].CredentialUID = GenerateCredentialUID(channel.AccountUID, channel.APIKeyConfigs[j].Key)
					updated = true
				}
			}
		}
	}
	apply(cm.config.Upstream)
	apply(cm.config.ResponsesUpstream)
	apply(cm.config.GeminiUpstream)
	apply(cm.config.ChatUpstream)
	apply(cm.config.ImagesUpstream)
	apply(cm.config.VectorsUpstream)
	return updated
}

// ensureOriginBackfill 为缺失 OriginType/OriginTier 的渠道补默认值 "unknown"。
// 设计 §12.2 P1.5：旧配置 backfill 不改变原调度——只补标签，不做任何基于
// URL/名称的猜测推断，避免把未知来源误判为某个具体信任等级。
// 已有非空值的渠道不会被覆盖。覆盖全部六类渠道。返回 true 表示有字段被补齐，需要持久化。
func (cm *ConfigManager) ensureOriginBackfill() bool {
	updated := false
	apply := func(channels []UpstreamConfig, channelKind string) {
		for i := range channels {
			changed := false
			if channels[i].OriginType == "" {
				channels[i].OriginType = "unknown"
				changed = true
			}
			if channels[i].OriginTier == "" {
				channels[i].OriginTier = "unknown"
				changed = true
			}
			if changed {
				updated = true
				log.Printf("[Config-OriginBackfill] %s 渠道 [%d] %s 已补齐 originType/originTier 为 unknown", channelKind, i, channels[i].Name)
			}
		}
	}
	apply(cm.config.Upstream, "Messages")
	apply(cm.config.ResponsesUpstream, "Responses")
	apply(cm.config.GeminiUpstream, "Gemini")
	apply(cm.config.ChatUpstream, "Chat")
	apply(cm.config.ImagesUpstream, "Images")
	apply(cm.config.VectorsUpstream, "Vectors")
	return updated
}

func (cm *ConfigManager) applyServiceTypeDefaults() bool {
	updated := false

	apply := func(channels []UpstreamConfig, fallback, channelName string) {
		for i := range channels {
			normalized := normalizeUpstreamServiceType(channels[i].ServiceType, fallback)
			if channels[i].ServiceType != normalized {
				channels[i].ServiceType = normalized
				updated = true
				log.Printf("[Config-Migration] %s 渠道 [%d] %s serviceType 为空，已回填为 %s", channelName, i, channels[i].Name, normalized)
			}

			if channels[i].ServiceType == "copilot" && strings.TrimSpace(channels[i].BaseURL) == "" && len(channels[i].BaseURLs) == 0 {
				applyDefaultBaseURL(&channels[i])
				updated = true
				log.Printf("[Config-Migration] %s 渠道 [%d] %s Copilot BaseURL 为空，已回填为 %s", channelName, i, channels[i].Name, channels[i].BaseURL)
			}
		}
	}

	apply(cm.config.Upstream, "claude", "Messages")
	apply(cm.config.ResponsesUpstream, "responses", "Responses")
	apply(cm.config.GeminiUpstream, "gemini", "Gemini")
	apply(cm.config.ChatUpstream, "openai", "Chat")
	for i := range cm.config.VectorsUpstream {
		normalized, err := normalizeVectorsServiceType(cm.config.VectorsUpstream[i].ServiceType)
		if err != nil {
			cm.config.VectorsUpstream[i].ServiceType = "openai"
			updated = true
			log.Printf("[Config-Migration] Vectors 渠道 [%d] %s serviceType=%s 不受支持，已强制改为 openai", i, cm.config.VectorsUpstream[i].Name, normalizeUpstreamServiceType(cm.config.VectorsUpstream[i].ServiceType, "openai"))
			continue
		}
		if cm.config.VectorsUpstream[i].ServiceType != normalized {
			cm.config.VectorsUpstream[i].ServiceType = normalized
			updated = true
			log.Printf("[Config-Migration] Vectors 渠道 [%d] %s serviceType 为空，已回填为 %s", i, cm.config.VectorsUpstream[i].Name, normalized)
		}
	}
	for i := range cm.config.ImagesUpstream {
		normalized, err := normalizeImagesServiceType(cm.config.ImagesUpstream[i].ServiceType)
		if err != nil {
			cm.config.ImagesUpstream[i].ServiceType = "openai"
			updated = true
			log.Printf("[Config-Migration] Images 渠道 [%d] %s serviceType=%s 不受支持，已强制改为 openai", i, cm.config.ImagesUpstream[i].Name, normalizeUpstreamServiceType(cm.config.ImagesUpstream[i].ServiceType, "openai"))
			continue
		}
		if cm.config.ImagesUpstream[i].ServiceType != normalized {
			cm.config.ImagesUpstream[i].ServiceType = normalized
			updated = true
			log.Printf("[Config-Migration] Images 渠道 [%d] %s serviceType 为空，已回填为 %s", i, cm.config.ImagesUpstream[i].Name, normalized)
		}
	}

	return updated
}

// migrateOldFormat 迁移旧格式配置，返回是否有迁移
func (cm *ConfigManager) migrateOldFormat() bool {
	needMigration := cm.migrateUpstreams(cm.config.Upstream, cm.config.CurrentUpstream, "Messages")

	// 迁移 Messages 渠道

	// 迁移 Responses 渠道
	if cm.migrateUpstreams(cm.config.ResponsesUpstream, cm.config.CurrentResponsesUpstream, "Responses") {
		needMigration = true
	}

	if needMigration {
		log.Printf("[Config-Migration] 检测到旧格式配置，正在迁移到新格式...")
	}

	return needMigration
}

// migrateUpstreams 迁移单个渠道列表
func (cm *ConfigManager) migrateUpstreams(upstreams []UpstreamConfig, currentIdx int, name string) bool {
	if len(upstreams) == 0 {
		return false
	}

	// 检查是否已有 status 字段
	for _, up := range upstreams {
		if up.Status != "" {
			return false
		}
	}

	// 需要迁移
	if currentIdx < 0 || currentIdx >= len(upstreams) {
		currentIdx = 0
	}

	for i := range upstreams {
		if i == currentIdx {
			upstreams[i].Status = "active"
		} else {
			upstreams[i].Status = "disabled"
		}
	}

	log.Printf("[Config-Migration] %s 渠道 [%d] %s 已设置为 active，其他 %d 个渠道已设为 disabled",
		name, currentIdx, upstreams[currentIdx].Name, len(upstreams)-1)

	return true
}

// validateChannelKeys 自检渠道密钥配置
// 没有配置 API key 的渠道，即使状态为 active 也应暂停
// 返回 true 表示有配置被修改，需要保存
func (cm *ConfigManager) validateChannelKeys() bool {
	modified := false

	// 检查 Messages 渠道
	for i := range cm.config.Upstream {
		upstream := &cm.config.Upstream[i]
		status := upstream.Status
		if status == "" {
			status = "active"
		}

		// 如果是 active 状态但没有配置 key，自动设为 suspended
		if status == "active" && len(upstream.APIKeys) == 0 {
			upstream.Status = "suspended"
			modified = true
			log.Printf("[Config-Validate] 警告: Messages 渠道 [%d] %s 没有配置 API key，已自动暂停", i, upstream.Name)
		}
	}

	// 检查 Responses 渠道
	for i := range cm.config.ResponsesUpstream {
		upstream := &cm.config.ResponsesUpstream[i]
		status := upstream.Status
		if status == "" {
			status = "active"
		}

		// 如果是 active 状态但没有配置 key，自动设为 suspended
		if status == "active" && len(upstream.APIKeys) == 0 {
			upstream.Status = "suspended"
			modified = true
			log.Printf("[Config-Validate] 警告: Responses 渠道 [%d] %s 没有配置 API key，已自动暂停", i, upstream.Name)
		}
	}

	// 检查 Chat 渠道
	for i := range cm.config.ChatUpstream {
		upstream := &cm.config.ChatUpstream[i]
		status := upstream.Status
		if status == "" {
			status = "active"
		}

		// 如果是 active 状态但没有配置 key，自动设为 suspended
		if status == "active" && len(upstream.APIKeys) == 0 {
			upstream.Status = "suspended"
			modified = true
			log.Printf("[Config-Validate] 警告: Chat 渠道 [%d] %s 没有配置 API key，已自动暂停", i, upstream.Name)
		}
	}

	// 检查 Gemini 渠道
	for i := range cm.config.GeminiUpstream {
		upstream := &cm.config.GeminiUpstream[i]
		status := upstream.Status
		if status == "" {
			status = "active"
		}

		// 如果是 active 状态但没有配置 key，自动设为 suspended
		if status == "active" && len(upstream.APIKeys) == 0 {
			upstream.Status = "suspended"
			modified = true
			log.Printf("[Config-Validate] 警告: Gemini 渠道 [%d] %s 没有配置 API key，已自动暂停", i, upstream.Name)
		}
	}

	// 检查 Images 渠道
	for i := range cm.config.ImagesUpstream {
		upstream := &cm.config.ImagesUpstream[i]
		status := upstream.Status
		if status == "" {
			status = "active"
		}

		// 如果是 active 状态但没有配置 key，自动设为 suspended
		if status == "active" && len(upstream.APIKeys) == 0 {
			upstream.Status = "suspended"
			modified = true
			log.Printf("[Config-Validate] 警告: Images 渠道 [%d] %s 没有配置 API key，已自动暂停", i, upstream.Name)
		}
	}

	// 检查 Vectors 渠道
	for i := range cm.config.VectorsUpstream {
		upstream := &cm.config.VectorsUpstream[i]
		status := upstream.Status
		if status == "" {
			status = "active"
		}

		if status == "active" && len(upstream.APIKeys) == 0 {
			upstream.Status = "suspended"
			modified = true
			log.Printf("[Config-Validate] 警告: Vectors 渠道 [%d] %s 没有配置 API key，已自动暂停", i, upstream.Name)
		}
	}

	return modified
}

// saveConfigLocked 保存配置（已加锁）
func (cm *ConfigManager) saveConfigLocked(config Config) error {
	// 备份当前配置
	cm.backupConfig()

	// 清理已废弃字段，确保不会被序列化到 JSON
	config.CurrentUpstream = 0
	config.CurrentResponsesUpstream = 0

	config.syncManagedAccountsFromChannels()
	persisted := config.deepCopy()
	persisted.stripManagedChannelSecrets()
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(cm.configFile, data, 0600); err != nil { // 仅所有者可读写，保护敏感配置
		return err
	}
	cm.config = config
	return nil
}

// SaveConfig 保存配置
func (cm *ConfigManager) SaveConfig() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.saveConfigLocked(cm.config)
}

// backupConfig 备份配置
func (cm *ConfigManager) backupConfig() {
	if _, err := os.Stat(cm.configFile); os.IsNotExist(err) {
		return
	}

	backupDir := cm.backupDir
	if backupDir == "" {
		backupDir = filepath.Join(filepath.Dir(cm.configFile), "backups")
	}
	if err := os.MkdirAll(backupDir, 0700); err != nil { // 仅所有者可访问
		log.Printf("[Config-Backup] 警告: 创建备份目录失败: %v", err)
		return
	}

	// 读取当前配置
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		log.Printf("[Config-Backup] 警告: 读取配置文件失败: %v", err)
		return
	}

	// 创建备份文件
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	backupFile := filepath.Join(backupDir, fmt.Sprintf("config-%s.json", timestamp))
	if err := os.WriteFile(backupFile, data, 0600); err != nil { // 仅所有者可读写
		log.Printf("[Config-Backup] 警告: 写入备份文件失败: %v", err)
		return
	}

	// 清理旧备份
	cm.cleanupOldBackups(backupDir)
}

// cleanupOldBackups 清理旧备份
func (cm *ConfigManager) cleanupOldBackups(backupDir string) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}

	if len(entries) <= maxBackups {
		return
	}

	// 删除最旧的备份
	for i := 0; i < len(entries)-maxBackups; i++ {
		_ = os.Remove(filepath.Join(backupDir, entries[i].Name()))
	}
}

// startWatcher 启动配置目录监听。
func (cm *ConfigManager) startWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	configDir := filepath.Dir(cm.configFile)
	configPath := filepath.Clean(cm.configFile)

	if err := watcher.Add(configDir); err != nil {
		_ = watcher.Close()
		return err
	}

	cm.watcher = watcher

	cm.backgroundWG.Add(1)
	go func() {
		defer cm.backgroundWG.Done()
		for {
			select {
			case <-cm.stopChan:
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Clean(event.Name) != configPath {
					continue
				}
				// 覆盖三种文件变更事件：直接写、原子保存（vim/VSCode 走 RENAME+CREATE）。
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0 {
					// 仅发送信号，由独立 goroutine 负责防抖与重载，避免 watcher 回调内做 IO。
					select {
					case cm.reloadCh <- struct{}{}:
					default:
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("[Config-Watcher] 警告: 文件监听错误: %v", err)
			}
		}
	}()

	cm.backgroundWG.Add(1)
	go func() {
		defer cm.backgroundWG.Done()
		// debounce: 收到第一个信号后启动 timer；后续信号 reset timer，
		// 直至连续 configReloadDebounce 内无新信号才触发实际 loadConfig。
		// 这样可以合并编辑器原子保存、CI 多次写入等多事件场景。
		var timer *time.Timer
		var timerC <-chan time.Time
		for {
			select {
			case <-cm.stopChan:
				if timer != nil {
					timer.Stop()
				}
				return
			case <-cm.reloadCh:
				if timer == nil {
					timer = time.NewTimer(configReloadDebounce)
					timerC = timer.C
				} else {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(configReloadDebounce)
				}
			case <-timerC:
				timer = nil
				timerC = nil
				if err := cm.loadConfig(); err != nil {
					log.Printf("[Config-Watcher] 警告: 配置重载失败: %v", err)
				} else {
					log.Printf("[Config-Watcher] 配置已重载")
				}
			}
		}
	}()

	return nil
}

// CloseWatcher 关闭配置文件监听并等待后台 goroutine 退出。
// 调用后不能再调用 Close 中的 stopChan close，所以同时标记 stopChan 已关闭。
func (cm *ConfigManager) CloseWatcher() {
	if cm == nil {
		return
	}
	cm.closeOnce.Do(func() {
		if cm.stopChan != nil {
			close(cm.stopChan)
		}
		if cm.watcher != nil {
			_ = cm.watcher.Close()
		}
		cm.backgroundWG.Wait()
	})
}

// Close 关闭 ConfigManager 并释放资源（幂等，可安全多次调用）
func (cm *ConfigManager) Close() error {
	var closeErr error
	cm.closeOnce.Do(func() {
		// 通知所有 goroutine 停止
		if cm.stopChan != nil {
			close(cm.stopChan)
		}

		// 关闭文件监听器
		if cm.watcher != nil {
			closeErr = cm.watcher.Close()
		}

		cm.backgroundWG.Wait()
	})
	return closeErr
}

// deepCopy 创建配置的深拷贝
func (c Config) deepCopy() Config {
	data, err := json.Marshal(c)
	if err != nil {
		return c
	}
	var copy Config
	if err := json.Unmarshal(data, &copy); err != nil {
		return c
	}
	return copy
}

// hasConfigChanged 检测配置是否发生了实质性变化
func (cm *ConfigManager) hasConfigChanged(old, new Config) bool {
	// 清理废弃字段以确保比较准确
	old.CurrentUpstream = 0
	old.CurrentResponsesUpstream = 0
	new.CurrentUpstream = 0
	new.CurrentResponsesUpstream = 0

	oldData, err := json.Marshal(old)
	if err != nil {
		return true
	}
	newData, err := json.Marshal(new)
	if err != nil {
		return true
	}
	return !bytes.Equal(oldData, newData)
}
