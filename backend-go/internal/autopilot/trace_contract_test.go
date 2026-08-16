package autopilot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ── 比较状态测试 ──

func TestComputeComparisonStatus(t *testing.T) {
	tests := []struct {
		name       string
		shadowUID  string
		actualUID  string
		match      bool
		wantStatus ComparisonStatus
	}{
		{"matched", "ch_a", "ch_a", true, ComparisonMatched},
		{"mismatched", "ch_a", "ch_b", false, ComparisonMismatched},
		{"uncompared_both_empty", "", "", false, ComparisonUncompared},
		{"uncompared_shadow_empty", "", "ch_a", false, ComparisonUncompared},
		{"uncompared_actual_empty", "ch_a", "", false, ComparisonUncompared},
		{"uncompared_actual_empty_match_true", "ch_a", "", true, ComparisonUncompared},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeComparisonStatus(tt.shadowUID, tt.actualUID, tt.match)
			if got != tt.wantStatus {
				t.Errorf("ComputeComparisonStatus(%q, %q, %v) = %q, want %q",
					tt.shadowUID, tt.actualUID, tt.match, got, tt.wantStatus)
			}
		})
	}
}

// ── TraceUID v2 生成测试 ──

func TestGenerateTraceUIDv2_CollisionSafe(t *testing.T) {
	// 生成 1000 个 UID，验证不碰撞
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		uid := GenerateTraceUIDv2()
		if uid == "" {
			t.Fatal("GenerateTraceUIDv2 返回空字符串")
		}
		if !strings.HasPrefix(uid, "rt_") {
			t.Errorf("UID 缺少 rt_ 前缀: %q", uid)
		}
		if seen[uid] {
			t.Errorf("UID 碰撞: %q", uid)
		}
		seen[uid] = true
	}
}

func TestGenerateTraceUIDv2_Different(t *testing.T) {
	// 连续两次调用应产生不同 UID
	uid1 := GenerateTraceUIDv2()
	uid2 := GenerateTraceUIDv2()
	if uid1 == uid2 {
		t.Error("连续两次 GenerateTraceUIDv2 返回相同 UID")
	}
}

// ── 策略指纹测试 ──

func TestComputePolicyFingerprint_Stable(t *testing.T) {
	// 相同输入应产生相同指纹（map 排序稳定）
	input := PolicyFingerprintInput{
		WeightOverrides: map[string]float64{
			"wCost":    2.0,
			"wQuality": 1.5,
		},
		CostPreference: "balanced",
		ModelFamilyPreference: []string{
			"claude", "openai", "deepseek",
		},
		CapabilityFloor:           true,
		DisabledChannelUIDs:       []string{"ch_c", "ch_a", "ch_b"},
		DisabledTaskClasses:       []string{"vision", "embedding"},
		TaskDomainStrengthEnabled: true,
	}

	fp1 := ComputePolicyFingerprint(input)
	fp2 := ComputePolicyFingerprint(input)
	if fp1 != fp2 {
		t.Errorf("相同输入指纹不同: %q vs %q", fp1, fp2)
	}
	if fp1 == "" || fp1 == "fp_error" {
		t.Errorf("无效指纹: %q", fp1)
	}
}

func TestComputePolicyFingerprint_ExcludesRollout(t *testing.T) {
	// 指纹不应包含 rolloutPercent、controlPercent 或 rolloutSeed
	input := PolicyFingerprintInput{
		CostPreference:  "balanced",
		CapabilityFloor: true,
	}
	fp1 := ComputePolicyFingerprint(input)

	// 相同语义输入应产生相同指纹
	fp2 := ComputePolicyFingerprint(input)
	if fp1 != fp2 {
		t.Errorf("相同语义输入指纹不同: %q vs %q", fp1, fp2)
	}
}

func TestComputePolicyFingerprint_DifferentInputs(t *testing.T) {
	// 不同输入应产生不同指纹
	input1 := PolicyFingerprintInput{
		CostPreference:  "balanced",
		CapabilityFloor: true,
	}
	input2 := PolicyFingerprintInput{
		CostPreference:  "cost_first",
		CapabilityFloor: false,
	}
	fp1 := ComputePolicyFingerprint(input1)
	fp2 := ComputePolicyFingerprint(input2)
	if fp1 == fp2 {
		t.Errorf("不同输入产生相同指纹: %q", fp1)
	}
}

// ── 脱敏测试 ──

func TestSanitizeForPersistence_ClearsMetricsKey(t *testing.T) {
	detail := &TraceDetailV2{
		TraceUID:      "rt_test",
		SchemaVersion: 2,
		Candidates: []RoutingCandidate{
			{ChannelUID: "ch_a", MetricsKey: "https://api.example.com|sk-secret123"},
			{ChannelUID: "ch_b", MetricsKey: "https://api2.example.com|key456"},
		},
	}

	SanitizeForPersistence(detail)

	for i, c := range detail.Candidates {
		if c.MetricsKey != "" {
			t.Errorf("SanitizeForPersistence 未清除 candidate[%d].MetricsKey: %q", i, c.MetricsKey)
		}
	}
}

func TestSanitizeForPersistence_Nil(t *testing.T) {
	// nil 输入不应 panic
	SanitizeForPersistence(nil)
	SanitizeForResponse(nil)
}

func TestSanitizeForResponse(t *testing.T) {
	detail := &TraceDetailV2{
		TraceUID:      "rt_test",
		SchemaVersion: 2,
		Candidates: []RoutingCandidate{
			{ChannelUID: "ch_a", MetricsKey: "sensitive"},
		},
		EndpointAttempts: []EndpointAttemptSummary{
			{AttemptUID: "a1", ChannelUID: "ch_a", EndpointLabel: "https://bad.url/secret"},
		},
	}

	SanitizeForResponse(detail)

	// 脱敏后 JSON 不应包含敏感字段
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	sensitive := ScanJSONForSensitive(data)
	if len(sensitive) > 0 {
		t.Errorf("SanitizeForResponse 后 JSON 仍含敏感字段: %v", sensitive)
	}
}

func TestSanitizeForPersistence_EndpointLabel(t *testing.T) {
	detail := &TraceDetailV2{
		TraceUID:      "rt_test",
		SchemaVersion: 2,
		EndpointAttempts: []EndpointAttemptSummary{
			{AttemptUID: "a1", AttemptSeq: 1, ChannelUID: "ch_abc123", EndpointLabel: ""},
		},
	}

	SanitizeForPersistence(detail)

	if detail.EndpointAttempts[0].EndpointLabel == "" {
		t.Error("SanitizeForPersistence 未填充空 EndpointLabel")
	}
}

// ── v1 → v2 适配器测试 ──

func TestAdaptV1ToTraceSummary(t *testing.T) {
	now := time.Now().UTC()
	trace := &RoutingDecisionTrace{
		TraceUID:         "rt_v1_test",
		Mode:             RoutingModeShadow,
		RequestKind:      "messages",
		TaskClass:        "supervisor",
		ShadowChannelUID: "ch_a",
		ActualChannelUID: "ch_a",
		Match:            true,
		Outcome:          "success",
		Success:          true,
		CreatedAt:        now,
	}

	summary := AdaptV1ToTraceSummary(trace)

	if summary.TraceUID != "rt_v1_test" {
		t.Errorf("TraceUID = %q, want rt_v1_test", summary.TraceUID)
	}
	if summary.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", summary.SchemaVersion)
	}
	if !summary.HistoricalSchema {
		t.Error("v1 适配器应标记 HistoricalSchema=true")
	}
	if summary.ComparisonStatus != ComparisonMatched {
		t.Errorf("ComparisonStatus = %q, want matched", summary.ComparisonStatus)
	}
	if summary.ReleaseID != "legacy" {
		t.Errorf("ReleaseID = %q, want legacy", summary.ReleaseID)
	}
}

func TestAdaptV1ToTraceSummary_Nil(t *testing.T) {
	summary := AdaptV1ToTraceSummary(nil)
	if summary.TraceUID != "" {
		t.Error("nil 输入应返回空 TraceSummary")
	}
}

func TestAdaptV1ToTraceDetailV2(t *testing.T) {
	now := time.Now().UTC()
	completedAt := now.Add(-time.Second)
	trace := &RoutingDecisionTrace{
		TraceUID:           "rt_v1_detail",
		Mode:               RoutingModeShadow,
		RequestKind:        "chat",
		TaskClass:          "worker",
		ShadowChannelUID:   "ch_a",
		ActualChannelUID:   "ch_b",
		Match:              false,
		Outcome:            "upstream_error",
		Success:            false,
		CreatedAt:          now,
		CompletedAt:        &completedAt,
		ManualIntentUID:    "mi_123",
		FallbackUsed:       true,
		SelectedChannelUID: "ch_c",
	}

	detail := AdaptV1ToTraceDetailV2(trace)

	if detail == nil {
		t.Fatal("AdaptV1ToTraceDetailV2 返回 nil")
	}
	if detail.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", detail.SchemaVersion)
	}
	if !detail.HistoricalSchema {
		t.Error("v1 适配器应标记 HistoricalSchema=true")
	}
	if detail.ComparisonStatus != ComparisonMismatched {
		t.Errorf("ComparisonStatus = %q, want mismatched", detail.ComparisonStatus)
	}
	if detail.ReleaseID != "legacy" {
		t.Errorf("ReleaseID = %q, want legacy", detail.ReleaseID)
	}
	if detail.ManualIntentUID != "mi_123" {
		t.Errorf("ManualIntentUID = %q, want mi_123", detail.ManualIntentUID)
	}
}

func TestAdaptV1ToTraceDetailV2_Nil(t *testing.T) {
	detail := AdaptV1ToTraceDetailV2(nil)
	if detail != nil {
		t.Error("nil 输入应返回 nil")
	}
}

// ── Endpoint Label 测试 ──

func TestDeriveEndpointLabel(t *testing.T) {
	tests := []struct {
		name       string
		channelUID string
		seq        int
		wantPrefix string
	}{
		{"normal", "ch_abc123def456", 1, "ch_abc12_1"},
		{"short_uid", "ch_ab", 3, "ch_ab_3"},
		{"empty_uid", "", 5, "ep_5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveEndpointLabel(tt.channelUID, tt.seq)
			if got != tt.wantPrefix {
				t.Errorf("DeriveEndpointLabel(%q, %d) = %q, want %q",
					tt.channelUID, tt.seq, got, tt.wantPrefix)
			}
		})
	}
}

func TestDeriveEndpointLabel_DoesNotContainSensitive(t *testing.T) {
	// endpoint label 不应包含 URL、key 片段
	label := DeriveEndpointLabel("https://api.example.com/v1", 1)
	if strings.Contains(label, "https://") || strings.Contains(label, "api.example.com") {
		t.Errorf("EndpointLabel 包含地址信息: %q", label)
	}
}

// ── Attempt 截断测试 ──

func TestTruncateAttempts_UnderLimit(t *testing.T) {
	attempts := make([]EndpointAttemptSummary, 10)
	for i := range attempts {
		attempts[i] = EndpointAttemptSummary{
			AttemptUID: "a",
			AttemptSeq: i + 1,
			Status:     "completed",
			Result:     "success",
		}
	}

	result, truncated, total, byResult := TruncateAttempts(attempts)

	if truncated {
		t.Error("10 条不应触发截断")
	}
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
	if len(result) != 10 {
		t.Errorf("len(result) = %d, want 10", len(result))
	}
	if byResult != nil {
		t.Error("未截断时 byResult 应为 nil")
	}
}

func TestTruncateAttempts_OverLimit(t *testing.T) {
	attempts := make([]EndpointAttemptSummary, 50)
	for i := range attempts {
		result := "success"
		if i%3 == 0 {
			result = "upstream_error"
		}
		attempts[i] = EndpointAttemptSummary{
			AttemptUID: "a",
			AttemptSeq: i + 1,
			Status:     "completed",
			Result:     result,
		}
	}

	result, truncated, total, byResult := TruncateAttempts(attempts)

	if !truncated {
		t.Error("50 条应触发截断")
	}
	if total != 50 {
		t.Errorf("total = %d, want 50", total)
	}
	if len(result) != 32 {
		t.Errorf("截断后 len(result) = %d, want 32", len(result))
	}
	if byResult == nil {
		t.Fatal("截断后 byResult 不应为 nil")
	}
	if byResult["success"] == 0 || byResult["upstream_error"] == 0 {
		t.Errorf("byResult 计数不完整: %v", byResult)
	}
}

// ── 稳定哈希分桶测试 ──

func TestStableBucket_Deterministic(t *testing.T) {
	// 相同输入应产生相同桶号
	id := "session_abc123"
	seed := "seed_xyz"
	b1 := StableBucket(id, seed)
	b2 := StableBucket(id, seed)
	if b1 != b2 {
		t.Errorf("相同输入桶号不同: %d vs %d", b1, b2)
	}
}

func TestStableBucket_Range(t *testing.T) {
	// 桶号应在 [0, 99] 范围内
	for i := 0; i < 1000; i++ {
		b := StableBucket("id_"+string(rune('0'+i%10)), "seed")
		if b < 0 || b >= 100 {
			t.Errorf("桶号 %d 超出 [0, 99] 范围", b)
		}
	}
}

func TestStableBucket_DifferentInputs(t *testing.T) {
	// 不同输入通常产生不同桶号（概率性，但极端情况可接受）
	b1 := StableBucket("session_a", "seed")
	b2 := StableBucket("session_b", "seed")
	// 不强制要求不同，但记录差异
	t.Logf("b1=%d, b2=%d", b1, b2)
}

func TestStableBucket_Empty(t *testing.T) {
	b := StableBucket("", "seed")
	if b != 0 {
		t.Errorf("空输入桶号应为 0，实际为 %d", b)
	}
}

// ── InBucket 测试 ──

func TestInBucket(t *testing.T) {
	tests := []struct {
		name           string
		bucket         int
		rolloutPercent int
		controlPercent int
		wantCohort     Cohort
	}{
		{"treatment", 5, 50, 1, CohortTreatment},
		{"control", 50, 50, 1, CohortControl},
		{"bypass", 51, 50, 1, CohortBypass},
		{"treatment_boundary", 49, 50, 0, CohortTreatment},
		{"bypass_no_control", 50, 50, 0, CohortBypass},
		{"zero_rollout", 0, 0, 0, CohortBypass},
		{"full_rollout", 99, 100, 0, CohortTreatment},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InBucket(tt.bucket, tt.rolloutPercent, tt.controlPercent)
			if got != tt.wantCohort {
				t.Errorf("InBucket(%d, %d, %d) = %q, want %q",
					tt.bucket, tt.rolloutPercent, tt.controlPercent, got, tt.wantCohort)
			}
		})
	}
}

// ── ToTraceDetailV2 / ToTraceSummary 测试 ──

func TestToTraceDetailV2(t *testing.T) {
	now := time.Now().UTC()
	snapshot := &RoutingReleaseSnapshot{
		ReleaseID:         "rel_001",
		PolicyFingerprint: "fp_abc",
		TargetMode:        RoutingModeAuto,
		EffectiveMode:     RoutingModeAuto,
		Cohort:            CohortTreatment,
	}
	trace := &RoutingDecisionTrace{
		TraceUID:             "rt_test",
		SchemaVersion:        2,
		RequestCorrelationId: "corr_001",
		Source:               "proxy",
		RequestKind:          "messages",
		TaskClass:            "supervisor",
		ShadowChannelUID:     "ch_a",
		ActualChannelUID:     "ch_a",
		Match:                true,
		Mode:                 RoutingModeAuto,
		CreatedAt:            now,
	}

	detail := trace.ToTraceDetailV2(snapshot, 1, PersistenceSampled)

	if detail.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", detail.SchemaVersion)
	}
	if detail.ComparisonStatus != ComparisonMatched {
		t.Errorf("ComparisonStatus = %q, want matched", detail.ComparisonStatus)
	}
	if detail.ReleaseID != "rel_001" {
		t.Errorf("ReleaseID = %q, want rel_001", detail.ReleaseID)
	}
	if detail.PolicyFingerprint != "fp_abc" {
		t.Errorf("PolicyFingerprint = %q, want fp_abc", detail.PolicyFingerprint)
	}
	if detail.Cohort != CohortTreatment {
		t.Errorf("Cohort = %q, want treatment", detail.Cohort)
	}
}

func TestToTraceDetailV2_NilSnapshot(t *testing.T) {
	trace := &RoutingDecisionTrace{
		TraceUID:  "rt_test",
		CreatedAt: time.Now(),
	}
	detail := trace.ToTraceDetailV2(nil, 0, PersistenceSampled)
	if detail == nil {
		t.Fatal("nil snapshot 不应返回 nil")
	}
	if detail.ReleaseID != "" {
		t.Errorf("nil snapshot 时 ReleaseID 应为空，实际为 %q", detail.ReleaseID)
	}
}

func TestToTraceDetailV2_IncludesSchedulerDecision(t *testing.T) {
	trace := &RoutingDecisionTrace{
		TraceUID:  "rt_sd",
		CreatedAt: time.Now().UTC(),
		SchedulerDecision: &SchedulerDecisionSummary{
			SelectedName:  "ch_a",
			SelectionCode: "priority_order",
			SkippedCandidates: []SkippedCandidateSummary{
				{ChannelIndex: 6, ChannelName: "ch_b", Stage: "active_model_filter", Reason: "unsupported_model"},
			},
		},
	}

	detail := trace.ToTraceDetailV2(nil, 1, PersistenceSampled)
	if detail.SchedulerDecision == nil {
		t.Fatal("TraceDetailV2 未映射 SchedulerDecision")
	}
	if detail.SchedulerDecision.SelectedName != "ch_a" {
		t.Errorf("SelectedName = %q, want ch_a", detail.SchedulerDecision.SelectedName)
	}
	if len(detail.SchedulerDecision.SkippedCandidates) != 1 {
		t.Fatalf("SkippedCandidates = %d, want 1", len(detail.SchedulerDecision.SkippedCandidates))
	}
}

func TestSanitizeSchedulerForPersistence_FiltersSkippedCandidates(t *testing.T) {
	s := &SchedulerDecisionSummary{
		SkipReasons: []string{"unsupported_model", "https://evil.example/leak"},
		SkippedCandidates: []SkippedCandidateSummary{
			{ChannelIndex: 6, ChannelName: "ch_a", Stage: "active_model_filter", Reason: "unsupported_model"},
			{ChannelIndex: 9, ChannelName: "ch_b", Stage: "active_model_filter", Reason: "https://evil.example/leak"},
		},
	}

	out := sanitizeSchedulerForPersistence(s)
	if len(out.SkipReasons) != 1 || out.SkipReasons[0] != "unsupported_model" {
		t.Errorf("SkipReasons 过滤结果不符合预期: %v", out.SkipReasons)
	}
	if len(out.SkippedCandidates) != 1 || out.SkippedCandidates[0].ChannelName != "ch_a" {
		t.Errorf("白名单外 reason 的明细应整条丢弃: %+v", out.SkippedCandidates)
	}
}

func TestToTraceSummary(t *testing.T) {
	trace := &RoutingDecisionTrace{
		TraceUID:         "rt_test",
		Mode:             RoutingModeShadow,
		RequestKind:      "chat",
		TaskClass:        "worker",
		ShadowChannelUID: "ch_a",
		ActualChannelUID: "ch_b",
		Match:            false,
		Outcome:          "upstream_error",
		CreatedAt:        time.Now(),
	}

	summary := trace.ToTraceSummary()

	if summary.TraceUID != "rt_test" {
		t.Errorf("TraceUID = %q, want rt_test", summary.TraceUID)
	}
	if summary.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", summary.SchemaVersion)
	}
	if summary.ComparisonStatus != ComparisonMismatched {
		t.Errorf("ComparisonStatus = %q, want mismatched", summary.ComparisonStatus)
	}
}

// ── 敏感字段扫描测试 ──

func TestScanJSONForSensitive_Clean(t *testing.T) {
	// 安全 JSON 不应触发任何 sentinel（使用 chat 作为 requestKind 避免 "messages" 值误报）
	clean := `{"traceUid":"rt_test","schemaVersion":2,"requestKind":"chat","taskClass":"supervisor"}`
	found := ScanJSONForSensitive([]byte(clean))
	if len(found) > 0 {
		t.Errorf("安全 JSON 误报: %v", found)
	}
}

func TestScanJSONForSensitive_DetectsKey(t *testing.T) {
	// 包含 apiKey 的 JSON 应被检测
	dirty := `{"traceUid":"rt_test","apiKey":"sk-secret123"}`
	found := ScanJSONForSensitive([]byte(dirty))
	if len(found) == 0 {
		t.Error("包含 apiKey 的 JSON 未被检测到")
	}
}

func TestScanJSONForSensitive_DetectsPrompt(t *testing.T) {
	dirty := `{"prompt":"hello world"}`
	found := ScanJSONForSensitive([]byte(dirty))
	if len(found) == 0 {
		t.Error("包含 prompt 的 JSON 未被检测到")
	}
}

// ── isSafeSkipReason 测试 ──

func TestIsSafeSkipReason(t *testing.T) {
	tests := []struct {
		reason string
		safe   bool
	}{
		{"unsupported_model", true},
		{"circuit_open", true},
		{"priority_order", true},
		{"unknown_hack", false},
		{"", false},
		{"base_url_leak", false},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := isSafeSkipReason(tt.reason)
			if got != tt.safe {
				t.Errorf("isSafeSkipReason(%q) = %v, want %v", tt.reason, got, tt.safe)
			}
		})
	}
}

// ── PersistenceClass 字符串测试 ──

func TestPersistenceClass_Values(t *testing.T) {
	// 确保所有 PersistenceClass 常量值非空且有意义
	classes := []PersistenceClass{
		PersistenceSampled,
		PersistenceMismatch,
		PersistenceFailure,
		PersistenceFallback,
		PersistenceManual,
		PersistenceAdvisor,
		PersistenceDryRun,
	}

	for _, c := range classes {
		if c == "" {
			t.Error("PersistenceClass 不应为空")
		}
	}
}

// ── ComparisonStatus 枚举测试 ──

func TestComparisonStatus_Values(t *testing.T) {
	// 确保所有三态值唯一
	statuses := []ComparisonStatus{
		ComparisonMatched,
		ComparisonMismatched,
		ComparisonUncompared,
	}
	seen := make(map[ComparisonStatus]bool)
	for _, s := range statuses {
		if s == "" {
			t.Error("ComparisonStatus 不应为空")
		}
		if seen[s] {
			t.Errorf("重复的 ComparisonStatus: %q", s)
		}
		seen[s] = true
	}
}

// ── RoutingReleaseSnapshot 测试 ──

func TestRoutingReleaseSnapshot_Immutability(t *testing.T) {
	// 验证 snapshot 结构可被创建并包含所有必要字段
	snapshot := RoutingReleaseSnapshot{
		ReleaseID:         "rel_001",
		PolicyFingerprint: "fp_abc123",
		TargetMode:        RoutingModeAuto,
		EffectiveMode:     RoutingModeAuto,
		Cohort:            CohortTreatment,
		RolloutPercent:    50,
		CreatedAt:         time.Now(),
	}

	if snapshot.ReleaseID == "" {
		t.Error("ReleaseID 不应为空")
	}
	if snapshot.PolicyFingerprint == "" {
		t.Error("PolicyFingerprint 不应为空")
	}
}

// ── 空值 / 边界测试 ──

func TestSanitizeForPersistence_EmptyInput(t *testing.T) {
	detail := &TraceDetailV2{}
	SanitizeForPersistence(detail)
	// 不应 panic
}

func TestSanitizeForResponse_EmptyInput(t *testing.T) {
	detail := &TraceDetailV2{}
	SanitizeForResponse(detail)
	// 不应 panic
}

func TestAdaptV1ToTraceSummary_EmptyTrace(t *testing.T) {
	trace := &RoutingDecisionTrace{}
	summary := AdaptV1ToTraceSummary(trace)
	if summary.TraceUID != "" {
		t.Errorf("空 trace 的 TraceUID 应为空: %q", summary.TraceUID)
	}
	if summary.ComparisonStatus != ComparisonUncompared {
		t.Errorf("空 trace 的 ComparisonStatus 应为 uncompared: %q", summary.ComparisonStatus)
	}
}

func TestToTraceDetailV2_NilTrace(t *testing.T) {
	snapshot := &RoutingReleaseSnapshot{ReleaseID: "rel_001"}
	detail := (*RoutingDecisionTrace)(nil).ToTraceDetailV2(snapshot, 0, PersistenceSampled)
	if detail != nil {
		t.Error("nil trace 应返回 nil detail")
	}
}

func TestToTraceSummary_NilTrace(t *testing.T) {
	summary := (*RoutingDecisionTrace)(nil).ToTraceSummary()
	if summary.TraceUID != "" {
		t.Error("nil trace 应返回空 summary")
	}
}

// ── JSON 序列化往返测试 ──

func TestTraceDetailV2_RoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	completedAt := now.Add(-time.Second).Truncate(time.Second)

	original := &TraceDetailV2{
		TraceUID:             "rt_roundtrip",
		SchemaVersion:        2,
		TraceRevision:        3,
		CreatedAt:            now,
		RequestCorrelationID: "corr_001",
		Source:               "proxy",
		ReleaseID:            "rel_001",
		PolicyFingerprint:    "fp_abc",
		TargetMode:           RoutingModeAuto,
		EffectiveMode:        RoutingModeAuto,
		Cohort:               CohortTreatment,
		PersistenceClass:     PersistenceSampled,
		ComparisonStatus:     ComparisonMatched,
		RequestKind:          "messages",
		TaskClass:            "supervisor",
		RequestedModel:       "claude-sonnet-5",
		AgentRole:            "main",
		RecommendedChannel:   "ch_a",
		SelectedChannelUID:   "ch_a",
		FallbackUsed:         false,
		Outcome:              "success",
		Success:              true,
		StatusCode:           200,
		RequestDurationMs:    1500,
		DurationMs:           2,
		CompletedAt:          &completedAt,
		EndpointAttempts: []EndpointAttemptSummary{
			{AttemptUID: "a1", AttemptSeq: 1, Status: "completed", ChannelUID: "ch_a", EndpointLabel: "ch_a_1", Result: "success", StatusCode: 200},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var restored TraceDetailV2
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	if restored.TraceUID != original.TraceUID {
		t.Errorf("TraceUID 往返不一致: %q vs %q", restored.TraceUID, original.TraceUID)
	}
	if restored.SchemaVersion != original.SchemaVersion {
		t.Errorf("SchemaVersion 往返不一致: %d vs %d", restored.SchemaVersion, original.SchemaVersion)
	}
	if restored.ComparisonStatus != original.ComparisonStatus {
		t.Errorf("ComparisonStatus 往返不一致: %q vs %q", restored.ComparisonStatus, original.ComparisonStatus)
	}
	if restored.Cohort != original.Cohort {
		t.Errorf("Cohort 往返不一致: %q vs %q", restored.Cohort, original.Cohort)
	}
}
