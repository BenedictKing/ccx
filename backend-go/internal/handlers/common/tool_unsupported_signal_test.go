package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
)

func TestToolUnsupportedFromError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		hasTools   bool
		wantNil    bool
	}{
		// 强信号：错误文案点名 tools / tool use / function calling
		{
			name:       "强信号-tools not supported",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"type":"invalid_request_error","message":"tools are not supported by this model"}}`,
			hasTools:   true,
		},
		{
			name:       "强信号-tool use 未开启",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"Tool use is not enabled for this model"}}`,
			hasTools:   true,
		},
		{
			name:       "强信号-否定词前置 tools",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"error":{"message":"unsupported parameter: tools"}}`,
			hasTools:   true,
		},
		{
			name:       "强信号-function calling 不支持",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"function calling is disabled for this deployment"}}`,
			hasTools:   true,
		},
		{
			name:       "强信号-does not support tools",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"this model does not support tools"}}`,
			hasTools:   true,
		},
		// 不学习：错误不含工具所指
		{
			name:       "不学习-具体参数名报错",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"messages.3.content: temperature must be between 0 and 1"}}`,
			hasTools:   true,
			wantNil:    true,
		},
		{
			name:       "不学习-通用 invalid_request（无弱信号归因）",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"type":"invalid_request_error","message":"Invalid request Error"}}`,
			hasTools:   true,
			wantNil:    true,
		},
		{
			name:       "不学习-5xx 容量问题",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"error":{"message":"tools are not supported"}}`,
			hasTools:   true,
			wantNil:    true,
		},
		{
			name:       "不学习-请求未携带 tools",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"tools are not supported"}}`,
			hasTools:   false,
			wantNil:    true,
		},
		{
			name:       "不学习-空响应体",
			statusCode: http.StatusBadRequest,
			body:       ``,
			hasTools:   true,
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolUnsupportedFromError(tt.statusCode, []byte(tt.body), tt.hasTools)
			if tt.wantNil && got != nil {
				t.Fatalf("ToolUnsupportedFromError() = %+v, want nil", got)
			}
			if !tt.wantNil && got == nil {
				t.Fatal("ToolUnsupportedFromError() = nil, want signal")
			}
		})
	}
}

func TestForcedToolChoiceInBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"messages-指定工具", `{"tool_choice":{"type":"tool","name":"ccx_probe"},"tools":[{}]}`, true},
		{"messages-任一工具", `{"tool_choice":{"type":"any"}}`, true},
		{"chat-function", `{"tool_choice":{"type":"function","function":{"name":"ccx_probe"}}}`, true},
		{"chat-legacy name 对象", `{"tool_choice":{"name":"ccx_probe"}}`, true},
		{"chat-required 字符串", `{"tool_choice":"required"}`, true},
		{"responses-custom", `{"tool_choice":{"type":"custom","name":"ccx_probe"}}`, true},
		{"gemini-ANY", `{"tool_config":{"function_calling_config":{"mode":"ANY"}}}`, true},
		{"auto-非强制", `{"tool_choice":{"type":"auto"}}`, false},
		{"none-非强制", `{"tool_choice":"none"}`, false},
		{"未声明", `{"messages":[]}`, false},
		{"空对象", `{}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ForcedToolChoiceInBody([]byte(tt.body)); got != tt.want {
				t.Fatalf("ForcedToolChoiceInBody() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBodyHasTools(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"messages 带工具", `{"tools":[{"name":"a"}]}`, true},
		{"gemini 带工具", `{"tools":[{"functionDeclarations":[{}]}]}`, true},
		{"空工具数组", `{"tools":[]}`, false},
		{"缺失字段", `{"messages":[]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BodyHasTools([]byte(tt.body)); got != tt.want {
				t.Fatalf("BodyHasTools() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBodyHasToolsCodexForm(t *testing.T) {
	// codex 形态：无明文 tools 数组，仅顶层 tool_choice（2026-09-12 实测 0.153.4）
	if !BodyHasTools([]byte(`{"model":"gpt-6-astra","tool_choice":"auto","input":"hi"}`)) {
		t.Fatal("codex 形态（tool_choice 存在）应判定为带工具语义")
	}
	if !BodyHasTools([]byte(`{"tools":[{"type":"function"}]}`)) {
		t.Fatal("标准 tools 数组形态应判定为带工具")
	}
	if BodyHasTools([]byte(`{"model":"x","input":"hi"}`)) {
		t.Fatal("无任何工具字段的普通请求不应误判")
	}
	if BodyHasTools([]byte(`{"tools":[]}`)) {
		t.Fatal("空 tools 数组不算带工具")
	}
}

// ── 伪工具调用标记流式扫描器 ──

func TestPseudoToolCallMarkerScanner(t *testing.T) {
	tests := []struct {
		name  string
		feeds []string
		want  bool
	}{
		{"单段命中", []string{`好的<tool_call>{"name":"get_time"}`}, true},
		{"跨 delta 切断", []string{"正文<tool", "_call>{}"}, true},
		{"闭标记命中", []string{"arg</parameter></function></tool_call>"}, true},
		{"DSML 总前缀", []string{"<｜DS", "ML｜<invoke"}, true},
		{"Qwen function 变体", []string{`<function=get_time>`}, true},
		{"纯文本不误报", []string{"这是一段正常的工具使用说明，包含 tool 字样"}, false},
		{"空增量", []string{"", ""}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := &PseudoToolCallMarkerScanner{}
			got := false
			for _, feed := range tt.feeds {
				if scanner.Feed(feed) {
					got = true
				}
			}
			if got != tt.want || scanner.Found() != tt.want {
				t.Fatalf("Feed 累计=%v Found=%v, want %v", got, scanner.Found(), tt.want)
			}
		})
	}

	// 幂等：命中后继续 Feed 仍报告命中
	scanner := &PseudoToolCallMarkerScanner{}
	if !scanner.Feed("<tool_call>") || !scanner.Feed("后续文本") {
		t.Fatal("命中后 Feed 应持续返回 true")
	}
}

// ── MaybeCountPseudoToolCallMiss 守卫与学习口径 ──

func TestMaybeCountPseudoToolCallMiss(t *testing.T) {
	restore := config.SwapSharedChannelCompatCacheForTest(config.NewChannelCompatCache())
	defer restore()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	upstream := &config.UpstreamConfig{ChannelUID: "ch_test", Name: "test-channel"}
	cache := config.SharedChannelCompatCache()

	const apiKey = "sk-test"
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	toolBody := []byte(`{"model":"m1","tools":[{"type":"function","function":{"name":"get_time"}}],"tool_choice":"auto"}`)
	forcedBody := []byte(`{"model":"m1","tools":[{"type":"function"}],"tool_choice":"required"}`)
	plainBody := []byte(`{"model":"m1","messages":[]}`)

	seed := func() {
		cache.Record("ch_test#messages", keyHash, "m1", config.TraitVerifiedToolCalls, true, config.CompatSourceRuntimeSignal, "e")
	}
	streak := func() int {
		state, ok := cache.Trait("ch_test#messages", keyHash, "m1", config.TraitVerifiedToolCalls)
		if !ok {
			return -1
		}
		return state.AutoMissStreak
	}
	enabled := func() bool {
		state, ok := cache.Trait("ch_test#messages", keyHash, "m1", config.TraitVerifiedToolCalls)
		return ok && state.Enabled
	}

	// 守卫矩阵：以下任一成立都不得计数
	seed()
	MaybeCountPseudoToolCallMiss(c, upstream, apiKey, "m1", toolBody, false, true, errNonNil(), "messages")
	MaybeCountPseudoToolCallMiss(c, upstream, apiKey, "m1", toolBody, true, true, nil, "messages")   // 有真实工具调用
	MaybeCountPseudoToolCallMiss(c, upstream, apiKey, "m1", toolBody, false, false, nil, "messages") // 无伪标记（纯文本合法回答）
	MaybeCountPseudoToolCallMiss(c, upstream, apiKey, "m1", plainBody, false, true, nil, "messages") // 请求无工具
	MaybeCountPseudoToolCallMiss(c, upstream, apiKey, "m1", forcedBody, false, true, nil, "messages")
	if got := streak(); got != 0 {
		t.Fatalf("守卫场景全部不应计数，got streak=%d", got)
	}

	// 无 verified 条目的模型：同样计数（fail-open 窗口的劣化证据留存，供竞速影子规避）
	MaybeCountPseudoToolCallMiss(c, upstream, apiKey, "m2", toolBody, false, true, nil, "messages")
	state2, ok := cache.Trait("ch_test#messages", keyHash, "m2", config.TraitVerifiedToolCalls)
	if !ok || state2.Enabled || state2.AutoMissStreak != 1 {
		t.Fatalf("无 verified 条目时应记入 Enabled=false 的 miss 计数，got %+v, ok=%v", state2, ok)
	}

	// 连续 3 次 miss：撤销
	for i := 0; i < 3; i++ {
		MaybeCountPseudoToolCallMiss(c, upstream, apiKey, "m1", toolBody, false, true, nil, "messages")
	}
	if enabled() {
		t.Fatal("连续 3 次伪标记 miss 应撤销 verified")
	}

	// 真实工具调用成功：重建条目且重置连续计数
	seed()
	MaybeCountPseudoToolCallMiss(c, upstream, apiKey, "m1", toolBody, false, true, nil, "messages")
	MaybeCountPseudoToolCallMiss(c, upstream, apiKey, "m1", toolBody, false, true, nil, "messages")
	MaybeLearnVerifiedToolCalls(c, upstream, apiKey, "m1", toolBody, true, "messages")
	if got := streak(); got != 0 {
		t.Fatalf("真实工具调用应重置连续计数，got streak=%d", got)
	}
	if !enabled() {
		t.Fatal("真实工具调用后条目应保持启用")
	}
}
