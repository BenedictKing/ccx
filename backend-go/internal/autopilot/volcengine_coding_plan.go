package autopilot

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
)

const (
	volcengineManagementHost = "ark.cn-beijing.volcengineapi.com"
	volcengineOpenAPIHost    = "open.volcengineapi.com"
	volcengineRegion         = "cn-beijing"
	volcengineAPIVersion     = "2024-01-01"
	volcengineContentType    = "application/json; charset=UTF-8"
	// 火山方舟管控面示例要求只对这三个请求头签名。Content-Type 仍会发送，
	// 但不能列入 SignedHeaders，否则套餐模型列表接口会拒绝签名。
	volcengineSignedHeaders = "host;x-content-sha256;x-date"
	volcenginePlanAgent     = "agent_plan"
	volcenginePlanCoding    = "coding_plan"
)

type volcenginePlanInfo struct {
	Plan   string
	Tier   string
	Status string
}

type volcenginePlanClient struct {
	Endpoint   string
	HTTPClient *http.Client
	Now        func() time.Time
}

type volcengineResponse struct {
	ResponseMetadata struct {
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error,omitempty"`
	} `json:"ResponseMetadata"`
	Result struct {
		PlanType string `json:"PlanType"`
		Status   string `json:"Status"`
		Datas    []struct {
			ModelID string `json:"ModelID"`
		} `json:"Datas"`
		// Agent Plan GetAFPUsage 用量窗口。
		AFPFiveHour *volcengineAFPWindow `json:"AFPFiveHour,omitempty"`
		AFPDaily    *volcengineAFPWindow `json:"AFPDaily,omitempty"`
		AFPWeekly   *volcengineAFPWindow `json:"AFPWeekly,omitempty"`
		AFPMonthly  *volcengineAFPWindow `json:"AFPMonthly,omitempty"`
		// Coding Plan GetCodingPlanUsage 用量窗口。
		QuotaUsage []volcengineCodingPlanQuota `json:"QuotaUsage,omitempty"`
	} `json:"Result"`
}

// volcengineAFPWindow 是 Agent Plan AFP 单窗口用量。
type volcengineAFPWindow struct {
	Quota     float64 `json:"Quota"`
	Used      float64 `json:"Used"`
	ResetTime int64   `json:"ResetTime"`
}

// volcengineCodingPlanQuota 是 Coding Plan 单个用量窗口（仅返回已用百分比）。
type volcengineCodingPlanQuota struct {
	Level          string  `json:"Level"`
	Percent        float64 `json:"Percent"`
	ResetTimestamp int64   `json:"ResetTimestamp"`
}

type volcengineAPIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *volcengineAPIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("火山管控面错误 %s（HTTP %d）: %s", e.Code, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("火山管控面返回 HTTP %d: %s", e.StatusCode, e.Message)
}

func (c *volcenginePlanClient) DetectPlan(ctx context.Context, pair *config.VolcengineAccessKeyPair, hint string) (volcenginePlanInfo, error) {
	type candidate struct {
		plan string
		info volcenginePlanInfo
	}
	var found []candidate
	for _, plan := range []string{volcenginePlanAgent, volcenginePlanCoding} {
		info, err := c.getPersonalPlan(ctx, pair, plan)
		if err != nil {
			if apiErr, ok := err.(*volcengineAPIError); ok && apiErr.StatusCode == http.StatusNotFound {
				continue
			}
			return volcenginePlanInfo{}, err
		}
		found = append(found, candidate{plan: plan, info: info})
	}
	if len(found) == 0 {
		return volcenginePlanInfo{}, fmt.Errorf("access Key 所属账号未查询到 Agent Plan 或 Coding Plan，请确认 AK/SK 与推理 Key 属于同一账号")
	}
	hint = normalizeVolcenginePlan(hint)
	for _, item := range found {
		if item.plan == hint {
			return item.info, nil
		}
	}
	if len(found) == 1 {
		return found[0].info, nil
	}
	for _, item := range found {
		if strings.EqualFold(item.info.Status, "Running") {
			otherRunning := false
			for _, other := range found {
				if other.plan != item.plan && strings.EqualFold(other.info.Status, "Running") {
					otherRunning = true
				}
			}
			if !otherRunning {
				return item.info, nil
			}
		}
	}
	return volcenginePlanInfo{}, fmt.Errorf("该账号同时存在 Agent Plan 与 Coding Plan，且无法从推理 Key 的数据面地址消歧")
}

func (c *volcenginePlanClient) getPersonalPlan(ctx context.Context, pair *config.VolcengineAccessKeyPair, plan string) (volcenginePlanInfo, error) {
	apiPlan := "AgentPlan"
	if normalizeVolcenginePlan(plan) == volcenginePlanCoding {
		apiPlan = "CodingPlan"
		plan = volcenginePlanCoding
	} else {
		plan = volcenginePlanAgent
	}
	var decoded volcengineResponse
	if err := c.doAction(ctx, pair, "GetPersonalPlan", "ark", map[string]string{"Plan": apiPlan}, &decoded); err != nil {
		return volcenginePlanInfo{}, err
	}
	return volcenginePlanInfo{Plan: plan, Tier: strings.TrimSpace(decoded.Result.PlanType), Status: strings.TrimSpace(decoded.Result.Status)}, nil
}

func (c *volcenginePlanClient) FetchModels(ctx context.Context, pair *config.VolcengineAccessKeyPair, plan string) ([]string, error) {
	action := "ListArkAgentPlanModel"
	plan = normalizeVolcenginePlan(plan)
	if plan == volcenginePlanCoding {
		action = "ListArkCodingPlanModel"
	} else if plan != volcenginePlanAgent {
		return nil, fmt.Errorf("未知的火山套餐类型: %s", plan)
	}
	var decoded volcengineResponse
	if err := c.doAction(ctx, pair, action, "ark", struct{}{}, &decoded); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	models := make([]string, 0, len(decoded.Result.Datas))
	for _, item := range decoded.Result.Datas {
		modelID := strings.TrimSpace(item.ModelID)
		if modelID != "" && !seen[modelID] {
			seen[modelID] = true
			models = append(models, modelID)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("火山 %s 模型列表为空", displayVolcenginePlan(plan))
	}
	sort.Strings(models)
	return models, nil
}

// FetchUsage 查询火山套餐用量快照。
// Agent Plan 走 GetAFPUsage，返回含额度的四窗口；
// Coding Plan 走 GetCodingPlanUsage，返回 session/weekly/monthly 已用百分比。
func (c *volcenginePlanClient) FetchUsage(ctx context.Context, pair *config.VolcengineAccessKeyPair, plan string) (*config.VolcenginePlanUsage, error) {
	plan = normalizeVolcenginePlan(plan)
	usage := &config.VolcenginePlanUsage{FetchedAt: c.now()}
	switch plan {
	case volcenginePlanAgent:
		var decoded volcengineResponse
		if err := c.doAction(ctx, pair, "GetAFPUsage", "ark", nil, &decoded); err != nil {
			return nil, err
		}
		usage.FiveHour = afpWindow(decoded.Result.AFPFiveHour)
		usage.Daily = afpWindow(decoded.Result.AFPDaily)
		usage.Weekly = afpWindow(decoded.Result.AFPWeekly)
		usage.Monthly = afpWindow(decoded.Result.AFPMonthly)
		return usage, nil
	case volcenginePlanCoding:
		var decoded volcengineResponse
		if err := c.doAction(ctx, pair, "GetCodingPlanUsage", "ark", nil, &decoded); err != nil {
			return nil, err
		}
		for _, quota := range decoded.Result.QuotaUsage {
			resetTime := quota.ResetTimestamp
			if resetTime <= 0 {
				resetTime = 0
			} else if resetTime < 1_000_000_000_000 {
				resetTime *= 1000
			}
			usedPercent := quota.Percent
			window := &config.VolcenginePlanUsageWindow{UsedPercent: &usedPercent, ResetTime: resetTime}
			switch strings.ToLower(strings.TrimSpace(quota.Level)) {
			case "session", "5h", "5-hour", "fivehour", "five_hour", "rolling_5h":
				usage.FiveHour = window
			case "weekly", "week", "7d":
				usage.Weekly = window
			case "monthly", "month":
				usage.Monthly = window
			}
		}
		if usage.FiveHour == nil && usage.Weekly == nil && usage.Monthly == nil {
			return nil, fmt.Errorf("火山 Coding Plan 未返回套餐用量")
		}
		return usage, nil
	default:
		return nil, fmt.Errorf("未知的火山套餐类型: %s", plan)
	}
}

// afpWindow 将火山 AFP 窗口转换为通用用量窗口；nil 输入返回 nil。
func afpWindow(w *volcengineAFPWindow) *config.VolcenginePlanUsageWindow {
	if w == nil {
		return nil
	}
	return &config.VolcenginePlanUsageWindow{Quota: w.Quota, Used: w.Used, ResetTime: w.ResetTime}
}

// now 返回客户端时钟（便于测试注入）。
func (c *volcenginePlanClient) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *volcenginePlanClient) doAction(ctx context.Context, pair *config.VolcengineAccessKeyPair, action, service string, payload any, target *volcengineResponse) error {
	if pair == nil || strings.TrimSpace(pair.AccessKeyID) == "" || strings.TrimSpace(pair.SecretAccessKey) == "" {
		return fmt.Errorf("火山套餐识别、模型发现和用量查询需要绑定 Access Key ID 与 Secret Access Key")
	}
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("编码火山管控面请求失败: %w", err)
		}
	}
	endpoint := c.endpointFor(action, service)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构造火山管控面请求失败: %w", err)
	}
	query := req.URL.Query()
	query.Set("Action", action)
	query.Set("Version", volcengineAPIVersion)
	req.URL.RawQuery = query.Encode()
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	applyVolcengineSignature(req, body, pair.AccessKeyID, pair.SecretAccessKey, service, now)
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("请求火山管控面失败: %w", err)
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return fmt.Errorf("读取火山管控面响应失败: %w", err)
	}
	var decoded volcengineResponse
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return fmt.Errorf("解析火山管控面响应失败: %w", err)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || decoded.ResponseMetadata.Error != nil {
		apiErr := &volcengineAPIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
		if decoded.ResponseMetadata.Error != nil {
			apiErr.Code = decoded.ResponseMetadata.Error.Code
			apiErr.Message = decoded.ResponseMetadata.Error.Message
		}
		return apiErr
	}
	*target = decoded
	return nil
}

func (c *volcenginePlanClient) endpointFor(action, service string) string {
	if endpoint := strings.TrimSpace(c.Endpoint); endpoint != "" {
		return endpoint
	}
	if action == "GetAFPUsage" || action == "GetCodingPlanUsage" {
		return "https://" + volcengineOpenAPIHost + "/"
	}
	return "https://" + volcengineManagementHost + "/"
}

func applyVolcengineSignature(req *http.Request, body []byte, accessKeyID, secretAccessKey, service string, now time.Time) {
	payloadHash := sha256Hex(body)
	xDate := now.UTC().Format("20060102T150405Z")
	shortDate := xDate[:8]
	host := req.URL.Host
	canonicalHeaders := "host:" + host + "\n" +
		"x-content-sha256:" + payloadHash + "\n" +
		"x-date:" + xDate + "\n"
	canonicalRequest := req.Method + "\n" + canonicalURI(req.URL) + "\n" + canonicalQuery(req.URL) + "\n" + canonicalHeaders + "\n" + volcengineSignedHeaders + "\n" + payloadHash
	credentialScope := shortDate + "/" + volcengineRegion + "/" + service + "/request"
	stringToSign := "HMAC-SHA256\n" + xDate + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))
	kDate := hmacSHA256([]byte(secretAccessKey), shortDate)
	kRegion := hmacSHA256(kDate, volcengineRegion)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
	req.Host = host
	req.Header.Set("Content-Type", volcengineContentType)
	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", payloadHash)
	req.Header.Set("Authorization", "HMAC-SHA256 Credential="+accessKeyID+"/"+credentialScope+", SignedHeaders="+volcengineSignedHeaders+", Signature="+signature)
}

func canonicalURI(value *url.URL) string {
	if value == nil || value.EscapedPath() == "" {
		return "/"
	}
	return value.EscapedPath()
}

func canonicalQuery(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.Query().Encode()
}

func hmacSHA256(secret []byte, value string) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func normalizeVolcenginePlan(plan string) string {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "agentplan", "agent_plan", "agent":
		return volcenginePlanAgent
	case "codingplan", "coding_plan", "coding":
		return volcenginePlanCoding
	default:
		return ""
	}
}

func volcenginePlanFromBaseURL(baseURL string) string {
	lower := strings.ToLower(strings.TrimSpace(baseURL))
	if strings.Contains(lower, "ark.cn-beijing.volces.com/api/plan") {
		return volcenginePlanAgent
	}
	if strings.Contains(lower, "ark.cn-beijing.volces.com/api/coding") {
		return volcenginePlanCoding
	}
	return ""
}

func displayVolcenginePlan(plan string) string {
	if normalizeVolcenginePlan(plan) == volcenginePlanAgent {
		return "Agent Plan"
	}
	return "Coding Plan"
}

// FetchVolcenginePlanModelsForChannel 供管理端"拉取模型列表"入口使用。
// 火山套餐渠道的数据面 /models 不反映套餐真实清单：/api/coding 返回账号可见的
// 全量模型目录（含按量付费模型），/api/plan 无此接口直接 404，因此改走管控面
// 套餐模型接口（ListArkCodingPlanModel / ListArkAgentPlanModel）。
// channel 可为 nil（编辑对话框带临时 baseUrl 时后端无渠道上下文），此时按
// API Key 在托管账号中定位凭证。
// 返回 (模型列表, 是否命中火山套餐端点, 错误)：
//   - baseURL 非火山套餐端点：未命中，调用方继续原数据面路径；
//   - 命中且推理 Key 绑定了 Access Key：实时调用管控面返回真实套餐清单；
//   - 命中但未绑定 Access Key（含新增渠道的临时 baseUrl 场景）：返回内置兜底
//     清单，与自动发现的"未绑定回退内置清单"口径一致。
func FetchVolcenginePlanModelsForChannel(ctx context.Context, cfgManager *config.ConfigManager, channel *config.UpstreamConfig, baseURL, apiKey string) ([]string, bool, error) {
	return fetchVolcenginePlanModelsForChannel(ctx, cfgManager, channel, baseURL, apiKey, "", nil)
}

func fetchVolcenginePlanModelsForChannel(ctx context.Context, cfgManager *config.ConfigManager, channel *config.UpstreamConfig, baseURL, apiKey, endpoint string, httpClient *http.Client) ([]string, bool, error) {
	plan := volcenginePlanFromBaseURL(baseURL)
	if plan == "" {
		return nil, false, nil
	}
	if cfgManager != nil {
		accountUID, credentialUID := "", ""
		if channel != nil && strings.TrimSpace(channel.AccountUID) != "" {
			accountUID = channel.AccountUID
			credentialUID = channel.CredentialUIDForKey(apiKey)
		} else {
			accountUID, credentialUID = cfgManager.FindCredentialByAPIKey(apiKey)
		}
		if accountUID != "" && credentialUID != "" {
			if credential, ok := cfgManager.GetManagedAccountCredential(accountUID, credentialUID); ok && credential.VolcengineAccessKey != nil {
				client := &volcenginePlanClient{Endpoint: endpoint, HTTPClient: httpClient}
				models, err := client.FetchModels(ctx, credential.VolcengineAccessKey, plan)
				if err != nil {
					return nil, true, err
				}
				return models, true, nil
			}
		}
	}
	if plan == volcenginePlanAgent {
		return config.VolcengineAgentPlanModelIDs(), true, nil
	}
	return config.VolcengineCodingPlanModelIDs(), true, nil
}
