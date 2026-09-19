package common

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/racing"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// 竞速质量闸门测试：伪工具调用标记检测 + 软裁决边界。

func TestDetectPseudoToolCallMarker(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"空串", "", false},
		{"干净文本", "我来执行这个命令。git log 的输出如下：a6c93a54", false},
		{"Qwen tool_call", `我来执行。<tool_call>
{"name": "exec"}`, true},
		{"Qwen tool_calls 复数", "<tool_calls>\nfoo", true},
		{"DeepSeek 官方标记", "分析中\n<｜tool▁calls▁begin｜>function", true},
		{"DSML 前缀", "<｜DSML｜tool_calls>\ninvoke", true},
		{"DSML ToolCode（实测形态）", "<｜DSML｜ToolCode>", true},
		{"Qwen function 变体（实测形态）", "</function>\n<parameter=timeout>10</parameter>\n</tool_call>", true},
		{"仅闭标记段（实测漏网形态）", "git log --oneline -1\n</parameter></function></tool_call>", true},
		{"DeepSeek tool_return（实测形态）", "<tool_return>\n<return>......", true},
		{"大小写不敏感", "<TOOL_CALL>", true},
		{"正文讨论 tool 一词不算", "tool call 是模型调工具的机制", false},
		{"HTML 标签不算", "<div>hello</div>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectPseudoToolCallMarker(tt.text); got != tt.want {
				t.Fatalf("DetectPseudoToolCallMarker(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func newQualityGateTestContext(t *testing.T, withGate bool, requestBody string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(requestBody))
	if requestBody != "" {
		c.Set("requestBodyBytes", []byte(requestBody))
	}
	if withGate {
		c.Set(racing.ContextKeyGate, racing.NewGate())
		c.Set(racing.ContextKeyRole, racing.RoleShadow)
	}
	return c, w
}

const toolsRequestBody = `{"model":"gpt-6-astra","stream":true,"tools":[{"type":"function","function":{"name":"exec"}}],"input":"run git log"}`

func TestRacingClaimClientCommitForStream(t *testing.T) {
	t.Run("无闸门直连放行（伪标记也不拦）", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, false, toolsRequestBody)
		if !RacingClaimClientCommitForStream(c, "<tool_call>boom") {
			t.Fatal("无闸门路径必须零开销放行，行为与直连一致")
		}
	})
	t.Run("带工具+伪标记让出提交权", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, toolsRequestBody)
		if RacingClaimClientCommitForStream(c, "我来执行。<tool_call>{\"name\":\"exec\"}") {
			t.Fatal("伪标记分支应让出提交权")
		}
	})
	t.Run("带工具+干净文本正常 claim", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, toolsRequestBody)
		if !RacingClaimClientCommitForStream(c, "PONG") {
			t.Fatal("干净文本应正常 claim")
		}
	})
	t.Run("无工具请求伪标记不拦", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, `{"model":"gpt-x","stream":true,"input":"写一段 DSML 教程"}`)
		if !RacingClaimClientCommitForStream(c, "<｜DSML｜tool_calls> 示例") {
			t.Fatal("不带 tools 的请求不做伪标记校验")
		}
	})
	t.Run("空缓冲不拦", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, toolsRequestBody)
		if !RacingClaimClientCommitForStream(c, "") {
			t.Fatal("空缓冲应正常 claim")
		}
	})
}

// TestNotePseudoToolCallYield 让出即证据：闸门让出路径把已完成的伪标记观察计入
// 白名单负反馈（无 verified 条目同样计数，fail-open 窗口收敛的证据源）。
func TestNotePseudoToolCallYield(t *testing.T) {
	restore := config.SwapSharedChannelCompatCacheForTest(config.NewChannelCompatCache())
	defer restore()

	upstream := &config.UpstreamConfig{ChannelUID: "ch_gate", LogicalChannelUID: "lc_gate", Name: "gate-ch"}
	const apiKey = "sk-gate"
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	forcedBody := `{"model":"m1","stream":true,"tools":[{"type":"function","function":{"name":"exec"}}],"tool_choice":"required"}`

	streakOf := func(model string) (int, bool) {
		state, ok := config.SharedChannelCompatCache().Trait("lc_gate#responses", keyHash, model, config.TraitVerifiedToolCalls)
		return state.AutoMissStreak, ok
	}

	t.Run("有身份+auto 带工具+伪标记：让出并计数", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, toolsRequestBody)
		setToolCallLearningIdentity(c, upstream, apiKey, "m1", "responses")
		if RacingClaimClientCommitForStream(c, "我来执行。<tool_call>{\"name\":\"exec\"}") {
			t.Fatal("伪标记分支应让出提交权")
		}
		streak, ok := streakOf("m1")
		if !ok || streak != 1 {
			t.Fatalf("让出应计一次 miss（streak=1），got streak=%d, ok=%v", streak, ok)
		}
	})

	t.Run("无身份：让出但不产生状态", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, toolsRequestBody)
		if RacingClaimClientCommitForStream(c, "<tool_call>boom") {
			t.Fatal("伪标记分支应让出提交权")
		}
		// 用未参与其他子用例的模型判定（共享缓存内 m1/m2 已有合法状态）
		if _, ok := streakOf("m_none"); ok {
			t.Fatal("无学习身份时不应产生任何状态")
		}
	})

	t.Run("强制 tool_choice：让出但不计数（直连完成路径覆盖，不双算）", func(t *testing.T) {
		c, _ := newQualityGateTestContext(t, true, forcedBody)
		setToolCallLearningIdentity(c, upstream, apiKey, "m3", "responses")
		if RacingClaimClientCommitForStream(c, "<tool_call>boom") {
			t.Fatal("伪标记分支应让出提交权")
		}
		if _, ok := streakOf("m3"); ok {
			t.Fatal("强制 tool_choice 的让出不应计入 pseudo-miss")
		}
	})

	t.Run("连续让出达阈值：构成影子规避证据", func(t *testing.T) {
		for i := 0; i < 2; i++ { // 首个子用例已计 1 次，补足 3 次
			c, _ := newQualityGateTestContext(t, true, toolsRequestBody)
			setToolCallLearningIdentity(c, upstream, apiKey, "m1", "responses")
			RacingClaimClientCommitForStream(c, "<tool_call>boom")
		}
		if !config.SharedChannelCompatCache().VerifiedToolCallPseudoMissed("lc_gate#responses", "m1") {
			t.Fatal("连续 3 次让出计数后应判定劣化（fail-open 窗口不派影子）")
		}
	})

	t.Run("已验证组合连续让出达阈值：撤销白名单", func(t *testing.T) {
		cache := config.SharedChannelCompatCache()
		cache.Record("lc_gate#responses", keyHash, "m2", config.TraitVerifiedToolCalls, true, config.CompatSourceRuntimeSignal, "e")
		for i := 0; i < 3; i++ {
			c, _ := newQualityGateTestContext(t, true, toolsRequestBody)
			setToolCallLearningIdentity(c, upstream, apiKey, "m2", "responses")
			RacingClaimClientCommitForStream(c, "<tool_call>boom")
		}
		state, ok := cache.Trait("lc_gate#responses", keyHash, "m2", config.TraitVerifiedToolCalls)
		if !ok || state.Enabled {
			t.Fatalf("已验证组合连续 3 次让出计数应撤销白名单，got %+v, ok=%v", state, ok)
		}
	})
}

// TestToolWhitelistAllowsColdStartDiscipline 冷启动影子纪律：协议白名单为空的
// fail-open 窗口内，连续伪标记 miss 达阈值的已知劣化组合不派影子；无证据组合
// 照常放行；白名单非空时排他规则优先。
func TestToolWhitelistAllowsColdStartDiscipline(t *testing.T) {
	restore := config.SwapSharedChannelCompatCacheForTest(config.NewChannelCompatCache())
	defer restore()
	cache := config.SharedChannelCompatCache()

	runs := &racingRuns{
		needsToolWhitelist: true,
		in:                 &RacingAttemptInput{Kind: scheduler.ChannelKindResponses, Model: "m1"},
	}
	bad := &config.UpstreamConfig{ChannelUID: "ch_bad", LogicalChannelUID: "lc_bad", Name: "bad"}
	clean := &config.UpstreamConfig{ChannelUID: "ch_clean", LogicalChannelUID: "lc_clean", Name: "clean"}

	// 无任何证据：fail-open 全放行（不堵冷启动）
	if !runs.toolWhitelistAllows(bad, "m1") || !runs.toolWhitelistAllows(clean, "m1") {
		t.Fatal("无证据时 fail-open 应放行全部组合")
	}

	// 劣化组合连续 miss 达阈值：窗口内不派影子；无证据组合不受影响
	for i := 0; i < 3; i++ {
		cache.RecordVerifiedToolCallPseudoMiss("lc_bad#responses", "k1", "m1")
	}
	if runs.toolWhitelistAllows(bad, "m1") {
		t.Fatal("连续伪标记 miss 达阈值的组合在 fail-open 窗口内不应获派影子")
	}
	if !runs.toolWhitelistAllows(clean, "m1") {
		t.Fatal("无证据组合应照常放行")
	}
	// 跨模型隔离：劣化证据不外溢到同路由其他模型
	if !runs.toolWhitelistAllows(bad, "m2") {
		t.Fatal("同路由其他模型不应受 miss 计数影响")
	}
	// 候选模型为空时回退请求模型判定
	if runs.toolWhitelistAllows(bad, "") {
		t.Fatal("候选模型为空应回退请求模型判定（m1 已劣化，不应放行）")
	}

	// 白名单非空时排他规则优先：成员放行，非成员（含无证据组合）拒绝
	cache.Record("lc_clean#responses", "k1", "m1", config.TraitVerifiedToolCalls, true, config.CompatSourceRuntimeSignal, "e")
	if !runs.toolWhitelistAllows(clean, "m1") {
		t.Fatal("白名单成员应放行")
	}
	if runs.toolWhitelistAllows(bad, "m1") {
		t.Fatal("白名单非空时非成员应拒绝")
	}

	// 不带工具的请求恒放行
	runsNoTools := &racingRuns{
		needsToolWhitelist: false,
		in:                 &RacingAttemptInput{Kind: scheduler.ChannelKindResponses, Model: "m1"},
	}
	if !runsNoTools.toolWhitelistAllows(bad, "m1") {
		t.Fatal("不带工具的请求恒放行")
	}
}
