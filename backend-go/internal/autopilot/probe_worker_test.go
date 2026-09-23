package autopilot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ── ProbeQueue 测试 ──

func TestProbeQueue_EnqueueDequeue(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name       string
		items      []probeQueueItem
		dequeueN   int
		wantOrder  []string // 出队顺序（endpointUID）
		wantRemain int
	}{
		{
			name: "优先级排序: dead > degraded > unknown",
			items: []probeQueueItem{
				{EndpointUID: "unknown-1", Priority: ProbePriorityUnknown, EnqueuedAt: now},
				{EndpointUID: "dead-1", Priority: ProbePriorityDead, EnqueuedAt: now},
				{EndpointUID: "degraded-1", Priority: ProbePriorityDegraded, EnqueuedAt: now},
			},
			dequeueN:   3,
			wantOrder:  []string{"dead-1", "degraded-1", "unknown-1"},
			wantRemain: 0,
		},
		{
			name: "同优先级按入队时间 FIFO",
			items: []probeQueueItem{
				{EndpointUID: "dead-2", Priority: ProbePriorityDead, EnqueuedAt: now.Add(1 * time.Second)},
				{EndpointUID: "dead-1", Priority: ProbePriorityDead, EnqueuedAt: now},
			},
			dequeueN:   2,
			wantOrder:  []string{"dead-1", "dead-2"},
			wantRemain: 0,
		},
		{
			name: "部分出队",
			items: []probeQueueItem{
				{EndpointUID: "dead-1", Priority: ProbePriorityDead, EnqueuedAt: now},
				{EndpointUID: "degraded-1", Priority: ProbePriorityDegraded, EnqueuedAt: now},
				{EndpointUID: "unknown-1", Priority: ProbePriorityUnknown, EnqueuedAt: now},
			},
			dequeueN:   2,
			wantOrder:  []string{"dead-1", "degraded-1"},
			wantRemain: 1,
		},
		{
			name:       "空队列出队",
			items:      nil,
			dequeueN:   5,
			wantOrder:  nil,
			wantRemain: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewProbeQueue()
			for _, item := range tt.items {
				q.Enqueue(item)
			}

			batch := q.DequeueBatch(tt.dequeueN)
			if len(batch) != len(tt.wantOrder) {
				t.Fatalf("出队数量: got %d, want %d", len(batch), len(tt.wantOrder))
			}
			for i, want := range tt.wantOrder {
				if batch[i].EndpointUID != want {
					t.Errorf("出队[%d]: got %s, want %s", i, batch[i].EndpointUID, want)
				}
			}
			if q.Len() != tt.wantRemain {
				t.Errorf("队列剩余: got %d, want %d", q.Len(), tt.wantRemain)
			}
		})
	}
}

func TestProbeQueue_Dedup(t *testing.T) {
	q := NewProbeQueue()
	now := time.Now()

	q.Enqueue(probeQueueItem{EndpointUID: "ep-1", Priority: ProbePriorityUnknown, EnqueuedAt: now})
	q.Enqueue(probeQueueItem{EndpointUID: "ep-1", Priority: ProbePriorityUnknown, EnqueuedAt: now})

	if q.Len() != 1 {
		t.Fatalf("去重失败: 队列长度=%d, want 1", q.Len())
	}
}

func TestProbeQueue_PriorityUpgrade(t *testing.T) {
	q := NewProbeQueue()
	now := time.Now()

	// 先以 unknown 优先级入队
	q.Enqueue(probeQueueItem{EndpointUID: "ep-1", Priority: ProbePriorityUnknown, EnqueuedAt: now})
	// 再以 dead 优先级入队（应提升优先级）
	q.Enqueue(probeQueueItem{EndpointUID: "ep-1", Priority: ProbePriorityDead, EnqueuedAt: now})

	if q.Len() != 1 {
		t.Fatalf("队列长度: got %d, want 1", q.Len())
	}

	batch := q.DequeueBatch(1)
	if len(batch) != 1 {
		t.Fatalf("出队数量: got %d, want 1", len(batch))
	}
	if batch[0].Priority != ProbePriorityDead {
		t.Errorf("优先级: got %d, want %d", batch[0].Priority, ProbePriorityDead)
	}
}

func TestProbeQueue_Remove(t *testing.T) {
	q := NewProbeQueue()
	now := time.Now()

	q.Enqueue(probeQueueItem{EndpointUID: "ep-1", Priority: ProbePriorityDead, EnqueuedAt: now})
	q.Enqueue(probeQueueItem{EndpointUID: "ep-2", Priority: ProbePriorityDegraded, EnqueuedAt: now})
	q.Enqueue(probeQueueItem{EndpointUID: "ep-3", Priority: ProbePriorityUnknown, EnqueuedAt: now})

	q.Remove("ep-2")

	if q.Len() != 2 {
		t.Fatalf("Remove 后队列长度: got %d, want 2", q.Len())
	}
	if q.Contains("ep-2") {
		t.Error("Remove 后 Contains 应返回 false")
	}
	if !q.Contains("ep-1") {
		t.Error("ep-1 应仍在队列中")
	}
	if !q.Contains("ep-3") {
		t.Error("ep-3 应仍在队列中")
	}

	// 出队验证顺序
	batch := q.DequeueBatch(2)
	if batch[0].EndpointUID != "ep-1" || batch[1].EndpointUID != "ep-3" {
		t.Errorf("出队顺序错误: got [%s, %s], want [ep-1, ep-3]",
			batch[0].EndpointUID, batch[1].EndpointUID)
	}
}

// ── ProbeBudget 测试 ──

func TestProbeBudget_Basic(t *testing.T) {
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	budget := NewProbeBudgetWithTime(3, func() time.Time { return now })

	// 消耗 3 次应全部成功
	for i := 0; i < 3; i++ {
		if !budget.TryConsume() {
			t.Fatalf("第 %d 次 TryConsume 应成功", i+1)
		}
	}

	// 第 4 次应失败（预算耗尽）
	if budget.TryConsume() {
		t.Error("第 4 次 TryConsume 应失败（预算耗尽）")
	}

	if budget.Used() != 3 {
		t.Errorf("Used: got %d, want 3", budget.Used())
	}
	if budget.Remaining() != 0 {
		t.Errorf("Remaining: got %d, want 0", budget.Remaining())
	}
}

func TestProbeBudget_DailyReset(t *testing.T) {
	var now time.Time
	now = time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	budget := NewProbeBudgetWithTime(2, func() time.Time { return now })

	// 消耗完
	budget.TryConsume()
	budget.TryConsume()
	if budget.TryConsume() {
		t.Error("预算应已耗尽")
	}

	// 跨天
	now = time.Date(2025, 1, 16, 1, 0, 0, 0, time.UTC)
	if !budget.TryConsume() {
		t.Error("跨天后预算应重置，TryConsume 应成功")
	}
	if budget.Remaining() != 1 {
		t.Errorf("Remaining: got %d, want 1", budget.Remaining())
	}
}

func TestProbeBudget_ZeroLimit(t *testing.T) {
	budget := NewProbeBudget(0) // 应使用默认值
	if budget.dailyLimit != int32(DefaultProbeDailyBudget) {
		t.Errorf("零值应使用默认 dailyLimit=%d, got %d", DefaultProbeDailyBudget, budget.dailyLimit)
	}
}

// ── isProbeAlive 测试 ──

func TestIsProbeAlive(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{200, true},
		{201, true},
		{204, true},
		{400, true},  // 参数问题但端点存活
		{401, true},  // 认证问题但端点存活
		{403, true},  // 权限问题但端点存活
		{422, true},  // 验证错误但端点存活
		{429, true},  // 限流但端点存活
		{404, false}, // 未找到
		{405, false}, // 方法不允许
		{408, false}, // 超时
		{500, false}, // 服务器错误
		{502, false}, // 网关错误
		{503, false}, // 服务不可用
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.code), func(t *testing.T) {
			got := isProbeAlive(tt.code)
			if got != tt.want {
				t.Errorf("isProbeAlive(%d) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

// ── isProbeEligible 测试 ──

func TestIsProbeEligible(t *testing.T) {
	tests := []struct {
		state HealthState
		want  bool
	}{
		{HealthStateUnknown, true},
		{HealthStateDegraded, true},
		{HealthStateLimited, true},
		{HealthStateDead, true},
		{HealthStateHealthy, false},
		{HealthStateMisconfigured, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			got := isProbeEligible(tt.state)
			if got != tt.want {
				t.Errorf("isProbeEligible(%s) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

// ── buildProbeRequest 测试 ──

func TestBuildProbeRequest(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		wantMethod  string
		wantURLFrag string // URL 应包含的片段
		wantHeader  string // 应设置的认证头名
	}{
		{
			name:        "claude → count_tokens",
			serviceType: "claude",
			wantMethod:  http.MethodPost,
			wantURLFrag: "/v1/messages/count_tokens",
			wantHeader:  "x-api-key",
		},
		{
			name:        "openai → chat/completions",
			serviceType: "openai",
			wantMethod:  http.MethodPost,
			wantURLFrag: "/v1/chat/completions",
			wantHeader:  "Authorization",
		},
		{
			name:        "responses → /v1/responses",
			serviceType: "responses",
			wantMethod:  http.MethodPost,
			wantURLFrag: "/v1/responses",
			wantHeader:  "Authorization",
		},
		{
			name:        "gemini → generateContent",
			serviceType: "gemini",
			wantMethod:  http.MethodPost,
			wantURLFrag: "generateContent",
			wantHeader:  "x-goog-api-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := &KeyEndpointProfile{
				EndpointUID: "test-ep",
				BaseURL:     "https://example.com",
				ServiceType: tt.serviceType,
				KeyMask:     "sk-***test",
			}

			req, err := buildProbeRequest(profile, "sk-real-test-key")
			if err != nil {
				t.Fatalf("buildProbeRequest 失败: %v", err)
			}
			if req.Method != tt.wantMethod {
				t.Errorf("Method: got %s, want %s", req.Method, tt.wantMethod)
			}
			if !contains(req.URL, tt.wantURLFrag) {
				t.Errorf("URL %q 不包含 %q", req.URL, tt.wantURLFrag)
			}
			if _, ok := req.Headers[tt.wantHeader]; !ok {
				t.Errorf("缺少认证头 %q, 实际 headers: %v", tt.wantHeader, req.Headers)
			}
		})
	}
}

func TestBuildProbeRequest_UnsupportedServiceType(t *testing.T) {
	profile := &KeyEndpointProfile{
		EndpointUID: "test-ep",
		BaseURL:     "https://example.com",
		ServiceType: "unknown_type",
	}
	_, err := buildProbeRequest(profile, "sk-real-test-key")
	if err == nil {
		t.Error("不支持的 serviceType 应返回错误")
	}
}

// ── ProbeWorker 集成测试（httptest 模拟）──

// alwaysResolveKey 测试用 APIKeyResolver：始终返回固定的假 key，模拟 resolver 命中。
func alwaysResolveKey(channelUID, keyHash string) (string, bool) {
	return "sk-test-real-key", true
}

func TestProbeWorker_CooldownSkip(t *testing.T) {
	// 准备：一个最近刚探测过的 endpoint
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	store := newTestProfileStore(t)
	lastProbe := now.Add(-1 * time.Hour) // 1 小时前探测过
	profile := &KeyEndpointProfile{
		EndpointUID:  "ep-cooldown",
		ChannelUID:   "ch-1",
		BaseURL:      "https://example.com",
		ServiceType:  "claude",
		HealthState:  HealthStateUnknown,
		LastProbeAt:  &lastProbe,
		ProbeSuccess: false,
	}
	_ = store.Upsert(profile)

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})

	// 扫描并入队
	worker.scanAndEnqueue()

	// 冷却期内不应入队
	if worker.QueueLen() != 0 {
		t.Errorf("冷却期内 endpoint 不应入队: queueLen=%d", worker.QueueLen())
	}
}

func TestProbeWorker_CooldownExpired(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	store := newTestProfileStore(t)
	lastProbe := now.Add(-7 * time.Hour) // 7 小时前探测过（超过 6h 冷却期）
	profile := &KeyEndpointProfile{
		EndpointUID:  "ep-expired",
		ChannelUID:   "ch-1",
		BaseURL:      "https://example.com",
		ServiceType:  "claude",
		HealthState:  HealthStateUnknown,
		LastProbeAt:  &lastProbe,
		ProbeSuccess: false,
	}
	_ = store.Upsert(profile)

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})

	worker.scanAndEnqueue()

	if worker.QueueLen() != 1 {
		t.Errorf("冷却期过期后应入队: queueLen=%d, want 1", worker.QueueLen())
	}
}

func TestProbeWorker_BudgetExhausted(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	store := newTestProfileStore(t)

	// 创建 10 个待探测 endpoint
	for i := 0; i < 10; i++ {
		_ = store.Upsert(&KeyEndpointProfile{
			EndpointUID: fmt.Sprintf("ep-%d", i),
			ChannelUID:  "ch-1",
			BaseURL:     "https://example.com",
			ServiceType: "claude",
			HealthState: HealthStateUnknown,
		})
	}

	// 预算仅 3
	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 3,
		BatchSize:   10, // 一次出队全部
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})
	worker.SetAPIKeyResolver(alwaysResolveKey)

	worker.scanAndEnqueue()
	if worker.QueueLen() != 10 {
		t.Fatalf("应入队 10 个, got %d", worker.QueueLen())
	}

	// processQueue 应只探测 3 个（受预算限制）
	worker.processQueue()

	// 预算耗尽后，剩余的应被重新入队
	if worker.BudgetRemaining() != 0 {
		t.Errorf("预算应耗尽: remaining=%d", worker.BudgetRemaining())
	}
}

func TestProbeWorker_ProbeSuccess(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	// 模拟上游：返回 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"test","stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	store := newTestProfileStore(t)
	profile := &KeyEndpointProfile{
		EndpointUID: "ep-ok",
		ChannelUID:  "ch-1",
		BaseURL:     server.URL,
		ServiceType: "claude",
		HealthState: HealthStateUnknown,
		KeyMask:     "sk-***test",
	}
	_ = store.Upsert(profile)

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		BatchSize:   5,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})
	worker.SetAPIKeyResolver(alwaysResolveKey)

	// 手动入队并处理
	worker.queue.Enqueue(probeQueueItem{
		EndpointUID: "ep-ok",
		Priority:    ProbePriorityUnknown,
		EnqueuedAt:  now,
	})
	worker.processQueue()

	// 验证画像更新
	result := store.Get("ep-ok")
	if result == nil {
		t.Fatal("画像不应为 nil")
	}
	if result.LastProbeAt == nil {
		t.Error("LastProbeAt 不应为 nil")
	}
	if !result.ProbeSuccess {
		t.Error("ProbeSuccess 应为 true (200 OK)")
	}
	if result.ProbeStatusCode != 200 {
		t.Errorf("ProbeStatusCode: got %d, want 200", result.ProbeStatusCode)
	}
	// unknown + 探测成功 → healthy
	if result.HealthState != HealthStateHealthy {
		t.Errorf("HealthState: got %s, want %s (unknown→healthy)", result.HealthState, HealthStateHealthy)
	}
}

func TestProbeWorker_ProbeAuthFailure(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	// 模拟上游：返回 401
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid key"}}`))
	}))
	defer server.Close()

	store := newTestProfileStore(t)
	profile := &KeyEndpointProfile{
		EndpointUID: "ep-auth-fail",
		ChannelUID:  "ch-1",
		BaseURL:     server.URL,
		ServiceType: "claude",
		HealthState: HealthStateUnknown,
		KeyMask:     "sk-***badkey",
	}
	_ = store.Upsert(profile)

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})
	worker.SetAPIKeyResolver(alwaysResolveKey)

	worker.queue.Enqueue(probeQueueItem{
		EndpointUID: "ep-auth-fail",
		Priority:    ProbePriorityUnknown,
		EnqueuedAt:  now,
	})
	worker.processQueue()

	result := store.Get("ep-auth-fail")
	if result == nil {
		t.Fatal("画像不应为 nil")
	}
	// 401 视为存活（认证问题但端点存在）
	if !result.ProbeSuccess {
		t.Error("401 应视为 ProbeSuccess=true（端点存活）")
	}
	if result.ProbeStatusCode != 401 {
		t.Errorf("ProbeStatusCode: got %d, want 401", result.ProbeStatusCode)
	}
}

func TestProbeWorker_ProbeServerDown(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	// 模拟上游：返回 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	store := newTestProfileStore(t)
	profile := &KeyEndpointProfile{
		EndpointUID: "ep-down",
		ChannelUID:  "ch-1",
		BaseURL:     server.URL,
		ServiceType: "claude",
		HealthState: HealthStateDead,
		KeyMask:     "sk-***test",
	}
	_ = store.Upsert(profile)

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})
	worker.SetAPIKeyResolver(alwaysResolveKey)

	worker.queue.Enqueue(probeQueueItem{
		EndpointUID: "ep-down",
		Priority:    ProbePriorityDead,
		EnqueuedAt:  now,
	})
	worker.processQueue()

	result := store.Get("ep-down")
	if result == nil {
		t.Fatal("画像不应为 nil")
	}
	// 500 不视为存活
	if result.ProbeSuccess {
		t.Error("500 应视为 ProbeSuccess=false")
	}
	// dead + 探测失败 → 保持 dead
	if result.HealthState != HealthStateDead {
		t.Errorf("HealthState: got %s, want dead (探测失败保持原状态)", result.HealthState)
	}
}

func TestProbeWorker_DeadToDegraded(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	// 模拟上游：返回 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	store := newTestProfileStore(t)
	profile := &KeyEndpointProfile{
		EndpointUID: "ep-dead-recover",
		ChannelUID:  "ch-1",
		BaseURL:     server.URL,
		ServiceType: "claude",
		HealthState: HealthStateDead,
		KeyMask:     "sk-***test",
	}
	_ = store.Upsert(profile)

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})
	worker.SetAPIKeyResolver(alwaysResolveKey)

	worker.queue.Enqueue(probeQueueItem{
		EndpointUID: "ep-dead-recover",
		Priority:    ProbePriorityDead,
		EnqueuedAt:  now,
	})
	worker.processQueue()

	result := store.Get("ep-dead-recover")
	if result == nil {
		t.Fatal("画像不应为 nil")
	}
	// dead + 探测成功 → degraded（等待 L1 验证）
	if result.HealthState != HealthStateDegraded {
		t.Errorf("HealthState: got %s, want degraded (dead→degraded)", result.HealthState)
	}
}

func TestProbeWorker_NoProbeWhenHealthy(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	store := newTestProfileStore(t)
	_ = store.Upsert(&KeyEndpointProfile{
		EndpointUID: "ep-healthy",
		ChannelUID:  "ch-1",
		BaseURL:     "https://example.com",
		ServiceType: "claude",
		HealthState: HealthStateHealthy,
	})

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})

	worker.scanAndEnqueue()

	if worker.QueueLen() != 0 {
		t.Errorf("healthy endpoint 不应入队: queueLen=%d", worker.QueueLen())
	}
}

func TestProbeWorker_NoProbeWhenMisconfigured(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	store := newTestProfileStore(t)
	_ = store.Upsert(&KeyEndpointProfile{
		EndpointUID: "ep-misconf",
		ChannelUID:  "ch-1",
		BaseURL:     "https://example.com",
		ServiceType: "claude",
		HealthState: HealthStateMisconfigured,
	})

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})

	worker.scanAndEnqueue()

	if worker.QueueLen() != 0 {
		t.Errorf("misconfigured endpoint 不应入队: queueLen=%d", worker.QueueLen())
	}
}

func TestProbeWorker_ContextCancel(t *testing.T) {
	store := newTestProfileStore(t)
	worker := NewProbeWorker(store, ProbeWorkerConfig{
		ScanInterval: 50 * time.Millisecond,
		QuietLogs:    true,
	})

	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// 等待至少一个循环
	time.Sleep(100 * time.Millisecond)

	cancel()
	worker.Stop() // 应正常返回，不阻塞
}

func TestProbeWorker_BudgetLog(t *testing.T) {
	// 验证预算耗尽时的行为：剩余条目被重新入队
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	store := newTestProfileStore(t)

	for i := 0; i < 5; i++ {
		_ = store.Upsert(&KeyEndpointProfile{
			EndpointUID: fmt.Sprintf("ep-%d", i),
			ChannelUID:  "ch-1",
			BaseURL:     "https://example.com",
			ServiceType: "claude",
			HealthState: HealthStateUnknown,
		})
	}

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 2, // 预算仅 2
		BatchSize:   5,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})
	worker.SetAPIKeyResolver(alwaysResolveKey)

	worker.scanAndEnqueue()
	queueLenBefore := worker.QueueLen()
	worker.processQueue()

	// 预算耗尽后，剩余的 3 个应被重新入队
	queueLenAfter := worker.QueueLen()
	// 出队 5 个，消耗 2 个，剩余 3 个重新入队
	if queueLenAfter != 3 {
		t.Errorf("预算耗尽后重新入队: queueLen=%d, want 3 (before=%d)", queueLenAfter, queueLenBefore)
	}
}

func TestProbeWorker_ConcurrentBudgetSafety(t *testing.T) {
	budget := NewProbeBudgetWithTime(1000, time.Now)

	// 并发消耗预算
	var successCount atomic.Int32
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 200; j++ {
				if budget.TryConsume() {
					successCount.Add(1)
				}
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	if successCount.Load() != 1000 {
		t.Errorf("并发消耗: got %d, want 1000", successCount.Load())
	}
	if budget.Remaining() != 0 {
		t.Errorf("并发消耗后剩余: got %d, want 0", budget.Remaining())
	}
}

// ── 额外覆盖：ProbeRequest body 验证 ──

func TestBuildProbeRequest_BodyValidJSON(t *testing.T) {
	tests := []struct {
		serviceType string
	}{
		{"claude"},
		{"openai"},
		{"responses"},
		{"gemini"},
	}

	for _, tt := range tests {
		t.Run(tt.serviceType, func(t *testing.T) {
			profile := &KeyEndpointProfile{
				EndpointUID: "ep-json",
				BaseURL:     "https://example.com",
				ServiceType: tt.serviceType,
				KeyMask:     "sk-***test",
			}

			req, err := buildProbeRequest(profile, "sk-real-test-key")
			if err != nil {
				t.Fatalf("buildProbeRequest 失败: %v", err)
			}

			var parsed map[string]interface{}
			if err := json.Unmarshal(req.Body, &parsed); err != nil {
				t.Errorf("Body 不是合法 JSON: %v", err)
			}
		})
	}
}

func TestProbeWorker_ProbeConfidence(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	store := newTestProfileStore(t)
	profile := &KeyEndpointProfile{
		EndpointUID: "ep-conf",
		ChannelUID:  "ch-1",
		BaseURL:     server.URL,
		ServiceType: "claude",
		HealthState: HealthStateUnknown,
		KeyMask:     "sk-***test",
	}
	_ = store.Upsert(profile)

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})
	worker.SetAPIKeyResolver(alwaysResolveKey)

	worker.queue.Enqueue(probeQueueItem{
		EndpointUID: "ep-conf",
		Priority:    ProbePriorityUnknown,
		EnqueuedAt:  now,
	})
	worker.processQueue()

	result := store.Get("ep-conf")
	if result == nil {
		t.Fatal("画像不应为 nil")
	}
	// 成功探测置信度应为 0.8
	if result.ProbeConfidence != 0.8 {
		t.Errorf("ProbeConfidence: got %f, want 0.8", result.ProbeConfidence)
	}
}

// ── APIKeyResolver fail-open 测试 ──

func TestProbeWorker_NoResolverSkipsProbeWithoutConsumingBudget(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	// 上游若被调用应记录到 called，用于验证探测请求确实没有发出
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := newTestProfileStore(t)
	_ = store.Upsert(&KeyEndpointProfile{
		EndpointUID: "ep-no-resolver",
		ChannelUID:  "ch-1",
		BaseURL:     server.URL,
		ServiceType: "claude",
		HealthState: HealthStateUnknown,
		KeyMask:     "sk-***test",
	})

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})
	// 未调用 SetAPIKeyResolver，resolver 保持 nil

	worker.queue.Enqueue(probeQueueItem{
		EndpointUID: "ep-no-resolver",
		Priority:    ProbePriorityUnknown,
		EnqueuedAt:  now,
	})
	worker.processQueue()

	if called {
		t.Error("resolver 未注入时不应发起真实探测请求")
	}
	if worker.BudgetRemaining() != 100 {
		t.Errorf("resolver 未注入时不应消耗预算: remaining=%d, want 100", worker.BudgetRemaining())
	}
	result := store.Get("ep-no-resolver")
	if result.LastProbeAt != nil {
		t.Error("跳过的探测不应写入 LastProbeAt")
	}
}

func TestProbeWorker_ResolverMissReturnsFalseSkipsProbe(t *testing.T) {
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := newTestProfileStore(t)
	_ = store.Upsert(&KeyEndpointProfile{
		EndpointUID: "ep-resolver-miss",
		ChannelUID:  "ch-deleted",
		BaseURL:     server.URL,
		ServiceType: "claude",
		HealthState: HealthStateUnknown,
	})

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:    6 * time.Hour,
		DailyBudget: 100,
		QuietLogs:   true,
		TimeFunc:    func() time.Time { return now },
	})
	// resolver 命中不到（渠道已删除），始终返回 ok=false
	worker.SetAPIKeyResolver(func(channelUID, keyHash string) (string, bool) {
		return "", false
	})

	worker.queue.Enqueue(probeQueueItem{
		EndpointUID: "ep-resolver-miss",
		Priority:    ProbePriorityUnknown,
		EnqueuedAt:  now,
	})
	worker.processQueue()

	if called {
		t.Error("resolver 未命中时不应发起真实探测请求")
	}
	if worker.BudgetRemaining() != 100 {
		t.Errorf("resolver 未命中时不应消耗预算: remaining=%d, want 100", worker.BudgetRemaining())
	}
}

// ── degraded/limited → healthy 恢复阈值测试 ──

func TestProbeWorker_DegradedToHealthyRequiresConsecutiveSuccesses(t *testing.T) {
	clock := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	store := newTestProfileStore(t)
	_ = store.Upsert(&KeyEndpointProfile{
		EndpointUID: "ep-recover",
		ChannelUID:  "ch-1",
		BaseURL:     server.URL,
		ServiceType: "claude",
		HealthState: HealthStateDegraded,
		KeyMask:     "sk-***test",
	})

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:               1 * time.Hour, // 每次手动触发前推进时钟越过冷却期
		DailyBudget:            100,
		ProbeRecoveryThreshold: 2,
		QuietLogs:              true,
		TimeFunc:               func() time.Time { return clock },
	})
	worker.SetAPIKeyResolver(alwaysResolveKey)

	// 第一次探测成功：连续成功计数=1，未达阈值，应保持 degraded
	worker.queue.Enqueue(probeQueueItem{EndpointUID: "ep-recover", Priority: ProbePriorityDegraded, EnqueuedAt: clock})
	worker.processQueue()

	result := store.Get("ep-recover")
	if result.ConsecutiveProbeSuccess != 1 {
		t.Fatalf("第 1 次成功后 ConsecutiveProbeSuccess: got %d, want 1", result.ConsecutiveProbeSuccess)
	}
	if result.HealthState != HealthStateDegraded {
		t.Fatalf("未达阈值前应保持 degraded: got %s", result.HealthState)
	}

	// 推进时钟越过冷却期，第二次探测成功：连续成功计数=2，达到阈值，应恢复 healthy
	clock = clock.Add(2 * time.Hour)
	worker.queue.Enqueue(probeQueueItem{EndpointUID: "ep-recover", Priority: ProbePriorityDegraded, EnqueuedAt: clock})
	worker.processQueue()

	result = store.Get("ep-recover")
	if result.ConsecutiveProbeSuccess != 2 {
		t.Fatalf("第 2 次成功后 ConsecutiveProbeSuccess: got %d, want 2", result.ConsecutiveProbeSuccess)
	}
	if result.HealthState != HealthStateHealthy {
		t.Errorf("达到恢复阈值后应变为 healthy: got %s", result.HealthState)
	}
}

func TestProbeWorker_LimitedToHealthyRequiresConsecutiveSuccesses(t *testing.T) {
	clock := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	store := newTestProfileStore(t)
	_ = store.Upsert(&KeyEndpointProfile{
		EndpointUID: "ep-limited-recover",
		ChannelUID:  "ch-1",
		BaseURL:     server.URL,
		ServiceType: "claude",
		HealthState: HealthStateLimited,
		KeyMask:     "sk-***test",
	})

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:               1 * time.Hour,
		DailyBudget:            100,
		ProbeRecoveryThreshold: 2,
		QuietLogs:              true,
		TimeFunc:               func() time.Time { return clock },
	})
	worker.SetAPIKeyResolver(alwaysResolveKey)

	worker.queue.Enqueue(probeQueueItem{EndpointUID: "ep-limited-recover", Priority: ProbePriorityDegraded, EnqueuedAt: clock})
	worker.processQueue()
	if store.Get("ep-limited-recover").HealthState != HealthStateLimited {
		t.Fatal("第 1 次成功后未达阈值应保持 limited")
	}

	clock = clock.Add(2 * time.Hour)
	worker.queue.Enqueue(probeQueueItem{EndpointUID: "ep-limited-recover", Priority: ProbePriorityDegraded, EnqueuedAt: clock})
	worker.processQueue()
	if store.Get("ep-limited-recover").HealthState != HealthStateHealthy {
		t.Error("达到恢复阈值后 limited 应变为 healthy")
	}
}

func TestProbeWorker_FailureResetsConsecutiveSuccessCounter(t *testing.T) {
	clock := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	var statusCode atomic.Int32
	statusCode.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(statusCode.Load()))
	}))
	defer server.Close()

	store := newTestProfileStore(t)
	_ = store.Upsert(&KeyEndpointProfile{
		EndpointUID: "ep-flap",
		ChannelUID:  "ch-1",
		BaseURL:     server.URL,
		ServiceType: "claude",
		HealthState: HealthStateDegraded,
		KeyMask:     "sk-***test",
	})

	worker := NewProbeWorker(store, ProbeWorkerConfig{
		Cooldown:               1 * time.Hour,
		DailyBudget:            100,
		ProbeRecoveryThreshold: 2,
		QuietLogs:              true,
		TimeFunc:               func() time.Time { return clock },
	})
	worker.SetAPIKeyResolver(alwaysResolveKey)

	// 第一次成功：计数=1
	worker.queue.Enqueue(probeQueueItem{EndpointUID: "ep-flap", Priority: ProbePriorityDegraded, EnqueuedAt: clock})
	worker.processQueue()
	if store.Get("ep-flap").ConsecutiveProbeSuccess != 1 {
		t.Fatal("第 1 次成功后计数应为 1")
	}

	// 推进时钟越过冷却期，第二次失败（500）：计数应清零，且不应因为之前累计的成功次数误恢复
	clock = clock.Add(2 * time.Hour)
	statusCode.Store(http.StatusInternalServerError)
	worker.queue.Enqueue(probeQueueItem{EndpointUID: "ep-flap", Priority: ProbePriorityDegraded, EnqueuedAt: clock})
	worker.processQueue()

	result := store.Get("ep-flap")
	if result.ConsecutiveProbeSuccess != 0 {
		t.Errorf("探测失败后 ConsecutiveProbeSuccess 应清零: got %d", result.ConsecutiveProbeSuccess)
	}
	if result.HealthState != HealthStateDegraded {
		t.Errorf("失败不应改变 HealthState: got %s, want degraded", result.HealthState)
	}
}
