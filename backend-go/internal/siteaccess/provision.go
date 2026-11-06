package siteaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
)

// ProvisionRequest 协议级接入请求：每次只为选定协议建立渠道。
// 与 3.0 的「账号有任意模型即自动建 Key」不同：目标模型由用户显式指定，
// 分组中没有任何目标模型时跳过该分组，不创建可用渠道。
type ProvisionRequest struct {
	AccountUID    string
	Protocol      string   // messages / chat / responses / gemini
	Groups        []string // 显式选定分组；空=全部不超过倍率阈值的合格分组
	ExcludeGroups []string // 用户显式排除的分组
	MaxMultiplier *float64 // 分组倍率上限，默认 DefaultNewApiMaxGroupMultiplier
	Models        []string // 该协议的目标模型（作为远端 Key 的 model_limits）
	ModelMapping  map[string]string
}

// ProvisionGroupResult 单个分组的接入结果。
type ProvisionGroupResult struct {
	Group       string `json:"group"`
	KeyName     string `json:"keyName"`
	ChannelName string `json:"channelName"`
	Skipped     string `json:"skipped"` // 非空表示该分组被跳过及原因
	Reused      bool   `json:"reused"`  // 是否复用远端已有同名 Key
}

// ProvisionResult 一次协议接入的完整结果。
type ProvisionResult struct {
	AccountUID string                 `json:"accountUID"`
	Protocol   string                 `json:"protocol"`
	Groups     []ProvisionGroupResult `json:"groups"`
}

// ProvisionedChannelName 是 CCX 托管渠道的稳定命名约定，用于幂等查找与对账定位。
func ProvisionedChannelName(baseURL, protocol, group string) string {
	host := baseURL
	if u, err := url.Parse(strings.TrimSpace(baseURL)); err == nil && u.Host != "" {
		host = u.Host
	}
	return fmt.Sprintf("ccx: %s / %s / %s", host, protocol, group)
}

// keyFingerprint 是推理 Key 的不可逆指纹，用于对账时在不存明文的前提下匹配本地 Key。
func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func provisionedKeyName(protocol, group string, models []string) string {
	ordered := append([]string(nil), models...)
	sort.Strings(ordered)
	sum := sha256.Sum256([]byte(strings.Join(ordered, "\x00")))
	return fmt.Sprintf("%s-%s-%x", defaultNewApiProvisionKeyNameForGroup(group), protocol, sum[:4])
}

// provisionProtocolInfo 归一协议名到 2.9 配置层的 apiType 与默认 ServiceType。
func provisionProtocolInfo(protocol string) (apiType, serviceType string, err error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "messages":
		return "Messages", "claude", nil
	case "chat":
		return "Chat", "openai", nil
	case "responses":
		return "Responses", "openai", nil
	case "gemini":
		return "Gemini", "gemini", nil
	default:
		return "", "", fmt.Errorf("[SiteAccess-Provision] 不支持的协议: %q", protocol)
	}
}

// ProvisionProtocol 为指定协议执行分组接入：校验分组倍率与目标模型 →
// 逐组建或复用远端 Key → 写入该协议的独立渠道 → 记录 Key 元数据。
// 渠道创建失败时，本次新建的远端 Key 会被删除回收（复用的已有 Key 不动）。
func ProvisionProtocol(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, adapter *NewApiAdapter, req ProvisionRequest) (*ProvisionResult, error) {
	apiType, serviceType, err := provisionProtocolInfo(req.Protocol)
	if err != nil {
		return nil, err
	}
	selectedModels := make(map[string]bool, len(req.Models))
	for _, model := range req.Models {
		selectedModels[model] = true
	}
	for alias, target := range req.ModelMapping {
		if strings.TrimSpace(alias) == "" || !selectedModels[target] {
			return nil, fmt.Errorf("[SiteAccess-Provision] 模型映射 %q 的目标 %q 不在所选模型中", alias, target)
		}
	}
	account, found, err := store.Get(req.AccountUID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("[SiteAccess-Provision] 账号不存在: %s", req.AccountUID)
	}
	accessToken, err := account.plaintextAccessToken()
	if err != nil {
		return nil, err
	}
	if accessToken == "" {
		return nil, fmt.Errorf("[SiteAccess-Provision] 账号 %s 缺少管理令牌", req.AccountUID)
	}

	groupRatios, err := adapter.FetchGroups(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode)
	if err != nil {
		return nil, fmt.Errorf("[SiteAccess-Provision] 拉取分组失败: %w", err)
	}

	// 显式选定/排除分组 → 逐组校验倍率
	selected := make(map[string]float64)
	excluded := make(map[string]bool, len(req.ExcludeGroups))
	for _, g := range req.ExcludeGroups {
		excluded[strings.TrimSpace(g)] = true
	}
	if len(req.Groups) > 0 {
		for _, name := range req.Groups {
			name = strings.TrimSpace(name)
			ratio, ok := groupRatios[name]
			if !ok {
				return nil, fmt.Errorf("[SiteAccess-Provision] 分组 %q 不在账号可用分组中", name)
			}
			selected[name] = ratio
		}
	} else {
		for name, ratio := range groupRatios {
			if !excluded[name] {
				selected[name] = ratio
			}
		}
	}

	// 逐组解析：单分组模式复用守卫的单组校验；多分组走倍率阈值过滤。
	resolved := make([]newApiResolvedGroup, 0, len(selected))
	if len(req.Groups) > 0 {
		for name := range selected {
			group, err := resolveNewApiSingleProvisionGroup(selected, name, provisionMaxMultiplier(req.MaxMultiplier))
			if err != nil {
				return nil, fmt.Errorf("[SiteAccess-Provision] %w", err)
			}
			resolved = append(resolved, group)
		}
	} else {
		resolved, err = resolveNewApiProvisionGroups(selected, "", true, req.MaxMultiplier)
		if err != nil {
			return nil, fmt.Errorf("[SiteAccess-Provision] %w", err)
		}
	}

	result := &ProvisionResult{AccountUID: req.AccountUID, Protocol: strings.ToLower(req.Protocol), Groups: make([]ProvisionGroupResult, 0, len(resolved))}
	for _, group := range resolved {
		groupResult := aProvisionGroup(ctx, store, cfgManager, adapter, account, apiType, serviceType, req, group)
		result.Groups = append(result.Groups, groupResult)
	}
	return result, nil
}

func provisionMaxMultiplier(requested *float64) float64 {
	if requested == nil {
		return DefaultNewApiMaxGroupMultiplier
	}
	return *requested
}

// aProvisionGroup 接入单个分组；任何失败都记入 Skipped 而不是中断整批，
// 让用户看到每个分组的明确去向。
func aProvisionGroup(ctx context.Context, store *AccountStore, cfgManager *config.ConfigManager, adapter *NewApiAdapter, account SiteAccount, apiType, serviceType string, req ProvisionRequest, group newApiResolvedGroup) ProvisionGroupResult {
	protocol := strings.ToLower(req.Protocol)
	groupResult := ProvisionGroupResult{
		Group:       group.Name,
		ChannelName: ProvisionedChannelName(account.BaseURL, protocol, group.Name),
	}
	accessToken, err := account.plaintextAccessToken()
	if err != nil {
		groupResult.Skipped = err.Error()
		return groupResult
	}

	// 模型可用性按该协议逐组校验：0 模型分组跳过；指定了目标模型但分组
	// 不含任何目标模型时同样跳过（不为该分组建可用渠道）。
	models, err := adapter.FetchGroupModels(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode, group.Name)
	if err != nil {
		groupResult.Skipped = fmt.Sprintf("查询分组模型失败: %v", err)
		return groupResult
	}
	if len(models) == 0 {
		groupResult.Skipped = "分组无可用模型"
		return groupResult
	}
	groupModels := req.Models
	groupMapping := req.ModelMapping
	if len(req.Models) > 0 {
		available := make(map[string]bool, len(models))
		for _, m := range models {
			available[m] = true
		}
		groupModels = make([]string, 0, len(req.Models))
		for _, target := range req.Models {
			if available[target] {
				groupModels = append(groupModels, target)
			}
		}
		if len(groupModels) == 0 {
			groupResult.Skipped = "分组不含任何目标模型"
			return groupResult
		}
		groupMapping = make(map[string]string)
		for alias, target := range req.ModelMapping {
			if available[target] {
				groupMapping[alias] = target
			}
		}
	}
	groupResult.KeyName = provisionedKeyName(protocol, group.Name, groupModels)
	supportedModels := append([]string(nil), groupModels...)
	for alias := range groupMapping {
		supportedModels = append(supportedModels, alias)
	}
	sort.Strings(supportedModels)

	tokenID, keyPlaintext, reused, finalName, err := adapter.ProvisionKey(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode, NewApiProvisionOptions{
		Name:   groupResult.KeyName,
		Group:  group.Name,
		Models: groupModels,
	})
	if err != nil {
		groupResult.Skipped = fmt.Sprintf("建 Key 失败: %v", err)
		return groupResult
	}
	groupResult.KeyName = finalName
	groupResult.Reused = reused

	previousFingerprint := ""
	for _, meta := range account.ProvisionedKeys {
		if meta.Protocol == protocol && meta.Group == group.Name {
			previousFingerprint = meta.KeyFingerprint
			break
		}
	}
	if err := upsertProvisionedChannel(cfgManager, apiType, serviceType, account.BaseURL, groupResult.ChannelName, keyPlaintext, previousFingerprint, groupMapping, supportedModels); err != nil {
		if !reused {
			if deleteErr := adapter.DeleteToken(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode, tokenID); deleteErr != nil {
				groupResult.Skipped = fmt.Sprintf("写入渠道失败: %v；新建远端 Key 回收也失败: %v，请到站点后台手动删除 %s", err, deleteErr, finalName)
				return groupResult
			}
		}
		groupResult.Skipped = fmt.Sprintf("写入渠道失败: %v", err)
		return groupResult
	}

	recordProvisionedKey(store, account, protocol, group, tokenID, finalName, keyPlaintext)
	RecordConfigured(store, account.UID, protocol, group.Name)
	return groupResult
}

// upsertProvisionedChannel 按稳定渠道名幂等写入：已存在则替换旧托管 Key、更新模型范围与映射，
// 不存在则新建该协议的独立渠道。不触碰其他协议的配置数组。
func upsertProvisionedChannel(cfgManager *config.ConfigManager, apiType, serviceType, baseURL, channelName, apiKey, previousFingerprint string, modelMapping map[string]string, supportedModels []string) error {
	channels := protocolChannels(cfgManager, apiType)
	for i := range channels {
		if channels[i].Name != channelName {
			continue
		}
		keys := make([]string, 0, len(channels[i].APIKeys)+1)
		found := false
		for _, k := range channels[i].APIKeys {
			if previousFingerprint != "" && keyFingerprint(k) == previousFingerprint && k != apiKey {
				continue
			}
			if k == apiKey {
				found = true
			}
			keys = append(keys, k)
		}
		if !found {
			keys = append(keys, apiKey)
		}
		_, err := updateProtocolChannel(cfgManager, apiType, i, config.UpstreamUpdate{APIKeys: keys, ModelMapping: modelMapping, SupportedModels: supportedModels})
		return err
	}

	channel := config.UpstreamConfig{
		Name:            channelName,
		BaseURL:         baseURL,
		APIKeys:         []string{apiKey},
		ServiceType:     serviceType,
		Status:          "active",
		ModelMapping:    modelMapping,
		SupportedModels: supportedModels,
	}
	switch apiType {
	case "Messages":
		return cfgManager.AddUpstream(channel)
	case "Chat":
		return cfgManager.AddChatUpstream(channel)
	case "Responses":
		return cfgManager.AddResponsesUpstream(channel)
	case "Gemini":
		return cfgManager.AddGeminiUpstream(channel)
	default:
		return fmt.Errorf("不支持的 apiType: %s", apiType)
	}
}

// recordProvisionedKey 把远端 Key 元数据（含明文指纹，不含明文）记回账号。
// 记录失败只打日志级返回，不影响已完成的接入——对账缺失可在下次接入时补齐。
func recordProvisionedKey(store *AccountStore, account SiteAccount, protocol string, group newApiResolvedGroup, tokenID int, keyName, keyPlaintext string) {
	current, found, err := store.Get(account.UID)
	if err != nil || !found {
		return
	}
	account = current
	meta := ProvisionedKeyMeta{
		Name:           keyName,
		Group:          group.Name,
		TokenID:        tokenID,
		Protocol:       protocol,
		KeyFingerprint: keyFingerprint(keyPlaintext),
	}
	keys := account.ProvisionedKeys[:0]
	for _, k := range account.ProvisionedKeys {
		if k.Protocol == protocol && k.Group == group.Name {
			continue
		}
		keys = append(keys, k)
	}
	account.ProvisionedKeys = append(keys, meta)
	_, _ = store.Upsert(account)
}

func protocolChannels(cfgManager *config.ConfigManager, apiType string) []config.UpstreamConfig {
	cfg := cfgManager.GetConfig()
	switch apiType {
	case "Messages":
		return cfg.Upstream
	case "Chat":
		return cfg.ChatUpstream
	case "Responses":
		return cfg.ResponsesUpstream
	case "Gemini":
		return cfg.GeminiUpstream
	default:
		return nil
	}
}

func updateProtocolChannel(cfgManager *config.ConfigManager, apiType string, index int, updates config.UpstreamUpdate) (bool, error) {
	switch apiType {
	case "Messages":
		return cfgManager.UpdateUpstream(index, updates)
	case "Chat":
		return cfgManager.UpdateChatUpstream(index, updates)
	case "Responses":
		return cfgManager.UpdateResponsesUpstream(index, updates)
	case "Gemini":
		return cfgManager.UpdateGeminiUpstream(index, updates)
	default:
		return false, fmt.Errorf("不支持的 apiType: %s", apiType)
	}
}
