package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/warmup"
)

// createTestConfigManager 创建测试用配置管理器
func createTestConfigManager(t *testing.T, cfg config.Config) (*config.ConfigManager, func()) {
	t.Helper()

	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "scheduler-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}

	// 创建临时配置文件
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("序列化配置失败: %v", err)
	}

	if err := os.WriteFile(configFile, data, 0644); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("写入配置文件失败: %v", err)
	}

	// 创建配置管理器
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("创建配置管理器失败: %v", err)
	}

	cleanup := func() {
		_ = cfgManager.Close()
		_ = os.RemoveAll(tmpDir)
	}

	return cfgManager, cleanup
}

// createTestScheduler 创建测试用调度器
func createTestScheduler(t *testing.T, cfg config.Config) (*ChannelScheduler, func()) {
	t.Helper()

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(30*time.Second, 3)

	scheduler := NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity, urlManager)
	scheduler.SetRateLimitManager(ratelimit.NewManager())

	return scheduler, func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		chatMetrics.Stop()
		geminiMetrics.Stop()
		imagesMetrics.Stop()
		cleanup()
	}
}

func TestSelectChannelDryRunDoesNotRecordLastSelectedChannel(t *testing.T) {
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

	if got := scheduler.GetCurrentChannelIndex(ChannelKindMessages); got != 0 {
		t.Fatalf("初始 current channel = %d, want 0", got)
	}

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind:           ChannelKindMessages,
		FailedChannels: map[int]bool{0: true},
		DryRun:         true,
	})
	if err != nil {
		t.Fatalf("dry-run 选择失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("dry-run result channel = %d, want 1", result.ChannelIndex)
	}
	if got := scheduler.GetCurrentChannelIndex(ChannelKindMessages); got != 0 {
		t.Fatalf("dry-run 不应更新 current channel，got %d want 0", got)
	}
}

func TestSelectChannelTraceRecordsActiveModelFilterSkips(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:            "unsupported",
				BaseURL:         "https://unsupported.example.com",
				APIKeys:         []string{"sk-unsupported"},
				Status:          "active",
				Priority:        1,
				SupportedModels: []string{"claude-*"},
			},
			{
				Name:            "disabled",
				BaseURL:         "https://disabled.example.com",
				APIKeys:         []string{"sk-disabled"},
				Status:          "disabled",
				Priority:        2,
				SupportedModels: []string{"gpt-*"},
			},
			{
				Name:            "selected",
				BaseURL:         "https://selected.example.com",
				APIKeys:         []string{"sk-selected"},
				Status:          "active",
				Priority:        3,
				SupportedModels: []string{"gpt-*"},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind:  ChannelKindMessages,
		Model: "gpt-test",
	})
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 2 {
		t.Fatalf("result channel = %d, want 2", result.ChannelIndex)
	}

	skips := result.Trace.Candidates
	if len(skips) != 2 {
		t.Fatalf("skips len = %d, want 2: %#v", len(skips), skips)
	}
	if skips[0].Reason != "unsupported_model" || skips[0].ChannelName != "unsupported" {
		t.Fatalf("first skip = %#v, want unsupported_model for unsupported", skips[0])
	}
	if skips[1].Reason != "disabled_status" || skips[1].ChannelName != "disabled" {
		t.Fatalf("second skip = %#v, want disabled_status for disabled", skips[1])
	}
}

func TestSelectChannelFiltersUnavailableKeysBeforeCandidateFilter(t *testing.T) {
	disabled := false
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "persistently-disabled",
				BaseURL:  "https://disabled.example.com",
				APIKeys:  []string{"sk-disabled"},
				Status:   "active",
				Priority: 1,
				DisabledAPIKeys: []config.DisabledKeyInfo{
					{Key: "sk-disabled", RecoverAt: time.Now().Add(time.Hour).Format(time.RFC3339)},
				},
			},
			{
				Name:     "config-disabled",
				BaseURL:  "https://config-disabled.example.com",
				APIKeys:  []string{"sk-config-disabled"},
				Status:   "active",
				Priority: 2,
				APIKeyConfigs: []config.APIKeyConfig{
					{Key: "sk-config-disabled", Enabled: &disabled},
				},
			},
			{
				Name:     "selected",
				BaseURL:  "https://selected.example.com",
				APIKeys:  []string{"sk-selected"},
				Status:   "active",
				Priority: 3,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	candidateCount := -1
	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindMessages,
		CandidateFilter: func(
			channels []ChannelInfo,
			upstreamFor func(ChannelInfo) *config.UpstreamConfig,
			candidateAvailable func(ChannelInfo, *config.UpstreamConfig) bool,
		) ([]ChannelInfo, error) {
			candidateCount = len(channels)
			return channels, nil
		},
	})
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 2 {
		t.Fatalf("result channel = %d, want 2", result.ChannelIndex)
	}
	if candidateCount != 1 {
		t.Fatalf("CandidateFilter 收到 %d 个渠道，want 1", candidateCount)
	}

	keySkips := 0
	for _, skipped := range result.Trace.Candidates {
		if skipped.Stage == "key_availability_filter" && skipped.Reason == "no_selectable_keys" {
			keySkips++
		}
	}
	if keySkips != 2 {
		t.Fatalf("key availability skips = %d, want 2: %#v", keySkips, result.Trace.Candidates)
	}
	stageCount := -1
	for _, stage := range result.Trace.Stages {
		if stage.Name == "key_availability_filter" {
			stageCount = stage.Count
			break
		}
	}
	if stageCount != 1 {
		t.Fatalf("key_availability_filter count = %d, want 1", stageCount)
	}
}

func TestSelectChannelAllowsExpiredDisabledKeyForRecovery(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "recovery-due",
				BaseURL:  "https://recovery.example.com",
				APIKeys:  []string{"sk-recovery"},
				Status:   "active",
				Priority: 1,
				DisabledAPIKeys: []config.DisabledKeyInfo{
					{Key: "sk-recovery", RecoverAt: time.Now().Add(-time.Minute).Format(time.RFC3339)},
				},
			},
			{
				Name:     "fallback",
				BaseURL:  "https://fallback.example.com",
				APIKeys:  []string{"sk-fallback"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannel(context.Background(), "", nil, ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 0 {
		t.Fatalf("到期禁用 Key 应允许恢复探测，selected=%d want=0", result.ChannelIndex)
	}
}

func TestSelectChannelTraceRecordsCandidateFilterSkips(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "kept",
				BaseURL:  "https://kept.example.com",
				APIKeys:  []string{"sk-kept"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "filtered",
				BaseURL:  "https://filtered.example.com",
				APIKeys:  []string{"sk-filtered"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindMessages,
		CandidateFilter: func(
			channels []ChannelInfo,
			upstreamFor func(ChannelInfo) *config.UpstreamConfig,
			candidateAvailable func(ChannelInfo, *config.UpstreamConfig) bool,
		) ([]ChannelInfo, error) {
			return channels[:1], nil
		},
	})
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 0 {
		t.Fatalf("result channel = %d, want 0", result.ChannelIndex)
	}

	skips := result.Trace.Candidates
	if len(skips) != 1 {
		t.Fatalf("skips len = %d, want 1: %#v", len(skips), skips)
	}
	if skips[0].Stage != "candidate_filter" || skips[0].Reason != "filtered_out" || skips[0].ChannelName != "filtered" {
		t.Fatalf("skip = %#v, want candidate_filter filtered_out for filtered", skips[0])
	}
}

// TestPromotedChannelBypassesHealthCheck 测试促销渠道绕过健康检查
func TestPromotedChannelBypassesHealthCheck(t *testing.T) {
	// 设置促销截止时间为 5 分钟后
	promotionUntil := time.Now().Add(5 * time.Minute)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "normal-channel",
				BaseURL:  "https://normal.example.com",
				APIKeys:  []string{"sk-normal-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-channel",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promoted-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 模拟促销渠道之前有高失败率（使其不健康）
	metricsManager := scheduler.messagesMetricsManager
	for i := 0; i < 10; i++ {
		metricsManager.RecordFailure("https://promoted.example.com", "sk-promoted-key", "claude")
	}

	// 验证促销渠道确实不健康
	isHealthy := metricsManager.IsChannelHealthyWithKeys("https://promoted.example.com", []string{"sk-promoted-key"}, "claude")
	if isHealthy {
		t.Fatal("促销渠道应该被标记为不健康")
	}

	// 选择渠道 - 促销渠道应该被选中，即使它不健康
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 1 {
		t.Errorf("期望选择促销渠道 (index=1)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Reason != "promotion_priority" {
		t.Errorf("期望选择原因为 promotion_priority，实际为 %s", result.Reason)
	}

	if result.Upstream.Name != "promoted-channel" {
		t.Errorf("期望选择 promoted-channel，实际选择了 %s", result.Upstream.Name)
	}

	if got := scheduler.GetCurrentChannelIndex(ChannelKindMessages); got != 1 {
		t.Errorf("运行态当前渠道 = %d, want 1", got)
	}
}

// TestPromotedChannelSkippedAfterFailure 测试促销渠道在本次请求失败后被跳过
func TestPromotedChannelSkippedAfterFailure(t *testing.T) {
	promotionUntil := time.Now().Add(5 * time.Minute)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "normal-channel",
				BaseURL:  "https://normal.example.com",
				APIKeys:  []string{"sk-normal-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-channel",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promoted-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 模拟促销渠道在本次请求中已经失败
	failedChannels := map[int]bool{
		1: true, // 促销渠道已失败
	}

	// 选择渠道 - 应该跳过促销渠道，选择正常渠道
	result, err := scheduler.SelectChannel(context.Background(), "test-user", failedChannels, ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 0 {
		t.Errorf("期望选择正常渠道 (index=0)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Upstream.Name != "normal-channel" {
		t.Errorf("期望选择 normal-channel，实际选择了 %s", result.Upstream.Name)
	}

	if got := scheduler.GetCurrentChannelIndex(ChannelKindMessages); got != 0 {
		t.Errorf("运行态当前渠道 = %d, want 0", got)
	}
}

func TestPromotedChannelSkippedInRuntimeCooldown(t *testing.T) {
	promotionUntil := time.Now().Add(5 * time.Minute)
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "normal-channel",
				BaseURL:  "https://normal.example.com",
				APIKeys:  []string{"sk-normal-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-channel",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promoted-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	scheduler.MarkChannelCooldown(ChannelKindMessages, 1, time.Minute)

	result, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 0 {
		t.Fatalf("期望跳过 cooldown 促销渠道并选择 index=0，实际为 index=%d", result.ChannelIndex)
	}
	if result.Reason != "priority_order" {
		t.Fatalf("期望选择原因为 priority_order，实际为 %s", result.Reason)
	}
}

// TestNonPromotedChannelStillChecksHealth 测试非促销渠道仍然检查健康状态
func TestNonPromotedChannelStillChecksHealth(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "unhealthy-channel",
				BaseURL:  "https://unhealthy.example.com",
				APIKeys:  []string{"sk-unhealthy-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "healthy-channel",
				BaseURL:  "https://healthy.example.com",
				APIKeys:  []string{"sk-healthy-key"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 模拟第一个渠道不健康
	metricsManager := scheduler.messagesMetricsManager
	for i := 0; i < 10; i++ {
		metricsManager.RecordFailure("https://unhealthy.example.com", "sk-unhealthy-key", "claude")
	}

	// 选择渠道 - 应该跳过不健康的渠道，选择健康的渠道
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 1 {
		t.Errorf("期望选择健康渠道 (index=1)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Upstream.Name != "healthy-channel" {
		t.Errorf("期望选择 healthy-channel，实际选择了 %s", result.Upstream.Name)
	}
}

func TestSelectChannelTraceRecordsSelectionAndSkips(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "suspended-channel",
				BaseURL:  "https://suspended.example.com",
				APIKeys:  []string{"sk-suspended-key"},
				Status:   "suspended",
				Priority: 1,
			},
			{
				Name:     "selected-channel",
				BaseURL:  "https://selected.example.com",
				APIKeys:  []string{"sk-selected-key"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if result.Trace == nil {
		t.Fatal("SelectionResult.Trace = nil, want trace")
	}
	if result.Trace.Kind != ChannelKindMessages {
		t.Fatalf("trace kind = %s, want %s", result.Trace.Kind, ChannelKindMessages)
	}
	if result.Trace.Selected == nil {
		t.Fatal("trace selected = nil")
	}
	if result.Trace.Selected.ChannelIndex != 1 || result.Trace.Selected.ChannelName != "selected-channel" || result.Trace.Selected.Reason != "priority_order" {
		t.Fatalf("trace selected = %+v, want selected-channel priority_order", result.Trace.Selected)
	}
	if !traceHasStage(result.Trace, "active_model_filter", 2) {
		t.Fatalf("trace stages = %+v, want active_model_filter count 2", result.Trace.Stages)
	}
	if !traceHasCandidateSkip(result.Trace, 0, "priority_order", "inactive_status") {
		t.Fatalf("trace candidates = %+v, want inactive_status skip for channel 0", result.Trace.Candidates)
	}
}

func traceHasStage(trace *SelectionTrace, name string, count int) bool {
	if trace == nil {
		return false
	}
	for _, stage := range trace.Stages {
		if stage.Name == name && stage.Count == count {
			return true
		}
	}
	return false
}

func traceHasCandidateSkip(trace *SelectionTrace, channelIndex int, stage, reason string) bool {
	if trace == nil {
		return false
	}
	for _, candidate := range trace.Candidates {
		if candidate.ChannelIndex == channelIndex && candidate.Stage == stage && candidate.Reason == reason {
			return true
		}
	}
	return false
}

func TestFormatSelectionTraceSummaryLimitsSkips(t *testing.T) {
	trace := &SelectionTrace{
		Kind: ChannelKindMessages,
		Stages: []SelectionTraceStage{
			{Name: "active_model_filter", Count: 3},
			{Name: "context_filter", Count: 2},
		},
		Candidates: []SelectionTraceCandidate{
			{ChannelIndex: 0, ChannelName: "a", Stage: "priority_order", Reason: "inactive_status"},
			{ChannelIndex: 1, ChannelName: "b", Stage: "priority_order", Reason: "runtime_cooldown"},
			{ChannelIndex: 2, ChannelName: "c", Stage: "priority_order", Reason: "rate_limit_pressure"},
		},
		Selected: &SelectionTraceSelection{ChannelIndex: 3, ChannelName: "d", Reason: "fallback"},
	}

	summary := FormatSelectionTraceSummary(trace, 2)
	for _, want := range []string{
		"stages=active_model_filter:3,context_filter:2",
		"skipped=0:a@priority_order/inactive_status,1:b@priority_order/runtime_cooldown,+1",
		"selected=3:d/fallback",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary = %q, want contains %q", summary, want)
		}
	}
}

// TestExpiredPromotionNotBypassHealthCheck 测试过期的促销不绕过健康检查
func TestExpiredPromotionNotBypassHealthCheck(t *testing.T) {
	// 设置促销截止时间为过去
	promotionUntil := time.Now().Add(-5 * time.Minute)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "healthy-channel",
				BaseURL:  "https://healthy.example.com",
				APIKeys:  []string{"sk-healthy-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "expired-promoted-channel",
				BaseURL:        "https://expired.example.com",
				APIKeys:        []string{"sk-expired-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil, // 已过期
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 模拟过期促销渠道不健康
	metricsManager := scheduler.messagesMetricsManager
	for i := 0; i < 10; i++ {
		metricsManager.RecordFailure("https://expired.example.com", "sk-expired-key", "claude")
	}

	// 选择渠道 - 过期促销渠道不应该被优先选择，应该选择健康的渠道
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 0 {
		t.Errorf("期望选择健康渠道 (index=0)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Upstream.Name != "healthy-channel" {
		t.Errorf("期望选择 healthy-channel，实际选择了 %s", result.Upstream.Name)
	}
}

func TestSelectChannel_DefaultRouteRejectsPrefixedOnlyChannels(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:        "kimi-only",
				BaseURL:     "https://kimi.example.com",
				APIKeys:     []string{"sk-kimi"},
				Status:      "active",
				Priority:    1,
				RoutePrefix: "kimi",
			},
			{
				Name:        "deepseek-only",
				BaseURL:     "https://deepseek.example.com",
				APIKeys:     []string{"sk-deepseek"},
				Status:      "active",
				Priority:    2,
				RoutePrefix: "deepseek",
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	_, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindMessages, "", "", "")
	if err == nil {
		t.Fatal("SelectChannel() error = nil, want default route rejection")
	}
}

// TestDeleteChannelMetrics_SharedMetricsKeyPreserved 测试删除渠道时共享的 metricsKey 被保留

func TestSelectChannelFiltersSupportedModels(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:            "image-excluded",
				BaseURL:         "https://excluded.example.com",
				APIKeys:         []string{"sk-excluded"},
				Status:          "active",
				Priority:        1,
				SupportedModels: []string{"gpt-4*", "!*image*"},
			},
			{
				Name:            "image-allowed",
				BaseURL:         "https://allowed.example.com",
				APIKeys:         []string{"sk-allowed"},
				Status:          "active",
				Priority:        2,
				SupportedModels: []string{"gpt-4*"},
			},
			{
				Name:            "invalid-pattern-fallback",
				BaseURL:         "https://invalid.example.com",
				APIKeys:         []string{"sk-invalid"},
				Status:          "active",
				Priority:        3,
				SupportedModels: []string{"foo*bar", "claude-*"},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	t.Run("命中排除规则时跳过高优先级渠道", func(t *testing.T) {
		result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "gpt-4-image-preview", "", "")
		if err != nil {
			t.Fatalf("选择渠道失败: %v", err)
		}
		if result.ChannelIndex != 1 {
			t.Fatalf("期望选择 index=1，实际为 %d", result.ChannelIndex)
		}
	})

	t.Run("模型过滤跳过渠道时记录命中排除规则", func(t *testing.T) {
		var buf bytes.Buffer
		oldOutput := log.Writer()
		log.SetOutput(&buf)
		defer log.SetOutput(oldOutput)

		result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "gpt-4-image-preview", "", "")
		if err != nil {
			t.Fatalf("选择渠道失败: %v", err)
		}
		if result.ChannelIndex != 1 {
			t.Fatalf("期望选择 index=1，实际为 %d", result.ChannelIndex)
		}

		logOutput := buf.String()
		if !strings.Contains(logOutput, "[Scheduler-ModelFilter] 跳过渠道 [0] image-excluded") {
			t.Fatalf("期望记录模型过滤跳过日志，实际日志: %s", logOutput)
		}
		if !strings.Contains(logOutput, "模型 \"gpt-4-image-preview\" 不被 supportedModels 支持 (命中排除规则 !*image*)") {
			t.Fatalf("期望记录命中的排除规则，实际日志: %s", logOutput)
		}
	})

	t.Run("非法规则被跳过且不影响合法规则", func(t *testing.T) {
		result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "claude-3-7-sonnet", "", "")
		if err != nil {
			t.Fatalf("选择渠道失败: %v", err)
		}
		if result.ChannelIndex != 2 {
			t.Fatalf("期望选择 index=2，实际为 %d", result.ChannelIndex)
		}
	})

	t.Run("所有活跃渠道都不支持模型时返回明确错误", func(t *testing.T) {
		_, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "gemini-2.5-pro", "", "")
		if err == nil {
			t.Fatal("期望返回错误，实际为 nil")
		}
		if err.Error() != "没有 Messages 渠道支持模型 \"gemini-2.5-pro\"，请检查渠道的 supportedModels 配置" {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestSelectChannelTraceAffinityStillRespectsSupportedModels(t *testing.T) {
	cfg := config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:            "LocalHostClaude",
				AccountUID:      "shared-account",
				BaseURL:         "http://127.0.0.1:3699",
				APIKeys:         []string{"sk-local"},
				Status:          "active",
				Priority:        1,
				SupportedModels: []string{"claude-*"},
				ServiceType:     "responses",
			},
			{
				Name:            "LocalHostOpenAIChat",
				AccountUID:      "shared-account",
				BaseURL:         "http://127.0.0.1:3699",
				APIKeys:         []string{"sk-local"},
				Status:          "active",
				Priority:        2,
				SupportedModels: []string{"gpt-5.5", "gpt-5.4"},
				ServiceType:     "responses",
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	scheduler.traceAffinity.SetPreferredChannel(string(ChannelKindResponses)+":test-user", 0)

	result, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindResponses, "gpt-5.5", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 1 {
		t.Fatalf("期望跳过不支持模型的亲和渠道并选择 index=1，实际为 index=%d", result.ChannelIndex)
	}
	if result.Reason != "priority_order" {
		t.Fatalf("期望回退到 priority_order，实际为 %s", result.Reason)
	}
}

func tcServiceType(kind ChannelKind) string {
	switch kind {
	case ChannelKindGemini:
		return "gemini"
	case ChannelKindResponses:
		return "responses"
	case ChannelKindChat:
		return "openai"
	default:
		return "claude"
	}
}

func TestNormalizedMetricsServiceType(t *testing.T) {
	tests := []struct {
		name       string
		kind       ChannelKind
		configured string
		want       string
	}{
		{name: "messages default", kind: ChannelKindMessages, want: "claude"},
		{name: "responses default", kind: ChannelKindResponses, want: "responses"},
		{name: "gemini default", kind: ChannelKindGemini, want: "gemini"},
		{name: "chat default", kind: ChannelKindChat, want: "openai"},
		{name: "configured wins", kind: ChannelKindChat, configured: "responses", want: "responses"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizedMetricsServiceType(tt.kind, tt.configured); got != tt.want {
				t.Fatalf("NormalizedMetricsServiceType(%q, %q)=%q, want=%q", tt.kind, tt.configured, got, tt.want)
			}
		})
	}
}

func TestSelectChannelByName(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "channel-a",
				BaseURL:  "https://a.example.com",
				APIKeys:  []string{"sk-a"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "channel-b",
				BaseURL:  "https://b.example.com",
				APIKeys:  []string{"sk-b"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	t.Run("指定渠道名直接定位", func(t *testing.T) {
		result, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindMessages, "", "", "channel-b")
		if err != nil {
			t.Fatalf("选择渠道失败: %v", err)
		}
		if result.ChannelIndex != 1 {
			t.Fatalf("期望选择 index=1 (channel-b)，实际为 %d", result.ChannelIndex)
		}
		if result.Reason != "channel_pin" {
			t.Fatalf("期望原因为 channel_pin，实际为 %s", result.Reason)
		}
		if result.Upstream.Name != "channel-b" {
			t.Fatalf("期望选择 channel-b，实际为 %s", result.Upstream.Name)
		}
	})

	t.Run("指定渠道名跳过更高优先级渠道", func(t *testing.T) {
		result, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindMessages, "", "", "channel-b")
		if err != nil {
			t.Fatalf("选择渠道失败: %v", err)
		}
		if result.ChannelIndex != 1 {
			t.Fatalf("应跳过高优先级 channel-a，实际选择 index=%d", result.ChannelIndex)
		}
	})

	t.Run("指定不存在的渠道名返回错误", func(t *testing.T) {
		_, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindMessages, "", "", "nonexistent")
		if err == nil {
			t.Fatal("期望返回错误，实际为 nil")
		}
		if !strings.Contains(err.Error(), "nonexistent") {
			t.Fatalf("错误信息应包含渠道名，实际: %v", err)
		}
	})

	t.Run("指定已失败的渠道名返回错误", func(t *testing.T) {
		failed := map[int]bool{1: true}
		_, err := scheduler.SelectChannel(context.Background(), "test-user", failed, ChannelKindMessages, "", "", "channel-b")
		if err == nil {
			t.Fatal("期望返回错误，实际为 nil")
		}
		if !strings.Contains(err.Error(), "已失败") {
			t.Fatalf("错误信息应提示已失败，实际: %v", err)
		}
	})

	t.Run("空渠道名走正常选择逻辑", func(t *testing.T) {
		result, err := scheduler.SelectChannel(context.Background(), "test-user", map[int]bool{}, ChannelKindMessages, "", "", "")
		if err != nil {
			t.Fatalf("选择渠道失败: %v", err)
		}
		if result.ChannelIndex != 0 {
			t.Fatalf("正常逻辑应选高优先级 index=0，实际为 %d", result.ChannelIndex)
		}
		if result.Reason == "channel_pin" {
			t.Fatal("空渠道名不应触发 channel_pin")
		}
	})
}

// TestModelSupportResolver_NilFallsBackToExplainModelSupport 验证 resolver 为 nil 时
// 行为与注册前完全一致（回归 pin）。
func TestModelSupportResolver_NilFallsBackToExplainModelSupport(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:            "claude-only",
				BaseURL:         "https://claude.example.com",
				APIKeys:         []string{"sk-1"},
				Status:          "active",
				Priority:        1,
				SupportedModels: []string{"claude-*"},
			},
			{
				Name:            "gpt-only",
				BaseURL:         "https://gpt.example.com",
				APIKeys:         []string{"sk-2"},
				Status:          "active",
				Priority:        2,
				SupportedModels: []string{"gpt-*"},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	// 不注册 resolver，应与原有行为完全一致

	// claude-3-opus → 只匹配 claude-only (index=0)
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "claude-3-opus", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 0 {
		t.Fatalf("期望选择 index=0 (claude-only)，实际为 %d", result.ChannelIndex)
	}

	// gpt-4o → 只匹配 gpt-only (index=1)
	result, err = scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "gpt-4o", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("期望选择 index=1 (gpt-only)，实际为 %d", result.ChannelIndex)
	}

	// gemini-pro → 无渠道支持，应报错
	_, err = scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "gemini-pro", "", "")
	if err == nil {
		t.Fatal("期望返回错误，实际为 nil")
	}
}

// TestModelSupportResolver_ResolverOverridesExplainModelSupport 验证 resolver 返回
// supported=true 时，即使 ExplainModelSupport 会拒绝该模型，渠道仍被纳入候选。
func TestModelSupportResolver_ResolverOverridesExplainModelSupport(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:            "strict-claude",
				BaseURL:         "https://strict.example.com",
				APIKeys:         []string{"sk-1"},
				Status:          "active",
				Priority:        1,
				SupportedModels: []string{"claude-*"}, // ExplainModelSupport 会拒绝 gpt-4o
			},
			{
				Name:     "fallback",
				BaseURL:  "https://fallback.example.com",
				APIKeys:  []string{"sk-2"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 注册 resolver：对 strict-claude 始终返回 supported=true
	scheduler.SetModelSupportResolverProvider(func(_ context.Context, kind ChannelKind, upstream *config.UpstreamConfig, model string) (bool, string, string, string) {
		if upstream.Name == "strict-claude" {
			return true, "mapped-model", "auto_resolve", ""
		}
		return false, "", "", "not handled by resolver"
	})

	// gpt-4o 本来会被 ExplainModelSupport 排除 strict-claude，但 resolver 覆盖了
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "gpt-4o", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 0 {
		t.Fatalf("resolver 应覆盖 ExplainModelSupport，期望选择 index=0 (strict-claude)，实际为 %d", result.ChannelIndex)
	}
}

// TestModelSupportResolver_ResolverFalseFallsBackToExplainModelSupport 验证 resolver
// 返回 supported=false 时，回退到 ExplainModelSupport（不自动排除渠道）。
func TestModelSupportResolver_ResolverFalseFallsBackToExplainModelSupport(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "open-channel",
				BaseURL:  "https://open.example.com",
				APIKeys:  []string{"sk-1"},
				Status:   "active",
				Priority: 1,
				// 无 SupportedModels 限制 → ExplainModelSupport 始终返回 true
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// resolver 对所有渠道返回 supported=false
	scheduler.SetModelSupportResolverProvider(func(_ context.Context, kind ChannelKind, upstream *config.UpstreamConfig, model string) (bool, string, string, string) {
		return false, "", "", "resolver says no"
	})

	// resolver 说不支持，但 ExplainModelSupport 无限制 → 渠道仍被纳入候选
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "claude-3-opus", "", "")
	if err != nil {
		t.Fatalf("期望 resolver=false 后回退到 ExplainModelSupport 并成功选择，实际错误: %v", err)
	}
	if result.ChannelIndex != 0 {
		t.Fatalf("期望选择 index=0 (open-channel)，实际为 %d", result.ChannelIndex)
	}
}

func TestModelSupportResolver_AuthoritativeDenyDoesNotFallBack(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:        "auto-profile-mismatch",
				BaseURL:     "https://auto.example.com",
				APIKeys:     []string{"sk-1"},
				Status:      "active",
				Priority:    1,
				AutoManaged: true,
				// 空 SupportedModels 的兼容语义原本会把该渠道重新放回候选。
			},
			{
				Name:            "exact-model-channel",
				BaseURL:         "https://exact.example.com",
				APIKeys:         []string{"sk-2"},
				Status:          "active",
				Priority:        2,
				SupportedModels: []string{"deepseek-chat"},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	scheduler.SetModelSupportResolverProvider(func(_ context.Context, kind ChannelKind, upstream *config.UpstreamConfig, model string) (bool, string, string, string) {
		if upstream.Name == "auto-profile-mismatch" {
			return false, "", ModelSupportSourceAuthoritativeDeny, "exact_model_required"
		}
		return false, "", "", "not handled by resolver"
	})

	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), ChannelKindMessages, "deepseek-chat", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("权威拒绝不应回退到空 SupportedModels，选择 index=%d, want 1", result.ChannelIndex)
	}
}

func TestModelSupportResolverReceivesRequestContext(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:        "request-incompatible",
				BaseURL:     "https://incompatible.example.com",
				APIKeys:     []string{"sk-1"},
				Status:      "active",
				Priority:    1,
				AutoManaged: true,
			},
			{
				Name:            "request-compatible",
				BaseURL:         "https://compatible.example.com",
				APIKeys:         []string{"sk-2"},
				Status:          "active",
				Priority:        2,
				SupportedModels: []string{"gpt-5.6-sol"},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	type requestContextKey struct{}
	ctx := context.WithValue(context.Background(), requestContextKey{}, "premium")
	scheduler.SetModelSupportResolverProvider(func(gotCtx context.Context, _ ChannelKind, upstream *config.UpstreamConfig, _ string) (bool, string, string, string) {
		if upstream.Name == "request-incompatible" && gotCtx.Value(requestContextKey{}) == "premium" {
			return false, "", ModelSupportSourceAuthoritativeDeny, "no_capable_model"
		}
		return false, "", "", "not handled by resolver"
	})

	result, err := scheduler.SelectChannel(ctx, "test-user", nil, ChannelKindMessages, "gpt-5.6-sol", "", "")
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("请求级能力拒绝应在首次选渠前生效，选择 index=%d, want 1", result.ChannelIndex)
	}
}

// TestModelCircuitFilterSkipsChannel 验证渠道-模型级熔断把故障渠道从候选中剔除，
// 并且同渠道的其他模型不受影响——复现"某渠道 sonnet-5 持续 403 但 opus-5 可用"。
func TestModelCircuitFilterSkipsChannel(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:       "broken-channel",
				ChannelUID: "ch_broken",
				BaseURL:    "https://broken.example.com",
				APIKeys:    []string{"sk-broken"},
				Status:     "active",
				Priority:   1,
			},
			{
				Name:       "backup-channel",
				ChannelUID: "ch_backup",
				BaseURL:    "https://backup.example.com",
				APIKeys:    []string{"sk-backup"},
				Status:     "active",
				Priority:   2,
			},
		},
	}

	sched, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	const brokenModel = "claude-sonnet-5"
	tracker := sched.messagesMetricsManager.ModelCircuit()
	keyHash := metrics.ModelCircuitKeyHash("sk-broken")

	// 5 分钟内 2 次失败 → 触发快速通道熔断。
	tracker.RecordModelFailure("ch_broken", keyHash, brokenModel, "HTTP 403")
	if opened, _ := tracker.RecordModelFailure("ch_broken", keyHash, brokenModel, "HTTP 403"); !opened {
		t.Fatal("连续 2 次失败应触发熔断")
	}

	// 熔断模型应绕过高优先级的故障渠道。
	result, err := sched.SelectChannel(context.Background(), "", map[int]bool{}, ChannelKindMessages, brokenModel, "", "")
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("熔断渠道应被跳过，期望 index=1, got %d", result.ChannelIndex)
	}
	if !traceHasCandidateSkip(result.Trace, 0, "model_circuit_filter", "model_circuit_open") {
		t.Fatalf("trace 应记录 model_circuit_filter 跳过, candidates = %+v", result.Trace.Candidates)
	}

	// 同渠道其他模型仍走原优先级顺序。
	healthy, err := sched.SelectChannel(context.Background(), "", map[int]bool{}, ChannelKindMessages, "claude-opus-5", "", "")
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if healthy.ChannelIndex != 0 {
		t.Fatalf("其他模型不应受连累，期望 index=0, got %d", healthy.ChannelIndex)
	}
}

// TestModelCircuitFilterFailsOpen 全部渠道都熔断时必须保留候选列表。
// 否则请求会退化成"没有可用渠道"，掩盖上游真实的故障原因。
func TestModelCircuitFilterFailsOpen(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:       "only-channel",
				ChannelUID: "ch_only",
				BaseURL:    "https://only.example.com",
				APIKeys:    []string{"sk-only"},
				Status:     "active",
				Priority:   1,
			},
		},
	}

	sched, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	const model = "claude-sonnet-5"
	tracker := sched.messagesMetricsManager.ModelCircuit()
	keyHash := metrics.ModelCircuitKeyHash("sk-only")
	tracker.RecordModelFailure("ch_only", keyHash, model, "HTTP 403")
	tracker.RecordModelFailure("ch_only", keyHash, model, "HTTP 403")

	result, err := sched.SelectChannel(context.Background(), "", map[int]bool{}, ChannelKindMessages, model, "", "")
	if err != nil {
		t.Fatalf("唯一渠道熔断时应 fail-open 而非报错: %v", err)
	}
	if result.ChannelIndex != 0 {
		t.Fatalf("fail-open 应保留唯一渠道, got index=%d", result.ChannelIndex)
	}
}

// TestModelCircuitSkipsTraceAffinity 亲和渠道命中熔断组合时必须重选。
// 长会话的亲和性会持续粘在故障渠道上，正是本机制要解决的放大路径。
func TestModelCircuitSkipsTraceAffinity(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:       "affinity-channel",
				ChannelUID: "ch_affinity",
				BaseURL:    "https://affinity.example.com",
				APIKeys:    []string{"sk-affinity"},
				Status:     "active",
				Priority:   1,
			},
			{
				Name:       "other-channel",
				ChannelUID: "ch_other",
				BaseURL:    "https://other.example.com",
				APIKeys:    []string{"sk-other"},
				Status:     "active",
				Priority:   2,
			},
		},
	}

	sched, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	const model = "claude-sonnet-5"
	const userID = "affinity-user"
	sched.SetTraceAffinity(userID, 0, ChannelKindMessages)

	tracker := sched.messagesMetricsManager.ModelCircuit()
	keyHash := metrics.ModelCircuitKeyHash("sk-affinity")
	tracker.RecordModelFailure("ch_affinity", keyHash, model, "HTTP 403")
	tracker.RecordModelFailure("ch_affinity", keyHash, model, "HTTP 403")

	result, err := sched.SelectChannel(context.Background(), userID, map[int]bool{}, ChannelKindMessages, model, "", "")
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("亲和渠道已熔断，应重选到 index=1, got %d", result.ChannelIndex)
	}
}

// TestChannelModelCircuitOpenHelper 直接覆盖 channelModelCircuitOpen。
// 亲和性分支的防线依赖它：正常路径下熔断渠道已被 filterChannelsByModelCircuit 剔除，
// 但该过滤 fail-open，全渠道熔断时亲和性仍需自行判定，否则长会话会粘住故障组合。
func TestChannelModelCircuitOpenHelper(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:       "multi-key-channel",
				ChannelUID: "ch_multi",
				BaseURL:    "https://multi.example.com",
				APIKeys:    []string{"sk-a", "sk-b"},
				Status:     "active",
				Priority:   1,
			},
		},
	}

	sched, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	const model = "claude-sonnet-5"
	upstream := sched.getUpstreamByIndex(0, ChannelKindMessages)
	tracker := sched.messagesMetricsManager.ModelCircuit()

	openKey := func(key string) {
		h := metrics.ModelCircuitKeyHash(key)
		tracker.RecordModelFailure("ch_multi", h, model, "HTTP 403")
		tracker.RecordModelFailure("ch_multi", h, model, "HTTP 403")
	}

	if sched.channelModelCircuitOpen(upstream, ChannelKindMessages, model) {
		t.Fatal("无失败记录时不应判定为熔断")
	}

	openKey("sk-a")
	if sched.channelModelCircuitOpen(upstream, ChannelKindMessages, model) {
		t.Fatal("sk-b 仍健康时不应升级为渠道级熔断")
	}

	openKey("sk-b")
	if !sched.channelModelCircuitOpen(upstream, ChannelKindMessages, model) {
		t.Fatal("全部 Key 熔断后应判定为渠道级熔断")
	}

	// 其他模型不受连累。
	if sched.channelModelCircuitOpen(upstream, ChannelKindMessages, "claude-opus-5") {
		t.Fatal("其他模型不应被连累")
	}
	// model 为空（Images/Vectors 等入口）时不参与该机制。
	if sched.channelModelCircuitOpen(upstream, ChannelKindMessages, "") {
		t.Fatal("model 为空时不应判定熔断")
	}
}

// TestModelCircuitChannelLevelIgnoresUnusableKeys 渠道级排除只统计实际可用的 Key。
//
// 回归防护：曾用 upstream.APIKeys 全量算 keyHash，已 blacklist 的 Key 永远不会有
// 失败记录，导致"是否全部熔断"永远为假——渠道有 3 把 Key、2 把已拉黑时，唯一在用的
// 那把熔断后渠道仍被判为健康，请求继续被路由到故障渠道。
func TestModelCircuitChannelLevelIgnoresUnusableKeys(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:       "partial-channel",
				ChannelUID: "ch_partial",
				BaseURL:    "https://partial.example.com",
				APIKeys:    []string{"sk-live", "sk-dead1", "sk-dead2"},
				DisabledAPIKeys: []config.DisabledKeyInfo{
					{Key: "sk-dead1", Reason: "authentication_error"},
					{Key: "sk-dead2", Reason: "authentication_error"},
				},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:       "healthy-backup",
				ChannelUID: "ch_backup",
				BaseURL:    "https://backup.example.com",
				APIKeys:    []string{"sk-backup"},
				Status:     "active",
				Priority:   2,
			},
		},
	}

	sched, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	const model = "claude-sonnet-5"
	tracker := sched.messagesMetricsManager.ModelCircuit()
	liveHash := metrics.ModelCircuitKeyHash("sk-live")
	tracker.RecordModelFailure("ch_partial", liveHash, model, "HTTP 403")
	tracker.RecordModelFailure("ch_partial", liveHash, model, "HTTP 403")

	upstream := sched.getUpstreamByIndex(0, ChannelKindMessages)
	if !sched.channelModelCircuitOpen(upstream, ChannelKindMessages, model) {
		t.Fatal("唯一可用 Key 已熔断，渠道级应判定为熔断（被拉黑的 Key 不应参与统计）")
	}

	result, err := sched.SelectChannel(context.Background(), "", map[int]bool{}, ChannelKindMessages, model, "", "")
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Fatalf("应规避故障渠道选备用渠道，got index=%d", result.ChannelIndex)
	}
}
