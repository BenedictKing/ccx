package autopilot

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// BenchmarkModelSelectionReport 记录一次 benchmark 更新后典型场景的模型选择结果。
type BenchmarkModelSelectionReport struct {
	GeneratedAt time.Time
	Scenarios   []BenchmarkReportScenario
}

// BenchmarkReportScenario 描述一个典型请求场景。
type BenchmarkReportScenario struct {
	Name           string
	RequestModel   string
	ChannelKind    string
	TaskClass      TaskClass
	CostPreference CostPreferenceMode
	Rows           []BenchmarkReportChannelRow
}

// BenchmarkReportChannelRow 描述某个渠道在该场景下的选择结果。
type BenchmarkReportChannelRow struct {
	ChannelName    string
	SelectedModel  string
	MappedModel    string
	BenchmarkScore float64
	CostUSD        float64
	QualityTier    string
	FrontierNote   string
	ScoreBreakdown map[string]float64
	BetterOptions  []string
	Anomaly        string
}

// benchmarkReportScenarioDef 是内部场景定义。
type benchmarkReportScenarioDef struct {
	Name           string
	RequestModel   string
	ChannelKind    string
	TaskClass      TaskClass
	CostPreference CostPreferenceMode
}

// defaultBenchmarkReportScenarios 是每次 benchmark 刷新后检查的典型场景。
var defaultBenchmarkReportScenarios = []benchmarkReportScenarioDef{
	{"claude-opus-4-8 -> messages/worker/balanced", "claude-opus-4-8", "messages", TaskClassWorker, CostPrefBalanced},
	{"claude-opus-4-8 -> messages/worker/quality_first", "claude-opus-4-8", "messages", TaskClassWorker, CostPrefQualityFirst},
	{"claude-opus-4-8 -> messages/supervisor/quality_first", "claude-opus-4-8", "messages", TaskClassSupervisor, CostPrefQualityFirst},
	{"claude-opus-4-8 -> messages/lightweight/cost_first", "claude-opus-4-8", "messages", TaskClassLightweight, CostPrefCostFirst},
	{"claude-opus-4-8 -> responses/worker/balanced", "claude-opus-4-8", "responses", TaskClassWorker, CostPrefBalanced},
	{"claude-opus-4-8 -> chat/worker/balanced", "claude-opus-4-8", "chat", TaskClassWorker, CostPrefBalanced},
}

// GenerateBenchmarkSelectionReport 在 benchmark 数据更新后生成一份典型场景模型选择报告。
// resolver 用于解析每个渠道的实际模型；cfgManager 用于读取活跃 auto-managed 渠道。
func GenerateBenchmarkSelectionReport(resolver *ModelResolver, cfgManager *config.ConfigManager) *BenchmarkModelSelectionReport {
	if resolver == nil || resolver.profileStore == nil || cfgManager == nil {
		return nil
	}

	report := &BenchmarkModelSelectionReport{
		GeneratedAt: time.Now().UTC(),
		Scenarios:   make([]BenchmarkReportScenario, 0, len(defaultBenchmarkReportScenarios)),
	}

	cfg := cfgManager.GetConfig()
	channels := collectAutoManagedChannels(cfg)

	for _, scenarioDef := range defaultBenchmarkReportScenarios {
		scenario := BenchmarkReportScenario{
			Name:           scenarioDef.Name,
			RequestModel:   scenarioDef.RequestModel,
			ChannelKind:    scenarioDef.ChannelKind,
			TaskClass:      scenarioDef.TaskClass,
			CostPreference: scenarioDef.CostPreference,
			Rows:           make([]BenchmarkReportChannelRow, 0, len(channels)),
		}

		for _, ch := range channels {
			if !channelKindMatches(ch.Kind, scenarioDef.ChannelKind) {
				continue
			}
			row := buildBenchmarkReportChannelRow(resolver, ch, scenarioDef)
			scenario.Rows = append(scenario.Rows, row)
		}

		if len(scenario.Rows) > 0 {
			report.Scenarios = append(report.Scenarios, scenario)
		}
	}

	return report
}

// LogBenchmarkSelectionReport 将报告以多行日志形式输出。
// 已在 autopilot 包外被 cmd/benchmark-report 复用，输出格式与运行态后端一致。
func LogBenchmarkSelectionReport(report *BenchmarkModelSelectionReport) {
	if report == nil || len(report.Scenarios) == 0 {
		return
	}

	log.Printf("[BenchmarkSelectionReport] generated_at=%s scenarios=%d", report.GeneratedAt.Format(time.RFC3339), len(report.Scenarios))
	for _, scenario := range report.Scenarios {
		log.Printf("[BenchmarkSelectionReport] scenario=%s request=%s kind=%s task=%s preference=%s", scenario.Name, scenario.RequestModel, scenario.ChannelKind, scenario.TaskClass, scenario.CostPreference)
		for _, row := range scenario.Rows {
			breakdownParts := make([]string, 0, len(row.ScoreBreakdown))
			for k, v := range row.ScoreBreakdown {
				breakdownParts = append(breakdownParts, fmt.Sprintf("%s=%.3f", k, v))
			}
			breakdown := strings.Join(breakdownParts, " ")
			extra := ""
			if row.Anomaly != "" {
				extra = fmt.Sprintf(" ANOMALY=%s", row.Anomaly)
			}
			if len(row.BetterOptions) > 0 {
				extra += fmt.Sprintf(" better_options=%v", row.BetterOptions)
			}
			log.Printf("[BenchmarkSelectionReport]   channel=%s selected=%s mapped=%s bench=%.2f cost=%.2f tier=%s note=%s %s%s",
				row.ChannelName, row.SelectedModel, row.MappedModel, row.BenchmarkScore, row.CostUSD, row.QualityTier, row.FrontierNote, breakdown, extra)
		}
	}
}

// channelInfoForReport 是报告用的渠道最小信息。
type channelInfoForReport struct {
	Name       string
	ChannelUID string
	Kind       string
}

func collectAutoManagedChannels(cfg config.Config) []channelInfoForReport {
	var result []channelInfoForReport
	for _, upstream := range cfg.Upstream {
		if !upstream.AutoManaged {
			continue
		}
		if upstream.Status == "disabled" {
			continue
		}
		if upstream.ChannelUID == "" {
			continue
		}
		result = append(result, channelInfoForReport{Name: upstream.Name, ChannelUID: upstream.ChannelUID, Kind: "messages"})
	}
	// responses/chat/gemini/images/vectors 渠道暂不逐一扫描，避免报告过长；
	// 典型场景已覆盖 responses/chat。
	return result
}

func channelKindMatches(upstreamKind, scenarioKind string) bool {
	if upstreamKind == "" {
		return scenarioKind == "messages"
	}
	return upstreamKind == scenarioKind
}

func buildBenchmarkReportChannelRow(resolver *ModelResolver, ch channelInfoForReport, scenario benchmarkReportScenarioDef) BenchmarkReportChannelRow {
	row := BenchmarkReportChannelRow{
		ChannelName:    ch.Name,
		ScoreBreakdown: make(map[string]float64),
	}

	// 构造能力下界，与真实请求尽量接近。
	floor := CapabilityFloor{
		MinContextTokens: 0,
		TaskClass:        scenario.TaskClass,
		TaskDomain:       "general",
	}

	// 收集该渠道已探测成功的画像。
	allProfiles := resolver.profileStore.ListActiveByChannel(ch.ChannelUID)
	eligible := make([]ModelProfile, 0, len(allProfiles))
	for _, p := range allProfiles {
		if p.ChannelKind != scenario.ChannelKind {
			continue
		}
		if !p.ProbeSuccess {
			continue
		}
		eligible = append(eligible, p)
	}

	if len(eligible) == 0 {
		row.Anomaly = "no_eligible_profiles"
		return row
	}

	// 使用 frontier 选型（与真实路由一致），但强制传入场景指定的 cost preference。
	ranked := resolver.buildRankedCandidates(eligible, scenario.RequestModel, ch.ChannelUID, scenario.ChannelKind, floor)
	idx, note, ok := selectViaFrontier(ranked, floor, scenario.CostPreference)
	if !ok || idx < 0 || idx >= len(ranked) {
		// frontier 失败时回退到旧字典序链。
		best := ranked[0]
		for i := 1; i < len(ranked); i++ {
			if betterRankedModel(ranked[i], best, scenario.CostPreference) {
				best = ranked[i]
			}
		}
		row.SelectedModel = best.profile.ModelID
		row.MappedModel = best.profile.ModelID
		row.BenchmarkScore = best.benchmarkScore
		row.CostUSD = best.normalizedPublicCostUSD
		row.QualityTier = string(best.profile.QualityTier)
		row.FrontierNote = "fallback:" + note
		fillBreakdown(&row, best)
	} else {
		best := ranked[idx]
		row.SelectedModel = best.profile.ModelID
		row.MappedModel = best.profile.ModelID
		row.BenchmarkScore = best.benchmarkScore
		row.CostUSD = best.normalizedPublicCostUSD
		row.QualityTier = string(best.profile.QualityTier)
		row.FrontierNote = note
		fillBreakdown(&row, best)
	}

	row.BetterOptions = findBetterOptions(ranked, row.SelectedModel)
	if len(row.BetterOptions) > 0 {
		row.Anomaly = "missed_better_model"
	}

	return row
}

func fillBreakdown(row *BenchmarkReportChannelRow, best rankedModelCandidate) {
	mode := CostPrefBalanced
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

func findBetterOptions(ranked []rankedModelCandidate, selectedModel string) []string {
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
	selectedQ := frontierQualityScore(*selected, CostPrefBalanced)
	for i := range ranked {
		cand := ranked[i]
		if cand.profile.ModelID == selectedModel {
			continue
		}
		candQ := frontierQualityScore(cand, CostPrefBalanced)
		// 更优定义：质量显著更高且成本可接受；或成本显著更低且质量不下降。
		if candQ-selectedQ >= 0.05 && cand.normalizedPublicCostUSD <= selected.normalizedPublicCostUSD*1.25 {
			options = append(options, fmt.Sprintf("%s(bench=%.2f,cost=%.2f)", cand.profile.ModelID, cand.benchmarkScore, cand.normalizedPublicCostUSD))
			continue
		}
		if selected.normalizedPublicCostUSD-cand.normalizedPublicCostUSD > 0.01 && candQ >= selectedQ*0.95 {
			options = append(options, fmt.Sprintf("%s(bench=%.2f,cost=%.2f)", cand.profile.ModelID, cand.benchmarkScore, cand.normalizedPublicCostUSD))
		}
	}
	return options
}
