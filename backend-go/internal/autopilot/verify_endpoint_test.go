package autopilot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestBuildClaudeProbeURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"anthropic 无版本后缀补 /v1/messages", "https://api.xiaomimimo.com/anthropic", "https://api.xiaomimimo.com/anthropic/v1/messages"},
		{"已含 /v1 直接拼 /messages", "https://api.deepseek.com/anthropic/v1", "https://api.deepseek.com/anthropic/v1/messages"},
		{"尾部斜杠归一化", "https://api.moonshot.cn/anthropic/", "https://api.moonshot.cn/anthropic/v1/messages"},
		{"# 结尾跳过版本前缀", "https://custom.example.com/relay#", "https://custom.example.com/relay/messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildClaudeProbeURL(tc.baseURL); got != tc.want {
				t.Errorf("buildClaudeProbeURL(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestBuildOpenAIChatProbeURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"无版本后缀补 /v1/chat/completions", "https://api.xiaomimimo.com", "https://api.xiaomimimo.com/v1/chat/completions"},
		{"已含 /v1 直接拼 /chat/completions", "https://api.xiaomimimo.com/v1", "https://api.xiaomimimo.com/v1/chat/completions"},
		{"尾部斜杠归一化", "https://token-plan-cn.xiaomimimo.com/v1/", "https://token-plan-cn.xiaomimimo.com/v1/chat/completions"},
		{"# 结尾跳过版本前缀", "https://custom.example.com/relay#", "https://custom.example.com/relay/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildOpenAIChatProbeURL(tc.baseURL); got != tc.want {
				t.Errorf("buildOpenAIChatProbeURL(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestBuildResponsesProbeURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"无版本后缀补 /v1/responses", "https://api.example.com", "https://api.example.com/v1/responses"},
		{"已含 /v1 直接拼 /responses", "https://api.example.com/v1", "https://api.example.com/v1/responses"},
		{"完整 Responses 端点不重复拼接", "https://api.example.com/v1/responses", "https://api.example.com/v1/responses"},
		{"# 结尾跳过版本前缀", "https://custom.example.com/relay#", "https://custom.example.com/relay/responses"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildResponsesProbeURL(tc.baseURL); got != tc.want {
				t.Errorf("buildResponsesProbeURL(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestBuildModelsListURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"Anthropic 入口", "https://api.kimi.com/coding", "https://api.kimi.com/coding/v1/models"},
		{"OpenAI 入口", "https://api.kimi.com/coding/v1", "https://api.kimi.com/coding/v1/models"},
		{"尾部斜杠", "https://api.kimi.com/coding/v1/", "https://api.kimi.com/coding/v1/models"},
		{"已有 models", "https://api.kimi.com/coding/v1/models", "https://api.kimi.com/coding/v1/models"},
		{"stepfun Messages 入口", "https://api.stepfun.com/step_plan", "https://api.stepfun.com/step_plan/v1/models"},
		{"stepfun Chat 入口", "https://api.stepfun.com/step_plan/v1", "https://api.stepfun.com/step_plan/v1/models"},
		{"stepfun 尾部斜杠", "https://api.stepfun.com/step_plan/", "https://api.stepfun.com/step_plan/v1/models"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildModelsListURL(tc.baseURL); got != tc.want {
				t.Errorf("buildModelsListURL(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestVerifyModelsListEndpoint(t *testing.T) {
	cases := []struct {
		name           string
		statusCode     int
		wantOK         bool
		wantAuthFailed bool
	}{
		{"200 鉴权通过", http.StatusOK, true, false},
		{"401 鉴权失败", http.StatusUnauthorized, false, true},
		{"403 鉴权失败", http.StatusForbidden, false, true},
		{"500 端点不可用", http.StatusInternalServerError, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var method, path, auth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
				w.WriteHeader(tc.statusCode)
			}))
			defer server.Close()

			res := VerifyModelsListEndpoint(context.Background(), server.URL+"/coding", "sk-kimi-test", "")
			if res.OK != tc.wantOK || res.AuthFailed != tc.wantAuthFailed {
				t.Fatalf("result = %+v, want ok=%v authFailed=%v", res, tc.wantOK, tc.wantAuthFailed)
			}
			if method != http.MethodGet || path != "/coding/v1/models" || auth != "Bearer sk-kimi-test" {
				t.Fatalf("request = method=%q path=%q auth=%q", method, path, auth)
			}
		})
	}
}

func TestVerifyModelsListEndpointStepFunBase(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	res := VerifyModelsListEndpoint(context.Background(), server.URL+"/step_plan", "sk-step-test", "")
	if !res.OK {
		t.Fatalf("result = %+v, want ok", res)
	}
	if path != "/step_plan/v1/models" {
		t.Fatalf("request path = %q, want /step_plan/v1/models", path)
	}
}

func TestIsKimiCodeBaseURL(t *testing.T) {
	for _, tc := range []struct {
		baseURL string
		want    bool
	}{
		{"https://api.kimi.com/coding", true},
		{"https://api.kimi.com/coding/v1", true},
		{"https://api.moonshot.ai/v1", false},
		{"https://api.kimi.com.evil.example/coding", false},
	} {
		if got := isKimiCodeBaseURL(tc.baseURL); got != tc.want {
			t.Errorf("isKimiCodeBaseURL(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

func TestIsStepFunPlanBaseURL(t *testing.T) {
	for _, tc := range []struct {
		baseURL string
		want    bool
	}{
		{"https://api.stepfun.com/step_plan", true},
		{"https://api.stepfun.com/step_plan/", true},
		{"https://api.stepfun.com/step_plan/v1", true},
		{"https://api.stepfun.com/v1", false},
		{"https://evil.example/step_plan", false},
		{"https://api.stepfun.com.evil.example/step_plan", false},
	} {
		if got := isStepFunPlanBaseURL(tc.baseURL); got != tc.want {
			t.Errorf("isStepFunPlanBaseURL(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

func TestModelsListProbeForBaseURL(t *testing.T) {
	for _, tc := range []struct {
		baseURL string
		want    bool
	}{
		{"https://api.stepfun.com/step_plan", true},
		{"https://api.stepfun.com/step_plan/v1", true},
		{"https://api.kimi.com/coding", true},
		{"https://api.stepfun.com/v1", false},
		{"https://api.moonshot.ai/v1", false},
	} {
		if got := modelsListProbeForBaseURL(tc.baseURL); got != tc.want {
			t.Errorf("modelsListProbeForBaseURL(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

func TestVerifyClaudeEndpoint(t *testing.T) {
	cases := []struct {
		name           string
		statusCode     int
		wantOK         bool
		wantAuthFailed bool
	}{
		{"200 鉴权通过", http.StatusOK, true, false},
		{"400 服务可达鉴权通过", http.StatusBadRequest, true, false},
		{"422 服务可达鉴权通过", http.StatusUnprocessableEntity, true, false},
		{"401 鉴权失败", http.StatusUnauthorized, false, true},
		{"403 鉴权失败", http.StatusForbidden, false, true},
		{"404 端点不可用", http.StatusNotFound, false, false},
		{"500 端点不可用", http.StatusInternalServerError, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
					t.Errorf("探测路径应以 /v1/messages 结尾，实际 %q", r.URL.Path)
				}
				w.WriteHeader(tc.statusCode)
			}))
			defer srv.Close()

			res := VerifyClaudeEndpoint(context.Background(), srv.URL, "sk-test", "")
			if res.OK != tc.wantOK {
				t.Errorf("OK = %v, want %v (status %d)", res.OK, tc.wantOK, tc.statusCode)
			}
			if res.AuthFailed != tc.wantAuthFailed {
				t.Errorf("AuthFailed = %v, want %v (status %d)", res.AuthFailed, tc.wantAuthFailed, tc.statusCode)
			}
		})
	}
}

func TestVerifyOpenAIChatEndpoint(t *testing.T) {
	cases := []struct {
		name           string
		statusCode     int
		wantOK         bool
		wantAuthFailed bool
	}{
		{"200 鉴权通过", http.StatusOK, true, false},
		{"400 服务可达鉴权通过", http.StatusBadRequest, true, false},
		{"422 服务可达鉴权通过", http.StatusUnprocessableEntity, true, false},
		{"401 鉴权失败", http.StatusUnauthorized, false, true},
		{"403 鉴权失败", http.StatusForbidden, false, true},
		{"404 端点不可用", http.StatusNotFound, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/v1/chat/completions") {
					t.Errorf("探测路径应以 /v1/chat/completions 结尾，实际 %q", r.URL.Path)
				}
				w.WriteHeader(tc.statusCode)
			}))
			defer srv.Close()

			res := VerifyOpenAIChatEndpoint(context.Background(), srv.URL, "sk-test", "")
			if res.OK != tc.wantOK {
				t.Errorf("OK = %v, want %v (status %d)", res.OK, tc.wantOK, tc.statusCode)
			}
			if res.AuthFailed != tc.wantAuthFailed {
				t.Errorf("AuthFailed = %v, want %v (status %d)", res.AuthFailed, tc.wantAuthFailed, tc.statusCode)
			}
		})
	}
}

func TestVerifyEndpointModelNotFound503(t *testing.T) {
	// new-api 实测响应：占位模型无渠道时返回 503 + model_not_found（鉴权已通过）
	const newAPIModelNotFoundBody = `{"error":{"code":"model_not_found","message":"No available channel for model probe under group default (distributor) (request id: xxx)","type":"new_api_error"}}`
	cases := []struct {
		name           string
		statusCode     int
		body           string
		wantOK         bool
		wantAuthFailed bool
	}{
		{"503 model_not_found 鉴权通过", http.StatusServiceUnavailable, newAPIModelNotFoundBody, true, false},
		{"503 无渠道英文消息鉴权通过", http.StatusServiceUnavailable, `{"error":{"message":"No available channel for model probe"}}`, true, false},
		{"503 普通错误体仍不可用", http.StatusServiceUnavailable, `{"error":{"message":"upstream overloaded"}}`, false, false},
		{"503 空 body 仍不可用", http.StatusServiceUnavailable, ``, false, false},
		{"401 即使 body 匹配也判鉴权失败", http.StatusUnauthorized, newAPIModelNotFoundBody, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			res := VerifyOpenAIChatEndpoint(t.Context(), srv.URL, "sk-test", "")
			if res.OK != tc.wantOK || res.AuthFailed != tc.wantAuthFailed {
				t.Fatalf("result = %+v, want ok=%v authFailed=%v", res, tc.wantOK, tc.wantAuthFailed)
			}
		})
	}
}

func TestVerifyChannelSpecificEndpoints(t *testing.T) {
	var paths []string
	var geminiKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		geminiKey = r.Header.Get("x-goog-api-key")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"status":"INVALID_ARGUMENT","message":"unknown model"}}`))
	}))
	defer server.Close()

	for _, tc := range []struct {
		name string
		call func() EndpointVerifyResult
		path string
	}{
		{"images", func() EndpointVerifyResult { return VerifyImagesEndpoint(t.Context(), server.URL, "sk-test", "") }, "/v1/images/generations"},
		{"vectors", func() EndpointVerifyResult { return VerifyVectorsEndpoint(t.Context(), server.URL, "sk-test", "") }, "/v1/embeddings"},
		{"gemini", func() EndpointVerifyResult { return VerifyGeminiEndpoint(t.Context(), server.URL, "gemini-test", "") }, "/v1beta/models/gemini-2.5-flash:generateContent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths = nil
			geminiKey = ""
			if result := tc.call(); !result.OK {
				t.Fatalf("HTTP 400 应表示鉴权通过: %+v", result)
			}
			if len(paths) != 1 || paths[0] != tc.path {
				t.Fatalf("探测路径=%v, want %s", paths, tc.path)
			}
			if tc.name == "gemini" && geminiKey != "gemini-test" {
				t.Fatalf("Gemini 认证头=%q", geminiKey)
			}
		})
	}
}

func TestVerifyGeminiEndpointRejectsInvalidAPIKeyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"status":"INVALID_ARGUMENT","message":"API key not valid. Please pass a valid API key.","details":[{"reason":"API_KEY_INVALID"}]}}`))
	}))
	defer server.Close()

	result := VerifyGeminiEndpoint(t.Context(), server.URL, "invalid-key", "")
	if result.OK || !result.AuthFailed {
		t.Fatalf("无效 Gemini Key 不应通过验证: %+v", result)
	}
}

func TestVerifyChannelKeyUsesBoundBaseURL(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()

	bound := config.UpstreamConfig{
		BaseURL: bad.URL, BaseURLs: []string{bad.URL, good.URL},
		APIKeys: []string{"sk-new"}, APIKeyConfigs: []config.APIKeyConfig{{Key: "sk-new", BaseURL: bad.URL}},
	}
	if err := verifyChannelKey(t.Context(), "chat", bound, "sk-new"); err == nil {
		t.Fatal("绑定地址鉴权失败时，不应回退未绑定的其他地址")
	}

	// 未绑定 Key 时验证地址池采用「任一候选通过即放行」策略（与 verifyProviderRouteKeys 对齐），
	// 避免个别候选超时/抽风阻断保存；全部失败时再按鉴权/非鉴权分类。
	unbound := config.UpstreamConfig{BaseURL: bad.URL, BaseURLs: []string{bad.URL, good.URL}}
	if err := verifyChannelKey(t.Context(), "chat", unbound, "sk-new"); err != nil {
		t.Fatalf("地址池中存在可用候选时应验证通过: %v", err)
	}
}

func TestVerifyChannelKeyFailureClassification(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authSrv.Close()
	authSrv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer authSrv2.Close()

	t.Run("全部候选鉴权失败标记 AuthFailed", func(t *testing.T) {
		upstream := config.UpstreamConfig{BaseURL: authSrv.URL, BaseURLs: []string{authSrv.URL, authSrv2.URL}}
		err := verifyChannelKey(t.Context(), "chat", upstream, "sk-bad")
		var kvErr *KeyVerifyError
		if !errors.As(err, &kvErr) {
			t.Fatalf("应返回 *KeyVerifyError: %v", err)
		}
		if !kvErr.AuthFailed {
			t.Fatalf("全部候选 401/403 时 AuthFailed 应为 true: %+v", kvErr)
		}
		if !strings.Contains(kvErr.Error(), "POST /v1/chat/completions") {
			t.Fatalf("错误消息应注明探测方式: %v", kvErr)
		}
	})

	t.Run("含非鉴权失败不标记 AuthFailed", func(t *testing.T) {
		// 一个候选 401，另一个不可达（连接拒绝）：key 未必无效，仅探测未通过
		upstream := config.UpstreamConfig{BaseURL: authSrv.URL, BaseURLs: []string{authSrv.URL, "http://127.0.0.1:1"}}
		err := verifyChannelKey(t.Context(), "chat", upstream, "sk-maybe")
		var kvErr *KeyVerifyError
		if !errors.As(err, &kvErr) {
			t.Fatalf("应返回 *KeyVerifyError: %v", err)
		}
		if kvErr.AuthFailed {
			t.Fatalf("混合失败不应判定为纯鉴权失败: %+v", kvErr)
		}
	})

	t.Run("任一候选通过即放行", func(t *testing.T) {
		okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer okSrv.Close()
		upstream := config.UpstreamConfig{BaseURL: "http://127.0.0.1:1", BaseURLs: []string{"http://127.0.0.1:1", okSrv.URL}}
		if err := verifyChannelKey(t.Context(), "chat", upstream, "sk-new"); err != nil {
			t.Fatalf("存在可用候选时应通过: %v", err)
		}
	})
}

func TestVerifyClaudeEndpointNetworkError(t *testing.T) {
	// 指向一个不可达地址，期望 Err 非空、OK=false
	res := VerifyClaudeEndpoint(context.Background(), "http://127.0.0.1:1/anthropic", "sk-test", "")
	if res.OK {
		t.Error("网络错误时 OK 应为 false")
	}
	if res.Err == nil {
		t.Error("网络错误时 Err 应非空")
	}
}

func TestVerifyProviderKeys(t *testing.T) {
	// okSrv 恒返回 200（鉴权通过），authSrv 恒返回 401（鉴权失败）
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authSrv.Close()

	t.Run("per-key 按前缀绑定候选端点", func(t *testing.T) {
		tmpl := &config.ProviderTemplate{
			ProviderID:  "test",
			ServiceType: "claude",
			KeyPrefixRules: []config.KeyPrefixRule{
				{Prefix: "sk-", PlanTag: "payg"},
				{Prefix: "tp-", PlanTag: "token_plan"},
			},
			Candidates: []config.ProviderCandidate{
				{BaseURL: okSrv.URL + "/anthropic", PlanTag: "payg", Priority: 0},
				{BaseURL: authSrv.URL + "/anthropic", PlanTag: "token_plan", Priority: 0},
			},
		}
		// sk- key 命中 payg 候选（okSrv，200 通过）；tp- key 命中 token_plan 候选后回退到 payg 候选（okSrv）
		keyConfigs, baseURLs, err := verifyProviderKeys(context.Background(), tmpl, []string{"sk-a", "tp-b"})
		if err != nil {
			t.Fatalf("verifyProviderKeys 意外失败: %v", err)
		}
		if len(keyConfigs) != 2 {
			t.Fatalf("keyConfigs 数量 = %d, want 2", len(keyConfigs))
		}
		wantURL := okSrv.URL + "/anthropic"
		// sk-a 绑定 okSrv
		if keyConfigs[0].Key != "sk-a" || keyConfigs[0].BaseURL != wantURL {
			t.Errorf("keyConfigs[0] = %+v, want key=sk-a baseURL=%s", keyConfigs[0], wantURL)
		}
		// tp-b 首选 token_plan 候选（authSrv 401）失败后回退到 payg 候选（okSrv 200）
		if keyConfigs[1].Key != "tp-b" || keyConfigs[1].BaseURL != wantURL {
			t.Errorf("keyConfigs[1] = %+v, want key=tp-b baseURL=%s", keyConfigs[1], wantURL)
		}
		// 两 key 均绑定 okSrv，去重后仅 1 个渠道级 baseURL
		if len(baseURLs) != 1 || baseURLs[0] != wantURL {
			t.Errorf("baseURLs = %v, want [%s]", baseURLs, wantURL)
		}
	})

	t.Run("全部候选鉴权失败时报错", func(t *testing.T) {
		tmpl := &config.ProviderTemplate{
			ProviderID:  "test",
			ServiceType: "claude",
			Candidates: []config.ProviderCandidate{
				{BaseURL: authSrv.URL + "/anthropic", Priority: 0},
			},
		}
		_, _, err := verifyProviderKeys(context.Background(), tmpl, []string{"sk-bad"})
		if err == nil {
			t.Fatal("所有候选鉴权失败时应返回错误")
		}
	})

	t.Run("openai route 使用 chat completions 探测", func(t *testing.T) {
		var gotPath string
		chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		}))
		defer chatSrv.Close()

		tmpl := &config.ProviderTemplate{ProviderID: "x"}
		route := config.ProviderRoute{
			ChannelKind: "chat",
			ServiceType: "openai",
			Candidates:  []config.ProviderCandidate{{BaseURL: chatSrv.URL + "/v1", Priority: 0}},
		}
		keyConfigs, baseURLs, err := verifyProviderRouteKeys(context.Background(), tmpl, route, []string{"sk-a"})
		if err != nil {
			t.Fatalf("openai route 应支持模板化验证: %v", err)
		}
		if gotPath != "/v1/chat/completions" {
			t.Fatalf("openai route 探测路径=%q, want /v1/chat/completions", gotPath)
		}
		if len(keyConfigs) != 1 || len(baseURLs) != 1 || baseURLs[0] != chatSrv.URL+"/v1" {
			t.Fatalf("验证结果不符合预期: keyConfigs=%+v baseURLs=%v", keyConfigs, baseURLs)
		}
	})

	t.Run("responses route 使用原生 responses 探测", func(t *testing.T) {
		var gotPath string
		responsesSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		}))
		defer responsesSrv.Close()

		tmpl := &config.ProviderTemplate{ProviderID: "xfyun"}
		route := config.ProviderRoute{
			ChannelKind: "responses",
			ServiceType: "responses",
			Candidates: []config.ProviderCandidate{{
				BaseURL: responsesSrv.URL + "/v1/responses",
			}},
		}
		keyConfigs, baseURLs, err := verifyProviderRouteKeys(context.Background(), tmpl, route, []string{"sk-a"})
		if err != nil {
			t.Fatalf("responses route 应支持模板化验证: %v", err)
		}
		if gotPath != "/v1/responses" {
			t.Fatalf("responses route 探测路径=%q, want /v1/responses", gotPath)
		}
		if len(keyConfigs) != 1 || len(baseURLs) != 1 || baseURLs[0] != responsesSrv.URL+"/v1/responses" {
			t.Fatalf("验证结果不符合预期: keyConfigs=%+v baseURLs=%v", keyConfigs, baseURLs)
		}
	})

	t.Run("火山套餐必须获得真实成功响应才绑定端点", func(t *testing.T) {
		var paths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			if strings.Contains(r.URL.Path, "/api/plan/") {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		tmpl := &config.ProviderTemplate{ProviderID: "volcengine"}
		route := config.ProviderRoute{
			ChannelKind: "chat",
			ServiceType: "openai",
			Candidates: []config.ProviderCandidate{
				{BaseURL: server.URL + "/api/plan/v3", Priority: 0},
				{BaseURL: server.URL + "/api/coding/v3", Priority: 1},
			},
		}
		configs, _, err := verifyProviderRouteKeys(context.Background(), tmpl, route, []string{"ark-test"})
		if err != nil {
			t.Fatal(err)
		}
		if len(configs) != 1 || configs[0].BaseURL != server.URL+"/api/coding/v3" {
			t.Fatalf("错误绑定套餐端点: %+v", configs)
		}
		if len(paths) != 2 || paths[0] != "/api/plan/v3/chat/completions" || paths[1] != "/api/coding/v3/chat/completions" {
			t.Fatalf("探测路径=%v", paths)
		}
	})

	t.Run("火山 Claude 验证使用 Claude Code 请求特征", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/plan/v1/messages" {
				http.NotFound(w, r)
				return
			}
			if r.Header.Get("Authorization") != "Bearer ark-test" ||
				!strings.HasPrefix(r.Header.Get("User-Agent"), "claude-cli/") ||
				r.Header.Get("X-App") != "cli" ||
				r.Header.Get("anthropic-beta") == "" ||
				r.Header.Get("anthropic-dangerous-direct-browser-access") != "true" {
				http.Error(w, "Claude Code request fingerprint required", http.StatusForbidden)
				return
			}

			var body struct {
				Model  string `json:"model"`
				System []struct {
					Text string `json:"text"`
				} `json:"system"`
				Metadata struct {
					UserID string `json:"user_id"`
				} `json:"metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body", http.StatusBadRequest)
				return
			}
			if body.Model != "deepseek-v4-flash" || len(body.System) < 2 ||
				!strings.HasPrefix(body.System[0].Text, "x-anthropic-billing-header") ||
				!strings.HasPrefix(body.System[1].Text, "You are Claude Code,") {
				http.Error(w, "Claude Code identity required", http.StatusForbidden)
				return
			}
			var userID struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal([]byte(body.Metadata.UserID), &userID); err != nil || userID.SessionID == "" ||
				r.Header.Get("X-Claude-Code-Session-Id") != userID.SessionID {
				http.Error(w, "invalid Claude Code session", http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		tmpl := &config.ProviderTemplate{ProviderID: "volcengine"}
		route := config.ProviderRoute{
			ChannelKind: "messages",
			ServiceType: "claude",
			Candidates:  []config.ProviderCandidate{{BaseURL: server.URL + "/api/plan"}},
		}
		configs, _, err := verifyProviderRouteKeys(context.Background(), tmpl, route, []string{"ark-test"})
		if err != nil {
			t.Fatalf("火山 Agent Plan 验证应使用兼容请求特征: %v", err)
		}
		if len(configs) != 1 || configs[0].BaseURL != server.URL+"/api/plan" {
			t.Fatalf("验证绑定结果=%+v", configs)
		}
	})

	t.Run("混合失败不误报所有候选均鉴权失败", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/api/plan/") {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		tmpl := &config.ProviderTemplate{ProviderID: "volcengine"}
		route := config.ProviderRoute{
			ChannelKind: "chat",
			ServiceType: "openai",
			Candidates: []config.ProviderCandidate{
				{BaseURL: server.URL + "/api/plan/v3"},
				{BaseURL: server.URL + "/api/coding/v3"},
			},
		}
		_, _, err := verifyProviderRouteKeys(context.Background(), tmpl, route, []string{"ark-test"})
		if err == nil {
			t.Fatal("混合失败时应返回错误")
		}
		if strings.Contains(err.Error(), "所有候选端点均返回 401/403") ||
			!strings.Contains(err.Error(), "候选 1: HTTP 403") ||
			!strings.Contains(err.Error(), "候选 2: HTTP 404") {
			t.Fatalf("错误诊断=%q", err)
		}
	})

	t.Run("不支持的 serviceType 拒绝", func(t *testing.T) {
		tmpl := &config.ProviderTemplate{ProviderID: "x"}
		route := config.ProviderRoute{ChannelKind: "gemini", ServiceType: "gemini"}
		if _, _, err := verifyProviderRouteKeys(context.Background(), tmpl, route, []string{"sk-a"}); err == nil {
			t.Fatal("不支持的 serviceType 应返回错误")
		}
	})
}
