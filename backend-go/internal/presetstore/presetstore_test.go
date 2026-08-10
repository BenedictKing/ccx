package presetstore

import (
	"math"
	"sync"
	"testing"

	"github.com/BenedictKing/ccx/internal/eventbus"
)

func floatPtr(v float64) *float64 { return &v }

// validBundle 返回一份通过校验的最小 bundle，供各用例按需改坏。
func validBundle() *PresetBundle {
	return &PresetBundle{
		SchemaVersion: CurrentSchemaVersion,
		DataVersion:   "2026.07.10-1",
		Subscription: SubscriptionPreset{
			OriginTypes: []OriginTypeEntry{
				{Value: "official_api", Tier: "first"},
				{Value: "relay", Tier: "second"},
				{Value: "community", Tier: "third"},
				{Value: "unknown", Tier: "unknown"},
			},
			BillingModes:         []string{"token_plan", "unknown"},
			Sources:              []string{"manual", "auto_discovered"},
			AutoRefreshProviders: []string{"openai", "anthropic", "google"},
			NewApiDefaults:       NewApiDefaults{OriginType: "relay", OriginTier: "second", BillingMode: "token_plan"},
			OriginTypeAliases:    map[string]string{"public_benefit": "community"},
		},
	}
}

func TestEmbeddedBundleValid(t *testing.T) {
	b := EmbeddedBundle()
	if err := Validate(b); err != nil {
		t.Fatalf("内置 bundle 应通过校验，得到: %v", err)
	}
	if b.DataVersion == "" {
		t.Error("内置 bundle DataVersion 不能为空")
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*PresetBundle){
		"schema 过高":      func(b *PresetBundle) { b.SchemaVersion = CurrentSchemaVersion + 1 },
		"originTypes 空":  func(b *PresetBundle) { b.Subscription.OriginTypes = nil },
		"tier 非法":        func(b *PresetBundle) { b.Subscription.OriginTypes[0].Tier = "platinum" },
		"value 空":        func(b *PresetBundle) { b.Subscription.OriginTypes[0].Value = "" },
		"重复 value":       func(b *PresetBundle) { b.Subscription.OriginTypes[1].Value = "official_api" },
		"billingModes 空": func(b *PresetBundle) { b.Subscription.BillingModes = nil },
		"sources 空":      func(b *PresetBundle) { b.Subscription.Sources = nil },
		"newApi 引用未知类型":  func(b *PresetBundle) { b.Subscription.NewApiDefaults.OriginType = "ghost" },
		"别名目标未知":         func(b *PresetBundle) { b.Subscription.OriginTypeAliases["x"] = "ghost" },
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			b := validBundle()
			corrupt(b)
			if err := Validate(b); err == nil {
				t.Errorf("%s：期望校验失败，却通过了", name)
			}
		})
	}
}

func TestValidateAcceptsOlderSchema(t *testing.T) {
	b := validBundle()
	b.SchemaVersion = CurrentSchemaVersion - 1 // 更旧 schema 仍可读
	if CurrentSchemaVersion-1 >= 0 {
		if err := Validate(b); err != nil {
			t.Errorf("更旧 schema 应可接受，得到: %v", err)
		}
	}
}

func TestValidateAcceptsBuiltinManifestOpenAIServiceType(t *testing.T) {
	b := validBundle()
	b.BuiltinModelsManifests = &BuiltinModelsManifestPreset{
		SchemaVersion: 1,
		Manifests: []BuiltinModelsManifestEntryPreset{
			{
				BaseURLPattern: "api.example.com/v1",
				ServiceType:    "openai",
				ModelIDs:       []string{"model-a"},
			},
		},
	}
	if err := Validate(b); err != nil {
		t.Fatalf("openai serviceType 应可用于 builtin manifest: %v", err)
	}
}

func TestValidateModelBenchmarkProfiles(t *testing.T) {
	validBenchmark := ModelBenchmarkProfilePreset{
		Patterns:             []string{"gpt-5.6-sol"},
		CanonicalModel:       "gpt-5.6-sol",
		OverallScore:         86,
		CategoryScores:       map[string]float64{"coding": 64.6},
		Sources:              []string{"https://benchlm.ai/methodology"},
		VerifiedAt:           "2026-07-13",
		Lane:                 "provisional",
		SharedResults:        30,
		ComparableCategories: 5,
		TotalCategories:      8,
	}

	b := validBundle()
	b.ModelRegistry = &ModelRegistryPreset{SchemaVersion: 1, BenchmarkProfiles: []ModelBenchmarkProfilePreset{validBenchmark}}
	if err := Validate(b); err != nil {
		t.Fatalf("合法 benchmark profile 未通过校验: %v", err)
	}

	evidenceOnly := validBenchmark
	evidenceOnly.CategoryScores = nil
	evidenceOnly.OverallScore = 0
	evidenceOnly.BenchmarkEvidence = []ModelBenchmarkEvidencePreset{{
		Benchmark:        "deepswe",
		BenchmarkVersion: "v1.1",
		SourceModel:      "gpt-5.6-sol",
		Domain:           "coding",
		Metric:           "pass_at_1",
		RawValue:         0.73,
		Uncertainty:      0.03,
		CohortPercentile: 1,
		TaskCount:        113,
		CohortSize:       16,
		Effort:           "max",
		SelectionBasis:   "best_available_effort",
		SourceURL:        "https://deepswe.datacurve.ai/",
		CapturedAt:       "2026-07-18",
		CostUSD:          floatPtr(0.0042),
	}}
	b.ModelRegistry = &ModelRegistryPreset{SchemaVersion: 1, BenchmarkProfiles: []ModelBenchmarkProfilePreset{evidenceOnly}}
	if err := Validate(b); err != nil {
		t.Fatalf("只含 benchmark evidence 的 profile 未通过校验: %v", err)
	}

	indexEvidence := evidenceOnly
	indexEvidence.BenchmarkEvidence = append([]ModelBenchmarkEvidencePreset(nil), evidenceOnly.BenchmarkEvidence...)
	indexEvidence.BenchmarkEvidence[0].Benchmark = "artificial_analysis"
	indexEvidence.BenchmarkEvidence[0].BenchmarkVersion = "v4.1"
	indexEvidence.BenchmarkEvidence[0].Metric = "coding_index"
	indexEvidence.BenchmarkEvidence[0].RawValue = 74.3
	indexEvidence.BenchmarkEvidence[0].Uncertainty = 0
	b.ModelRegistry = &ModelRegistryPreset{SchemaVersion: 1, BenchmarkProfiles: []ModelBenchmarkProfilePreset{indexEvidence}}
	if err := Validate(b); err != nil {
		t.Fatalf("使用 0-100 原始量纲的 benchmark evidence 未通过校验: %v", err)
	}

	tests := map[string]func(*ModelBenchmarkProfilePreset){
		"缺少 pattern":         func(p *ModelBenchmarkProfilePreset) { p.Patterns = nil },
		"pattern 非法":         func(p *ModelBenchmarkProfilePreset) { p.Patterns = []string{"("} },
		"缺少 canonical model": func(p *ModelBenchmarkProfilePreset) { p.CanonicalModel = "" },
		"总分越界":               func(p *ModelBenchmarkProfilePreset) { p.OverallScore = 101 },
		"类别分越界":              func(p *ModelBenchmarkProfilePreset) { p.CategoryScores["coding"] = -1 },
		"缺少类别向量":             func(p *ModelBenchmarkProfilePreset) { p.CategoryScores = nil },
		"缺少来源":               func(p *ModelBenchmarkProfilePreset) { p.Sources = nil },
		"核验日期非法":             func(p *ModelBenchmarkProfilePreset) { p.VerifiedAt = "20260713" },
		"lane 非法":            func(p *ModelBenchmarkProfilePreset) { p.Lane = "draft" },
		"共享结果数缺失":            func(p *ModelBenchmarkProfilePreset) { p.SharedResults = 0 },
		"覆盖类别数越界":            func(p *ModelBenchmarkProfilePreset) { p.ComparableCategories = 9 },
		"证据 sourceUrl 非法": func(p *ModelBenchmarkProfilePreset) {
			p.BenchmarkEvidence = append([]ModelBenchmarkEvidencePreset(nil), evidenceOnly.BenchmarkEvidence...)
			p.CategoryScores = nil
			p.BenchmarkEvidence[0].SourceURL = "http://deepswe.datacurve.ai/"
		},
		"证据原始值为负数": func(p *ModelBenchmarkProfilePreset) {
			p.BenchmarkEvidence = append([]ModelBenchmarkEvidencePreset(nil), evidenceOnly.BenchmarkEvidence...)
			p.BenchmarkEvidence[0].RawValue = -1
		},
		"证据不确定性非有限数": func(p *ModelBenchmarkProfilePreset) {
			p.BenchmarkEvidence = append([]ModelBenchmarkEvidencePreset(nil), evidenceOnly.BenchmarkEvidence...)
			p.BenchmarkEvidence[0].Uncertainty = math.Inf(1)
		},
		"证据成本为 NaN": func(p *ModelBenchmarkProfilePreset) {
			p.BenchmarkEvidence = append([]ModelBenchmarkEvidencePreset(nil), evidenceOnly.BenchmarkEvidence...)
			bad := math.NaN()
			p.BenchmarkEvidence[0].CostUSD = &bad
		},
		"证据百分位越界": func(p *ModelBenchmarkProfilePreset) {
			p.BenchmarkEvidence = append([]ModelBenchmarkEvidencePreset(nil), evidenceOnly.BenchmarkEvidence...)
			p.BenchmarkEvidence[0].CohortPercentile = 1.01
		},
		"证据 costUsd 可选且通过": func(p *ModelBenchmarkProfilePreset) {
			p.BenchmarkEvidence = append([]ModelBenchmarkEvidencePreset(nil), evidenceOnly.BenchmarkEvidence...)
			p.BenchmarkEvidence[0].CostUSD = floatPtr(0.0042)
		},
	}
	for name, corrupt := range tests {
		t.Run(name, func(t *testing.T) {
			profile := validBenchmark
			profile.Patterns = append([]string(nil), validBenchmark.Patterns...)
			profile.Sources = append([]string(nil), validBenchmark.Sources...)
			profile.CategoryScores = map[string]float64{"coding": validBenchmark.CategoryScores["coding"]}
			profile.BenchmarkEvidence = nil
			corrupt(&profile)
			candidate := validBundle()
			candidate.ModelRegistry = &ModelRegistryPreset{SchemaVersion: 1, BenchmarkProfiles: []ModelBenchmarkProfilePreset{profile}}
			if name == "证据 costUsd 可选且通过" {
				if err := Validate(candidate); err != nil {
					t.Fatalf("costUsd 可选的合法证据未通过校验: %v", err)
				}
				return
			}
			if err := Validate(candidate); err == nil {
				t.Fatal("期望非法 benchmark profile 被拒绝")
			}
		})
	}
}

func TestTierForAndCanonicalize(t *testing.T) {
	sub := validBundle().Subscription
	if got := sub.TierFor("relay"); got != "second" {
		t.Errorf("relay tier=%q，期望 second", got)
	}
	// 别名归一化：public_benefit -> community -> third
	if got := sub.TierFor("public_benefit"); got != "third" {
		t.Errorf("public_benefit tier=%q，期望 third（经别名归一化）", got)
	}
	if got := sub.Canonicalize("public_benefit"); got != "community" {
		t.Errorf("Canonicalize(public_benefit)=%q，期望 community", got)
	}
	if got := sub.TierFor("nonexistent"); got != "unknown" {
		t.Errorf("未知类型 tier=%q，期望 unknown", got)
	}
}

func TestSupportsAutoRefresh(t *testing.T) {
	sub := validBundle().Subscription
	if !sub.SupportsAutoRefresh("openai") {
		t.Error("openai 应支持自动刷新")
	}
	if sub.SupportsAutoRefresh("relay_x") {
		t.Error("relay_x 不应支持自动刷新")
	}
}

func TestStoreSwapAndObserver(t *testing.T) {
	s := NewPresetStore(nil) // 回退内置
	if s.DataVersion() != EmbeddedBundle().DataVersion {
		t.Errorf("初始版本=%q，期望内置版本 %q", s.DataVersion(), EmbeddedBundle().DataVersion)
	}

	var mu sync.Mutex
	var notified string
	s.RegisterOnChange(func(b *PresetBundle) {
		mu.Lock()
		notified = b.DataVersion
		mu.Unlock()
	})

	next := validBundle()
	s.Swap(next)

	if s.DataVersion() != "2026.07.10-1" {
		t.Errorf("Swap 后 DataVersion=%q，期望 2026.07.10-1", s.DataVersion())
	}
	mu.Lock()
	if notified != "2026.07.10-1" {
		t.Errorf("观察者应收到新版本，得到 %q", notified)
	}
	mu.Unlock()

	// nil swap 应被忽略，不改变当前值
	s.Swap(nil)
	if s.DataVersion() != "2026.07.10-1" {
		t.Errorf("nil Swap 后 DataVersion 应不变，得到 %q", s.DataVersion())
	}
}

// TestStoreSwapPublishesEvent（Phase B.2）：Swap 注入 bus 后发布 preset_bundle_swapped 事件。
func TestStoreSwapPublishesEvent(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypePresetBundleSwapped)
	defer unsub()

	s := NewPresetStore(nil) // 内置版本（DataVersion 通常为空）
	s.SetEventBus(bus)

	prior := s.DataVersion()
	s.Swap(validBundle())

	ev := <-ch
	if ev.Type != eventbus.TypePresetBundleSwapped {
		t.Errorf("Type=%s，期望 %s", ev.Type, eventbus.TypePresetBundleSwapped)
	}
	if ev.From != prior {
		t.Errorf("From=%q，期望置换前版本 %q", ev.From, prior)
	}
	if ev.To != "2026.07.10-1" {
		t.Errorf("To=%q，期望 2026.07.10-1", ev.To)
	}
	if ev.Scope != eventbus.ScopePreset {
		t.Errorf("Scope=%s，期望 %s", ev.Scope, eventbus.ScopePreset)
	}
}

// TestStoreSwapNilBusNoPanic（Phase B.2）：未注入 bus 时 Swap 不 panic 且行为不变。
func TestStoreSwapNilBusNoPanic(t *testing.T) {
	s := NewPresetStore(nil)
	s.Swap(validBundle())
	if s.DataVersion() != "2026.07.10-1" {
		t.Errorf("无 bus 时 Swap 仍应生效，DataVersion=%q", s.DataVersion())
	}
}

func TestStoreGetReturnsImmutableClone(t *testing.T) {
	s := NewPresetStore(validBundle())
	got := s.Get()
	got.DataVersion = "mutated"
	got.Subscription.OriginTypes[0].Value = "mutated"

	again := s.Get()
	if again.DataVersion != "2026.07.10-1" {
		t.Fatalf("DataVersion = %q, want 2026.07.10-1", again.DataVersion)
	}
	if again.Subscription.OriginTypes[0].Value != "official_api" {
		t.Fatalf("OriginTypes[0].Value = %q, want official_api", again.Subscription.OriginTypes[0].Value)
	}
}

func TestStoreObserverGetsImmutableClone(t *testing.T) {
	s := NewPresetStore(validBundle())
	var first *PresetBundle
	s.RegisterOnChange(func(b *PresetBundle) {
		first = b
		b.DataVersion = "observer-mutated"
	})
	next := validBundle()
	next.DataVersion = "2026.07.10-2"
	s.Swap(next)
	if first == nil {
		t.Fatal("observer not invoked")
	}
	if s.DataVersion() != "2026.07.10-2" {
		t.Fatalf("store DataVersion = %q, want 2026.07.10-2", s.DataVersion())
	}
}
