// Package upstreamprobe 提供与渠道类型/协议无关的共享数据面探针。
//
// autopilot 新增渠道验证与 healthcheck 后台保活共享同一套火山 Agent/Coding Plan
// 数据面探测实现，避免请求特征（路径、模型名、Claude Code 特征）在两处各自漂移。
// 本包不依赖 autopilot / healthcheck，避免反向包循环。
package upstreamprobe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/utils"
)

// probeTimeout 单次火山套餐数据面探针的超时，与 autopilot verifyEndpointTimeout 对齐。
const probeTimeout = 12 * time.Second

// probeBodyLimit 探针响应体读取上限，仅供错误分类使用，避免撑爆内存。
const probeBodyLimit = 8 * 1024

// verifyVersionPattern 用于判断 baseURL 是否已含 /vN 版本段（与 autopilot 同口径）。
var verifyVersionPattern = regexp.MustCompile(`/v\d+[a-z]*$`)

// Result 火山套餐数据面探针结果。
//
// 与调用方解耦的 DTO：成功只接受真实 2xx；401/403 标记 AuthFailed；
// 其他 4xx/5xx 与网络错误保留原状态，不推断 Key 无效。Body 为截断后的上游响应体，
// 供调用方做 ShouldBlacklistKey 等错误分类。
type Result struct {
	OK         bool
	StatusCode int
	AuthFailed bool
	Model      string
	Body       []byte
	Err        error
}

// ProbeOptions 火山套餐数据面探针的附加请求选项。
// 透传渠道级 proxyURL、customHeaders 与 insecureSkipVerify，使探针与真实请求路径一致。
type ProbeOptions struct {
	ProxyURL           string
	ProxyPreferDirect  bool
	CustomHeaders      map[string]string
	InsecureSkipVerify bool
}

// probeOptionsFrom 把变长 opts 归一化为单一 ProbeOptions；空则返回零值。
func probeOptionsFrom(opts []ProbeOptions) ProbeOptions {
	if len(opts) > 0 {
		return opts[0]
	}
	return ProbeOptions{}
}

// IsVolcenginePlanBaseURL 判断 baseURL 是否为火山方舟 Agent/Coding Plan 官方入口。
//
// 使用解析后的官方 hostname（ark.cn-beijing.volces.com）和精确 path 前缀
// （/api/plan 或 /api/coding，含 /v3 后缀的 openai 入口）匹配，不用裸 strings.Contains
// 匹配任意域名，避免误命中中转站或自定义渠道。
func IsVolcenginePlanBaseURL(baseURL string) bool {
	target, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(baseURL), "#"))
	if err != nil || target.Hostname() == "" {
		return false
	}
	if !strings.EqualFold(target.Hostname(), "ark.cn-beijing.volces.com") {
		return false
	}
	path := strings.TrimRight(target.EscapedPath(), "/")
	if path == "/api/plan" || strings.HasPrefix(path, "/api/plan/") {
		return true
	}
	if path == "/api/coding" || strings.HasPrefix(path, "/api/coding/") {
		return true
	}
	return false
}

// volcenginePlanProbeModel 选择端点验证用的探针模型。
//
// 优先 deepseek-v4-flash（成本最低且当前默认）；若候选列表不包含它，
// Agent Plan 按 AFP 成本选最便宜模型，Coding Plan 回退到首个候选。
// candidates 为空时回退到常量 deepseek-v4-flash，保持历史行为。
func volcenginePlanProbeModel(baseURL string, candidates []string) string {
	if len(candidates) == 0 {
		return "deepseek-v4-flash"
	}
	// 标准化并去重，保持原始顺序
	seen := make(map[string]bool)
	models := make([]string, 0, len(candidates))
	for _, m := range candidates {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[strings.ToLower(m)] = true
		models = append(models, m)
	}
	if len(models) == 0 {
		return "deepseek-v4-flash"
	}
	for _, m := range models {
		if strings.EqualFold(m, "deepseek-v4-flash") {
			return "deepseek-v4-flash"
		}
	}
	if isVolcengineAgentPlanBaseURL(baseURL) {
		return cheapestVolcengineAgentPlanModel(models)
	}
	return models[0]
}

// isVolcengineAgentPlanBaseURL 判断 baseURL 是否为 Agent Plan 入口。
func isVolcengineAgentPlanBaseURL(baseURL string) bool {
	target, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(baseURL), "#"))
	if err != nil || target.Hostname() == "" {
		return false
	}
	path := strings.TrimRight(target.EscapedPath(), "/")
	return strings.EqualFold(target.Hostname(), "ark.cn-beijing.volces.com") &&
		(path == "/api/plan" || strings.HasPrefix(path, "/api/plan/"))
}

// manifestServiceType 把渠道配置的 serviceType 归一化为 manifest 查找口径。
// claude → messages（manifest 条目按 Anthropic Messages 协议登记）；openai 保持原值。
func manifestServiceType(serviceType string) string {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "messages":
		return "messages"
	case "openai":
		return "openai"
	default:
		return strings.ToLower(strings.TrimSpace(serviceType))
	}
}

// cheapestVolcengineAgentPlanModel 从候选中按 AFP 成本选最便宜的模型。
// 使用固定小 token 估算（32k 输入 / 1k 输出）仅用于相对比较。
func cheapestVolcengineAgentPlanModel(models []string) string {
	if len(models) == 0 {
		return "deepseek-v4-flash"
	}
	best := models[0]
	bestCost := float64(1<<63 - 1)
	now := time.Now()
	for _, m := range models {
		cost := volcengineAgentPlanProbeCost(now, m)
		if cost < bestCost {
			bestCost = cost
			best = m
		}
	}
	return best
}

// volcengineAgentPlanProbeCost 估算模型小 token 探针的 AFP 成本。
// 非 Agent Plan 已知模型返回 MaxFloat64，使其不会被选中。
func volcengineAgentPlanProbeCost(now time.Time, model string) float64 {
	result := config.ResolveVolcengineAFPCost(now, "agent_plan", model, 32000, 1000)
	if !result.Matched {
		return math.MaxFloat64
	}
	return float64(result.TotalAFP)
}

// ProbeVolcenginePlan 对一个 (baseURL, apiKey) 发火山套餐数据面最小请求验证可用性。
// 保持历史签名兼容，opts 透传渠道级网络与请求头配置；内部委托到 ProbeVolcenginePlanWithModels。
func ProbeVolcenginePlan(ctx context.Context, serviceType, baseURL, apiKey, authHeader string, opts ...ProbeOptions) Result {
	return ProbeVolcenginePlanWithModels(ctx, serviceType, baseURL, apiKey, authHeader, nil, opts...)
}

// ProbeVolcenginePlanWithModels 与 ProbeVolcenginePlan 相同，但允许调用方指定候选模型清单。
// candidates 为空时按内置清单查找；仍未命中则回退 deepseek-v4-flash。
// 探针统一流式优先：按渠道的上游协议（serviceType）选择对应端点发 stream=true 最小请求，
// 读到首个 SSE 事件即判活，避免推理模型非流式整段生成导致的高延迟误判超时。
func ProbeVolcenginePlanWithModels(ctx context.Context, serviceType, baseURL, apiKey, authHeader string, candidates []string, opts ...ProbeOptions) Result {
	model := volcenginePlanProbeModel(baseURL, candidates)
	options := probeOptionsFrom(opts)
	var result Result
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "messages":
		body := []byte(`{"model":"` + model + `","max_tokens":1,"stream":true,"messages":[{"role":"user","content":"ping"}]}`)
		body, sessionID := utils.EnsureClaudeCodeProbeBody(body)
		result = postJSONProbe(ctx, buildVersionedProbeURL(baseURL, "/messages"), apiKey, authHeader,
			func(req *http.Request) {
				utils.ApplyClaudeCodeProbeHeaders(req.Header, sessionID)
			}, body, options)
	case "openai":
		body := []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"ping"}],"max_tokens":1,"stream":true}`)
		result = postJSONProbe(ctx, buildVersionedProbeURL(baseURL, "/chat/completions"), apiKey, authHeader, nil, body, options)
	case "responses":
		body := []byte(`{"model":"` + model + `","input":"ping","max_output_tokens":16,"stream":true}`)
		result = postJSONProbe(ctx, buildVersionedProbeURL(baseURL, "/responses"), apiKey, authHeader, nil, body, options)
	default:
		return Result{Err: errUnsupportedServiceType(serviceType)}
	}
	result.Model = model
	return result
}

// VolcenginePlanL1Probe 执行火山套餐数据面探针并返回保活 L1 适配的响应。
// candidates 允许调用方传入该 key 的真实模型清单；为空时按 baseURL 从内置清单查找。
func VolcenginePlanL1Probe(ctx context.Context, serviceType, baseURL, apiKey, authHeader string, candidates []string, opts ...ProbeOptions) (statusCode int, body []byte, model string, err error) {
	res := ProbeVolcenginePlanWithModels(ctx, serviceType, baseURL, apiKey, authHeader, candidates, opts...)
	if res.Err != nil {
		return 0, nil, res.Model, res.Err
	}
	if res.OK {
		manifest, ok := config.LookupBuiltinManifest(baseURL, manifestServiceType(serviceType))
		if ok {
			return http.StatusOK, buildModelsListJSON(manifest.ModelIDs), res.Model, nil
		}
		// 命中官方套餐入口但无内置清单：视为可达但模型未知，返回空列表避免臆造模型。
		return http.StatusOK, []byte(`{"data":[]}`), res.Model, nil
	}
	return res.StatusCode, res.Body, res.Model, nil
}

// errEmptyProbeStream 上游接受了流式请求（2xx + SSE），但流结束都未返回任何 data 事件。
var errEmptyProbeStream = errors.New("上游返回空 SSE 流")

// probeSSEMaxScanBytes SSE 首事件扫描上限，防止异常上游无限冲刷事件流。
const probeSSEMaxScanBytes = 256 * 1024

// awaitFirstSSEEvent 等待 SSE 流的首个 data 事件：收到任意 data 行即判活。
// 流自然结束（或扫描超限）仍无 data 事件返回 (false, nil)；读取出错返回 (false, err)。
func awaitFirstSSEEvent(body io.Reader) (bool, error) {
	reader := bufio.NewReader(body)
	scanned := 0
	for scanned < probeSSEMaxScanBytes {
		line, err := reader.ReadString('\n')
		scanned += len(line)
		if strings.HasPrefix(line, "data:") {
			return true, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
	}
	return false, nil
}

// postJSONProbe 发送 JSON POST 探测并按火山严格策略分类结果：
// 成功只接受真实 2xx——SSE 响应需读到首个 data 事件（流式探针判活口径）；
// 上游忽略 stream 返回普通 JSON 时维持 2xx 即成功的历史口径；
// 401/403 标记 AuthFailed；其他 4xx/5xx 与网络错误保留原状态。
func postJSONProbe(ctx context.Context, urlStr, apiKey, authHeader string, prepare func(*http.Request), body []byte, options ProbeOptions) Result {
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, urlStr, bytes.NewReader(body))
	if err != nil {
		return Result{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	if prepare != nil {
		prepare(req)
	}
	utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, authHeader)
	utils.ApplyCustomHeaders(req.Header, options.CustomHeaders)

	proxyURL := options.ProxyURL
	client := httpclient.GetManager().GetClient(httpclient.ClientOptions{
		Timeout:           probeTimeout,
		Insecure:          options.InsecureSkipVerify,
		ProxyURL:          proxyURL,
		ProxyPreferDirect: options.ProxyPreferDirect,
	})
	resp, err := client.Do(req)
	if err != nil {
		return Result{Err: err}
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)

	sc := resp.StatusCode
	isSSE := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	switch {
	case sc >= 200 && sc < 300 && isSSE:
		ok, err := awaitFirstSSEEvent(resp.Body)
		if err != nil {
			return Result{StatusCode: sc, Err: err}
		}
		if !ok {
			return Result{StatusCode: sc, Err: errEmptyProbeStream}
		}
		return Result{OK: true, StatusCode: sc}
	case sc >= 200 && sc < 300:
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, probeBodyLimit))
		return Result{OK: true, StatusCode: sc, Body: bodyBytes}
	default:
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, probeBodyLimit))
		if sc == http.StatusUnauthorized || sc == http.StatusForbidden {
			return Result{OK: false, StatusCode: sc, AuthFailed: true, Body: bodyBytes}
		}
		return Result{OK: false, StatusCode: sc, Body: bodyBytes}
	}
}

// buildVersionedProbeURL 按 provider 拼接规则构建探测 URL（与 autopilot 同口径）：
//   - baseURL 以 # 结尾 → 跳过自动补 /v1
//   - baseURL 已含 /vN 后缀 → 直接拼端点
//   - 否则补 /v1 + 端点
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

// buildModelsListJSON 把模型 ID 列表编码为标准 OpenAI models 列表响应。
func buildModelsListJSON(modelIDs []string) []byte {
	type modelObj struct {
		ID string `json:"id"`
	}
	type listResp struct {
		Data []modelObj `json:"data"`
	}
	resp := listResp{Data: make([]modelObj, 0, len(modelIDs))}
	for _, id := range modelIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		resp.Data = append(resp.Data, modelObj{ID: id})
	}
	b, _ := json.Marshal(resp)
	return b
}

// errUnsupportedServiceType 返回不支持的 serviceType 错误。
func errUnsupportedServiceType(serviceType string) error {
	return &probeError{msg: "火山套餐探针不支持的 serviceType: " + serviceType}
}

type probeError struct{ msg string }

func (e *probeError) Error() string { return e.msg }
