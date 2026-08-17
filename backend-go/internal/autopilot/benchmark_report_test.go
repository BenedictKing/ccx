package autopilot

import (
	"strings"
	"testing"
)

// makeRankedCand 构造一条 rankedModelCandidate，仅填充 buildTopCandidates 关注的字段。
func makeRankedCand(model string, effort EffortLevel, bench, cost float64, tier QualityTier) rankedModelCandidate {
	return rankedModelCandidate{
		profile:                 ModelProfile{ModelID: model, QualityTier: tier},
		effort:                  effort,
		benchmarkScore:          bench,
		normalizedPublicCostUSD: cost,
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
		name       string
		ranked     []rankedModelCandidate
		limit      int
		wantModels []string // 期望的模型顺序；nil 表示期望返回 nil
	}
	cases := []tc{
		{
			name:       "limit<=0 返回 nil",
			ranked:     []rankedModelCandidate{makeRankedCand("a", EffortOff, 1, 1, testTierFrontier)},
			limit:      0,
			wantModels: nil,
		},
		{
			name:       "空 ranked 返回 nil",
			ranked:     nil,
			limit:      5,
			wantModels: nil,
		},
		{
			name: "跳过零分候选并按排名顺序取前 limit 条",
			ranked: []rankedModelCandidate{
				makeRankedCand("a", EffortOff, 0.8, 1, testTierFrontier),
				makeRankedCand("zero", EffortOff, 0, 1, testTierFrontier), // 跳过
				makeRankedCand("b", EffortHigh, 0.6, 2, testTierPremium),
				makeRankedCand("c", EffortMax, 0.5, 3, testTierStandard),
			},
			limit:      2,
			wantModels: []string{"a", "b"},
		},
		{
			name: "同模型去重仅保留排名最靠前的一条",
			ranked: []rankedModelCandidate{
				makeRankedCand("a", EffortMax, 0.9, 5, testTierFrontier),
				makeRankedCand("a", EffortOff, 0.7, 1, testTierFrontier), // 去重
				makeRankedCand("b", EffortHigh, 0.6, 2, testTierPremium),
			},
			limit:      5,
			wantModels: []string{"a", "b"},
		},
		{
			name: "不足 limit 条返回实际长度",
			ranked: []rankedModelCandidate{
				makeRankedCand("a", EffortOff, 0.8, 1, testTierFrontier),
				makeRankedCand("b", EffortHigh, 0.6, 2, testTierPremium),
			},
			limit:      5,
			wantModels: []string{"a", "b"},
		},
		{
			name: "全部零分返回 nil",
			ranked: []rankedModelCandidate{
				makeRankedCand("a", EffortOff, 0, 1, testTierFrontier),
				makeRankedCand("b", EffortOff, 0, 1, testTierFrontier),
			},
			limit:      5,
			wantModels: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildTopCandidates(c.ranked, c.limit)
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
		})
	}
}

func TestBuildTopCandidatesFieldMapping(t *testing.T) {
	ranked := []rankedModelCandidate{
		makeRankedCand("glm-5.2", EffortHigh, 0.78, 7.34, testTierFrontier),
	}
	got := buildTopCandidates(ranked, 5)
	if len(got) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(got))
	}
	r := got[0]
	if r.Model != "glm-5.2" || r.Effort != string(EffortHigh) ||
		r.BenchmarkScore != 0.78 || r.CostUSD != 7.34 || r.QualityTier != string(testTierFrontier) {
		t.Fatalf("字段映射错误: %+v", r)
	}
}

func TestFormatTopCandidatesTable(t *testing.T) {
	if got := formatTopCandidatesTable(nil); got != "" {
		t.Fatalf("空列表应返回空串，实际 %q", got)
	}
	rows := []TopCandidateRow{
		{Model: "a", Effort: "high", BenchmarkScore: 0.8, CostUSD: 1.5, QualityTier: "frontier"},
		{Model: "b", Effort: "", BenchmarkScore: 0.6, CostUSD: 2, QualityTier: "premium"},
	}
	got := formatTopCandidatesTable(rows)
	// 表格应包含表头、两行数据、分隔线。
	for _, want := range []string{"#", "model", "effort", "bench", "cost", "tier", "a", "b", "high", "-"} {
		if !strings.Contains(got, want) {
			t.Fatalf("表格输出缺少 %q:\n%s", want, got)
		}
	}
	// 表格应有 3 条分隔线（头前、头后、尾）。
	if sepCount := strings.Count(got, "\n+"); sepCount < 2 {
		t.Fatalf("期望至少 2 条内部分隔线，实际 %d:\n%s", sepCount, got)
	}
}

func TestFormatTopCandidatesTableAlignment(t *testing.T) {
	// 确保列宽对齐：长模型名应撑宽 model 列，短名右补空格。
	rows := []TopCandidateRow{
		{Model: "very-long-model-name", Effort: "off", BenchmarkScore: 0.5, CostUSD: 1, QualityTier: "frontier"},
		{Model: "x", Effort: "off", BenchmarkScore: 0.5, CostUSD: 1, QualityTier: "frontier"},
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
