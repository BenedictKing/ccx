package autopilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/upstreamprobe"
	"github.com/BenedictKing/ccx/internal/utils"
)

// verifyEndpointTimeout 单次端点验证探测的超时。
const verifyEndpointTimeout = 12 * time.Second

// minimalClaudeProbeBody 最小 Anthropic Messages 探测请求体。
// max_tokens 取极小值，模型名用占位（鉴权判定不依赖模型有效性）。
// 若上游因模型无效返回 4xx（非 401/403），仍说明服务可达且鉴权通过。
var minimalClaudeProbeBody = []byte(`{"model":"probe","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`)

// minimalOpenAIChatProbeBody 最小 OpenAI Chat Completions 探测请求体。
// 与 Claude 探测相同，400/422 通常表示占位模型或参数无效，但鉴权已通过。
var minimalOpenAIChatProbeBody = []byte(`{"model":"probe","messages":[{"role":"user","content":"ping"}],"max_tokens":1}`)

// minimalResponsesProbeBody 最小 OpenAI Responses 探测请求体。
var minimalResponsesProbeBody = []byte(`{"model":"probe","input":"ping","max_output_tokens":1}`)

var minimalImagesProbeBody = []byte(`{"model":"probe","prompt":"ping","size":"256x256"}`)

var minimalVectorsProbeBody = []byte(`{"model":"probe","input":"ping"}`)

var verifyVersionPattern = regexp.MustCompile(`/v\d+[a-z]*$`)

// EndpointVerifyResult 端点验证结果。
type EndpointVerifyResult struct {
	OK         bool   // 鉴权通过且服务可用
	StatusCode int    // 上游返回状态码（网络错误时为 0）
	AuthFailed bool   // true 表示服务可达但鉴权失败（401/403）
	Err        error  // 网络/构建错误
	Message    string // 简要诊断信息
}

// VerifyClaudeEndpoint 对一个 (baseURL, apiKey) 发最小 Anthropic Messages 请求验证可用性。
//
// 判定规则：
//   - 2xx / 400 / 422：鉴权通过（400/422 通常是探测用的占位模型或参数被拒，但服务可达且 key 有效）→ OK=true
//   - 401 / 403：服务可达但鉴权失败 → OK=false, AuthFailed=true
//   - 其他 4xx/5xx：该 baseURL 不可用（换下一个候选）→ OK=false
//     （例外：new-api 系对无渠道占位模型返回 503 + model_not_found，同样视为鉴权通过）
//   - 网络错误：该 baseURL 不可用 → OK=false, Err!=nil
//
// baseURL 应为 Anthropic 兼容入口（如 https://api.xiaomimimo.com/anthropic），
// 本函数按 claude provider 的拼接规则补 /v1/messages。
func VerifyClaudeEndpoint(ctx context.Context, baseURL, apiKey, authHeader string) EndpointVerifyResult {
	url := buildClaudeProbeURL(baseURL)
	return verifyJSONPostEndpoint(ctx, url, apiKey, authHeader, func(req *http.Request) {
		req.Header.Set("anthropic-version", "2023-06-01")
	}, minimalClaudeProbeBody)
}

// VerifyOpenAIChatEndpoint 对一个 (baseURL, apiKey) 发最小 Chat Completions 请求验证可用性。
func VerifyOpenAIChatEndpoint(ctx context.Context, baseURL, apiKey, authHeader string) EndpointVerifyResult {
	url := buildOpenAIChatProbeURL(baseURL)
	return verifyJSONPostEndpoint(ctx, url, apiKey, authHeader, nil, minimalOpenAIChatProbeBody)
}

// VerifyResponsesEndpoint 对一个 (baseURL, apiKey) 发最小 OpenAI Responses 请求验证可用性。
func VerifyResponsesEndpoint(ctx context.Context, baseURL, apiKey, authHeader string) EndpointVerifyResult {
	url := buildResponsesProbeURL(baseURL)
	return verifyJSONPostEndpoint(ctx, url, apiKey, authHeader, nil, minimalResponsesProbeBody)
}

// VerifyImagesEndpoint 对 OpenAI Images generations 端点发送最小请求验证可用性。
func VerifyImagesEndpoint(ctx context.Context, baseURL, apiKey, authHeader string) EndpointVerifyResult {
	return verifyJSONPostEndpoint(ctx, buildVersionedProbeURL(baseURL, "/images/generations"), apiKey, authHeader, nil, minimalImagesProbeBody)
}

// VerifyVectorsEndpoint 对 OpenAI Embeddings 端点发送最小请求验证可用性。
func VerifyVectorsEndpoint(ctx context.Context, baseURL, apiKey, authHeader string) EndpointVerifyResult {
	return verifyJSONPostEndpoint(ctx, buildVersionedProbeURL(baseURL, "/embeddings"), apiKey, authHeader, nil, minimalVectorsProbeBody)
}

// VerifyGeminiEndpoint 对 Gemini generateContent 端点发送最小请求验证可用性。
func VerifyGeminiEndpoint(ctx context.Context, baseURL, apiKey, authHeader string) EndpointVerifyResult {
	url := buildGeminiProbeURL(baseURL)
	if !utils.HasAuthenticationHeaderOverride(authHeader) {
		authHeader = "x-goog-api-key"
	}
	return verifyJSONPostEndpoint(ctx, url, apiKey, authHeader, nil, []byte(`{"contents":[{"role":"user","parts":[{"text":"ping"}]}],"generationConfig":{"maxOutputTokens":1}}`))
}

func verifyChannelKey(ctx context.Context, kind string, upstream config.UpstreamConfig, apiKey string) error {
	baseURLs := upstream.BaseURLsForKey(apiKey)
	if len(baseURLs) == 0 {
		return fmt.Errorf("渠道 %s 没有可验证的上游地址", upstream.Name)
	}
	var diagnostics []string
	for candidateIndex, baseURL := range baseURLs {
		var result EndpointVerifyResult
		switch kind {
		case "messages":
			result = VerifyClaudeEndpoint(ctx, baseURL, apiKey, upstream.AuthHeader)
		case "responses":
			result = VerifyResponsesEndpoint(ctx, baseURL, apiKey, upstream.AuthHeader)
		case "gemini":
			result = VerifyGeminiEndpoint(ctx, baseURL, apiKey, upstream.AuthHeader)
		case "chat":
			result = VerifyOpenAIChatEndpoint(ctx, baseURL, apiKey, upstream.AuthHeader)
		case "images":
			result = VerifyImagesEndpoint(ctx, baseURL, apiKey, upstream.AuthHeader)
		case "vectors":
			result = VerifyVectorsEndpoint(ctx, baseURL, apiKey, upstream.AuthHeader)
		default:
			return fmt.Errorf("不支持验证的渠道类型: %s", kind)
		}
		if !result.OK {
			diagnostics = append(diagnostics, verifyCandidateDiagnostic(candidateIndex+1, result))
		}
	}
	if len(diagnostics) == 0 {
		return nil
	}
	return fmt.Errorf("key %s 验证失败（%s）", utils.MaskAPIKey(apiKey), strings.Join(diagnostics, "；"))
}

func buildGeminiProbeURL(baseURL string) string {
	baseURL = strings.TrimSuffix(strings.TrimSuffix(baseURL, "#"), "/")
	if verifyVersionPattern.MatchString(baseURL) {
		return baseURL + "/models/gemini-2.5-flash:generateContent"
	}
	return baseURL + "/v1beta/models/gemini-2.5-flash:generateContent"
}

// VerifyKimiCodeModelsEndpoint 使用 Kimi Code 的模型列表接口验证 Key。
// Kimi Code 的 /coding/v1/models 会按 Key 返回套餐可用模型，避免用虚构模型
// 发起推理请求，也不会消耗请求额度。
func VerifyKimiCodeModelsEndpoint(ctx context.Context, baseURL, apiKey, authHeader string) EndpointVerifyResult {
	reqCtx, cancel := context.WithTimeout(ctx, verifyEndpointTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, buildKimiCodeModelsURL(baseURL), nil)
	if err != nil {
		return EndpointVerifyResult{Err: err, Message: "构建请求失败"}
	}
	req.Header.Set("Accept", "application/json")
	utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, authHeader)

	client := httpclient.GetManager().GetStandardClient(verifyEndpointTimeout, false)
	resp, err := client.Do(req)
	if err != nil {
		return EndpointVerifyResult{Err: err, Message: "请求失败: " + err.Error()}
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	sc := resp.StatusCode
	switch {
	case sc >= 200 && sc < 300:
		return EndpointVerifyResult{OK: true, StatusCode: sc}
	case sc == http.StatusUnauthorized || sc == http.StatusForbidden:
		return EndpointVerifyResult{OK: false, StatusCode: sc, AuthFailed: true, Message: "鉴权失败"}
	default:
		return EndpointVerifyResult{OK: false, StatusCode: sc, Message: "端点不可用"}
	}
}

func verifyJSONPostEndpoint(ctx context.Context, url, apiKey, authHeader string, prepare func(*http.Request), body []byte) EndpointVerifyResult {
	return verifyJSONPostEndpointWithPolicy(ctx, url, apiKey, authHeader, prepare, body, true)
}

func verifyJSONPostEndpointWithPolicy(ctx context.Context, url, apiKey, authHeader string, prepare func(*http.Request), body []byte, acceptValidationError bool) EndpointVerifyResult {
	reqCtx, cancel := context.WithTimeout(ctx, verifyEndpointTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return EndpointVerifyResult{Err: err, Message: "构建请求失败"}
	}
	req.Header.Set("Content-Type", "application/json")
	if prepare != nil {
		prepare(req)
	}
	utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, authHeader)

	client := httpclient.GetManager().GetStandardClient(verifyEndpointTimeout, false)
	resp, err := client.Do(req)
	if err != nil {
		return EndpointVerifyResult{Err: err, Message: "请求失败: " + err.Error()}
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	sc := resp.StatusCode
	switch {
	case sc >= 200 && sc < 300:
		return EndpointVerifyResult{OK: true, StatusCode: sc}
	case strings.EqualFold(authHeader, "x-goog-api-key") && geminiAuthFailure(responseBody):
		return EndpointVerifyResult{OK: false, StatusCode: sc, AuthFailed: true, Message: "鉴权失败"}
	case acceptValidationError && (sc == http.StatusBadRequest || sc == http.StatusUnprocessableEntity):
		// 占位模型/参数被拒，但服务可达且 key 有效
		return EndpointVerifyResult{OK: true, StatusCode: sc, Message: "服务可达（探测参数被拒，鉴权通过）"}
	case sc == http.StatusUnauthorized || sc == http.StatusForbidden:
		return EndpointVerifyResult{OK: false, StatusCode: sc, AuthFailed: true, Message: "鉴权失败"}
	case acceptValidationError && modelNotFoundErrorBody(responseBody):
		// new-api/one-api 系网关对无渠道的占位模型返回 503 + model_not_found，
		// 与 400/422 同属探测产物：鉴权已通过，不应误判为端点不可用
		return EndpointVerifyResult{OK: true, StatusCode: sc, Message: "服务可达（探测模型无可用渠道，鉴权通过）"}
	default:
		return EndpointVerifyResult{OK: false, StatusCode: sc, Message: "端点不可用"}
	}
}

// modelNotFoundErrorBody 判断错误响应是否为「鉴权已通过，但探测占位模型无可用渠道」。
// new-api/one-api 在 token 鉴权通过后按模型选渠道，模型无渠道时返回
// {"error":{"code":"model_not_found","message":"No available channel for model ..."}}（通常 503）。
func modelNotFoundErrorBody(body []byte) bool {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	if strings.EqualFold(payload.Error.Code, "model_not_found") {
		return true
	}
	return strings.Contains(strings.ToLower(payload.Error.Message), "no available channel for model")
}

func geminiAuthFailure(body []byte) bool {
	upper := strings.ToUpper(string(body))
	return strings.Contains(upper, "API_KEY_INVALID") ||
		strings.Contains(upper, "API KEY NOT VALID") ||
		strings.Contains(upper, "INVALID API KEY")
}

// buildClaudeProbeURL 按 claude provider 拼接规则构建 /messages 探测 URL。
// 复用 internal/providers/claude.go 的智能拼接逻辑：
//   - baseURL 以 # 结尾 → 跳过自动补 /v1
//   - baseURL 已含 /vN 后缀 → 直接拼 /messages
//   - 否则补 /v1/messages
func buildClaudeProbeURL(baseURL string) string {
	return buildVersionedProbeURL(baseURL, "/messages")
}

func buildOpenAIChatProbeURL(baseURL string) string {
	return buildVersionedProbeURL(baseURL, "/chat/completions")
}

func buildResponsesProbeURL(baseURL string) string {
	return buildVersionedProbeURL(baseURL, "/responses")
}

func buildVersionedProbeURL(baseURL, endpoint string) string {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	if strings.HasSuffix(strings.ToLower(baseURL), strings.ToLower(endpoint)) {
		return baseURL
	}
	if verifyVersionPattern.MatchString(baseURL) || skipVersionPrefix {
		return baseURL + endpoint
	}
	return baseURL + "/v1" + endpoint
}

// verifyProviderKeys 对一批 API Key 按 provider 模板的候选 baseURL 逐个探测验证，
// 为每个 key 绑定其首个可用端点（per-key baseURL），供 failover 热路径过滤无效组合。
//
// 探测策略（对应用户决策）：
//   - 每个 key 按 CandidatesForKey 得到的顺序探测（前缀命中的 plan 候选优先，其余回退在后）
//   - 命中首个 OK 端点即绑定，停止该 key 的后续探测
//   - 若某 key 遍历完所有候选：
//     · 全部为鉴权失败（401/403）→ 该 key 无效
//     · 存在其他失败类型 → 返回逐候选状态，避免把端点或协议问题误报为鉴权失败
//
// 返回：
//   - keyConfigs：与 apiKeys 一一对应、且 BaseURL 已绑定的 APIKeyConfig 列表
//   - baseURLs：去重后的所有命中端点（渠道级 BaseURLs，保持首次命中顺序）
//   - err：任一 key 验证失败时返回聚合错误（渠道不创建）
func verifyProviderKeys(ctx context.Context, tmpl *config.ProviderTemplate, apiKeys []string) ([]config.APIKeyConfig, []string, error) {
	if tmpl == nil {
		return nil, nil, fmt.Errorf("provider 模板为空")
	}
	return verifyProviderRouteKeys(ctx, tmpl, config.ProviderRoute{
		ChannelKind: tmpl.ChannelKind,
		ServiceType: tmpl.ServiceType,
		Candidates:  tmpl.Candidates,
	}, apiKeys)
}

func verifyProviderRouteKeys(ctx context.Context, tmpl *config.ProviderTemplate, route config.ProviderRoute, apiKeys []string) ([]config.APIKeyConfig, []string, error) {
	if tmpl == nil {
		return nil, nil, fmt.Errorf("provider 模板为空")
	}
	if route.ServiceType != "claude" && route.ServiceType != "openai" && route.ServiceType != "responses" {
		return nil, nil, fmt.Errorf("provider %s 暂不支持模板化验证（serviceType=%s）", tmpl.ProviderID, route.ServiceType)
	}
	if len(apiKeys) == 0 {
		return nil, nil, fmt.Errorf("apiKeys 不能为空")
	}

	keyConfigs := make([]config.APIKeyConfig, 0, len(apiKeys))
	baseURLs := make([]string, 0, len(route.Candidates))
	seenBaseURL := make(map[string]bool)

	for _, apiKey := range apiKeys {
		candidates := tmpl.CandidatesForRouteKey(route, apiKey)
		if len(candidates) == 0 {
			return nil, nil, fmt.Errorf("provider %s 无可用候选端点（kind=%s serviceType=%s）", tmpl.ProviderID, route.ChannelKind, route.ServiceType)
		}

		var (
			boundURL        string
			authFailedCount int
			diagnostics     []string
		)
		for candidateIndex, cand := range candidates {
			res := verifyProviderCandidateEndpoint(ctx, tmpl.ProviderID, route, cand.BaseURL, apiKey)
			if res.OK {
				boundURL = cand.BaseURL
				break
			}
			if res.AuthFailed {
				authFailedCount++
			}
			diagnostics = append(diagnostics, verifyCandidateDiagnostic(candidateIndex+1, res))
		}

		if boundURL == "" {
			mask := utils.MaskAPIKey(apiKey)
			summary := strings.Join(diagnostics, "；")
			if authFailedCount == len(candidates) {
				return nil, nil, fmt.Errorf("key %s 鉴权失败：所有候选端点均返回 401/403（%s）", mask, summary)
			}
			return nil, nil, fmt.Errorf("key %s 无可用候选端点（%s）", mask, summary)
		}

		keyConfigs = append(keyConfigs, config.APIKeyConfig{
			Key:     apiKey,
			BaseURL: boundURL,
		})
		if !seenBaseURL[boundURL] {
			seenBaseURL[boundURL] = true
			baseURLs = append(baseURLs, boundURL)
		}
	}

	return keyConfigs, baseURLs, nil
}

func verifyCandidateDiagnostic(index int, result EndpointVerifyResult) string {
	if result.StatusCode > 0 {
		if result.Message != "" {
			return fmt.Sprintf("候选 %d: HTTP %d（%s）", index, result.StatusCode, result.Message)
		}
		return fmt.Sprintf("候选 %d: HTTP %d", index, result.StatusCode)
	}
	if result.Message != "" {
		return fmt.Sprintf("候选 %d: %s", index, result.Message)
	}
	if result.Err != nil {
		return fmt.Sprintf("候选 %d: %v", index, result.Err)
	}
	return fmt.Sprintf("候选 %d: 未知错误", index)
}

func verifyProviderCandidateEndpoint(ctx context.Context, providerID string, route config.ProviderRoute, baseURL, apiKey string) EndpointVerifyResult {
	if providerID == "volcengine" {
		return verifyVolcenginePlanEndpoint(ctx, route, baseURL, apiKey)
	}
	if strings.EqualFold(providerID, "kimi") && isKimiCodeBaseURL(baseURL) {
		return VerifyKimiCodeModelsEndpoint(ctx, baseURL, apiKey, "")
	}
	switch route.ServiceType {
	case "claude":
		return VerifyClaudeEndpoint(ctx, baseURL, apiKey, "")
	case "openai":
		return VerifyOpenAIChatEndpoint(ctx, baseURL, apiKey, "")
	case "responses":
		return VerifyResponsesEndpoint(ctx, baseURL, apiKey, "")
	default:
		return EndpointVerifyResult{Message: fmt.Sprintf("不支持的 serviceType: %s", route.ServiceType)}
	}
}

func isKimiCodeBaseURL(baseURL string) bool {
	target, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(baseURL), "#"))
	if err != nil || !strings.EqualFold(target.Hostname(), "api.kimi.com") {
		return false
	}
	path := strings.TrimRight(target.EscapedPath(), "/")
	return path == "/coding" || strings.HasPrefix(path, "/coding/")
}

func buildKimiCodeModelsURL(baseURL string) string {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "#")
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(strings.ToLower(baseURL), "/models") {
		return baseURL
	}
	if strings.HasSuffix(strings.ToLower(baseURL), "/v1") {
		return baseURL + "/models"
	}
	return baseURL + "/v1/models"
}

func verifyVolcenginePlanEndpoint(ctx context.Context, route config.ProviderRoute, baseURL, apiKey string) EndpointVerifyResult {
	// 复用 internal/upstreamprobe 共享火山套餐数据面探针，避免与 healthcheck 保活请求特征漂移。
	res := upstreamprobe.ProbeVolcenginePlan(ctx, route.ServiceType, baseURL, apiKey, "", upstreamprobe.ProbeOptions{})
	switch {
	case res.Err != nil:
		return EndpointVerifyResult{Err: res.Err, Message: "请求失败: " + res.Err.Error()}
	case res.OK:
		return EndpointVerifyResult{OK: true, StatusCode: res.StatusCode}
	case res.AuthFailed:
		return EndpointVerifyResult{OK: false, StatusCode: res.StatusCode, AuthFailed: true, Message: "鉴权失败"}
	default:
		return EndpointVerifyResult{OK: false, StatusCode: res.StatusCode, Message: "端点不可用"}
	}
}
