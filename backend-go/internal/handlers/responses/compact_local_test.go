package responses

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestFormatItemsAsTranscript_BasicMessages(t *testing.T) {
	items := []types.ResponsesItem{
		{Type: "message", Role: "user", Content: "Hello world"},
		{Type: "message", Role: "assistant", Content: "Hi there"},
	}
	transcript := formatItemsAsTranscript(items)
	if !strings.Contains(transcript, "[User]\nHello world") {
		t.Fatalf("missing user message in transcript: %s", transcript)
	}
	if !strings.Contains(transcript, "[Assistant]\nHi there") {
		t.Fatalf("missing assistant message in transcript: %s", transcript)
	}
	if !strings.Contains(transcript, "---") {
		t.Fatalf("missing separator in transcript: %s", transcript)
	}
}

func TestFormatItemsAsTranscript_FunctionCall(t *testing.T) {
	items := []types.ResponsesItem{
		{Type: "function_call", Name: "Read", Arguments: `{"file":"/tmp/x"}`},
	}
	transcript := formatItemsAsTranscript(items)
	if !strings.Contains(transcript, "Tool Call: Read") {
		t.Fatalf("missing tool call in transcript: %s", transcript)
	}
}

// PLACEHOLDER_MORE_TESTS

func TestFormatItemsAsTranscript_FunctionCallOutputSummary(t *testing.T) {
	items := []types.ResponsesItem{
		{Type: "function_call_output", Output: "very long output from tool"},
	}
	transcript := formatItemsAsTranscript(items)
	// function_call_output 不再完全丢弃，而是生成简要摘要
	if !strings.Contains(transcript, "Tool Result") {
		t.Fatalf("function_call_output should produce a Tool Result summary, got: %s", transcript)
	}
	if !strings.Contains(transcript, "very long output from tool") {
		t.Fatalf("function_call_output summary should contain output text, got: %s", transcript)
	}
}

func TestFormatItemsAsTranscript_FunctionCallOutputTruncated(t *testing.T) {
	longOutput := strings.Repeat("x", 1000)
	items := []types.ResponsesItem{
		{Type: "function_call_output", Output: longOutput},
	}
	transcript := formatItemsAsTranscript(items)
	// 超长输出应被截断到 localCompactMaxToolOutputRunes (500)
	if strings.Contains(transcript, strings.Repeat("x", 600)) {
		t.Fatalf("function_call_output output should be truncated to <=500 runes")
	}
	if !strings.Contains(transcript, "...") {
		t.Fatalf("truncated output should end with ..., got: %s", transcript)
	}
}

func TestFormatItemsAsTranscript_ContentBlocks(t *testing.T) {
	items := []types.ResponsesItem{
		{Type: "message", Role: "user", Content: []interface{}{
			map[string]interface{}{"type": "input_text", "text": "block1"},
			map[string]interface{}{"type": "input_text", "text": "block2"},
		}},
	}
	transcript := formatItemsAsTranscript(items)
	if !strings.Contains(transcript, "block1") || !strings.Contains(transcript, "block2") {
		t.Fatalf("missing content blocks in transcript: %s", transcript)
	}
}

func TestFormatItemsAsTranscript_InputImage(t *testing.T) {
	tests := []struct {
		name     string
		imageURL interface{}
		leaks    []string
	}{
		{
			name:     "base64 string image_url",
			imageURL: "data:image/png;base64,SGVsbG8gV29ybGQh...",
			leaks:    []string{"data:image", "base64", "SGVsbG8gV29ybGQh"},
		},
		{
			name: "base64 object image_url",
			imageURL: map[string]interface{}{
				"url": "data:image/jpeg;base64,QUJDREVGRw==",
			},
			leaks: []string{"data:image", "base64", "QUJDREVGRw"},
		},
		{
			name:     "remote string image_url",
			imageURL: "https://example.com/images/cat.png",
			leaks:    []string{"https://example.com/images/cat.png", "example.com"},
		},
		{
			name: "remote object image_url",
			imageURL: map[string]interface{}{
				"url": "https://cdn.example.com/images/dog.png",
			},
			leaks: []string{"https://cdn.example.com/images/dog.png", "cdn.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := []types.ResponsesItem{
				{Type: "message", Role: "user", Content: []interface{}{
					map[string]interface{}{"type": "input_text", "text": "Describe this image"},
					map[string]interface{}{"type": "input_image", "image_url": tt.imageURL, "detail": "high"},
				}},
			}
			transcript := formatItemsAsTranscript(items)
			if !strings.Contains(transcript, "[Image]") {
				t.Fatalf("expected [Image] placeholder, got: %s", transcript)
			}
			if strings.Contains(transcript, "[Image:") {
				t.Fatalf("image URL should not be included in placeholder: %s", transcript)
			}
			for _, leak := range tt.leaks {
				if strings.Contains(transcript, leak) {
					t.Fatalf("image data leaked into transcript (%s): %s", leak, transcript)
				}
			}
			if !strings.Contains(transcript, "Describe this image") {
				t.Fatalf("text content should be preserved: %s", transcript)
			}
		})
	}
}

func TestTruncateTranscript(t *testing.T) {
	long := strings.Repeat("a", localCompactMaxTranscriptRunes+1000)
	result := truncateTranscript(long)
	if len([]rune(result)) >= len([]rune(long)) {
		t.Fatalf("transcript should be truncated")
	}
	if !strings.Contains(result, "omitted") {
		t.Fatalf("truncated transcript should contain omitted marker")
	}
}

func TestNeedsLocalCompact(t *testing.T) {
	tests := []struct {
		serviceType string
		want        bool
	}{
		{"responses", false},
		{"openai", true},
		{"claude", true},
		{"gemini", true},
		{"", true},
	}
	for _, tt := range tests {
		got := needsLocalCompact(&config.UpstreamConfig{ServiceType: tt.serviceType})
		if got != tt.want {
			t.Errorf("needsLocalCompact(%q) = %v, want %v", tt.serviceType, got, tt.want)
		}
	}
}

func TestBuildLocalCompactRequestBody_StripsClientAgentControls(t *testing.T) {
	body := `{
		"model":"gpt-5.5",
		"instructions":"You are Claude Code, Anthropic's official CLI for Claude.",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"CRITICAL: Respond with TEXT ONLY. Do NOT call any tools."}]}],
		"tools":[{"type":"function","name":"Bash","parameters":{"type":"object"}}],
		"tool_choice":"auto",
		"parallel_tool_calls":true,
		"max_output_tokens":20000,
		"stream":true
	}`

	localBody, err := buildLocalCompactRequestBody([]byte(body), true, nil, nil)
	if err != nil {
		t.Fatalf("buildLocalCompactRequestBody failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(localBody, &req); err != nil {
		t.Fatalf("local compact request JSON 解析失败: %v", err)
	}

	instructions, _ := req["instructions"].(string)
	if strings.Contains(instructions, "Claude Code") {
		t.Fatalf("local compact 不应继承原始 agent instructions: %s", instructions)
	}
	if !strings.Contains(instructions, "Do NOT call tools") {
		t.Fatalf("local compact system prompt 应显式禁止工具调用: %s", instructions)
	}
	if !strings.Contains(instructions, "inert data") {
		t.Fatalf("local compact system prompt 应把 transcript 标记为非活动指令: %s", instructions)
	}

	for _, field := range []string{"tools", "tool_choice", "parallel_tool_calls"} {
		if _, ok := req[field]; ok {
			t.Fatalf("local compact request 不应携带 %s: %s", field, string(localBody))
		}
	}
}

func TestLocalCompact_OpenAIUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		// 验证请求被转换为 chat completions 格式
		if _, ok := req["messages"]; !ok {
			t.Error("expected messages field in converted request")
		}
		if req["stream"] != false {
			t.Error("expected stream=false for non-streaming compact")
		}
		// 不应有 tools
		if _, ok := req["tools"]; ok {
			t.Error("compact request should not have tools")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		io.WriteString(w, `{"id":"chatcmpl-123","choices":[{"message":{"role":"assistant","content":"## Summary\nCompacted context"}}],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`)
	}))
	defer upstream.Close()

	sessionManager := session.NewSessionManager(24*60*60*1000000000, 100, 100000)

	cfgManager := setupResponsesTestConfigManager(t, []config.UpstreamConfig{{
		Name:        "openai-channel",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "openai",
		Status:      "active",
	}})

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	t.Cleanup(func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		imagesMetrics.Stop()
		traceAffinity.Stop()
	})

	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, nil)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret-key",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	r.POST("/v1/responses/compact", CompactHandler(envCfg, cfgManager, sessionManager, sch))

	body := `{"model":"gpt-4o","input":[{"type":"message","role":"user","content":"Hello"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var resp types.ResponsesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v, body=%s", err, w.Body.String())
	}
	if resp.Status != "completed" {
		t.Fatalf("status = %q, want completed", resp.Status)
	}
	if len(resp.Output) == 0 {
		t.Fatal("expected output items")
	}
}

func TestGetSessionByResponseID(t *testing.T) {
	sm := session.NewSessionManager(24*60*60*1000000000, 100, 100000)

	// 未命中
	_, err := sm.GetSessionByResponseID("resp_nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent response ID")
	}

	// 创建 session 并记录映射
	sess, _ := sm.GetOrCreateSession("")
	sm.RecordResponseMapping("resp_123", sess.ID)

	// 命中
	found, err := sm.GetSessionByResponseID("resp_123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.ID != sess.ID {
		t.Fatalf("session ID = %q, want %q", found.ID, sess.ID)
	}
}

func TestNormalizeCompactOutputKeepsOnlyAssistantMessage(t *testing.T) {
	output := []types.ResponsesItem{
		{
			Type: "reasoning",
			Summary: []interface{}{map[string]interface{}{
				"type": "summary_text",
				"text": "internal reasoning",
			}},
		},
		{
			ID:      "msg_1",
			Type:    "message",
			Role:    "assistant",
			Status:  "completed",
			Content: []types.ContentBlock{{Type: "output_text", Text: "compacted summary"}},
		},
	}

	result := normalizeCompactOutput(output)

	if len(result) != 1 {
		t.Fatalf("期望 1 个 output item，实际 %d 个", len(result))
	}
	if result[0].Type != "message" {
		t.Fatalf("期望 message item，实际 %s", result[0].Type)
	}
	if text := extractContentText(result[0].Content); text != "compacted summary" {
		t.Fatalf("期望只保留 message 文本，实际 %q", text)
	}
}

func TestNormalizeCompactResponseBodyKeepsOnlyMessageOutput(t *testing.T) {
	body := []byte(`{
		"id":"resp_1",
		"status":"completed",
		"output":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"internal reasoning"}]},
			{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"compacted summary"}]}
		],
		"usage":{"output_tokens_details":{"reasoning_tokens":12}}
	}`)

	normalized := normalizeCompactResponseBody(body)

	var payload map[string]interface{}
	if err := json.Unmarshal(normalized, &payload); err != nil {
		t.Fatalf("normalized JSON 解析失败: %v", err)
	}
	output, ok := payload["output"].([]interface{})
	if !ok {
		t.Fatalf("output 类型错误: %T", payload["output"])
	}
	if len(output) != 1 {
		t.Fatalf("期望 1 个 output item，实际 %d 个", len(output))
	}
	item, ok := output[0].(map[string]interface{})
	if !ok {
		t.Fatalf("output[0] 类型错误: %T", output[0])
	}
	if item["type"] != "message" {
		t.Fatalf("期望 message item，实际 %v", item["type"])
	}
	if text := extractContentText(item["content"]); text != "compacted summary" {
		t.Fatalf("期望只保留 message 文本，实际 %q", text)
	}
	usage, ok := payload["usage"].(map[string]interface{})
	if !ok || usage["output_tokens_details"] == nil {
		t.Fatal("期望保留 usage.output_tokens_details")
	}
}

func TestShouldSkipCompactStreamEvent(t *testing.T) {
	tests := []struct {
		name  string
		event string
		skip  bool
	}{
		{
			name:  "skip reasoning output_item.added",
			event: `event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","item":{"type":"reasoning","id":"rs_1"}}` + "\n",
			skip:  true,
		},
		{
			name:  "skip reasoning_summary_part.added",
			event: `event: response.reasoning_summary_part.added` + "\n" + `data: {"type":"response.reasoning_summary_part.added"}` + "\n",
			skip:  true,
		},
		{
			name:  "skip reasoning_summary_text.delta",
			event: `event: response.reasoning_summary_text.delta` + "\n" + `data: {"type":"response.reasoning_summary_text.delta","text":"thinking..."}` + "\n",
			skip:  true,
		},
		{
			name:  "skip reasoning_summary_text.done",
			event: `event: response.reasoning_summary_text.done` + "\n" + `data: {"type":"response.reasoning_summary_text.done"}` + "\n",
			skip:  true,
		},
		{
			name:  "skip reasoning_summary_part.done",
			event: `event: response.reasoning_summary_part.done` + "\n" + `data: {"type":"response.reasoning_summary_part.done"}` + "\n",
			skip:  true,
		},
		{
			name:  "skip reasoning output_item.done",
			event: `event: response.output_item.done` + "\n" + `data: {"type":"response.output_item.done","item":{"type":"reasoning","status":"completed"}}` + "\n",
			skip:  true,
		},
		{
			name:  "keep message output_item.added",
			event: `event: response.output_item.added` + "\n" + `data: {"type":"response.output_item.added","item":{"type":"message","id":"msg_1"}}` + "\n",
			skip:  false,
		},
		{
			name:  "keep output_text.delta",
			event: `event: response.output_text.delta` + "\n" + `data: {"type":"response.output_text.delta","delta":"hello"}` + "\n",
			skip:  false,
		},
		{
			name:  "keep response.completed",
			event: `event: response.completed` + "\n" + `data: {"type":"response.completed","response":{"id":"resp_1"}}` + "\n",
			skip:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipCompactStreamEvent(tt.event)
			if got != tt.skip {
				t.Errorf("shouldSkipCompactStreamEvent() = %v, want %v for event: %s", got, tt.skip, tt.event)
			}
		})
	}
}
