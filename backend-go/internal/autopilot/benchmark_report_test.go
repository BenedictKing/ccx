package autopilot

import (
	"strings"
	"testing"
)

// makeRankedCand 构造一条 rankedModelCandidate，填充 buildTopCandidates /
// scenarioCandidateOrder / findBetterOptions 关注的字段。
// bench>0 视为有实测 benchmark 证据；cost>0 视为公开价已知（参与 frontier 排序）。
// effortDecided 保持 false，使 frontier 成本轴不做 effort 系数缩放（全部按 1.0）。
func makeRankedCand(model string, effort EffortLevel, bench, cost float64, tier QualityTier) rankedModelCandidate {
	return rankedModelCandidate{
		profile:                 ModelProfile{ModelID: model, QualityTier: tier},
		effort:                  effort,
		benchmarkKnown:          bench > 0,
		benchmarkScore:          bench,
		publicCostKnown:         cost > 0,
		normalizedPublicCostUSD: cost,
		normalizedCandidateID:   strings.ToLower(model),
	}
}

// 测试用 tier 常量（避免依赖具体业务档位）。
const (
	testTierFrontier = QualityTier("frontier")
	testTierPremium  = QualityTier("premium")
	testTierStandard = QualityTier("standard")
)

func TestBuildTopCandidates(t *testing.T) {
	type tc struct {
		name        string
		ranked      []rankedModelCandidate
		mode        CostPreferenceMode
		selectedIdx int
		limit       int
		wantModels  []string // 期望的模型顺序；nil 表示期望返回 nil
		wantKnown   []bool   // 期望的 BenchmarkKnown 序列；nil 表示不检查
	}
	cases := []tc{
		{
			name:        "limit<=0 返回 nil",
			ranked:      []rankedModelCandidate{makeRankedCand("a", EffortOff, 80, 1, testTierFrontier)},
			mode:        CostPrefBalanced,
			selectedIdx: 0,
			limit:       0,
			wantModels:  nil,
		},
		{
			name:        "空 ranked 返回 nil",
			ranked:      nil,
			mode:        CostPrefBalanced,
			selectedIdx: -1,
			limit:       5,
			wantModels:  nil,
		},
		{
			name: "选中候选固定排第一，其余按 cost_first 成本升序",
			ranked: []rankedModelCandidate{
				makeRankedCand("a", EffortOff, 80, 10, testTierFrontier),
				makeRankedCand("b", EffortOff, 60, 1, testTierPremium),
				makeRankedCand("c", EffortOff, 50, 2, testTierStandard),
			},
			mode:        CostPrefCostFirst,
			selectedIdx: 0, // a 成本最高但被选中，仍排第一
			limit:       5,
			wantModels:  []string{"a", "b", "c"},
		},
		{
			name: "同模型去重仅保留排名最靠前的一条",
			ranked: []rankedModelCandidate{
				makeRankedCand("a", EffortMax, 70, 5, testTierFrontier),
				makeRankedCand("a", EffortOff, 60, 1, testTierFrontier), // 成本更低排名靠前，保留此条
				makeRankedCand("b", EffortOff, 50, 2, testTierPremium),
			},
			mode:        CostPrefCostFirst,
			selectedIdx: -1,
			limit:       5,
			wantModels:  []string{"a", "b"},
		},
		{
			name: "bench 未知候选保留并标注 BenchmarkKnown=false",
			ranked: []rankedModelCandidate{
				makeRankedCand("a", EffortOff, 80, 5, testTierFrontier),
				makeRankedCand("z", EffortOff, 0, 1, testTierStandard), // 无实测分，未被支配则留在前沿
			},
			mode:        CostPrefBalanced,
			selectedIdx: 0,
			limit:       5,
			wantModels:  []string{"a", "z"},
			wantKnown:   []bool{true, false},
		},
		{
			name: "不足 limit 条返回实际长度",
			ranked: []rankedModelCandidate{
				makeRankedCand("a", EffortOff, 80, 1, testTierFrontier),
				makeRankedCand("b", EffortOff, 60, 2, testTierPremium),
			},
			mode:        CostPrefBalanced,
			selectedIdx: 0,
			limit:       5,
			wantModels:  []string{"a", "b"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildTopCandidates(c.ranked, CapabilityFloor{}, c.mode, c.selectedIdx, c.limit)
			if c.wantModels == nil {
				if got != nil {
					t.Fatalf("期望 nil，实际 %v", got)
				}
				return
			}
			if len(got) != len(c.wantModels) {
				t.Fatalf("期望 %d 条 %v，实际 %d 条 %v", len(c.wantModels), c.wantModels, len(got), got)
			}
			for i, w := range c.wantModels {
				if got[i].Model != w {
					t.Fatalf("第 %d 条期望 %s，实际 %s（全部 %v）", i, w, got[i].Model, got)
				}
			}
			if c.wantKnown != nil {
				for i, w := range c.wantKnown {
					if got[i].BenchmarkKnown != w {
						t.Fatalf("第 %d 条 BenchmarkKnown 期望 %v，实际 %v（全部 %+v）", i, w, got[i].BenchmarkKnown, got)
					}
				}
			}
		})
	}
}

// TestScenarioCandidateOrderDeterministic 钉住核心修复：展示顺序由候选内容决定，
// 与 ranked 输入顺序无关（输入顺序来自 map 迭代，每次运行随机）。
func TestScenarioCandidateOrderDeterministic(t *testing.T) {
	base := []rankedModelCandidate{
		makeRankedCand("m1", EffortOff, 80, 10, testTierFrontier),
		makeRankedCand("m2", EffortOff, 60, 1, testTierPremium),
		makeRankedCand("m3", EffortOff, 70, 3, testTierPremium),
		makeRankedCand("m4", EffortOff, 50, 2, testTierStandard),
	}
	// 同一候选集的另一种输入排列（selected=m3 在新排列中的下标为 0）。
	permuted := []rankedModelCandidate{base[2], base[3], base[0], base[1]}

	modelsOf := func(ranked []rankedModelCandidate, order []int) []string {
		out := make([]string, 0, len(order))
		for _, idx := range order {
			out = append(out, ranked[idx].profile.ModelID)
		}
		return out
	}
	got1 := modelsOf(base, scenarioCandidateOrder(base, CapabilityFloor{}, CostPrefBalanced, 2))
	got2 := modelsOf(permuted, scenarioCandidateOrder(permuted, CapabilityFloor{}, CostPrefBalanced, 0))
	if len(got1) != len(base) || len(got2) != len(base) {
		t.Fatalf("顺序序列应覆盖全部候选: %v vs %v", got1, got2)
	}
	for i := range got1 {
		if got1[i] != got2[i] {
			t.Fatalf("输入排列不同导致顺序不一致: %v vs %v", got1, got2)
		}
	}
	if got1[0] != "m3" {
		t.Fatalf("选中候选应固定排第一，实际 %v", got1)
	}
}

func TestBuildTopCandidatesFieldMapping(t *testing.T) {
	ranked := []rankedModelCandidate{
		makeRankedCand("glm-5.2", EffortHigh, 78, 7.34, testTierFrontier),
	}
	got := buildTopCandidates(ranked, CapabilityFloor{}, CostPrefBalanced, 0, 5)
	if len(got) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(got))
	}
	r := got[0]
	if r.Model != "glm-5.2" || r.Effort != string(EffortHigh) ||
		r.BenchmarkScore != 78 || r.CostUSD != 7.34 || r.QualityTier != string(testTierFrontier) ||
		!r.BenchmarkKnown {
		t.Fatalf("字段映射错误: %+v", r)
	}
}

func TestFindBetterOptions(t *testing.T) {
	// selected：bench 83 / cost 30；cheaper：bench 80 / cost 17（更便宜但质量略低）；
	// stronger：bench 95 / cost 200（质量显著更高但贵得多）。
	ranked := []rankedModelCandidate{
		makeRankedCand("selected", EffortHigh, 83, 30, testTierPremium),
		makeRankedCand("cheaper", EffortOff, 80, 17, testTierPremium),
		makeRankedCand("stronger", EffortMax, 95, 200, testTierPremium),
	}

	// quality_first：更便宜但质量略低的候选不构成"更优"，避免 missed_better_model 误报；
	// 质量显著更高的候选即使贵得多也应报出。
	gotQuality := findBetterOptions(ranked, "selected", CostPrefQualityFirst)
	wantQuality := []string{"stronger(bench=95.00,cost=200.00)"}
	if strings.Join(gotQuality, ",") != strings.Join(wantQuality, ",") {
		t.Fatalf("quality_first 期望 %v，实际 %v", wantQuality, gotQuality)
	}

	// balanced：cheaper 成本显著更低且质量 ≥95%，应报为更优；stronger 成本超 1.25 倍不报。
	gotBalanced := findBetterOptions(ranked, "selected", CostPrefBalanced)
	wantBalanced := []string{"cheaper(bench=80.00,cost=17.00)"}
	if strings.Join(gotBalanced, ",") != strings.Join(wantBalanced, ",") {
		t.Fatalf("balanced 期望 %v，实际 %v", wantBalanced, gotBalanced)
	}
}

func TestFindBetterOptionsUnknownCost(t *testing.T) {
	// 公开价未知的候选仍应作为 anomaly 报出（frontier 因成本不可比将其排除在选型外），
	// 但 cost 必须渲染 unknown，不得以 0.00 冒充免费。
	ranked := []rankedModelCandidate{
		makeRankedCand("selected", EffortMedium, 60.72, 0.42, QualityTierNormal),
		makeRankedCand("unpriced", EffortMedium, 66.09, 0, QualityTierNormal),
	}
	got := findBetterOptions(ranked, "selected", CostPrefBalanced)
	want := []string{"unpriced(bench=66.09,cost=unknown)"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("期望 %v，实际 %v", want, got)
	}
}

func TestFormatTopCandidatesTable(t *testing.T) {
	if got := formatTopCandidatesTable(nil); got != "" {
		t.Fatalf("空列表应返回空串，实际 %q", got)
	}
	rows := []TopCandidateRow{
		{Model: "a", Effort: "high", BenchmarkScore: 80, BenchmarkKnown: true, CostUSD: 1.5, QualityTier: "frontier"},
		{Model: "b", Effort: "", BenchmarkScore: 0, BenchmarkKnown: false, CostUSD: 2, QualityTier: "premium"},
	}
	got := formatTopCandidatesTable(rows)
	// 表格应包含表头、两行数据、分隔线；无实测证据的 bench 列渲染为 "-"。
	for _, want := range []string{"#", "model", "effort", "bench", "cost", "tier", "a", "b", "high", "80.00", "-"} {
		if !strings.Contains(got, want) {
			t.Fatalf("表格输出缺少 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "| 0.00 |") {
		t.Fatalf("无实测证据候选的 bench 不应渲染为 0.00:\n%s", got)
	}
	// 表格应有 3 条分隔线（头前、头后、尾）。
	if sepCount := strings.Count(got, "\n+"); sepCount < 2 {
		t.Fatalf("期望至少 2 条内部分隔线，实际 %d:\n%s", sepCount, got)
	}
}

func TestFormatTopCandidatesTableAlignment(t *testing.T) {
	// 确保列宽对齐：长模型名应撑宽 model 列，短名右补空格。
	rows := []TopCandidateRow{
		{Model: "very-long-model-name", Effort: "off", BenchmarkScore: 50, BenchmarkKnown: true, CostUSD: 1, QualityTier: "frontier"},
		{Model: "x", Effort: "off", BenchmarkScore: 50, BenchmarkKnown: true, CostUSD: 1, QualityTier: "frontier"},
	}
	got := formatTopCandidatesTable(rows)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) < 5 {
		t.Fatalf("期望至少 5 行（分隔/表头/分隔/2 数据/分隔），实际 %d:\n%s", len(lines), got)
	}
	// 所有非分隔行长度应一致（对齐）。
	for i, ln := range lines {
		if strings.HasPrefix(ln, "+") {
			continue
		}
		if len(ln) != len(lines[1]) {
			t.Fatalf("第 %d 行长度 %d 与表头 %d 不一致:\n%s", i, len(ln), len(lines[1]), got)
		}
	}
}
