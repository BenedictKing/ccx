package autopilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestKimiConsoleClientVerify(t *testing.T) {
	now := time.Date(2026, 7, 21, 3, 0, 0, 0, time.UTC)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer test-access-token" {
			t.Fatalf("请求认证错误: %s %s", r.Method, r.Header.Get("Authorization"))
		}
		if r.Header.Get("Connect-Protocol-Version") != "1" || r.Header.Get("X-Msh-Platform") != "web" {
			t.Fatalf("Connect 请求头缺失: %+v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case kimiUsagesPath:
			var body struct {
				Scope []string `json:"scope"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if strings.Join(body.Scope, ",") != "FEATURE_CODING" {
				t.Fatalf("scope=%v", body.Scope)
			}
			_, _ = w.Write([]byte(`{
  "usages": [{
    "scope": "FEATURE_CODING",
    "detail": {"limit":"100","remaining":"75","resetTime":"2026-07-27T16:13:43.478258Z"},
    "limits": [{
      "window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},
      "detail":{"limit":"100","remaining":"90","resetTime":"2026-07-21T02:13:43.478258Z"}
    }]
  }],
  "totalQuota":{"limit":"100","remaining":"75"}
}`))
		case kimiSubscriptionStatsPath:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 0 {
				t.Fatalf("订阅统计请求体应为空对象: %v", body)
			}
			_, _ = w.Write([]byte(`{
  "ratelimitCode5h":{"ratio":0.1,"enabled":true,"resetTime":"2026-07-21T02:13:43.755984654Z"},
  "ratelimitCode7d":{"ratio":0.25,"enabled":true,"resetTime":"2026-07-27T16:13:42.755984654Z"},
  "subscriptionBalance":{
    "feature":"FEATURE_OMNI","type":"SUBSCRIPTION","unit":"UNIT_CREDIT",
    "amountUsedRatio":0.2,"kimiCodeUsedRatio":0.15,"expireTime":"2026-08-20T16:14:07.824348Z"
  },
  "giftBalances":[{
    "feature":"FEATURE_OMNI","type":"GIFT","unit":"UNIT_CREDIT",
    "amountUsedRatio":0,"kimiCodeUsedRatio":0,"expireTime":"2026-12-31T15:59:59Z"
  }],
  "boosterWallets":[{
    "id":"wallet-1","status":"STATUS_ACTIVE","allowTopup":true,
    "moneyLeft":{"currency":"CNY","priceInCents":"4912"},
    "moneyTotal":{"currency":"CNY","priceInCents":"5000"},
    "monthlyChargeLimit":{"currency":"CNY","priceInCents":"10000"},
    "monthlyUsed":{"currency":"CNY","priceInCents":"88"}
  }]
}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &KimiConsoleClient{
		HTTPClient: server.Client(), BaseURL: server.URL, Now: func() time.Time { return now },
	}
	credential, err := client.Verify(context.Background(), "Authorization: Bearer test-access-token")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || credential.AccessToken != "test-access-token" {
		t.Fatalf("请求数或令牌错误: requests=%d credential=%+v", requests, credential)
	}
	usage := credential.Usage
	if usage.WeeklyUsage.Used != 25 || usage.WeeklyUsage.Remaining != 75 || usage.TotalQuota.Limit != 100 {
		t.Fatalf("周额度解析错误: %+v", usage)
	}
	if len(usage.RateLimits) != 1 || usage.RateLimits[0].WindowSeconds != 5*60*60 || usage.RateLimits[0].Usage.Used != 10 {
		t.Fatalf("频限解析错误: %+v", usage.RateLimits)
	}
	if usage.CodeFiveHour == nil || usage.CodeFiveHour.Ratio != 0.1 || usage.CodeSevenDay == nil || usage.CodeSevenDay.Ratio != 0.25 {
		t.Fatalf("订阅频限解析错误: %+v", usage)
	}
	if usage.SubscriptionBalance == nil || usage.SubscriptionBalance.KimiCodeUsedRatio != 0.15 || len(usage.GiftBalances) != 1 {
		t.Fatalf("订阅余额解析错误: %+v", usage)
	}
	if len(usage.BoosterWallets) != 1 {
		t.Fatalf("加油包解析错误: %+v", usage.BoosterWallets)
	}
	wallet := usage.BoosterWallets[0]
	if wallet.ID != "wallet-1" || wallet.Status != "STATUS_ACTIVE" || !wallet.AllowTopup {
		t.Fatalf("加油包元信息解析错误: %+v", wallet)
	}
	if wallet.MoneyLeft.PriceInCents != 4912 || wallet.MoneyTotal.PriceInCents != 5000 || wallet.MonthlyUsed.PriceInCents != 88 {
		t.Fatalf("加油包金额解析错误: %+v", wallet)
	}
	if !usage.ValidatedAt.Equal(now) {
		t.Fatalf("ValidatedAt=%s, want %s", usage.ValidatedAt, now)
	}
}

func TestKimiConsoleClientRejectsInvalidToken(t *testing.T) {
	for _, token := range []string{"", "Bearer ", "token with spaces", "token\nvalue"} {
		if _, err := (&KimiConsoleClient{}).Verify(context.Background(), token); err == nil {
			t.Fatalf("无效令牌应被拒绝: %q", token)
		}
	}
}

func TestNormalizeKimiConsoleTokenFormats(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"裸令牌", "tok", "tok"},
		{"Bearer 前缀", "Bearer tok", "tok"},
		{"Authorization 头", "Authorization: Bearer tok", "tok"},
		{"小写前缀", "authorization: bearer tok", "tok"},
		{"前后空白", "  tok  ", "tok"},
		{"带引号", `"tok"`, "tok"},
		{"带引号 Bearer", `"Bearer tok"`, "tok"},
		{"Cookie 键值", "access_token=tok", "tok"},
		{"Cookie 带属性", "access_token=tok; Path=/; HttpOnly", "tok"},
		{"完整 Cookie 串", "device_id=1; access_token=tok; session_id=2", "tok"},
		{"URL 编码", "access_token=tok%2B123", "tok+123"},
		{"localStorage JSON", `{"access_token":"tok","refresh_token":"r"}`, "tok"},
		{"localStorage JSON camelCase", `{"accessToken":"tok"}`, "tok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeKimiConsoleToken(tc.raw)
			if err != nil {
				t.Fatalf("应解析成功: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeKimiConsoleTokenRejects(t *testing.T) {
	for _, raw := range []string{"", "  ", "Bearer ", "access_token=", "token with spaces", "token\tvalue", "token\nvalue"} {
		if _, err := normalizeKimiConsoleToken(raw); err == nil {
			t.Fatalf("无效令牌应被拒绝: %q", raw)
		}
	}
}

func TestKimiConsoleClientToleratesMissingCodingUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == kimiUsagesPath {
			_, _ = w.Write([]byte(`{"usages":[],"totalQuota":{"limit":"0","remaining":"0"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ratelimitCode7d":{"ratio":0.4,"enabled":true,"resetTime":"2026-07-27T16:13:42Z"}}`))
	}))
	defer server.Close()

	credential, err := (&KimiConsoleClient{HTTPClient: server.Client(), BaseURL: server.URL}).Verify(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("缺少 FEATURE_CODING 不应再失败: %v", err)
	}
	usage := credential.Usage
	if usage.WeeklyUsage.Limit != 0 || len(usage.RateLimits) != 0 {
		t.Fatalf("缺少 FEATURE_CODING 时不应填充周额度: %+v", usage)
	}
	if usage.CodeSevenDay == nil || usage.CodeSevenDay.Ratio != 0.4 {
		t.Fatalf("订阅频限解析错误: %+v", usage)
	}
}

func TestKimiConsoleClientToleratesUsagesFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == kimiUsagesPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"ratelimitCode5h":{"ratio":0.1,"enabled":true,"resetTime":"2026-07-21T02:13:43Z"}}`))
	}))
	defer server.Close()

	credential, err := (&KimiConsoleClient{HTTPClient: server.Client(), BaseURL: server.URL}).Verify(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("GetUsages 失败不应影响绑定: %v", err)
	}
	if credential.Usage.CodeFiveHour == nil || credential.Usage.CodeFiveHour.Ratio != 0.1 {
		t.Fatalf("订阅频限解析错误: %+v", credential.Usage)
	}
}
