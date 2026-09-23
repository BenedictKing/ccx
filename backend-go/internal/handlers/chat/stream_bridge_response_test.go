package chat

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/gin-gonic/gin"
)

// 流式桥接响应侧端到端：非流式客户端请求 + 上游返回 SSE（仅接受流式渠道的
// 兼容改写），handleSuccess 应合成 chat.completion JSON 并携带 usage 交付客户端。
func TestHandleSuccessSynthesizesStreamBridgeResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sse := "data: {\"id\":\"chatcmpl-b1\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"gpt-5\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你好\"},\"finish_reason\":null}]}\n" +
		"data: {\"id\":\"chatcmpl-b1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n" +
		"data: {\"id\":\"chatcmpl-b1\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":2,\"total_tokens\":13}}\n" +
		"data: [DONE]\n"

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","stream":false}`))

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       http.NoBody,
	}
	// Body 单独赋值避免 http.NoBody 不可读
	resp.Body = io.NopCloser(strings.NewReader(sse))

	envCfg := config.NewEnvConfig()
	usage, err := handleSuccess(c, resp, "openai", envCfg, time.Now(), "gpt-5", false, common.StreamPreflightTimeouts{})
	if err != nil {
		t.Fatalf("handleSuccess 失败: %v", err)
	}
	if usage == nil || usage.InputTokens != 11 || usage.OutputTokens != 2 {
		t.Fatalf("usage = %#v, want input=11 output=2（SSE usage 应被提取）", usage)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("客户端状态码 = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"object":"chat.completion"`) {
		t.Fatalf("客户端应收到合成后的 chat.completion, got: %s", body)
	}
	if !strings.Contains(body, `"content":"你好"`) {
		t.Fatalf("content 缺失: %s", body)
	}
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "text/event-stream") {
		t.Fatalf("客户端 Content-Type 不应是 SSE: %s", ct)
	}
}

// 上游返回 JSON 的常规非流式路径不受流式桥接影响（Content-Type 判定不误伤）。
func TestHandleSuccessNormalNonStreamUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","stream":false}`))

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl-n1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)),
	}

	envCfg := config.NewEnvConfig()
	usage, err := handleSuccess(c, resp, "openai", envCfg, time.Now(), "gpt-5", false, common.StreamPreflightTimeouts{})
	if err != nil {
		t.Fatalf("handleSuccess 失败: %v", err)
	}
	if usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 1 {
		t.Fatalf("usage = %#v, want input=3 output=1", usage)
	}
	if !strings.Contains(w.Body.String(), `"content":"hi"`) {
		t.Fatalf("透传体错误: %s", w.Body.String())
	}
}
