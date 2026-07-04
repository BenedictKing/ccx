package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/ratelimit"
)

func TestDeleteChannelMetrics_SharedMetricsKeyPreserved(t *testing.T) {
	// 场景：两个渠道共享同一个 (BaseURL, APIKey) 组合
	// 删除其中一个渠道时，共享的 metricsKey 应该被保留

	testCases := []struct {
		name string
		kind ChannelKind
	}{
		{"Messages", ChannelKindMessages},
		{"Responses", ChannelKindResponses},
		{"Gemini", ChannelKindGemini},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sharedBaseURL := "https://shared.example.com"
			sharedAPIKey := "sk-shared-key"

			// 根据渠道类型构建配置
			var cfg config.Config
			switch tc.kind {
			case ChannelKindMessages:
				cfg = config.Config{
					Upstream: []config.UpstreamConfig{
						{
							Name:     "channel-A",
							BaseURL:  sharedBaseURL,
							APIKeys:  []string{sharedAPIKey, "sk-exclusive-A"},
							Status:   "active",
							Priority: 1,
						},
						{
							Name:     "channel-B",
							BaseURL:  sharedBaseURL,
							APIKeys:  []string{sharedAPIKey},
							Status:   "active",
							Priority: 2,
						},
					},
				}
			case ChannelKindResponses:
				cfg = config.Config{
					ResponsesUpstream: []config.UpstreamConfig{
						{
							Name:     "channel-A",
							BaseURL:  sharedBaseURL,
							APIKeys:  []string{sharedAPIKey, "sk-exclusive-A"},
							Status:   "active",
							Priority: 1,
						},
						{
							Name:     "channel-B",
							BaseURL:  sharedBaseURL,
							APIKeys:  []string{sharedAPIKey},
							Status:   "active",
							Priority: 2,
						},
					},
				}
			case ChannelKindGemini:
				cfg = config.Config{
					GeminiUpstream: []config.UpstreamConfig{
						{
							Name:     "channel-A",
							BaseURL:  sharedBaseURL,
							APIKeys:  []string{sharedAPIKey, "sk-exclusive-A"},
							Status:   "active",
							Priority: 1,
						},
						{
							Name:     "channel-B",
							BaseURL:  sharedBaseURL,
							APIKeys:  []string{sharedAPIKey},
							Status:   "active",
							Priority: 2,
						},
					},
				}
			}

			scheduler, cleanup := createTestScheduler(t, cfg)
			defer cleanup()

			// 根据渠道类型获取对应的 metricsManager
			var metricsManager *metrics.MetricsManager
			switch tc.kind {
			case ChannelKindMessages:
				metricsManager = scheduler.messagesMetricsManager
			case ChannelKindResponses:
				metricsManager = scheduler.responsesMetricsManager
			case ChannelKindGemini:
				metricsManager = scheduler.geminiMetricsManager
			}

			// 为所有 key 记录一些指标
			metricsManager.RecordSuccess(sharedBaseURL, sharedAPIKey, tcServiceType(tc.kind))
			metricsManager.RecordSuccess(sharedBaseURL, "sk-exclusive-A", tcServiceType(tc.kind))

			// 验证指标存在
			sharedMetricsKey := metrics.GenerateMetricsIdentityKey(sharedBaseURL, sharedAPIKey, tcServiceType(tc.kind))
			exclusiveMetricsKey := metrics.GenerateMetricsIdentityKey(sharedBaseURL, "sk-exclusive-A", tcServiceType(tc.kind))

			if !hasMetricsKey(metricsManager.GetAllKeyMetrics(), sharedMetricsKey) {
				t.Fatal("共享 metricsKey 应该存在")
			}
			if !hasMetricsKey(metricsManager.GetAllKeyMetrics(), exclusiveMetricsKey) {
				t.Fatal("独占 metricsKey 应该存在")
			}

			// 从配置中移除 channel-A
			var channelAConfig config.UpstreamConfig
			var err error
			switch tc.kind {
			case ChannelKindMessages:
				channelAConfig = cfg.Upstream[0]
				_, err = scheduler.configManager.RemoveUpstream(0)
			case ChannelKindResponses:
				channelAConfig = cfg.ResponsesUpstream[0]
				_, err = scheduler.configManager.RemoveResponsesUpstream(0)
			case ChannelKindGemini:
				channelAConfig = cfg.GeminiUpstream[0]
				_, err = scheduler.configManager.RemoveGeminiUpstream(0)
			}
			if err != nil {
				t.Fatalf("移除渠道失败: %v", err)
			}

			// 调用 DeleteChannelMetrics
			scheduler.DeleteChannelMetrics(&channelAConfig, tc.kind)

			// 验证结果
			// 共享的 metricsKey 应该被保留（因为 channel-B 还在使用）
			if !hasMetricsKey(metricsManager.GetAllKeyMetrics(), sharedMetricsKey) {
				t.Error("共享 metricsKey 应该被保留，但被删除了")
			}

			// 独占的 metricsKey 应该被删除
			if hasMetricsKey(metricsManager.GetAllKeyMetrics(), exclusiveMetricsKey) {
				t.Error("独占 metricsKey 应该被删除，但仍然存在")
			}
		})
	}
}

// TestDeleteChannelMetrics_AllExclusiveKeysDeleted 测试删除渠道时所有独占的 metricsKey 都被删除
func TestDeleteChannelMetrics_AllExclusiveKeysDeleted(t *testing.T) {
	// 场景：渠道有多个独占的 (BaseURL, APIKey) 组合
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "channel-to-delete",
				BaseURL:  "https://exclusive.example.com",
				APIKeys:  []string{"sk-key-1", "sk-key-2"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "other-channel",
				BaseURL:  "https://other.example.com",
				APIKeys:  []string{"sk-other-key"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	metricsManager := scheduler.messagesMetricsManager

	// 为所有 key 记录指标
	metricsManager.RecordSuccess("https://exclusive.example.com", "sk-key-1", "claude")
	metricsManager.RecordSuccess("https://exclusive.example.com", "sk-key-2", "claude")
	metricsManager.RecordSuccess("https://other.example.com", "sk-other-key", "claude")

	// 从配置中移除要删除的渠道
	channelToDelete := cfg.Upstream[0]
	_, err := scheduler.configManager.RemoveUpstream(0)
	if err != nil {
		t.Fatalf("移除渠道失败: %v", err)
	}

	// 调用 DeleteChannelMetrics
	scheduler.DeleteChannelMetrics(&channelToDelete, ChannelKindMessages)

	// 验证结果
	key1 := metrics.GenerateMetricsIdentityKey("https://exclusive.example.com", "sk-key-1", "claude")
	key2 := metrics.GenerateMetricsIdentityKey("https://exclusive.example.com", "sk-key-2", "claude")
	otherKey := metrics.GenerateMetricsIdentityKey("https://other.example.com", "sk-other-key", "claude")

	// 被删除渠道的所有 metricsKey 都应该被删除
	if hasMetricsKey(metricsManager.GetAllKeyMetrics(), key1) {
		t.Error("sk-key-1 的 metricsKey 应该被删除")
	}
	if hasMetricsKey(metricsManager.GetAllKeyMetrics(), key2) {
		t.Error("sk-key-2 的 metricsKey 应该被删除")
	}
	// 其他渠道的 metricsKey 应该保留
	if !hasMetricsKey(metricsManager.GetAllKeyMetrics(), otherKey) {
		t.Error("其他渠道的 metricsKey 应该被保留")
	}
}

// TestDeleteChannelMetrics_SkipsWhenUpstreamStillInConfig 测试前置条件守卫：渠道仍在配置中时跳过删除
func TestDeleteChannelMetrics_SkipsWhenUpstreamStillInConfig(t *testing.T) {
	// 场景：在渠道仍在配置中时调用 DeleteChannelMetrics
	// 应该记录警告但仍然执行（可能结果不正确）
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "channel-still-in-config",
				BaseURL:  "https://example.com",
				APIKeys:  []string{"sk-key"},
				Status:   "active",
				Priority: 1,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	metricsManager := scheduler.messagesMetricsManager
	metricsManager.RecordSuccess("https://example.com", "sk-key", "claude")

	// 不从配置中移除渠道，直接调用 DeleteChannelMetrics
	// 这违反了前置条件，但方法应该仍然执行（只是结果可能不正确）
	channelConfig := cfg.Upstream[0]
	scheduler.DeleteChannelMetrics(&channelConfig, ChannelKindMessages)

	// 由于渠道仍在配置中，collectUsedCombinations 会返回该组合
	// 因此 metricsKey 不会被删除
	metricsKey := metrics.GenerateMetricsIdentityKey("https://example.com", "sk-key", "claude")

	if !hasMetricsKey(metricsManager.GetAllKeyMetrics(), metricsKey) {
		t.Error("由于渠道仍在配置中，metricsKey 应该被保留（前置条件违反时的预期行为）")
	}
}

func TestDeleteChannelMetrics_DeletesOnlyRealServiceTypeIdentity(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:        "channel-to-delete",
				BaseURL:     "https://shared.example.com",
				APIKeys:     []string{"sk-key"},
				ServiceType: "gemini",
				Status:      "active",
				Priority:    1,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	metricsManager := scheduler.messagesMetricsManager
	metricsManager.RecordSuccess("https://shared.example.com", "sk-key", "openai")
	metricsManager.RecordSuccess("https://shared.example.com", "sk-key", "gemini")
	legacyKey := metrics.GenerateMetricsIdentityKey("https://shared.example.com", "sk-key", "openai")
	currentKey := metrics.GenerateMetricsIdentityKey("https://shared.example.com", "sk-key", "gemini")

	if !hasMetricsKey(metricsManager.GetAllKeyMetrics(), legacyKey) {
		t.Fatal("非真实 serviceType 的 metricsKey 应该存在")
	}
	if !hasMetricsKey(metricsManager.GetAllKeyMetrics(), currentKey) {
		t.Fatal("真实 serviceType 的 metricsKey 应该存在")
	}

	channelToDelete := cfg.Upstream[0]
	_, err := scheduler.configManager.RemoveUpstream(0)
	if err != nil {
		t.Fatalf("移除渠道失败: %v", err)
	}

	scheduler.DeleteChannelMetrics(&channelToDelete, ChannelKindMessages)

	if hasMetricsKey(metricsManager.GetAllKeyMetrics(), currentKey) {
		t.Error("真实 serviceType 的 metricsKey 应该被删除")
	}
	if !hasMetricsKey(metricsManager.GetAllKeyMetrics(), legacyKey) {
		t.Error("非真实 serviceType 的 metricsKey 不应被本次删除清理")
	}
}

func TestDeleteChannelMetrics_RemovesEquivalentLegacyMetricsKeys(t *testing.T) {
	serviceType := "claude"
	baseURLs := []string{"https://shared.example.com"}
	apiKeys := []string{"sk-key"}

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "channel-to-delete",
			BaseURL:     baseURLs[0],
			APIKeys:     apiKeys,
			ServiceType: serviceType,
			Status:      "active",
			Priority:    1,
		}},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	metricsManager := scheduler.messagesMetricsManager
	deletedKeys := metricsManager.DeleteKeysForChannel(baseURLs, apiKeys, serviceType)
	identityKey := metrics.GenerateMetricsIdentityKey(baseURLs[0], apiKeys[0], serviceType)
	legacyKey := metrics.GenerateMetricsKey(baseURLs[0], apiKeys[0])

	if !containsString(deletedKeys, identityKey) {
		t.Fatalf("deletedKeys should contain identity key %s", identityKey)
	}
	if !containsString(deletedKeys, legacyKey) {
		t.Fatalf("deletedKeys should contain equivalent legacy key %s", legacyKey)
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// hasMetricsKey 辅助函数：检查 metricsKey 是否存在于指标列表中
func hasMetricsKey(allMetrics []*metrics.KeyMetrics, metricsKey string) bool {
	for _, m := range allMetrics {
		if m.MetricsKey == metricsKey {
			return true
		}
	}
	return false
}

func TestFallbackSkipsRuntimeCooldownChannel(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "unhealthy-channel",
				BaseURL:  "https://unhealthy.example.com",
				APIKeys:  []string{"sk-unhealthy"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "cooldown-channel",
				BaseURL:  "https://cooldown.example.com",
				APIKeys:  []string{"sk-cooldown"},
				Status:   "active",
				Priority: 2,
			},
			{
				Name:     "fallback-channel",
				BaseURL:  "https://fallback.example.com",
				APIKeys:  []string{"sk-fallback"},
				Status:   "active",
				Priority: 3,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	scheduler.MarkChannelCooldown(ChannelKindMessages, 1, time.Minute)

	activeChannels := []ChannelInfo{
		{Index: 1, Name: "cooldown-channel", Priority: 1, Status: "active"},
		{Index: 2, Name: "fallback-channel", Priority: 2, Status: "active"},
	}
	result, err := scheduler.selectFallbackChannel(activeChannels, map[int]bool{}, ChannelKindMessages)
	if err != nil {
		t.Fatalf("fallback 选择失败: %v", err)
	}
	if result.ChannelIndex != 2 {
		t.Fatalf("期望 fallback 跳过 cooldown 渠道并选择 index=2，实际为 index=%d", result.ChannelIndex)
	}
	if result.Reason != "fallback" {
		t.Fatalf("期望选择原因为 fallback，实际为 %s", result.Reason)
	}
}

func TestAffinityYieldToHigherPriorityHealthyChannel(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "high-priority-channel",
				BaseURL:  "https://high.example.com",
				APIKeys:  []string{"sk-high"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "affinity-channel",
				BaseURL:  "https://affinity.example.com",
				APIKeys:  []string{"sk-affinity"},
				Status:   "active",
				Priority: 9,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	scheduler.traceAffinity.SetPreferredChannel(string(ChannelKindMessages)+":test-user", 1)

	result, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 0 {
		t.Fatalf("期望选择更高优先级渠道 index=0，实际为 index=%d", result.ChannelIndex)
	}
	if result.Reason != "priority_order" {
		t.Fatalf("期望选择原因为 priority_order，实际为 %s", result.Reason)
	}
}

func TestAffinityStillWorksWithoutHigherPriorityAlternative(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "affinity-channel",
				BaseURL:  "https://affinity.example.com",
				APIKeys:  []string{"sk-affinity"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "lower-priority-channel",
				BaseURL:  "https://low.example.com",
				APIKeys:  []string{"sk-low"},
				Status:   "active",
				Priority: 9,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	scheduler.traceAffinity.SetPreferredChannel(string(ChannelKindMessages)+":test-user", 0)

	result, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 0 {
		t.Fatalf("期望继续选择亲和渠道 index=0，实际为 index=%d", result.ChannelIndex)
	}
	if result.Reason != "trace_affinity" {
		t.Fatalf("期望选择原因为 trace_affinity，实际为 %s", result.Reason)
	}
}

func TestAffinitySkipsRuntimeCooldownChannel(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "affinity-channel",
				BaseURL:  "https://affinity.example.com",
				APIKeys:  []string{"sk-affinity"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "fallback-channel",
				BaseURL:  "https://fallback.example.com",
				APIKeys:  []string{"sk-fallback"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	scheduler.traceAffinity.SetPreferredChannel(string(ChannelKindMessages)+":test-user", 0)
	scheduler.MarkChannelCooldown(ChannelKindMessages, 0, time.Minute)

	result, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("期望跳过 cooldown 亲和渠道并选择 index=1，实际为 index=%d", result.ChannelIndex)
	}
}

func TestSelectChannelSoftShiftsNewSessionsAtHalfRateLimit(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:                   "primary-channel",
				BaseURL:                "https://primary.example.com",
				APIKeys:                []string{"sk-primary"},
				Status:                 "active",
				Priority:               1,
				RateLimitRPM:           4,
				RateLimitWindowMinutes: 1,
			},
			{
				Name:     "next-channel",
				BaseURL:  "https://next.example.com",
				APIKeys:  []string{"sk-next"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	limiter := scheduler.GetRateLimitManager().GetOrCreate("Messages", 0, ratelimit.Config{
		RPM:           4,
		WindowSeconds: 60,
	})
	now := time.Now()
	for i := 0; i < 2; i++ {
		release, err := limiter.Acquire(context.Background(), time.Millisecond, now)
		if err != nil {
			t.Fatalf("预置限速窗口失败: %v", err)
		}
		release()
	}

	result, err := scheduler.SelectChannel(context.Background(), "new-user", map[int]bool{}, ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("期望新会话在主渠道达到 50%% 后选择 index=1，实际为 index=%d", result.ChannelIndex)
	}
	if result.Reason != "priority_order" {
		t.Fatalf("期望选择原因为 priority_order，实际为 %s", result.Reason)
	}
}

func TestRateLimitSoftSheddingRequiresSustainedLowWater(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "primary-channel",
				BaseURL:  "https://primary.example.com",
				APIKeys:  []string{"sk-primary"},
				Status:   "active",
				Priority: 1,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	limiter := scheduler.GetRateLimitManager().GetOrCreate("Messages", 0, ratelimit.Config{
		MaxConcurrent: 10,
	})
	base := time.Now()
	releases := make([]func(), 0, 5)
	for i := 0; i < 5; i++ {
		release, err := limiter.Acquire(context.Background(), time.Millisecond, base)
		if err != nil {
			t.Fatalf("预置并发水位失败: %v", err)
		}
		releases = append(releases, release)
	}

	deferred, ratio, _ := scheduler.ShouldDeferForRateLimit(ChannelKindMessages, 0, "", ratelimit.Config{}, base)
	if !deferred || ratio < rateLimitLoadShedHighWatermark {
		t.Fatalf("期望 50%% 水位进入软分流，deferred=%v ratio=%.2f", deferred, ratio)
	}

	for _, release := range releases {
		release()
	}

	deferred, ratio, _ = scheduler.ShouldDeferForRateLimit(ChannelKindMessages, 0, "", ratelimit.Config{}, base.Add(time.Second))
	if !deferred || ratio >= rateLimitLoadShedLowWatermark {
		t.Fatalf("期望低水位未满 5 分钟仍保持软分流，deferred=%v ratio=%.2f", deferred, ratio)
	}

	deferred, ratio, _ = scheduler.ShouldDeferForRateLimit(ChannelKindMessages, 0, "", ratelimit.Config{}, base.Add(5*time.Minute+2*time.Second))
	if deferred || ratio >= rateLimitLoadShedLowWatermark {
		t.Fatalf("期望低水位持续 5 分钟后恢复，deferred=%v ratio=%.2f", deferred, ratio)
	}
}

func TestTextRequestReservesVisionChannelDuringSoftShedding(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:         "deepseek-text",
				BaseURL:      "https://deepseek.example.com",
				APIKeys:      []string{"sk-text"},
				Status:       "active",
				Priority:     1,
				NoVision:     true,
				RateLimitRPM: 10,
			},
			{
				Name:     "minimax-vision",
				BaseURL:  "https://minimax.example.com",
				APIKeys:  []string{"sk-vision"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	limiter := scheduler.GetRateLimitManager().GetOrCreate("Messages", 0, ratelimit.Config{
		RPM:           10,
		WindowSeconds: 60,
	})
	now := time.Now()
	for i := 0; i < 5; i++ {
		release, err := limiter.Acquire(context.Background(), time.Millisecond, now)
		if err != nil {
			t.Fatalf("预置限速窗口失败: %v", err)
		}
		release()
	}

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:          "text-user",
		FailedChannels:  map[int]bool{},
		Kind:            ChannelKindMessages,
		HasImageContent: false,
	})
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 0 {
		t.Fatalf("期望文本请求在 50%% 水位继续使用文本渠道 index=0，实际为 index=%d", result.ChannelIndex)
	}
	if result.Reason != "rate_limit_pressure" {
		t.Fatalf("期望选择原因为 rate_limit_pressure，实际为 %s", result.Reason)
	}
}

func TestTextRequestUsesVisionChannelAfterReserveWatermark(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:         "deepseek-text",
				BaseURL:      "https://deepseek.example.com",
				APIKeys:      []string{"sk-text"},
				Status:       "active",
				Priority:     1,
				NoVision:     true,
				RateLimitRPM: 10,
			},
			{
				Name:     "minimax-vision",
				BaseURL:  "https://minimax.example.com",
				APIKeys:  []string{"sk-vision"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	limiter := scheduler.GetRateLimitManager().GetOrCreate("Messages", 0, ratelimit.Config{
		RPM:           10,
		WindowSeconds: 60,
	})
	now := time.Now()
	for i := 0; i < 8; i++ {
		release, err := limiter.Acquire(context.Background(), time.Millisecond, now)
		if err != nil {
			t.Fatalf("预置限速窗口失败: %v", err)
		}
		release()
	}

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:          "text-user",
		FailedChannels:  map[int]bool{},
		Kind:            ChannelKindMessages,
		HasImageContent: false,
	})
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("期望文本渠道达到 80%% 后可使用视觉渠道 index=1，实际为 index=%d", result.ChannelIndex)
	}
	if result.Reason != "priority_order" {
		t.Fatalf("期望选择原因为 priority_order，实际为 %s", result.Reason)
	}
}

func TestImageRequestCanUseVisionChannelDuringSoftShedding(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:         "deepseek-text",
				BaseURL:      "https://deepseek.example.com",
				APIKeys:      []string{"sk-text"},
				Status:       "active",
				Priority:     1,
				NoVision:     true,
				RateLimitRPM: 10,
			},
			{
				Name:     "minimax-vision",
				BaseURL:  "https://minimax.example.com",
				APIKeys:  []string{"sk-vision"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	limiter := scheduler.GetRateLimitManager().GetOrCreate("Messages", 0, ratelimit.Config{
		RPM:           10,
		WindowSeconds: 60,
	})
	now := time.Now()
	for i := 0; i < 5; i++ {
		release, err := limiter.Acquire(context.Background(), time.Millisecond, now)
		if err != nil {
			t.Fatalf("预置限速窗口失败: %v", err)
		}
		release()
	}

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:          "vision-user",
		FailedChannels:  map[int]bool{},
		Kind:            ChannelKindMessages,
		HasImageContent: true,
	})
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("期望含图请求可使用视觉渠道 index=1，实际为 index=%d", result.ChannelIndex)
	}
	if result.Reason != "priority_order" {
		t.Fatalf("期望选择原因为 priority_order，实际为 %s", result.Reason)
	}
}
