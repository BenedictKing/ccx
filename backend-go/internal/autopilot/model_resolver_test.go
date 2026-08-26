package autopilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		DocumentNeed:  true,
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
	if !floor.NeedsDocument {
		t.Error("NeedsDocument should be true")
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
		empty.NeedsDocument || empty.NeedsToolCalls || empty.MinQualityTier != "" || empty.QualityBenefitCap != "" {
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

func TestFilterByCapabilityFloor_DropsWithoutDocumentSupport(t *testing.T) {
	withDoc := makeModelProfile("model-doc", ModelFamilyClaude, QualityTierPremium, 200000,
		true, true, true, true, 100)
	withDoc.SupportsDocument = true
	withoutDoc := makeModelProfile("model-nodoc", ModelFamilyClaude, QualityTierPremium, 200000,
		true, true, true, true, 100)

	floor := CapabilityFloor{NeedsDocument: true}
	eligible := filterByCapabilityFloor([]ModelProfile{withDoc, withoutDoc}, floor)
	if len(eligible) != 1 || eligible[0].ModelID != "model-doc" {
		t.Fatalf("expected only model-doc, got %+v", eligible)
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

func TestRankEligibleModels_FrontierRejectsEqualQualityCostPremium(t *testing.T) {
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
		t.Fatalf("balanced 应取更低公开成本，got %s", got)
	}

	// 两者质量信号完全相同（同档、无基准证据）：model-fast 被 model-cheap 支配而不在前沿上，
	// quality_first 车道同样不会为等质量候选付 10 倍成本；延迟不是前沿维度，不再参与。
	qualityConfig := baseConfig
	qualityConfig.AutopilotRouting = config.DefaultAutopilotRoutingConfig()
	qualityConfig.AutopilotRouting.CostPreference.Mode = "quality_first"
	qualityFirst := newTestResolverWithConfig(t, profiles, qualityConfig)
	if got := qualityFirst.rankEligibleModels(profiles, "claude-fable-5", "ch_test", "messages", CapabilityFloor{}).profile.ModelID; got != "model-cheap" {
		t.Fatalf("frontier quality_first 同样拒绝为等质量付 10 倍成本，got %s", got)
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
			target, resolved, reason := resolver.ResolveModel(
				tt.profile.Model, "ch_test", "messages", "metrics_test", floor)
			if !resolved || target.Model != tt.wantModel {
				t.Fatalf("ResolveModel() = (%q, %v, %q), want %q", target.Model, resolved, reason, tt.wantModel)
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

func TestRankEligibleModels_DomesticModelsFrontierVsFallback(t *testing.T) {
	// Frontier 默认启用后的两种路径：
	// - kimi-k3 无公开定价 → 可比成本候选不足 → fail-open 回退旧链，qualityRank 主导，k3 胜
	// - glm-5.2 与 deepseek-v4-pro 质量置信区间重叠但成本差约 4.4 倍，
	//   balanced 车道溢价帽拒绝升级为重叠质量付费，便宜的 deepseek-v4-pro 胜
	tests := []struct {
		name      string
		modelID   string
		family    ModelFamily
		context   int
		vision    bool
		toolCalls bool
		expected  string
	}{
		{
			// mythos-preview 仍无公开定价 → frontier 可比成本不足 → fail-open 回退旧链，
			// qualityRank 主导（Claude 系兜底 premium）胜出 deepseek-v4-pro（high）
			name:      "Claude Mythos Preview（无定价，回退旧链）",
			modelID:   "claude-mythos-preview",
			family:    ModelFamilyClaude,
			context:   1_000_000,
			vision:    true,
			toolCalls: true,
			expected:  "claude-mythos-preview",
		},
		{
			name:      "GLM-5.2（区间重叠，溢价帽选便宜者）",
			modelID:   "glm-5.2",
			family:    ModelFamilyGLM,
			context:   1_048_576,
			toolCalls: true,
			expected:  "deepseek-v4-pro",
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
			if best.ModelID != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, best.ModelID)
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
	// 官方现役 deepseek 模型对：v4-flash 公开价低于 v4-pro，低成本者胜出。
	eligible := []ModelProfile{
		makeModelProfile("deepseek-v4-pro", ModelFamilyDeepSeek, QualityTierNormal, 1000000,
			true, false, true, true, 0),
		makeModelProfile("deepseek-v4-flash", ModelFamilyDeepSeek, QualityTierNormal, 1000000,
			true, false, true, true, 0),
	}

	best := rankTestModels(eligible, "claude-sonnet-5")
	if best.ModelID != "deepseek-v4-flash" {
		t.Errorf("expected lower-cost deepseek-v4-flash, got %s", best.ModelID)
	}
}

func TestRankEligibleModels_FrontierCostPremiumCapsProviderQualityOrder(t *testing.T) {
	// compshare 质量顺序（glm-5.1 优先）证据仍在，但三者在 compshare 的成本
	//  multiplier 为 6x/5x/2x，质量置信区间重叠；balanced 车道的溢价帽拒绝
	// 为重叠质量付 5-6 倍成本，最便宜的 MiniMax-M2.7 在前沿上胜出。
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
	if best.profile.ModelID != "MiniMax-M2.7" {
		t.Fatalf("expected MiniMax-M2.7 (premium cap rejects 5-6x cost for overlapping quality), got %s", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "frontier:balanced") {
		t.Fatalf("expected frontier balanced selection note, got %q", best.frontierNote)
	}
}

func TestRankEligibleModels_FrontierBenchmarkOverridesVersion(t *testing.T) {
	// k2.6 与 k2.7-code 同族同档同成本，但规范基准分 k2.7-code（54）低于 k2.6，
	// 前沿等成本点按质量分取舍，k2.6 胜出；旧链的"未声明顺序时优先较高版本"
	// 启发式不再主导。
	eligible := []ModelProfile{
		makeModelProfile("kimi-k2.6", ModelFamilyKimi, QualityTierHigh, 262144,
			true, true, true, true, 0),
		makeModelProfile("kimi-k2.7-code", ModelFamilyKimi, QualityTierHigh, 262144,
			true, true, true, true, 0),
	}

	best := rankTestModels(eligible, "claude-sonnet-5")
	if best.ModelID != "kimi-k2.6" {
		t.Fatalf("等成本时应取基准分更高的 kimi-k2.6，got %s", best.ModelID)
	}
}

func TestCompareModelVersionDoesNotCrossFamily(t *testing.T) {
	left := rankedModelCandidate{profile: ModelProfile{ModelFamily: ModelFamilyKimi}, versionLineage: "k2", versionNumbers: []int{2, 7}}
	right := rankedModelCandidate{profile: ModelProfile{ModelFamily: ModelFamilyGLM}, versionLineage: "glm5", versionNumbers: []int{5, 1}}
	if _, decided := compareModelVersion(left, right); decided {
		t.Fatal("版本号启发式不应跨模型族比较")
	}
}

func TestCompareModelVersionPrefersDatedSnapshot(t *testing.T) {
	// 同族同 lineage 的日期快照（deepseek-v4-flash-0731）是更新的 checkpoint，
	// 版本号兜底应优先于无快照的基础版（deepseek-v4-flash）。
	dated := rankedModelCandidate{
		profile:        ModelProfile{ModelFamily: ModelFamilyDeepSeek, ModelID: "deepseek-v4-flash-0731"},
		versionLineage: modelVersionLineage(ModelFamilyDeepSeek, "deepseek-v4-flash-0731"),
		versionNumbers: modelVersionNumbers(ModelFamilyDeepSeek, "deepseek-v4-flash-0731"),
	}
	base := rankedModelCandidate{
		profile:        ModelProfile{ModelFamily: ModelFamilyDeepSeek, ModelID: "deepseek-v4-flash"},
		versionLineage: modelVersionLineage(ModelFamilyDeepSeek, "deepseek-v4-flash"),
		versionNumbers: modelVersionNumbers(ModelFamilyDeepSeek, "deepseek-v4-flash"),
	}
	if better, decided := compareModelVersion(dated, base); !decided || !better {
		t.Fatalf("dated snapshot should win: better=%v decided=%v (numbers %v vs %v)", better, decided, dated.versionNumbers, base.versionNumbers)
	}
	if better, decided := compareModelVersion(base, dated); decided && better {
		t.Fatal("base version should not beat dated snapshot")
	}
}

func TestRankEligibleModels_PrefersProviderRelativeCostWhenQualityOrderIncomplete(t *testing.T) {
	// MiniMax-M2.7(2x) 与 deepseek-v4-flash(1x) 同 Normal 档、跨族、均无质量优先级
	// 证据；质量序不完整时由 provider 相对成本主导：1 次优于 2 次，flash 胜出。
	eligible := []ModelProfile{
		makeModelProfile("MiniMax-M2.7", ModelFamilyMiniMax, QualityTierNormal, 204800,
			true, false, true, true, 0),
		makeModelProfile("deepseek-v4-flash", ModelFamilyDeepSeek, QualityTierNormal, 1000000,
			true, false, true, true, 0),
	}
	resolver := newTestResolverWithConfig(t, eligible, config.Config{Upstream: []config.UpstreamConfig{{
		ChannelUID: "ch_test", ProviderID: "compshare", BaseURL: "https://cp.compshare.cn", ServiceType: "claude",
	}}})

	orders := [][]ModelProfile{eligible, {eligible[1], eligible[0]}}
	for _, order := range orders {
		best := resolver.rankEligibleModels(order, "claude-sonnet-5", "ch_test", "messages", CapabilityFloor{})
		if best.profile.ModelID != "deepseek-v4-flash" {
			t.Fatalf("expected deepseek-v4-flash (1 次优于 2 次 when quality tier metadata is incomplete), got %s", best.profile.ModelID)
		}
		if !best.providerCostKnown || best.providerCostMultiplier != 1 || best.providerModelQualityComparable {
			t.Fatalf("ranking evidence = %+v, want cost multiplier 1 with inactive quality priority", best)
		}
	}
}

func TestRankEligibleModels_UnknownProviderCostFallsBackToPublicPrice(t *testing.T) {
	// deepseek-v4-flash 在 compshare 倍率表内（1x），deepseek-v4-pro 在表外无倍率
	// 证据；成本证据不完整时回退公开价比较，flash 公开价更低而胜出。
	eligible := []ModelProfile{
		makeModelProfile("deepseek-v4-pro", ModelFamilyDeepSeek, QualityTierNormal, 1000000,
			true, false, true, true, 0),
		makeModelProfile("deepseek-v4-flash", ModelFamilyDeepSeek, QualityTierNormal, 1000000,
			true, false, true, true, 0),
	}
	resolver := newTestResolverWithConfig(t, eligible, config.Config{Upstream: []config.UpstreamConfig{{
		ChannelUID: "ch_test", ProviderID: "compshare", BaseURL: "https://cp.compshare.cn", ServiceType: "claude",
	}}})

	best := resolver.rankEligibleModels(eligible, "claude-sonnet-5", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "deepseek-v4-flash" {
		t.Fatalf("expected deepseek-v4-flash (表外模型回退公开价后 flash 更便宜), got %s", best.profile.ModelID)
	}
}

func TestProviderModelCostMultiplierInfersLegacyCompshareURL(t *testing.T) {
	multiplier, source, found := providerModelCostMultiplier("GLM-5.2", &config.UpstreamConfig{
		BaseURL: "https://cp.compshare.cn", ServiceType: "claude",
	})
	if !found || multiplier != 6 || source != "provider_template:compshare" {
		t.Fatalf("providerModelCostMultiplier() = %v, %q, %v; want 6, compshare, true", multiplier, source, found)
	}
}

// ── ResolveModel 端到端测试 ──

func TestResolveModel_NoProfiles_ReturnsFalse(t *testing.T) {
	resolver := newTestResolver(t, nil)
	target, resolved, reason := resolver.ResolveModel(
		"claude-opus-4-8", "ch_empty", "messages", "mkey", CapabilityFloor{})
	if resolved {
		t.Error("expected resolved=false when no profiles")
	}
	if target.Model != "claude-opus-4-8" {
		t.Errorf("expected passthrough model, got %s", target.Model)
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
	target, resolved, reason := resolver.ResolveModel(
		"claude-opus-4-8", "ch_test", "messages", "metrics_test", floor)
	if resolved {
		t.Error("expected resolved=false when all filtered")
	}
	if reason != "no_capable_model" {
		t.Errorf("expected reason 'no_capable_model', got %s", reason)
	}
	if target.Model != "claude-opus-4-8" {
		t.Errorf("expected passthrough model, got %s", target.Model)
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
	target, resolved, reason := resolver.ResolveModel(
		"claude-sonnet-5", "ch_test", "messages", "metrics_test", floor)
	if !resolved {
		t.Error("expected resolved=true")
	}
	if target.Model != "claude-sonnet-4-6" {
		t.Errorf("expected same-family claude-sonnet-4-6, got %s", target.Model)
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestResolveModel_CompshareInventoryFrontierByFloor(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("glm-5.2", ModelFamilyGLM, QualityTierPremium, 1048576,
			true, false, true, true, 0),
		makeModelProfile("glm-5.1", ModelFamilyGLM, QualityTierHigh, 202800,
			true, false, true, true, 0),
		makeModelProfile("MiniMax-M2.7", ModelFamilyMiniMax, QualityTierNormal, 204800,
			true, false, true, true, 0),
		makeModelProfile("kimi-k2.6", ModelFamilyKimi, QualityTierHigh, 262144,
			true, false, true, true, 0),
		makeModelProfile("deepseek-v4-flash", ModelFamilyDeepSeek, QualityTierNormal, 1000000,
			true, false, true, true, 0),
		makeModelProfile("deepseek-v4-flash-0731", ModelFamilyDeepSeek, QualityTierNormal, 1000000,
			true, false, true, true, 0),
	}
	resolver := newTestResolverWithConfig(t, profiles, config.Config{Upstream: []config.UpstreamConfig{{
		ChannelUID: "ch_test", ProviderID: "compshare", BaseURL: "https://cp.compshare.cn", ServiceType: "claude",
		AutoManaged: true,
	}}})

	// Frontier 默认启用后按能力下界分车道：
	// - Normal 下界：全部候选可比较，balanced 前沿最廉价可比点是同 1x 同公开价的
	//   deepseek-v4-flash 对，日期快照 0731 为更新 checkpoint，优先胜出
	//   （DeepSeek-V3.2 已从优云下架）
	// - High 下界 + 收益帽 High：effort 级实测豁免（model × effort 组合准入）后，
	//   deepseek-v4-flash 凭 max 档实测 52.7 达 high 边界进入候选，glm-5.2 的
	//   Premium 溢价被收益帽截断，balanced 前沿以更廉价的 deepseek-v4-flash 胜出
	//   （质量下限是准入门槛而非排序标准，过线后按性价比选）
	floors := []struct {
		floor    CapabilityFloor
		expected string
	}{
		{
			floor:    CapabilityFloor{MinContextTokens: 39_561, MinQualityTier: QualityTierNormal},
			expected: "deepseek-v4-flash-0731",
		},
		{
			floor: CapabilityFloor{
				MinContextTokens: 39_561, MinQualityTier: QualityTierHigh,
				QualityBenefitCap: QualityTierHigh,
			},
			expected: "deepseek-v4-flash",
		},
	}
	for _, tt := range floors {
		target, resolved, reason := resolver.ResolveModel(
			"claude-sonnet-5", "ch_test", "messages", "metrics_test", tt.floor)
		if !resolved || target.Model != tt.expected {
			t.Fatalf("ResolveModel(%+v) = (%q, %v, %q), want %s", tt.floor, target.Model, resolved, reason, tt.expected)
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

	target, resolved, reason := resolver.ResolveModel(
		"gpt-5.6-sol", "ch_test", "responses", "metrics_test", floor)
	if !resolved || target.Model != "glm-5.2" {
		t.Fatalf("ResolveModel() = (%q, %v, %q), want premium glm-5.2", target.Model, resolved, reason)
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
		MinQualityTier:   QualityTierHigh,
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
			target, resolved, reason := resolver.ResolveModel(
				tt.request, "ch_test", "messages", "metrics_test", floor)
			if !resolved || target.Model != tt.want {
				t.Fatalf("ResolveModel() = (%q, %v, %q), want %q", target.Model, resolved, reason, tt.want)
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

	target, resolved, reason := resolver.ResolveModel(
		"gpt-5.6-sol", "ch_test", "responses", "metrics_test",
		CapabilityFloor{MinQualityTier: QualityTierPremium, NeedsReasoning: true, NeedsToolCalls: true})
	if !resolved || target.Model != "glm-5.2" {
		t.Fatalf("ResolveModel() = (%q, %v, %q), want refreshed glm-5.2 capabilities", target.Model, resolved, reason)
	}
	refreshed := store.Get("ch_test", "responses", "metrics_test", "glm-5.2")
	// glm-5.2 常规口径 22.2 → low 档
	if refreshed == nil || refreshed.QualityTier != QualityTierLow ||
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

	target, resolved, reason := resolver.ResolveModel(
		"claude-opus-4-8", "ch_test", "messages", "metrics_test",
		CapabilityFloor{
			MinContextTokens: 200000,
			MinQualityTier:   QualityTierPremium,
			NeedsReasoning:   true,
			NeedsVision:      true,
			NeedsToolCalls:   true,
		})
	if !resolved || target.Model != "k3" {
		t.Fatalf("ResolveModel() = (%q, %v, %q), want vision-capable k3", target.Model, resolved, reason)
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

	target, found, reason := resolver.ResolveModelAnyEndpoint("claude-sonnet-5", "ch_test", "messages")
	if !found {
		t.Fatalf("expected found=true, reason=%s", reason)
	}
	if target.Model == "" || target.Model == "claude-sonnet-5" {
		t.Fatalf("expected request model to be target.Model to discovered model, got %q", target.Model)
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

	target, resolved, reason := resolver.ResolveModel(
		"claude-sonnet-5", "ch_test", "messages", "metrics_test", CapabilityFloor{})
	if !resolved {
		t.Fatalf("expected resolved=true, reason=%s", reason)
	}
	if target.Model == "legacy-target" {
		t.Fatalf("autoManaged provider should ignore legacy modelMapping, got %q", target.Model)
	}
	if target.Model != "mimo-v2.5-pro" {
		t.Fatalf("target.Model = %q, want mimo-v2.5-pro", target.Model)
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

	target, resolved, reason := resolver.ResolveModel(
		"claude-opus-4-8", "ch_manual", "messages", "any", CapabilityFloor{})

	if !resolved {
		t.Error("expected resolved=true for manual redirect")
	}
	if target.Model != "claude-opus-4-7" {
		t.Errorf("expected claude-opus-4-7, got %s", target.Model)
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

	target, resolved, _ := resolver.ResolveModel(
		"claude-opus-4-8", "ch_manual", "messages", "any", CapabilityFloor{})

	if resolved {
		t.Error("expected resolved=false when no mapping and no store")
	}
	if target.Model != "claude-opus-4-8" {
		t.Errorf("expected passthrough, got %s", target.Model)
	}
}

func TestResolveModel_NilStore_FailOpen(t *testing.T) {
	resolver := NewModelResolver(nil, nil)
	target, resolved, reason := resolver.ResolveModel(
		"claude-sonnet-5", "ch_x", "messages", "mkey", CapabilityFloor{})
	if resolved {
		t.Error("expected resolved=false with nil store")
	}
	if reason != "model_profile_store_unavailable" {
		t.Errorf("expected 'model_profile_store_unavailable', got %s", reason)
	}
	if target.Model != "claude-sonnet-5" {
		t.Errorf("expected passthrough, got %s", target.Model)
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

// ── ManualRoutingIntent effort 覆盖优先级测试 ──
//
// 覆盖计划要求的三个场景（加上 resolveIntentPinnedEffort 单测）：
//   - 只锁模型不锁 effort：effort 仍由 Autopilot 决定
//   - 模型和 effort 都锁：两者都生效
//   - 客户端显式 off 优先于意图的 effort 覆盖

func TestResolveIntentPinnedEffort_ModelOnlyIntentLeavesEffortToAutopilot(t *testing.T) {
	profile := &RequestProfile{
		IntentEffortPin: &IntentEffortPin{Set: false}, // 意图只锁了模型，Set 保持 false
	}
	floor := BuildCapabilityFloorFromRequestProfile(profile)
	if floor.PinnedEffort != "" {
		t.Errorf("PinnedEffort = %q, want empty when intent only pins model", floor.PinnedEffort)
	}
}

func TestResolveIntentPinnedEffort_ModelAndEffortBothHonored(t *testing.T) {
	profile := &RequestProfile{
		IntentEffortPin: &IntentEffortPin{Effort: EffortHigh, Set: true},
	}
	floor := BuildCapabilityFloorFromRequestProfile(profile)
	if floor.PinnedEffort != EffortHigh {
		t.Errorf("PinnedEffort = %q, want %q when intent pins both model and effort", floor.PinnedEffort, EffortHigh)
	}
}

func TestResolveIntentPinnedEffort_ClientExplicitOffOverridesIntentEffort(t *testing.T) {
	profile := &RequestProfile{
		ClientEffort:         EffortOff,
		ClientEffortExplicit: true,
		IntentEffortPin:      &IntentEffortPin{Effort: EffortHigh, Set: true},
	}
	floor := BuildCapabilityFloorFromRequestProfile(profile)
	if floor.PinnedEffort != "" {
		t.Errorf("PinnedEffort = %q, want empty: client explicit off must win over intent effort", floor.PinnedEffort)
	}
}

func TestResolveIntentPinnedEffort_ClientExplicitNonOffDoesNotBlockIntent(t *testing.T) {
	// 客户端显式声明了非 off 的 effort（例如 low）时，不属于"关闭思考"的最强信号，
	// 意图的 effort 覆盖仍应生效。
	profile := &RequestProfile{
		ClientEffort:         EffortLow,
		ClientEffortExplicit: true,
		IntentEffortPin:      &IntentEffortPin{Effort: EffortHigh, Set: true},
	}
	floor := BuildCapabilityFloorFromRequestProfile(profile)
	if floor.PinnedEffort != EffortHigh {
		t.Errorf("PinnedEffort = %q, want %q: client non-off explicit effort must not block intent pin", floor.PinnedEffort, EffortHigh)
	}
}

// TestResolveEffortVariants_PinnedEffortHonoredWhenSupported 验证 ResolveModel 端到端：
// CapabilityFloor.PinnedEffort 命中模型支持的档位时，直接采纳该档位并标记 EffortDecided=true，
// 从而确保 effort 真正被注入下游（而非仅停留在 CapabilityFloor 层）。
func TestResolveEffortVariants_PinnedEffortHonoredWhenSupported(t *testing.T) {
	profiles := []ModelProfile{
		makeEffortProfile("claude-sonnet-5", ModelFamilyClaude, QualityTierHigh, 1000000,
			true, 100, true, []EffortLevel{EffortLow, EffortMedium, EffortHigh, EffortMax}),
	}
	// ReasoningEffort 全局开关关闭，验证 PinnedEffort 不受该开关影响（fail-open 之外的强制路径）。
	cfg := makeRoutingConfig(false, false, nil)
	resolver := newTestResolverWithConfig(t, profiles, cfg)

	floor := CapabilityFloor{PinnedEffort: EffortHigh}
	// 使用自适应别名 "sonnet" 而非精确模型 ID，确保命中 Step 6 的排序/effort 展开路径
	// 而不是 Step 5 的精确匹配短路（精确匹配不会经过 resolveEffortVariants）。
	target, resolved, _ := resolver.ResolveModel(
		"sonnet", "ch_test", "messages", "metrics_test", floor)
	if !resolved {
		t.Fatal("expected resolved=true")
	}
	if target.Effort != EffortHigh {
		t.Errorf("Effort = %q, want %q", target.Effort, EffortHigh)
	}
	if !target.EffortDecided {
		t.Error("expected EffortDecided=true so the pinned effort is actually injected downstream")
	}
}

// TestResolveEffortVariants_PinnedEffortUnsupportedFallsBackToAutopilot 验证模型不支持
// 锁定档位时 fail-open，落回常规展开逻辑而不是硬失败。
func TestResolveEffortVariants_PinnedEffortUnsupportedFallsBackToAutopilot(t *testing.T) {
	profiles := []ModelProfile{
		makeEffortProfile("claude-sonnet-5", ModelFamilyClaude, QualityTierHigh, 1000000,
			true, 100, true, []EffortLevel{EffortLow, EffortMedium}), // 不含 EffortMax
	}
	cfg := makeRoutingConfig(true, false, nil)
	resolver := newTestResolverWithConfig(t, profiles, cfg)

	floor := CapabilityFloor{PinnedEffort: EffortMax} // 模型不支持
	// 同样使用自适应别名 "sonnet"，确保真正落入 resolveEffortVariants 的展开逻辑。
	target, resolved, _ := resolver.ResolveModel(
		"sonnet", "ch_test", "messages", "metrics_test", floor)
	if !resolved {
		t.Fatal("expected resolved=true")
	}
	if target.Effort == EffortMax {
		t.Error("expected fail-open: unsupported pinned effort should not be forced onto the model")
	}
	// fail-open 后仍应落到常规展开逻辑，产生一个已决定的、模型支持的档位。
	if !target.EffortDecided {
		t.Error("expected EffortDecided=true from the regular expansion fallback")
	}
	if target.Effort != EffortLow {
		t.Errorf("Effort = %q, want %q (lowest supported, ExpandVariants=false)", target.Effort, EffortLow)
	}
}

// ── Effort 变体展开测试 ──

// makeEffortProfile 创建带 effort 控制能力的测试 ModelProfile。
func makeEffortProfile(modelID string, family ModelFamily, tier QualityTier, ctxTokens int,
	probeOK bool, latencyMs int64, effortControl bool, effortLevels []EffortLevel) ModelProfile {
	return ModelProfile{
		ChannelUID:            "ch_test",
		ChannelKind:           "messages",
		MetricsKey:            "metrics_test",
		ModelID:               modelID,
		ModelFamily:           family,
		QualityTier:           tier,
		ContextTokens:         ctxTokens,
		SupportsReasoning:     true,
		SupportsVision:        true,
		SupportsToolCalls:     true,
		ProbeSuccess:          probeOK,
		ProbeLatencyMs:        latencyMs,
		SupportsEffortControl: effortControl,
		SupportedEffortLevels: effortLevels,
	}
}

// makeRoutingConfig 构造带 ReasoningEffort 配置的 Config。
// SchemaVersion 设为 99 避免 ConfigManager 迁移覆盖测试值。
func makeRoutingConfig(enabled, expandVariants bool, perTaskClass map[string][]string) config.Config {
	return config.Config{
		AutopilotRouting: config.AutopilotRoutingConfig{
			SchemaVersion: 99,
			ModelMapping: config.ModelMappingRoutingConfig{
				CapabilityFloorEnabled: true,
			},
			ReasoningEffort: config.ReasoningEffortConfig{
				Enabled:        enabled,
				ExpandVariants: expandVariants,
				PerTaskClass:   perTaskClass,
			},
		},
	}
}

func TestResolveEffortVariants_Disabled_ReturnsPassthrough(t *testing.T) {
	profiles := []ModelProfile{
		makeEffortProfile("claude-sonnet-5", ModelFamilyClaude, QualityTierHigh, 1000000,
			true, 100, true, []EffortLevel{EffortLow, EffortMedium, EffortHigh}),
	}
	cfg := makeRoutingConfig(false, false, nil) // Enabled=false
	resolver := newTestResolverWithConfig(t, profiles, cfg)

	target, resolved, _ := resolver.ResolveModel(
		"claude-sonnet-5", "ch_test", "messages", "metrics_test", CapabilityFloor{})
	if !resolved {
		t.Fatal("expected resolved=true")
	}
	if target.EffortDecided {
		t.Error("expected EffortDecided=false when reasoning effort disabled")
	}
	if target.Effort != "" {
		t.Errorf("expected empty Effort, got %s", target.Effort)
	}
}

func TestResolveEffortVariants_NoEffortControl_ReturnsPassthrough(t *testing.T) {
	profiles := []ModelProfile{
		makeEffortProfile("claude-sonnet-5", ModelFamilyClaude, QualityTierHigh, 1000000,
			true, 100, false, nil), // SupportsEffortControl=false
	}
	cfg := makeRoutingConfig(true, false, nil)
	resolver := newTestResolverWithConfig(t, profiles, cfg)

	target, resolved, _ := resolver.ResolveModel(
		"claude-sonnet-5", "ch_test", "messages", "metrics_test", CapabilityFloor{})
	if !resolved {
		t.Fatal("expected resolved=true")
	}
	if target.EffortDecided {
		t.Error("expected EffortDecided=false when model lacks effort control")
	}
}

func TestResolveEffortVariants_ExpandVariantsFalse_ReturnsLowest(t *testing.T) {
	// 注意: ExpandVariants=false 在 JSON 中因 omitempty 被省略，ConfigManager 的
	// overlay 逻辑会保留默认 true。因此该场景直接测试 resolveEffortVariants 方法。
	profile := makeEffortProfile("claude-sonnet-5", ModelFamilyClaude, QualityTierHigh, 1000000,
		true, 100, true, []EffortLevel{EffortLow, EffortMedium, EffortHigh, EffortMax})
	floor := CapabilityFloor{TaskClass: TaskClass("coding")}

	// 手动构造一个带 ExpandVariants=false 的 cfgManager 来测试。
	cfg := config.Config{
		AutopilotRouting: config.AutopilotRoutingConfig{
			SchemaVersion: 99, // 避免迁移覆盖
			ReasoningEffort: config.ReasoningEffortConfig{
				Enabled:        true,
				ExpandVariants: false,
			},
		},
	}
	resolver := newTestResolverWithConfig(t, []ModelProfile{profile}, cfg)
	levels, decided := resolver.resolveEffortVariants(profile, floor)

	if len(levels) != 1 {
		t.Fatalf("expected 1 variant with ExpandVariants=false, got %d", len(levels))
	}
	if !decided[0] {
		t.Error("expected decided=true")
	}
	if levels[0] != EffortLow {
		t.Errorf("expected lowest effort 'low', got '%s'", levels[0])
	}
}

func TestResolveEffortVariants_ExpandVariantsTrue_UsesPerTaskClass(t *testing.T) {
	profiles := []ModelProfile{
		makeEffortProfile("claude-sonnet-5", ModelFamilyClaude, QualityTierHigh, 1000000,
			true, 100, true, []EffortLevel{EffortLow, EffortMedium, EffortHigh, EffortMax}),
	}
	// 只允许 supervisor 任务用 high 和 max
	perTaskClass := map[string][]string{
		"supervisor": {"high", "max"},
	}
	cfg := makeRoutingConfig(true, true, perTaskClass)
	resolver := newTestResolverWithConfig(t, profiles, cfg)

	floor := CapabilityFloor{TaskClass: TaskClass("supervisor")}
	best := resolver.rankEligibleModels(profiles, "claude-sonnet-5", "ch_test", "messages", floor)

	// 验证选出的 effort 在 {high, max} 范围内
	if !best.effortDecided {
		t.Error("expected effortDecided=true")
	}
	if best.effort != EffortHigh && best.effort != EffortMax {
		t.Errorf("expected effort in {high, max}, got '%s'", best.effort)
	}
}

func TestResolveEffortVariants_PerTaskClassIntersectionEmpty_FallbackToLowestAboveFloor(t *testing.T) {
	profiles := []ModelProfile{
		makeEffortProfile("claude-sonnet-5", ModelFamilyClaude, QualityTierHigh, 1000000,
			true, 100, true, []EffortLevel{EffortMedium, EffortHigh}),
	}
	// 配置的档位 {max} 与模型支持的 {medium, high} 无交集
	perTaskClass := map[string][]string{
		"coding": {"max"},
	}
	cfg := makeRoutingConfig(true, false, perTaskClass)
	resolver := newTestResolverWithConfig(t, profiles, cfg)

	floor := CapabilityFloor{TaskClass: TaskClass("coding"), EffortFloor: EffortMedium}
	best := resolver.rankEligibleModels(profiles, "claude-sonnet-5", "ch_test", "messages", floor)

	if !best.effortDecided {
		t.Error("expected effortDecided=true on fallback")
	}
	if best.effort != EffortMedium {
		t.Errorf("expected fallback to 'medium', got '%s'", best.effort)
	}
}

func TestEffortFloorFilter_BelowFloorFilteredOut(t *testing.T) {
	lowModel := makeEffortProfile("model-low", ModelFamilyDeepSeek, QualityTierNormal, 100000,
		true, 100, true, []EffortLevel{EffortLow, EffortMedium})
	highModel := makeEffortProfile("model-high", ModelFamilyDeepSeek, QualityTierNormal, 100000,
		true, 100, true, []EffortLevel{EffortLow, EffortHigh})

	profiles := []ModelProfile{lowModel, highModel}
	// 不限制 PerTaskClass，让两个模型都展开
	cfg := makeRoutingConfig(true, true, nil)
	resolver := newTestResolverWithConfig(t, profiles, cfg)

	// EffortFloor=medium 应该过滤掉两个模型的 low 变体
	floor := CapabilityFloor{EffortFloor: EffortMedium, TaskClass: TaskClass("coding")}
	best := resolver.rankEligibleModels(profiles, "deepseek-v4", "ch_test", "messages", floor)

	if !best.effortDecided {
		t.Error("expected effortDecided=true")
	}
	bestOrd, _ := effortOrdinal[best.effort]
	floorOrd, _ := effortOrdinal[EffortMedium]
	if bestOrd < floorOrd {
		t.Errorf("expected effort >= medium, got '%s'", best.effort)
	}
}

func TestEffortFloorFilter_FailOpen_WhenAllFiltered(t *testing.T) {
	// 模型只有 low 档位，EffortFloor=high 会把所有变体都过滤掉
	profile := makeEffortProfile("model-low-only", ModelFamilyDeepSeek, QualityTierNormal, 100000,
		true, 100, true, []EffortLevel{EffortLow})
	profiles := []ModelProfile{profile}
	cfg := makeRoutingConfig(true, true, nil)
	resolver := newTestResolverWithConfig(t, profiles, cfg)

	floor := CapabilityFloor{EffortFloor: EffortHigh, TaskClass: TaskClass("coding")}
	best := resolver.rankEligibleModels(profiles, "deepseek-v4", "ch_test", "messages", floor)

	// fail-open: 应该仍然返回该候选（而不是 panic 或返回零值）
	if best.profile.ModelID == "" {
		t.Error("expected fail-open: should still return a candidate")
	}
}

func TestBetterRankedModel_AntiEffortInflation_Tiebreak(t *testing.T) {
	// 同模型、同质量分数、同 normalizedCandidateID，高 effort 应该输给低 effort（够用即止）。
	// anti-effort-inflation 分支在 normalizedCandidateID 相同且质量分完全一致时生效。
	sameScoreLow := rankedModelCandidate{
		profile:               ModelProfile{ModelID: "model-x", ModelFamily: ModelFamilyClaude, QualityTier: QualityTierHigh},
		effort:                EffortLow,
		effortDecided:         true,
		qualityRank:           qualityTierRank(QualityTierHigh),
		measuredQualityScore:  0.5,
		normalizedCandidateID: "model-x",
	}
	sameScoreHigh := rankedModelCandidate{
		profile:               ModelProfile{ModelID: "model-x", ModelFamily: ModelFamilyClaude, QualityTier: QualityTierHigh},
		effort:                EffortHigh,
		effortDecided:         true,
		qualityRank:           qualityTierRank(QualityTierHigh),
		measuredQualityScore:  0.5,
		normalizedCandidateID: "model-x",
	}

	if !betterRankedModel(sameScoreLow, sameScoreHigh, CostPrefBalanced) {
		t.Error("expected lower effort to win when all else equal (anti-inflation)")
	}
	if betterRankedModel(sameScoreHigh, sameScoreLow, CostPrefBalanced) {
		t.Error("expected higher effort NOT to win when all else equal (anti-inflation)")
	}
}

func TestBetterRankedModel_AntiEffortInflation_DifferentModelsIgnored(t *testing.T) {
	// 不同模型不应触发 effort anti-inflation 逻辑
	a := rankedModelCandidate{
		profile:               ModelProfile{ModelID: "model-a", QualityTier: QualityTierHigh},
		effort:                EffortHigh,
		effortDecided:         true,
		qualityRank:           qualityTierRank(QualityTierHigh),
		measuredQualityScore:  0.5,
		normalizedCandidateID: "model-a",
	}
	b := rankedModelCandidate{
		profile:               ModelProfile{ModelID: "model-b", QualityTier: QualityTierHigh},
		effort:                EffortLow,
		effortDecided:         true,
		qualityRank:           qualityTierRank(QualityTierHigh),
		measuredQualityScore:  0.5,
		normalizedCandidateID: "model-b",
	}
	// 不同模型、effort 不同，不应因为 effort 低就赢
	// 应该走到 normalizedCandidateID 比较（model-a < model-b 字典序）
	if !betterRankedModel(a, b, CostPrefBalanced) {
		t.Error("expected model-a to win by normalizedCandidateID, not effort")
	}
}

func TestEffortQualityBonus_AppliedToScore(t *testing.T) {
	// 验证 EffortQualityBonus * 0.1 被加到 measuredQualityScore
	profile := makeEffortProfile("claude-sonnet-5", ModelFamilyClaude, QualityTierHigh, 1000000,
		true, 100, true, []EffortLevel{EffortLow, EffortMax})
	cfg := makeRoutingConfig(true, true, nil)
	resolver := newTestResolverWithConfig(t, []ModelProfile{profile}, cfg)

	floor := CapabilityFloor{TaskClass: TaskClass("coding")}
	best := resolver.rankEligibleModels([]ModelProfile{profile}, "claude-sonnet-5", "ch_test", "messages", floor)

	if !best.effortDecided {
		t.Fatal("expected effortDecided=true")
	}
	// Max 应该因 bonus 更高而被选中（同模型唯一候选，bonus 影响分数）
	expectedBonus := EffortQualityBonus(best.effort) * 0.1
	baseScore := measuredProviderQualityScore(profile)
	if best.measuredQualityScore < baseScore+expectedBonus-0.001 {
		t.Errorf("expected bonus applied: score=%.4f, base=%.4f, bonus=%.4f",
			best.measuredQualityScore, baseScore, expectedBonus)
	}
}

// TestMeasuredCostForEffort 覆盖 bestEffort 超注册表档位时的成本键回退。
func TestMeasuredCostForEffort(t *testing.T) {
	t.Run("候选 effort 精确命中", func(t *testing.T) {
		costs := map[EffortLevel]float64{EffortHigh: 2.0, EffortMax: 3.5}
		if got := measuredCostForEffort(costs, EffortHigh); got != 2.0 {
			t.Fatalf("measuredCostForEffort() = %v, want 2.0", got)
		}
	})
	t.Run("evidence 档位超注册表档位时回退最小成本", func(t *testing.T) {
		// evidence 仅测了 ultra（注册表该模型只到 max，候选 effort 不含 ultra）；
		// 候选 max 精确键缺失，应回退到该模型已测档位的最小成本（ultra 的下界）。
		costs := map[EffortLevel]float64{EffortUltra: 2.0}
		if got := measuredCostForEffort(costs, EffortMax); got != 2.0 {
			t.Fatalf("measuredCostForEffort() = %v, want fallback min 2.0", got)
		}
	})
	t.Run("多档实测时取最小成本下界", func(t *testing.T) {
		costs := map[EffortLevel]float64{EffortUltra: 4.0, EffortMax: 2.5}
		if got := measuredCostForEffort(costs, EffortHigh); got != 2.5 {
			t.Fatalf("measuredCostForEffort() = %v, want min 2.5", got)
		}
	})
	t.Run("无实测成本时返回 0", func(t *testing.T) {
		if got := measuredCostForEffort(map[EffortLevel]float64{}, EffortMax); got != 0 {
			t.Fatalf("measuredCostForEffort() = %v, want 0", got)
		}
	})
}
