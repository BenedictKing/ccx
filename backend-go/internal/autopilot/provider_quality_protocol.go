package autopilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/utils"
)

func (p *ProviderQualityProbe) runSample(
	ctx context.Context,
	profile *KeyEndpointProfile,
	upstream *config.UpstreamConfig,
	apiKey string,
	modelID string,
	index int,
) ProviderQualitySampleResult {
	sample := ProviderQualitySampleResult{Index: index}
	req, err := buildProviderQualityRequest(ctx, profile, upstream, apiKey, modelID)
	if err != nil {
		sample.ErrorCode = "request_build_failed"
		return sample
	}

	reqCtx, cancel := context.WithTimeout(req.Context(), p.config.RequestTimeout)
	defer cancel()
	req = req.WithContext(reqCtx)
	client := httpclient.GetManager().GetClient(httpclient.ClientOptions{
		Timeout:           p.config.RequestTimeout,
		Insecure:          upstream.InsecureSkipVerify,
		ProxyURL:          upstream.ProxyURL,
		ProxyPreferDirect: upstream.ProxyPreferDirect,
	})

	start := time.Now()
	resp, err := client.Do(req)
	sample.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		if reqCtx.Err() != nil {
			sample.ErrorCode = "request_timeout"
		} else {
			sample.ErrorCode = "request_failed"
		}
		return sample
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)
	sample.StatusCode = resp.StatusCode

	body, err := io.ReadAll(io.LimitReader(resp.Body, p.config.MaxResponseBody+1))
	if err != nil {
		sample.ErrorCode = "response_read_failed"
		return sample
	}
	if int64(len(body)) > p.config.MaxResponseBody {
		sample.ErrorCode = "response_too_large"
		return sample
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		sample.ErrorCode = fmt.Sprintf("upstream_http_%d", resp.StatusCode)
		return sample
	}

	text, ok := extractProviderQualityText(profile.ServiceType, body)
	if !ok || strings.TrimSpace(text) == "" {
		sample.ErrorCode = "invalid_response"
		return sample
	}

	sample.Dimensions, sample.Evidence, sample.Score = scoreProviderQualityOutput(text, sample.LatencyMs)
	return sample
}

func buildProviderQualityRequest(
	ctx context.Context,
	profile *KeyEndpointProfile,
	upstream *config.UpstreamConfig,
	apiKey string,
	modelID string,
) (*http.Request, error) {
	serviceType := strings.ToLower(strings.TrimSpace(profile.ServiceType))
	baseURL := strings.TrimSpace(profile.BaseURL)
	if baseURL == "" {
		baseURL = upstream.GetEffectiveBaseURL()
	}

	var (
		targetURL string
		payload   any
	)
	switch serviceType {
	case "claude", "messages":
		targetURL = buildVersionedProbeURL(baseURL, "/messages")
		body := map[string]any{
			"model":      modelID,
			"max_tokens": providerQualityMaxOutputTokens,
			"messages": []map[string]string{
				{"role": "user", "content": providerQualityCanaryPrompt},
			},
		}
		applyProviderQualityReasoningControl(body, serviceType, modelID, upstream)
		payload = body
	case "openai", "openai-chat", "chat":
		targetURL = buildVersionedProbeURL(baseURL, "/chat/completions")
		body := map[string]any{
			"model": modelID,
			"messages": []map[string]string{
				{"role": "user", "content": providerQualityCanaryPrompt},
			},
			"max_completion_tokens": providerQualityMaxOutputTokens,
		}
		applyProviderQualityReasoningControl(body, serviceType, modelID, upstream)
		payload = body
	case "responses", "codex":
		targetURL = buildVersionedProbeURL(baseURL, "/responses")
		body := map[string]any{
			"model":             modelID,
			"input":             providerQualityCanaryPrompt,
			"max_output_tokens": providerQualityMaxOutputTokens,
		}
		applyProviderQualityReasoningControl(body, serviceType, modelID, upstream)
		payload = body
	case "gemini":
		targetURL = buildGeminiProviderQualityURL(baseURL, modelID)
		payload = map[string]any{
			"contents": []map[string]any{
				{
					"role":  "user",
					"parts": []map[string]string{{"text": providerQualityCanaryPrompt}},
				},
			},
			"generationConfig": map[string]any{
				"maxOutputTokens": providerQualityMaxOutputTokens,
			},
		}
	default:
		return nil, fmt.Errorf("不支持的 serviceType: %s", profile.ServiceType)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化探测请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构建探测请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "CCX-Autopilot-ProviderQuality-Probe/1")

	switch serviceType {
	case "claude", "messages":
		utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)
		utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "gemini":
		if utils.HasAuthenticationHeaderOverride(upstream.AuthHeader) {
			utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
		} else {
			utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
		}
		utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)
	default:
		utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
		utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)
	}
	return req, nil
}

func applyProviderQualityReasoningControl(body map[string]any, serviceType, modelID string, upstream *config.UpstreamConfig) {
	resolved := config.ResolveUpstreamCapability(modelID, upstream, nil)
	if !resolved.Known || len(resolved.Capability.ReasoningEfforts) == 0 {
		return
	}
	effort := lowestProviderQualityReasoningEffort(resolved.Capability.ReasoningEfforts)
	switch serviceType {
	case "claude", "messages":
		switch resolved.Capability.ThinkingMode {
		case "adaptive_only":
			// adaptive_only 模型不接受手动 enabled/disabled，沿用上游默认。
			return
		case "thinking":
			body["thinking"] = map[string]any{"type": "enabled", "effort": effort}
		default:
			// MiMo 等可返回 reasoning_content、但未声明 Claude thinking 模式的兼容端点，
			// 使用与 capability-test 相同的关闭策略，避免固定 canary 被隐藏思考耗尽。
			body["thinking"] = map[string]any{"type": "disabled"}
		}
	case "openai", "openai-chat", "chat":
		body["reasoning_effort"] = effort
	case "responses", "codex":
		body["reasoning"] = map[string]any{"effort": effort}
	}
}

func lowestProviderQualityReasoningEffort(efforts []string) string {
	for _, preferred := range []string{"none", "off", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"} {
		for _, effort := range efforts {
			if strings.EqualFold(strings.TrimSpace(effort), preferred) {
				return preferred
			}
		}
	}
	return strings.TrimSpace(efforts[0])
}

func buildGeminiProviderQualityURL(baseURL, modelID string) string {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	if !verifyVersionPattern.MatchString(baseURL) && !skipVersionPrefix {
		baseURL += "/v1beta"
	}
	return fmt.Sprintf("%s/models/%s:generateContent", baseURL, url.PathEscape(modelID))
}

func extractProviderQualityText(serviceType string, body []byte) (string, bool) {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return "", false
	}

	var parts []string
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "messages":
		parts = appendTextValue(parts, payload["content"])
	case "openai", "openai-chat", "chat":
		parts = appendChatChoiceText(parts, payload["choices"])
	case "responses", "codex":
		if text, ok := payload["output_text"].(string); ok {
			parts = append(parts, text)
		}
		parts = appendResponsesOutputText(parts, payload["output"])
	case "gemini":
		parts = appendGeminiCandidateText(parts, payload["candidates"])
	}

	// 兼容部分中转站虽声明一种 serviceType、却返回另一种标准响应外形。
	if len(parts) == 0 {
		parts = appendTextValue(parts, payload["content"])
		parts = appendChatChoiceText(parts, payload["choices"])
		parts = appendResponsesOutputText(parts, payload["output"])
		parts = appendGeminiCandidateText(parts, payload["candidates"])
		if text, ok := payload["output_text"].(string); ok {
			parts = append(parts, text)
		}
	}

	text := strings.TrimSpace(strings.Join(parts, "\n"))
	return text, text != ""
}

func appendTextValue(parts []string, value any) []string {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			parts = append(parts, v)
		}
	case []any:
		for _, item := range v {
			parts = appendTextValue(parts, item)
		}
	case map[string]any:
		if text, ok := v["text"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
		if nested, ok := v["content"]; ok {
			parts = appendTextValue(parts, nested)
		}
		if nested, ok := v["parts"]; ok {
			parts = appendTextValue(parts, nested)
		}
	}
	return parts
}

func appendChatChoiceText(parts []string, value any) []string {
	choices, ok := value.([]any)
	if !ok {
		return parts
	}
	for _, choice := range choices {
		choiceMap, ok := choice.(map[string]any)
		if !ok {
			continue
		}
		if message, ok := choiceMap["message"].(map[string]any); ok {
			parts = appendTextValue(parts, message["content"])
		}
		parts = appendTextValue(parts, choiceMap["text"])
	}
	return parts
}

func appendResponsesOutputText(parts []string, value any) []string {
	items, ok := value.([]any)
	if !ok {
		return parts
	}
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		parts = appendTextValue(parts, itemMap["content"])
		parts = appendTextValue(parts, itemMap["text"])
	}
	return parts
}

func appendGeminiCandidateText(parts []string, value any) []string {
	candidates, ok := value.([]any)
	if !ok {
		return parts
	}
	for _, candidate := range candidates {
		candidateMap, ok := candidate.(map[string]any)
		if !ok {
			continue
		}
		parts = appendTextValue(parts, candidateMap["content"])
	}
	return parts
}
