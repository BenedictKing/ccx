package messages

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func newPlainClaudeUpstream() *config.UpstreamConfig {
	return &config.UpstreamConfig{
		ServiceType: "claude",
		Name:        "test-claude",
	}
}

func TestMessagesInbound_TransformRequest_ParsesModelStream(t *testing.T) {
	body := []byte(`{"model":"claude-3-5-sonnet-20241022","stream":true,"messages":[{"role":"user","content":"hi"}],"max_tokens":1024}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))

	in := NewInbound()
	if got := in.Format(); got != llm.FormatClaudeMessages {
		t.Fatalf("Format mismatch: %q", got)
	}
	llmReq, err := in.TransformRequest(context.Background(), req, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	if llmReq.Model != "claude-3-5-sonnet-20241022" {
		t.Fatalf("model mismatch: %q", llmReq.Model)
	}
	if !llmReq.IsStream() {
		t.Fatal("expected stream=true")
	}
	v, ok := llmReq.Metadata[MetaParsedRequest]
	if !ok {
		t.Fatal("parsed metadata missing")
	}
	parsed, ok := v.(*types.ClaudeRequest)
	if !ok || parsed.Model != "claude-3-5-sonnet-20241022" {
		t.Fatalf("parsed shape mismatch: %+v", v)
	}
}

func TestMessagesInbound_TransformRequest_RejectsEmptyAndMissingModel(t *testing.T) {
	in := NewInbound()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if _, err := in.TransformRequest(context.Background(), req, nil); err == nil {
		t.Fatal("expected error for empty body")
	}
	if _, err := in.TransformRequest(context.Background(), req, []byte(`{"messages":[]}`)); err == nil {
		t.Fatal("expected error for missing model")
	}
	if _, err := in.TransformRequest(context.Background(), req, []byte(`{"`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestMessagesInbound_TransformResponse_PassthroughBody(t *testing.T) {
	in := NewInbound()
	resp := &llm.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       []byte(`{"id":"msg_1","type":"message"}`),
	}
	body, hdr, err := in.TransformResponse(context.Background(), resp)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if string(body) != string(resp.Body) {
		t.Fatalf("body mismatch")
	}
	if hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type fallback missing")
	}
}

func TestMessagesInbound_TransformStream_ForwardsBytes(t *testing.T) {
	in := NewInbound()
	respCh := make(chan *llm.Response, 2)
	respCh <- &llm.Response{Body: []byte("event: message_start\ndata: {}\n\n")}
	respCh <- &llm.Response{Body: []byte("event: message_stop\ndata: {}\n\n")}
	close(respCh)
	upstream := llm.NewChanStream(context.Background(), respCh, nil)

	out := in.TransformStream(context.Background(), upstream)
	defer out.Close()

	var got []string
	for out.Next() {
		got = append(got, string(out.Current().Data))
	}
	if len(got) != 2 || !strings.Contains(got[0], "message_start") {
		t.Fatalf("unexpected events: %v", got)
	}
}

func TestMessagesOutbound_TransformRequest_BuildsViaProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(context.Background())

	body := []byte(`{"model":"claude-3-5-sonnet-20241022","stream":false,"messages":[{"role":"user","content":"hi"}],"max_tokens":1024}`)
	in := NewInbound()
	llmReq, err := in.TransformRequest(context.Background(), c.Request, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	adapters.SetGinContext(llmReq, c)
	adapters.SetUpstreamBinding(llmReq, newPlainClaudeUpstream(), "sk-test-123", "https://api.anthropic.example.com")

	out := NewOutbound()
	httpReq, realBody, err := out.TransformRequest(context.Background(), llmReq)
	if err != nil {
		t.Fatalf("outbound: %v", err)
	}
	if httpReq == nil {
		t.Fatal("nil http request")
	}
	if !strings.HasSuffix(httpReq.URL.String(), "/v1/messages") {
		t.Fatalf("expected /v1/messages URL, got %s", httpReq.URL.String())
	}
	// ClaudeProvider 走 AuthKindStandard；utils.SetAuthenticationHeader 根据
	// key 前缀智能选 x-api-key（Anthropic 格式）或 Authorization Bearer。
	// 测试用 sk-test-123（非 sk-ant- 前缀）期望走 Bearer 路径，避免依赖具体
	// 智能匹配规则。
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test-123" {
		t.Fatalf("expected Bearer auth header, got %q (full headers: %v)", got, httpReq.Header)
	}
	if len(realBody) == 0 {
		t.Fatal("realBody empty")
	}
}

func TestMessagesOutbound_TransformRequest_RejectsMissingMetadata(t *testing.T) {
	body := []byte(`{"model":"claude-3-5","messages":[],"max_tokens":1}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	in := NewInbound()
	llmReq, err := in.TransformRequest(context.Background(), req, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}

	out := NewOutbound()
	if _, _, err := out.TransformRequest(context.Background(), llmReq); err == nil {
		t.Fatal("expected error for missing gin context")
	}
}

func TestMessagesOutbound_TransformRequest_RejectsUnknownService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(context.Background())

	body := []byte(`{"model":"claude-3-5","messages":[],"max_tokens":1}`)
	in := NewInbound()
	llmReq, _ := in.TransformRequest(context.Background(), c.Request, body)
	adapters.SetGinContext(llmReq, c)
	adapters.SetUpstreamBinding(llmReq, &config.UpstreamConfig{ServiceType: "unknown-xyz"}, "sk", "https://x.local")

	out := NewOutbound()
	if _, _, err := out.TransformRequest(context.Background(), llmReq); err == nil {
		t.Fatal("expected error for unknown service type")
	}
}

// TestMessagesOutbound_TransformResponse_ClaudeUpstreamPassthrough：claude → claude
// 走字节透传路径（与 PR1 同行为），usage 通过 parseClaudeUsage 解析。
func TestMessagesOutbound_TransformResponse_ClaudeUpstreamPassthrough(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":7}}`)
	resp := buildHTTPResp(body)

	llmReq := &llm.Request{Metadata: map[string]any{
		adapters.MetaUpstreamConfig: &config.UpstreamConfig{ServiceType: "claude"},
	}}
	ctx := adapters.WithRequestContext(context.Background(), llmReq)

	out := NewOutbound()
	got, err := out.TransformResponse(ctx, resp)
	if err != nil {
		t.Fatalf("TransformResponse: %v", err)
	}
	if string(got.Body) != string(body) {
		t.Fatalf("same-format body must passthrough; got=%s want=%s", got.Body, body)
	}
	if got.Usage == nil || got.Usage.Inner.InputTokens != 11 || got.Usage.Inner.OutputTokens != 7 {
		t.Fatalf("usage parse mismatch: %+v", got.Usage)
	}
}

// TestMessagesOutbound_TransformResponse_CrossFormatUpstreams：openai/gemini/responses
// 三种上游都应转换为 Claude Messages 协议；stop_reason / content / usage 必须正确。
func TestMessagesOutbound_TransformResponse_CrossFormatUpstreams(t *testing.T) {
	tests := []struct {
		name         string
		serviceType  string
		upstreamBody string
		wantStop     string
		wantText     string
		wantInput    int
		wantOutput   int
	}{
		{
			name:         "openai_to_claude",
			serviceType:  "openai",
			upstreamBody: `{"id":"chat_1","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":13,"completion_tokens":5,"total_tokens":18}}`,
			wantStop:     "end_turn",
			wantText:     "hi",
			wantInput:    13,
			wantOutput:   5,
		},
		{
			name:         "gemini_to_claude",
			serviceType:  "gemini",
			upstreamBody: `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":3,"totalTokenCount":20}}`,
			wantStop:     "end_turn",
			wantText:     "hi",
			wantInput:    17,
			wantOutput:   3,
		},
		{
			name:         "responses_to_claude",
			serviceType:  "responses",
			upstreamBody: `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":19,"output_tokens":9,"total_tokens":28}}`,
			wantStop:     "end_turn",
			wantText:     "hi",
			wantInput:    19,
			wantOutput:   9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := buildHTTPResp([]byte(tt.upstreamBody))

			llmReq := &llm.Request{Metadata: map[string]any{
				adapters.MetaUpstreamConfig: &config.UpstreamConfig{ServiceType: tt.serviceType},
			}}
			ctx := adapters.WithRequestContext(context.Background(), llmReq)

			out := NewOutbound()
			got, err := out.TransformResponse(ctx, resp)
			if err != nil {
				t.Fatalf("TransformResponse: %v", err)
			}

			var parsed types.ClaudeResponse
			if err := json.Unmarshal(got.Body, &parsed); err != nil {
				t.Fatalf("converted body must be Claude JSON, parse err=%v body=%s", err, string(got.Body))
			}
			if parsed.StopReason != tt.wantStop {
				t.Fatalf("stop_reason = %q, want %q (body=%s)", parsed.StopReason, tt.wantStop, string(got.Body))
			}
			var foundText string
			for _, c := range parsed.Content {
				if c.Type == "text" && c.Text != "" {
					foundText = c.Text
				}
			}
			if foundText != tt.wantText {
				t.Fatalf("content text = %q, want %q (body=%s)", foundText, tt.wantText, string(got.Body))
			}
			if parsed.Usage == nil || parsed.Usage.InputTokens != tt.wantInput || parsed.Usage.OutputTokens != tt.wantOutput {
				t.Fatalf("usage mismatch: %+v want input=%d output=%d", parsed.Usage, tt.wantInput, tt.wantOutput)
			}
		})
	}
}

// TestMessagesOutbound_TransformStream_ClaudeRawPassthrough：claude → claude 直接按 \n\n 分帧。
func TestMessagesOutbound_TransformStream_ClaudeRawPassthrough(t *testing.T) {
	raw := "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	resp := buildHTTPRespStream([]byte(raw))

	llmReq := &llm.Request{Metadata: map[string]any{
		adapters.MetaUpstreamConfig: &config.UpstreamConfig{ServiceType: "claude"},
	}}
	ctx := adapters.WithRequestContext(context.Background(), llmReq)

	out := NewOutbound()
	stream := out.TransformStream(ctx, resp)
	defer stream.Close()

	var seen []string
	for stream.Next() {
		seen = append(seen, string(stream.Current().Body))
	}
	if len(seen) != 2 {
		t.Fatalf("frames = %d, want 2 (got=%v)", len(seen), seen)
	}
	if !strings.Contains(seen[0], "message_start") || !strings.Contains(seen[1], "message_stop") {
		t.Fatalf("raw passthrough frame mismatch: %v", seen)
	}
}

// TestMessagesOutbound_TransformStream_CrossFormatViaProvider：openai 流必须经 provider
// HandleStreamResponse 转换为 Claude SSE 协议，不能 raw passthrough 上游 OpenAI 帧。
func TestMessagesOutbound_TransformStream_CrossFormatViaProvider(t *testing.T) {
	upstreamRaw := "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	resp := buildHTTPRespStream([]byte(upstreamRaw))

	llmReq := &llm.Request{Metadata: map[string]any{
		adapters.MetaUpstreamConfig: &config.UpstreamConfig{ServiceType: "openai"},
	}}
	ctx := adapters.WithRequestContext(context.Background(), llmReq)

	out := NewOutbound()
	stream := out.TransformStream(ctx, resp)
	defer stream.Close()

	var concat strings.Builder
	for stream.Next() {
		concat.Write(stream.Current().Body)
	}
	got := concat.String()
	if strings.Contains(got, `"object":"chat.completion.chunk"`) {
		t.Fatalf("cross-format stream leaked raw OpenAI chunk: %s", got)
	}
	if !strings.Contains(got, "hi") {
		t.Fatalf("cross-format stream missing converted content 'hi': %s", got)
	}
	// Claude SSE 帧应包含 message_start / content_block_delta 之类的事件类型。
	if !strings.Contains(got, "content_block_delta") && !strings.Contains(got, "message_start") {
		t.Fatalf("cross-format stream not in Claude SSE shape: %s", got)
	}
}

// buildHTTPResp 构造一个最小 *http.Response，便于直接 feed 进 outbound.TransformResponse。
func buildHTTPResp(body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func buildHTTPRespStream(body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}
