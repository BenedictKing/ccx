package autopilot

import (
	"math"
	"testing"

	"github.com/BenedictKing/ccx/internal/presetstore"
)

// ── DomainStrength 测试（种子矩阵回退链）──

func TestDomainStrength_ProfileOverride(t *testing.T) {
	// ModelProfile 级覆盖优先于种子矩阵
	profile := &ModelProfile{
		ModelFamily: ModelFamilyClaude,
		ModelID:     "claude-fable-4",
		TaskDomainStrengths: map[TaskDomain]float64{
			TaskDomainCodeReview: 0.99, // 用户反馈：这个模型做代码审核特别好
		},
	}

	got := DomainStrength(profile, TaskDomainCodeReview)
	if got != 0.99 {
		t.Errorf("DomainStrength(profile override) = %v, want 0.99", got)
	}
}

func TestDomainStrength_SeedMatrixFallback(t *testing.T) {
	tests := []struct {
		name     string
		family   ModelFamily
		modelID  string
		domain   TaskDomain
		expected float64
	}{
		// ── 国际 ──
		{"claude fable code_review", ModelFamilyClaude, "claude-fable-4", TaskDomainCodeReview, 0.90},
		{"claude fable aesthetics", ModelFamilyClaude, "claude-fable-4", TaskDomainAestheticsUI, 0.90},
		{"claude fable reasoning", ModelFamilyClaude, "claude-fable-4", TaskDomainReasoning, 0.90},
		{"claude fable coding", ModelFamilyClaude, "claude-fable-4", TaskDomainCoding, 0.85},
		{"claude fable writing", ModelFamilyClaude, "claude-fable-4", TaskDomainWriting, 0.85},

		{"claude opus aesthetics", ModelFamilyClaude, "claude-opus-4", TaskDomainAestheticsUI, 0.90},
		{"claude opus code_review", ModelFamilyClaude, "claude-opus-4", TaskDomainCodeReview, 0.85},
		{"claude opus reasoning", ModelFamilyClaude, "claude-opus-4", TaskDomainReasoning, 0.85},

		{"openai gpt-5 seed code_review", ModelFamilyOpenAI, "gpt-5-seed-fallback", TaskDomainCodeReview, 0.90},
		{"openai gpt-5 seed reasoning", ModelFamilyOpenAI, "gpt-5-seed-fallback", TaskDomainReasoning, 0.85},
		{"openai gpt-5 seed aesthetics", ModelFamilyOpenAI, "gpt-5-seed-fallback", TaskDomainAestheticsUI, 0.60},
		{"openai gpt-5 seed coding", ModelFamilyOpenAI, "gpt-5-seed-fallback", TaskDomainCoding, 0.80},

		{"gemini aesthetics", ModelFamilyGemini, "gemini-2.5-pro", TaskDomainAestheticsUI, 0.85},
		{"gemini reasoning", ModelFamilyGemini, "gemini-2.5-pro", TaskDomainReasoning, 0.80},

		// ── 国产 ──
		{"deepseek v4 reasoning", ModelFamilyDeepSeek, "deepseek-v4-pro", TaskDomainReasoning, 0.85},
		{"deepseek v4 coding", ModelFamilyDeepSeek, "deepseek-v4", TaskDomainCoding, 0.80},
		{"deepseek v4 aesthetics", ModelFamilyDeepSeek, "deepseek-v4", TaskDomainAestheticsUI, 0.55},
		{"deepseek v3 coding", ModelFamilyDeepSeek, "deepseek-v3", TaskDomainCoding, 0.75},

		{"glm aesthetics", ModelFamilyGLM, "glm-5-plus", TaskDomainAestheticsUI, 0.80},
		{"glm coding", ModelFamilyGLM, "glm-5-plus", TaskDomainCoding, 0.75},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := &ModelProfile{
				ModelFamily: tt.family,
				ModelID:     tt.modelID,
			}
			evidence := ResolveDomainStrength(profile, tt.domain)
			if evidence.Source != "family_seed" {
				t.Errorf("ResolveDomainStrength(%s, %s, %s) source = %q, want family_seed",
					tt.family, tt.modelID, tt.domain, evidence.Source)
			}
			if evidence.Score != tt.expected {
				t.Errorf("DomainStrength(%s, %s, %s) = %v, want %v",
					tt.family, tt.modelID, tt.domain, evidence.Score, tt.expected)
			}
		})
	}
}

func TestDomainStrength_UnknownFallback05(t *testing.T) {
	tests := []struct {
		name    string
		family  ModelFamily
		modelID string
		domain  TaskDomain
	}{
		{"unknown family", ModelFamilyUnknown, "some-model", TaskDomainCoding},
		{"family not in matrix", ModelFamilyMistral, "mistral-large", TaskDomainCoding},
		{"claude sonnet not in matrix", ModelFamilyClaude, "claude-sonnet-4", TaskDomainCoding},
		{"domain not in seed row", ModelFamilyClaude, "claude-fable-4", TaskDomainTranslation},
		{"openai mini not matched", ModelFamilyOpenAI, "gpt-4o-mini", TaskDomainReasoning},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := &ModelProfile{
				ModelFamily: tt.family,
				ModelID:     tt.modelID,
			}
			got := DomainStrength(profile, tt.domain)
			if got != 0.5 {
				t.Errorf("DomainStrength(%s, %s, %s) = %v, want 0.5 (neutral fallback)",
					tt.family, tt.modelID, tt.domain, got)
			}
		})
	}
}

func TestDomainStrength_OverridePartialDomain(t *testing.T) {
	// 用户只覆盖了部分域，其他域仍走种子矩阵
	profile := &ModelProfile{
		ModelFamily: ModelFamilyClaude,
		ModelID:     "claude-fable-4",
		TaskDomainStrengths: map[TaskDomain]float64{
			TaskDomainTranslation: 0.95, // 用户反馈翻译很好
		},
	}

	// 覆盖的域
	if got := DomainStrength(profile, TaskDomainTranslation); got != 0.95 {
		t.Errorf("DomainStrength(override translation) = %v, want 0.95", got)
	}

	// 未覆盖的域走种子矩阵
	if got := DomainStrength(profile, TaskDomainCodeReview); got != 0.90 {
		t.Errorf("DomainStrength(seed code_review) = %v, want 0.90", got)
	}
}

func TestResolveDomainStrength_CanonicalBenchmarkByVariant(t *testing.T) {
	tests := []struct {
		model  string
		domain TaskDomain
		want   float64
	}{
		{model: "claude-opus-4-8", domain: TaskDomainCoding, want: 0.691},
		{model: "gpt-5.6-terra", domain: TaskDomainReasoning, want: 0.971},
		{model: "gpt-5.6-sol", domain: TaskDomainReasoning, want: 0.971},
		{model: "gpt-5.6-sol", domain: TaskDomainAgentic, want: 0.947},
	}

	for _, tt := range tests {
		t.Run(tt.model+"/"+string(tt.domain), func(t *testing.T) {
			profile := &ModelProfile{ModelID: tt.model, ModelFamily: InferModelFamily(tt.model, "")}
			evidence := ResolveDomainStrength(profile, tt.domain)
			if evidence.Source != "canonical_benchmark" {
				t.Fatalf("Source = %q, want canonical_benchmark", evidence.Source)
			}
			if math.Abs(evidence.Score-tt.want) > 1e-9 {
				t.Fatalf("Score = %v, want %v", evidence.Score, tt.want)
			}
			if evidence.ProviderQualityFactor != 1 {
				t.Fatalf("ProviderQualityFactor = %v, want 1 without endpoint evidence", evidence.ProviderQualityFactor)
			}
			if evidence.BenchmarkLane != "provisional" || evidence.BenchmarkVerifiedAt != "2026-07-25" {
				t.Fatalf("benchmark metadata = lane %q date %q", evidence.BenchmarkLane, evidence.BenchmarkVerifiedAt)
			}
			if evidence.EvidenceConfidence != 0.625 {
				t.Fatalf("EvidenceConfidence = %v, want 0.625", evidence.EvidenceConfidence)
			}
		})
	}
}

func TestResolveDomainStrength_AppliesProviderQualityAsDownwardFactor(t *testing.T) {
	profile := &ModelProfile{
		ModelID:                   "gpt-5.6-sol",
		ModelFamily:               ModelFamilyOpenAI,
		ProviderQualityScore:      0.8,
		ProviderQualityConfidence: 0.75,
	}
	evidence := ResolveDomainStrength(profile, TaskDomainReasoning)
	// factor = 1 - 0.75 * (1 - 0.8) = 0.85; effective = 0.971 * 0.85.
	if math.Abs(evidence.ProviderQualityFactor-0.85) > 1e-9 {
		t.Fatalf("ProviderQualityFactor = %v, want 0.85", evidence.ProviderQualityFactor)
	}
	if math.Abs(evidence.Score-0.82535) > 1e-9 {
		t.Fatalf("Score = %v, want 0.82535", evidence.Score)
	}

	profile.ProviderQualityConfidence = 0.49
	lowConfidence := ResolveDomainStrength(profile, TaskDomainReasoning)
	if lowConfidence.ProviderQualityFactor != 1 || lowConfidence.Score != 0.971 {
		t.Fatalf("低置信度不应下调规范上界: %+v", lowConfidence)
	}
}

func TestResolveDomainStrength_DeepSWEUsesBoundedRelativeCodingSignal(t *testing.T) {
	store := presetstore.Default()
	original := store.Get()
	t.Cleanup(func() {
		store.Swap(original)
	})
	relativeProfile := func(model, pattern string, rawValue, percentile float64) presetstore.ModelBenchmarkProfilePreset {
		return presetstore.ModelBenchmarkProfilePreset{
			Patterns:       []string{pattern},
			CanonicalModel: model,
			BenchmarkEvidence: []presetstore.ModelBenchmarkEvidencePreset{{
				Benchmark:        "deepswe",
				BenchmarkVersion: "v1.1",
				SourceModel:      model,
				Domain:           "coding",
				Metric:           "pass_at_1",
				RawValue:         rawValue,
				CohortPercentile: percentile,
				TaskCount:        100,
				CohortSize:       10,
				Effort:           "xhigh",
				SelectionBasis:   "best_available_effort",
				SourceURL:        "https://example.test/deepswe",
				CapturedAt:       "2026-07-22",
			}},
			Sources:              []string{"https://example.test/deepswe"},
			VerifiedAt:           "2026-07-22",
			Lane:                 "provisional",
			SharedResults:        1,
			ComparableCategories: 1,
			TotalCategories:      1,
		}
	}
	store.Swap(&presetstore.PresetBundle{
		SchemaVersion: original.SchemaVersion,
		DataVersion:   "task-domain-relative-benchmark-test",
		Subscription:  original.Subscription,
		ModelRegistry: &presetstore.ModelRegistryPreset{
			SchemaVersion: 1,
			BenchmarkProfiles: []presetstore.ModelBenchmarkProfilePreset{
				relativeProfile("gpt-5.5-relative", `(?:^|[-/])gpt-5\.5-relative(?=$|@)`, 0.67, 0.75),
				relativeProfile("gpt-5.4-relative", `(?:^|[-/])gpt-5\.4-relative(?=$|@)`, 0.51, 0.25),
			},
		},
	})

	gpt55 := &ModelProfile{ModelID: "gpt-5.5-relative", ModelFamily: ModelFamilyOpenAI}
	evidence := ResolveDomainStrength(gpt55, TaskDomainCoding)
	if evidence.Source != "relative_benchmark" {
		t.Fatalf("Source = %q, want relative_benchmark", evidence.Source)
	}
	if evidence.BenchmarkName != "deepswe" || evidence.BenchmarkVersion != "v1.1" {
		t.Fatalf("benchmark metadata = %+v", evidence)
	}
	if evidence.BenchmarkMetric != "pass_at_1" || evidence.BenchmarkEffort != "xhigh" {
		t.Fatalf("benchmark effort metadata = %+v", evidence)
	}
	if evidence.BenchmarkRawValue != 0.67 || evidence.BenchmarkPercentile != 0.75 {
		t.Fatalf("benchmark values = %+v", evidence)
	}
	if math.Abs(evidence.Score-0.85) > 1e-9 {
		t.Fatalf("bounded score = %v, want 0.85", evidence.Score)
	}

	gpt54 := &ModelProfile{ModelID: "gpt-5.4-relative", ModelFamily: ModelFamilyOpenAI}
	lower := ResolveDomainStrength(gpt54, TaskDomainCoding)
	if lower.Source != "relative_benchmark" || math.Abs(lower.Score-0.75) > 1e-9 {
		t.Fatalf("gpt-5.4 evidence = %+v, want bounded score 0.75", lower)
	}

	review := ResolveDomainStrength(gpt55, TaskDomainCodeReview)
	if review.Source != "family_seed" || review.Score != 0.9 {
		t.Fatalf("DeepSWE coding evidence must not leak to code review: %+v", review)
	}
}

func TestResolveDomainStrength_CategoryScoreWinsOverRelativeEvidence(t *testing.T) {
	profile := &ModelProfile{ModelID: "gpt-5.6-sol", ModelFamily: ModelFamilyOpenAI}
	evidence := ResolveDomainStrength(profile, TaskDomainCoding)
	if evidence.Source != "canonical_benchmark" || math.Abs(evidence.Score-0.55) > 1e-9 {
		t.Fatalf("evidence = %+v, want existing category score", evidence)
	}
}

func TestResolveDomainStrength_OverrideAndFallbackPriority(t *testing.T) {
	profile := &ModelProfile{
		ModelID:     "claude-opus-4-8",
		ModelFamily: ModelFamilyClaude,
		TaskDomainStrengths: map[TaskDomain]float64{
			TaskDomainCoding: 0.99,
		},
	}

	override := ResolveDomainStrength(profile, TaskDomainCoding)
	if override.Source != "endpoint_override" || override.Score != 0.99 {
		t.Fatalf("override = %+v, want endpoint override 0.99", override)
	}
	writing := ResolveDomainStrength(profile, TaskDomainWriting)
	if writing.Source != "family_seed" || writing.Score != 0.85 {
		t.Fatalf("writing = %+v, want family seed 0.85", writing)
	}
	translation := ResolveDomainStrength(profile, TaskDomainTranslation)
	if translation.Source != "neutral" || translation.Score != 0.5 {
		t.Fatalf("translation = %+v, want neutral 0.5", translation)
	}
}

func TestResolveDomainStrength_MultimodalProxyHasLowerConfidence(t *testing.T) {
	evidence := ResolveDomainStrength(&ModelProfile{
		ModelID:     "claude-opus-4-8",
		ModelFamily: ModelFamilyClaude,
	}, TaskDomainAestheticsUI)
	if evidence.Score != 0.902 {
		t.Fatalf("Score = %v, want 0.902", evidence.Score)
	}
	if evidence.EvidenceConfidence != 0.3125 {
		t.Fatalf("EvidenceConfidence = %v, want 0.3125", evidence.EvidenceConfidence)
	}
}

// ── InferTaskDomain 测试（确定性推导各优先级）──

func TestInferTaskDomain_ExplicitHeader(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		expected TaskDomain
	}{
		{"exact enum", "code_review", TaskDomainCodeReview},
		{"uppercase", "CODE_REVIEW", TaskDomainCodeReview},
		{"mixed case with spaces", "  Reasoning  ", TaskDomainReasoning},
		{"aesthetics_ui", "aesthetics_ui", TaskDomainAestheticsUI},
		{"translation", "translation", TaskDomainTranslation},
		{"agentic", "agentic", TaskDomainAgentic},
		{"general", "general", TaskDomainGeneral},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hints := DomainHints{ExplicitDomain: tt.domain}
			got := InferTaskDomain(hints)
			if got != tt.expected {
				t.Errorf("InferTaskDomain(explicit=%q) = %s, want %s",
					tt.domain, got, tt.expected)
			}
		})
	}
}

func TestInferTaskDomain_ExplicitOverridesEverything(t *testing.T) {
	// 显式 header 即使与 system prompt 矛盾，也应优先
	hints := DomainHints{
		ExplicitDomain: "translation",
		SystemPrompt:   "请帮我进行代码审核，找出所有 bug",
	}
	got := InferTaskDomain(hints)
	if got != TaskDomainTranslation {
		t.Errorf("InferTaskDomain(explicit overrides prompt) = %s, want translation", got)
	}
}

func TestInferTaskDomain_SystemPromptKeywords(t *testing.T) {
	tests := []struct {
		name     string
		prompt   string
		expected TaskDomain
	}{
		{"code review english", "Please do a code review of this PR", TaskDomainCodeReview},
		{"code review chinese", "请帮我审查代码中的问题", TaskDomainCodeReview},
		{"code audit", "Perform a code audit on this module", TaskDomainCodeReview},
		{"find bugs", "Find bugs in this function", TaskDomainCodeReview},
		{"UI design", "设计一个美观的 UI 界面", TaskDomainAestheticsUI},
		{"tailwind", "用 Tailwind 写一个登录页面", TaskDomainAestheticsUI},
		{"css styling", "调整 CSS 样式让页面更好看", TaskDomainAestheticsUI},
		{"translation", "请将这段话翻译成英文", TaskDomainTranslation},
		{"algorithm", "实现一个高效的排序算法", TaskDomainReasoning},
		{"math proof", "证明这个数学定理", TaskDomainReasoning},
		{"writing", "帮我写一篇技术博客文章", TaskDomainWriting},
		{"implement", "实现这个 REST API 的 CRUD 功能", TaskDomainCoding},
		{"agent workflow", "Build a multi-step agent workflow with tool use", TaskDomainAgentic},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hints := DomainHints{SystemPrompt: tt.prompt}
			got := InferTaskDomain(hints)
			if got != tt.expected {
				t.Errorf("InferTaskDomain(prompt=%q) = %s, want %s",
					tt.prompt, got, tt.expected)
			}
		})
	}
}

func TestInferTaskDomain_ToolSetCharacteristics(t *testing.T) {
	tests := []struct {
		name     string
		tools    []string
		hasDiff  bool
		expected TaskDomain
	}{
		{"read-only tools with diff", []string{"read", "grep", "git_diff"}, true, TaskDomainCodeReview},
		{"read-only tools without diff", []string{"read", "grep"}, false, TaskDomainGeneral},
		{"mixed tools with diff", []string{"read", "write", "edit"}, true, TaskDomainGeneral},
		{"empty tools with diff", []string{}, true, TaskDomainGeneral},
		{"read-only tools case insensitive", []string{"Read", "Grep"}, true, TaskDomainCodeReview},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hints := DomainHints{
				ToolNames:      tt.tools,
				HasDiffContext: tt.hasDiff,
			}
			got := InferTaskDomain(hints)
			if got != tt.expected {
				t.Errorf("InferTaskDomain(tools=%v, diff=%v) = %s, want %s",
					tt.tools, tt.hasDiff, got, tt.expected)
			}
		})
	}
}

func TestInferTaskDomain_FileExtensions(t *testing.T) {
	tests := []struct {
		name     string
		exts     []string
		expected TaskDomain
	}{
		{"vue file", []string{".vue"}, TaskDomainAestheticsUI},
		{"css file", []string{".css"}, TaskDomainAestheticsUI},
		{"scss file", []string{".scss"}, TaskDomainAestheticsUI},
		{"svelte file", []string{".svelte"}, TaskDomainAestheticsUI},
		{"mixed frontend", []string{".ts", ".vue", ".go"}, TaskDomainAestheticsUI},
		{"go file", []string{".go"}, TaskDomainGeneral},
		{"python file", []string{".py"}, TaskDomainGeneral},
		{"empty exts", []string{}, TaskDomainGeneral},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hints := DomainHints{FileExtensions: tt.exts}
			got := InferTaskDomain(hints)
			if got != tt.expected {
				t.Errorf("InferTaskDomain(exts=%v) = %s, want %s",
					tt.exts, got, tt.expected)
			}
		})
	}
}

func TestInferTaskDomain_PriorityOrder(t *testing.T) {
	// 同时有多个信号，验证优先级：explicit > prompt > tools > exts
	hints := DomainHints{
		ExplicitDomain: "",                       // 无显式声明
		SystemPrompt:   "请帮我审查代码",                // → code_review
		ToolNames:      []string{"read", "grep"}, // → 搭配 diff 才生效
		HasDiffContext: true,
		FileExtensions: []string{".vue"}, // → aesthetics_ui
	}
	got := InferTaskDomain(hints)
	if got != TaskDomainCodeReview {
		t.Errorf("InferTaskDomain(priority) = %s, want code_review (prompt > tools+diff)", got)
	}
}

func TestInferTaskDomain_AllSignalsEmpty(t *testing.T) {
	// 所有信号为空 → general
	hints := DomainHints{}
	got := InferTaskDomain(hints)
	if got != TaskDomainGeneral {
		t.Errorf("InferTaskDomain(empty) = %s, want general", got)
	}
}

func TestInferTaskDomain_InvalidExplicitFallsThrough(t *testing.T) {
	// 无法识别的显式域值应回退到后续信号
	hints := DomainHints{
		ExplicitDomain: "unknown_domain_xyz",
		SystemPrompt:   "请翻译这段话",
	}
	got := InferTaskDomain(hints)
	if got != TaskDomainTranslation {
		t.Errorf("InferTaskDomain(invalid explicit + prompt) = %s, want translation", got)
	}
}

func TestInferTaskDomain_Deterministic(t *testing.T) {
	// 同一输入必须永远返回相同结果
	hints := DomainHints{
		SystemPrompt:   "实现一个 agent 工作流",
		ToolNames:      []string{"read", "write"},
		FileExtensions: []string{".go", ".py"},
	}

	first := InferTaskDomain(hints)
	for i := 0; i < 100; i++ {
		got := InferTaskDomain(hints)
		if got != first {
			t.Fatalf("InferTaskDomain non-deterministic: iteration %d got %s, want %s", i, got, first)
		}
	}
}

// ── EffortQualityBonus 测试 ──

func TestEffortQualityBonus_AllLevels(t *testing.T) {
	tests := []struct {
		level    EffortLevel
		expected float64
	}{
		{EffortOff, 0.0},
		{EffortMinimal, 0.2},
		{EffortLow, 0.4},
		{EffortMedium, 0.6},
		{EffortHigh, 0.9},
		{EffortMax, 1.0},
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			got := EffortQualityBonus(tt.level)
			if got != tt.expected {
				t.Errorf("EffortQualityBonus(%s) = %v, want %v", tt.level, got, tt.expected)
			}
		})
	}
}

func TestEffortQualityBonus_InvalidLevel(t *testing.T) {
	got := EffortQualityBonus(EffortLevel("ultra"))
	if got != 0.0 {
		t.Errorf("EffortQualityBonus(invalid) = %v, want 0.0", got)
	}
}

func TestEffortQualityBonus_Monotonic(t *testing.T) {
	// bonus 应严格递增
	levels := AllEffortLevels()
	for i := 1; i < len(levels); i++ {
		prev := EffortQualityBonus(levels[i-1])
		curr := EffortQualityBonus(levels[i])
		if curr <= prev {
			t.Errorf("EffortQualityBonus not monotonic: %s=%v >= %s=%v",
				levels[i-1], prev, levels[i], curr)
		}
	}
}

// ── 辅助函数测试 ──

func TestBuildSeedKey(t *testing.T) {
	tests := []struct {
		name     string
		family   ModelFamily
		modelID  string
		expected seedDomainKey
	}{
		{"claude fable", ModelFamilyClaude, "claude-fable-4", "claude/fable"},
		{"claude opus", ModelFamilyClaude, "claude-opus-4", "claude/opus"},
		{"claude mythos", ModelFamilyClaude, "claude-mythos-4", "claude/opus"},
		{"claude sonnet no match", ModelFamilyClaude, "claude-sonnet-4", ""},
		{"openai gpt-5", ModelFamilyOpenAI, "gpt-5.4", "openai/gpt-5"},
		{"openai gpt-4o no match", ModelFamilyOpenAI, "gpt-4o", ""},
		{"gemini 2", ModelFamilyGemini, "gemini-2.5-pro", "gemini/gemini-2"},
		{"deepseek v4", ModelFamilyDeepSeek, "deepseek-v4-pro", "deepseek/v4"},
		{"deepseek v3", ModelFamilyDeepSeek, "deepseek-v3", "deepseek/v3"},
		{"glm-5", ModelFamilyGLM, "glm-5-plus", "glm/glm-5"},
		{"unknown family", ModelFamilyUnknown, "some-model", ""},
		{"mistral not in matrix", ModelFamilyMistral, "mistral-large", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSeedKey(tt.family, tt.modelID)
			if got != tt.expected {
				t.Errorf("buildSeedKey(%s, %s) = %q, want %q",
					tt.family, tt.modelID, got, tt.expected)
			}
		})
	}
}

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		input    string
		expected TaskDomain
	}{
		{"code_review", TaskDomainCodeReview},
		{"CODE_REVIEW", TaskDomainCodeReview},
		{"  reasoning  ", TaskDomainReasoning},
		{"invalid", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeDomain(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeDomain(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestAllTaskDomains_Count(t *testing.T) {
	domains := AllTaskDomains()
	if len(domains) != 8 {
		t.Errorf("AllTaskDomains() returned %d domains, want 8", len(domains))
	}
}

func TestAllEffortLevels_Count(t *testing.T) {
	levels := AllEffortLevels()
	if len(levels) != 6 {
		t.Errorf("AllEffortLevels() returned %d levels, want 6", len(levels))
	}
}

func TestSeedDomainMatrix_Coverage(t *testing.T) {
	// 验证种子矩阵中所有 key 都是合法的
	for key, matrix := range seedDomainMatrix {
		if len(matrix) == 0 {
			t.Errorf("seed key %q has empty domain matrix", key)
		}
		for domain := range matrix {
			switch domain {
			case TaskDomainAestheticsUI, TaskDomainCodeReview, TaskDomainCoding,
				TaskDomainReasoning, TaskDomainWriting, TaskDomainTranslation,
				TaskDomainAgentic, TaskDomainGeneral:
				// 合法
			default:
				t.Errorf("seed key %q has invalid domain %q", key, domain)
			}
		}
	}
}
