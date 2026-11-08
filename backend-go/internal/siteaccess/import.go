package siteaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// applyAgentDefaults 只生成客户端别名映射，实际目标模型始终由用户指定。
func applyAgentDefaults(req *ProvisionRequest) error {
	if req.MaxAttempts < 0 || req.MaxAttempts > 30 || req.MaxWaitSeconds < 0 || req.MaxWaitSeconds > 300 {
		return fmt.Errorf("重试次数须为 1–30，重试窗口须为 1–300 秒")
	}
	models := make([]string, 0, len(req.Models))
	seen := map[string]bool{}
	for _, model := range req.Models {
		model = strings.TrimSpace(model)
		if model != "" && !seen[model] {
			models = append(models, model)
			seen[model] = true
		}
	}
	req.Models = models
	if len(models) == 0 {
		return fmt.Errorf("请至少选择一个目标模型")
	}
	var alias string
	switch req.Agent {
	case "", "custom":
	case "claude":
		req.Protocol, alias = "messages", "claude"
	case "codex":
		req.Protocol, alias = "responses", "gpt"
	case "opencode":
		if req.Protocol == "" {
			req.Protocol = "chat"
		}
	default:
		return fmt.Errorf("不支持的 Agent")
	}
	if alias != "" {
		mapping := make(map[string]string, len(req.ModelMapping)+1)
		mapping[alias] = models[0]
		for k, v := range req.ModelMapping {
			mapping[k] = v
		}
		req.ModelMapping = mapping
	}
	if req.ModelMapping == nil {
		req.ModelMapping = map[string]string{}
	}
	for _, model := range models {
		if _, exists := req.ModelMapping[model]; !exists {
			req.ModelMapping[model] = model
		}
	}
	return nil
}

func saveAgentRouting(cm *config.ConfigManager, req ProvisionRequest) error {
	if req.Agent == "" || req.Agent == "custom" {
		return nil
	}
	pattern := req.Models[0]
	if req.Agent == "claude" {
		pattern = "claude"
	}
	if req.Agent == "codex" {
		pattern = "gpt"
	}
	models := req.Models[:1]
	if req.AllowModelFallback {
		models = req.Models
	}
	attempts, wait := req.MaxAttempts, req.MaxWaitSeconds
	if attempts == 0 {
		attempts = 6
	}
	if wait == 0 {
		wait = 90
	}
	patterns := map[string]string{pattern: models[0]}
	for from, to := range req.ModelMapping {
		patterns[from] = to
	}
	policies := make([]config.ModelFallbackPolicy, 0, len(patterns))
	for from, target := range patterns {
		order := []string{target}
		if req.AllowModelFallback {
			for i, model := range req.Models {
				if model == target {
					order = append([]string(nil), req.Models[i:]...)
					break
				}
			}
		}
		policies = append(policies, config.ModelFallbackPolicy{Protocol: req.Protocol, RequestModel: from, Models: order, MaxAttempts: attempts, MaxWaitSeconds: wait})
	}
	return cm.ReplaceAgentModelFallbackPolicies(req.Agent, req.Protocol, policies)
}

func fetchInferenceModels(ctx context.Context, account SiteAccount) ([]string, error) {
	key, err := account.plaintextAccessToken()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, account.BaseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("模型列表查询失败，可手动输入模型")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("模型列表 HTTP %d，可手动输入模型", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("模型列表格式不支持，可手动输入模型")
	}
	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return models, nil
}

func provisionOrImportKey(ctx context.Context, adapter siteAdapter, account SiteAccount, token string, existingID int, opts NewApiProvisionOptions) (int, string, bool, string, error) {
	if existingID == 0 {
		return adapter.ProvisionKey(ctx, account.BaseURL, token, account.UserID, account.AuthTokenMode, opts)
	}
	items, err := adapter.ListAllTokens(ctx, account, token)
	if err != nil {
		return 0, "", false, "", err
	}
	for _, item := range items {
		if item.ID != existingID {
			continue
		}
		if item.Group != opts.Group || (item.Status != 0 && item.Status != 1) {
			return 0, "", false, "", fmt.Errorf("已有 Key 的分组或状态不符合接入条件")
		}
		if !newApiTokenSupportsModels(item, opts.Models) {
			return 0, "", false, "", fmt.Errorf("已有 Key 未授权所选目标模型")
		}
		plain := item.Key
		if plain == "" || IsMaskedNewApiKey(plain) {
			plain, err = adapter.GetTokenKey(ctx, account.BaseURL, token, account.UserID, account.AuthTokenMode, item.ID)
		}
		if err != nil {
			return 0, "", false, "", err
		}
		if plain == "" || IsMaskedNewApiKey(plain) {
			return 0, "", false, "", fmt.Errorf("远端未返回完整 Key，请在站点后台获取后导入")
		}
		if account.SiteType == "newapi" {
			plain = normalizeNewApiPlaintextKey(plain)
		}
		return item.ID, plain, true, item.Name, nil
	}
	return 0, "", false, "", fmt.Errorf("选择的远端 Key 不存在")
}

func newApiTokenSupportsModels(token NewApiToken, models []string) bool {
	if !token.ModelLimitsEnabled {
		return true
	}
	allowed := map[string]bool{}
	for _, model := range strings.Split(token.ModelLimits, ",") {
		allowed[strings.TrimSpace(model)] = true
	}
	for _, model := range models {
		if !allowed[model] {
			return false
		}
	}
	return true
}

func importInferenceKey(store *AccountStore, cm *config.ConfigManager, account SiteAccount, req ProvisionRequest, apiType, serviceType string) (*ProvisionResult, error) {
	key, err := account.plaintextAccessToken()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("推理 Key 不能为空")
	}
	group := newApiResolvedGroup{Name: "imported"}
	name := ProvisionedChannelName(account.BaseURL, req.Protocol, group.Name)
	models := append([]string(nil), req.Models...)
	for alias := range req.ModelMapping {
		models = append(models, alias)
	}
	previousFingerprint := keyFingerprint(key)
	for _, meta := range account.ProvisionedKeys {
		if meta.Protocol == req.Protocol && meta.Group == group.Name {
			previousFingerprint = meta.KeyFingerprint
			break
		}
	}
	if err := upsertProvisionedChannel(cm, apiType, serviceType, account, group, 0, name, key, previousFingerprint, req.ModelMapping, models); err != nil {
		return nil, err
	}
	if err := recordProvisionedKey(store, account, req.Protocol, group, 0, "imported", key); err != nil {
		return nil, err
	}
	_ = store.Patch(account.UID, func(a *SiteAccount) error {
		for i := range a.ProvisionedKeys {
			if a.ProvisionedKeys[i].Protocol == req.Protocol {
				a.ProvisionedKeys[i].Imported = true
			}
		}
		return nil
	})
	RecordConfigured(store, account.UID, req.Protocol, group.Name)
	for _, ch := range protocolChannels(cm, apiType) {
		if ch.SiteAccountUID == account.UID && ch.SiteGroup == group.Name {
			name = ch.Name
			break
		}
	}
	return &ProvisionResult{AccountUID: account.UID, Protocol: req.Protocol, Groups: []ProvisionGroupResult{{Group: group.Name, ChannelName: name, KeyName: "imported", Reused: true}}}, nil
}
