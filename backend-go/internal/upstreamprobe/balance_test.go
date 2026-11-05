package upstreamprobe

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func floatPtr(v float64) *float64 { return &v }

// newBalanceTestServer 按路径分发固定响应的余额探测测试服务器。
func newBalanceTestServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, h)
	}
	return httptest.NewServer(mux)
}

func requireBearerKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"Invalid API key"}}`))
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func TestProbeNewapiTokenUsage(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantIdent  bool
		wantUnltd  bool
		wantRemain *float64
		wantErr    bool
	}{
		{
			name: "正常 key 级点数",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"code":true,"message":"ok","data":{"object":"token_usage","name":"k1","total_granted":5000000,"total_used":1000000,"total_available":4000000,"unlimited_quota":false}}`)
			},
			wantIdent:  true,
			wantRemain: floatPtr(4000000),
		},
		{
			name: "无限额度哨兵",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"code":true,"data":{"object":"token_usage","total_available":0,"unlimited_quota":true}}`)
			},
			wantIdent: true,
			wantUnltd: true,
		},
		{
			name: "无效 key 401",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 401, `{"success":false,"message":"无效的令牌"}`)
			},
			wantErr: true,
		},
		{
			name: "200 但 HTML 错误页不误判",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`<html><body>Not Found</body></html>`))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newBalanceTestServer(t, map[string]http.HandlerFunc{"/api/usage/token/": requireBearerKey(tt.handler)})
			defer srv.Close()
			res := probeNewapiTokenUsage(BalanceProbeRequest{BaseURL: srv.URL, APIKey: "sk-test-key"})
			if res.Identified != tt.wantIdent {
				t.Fatalf("Identified = %v, want %v (err=%v)", res.Identified, tt.wantIdent, res.Err)
			}
			if res.Unlimited != tt.wantUnltd {
				t.Errorf("Unlimited = %v, want %v", res.Unlimited, tt.wantUnltd)
			}
			if tt.wantRemain != nil {
				if res.Remaining == nil || *res.Remaining != *tt.wantRemain {
					t.Errorf("Remaining = %v, want %v", res.Remaining, *tt.wantRemain)
				}
				if res.RawCredits == nil || *res.RawCredits != *tt.wantRemain {
					t.Errorf("RawCredits = %v, want %v", res.RawCredits, *tt.wantRemain)
				}
			}
			if (res.Err != nil) != tt.wantErr {
				t.Errorf("Err = %v, wantErr %v", res.Err, tt.wantErr)
			}
		})
	}
}

func TestProbeSub2apiUsage(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantIdent  bool
		wantScope  string
		wantRemain *float64
		wantUnit   string
		wantErr    bool
	}{
		{
			name: "quota_limited key 级",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"mode":"quota_limited","isValid":true,"quota":{"limit":100,"used":30,"remaining":70,"unit":"USD"},"remaining":70,"unit":"USD","rate_limits":[{"window":"5h","limit":1000,"used":400,"remaining":600}]}`)
			},
			wantIdent:  true,
			wantScope:  "key",
			wantRemain: floatPtr(70),
			wantUnit:   "USD",
		},
		{
			name: "unrestricted 钱包余额",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"mode":"unrestricted","isValid":true,"planName":"钱包余额","remaining":12.5,"unit":"USD","balance":12.5}`)
			},
			wantIdent:  true,
			wantScope:  "wallet",
			wantRemain: floatPtr(12.5),
			wantUnit:   "USD",
		},
		{
			name: "unrestricted 订阅无 remaining 也算识别成功",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"mode":"unrestricted","isValid":true,"planName":"Pro","unit":"USD"}`)
			},
			wantIdent: true,
			wantScope: "subscription",
			wantUnit:  "USD",
		},
		{
			name: "无效 key 401",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 401, `{"error":{"type":"authentication_error","message":"Invalid API key"}}`)
			},
			wantErr:   true,
			wantUnit:  "USD", // 错误路径保持初始化值
			wantScope: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newBalanceTestServer(t, map[string]http.HandlerFunc{"/v1/usage": requireBearerKey(tt.handler)})
			defer srv.Close()
			res := probeSub2apiUsage(BalanceProbeRequest{BaseURL: srv.URL, APIKey: "sk-test-key"})
			if res.Identified != tt.wantIdent {
				t.Fatalf("Identified = %v, want %v (err=%v)", res.Identified, tt.wantIdent, res.Err)
			}
			if res.Scope != tt.wantScope {
				t.Errorf("Scope = %q, want %q", res.Scope, tt.wantScope)
			}
			if tt.wantRemain != nil && (res.Remaining == nil || *res.Remaining != *tt.wantRemain) {
				t.Errorf("Remaining = %v, want %v", res.Remaining, *tt.wantRemain)
			}
			if res.OriginalUnit != tt.wantUnit {
				t.Errorf("OriginalUnit = %q, want %q", res.OriginalUnit, tt.wantUnit)
			}
			if (res.Err != nil) != tt.wantErr {
				t.Errorf("Err = %v, wantErr %v", res.Err, tt.wantErr)
			}
		})
	}
}

func TestProbeNewapiBilling(t *testing.T) {
	newServer := func(t *testing.T, hardLimit, totalUsage string, usageStatus int) *httptest.Server {
		return newBalanceTestServer(t, map[string]http.HandlerFunc{
			"/v1/dashboard/billing/subscription": requireBearerKey(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, fmt.Sprintf(`{"object":"billing_subscription","has_payment_method":true,"soft_limit_usd":%s,"hard_limit_usd":%s,"system_hard_limit_usd":%s,"access_until":0}`, hardLimit, hardLimit, hardLimit))
			}),
			"/v1/dashboard/billing/usage": requireBearerKey(func(w http.ResponseWriter, r *http.Request) {
				if usageStatus != 200 {
					writeJSON(w, usageStatus, `{"error":{"message":"boom"}}`)
					return
				}
				writeJSON(w, 200, fmt.Sprintf(`{"object":"list","total_usage":%s}`, totalUsage))
			}),
		})
	}
	t.Run("正常减法", func(t *testing.T) {
		srv := newServer(t, "10", "2500", 200) // hard=10, used=25.00 → remaining=-15? 不：2500/100=25
		defer srv.Close()
		res := probeNewapiBilling(BalanceProbeRequest{BaseURL: srv.URL, APIKey: "sk-test-key"})
		if !res.Identified {
			t.Fatalf("Identified = false, err=%v", res.Err)
		}
		if res.Remaining == nil || *res.Remaining != -15 {
			t.Errorf("Remaining = %v, want -15", res.Remaining)
		}
	})
	t.Run("无限额度哨兵 1e8", func(t *testing.T) {
		srv := newServer(t, "100000000", "0", 200)
		defer srv.Close()
		res := probeNewapiBilling(BalanceProbeRequest{BaseURL: srv.URL, APIKey: "sk-test-key"})
		if !res.Identified || !res.Unlimited {
			t.Fatalf("Identified=%v Unlimited=%v, want true/true (err=%v)", res.Identified, res.Unlimited, res.Err)
		}
	})
	t.Run("usage 端点失败则整体失败", func(t *testing.T) {
		srv := newServer(t, "10", "0", 500)
		defer srv.Close()
		res := probeNewapiBilling(BalanceProbeRequest{BaseURL: srv.URL, APIKey: "sk-test-key"})
		if res.Identified || res.Err == nil {
			t.Fatalf("usage 失败应返回错误，got Identified=%v Err=%v", res.Identified, res.Err)
		}
	})
}

func TestProbeBalanceAutoOrder(t *testing.T) {
	t.Run("无端点匹配时全序列失败", func(t *testing.T) {
		srv := newBalanceTestServer(t, map[string]http.HandlerFunc{"/": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 404, `{"error":"not found"}`)
		}})
		defer srv.Close()
		res := ProbeBalance(t.Context(), BalanceProbeRequest{BaseURL: srv.URL, APIKey: "sk-test-key"}, "auto")
		if res.Identified || res.Err == nil {
			t.Fatalf("全 404 应失败, got Identified=%v Err=%v", res.Identified, res.Err)
		}
	})
	t.Run("仅 sub2api 端点存在时按序命中", func(t *testing.T) {
		srv := newBalanceTestServer(t, map[string]http.HandlerFunc{
			"/v1/usage": requireBearerKey(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"mode":"quota_limited","quota":{"limit":10,"used":2,"remaining":8,"unit":"USD"}}`)
			}),
		})
		defer srv.Close()
		res := ProbeBalance(t.Context(), BalanceProbeRequest{BaseURL: srv.URL, APIKey: "sk-test-key"}, "auto")
		if !res.Identified || res.Provider != "sub2api" {
			t.Fatalf("Provider=%s Identified=%v, want sub2api/true (err=%v)", res.Provider, res.Identified, res.Err)
		}
		if res.Remaining == nil || *res.Remaining != 8 {
			t.Errorf("Remaining = %v, want 8", res.Remaining)
		}
	})
	t.Run("preferred 优先命中避免全序列", func(t *testing.T) {
		var tokenHits, billingHits int
		srv := newBalanceTestServer(t, map[string]http.HandlerFunc{
			"/api/usage/token/": func(w http.ResponseWriter, r *http.Request) {
				tokenHits++
				writeJSON(w, 500, `{"error":"server error"}`)
			},
			"/v1/dashboard/billing/subscription": requireBearerKey(func(w http.ResponseWriter, r *http.Request) {
				billingHits++
				writeJSON(w, 200, `{"object":"billing_subscription","hard_limit_usd":5}`)
			}),
			"/v1/dashboard/billing/usage": requireBearerKey(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"object":"list","total_usage":100}`) // used=1 → remaining=4
			}),
		})
		defer srv.Close()
		res := ProbeBalance(t.Context(), BalanceProbeRequest{BaseURL: srv.URL, APIKey: "sk-test-key"}, "auto", "newapi_billing")
		if !res.Identified || res.Provider != "newapi_billing" {
			t.Fatalf("Provider=%s Identified=%v, want newapi_billing/true (err=%v)", res.Provider, res.Identified, res.Err)
		}
		if tokenHits != 0 {
			t.Errorf("preferred 命中后不应再试 newapi_token, hits=%d", tokenHits)
		}
		if res.Remaining == nil || *res.Remaining != 4 {
			t.Errorf("Remaining = %v, want 4", res.Remaining)
		}
		_ = billingHits
	})
	t.Run("手动指定 provider 失败不回退", func(t *testing.T) {
		srv := newBalanceTestServer(t, map[string]http.HandlerFunc{
			"/v1/usage": requireBearerKey(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, 200, `{"mode":"quota_limited","quota":{"remaining":8,"unit":"USD"}}`)
			}),
		})
		defer srv.Close()
		res := ProbeBalance(t.Context(), BalanceProbeRequest{BaseURL: srv.URL, APIKey: "sk-test-key"}, "newapi_token")
		if res.Identified || res.Err == nil {
			t.Fatalf("手动 newapi_token 失败不应回退 sub2api, got Identified=%v", res.Identified)
		}
	})
	t.Run("指纹拦截时带探针头重试", func(t *testing.T) {
		var attempts int
		srv := newBalanceTestServer(t, map[string]http.HandlerFunc{"/api/usage/token/": func(w http.ResponseWriter, r *http.Request) {
			attempts++
			// Go 客户端默认自带 User-Agent，用探针头独有字段区分首发与重试。
			if r.Header.Get("anthropic-version") == "" {
				writeJSON(w, 403, `{"error":{"message":"Unauthorized client detected"}}`)
				return
			}
			writeJSON(w, 200, `{"code":true,"data":{"object":"token_usage","total_available":100,"unlimited_quota":false}}`)
		}})
		defer srv.Close()
		res := ProbeBalance(t.Context(), BalanceProbeRequest{BaseURL: srv.URL, APIKey: "sk-test-key"}, "auto")
		if !res.Identified {
			t.Fatalf("指纹重试后应成功, err=%v", res.Err)
		}
		if attempts != 2 {
			t.Errorf("attempts = %d, want 2", attempts)
		}
	})
}

func TestBalanceAPIRoot(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://x.com", "https://x.com"},
		{"https://x.com/", "https://x.com"},
		{"https://x.com/v1", "https://x.com"},
		{"https://x.com/v1/", "https://x.com"},
		{"https://x.com/v1beta", "https://x.com/v1beta"},
		{" https://x.com# ", "https://x.com"},
	}
	for _, tt := range tests {
		if got := balanceAPIRoot(tt.in); got != tt.want {
			t.Errorf("balanceAPIRoot(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
