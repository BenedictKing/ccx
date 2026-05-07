package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
)

// TestSelectChannelLB_SingleCandidate 单候选时 LB 直接返回该 channel。
func TestSelectChannelLB_SingleCandidate(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "only-channel",
				BaseURL:  "https://only.example.com",
				APIKeys:  []string{"sk-only"},
				Status:   "active",
				Priority: 1,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannel(context.Background(), "u1", nil, ChannelKindMessages, "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 0 {
		t.Fatalf("期望 index=0，实际为 %d", result.ChannelIndex)
	}
}

// TestSelectChannelLB_PromotionBoostBeatsPriority 促销期渠道 +800 分，胜过同等条件的高优先级渠道。
func TestSelectChannelLB_PromotionBoostBeatsPriority(t *testing.T) {
	until := time.Now().Add(5 * time.Minute)
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "high-priority",
				BaseURL:  "https://high.example.com",
				APIKeys:  []string{"sk-high"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promo"},
				Status:         "active",
				Priority:       9,
				PromotionUntil: &until,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannel(context.Background(), "", nil, ChannelKindMessages, "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("期望选择促销渠道 index=1，实际为 %d", result.ChannelIndex)
	}
	if result.Reason != "promotion_priority" {
		t.Fatalf("期望 reason=promotion_priority，实际为 %s", result.Reason)
	}
}

// TestSelectChannelLB_TraceAffinityHit trace 亲和命中时 +1000 分，覆盖优先级。
func TestSelectChannelLB_TraceAffinityHit(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "preferred-by-priority",
				BaseURL:  "https://prio.example.com",
				APIKeys:  []string{"sk-prio"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "affinity-target",
				BaseURL:  "https://aff.example.com",
				APIKeys:  []string{"sk-aff"},
				Status:   "active",
				Priority: 5,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	scheduler.traceAffinity.SetPreferredChannel(string(ChannelKindMessages)+":user-x", 1)

	result, err := scheduler.SelectChannel(context.Background(), "user-x", nil, ChannelKindMessages, "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("期望选择 affinity 渠道 index=1，实际为 %d", result.ChannelIndex)
	}
	if result.Reason != "trace_affinity" {
		t.Fatalf("期望 reason=trace_affinity，实际为 %s", result.Reason)
	}
}

// TestSelectChannelLB_CircuitOpenFiltered circuit Open 渠道被 hard filter 直接剔除（非促销期）。
func TestSelectChannelLB_CircuitOpenFiltered(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "broken",
				BaseURL:  "https://broken.example.com",
				APIKeys:  []string{"sk-broken"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "healthy",
				BaseURL:  "https://healthy.example.com",
				APIKeys:  []string{"sk-healthy"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 让 broken 渠道连续失败若干次以触发熔断/不健康判定
	for i := 0; i < 20; i++ {
		scheduler.messagesMetricsManager.RecordFailure("https://broken.example.com", "sk-broken", "claude")
	}

	result, err := scheduler.SelectChannel(context.Background(), "", nil, ChannelKindMessages, "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("期望选择 healthy 渠道 index=1（broken 被硬过滤），实际为 %d", result.ChannelIndex)
	}
}

// TestSelectChannelLB_FailedChannelExcluded 已在本次请求失败的渠道不应被再次选中。
func TestSelectChannelLB_FailedChannelExcluded(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "first",
				BaseURL:  "https://first.example.com",
				APIKeys:  []string{"sk-first"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "second",
				BaseURL:  "https://second.example.com",
				APIKeys:  []string{"sk-second"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	failed := map[int]bool{0: true}
	result, err := scheduler.SelectChannel(context.Background(), "", failed, ChannelKindMessages, "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("期望跳过已失败的 index=0，选择 index=1，实际为 %d", result.ChannelIndex)
	}
}

// TestSelectChannelLB_PromotionBypassesUnhealthy 促销期渠道即使不健康也通过硬过滤。
func TestSelectChannelLB_PromotionBypassesUnhealthy(t *testing.T) {
	until := time.Now().Add(5 * time.Minute)
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "normal",
				BaseURL:  "https://normal.example.com",
				APIKeys:  []string{"sk-normal"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-but-unhealthy",
				BaseURL:        "https://punh.example.com",
				APIKeys:        []string{"sk-punh"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &until,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	for i := 0; i < 20; i++ {
		scheduler.messagesMetricsManager.RecordFailure("https://punh.example.com", "sk-punh", "claude")
	}

	result, err := scheduler.SelectChannel(context.Background(), "", nil, ChannelKindMessages, "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("期望促销期渠道 index=1 即使不健康仍被选中，实际为 %d", result.ChannelIndex)
	}
	if result.Reason != "promotion_priority" {
		t.Fatalf("期望 reason=promotion_priority，实际为 %s", result.Reason)
	}
}

// TestSelectChannelLB_PriorityTiebreakerWhenScoresEqual 各 strategy 总分相同时
// LB 通过 OrderingWeight 次级 tiebreaker 保留 priority 主序。
func TestSelectChannelLB_PriorityTiebreakerWhenScoresEqual(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "p3",
				BaseURL:  "https://p3.example.com",
				APIKeys:  []string{"sk-p3"},
				Status:   "active",
				Priority: 3,
			},
			{
				Name:     "p1",
				BaseURL:  "https://p1.example.com",
				APIKeys:  []string{"sk-p1"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "p2",
				BaseURL:  "https://p2.example.com",
				APIKeys:  []string{"sk-p2"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannel(context.Background(), "", nil, ChannelKindMessages, "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	// upstream slice 中 p3=index 0、p1=index 1、p2=index 2；priority=1 应胜出。
	if result.ChannelIndex != 1 {
		t.Fatalf("期望选择 priority=1 的渠道 index=1，实际为 %d", result.ChannelIndex)
	}
	if result.Upstream.Name != "p1" {
		t.Fatalf("期望选择 p1，实际为 %s", result.Upstream.Name)
	}
}

// TestSelectChannelLB_LBProviderInitialized 验证 NewChannelScheduler 已为每个 kind
// 注入 LoadBalancer 与 LBMetricsProvider。
func TestSelectChannelLB_LBProviderInitialized(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name: "x", BaseURL: "https://x.example.com", APIKeys: []string{"sk-x"},
			Status: "active", Priority: 1,
		}},
	}
	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	for _, kind := range []ChannelKind{
		ChannelKindMessages, ChannelKindResponses, ChannelKindGemini,
		ChannelKindChat, ChannelKindImages,
	} {
		if scheduler.loadBalancerByKind[kind] == nil {
			t.Errorf("kind=%s 的 LoadBalancer 未初始化", kind)
		}
		if scheduler.lbProviderByKind[kind] == nil {
			t.Errorf("kind=%s 的 LBMetricsProvider 未初始化", kind)
		}
	}

	// 通过 provider 验证 channelKey 命名空间使用 BuildLBChannelKey（与 T3 单一来源对齐）。
	provider := scheduler.lbProviderByKind[ChannelKindMessages]
	if provider == nil {
		t.Fatal("messages provider 不应为 nil")
	}
	expectKey := metrics.BuildLBChannelKey(string(ChannelKindMessages), "x")
	_, key := provider.resolveChannel(0)
	if key != expectKey {
		t.Fatalf("provider channelKey 不一致：期望 %q 实际 %q", expectKey, key)
	}
}
