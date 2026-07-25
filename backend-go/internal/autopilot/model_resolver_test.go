package autopilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── 测试辅助 ──

// makeModelProfile 创建测试用 ModelProfile，仅填充 ResolveModel 需要的字段。
func makeModelProfile(modelID string, family ModelFamily, tier QualityTier, ctxTokens int,
	reasoning, vision, toolCalls bool, probeOK bool, latencyMs int64) ModelProfile {
	return ModelProfile{
		ChannelUID:        "ch_test",
		ChannelKind:       "messages",
		MetricsKey:        "metrics_test",
		ModelID:           modelID,
		ModelFamily:       family,
		QualityTier:       tier,
		ContextTokens:     ctxTokens,
		SupportsReasoning: reasoning,
		SupportsVision:    vision,
		SupportsToolCalls: toolCalls,
		ProbeSuccess:      probeOK,
		ProbeLatencyMs:    latencyMs,
	}
}

// newTestResolver 创建带预填充画像的 ModelResolver（无 cfgManager，跳过手动映射检查）。
func newTestResolver(t *testing.T, profiles []ModelProfile) *ModelResolver {
	t.Helper()
	return NewModelResolver(newTestModelProfileStore(profiles), nil)
}

func newTestModelProfileStore(profiles []ModelProfile) *ModelProfileStore {
	// 直接构造 ModelProfileStore，仅使用内存缓存（测试不需要 SQLite）。
	store := &ModelProfileStore{
		cache:     make(map[string]*ModelProfile),
		dirtyKeys: make(map[string]struct{}),
	}
	for i := range profiles {
		p := profiles[i]
		_ = store.Upsert(&p)
	}
	return store
}

func newTestResolverWithConfig(t *testing.T, profiles []ModelProfile, cfg config.Config) *ModelResolver {
	t.Helper()
	cfgManager, cleanup := createTestConfigManagerForResolver(t, cfg)
	t.Cleanup(cleanup)
	return NewModelResolver(newTestModelProfileStore(profiles), cfgManager)
}

func rankTestModels(eligible []ModelProfile, requestModel string, floors ...CapabilityFloor) ModelProfile {
	resolver := &ModelResolver{}
	floor := CapabilityFloor{}
	if len(floors) > 0 {
		floor = floors[0]
	}
	return resolver.rankEligibleModels(eligible, requestModel, "", "", floor).profile
}

// createTestConfigManagerForResolver 创建测试用 ConfigManager。
func createTestConfigManagerForResolver(t *testing.T, cfg config.Config) (*config.ConfigManager, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "model-resolver-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
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

// ── CapabilityFloor 测试 ──

func TestBuildCapabilityFloorFromRequestProfile(t *testing.T) {
	profile := &RequestProfile{
		ContextNeed:   128000,
		ReasoningNeed: true,
		VisionNeed:    true,
		ToolUseNeed:   true,
		QualityNeed:   QualityTierHigh,
	}
	floor := BuildCapabilityFloorFromRequestProfile(profile)

	if floor.MinContextTokens != 128000 {
		t.Errorf("MinContextTokens = %d, want 128000", floor.MinContextTokens)
	}
	if !floor.NeedsReasoning {
		t.Error("NeedsReasoning should be true")
	}
	if !floor.NeedsVision {
		t.Error("NeedsVision should be true")
	}
	if !floor.NeedsToolCalls {
		t.Error("NeedsToolCalls should be true")
	}
	if floor.MinQualityTier != QualityTierHigh {
		t.Errorf("MinQualityTier = %s, want high", floor.MinQualityTier)
	}
	if floor.QualityBenefitCap != "" {
		t.Errorf("QualityBenefitCap = %s, want empty for unknown complexity", floor.QualityBenefitCap)
	}

	// 空 profile 应生成零值 floor
	empty := BuildCapabilityFloorFromRequestProfile(&RequestProfile{})
	if empty.MinContextTokens != 0 || empty.NeedsReasoning || empty.NeedsVision ||
		empty.NeedsToolCalls || empty.MinQualityTier != "" || empty.QualityBenefitCap != "" {
		t.Errorf("empty profile should produce zero-value floor, got %+v", empty)
	}
}

func TestBuildCapabilityFloorCapsQualityBenefitForKnownRoutineTasks(t *testing.T) {
	routine := &RequestProfile{
		TaskClass: TaskClassWorker, Complexity: TaskComplexityRoutine,
		QualityNeed: QualityTierHigh, ReasoningNeed: true,
	}
	if floor := BuildCapabilityFloorFromRequestProfile(routine); floor.QualityBenefitCap != QualityTierHigh {
		t.Fatalf("routine QualityBenefitCap = %q, want high", floor.QualityBenefitCap)
	}

	complex := &RequestProfile{
		TaskClass: TaskClassSupervisor, Complexity: TaskComplexityComplex,
		QualityNeed: QualityTierHigh, ReasoningNeed: true,
	}
	if floor := BuildCapabilityFloorFromRequestProfile(complex); floor.QualityBenefitCap != "" {
		t.Fatalf("complex QualityBenefitCap = %q, want empty", floor.QualityBenefitCap)
	}
}

func TestQualityTargetFromRequestProfileUsesTaskClass(t *testing.T) {
	tests := []struct {
		name       string
		taskClass  TaskClass
		quality    QualityTier
		context    int
		tool       bool
		reasoning  bool
		complexity TaskComplexity
		wantTarget QualityTier
	}{
		{name: "lightweight opus 降到 low", taskClass: TaskClassLightweight, quality: QualityTierPremium, wantTarget: QualityTierLow},
		{name: "worker opus 使用 normal", taskClass: TaskClassWorker, quality: QualityTierPremium, wantTarget: QualityTierNormal},
		{name: "worker 工具请求至少 normal", taskClass: TaskClassWorker, quality: QualityTierPremium, tool: true, wantTarget: QualityTierNormal},
		{name: "supervisor 保持 high", taskClass: TaskClassSupervisor, quality: QualityTierPremium, wantTarget: QualityTierHigh},
		{name: "复杂 supervisor 保持 premium", taskClass: TaskClassSupervisor, quality: QualityTierPremium, complexity: TaskComplexityComplex, wantTarget: QualityTierPremium},
		{name: "复杂 worker 提升到 high", taskClass: TaskClassWorker, quality: QualityTierPremium, complexity: TaskComplexityComplex, wantTarget: QualityTierHigh},
		{name: "常规 supervisor 使用 normal", taskClass: TaskClassSupervisor, quality: QualityTierPremium, complexity: TaskComplexityRoutine, wantTarget: QualityTierNormal},
		{name: "长上下文至少 high", taskClass: TaskClassWorker, quality: QualityTierPremium, context: 50_000, wantTarget: QualityTierHigh},
		{name: "低档请求不被升级", taskClass: TaskClassSupervisor, quality: QualityTierNormal, wantTarget: QualityTierNormal},
		{name: "未知分类保持原档位", taskClass: TaskClass("unknown"), quality: QualityTierPremium, wantTarget: QualityTierPremium},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := &RequestProfile{
				TaskClass:     tt.taskClass,
				QualityNeed:   tt.quality,
				ContextNeed:   tt.context,
				ToolUseNeed:   tt.tool,
				ReasoningNeed: tt.reasoning,
				Complexity:    tt.complexity,
			}
			if got := ResolveQualityTarget(profile); got != tt.wantTarget {
				t.Fatalf("ResolveQualityTarget() = %q, want %q", got, tt.wantTarget)
			}
			floor := BuildCapabilityFloorFromRequestProfile(profile)
			if floor.MinQualityTier != tt.wantTarget {
				t.Fatalf("floor.MinQualityTier = %q, want %q", floor.MinQualityTier, tt.wantTarget)
			}
		})
	}
}

// ── filterByCapabilityFloor 测试 ──

func TestFilterByCapabilityFloor_DropsUnderQualified(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("model-a", ModelFamilyClaude, QualityTierPremium, 200000,
			true, true, true, true, 100), // 全满足
		makeModelProfile("model-b", ModelFamilyClaude, QualityTierNormal, 200000,
			true, true, true, true, 100), // quality 不满足 premium
		makeModelProfile("model-c", ModelFamilyClaude, QualityTierPremium, 50000,
			true, true, true, true, 100), // context 不足
		makeModelProfile("model-d", ModelFamilyClaude, QualityTierPremium, 200000,
			false, true, true, true, 100), // 无 reasoning
		makeModelProfile("model-e", ModelFamilyClaude, QualityTierPremium, 200000,
			true, false, true, true, 100), // 无 vision
		makeModelProfile("model-f", ModelFamilyClaude, QualityTierPremium, 200000,
			true, true, false, true, 100), // 无 tool calls
		makeModelProfile("model-g", ModelFamilyClaude, QualityTierPremium, 200000,
			true, true, true, false, 100), // ProbeSuccess=false
	}

	floor := CapabilityFloor{
		MinContextTokens: 100000,
		NeedsReasoning:   true,
		NeedsVision:      true,
		NeedsToolCalls:   true,
		MinQualityTier:   QualityTierPremium,
	}

	eligible := filterByCapabilityFloor(profiles, floor)

	if len(eligible) != 1 {
		t.Fatalf("expected 1 eligible, got %d", len(eligible))
	}
	if eligible[0].ModelID != "model-a" {
		t.Errorf("expected model-a, got %s", eligible[0].ModelID)
	}
}

func TestFilterByCapabilityFloor_ZeroFloorPassesAllProbed(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("m1", ModelFamilyUnknown, QualityTierLow, 0,
			false, false, false, true, 0),
		makeModelProfile("m2", ModelFamilyUnknown, QualityTierLow, 0,
			false, false, false, false, 0), // 未探测
	}
	eligible := filterByCapabilityFloor(profiles, CapabilityFloor{})
	if len(eligible) != 1 {
		t.Fatalf("expected 1 (only probed), got %d", len(eligible))
	}
}

// ── rankEligibleModels 测试 ──

func TestRankEligibleModels_PrefersSameFamilyAsFinalTieBreaker(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("a-other", ModelFamilyOpenAI, QualityTierHigh, 200000,
			true, false, true, true, 50),
		makeModelProfile("z-claude", ModelFamilyClaude, QualityTierHigh, 200000,
			true, false, true, true, 50),
	}

	best := rankTestModels(eligible, "claude-sonnet-5")
	if best.ModelID != "z-claude" {
		t.Errorf("expected z-claude (same family), got %s", best.ModelID)
	}
}

func TestRankEligibleModels_UsesBenchmarkInsteadOfModelIDForGPT56(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("gpt-5.6-luna", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 50),
		makeModelProfile("gpt-5.6-terra", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 50),
		makeModelProfile("gpt-5.6-sol", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 50),
	}

	orders := [][]ModelProfile{profiles, {profiles[2], profiles[1], profiles[0]}}
	for _, order := range orders {
		best := rankTestModels(order, "claude-fable-5")
		if best.ModelID != "gpt-5.6-sol" {
			t.Fatalf("同档 GPT-5.6 应按基准能力选择 sol，got %s", best.ModelID)
		}
	}
}

func TestRankEligibleModels_AppliesQualityFirstToCostLatencyTieBreak(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("model-cheap", ModelFamilyUnknown, QualityTierLow, 100_000,
			true, false, true, true, 100),
		makeModelProfile("model-fast", ModelFamilyUnknown, QualityTierLow, 100_000,
			true, false, true, true, 10),
	}
	cheapInput, cheapOutput := 1.0, 1.0
	fastInput, fastOutput := 10.0, 10.0
	baseConfig := config.Config{
		Upstream: []config.UpstreamConfig{{ChannelUID: "ch_test", ServiceType: "claude"}},
		UpstreamModelCapabilities: map[string]config.UpstreamModelCapability{
			"model-cheap": {Pricing: &config.ModelPricing{InputCacheMissPrice: &cheapInput, OutputPrice: &cheapOutput}},
			"model-fast":  {Pricing: &config.ModelPricing{InputCacheMissPrice: &fastInput, OutputPrice: &fastOutput}},
		},
	}

	balanced := newTestResolverWithConfig(t, profiles, baseConfig)
	if got := balanced.rankEligibleModels(profiles, "claude-fable-5", "ch_test", "messages", CapabilityFloor{}).profile.ModelID; got != "model-cheap" {
		t.Fatalf("balanced 应先平衡公开成本，got %s", got)
	}

	qualityConfig := baseConfig
	qualityConfig.AutopilotRouting = config.DefaultAutopilotRoutingConfig()
	qualityConfig.AutopilotRouting.CostPreference.Mode = "quality_first"
	qualityFirst := newTestResolverWithConfig(t, profiles, qualityConfig)
	if got := qualityFirst.rankEligibleModels(profiles, "claude-fable-5", "ch_test", "messages", CapabilityFloor{}).profile.ModelID; got != "model-fast" {
		t.Fatalf("quality_first 应把成本放到延迟之后，got %s", got)
	}
}

func TestRankEligibleModels_PrefersNewerClaudeLineWithinSameFamily(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("claude-opus-4.8", ModelFamilyClaude, QualityTierPremium, 1_000_000,
			true, true, true, true, 50),
		makeModelProfile("claude-opus-5", ModelFamilyClaude, QualityTierPremium, 1_000_000,
			true, true, true, true, 50),
	}

	best := rankTestModels(eligible, "claude-fable-5")
	if best.ModelID != "claude-opus-5" {
		t.Fatalf("Claude 同族降级应优先新版 Opus，got %s", best.ModelID)
	}
}

func TestRankEligibleModels_PrefersHigherQualityAboveFloor(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("gpt-5.3", ModelFamilyOpenAI, QualityTierHigh, 200000,
			true, false, true, true, 50),
		makeModelProfile("gpt-5.4", ModelFamilyOpenAI, QualityTierPremium, 200000,
			true, false, true, true, 50),
	}

	best := rankTestModels(eligible, "gpt-5.5")
	if best.ModelID != "gpt-5.4" {
		t.Errorf("expected gpt-5.4 (higher quality), got %s", best.ModelID)
	}
}

func TestRankEligibleModels_DoesNotPenalizeQualityAboveTarget(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("k3", ModelFamilyKimi, QualityTierPremium, 200000,
			true, false, true, true, 1),
		makeModelProfile("kimi-for-coding", ModelFamilyKimi, QualityTierHigh, 200000,
			true, false, true, true, 100),
	}

	best := rankTestModels(eligible, "claude-opus-4-8")
	if best.ModelID != "k3" {
		t.Errorf("expected higher-quality k3, got %s", best.ModelID)
	}
}

func TestResolveModel_UsesQualityBenefitCapForRoutineTasks(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("k3", ModelFamilyKimi, QualityTierPremium, 1_048_576,
			true, true, true, true, 10),
		makeModelProfile("kimi-for-coding", ModelFamilyKimi, QualityTierHigh, 262_144,
			true, true, true, true, 100),
		makeModelProfile("kimi-v2", ModelFamilyKimi, QualityTierNormal, 128_000,
			false, false, true, true, 50),
	}
	resolver := newTestResolver(t, profiles)

	tests := []struct {
		name      string
		profile   RequestProfile
		wantModel string
	}{
		{
			name: "lightweight 选择最低的足够质量档",
			profile: RequestProfile{
				Model: "claude-opus-4-8", ChannelKind: "messages", QualityNeed: QualityTierPremium,
				TaskClass: TaskClassLightweight, Complexity: TaskComplexityTrivial, ContextNeed: 1000,
			},
			wantModel: "kimi-v2",
		},
		{
			name: "常规 Sonnet 工具请求不升级到 premium K3",
			profile: RequestProfile{
				Model: "claude-sonnet-5", ChannelKind: "messages", QualityNeed: QualityTierHigh,
				TaskClass: TaskClassWorker, Complexity: TaskComplexityRoutine, ContextNeed: 26_708,
				ToolUseNeed: true, ReasoningNeed: true,
			},
			wantModel: "kimi-for-coding",
		},
		{
			name: "复杂 Sonnet 仍允许选择 premium K3",
			profile: RequestProfile{
				Model: "claude-sonnet-5", ChannelKind: "messages", QualityNeed: QualityTierHigh,
				TaskClass: TaskClassSupervisor, Complexity: TaskComplexityComplex, ContextNeed: 26_708,
				ToolUseNeed: true, ReasoningNeed: true,
			},
			wantModel: "k3",
		},
		{
			name: "大上下文硬约束保留 K3",
			profile: RequestProfile{
				Model: "claude-sonnet-5", ChannelKind: "messages", QualityNeed: QualityTierHigh,
				TaskClass: TaskClassWorker, Complexity: TaskComplexityRoutine, ContextNeed: 500_000,
				ToolUseNeed: true, ReasoningNeed: true,
			},
			wantModel: "k3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			floor := BuildCapabilityFloorFromRequestProfile(&tt.profile)
			mapped, resolved, reason := resolver.ResolveModel(
				tt.profile.Model, "ch_test", "messages", "metrics_test", floor)
			if !resolved || mapped != tt.wantModel {
				t.Fatalf("ResolveModel() = (%q, %v, %q), want %q", mapped, resolved, reason, tt.wantModel)
			}
		})
	}
}

func TestRankEligibleModels_DoesNotPenalizeLargerContextWindow(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("a-large-window", ModelFamilyClaude, QualityTierNormal, 1000000,
			false, false, false, true, 100),
		makeModelProfile("z-small-window", ModelFamilyClaude, QualityTierNormal, 110000,
			false, false, false, true, 100),
	}

	best := rankTestModels(eligible, "claude-haiku-4-5")
	if best.ModelID != "a-large-window" {
		t.Errorf("expected context size to be ignored after floor filtering, got %s", best.ModelID)
	}
}

func TestRankEligibleModels_PrefersMeasuredProviderQuality(t *testing.T) {
	higherQuality := makeModelProfile("quality-proven", ModelFamilyClaude, QualityTierNormal, 100000,
		false, false, false, true, 500)
	higherQuality.ProviderQualityScore = 0.9
	higherQuality.ProviderQualityConfidence = 0.8
	lowerQuality := makeModelProfile("latency-fast", ModelFamilyClaude, QualityTierNormal, 100000,
		false, false, false, true, 10)
	lowerQuality.ProviderQualityScore = 0.6
	lowerQuality.ProviderQualityConfidence = 0.8

	best := rankTestModels([]ModelProfile{lowerQuality, higherQuality}, "claude-haiku-4-5")
	if best.ModelID != "quality-proven" {
		t.Errorf("expected provider quality evidence to precede latency, got %s", best.ModelID)
	}
}

func TestRankEligibleModels_PrefersPremiumDomesticModelsOverDeepSeekV4Pro(t *testing.T) {
	tests := []struct {
		name      string
		modelID   string
		family    ModelFamily
		context   int
		vision    bool
		toolCalls bool
	}{
		{
			name:      "Kimi K3 alias",
			modelID:   "kimi-k3",
			family:    ModelFamilyKimi,
			context:   262_144,
			vision:    true,
			toolCalls: true,
		},
		{
			name:      "GLM-5.2",
			modelID:   "glm-5.2",
			family:    ModelFamilyGLM,
			context:   1_048_576,
			toolCalls: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eligible := []ModelProfile{
				makeModelProfile("deepseek-v4-pro", ModelFamilyDeepSeek, QualityTierHigh, 1_000_000,
					true, false, true, true, 0),
				makeModelProfile(tt.modelID, tt.family,
					ModelProfileQualityTierFromFamily(tt.family, tt.modelID), tt.context,
					tt.vision, false, tt.toolCalls, true, 0),
			}

			best := rankTestModels(eligible, "claude-opus-4-8", CapabilityFloor{MinQualityTier: QualityTierHigh})
			if best.ModelID != tt.modelID {
				t.Fatalf("expected premium %s to precede high deepseek-v4-pro, got %s", tt.modelID, best.ModelID)
			}
		})
	}
}

func TestRankEligibleModels_PrefersLowerLatency(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("fast", ModelFamilyClaude, QualityTierNormal, 100000,
			false, false, false, true, 50),
		makeModelProfile("slow", ModelFamilyClaude, QualityTierNormal, 100000,
			false, false, false, true, 500),
	}

	best := rankTestModels(eligible, "claude-haiku-4-5")
	if best.ModelID != "fast" {
		t.Errorf("expected fast (lower latency tie-break), got %s", best.ModelID)
	}
}

func TestRankEligibleModels_PrefersLowerKnownCost(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("deepseek-ai/DeepSeek-V3.2", ModelFamilyDeepSeek, QualityTierNormal, 163840,
			true, false, false, true, 0),
		makeModelProfile("deepseek-v4-flash", ModelFamilyDeepSeek, QualityTierNormal, 1000000,
			true, false, true, true, 0),
	}

	best := rankTestModels(eligible, "claude-sonnet-5")
	if best.ModelID != "deepseek-v4-flash" {
		t.Errorf("expected lower-cost deepseek-v4-flash, got %s", best.ModelID)
	}
}

func TestRankEligibleModels_PrefersProviderModelQualityBeforeRelativeCost(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("glm-5.1", ModelFamilyGLM, QualityTierHigh, 202800,
			true, false, true, true, 0),
		makeModelProfile("kimi-k2.6", ModelFamilyKimi, QualityTierHigh, 262144,
			true, false, true, true, 0),
		makeModelProfile("MiniMax-M2.7", ModelFamilyMiniMax, QualityTierNormal, 204800,
			true, false, true, true, 0),
	}
	resolver := newTestResolverWithConfig(t, eligible, config.Config{Upstream: []config.UpstreamConfig{{
		ChannelUID: "ch_test", ProviderID: "compshare", BaseURL: "https://cp.compshare.cn", ServiceType: "claude",
	}}})

	best := resolver.rankEligibleModels(eligible, "claude-sonnet-5", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "glm-5.1" {
		t.Fatalf("expected glm-5.1 quality to precede 6x vs 5x cost, got %s", best.profile.ModelID)
	}
	if !best.providerModelQualityKnown || !best.providerModelQualityComparable ||
		best.providerModelQualityPriority != 2 || best.providerModelQualitySource != "provider_template:compshare" {
		t.Fatalf("provider quality evidence = %+v, want compshare priority 2", best)
	}
}

func TestRankEligibleModels_UsesHigherVersionWithinUndeclaredFamily(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("kimi-k2.6", ModelFamilyKimi, QualityTierHigh, 262144,
			true, true, true, true, 0),
		makeModelProfile("kimi-k2.7-code", ModelFamilyKimi, QualityTierHigh, 262144,
			true, true, true, true, 0),
	}

	best := rankTestModels(eligible, "claude-sonnet-5")
	if best.ModelID != "kimi-k2.7-code" {
		t.Fatalf("未声明同档能力顺序时应优先较高版本，got %s", best.ModelID)
	}
}

func TestCompareModelVersionDoesNotCrossFamily(t *testing.T) {
	left := rankedModelCandidate{profile: ModelProfile{ModelFamily: ModelFamilyKimi}, versionLineage: "k2", versionNumbers: []int{2, 7}}
	right := rankedModelCandidate{profile: ModelProfile{ModelFamily: ModelFamilyGLM}, versionLineage: "glm5", versionNumbers: []int{5, 1}}
	if _, decided := compareModelVersion(left, right); decided {
		t.Fatal("版本号启发式不应跨模型族比较")
	}
}

func TestRankEligibleModels_PrefersProviderRelativeCostWhenQualityOrderIncomplete(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("glm-5.2", ModelFamilyGLM, QualityTierHigh, 1048576,
			true, false, true, true, 0),
		makeModelProfile("glm-5.1", ModelFamilyGLM, QualityTierHigh, 202800,
			true, false, true, true, 0),
	}
	resolver := newTestResolverWithConfig(t, eligible, config.Config{Upstream: []config.UpstreamConfig{{
		ChannelUID: "ch_test", ProviderID: "compshare", BaseURL: "https://cp.compshare.cn", ServiceType: "claude",
	}}})

	orders := [][]ModelProfile{eligible, {eligible[1], eligible[0]}}
	for _, order := range orders {
		best := resolver.rankEligibleModels(order, "claude-sonnet-5", "ch_test", "messages", CapabilityFloor{})
		if best.profile.ModelID != "glm-5.2" {
			t.Fatalf("expected glm-5.2 (2 次优于 6 次 when quality tier metadata is incomplete), got %s", best.profile.ModelID)
		}
		if !best.providerCostKnown || best.providerCostMultiplier != 2 || best.providerModelQualityComparable {
			t.Fatalf("ranking evidence = %+v, want cost multiplier 2 with inactive quality priority", best)
		}
	}
}

func TestRankEligibleModels_ProviderCostTieFallsBackToPublicPrice(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("deepseek-ai/DeepSeek-V3.2", ModelFamilyDeepSeek, QualityTierNormal, 163840,
			true, false, false, true, 0),
		makeModelProfile("deepseek-v4-flash", ModelFamilyDeepSeek, QualityTierNormal, 1000000,
			true, false, true, true, 0),
	}
	resolver := newTestResolverWithConfig(t, eligible, config.Config{Upstream: []config.UpstreamConfig{{
		ChannelUID: "ch_test", ProviderID: "compshare", BaseURL: "https://cp.compshare.cn", ServiceType: "claude",
	}}})

	best := resolver.rankEligibleModels(eligible, "claude-sonnet-5", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "deepseek-v4-flash" {
		t.Fatalf("expected deepseek-v4-flash after equal 1x multipliers fall back to public price, got %s", best.profile.ModelID)
	}
}

func TestProviderModelCostMultiplierInfersLegacyCompshareURL(t *testing.T) {
	multiplier, source, found := providerModelCostMultiplier("GLM-5.2", &config.UpstreamConfig{
		BaseURL: "https://cp.compshare.cn", ServiceType: "claude",
	})
	if !found || multiplier != 2 || source != "provider_template:compshare" {
		t.Fatalf("providerModelCostMultiplier() = %v, %q, %v; want 2, compshare, true", multiplier, source, found)
	}
}

// ── ResolveModel 端到端测试 ──

func TestResolveModel_NoProfiles_ReturnsFalse(t *testing.T) {
	resolver := newTestResolver(t, nil)
	mapped, resolved, reason := resolver.ResolveModel(
		"claude-opus-4-8", "ch_empty", "messages", "mkey", CapabilityFloor{})
	if resolved {
		t.Error("expected resolved=false when no profiles")
	}
	if mapped != "claude-opus-4-8" {
		t.Errorf("expected passthrough model, got %s", mapped)
	}
	if reason != "no_model_profiles" {
		t.Errorf("expected reason 'no_model_profiles', got %s", reason)
	}
}

func TestResolveModel_AllFilteredOut_ReturnsFalse(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("tiny-model", ModelFamilyUnknown, QualityTierLow, 1000,
			false, false, false, true, 100),
	}
	resolver := newTestResolver(t, profiles)

	floor := CapabilityFloor{
		MinContextTokens: 100000,
		MinQualityTier:   QualityTierHigh,
	}
	mapped, resolved, reason := resolver.ResolveModel(
		"claude-opus-4-8", "ch_test", "messages", "metrics_test", floor)
	if resolved {
		t.Error("expected resolved=false when all filtered")
	}
	if reason != "no_capable_model" {
		t.Errorf("expected reason 'no_capable_model', got %s", reason)
	}
	if mapped != "claude-opus-4-8" {
		t.Errorf("expected passthrough model, got %s", mapped)
	}
}

func TestResolveModel_FindsBestMatch(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("claude-sonnet-4-6", ModelFamilyClaude, QualityTierHigh, 200000,
			true, false, true, true, 80),
		makeModelProfile("gpt-5.3", ModelFamilyOpenAI, QualityTierHigh, 200000,
			true, false, true, true, 60),
	}
	resolver := newTestResolver(t, profiles)

	floor := CapabilityFloor{MinQualityTier: QualityTierHigh}
	mapped, resolved, reason := resolver.ResolveModel(
		"claude-sonnet-5", "ch_test", "messages", "metrics_test", floor)
	if !resolved {
		t.Error("expected resolved=true")
	}
	if mapped != "claude-sonnet-4-6" {
		t.Errorf("expected same-family claude-sonnet-4-6, got %s", mapped)
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestResolveModel_CompshareInventoryPrefersGLM52OverDeepSeekFallbacks(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("glm-5.2", ModelFamilyGLM, QualityTierPremium, 1048576,
			true, false, true, true, 0),
		makeModelProfile("glm-5.1", ModelFamilyGLM, QualityTierHigh, 202800,
			true, false, true, true, 0),
		makeModelProfile("MiniMax-M2.7", ModelFamilyMiniMax, QualityTierNormal, 204800,
			true, false, true, true, 0),
		makeModelProfile("kimi-k2.6", ModelFamilyKimi, QualityTierHigh, 262144,
			true, false, true, true, 0),
		makeModelProfile("deepseek-ai/DeepSeek-V3.2", ModelFamilyDeepSeek, QualityTierNormal, 163840,
			true, false, false, true, 0),
		makeModelProfile("deepseek-v4-flash", ModelFamilyDeepSeek, QualityTierNormal, 1000000,
			true, false, true, true, 0),
	}
	resolver := newTestResolverWithConfig(t, profiles, config.Config{Upstream: []config.UpstreamConfig{{
		ChannelUID: "ch_test", ProviderID: "compshare", BaseURL: "https://cp.compshare.cn", ServiceType: "claude",
		AutoManaged: true,
	}}})

	floors := []CapabilityFloor{
		{MinContextTokens: 39_561, MinQualityTier: QualityTierNormal},
		{
			MinContextTokens: 39_561, MinQualityTier: QualityTierHigh,
			QualityBenefitCap: QualityTierHigh,
		},
	}
	for _, floor := range floors {
		mapped, resolved, reason := resolver.ResolveModel(
			"claude-sonnet-5", "ch_test", "messages", "metrics_test", floor)
		if !resolved || mapped != "glm-5.2" {
			t.Fatalf("ResolveModel(%+v) = (%q, %v, %q), want glm-5.2", floor, mapped, resolved, reason)
		}
	}
}

func TestResolveModel_GPT56RequiresPremiumReplacement(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("glm-4.5", ModelFamilyGLM, QualityTierNormal, 128000,
			false, false, true, true, 20),
		makeModelProfile("glm-5.1", ModelFamilyGLM, QualityTierHigh, 202800,
			true, false, true, true, 30),
		makeModelProfile("glm-5.2", ModelFamilyGLM, QualityTierPremium, 1048576,
			true, false, true, true, 40),
	}
	for i := range profiles {
		profiles[i].ChannelKind = "responses"
	}
	resolver := newTestResolver(t, profiles)
	floor := CapabilityFloor{MinQualityTier: ModelProfileQualityTierFromFamily(ModelFamilyOpenAI, "gpt-5.6-sol")}

	mapped, resolved, reason := resolver.ResolveModel(
		"gpt-5.6-sol", "ch_test", "responses", "metrics_test", floor)
	if !resolved || mapped != "glm-5.2" {
		t.Fatalf("ResolveModel() = (%q, %v, %q), want premium glm-5.2", mapped, resolved, reason)
	}
}

func TestResolveModel_RecognizesDottedOpus48AndOpus5BeforeCapabilityFilter(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("claude-opus-4.8", ModelFamilyClaude, QualityTierLow, 0,
			false, false, false, true, 20),
		makeModelProfile("claude-opus-5", ModelFamilyClaude, QualityTierLow, 0,
			false, false, false, true, 30),
	}
	for i := range profiles {
		profiles[i].ChannelKind = "messages"
		profiles[i].Source = "auto_discovery"
	}
	resolver := newTestResolverWithConfig(t, profiles, config.Config{Upstream: []config.UpstreamConfig{{
		ChannelUID: "ch_test", AutoManaged: true, ServiceType: "claude",
	}}})
	floor := CapabilityFloor{
		MinContextTokens: 200_000,
		MinQualityTier:   QualityTierPremium,
		NeedsReasoning:   true,
		NeedsVision:      true,
		NeedsToolCalls:   true,
	}

	tests := []struct {
		request string
		want    string
	}{
		{request: "claude-opus-4-8", want: "claude-opus-4.8"},
		{request: "claude-opus-5", want: "claude-opus-5"},
	}
	for _, tt := range tests {
		t.Run(tt.request, func(t *testing.T) {
			mapped, resolved, reason := resolver.ResolveModel(
				tt.request, "ch_test", "messages", "metrics_test", floor)
			if !resolved || mapped != tt.want {
				t.Fatalf("ResolveModel() = (%q, %v, %q), want %q", mapped, resolved, reason, tt.want)
			}
		})
	}
}

func TestResolveModel_RefreshesLegacyAutoDiscoveryCapabilities(t *testing.T) {
	profile := makeModelProfile("glm-5.2", ModelFamilyGLM, QualityTierHigh, 272000,
		false, false, false, true, 0)
	profile.ChannelKind = "responses"
	profile.Source = "auto_discovery"
	store := &ModelProfileStore{
		cache:     make(map[string]*ModelProfile),
		dirtyKeys: make(map[string]struct{}),
	}
	if err := store.Upsert(&profile); err != nil {
		t.Fatal(err)
	}
	cfgManager, cleanup := createTestConfigManagerForResolver(t, config.Config{
		ResponsesUpstream: []config.UpstreamConfig{{
			ChannelUID: "ch_test", AutoManaged: true, ServiceType: "openai",
		}},
	})
	defer cleanup()
	resolver := NewModelResolver(store, cfgManager)

	mapped, resolved, reason := resolver.ResolveModel(
		"gpt-5.6-sol", "ch_test", "responses", "metrics_test",
		CapabilityFloor{MinQualityTier: QualityTierPremium, NeedsReasoning: true, NeedsToolCalls: true})
	if !resolved || mapped != "glm-5.2" {
		t.Fatalf("ResolveModel() = (%q, %v, %q), want refreshed glm-5.2 capabilities", mapped, resolved, reason)
	}
	refreshed := store.Get("ch_test", "responses", "metrics_test", "glm-5.2")
	if refreshed == nil || refreshed.QualityTier != QualityTierPremium ||
		refreshed.ContextTokens != 1048576 || !refreshed.SupportsReasoning || !refreshed.SupportsToolCalls {
		t.Fatalf("旧自动发现画像未在内存中完成升级: %+v", refreshed)
	}
}

func TestResolveModel_RefreshesKimiK3VisionCapabilities(t *testing.T) {
	profile := makeModelProfile("k3", ModelFamilyKimi, QualityTierPremium, 262144,
		true, false, true, true, 0)
	profile.Source = "auto_discovery"
	store := &ModelProfileStore{
		cache:     make(map[string]*ModelProfile),
		dirtyKeys: make(map[string]struct{}),
	}
	if err := store.Upsert(&profile); err != nil {
		t.Fatal(err)
	}
	cfgManager, cleanup := createTestConfigManagerForResolver(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			ChannelUID: "ch_test", AutoManaged: true, ProviderID: "kimi", ServiceType: "claude",
		}},
	})
	defer cleanup()
	resolver := NewModelResolver(store, cfgManager)

	mapped, resolved, reason := resolver.ResolveModel(
		"claude-opus-4-8", "ch_test", "messages", "metrics_test",
		CapabilityFloor{
			MinContextTokens: 200000,
			MinQualityTier:   QualityTierPremium,
			NeedsReasoning:   true,
			NeedsVision:      true,
			NeedsToolCalls:   true,
		})
	if !resolved || mapped != "k3" {
		t.Fatalf("ResolveModel() = (%q, %v, %q), want vision-capable k3", mapped, resolved, reason)
	}
	refreshed := store.Get("ch_test", "messages", "metrics_test", "k3")
	if refreshed == nil || !refreshed.SupportsVision || !refreshed.SupportsToolCalls ||
		!refreshed.SupportsReasoning || refreshed.QualityTier != QualityTierPremium {
		t.Fatalf("K3 自动发现画像未按当前注册表刷新: %+v", refreshed)
	}
}

func TestResolveModelAnyEndpoint_MapsWithoutExactModelMatch(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("mimo-v2.5-pro", ModelFamilyMiMo, QualityTierHigh, 1000000,
			true, false, true, true, 80),
		makeModelProfile("mimo-v2.5", ModelFamilyMiMo, QualityTierNormal, 1000000,
			true, true, true, true, 90),
	}
	resolver := newTestResolver(t, profiles)

	mapped, found, reason := resolver.ResolveModelAnyEndpoint("claude-sonnet-5", "ch_test", "messages")
	if !found {
		t.Fatalf("expected found=true, reason=%s", reason)
	}
	if mapped == "" || mapped == "claude-sonnet-5" {
		t.Fatalf("expected request model to be mapped to discovered model, got %q", mapped)
	}
}

func TestResolveModel_IgnoresLegacyManualRedirectForAutoManagedProvider(t *testing.T) {
	upstream := config.UpstreamConfig{
		ChannelUID:   "ch_test",
		AutoManaged:  true,
		ProviderID:   "mimo",
		ModelMapping: map[string]string{"claude-sonnet-5": "legacy-target"},
	}
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{upstream},
	}
	cfgManager, cleanup := createTestConfigManagerForResolver(t, cfg)
	defer cleanup()

	store := &ModelProfileStore{
		cache:     make(map[string]*ModelProfile),
		dirtyKeys: make(map[string]struct{}),
	}
	profile := makeModelProfile("mimo-v2.5-pro", ModelFamilyMiMo, QualityTierHigh, 1000000,
		true, false, true, true, 80)
	if err := store.Upsert(&profile); err != nil {
		t.Fatalf("写入模型画像失败: %v", err)
	}
	resolver := NewModelResolver(store, cfgManager)

	mapped, resolved, reason := resolver.ResolveModel(
		"claude-sonnet-5", "ch_test", "messages", "metrics_test", CapabilityFloor{})
	if !resolved {
		t.Fatalf("expected resolved=true, reason=%s", reason)
	}
	if mapped == "legacy-target" {
		t.Fatalf("autoManaged provider should ignore legacy modelMapping, got %q", mapped)
	}
	if mapped != "mimo-v2.5-pro" {
		t.Fatalf("mapped = %q, want mimo-v2.5-pro", mapped)
	}
}

func TestResolveModel_ManualRedirect_ShortCircuits(t *testing.T) {
	upstream := config.UpstreamConfig{
		ChannelUID:   "ch_manual",
		ModelMapping: map[string]string{"claude-opus-4-8": "claude-opus-4-7"},
	}
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{upstream},
	}
	cfgManager, cleanup := createTestConfigManagerForResolver(t, cfg)
	defer cleanup()

	resolver := &ModelResolver{
		profileStore: nil, // 无 ModelProfileStore
		cfgManager:   cfgManager,
	}

	mapped, resolved, reason := resolver.ResolveModel(
		"claude-opus-4-8", "ch_manual", "messages", "any", CapabilityFloor{})

	if !resolved {
		t.Error("expected resolved=true for manual redirect")
	}
	if mapped != "claude-opus-4-7" {
		t.Errorf("expected claude-opus-4-7, got %s", mapped)
	}
	if reason != "manual_redirect" {
		t.Errorf("expected reason 'manual_redirect', got %s", reason)
	}
}

func TestResolveModel_ManualRedirect_NotApplied_WhenNoMapping(t *testing.T) {
	upstream := config.UpstreamConfig{
		ChannelUID:   "ch_manual",
		ModelMapping: nil,
	}
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{upstream},
	}
	cfgManager, cleanup := createTestConfigManagerForResolver(t, cfg)
	defer cleanup()

	resolver := &ModelResolver{
		profileStore: nil,
		cfgManager:   cfgManager,
	}

	mapped, resolved, _ := resolver.ResolveModel(
		"claude-opus-4-8", "ch_manual", "messages", "any", CapabilityFloor{})

	if resolved {
		t.Error("expected resolved=false when no mapping and no store")
	}
	if mapped != "claude-opus-4-8" {
		t.Errorf("expected passthrough, got %s", mapped)
	}
}

func TestResolveModel_NilStore_FailOpen(t *testing.T) {
	resolver := NewModelResolver(nil, nil)
	mapped, resolved, reason := resolver.ResolveModel(
		"claude-sonnet-5", "ch_x", "messages", "mkey", CapabilityFloor{})
	if resolved {
		t.Error("expected resolved=false with nil store")
	}
	if reason != "model_profile_store_unavailable" {
		t.Errorf("expected 'model_profile_store_unavailable', got %s", reason)
	}
	if mapped != "claude-sonnet-5" {
		t.Errorf("expected passthrough, got %s", mapped)
	}
}

func TestResolveModel_ProbeSuccessFalse_Filtered(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("model-x", ModelFamilyClaude, QualityTierPremium, 200000,
			true, true, true, false, 100), // probeOK=false
	}
	resolver := newTestResolver(t, profiles)

	_, resolved, reason := resolver.ResolveModel(
		"claude-opus-4-8", "ch_test", "messages", "metrics_test", CapabilityFloor{})
	if resolved {
		t.Error("expected resolved=false when all candidates have ProbeSuccess=false")
	}
	if reason != "no_capable_model" {
		t.Errorf("expected 'no_capable_model', got %s", reason)
	}
}
