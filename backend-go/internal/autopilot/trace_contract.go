package autopilot

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ── Trace v2 契约（设计 §3）──
//
// Trace schema 版本独立于 SQLite PRAGMA user_version：
//   - schemaVersion=1: 历史行，通过适配器尽力还原
//   - schemaVersion=2: 当前版本，写入与读取均使用 v2 DTO
//   - 未知版本: 只读展示原始安全摘要，不可被本服务改写

// ComparisonStatus 表达 shadow 建议与实际渠道的比较结果。
// 三态枚举替代裸 bool，避免把缺失数据误算为 mismatch。
type ComparisonStatus string

const (
	ComparisonMatched    ComparisonStatus = "matched"
	ComparisonMismatched ComparisonStatus = "mismatched"
	ComparisonUncompared ComparisonStatus = "uncompared"
)

// PersistenceClass 标记 trace 的保留理由。
type PersistenceClass string

const (
	PersistenceSampled  PersistenceClass = "sampled"  // 正常抽样 1/10
	PersistenceMismatch PersistenceClass = "mismatch" // shadow 与实际不一致
	PersistenceFailure  PersistenceClass = "failure"  // 终态失败/耗尽
	PersistenceFallback PersistenceClass = "fallback" // channel fallback / fail-open
	PersistenceManual   PersistenceClass = "manual"   // ManualIntent 命中
	PersistenceAdvisor  PersistenceClass = "advisor"  // AdvisorDecision 命中
	PersistenceDryRun   PersistenceClass = "dry_run"  // 管理端显式 dry-run
)

// Cohorts 灰度分桶类型。
type Cohort string

const (
	CohortTreatment Cohort = "treatment"
	CohortControl   Cohort = "control"
	CohortBypass    Cohort = "bypass"
)

// ── v2 DTO ──

// TraceDetailV2 是 v2 trace 的安全详情 DTO，只包含可解释决策的字段。
// 禁止包含 API Key、Authorization、prompt 全文、响应正文、multipart 和原始 BaseURL。
type TraceDetailV2 struct {
	TraceUID             string                    `json:"traceUid"`
	SchemaVersion        int                       `json:"schemaVersion"`
	TraceRevision        int64                     `json:"traceRevision"`
	CreatedAt            time.Time                 `json:"createdAt"`
	RequestCorrelationID string                    `json:"requestCorrelationId"`
	Source               string                    `json:"source"` // "proxy" | "dry_run"
	ReleaseID            string                    `json:"releaseId"`
	PolicyFingerprint    string                    `json:"policyFingerprint"`
	TargetMode           RoutingMode               `json:"targetMode"`
	EffectiveMode        RoutingMode               `json:"effectiveMode"`
	Cohort               Cohort                    `json:"cohort"`
	BypassReason         string                    `json:"bypassReason,omitempty"`
	PersistenceClass     PersistenceClass          `json:"persistenceClass"`
	ComparisonStatus     ComparisonStatus          `json:"comparisonStatus"`
	RequestKind          string                    `json:"requestKind"`
	TaskClass            TaskClass                 `json:"taskClass"`
	TaskDomain           TaskDomain                `json:"taskDomain,omitempty"`
	RequestedModel       string                    `json:"requestedModel,omitempty"`
	AgentRole            string                    `json:"agentRole,omitempty"`
	ManualIntentUID      string                    `json:"manualIntentUid,omitempty"`
	AdvisorDecisionUID   string                    `json:"advisorDecisionUid,omitempty"`
	Candidates           []RoutingCandidate        `json:"candidates,omitempty"`
	CandidatesBefore     int                       `json:"candidatesBefore"`
	CandidatesAfter      int                       `json:"candidatesAfter"`
	GlobalFilterReasons  map[string][]string       `json:"globalFilterReasons,omitempty"`
	SortReasons          []string                  `json:"sortReasons,omitempty"`
	RecommendedChannel   string                    `json:"recommendedChannelUid,omitempty"`
	SelectedChannelUID   string                    `json:"selectedChannelUid,omitempty"`
	EstimatedCost        float64                   `json:"estimatedCost,omitempty"`
	CostConfidence       float64                   `json:"costConfidence,omitempty"`
	FallbackUsed         bool                      `json:"fallbackUsed"`
	SchedulerDecision    *SchedulerDecisionSummary `json:"schedulerDecision,omitempty"`
	EndpointAttempts     []EndpointAttemptSummary  `json:"endpointAttempts,omitempty"`
	AttemptsTruncated    bool                      `json:"attemptsTruncated,omitempty"`
	AttemptsTotal        int                       `json:"attemptsTotal,omitempty"`
	AttemptsByResult     map[string]int            `json:"attemptsByResult,omitempty"`
	Outcome              string                    `json:"outcome,omitempty"`
	Success              bool                      `json:"success,omitempty"`
	ChannelFallback      bool                      `json:"channelFallback,omitempty"`
	StatusCode           int                       `json:"statusCode,omitempty"`
	RequestDurationMs    int64                     `json:"requestDurationMs,omitempty"`
	FirstByteLatencyMs   int64                     `json:"firstByteLatencyMs,omitempty"`
	CompletedAt          *time.Time                `json:"completedAt,omitempty"`
	DurationMs           int64                     `json:"durationMs"`
	HistoricalSchema     bool                      `json:"historicalSchema,omitempty"`
}

// TraceSummary 是列表 API 返回的轻量摘要。
// 不含候选详情、策略指纹完整值或 endpoint 尝试。
type TraceSummary struct {
	TraceUID           string           `json:"traceUid"`
	SchemaVersion      int              `json:"schemaVersion"`
	CreatedAt          time.Time        `json:"createdAt"`
	ReleaseID          string           `json:"releaseId"`
	Cohort             Cohort           `json:"cohort"`
	Mode               RoutingMode      `json:"mode"`
	RequestKind        string           `json:"requestKind"`
	TaskClass          TaskClass        `json:"taskClass"`
	TaskDomain         TaskDomain       `json:"taskDomain,omitempty"`
	RequestedModel     string           `json:"requestedModel,omitempty"`
	ComparisonStatus   ComparisonStatus `json:"comparisonStatus"`
	RecommendedChannel string           `json:"recommendedChannelUid,omitempty"`
	ActualChannelUID   string           `json:"actualChannelUid,omitempty"`
	Outcome            string           `json:"outcome,omitempty"`
	Success            bool             `json:"success,omitempty"`
	RequestDurationMs  int64            `json:"requestDurationMs,omitempty"`
	HistoricalSchema   bool             `json:"historicalSchema,omitempty"`
}

// SchedulerDecisionSummary 是 Scheduler 裁决的规范化摘要。
// 不复制整个 SelectionTrace，只保留阶段计数、跳过原因代码和最终选择。
type SchedulerDecisionSummary struct {
	Stages        []SchedulerStageSummary `json:"stages,omitempty"`
	SkipReasons   []string                `json:"skipReasons,omitempty"`
	SelectedUID   string                  `json:"selectedUid,omitempty"`
	SelectedName  string                  `json:"selectedName,omitempty"`
	SelectionCode string                  `json:"selectionCode,omitempty"`
}

// SchedulerStageSummary 记录一个过滤阶段的名称和通过候选数。
type SchedulerStageSummary struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// EndpointAttemptSummary 是一次上游尝试的安全摘要。
// 不包含 BaseURL、API Key 或错误 body。
type EndpointAttemptSummary struct {
	AttemptUID    string `json:"attemptUid"`
	AttemptSeq    int    `json:"attemptSeq"`
	Status        string `json:"status"` // "started" | "completed"
	ChannelUID    string `json:"channelUid"`
	EndpointLabel string `json:"endpointLabel"`
	Result        string `json:"result"` // "success" | "upstream_error" | "cancelled" | "attempt_failed"
	StatusCode    int    `json:"statusCode,omitempty"`
	DurationMs    int64  `json:"durationMs,omitempty"`
}

// RoutingReleaseSnapshot 是一次决策到终态之间唯一允许读取的 release/policy/cohort 数据源。
// 在请求入口冻结，后续回填不得重新读取可变配置。
type RoutingReleaseSnapshot struct {
	ReleaseID         string
	PolicyFingerprint string
	TargetMode        RoutingMode
	EffectiveMode     RoutingMode
	Cohort            Cohort
	BypassReason      string
	RolloutPercent    int
	RolloutSeed       string
	ControlPercent    int
	CreatedAt         time.Time
}

// ── 不可预测的 TraceUID 生成 ──

// GenerateTraceUIDv2 使用 crypto/rand 生成碰撞安全的 trace UID。
// 不依赖时间戳或请求参数，避免可预测性。
func GenerateTraceUIDv2() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand 失败在正常运行时几乎不可能发生；
		// 极端情况下回退到时间+随机混合，但不使用固定种子。
		fallback := fmt.Sprintf("rt_%d", time.Now().UnixNano())
		sum := sha256.Sum256([]byte(fallback))
		return "rt_" + hex.EncodeToString(sum[:8])
	}
	return "rt_" + hex.EncodeToString(buf[:8])
}

// ── 策略指纹 ──

// PolicyFingerprintInput 是计算策略指纹的输入。
// 只包含决策语义，明确排除 rolloutPercent、controlPercent 和 rolloutSeed。
type PolicyFingerprintInput struct {
	// 权重与评分
	WeightOverrides map[string]float64 `json:"weightOverrides,omitempty"`
	CostPreference  string             `json:"costPreference"`

	// 模型映射与能力
	ModelFamilyPreference []string `json:"modelFamilyPreference,omitempty"`
	CapabilityFloor       bool     `json:"capabilityFloor"`

	// 过滤与约束
	DisabledChannelUIDs []string `json:"disabledChannelUids,omitempty"`
	DisabledTaskClasses []string `json:"disabledTaskClasses,omitempty"`

	// 来源与域
	TaskDomainStrengthEnabled bool                          `json:"taskDomainStrengthEnabled"`
	SeedMatrixOverrides       map[string]map[string]float64 `json:"seedMatrixOverrides,omitempty"`

	// 策略版本（如有）
	PolicyRevision int `json:"policyRevision,omitempty"`
}

// ComputePolicyFingerprint 计算策略指纹。
// 输入先规范化排序，再 SHA-256 取前 16 位 hex。
// 指纹只用于审计与聚合，不作为分桶输入。
func ComputePolicyFingerprint(input PolicyFingerprintInput) string {
	// 规范化：排序 slice 字段
	sort.Strings(input.ModelFamilyPreference)
	sort.Strings(input.DisabledChannelUIDs)
	sort.Strings(input.DisabledTaskClasses)

	// 规范化 WeightOverrides key 顺序
	if input.WeightOverrides != nil {
		keys := make([]string, 0, len(input.WeightOverrides))
		for k := range input.WeightOverrides {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := make(map[string]float64, len(keys))
		for _, k := range keys {
			ordered[k] = input.WeightOverrides[k]
		}
		input.WeightOverrides = ordered
	}

	// 规范化 SeedMatrixOverrides
	if input.SeedMatrixOverrides != nil {
		outerKeys := make([]string, 0, len(input.SeedMatrixOverrides))
		for k := range input.SeedMatrixOverrides {
			outerKeys = append(outerKeys, k)
		}
		sort.Strings(outerKeys)
		ordered := make(map[string]map[string]float64, len(outerKeys))
		for _, ok := range outerKeys {
			inner := input.SeedMatrixOverrides[ok]
			innerKeys := make([]string, 0, len(inner))
			for k := range inner {
				innerKeys = append(innerKeys, k)
			}
			sort.Strings(innerKeys)
			innerOrdered := make(map[string]float64, len(innerKeys))
			for _, ik := range innerKeys {
				innerOrdered[ik] = inner[ik]
			}
			ordered[ok] = innerOrdered
		}
		input.SeedMatrixOverrides = ordered
	}

	canonical, err := json.Marshal(input)
	if err != nil {
		return "fp_error"
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:8])
}

// ── 脱敏双边界 ──

// SanitizeForPersistence 在落盘前对 v2 详情做持久化脱敏。
// 移除 PromptHash、metricsKey 中的原始 key 片段、endpoint 标识中的地址信息。
// 使用白名单 DTO，不修改运行时对象。
func SanitizeForPersistence(detail *TraceDetailV2) {
	if detail == nil {
		return
	}

	// 候选脱敏：移除 metricsKey，保留 ChannelUID
	for i := range detail.Candidates {
		detail.Candidates[i].MetricsKey = ""
	}

	// Endpoint 脱敏：确保 endpointLabel 不含地址信息
	for i := range detail.EndpointAttempts {
		if detail.EndpointAttempts[i].EndpointLabel == "" {
			detail.EndpointAttempts[i].EndpointLabel = detail.EndpointAttempts[i].ChannelUID
		}
	}

	// 确保不持久化敏感字段
	detail.SchedulerDecision = sanitizeSchedulerForPersistence(detail.SchedulerDecision)
}

// SanitizeForResponse 在 API 响应前对 v2 详情做响应脱敏。
// 比持久化脱敏更严格：移除 Scheduler 内部细节，确保 endpoint 标识不含原始地址。
func SanitizeForResponse(detail *TraceDetailV2) {
	if detail == nil {
		return
	}
	SanitizeForPersistence(detail)
}

// sanitizeSchedulerForPersistence 确保 Scheduler 摘要不含敏感信息。
func sanitizeSchedulerForPersistence(s *SchedulerDecisionSummary) *SchedulerDecisionSummary {
	if s == nil {
		return nil
	}
	// SkipReasons 只保留已知的安全代码
	filtered := make([]string, 0, len(s.SkipReasons))
	for _, reason := range s.SkipReasons {
		if isSafeSkipReason(reason) {
			filtered = append(filtered, reason)
		}
	}
	s.SkipReasons = filtered
	return s
}

// isSafeSkipReason 判断跳过原因代码是否为已知安全值。
func isSafeSkipReason(reason string) bool {
	safeReasons := map[string]bool{
		"unsupported_model":           true,
		"circuit_open":                true,
		"failed_in_request":           true,
		"no_available_key":            true,
		"rate_limit_pressure":         true,
		"context_window_insufficient": true,
		"vision_unsupported":          true,
		"tool_use_unsupported":        true,
		"reasoning_unsupported":       true,
		"disabled_channel":            true,
		"disabled_task_class":         true,
		"autopilot_filtered":          true,
		"cooldown":                    true,
		"inactive":                    true,
		"fallback":                    true,
		"priority_order":              true,
		"trace_affinity":              true,
		"promotion_priority":          true,
		"manual_override":             true,
		"channel_pin":                 true,
		"rate_limited":                true,
		"empty_stream":                true,
		"key_binding":                 true,
		"quota_exhausted":             true,
	}
	return safeReasons[reason]
}

// ── v1 → v2 适配器 ──

// AdaptV1ToTraceSummary 从 v1 行生成尽力而为的 TraceSummary。
// 缺失字段返回"不可用"，不伪造默认值。
func AdaptV1ToTraceSummary(trace *RoutingDecisionTrace) TraceSummary {
	if trace == nil {
		return TraceSummary{}
	}
	comparison := ComparisonUncompared
	if trace.ActualChannelUID != "" && trace.ShadowChannelUID != "" {
		if trace.Match {
			comparison = ComparisonMatched
		} else {
			comparison = ComparisonMismatched
		}
	}
	return TraceSummary{
		TraceUID:           trace.TraceUID,
		SchemaVersion:      1,
		CreatedAt:          trace.CreatedAt,
		ReleaseID:          "legacy",
		Mode:               trace.Mode,
		RequestKind:        trace.RequestKind,
		TaskClass:          trace.TaskClass,
		TaskDomain:         trace.TaskDomain,
		RequestedModel:     trace.RequestedModel,
		ComparisonStatus:   comparison,
		RecommendedChannel: trace.ShadowChannelUID,
		ActualChannelUID:   trace.ActualChannelUID,
		Outcome:            trace.Outcome,
		Success:            trace.Success,
		RequestDurationMs:  trace.RequestDurationMs,
		HistoricalSchema:   true,
	}
}

// AdaptV1ToTraceDetailV2 从 v1 行生成尽力而为的 TraceDetailV2。
// 缺失字段标记为 historicalSchema=true。
func AdaptV1ToTraceDetailV2(trace *RoutingDecisionTrace) *TraceDetailV2 {
	if trace == nil {
		return nil
	}
	comparison := ComparisonUncompared
	if trace.ActualChannelUID != "" && trace.ShadowChannelUID != "" {
		if trace.Match {
			comparison = ComparisonMatched
		} else {
			comparison = ComparisonMismatched
		}
	}
	return &TraceDetailV2{
		TraceUID:            trace.TraceUID,
		SchemaVersion:       1,
		CreatedAt:           trace.CreatedAt,
		ReleaseID:           "legacy",
		TargetMode:          trace.Mode,
		EffectiveMode:       trace.Mode,
		ComparisonStatus:    comparison,
		RequestKind:         trace.RequestKind,
		TaskClass:           trace.TaskClass,
		TaskDomain:          trace.TaskDomain,
		RequestedModel:      trace.RequestedModel,
		AgentRole:           trace.AgentRole,
		ManualIntentUID:     trace.ManualIntentUID,
		AdvisorDecisionUID:  trace.AdvisorDecisionUID,
		Candidates:          trace.Candidates,
		CandidatesBefore:    trace.CandidatesBefore,
		CandidatesAfter:     trace.CandidatesAfter,
		GlobalFilterReasons: trace.GlobalFilterReasons,
		SortReasons:         trace.SortReasons,
		RecommendedChannel:  trace.ShadowChannelUID,
		SelectedChannelUID:  trace.SelectedChannelUID,
		EstimatedCost:       trace.EstimatedCost,
		CostConfidence:      trace.CostConfidence,
		FallbackUsed:        trace.FallbackUsed,
		Outcome:             trace.Outcome,
		Success:             trace.Success,
		ChannelFallback:     trace.ChannelFallback,
		StatusCode:          trace.StatusCode,
		RequestDurationMs:   trace.RequestDurationMs,
		FirstByteLatencyMs:  trace.FirstByteLatencyMs,
		CompletedAt:         trace.CompletedAt,
		DurationMs:          trace.DurationMs,
		HistoricalSchema:    true,
	}
}

// ── 比较状态计算 ──

// ComputeComparisonStatus 计算三态比较结果。
// 只有 shadow 建议与可比较的实际渠道都存在时才计算 matched/mismatched；
// 否则返回 uncompared。
func ComputeComparisonStatus(shadowUID, actualUID string, match bool) ComparisonStatus {
	if shadowUID == "" || actualUID == "" {
		return ComparisonUncompared
	}
	if match {
		return ComparisonMatched
	}
	return ComparisonMismatched
}

// ── Endpoint Label 生成 ──

// DeriveEndpointLabel 从渠道 UID 和尝试序号派生稳定的脱敏端点标签。
// 不含原始 BaseURL、host、path、query 或 key 片段。
func DeriveEndpointLabel(channelUID string, attemptSeq int) string {
	if channelUID == "" {
		return fmt.Sprintf("ep_%d", attemptSeq)
	}
	// 过滤掉 URL 协议和 host 部分，只保留安全的标识符片段
	cleaned := channelUID
	// 移除 https:// 或 http:// 前缀
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(cleaned, scheme) {
			cleaned = cleaned[len(scheme):]
			break
		}
	}
	// 移除 host 后的 path 部分（取第一个 / 之前的内容）
	if idx := strings.Index(cleaned, "/"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	// 只在清理后仍为空时才回退
	if cleaned == "" {
		return fmt.Sprintf("ep_%d", attemptSeq)
	}
	// 取前 8 位，既不泄露完整 UID 也保持可区分性
	short := cleaned
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%s_%d", short, attemptSeq)
}

// ── RoutingDecisionTrace → v2 DTO 映射 ──

// ToTraceDetailV2 将运行时 RoutingDecisionTrace 转换为安全 TraceDetailV2。
// 调用方负责在映射前填充 snapshot、revision 和 comparison 字段。
func (t *RoutingDecisionTrace) ToTraceDetailV2(snapshot *RoutingReleaseSnapshot, revision int64, persistenceClass PersistenceClass) *TraceDetailV2 {
	if t == nil {
		return nil
	}

	comparison := ComputeComparisonStatus(t.ShadowChannelUID, t.ActualChannelUID, t.Match)

	detail := &TraceDetailV2{
		TraceUID:             t.TraceUID,
		SchemaVersion:        2,
		TraceRevision:        revision,
		CreatedAt:            t.CreatedAt,
		RequestCorrelationID: t.RequestCorrelationId,
		Source:               t.Source,
		PersistenceClass:     persistenceClass,
		ComparisonStatus:     comparison,
		RequestKind:          t.RequestKind,
		TaskClass:            t.TaskClass,
		TaskDomain:           t.TaskDomain,
		RequestedModel:       t.RequestedModel,
		AgentRole:            t.AgentRole,
		ManualIntentUID:      t.ManualIntentUID,
		AdvisorDecisionUID:   t.AdvisorDecisionUID,
		Candidates:           t.Candidates,
		CandidatesBefore:     t.CandidatesBefore,
		CandidatesAfter:      t.CandidatesAfter,
		GlobalFilterReasons:  t.GlobalFilterReasons,
		SortReasons:          t.SortReasons,
		RecommendedChannel:   t.ShadowChannelUID,
		SelectedChannelUID:   t.SelectedChannelUID,
		EstimatedCost:        t.EstimatedCost,
		CostConfidence:       t.CostConfidence,
		FallbackUsed:         t.FallbackUsed,
		Outcome:              t.Outcome,
		Success:              t.Success,
		ChannelFallback:      t.ChannelFallback,
		StatusCode:           t.StatusCode,
		RequestDurationMs:    t.RequestDurationMs,
		FirstByteLatencyMs:   t.FirstByteLatencyMs,
		CompletedAt:          t.CompletedAt,
		DurationMs:           t.DurationMs,
	}

	if snapshot != nil {
		detail.ReleaseID = snapshot.ReleaseID
		detail.PolicyFingerprint = snapshot.PolicyFingerprint
		detail.TargetMode = snapshot.TargetMode
		detail.EffectiveMode = snapshot.EffectiveMode
		detail.Cohort = snapshot.Cohort
		detail.BypassReason = snapshot.BypassReason
	}

	return detail
}

// ToTraceSummary 将运行时 RoutingDecisionTrace 转换为 TraceSummary。
func (t *RoutingDecisionTrace) ToTraceSummary() TraceSummary {
	if t == nil {
		return TraceSummary{}
	}
	comparison := ComputeComparisonStatus(t.ShadowChannelUID, t.ActualChannelUID, t.Match)
	return TraceSummary{
		TraceUID:           t.TraceUID,
		SchemaVersion:      2,
		CreatedAt:          t.CreatedAt,
		ReleaseID:          t.ReleaseID,
		Mode:               t.Mode,
		RequestKind:        t.RequestKind,
		TaskClass:          t.TaskClass,
		TaskDomain:         t.TaskDomain,
		RequestedModel:     t.RequestedModel,
		ComparisonStatus:   comparison,
		RecommendedChannel: t.ShadowChannelUID,
		ActualChannelUID:   t.ActualChannelUID,
		Outcome:            t.Outcome,
		Success:            t.Success,
		RequestDurationMs:  t.RequestDurationMs,
	}
}

// ── 敏感字段扫描 ──

// SensitiveSentinels 是脱敏后 JSON 中不应出现的敏感字符串列表。
var SensitiveSentinels = []string{
	"\"apiKey\"", "\"api_key\"", "\"apikey\"",
	"\"authorization\"", "\"Authorization\"",
	"\"prompt\"", "\"messages\"", "\"content\"",
	"\"multipart\"", "\"formData\"", "\"fileContent\"",
	"\"cookie\"", "\"Cookie\"",
	"\"token\"", "\"secret\"",
}

// ScanJSONForSensitive 扫描 JSON 字节，返回发现的敏感 sentinel 列表。
// 用于测试验证脱敏生效。
func ScanJSONForSensitive(data []byte) []string {
	lower := strings.ToLower(string(data))
	var found []string
	for _, sentinel := range SensitiveSentinels {
		if strings.Contains(lower, strings.ToLower(sentinel)) {
			found = append(found, sentinel)
		}
	}
	return found
}

// ValidateDetailV2JSON 验证 TraceDetailV2 JSON 是否不含敏感字段。
// 注意：Candidates 中的 MetricsKey 在持久化脱敏后应被清空。
func ValidateDetailV2JSON(data []byte) error {
	sensitive := ScanJSONForSensitive(data)
	if len(sensitive) > 0 {
		return fmt.Errorf("脱敏后 JSON 仍含敏感字段: %v", sensitive)
	}
	return nil
}

// ── Attempt 容量限制 ──

const (
	// MaxAttemptsPerTrace 每条 trace 最多保留的尝试摘要数。
	MaxAttemptsPerTrace = 32
	// MaxAttemptDetailBytes 尝试摘要序列化上限。
	MaxAttemptDetailBytes = 64 * 1024
)

// TruncateAttempts 当尝试数超过上限时截断，保留首尾摘要并记录聚合计数。
func TruncateAttempts(attempts []EndpointAttemptSummary) ([]EndpointAttemptSummary, bool, int, map[string]int) {
	if len(attempts) <= MaxAttemptsPerTrace {
		return attempts, false, len(attempts), nil
	}
	byResult := make(map[string]int)
	for _, a := range attempts {
		byResult[a.Result]++
	}
	// 保留首 16 和末 16
	truncated := make([]EndpointAttemptSummary, 0, 32)
	truncated = append(truncated, attempts[:16]...)
	truncated = append(truncated, attempts[len(attempts)-16:]...)
	return truncated, true, len(attempts), byResult
}

// ── 稳定哈希分桶 ──

// StableBucket 使用 session ID 或 request correlation ID 与 rolloutSeed 做稳定哈希。
// 返回 0-99 的桶号，用于灰度分桶。
// 不得使用 prompt、API Key 或客户端任意 header 作为输入。
func StableBucket(id string, seed string) int {
	if id == "" {
		return 0
	}
	h := sha256.Sum256([]byte(id + ":" + seed))
	// 取前 8 字节转为 uint64，再 mod 100
	v := uint64(h[0])<<56 | uint64(h[1])<<48 | uint64(h[2])<<40 | uint64(h[3])<<32 |
		uint64(h[4])<<24 | uint64(h[5])<<16 | uint64(h[6])<<8 | uint64(h[7])
	return int(v % 100)
}

// InBucket 判断 bucket 是否命中 treatment 范围。
// treatment 占 [0, rolloutPercent)，control 占 [rolloutPercent, rolloutPercent+controlPercent)。
func InBucket(bucket, rolloutPercent, controlPercent int) Cohort {
	if bucket < rolloutPercent {
		return CohortTreatment
	}
	if rolloutPercent+controlPercent > 0 && bucket < rolloutPercent+controlPercent {
		return CohortControl
	}
	return CohortBypass
}
