package wire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
	pipelinemw "github.com/BenedictKing/ccx/internal/pipeline/middleware"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/usage"
)

// fakeMetrics 实现 MetricsRecorder，记录所有写端调用以供断言。
type fakeMetrics struct {
	mu             sync.Mutex
	connDeltas     map[string]int64
	tpsRecords     map[string]int64 // 累计 totalTokens（每次调用累加）
	tpsDurations   map[string]int64 // 累计 durationMs
	tpsCallCount   map[string]int   // 调用次数
	connCallCount  int
	successCalls   []recordSuccessCall
	failureCalls   []recordFailureCall
}

type recordSuccessCall struct {
	baseURL     string
	apiKey      string
	serviceType string
	usage       *types.Usage
}

type recordFailureCall struct {
	baseURL     string
	apiKey      string
	serviceType string
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{
		connDeltas:   map[string]int64{},
		tpsRecords:   map[string]int64{},
		tpsDurations: map[string]int64{},
		tpsCallCount: map[string]int{},
	}
}

func (f *fakeMetrics) RecordActiveConnDelta(key string, delta int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connDeltas[key] += delta
	f.connCallCount++
}

func (f *fakeMetrics) RecordTPS(key string, tokens int64, durMs int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tpsRecords[key] += tokens
	f.tpsDurations[key] += durMs
	f.tpsCallCount[key]++
}

func (f *fakeMetrics) RecordSuccessWithUsage(baseURL, apiKey, serviceType string, u *types.Usage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var copyUsage *types.Usage
	if u != nil {
		c := *u
		copyUsage = &c
	}
	f.successCalls = append(f.successCalls, recordSuccessCall{
		baseURL:     baseURL,
		apiKey:      apiKey,
		serviceType: serviceType,
		usage:       copyUsage,
	})
}

func (f *fakeMetrics) RecordFailure(baseURL, apiKey, serviceType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failureCalls = append(f.failureCalls, recordFailureCall{
		baseURL:     baseURL,
		apiKey:      apiKey,
		serviceType: serviceType,
	})
}

// fakeStore 实现 usage.UsageStore，记录 Append 调用。
type fakeStore struct {
	mu      sync.Mutex
	records []usage.UsageRecord
	err     error
	closed  bool
}

func (s *fakeStore) Append(ctx context.Context, rec usage.UsageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.records = append(s.records, rec)
	return nil
}

func (s *fakeStore) Close() error { s.closed = true; return nil }

// fakeScheduler 实现 SchedulerView，按队列返回结果。
type fakeScheduler struct {
	results []*scheduler.SelectionResult
	errs    []error
	idx     int
	mu      sync.Mutex
	calls   []map[int]bool
}

func (f *fakeScheduler) SelectChannel(
	_ context.Context,
	_ string,
	failed map[int]bool,
	_ scheduler.ChannelKind,
	_ string,
	_ string,
) (*scheduler.SelectionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make(map[int]bool, len(failed))
	for k, v := range failed {
		cp[k] = v
	}
	f.calls = append(f.calls, cp)
	if f.idx >= len(f.results) {
		return nil, pipeline.ErrChannelExhausted
	}
	res := f.results[f.idx]
	var err error
	if f.idx < len(f.errs) {
		err = f.errs[f.idx]
	}
	f.idx++
	return res, err
}

// fakeOutbound 实现 pipeline.Outbound，仅供 NextChannel/TransformRequest 联动测试。
type fakeOutbound struct{}

func (fakeOutbound) TransformRequest(_ context.Context, _ *llm.Request) (*http.Request, []byte, error) {
	return httptest.NewRequest(http.MethodPost, "https://x.local", nil), nil, nil
}
func (fakeOutbound) TransformResponse(_ context.Context, _ *http.Response) (*llm.Response, error) {
	return &llm.Response{}, nil
}
func (fakeOutbound) TransformStream(_ context.Context, _ *http.Response) llm.Stream[*llm.Response] {
	ch := make(chan *llm.Response)
	close(ch)
	return llm.NewChanStream(context.Background(), ch, nil)
}

func newSelection(idx int, name, key, baseURL string) *scheduler.SelectionResult {
	return &scheduler.SelectionResult{
		Upstream: &config.UpstreamConfig{
			Name:        name,
			BaseURL:     baseURL,
			APIKeys:     []string{key},
			ServiceType: "claude",
		},
		ChannelIndex: idx,
		Reason:       "priority_order",
	}
}

func TestLBOutboundAdapter_NextChannel_UpdatesAttemptInfoAndMetadata(t *testing.T) {
	mm := newFakeMetrics()
	sched := &fakeScheduler{
		results: []*scheduler.SelectionResult{newSelection(7, "ch-A", "sk-aaa", "https://a.local")},
	}
	info := &pipelinemw.AttemptInfo{APIType: "Messages"}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	lb.AttemptInfo = info
	lb.LBMetricsMgr = mm
	lb.Model = "claude-3-5"
	lb.Request = &llm.Request{}

	if err := lb.NextChannel(context.Background()); err != nil {
		t.Fatalf("NextChannel err = %v", err)
	}
	if info.ChannelIndex != 7 || info.APIKey != "sk-aaa" || info.Upstream == nil || info.Upstream.Name != "ch-A" {
		t.Fatalf("AttemptInfo not updated: %+v", info)
	}
	upstream, key, baseURL, err := adapters.UpstreamBinding(lb.Request)
	if err != nil {
		t.Fatalf("upstream binding err = %v", err)
	}
	if upstream.Name != "ch-A" || key != "sk-aaa" || baseURL != "https://a.local" {
		t.Fatalf("metadata mismatch: %s/%s/%s", upstream.Name, key, baseURL)
	}
	if lb.CurrentChannelIndex() != 7 {
		t.Fatalf("currentChannel = %d, want 7", lb.CurrentChannelIndex())
	}
	if got := mm.connDeltas["messages:ch-A"]; got != 1 {
		t.Fatalf("ActiveConnDelta = %d, want 1", got)
	}
}

func TestLBOutboundAdapter_NextChannel_AccumulatesFailedChannels(t *testing.T) {
	sched := &fakeScheduler{
		results: []*scheduler.SelectionResult{
			newSelection(1, "ch-A", "k1", "u1"),
			newSelection(2, "ch-B", "k2", "u2"),
			newSelection(3, "ch-C", "k3", "u3"),
		},
	}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	lb.Request = &llm.Request{}

	for i := 0; i < 3; i++ {
		if err := lb.NextChannel(context.Background()); err != nil {
			t.Fatalf("NextChannel[%d] err = %v", i, err)
		}
	}
	failed := lb.FailedChannels()
	if !failed[1] || !failed[2] {
		t.Fatalf("failedChannels missing accumulated entries: %v", failed)
	}
	if failed[3] {
		t.Fatalf("currently selected channel 3 must not be in failedChannels: %v", failed)
	}
	// 第三次调用前 scheduler 应看到 {1, 2}
	if len(sched.calls) < 3 {
		t.Fatalf("scheduler call count = %d, want >= 3", len(sched.calls))
	}
	last := sched.calls[2]
	if !last[1] || !last[2] {
		t.Fatalf("scheduler call[2] failedChannels = %v, want contains {1,2}", last)
	}
}

func TestLBOutboundAdapter_NextChannel_ExhaustedReturnsErrChannelExhausted(t *testing.T) {
	sched := &fakeScheduler{}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	err := lb.NextChannel(context.Background())
	if !errors.Is(err, pipeline.ErrChannelExhausted) {
		t.Fatalf("err = %v, want ErrChannelExhausted", err)
	}
}

func TestLBOutboundAdapter_MarkFailed_AddsEntry(t *testing.T) {
	sched := &fakeScheduler{}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	lb.MarkFailed(42)
	lb.MarkFailed(7)
	got := lb.FailedChannels()
	if !got[42] || !got[7] {
		t.Fatalf("FailedChannels = %v, want contains {42, 7}", got)
	}
}

func TestLBOutboundAdapter_Finalize_SuccessRecordsTPSAndAppends(t *testing.T) {
	mm := newFakeMetrics()
	store := &fakeStore{}
	sched := &fakeScheduler{results: []*scheduler.SelectionResult{newSelection(0, "ch-X", "sk-xxx", "u")}}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	lb.AttemptInfo = &pipelinemw.AttemptInfo{APIType: "Messages"}
	lb.LBMetricsMgr = mm
	lb.UsageStore = store
	lb.Request = &llm.Request{}
	if err := lb.NextChannel(context.Background()); err != nil {
		t.Fatalf("NextChannel: %v", err)
	}

	llmUsage := llm.Usage{Inner: types.Usage{InputTokens: 10, OutputTokens: 20}, Format: llm.FormatClaudeMessages}
	lb.Finalize(context.Background(), true, "", "claude-3-5", llmUsage, 1500)

	if mm.tpsCallCount["messages:ch-X"] != 1 {
		t.Fatalf("RecordTPS call count = %d, want 1", mm.tpsCallCount["messages:ch-X"])
	}
	if mm.tpsRecords["messages:ch-X"] != 30 {
		t.Fatalf("RecordTPS tokens = %d, want 30", mm.tpsRecords["messages:ch-X"])
	}
	if mm.connDeltas["messages:ch-X"] != 0 {
		t.Fatalf("ActiveConnDelta net = %d, want 0 (paired +1/-1)", mm.connDeltas["messages:ch-X"])
	}
	if len(store.records) != 1 {
		t.Fatalf("usage records = %d, want 1", len(store.records))
	}
	rec := store.records[0]
	if !rec.Success || rec.ChannelID != 0 || rec.ChannelName != "ch-X" || rec.ModelID != "claude-3-5" {
		t.Fatalf("usage record mismatch: %+v", rec)
	}
	if rec.TotalTokens != 30 || rec.InputTokens != 10 || rec.OutputTokens != 20 {
		t.Fatalf("usage tokens mismatch: %+v", rec)
	}
	if rec.APIKeyMasked == "" || rec.APIKeyMasked == "sk-xxx" {
		t.Fatalf("APIKeyMasked = %q, expected masked form", rec.APIKeyMasked)
	}
	if rec.Format != string(llm.FormatClaudeMessages) {
		t.Fatalf("Format = %q, want claude_messages", rec.Format)
	}
}

func TestLBOutboundAdapter_Finalize_FailureSkipsTPSButStillAppends(t *testing.T) {
	mm := newFakeMetrics()
	store := &fakeStore{}
	sched := &fakeScheduler{results: []*scheduler.SelectionResult{newSelection(2, "ch-F", "k", "u")}}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	lb.AttemptInfo = &pipelinemw.AttemptInfo{APIType: "Messages"}
	lb.LBMetricsMgr = mm
	lb.UsageStore = store
	lb.Request = &llm.Request{}
	if err := lb.NextChannel(context.Background()); err != nil {
		t.Fatalf("NextChannel: %v", err)
	}

	llmUsage := llm.Usage{Inner: types.Usage{InputTokens: 5, OutputTokens: 0}}
	lb.Finalize(context.Background(), false, "upstream_error", "claude-3-5", llmUsage, 200)

	if mm.tpsCallCount["messages:ch-F"] != 0 {
		t.Fatalf("failure path must not RecordTPS, got %d", mm.tpsCallCount["messages:ch-F"])
	}
	if len(store.records) != 1 {
		t.Fatalf("usage records = %d, want 1", len(store.records))
	}
	rec := store.records[0]
	if rec.Success || rec.ErrorCode != "upstream_error" {
		t.Fatalf("usage record success/err mismatch: %+v", rec)
	}
}

func TestLBOutboundAdapter_Finalize_NilDependenciesDoNotPanic(t *testing.T) {
	sched := &fakeScheduler{results: []*scheduler.SelectionResult{newSelection(0, "ch-A", "k", "u")}}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	lb.Request = &llm.Request{}
	if err := lb.NextChannel(context.Background()); err != nil {
		t.Fatalf("NextChannel: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Finalize panicked with nil deps: %v", r)
		}
	}()
	lb.Finalize(context.Background(), true, "", "claude-3-5",
		llm.Usage{Inner: types.Usage{InputTokens: 1, OutputTokens: 2}}, 100)
}

func TestLBOutboundAdapter_Finalize_Idempotent(t *testing.T) {
	mm := newFakeMetrics()
	store := &fakeStore{}
	sched := &fakeScheduler{results: []*scheduler.SelectionResult{newSelection(0, "ch-Y", "k", "u")}}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	lb.LBMetricsMgr = mm
	lb.UsageStore = store
	lb.Request = &llm.Request{}
	_ = lb.NextChannel(context.Background())

	lb.Finalize(context.Background(), true, "", "claude", llm.Usage{Inner: types.Usage{InputTokens: 1, OutputTokens: 1}}, 10)
	lb.Finalize(context.Background(), true, "", "claude", llm.Usage{Inner: types.Usage{InputTokens: 1, OutputTokens: 1}}, 10)
	if len(store.records) != 1 {
		t.Fatalf("Finalize must be idempotent, records = %d", len(store.records))
	}
}

func TestLBOutboundAdapter_Finalize_SuccessRecordsPerKeySuccessWithUsage(t *testing.T) {
	mm := newFakeMetrics()
	sched := &fakeScheduler{results: []*scheduler.SelectionResult{newSelection(0, "ch-K", "sk-key", "https://up.local")}}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	lb.AttemptInfo = &pipelinemw.AttemptInfo{APIType: "Messages"}
	lb.LBMetricsMgr = mm
	lb.Request = &llm.Request{}
	if err := lb.NextChannel(context.Background()); err != nil {
		t.Fatalf("NextChannel: %v", err)
	}

	llmUsage := llm.Usage{Inner: types.Usage{InputTokens: 4, OutputTokens: 9}, Format: llm.FormatClaudeMessages}
	lb.Finalize(context.Background(), true, "", "claude-3-5", llmUsage, 250)

	if len(mm.successCalls) != 1 {
		t.Fatalf("RecordSuccessWithUsage call count = %d, want 1", len(mm.successCalls))
	}
	if len(mm.failureCalls) != 0 {
		t.Fatalf("RecordFailure must not be called on success path, got %d", len(mm.failureCalls))
	}
	got := mm.successCalls[0]
	if got.baseURL != "https://up.local" || got.apiKey != "sk-key" || got.serviceType != "claude" {
		t.Fatalf("RecordSuccessWithUsage args = %+v, want (https://up.local, sk-key, claude)", got)
	}
	if got.usage == nil || got.usage.InputTokens != 4 || got.usage.OutputTokens != 9 {
		t.Fatalf("RecordSuccessWithUsage usage = %+v, want input=4 output=9", got.usage)
	}
}

func TestLBOutboundAdapter_Finalize_FailureRecordsPerKeyFailure(t *testing.T) {
	mm := newFakeMetrics()
	sched := &fakeScheduler{results: []*scheduler.SelectionResult{newSelection(2, "ch-F", "sk-fail", "https://up2.local")}}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	lb.AttemptInfo = &pipelinemw.AttemptInfo{APIType: "Messages"}
	lb.LBMetricsMgr = mm
	lb.Request = &llm.Request{}
	if err := lb.NextChannel(context.Background()); err != nil {
		t.Fatalf("NextChannel: %v", err)
	}

	lb.Finalize(context.Background(), false, "upstream_error", "claude-3-5",
		llm.Usage{Inner: types.Usage{InputTokens: 1}}, 100)

	if len(mm.failureCalls) != 1 {
		t.Fatalf("RecordFailure call count = %d, want 1", len(mm.failureCalls))
	}
	if len(mm.successCalls) != 0 {
		t.Fatalf("RecordSuccessWithUsage must not be called on failure path, got %d", len(mm.successCalls))
	}
	got := mm.failureCalls[0]
	if got.baseURL != "https://up2.local" || got.apiKey != "sk-fail" || got.serviceType != "claude" {
		t.Fatalf("RecordFailure args = %+v, want (https://up2.local, sk-fail, claude)", got)
	}
}

func TestLBOutboundAdapter_Finalize_NilUpstreamOrKeySkipsPerKeyRecord(t *testing.T) {
	mm := newFakeMetrics()
	// 不调用 NextChannel —— currentUpstream / currentKey 均为零值。
	sched := &fakeScheduler{}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	lb.LBMetricsMgr = mm
	lb.Request = &llm.Request{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Finalize panicked with nil upstream: %v", r)
		}
	}()
	lb.Finalize(context.Background(), true, "", "claude",
		llm.Usage{Inner: types.Usage{InputTokens: 1, OutputTokens: 1}}, 10)

	if len(mm.successCalls) != 0 || len(mm.failureCalls) != 0 {
		t.Fatalf("per-key record must be skipped when upstream/key missing, success=%d failure=%d",
			len(mm.successCalls), len(mm.failureCalls))
	}
}

func TestBuildPipelineOpts_RegistersPauseBeforeKeyAndRetry(t *testing.T) {
	cfgManager := setupTestCfg(t)
	sched := &fakeScheduler{}
	lb := NewLBOutboundAdapter(fakeOutbound{}, sched, scheduler.ChannelKindMessages, time.Now())
	opts := BuildPipelineOpts(cfgManager, lb)
	// 期望至少 3 个 option：mws + emptyDetect + retry
	if len(opts) < 3 {
		t.Fatalf("opts count = %d, want >= 3", len(opts))
	}

	// 通过 factory 应用 opts，验证不 panic + 接受所有选项。
	factory := pipeline.NewFactory(pipeline.ExecutorFunc(func(_ context.Context, _ *http.Request) (*http.Response, error) {
		return nil, errors.New("not used")
	}))
	in := stubInbound{}
	p := factory.Pipeline(in, lb, opts...)
	if p == nil {
		t.Fatal("pipeline must not be nil")
	}
}

// stubInbound 仅用于 BuildPipelineOpts 的 smoke 测试。
type stubInbound struct{}

func (stubInbound) Format() llm.Format { return llm.FormatClaudeMessages }
func (stubInbound) TransformRequest(_ context.Context, _ *http.Request, _ []byte) (*llm.Request, error) {
	return &llm.Request{}, nil
}
func (stubInbound) TransformResponse(_ context.Context, _ *llm.Response) ([]byte, http.Header, error) {
	return nil, nil, nil
}
func (stubInbound) TransformStream(_ context.Context, _ llm.Stream[*llm.Response]) llm.Stream[*llm.StreamEvent] {
	ch := make(chan *llm.StreamEvent)
	close(ch)
	return llm.NewChanStream(context.Background(), ch, nil)
}

// setupTestCfg 构造一个最小 ConfigManager（空 upstream 切片）。
func setupTestCfg(t *testing.T) *config.ConfigManager {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(tmp, []byte(`{"upstream":[]}`), 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	cm, err := config.NewConfigManager(tmp)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	return cm
}
