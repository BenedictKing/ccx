package upstreamprobe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsVolcenginePlanBaseURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"agent plan claude", "https://ark.cn-beijing.volces.com/api/plan", true},
		{"coding plan claude", "https://ark.cn-beijing.volces.com/api/coding", true},
		{"agent plan openai v3", "https://ark.cn-beijing.volces.com/api/plan/v3", true},
		{"coding plan openai v3", "https://ark.cn-beijing.volces.com/api/coding/v3", true},
		{"trailing slash", "https://ark.cn-beijing.volces.com/api/plan/", true},
		{"hash suffix skip version", "https://ark.cn-beijing.volces.com/api/plan#", true},
		{"regular ark endpoint not matched", "https://ark.cn-beijing.volces.com/api/v3", false},
		{"relay station not matched by substring", "https://relay.example.com/api/plan", false},
		{"unrelated host", "https://api.anthropic.com/v1", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsVolcenginePlanBaseURL(tt.url); got != tt.want {
				t.Fatalf("IsVolcenginePlanBaseURL(%q) = %v, 期望 %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestVolcenginePlanProbeModel(t *testing.T) {
	agentPlanURL := "https://ark.cn-beijing.volces.com/api/plan"
	codingURL := "https://ark.cn-beijing.volces.com/api/coding"

	// 无候选时回退常量
	if got := volcenginePlanProbeModel(agentPlanURL, nil); got != "deepseek-v4-flash" {
		t.Fatalf("无候选时探针模型 = %q, 期望 deepseek-v4-flash", got)
	}
	// deepseek-v4-flash 存在时优先使用
	if got := volcenginePlanProbeModel(agentPlanURL, []string{"kimi-k3", "deepseek-v4-flash", "glm-5.2"}); got != "deepseek-v4-flash" {
		t.Fatalf("含 flash 时应优先 = %q, 期望 deepseek-v4-flash", got)
	}
	// Agent Plan 无 flash 时按 AFP 选最便宜（glm-5.2 当前有 ×0.25 活动，成本低于 pro）
	if got := volcenginePlanProbeModel(agentPlanURL, []string{"kimi-k3", "deepseek-v4-pro", "glm-5.2"}); got != "glm-5.2" {
		t.Fatalf("Agent Plan 最便宜模型 = %q, 期望 glm-5.2", got)
	}
	// Coding Plan 无 flash 时回退首个候选
	if got := volcenginePlanProbeModel(codingURL, []string{"kimi-k3", "deepseek-v4-pro"}); got != "kimi-k3" {
		t.Fatalf("Coding Plan 首个候选 = %q, 期望 kimi-k3", got)
	}
}

func TestProbeVolcenginePlanWithModels(t *testing.T) {
	srv := newCaptureServer(200, `{"id":"msg_1"}`)
	defer srv.Close()

	res := ProbeVolcenginePlanWithModels(context.Background(), "claude", srv.URL, "ark-key", "", []string{"kimi-k3"})
	if !res.OK || res.Err != nil {
		t.Fatalf("期望成功: %+v", res)
	}
	if res.Model != "kimi-k3" {
		t.Fatalf("动态选择模型 = %q, 期望 kimi-k3", res.Model)
	}
	if !strings.Contains(srv.body, `"model":"kimi-k3"`) {
		t.Fatalf("探针 body 应使用 kimi-k3: %s", srv.body)
	}
}

func TestManifestServiceType(t *testing.T) {
	tests := map[string]string{
		"claude":   "messages",
		"messages": "messages",
		"openai":   "openai",
		"":         "",
	}
	for in, want := range tests {
		if got := manifestServiceType(in); got != want {
			t.Fatalf("manifestServiceType(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// captureServer 记录收到的请求方法/路径/请求体与认证头。
type captureServer struct {
	*httptest.Server
	method    string
	path      string
	body      string
	auth      string
	userAgent string
	xApp      string
	ccSession string
}

func newCaptureServer(status int, respBody string) *captureServer {
	c := &captureServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.method = r.Method
		c.path = r.URL.Path
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			c.body = string(b)
		}
		c.auth = r.Header.Get("Authorization")
		c.userAgent = r.Header.Get("User-Agent")
		c.xApp = r.Header.Get("X-App")
		c.ccSession = r.Header.Get("X-Claude-Code-Session-Id")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	return c
}

func TestProbeVolcenginePlanClaudeSuccess(t *testing.T) {
	srv := newCaptureServer(200, `{"id":"msg_1"}`)
	defer srv.Close()

	res := ProbeVolcenginePlan(context.Background(), "claude", srv.URL, "ark-key-123", "")

	if !res.OK || res.AuthFailed || res.Err != nil {
		t.Fatalf("2xx 应为成功: %+v", res)
	}
	if !strings.HasSuffix(srv.path, "/v1/messages") {
		t.Fatalf("Claude 探针路径 = %q, 期望以 /v1/messages 结尾", srv.path)
	}
	if srv.method != http.MethodPost {
		t.Fatalf("方法 = %q, 期望 POST", srv.method)
	}
	if !strings.Contains(srv.body, `"model":"deepseek-v4-flash"`) {
		t.Fatalf("agent plan 探针 body 缺少 model=deepseek-v4-flash: %s", srv.body)
	}
	if res.Model != "deepseek-v4-flash" {
		t.Fatalf("探针结果模型 = %q, 期望 deepseek-v4-flash", res.Model)
	}
	// Claude Code 特征
	if srv.userAgent == "" || srv.xApp == "" {
		t.Fatalf("Claude 探针应注入 Claude Code 特征头, UA=%q X-App=%q", srv.userAgent, srv.xApp)
	}
	if !strings.Contains(srv.auth, "ark-key-123") {
		t.Fatalf("认证头应携带 key: %q", srv.auth)
	}
}

func TestProbeVolcenginePlanCodingClaudeUsesArkCodeLatest(t *testing.T) {
	srv := newCaptureServer(200, `{\"id\":\"msg_1\"}`)
	defer srv.Close()

	// 用 coding 路径前缀触发模型选择（host 任意，探针内部只按 URL 子串选模型）
	res := ProbeVolcenginePlan(context.Background(), "claude", srv.URL+"/api/coding", "ark-key", "")
	if !res.OK {
		t.Fatalf("期望成功: %+v", res)
	}
	// captureServer 收到的 path 会包含 /api/coding；模型应在 body 里
	if !strings.Contains(srv.body, `"model":"deepseek-v4-flash"`) {
		t.Fatalf("coding plan 探针 body 缺少 deepseek-v4-flash: %s", srv.body)
	}
	if res.Model != "deepseek-v4-flash" {
		t.Fatalf("探针结果模型 = %q, 期望 deepseek-v4-flash", res.Model)
	}
}

func TestProbeVolcenginePlanOpenAISuccess(t *testing.T) {
	srv := newCaptureServer(200, `{"id":"chat_1"}`)
	defer srv.Close()

	res := ProbeVolcenginePlan(context.Background(), "openai", srv.URL+"/v3", "ark-key", "")
	if !res.OK {
		t.Fatalf("期望成功: %+v", res)
	}
	if !strings.HasSuffix(srv.path, "/chat/completions") {
		t.Fatalf("OpenAI 探针路径 = %q, 期望以 /chat/completions 结尾", srv.path)
	}
	// OpenAI 入口不应注入 Claude Code 专有特征（X-Claude-Code-Session-Id / claude-cli UA）
	if srv.ccSession != "" {
		t.Fatalf("OpenAI 探针不应注入 X-Claude-Code-Session-Id: %q", srv.ccSession)
	}
	if strings.HasPrefix(srv.userAgent, "claude-cli/") {
		t.Fatalf("OpenAI 探针不应注入 Claude Code User-Agent: %q", srv.userAgent)
	}
}

func TestProbeVolcenginePlanAuthFailed(t *testing.T) {
	srv := newCaptureServer(401, `{"error":{"type":"authentication_error","message":"invalid api key"}}`)
	defer srv.Close()

	res := ProbeVolcenginePlan(context.Background(), "claude", srv.URL, "ark-bad", "")
	if res.OK || !res.AuthFailed || res.Err != nil {
		t.Fatalf("401 应标记 AuthFailed: %+v", res)
	}
	if res.StatusCode != 401 {
		t.Fatalf("StatusCode = %d, 期望 401", res.StatusCode)
	}
	if !strings.Contains(string(res.Body), "authentication_error") {
		t.Fatalf("响应体应保留供错误分类: %s", string(res.Body))
	}
}

func TestProbeVolcenginePlan400NotAuthFailed(t *testing.T) {
	srv := newCaptureServer(400, `{"error":{"message":"bad model"}}`)
	defer srv.Close()

	res := ProbeVolcenginePlan(context.Background(), "claude", srv.URL, "ark-key", "")
	if res.OK || res.AuthFailed {
		t.Fatalf("400 不应视为成功或鉴权失败: %+v", res)
	}
	if res.StatusCode != 400 {
		t.Fatalf("StatusCode = %d, 期望 400", res.StatusCode)
	}
}

func TestProbeVolcenginePlan500NotAuthFailed(t *testing.T) {
	srv := newCaptureServer(500, `{"error":"internal"}`)
	defer srv.Close()

	res := ProbeVolcenginePlan(context.Background(), "openai", srv.URL+"/v3", "ark-key", "")
	if res.OK || res.AuthFailed {
		t.Fatalf("500 不应视为成功或鉴权失败: %+v", res)
	}
}

func TestProbeVolcenginePlanNetworkError(t *testing.T) {
	srv := newCaptureServer(200, ``)
	srv.Close() // 立即关闭制造连接失败

	res := ProbeVolcenginePlan(context.Background(), "claude", srv.URL, "ark-key", "")
	if res.OK || res.AuthFailed {
		t.Fatalf("网络错误不应视为成功或鉴权失败: %+v", res)
	}
	if res.Err == nil {
		t.Fatal("网络错误应携带 Err")
	}
}

func TestProbeVolcenginePlanUnsupportedServiceType(t *testing.T) {
	res := ProbeVolcenginePlan(context.Background(), "gemini", "https://ark.cn-beijing.volces.com/api/plan", "ark-key", "")
	if res.Err == nil {
		t.Fatal("不支持的 serviceType 应返回错误")
	}
}

func TestVolcenginePlanL1ProbeSuccessReturnsManifestModels(t *testing.T) {
	srv := newCaptureServer(200, `{"id":"msg_1"}`)
	defer srv.Close()

	sc, body, model, err := VolcenginePlanL1Probe(context.Background(), "claude", srv.URL, "ark-key", "", nil)
	if err != nil || sc != http.StatusOK {
		t.Fatalf("成功应返回 200: sc=%d err=%v", sc, err)
	}
	if model != "deepseek-v4-flash" {
		t.Fatalf("探针模型 = %q, 期望 deepseek-v4-flash", model)
	}
	// 内置 manifest 仅对官方 host 命中；本地 server 不命中，应返回空 data 列表而非臆造模型
	if !strings.Contains(string(body), `"data":[]`) && !strings.Contains(string(body), `"data"`) {
		t.Fatalf("成功 body 应为模型列表 JSON: %s", string(body))
	}
}

func TestVolcenginePlanL1ProbeAuthFailedReturnsUpstreamStatus(t *testing.T) {
	srv := newCaptureServer(403, `{"error":"forbidden"}`)
	defer srv.Close()

	sc, body, model, err := VolcenginePlanL1Probe(context.Background(), "openai", srv.URL+"/v3", "ark-bad", "", nil)
	if err != nil || sc != 403 {
		t.Fatalf("403 应原样返回上游状态: sc=%d err=%v", sc, err)
	}
	if model != "deepseek-v4-flash" {
		t.Fatalf("失败探针模型 = %q, 期望 deepseek-v4-flash", model)
	}
	if !strings.Contains(string(body), "forbidden") {
		t.Fatalf("body 应保留上游响应: %s", string(body))
	}
}

func TestVolcenginePlanL1ProbeNetworkErrorPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	status, body, model, err := VolcenginePlanL1Probe(ctx, "claude", srv.URL+"/api/plan", "ark-key-123", "", nil)
	if err == nil {
		t.Fatal("期望返回网络错误")
	}
	if status != 0 || body != nil {
		t.Fatalf("status/body = %d/%v, 期望 0/nil", status, body)
	}
	if model != "deepseek-v4-flash" {
		t.Fatalf("网络错误探针模型 = %q, 期望 deepseek-v4-flash", model)
	}
}

// newSSECaptureServer 与 newCaptureServer 相同，但响应 Content-Type 为 text/event-stream。
func newSSECaptureServer(respBody string) *captureServer {
	c := &captureServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.method = r.Method
		c.path = r.URL.Path
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			c.body = string(b)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(respBody))
	}))
	return c
}

func TestProbeVolcenginePlanStreamClaudeSuccess(t *testing.T) {
	srv := newSSECaptureServer("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
	defer srv.Close()

	res := ProbeVolcenginePlan(context.Background(), "claude", srv.URL, "ark-key", "")
	if !res.OK || res.Err != nil {
		t.Fatalf("SSE 首事件应判活: %+v", res)
	}
	if !strings.Contains(srv.body, `"stream":true`) {
		t.Fatalf("claude 探针应为流式: %s", srv.body)
	}
}

func TestProbeVolcenginePlanStreamOpenAISuccess(t *testing.T) {
	srv := newSSECaptureServer("data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
	defer srv.Close()

	res := ProbeVolcenginePlan(context.Background(), "openai", srv.URL+"/v3", "ark-key", "")
	if !res.OK || res.Err != nil {
		t.Fatalf("SSE 首事件应判活: %+v", res)
	}
	if !strings.HasSuffix(srv.path, "/chat/completions") {
		t.Fatalf("openai 探针路径 = %q, 期望以 /chat/completions 结尾", srv.path)
	}
	if !strings.Contains(srv.body, `"stream":true`) {
		t.Fatalf("openai 探针应为流式: %s", srv.body)
	}
}

func TestProbeVolcenginePlanResponsesBranch(t *testing.T) {
	srv := newSSECaptureServer("event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
	defer srv.Close()

	res := ProbeVolcenginePlan(context.Background(), "responses", srv.URL+"/v3", "ark-key", "")
	if !res.OK || res.Err != nil {
		t.Fatalf("responses 探针应成功: %+v", res)
	}
	if !strings.HasSuffix(srv.path, "/responses") {
		t.Fatalf("responses 探针路径 = %q, 期望以 /responses 结尾", srv.path)
	}
	if !strings.Contains(srv.body, `"stream":true`) {
		t.Fatalf("responses 探针应为流式: %s", srv.body)
	}
}

func TestProbeVolcenginePlanStreamDoneCountsAsAlive(t *testing.T) {
	srv := newSSECaptureServer("data: [DONE]\n\n")
	defer srv.Close()

	res := ProbeVolcenginePlan(context.Background(), "openai", srv.URL+"/v3", "ark-key", "")
	if !res.OK {
		t.Fatalf("收到 [DONE] 事件应判活: %+v", res)
	}
}

func TestProbeVolcenginePlanStreamEmptyFails(t *testing.T) {
	srv := newSSECaptureServer("")
	defer srv.Close()

	res := ProbeVolcenginePlan(context.Background(), "openai", srv.URL+"/v3", "ark-key", "")
	if res.OK || res.Err == nil {
		t.Fatalf("空 SSE 流应判失败: %+v", res)
	}
}
