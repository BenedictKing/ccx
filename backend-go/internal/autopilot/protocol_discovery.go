package autopilot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/utils"
)

const (
	protocolDiscoveryProbeTimeout = 8 * time.Second
	protocolDiscoveryMaxTokens    = 64
)

var discoverableProtocols = []string{"messages", "chat", "responses", "gemini"}

type protocolProbeResult struct {
	protocol string
	model    string
	err      error
}

// discoverEndpointProtocols 用一个代表模型验证其他原生协议是否可调用。
// 协议成功后沿用同一凭证的 models 清单；这是协议级可达性画像，不声称逐模型完成推理验证。
func (r *AutoDiscoveryRunner) discoverEndpointProtocols(
	ctx context.Context,
	channel *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	result *EndpointDiscoveryResult,
) {
	if channel == nil || result == nil || !result.ProtocolOk {
		return
	}
	ensureConfiguredProtocolDiscovery(channel, result)

	models := normalizeProtocolModels(result.Models)
	if len(models) == 0 {
		models = normalizeProtocolModels(channel.SupportedModels)
	}

	configuredProtocol := protocolForServiceType(channel.ServiceType)
	results := make(chan protocolProbeResult, len(discoverableProtocols))
	var wg sync.WaitGroup
	for _, protocol := range discoverableProtocols {
		if protocol == configuredProtocol {
			continue
		}
		model := selectProtocolProbeModel(protocol, models)
		if model == "" {
			result.ProtocolDiscoveryError[protocol] = "没有可用于协议探测的模型"
			continue
		}
		wg.Add(1)
		go func(protocol, model string) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, protocolDiscoveryProbeTimeout)
			defer cancel()
			results <- protocolProbeResult{
				protocol: protocol,
				model:    model,
				err:      r.probeProtocolModel(probeCtx, channel, baseURL, apiKey, protocol, model),
			}
		}(protocol, model)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	for probe := range results {
		if probe.err != nil {
			result.ProtocolDiscoveryError[probe.protocol] = probe.err.Error()
			continue
		}
		protocolModels := models
		if len(protocolModels) == 0 {
			protocolModels = []string{probe.model}
		}
		discoveredAt := time.Now().UTC()
		result.ProtocolModels[probe.protocol] = append([]string(nil), protocolModels...)
		result.ProtocolDiscoveredAt[probe.protocol] = discoveredAt
		result.ProtocolDiscoverySource[probe.protocol] = "protocol_probe"
		result.ProtocolDiscoveryMessage[probe.protocol] = fmt.Sprintf(
			"使用模型 %s 验证 %s 协议成功；模型范围沿用当前端点清单",
			probe.model,
			probe.protocol,
		)
		delete(result.ProtocolDiscoveryError, probe.protocol)
	}
}

func ensureConfiguredProtocolDiscovery(channel *config.UpstreamConfig, result *EndpointDiscoveryResult) {
	if channel == nil || result == nil || !result.ProtocolOk {
		return
	}
	ensureProtocolDiscoveryMaps(result)
	protocol := protocolForServiceType(channel.ServiceType)
	if protocol == "" {
		return
	}
	models := normalizeProtocolModels(result.Models)
	result.ProtocolModels[protocol] = models
	discoveredAt := time.Now().UTC()
	if result.ModelsDiscoveredAt != nil {
		discoveredAt = result.ModelsDiscoveredAt.UTC()
	}
	result.ProtocolDiscoveredAt[protocol] = discoveredAt
	result.ProtocolDiscoverySource[protocol] = result.ModelDiscoverySource
	result.ProtocolDiscoveryMessage[protocol] = result.ModelDiscoveryMessage
	delete(result.ProtocolDiscoveryError, protocol)
}

func ensureProtocolDiscoveryMaps(result *EndpointDiscoveryResult) {
	if result.ProtocolModels == nil {
		result.ProtocolModels = make(map[string][]string)
	}
	if result.ProtocolDiscoveredAt == nil {
		result.ProtocolDiscoveredAt = make(map[string]time.Time)
	}
	if result.ProtocolDiscoverySource == nil {
		result.ProtocolDiscoverySource = make(map[string]string)
	}
	if result.ProtocolDiscoveryMessage == nil {
		result.ProtocolDiscoveryMessage = make(map[string]string)
	}
	if result.ProtocolDiscoveryError == nil {
		result.ProtocolDiscoveryError = make(map[string]string)
	}
}

func protocolForServiceType(serviceType string) string {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "messages":
		return "messages"
	case "openai", "openai-chat", "chat":
		return "chat"
	case "responses", "codex", "copilot":
		return "responses"
	case "gemini":
		return "gemini"
	default:
		return ""
	}
}

func selectProtocolProbeModel(protocol string, models []string) string {
	preferredPrefixes := map[string][]string{
		"messages":  {"claude-", "mimo-", "kimi-", "glm-", "deepseek-", "minimax-"},
		"chat":      {"gpt-", "o1", "o3", "o4", "codex-", "mimo-", "kimi-", "glm-", "deepseek-", "minimax-"},
		"responses": {"gpt-", "o1", "o3", "o4", "codex-", "mimo-", "kimi-", "glm-", "deepseek-", "minimax-"},
		"gemini":    {"gemini-", "mimo-", "kimi-", "glm-", "deepseek-", "minimax-"},
	}
	for _, prefix := range preferredPrefixes[protocol] {
		for _, model := range models {
			if strings.HasPrefix(strings.ToLower(model), prefix) {
				return model
			}
		}
	}
	if len(models) > 0 {
		return models[0]
	}
	return map[string]string{
		"messages":  "claude-sonnet-4-6",
		"chat":      "gpt-5.4",
		"responses": "gpt-5.4",
		"gemini":    "gemini-3.5-flash",
	}[protocol]
}

func (r *AutoDiscoveryRunner) probeProtocolModel(
	ctx context.Context,
	channel *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	protocol string,
	model string,
) error {
	requestURL, body, sessionID, err := buildProtocolDiscoveryRequest(baseURL, protocol, model)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构建 %s 探测请求失败: %w", protocol, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if protocol == "gemini" && !utils.HasAuthenticationHeaderOverride(channel.AuthHeader) {
		utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
	} else {
		utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, channel.AuthHeader)
	}
	if protocol == "messages" {
		utils.ApplyClaudeCodeProbeHeaders(req.Header, sessionID)
	}
	if protocol == "responses" {
		req.Header.Set("Originator", "codex_cli_rs")
		req.Header.Set("User-Agent", "codex_cli_rs/0.111.0")
	}
	utils.ApplyCustomHeaders(req.Header, channel.CustomHeaders)

	client := r.client
	if client == nil {
		client = httpclient.GetManager().GetStandardClient(protocolDiscoveryProbeTimeout, channel.InsecureSkipVerify, channel.ProxyURL)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s 协议请求失败: %w", protocol, err)
	}
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		detail := strings.TrimSpace(string(bodyBytes))
		if len(detail) > 512 {
			detail = detail[:512]
		}
		if detail == "" {
			return fmt.Errorf("%s 协议返回 HTTP %d", protocol, resp.StatusCode)
		}
		return fmt.Errorf("%s 协议返回 HTTP %d: %s", protocol, resp.StatusCode, detail)
	}
	return nil
}

func buildProtocolDiscoveryRequest(baseURL, protocol, model string) (string, []byte, string, error) {
	var (
		requestURL string
		body       any
		sessionID  string
	)
	switch protocol {
	case "messages":
		metadata, generatedSessionID := utils.NewClaudeCodeProbeMetadata()
		sessionID = generatedSessionID
		requestURL = buildProtocolDiscoveryURL(baseURL, "/v1", "/messages")
		body = map[string]any{
			"model":      model,
			"system":     []any{utils.NewClaudeCodeProbeBillingBlock(), utils.NewClaudeCodeProbeIdentityBlock()},
			"messages":   []map[string]string{{"role": "user", "content": "Reply with OK."}},
			"metadata":   metadata,
			"max_tokens": protocolDiscoveryMaxTokens,
			"stream":     false,
		}
	case "chat":
		requestURL = buildProtocolDiscoveryURL(baseURL, "/v1", "/chat/completions")
		body = map[string]any{
			"model":      model,
			"messages":   []map[string]string{{"role": "user", "content": "Reply with OK."}},
			"max_tokens": protocolDiscoveryMaxTokens,
			"stream":     false,
		}
	case "responses":
		requestURL = buildProtocolDiscoveryURL(baseURL, "/v1", "/responses")
		body = map[string]any{
			"model":             model,
			"input":             "Reply with OK.",
			"max_output_tokens": protocolDiscoveryMaxTokens,
			"stream":            false,
		}
	case "gemini":
		requestURL = buildProtocolDiscoveryURL(baseURL, "/v1beta", "/models/"+model+":generateContent")
		body = map[string]any{
			"contents": []map[string]any{{
				"role":  "user",
				"parts": []map[string]string{{"text": "Reply with OK."}},
			}},
			"generationConfig": map[string]any{"maxOutputTokens": protocolDiscoveryMaxTokens},
		}
	default:
		return "", nil, "", fmt.Errorf("不支持的协议: %s", protocol)
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", nil, "", fmt.Errorf("序列化 %s 探测请求失败: %w", protocol, err)
	}
	return requestURL, bodyBytes, sessionID, nil
}

func buildProtocolDiscoveryURL(baseURL, versionPrefix, endpoint string) string {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	baseURL = strings.TrimSuffix(baseURL, "#")
	baseURL = strings.TrimRight(baseURL, "/")
	if !skipVersionPrefix && !verifyVersionPattern.MatchString(baseURL) {
		baseURL += versionPrefix
	}
	return baseURL + endpoint
}

func normalizeProtocolModels(models []string) []string {
	if len(models) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(models))
	result := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}

func hashProtocolModels(protocolModels map[string][]string) map[string]string {
	if len(protocolModels) == 0 {
		return nil
	}
	result := make(map[string]string, len(protocolModels))
	for protocol, models := range protocolModels {
		normalized := normalizeProtocolModels(models)
		sum := sha256.Sum256([]byte(strings.Join(normalized, ",")))
		result[protocol] = hex.EncodeToString(sum[:8])
	}
	return result
}

func cloneProtocolModels(source map[string][]string) map[string][]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string][]string, len(source))
	for protocol, models := range source {
		result[protocol] = append([]string(nil), models...)
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneTimeMap(source map[string]time.Time) map[string]time.Time {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]time.Time, len(source))
	for key, value := range source {
		result[key] = value.UTC()
	}
	return result
}

func cloneTimePointer(source *time.Time) *time.Time {
	if source == nil {
		return nil
	}
	value := source.UTC()
	return &value
}
