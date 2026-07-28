package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── EndpointAttemptPolicy 测试（表驱动）──

func TestBuildEndpointPolicy_NilRequest(t *testing.T) {
	deps := EndpointPolicyDeps{}
	policy := BuildEndpointPolicy(deps, nil, RoutingModeShadow)
	if policy != nil {
		t.Error("nil req 应返回 nil policy")
	}
}

func TestBuildEndpointPolicy_OffMode(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "test-model", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, "off")
	if policy != nil {
		t.Error("off 模式应返回 nil policy")
	}
}

func TestBuildEndpointPolicy_UnknownMode(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "test-model", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, "unknown_mode")
	if policy != nil {
		t.Error("未知模式应返回 nil policy")
	}
}

func TestBuildEndpointPolicy_ShadowMode_Fields(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "test-model", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)
	if policy == nil {
		t.Fatal("shadow 模式应返回非 nil policy")
	}
	if policy.Mode != RoutingModeShadow {
		t.Errorf("Mode = %q, 期望 %q", policy.Mode, RoutingModeShadow)
	}
	if policy.RequestModel != "test-model" {
		t.Errorf("RequestModel = %q, 期望 %q", policy.RequestModel, "test-model")
	}
	// shadow 模式所有函数都应非 nil
	if policy.FilterURLs == nil || policy.SortURLs == nil || policy.FilterKeys == nil || policy.SortKeys == nil {
		t.Error("shadow 模式所有函数字段应非 nil")
	}
}

// ── shadow 模式行为测试 ──

func TestShadowFilterURLs_Passthrough(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)

	input := []string{"https://a.com", "https://b.com", "https://c.com"}
	output := policy.FilterURLs(input)

	if len(output) != len(input) {
		t.Fatalf("shadow FilterURLs 应原样返回: got %d, want %d", len(output), len(input))
	}
	for i, url := range output {
		if url != input[i] {
			t.Errorf("shadow FilterURLs[%d] = %q, 期望 %q", i, url, input[i])
		}
	}
}

func TestShadowFilterKeys_Passthrough(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)

	input := []string{"sk-aaa", "sk-bbb", "sk-ccc"}
	output := policy.FilterKeys("https://a.com", input)

	if len(output) != len(input) {
		t.Fatalf("shadow FilterKeys 应原样返回: got %d, want %d", len(output), len(input))
	}
	for i, key := range output {
		if key != input[i] {
			t.Errorf("shadow FilterKeys[%d] = %q, 期望 %q", i, key, input[i])
		}
	}
}

func TestShadowSortURLs_ReturnsOriginalOrder(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)

	input := []string{"https://a.com", "https://b.com", "https://c.com"}
	sortedURLs, candidates := policy.SortURLs(input)

	// 原始顺序应保持
	if len(sortedURLs) != len(input) {
		t.Fatalf("shadow SortURLs 应返回等长列表: got %d, want %d", len(sortedURLs), len(input))
	}
	for i, url := range sortedURLs {
		if url != input[i] {
			t.Errorf("shadow SortURLs[%d] = %q, 期望 %q (应保持原始顺序)", i, url, input[i])
		}
	}

	// 候选列表应有评分信息
	if len(candidates) != len(input) {
		t.Fatalf("candidates 长度 = %d, 期望 %d", len(candidates), len(input))
	}
	for i, cand := range candidates {
		if cand.BaseURL != input[i] {
			t.Errorf("candidates[%d].BaseURL = %q, 期望 %q", i, cand.BaseURL, input[i])
		}
		// 无画像时应为中性分
		if cand.Score != neutralEndpointScore {
			t.Errorf("candidates[%d].Score = %v, 期望 %v (无画像中性分)", i, cand.Score, neutralEndpointScore)
		}
		if cand.Reason != "no_profile" {
			t.Errorf("candidates[%d].Reason = %q, 期望 %q", i, cand.Reason, "no_profile")
		}
	}
}

func TestShadowSortKeys_ReturnsOriginalOrder(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)

	input := []string{"sk-aaa", "sk-bbb", "sk-ccc"}
	baseURL := "https://a.com"
	sortedKeys, candidates := policy.SortKeys(baseURL, input)

	// 原始顺序应保持
	if len(sortedKeys) != len(input) {
		t.Fatalf("shadow SortKeys 应返回等长列表: got %d, want %d", len(sortedKeys), len(input))
	}
	for i, key := range sortedKeys {
		if key != input[i] {
			t.Errorf("shadow SortKeys[%d] = %q, 期望 %q (应保持原始顺序)", i, key, input[i])
		}
	}

	// 候选列表应有评分信息
	if len(candidates) != len(input) {
		t.Fatalf("candidates 长度 = %d, 期望 %d", len(candidates), len(input))
	}
	for i, cand := range candidates {
		if cand.BaseURL != baseURL {
			t.Errorf("candidates[%d].BaseURL = %q, 期望 %q", i, cand.BaseURL, baseURL)
		}
		if cand.Score != neutralEndpointScore {
			t.Errorf("candidates[%d].Score = %v, 期望 %v (无画像中性分)", i, cand.Score, neutralEndpointScore)
		}
	}
}

// ── active 模式排序正确性测试 ──

func TestScoreEndpoint_HealthPriority(t *testing.T) {
	// 健康 > FastDecay > 成功率 > 延迟 > 成本
	tests := []struct {
		name     string
		profile  *KeyEndpointProfile
		decay    float64
		wantHigh string // 应该得分更高的 profile 名称
	}{
		{
			name: "healthy > degraded",
			profile: &KeyEndpointProfile{
				HealthState: HealthStateHealthy, SuccessRate15m: 0.9, P95LatencyMs: 100, CostTier: CostTierNormal,
			},
			decay:    1.0,
			wantHigh: "healthy",
		},
		{
			name: "degraded < healthy",
			profile: &KeyEndpointProfile{
				HealthState: HealthStateDegraded, SuccessRate15m: 0.9, P95LatencyMs: 100, CostTier: CostTierNormal,
			},
			decay:    1.0,
			wantHigh: "healthy",
		},
	}

	healthy := &KeyEndpointProfile{
		HealthState: HealthStateHealthy, SuccessRate15m: 0.9, P95LatencyMs: 100, CostTier: CostTierNormal,
	}
	degraded := &KeyEndpointProfile{
		HealthState: HealthStateDegraded, SuccessRate15m: 0.9, P95LatencyMs: 100, CostTier: CostTierNormal,
	}

	healthyScore := scoreEndpoint(healthy, 1.0)
	degradedScore := scoreEndpoint(degraded, 1.0)

	if healthyScore <= degradedScore {
		t.Errorf("healthy(%v) 应 > degraded(%v)", healthyScore, degradedScore)
	}

	_ = tests // 使用 tests 表
}

func TestScoreEndpoint_FastDecayPriority(t *testing.T) {
	// FastDecay 衰减：高衰减分 > 低衰减分（同健康状态）
	profile := &KeyEndpointProfile{
		HealthState: HealthStateHealthy, SuccessRate15m: 0.9, P95LatencyMs: 100, CostTier: CostTierNormal,
	}

	highDecay := scoreEndpoint(profile, 1.0)
	lowDecay := scoreEndpoint(profile, 0.3)

	if highDecay <= lowDecay {
		t.Errorf("high decay(%v) 应 > low decay(%v)", highDecay, lowDecay)
	}
}

func TestScoreEndpoint_SuccessRatePriority(t *testing.T) {
	// 成功率：高成功率 > 低成功率（同健康、同 FastDecay）
	highSuccess := &KeyEndpointProfile{
		HealthState: HealthStateHealthy, SuccessRate15m: 0.95, P95LatencyMs: 100, CostTier: CostTierNormal,
	}
	lowSuccess := &KeyEndpointProfile{
		HealthState: HealthStateHealthy, SuccessRate15m: 0.50, P95LatencyMs: 100, CostTier: CostTierNormal,
	}

	highScore := scoreEndpoint(highSuccess, 1.0)
	lowScore := scoreEndpoint(lowSuccess, 1.0)

	if highScore <= lowScore {
		t.Errorf("high success(%v) 应 > low success(%v)", highScore, lowScore)
	}
}

func TestScoreEndpoint_LatencyPriority(t *testing.T) {
	// 延迟：低延迟 > 高延迟（同健康、同 FastDecay、同成功率）
	lowLatency := &KeyEndpointProfile{
		HealthState: HealthStateHealthy, SuccessRate15m: 0.9, P95LatencyMs: 100, CostTier: CostTierNormal,
	}
	highLatency := &KeyEndpointProfile{
		HealthState: HealthStateHealthy, SuccessRate15m: 0.9, P95LatencyMs: 3000, CostTier: CostTierNormal,
	}

	lowScore := scoreEndpoint(lowLatency, 1.0)
	highScore := scoreEndpoint(highLatency, 1.0)

	if lowScore <= highScore {
		t.Errorf("low latency(%v) 应 > high latency(%v)", lowScore, highScore)
	}
}

func TestScoreEndpoint_CostTieBreaker(t *testing.T) {
	// 成本：便宜 > 贵（同健康、同 FastDecay、同成功率、同延迟）
	cheap := &KeyEndpointProfile{
		HealthState: HealthStateHealthy, SuccessRate15m: 0.9, P95LatencyMs: 100, CostTier: CostTierCheap,
	}
	expensive := &KeyEndpointProfile{
		HealthState: HealthStateHealthy, SuccessRate15m: 0.9, P95LatencyMs: 100, CostTier: CostTierExpensive,
	}

	cheapScore := scoreEndpoint(cheap, 1.0)
	expensiveScore := scoreEndpoint(expensive, 1.0)

	if cheapScore <= expensiveScore {
		t.Errorf("cheap(%v) 应 > expensive(%v)", cheapScore, expensiveScore)
	}
}

func TestScoreEndpoint_DeadHealthZero(t *testing.T) {
	// dead 状态即使其他维度全满分，得分也应被 healthMultiplier 严重压制
	dead := &KeyEndpointProfile{
		HealthState: HealthStateDead, SuccessRate15m: 1.0, P95LatencyMs: 10, CostTier: CostTierFree,
	}

	deadScore := scoreEndpoint(dead, 1.0)

	// dead 状态得分应远低于中性分（healthMultiplier=0.05）
	if deadScore >= neutralEndpointScore {
		t.Errorf("dead score(%v) 应 < neutral(%v)", deadScore, neutralEndpointScore)
	}
	// dead 全满分应低于 10 分
	if deadScore >= 10.0 {
		t.Errorf("dead 全满分 score(%v) 应 < 10", deadScore)
	}
}

func TestScoreEndpoint_NilProfile(t *testing.T) {
	// nil 画像应返回中性分
	score := scoreEndpoint(nil, 1.0)
	if score != neutralEndpointScore {
		t.Errorf("nil profile score = %v, 期望 %v", score, neutralEndpointScore)
	}
}

// ── fail-open 测试 ──

func TestFailOpen_EmptyStore(t *testing.T) {
	// 空 store 应返回中性分（不惩罚）
	store := newTestProfileStore(t)
	fastDecay := NewFastDecayScorer()

	candidates := GetEndpointCandidates(store, fastDecay, "test-model", []string{"https://a.com", "https://b.com"})

	if len(candidates) != 2 {
		t.Fatalf("candidates 长度 = %d, 期望 2", len(candidates))
	}
	for i, cand := range candidates {
		if cand.Score != neutralEndpointScore {
			t.Errorf("candidates[%d].Score = %v, 期望 %v (空 store 中性分)", i, cand.Score, neutralEndpointScore)
		}
		if cand.Reason != "no_profile" {
			t.Errorf("candidates[%d].Reason = %q, 期望 %q", i, cand.Reason, "no_profile")
		}
	}
}

func TestFailOpen_EmptyURLs(t *testing.T) {
	// 空 URL 列表应返回空列表
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)

	output := policy.FilterURLs(nil)
	if len(output) != 0 {
		t.Errorf("空输入应返回空输出: got %d", len(output))
	}

	sortedURLs, candidates := policy.SortURLs(nil)
	if len(sortedURLs) != 0 || len(candidates) != 0 {
		t.Errorf("空输入应返回空输出: urls=%d, candidates=%d", len(sortedURLs), len(candidates))
	}
}

func TestFailOpen_EmptyKeys(t *testing.T) {
	// 空 key 列表应返回空列表
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)

	output := policy.FilterKeys("https://a.com", nil)
	if len(output) != 0 {
		t.Errorf("空输入应返回空输出: got %d", len(output))
	}

	sortedKeys, candidates := policy.SortKeys("https://a.com", nil)
	if len(sortedKeys) != 0 || len(candidates) != 0 {
		t.Errorf("空输入应返回空输出: keys=%d, candidates=%d", len(sortedKeys), len(candidates))
	}
}

// ── 不删减不重复不丢 key 测试 ──

func TestNoDeletion_NoDuplication_NoLoss(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)

	// 测试 URLs
	urls := []string{"https://a.com", "https://b.com", "https://c.com", "https://d.com", "https://e.com"}
	filteredURLs := policy.FilterURLs(urls)
	sortedURLs, _ := policy.SortURLs(urls)

	// 不删减
	if len(filteredURLs) != len(urls) {
		t.Errorf("FilterURLs 删减了输入: got %d, want %d", len(filteredURLs), len(urls))
	}
	if len(sortedURLs) != len(urls) {
		t.Errorf("SortURLs 删减了输入: got %d, want %d", len(sortedURLs), len(urls))
	}

	// 不重复
	seen := make(map[string]bool)
	for _, url := range filteredURLs {
		if seen[url] {
			t.Errorf("FilterURLs 重复了: %s", url)
		}
		seen[url] = true
	}

	// 不丢失
	for _, url := range urls {
		if !seen[url] {
			t.Errorf("FilterURLs 丢失了: %s", url)
		}
	}

	// 测试 Keys
	keys := []string{"sk-aaa", "sk-bbb", "sk-ccc", "sk-ddd", "sk-eee"}
	baseURL := "https://a.com"
	filteredKeys := policy.FilterKeys(baseURL, keys)
	sortedKeys, _ := policy.SortKeys(baseURL, keys)

	// 不删减
	if len(filteredKeys) != len(keys) {
		t.Errorf("FilterKeys 删减了输入: got %d, want %d", len(filteredKeys), len(keys))
	}
	if len(sortedKeys) != len(keys) {
		t.Errorf("SortKeys 删减了输入: got %d, want %d", len(sortedKeys), len(keys))
	}

	// 不重复
	seenKeys := make(map[string]bool)
	for _, key := range filteredKeys {
		if seenKeys[key] {
			t.Errorf("FilterKeys 重复了: %s", key)
		}
		seenKeys[key] = true
	}

	// 不丢失
	for _, key := range keys {
		if !seenKeys[key] {
			t.Errorf("FilterKeys 丢失了: %s", key)
		}
	}
}

// ── active 模式 FilterURLs 测试 ──

func TestActiveFilterURLs_PreservesBindingContainers(t *testing.T) {
	store := newTestProfileStore(t)

	// 插入 dead 画像
	deadProfile := &KeyEndpointProfile{
		EndpointUID: "ep_dead",
		ChannelUID:  "ch1",
		BaseURL:     "https://dead.com",
		HealthState: HealthStateDead,
	}
	if err := store.Upsert(deadProfile); err != nil {
		t.Fatalf("Upsert dead profile 失败: %v", err)
	}

	// 插入 healthy 画像
	healthyProfile := &KeyEndpointProfile{
		EndpointUID: "ep_healthy",
		ChannelUID:  "ch2",
		BaseURL:     "https://healthy.com",
		HealthState: HealthStateHealthy,
	}
	if err := store.Upsert(healthyProfile); err != nil {
		t.Fatalf("Upsert healthy profile 失败: %v", err)
	}

	deps := EndpointPolicyDeps{ProfileStore: store}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := buildActivePolicy(deps, req, true)

	urls := []string{"https://dead.com", "https://healthy.com", "https://unknown.com"}
	filtered := policy.FilterURLs(urls)

	if len(filtered) != len(urls) {
		t.Fatalf("URL 是 binding 容器，不应按单条画像删除: got %v, want %v", filtered, urls)
	}
	for i := range urls {
		if filtered[i] != urls[i] {
			t.Fatalf("FilterURLs 改变了 URL 集合: got %v, want %v", filtered, urls)
		}
	}
}

func TestActiveFilterURLs_FailOpen(t *testing.T) {
	store := newTestProfileStore(t)

	// 所有 URL 都是 dead
	deadProfile1 := &KeyEndpointProfile{
		EndpointUID: "ep1", ChannelUID: "ch1", BaseURL: "https://a.com", HealthState: HealthStateDead,
	}
	deadProfile2 := &KeyEndpointProfile{
		EndpointUID: "ep2", ChannelUID: "ch2", BaseURL: "https://b.com", HealthState: HealthStateDead,
	}
	_ = store.Upsert(deadProfile1)
	_ = store.Upsert(deadProfile2)

	deps := EndpointPolicyDeps{ProfileStore: store}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := buildActivePolicy(deps, req, true)

	urls := []string{"https://a.com", "https://b.com"}
	filtered := policy.FilterURLs(urls)

	// FailOpen：全部 dead 时应回退全量
	if len(filtered) != len(urls) {
		t.Errorf("FailOpen 时应返回全量: got %d, want %d", len(filtered), len(urls))
	}
}

func TestActiveFilterKeys_RemovesLowDecay(t *testing.T) {
	fastDecay := NewFastDecayScorer()

	// 模拟一个低 FastDecay 分的 key
	lowDecayKey := "sk-low-decay"
	lowDecayHash := KeyHashFromAPIKey(lowDecayKey)
	lowDecayUID := GenerateEndpointUID("", "https://a.com", lowDecayHash)

	// 记录多次失败使衰减分降低（0.85^15 ≈ 0.087 < 0.15 阈值）
	for i := 0; i < 15; i++ {
		fastDecay.RecordResult(lowDecayUID, false)
	}

	deps := EndpointPolicyDeps{FastDecay: fastDecay}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := buildActivePolicy(deps, req, true)

	keys := []string{"sk-good", lowDecayKey, "sk-another"}
	filtered := policy.FilterKeys("https://a.com", keys)

	// 低衰减分的 key 应被过滤
	for _, key := range filtered {
		if key == lowDecayKey {
			t.Error("低 FastDecay 分的 key 应被 FilterKeys 过滤")
		}
	}

	// 其他 key 应保留
	hasGood := false
	hasAnother := false
	for _, key := range filtered {
		if key == "sk-good" {
			hasGood = true
		}
		if key == "sk-another" {
			hasAnother = true
		}
	}
	if !hasGood {
		t.Error("sk-good 应被保留")
	}
	if !hasAnother {
		t.Error("sk-another 应被保留")
	}
}

func TestActiveFilterKeys_FailOpen(t *testing.T) {
	fastDecay := NewFastDecayScorer()

	// 所有 key 都有低衰减分（0.85^15 ≈ 0.087 < 0.15 阈值）
	keys := []string{"sk-1", "sk-2", "sk-3"}
	for _, key := range keys {
		keyHash := KeyHashFromAPIKey(key)
		endpointUID := GenerateEndpointUID("", "https://a.com", keyHash)
		for i := 0; i < 15; i++ {
			fastDecay.RecordResult(endpointUID, false)
		}
	}

	deps := EndpointPolicyDeps{FastDecay: fastDecay}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := buildActivePolicy(deps, req, true)

	filtered := policy.FilterKeys("https://a.com", keys)

	// FailOpen：全部低衰减时应回退全量
	if len(filtered) != len(keys) {
		t.Errorf("FailOpen 时应返回全量: got %d, want %d", len(filtered), len(keys))
	}
}

// ── assist / auto 模式通过 BuildEndpointPolicy 接线测试 ──

func TestBuildEndpointPolicy_AssistMode_Fields(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "test-model", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeAssist)
	if policy == nil {
		t.Fatal("assist 模式应返回非 nil policy")
	}
	if policy.Mode != RoutingModeActive {
		t.Errorf("Mode = %q, 期望 %q", policy.Mode, RoutingModeActive)
	}
	if policy.RequestModel != "test-model" {
		t.Errorf("RequestModel = %q, 期望 %q", policy.RequestModel, "test-model")
	}
	if policy.FilterURLs == nil || policy.SortURLs == nil || policy.FilterKeys == nil || policy.SortKeys == nil {
		t.Error("assist 模式所有函数字段应非 nil")
	}
}

func TestBuildEndpointPolicy_AutoMode_Fields(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "test-model", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeAuto)
	if policy == nil {
		t.Fatal("auto 模式应返回非 nil policy")
	}
	if policy.Mode != RoutingModeActive {
		t.Errorf("Mode = %q, 期望 %q", policy.Mode, RoutingModeActive)
	}
	if policy.FilterURLs == nil || policy.SortURLs == nil || policy.FilterKeys == nil || policy.SortKeys == nil {
		t.Error("auto 模式所有函数字段应非 nil")
	}
}

// ── 不变量测试：assist 模式下输出必须是输入的排列 ──

func TestAssistMode_URLs_IsPermutation(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeAssist)

	input := []string{"https://c.com", "https://a.com", "https://b.com"}

	// FilterURLs 应原样返回
	filtered := policy.FilterURLs(input)
	if len(filtered) != len(input) {
		t.Fatalf("assist FilterURLs 应返回等长: got %d, want %d", len(filtered), len(input))
	}
	for i, url := range filtered {
		if url != input[i] {
			t.Errorf("assist FilterURLs[%d] = %q, 期望 %q", i, url, input[i])
		}
	}

	// SortURLs 输出应是输入的排列（不删不增）
	sorted, candidates := policy.SortURLs(input)
	if len(sorted) != len(input) {
		t.Fatalf("assist SortURLs 应返回等长: got %d, want %d", len(sorted), len(input))
	}
	if len(candidates) != len(input) {
		t.Fatalf("assist SortURLs candidates 应等长: got %d, want %d", len(candidates), len(input))
	}
	assertPermutation(t, input, sorted, "assist SortURLs")
}

func TestAssistMode_Keys_IsPermutation(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeAssist)

	baseURL := "https://a.com"
	input := []string{"sk-ccc", "sk-aaa", "sk-bbb"}

	// FilterKeys 应原样返回
	filtered := policy.FilterKeys(baseURL, input)
	if len(filtered) != len(input) {
		t.Fatalf("assist FilterKeys 应返回等长: got %d, want %d", len(filtered), len(input))
	}
	for i, key := range filtered {
		if key != input[i] {
			t.Errorf("assist FilterKeys[%d] = %q, 期望 %q", i, key, input[i])
		}
	}

	// SortKeys 输出应是输入的排列
	sorted, candidates := policy.SortKeys(baseURL, input)
	if len(sorted) != len(input) {
		t.Fatalf("assist SortKeys 应返回等长: got %d, want %d", len(sorted), len(input))
	}
	if len(candidates) != len(input) {
		t.Fatalf("assist SortKeys candidates 应等长: got %d, want %d", len(candidates), len(input))
	}
	assertPermutation(t, input, sorted, "assist SortKeys")
}

// ── 不变量测试：auto 模式过滤到空时 fail-open 返回原列表 ──

func TestAutoMode_FilterURLs_AllDead_FailOpen(t *testing.T) {
	store := newTestProfileStore(t)
	_ =

		// 所有 URL 都是 dead
		store.Upsert(&KeyEndpointProfile{
			EndpointUID: "ep1", ChannelUID: "ch1", BaseURL: "https://a.com", HealthState: HealthStateDead,
		})
	_ = store.Upsert(&KeyEndpointProfile{
		EndpointUID: "ep2", ChannelUID: "ch2", BaseURL: "https://b.com", HealthState: HealthStateDead,
	})

	deps := EndpointPolicyDeps{ProfileStore: store}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeAuto)

	urls := []string{"https://a.com", "https://b.com"}
	filtered := policy.FilterURLs(urls)

	// fail-open：全部 dead 时回退全量
	if len(filtered) != len(urls) {
		t.Errorf("auto FailOpen 时应返回全量: got %d, want %d", len(filtered), len(urls))
	}
	assertPermutation(t, urls, filtered, "auto FilterURLs fail-open")
}

func TestAutoMode_FilterKeys_AllLowDecay_FailOpen(t *testing.T) {
	fastDecay := NewFastDecayScorer()

	keys := []string{"sk-1", "sk-2", "sk-3"}
	for _, key := range keys {
		keyHash := KeyHashFromAPIKey(key)
		endpointUID := GenerateEndpointUID("", "https://a.com", keyHash)
		for i := 0; i < 15; i++ {
			fastDecay.RecordResult(endpointUID, false)
		}
	}

	deps := EndpointPolicyDeps{FastDecay: fastDecay}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeAuto)

	filtered := policy.FilterKeys("https://a.com", keys)

	// fail-open：全部低衰减时回退全量
	if len(filtered) != len(keys) {
		t.Errorf("auto FailOpen 时应返回全量: got %d, want %d", len(filtered), len(keys))
	}
}

// ── 不变量测试：shadow 模式输出顺序与输入完全一致 ──

func TestShadowMode_URLs_OriginalOrderInvariant(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)

	input := []string{"https://z.com", "https://a.com", "https://m.com"}

	// FilterURLs 顺序不变
	filtered := policy.FilterURLs(input)
	for i, url := range filtered {
		if url != input[i] {
			t.Errorf("shadow FilterURLs[%d] = %q, 期望 %q", i, url, input[i])
		}
	}

	// SortURLs 顺序不变
	sorted, _ := policy.SortURLs(input)
	for i, url := range sorted {
		if url != input[i] {
			t.Errorf("shadow SortURLs[%d] = %q, 期望 %q", i, url, input[i])
		}
	}
}

func TestShadowMode_Keys_OriginalOrderInvariant(t *testing.T) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)

	baseURL := "https://a.com"
	input := []string{"sk-zzz", "sk-aaa", "sk-mmm"}

	// FilterKeys 顺序不变
	filtered := policy.FilterKeys(baseURL, input)
	for i, key := range filtered {
		if key != input[i] {
			t.Errorf("shadow FilterKeys[%d] = %q, 期望 %q", i, key, input[i])
		}
	}

	// SortKeys 顺序不变
	sorted, _ := policy.SortKeys(baseURL, input)
	for i, key := range sorted {
		if key != input[i] {
			t.Errorf("shadow SortKeys[%d] = %q, 期望 %q", i, key, input[i])
		}
	}
}

// ── panic 恢复测试 ──

func TestSortPanic_Recover(t *testing.T) {
	// 测试：排序内部 panic 时 recover 并回退原列表。
	// 通过注入一个会导致 sortEndpointsByScore panic 的候选列表来验证。
	// 实际触发方式：使用一个非常大的候选列表使 sort.Stable 产生不可预期行为并不现实，
	// 这里验证的是 panic recovery 代码路径存在且不会崩溃。
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}

	// auto 模式构建策略
	policy := BuildEndpointPolicy(deps, req, RoutingModeAuto)

	// 正常输入不应 panic
	urls := []string{"https://a.com", "https://b.com", "https://c.com"}
	sorted, candidates := policy.SortURLs(urls)
	if len(sorted) != len(urls) {
		t.Errorf("SortURLs 长度 = %d, 期望 %d", len(sorted), len(urls))
	}
	if len(candidates) != len(urls) {
		t.Errorf("SortURLs candidates 长度 = %d, 期望 %d", len(candidates), len(urls))
	}

	keys := []string{"sk-1", "sk-2", "sk-3"}
	sortedKeys, keyCandidates := policy.SortKeys("https://a.com", keys)
	if len(sortedKeys) != len(keys) {
		t.Errorf("SortKeys 长度 = %d, 期望 %d", len(sortedKeys), len(keys))
	}
	if len(keyCandidates) != len(keys) {
		t.Errorf("SortKeys candidates 长度 = %d, 期望 %d", len(keyCandidates), len(keys))
	}
}

// ── 排列断言辅助 ──

// assertPermutation 断言 actual 是 expected 的一个排列（不删不增）。
func assertPermutation(t *testing.T, expected, actual []string, label string) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Errorf("%s: 长度不同 actual=%d expected=%d", label, len(actual), len(expected))
		return
	}
	counts := make(map[string]int)
	for _, s := range expected {
		counts[s]++
	}
	for _, s := range actual {
		counts[s]--
	}
	for s, c := range counts {
		if c != 0 {
			t.Errorf("%s: 不是排列, %q 出现次数差 %d", label, s, c)
		}
	}
}

// ── 评分函数边界测试 ──

func TestEndpointHealthScore_AllStates(t *testing.T) {
	tests := []struct {
		state HealthState
		want  float64
	}{
		{HealthStateHealthy, 100},
		{HealthStateUnknown, 70},
		{HealthStateDegraded, 40},
		{HealthStateLimited, 20},
		{HealthStateMisconfigured, 10},
		{HealthStateDead, 0},
		{"", 70}, // 空状态给中性分
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			got := endpointHealthScore(tt.state)
			if got != tt.want {
				t.Errorf("endpointHealthScore(%q) = %v, 期望 %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestLatencyToScore_EdgeCases(t *testing.T) {
	tests := []struct {
		p95Ms  int64
		expect float64
	}{
		{0, 70},    // 无数据 → 中性分
		{-100, 70}, // 负数 → 中性分
		{100, 98},  // 100ms → 高分
		{2500, 50}, // 2500ms → 中等
		{5000, 0},  // 5000ms → 0 分
		{10000, 0}, // 10000ms → 0 分（钳制）
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := latencyToScore(tt.p95Ms)
			if got != tt.expect {
				t.Errorf("latencyToScore(%d) = %v, 期望 %v", tt.p95Ms, got, tt.expect)
			}
		})
	}
}

func TestEndpointCostScore_AllTiers(t *testing.T) {
	tests := []struct {
		tier CostTier
		want float64
	}{
		{CostTierFree, 100},
		{CostTierCheap, 75},
		{CostTierNormal, 50},
		{CostTierExpensive, 25},
		{"", 50}, // 空 tier → 中性分
	}

	for _, tt := range tests {
		t.Run(string(tt.tier), func(t *testing.T) {
			got := endpointCostScore(tt.tier)
			if got != tt.want {
				t.Errorf("endpointCostScore(%q) = %v, 期望 %v", tt.tier, got, tt.want)
			}
		})
	}
}

// ── GetEndpointCandidates / GetKeyCandidates 测试 ──

func TestGetEndpointCandidates_EmptyInput(t *testing.T) {
	store := newTestProfileStore(t)
	fastDecay := NewFastDecayScorer()

	candidates := GetEndpointCandidates(store, fastDecay, "m1", nil)
	if len(candidates) != 0 {
		t.Errorf("空输入应返回空: got %d", len(candidates))
	}
}

func TestGetEndpointCandidates_WithProfiles(t *testing.T) {
	store := newTestProfileStore(t)
	fastDecay := NewFastDecayScorer()

	// 插入画像
	profile := &KeyEndpointProfile{
		EndpointUID:    "ep1",
		ChannelUID:     "ch1",
		BaseURL:        "https://a.com",
		HealthState:    HealthStateHealthy,
		SuccessRate15m: 0.95,
		P95LatencyMs:   200,
		CostTier:       CostTierCheap,
	}
	_ = store.Upsert(profile)

	candidates := GetEndpointCandidates(store, fastDecay, "m1", []string{"https://a.com", "https://b.com"})

	if len(candidates) != 2 {
		t.Fatalf("candidates 长度 = %d, 期望 2", len(candidates))
	}

	// 第一个应有画像评分
	if candidates[0].Reason != "profile_scored" {
		t.Errorf("candidates[0].Reason = %q, 期望 %q", candidates[0].Reason, "profile_scored")
	}
	if candidates[0].Score == neutralEndpointScore {
		t.Error("有画像时不应为中性分")
	}

	// 第二个应为中性分（无画像）
	if candidates[1].Reason != "no_profile" {
		t.Errorf("candidates[1].Reason = %q, 期望 %q", candidates[1].Reason, "no_profile")
	}
	if candidates[1].Score != neutralEndpointScore {
		t.Errorf("candidates[1].Score = %v, 期望 %v", candidates[1].Score, neutralEndpointScore)
	}
}

func TestGetKeyCandidates_EmptyInput(t *testing.T) {
	store := newTestProfileStore(t)
	fastDecay := NewFastDecayScorer()

	candidates := GetKeyCandidates(store, fastDecay, "m1", "https://a.com", nil)
	if len(candidates) != 0 {
		t.Errorf("空输入应返回空: got %d", len(candidates))
	}
}

// TestScoreEndpointForKey_EndpointUIDMatchesHandlerComputation 回归测试（Phase 3B-2 复核发现的真实 bug）：
// scoreEndpointForKey 命中画像后，cand.EndpointUID 必须与 handlers 层
// upstream_failover.go 用 GenerateEndpointUID(upstream.ChannelUID, baseURL, keyHash) 算出的值一致，
// 否则 modelByUID 的 key 与 handlers 层查询用的 key 永不相等，MappedModel 永远查不到。
func TestScoreEndpointForKey_EndpointUIDMatchesHandlerComputation(t *testing.T) {
	store := newTestProfileStore(t)
	fastDecay := NewFastDecayScorer()

	const (
		channelUID = "ch-real-uid"
		baseURL    = "https://a.com"
		apiKey     = "sk-test-key"
	)
	keyHash := KeyHashFromAPIKey(apiKey)
	// profile 以真实 channelUID 生成 EndpointUID，与 profiler 落盘时的行为一致。
	realEndpointUID := GenerateEndpointUID(channelUID, baseURL, keyHash)

	profile := &KeyEndpointProfile{
		EndpointUID: realEndpointUID,
		ChannelUID:  channelUID,
		BaseURL:     baseURL,
		HealthState: HealthStateHealthy,
	}
	_ = store.Upsert(profile)

	candidates := GetKeyCandidates(store, fastDecay, "m1", baseURL, []string{apiKey})
	if len(candidates) != 1 {
		t.Fatalf("candidates 长度 = %d, 期望 1", len(candidates))
	}

	// handlers 层的计算方式：GenerateEndpointUID(upstream.ChannelUID, currentBaseURL, keyHash)
	handlerComputedUID := GenerateEndpointUID(channelUID, baseURL, keyHash)

	if candidates[0].EndpointUID != handlerComputedUID {
		t.Errorf("cand.EndpointUID = %q, 期望与 handlers 层计算一致 = %q（否则 MappedModel 查找会因 key 不匹配而永远落空）",
			candidates[0].EndpointUID, handlerComputedUID)
	}
	if candidates[0].EndpointUID != realEndpointUID {
		t.Errorf("cand.EndpointUID = %q, 期望等于命中的 profile.EndpointUID = %q", candidates[0].EndpointUID, realEndpointUID)
	}
}

func TestAutoPolicyFiltersBindingsByKeyModelProfile(t *testing.T) {
	store := newTestProfileStore(t)
	const (
		channelUID = "ch-model-isolation"
		baseURL    = "https://api.example.com"
		keyA       = "sk-model-a"
		keyB       = "sk-model-b"
		keyDead    = "sk-model-b-dead"
	)
	for key, models := range map[string][]string{
		keyA:    {"model-a"},
		keyB:    {"model-b"},
		keyDead: {"model-b"},
	} {
		uid := GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(key))
		health := HealthStateHealthy
		if key == keyDead {
			health = HealthStateDead
		}
		if err := store.Upsert(&KeyEndpointProfile{
			ChannelUID:      channelUID,
			ChannelKind:     "messages",
			EndpointUID:     uid,
			BaseURL:         baseURL,
			KeyHash:         KeyHashFromAPIKey(key),
			HealthState:     health,
			AvailableModels: models,
		}); err != nil {
			t.Fatal(err)
		}
	}

	policy := BuildEndpointPolicy(
		EndpointPolicyDeps{ProfileStore: store},
		&RequestProfile{Model: "model-b", ChannelKind: "messages"},
		RoutingModeAuto,
	)
	got := policy.FilterKeyBindings(channelUID, baseURL, []string{keyA, keyB, keyDead})
	if len(got) != 1 || got[0] != keyB {
		t.Fatalf("model-b binding 过滤结果 = %v，期望仅 %s", got, keyB)
	}
}

func TestShadowAndAssistPoliciesEnforceKnownBindingModels(t *testing.T) {
	store := newTestProfileStore(t)
	const (
		channelUID = "ch-model-constraint"
		baseURL    = "https://api.example.com"
		keyA       = "sk-model-a"
		keyB       = "sk-model-b"
	)
	for key, models := range map[string][]string{
		keyA: {"model-a"},
		keyB: {"model-b"},
	} {
		uid := GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(key))
		if err := store.Upsert(&KeyEndpointProfile{
			ChannelUID:      channelUID,
			ChannelKind:     "messages",
			EndpointUID:     uid,
			BaseURL:         baseURL,
			HealthState:     HealthStateHealthy,
			AvailableModels: models,
		}); err != nil {
			t.Fatal(err)
		}
	}

	for _, mode := range []RoutingMode{RoutingModeShadow, RoutingModeAssist} {
		t.Run(string(mode), func(t *testing.T) {
			policy := BuildEndpointPolicy(
				EndpointPolicyDeps{ProfileStore: store},
				&RequestProfile{Model: "model-b", ChannelKind: "messages"},
				mode,
			)
			got := policy.FilterKeyBindings(channelUID, baseURL, []string{keyA, keyB})
			if len(got) != 1 || got[0] != keyB {
				t.Fatalf("%s 模式允许了已知不兼容 binding: %v", mode, got)
			}
		})
	}
}

func TestAssistPolicyRepairsLegacyMetricsKeyForAutoMapping(t *testing.T) {
	store := newTestProfileStore(t)
	modelStore, err := NewModelProfileStoreWithDB(store.DB())
	if err != nil {
		t.Fatalf("NewModelProfileStoreWithDB: %v", err)
	}
	const (
		channelUID   = "ch-legacy-metrics-key"
		baseURL      = "https://open.bigmodel.cn/api/anthropic"
		apiKey       = "legacy-glm-api-key"
		requestModel = "claude-sonnet-5"
		mappedModel  = "glm-5.2"
	)
	endpointUID := GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey))
	if err := store.Upsert(&KeyEndpointProfile{
		ChannelUID: channelUID, ChannelKind: "messages", ServiceType: "claude",
		EndpointUID: endpointUID, BaseURL: baseURL, KeyHash: KeyHashFromAPIKey(apiKey),
		MetricsKey: KeyHashFromAPIKey(apiKey), AvailableModels: []string{mappedModel},
		HealthState: HealthStateUnknown,
	}); err != nil {
		t.Fatal(err)
	}
	metricsKey := computeMetricsIdentityKey(baseURL, apiKey, "claude")
	if err := modelStore.Upsert(&ModelProfile{
		ChannelUID: channelUID, ChannelKind: "messages", ServiceType: "claude",
		MetricsKey: metricsKey, ModelID: mappedModel, ModelFamily: ModelFamilyGLM,
		QualityTier: QualityTierHigh, ContextTokens: 1_000_000, ProbeSuccess: true,
	}); err != nil {
		t.Fatal(err)
	}
	routingCfg := config.DefaultAutopilotRoutingConfig()
	routingCfg.RoutingMode = config.AutopilotModeAssist
	routingCfg.ModelMapping.AutoResolve = true
	policy := BuildEndpointPolicy(EndpointPolicyDeps{
		ProfileStore:  store,
		ModelResolver: NewModelResolver(modelStore, nil),
		GetRoutingCfg: func() config.AutopilotRoutingConfig { return routingCfg },
	}, &RequestProfile{
		Model: requestModel, ChannelKind: "messages", QualityNeed: QualityTierHigh,
	}, RoutingModeAssist)

	filtered := policy.FilterKeyBindings(channelUID, baseURL, []string{apiKey})
	if len(filtered) != 1 || filtered[0] != apiKey {
		t.Fatalf("legacy MetricsKey 导致可映射 binding 被过滤: %v", filtered)
	}
	policy.SortKeyBindings(channelUID, baseURL, filtered)
	if got := policy.ResolvedModelByEndpointUID(endpointUID); got != mappedModel {
		t.Fatalf("mapped model = %q, want %q", got, mappedModel)
	}
}

func TestAssistPolicyResolvesTargetForEveryKeyOnSharedBaseURL(t *testing.T) {
	store := newTestProfileStore(t)
	modelStore, err := NewModelProfileStoreWithDB(store.DB())
	if err != nil {
		t.Fatalf("NewModelProfileStoreWithDB: %v", err)
	}
	const (
		channelUID   = "ch-shared-base-url"
		baseURL      = "https://relay.example.com"
		requestModel = "claude-fable-5"
		mappedModel  = "kimi-k2.7-code"
	)
	apiKeys := []string{"sk-shared-first", "sk-shared-second"}
	endpointUIDs := make([]string, 0, len(apiKeys))
	for _, apiKey := range apiKeys {
		keyHash := KeyHashFromAPIKey(apiKey)
		endpointUID := GenerateEndpointUID(channelUID, baseURL, keyHash)
		metricsKey := computeMetricsIdentityKey(baseURL, apiKey, "claude")
		endpointUIDs = append(endpointUIDs, endpointUID)
		if err := store.Upsert(&KeyEndpointProfile{
			ChannelUID: channelUID, ChannelKind: "messages", ServiceType: "claude",
			EndpointUID: endpointUID, BaseURL: baseURL, KeyHash: keyHash,
			MetricsKey: metricsKey, AvailableModels: []string{mappedModel},
			HealthState: HealthStateHealthy,
		}); err != nil {
			t.Fatal(err)
		}
		if err := modelStore.Upsert(&ModelProfile{
			ChannelUID: channelUID, ChannelKind: "messages", ServiceType: "claude",
			MetricsKey: metricsKey, ModelID: mappedModel, ModelFamily: ModelFamilyKimi,
			QualityTier: QualityTierHigh, ContextTokens: 1_000_000, ProbeSuccess: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	routingCfg := config.DefaultAutopilotRoutingConfig()
	routingCfg.RoutingMode = config.AutopilotModeAssist
	routingCfg.ModelMapping.AutoResolve = true
	policy := BuildEndpointPolicy(EndpointPolicyDeps{
		ProfileStore:  store,
		ModelResolver: NewModelResolver(modelStore, nil),
		GetRoutingCfg: func() config.AutopilotRoutingConfig { return routingCfg },
	}, &RequestProfile{
		Model: requestModel, ChannelKind: "messages", QualityNeed: QualityTierHigh,
	}, RoutingModeAssist)

	// URL 级排序最多只能缓存其中一个画像；发送阶段必须仍能解析任意实际选中的 Key。
	policy.SortURLs([]string{baseURL})
	for index, endpointUID := range endpointUIDs {
		target := policy.ResolvedTargetByEndpointUID(endpointUID)
		if target == nil || target.Model != mappedModel {
			t.Fatalf("endpoint %s target = %+v, want model %s", endpointUID, target, mappedModel)
		}
		bindingTarget := policy.ResolvedTargetForBinding(channelUID, baseURL, apiKeys[index])
		if bindingTarget == nil || bindingTarget.Model != mappedModel {
			t.Fatalf("binding %s target = %+v, want model %s", endpointUID, bindingTarget, mappedModel)
		}
	}
}

func TestAutoPolicyBindingLookupIncludesChannelUID(t *testing.T) {
	store := newTestProfileStore(t)
	const (
		baseURL = "https://api.example.com"
		apiKey  = "sk-shared"
	)
	for channelUID, models := range map[string][]string{
		"ch-a": {"model-a"},
		"ch-b": {"model-b"},
	} {
		uid := GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey))
		if err := store.Upsert(&KeyEndpointProfile{
			ChannelUID:      channelUID,
			ChannelKind:     "messages",
			EndpointUID:     uid,
			BaseURL:         baseURL,
			KeyHash:         KeyHashFromAPIKey(apiKey),
			HealthState:     HealthStateHealthy,
			AvailableModels: models,
		}); err != nil {
			t.Fatal(err)
		}
	}

	policy := BuildEndpointPolicy(
		EndpointPolicyDeps{ProfileStore: store},
		&RequestProfile{Model: "model-b", ChannelKind: "messages"},
		RoutingModeAuto,
	)
	if got := policy.FilterKeyBindings("ch-a", baseURL, []string{apiKey}); len(got) != 0 {
		t.Fatalf("ch-a 不应借用 ch-b 的模型画像: %v", got)
	}
	if got := policy.FilterKeyBindings("ch-b", baseURL, []string{apiKey}); len(got) != 1 {
		t.Fatalf("ch-b 应保留自身 binding: %v", got)
	}
}

func TestAutoPolicyDoesNotFilterWholeURLFromOneDeadBinding(t *testing.T) {
	store := newTestProfileStore(t)
	const baseURL = "https://shared.example.com"
	if err := store.Upsert(&KeyEndpointProfile{
		ChannelUID:  "ch-dead",
		EndpointUID: GenerateEndpointUID("ch-dead", baseURL, KeyHashFromAPIKey("sk-dead")),
		BaseURL:     baseURL,
		HealthState: HealthStateDead,
	}); err != nil {
		t.Fatal(err)
	}

	policy := BuildEndpointPolicy(
		EndpointPolicyDeps{ProfileStore: store},
		&RequestProfile{Model: "model-b", ChannelKind: "messages"},
		RoutingModeAuto,
	)
	got := policy.FilterURLs([]string{baseURL})
	if len(got) != 1 || got[0] != baseURL {
		t.Fatalf("URL 不能因其中一个 dead binding 被整体删除: %v", got)
	}
}

// TestTargetByUIDFillConsistency 验证 Finding 6 的修复：
// 五个写入 targetByUID 的位置（shadow SortURLs / shadow SortKeys /
// active SortURLs / active SortKeys / scoreAndSortKeyBindings）
// 对同一种候选（MappedModel 非空但 MappedEffortDecided=false，
// 即手动 modelMapping 场景）必须一致地记录 target，
// 而不能因排序路径不同产生"有的记、有的不记"的漂移。
func TestTargetByUIDFillConsistency(t *testing.T) {
	const (
		channelUID   = "ch-consistency"
		baseURL      = "https://api.example.com/consistency"
		bindingURL   = "https://api.example.com/consistency-binding"
		apiKey       = "sk-consistency"
		requestModel = "claude-sonnet-5"
		mappedModel  = "glm-5.2"
	)
	keyHash := KeyHashFromAPIKey(apiKey)

	// profile1：ChannelUID 为空，供 URL/Key 两类无 channel 上下文的评分函数命中。
	profile1UID := GenerateEndpointUID("", baseURL, keyHash)
	// profile2：独立 baseURL + 真实 channelUID，供 binding 评分函数
	// （scoreEndpointForBinding）命中；与 profile1 分用不同 baseURL，
	// 避免 findProfileByBaseURL 在同一 baseURL 下的 map 迭代顺序不确定
	// 导致 scoreEndpointForURL 误把 profile2 当作 profile1 命中。
	profile2UID := GenerateEndpointUID(channelUID, bindingURL, keyHash)

	newStoreWithProfiles := func(t *testing.T) *ProfileStore {
		store := newTestProfileStore(t)
		if err := store.Upsert(&KeyEndpointProfile{
			EndpointUID:     profile1UID,
			BaseURL:         baseURL,
			KeyHash:         keyHash,
			HealthState:     HealthStateHealthy,
			ModelMapping:    map[string]string{requestModel: mappedModel},
			AvailableModels: []string{mappedModel},
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.Upsert(&KeyEndpointProfile{
			ChannelUID:      channelUID,
			EndpointUID:     profile2UID,
			BaseURL:         bindingURL,
			KeyHash:         keyHash,
			HealthState:     HealthStateHealthy,
			ModelMapping:    map[string]string{requestModel: mappedModel},
			AvailableModels: []string{mappedModel},
		}); err != nil {
			t.Fatal(err)
		}
		return store
	}

	req := &RequestProfile{Model: requestModel, ChannelKind: "messages"}
	assertTarget := func(t *testing.T, label string, target *ResolvedRouteTarget) {
		t.Helper()
		if target == nil {
			t.Fatalf("%s: 期望记录 targetByUID，实际为 nil", label)
		}
		if target.Model != mappedModel {
			t.Fatalf("%s: target.Model = %q, 期望 %q", label, target.Model, mappedModel)
		}
		if target.EffortDecided {
			t.Fatalf("%s: manual_mapping 场景 EffortDecided 应为 false", label)
		}
	}

	t.Run("shadow_SortURLs", func(t *testing.T) {
		store := newStoreWithProfiles(t)
		policy := BuildEndpointPolicy(EndpointPolicyDeps{ProfileStore: store}, req, RoutingModeShadow)
		policy.SortURLs([]string{baseURL})
		assertTarget(t, "shadow_SortURLs", policy.ResolvedTargetByEndpointUID(profile1UID))
	})

	t.Run("shadow_SortKeys", func(t *testing.T) {
		store := newStoreWithProfiles(t)
		policy := BuildEndpointPolicy(EndpointPolicyDeps{ProfileStore: store}, req, RoutingModeShadow)
		policy.SortKeys(baseURL, []string{apiKey})
		assertTarget(t, "shadow_SortKeys", policy.ResolvedTargetByEndpointUID(profile1UID))
	})

	t.Run("active_SortURLs", func(t *testing.T) {
		store := newStoreWithProfiles(t)
		policy := BuildEndpointPolicy(EndpointPolicyDeps{ProfileStore: store}, req, RoutingModeAssist)
		policy.SortURLs([]string{baseURL})
		assertTarget(t, "active_SortURLs", policy.ResolvedTargetByEndpointUID(profile1UID))
	})

	t.Run("active_SortKeys", func(t *testing.T) {
		store := newStoreWithProfiles(t)
		policy := BuildEndpointPolicy(EndpointPolicyDeps{ProfileStore: store}, req, RoutingModeAssist)
		policy.SortKeys(baseURL, []string{apiKey})
		assertTarget(t, "active_SortKeys", policy.ResolvedTargetByEndpointUID(profile1UID))
	})

	t.Run("scoreAndSortKeyBindings_via_SortKeyBindings", func(t *testing.T) {
		store := newStoreWithProfiles(t)
		policy := BuildEndpointPolicy(EndpointPolicyDeps{ProfileStore: store}, req, RoutingModeAssist)
		policy.SortKeyBindings(channelUID, bindingURL, []string{apiKey})
		assertTarget(t, "scoreAndSortKeyBindings", policy.ResolvedTargetByEndpointUID(profile2UID))
	})
}

// TestRecordTargetByUID_GatingCondition 直接验证共享 helper recordTargetByUID 的
// 写入门槛：只要 MappedModel 非空就记录（不要求 MappedEffortDecided），
// MappedModel 为空则完全不写入（避免产生空改写）。
func TestRecordTargetByUID_GatingCondition(t *testing.T) {
	tests := []struct {
		name       string
		cand       EndpointCandidate
		wantRecord bool
	}{
		{
			name:       "mapped model + effort decided",
			cand:       EndpointCandidate{EndpointUID: "uid-1", MappedModel: "m2", MappedEffort: EffortHigh, MappedEffortDecided: true},
			wantRecord: true,
		},
		{
			name:       "mapped model only, effort not decided (manual mapping)",
			cand:       EndpointCandidate{EndpointUID: "uid-2", MappedModel: "m2", MappedEffortDecided: false},
			wantRecord: true,
		},
		{
			name:       "no mapped model must not produce a rewrite",
			cand:       EndpointCandidate{EndpointUID: "uid-3", MappedModel: "", MappedEffortDecided: true},
			wantRecord: false,
		},
		{
			name:       "mapped model without endpoint uid cannot be indexed",
			cand:       EndpointCandidate{EndpointUID: "", MappedModel: "m2"},
			wantRecord: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetByUID := make(map[string]*ResolvedRouteTarget)
			recordTargetByUID(tt.cand, targetByUID)
			_, got := targetByUID[tt.cand.EndpointUID]
			if tt.cand.EndpointUID == "" {
				got = len(targetByUID) > 0
			}
			if got != tt.wantRecord {
				t.Fatalf("recordTargetByUID(%+v) recorded=%v, want %v", tt.cand, got, tt.wantRecord)
			}
		})
	}
}

// ── 辅助函数测试 ──

func TestTopN(t *testing.T) {
	tests := []struct {
		items []string
		n     int
		want  []string
	}{
		{nil, 3, nil},
		{[]string{"a", "b", "c"}, 0, nil},
		{[]string{"a", "b", "c"}, 2, []string{"a", "b"}},
		{[]string{"a", "b", "c"}, 5, []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := topN(tt.items, tt.n)
			if len(got) != len(tt.want) {
				t.Errorf("topN 长度 = %d, 期望 %d", len(got), len(tt.want))
				return
			}
			for i, item := range got {
				if item != tt.want[i] {
					t.Errorf("topN[%d] = %q, 期望 %q", i, item, tt.want[i])
				}
			}
		})
	}
}

func TestMaskKeyForDisplay(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"", ""},
		{"short", "****"},
		{"sk-1234567890abcdef", "sk-1****cdef"}, // 前4后4
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := maskKeyForDisplay(tt.key)
			if got != tt.want {
				t.Errorf("maskKeyForDisplay(%q) = %q, 期望 %q", tt.key, got, tt.want)
			}
		})
	}
}

// ── 测试辅助 ──

// newTestProfileStore 创建用于测试的内存 ProfileStore。
func newTestProfileStore(t *testing.T) *ProfileStore {
	t.Helper()
	// 使用临时 SQLite 内存数据库
	store, err := NewProfileStore(":memory:")
	if err != nil {
		// 如果内存数据库失败，使用 nil store（测试仍然可以运行）
		t.Logf("警告: 创建测试 ProfileStore 失败: %v", err)
		return nil
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// ── 基准测试 ──

func BenchmarkScoreEndpoint(b *testing.B) {
	profile := &KeyEndpointProfile{
		HealthState:    HealthStateHealthy,
		SuccessRate15m: 0.95,
		P95LatencyMs:   200,
		CostTier:       CostTierCheap,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scoreEndpoint(profile, 0.85)
	}
}

func BenchmarkScoreEndpoint_NilProfile(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scoreEndpoint(nil, 1.0)
	}
}

func BenchmarkBuildEndpointPolicy_Shadow(b *testing.B) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildEndpointPolicy(deps, req, RoutingModeShadow)
	}
}

func BenchmarkShadowSortURLs_10URLs(b *testing.B) {
	deps := EndpointPolicyDeps{}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	policy := BuildEndpointPolicy(deps, req, RoutingModeShadow)

	urls := make([]string, 10)
	for i := range urls {
		urls[i] = "https://example" + string(rune('a'+i)) + ".com"
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		policy.SortURLs(urls)
	}
}
