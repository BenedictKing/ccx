package siteaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
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
	Group       string
	KeyName     string
	ChannelName string
	Skipped     string // 非空表示该分组被跳过及原因
	Reused      bool   // 是否复用远端已有同名 Key
}

// ProvisionResult 一次协议接入的完整结果。
type ProvisionResult struct {
	AccountUID string
	Protocol   string
	Groups     []ProvisionGroupResult
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

	result := &ProvisionResult{AccountUID: req.AccountUID, Protocol: strings.ToLower(req.Protocol)}
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
		KeyName:     defaultNewApiProvisionKeyNameForGroup(group.Name),
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
	if len(req.Models) > 0 {
		available := make(map[string]bool, len(models))
		for _, m := range models {
			available[m] = true
		}
		matched := false
		for _, target := range req.Models {
			if available[target] {
				matched = true
				break
			}
		}
		if !matched {
			groupResult.Skipped = "分组不含任何目标模型"
			return groupResult
		}
	}

	tokenID, keyPlaintext, reused, finalName, err := adapter.ProvisionKey(ctx, account.BaseURL, accessToken, account.UserID, account.AuthTokenMode, NewApiProvisionOptions{
		Name:   groupResult.KeyName,
		Group:  group.Name,
		Models: req.Models,
	})
	if err != nil {
		groupResult.Skipped = fmt.Sprintf("建 Key 失败: %v", err)
		return groupResult
	}
	groupResult.KeyName = finalName
	groupResult.Reused = reused

	if err := upsertProvisionedChannel(cfgManager, apiType, serviceType, account.BaseURL, groupResult.ChannelName, keyPlaintext, req.ModelMapping); err != nil {
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

// upsertProvisionedChannel 按稳定渠道名幂等写入：已存在则补充 Key/更新映射，
// 不存在则新建该协议的独立渠道。不触碰其他协议的配置数组。
func upsertProvisionedChannel(cfgManager *config.ConfigManager, apiType, serviceType, baseURL, channelName, apiKey string, modelMapping map[string]string) error {
	channels := protocolChannels(cfgManager, apiType)
	for i := range channels {
		if channels[i].Name != channelName {
			continue
		}
		keys := channels[i].APIKeys
		found := false
		for _, k := range keys {
			if k == apiKey {
				found = true
				break
			}
		}
		if !found {
			keys = append(keys, apiKey)
		}
		_, err := updateProtocolChannel(cfgManager, apiType, i, config.UpstreamUpdate{APIKeys: keys, ModelMapping: modelMapping})
		return err
	}

	channel := config.UpstreamConfig{
		Name:         channelName,
		BaseURL:      baseURL,
		APIKeys:      []string{apiKey},
		ServiceType:  serviceType,
		Status:       "active",
		ModelMapping: modelMapping,
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
