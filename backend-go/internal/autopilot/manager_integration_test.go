package autopilot

import (
	"net/http"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/keypool"
)

func TestManagerCollectAllPreservesUpstreamServiceType(t *testing.T) {
	const (
		channelUID = "ch-service-type"
		baseURL    = "https://service-type.example.com"
		apiKey     = "sk-service-type"
	)
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	cfgManager, cleanup := createTestConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{
			{
				ChannelUID:  channelUID,
				BaseURL:     baseURL,
				APIKeys:     []string{apiKey},
				ServiceType: "openai",
			},
		},
	})
	t.Cleanup(cleanup)

	var gotChannelKind, gotServiceType string
	provider := &mockMetricsProvider{
		statsFn: func(channelKind, _, _, serviceType string, _ time.Duration) TimeWindowStats {
			gotChannelKind, gotServiceType = channelKind, serviceType
			return TimeWindowStats{}
		},
		snapshotFn: func(channelKind, _, _, serviceType string) KeyCircuitSnapshot {
			gotChannelKind, gotServiceType = channelKind, serviceType
			return KeyCircuitSnapshot{}
		},
	}
	metrics := NewMetricsAdapterManager(map[string]MetricsProvider{
		"messages": provider,
	})
	mgr, err := NewManager(store, metrics, cfgManager, ManagerConfig{QuietLogs: true})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.collectAll()

	endpointUID := GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey))
	profile := store.Get(endpointUID)
	if profile == nil {
		t.Fatal("collectAll 未写入 endpoint 画像")
	}
	if profile.ChannelKind != "messages" || profile.ServiceType != "openai" {
		t.Fatalf("画像协议身份错误: channelKind=%q serviceType=%q", profile.ChannelKind, profile.ServiceType)
	}
	if gotChannelKind != "messages" || gotServiceType != "openai" {
		t.Fatalf("指标路由身份错误: channelKind=%q serviceType=%q", gotChannelKind, gotServiceType)
	}
	expectedMetricsKey := computeMetricsIdentityKey(baseURL, apiKey, "openai")
	if profile.MetricsKey != expectedMetricsKey || profile.KeyHash != KeyHashFromAPIKey(apiKey) {
		t.Fatalf("画像 endpoint 身份不一致: metricsKey=%q keyHash=%q", profile.MetricsKey, profile.KeyHash)
	}
}

func TestManagerCollectAllReconcilesActiveMultiURLEndpoints(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	stale := newTestProfile("ep-stale", "ch-removed", "messages", "https://removed.example.com")
	if err := store.Upsert(stale); err != nil {
		t.Fatalf("写入旧画像失败: %v", err)
	}

	channel := config.UpstreamConfig{
		ChannelUID:  "ch-multi",
		ServiceType: "claude",
		BaseURLs: []string{
			"https://api.example.com/anthropic",
			"https://plan.example.com/anthropic",
		},
		APIKeys: []string{"sk-api", "sk-plan", "sk-unbound"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-api", BaseURL: "https://api.example.com/anthropic"},
			{Key: "sk-plan", BaseURL: "https://plan.example.com/anthropic"},
			{Key: "sk-unbound"},
		},
	}
	cfgManager, cleanup := createTestConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{channel},
	})
	t.Cleanup(cleanup)

	provider := newMockProvider(TimeWindowStats{}, KeyCircuitSnapshot{})
	mgr, err := NewManager(
		store,
		NewMetricsAdapterManager(map[string]MetricsProvider{"messages": provider}),
		cfgManager,
		ManagerConfig{QuietLogs: true},
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := len(store.ListActive()); got != 0 {
		t.Fatalf("Manager 初始化后旧画像仍在有效视图中: got=%d, want 0", got)
	}
	mgr.collectAll()

	active := store.ListActive()
	if len(active) != 4 {
		t.Fatalf("有效画像数=%d, want 4", len(active))
	}
	if got := len(store.ListAll()); got != 5 {
		t.Fatalf("全量画像数=%d, want 5（4 个当前 + 1 个历史）", got)
	}
	if store.Get(stale.EndpointUID) == nil {
		t.Fatal("对账不应删除历史画像")
	}

	configured := cfgManager.GetConfig().Upstream[0]
	for _, baseURL := range configured.GetAllBaseURLs() {
		for _, apiKey := range configured.APIKeys {
			bound := configured.BoundBaseURLForKey(apiKey)
			if bound != "" && bound != baseURL {
				continue
			}
			uid := GenerateEndpointUID(configured.ChannelUID, baseURL, KeyHashFromAPIKey(apiKey))
			if store.Get(uid) == nil {
				t.Fatalf("缺少当前 binding 画像: url=%s key=%s", baseURL, apiKey)
			}
		}
	}
}

func TestManagerBuildEndpointLimiterMappingsMatchesFailoverScope(t *testing.T) {
	const (
		legacyKey = "sk-legacy"
		scopedKey = "sk-scoped"
	)
	cfgManager, cleanup := createTestConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{
			{
				ChannelUID: "ch-legacy",
				BaseURL:    "https://legacy.example.com",
				APIKeys:    []string{legacyKey},
			},
			{
				ChannelUID: "ch-scoped",
				BaseURL:    "https://scoped.example.com",
				APIKeys:    []string{scopedKey},
				APIKeyConfigs: []config.APIKeyConfig{
					{Key: scopedKey, Name: "scoped account"},
				},
			},
		},
	})
	t.Cleanup(cleanup)

	mgr := &Manager{cfgManager: cfgManager}
	cfg := cfgManager.GetConfig()
	mappings := mgr.buildEndpointLimiterMappings(buildEndpointInventory(cfg))
	byEndpoint := make(map[string]EndpointLimiterMapping, len(mappings))
	for _, mapping := range mappings {
		byEndpoint[mapping.EndpointUID] = mapping
	}

	legacyUID := GenerateEndpointUID("ch-legacy", "https://legacy.example.com", KeyHashFromAPIKey(legacyKey))
	if got := byEndpoint[legacyUID].LimiterKey; got != "Messages:0" {
		t.Fatalf("legacy limiter key = %q, want channel limiter Messages:0", got)
	}

	scopedUID := GenerateEndpointUID("ch-scoped", "https://scoped.example.com", KeyHashFromAPIKey(scopedKey))
	wantScope := keypool.LimiterScopeFor(scopedKey, config.APIKeyConfig{Key: scopedKey, Name: "scoped account"})
	if got := byEndpoint[scopedUID].LimiterKey; got != "Messages:1:"+wantScope {
		t.Fatalf("scoped limiter key = %q, want %q", got, "Messages:1:"+wantScope)
	}
}

// TestObserveRateLimitSignal_FeedsDiscovererAndBuckets 验证 ObserveRateLimitSignal
// 同时喂 RateLimitDiscoverer 和 TimeBucketStore。
func TestObserveRateLimitSignal_FeedsDiscovererAndBuckets(t *testing.T) {
	store := NewTimeBucketStore()
	mgr := &Manager{
		store:               nil, // collectAll 不调用，不需要
		rateLimitDiscoverer: NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true}),
		timeBucketStore:     store,
	}

	endpointUID := "ep-test-001"
	metricsKey := "mk-test-001"

	// 模拟 2xx 成功响应（带 Anthropic limit header）
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-requests-limit", "60")
	headers.Set("anthropic-ratelimit-requests-remaining", "40")
	headers.Set("anthropic-ratelimit-requests-reset", "2026-01-01T00:01:00Z")

	mgr.ObserveRateLimitSignal(endpointUID, 1, metricsKey, "messages", "test-channel", false, 200, headers, http.StatusOK, "")

	// 验证 RateLimitDiscoverer 收到信号
	state := mgr.rateLimitDiscoverer.GetState(endpointUID)
	if state == nil {
		t.Fatal("RateLimitDiscoverer 未收到信号")
	}
	if state.ObserveCount != 1 {
		t.Errorf("ObserveCount = %d, want 1", state.ObserveCount)
	}
	// header limit=60 → RPM 应为 60（1 分钟窗口）
	if state.EstimatedRPM != 60 {
		t.Errorf("EstimatedRPM = %d, want 60", state.EstimatedRPM)
	}
	if state.Source != RateLimitSourceHeader {
		t.Errorf("Source = %s, want %s", state.Source, RateLimitSourceHeader)
	}

	// 验证 TimeBucketStore 收到信号
	buckets := store.GetBuckets(endpointUID, 1)
	if len(buckets) == 0 {
		t.Fatal("TimeBucketStore 未收到信号")
	}
	b := buckets[0]
	if b.RequestCount != 1 {
		t.Errorf("RequestCount = %d, want 1", b.RequestCount)
	}
	if b.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", b.SuccessCount)
	}
}

// TestObserveRateLimitSignal_429FeedsDiscoverer 验证 429 信号喂发现器并记录为失败。
func TestObserveRateLimitSignal_429FeedsDiscoverer(t *testing.T) {
	store := NewTimeBucketStore()
	mgr := &Manager{
		rateLimitDiscoverer: NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true}),
		timeBucketStore:     store,
	}

	endpointUID := "ep-test-429"
	headers := http.Header{}
	headers.Set("Retry-After", "30")

	mgr.ObserveRateLimitSignal(endpointUID, 1, "mk-429", "messages", "test-channel", false, 100, headers, http.StatusTooManyRequests, "")

	// 验证发现器收到 429 信号
	state := mgr.rateLimitDiscoverer.GetState(endpointUID)
	if state == nil {
		t.Fatal("RateLimitDiscoverer 未收到 429 信号")
	}
	if state.Last429At == nil {
		t.Error("Last429At 应非 nil")
	}
	if state.Source != RateLimitSourcePassiveAIMD {
		t.Errorf("Source = %s, want %s", state.Source, RateLimitSourcePassiveAIMD)
	}
	// 429+Retry-After: RPM 应被减半（从 MaxAutoRPM/2=60 降到 30）
	if state.EstimatedRPM <= 0 {
		t.Errorf("EstimatedRPM = %d, 应 > 0", state.EstimatedRPM)
	}

	// 验证时间桶记录为失败
	buckets := store.GetBuckets(endpointUID, 1)
	if len(buckets) == 0 {
		t.Fatal("TimeBucketStore 未收到 429 信号")
	}
	b := buckets[0]
	if b.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", b.FailureCount)
	}
	if b.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0", b.SuccessCount)
	}
}

// TestObserveRateLimitSignal_Non2xxNon429RecordsBucketOnly 验证非 2xx 非 429 只记录时间桶。
func TestObserveRateLimitSignal_Non2xxNon429RecordsBucketOnly(t *testing.T) {
	store := NewTimeBucketStore()
	mgr := &Manager{
		rateLimitDiscoverer: NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true}),
		timeBucketStore:     store,
	}

	endpointUID := "ep-test-500"
	headers := http.Header{}

	mgr.ObserveRateLimitSignal(endpointUID, 1, "mk-500", "messages", "test-channel", false, 50, headers, http.StatusInternalServerError, "")

	// 发现器不应收到信号
	state := mgr.rateLimitDiscoverer.GetState(endpointUID)
	if state != nil {
		t.Error("RateLimitDiscoverer 不应收到 500 信号")
	}

	// 时间桶应记录为失败
	buckets := store.GetBuckets(endpointUID, 1)
	if len(buckets) == 0 {
		t.Fatal("TimeBucketStore 应收到 500 信号")
	}
	if buckets[0].FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", buckets[0].FailureCount)
	}
}

// TestObserveRateLimitSignal_EmptyEndpointUIDNoop 验证空 endpointUID 安全跳过。
func TestObserveRateLimitSignal_EmptyEndpointUIDNoop(t *testing.T) {
	store := NewTimeBucketStore()
	mgr := &Manager{
		rateLimitDiscoverer: NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true}),
		timeBucketStore:     store,
	}

	headers := http.Header{}
	mgr.ObserveRateLimitSignal("", 1, "mk", "messages", "test-channel", false, 100, headers, http.StatusOK, "")

	if mgr.rateLimitDiscoverer.StateCount() != 0 {
		t.Error("空 endpointUID 不应触发发现器")
	}
	if len(store.GetBuckets("", 1)) != 0 {
		t.Error("空 endpointUID 不应触发时间桶")
	}
}

// TestObserveRateLimitSignal_NilHeadersNoop 验证 nil headers 安全跳过。
func TestObserveRateLimitSignal_NilHeadersNoop(t *testing.T) {
	store := NewTimeBucketStore()
	mgr := &Manager{
		rateLimitDiscoverer: NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true}),
		timeBucketStore:     store,
	}

	// nil headers 不应 panic
	mgr.ObserveRateLimitSignal("ep", 1, "mk", "messages", "test-channel", false, 100, nil, http.StatusOK, "")
}
