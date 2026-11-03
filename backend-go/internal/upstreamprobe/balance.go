package upstreamprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/utils"
)

// ── 上游余额探测 ──
//
// 用渠道持有的上游 API key 调上游余额接口（new-api 系 / sub2api / OpenAI 兼容
// billing 端点对），拿该 key 的剩余额度。与渠道类型/协议无关：只要上游部署暴露
// 上述任一端点即可探测。端点语义源自 refs/new-api 与 refs/sub2api 源码：
//   - newapi_token  GET /api/usage/token/         key 级，原始 quota 点，一发直取 remaining
//   - sub2api       GET /v1/usage                 key 级/订阅/钱包三模式，unit USD
//   - newapi_billing GET /v1/dashboard/billing/subscription + /usage  one-api 系通用，两发做减法

// balanceProbeBodyLimit 余额探测响应体读取上限。余额响应均为小 JSON；防异常大响应撑爆内存。
const balanceProbeBodyLimit = 256 * 1024

// unlimitedSentinelUSD new-api billing 端点对无限额度 token 返回的哨兵值（1e8）。
const unlimitedSentinelUSD = 100000000.0

// BalanceProbeRequest 单个上游 key 的余额探测请求参数。
type BalanceProbeRequest struct {
	BaseURL            string
	APIKey             string
	CustomHeaders      map[string]string
	ProxyURL           string
	ProxyPreferDirect  bool
	InsecureSkipVerify bool
	Timeout            time.Duration // 单次（每端点）请求超时；0=15s
}

// BalanceRateWindow sub2api 风格的窗口限速用量（5h/1d/7d），仅用于展示，不进调度。
type BalanceRateWindow struct {
	Window  string  `json:"window"`
	Limit   float64 `json:"limit"`
	Used    float64 `json:"used"`
	Remain  float64 `json:"remaining"`
	ResetAt *int64  `json:"resetAtMs,omitempty"` // 毫秒时间戳，窗口未知为 nil
}

// BalanceProbeResult 余额探测结果。
//
// Identified 表示成功识别上游类型并解析出余额语义（含「无限额度」）；
// 数值字段（Limit/Used/Remaining）以 OriginalUnit 为单位：
//   - credits：new-api 原始 quota 点（RawCredits 保留同值，便于上层按 QuotaPerUnit 换算）
//   - USD：billing/sub2api 的展示货币数字
type BalanceProbeResult struct {
	Provider     string // 实际使用的 provider（auto 识别后回填为具体类型）
	Identified   bool
	StatusCode   int
	Limit        *float64
	Used         *float64
	Remaining    *float64
	OriginalUnit string // "credits" | "USD"
	RawCredits   *float64
	Unlimited    bool
	Scope        string // key | user | subscription | wallet | unknown
	RateWindows  []BalanceRateWindow
	Err          error
}

// autoBalanceProbeOrder auto 模式的端点尝试顺序：
// newapi_token 最准（恒 key 级 + 点数语义明确）→ sub2api（key 级 + USD 明确）
// → newapi_billing（兼容面最广，但受上游站点展示单位/统计口径影响）。
var autoBalanceProbeOrder = []string{
	"newapi_token",
	"sub2api",
	"newapi_billing",
}

// ProbeBalance 按指定 provider 探测上游余额。
//
// provider 为具体类型（newapi_token/sub2api/newapi_billing）时只调该端点，失败即失败
// （用户显式指定即终审）；provider 为 auto 时按 autoBalanceProbeOrder 逐个尝试，
// 可选 preferred 变参优先复用上次识别成功的 provider（减少每轮探测请求数），
// 第一个 Identified 的结果即返回，全部失败时返回最后一个错误。
func ProbeBalance(ctx context.Context, req BalanceProbeRequest, provider string, preferred ...string) BalanceProbeResult {
	if req.Timeout <= 0 {
		req.Timeout = 15 * time.Second
	}
	switch provider {
	case "newapi_token", "sub2api", "newapi_billing":
		return probeBalanceWith(req, provider)
	}
	// auto：先试 preferred，再按固定顺序补齐其余端点。
	order := make([]string, 0, len(autoBalanceProbeOrder)+1)
	for _, p := range preferred {
		if isConcreteBalanceProvider(p) {
			order = append(order, p)
		}
	}
	for _, p := range autoBalanceProbeOrder {
		if !containsString(order, p) {
			order = append(order, p)
		}
	}
	var lastResult BalanceProbeResult
	for i, p := range order {
		res := probeBalanceWith(req, p)
		if res.Identified {
			return res
		}
		if i == 0 {
			lastResult = res
		} else {
			lastResult = mergeBalanceFailure(lastResult, res)
		}
	}
	lastResult.Provider = "auto"
	return lastResult
}

func isConcreteBalanceProvider(p string) bool {
	switch p {
	case "newapi_token", "sub2api", "newapi_billing":
		return true
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// mergeBalanceFailure 聚合 auto 模式下各端点的失败信息（截断防膨胀），保留最后状态码。
func mergeBalanceFailure(a, b BalanceProbeResult) BalanceProbeResult {
	merged := b
	if a.Err != nil && b.Err != nil {
		merged.Err = fmt.Errorf("%v; %v", truncateBalanceErr(a.Err), truncateBalanceErr(b.Err))
	} else if a.Err != nil {
		merged.Err = a.Err
	}
	return merged
}

func truncateBalanceErr(err error) error {
	msg := err.Error()
	if len(msg) > 120 {
		msg = msg[:120] + "..."
	}
	return fmt.Errorf("%s", msg)
}

func probeBalanceWith(req BalanceProbeRequest, provider string) BalanceProbeResult {
	switch provider {
	case "newapi_token":
		return probeNewapiTokenUsage(req)
	case "sub2api":
		return probeSub2apiUsage(req)
	case "newapi_billing":
		return probeNewapiBilling(req)
	}
	return BalanceProbeResult{Provider: provider, Err: fmt.Errorf("unknown balance provider: %s", provider)}
}

// balanceAPIRoot 归一化上游 base URL 为 API 根（剥尾部斜杠、# 标记与 /v1 版本段）。
// 渠道 BaseURL 存 https://x.com 与 https://x.com/v1 两种形态，余额端点路径自带版本段。
func balanceAPIRoot(baseURL string) string {
	u := strings.TrimSuffix(strings.TrimSpace(baseURL), "#")
	u = strings.TrimSuffix(u, "/")
	if strings.HasSuffix(u, "/v1") {
		u = strings.TrimSuffix(u, "/v1")
	}
	return strings.TrimSuffix(u, "/")
}

// balanceGetJSON 对余额端点发 GET（Bearer 鉴权），返回状态码与 body。
// 命中客户端指纹拦截特征时带 Claude Code 探针头重试一次（与 FetchUpstreamModels 同口径，
// new-api 系中转的风控常见）。customHeaders 最后应用，保证用户显式配置可覆盖。
func balanceGetJSON(req BalanceProbeRequest, url string) (int, []byte, error) {
	// 默认化下沉到这里：probe 函数既被 ProbeBalance 编排调用，也被测试/上层直接调用。
	if req.Timeout <= 0 {
		req.Timeout = 15 * time.Second
	}
	client := httpclient.GetManager().GetClient(httpclient.ClientOptions{
		Timeout:           req.Timeout,
		Insecure:          req.InsecureSkipVerify,
		ProxyURL:          req.ProxyURL,
		ProxyPreferDirect: req.ProxyPreferDirect,
	})

	doRequest := func(withProbeHeaders bool) (int, []byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), req.Timeout)
		defer cancel()
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, nil, err
		}
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
		if withProbeHeaders {
			utils.ApplyClaudeCodeProbeHeaders(httpReq.Header, "")
		}
		utils.ApplyCustomHeaders(httpReq.Header, req.CustomHeaders)
		resp, err := client.Do(httpReq)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(resp.Body, balanceProbeBodyLimit))
		if err != nil {
			return resp.StatusCode, nil, err
		}
		return resp.StatusCode, body, nil
	}

	statusCode, body, err := doRequest(false)
	if err != nil {
		return statusCode, nil, err
	}
	if utils.IsClientFingerprintRejection(statusCode, body) {
		if retryStatus, retryBody, retryErr := doRequest(true); retryErr == nil {
			return retryStatus, retryBody, nil
		}
	}
	return statusCode, body, nil
}

// ── newapi_token：GET {root}/api/usage/token/ ──

type newapiTokenUsageResponse struct {
	Code bool `json:"code"`
	Data *struct {
		Object         string  `json:"object"`
		Name           string  `json:"name"`
		TotalGranted   float64 `json:"total_granted"`
		TotalUsed      float64 `json:"total_used"`
		TotalAvailable float64 `json:"total_available"`
		UnlimitedQuota bool    `json:"unlimited_quota"`
	} `json:"data"`
}

func probeNewapiTokenUsage(req BalanceProbeRequest) BalanceProbeResult {
	res := BalanceProbeResult{Provider: "newapi_token", Scope: "key", OriginalUnit: "credits"}
	url := balanceAPIRoot(req.BaseURL) + "/api/usage/token/"
	statusCode, body, err := balanceGetJSON(req, url)
	res.StatusCode = statusCode
	if err != nil {
		res.Err = fmt.Errorf("request: %w", err)
		return res
	}
	if statusCode != http.StatusOK {
		res.Err = fmt.Errorf("newapi_token endpoint status %d", statusCode)
		return res
	}
	var parsed newapiTokenUsageResponse
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Data == nil {
		// 200 但不是预期 JSON（如网关兜底页），按未识别处理而不是误判余额。
		res.Err = fmt.Errorf("newapi_token unrecognized response body")
		return res
	}
	data := parsed.Data
	if data.Object != "" && data.Object != "token_usage" {
		res.Err = fmt.Errorf("newapi_token unexpected object %q", data.Object)
		return res
	}
	if data.UnlimitedQuota {
		res.Identified = true
		res.Unlimited = true
		return res
	}
	res.Identified = true
	res.RawCredits = &data.TotalAvailable
	res.Limit = &data.TotalGranted
	res.Used = &data.TotalUsed
	res.Remaining = &data.TotalAvailable
	return res
}

// ── sub2api：GET {root}/v1/usage ──

type sub2apiUsageResponse struct {
	Mode  string `json:"mode"`
	Quota *struct {
		Limit     *float64 `json:"limit"`
		Used      *float64 `json:"used"`
		Remaining *float64 `json:"remaining"`
		Unit      string   `json:"unit"`
	} `json:"quota"`
	Remaining  *float64 `json:"remaining"`
	Balance    *float64 `json:"balance"`
	RateLimits []struct {
		Window    string  `json:"window"`
		Limit     float64 `json:"limit"`
		Used      float64 `json:"used"`
		Remaining float64 `json:"remaining"`
		ResetAt   *int64  `json:"reset_at"`
	} `json:"rate_limits"`
}

func probeSub2apiUsage(req BalanceProbeRequest) BalanceProbeResult {
	res := BalanceProbeResult{Provider: "sub2api", OriginalUnit: "USD"}
	url := balanceAPIRoot(req.BaseURL) + "/v1/usage"
	statusCode, body, err := balanceGetJSON(req, url)
	res.StatusCode = statusCode
	if err != nil {
		res.Err = fmt.Errorf("request: %w", err)
		return res
	}
	if statusCode != http.StatusOK {
		res.Err = fmt.Errorf("sub2api endpoint status %d", statusCode)
		return res
	}
	var parsed sub2apiUsageResponse
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Mode == "" {
		res.Err = fmt.Errorf("sub2api unrecognized response body")
		return res
	}
	switch {
	case parsed.Mode == "quota_limited" && parsed.Quota != nil:
		res.Identified = true
		res.Scope = "key"
		res.Limit = parsed.Quota.Limit
		res.Used = parsed.Quota.Used
		res.Remaining = parsed.Quota.Remaining
		if parsed.Quota.Unit != "" {
			res.OriginalUnit = parsed.Quota.Unit
		}
	case parsed.Mode == "unrestricted" && parsed.Balance != nil:
		res.Identified = true
		res.Scope = "wallet"
		res.Remaining = parsed.Balance
	case parsed.Mode == "unrestricted":
		// 订阅模式：remaining 可能缺失（上游跳过计费中间件时无订阅上下文），
		// 仍视为识别成功，避免 auto 每轮重复全序列探测。
		res.Identified = true
		res.Scope = "subscription"
		res.Remaining = parsed.Remaining
	default:
		res.Err = fmt.Errorf("sub2api unexpected mode %q", parsed.Mode)
		return res
	}
	for _, w := range parsed.RateLimits {
		res.RateWindows = append(res.RateWindows, BalanceRateWindow{
			Window:  w.Window,
			Limit:   w.Limit,
			Used:    w.Used,
			Remain:  w.Remaining,
			ResetAt: w.ResetAt,
		})
	}
	return res
}

// ── newapi_billing：GET {root}/v1/dashboard/billing/subscription + /usage ──

type newapiBillingSubscriptionResponse struct {
	Object       string   `json:"object"`
	HardLimitUSD *float64 `json:"hard_limit_usd"`
}

type newapiBillingUsageResponse struct {
	Object     string   `json:"object"`
	TotalUsage *float64 `json:"total_usage"` // 单位：0.01 USD
}

func probeNewapiBilling(req BalanceProbeRequest) BalanceProbeResult {
	res := BalanceProbeResult{Provider: "newapi_billing", Scope: "unknown", OriginalUnit: "USD"}
	root := balanceAPIRoot(req.BaseURL)

	subStatus, subBody, err := balanceGetJSON(req, root+"/v1/dashboard/billing/subscription")
	res.StatusCode = subStatus
	if err != nil {
		res.Err = fmt.Errorf("subscription request: %w", err)
		return res
	}
	if subStatus != http.StatusOK {
		res.Err = fmt.Errorf("newapi_billing subscription status %d", subStatus)
		return res
	}
	var sub newapiBillingSubscriptionResponse
	if err := json.Unmarshal(subBody, &sub); err != nil || sub.HardLimitUSD == nil {
		res.Err = fmt.Errorf("newapi_billing unrecognized subscription body")
		return res
	}

	usageStatus, usageBody, err := balanceGetJSON(req, root+"/v1/dashboard/billing/usage")
	if err != nil {
		res.Err = fmt.Errorf("usage request: %w", err)
		return res
	}
	if usageStatus != http.StatusOK {
		res.Err = fmt.Errorf("newapi_billing usage status %d", usageStatus)
		return res
	}
	var usage newapiBillingUsageResponse
	if err := json.Unmarshal(usageBody, &usage); err != nil || usage.TotalUsage == nil {
		res.Err = fmt.Errorf("newapi_billing unrecognized usage body")
		return res
	}

	res.Identified = true
	if *sub.HardLimitUSD >= unlimitedSentinelUSD {
		res.Unlimited = true
		return res
	}
	used := *usage.TotalUsage / 100
	remaining := *sub.HardLimitUSD - used
	res.Limit = sub.HardLimitUSD
	res.Used = &used
	res.Remaining = &remaining
	return res
}
