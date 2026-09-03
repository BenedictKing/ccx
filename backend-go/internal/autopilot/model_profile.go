package autopilot

import (
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── 质量档 ──

// QualityTier 表示模型或 endpoint 的质量档位。
// 基于模型族推导（opus=premium, sonnet=high, haiku=normal），不来自 OriginTier。
type QualityTier string

const (
	QualityTierPremium QualityTier = "premium" // 旗舰：claude-opus, gpt-5.5, gpt-5.4
	QualityTierHigh    QualityTier = "high"    // 高端：claude-sonnet, gpt-5.3-codex
	QualityTierNormal  QualityTier = "normal"  // 标准：claude-haiku, gpt-5.4-mini/nano
	QualityTierLow     QualityTier = "low"     // 低端：其他
)

// ── 稳定性档 ──

// StabilityTier 表示 endpoint 的稳定性档位。
// 基于最近 1 小时的成功率和 429 率推导。
type StabilityTier string

const (
	StabilityTierStable   StabilityTier = "stable"   // 成功率 >= 95% 且 429 率 < 5%
	StabilityTierNormal   StabilityTier = "normal"   // 成功率 >= 80% 且 429 率 < 20%
	StabilityTierUnstable StabilityTier = "unstable" // 其他
)

// ── 速度档 ──

// SpeedTier 表示 endpoint 的速度档位。
// 基于最近 100 次请求的 p95 首 token 延迟推导。
type SpeedTier string

const (
	SpeedTierFast   SpeedTier = "fast"   // p95 < 500ms
	SpeedTierNormal SpeedTier = "normal" // p95 < 2000ms
	SpeedTierSlow   SpeedTier = "slow"   // p95 >= 2000ms
)

// ── 成本档 ──

// CostTier 表示 endpoint 的成本档位。
type CostTier string

const (
	CostTierFree      CostTier = "free"      // Input/Output 都是 0
	CostTierCheap     CostTier = "cheap"     // EffectiveInput < $1/M 且 EffectiveOutput < $5/M
	CostTierNormal    CostTier = "normal"    // EffectiveInput < $10/M 且 EffectiveOutput < $30/M
	CostTierExpensive CostTier = "expensive" // 其他
)

// ── 任务域 ──

// TaskDomain 表示请求的内容领域（审美、代码审核、算法等）。
// 与 TaskClass 正交：TaskClass 回答"谁在干活"，TaskDomain 回答"干的是什么活"。
type TaskDomain string

const (
	TaskDomainAestheticsUI TaskDomain = "aesthetics_ui" // 前端 UI/视觉设计/审美
	TaskDomainCodeReview   TaskDomain = "code_review"   // 代码审核/找 bug
	TaskDomainCoding       TaskDomain = "coding"        // 通用编码实现
	TaskDomainReasoning    TaskDomain = "reasoning"     // 算法/数学/复杂推理
	TaskDomainWriting      TaskDomain = "writing"       // 文案/长文写作
	TaskDomainTranslation  TaskDomain = "translation"   // 翻译
	TaskDomainAgentic      TaskDomain = "agentic"       // 多步工具调用/agent 编排
	TaskDomainGeneral      TaskDomain = "general"       // 无法细分的通用任务；缺少基准证据时中性
)

// ── 思考等级 ──

// EffortLevel 表示模型的统一思考能力档位。
// 调度时翻译为各派系原生参数（thinking.budget_tokens / reasoning_effort 等）。
type EffortLevel string

const (
	EffortOff     EffortLevel = "off"     // 不开思考
	EffortMinimal EffortLevel = "minimal" // 最低思考
	EffortLow     EffortLevel = "low"     // 低思考
	EffortMedium  EffortLevel = "medium"  // 中等思考
	EffortHigh    EffortLevel = "high"    // 高思考
	EffortXhigh   EffortLevel = "xhigh"   // 超高思考（实测最优档，成本高于 max）
	EffortMax     EffortLevel = "max"     // 最大思考（厂商满档，成本高于 xhigh）
	EffortUltra   EffortLevel = "ultra"   // 极致思考（厂商最高强度档，成本最高）
)

// ── ModelFamily 模型派系 ──

// ModelFamily 表示模型派系（厂商系列）。
// 用于派系偏好排序和质量档推导的基础分类。
type ModelFamily string

const (
	// ── 国际主流 ──
	ModelFamilyClaude  ModelFamily = "claude"  // claude-*，Anthropic
	ModelFamilyOpenAI  ModelFamily = "openai"  // gpt-*, o*, codex-*，OpenAI / Amazon Bedrock
	ModelFamilyGemini  ModelFamily = "gemini"  // gemini-*，Google
	ModelFamilyMistral ModelFamily = "mistral" // mistral-*, mixtral-*，Mistral AI
	ModelFamilyGrok    ModelFamily = "grok"    // grok-*，xAI
	ModelFamilyMuse    ModelFamily = "muse"    // muse-spark-*，Meta

	// ── 国产主流 ──
	ModelFamilyDeepSeek  ModelFamily = "deepseek"  // DeepSeek V3/V4，DeepSeek
	ModelFamilyQwen      ModelFamily = "qwen"      // qwen3-*，通义千问，DashScope
	ModelFamilyGLM       ModelFamily = "glm"       // glm-5-*，智谱 AI
	ModelFamilyKimi      ModelFamily = "kimi"      // kimi-k2-*，月之暗面 Moonshot
	ModelFamilyMiMo      ModelFamily = "mimo"      // mimo-v2-*，小米
	ModelFamilyERNIE     ModelFamily = "ernie"     // ernie-4.5，百度
	ModelFamilyDoubao    ModelFamily = "doubao"    // doubao-seed-*，字节豆包 Volcengine
	ModelFamilyMiniMax   ModelFamily = "minimax"   // minimax-m*，MiniMax
	ModelFamilyYi        ModelFamily = "yi"        // yi-*，零一万物 01.ai
	ModelFamilyBaichuan  ModelFamily = "baichuan"  // baichuan-m*，百川智能
	ModelFamilyStep      ModelFamily = "step"      // step-3.*，阶跃星辰 StepFun
	ModelFamilySenseNova ModelFamily = "sensenova" // sensenova-6.*，商汤 SenseTime
	ModelFamilyAgnes     ModelFamily = "agnes"     // agnes-2.*，Sapiens AI（小米独立系列）
	ModelFamilyLongCat   ModelFamily = "longcat"   // longcat-2.*，京东

	// ── 特殊 ──
	ModelFamilyLocal   ModelFamily = "local"   // ollama/lmstudio/llama-server 本地运行时
	ModelFamilyUnknown ModelFamily = "unknown" // 无法识别
)

// Provider → ModelFamily 映射表，与 model registry 的 provider 标识保持一致。
// providerFamilyMap 是全局只读映射，init 时构建。
var providerFamilyMap = map[string]ModelFamily{
	"anthropic":      ModelFamilyClaude,
	"openai":         ModelFamilyOpenAI,
	"amazon-bedrock": ModelFamilyOpenAI,
	"dashscope":      ModelFamilyQwen,
	"volcengine":     ModelFamilyDoubao,
	"xiaomi":         ModelFamilyMiMo, // 具体子系列需按前缀再细分
	"baidu":          ModelFamilyERNIE,
	"01-ai":          ModelFamilyYi,
	"moonshot":       ModelFamilyKimi,
	"zai":            ModelFamilyGLM,
	"deepseek":       ModelFamilyDeepSeek,
	"minimax":        ModelFamilyMiniMax,
	"baichuan":       ModelFamilyBaichuan,
	"stepfun":        ModelFamilyStep,
	"sensenova":      ModelFamilySenseNova,
	"agnes":          ModelFamilyAgnes,
	"longcat":        ModelFamilyLongCat,
	"mistral":        ModelFamilyMistral,
	"google":         ModelFamilyGemini,
}

// modelIDPrefixRules 定义模型 ID 前缀到派系的映射（兜底规则，优先级低于 provider 映射）。
// 按长度降序排列以确保最长前缀优先匹配。
var modelIDPrefixRules = []struct {
	prefix string
	family ModelFamily
}{
	// 国际
	{"claude-", ModelFamilyClaude},
	{"codex-", ModelFamilyOpenAI},
	{"gpt-", ModelFamilyOpenAI},
	{"o1-", ModelFamilyOpenAI},
	{"o3-", ModelFamilyOpenAI},
	{"o4-", ModelFamilyOpenAI},
	{"gemini-", ModelFamilyGemini},
	{"mistral-", ModelFamilyMistral},
	{"mixtral-", ModelFamilyMistral},
	{"grok-", ModelFamilyGrok},
	{"muse-spark-", ModelFamilyMuse},
	// 国产
	{"deepseek-", ModelFamilyDeepSeek},
	{"qwen3.", ModelFamilyQwen}, // 点号命名（qwen3.8-max），连字符 qwen3- 由下方 qwen- 兜底
	{"qwen3-", ModelFamilyQwen},
	{"qwen-", ModelFamilyQwen},
	{"glm-", ModelFamilyGLM},
	{"kimi-for-coding", ModelFamilyKimi},
	{"kimi-", ModelFamilyKimi},
	{"mimo-", ModelFamilyMiMo},
	{"ernie-", ModelFamilyERNIE},
	{"doubao-", ModelFamilyDoubao},
	{"minimax-", ModelFamilyMiniMax},
	{"yi-", ModelFamilyYi},
	{"baichuan-", ModelFamilyBaichuan},
	{"step-", ModelFamilyStep},
	{"sensenova-", ModelFamilySenseNova},
	{"agnes-", ModelFamilyAgnes},
	{"longcat-", ModelFamilyLongCat},
	// 特殊
	{"ollama/", ModelFamilyLocal},
	{"lmstudio/", ModelFamilyLocal},
}

// InferModelFamily 从 provider 字段或模型 ID 前缀推导模型派系。
// 优先使用 provider 映射（来自模型注册表的显式标注），回退到 modelID 前缀匹配。
// provider 参数可为空，此时仅依赖 modelID 前缀匹配。
func InferModelFamily(modelID, provider string) ModelFamily {
	// 优先级 1：provider 显式映射
	if provider != "" {
		normalized := strings.ToLower(strings.TrimSpace(provider))
		if family, ok := providerFamilyMap[normalized]; ok {
			// xiaomi 的子系列细分：mimo-v2-* 归 mimo，agnes-* 归 agnes
			if family == ModelFamilyMiMo {
				if strings.HasPrefix(strings.ToLower(modelID), "agnes-") {
					return ModelFamilyAgnes
				}
			}
			return family
		}
	}

	// 优先级 2：modelID 前缀匹配
	lowerID := strings.ToLower(strings.TrimSpace(modelID))
	if lowerID == "k3" || lowerID == "k3-256k" || strings.HasPrefix(lowerID, "k3[") {
		return ModelFamilyKimi
	}
	for _, rule := range modelIDPrefixRules {
		if strings.HasPrefix(lowerID, rule.prefix) {
			return rule.family
		}
	}

	return ModelFamilyUnknown
}

// ModelProfileQualityTierFromFamily 根据 ModelFamily 和 modelID 推导该模型的 QualityTier。
// 遵循设计 §3.4 QualityTier 推导规则（优先级 1：模型注册表中的模型族）。
func ModelProfileQualityTierFromFamily(family ModelFamily, modelID string) QualityTier {
	lowerID := strings.ToLower(modelID)

	switch family {
	case ModelFamilyClaude:
		if strings.Contains(lowerID, "opus") || strings.Contains(lowerID, "mythos") || strings.Contains(lowerID, "fable") {
			return QualityTierPremium
		}
		if strings.Contains(lowerID, "sonnet") {
			return QualityTierHigh
		}
		if strings.Contains(lowerID, "haiku") {
			return QualityTierNormal
		}
		return QualityTierNormal

	case ModelFamilyOpenAI:
		if strings.Contains(lowerID, "gpt-5.6") ||
			strings.Contains(lowerID, "gpt-5.5") ||
			strings.Contains(lowerID, "gpt-5.4") && !strings.Contains(lowerID, "mini") && !strings.Contains(lowerID, "nano") {
			return QualityTierPremium
		}
		if strings.Contains(lowerID, "gpt-5.3") || strings.Contains(lowerID, "gpt-5.2") {
			return QualityTierHigh
		}
		if strings.Contains(lowerID, "mini") || strings.Contains(lowerID, "nano") {
			return QualityTierNormal
		}
		return QualityTierNormal

	case ModelFamilyGemini:
		if strings.Contains(lowerID, "ultra") || strings.Contains(lowerID, "pro") {
			return QualityTierHigh
		}
		return QualityTierNormal

	case ModelFamilyGrok:
		// Grok 系列一直是 xAI 旗舰线；无实测分时按高端兜底
		return QualityTierPremium

	case ModelFamilyMuse:
		// Muse Spark 是 Meta 高端实验线；无实测分时按 high 兜底
		return QualityTierHigh

	case ModelFamilyDeepSeek:
		if strings.Contains(lowerID, "v4-pro") {
			return QualityTierHigh
		}
		return QualityTierNormal

	case ModelFamilyKimi:
		if lowerID == "k3" || lowerID == "kimi-k3" ||
			strings.HasPrefix(lowerID, "k3[") || strings.HasPrefix(lowerID, "kimi-k3[") ||
			lowerID == "k3-256k" || strings.HasPrefix(lowerID, "kimi-k3-256k") {
			return QualityTierPremium
		}
		if strings.Contains(lowerID, "kimi-for-coding") {
			return QualityTierHigh
		}
		if strings.Contains(lowerID, "k2.7") || strings.Contains(lowerID, "k2.6") {
			return QualityTierHigh
		}
		return QualityTierNormal

	case ModelFamilyGLM:
		if strings.Contains(lowerID, "glm-5.2") || strings.Contains(lowerID, "glm-5p2") {
			return QualityTierPremium
		}
		if strings.Contains(lowerID, "glm-5") {
			return QualityTierHigh
		}
		return QualityTierNormal

	case ModelFamilyMiMo:
		if strings.Contains(lowerID, "mimo-v2.5-pro") {
			return QualityTierHigh
		}
		return QualityTierNormal

	case ModelFamilyMiniMax:
		if strings.Contains(lowerID, "minimax-m3") {
			return QualityTierPremium
		}
		return QualityTierNormal

	case ModelFamilyQwen:
		if strings.Contains(lowerID, "max") {
			return QualityTierHigh
		}
		return QualityTierNormal

	default:
		return QualityTierLow
	}
}

// ── Benchmark 驱动的质量档推导 ──

// 默认质量档边界，在注册表直测分数不足时作为回退。
const (
	defaultBenchmarkTierPremiumMin = 75.0
	defaultBenchmarkTierHighMin    = 61.0
	defaultBenchmarkTierNormalMin  = 55.0
)

// effortQualityRatio 是各思考强度档相对常规口径（medium/default=1.0）的平均分数比率。
// 来源：deepswe v1.1 live leaderboard 同模型 effort 曲线统计（2026-08，
// 每档 n=7~10）：low=0.686、high=1.413、xhigh=1.627、max=1.975。
// 档位评定衡量模型的基础能力，须把"开满思考强度"的成绩折回常规口径，
// 否则 terra(max=69.6/medium=35.1)、luna(max=67.2/medium=11.3)这类
// 断崖曲线会被误评为旗舰档。
var effortQualityRatio = map[string]float64{
	"low":     0.686,
	"default": 1.0,
	"medium":  1.0,
	"high":    1.413,
	"xhigh":   1.627,
	"max":     1.975,
}

// minReliableBenchmarkTasks 是直测档位参与等效分与档位评定的最小任务格数。
// CodexRadar 新模型常先跑极少量任务：1 格全过即 pass@1=100%，纯属噪声
// （2026-08 hy4-preview low 档 1/1 曾把插值等效分虚抬进 premium 档）。
// TaskCount==0 表示来源未提供任务数（兼容旧数据），不视为小样本。
const minReliableBenchmarkTasks = 3

// isSmallSampleEvidence 报告一条直测证据是否属于小样本档位。
func isSmallSampleEvidence(ev config.ModelBenchmarkEvidence) bool {
	return ev.TaskCount > 0 && ev.TaskCount < minReliableBenchmarkTasks
}

// regularEffortBaselineScore 从直测证据提取常规 effort 口径（medium/default）的 coding 分。
// 证据按来源（deepswe / codexradar）分组，再按证据等级分层合成，升级仅作用于
// 缺 medium 直测的模型（有直测的模型行为与旧算法一致）：
//  1. 有 medium/default 实测 → 跨源取最大（与旧合并语义一致，证据最充分）；
//  2. 曲线跨 medium（如 low+high）→ 相邻两档按 effort 序数线性插值，
//     源间取最小（模型自身的 effort 曲线是对其中档水平的最好估计，但插值
//     属估计值，源间分歧保守取低）；
//  3. 全部在一侧或仅剩单点 → 跨源合并各档后按全局平均比率折算取最小值
//     （保守：断崖曲线的高档成绩不会高估基础能力），仅剩单档时返回
//     singleEffortOnly=true（折算完全依赖全局曲线，不足以支撑 premium，
//     调用方应封顶 high）。
//
// 无直测证据时 ok=false。
func regularEffortBaselineScore(evidence []config.ModelBenchmarkEvidence) (score float64, singleEffortOnly bool, ok bool) {
	// 按来源收集各档原始分（百分制），同档多条证据取最大。小样本档位
	// （任务格数不足）视为未完成测量，不参与校准。
	bySource := map[string]map[EffortLevel]float64{}
	for _, ev := range evidence {
		if ev.Domain != "coding" || ev.Metric != "pass_at_1" {
			continue
		}
		if ev.Benchmark != "deepswe" && ev.Benchmark != "codexradar" {
			continue
		}
		if isSmallSampleEvidence(ev) {
			continue
		}
		level := NormalizeEffortLevel(ev.Effort)
		if level == "" {
			continue
		}
		efforts := bySource[ev.Benchmark]
		if efforts == nil {
			efforts = map[EffortLevel]float64{}
			bySource[ev.Benchmark] = efforts
		}
		if raw := ev.RawValue * 100; raw > efforts[level] {
			efforts[level] = raw
		}
	}
	if len(bySource) == 0 {
		return 0, false, false
	}

	// 第 1 层：实测常规口径，跨源取最大（与旧合并语义一致）。
	regularBest := -1.0
	for _, efforts := range bySource {
		if s, hit := efforts[EffortMedium]; hit && s > regularBest {
			regularBest = s
		}
	}
	if regularBest >= 0 {
		return regularBest, false, true
	}

	// 第 2 层：跨 medium 的同源曲线插值，源间取最小。
	straddleWorst := -1.0
	for _, efforts := range bySource {
		if s, hit := interpolatedMediumScore(efforts); hit && (straddleWorst < 0 || s < straddleWorst) {
			straddleWorst = s
		}
	}
	if straddleWorst >= 0 {
		return straddleWorst, false, true
	}

	// 第 3 层：全部在一侧或单点，跨源合并后全局比率折算取最小。
	pooled := map[EffortLevel]float64{}
	for _, efforts := range bySource {
		for level, raw := range efforts {
			if raw > pooled[level] {
				pooled[level] = raw
			}
		}
	}
	worst := -1.0
	for level, raw := range pooled {
		deflated := raw / effortQualityRatio[string(level)]
		if worst < 0 || deflated < worst {
			worst = deflated
		}
	}
	return worst, len(pooled) == 1, true
}

// interpolatedMediumScore 在单来源的 effort 曲线跨 medium 时，取相邻两档按序数线性插值。
func interpolatedMediumScore(efforts map[EffortLevel]float64) (score float64, ok bool) {
	levels := make([]EffortLevel, 0, len(efforts))
	for level := range efforts {
		levels = append(levels, level)
	}
	sort.Slice(levels, func(i, j int) bool {
		return EffortLevelOrdinal(levels[i]) < EffortLevelOrdinal(levels[j])
	})
	regularOrd := EffortLevelOrdinal(EffortMedium)
	for i := 0; i+1 < len(levels); i++ {
		lo, hi := EffortLevelOrdinal(levels[i]), EffortLevelOrdinal(levels[i+1])
		if lo < regularOrd && hi > regularOrd {
			t := float64(regularOrd-lo) / float64(hi-lo)
			return efforts[levels[i]] + (efforts[levels[i+1]]-efforts[levels[i]])*t, true
		}
	}
	return 0, false
}

// directBenchmarkScoreFromEvidence 从证据列表提取直接实测 coding 分数（DeepSWE 等价 0-100 分）。
// 只接受 deepswe / codexradar 的 pass_at_1 实测值，不含 calibrated 估计值。
// 注意：取的是最佳档原始分（含思考强度加成），仅供按 effort 口径展示；
// 档位评定与边界计算必须用 regularEffortBaselineScore 的常规口径分。
func directBenchmarkScoreFromEvidence(evidence []config.ModelBenchmarkEvidence) float64 {
	best := -1.0
	for _, ev := range evidence {
		if ev.Domain != "coding" || ev.Metric != "pass_at_1" {
			continue
		}
		if ev.Benchmark != "deepswe" && ev.Benchmark != "codexradar" {
			continue
		}
		if raw := ev.RawValue * 100; raw > best {
			best = raw
		}
	}
	return best
}

// normalizedCapabilityScoreWithEvidenceClass 把不同 benchmark 的 coding 证据归一化到 DeepSWE 等价的
// 0-100 分，并统一到常规 effort 口径（medium/default）。
// 优先级：直测常规口径分（regularEffortBaselineScore）> artificial_analysis
// coding_index 线性校准。无任何可用证据时返回 -1。
// singleEffortOnly 表示直测仅剩单一非常规档（无论高低），证据不足以支撑
// premium（调用方 ModelProfileQualityTier 据此封顶）。
func normalizedCapabilityScoreWithEvidenceClass(modelID string) (float64, bool) {
	benchmark := config.ResolveModelBenchmarkProfile(modelID)
	if !benchmark.Known {
		return -1, false
	}
	if score, singleEffortOnly, ok := regularEffortBaselineScore(benchmark.Profile.BenchmarkEvidence); ok {
		return score, singleEffortOnly
	}
	bestAACoding := -1.0
	for _, ev := range benchmark.Profile.BenchmarkEvidence {
		if ev.Domain == "coding" && ev.Benchmark == "artificial_analysis" && ev.Metric == "coding_index" && ev.RawValue > bestAACoding {
			bestAACoding = ev.RawValue
		}
	}
	if bestAACoding >= 0 {
		// 系数来自注册表中同时具有 deepswe/codexradar 实测分与 AA coding_index 的
		// 重叠模型的最小二乘线性拟合；基准数据大改后需重算。
		// deepswe ≈ 2.391 * aa_coding - 116.007
		return 2.391*bestAACoding - 116.007, false
	}
	return -1, false
}

// normalizedCapabilityScore 返回常规 effort 口径的归一化能力分（无证据类信息）。
func normalizedCapabilityScore(modelID string) float64 {
	score, _ := normalizedCapabilityScoreWithEvidenceClass(modelID)
	return score
}

// benchmarkTierBoundariesCache 缓存质量档边界，以注册表世代号做缓存键：
// 基准数据不变时直接复用，避免热路径（每请求画像、渠道评分）重复深拷贝
// 整个注册表并重算分布。
var benchmarkTierBoundariesCache atomic.Pointer[benchmarkTierBoundaries]

type benchmarkTierBoundaries struct {
	generation uint64
	premiumMin float64
	highMin    float64
	normalMin  float64
}

// computeQualityTierBoundaries 返回质量档边界（带世代缓存）。
func computeQualityTierBoundaries() (premiumMin, highMin, normalMin float64) {
	generation := config.BuiltinSnapshotGeneration()
	if cached := benchmarkTierBoundariesCache.Load(); cached != nil && cached.generation == generation {
		return cached.premiumMin, cached.highMin, cached.normalMin
	}
	premiumMin, highMin, normalMin = computeQualityTierBoundariesFromRegistry()
	benchmarkTierBoundariesCache.Store(&benchmarkTierBoundaries{
		generation: generation,
		premiumMin: premiumMin,
		highMin:    highMin,
		normalMin:  normalMin,
	})
	return
}

// computeQualityTierBoundariesFromRegistry 从注册表直接 benchmark 证据的分数分布自动划分质量档边界。
// 算法：分数排序后自顶向下分段寻找最大间隙（自然断层）——premium 边界取顶部四分位
// （按名次）区间内最大间隙的中点，high / normal 依次在低于上一档边界的分段中
// 找最大间隙。calibrated 估计值不参与边界计算，避免污染分布形态。
// 数据不足时回退默认边界。
func computeQualityTierBoundariesFromRegistry() (premiumMin, highMin, normalMin float64) {
	premiumMin, highMin, normalMin = defaultBenchmarkTierPremiumMin, defaultBenchmarkTierHighMin, defaultBenchmarkTierNormalMin
	profiles := config.BuiltinModelBenchmarkProfiles()
	scores := make([]float64, 0, len(profiles))
	seen := make(map[string]struct{}, len(profiles))
	for _, bp := range profiles {
		// pattern 别名与 canonical 模型共享同一 CanonicalModel，按 canonical 去重，
		// 每个模型只计一次常规口径分（与模型侧评分同口径，避免边界线与
		// 模型分数量纲不一致）。
		if _, ok := seen[bp.CanonicalModel]; ok {
			continue
		}
		seen[bp.CanonicalModel] = struct{}{}
		if score, _, ok := regularEffortBaselineScore(bp.BenchmarkEvidence); ok {
			scores = append(scores, score)
		}
	}
	if len(scores) < 4 {
		return
	}
	sort.Float64s(scores)

	// largestGapMid 返回 vals 中完全位于区域内的最大间隙的中点：
	// 间隙两端都必须 >= floor。只查上端会让低段的跨区域间隙被误当成顶部断层。
	largestGapMid := func(vals []float64, floor float64) (float64, bool) {
		bestSize, bestMid := 0.0, 0.0
		found := false
		for i := 0; i+1 < len(vals); i++ {
			if vals[i] < floor || vals[i+1] < floor {
				continue
			}
			if size := vals[i+1] - vals[i]; size > bestSize {
				bestSize, bestMid, found = size, (vals[i]+vals[i+1])/2, true
			}
		}
		return bestMid, found
	}

	// premium 断层在顶部四分位（按名次）区间内寻找。区域锚定经历两代修正：
	// 60% 分数锚假设分布铺满量表，分数集中时把中段空隙包进"顶部区域"，
	// premiumMin 一度塌到 49；改为 75% 分数锚后又对榜首离群值敏感——单个
	// 高分新模型入场抬高锚线，把定义 premium 档的最大自然断层挤出区域
	// （8 分间隙因下端 56.2 < 76.1×0.75 被排除），premiumMin 从 60 跳到 73，
	// premium 档塌缩成 singleton。按名次取顶部四分位与分数量纲解耦，
	// 区域成员不受榜首分数波动影响，两个方向都稳定。
	if mid, ok := largestGapMid(scores, scores[(3*len(scores))/4]); ok {
		premiumMin = mid
	}
	if rest := filterBelow(scores, premiumMin); len(rest) >= 2 {
		if mid, ok := largestGapMid(rest, premiumMin*0.5); ok {
			highMin = mid
		}
	}
	if rest := filterBelow(scores, highMin); len(rest) >= 2 {
		if mid, ok := largestGapMid(rest, highMin*0.4); ok {
			normalMin = mid
		}
	}
	return
}

// filterBelow 返回 vals 中小于 cut 的元素（保持有序）。
func filterBelow(vals []float64, cut float64) []float64 {
	out := make([]float64, 0, len(vals))
	for _, v := range vals {
		if v < cut {
			out = append(out, v)
		}
	}
	return out
}

// ModelProfileQualityTier 优先按常规 effort 口径的归一化能力分推导质量档，
// 无 benchmark 时回退到模型族规则。仅剩单一非常规档（低档或高思考档）直测证据时
// 封顶 high：全局平均曲线的折算不足以支撑 premium（等该模型补测常规口径）。
func ModelProfileQualityTier(modelID string, family ModelFamily) QualityTier {
	score, singleEffortOnly := normalizedCapabilityScoreWithEvidenceClass(modelID)
	if score >= 0 {
		premiumMin, highMin, normalMin := computeQualityTierBoundaries()
		switch {
		case score >= premiumMin && !singleEffortOnly:
			return QualityTierPremium
		case score >= highMin:
			return QualityTierHigh
		case score >= normalMin:
			return QualityTierNormal
		default:
			return QualityTierLow
		}
	}
	return ModelProfileQualityTierFromFamily(family, modelID)
}

// ── ModelProfile ──

// ModelProfile 是每个 (KeyEndpoint + 模型) 组合的画像。
type ModelProfile struct {
	// ── 锚定到 KeyEndpoint ──
	ChannelUID  string    `json:"channelUid"`
	ChannelID   int       `json:"channelId"` // 当前配置数组 index，仅用于展示/兼容
	ChannelKind string    `json:"channelKind"`
	ServiceType string    `json:"serviceType"`
	MetricsKey  string    `json:"metricsKey"` // 精确到 identityBaseURL + key + serviceType
	ModelID     string    `json:"modelId"`    // 该 endpoint 内的实际模型名
	UpdatedAt   time.Time `json:"updatedAt"`

	// ── 能力 ──
	ModelFamily       ModelFamily `json:"modelFamily"` // 派系，从注册表推导
	QualityTier       QualityTier `json:"qualityTier"` // 基于模型族的质量档
	SpeedTier         SpeedTier   `json:"speedTier"`
	ContextTokens     int         `json:"contextTokens"`
	SupportsVision    bool        `json:"supportsVision"`
	SupportsDocument  bool        `json:"supportsDocument"`
	SupportsToolCalls bool        `json:"supportsToolCalls"`
	SupportsReasoning bool        `json:"supportsReasoning"`

	// ── 上游供应商质量（同模型在不同上游的质量差异）──
	ProviderQualityScore        float64 `json:"providerQualityScore,omitempty"`        // 0.0-1.0
	ProviderQualitySource       string  `json:"providerQualitySource,omitempty"`       // probe | user_feedback | inferred | default
	ProviderQualityConfidence   float64 `json:"providerQualityConfidence,omitempty"`   // 置信度
	ProviderQualityProbeVersion string  `json:"providerQualityProbeVersion,omitempty"` // 固定 canary 版本，仅 source=probe 时有值

	// ── 任务域优势（不同模型的强项任务不同）──
	// 缺省时回退到 ModelFamily 级种子矩阵（§5.7），0.5 = 中性
	TaskDomainStrengths map[TaskDomain]float64 `json:"taskDomainStrengths,omitempty"`

	// ── 思考等级（同模型不同思考档位的智商差异，§5.8）──
	SupportsEffortControl bool          `json:"supportsEffortControl,omitempty"` // 上游是否可控思考等级
	SupportedEffortLevels []EffortLevel `json:"supportedEffortLevels,omitempty"` // 可用档位（按派系映射）

	// ── 探测结果 ──
	ProbeSuccess    bool      `json:"probeSuccess"`
	LastProbeAt     time.Time `json:"lastProbeAt"`
	ProbeLatencyMs  int64     `json:"probeLatencyMs"`
	ProbeConfidence float64   `json:"probeConfidence"`

	// ── 来源 ──
	Source string `json:"source"` // builtin_registry | auto_probe | capability_test | manual
}

// upstreamCapabilitySupportsReasoning 判断模型注册表能力是否支持推理。
// capabilities.reasoning 与 thinkingMode/reasoningEfforts 任一命中即视为支持：
// 部分模型（如 grok-4.5）会推理但不可控思考档位，只登记 capabilities.reasoning。
// 路由硬约束（buildChannelEntry）与模型画像（applyUpstreamModelCapability）必须共用
// 此口径，否则同一模型会在候选表被标"推理能力不满足"的同时被请求期解析选中。
func upstreamCapabilitySupportsReasoning(capability config.UpstreamModelCapability) bool {
	return capability.ThinkingMode != "" ||
		capability.Capabilities["reasoning"] ||
		len(capability.ReasoningEfforts) > 0
}

// applyUpstreamModelCapability 将模型注册表中的上游能力写入模型画像。
// 该能力描述实际发送给供应商的模型，不应与下游客户端 AgentModelProfile 混用。
func applyUpstreamModelCapability(profile *ModelProfile, capability config.UpstreamModelCapability) {
	if profile == nil {
		return
	}
	profile.ContextTokens = capability.ContextWindowTokens
	profile.SupportsVision = capability.Capabilities["vision"]
	profile.SupportsDocument = capability.Capabilities["document"]
	profile.SupportsToolCalls = capability.Capabilities["toolCalls"] ||
		capability.Capabilities["tool_calls"] || capability.Capabilities["tools"]
	profile.SupportsReasoning = upstreamCapabilitySupportsReasoning(capability)
}
