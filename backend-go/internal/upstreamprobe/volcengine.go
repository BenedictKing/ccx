// Package upstreamprobe 提供与渠道类型/协议无关的共享数据面探针。
//
// autopilot 新增渠道验证与 healthcheck 后台保活共享同一套火山 Agent/Coding Plan
// 数据面探测实现，避免请求特征（路径、模型名、Claude Code 特征）在两处各自漂移。
// 本包不依赖 autopilot / healthcheck，避免反向包循环。
package upstreamprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
// Agent Plan 使用上游 Auto 模式；Coding Plan 沿用 ark-code-latest 兼容模型名。
func volcenginePlanProbeModel(baseURL string) string {
	if strings.Contains(strings.ToLower(baseURL), "/api/coding") {
		return "ark-code-latest"
	}
	return "auto"
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

// ProbeVolcenginePlan 对一个 (baseURL, apiKey) 发火山套餐数据面最小请求验证可用性。
//
// 探针行为固定为：
//   - Claude/Messages 入口：POST {baseURL}/v1/messages，model=auto 或 ark-code-latest，
//     注入 Claude Code system 身份、session metadata 与请求头。
//   - OpenAI 兼容入口：POST {baseURL}/chat/completions（baseURL 已含 /v3 时直接拼），
//     model 同上，不注入 Claude Code 特征。
//
// 判定规则：
//   - 2xx：OK=true（真实最小调用成功）
//   - 401/403：AuthFailed=true
//   - 其他 4xx/5xx、网络错误：保留原状态，不推断 Key 无效
//
// serviceType 为渠道配置口径（claude/openai/messages），内部归一化分派。
func ProbeVolcenginePlan(ctx context.Context, serviceType, baseURL, apiKey, authHeader string) Result {
	model := volcenginePlanProbeModel(baseURL)
	var result Result
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "messages":
		body := []byte(`{"model":"` + model + `","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`)
		body, sessionID := utils.EnsureClaudeCodeProbeBody(body)
		result = postJSONProbe(ctx, buildVersionedProbeURL(baseURL, "/messages"), apiKey, authHeader,
			func(req *http.Request) {
				utils.ApplyClaudeCodeProbeHeaders(req.Header, sessionID)
			}, body)
	case "openai":
		body := []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"ping"}],"max_tokens":1}`)
		result = postJSONProbe(ctx, buildVersionedProbeURL(baseURL, "/chat/completions"), apiKey, authHeader, nil, body)
	default:
		return Result{Err: errUnsupportedServiceType(serviceType)}
	}
	result.Model = model
	return result
}

// VolcenginePlanL1Probe 执行火山套餐数据面探针并返回保活 L1 适配的响应。
//
// 成功时从内置 manifest 生成标准 OpenAI models 列表响应（{"data":[{"id":"..."}]}），
// 复用 healthcheck 的 countModels/extractModelIDs 解析；失败时原样返回上游状态码与
// 截断响应体，让现有错误分类逻辑处理。返回的 (statusCode, body, err) 直接对应 L1Response。
//
// 真实调用已完成：调用方应据此跳过同周期等价 L2，避免重复消耗套餐额度。
func VolcenginePlanL1Probe(ctx context.Context, serviceType, baseURL, apiKey, authHeader string) (statusCode int, body []byte, model string, err error) {
	res := ProbeVolcenginePlan(ctx, serviceType, baseURL, apiKey, authHeader)
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

// postJSONProbe 发送 JSON POST 探测并按火山严格策略分类结果（只接受 2xx 为成功）。
func postJSONProbe(ctx context.Context, urlStr, apiKey, authHeader string, prepare func(*http.Request), body []byte) Result {
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

	client := httpclient.GetManager().GetStandardClient(probeTimeout, false)
	resp, err := client.Do(req)
	if err != nil {
		return Result{Err: err}
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, probeBodyLimit))

	sc := resp.StatusCode
	switch {
	case sc >= 200 && sc < 300:
		return Result{OK: true, StatusCode: sc, Body: bodyBytes}
	case sc == http.StatusUnauthorized || sc == http.StatusForbidden:
		return Result{OK: false, StatusCode: sc, AuthFailed: true, Body: bodyBytes}
	default:
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
