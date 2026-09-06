package autopilot

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BenchmarkModelSelectionReport 是渠道无关的基准选型报告：
// 在调用方合成的全局候选池上，为每个典型场景跑一次与真实路由一致的
// frontier 选型，回答"benchmark 数据下该选什么模型、什么思考强度"。
// 报告不展示逐渠道映射；渠道实际可选范围取决于各自探测画像。
type BenchmarkModelSelectionReport struct {
	GeneratedAt time.Time
	PoolSize    int
	Scenarios   []BenchmarkReportScenario
}

// BenchmarkReportScenario 描述一个典型请求场景及其选择结果。
type BenchmarkReportScenario struct {
	Name             string
	RequestModel     string
	TaskClass        TaskClass
	CostPreference   CostPreferenceMode
	TaskDomain       TaskDomain
	MinContextTokens int
	Row              BenchmarkReportRow
}

// BenchmarkReportRow 描述该场景下的选择结果。
type BenchmarkReportRow struct {
	SelectedModel  string
	Effort         string
	BenchmarkScore float64
	// BenchmarkKnown 表示所选模型的 bench 分是否有实测证据；
	// false 时质量分由质量档先验合成，日志中标注 bench_evidence=none。
	BenchmarkKnown bool
	CostUSD        float64
	QualityTier    string
	FrontierNote   string
	ScoreBreakdown map[string]float64
	BetterOptions  []string
	TopCandidates  []TopCandidateRow
	Anomaly        string
}

// TopCandidateRow 描述 frontier 排名中的一个候选，用于 Top 5 展示。
type TopCandidateRow struct {
	Model          string
	Effort         string
	BenchmarkScore float64
	// BenchmarkKnown 为 false 时表格 bench 列渲染为 "-"，与选中项口径一致。
	BenchmarkKnown bool
	CostUSD        float64
	QualityTier    string
}

// benchmarkReportScenarioDef 是内部场景定义。
type benchmarkReportScenarioDef struct {
	Name             string
	RequestModel     string
	TaskClass        TaskClass
	CostPreference   CostPreferenceMode
	TaskDomain       TaskDomain
	MinContextTokens int
}

// defaultBenchmarkReportScenarios 覆盖 opus 类请求最关心的选型维度：
// 默认干活、主代理、轻量省钱、长上下文两档门槛、编程域。
var defaultBenchmarkReportScenarios = []benchmarkReportScenarioDef{
	{"worker/balanced（默认干活）", "claude-opus-4-8", TaskClassWorker, CostPrefBalanced, TaskDomainGeneral, 0},
	{"supervisor/quality_first（主代理）", "claude-opus-4-8", TaskClassSupervisor, CostPrefQualityFirst, TaskDomainGeneral, 0},
	{"lightweight/cost_first（轻量省钱）", "claude-opus-4-8", TaskClassLightweight, CostPrefCostFirst, TaskDomainGeneral, 0},
	{"long_context/balanced ctx>=200k", "claude-opus-4-8", TaskClassLongContext, CostPrefBalanced, TaskDomainGeneral, 200_000},
	{"long_context/balanced ctx>=1M", "claude-opus-4-8", TaskClassLongContext, CostPrefBalanced, TaskDomainGeneral, 1_000_000},
	{"worker/balanced coding（编程）", "claude-opus-4-8", TaskClassWorker, CostPrefBalanced, TaskDomainCoding, 0},
}

// benchmarkReportMaxBetterOptions 限制更优候选列表长度，保持报告可读。
const benchmarkReportMaxBetterOptions = 5

// GenerateBenchmarkSelectionReport 在全局候选池上为每个典型场景生成选择结果。
// poolUID 是合成候选池在 profileStore 中的 ChannelUID；候选为空时返回 nil。
// 当前仅由 cmd/benchmark-report CLI 调用（主程序不再输出该报告）。
func GenerateBenchmarkSelectionReport(resolver *ModelResolver, poolUID string) *BenchmarkModelSelectionReport {
	if resolver == nil || resolver.profileStore == nil || poolUID == "" {
		return nil
	}

	pool := filterProbedModelProfiles(resolver.profileStore.ListActiveByChannel(poolUID))
	if len(pool) == 0 {
		return nil
	}

	report := &BenchmarkModelSelectionReport{
		GeneratedAt: time.Now().UTC(),
		PoolSize:    len(pool),
		Scenarios:   make([]BenchmarkReportScenario, 0, len(defaultBenchmarkReportScenarios)),
	}
	for _, scenarioDef := range defaultBenchmarkReportScenarios {
		report.Scenarios = append(report.Scenarios, BenchmarkReportScenario{
			Name:             scenarioDef.Name,
			RequestModel:     scenarioDef.RequestModel,
			TaskClass:        scenarioDef.TaskClass,
			CostPreference:   scenarioDef.CostPreference,
			TaskDomain:       scenarioDef.TaskDomain,
			MinContextTokens: scenarioDef.MinContextTokens,
			Row:              buildBenchmarkReportRow(resolver, pool, poolUID, scenarioDef),
		})
	}
	return report
}

// LogBenchmarkSelectionReport 将报告以单行/场景形式输出。
// 已在 autopilot 包外被 cmd/benchmark-report 复用。
func LogBenchmarkSelectionReport(report *BenchmarkModelSelectionReport) {
	if report == nil || len(report.Scenarios) == 0 {
		return
	}

	log.Printf("[BenchmarkSelectionReport] generated_at=%s pool=%d scenarios=%d", report.GeneratedAt.Format(time.RFC3339), report.PoolSize, len(report.Scenarios))
	for _, scenario := range report.Scenarios {
		row := scenario.Row
		if row.Anomaly == "no_eligible_after_floor" {
			log.Printf("[BenchmarkSelectionReport] %-38s -> 无满足能力下界的候选", scenario.Name)
			continue
		}
		effort := row.Effort
		if effort == "" {
			effort = "-"
		}
		extra := ""
		if !row.BenchmarkKnown {
			extra = " bench_evidence=none"
		}
		if row.Anomaly != "" {
			extra += fmt.Sprintf(" ANOMALY=%s", row.Anomaly)
		}
		if len(row.BetterOptions) > 0 {
			extra += fmt.Sprintf(" better_options=%v", row.BetterOptions)
		}
		log.Printf("[BenchmarkSelectionReport] %-38s selected=%s effort=%s bench=%.2f cost=%.2f tier=%s note=%s %s%s",
			scenario.Name, row.SelectedModel, effort, row.BenchmarkScore, row.CostUSD, row.QualityTier, row.FrontierNote, formatBreakdown(row.ScoreBreakdown), extra)
		if table := formatTopCandidatesTable(row.TopCandidates); table != "" {
			log.Printf("[BenchmarkSelectionReport] %-38s top5:\n%s", scenario.Name, table)
		}
	}
}

// formatTopCandidatesTable 将 Top 候选列表渲染为对齐的 ASCII 表格。
// 列：# / model / effort / bench / cost / tier。列宽按内容自适应。
// 空列表返回空串。
func formatTopCandidatesTable(rows []TopCandidateRow) string {
	if len(rows) == 0 {
		return ""
	}
	headers := []string{"#", "model", "effort", "bench", "cost", "tier"}
	// 预渲染每行的单元格字符串，便于计算列宽。
	type renderedRow struct {
		cells []string
	}
	rendered := make([]renderedRow, 0, len(rows))
	for i, r := range rows {
		effort := r.Effort
		if effort == "" {
			effort = "-"
		}
		// 无实测 benchmark 证据时 bench 列渲染为 "-"，避免把档先验合成分误读为实测分。
		bench := "-"
		if r.BenchmarkKnown {
			bench = fmt.Sprintf("%.2f", r.BenchmarkScore)
		}
		rendered = append(rendered, renderedRow{cells: []string{
			fmt.Sprintf("%d", i+1),
			r.Model,
			effort,
			bench,
			fmt.Sprintf("%.2f", r.CostUSD),
			r.QualityTier,
		}})
	}
	// 计算每列最大宽度（含表头）。
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, rr := range rendered {
		for i, c := range rr.cells {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	// padRight 右补空格对齐到列宽。
	padRight := func(s string, w int) string {
		if len(s) >= w {
			return s
		}
		return s + strings.Repeat(" ", w-len(s))
	}
	// 分隔线：+----+----+...
	makeSep := func() string {
		parts := make([]string, 0, len(widths))
		for _, w := range widths {
			parts = append(parts, strings.Repeat("-", w+2))
		}
		return "+" + strings.Join(parts, "+") + "+"
	}
	// 渲染一行：| cell | cell |...
	renderLine := func(cells []string) string {
		parts := make([]string, 0, len(cells))
		for i, c := range cells {
			parts = append(parts, " "+padRight(c, widths[i])+" ")
		}
		return "|" + strings.Join(parts, "|") + "|"
	}
	var b strings.Builder
	b.WriteString(makeSep())
	b.WriteByte('\n')
	b.WriteString(renderLine(headers))
	b.WriteByte('\n')
	b.WriteString(makeSep())
	b.WriteByte('\n')
	for _, rr := range rendered {
		b.WriteString(renderLine(rr.cells))
		b.WriteByte('\n')
	}
	b.WriteString(makeSep())
	return b.String()
}

func formatBreakdown(breakdown map[string]float64) string {
	keys := make([]string, 0, len(breakdown))
	for k := range breakdown {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%.3f", k, breakdown[k]))
	}
	return strings.Join(parts, " ")
}

// buildBenchmarkReportRow 在全局候选池上按场景能力下界过滤后做一次 frontier 选型。
// 上下文门槛在此生效（低于 MinContextTokens 的候选被硬过滤），与真实路由一致。
func buildBenchmarkReportRow(resolver *ModelResolver, pool []ModelProfile, poolUID string, scenario benchmarkReportScenarioDef) BenchmarkReportRow {
	row := BenchmarkReportRow{ScoreBreakdown: make(map[string]float64)}

	floor := CapabilityFloor{
		MinContextTokens: scenario.MinContextTokens,
		TaskClass:        scenario.TaskClass,
		TaskDomain:       scenario.TaskDomain,
	}
	eligible := filterByCapabilityFloor(pool, floor, "")
	if len(eligible) == 0 {
		row.Anomaly = "no_eligible_after_floor"
		return row
	}

	// 使用 frontier 选型（与真实路由一致），但强制传入场景指定的 cost preference。
	ranked := resolver.buildRankedCandidates(eligible, scenario.RequestModel, poolUID, "messages", floor)
	if len(ranked) == 0 {
		row.Anomaly = "no_eligible_after_floor"
		return row
	}
	selectedIdx := -1
	idx, note, ok := selectViaFrontier(ranked, floor, scenario.CostPreference)
	if !ok || idx < 0 || idx >= len(ranked) {
		// frontier 失败时回退到旧字典序链。
		bestIdx := 0
		for i := 1; i < len(ranked); i++ {
			if betterRankedModel(ranked[i], ranked[bestIdx], scenario.CostPreference) {
				bestIdx = i
			}
		}
		row.FrontierNote = "fallback:" + note
		selectedIdx = bestIdx
	} else {
		row.FrontierNote = note
		selectedIdx = idx
	}
	fillBenchmarkReportRow(&row, ranked[selectedIdx], scenario.CostPreference)

	row.BetterOptions = findBetterOptions(ranked, row.SelectedModel, scenario.CostPreference)
	if len(row.BetterOptions) > 0 {
		row.Anomaly = "missed_better_model"
	}
	row.TopCandidates = buildTopCandidates(ranked, floor, scenario.CostPreference, selectedIdx, benchmarkReportTopN)
	return row
}

func fillBenchmarkReportRow(row *BenchmarkReportRow, best rankedModelCandidate, mode CostPreferenceMode) {
	row.SelectedModel = best.profile.ModelID
	row.Effort = string(best.effort)
	row.BenchmarkScore = best.benchmarkScore
	row.BenchmarkKnown = best.benchmarkKnown
	row.CostUSD = best.normalizedPublicCostUSD
	row.QualityTier = string(best.profile.QualityTier)

	row.ScoreBreakdown["quality_score"] = frontierQualityScore(best, mode)
	row.ScoreBreakdown["cost_usd"] = best.normalizedPublicCostUSD
	if best.publicCostKnown && best.normalizedPublicCostUSD > 0 {
		row.ScoreBreakdown["cost_score"] = 1.0 / best.normalizedPublicCostUSD
	}
	row.ScoreBreakdown["benchmark_score"] = best.benchmarkScore
	row.ScoreBreakdown["quality_rank"] = float64(best.qualityRank)
	if best.sameFamily {
		row.ScoreBreakdown["same_family"] = 1.0
	}
}

// findBetterOptions 列出"更优"候选，判定口径随场景的成本偏好走：
//   - quality_first：仅质量显著更高才算更优，更便宜但质量略低的候选不构成更优；
//   - 其他车道：质量显著更高且成本可接受，或成本显著更低且质量不降（≥95%）。
//
// 只保留有实测 benchmark 分的模型，按模型去重并限制条数，避免报告刷屏。
// 输出排序与候选池顺序无关（池顺序来自 map 迭代，本身不确定）。
func findBetterOptions(ranked []rankedModelCandidate, selectedModel string, mode CostPreferenceMode) []string {
	var selected *rankedModelCandidate
	for i := range ranked {
		if ranked[i].profile.ModelID == selectedModel {
			selected = &ranked[i]
			break
		}
	}
	if selected == nil {
		return nil
	}

	var options []string
	seen := map[string]bool{selectedModel: true}
	selectedQ := frontierQualityScore(*selected, mode)
	for i := range ranked {
		cand := ranked[i]
		if seen[cand.profile.ModelID] {
			continue
		}
		if cand.benchmarkScore <= 0 {
			continue
		}
		candQ := frontierQualityScore(cand, mode)
		var better bool
		if mode == CostPrefQualityFirst {
			better = candQ-selectedQ >= 0.05
		} else {
			// 质量显著更高且成本可接受；或成本显著更低且质量不下降。
			better = candQ-selectedQ >= 0.05 && cand.normalizedPublicCostUSD <= selected.normalizedPublicCostUSD*1.25
			if !better {
				better = selected.normalizedPublicCostUSD-cand.normalizedPublicCostUSD > 0.01 && candQ >= selectedQ*0.95
			}
		}
		if !better {
			continue
		}
		seen[cand.profile.ModelID] = true
		// 公开价未知的候选渲染 cost=unknown：frontier 因成本不可比将其排除在
		// 选型外，cost=0.00 会被误读为"免费"。
		costLabel := "unknown"
		if cand.publicCostKnown && cand.normalizedPublicCostUSD > 0 {
			costLabel = fmt.Sprintf("%.2f", cand.normalizedPublicCostUSD)
		}
		options = append(options, fmt.Sprintf("%s(bench=%.2f,cost=%s)", cand.profile.ModelID, cand.benchmarkScore, costLabel))
	}
	sort.Strings(options)
	if len(options) > benchmarkReportMaxBetterOptions {
		options = options[:benchmarkReportMaxBetterOptions]
	}
	return options
}

// benchmarkReportTopN 限制 Top 候选展示条数，保持报告可读。
const benchmarkReportTopN = 5

// buildTopCandidates 按场景真实排名取前 limit 条候选用于 Top 5 展示：
// 选中候选固定排第 1，其余按 scenarioCandidateOrder 的场景顺序
// （frontier 阶梯：quality_first 高质量簇优先，cost_first 低成本簇优先，
// balanced 膝点邻域按成本升序）。同模型去重仅保留排名最靠前的一条；
// 无实测 benchmark 分的候选保留并标注 BenchmarkKnown=false，与选中项口径一致。
// limit<=0 或候选为空时返回 nil。
func buildTopCandidates(ranked []rankedModelCandidate, floor CapabilityFloor, mode CostPreferenceMode, selectedIdx, limit int) []TopCandidateRow {
	if limit <= 0 || len(ranked) == 0 {
		return nil
	}
	var rows []TopCandidateRow
	seen := map[string]bool{}
	for _, idx := range scenarioCandidateOrder(ranked, floor, mode, selectedIdx) {
		if len(rows) >= limit {
			break
		}
		cand := ranked[idx]
		if seen[cand.profile.ModelID] {
			continue
		}
		seen[cand.profile.ModelID] = true
		rows = append(rows, TopCandidateRow{
			Model:          cand.profile.ModelID,
			Effort:         string(cand.effort),
			BenchmarkScore: cand.benchmarkScore,
			BenchmarkKnown: cand.benchmarkKnown,
			CostUSD:        cand.normalizedPublicCostUSD,
			QualityTier:    string(cand.profile.QualityTier),
		})
	}
	return rows
}

// scenarioCandidateOrder 产出该场景下的候选展示顺序（返回 ranked 下标序列）。
// 选中候选固定为首；其余优先取 frontier 阶梯顺序（与选型同源的 Pareto 前沿，
// 阶梯顺序本身随车道变化），阶梯未覆盖的候选（公开价未知等）按
// betterRankedModel 字典序链排在尾部。顺序完全由候选内容决定，
// 与 ranked 的输入顺序无关（输入顺序来自 map 迭代，每次运行随机）。
func scenarioCandidateOrder(ranked []rankedModelCandidate, floor CapabilityFloor, mode CostPreferenceMode, selectedIdx int) []int {
	order := make([]int, 0, len(ranked))
	seen := make([]bool, len(ranked))
	if selectedIdx >= 0 && selectedIdx < len(ranked) {
		order = append(order, selectedIdx)
		seen[selectedIdx] = true
	}

	if points := buildFrontierPoints(ranked, floor, mode); len(points) >= 2 {
		forest := ComputeFrontierForest(points, points[0].Cost.ScopeID, frontierEvidenceVersion)
		ladder := BuildCandidateLadder(forest, mode)
		for _, stages := range [][]LadderStage{ladder.Preferred, ladder.Overflow} {
			for _, stage := range stages {
				for _, p := range stage.Points {
					idx, err := strconv.Atoi(p.CandidateID)
					if err != nil || idx < 0 || idx >= len(ranked) || seen[idx] {
						continue
					}
					seen[idx] = true
					order = append(order, idx)
				}
			}
		}
	}

	var rest []int
	for i := range ranked {
		if !seen[i] {
			rest = append(rest, i)
		}
	}
	sort.SliceStable(rest, func(a, b int) bool {
		return betterRankedModel(ranked[rest[a]], ranked[rest[b]], mode)
	})
	return append(order, rest...)
}
