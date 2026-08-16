package autopilot

import (
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/scheduler"
)

// ── NormalizeSelectionTrace 测试 ──

func TestNormalizeSelectionTrace_Nil(t *testing.T) {
	if NormalizeSelectionTrace(nil) != nil {
		t.Error("nil 输入应返回 nil")
	}
}

func TestNormalizeSelectionTrace_Stages(t *testing.T) {
	trace := &scheduler.SelectionTrace{
		Stages: []scheduler.SelectionTraceStage{
			{Name: "active_model_filter", Count: 5},
			{Name: "context_filter", Count: 3},
		},
		Candidates: []scheduler.SelectionTraceCandidate{
			{ChannelName: "ch_a", Reason: "circuit_open"},
			{ChannelName: "ch_b", Reason: "unsupported_model"},
			{ChannelName: "ch_c", Reason: "some_unsafe_reason_with_url"},
		},
		Selected: &scheduler.SelectionTraceSelection{
			ChannelName: "ch_d",
			Reason:      "priority_order",
		},
	}

	summary := NormalizeSelectionTrace(trace)
	if summary == nil {
		t.Fatal("非 nil 输入应返回摘要")
	}
	if len(summary.Stages) != 2 {
		t.Errorf("阶段数 = %d, want 2", len(summary.Stages))
	}
	// 只有安全的跳过原因被保留（circuit_open, unsupported_model）
	if len(summary.SkipReasons) != 2 {
		t.Errorf("安全跳过原因数 = %d, want 2 (%v)", len(summary.SkipReasons), summary.SkipReasons)
	}
	if summary.SelectedName != "ch_d" {
		t.Errorf("SelectedName = %q, want ch_d", summary.SelectedName)
	}
	if summary.SelectionCode != "priority_order" {
		t.Errorf("SelectionCode = %q, want priority_order", summary.SelectionCode)
	}
}

func TestNormalizeSelectionTrace_FiltersUnsafeReasons(t *testing.T) {
	trace := &scheduler.SelectionTrace{
		Candidates: []scheduler.SelectionTraceCandidate{
			{ChannelName: "ch_a", Reason: "https://leaked.url/secret"},
			{ChannelName: "ch_b", Reason: "circuit_open"},
		},
	}
	summary := NormalizeSelectionTrace(trace)
	for _, reason := range summary.SkipReasons {
		if reason == "https://leaked.url/secret" {
			t.Error("不安全的跳过原因不应被保留")
		}
	}
}

func TestNormalizeSelectionTrace_SkippedCandidates(t *testing.T) {
	trace := &scheduler.SelectionTrace{
		Candidates: []scheduler.SelectionTraceCandidate{
			{ChannelIndex: 6, ChannelName: "ai-muapi-cn", Stage: "active_model_filter", Reason: "unsupported_model", Details: "model=claude-opus-4-8"},
			{ChannelIndex: 1, ChannelName: "vip-lyclaude-site", Stage: "active_model_filter", Reason: "disabled_status"},
			{ChannelIndex: 9, ChannelName: "evil", Stage: "active_model_filter", Reason: "https://leaked.url/x", Details: "should be dropped"},
		},
		Selected: &scheduler.SelectionTraceSelection{
			Route:       scheduler.ChannelRouteRef{Index: 3, ChannelUID: "uid-kimi"},
			ChannelName: "kimi-official",
			Reason:      "priority_order",
		},
	}

	summary := NormalizeSelectionTrace(trace)
	if summary == nil {
		t.Fatal("非 nil 输入应返回摘要")
	}
	// 白名单外 reason 的明细整条丢弃
	if len(summary.SkippedCandidates) != 2 {
		t.Fatalf("SkippedCandidates = %d, want 2: %+v", len(summary.SkippedCandidates), summary.SkippedCandidates)
	}
	first := summary.SkippedCandidates[0]
	if first.ChannelIndex != 6 || first.ChannelName != "ai-muapi-cn" || first.Stage != "active_model_filter" ||
		first.Reason != "unsupported_model" || first.Details != "model=claude-opus-4-8" {
		t.Errorf("首条明细不符合预期: %+v", first)
	}
	if summary.SelectedUID != "uid-kimi" {
		t.Errorf("SelectedUID = %q, want uid-kimi", summary.SelectedUID)
	}
	if summary.SelectedName != "kimi-official" {
		t.Errorf("SelectedName = %q, want kimi-official", summary.SelectedName)
	}
}

// ── AttachSchedulerDecision 测试 ──

func TestAttachSchedulerDecision(t *testing.T) {
	store := &TraceStore{
		records:  make([]*RoutingDecisionTrace, 0),
		inflight: make(map[string]*RoutingDecisionTrace),
	}
	trace := &RoutingDecisionTrace{
		TraceUID:  "rt_attach",
		CreatedAt: time.Now(),
	}
	store.records = append(store.records, trace)

	decision := &SchedulerDecisionSummary{
		SelectedName:  "ch_a",
		SelectionCode: "priority_order",
	}
	store.AttachSchedulerDecision("rt_attach", decision)

	if store.records[0].SchedulerDecision == nil {
		t.Fatal("SchedulerDecision 未附加")
	}
	if store.records[0].SchedulerDecision.SelectedName != "ch_a" {
		t.Errorf("SelectedName = %q, want ch_a", store.records[0].SchedulerDecision.SelectedName)
	}
}

func TestAttachSchedulerDecision_NilSafety(t *testing.T) {
	store := &TraceStore{
		records:  make([]*RoutingDecisionTrace, 0),
		inflight: make(map[string]*RoutingDecisionTrace),
	}
	// 不应 panic
	store.AttachSchedulerDecision("", nil)
	store.AttachSchedulerDecision("rt_missing", &SchedulerDecisionSummary{})
}

// TestAttachSchedulerDecision_PersistsDetailsJSON 验证已落盘记录的 details_json 被同步更新。
func TestAttachSchedulerDecision_PersistsDetailsJSON(t *testing.T) {
	store, _ := newTempTraceStore(t)
	defer func() { _ = store.Close() }()

	traceUID := GenerateTraceUIDv2()
	trace := &RoutingDecisionTrace{
		TraceUID:        traceUID,
		SchemaVersion:   2,
		Source:          "proxy",
		RequestKind:     "messages",
		TaskClass:       TaskClassSupervisor,
		Mode:            RoutingModeShadow,
		ManualIntentUID: "mi_attach_persist", // 必落盘类别
		CreatedAt:       time.Now().UTC(),
	}
	store.Record(trace)

	store.AttachSchedulerDecision(traceUID, &SchedulerDecisionSummary{
		SelectedName:  "ch_a",
		SelectionCode: "priority_order",
		SkippedCandidates: []SkippedCandidateSummary{
			{ChannelIndex: 6, ChannelName: "ch_b", Stage: "active_model_filter", Reason: "unsupported_model", Details: "model=x"},
		},
	})

	// GetTraceDetail 以 SQLite details_json 为权威源，能读到即证明 UPDATE 生效
	detail, err := store.GetTraceDetail(traceUID)
	if err != nil {
		t.Fatalf("GetTraceDetail 失败: %v", err)
	}
	if detail == nil || detail.SchedulerDecision == nil {
		t.Fatal("落盘 details_json 应包含 SchedulerDecision")
	}
	if detail.SchedulerDecision.SelectedName != "ch_a" {
		t.Errorf("SelectedName = %q, want ch_a", detail.SchedulerDecision.SelectedName)
	}
	if len(detail.SchedulerDecision.SkippedCandidates) != 1 {
		t.Fatalf("SkippedCandidates = %d, want 1", len(detail.SchedulerDecision.SkippedCandidates))
	}
	if detail.SchedulerDecision.SkippedCandidates[0].Reason != "unsupported_model" {
		t.Errorf("Reason = %q, want unsupported_model", detail.SchedulerDecision.SkippedCandidates[0].Reason)
	}
}

// ── AppendEndpointAttempt 测试 ──

func TestAppendEndpointAttempt(t *testing.T) {
	store := &TraceStore{
		records:  make([]*RoutingDecisionTrace, 0),
		inflight: make(map[string]*RoutingDecisionTrace),
	}
	trace := &RoutingDecisionTrace{
		TraceUID:  "rt_append",
		CreatedAt: time.Now(),
	}
	store.records = append(store.records, trace)

	store.AppendEndpointAttempt("rt_append", EndpointAttemptSummary{
		AttemptUID: "a1",
		ChannelUID: "ch_a",
		Status:     "completed",
		Result:     "success",
		StatusCode: 200,
	})

	if len(store.records[0].EndpointAttempts) != 1 {
		t.Fatalf("尝试数 = %d, want 1", len(store.records[0].EndpointAttempts))
	}
	att := store.records[0].EndpointAttempts[0]
	if att.AttemptSeq != 1 {
		t.Errorf("AttemptSeq = %d, want 1", att.AttemptSeq)
	}
	if att.EndpointLabel == "" {
		t.Error("EndpointLabel 应自动派生")
	}
	clearAttemptCounter("rt_append")
}

func TestAppendEndpointAttempt_Merge(t *testing.T) {
	store := &TraceStore{
		records:  make([]*RoutingDecisionTrace, 0),
		inflight: make(map[string]*RoutingDecisionTrace),
	}
	trace := &RoutingDecisionTrace{TraceUID: "rt_merge", CreatedAt: time.Now()}
	store.records = append(store.records, trace)

	// 先 started
	store.AppendEndpointAttempt("rt_merge", EndpointAttemptSummary{
		AttemptUID: "a1", ChannelUID: "ch_a", Status: "started", AttemptSeq: 1,
	})
	// 后 completed（相同 attemptUid，应合并）
	store.AppendEndpointAttempt("rt_merge", EndpointAttemptSummary{
		AttemptUID: "a1", ChannelUID: "ch_a", Status: "completed", Result: "success", AttemptSeq: 1,
	})

	if len(store.records[0].EndpointAttempts) != 1 {
		t.Fatalf("合并后尝试数 = %d, want 1", len(store.records[0].EndpointAttempts))
	}
	if store.records[0].EndpointAttempts[0].Status != "completed" {
		t.Errorf("合并后 Status = %q, want completed", store.records[0].EndpointAttempts[0].Status)
	}
	clearAttemptCounter("rt_merge")
}

func TestAppendEndpointAttempt_Truncation(t *testing.T) {
	store := &TraceStore{
		records:  make([]*RoutingDecisionTrace, 0),
		inflight: make(map[string]*RoutingDecisionTrace),
	}
	trace := &RoutingDecisionTrace{TraceUID: "rt_trunc", CreatedAt: time.Now()}
	store.records = append(store.records, trace)

	// 追加超过 MaxAttemptsPerTrace 的尝试（唯一 UID 避免合并）
	for i := 0; i < MaxAttemptsPerTrace+5; i++ {
		store.AppendEndpointAttempt("rt_trunc", EndpointAttemptSummary{
			AttemptUID: "a" + string(rune('0'+i%10)) + string(rune('0'+i/10)),
			ChannelUID: "ch_a", Status: "completed", Result: "success",
			AttemptSeq: i + 1,
		})
	}

	if !store.records[0].AttemptsTruncated {
		t.Error("超过上限应标记 AttemptsTruncated")
	}
	if len(store.records[0].EndpointAttempts) > MaxAttemptsPerTrace {
		t.Errorf("尝试数 = %d, 应 <= %d", len(store.records[0].EndpointAttempts), MaxAttemptsPerTrace)
	}
	clearAttemptCounter("rt_trunc")
}

func TestAppendEndpointAttempt_NilSafety(t *testing.T) {
	store := &TraceStore{
		records:  make([]*RoutingDecisionTrace, 0),
		inflight: make(map[string]*RoutingDecisionTrace),
	}
	// 不应 panic
	store.AppendEndpointAttempt("", EndpointAttemptSummary{})
	store.AppendEndpointAttempt("rt_missing", EndpointAttemptSummary{AttemptUID: "a1"})
}

func TestAppendEndpointAttempt_AutoSeq(t *testing.T) {
	store := &TraceStore{
		records:  make([]*RoutingDecisionTrace, 0),
		inflight: make(map[string]*RoutingDecisionTrace),
	}
	trace := &RoutingDecisionTrace{TraceUID: "rt_seq", CreatedAt: time.Now()}
	store.records = append(store.records, trace)

	// 不指定 AttemptSeq，应自动递增
	store.AppendEndpointAttempt("rt_seq", EndpointAttemptSummary{AttemptUID: "a1", ChannelUID: "ch_a"})
	store.AppendEndpointAttempt("rt_seq", EndpointAttemptSummary{AttemptUID: "a2", ChannelUID: "ch_b"})

	if store.records[0].EndpointAttempts[0].AttemptSeq != 1 {
		t.Errorf("第一次 AttemptSeq = %d, want 1", store.records[0].EndpointAttempts[0].AttemptSeq)
	}
	if store.records[0].EndpointAttempts[1].AttemptSeq != 2 {
		t.Errorf("第二次 AttemptSeq = %d, want 2", store.records[0].EndpointAttempts[1].AttemptSeq)
	}
	clearAttemptCounter("rt_seq")
}
