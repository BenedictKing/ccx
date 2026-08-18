package autopilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
)

const (
	kimiConsoleBaseURL          = "https://www.kimi.com"
	kimiUsagesPath              = "/apiv2/kimi.gateway.billing.v1.BillingService/GetUsages"
	kimiSubscriptionStatsPath   = "/apiv2/kimi.gateway.membership.v2.MembershipService/GetSubscriptionStats"
	maxKimiConsoleTokenBytes    = 32 << 10
	maxKimiConsoleResponseBytes = 1 << 20
)

// KimiConsoleClient 使用 Kimi Web 登录态查询 Kimi Code 套餐额度。
type KimiConsoleClient struct {
	HTTPClient *http.Client
	BaseURL    string
	Now        func() time.Time
}

type kimiJSONInt64 int64

func (value *kimiJSONInt64) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "null" || raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return err
		}
		raw = unquoted
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("不是有效整数: %q", raw)
	}
	*value = kimiJSONInt64(parsed)
	return nil
}

type kimiQuotaResponse struct {
	Limit     *kimiJSONInt64 `json:"limit"`
	Used      *kimiJSONInt64 `json:"used"`
	Remaining *kimiJSONInt64 `json:"remaining"`
	ResetTime string         `json:"resetTime"`
}

type kimiUsageWindowResponse struct {
	Duration *kimiJSONInt64 `json:"duration"`
	TimeUnit string         `json:"timeUnit"`
}

type kimiUsageLimitResponse struct {
	Window kimiUsageWindowResponse `json:"window"`
	Detail kimiQuotaResponse       `json:"detail"`
}

type kimiUsageResponse struct {
	Scope  string                   `json:"scope"`
	Detail kimiQuotaResponse        `json:"detail"`
	Limits []kimiUsageLimitResponse `json:"limits"`
}

type kimiUsagesResponse struct {
	Usages     []kimiUsageResponse `json:"usages"`
	TotalQuota kimiQuotaResponse   `json:"totalQuota"`
}

type kimiRatioResponse struct {
	Ratio     *float64 `json:"ratio"`
	Enabled   bool     `json:"enabled"`
	ResetTime string   `json:"resetTime"`
}

type kimiBalanceResponse struct {
	Feature           string   `json:"feature"`
	Type              string   `json:"type"`
	Unit              string   `json:"unit"`
	AmountUsedRatio   *float64 `json:"amountUsedRatio"`
	KimiCodeUsedRatio *float64 `json:"kimiCodeUsedRatio"`
	ExpireTime        string   `json:"expireTime"`
}

type kimiMoneyResponse struct {
	Currency     string         `json:"currency"`
	PriceInCents *kimiJSONInt64 `json:"priceInCents"`
}

type kimiBoosterWalletResponse struct {
	ID                 string             `json:"id"`
	Status             string             `json:"status"`
	AllowTopup         bool               `json:"allowTopup"`
	MoneyLeft          *kimiMoneyResponse `json:"moneyLeft"`
	MoneyTotal         *kimiMoneyResponse `json:"moneyTotal"`
	MonthlyChargeLimit *kimiMoneyResponse `json:"monthlyChargeLimit"`
	MonthlyUsed        *kimiMoneyResponse `json:"monthlyUsed"`
}

type kimiSubscriptionStatsResponse struct {
	RateLimitCodeFiveHour *kimiRatioResponse          `json:"ratelimitCode5h"`
	RateLimitCodeSevenDay *kimiRatioResponse          `json:"ratelimitCode7d"`
	SubscriptionBalance   *kimiBalanceResponse        `json:"subscriptionBalance"`
	GiftBalances          []kimiBalanceResponse       `json:"giftBalances"`
	BoosterWallets        []kimiBoosterWalletResponse `json:"boosterWallets"`
}

// Verify 校验 Kimi Web 会话，返回待持久化的令牌与套餐快照。
func (client *KimiConsoleClient) Verify(ctx context.Context, rawAccessToken string) (*config.KimiConsoleCredential, error) {
	accessToken, err := normalizeKimiConsoleToken(rawAccessToken)
	if err != nil {
		return nil, err
	}

	var usages kimiUsagesResponse
	if err := client.post(ctx, kimiUsagesPath, accessToken, map[string][]string{
		"scope": {"FEATURE_CODING"},
	}, &usages); err != nil {
		return nil, fmt.Errorf("查询 Kimi Code 套餐用量失败: %w", err)
	}

	var stats kimiSubscriptionStatsResponse
	if err := client.post(ctx, kimiSubscriptionStatsPath, accessToken, struct{}{}, &stats); err != nil {
		return nil, fmt.Errorf("查询 Kimi 订阅余额失败: %w", err)
	}

	snapshot, err := buildKimiCodeUsageSnapshot(usages, stats, client.now())
	if err != nil {
		return nil, err
	}
	return &config.KimiConsoleCredential{AccessToken: accessToken, Usage: snapshot}, nil
}

func buildKimiCodeUsageSnapshot(usages kimiUsagesResponse, stats kimiSubscriptionStatsResponse, now time.Time) (config.KimiCodeUsageSnapshot, error) {
	var codingUsage *kimiUsageResponse
	for index := range usages.Usages {
		if strings.EqualFold(strings.TrimSpace(usages.Usages[index].Scope), "FEATURE_CODING") {
			codingUsage = &usages.Usages[index]
			break
		}
	}
	if codingUsage == nil {
		return config.KimiCodeUsageSnapshot{}, fmt.Errorf("kimi 用量接口未返回 FEATURE_CODING")
	}

	weekly, err := kimiQuotaSnapshot(codingUsage.Detail)
	if err != nil {
		return config.KimiCodeUsageSnapshot{}, fmt.Errorf("解析 Kimi 周额度失败: %w", err)
	}
	total := weekly
	if usages.TotalQuota.Limit != nil {
		total, err = kimiQuotaSnapshot(usages.TotalQuota)
		if err != nil {
			return config.KimiCodeUsageSnapshot{}, fmt.Errorf("解析 Kimi 总额度失败: %w", err)
		}
	}

	snapshot := config.KimiCodeUsageSnapshot{
		WeeklyUsage: weekly,
		TotalQuota:  total,
		ValidatedAt: now,
	}
	for _, limit := range codingUsage.Limits {
		windowSeconds, err := kimiWindowSeconds(limit.Window.Duration, limit.Window.TimeUnit)
		if err != nil {
			return config.KimiCodeUsageSnapshot{}, err
		}
		usage, err := kimiQuotaSnapshot(limit.Detail)
		if err != nil {
			return config.KimiCodeUsageSnapshot{}, fmt.Errorf("解析 Kimi 频限窗口失败: %w", err)
		}
		snapshot.RateLimits = append(snapshot.RateLimits, config.KimiCodeRateLimit{
			WindowSeconds: windowSeconds,
			Usage:         usage,
		})
	}

	snapshot.CodeFiveHour, err = kimiRatioSnapshot(stats.RateLimitCodeFiveHour)
	if err != nil {
		return config.KimiCodeUsageSnapshot{}, fmt.Errorf("解析 Kimi 5 小时频限失败: %w", err)
	}
	snapshot.CodeSevenDay, err = kimiRatioSnapshot(stats.RateLimitCodeSevenDay)
	if err != nil {
		return config.KimiCodeUsageSnapshot{}, fmt.Errorf("解析 Kimi 7 天频限失败: %w", err)
	}
	snapshot.SubscriptionBalance, err = kimiBalanceSnapshot(stats.SubscriptionBalance)
	if err != nil {
		return config.KimiCodeUsageSnapshot{}, fmt.Errorf("解析 Kimi 订阅余额失败: %w", err)
	}
	for index := range stats.GiftBalances {
		balance, err := kimiBalanceSnapshot(&stats.GiftBalances[index])
		if err != nil {
			return config.KimiCodeUsageSnapshot{}, fmt.Errorf("解析 Kimi 赠送余额失败: %w", err)
		}
		if balance != nil {
			snapshot.GiftBalances = append(snapshot.GiftBalances, *balance)
		}
	}
	for index := range stats.BoosterWallets {
		wallet, err := kimiBoosterWalletSnapshot(&stats.BoosterWallets[index])
		if err != nil {
			return config.KimiCodeUsageSnapshot{}, fmt.Errorf("解析 Kimi 加油包失败: %w", err)
		}
		if wallet != nil {
			snapshot.BoosterWallets = append(snapshot.BoosterWallets, *wallet)
		}
	}
	return snapshot, nil
}

func kimiQuotaSnapshot(source kimiQuotaResponse) (config.KimiCodeQuotaWindow, error) {
	if source.Limit == nil {
		return config.KimiCodeQuotaWindow{}, fmt.Errorf("缺少 limit")
	}
	limit := int64(*source.Limit)
	if limit < 0 {
		return config.KimiCodeQuotaWindow{}, fmt.Errorf("limit 不能为负数")
	}

	var used, remaining int64
	switch {
	case source.Remaining != nil:
		remaining = int64(*source.Remaining)
		if remaining < 0 {
			return config.KimiCodeQuotaWindow{}, fmt.Errorf("remaining 不能为负数")
		}
		used = limit - remaining
		if used < 0 {
			used = 0
		}
	case source.Used != nil:
		used = int64(*source.Used)
		if used < 0 {
			return config.KimiCodeQuotaWindow{}, fmt.Errorf("used 不能为负数")
		}
		remaining = limit - used
		if remaining < 0 {
			remaining = 0
		}
	default:
		return config.KimiCodeQuotaWindow{}, fmt.Errorf("缺少 remaining/used")
	}

	resetTime, err := normalizeKimiTimestamp(source.ResetTime)
	if err != nil {
		return config.KimiCodeQuotaWindow{}, err
	}
	return config.KimiCodeQuotaWindow{
		Used: used, Limit: limit, Remaining: remaining, ResetTime: resetTime,
	}, nil
}

func kimiWindowSeconds(duration *kimiJSONInt64, unit string) (int64, error) {
	if duration == nil || *duration <= 0 {
		return 0, fmt.Errorf("kimi 频限窗口时长无效")
	}
	multiplier := int64(0)
	switch strings.ToUpper(strings.TrimSpace(unit)) {
	case "TIME_UNIT_SECOND":
		multiplier = 1
	case "TIME_UNIT_MINUTE":
		multiplier = 60
	case "TIME_UNIT_HOUR":
		multiplier = 60 * 60
	case "TIME_UNIT_DAY":
		multiplier = 24 * 60 * 60
	default:
		return 0, fmt.Errorf("kimi 频限窗口单位不受支持: %s", unit)
	}
	value := int64(*duration)
	if value > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("kimi 频限窗口时长溢出")
	}
	return value * multiplier, nil
}

func kimiRatioSnapshot(source *kimiRatioResponse) (*config.KimiCodeRatioWindow, error) {
	if source == nil {
		return nil, nil
	}
	ratio := 0.0
	if source.Ratio != nil {
		ratio = *source.Ratio
	}
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
		return nil, fmt.Errorf("ratio 无效")
	}
	resetTime, err := normalizeKimiTimestamp(source.ResetTime)
	if err != nil {
		return nil, err
	}
	return &config.KimiCodeRatioWindow{Ratio: ratio, Enabled: source.Enabled, ResetTime: resetTime}, nil
}

func kimiBalanceSnapshot(source *kimiBalanceResponse) (*config.KimiCodeBalance, error) {
	if source == nil {
		return nil, nil
	}
	amountRatio, codeRatio := 0.0, 0.0
	if source.AmountUsedRatio != nil {
		amountRatio = *source.AmountUsedRatio
	}
	if source.KimiCodeUsedRatio != nil {
		codeRatio = *source.KimiCodeUsedRatio
	}
	for _, ratio := range []float64{amountRatio, codeRatio} {
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
			return nil, fmt.Errorf("余额比例无效")
		}
	}
	expireTime, err := normalizeKimiTimestamp(source.ExpireTime)
	if err != nil {
		return nil, err
	}
	return &config.KimiCodeBalance{
		Feature: strings.TrimSpace(source.Feature), Type: strings.TrimSpace(source.Type), Unit: strings.TrimSpace(source.Unit),
		AmountUsedRatio: amountRatio, KimiCodeUsedRatio: codeRatio, ExpireTime: expireTime,
	}, nil
}

func kimiMoneySnapshot(source *kimiMoneyResponse) config.KimiCodeMoney {
	if source == nil {
		return config.KimiCodeMoney{}
	}
	priceInCents := int64(0)
	if source.PriceInCents != nil {
		priceInCents = int64(*source.PriceInCents)
	}
	return config.KimiCodeMoney{Currency: strings.TrimSpace(source.Currency), PriceInCents: priceInCents}
}

func kimiBoosterWalletSnapshot(source *kimiBoosterWalletResponse) (*config.KimiBoosterWallet, error) {
	if source == nil {
		return nil, nil
	}
	return &config.KimiBoosterWallet{
		ID:                 strings.TrimSpace(source.ID),
		Status:             strings.TrimSpace(source.Status),
		AllowTopup:         source.AllowTopup,
		MoneyLeft:          kimiMoneySnapshot(source.MoneyLeft),
		MoneyTotal:         kimiMoneySnapshot(source.MoneyTotal),
		MonthlyChargeLimit: kimiMoneySnapshot(source.MonthlyChargeLimit),
		MonthlyUsed:        kimiMoneySnapshot(source.MonthlyUsed),
	}, nil
}

func normalizeKimiTimestamp(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", fmt.Errorf("时间格式无效: %w", err)
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func normalizeKimiConsoleToken(raw string) (string, error) {
	token := strings.TrimSpace(raw)
	if len(token) > maxKimiConsoleTokenBytes {
		return "", fmt.Errorf("kimi 控制台令牌长度超过限制")
	}

	// 兼容 localStorage 中 JSON 形式的登录态，如 {"access_token":"..."}
	if strings.HasPrefix(token, "{") {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(token), &payload); err == nil {
			for _, key := range []string{"access_token", "accessToken", "token"} {
				var value string
				if rawValue, ok := payload[key]; ok && json.Unmarshal(rawValue, &value) == nil && strings.TrimSpace(value) != "" {
					token = strings.TrimSpace(value)
					break
				}
			}
		}
	}

	// 兼容 Cookie 形式：access_token=xxx、access_token=xxx; Path=/... 或含多个条目的 Cookie 串
	if strings.Contains(strings.ToLower(token), "access_token=") {
		for _, segment := range strings.Split(token, ";") {
			segment = strings.TrimSpace(segment)
			if len(segment) >= len("access_token=") && strings.EqualFold(segment[:len("access_token=")], "access_token=") {
				token = strings.TrimSpace(segment[len("access_token="):])
				break
			}
		}
	}

	// 去掉成对引号（从 HAR / localStorage 复制时常见）
	if len(token) >= 2 {
		first, last := token[0], token[len(token)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			token = strings.TrimSpace(token[1 : len(token)-1])
		}
	}

	// Cookie / localStorage 复制可能带 URL 编码
	if strings.Contains(token, "%") {
		if decoded, err := url.PathUnescape(token); err == nil {
			token = decoded
		}
	}

	if len(token) >= len("authorization:") && strings.EqualFold(token[:len("authorization:")], "authorization:") {
		token = strings.TrimSpace(token[len("authorization:"):])
	}
	if len(token) >= len("bearer ") && strings.EqualFold(token[:len("bearer ")], "bearer ") {
		token = strings.TrimSpace(token[len("bearer "):])
	}
	if strings.EqualFold(token, "bearer") {
		token = ""
	}
	if token == "" {
		return "", fmt.Errorf("kimi 控制台令牌不能为空")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return "", fmt.Errorf("kimi 控制台令牌格式无效")
	}
	return token, nil
}

func (client *KimiConsoleClient) post(ctx context.Context, path, accessToken string, payload, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	baseURL := kimiConsoleBaseURL
	httpClient := http.DefaultClient
	if client != nil {
		if strings.TrimSpace(client.BaseURL) != "" {
			baseURL = strings.TrimRight(strings.TrimSpace(client.BaseURL), "/")
		}
		if client.HTTPClient != nil {
			httpClient = client.HTTPClient
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", kimiConsoleBaseURL)
	req.Header.Set("Referer", kimiConsoleBaseURL+"/code/console")
	req.Header.Set("R-Timezone", "Asia/Shanghai")
	req.Header.Set("X-Language", "zh-CN")
	req.Header.Set("X-Msh-Platform", "web")
	req.Header.Set("X-Msh-Version", "2.0.0")

	response, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer errutil.IgnoreDeferred(response.Body.Close)
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxKimiConsoleResponseBytes+1))
	if err != nil {
		return err
	}
	if len(responseBody) > maxKimiConsoleResponseBytes {
		return fmt.Errorf("响应超过大小限制")
	}
	if response.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("HTTP 401（令牌无效或已过期，请从 kimi.com 登录态重新复制 access_token）")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(responseBody, target); err != nil {
		return fmt.Errorf("响应不是有效 JSON: %w", err)
	}
	return nil
}

func (client *KimiConsoleClient) now() time.Time {
	if client != nil && client.Now != nil {
		return client.Now()
	}
	return time.Now()
}
