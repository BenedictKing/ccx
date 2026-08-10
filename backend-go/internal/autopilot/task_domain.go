package autopilot

import (
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── 种子矩阵（§5.7.3）──

// seedDomainKey 是种子矩阵的查找键，格式 "family/majorVersion"。
type seedDomainKey string

// seedDomainMatrix 是 ModelFamily + 主版本 级别的任务域优势种子值。
// 设计文档 §5.7.3：同族不同版本的域优势也会漂移，因此按 family + 主版本建键。
// 未列出的族/域一律 0.5 中性。
var seedDomainMatrix = map[seedDomainKey]map[TaskDomain]float64{
	// ── 国际 ──
	"claude/fable": {
		TaskDomainAestheticsUI: 0.90, TaskDomainCodeReview: 0.90, TaskDomainCoding: 0.85,
		TaskDomainReasoning: 0.90, TaskDomainWriting: 0.85,
	},
	"claude/opus": {
		TaskDomainAestheticsUI: 0.90, TaskDomainCodeReview: 0.85, TaskDomainCoding: 0.85,
		TaskDomainReasoning: 0.85, TaskDomainWriting: 0.85,
	},
	"openai/gpt-5": {
		TaskDomainAestheticsUI: 0.60, TaskDomainCodeReview: 0.90, TaskDomainCoding: 0.80,
		TaskDomainReasoning: 0.85, TaskDomainWriting: 0.70,
	},
	"gemini/gemini-2": {
		TaskDomainAestheticsUI: 0.85, TaskDomainCodeReview: 0.75, TaskDomainCoding: 0.75,
		TaskDomainReasoning: 0.80, TaskDomainWriting: 0.75,
	},
	// ── 国产 ──
	"glm/glm-5": {
		TaskDomainAestheticsUI: 0.80, TaskDomainCodeReview: 0.70, TaskDomainCoding: 0.75,
		TaskDomainReasoning: 0.70, TaskDomainWriting: 0.75,
	},
	"deepseek/v4": {
		TaskDomainAestheticsUI: 0.55, TaskDomainCodeReview: 0.75, TaskDomainCoding: 0.80,
		TaskDomainReasoning: 0.85, TaskDomainWriting: 0.65,
	},
	"deepseek/v3": {
		TaskDomainAestheticsUI: 0.50, TaskDomainCodeReview: 0.70, TaskDomainCoding: 0.75,
		TaskDomainReasoning: 0.80, TaskDomainWriting: 0.60,
	},
}

// ── 域推导（§5.7.2）──

// DomainHints 是脱敏后的请求特征，用于确定性域推导。
// 不存储原始请求体，只提取推导所需的关键信号。
type DomainHints struct {
	ExplicitDomain string   // X-Task-Domain 头（最高优先级）
	SystemPrompt   string   // system prompt 文本（关键词匹配）
	ToolNames      []string // 请求携带的工具名列表
	FileExtensions []string // 首条 user 消息中出现的文件扩展名
	HasDiffContext bool     // 是否包含 diff 上下文
}

// domainKeywordRule 定义 system prompt 关键词到 TaskDomain 的映射。
// 按匹配顺序排列，首个命中的规则生效。
type domainKeywordRule struct {
	domain   TaskDomain
	keywords []string // 全部转小写后匹配
}

// domainKeywords 是系统提示词关键词匹配表。
var domainKeywords = []domainKeywordRule{
	{TaskDomainCodeReview, []string{"code review", "审查代码", "代码审核", "code audit", "代码审计", "find bugs", "找bug", "找 bug"}},
	{TaskDomainAestheticsUI, []string{"ui", "ux", "设计", "样式", "tailwind", "css", "前端设计", "visual design", "审美", "界面"}},
	{TaskDomainTranslation, []string{"翻译", "translate", "localization", "本地化", "i18n"}},
	{TaskDomainReasoning, []string{"算法", "数学", "证明", "algorithm", "math", "prove", "推理", "reasoning", "逻辑"}},
	{TaskDomainWriting, []string{"写作", "文案", "文章", "blog", "writing", "copywriting", "长文", "文档撰写"}},
	{TaskDomainCoding, []string{"实现", "编码", "代码", "implement", "coding", "programming", "开发", "function", "函数"}},
	{TaskDomainAgentic, []string{"agent", "工具调用", "tool use", "workflow", "工作流", "多步", "multi-step", "编排"}},
}

// readOnlyToolNames 是只读工具的集合，用于 code_review 域推导。
var readOnlyToolNames = map[string]bool{
	"read":       true,
	"grep":       true,
	"search":     true,
	"find":       true,
	"list":       true,
	"glob":       true,
	"rg":         true,
	"cat":        true,
	"head":       true,
	"tail":       true,
	"git_diff":   true,
	"git_log":    true,
	"git_show":   true,
	"git_status": true,
}

// aestheticsExtensions 是与审美/UI 相关的文件扩展名。
var aestheticsExtensions = map[string]bool{
	".vue": true, ".css": true, ".scss": true, ".sass": true, ".less": true,
	".styl": true, ".html": true, ".jsx": true, ".tsx": true,
	".svelte": true, ".astro": true, ".blade.php": true,
}

// InferTaskDomain 根据 DomainHints 确定性推导请求的 TaskDomain。
// 优先级（§5.7.2）：
//  1. 显式 header（X-Task-Domain）
//  2. system prompt 关键词
//  3. 工具集特征
//  4. 文件扩展名
//  5. 回退到 general
func InferTaskDomain(hints DomainHints) TaskDomain {
	// 优先级 1：显式声明
	if d := normalizeDomain(hints.ExplicitDomain); d != "" {
		return d
	}

	// 优先级 2：system prompt 关键词
	if d := matchSystemPromptKeywords(hints.SystemPrompt); d != TaskDomainGeneral {
		return d
	}

	// 优先级 3：工具集特征（只读工具 + diff 上下文 → code_review）
	if d := inferFromToolSet(hints.ToolNames, hints.HasDiffContext); d != TaskDomainGeneral {
		return d
	}

	// 优先级 4：文件扩展名
	if d := inferFromExtensions(hints.FileExtensions); d != TaskDomainGeneral {
		return d
	}

	return TaskDomainGeneral
}

// normalizeDomain 将用户输入的 domain 字符串标准化为 TaskDomain 枚举值。
// 返回空串表示无法识别。
func normalizeDomain(raw string) TaskDomain {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch TaskDomain(normalized) {
	case TaskDomainAestheticsUI, TaskDomainCodeReview, TaskDomainCoding,
		TaskDomainReasoning, TaskDomainWriting, TaskDomainTranslation,
		TaskDomainAgentic, TaskDomainGeneral:
		return TaskDomain(normalized)
	default:
		return ""
	}
}

// matchSystemPromptKeywords 在 system prompt 中匹配关键词表，返回首个命中的域。
// 短关键词（<=3 字符）使用单词边界匹配，避免 "ui" 匹配到 "Build" 中的子串。
func matchSystemPromptKeywords(prompt string) TaskDomain {
	if prompt == "" {
		return TaskDomainGeneral
	}
	lower := strings.ToLower(prompt)
	for _, rule := range domainKeywords {
		for _, kw := range rule.keywords {
			if len(kw) <= 3 {
				// 短关键词使用单词边界匹配
				if matchWordBoundary(lower, kw) {
					return rule.domain
				}
			} else {
				if strings.Contains(lower, kw) {
					return rule.domain
				}
			}
		}
	}
	return TaskDomainGeneral
}

// matchWordBoundary 检查 kw 是否作为完整单词出现在 text 中。
// 单词边界由非字母数字字符或字符串首尾界定。
func matchWordBoundary(text, kw string) bool {
	idx := 0
	for {
		pos := strings.Index(text[idx:], kw)
		if pos == -1 {
			return false
		}
		pos += idx
		// 检查前边界
		if pos > 0 {
			prev := rune(text[pos-1])
			if isAlphaNumeric(prev) {
				idx = pos + 1
				continue
			}
		}
		// 检查后边界
		end := pos + len(kw)
		if end < len(text) {
			next := rune(text[end])
			if isAlphaNumeric(next) {
				idx = pos + 1
				continue
			}
		}
		return true
	}
}

// isAlphaNumeric 判断字符是否为字母或数字。
func isAlphaNumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// inferFromToolSet 从工具集特征推导域。
// 只读工具 + diff 上下文 → code_review；仅此条件命中。
func inferFromToolSet(toolNames []string, hasDiff bool) TaskDomain {
	if !hasDiff || len(toolNames) == 0 {
		return TaskDomainGeneral
	}
	allReadOnly := true
	for _, name := range toolNames {
		if !readOnlyToolNames[strings.ToLower(name)] {
			allReadOnly = false
			break
		}
	}
	if allReadOnly {
		return TaskDomainCodeReview
	}
	return TaskDomainGeneral
}

// inferFromExtensions 从文件扩展名分布推导域。
// aestheticsExtensions 命中 → aesthetics_ui。
func inferFromExtensions(exts []string) TaskDomain {
	for _, ext := range exts {
		normalized := strings.ToLower(ext)
		if !strings.HasPrefix(normalized, ".") {
			normalized = "." + normalized
		}
		if aestheticsExtensions[normalized] {
			return TaskDomainAestheticsUI
		}
	}
	return TaskDomainGeneral
}

// ── DomainStrength 查询（§5.7.3）──

// DomainStrengthEvidence 解释任务域强度的来源及规范基准折算过程。
// Benchmark 字段只在 source=canonical_benchmark 时填写。
type DomainStrengthEvidence struct {
	Source                string   `json:"source"`
	Score                 float64  `json:"score"`
	CanonicalCeiling      float64  `json:"canonicalCeiling,omitempty"`
	ProviderQualityFactor float64  `json:"providerQualityFactor,omitempty"`
	CanonicalModel        string   `json:"canonicalModel,omitempty"`
	BenchmarkCategory     string   `json:"benchmarkCategory,omitempty"`
	BenchmarkSources      []string `json:"benchmarkSources,omitempty"`
	BenchmarkVerifiedAt   string   `json:"benchmarkVerifiedAt,omitempty"`
	BenchmarkLane         string   `json:"benchmarkLane,omitempty"`
	BenchmarkName         string   `json:"benchmarkName,omitempty"`
	BenchmarkVersion      string   `json:"benchmarkVersion,omitempty"`
	BenchmarkMetric       string   `json:"benchmarkMetric,omitempty"`
	BenchmarkRawValue     float64  `json:"benchmarkRawValue,omitempty"`
	BenchmarkPercentile   float64  `json:"benchmarkPercentile,omitempty"`
	BenchmarkEffort       string   `json:"benchmarkEffort,omitempty"`
	BenchmarkSelection    string   `json:"benchmarkSelection,omitempty"`
	EvidenceConfidence    float64  `json:"evidenceConfidence,omitempty"`
}

type benchmarkDomainMapping struct {
	category   string
	confidence float64
}

// benchmarkDomainMappings 保持基准原始类别与 CCX 任务域的映射集中、可审计。
// aesthetics_ui 仅借用多模态分作为弱代理信号，因此解释置信度减半。
var benchmarkDomainMappings = map[TaskDomain]benchmarkDomainMapping{
	TaskDomainGeneral:      {category: "knowledge", confidence: 1.0},
	TaskDomainReasoning:    {category: "math", confidence: 1.0},
	TaskDomainCoding:       {category: "coding", confidence: 1.0},
	TaskDomainCodeReview:   {category: "coding", confidence: 1.0},
	TaskDomainAgentic:      {category: "agentic", confidence: 1.0},
	TaskDomainAestheticsUI: {category: "multimodal", confidence: 0.5},
	TaskDomainTranslation:  {category: "multilingual", confidence: 0.8},
	TaskDomainWriting:      {category: "writing", confidence: 1.0},
}

// relativeBenchmarkMaxDelta 限制单个相对榜单对既有家族先验的修正幅度。
// 深度编程 benchmark 的 Pass@1 不是通用能力百分比，只能作为局部软信号。
const relativeBenchmarkMaxDelta = 0.10

// DomainStrength 返回指定模型在给定任务域的优势分。
// 覆盖优先级：ModelProfile 级值 > 规范模型基准 > 种子矩阵 > 0.5 中性。
func DomainStrength(profile *ModelProfile, domain TaskDomain) float64 {
	return ResolveDomainStrength(profile, domain).Score
}

// ResolveDomainStrength 返回任务域强度及其可解释证据。
// 规范基准代表模型能力上界；有可信渠道质量证据时只允许向下折算。
func ResolveDomainStrength(profile *ModelProfile, domain TaskDomain) DomainStrengthEvidence {
	return ResolveDomainStrengthForEffort(profile, domain, "")
}

// ResolveDomainStrengthForEffort 在已知目标思考档位时解析任务域强度证据。
// targetEffort 非空时，基准证据优先精确匹配 (domain, effort)，跨档位回退会降低置信度；
// 为空时退化为原有的 domain-only 行为，供尚未决定 effort 的渠道打分阶段使用。
func ResolveDomainStrengthForEffort(profile *ModelProfile, domain TaskDomain, targetEffort EffortLevel) DomainStrengthEvidence {
	if profile == nil {
		return DomainStrengthEvidence{Source: "neutral", Score: 0.5}
	}

	// 优先级 1：ModelProfile 级覆盖
	if profile.TaskDomainStrengths != nil {
		if score, ok := profile.TaskDomainStrengths[domain]; ok {
			return DomainStrengthEvidence{Source: "endpoint_override", Score: clampUnit(score)}
		}
	}

	// 优先级 2：规范模型基准。缺少当前任务域的直接/代理类别时继续回退。
	if mapping, ok := benchmarkDomainMappings[domain]; ok {
		resolved := config.ResolveModelBenchmarkProfile(profile.ModelID)
		if resolved.Known {
			if rawScore, found := resolved.Profile.CategoryScores[mapping.category]; found {
				ceiling := clampUnit(rawScore / 100)
				providerFactor := providerQualityFactor(profile)
				confidence := benchmarkEvidenceConfidence(resolved.Profile) * mapping.confidence
				return DomainStrengthEvidence{
					Source:                "canonical_benchmark",
					Score:                 clampUnit(ceiling * providerFactor),
					CanonicalCeiling:      ceiling,
					ProviderQualityFactor: providerFactor,
					CanonicalModel:        resolved.Profile.CanonicalModel,
					BenchmarkCategory:     mapping.category,
					BenchmarkSources:      append([]string(nil), resolved.Profile.Sources...),
					BenchmarkVerifiedAt:   resolved.Profile.VerifiedAt,
					BenchmarkLane:         resolved.Profile.Lane,
					EvidenceConfidence:    clampUnit(confidence),
				}
			}
			if evidence, ok := resolveRelativeBenchmarkEvidence(profile, resolved.Profile, domain, targetEffort); ok {
				return evidence
			}
		}
	}

	// 优先级 3：种子矩阵（family + 主版本）
	if score := seedLookup(profile.ModelFamily, profile.ModelID, domain); score > 0 {
		return DomainStrengthEvidence{Source: "family_seed", Score: clampUnit(score)}
	}

	// 优先级 4：中性默认
	return DomainStrengthEvidence{Source: "neutral", Score: 0.5}
}

// providerQualityFactor 将渠道质量证据折算为 [0,1] 的保守下调因子。
// 无证据时因子为 1；置信度越高，越接近观测到的 providerQualityScore。
func providerQualityFactor(profile *ModelProfile) float64 {
	if profile == nil || profile.ProviderQualityConfidence < 0.5 {
		return 1
	}
	quality := clampUnit(profile.ProviderQualityScore)
	confidence := clampUnit(profile.ProviderQualityConfidence)
	return clampUnit(1 - confidence*(1-quality))
}

func benchmarkEvidenceConfidence(profile config.ModelBenchmarkProfile) float64 {
	if profile.TotalCategories <= 0 || profile.ComparableCategories <= 0 {
		return 0
	}
	return clampUnit(float64(profile.ComparableCategories) / float64(profile.TotalCategories))
}

// relativeBenchmarkQualityFloorRatio 是新鲜度闸门的质量地板比例。
// 仅当一条更新证据的质量置信度不低于「全局最优质量 × 该比例」时，
// 它的更新日期才允许淘汰更旧的证据；质量差距过大的新证据无权翻盘。
const relativeBenchmarkQualityFloorRatio = 0.7

// filterByRecencyGate 在同一比较集合（同 domain，必要时同 effort）内施加相对新鲜度闸门：
// 更新的可靠证据会让更旧的次优证据出局，从而体现「更新更快的数据源应优先采信」。
//
// 规则：
//  1. 以集合内最高质量置信度 bestQuality 为基准，质量地板 = bestQuality × relativeBenchmarkQualityFloorRatio。
//  2. 在达到质量地板的证据中取最新 CapturedAt 作为 freshestViable。
//  3. 凡是 CapturedAt 早于 freshestViable、且自身质量低于 bestQuality 的证据被淘汰；
//     全局最优那条永远豁免，保证集合不会被打空。
//
// CapturedAt 为 ISO YYYY-MM-DD，字典序即时间序；空串视为最旧，自然沉底。
// 单候选或全部同日期时闸门不生效，行为与旧逻辑完全一致（向后兼容）。
func filterByRecencyGate(candidates []config.ModelBenchmarkEvidence) []config.ModelBenchmarkEvidence {
	if len(candidates) <= 1 {
		return candidates
	}

	bestQuality := 0.0
	for _, candidate := range candidates {
		if q := relativeBenchmarkEvidenceConfidence(candidate); q > bestQuality {
			bestQuality = q
		}
	}
	if bestQuality <= 0 {
		return candidates
	}

	floor := bestQuality * relativeBenchmarkQualityFloorRatio
	freshestViable := ""
	for _, candidate := range candidates {
		if relativeBenchmarkEvidenceConfidence(candidate) < floor {
			continue
		}
		if candidate.CapturedAt > freshestViable {
			freshestViable = candidate.CapturedAt
		}
	}
	if freshestViable == "" {
		return candidates
	}

	filtered := make([]config.ModelBenchmarkEvidence, 0, len(candidates))
	for _, candidate := range candidates {
		stale := candidate.CapturedAt < freshestViable
		belowBest := relativeBenchmarkEvidenceConfidence(candidate) < bestQuality
		if stale && belowBest {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

// resolveRelativeBenchmarkEvidence 将固定 cohort 内的 percentile 保守地折算到家族先验。
// 该路径刻意不使用 raw Pass@1 作为能力绝对值；它仅说明同一 harness 下的相对位置。
// targetEffort 为空时退化为纯 domain 匹配（与旧行为一致）。
// 非空时优先匹配 (domain, effort) 双键；无精确匹配则回退到 domain-only 并衰减置信度。
func resolveRelativeBenchmarkEvidence(profile *ModelProfile, benchmark config.ModelBenchmarkProfile, domain TaskDomain, targetEffort EffortLevel) (DomainStrengthEvidence, bool) {
	if profile == nil {
		return DomainStrengthEvidence{}, false
	}

	prior := seedLookup(profile.ModelFamily, profile.ModelID, domain)
	if prior <= 0 {
		prior = 0.5
	}

	// ── Pass 1：精确 (domain, effort) 匹配 ──
	// 未锚定 effort 的证据（如 "default"）不代表任何具体挡位，
	// 即便标准化后恰好等于 targetEffort，也不能作为精确匹配。
	var selected config.ModelBenchmarkEvidence
	confidence := 0.0
	effortMatched := false
	if targetEffort != "" {
		var candidates []config.ModelBenchmarkEvidence
		for _, candidate := range benchmark.BenchmarkEvidence {
			if !strings.EqualFold(candidate.Domain, string(domain)) {
				continue
			}
			if IsUnpinnedEffort(candidate.Effort) {
				continue
			}
			candidateEffort := NormalizeEffortLevel(candidate.Effort)
			if candidateEffort != targetEffort {
				continue
			}
			candidates = append(candidates, candidate)
		}
		for _, candidate := range filterByRecencyGate(candidates) {
			candidateConfidence := relativeBenchmarkEvidenceConfidence(candidate)
			if candidateConfidence > confidence {
				selected = candidate
				confidence = candidateConfidence
				effortMatched = true
			}
		}
	}

	// ── Pass 2（fallback）：仅 domain 匹配，衰减置信度 ──
	if !effortMatched {
		domainConfidence := 0.0
		var domainSelected config.ModelBenchmarkEvidence
		var domainCandidates []config.ModelBenchmarkEvidence
		for _, candidate := range benchmark.BenchmarkEvidence {
			if !strings.EqualFold(candidate.Domain, string(domain)) {
				continue
			}
			domainCandidates = append(domainCandidates, candidate)
		}
		for _, candidate := range filterByRecencyGate(domainCandidates) {
			candidateConfidence := relativeBenchmarkEvidenceConfidence(candidate)
			if candidateConfidence > domainConfidence {
				domainSelected = candidate
				domainConfidence = candidateConfidence
			}
		}
		if domainConfidence <= 0 {
			return DomainStrengthEvidence{}, false
		}

		// 有 targetEffort 时按距离衰减置信度；空 effort 时保持原行为（乘数 1.0）。
		// 命中的证据若本身未锚定 effort，统一按距离 1 的折扣（0.7）处理：
		// 它只能证明与 domain 相关，无法说明模型在 targetEffort 挡位上的表现。
		fallbackMultiplier := 1.0
		if targetEffort != "" {
			if IsUnpinnedEffort(domainSelected.Effort) {
				fallbackMultiplier = EffortFallbackConfidence(1)
			} else {
				closestDistance := findClosestEffortDistance(targetEffort, benchmark.BenchmarkEvidence, domain)
				fallbackMultiplier = EffortFallbackConfidence(closestDistance)
			}
		}
		selected = domainSelected
		confidence = domainConfidence * fallbackMultiplier
	}

	if confidence <= 0 {
		return DomainStrengthEvidence{}, false
	}

	adjustment := (selected.CohortPercentile - 0.5) * 2 * relativeBenchmarkMaxDelta * confidence
	providerFactor := providerQualityFactor(profile)
	return DomainStrengthEvidence{
		Source:                "relative_benchmark",
		Score:                 clampUnit((prior + adjustment) * providerFactor),
		ProviderQualityFactor: providerFactor,
		CanonicalModel:        benchmark.CanonicalModel,
		BenchmarkCategory:     selected.Domain,
		BenchmarkSources:      []string{selected.SourceURL},
		BenchmarkVerifiedAt:   selected.CapturedAt,
		BenchmarkLane:         benchmark.Lane,
		BenchmarkName:         selected.Benchmark,
		BenchmarkVersion:      selected.BenchmarkVersion,
		BenchmarkMetric:       selected.Metric,
		BenchmarkRawValue:     selected.RawValue,
		BenchmarkPercentile:   selected.CohortPercentile,
		BenchmarkEffort:       selected.Effort,
		BenchmarkSelection:    selected.SelectionBasis,
		EvidenceConfidence:    confidence,
	}, true
}

// findClosestEffortDistance 在同 domain 的 benchmark 证据中找到与 targetEffort 距离最近的 effort。
// 未锚定 effort（如 "default"）的证据会被跳过：它们没有具体挡位，不能参与距离计算。
// 无匹配证据时返回 3+（保守衰减）。
func findClosestEffortDistance(targetEffort EffortLevel, candidates []config.ModelBenchmarkEvidence, domain TaskDomain) int {
	minDist := 4 // 超出 max(3)=3 的范围，等效于 3+
	for _, c := range candidates {
		if !strings.EqualFold(c.Domain, string(domain)) {
			continue
		}
		if IsUnpinnedEffort(c.Effort) {
			continue
		}
		effort := NormalizeEffortLevel(c.Effort)
		if effort == "" {
			continue
		}
		dist := EffortLevelDistance(targetEffort, effort)
		if dist >= 0 && dist < minDist {
			minDist = dist
		}
	}
	return minDist
}

func relativeBenchmarkEvidenceConfidence(evidence config.ModelBenchmarkEvidence) float64 {
	taskConfidence := clampUnit(float64(evidence.TaskCount) / 100)
	cohortConfidence := clampUnit(float64(evidence.CohortSize) / 10)
	uncertaintyConfidence := clampUnit(1 - evidence.Uncertainty*3)
	return clampUnit(taskConfidence * cohortConfidence * uncertaintyConfidence)
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// seedLookup 从种子矩阵中查找 family + 主版本 对应的域优势值。
// 未命中返回 0，由调用方回退到 0.5。
func seedLookup(family ModelFamily, modelID string, domain TaskDomain) float64 {
	key := buildSeedKey(family, modelID)
	matrix, ok := seedDomainMatrix[key]
	if !ok {
		return 0
	}
	if score, ok := matrix[domain]; ok {
		return score
	}
	return 0
}

// buildSeedKey 从 ModelFamily 和 modelID 构建种子矩阵查找键。
// 格式："family/majorVersionSegment"。
func buildSeedKey(family ModelFamily, modelID string) seedDomainKey {
	lower := strings.ToLower(modelID)

	switch family {
	case ModelFamilyClaude:
		// 区分 fable/opus/sonnet/haiku 子系列
		if strings.Contains(lower, "fable") {
			return "claude/fable"
		}
		if strings.Contains(lower, "opus") || strings.Contains(lower, "mythos") {
			return "claude/opus"
		}
		// sonnet/haiku 不在种子矩阵中，回退到通用 claude

	case ModelFamilyOpenAI:
		// gpt-5.x 系列
		if strings.Contains(lower, "gpt-5") {
			return "openai/gpt-5"
		}

	case ModelFamilyGemini:
		if strings.Contains(lower, "gemini-2") {
			return "gemini/gemini-2"
		}

	case ModelFamilyDeepSeek:
		if strings.Contains(lower, "v4") || strings.Contains(lower, "deepseek-v4") {
			return "deepseek/v4"
		}
		if strings.Contains(lower, "v3") || strings.Contains(lower, "deepseek-v3") {
			return "deepseek/v3"
		}

	case ModelFamilyGLM:
		if strings.Contains(lower, "glm-5") {
			return "glm/glm-5"
		}
	}

	return ""
}

// ── 思考等级质量加分（§5.8.2）──

// effortBonusTable 定义各 EffortLevel 对质量分的加分值。
// 设计文档 §5.8.2：off=0, minimal=+0.2, low=+0.4, medium=+0.6, high=+0.9, xhigh=+1.0。
// 雷达站实测 xhigh 为效果最优档；max/ultra 投入更高但实测效果递减，
// 故加分不随投入单调涨（体现"更贵不等于更好"）：max=+0.95, ultra=+0.9。
var effortBonusTable = map[EffortLevel]float64{
	EffortOff:     0.0,
	EffortMinimal: 0.2,
	EffortLow:     0.4,
	EffortMedium:  0.6,
	EffortHigh:    0.9,
	EffortXhigh:   1.0,
	EffortMax:     0.95,
	EffortUltra:   0.9,
}

// EffortQualityBonus 返回指定思考等级对质量评分的加分值。
// 上限钳制在 premium=4，避免"低档模型开满思考"虚标超过高档模型（§5.8.2）。
func EffortQualityBonus(level EffortLevel) float64 {
	if bonus, ok := effortBonusTable[level]; ok {
		return bonus
	}
	return 0.0
}

// AllEffortLevels 返回所有可用的 EffortLevel（按厂商投入/成本序升序）。
func AllEffortLevels() []EffortLevel {
	return []EffortLevel{
		EffortOff, EffortMinimal, EffortLow, EffortMedium, EffortHigh,
		EffortXhigh, EffortMax, EffortUltra,
	}
}

// AllTaskDomains 返回所有可用的 TaskDomain。
func AllTaskDomains() []TaskDomain {
	return []TaskDomain{
		TaskDomainAestheticsUI, TaskDomainCodeReview, TaskDomainCoding,
		TaskDomainReasoning, TaskDomainWriting, TaskDomainTranslation,
		TaskDomainAgentic, TaskDomainGeneral,
	}
}

// SeedDomainKeys 返回种子矩阵中所有已注册的 seed key（用于调试/展示）。
func SeedDomainKeys() []seedDomainKey {
	keys := make([]seedDomainKey, 0, len(seedDomainMatrix))
	for k := range seedDomainMatrix {
		keys = append(keys, k)
	}
	return keys
}

// resolveFileExtension 从文件路径中提取扩展名，兼容 "path/to/file.go" 和 ".go"。

// filepath.Ext 对无扩展名路径返回空，尝试直接匹配
