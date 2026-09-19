package config

import (
	"os"
	"testing"
	"time"
)

// IsToolCallUnsupportedForChannelModel 的查询口径：
// 渠道×模型命中、跨模型/跨渠道隔离、TTL 过期失效（docs/specs/tool-call-capability.md §4.1）。
func TestIsToolCallUnsupportedForChannelModel(t *testing.T) {
	cache := NewChannelCompatCache()

	if cache.IsToolCallUnsupportedForChannelModel("ch_a", "m1") {
		t.Fatal("无学习记录时应 fail-open 返回 false")
	}

	if !cache.Record("ch_a", "keyhash1", "m1", TraitNoToolCallSupport, true, CompatSourceProbe, "probe evidence") {
		t.Fatal("首次记录应返回 true")
	}

	if !cache.IsToolCallUnsupportedForChannelModel("ch_a", "m1") {
		t.Fatal("命中记录应返回 true")
	}
	if cache.IsToolCallUnsupportedForChannelModel("ch_a", "m2") {
		t.Fatal("同渠道其他模型不应命中")
	}
	if cache.IsToolCallUnsupportedForChannelModel("ch_b", "m1") {
		t.Fatal("其他渠道不应命中")
	}

	// 重复记录相同结论不视为新增
	if cache.Record("ch_a", "keyhash1", "m1", TraitNoToolCallSupport, true, CompatSourceProbe, "probe evidence") {
		t.Fatal("相同结论重复记录应返回 false")
	}
}

func TestIsToolCallUnsupportedForChannelModelTTL(t *testing.T) {
	// 通过落盘文件注入过期条目，验证 TTL 判定（无需等待真实 24h）。
	expired := time.Now().Add(-25 * time.Hour).Format(time.RFC3339Nano)
	path := t.TempDir() + "/compat.json"
	state := `{
		"ch_old:keyhash1:m1": {
			"traits": {"no_tool_call_support": {"enabled": true, "source": "probe", "evidence": "old", "learned_at": "` + expired + `"}},
			"detected_at": "` + expired + `"
		}
	}`
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatalf("写入状态文件失败: %v", err)
	}

	cache := NewChannelCompatCacheWithPersistence(path)
	if cache.IsToolCallUnsupportedForChannelModel("ch_old", "m1") {
		t.Fatal("TTL 过期条目不应命中")
	}
}

func TestIsToolCallUnsupportedForChannelModelEmptyArgs(t *testing.T) {
	cache := NewChannelCompatCache()
	_ = cache.Record("ch_a", "k", "m1", TraitNoToolCallSupport, true, CompatSourceProbe, "e")
	if cache.IsToolCallUnsupportedForChannelModel("", "m1") || cache.IsToolCallUnsupportedForChannelModel("ch_a", "") {
		t.Fatal("空参数应直接返回 false")
	}
}

// ToolRouteIdentity 的锚选择与协议维度（表驱动）。
func TestToolRouteIdentity(t *testing.T) {
	cases := []struct {
		name string
		up   *UpstreamConfig
		kind string
		want string
	}{
		{"逻辑锚优先", &UpstreamConfig{ChannelUID: "ch_new", LogicalChannelUID: "lc_x"}, "responses", "lc_x#responses"},
		{"无逻辑锚回退物理 UID", &UpstreamConfig{ChannelUID: "ch_old"}, "chat", "ch_old#chat"},
		{"kind 空返回裸锚", &UpstreamConfig{LogicalChannelUID: "lc_x"}, "", "lc_x"},
		{"kind 大小写归一", &UpstreamConfig{LogicalChannelUID: "lc_x"}, " Responses ", "lc_x#responses"},
		{"nil 渠道", nil, "chat", ""},
		{"无任何 UID", &UpstreamConfig{Name: "n"}, "chat", ""},
		{"两 UID 都空白", &UpstreamConfig{ChannelUID: "  ", LogicalChannelUID: " "}, "chat", ""},
	}
	for _, tc := range cases {
		if got := ToolRouteIdentity(tc.up, tc.kind); got != tc.want {
			t.Fatalf("%s: ToolRouteIdentity = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// VerifiedToolCallRoutes 的口径：按执行协议独立聚合、只认 runtime 来源、
// 跨协议隔离、历史裸键（重铸前旧格式）不参与任何协议集合。
func TestVerifiedToolCallRoutesKindScoping(t *testing.T) {
	cache := NewChannelCompatCache()

	_ = cache.Record("lc_a#responses", "k1", "m1", TraitVerifiedToolCalls, true, CompatSourceRuntimeSignal, "e")
	_ = cache.Record("lc_b#messages", "k1", "m2", TraitVerifiedToolCalls, true, CompatSourceRuntimeSignal, "e")
	// 探针来源不进排他集合（只认 runtime）
	_ = cache.Record("lc_c#responses", "k1", "m3", TraitVerifiedToolCalls, true, CompatSourceProbe, "e")
	// 历史裸键（无 #kind 段）：不参与任何协议集合
	_ = cache.Record("lc_d", "k1", "m4", TraitVerifiedToolCalls, true, CompatSourceRuntimeSignal, "e")

	resp := cache.VerifiedToolCallRoutes("responses", true)
	if len(resp) != 1 || !resp["lc_a#responses"] {
		t.Fatalf("responses 集合应只含 lc_a，got %v", resp)
	}
	msg := cache.VerifiedToolCallRoutes("messages", true)
	if len(msg) != 1 || !msg["lc_b#messages"] {
		t.Fatalf("messages 集合应只含 lc_b，got %v", msg)
	}
	// onlyRuntime=false 时探针来源纳入
	respAll := cache.VerifiedToolCallRoutes("responses", false)
	if len(respAll) != 2 || !respAll["lc_c#responses"] {
		t.Fatalf("responses 全量集合应含探针来源，got %v", respAll)
	}
	// 未学习协议 fail-open（空集合）
	if gem := cache.VerifiedToolCallRoutes("gemini", true); len(gem) != 0 {
		t.Fatalf("gemini 集合应为空，got %v", gem)
	}
	// 空 kind 直接返回 nil
	if got := cache.VerifiedToolCallRoutes("", true); got != nil {
		t.Fatalf("空 kind 应返回 nil，got %v", got)
	}
}

// RecordVerifiedToolCallPseudoMiss / ClearVerifiedToolCallPseudoMiss /
// VerifiedToolCallPseudoMissed 的口径：无 verified 条目同样计数（fail-open 窗口的
// 劣化证据留存，供竞速影子规避）；启用条目连续计数达阈值撤销并摘牌；真实工具调用
// 重置（连续而非累计）；撤销后继续计数（重新累积影子规避证据）。
func TestRecordVerifiedToolCallPseudoMiss(t *testing.T) {
	cache := NewChannelCompatCache()

	// 无 verified 条目：同样计数（Enabled=false 的负证据条目），不撤销
	if streak, revoked := cache.RecordVerifiedToolCallPseudoMiss("lc_a#responses", "k1", "m1"); streak != 1 || revoked {
		t.Fatalf("无条目时也应计数 (1,false)，got (%d,%v)", streak, revoked)
	}
	state, ok := cache.Trait("lc_a#responses", "k1", "m1", TraitVerifiedToolCalls)
	if !ok || state.Enabled || state.AutoMissStreak != 1 {
		t.Fatalf("无 verified 条目时应记入 Enabled=false 的 miss 计数，got %+v, ok=%v", state, ok)
	}
	// 未达阈值：不构成劣化证据
	if cache.VerifiedToolCallPseudoMissed("lc_a#responses", "m1") {
		t.Fatal("streak 未达阈值不应判定劣化")
	}

	_ = cache.Record("lc_a#responses", "k1", "m1", TraitVerifiedToolCalls, true, CompatSourceRuntimeSignal, "e")

	// 第 1、2 次：计数递增但不撤销
	for i, want := range []int{1, 2} {
		streak, revoked := cache.RecordVerifiedToolCallPseudoMiss("lc_a#responses", "k1", "m1")
		if streak != want || revoked {
			t.Fatalf("第 %d 次计数应=%d 且不撤销，got (%d,%v)", i+1, want, streak, revoked)
		}
	}
	if routes := cache.VerifiedToolCallRoutes("responses", true); !routes["lc_a#responses"] {
		t.Fatal("未达阈值时条目应保持启用")
	}

	// Clear 重置：连续语义（真实工具调用成功后重新计数）
	cache.ClearVerifiedToolCallPseudoMiss("lc_a#responses", "k1", "m1")
	if streak, _ := cache.RecordVerifiedToolCallPseudoMiss("lc_a#responses", "k1", "m1"); streak != 1 {
		t.Fatalf("Clear 后计数应从 1 开始，got %d", streak)
	}

	// 连续达阈值：撤销并摘牌
	cache.RecordVerifiedToolCallPseudoMiss("lc_a#responses", "k1", "m1")
	streak, revoked := cache.RecordVerifiedToolCallPseudoMiss("lc_a#responses", "k1", "m1")
	if streak != 3 || !revoked {
		t.Fatalf("第 3 次应触发撤销，got (%d,%v)", streak, revoked)
	}
	if routes := cache.VerifiedToolCallRoutes("responses", true); len(routes) != 0 {
		t.Fatalf("撤销后路由集合应为空，got %v", routes)
	}
	// 撤销时计数清零：刚摘牌的组合不立即构成影子规避证据（再积累需新一轮连续 miss）
	if cache.VerifiedToolCallPseudoMissed("lc_a#responses", "m1") {
		t.Fatal("撤销清零后不应立即判定劣化")
	}

	// 已禁用条目：继续计数（撤销不是终点，重新累积达阈值后供 fail-open 窗口规避影子）
	if streak, revoked := cache.RecordVerifiedToolCallPseudoMiss("lc_a#responses", "k1", "m1"); streak != 1 || revoked {
		t.Fatalf("已禁用条目应继续计数 (1,false)，got (%d,%v)", streak, revoked)
	}
	cache.RecordVerifiedToolCallPseudoMiss("lc_a#responses", "k1", "m1")
	cache.RecordVerifiedToolCallPseudoMiss("lc_a#responses", "k1", "m1")
	if !cache.VerifiedToolCallPseudoMissed("lc_a#responses", "m1") {
		t.Fatal("禁用条目重新累积达阈值后应判定劣化（fail-open 窗口不派影子）")
	}
}

// VerifiedToolCallPseudoMissed 的判定口径：路由×模型维度、任一 Key 命中即劣化、
// 跨模型/跨路由隔离、空参数与无记录 fail-open。
func TestVerifiedToolCallPseudoMissed(t *testing.T) {
	cache := NewChannelCompatCache()

	if cache.VerifiedToolCallPseudoMissed("lc_a#responses", "m1") {
		t.Fatal("无记录时应 fail-open 返回 false")
	}

	// 达阈值：任一 Key 命中即判定
	for i := 0; i < 3; i++ {
		cache.RecordVerifiedToolCallPseudoMiss("lc_a#responses", "k1", "m1")
	}
	if !cache.VerifiedToolCallPseudoMissed("lc_a#responses", "m1") {
		t.Fatal("连续 3 次 miss 应判定劣化")
	}
	// 跨模型/跨路由/跨协议隔离
	if cache.VerifiedToolCallPseudoMissed("lc_a#responses", "m2") {
		t.Fatal("同路由其他模型不应命中")
	}
	if cache.VerifiedToolCallPseudoMissed("lc_b#responses", "m1") {
		t.Fatal("其他路由不应命中")
	}
	if cache.VerifiedToolCallPseudoMissed("lc_a#messages", "m1") {
		t.Fatal("同渠道其他协议不应命中")
	}
	// 空参数直接 false
	if cache.VerifiedToolCallPseudoMissed("", "m1") || cache.VerifiedToolCallPseudoMissed("lc_a#responses", "") {
		t.Fatal("空参数应直接返回 false")
	}
	// 真实工具调用重建（正向学习覆盖状态）后清零
	_ = cache.Record("lc_a#responses", "k1", "m1", TraitVerifiedToolCalls, true, CompatSourceRuntimeSignal, "e")
	if cache.VerifiedToolCallPseudoMissed("lc_a#responses", "m1") {
		t.Fatal("真实工具调用重建后不应判定劣化")
	}
}
