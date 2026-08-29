package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/utils"
)

// maxChannelProbeModels 「渠道模型列表」探测范围上限，防止全量目录探测成本失控
const maxChannelProbeModels = 20

// resolveChannelProbeModels 解析「渠道认可的模型列表」作为能力测试探测范围。
// 优先实时拉取上游清单（火山套餐走管控面），剔除非对话模型后按 SupportedModels 规则过滤；
// 拉取失败时回退 SupportedModels 精确项。结果截断至 maxChannelProbeModels。
func resolveChannelProbeModels(ctx context.Context, channel *config.UpstreamConfig, channelKind string, cfgManager *config.ConfigManager) ([]string, error) {
	apiKey := ""
	if len(channel.APIKeys) > 0 {
		apiKey = channel.APIKeys[0]
	} else if len(channel.DisabledAPIKeys) > 0 {
		apiKey = channel.DisabledAPIKeys[0].Key
	}
	if apiKey == "" {
		return nil, fmt.Errorf("no_api_key")
	}

	baseURL := channel.BoundBaseURLForKey(apiKey)
	if baseURL == "" {
		baseURL = capabilityTestBaseURL(channel)
	}
	if baseURL == "" {
		return nil, fmt.Errorf("no base URL configured")
	}

	// 1. 实时拉取渠道认可清单
	fetched, fetchErr := fetchChannelRecognizedModels(ctx, channel, channelKind, cfgManager, baseURL, apiKey)
	if fetchErr != nil {
		log.Printf("[CapabilityTest-ChannelModels] 渠道 %s 拉取模型清单失败: %v", channel.Name, fetchErr)
	}
	fetched = filterNonChatProbeModels(fetched, channel)

	// 2. SupportedModels 过滤。口径完全不交时退回清单全量：
	// SupportedModels 是对外放行口径（如 claude-*），探测目标是上游真实模型，两者天然可能不交。
	var resolved []string
	switch {
	case len(fetched) > 0 && len(channel.SupportedModels) > 0:
		for _, m := range fetched {
			if ok, _ := channel.ExplainModelSupport(m); ok {
				resolved = append(resolved, m)
			}
		}
		if len(resolved) == 0 {
			log.Printf("[CapabilityTest-ChannelModels] 渠道 %s SupportedModels 与上游清单无交集，使用清单全量", channel.Name)
			resolved = fetched
		}
	case len(fetched) > 0:
		resolved = fetched
	default:
		// 3. 拉取失败回退 SupportedModels 精确项
		resolved = exactSupportedModels(channel.SupportedModels)
	}
	if len(resolved) == 0 {
		if fetchErr != nil {
			return nil, fmt.Errorf("拉取渠道模型列表失败: %v", fetchErr)
		}
		return nil, fmt.Errorf("渠道模型列表为空")
	}

	// 4. 去重排序 + 上限截断
	resolved = normalizeCapabilityModels(resolved)
	if len(resolved) > maxChannelProbeModels {
		resolved = resolved[:maxChannelProbeModels]
	}
	log.Printf("[CapabilityTest-ChannelModels] 渠道 %s 解析探测模型 %d 个", channel.Name, len(resolved))
	return resolved, nil
}

// fetchChannelRecognizedModels 实时拉取渠道认可的模型清单：火山套餐走管控面，其余走数据面 /models。
func fetchChannelRecognizedModels(ctx context.Context, channel *config.UpstreamConfig, channelKind string, cfgManager *config.ConfigManager, baseURL, apiKey string) ([]string, error) {
	// 火山套餐：数据面 /models 不反映套餐清单，改走管控面套餐模型接口
	if models, handled, err := autopilot.FetchVolcenginePlanModelsForChannel(ctx, cfgManager, channel, baseURL, apiKey); handled {
		return models, err
	}

	if strings.EqualFold(strings.TrimSpace(channel.ServiceType), "copilot") {
		return nil, fmt.Errorf("copilot 渠道不支持渠道模型列表探测")
	}

	modelsURL := buildCapabilityTestURL(baseURL, "/v1", "/models")
	if channelKind == "gemini" {
		modelsURL = buildCapabilityTestURL(baseURL, "/v1beta", "/models")
	}

	client := httpclient.GetManager().GetClient(httpclient.ClientOptions{
		Timeout:           10 * time.Second,
		Insecure:          channel.InsecureSkipVerify,
		ProxyURL:          channel.ProxyURL,
		ProxyPreferDirect: channel.ProxyPreferDirect,
	})

	applyAuth := func(h http.Header) {
		if channelKind == "gemini" && !utils.HasAuthenticationHeaderOverride(channel.AuthHeader) {
			utils.SetGeminiAuthenticationHeader(h, apiKey)
		} else {
			utils.SetAuthenticationHeaderWithOverride(h, apiKey, channel.AuthHeader)
		}
	}
	// claude 系或已学习客户端伪装标记的上游首发带探针头（语义同 GetChannelModels）
	useProbeHeaders := strings.EqualFold(strings.TrimSpace(channel.ServiceType), "claude") || channel.LearnedClientFingerprint

	statusCode, body, _, err := utils.FetchUpstreamModels(ctx, client, modelsURL, applyAuth, channel.CustomHeaders, useProbeHeaders)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回 HTTP %d", statusCode)
	}
	return parseChannelModelsBody(body, channelKind)
}

// parseChannelModelsBody 解析 /models 响应：OpenAI/Claude 为 {"data":[{"id"}]}，Gemini 为 {"models":[{"name":"models/x"}]}。
func parseChannelModelsBody(body []byte, channelKind string) ([]string, error) {
	if channelKind == "gemini" {
		var payload struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		models := make([]string, 0, len(payload.Models))
		for _, m := range payload.Models {
			if name := strings.TrimPrefix(m.Name, "models/"); name != "" {
				models = append(models, name)
			}
		}
		return models, nil
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return models, nil
}

// filterNonChatProbeModels 按内置清单的 ExcludeModelPatterns 剔除明确非对话命名族
// （embedding/seedance/seedream/tts/asr 等），语义对齐发现层 filterExcludedDiscoveryModels。
func filterNonChatProbeModels(models []string, channel *config.UpstreamConfig) []string {
	if len(models) == 0 {
		return models
	}
	manifest, ok := config.LookupBuiltinManifest(capabilityTestBaseURL(channel), channel.ServiceType)
	if !ok || len(manifest.ExcludeModelPatterns) == 0 {
		return models
	}
	rules := make([]*regexp.Regexp, 0, len(manifest.ExcludeModelPatterns))
	for _, pattern := range manifest.ExcludeModelPatterns {
		rule, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		rules = append(rules, rule)
	}
	if len(rules) == 0 {
		return models
	}
	filtered := make([]string, 0, len(models))
	for _, m := range models {
		excluded := false
		for _, rule := range rules {
			if rule.MatchString(m) {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// exactSupportedModels 提取 SupportedModels 中的精确项（剔除通配与排除规则）
func exactSupportedModels(models []string) []string {
	var exact []string
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || strings.HasPrefix(m, "!") || strings.Contains(m, "*") {
			continue
		}
		exact = append(exact, m)
	}
	return exact
}
