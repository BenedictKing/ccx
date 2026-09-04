package autopilot

import (
	"sort"
	"strings"
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

// 固定质量档阈值（版本化）。生产热路径直接使用，不随注册表刷新动态重算：
// 动态聚类会让每次数据同步都重排全部模型档位（2026-09-04 hy4-preview 的
// 小样本插值 76.1 曾把动态 premiumMin 从 60.70 推到 74.45，全榜连锁换档）。
// 阈值由离线校准从 medium/default 直测分布推导（锚定测试见
// TestQualityTierThresholdsAnchoredToDirectEvidence）：取 2026-09-04 快照
// 直测池（16 模型）升序分布的最大间隙断层中点——
// premium = 68.9↔71.0、high = 54.0↔64.3、normal = 39.8↔48.7。
// 调整阈值必须升版本号并在 CHANGELOG 说明掉档/升档面。
const (
	qualityTierThresholdsVersion = "fixed-direct-calibration-v2-2026-09-04"
	qualityTierPremiumMin        = 69.95
	qualityTierHighMin           = 59.15
	qualityTierNormalMin         = 44.25
)

// computeQualityTierBoundaries 返回质量档边界。阈值固定版本化（见上），
// 注册表直测分布只在离线校准（锚定测试）中验证其合理性，不进入请求热路径。
func computeQualityTierBoundaries() (premiumMin, highMin, normalMin float64) {
	return qualityTierPremiumMin, qualityTierHighMin, qualityTierNormalMin
}

// EvidenceClass 标注校准分数的证据等级，决定该分数可证明的最高质量档。
type EvidenceClass string

const (
	// EvidenceDirect：medium/default 可靠直测。唯一可证明 premium 的等级。
	EvidenceDirect EvidenceClass = "direct"
	// EvidenceInterpolated：同源 effort 曲线跨 medium 相邻档线性插值。
	// 用了模型自身曲线，可信度高于全局折算，但终属估计，封顶 high。
	EvidenceInterpolated EvidenceClass = "interpolated"
	// EvidenceDeflated：无跨 medium 曲线，按全局平均比率折算（单档或单侧多档）。
	// 完全依赖全局曲线，封顶 high（含旧 singleEffortOnly 语义）。
	EvidenceDeflated EvidenceClass = "deflated"
	// EvidenceCalibrated：artificial_analysis coding_index 线性校准，封顶 high。
	EvidenceCalibrated EvidenceClass = "calibrated"
)

// maxTierProvableByEvidence 返回各证据等级可证明的最高质量档：
// 只有常规口径直测可以证明 premium，估计类证据一律封顶 high。
func maxTierProvableByEvidence(class EvidenceClass) QualityTier {
	if class == EvidenceDirect {
		return QualityTierPremium
	}
	return QualityTierHigh
}

// CalibrationResult 结构化承载常规 effort 口径的校准结果：
// 分数 + 证据等级 + 观测档位，替代此前散落的 (score, singleEffortOnly) 元组。
type CalibrationResult struct {
	Score float64
	Class EvidenceClass
	// MeasuredEffort 是该分数的观测档位：direct 为 medium；
	// deflated 为实际参与折算的非常规档；interpolated 为 medium（两档中点口径）。
	MeasuredEffort EffortLevel
}

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

func directRegularEffortScore(evidence []config.ModelBenchmarkEvidence) (float64, bool) {
	best := -1.0
	for _, ev := range evidence {
		if ev.Domain != "coding" || ev.Metric != "pass_at_1" ||
			(ev.Benchmark != "deepswe" && ev.Benchmark != "codexradar") ||
			isSmallSampleEvidence(ev) {
			continue
		}
		if NormalizeEffortLevel(ev.Effort) != EffortMedium {
			continue
		}
		if score := ev.RawValue * 100; score > best {
			best = score
		}
	}
	return best, best >= 0
}

// calibrateRegularEffort 从直测证据提取常规 effort 口径（medium/default）的 coding 分，
// 并标注证据等级。证据按来源（deepswe / codexradar）分组，再按证据等级分层合成，
// 升级仅作用于缺 medium 直测的模型（有直测的模型行为与旧算法一致）：
//  1. 有 medium/default 实测 → 跨源取最大（与旧合并语义一致，证据最充分），EvidenceDirect；
//  2. 曲线跨 medium（如 low+high）→ 相邻两档按 effort 序数线性插值，
//     源间取最小（模型自身的 effort 曲线是对其中档水平的最好估计，但插值
//     属估计值，源间分歧保守取低），EvidenceInterpolated；
//  3. 全部在一侧或仅剩单点 → 跨源合并各档后按全局平均比率折算取最小值
//     （保守：断崖曲线的高档成绩不会高估基础能力），EvidenceDeflated。
//
// 估计类证据（interpolated/deflated）封顶 high，由 maxTierProvableByEvidence 统一表达。
// 无直测证据时 ok=false。
func calibrateRegularEffort(evidence []config.ModelBenchmarkEvidence) (CalibrationResult, bool) {
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
		return CalibrationResult{}, false
	}

	// 第 1 层：实测常规口径，跨源取最大（与旧合并语义一致）。
	regularBest := -1.0
	for _, efforts := range bySource {
		if s, hit := efforts[EffortMedium]; hit && s > regularBest {
			regularBest = s
		}
	}
	if regularBest >= 0 {
		return CalibrationResult{Score: regularBest, Class: EvidenceDirect, MeasuredEffort: EffortMedium}, true
	}

	// 第 2 层：跨 medium 的同源曲线插值，源间取最小。
	straddleWorst := -1.0
	for _, efforts := range bySource {
		if s, hit := interpolatedMediumScore(efforts); hit && (straddleWorst < 0 || s < straddleWorst) {
			straddleWorst = s
		}
	}
	if straddleWorst >= 0 {
		return CalibrationResult{Score: straddleWorst, Class: EvidenceInterpolated, MeasuredEffort: EffortMedium}, true
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
	worstLevel := EffortMedium
	for level, raw := range pooled {
		deflated := raw / effortQualityRatio[string(level)]
		if worst < 0 || deflated < worst {
			worst = deflated
			worstLevel = level
		}
	}
	return CalibrationResult{Score: worst, Class: EvidenceDeflated, MeasuredEffort: worstLevel}, true
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

// calibrateModelCapability 把不同 benchmark 的 coding 证据归一化到 DeepSWE 等价的
// 0-100 分，统一到常规 effort 口径（medium/default），并携带证据等级。
// 优先级：直测常规口径分（calibrateRegularEffort）> artificial_analysis
// coding_index 线性校准（EvidenceCalibrated，封顶 high）。
// 模型不在注册表时 ok=false。
func calibrateModelCapability(modelID string) (CalibrationResult, bool) {
	benchmark := config.ResolveModelBenchmarkProfile(modelID)
	if !benchmark.Known {
		return CalibrationResult{}, false
	}
	if result, ok := calibrateRegularEffort(benchmark.Profile.BenchmarkEvidence); ok {
		return result, true
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
		return CalibrationResult{
			Score:          2.391*bestAACoding - 116.007,
			Class:          EvidenceCalibrated,
			MeasuredEffort: EffortMedium,
		}, true
	}
	return CalibrationResult{}, false
}

// normalizedCapabilityScore 返回常规 effort 口径的归一化能力分（无证据类信息）。
func normalizedCapabilityScore(modelID string) float64 {
	result, ok := calibrateModelCapability(modelID)
	if !ok {
		return -1
	}
	return result.Score
}

// qualityTierFromCalibration 按固定阈值与证据等级上限评定质量档：
// 分数决定档位，证据等级封顶（估计类证据不得证明 premium）。
func qualityTierFromCalibration(calib CalibrationResult) QualityTier {
	premiumMin, highMin, normalMin := computeQualityTierBoundaries()
	cap := maxTierProvableByEvidence(calib.Class)
	tier := QualityTierLow
	switch {
	case calib.Score >= premiumMin:
		tier = QualityTierPremium
	case calib.Score >= highMin:
		tier = QualityTierHigh
	case calib.Score >= normalMin:
		tier = QualityTierNormal
	}
	if qualityTierRank(tier) > qualityTierRank(cap) {
		return cap
	}
	return tier
}

// ModelProfileQualityTier 优先按常规 effort 口径的归一化能力分推导质量档，
// 无 benchmark 时回退到模型族规则。估计类证据（插值/折算/校准）封顶 high：
// premium 必须有常规口径直测证明（等该模型补测 medium/default）。
func ModelProfileQualityTier(modelID string, family ModelFamily) QualityTier {
	if calib, ok := calibrateModelCapability(modelID); ok {
		return qualityTierFromCalibration(calib)
	}
	return ModelProfileQualityTierFromFamily(family, modelID)
}

// directEffortScore 返回指定 effort 档的可靠直测 coding 分（跨源取最大）。
// ok=false 表示该档无可靠直测（无证据或小样本）。
func directEffortScore(evidence []config.ModelBenchmarkEvidence, level EffortLevel) (float64, bool) {
	best := -1.0
	for _, ev := range evidence {
		if ev.Domain != "coding" || ev.Metric != "pass_at_1" ||
			(ev.Benchmark != "deepswe" && ev.Benchmark != "codexradar") ||
			isSmallSampleEvidence(ev) {
			continue
		}
		if NormalizeEffortLevel(ev.Effort) != level {
			continue
		}
		if score := ev.RawValue * 100; score > best {
			best = score
		}
	}
	return best, best >= 0
}

// interpolatedEffortScore 用同源 effort 曲线中紧邻目标档两侧的实测点
// 线性插值出该档估计分，源间取最小（插值属估计值，保守取低）。
// ok=false 表示没有任何来源的曲线能覆盖该档。
func interpolatedEffortScore(evidence []config.ModelBenchmarkEvidence, level EffortLevel) (float64, bool) {
	targetOrd := EffortLevelOrdinal(level)
	worst := -1.0
	bySource := map[string]map[EffortLevel]float64{}
	for _, ev := range evidence {
		if ev.Domain != "coding" || ev.Metric != "pass_at_1" ||
			(ev.Benchmark != "deepswe" && ev.Benchmark != "codexradar") ||
			isSmallSampleEvidence(ev) {
			continue
		}
		norm := NormalizeEffortLevel(ev.Effort)
		if norm == "" {
			continue
		}
		efforts := bySource[ev.Benchmark]
		if efforts == nil {
			efforts = map[EffortLevel]float64{}
			bySource[ev.Benchmark] = efforts
		}
		if raw := ev.RawValue * 100; raw > efforts[norm] {
			efforts[norm] = raw
		}
	}
	for _, efforts := range bySource {
		var lo, hi *struct {
			ord   int
			score float64
		}
		for lv, score := range efforts {
			ord := EffortLevelOrdinal(lv)
			if ord < targetOrd && (lo == nil || ord > lo.ord) {
				lo = &struct {
					ord   int
					score float64
				}{ord, score}
			}
			if ord > targetOrd && (hi == nil || ord < hi.ord) {
				hi = &struct {
					ord   int
					score float64
				}{ord, score}
			}
		}
		if lo == nil || hi == nil {
			continue
		}
		t := float64(targetOrd-lo.ord) / float64(hi.ord-lo.ord)
		estimate := lo.score + (hi.score-lo.score)*t
		if worst < 0 || estimate < worst {
			worst = estimate
		}
	}
	return worst, worst >= 0
}

// EffortAwareQualityTier 评定 (模型, 思考强度) 组合的质量档，是模型×effort
// 候选过滤与 advisor MinQualityTier 豁免的统一口径：
//  1. 该档有可靠直测 → 直测分按固定阈值评档（direct 证据可证明 premium）；
//  2. 该档无直测但同模型曲线可插值 → 估计分评档，封顶 high；
//  3. 无该档证据 → 回落模型基础档（medium 口径）：effort 不低于 medium 时
//     沿用基础档（effort 曲线单调不减），低于 medium 时按全局比率保守下调
//     并封顶 high（low 档能力可能显著低于常规口径）。
//
// 模型不在注册表时回退模型族规则（与 ModelProfileQualityTier 一致）。
// effort 为空表示未指定，等价 medium 口径（与模型基础档相同）。
func EffortAwareQualityTier(modelID string, effort EffortLevel, family ModelFamily) QualityTier {
	benchmark := config.ResolveModelBenchmarkProfile(modelID)
	if !benchmark.Known {
		return ModelProfileQualityTierFromFamily(family, modelID)
	}
	evidence := benchmark.Profile.BenchmarkEvidence

	// 1. 该档可靠直测。
	if score, ok := directEffortScore(evidence, effort); ok {
		return qualityTierFromCalibration(CalibrationResult{
			Score:          score,
			Class:          EvidenceDirect,
			MeasuredEffort: effort,
		})
	}

	// 2. 同模型曲线插值该档（估计值，封顶 high）。
	if score, ok := interpolatedEffortScore(evidence, effort); ok {
		return qualityTierFromCalibration(CalibrationResult{
			Score:          score,
			Class:          EvidenceInterpolated,
			MeasuredEffort: effort,
		})
	}

	// 3. 回落模型基础档。
	calib, ok := calibrateModelCapability(modelID)
	if !ok {
		return ModelProfileQualityTierFromFamily(family, modelID)
	}
	if effort == "" || EffortLevelOrdinal(effort) >= EffortLevelOrdinal(EffortMedium) {
		return qualityTierFromCalibration(calib)
	}
	deflated := CalibrationResult{
		Score:          calib.Score / effortQualityRatio[string(effort)],
		Class:          EvidenceDeflated,
		MeasuredEffort: effort,
	}
	return qualityTierFromCalibration(deflated)
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

// requestEffortOfProfile 返回请求实际生效的思考等级：手动意图 pin 优先于
// 客户端显式声明；空表示未指定（按 medium 口径评定，与模型基础档同义）。
func requestEffortOfProfile(profile *RequestProfile) EffortLevel {
	if profile == nil {
		return ""
	}
	if profile.IntentEffortPin != nil && profile.IntentEffortPin.Set && profile.IntentEffortPin.Effort != "" {
		return profile.IntentEffortPin.Effort
	}
	if profile.ClientEffort != "" {
		return profile.ClientEffort
	}
	return ""
}
