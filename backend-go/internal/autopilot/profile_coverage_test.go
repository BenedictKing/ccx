package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// newTestManagerForCoverage 构造一个具备 ProfileStore + ModelProfileStore + cfgManager 的最小 Manager，
// 供 computeProfileCoverage 测试使用。
func newTestManagerForCoverage(t *testing.T) *Manager {
	t.Helper()
	store, err := NewProfileStore(":memory:")
	if err != nil {
		t.Fatalf("创建 ProfileStore 失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	modelStore, err := NewModelProfileStoreWithDB(store.DB())
	if err != nil {
		t.Fatalf("创建 ModelProfileStore 失败: %v", err)
	}

	cfgManager, cleanup := createTestConfigManagerForResolver(t, config.Config{})
	t.Cleanup(cleanup)

	return &Manager{
		store:             store,
		modelProfileStore: modelStore,
		cfgManager:        cfgManager,
	}
}

// TestComputeProfileCoverage_NotReadyWithoutProbedProfiles 覆盖 Goal B 的核心断言：
// 渠道的 endpoint 缺少画像，或画像存在但从未探测成功时，渠道级 verdict 必须是 not_ready。
func TestComputeProfileCoverage_NotReadyWithoutProbedProfiles(t *testing.T) {
	const (
		channelUID = "ch_coverage_not_ready"
		baseURL    = "https://example.test/api"
		apiKey     = "test-api-key"
	)
	mgr := newTestManagerForCoverage(t)

	endpointUID := GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey))
	metricsKey := KeyHashFromAPIKey(apiKey)
	if err := mgr.ProfileStore().Upsert(&KeyEndpointProfile{
		ChannelUID:  channelUID,
		ChannelKind: "messages",
		EndpointUID: endpointUID,
		BaseURL:     baseURL,
		KeyHash:     metricsKey,
		MetricsKey:  metricsKey,
		HealthState: HealthStateUnknown,
	}); err != nil {
		t.Fatalf("Upsert KeyEndpointProfile 失败: %v", err)
	}

	// 该 endpoint 完全没有模型画像 —— 覆盖率门槛必须判定为 not_ready。
	resp := computeProfileCoverage(mgr)
	report := findChannelCoverageReport(t, resp, channelUID)
	if report.Verdict != "not_ready" {
		t.Fatalf("Verdict = %q, want not_ready (endpoint 缺少模型画像)", report.Verdict)
	}
	if len(report.Endpoints) != 1 || report.Endpoints[0].HasProfile {
		t.Fatalf("Endpoints = %+v, want 1 endpoint with HasProfile=false", report.Endpoints)
	}

	// 补一个画像，但探测未成功（ProbeSuccess=false）——仍应判定为 not_ready。
	if err := mgr.ModelProfileStore().Upsert(&ModelProfile{
		ChannelUID:   channelUID,
		ChannelKind:  "messages",
		MetricsKey:   metricsKey,
		ModelID:      "some-model",
		Source:       "auto_probe",
		ProbeSuccess: false,
	}); err != nil {
		t.Fatalf("Upsert ModelProfile 失败: %v", err)
	}

	resp = computeProfileCoverage(mgr)
	report = findChannelCoverageReport(t, resp, channelUID)
	if report.Verdict != "not_ready" {
		t.Fatalf("Verdict = %q, want not_ready (画像未探测成功)", report.Verdict)
	}
	if len(report.Endpoints) != 1 || !report.Endpoints[0].HasProfile || report.Endpoints[0].ProbedOK {
		t.Fatalf("Endpoints = %+v, want HasProfile=true ProbedOK=false", report.Endpoints)
	}
}

// TestComputeProfileCoverage_ReadyWithProbedNonBuiltinProfile 验证反例：
// 当 endpoint 拥有探测成功且来源非纯 builtin_registry 的画像时，渠道级 verdict 应为 ready。
func TestComputeProfileCoverage_ReadyWithProbedNonBuiltinProfile(t *testing.T) {
	const (
		channelUID = "ch_coverage_ready"
		baseURL    = "https://example.test/api"
		apiKey     = "test-api-key-ready"
	)
	mgr := newTestManagerForCoverage(t)

	endpointUID := GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey))
	metricsKey := KeyHashFromAPIKey(apiKey)
	if err := mgr.ProfileStore().Upsert(&KeyEndpointProfile{
		ChannelUID:  channelUID,
		ChannelKind: "messages",
		EndpointUID: endpointUID,
		BaseURL:     baseURL,
		KeyHash:     metricsKey,
		MetricsKey:  metricsKey,
		HealthState: HealthStateHealthy,
	}); err != nil {
		t.Fatalf("Upsert KeyEndpointProfile 失败: %v", err)
	}
	if err := mgr.ModelProfileStore().Upsert(&ModelProfile{
		ChannelUID:            channelUID,
		ChannelKind:           "messages",
		MetricsKey:            metricsKey,
		ModelID:               "some-model",
		Source:                "auto_probe",
		ProbeSuccess:          true,
		SupportsEffortControl: true,
	}); err != nil {
		t.Fatalf("Upsert ModelProfile 失败: %v", err)
	}

	resp := computeProfileCoverage(mgr)
	report := findChannelCoverageReport(t, resp, channelUID)
	if report.Verdict != "ready" {
		t.Fatalf("Verdict = %q, want ready (reasons=%v)", report.Verdict, report.Reasons)
	}
	if len(report.Endpoints) != 1 {
		t.Fatalf("Endpoints = %+v, want 1 endpoint", report.Endpoints)
	}
	ep := report.Endpoints[0]
	if !ep.HasProfile || !ep.ProbedOK || !ep.HasEffort {
		t.Fatalf("endpoint report = %+v, want HasProfile/ProbedOK/HasEffort all true", ep)
	}
}

func findChannelCoverageReport(t *testing.T, resp CoverageReportResponse, channelUID string) CoverageChannelReport {
	t.Helper()
	for _, r := range resp.Channels {
		if r.ChannelUID == channelUID {
			return r
		}
	}
	t.Fatalf("未找到渠道 %s 的覆盖率报告: %+v", channelUID, resp.Channels)
	return CoverageChannelReport{}
}
