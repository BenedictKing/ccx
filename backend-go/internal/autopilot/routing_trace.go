package autopilot

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/BenedictKing/ccx/internal/errutil"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// ── RoutingDecisionTrace（P1.4）──

// RoutingMode 追踪记录的运行模式。
type RoutingMode string

const (
	RoutingModeOff    RoutingMode = "off"     // 完全关闭
	RoutingModeShadow RoutingMode = "shadow"  // shadow：只计算+记录，不影响真实调度
	RoutingModeDryRun RoutingMode = "dry_run" // dry-run：计算但不生效，与 shadow 等价
	RoutingModeAssist RoutingMode = "assist"  // assist：重排候选列表（不删除），影响调度顺序
	RoutingModeAuto   RoutingMode = "auto"    // auto：硬约束过滤+重排，fail-open 兜底
	RoutingModeActive RoutingMode = "active"  // active：计算并影响真实调度（Phase 2+）
)

// CandidateScore 候选渠道单项得分明细。
type CandidateScore struct {
	Dimension string  `json:"dimension"` // quality | cost | health | domain | effort | stability
	Score     float64 `json:"score"`     // 该维度得分
	Weight    float64 `json:"weight"`    // 权重系数
}

// RoutingCandidate 路由候选渠道信息（已脱敏）。
type RoutingCandidate struct {
	ChannelUID    string `json:"channelUid"`
	MetricsKey    string `json:"metricsKey,omitempty"` // 已脱敏：不含 key 明文
	OriginTier    string `json:"originTier,omitempty"`
	ChannelKind   string `json:"channelKind,omitempty"`
	HealthState   string `json:"healthState,omitempty"`
	MappedModel   string `json:"mappedModel,omitempty"`
	MappingSource string `json:"mappingSource,omitempty"`
	MappingReason string `json:"mappingReason,omitempty"`

	// 分数明细
	TotalScore float64          `json:"totalScore"`
	Scores     []CandidateScore `json:"scores,omitempty"`
	// DomainEvidence 解释 domain 分来自 endpoint 覆盖、规范基准、家族种子或中性回退。
	DomainEvidence *DomainStrengthEvidence `json:"domainEvidence,omitempty"`

	// 是否通过当前模式的硬约束；shadow 中仅表示模拟结果，不改变真实调度。
	Selected bool `json:"selected"`

	// 被过滤的原因列表（非空表示该候选被过滤掉）
	FilterReasons []string `json:"filterReasons,omitempty"`
}

// RoutingDecisionTrace 结构化路由决策追踪。
// 设计文档 P1.4：所有真实或 dry-run 路由都输出结构化 trace。
// 只记录解释性字段，不记录明文 prompt、密钥、敏感 header 或 multipart。
type RoutingDecisionTrace struct {
	TraceUID string `json:"traceUid"`

	// ── v2 身份与版本（§3.1）──
	SchemaVersion        int    `json:"schemaVersion"`
	TraceRevision        int64  `json:"traceRevision"`
	RequestCorrelationId string `json:"requestCorrelationId,omitempty"` // 服务端逻辑请求关联 ID
	Source               string `json:"source,omitempty"`               // "proxy" | "dry_run"

	// ── v2 策略快照（§3.1）──
	ReleaseID         string      `json:"releaseId,omitempty"`
	PolicyFingerprint string      `json:"policyFingerprint,omitempty"`
	TargetMode        RoutingMode `json:"targetMode,omitempty"`
	EffectiveMode     RoutingMode `json:"effectiveMode,omitempty"`
	Cohort            Cohort      `json:"cohort,omitempty"`
	BypassReason      string      `json:"bypassReason,omitempty"`

	// 请求特征摘要（脱敏）
	RequestKind    string     `json:"requestKind"` // messages | chat | responses | gemini
	TaskClass      TaskClass  `json:"taskClass"`
	TaskDomain     TaskDomain `json:"taskDomain,omitempty"`
	RequestedModel string     `json:"requestedModel,omitempty"`
	AgentRole      string     `json:"agentRole,omitempty"` // main | subagent

	// 关联标识
	ManualIntentUID    string `json:"manualIntentUid,omitempty"`
	AdvisorDecisionUID string `json:"advisorDecisionUid,omitempty"`

	// 候选列表（每项含分数明细与过滤原因）
	Candidates       []RoutingCandidate `json:"candidates"`
	CandidatesBefore int                `json:"candidatesBefore"` // 粗筛前候选数
	CandidatesAfter  int                `json:"candidatesAfter"`  // 粗筛后候选数

	// 过滤与排序说明
	GlobalFilterReasons map[string][]string `json:"globalFilterReasons,omitempty"` // key=过滤阶段, value=原因列表
	SortReasons         []string            `json:"sortReasons,omitempty"`

	// 最终选择
	SelectedChannelUID string  `json:"selectedChannelUid,omitempty"`
	SelectedMetricsKey string  `json:"selectedMetricsKey,omitempty"` // 已脱敏
	SelectedOriginTier string  `json:"selectedOriginTier,omitempty"`
	EstimatedCost      float64 `json:"estimatedCost,omitempty"`
	CostConfidence     float64 `json:"costConfidence,omitempty"`
	FallbackUsed       bool    `json:"fallbackUsed"`

	// Shadow 与实际调度结果对比
	ShadowChannelUID string `json:"shadowChannelUid,omitempty"` // shadow 建议的渠道
	ActualChannelUID string `json:"actualChannelUid,omitempty"` // 实际调度使用的渠道
	Match            bool   `json:"match"`                      // shadow 与实际是否一致

	// 请求终态。详细 trace 仍按原策略抽样落盘；无偏统计写入独立 15 分钟窗口表。
	OutcomeRecorded    bool       `json:"outcomeRecorded,omitempty"`
	Outcome            string     `json:"outcome,omitempty"` // success | upstream_error | exhausted | cancelled | attempt_failed
	Success            bool       `json:"success,omitempty"`
	ChannelFallback    bool       `json:"channelFallback,omitempty"`
	StatusCode         int        `json:"statusCode,omitempty"`
	RequestDurationMs  int64      `json:"requestDurationMs,omitempty"`
	FirstByteLatencyMs int64      `json:"firstByteLatencyMs,omitempty"`
	CompletedAt        *time.Time `json:"completedAt,omitempty"`

	// ── v2 尝试摘要（§3.1）──
	EndpointAttempts  []EndpointAttemptSummary `json:"endpointAttempts,omitempty"`
	AttemptsTruncated bool                     `json:"attemptsTruncated,omitempty"`
	AttemptsTotal     int                      `json:"attemptsTotal,omitempty"`
	AttemptsByResult  map[string]int           `json:"attemptsByResult,omitempty"`

	// ── v2 Scheduler 裁决（§3.1）──
	SchedulerDecision *SchedulerDecisionSummary `json:"schedulerDecision,omitempty"`

	// 脱敏标识
	PromptHash string `json:"promptHash,omitempty"` // prompt SHA256 前 16 位

	// 元信息
	Mode       RoutingMode `json:"mode"`
	DurationMs int64       `json:"durationMs"` // 决策耗时（毫秒）
	CreatedAt  time.Time   `json:"createdAt"`
}

// ── 脱敏输出 ──

// SanitizeMetricsKey 对 metricsKey 中可能包含的 API Key 进行掩码处理。
// metricsKey 格式通常为 "baseURL|apiKey" 或 "baseURL|keyMask"。
// 如果 apiKey 长度 > 8，保留前 4 后 4，中间用 **** 替代；否则全掩码。
func SanitizeMetricsKey(metricsKey string) string {
	if metricsKey == "" {
		return ""
	}

	// 查找分隔符
	for _, sep := range []string{"|", "/"} {
		idx := -1
		for i := len(metricsKey) - 1; i >= 0; i-- {
			if string(metricsKey[i]) == sep {
				idx = i
				break
			}
		}
		if idx > 0 && idx < len(metricsKey)-1 {
			prefix := metricsKey[:idx+1]
			key := metricsKey[idx+1:]
			return prefix + MaskKey(key)
		}
	}

	// 无分隔符，原样返回（可能本身就是 base URL）
	return metricsKey
}

// MaskKey 对 API Key 进行掩码。
// 长度 > 8 时保留前 4 后 4，中间用 **** 替代。
// 长度 <= 8 时全部掩码为 ****。
func MaskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) > 8 {
		return key[:4] + "****" + key[len(key)-4:]
	}
	return "****"
}

// SanitizeTrace 对 RoutingDecisionTrace 做脱敏输出。
// 副作用修改传入指针：
//   - 对所有候选和最终选择的 metricsKey 做掩码
//   - 清除 promptHash（API 响应不返回 hash）
//   - 清除 v2 endpoint 摘要中的敏感信息
//   - 不含消息正文（结构本身不存储）
func SanitizeTrace(t *RoutingDecisionTrace) {
	if t == nil {
		return
	}

	// 脱敏候选
	for i := range t.Candidates {
		t.Candidates[i].MetricsKey = SanitizeMetricsKey(t.Candidates[i].MetricsKey)
	}

	// 脱敏最终选择
	t.SelectedMetricsKey = SanitizeMetricsKey(t.SelectedMetricsKey)

	// 清除 promptHash（API 响应层不返回，防碰撞攻击）
	t.PromptHash = ""

	// 脱敏 v2 endpoint 摘要
	for i := range t.EndpointAttempts {
		t.EndpointAttempts[i].EndpointLabel = SanitizeMetricsKey(t.EndpointAttempts[i].EndpointLabel)
	}

	// 清除 shadow/actual channel UID 中可能的额外信息（只保留标识符）
	// ChannelUID 本身不含敏感信息（是 UUID），不做额外处理
}

// SanitizeTraces 批量脱敏，返回新切片（不修改原始数据）。
func SanitizeTraces(traces []*RoutingDecisionTrace) []*RoutingDecisionTrace {
	result := make([]*RoutingDecisionTrace, len(traces))
	for i, t := range traces {
		cp := *t
		SanitizeTrace(&cp)
		result[i] = &cp
	}
	return result
}

// ── PromptHash 生成 ──

// GeneratePromptHash 生成 prompt 的短哈希（前 16 位 hex）。
// 只用于关联，不用于安全校验。
func GeneratePromptHash(promptSnippet string) string {
	if promptSnippet == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(promptSnippet))
	return hex.EncodeToString(sum[:8])
}

// ── TraceUID 生成 ──

// GenerateTraceUID 生成路由追踪唯一标识。
// 使用 crypto/rand 生成碰撞安全 UID，不依赖时间戳或请求参数。
func GenerateTraceUID(requestKind string, taskClass TaskClass, createdAt time.Time) string {
	return GenerateTraceUIDv2()
}

// ── TraceStore（内存环形 + 可选 SQLite 落盘）──

const (
	// traceMaxRecords 内存环形保留最大记录数。
	traceMaxRecords = 500

	// traceSampleRate SQLite 抽样率：每 N 条落盘 1 条。
	// mismatch 样本（shadow != actual）全部落盘，不受抽样率限制。
	traceSampleRate = 10
)

// TraceStore 管理 RoutingDecisionTrace 的内存环形缓存与 SQLite 可选落盘。
// 内存环形保留最近 500 条，超出时淘汰最旧记录。
// SQLite 落盘策略：1/10 抽样 + 所有 shadow 与实际不一致的样本。
// 异步 writer 在后台批量写入，不阻塞代理请求。
type TraceStore struct {
	db     *sql.DB
	writer *asyncWriter // 有界异步写入后端，db != nil 时创建

	records []*RoutingDecisionTrace // 按时间排序，最新在末尾
	mu      sync.RWMutex

	// 计数器，用于抽样判断
	counter atomic.Int64

	// in-flight 索引：未采样但仍在途的 trace，终态回填时可提升为持久化
	// key=traceUID, value=trace 指针；最大 200 条，超限淘汰最旧
	inflight   map[string]*RoutingDecisionTrace
	inflightMu sync.RWMutex

	// 清理门限：至多每 24 小时执行一次清理
	lastCleanupAt time.Time
}

// NewTraceStoreWithDB 使用外部 *sql.DB 创建 TraceStore。
// db 为 nil 时只使用内存环形缓存，不落盘。
func NewTraceStoreWithDB(db *sql.DB) (*TraceStore, error) {
	if db != nil {
		if err := initTraceSchema(db); err != nil {
			return nil, fmt.Errorf("[TraceStore-Init] 建表失败: %w", err)
		}
		if err := initRoutingSafetySchema(db); err != nil {
			return nil, fmt.Errorf("[TraceStore-Init] 初始化路由安全表失败: %w", err)
		}
	}

	store := &TraceStore{
		db:       db,
		records:  make([]*RoutingDecisionTrace, 0, traceMaxRecords),
		inflight: make(map[string]*RoutingDecisionTrace, 200),
	}

	// 从 SQLite 加载历史（如有）
	if db != nil {
		if err := store.loadRecent(); err != nil {
			log.Printf("[TraceStore-Init] 警告: 加载历史记录失败: %v", err)
		}
		// 启动时执行一次清理
		store.cleanupExpired()
		// 启动异步 writer
		store.writer = newAsyncWriter(&sqlDBAdapter{db: db})
	}

	log.Printf("[TraceStore-Init] 初始化完成，已加载 %d 条追踪记录", len(store.records))
	return store, nil
}

// initTraceSchema 建表迁移。
func initTraceSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS autopilot_routing_traces (
    trace_uid       TEXT PRIMARY KEY,
    schema_version  INTEGER NOT NULL DEFAULT 1,
    trace_revision  INTEGER NOT NULL DEFAULT 0,
    request_correlation_id TEXT NOT NULL DEFAULT '',
    request_kind    TEXT    NOT NULL,
    task_class      TEXT    NOT NULL,
    task_domain     TEXT    NOT NULL DEFAULT '',
    requested_model TEXT    NOT NULL DEFAULT '',
    agent_role      TEXT    NOT NULL DEFAULT '',
    mode            TEXT    NOT NULL DEFAULT 'shadow',
    release_id      TEXT    NOT NULL DEFAULT 'legacy',
    policy_fingerprint TEXT NOT NULL DEFAULT '',
    persistence_class TEXT NOT NULL DEFAULT 'sampled',
    shadow_uid      TEXT    NOT NULL DEFAULT '',
    actual_uid      TEXT    NOT NULL DEFAULT '',
    match           INTEGER NOT NULL DEFAULT 0,
    selected_uid    TEXT    NOT NULL DEFAULT '',
    fallback_used   INTEGER NOT NULL DEFAULT 0,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    prompt_hash     TEXT    NOT NULL DEFAULT '',
    candidates_json TEXT    NOT NULL DEFAULT '[]',
    details_json    TEXT    NOT NULL DEFAULT '{}',
    outcome_recorded INTEGER NOT NULL DEFAULT 0,
    outcome          TEXT    NOT NULL DEFAULT '',
    success          INTEGER NOT NULL DEFAULT 0,
    channel_fallback INTEGER NOT NULL DEFAULT 0,
    status_code      INTEGER NOT NULL DEFAULT 0,
    request_duration_ms INTEGER NOT NULL DEFAULT 0,
    first_byte_latency_ms INTEGER NOT NULL DEFAULT 0,
    completed_at     TEXT    NOT NULL DEFAULT '',
    created_at      TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_routing_traces_created
    ON autopilot_routing_traces(created_at);
CREATE INDEX IF NOT EXISTS idx_routing_traces_match
    ON autopilot_routing_traces(match);
CREATE INDEX IF NOT EXISTS idx_routing_traces_task_class
    ON autopilot_routing_traces(task_class);
CREATE INDEX IF NOT EXISTS idx_routing_traces_correlation
    ON autopilot_routing_traces(request_correlation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_routing_traces_release_policy
    ON autopilot_routing_traces(release_id, policy_fingerprint, created_at);
CREATE INDEX IF NOT EXISTS idx_routing_traces_mode
    ON autopilot_routing_traces(mode, created_at);
CREATE INDEX IF NOT EXISTS idx_routing_traces_persistence
    ON autopilot_routing_traces(persistence_class, created_at);
`
	_, err := db.Exec(schema)
	return err
}

// loadRecent 从 SQLite 加载最近 traceMaxRecords 条到内存。
func (s *TraceStore) loadRecent() error {
	rows, err := s.db.Query(`
		SELECT trace_uid, request_kind, task_class, task_domain,
		       requested_model, agent_role, mode,
		       shadow_uid, actual_uid, match,
		       selected_uid, fallback_used, duration_ms,
		       prompt_hash, candidates_json,
		       outcome_recorded, outcome, success, channel_fallback,
		       status_code, request_duration_ms, first_byte_latency_ms, completed_at,
		       created_at
		FROM autopilot_routing_traces
		ORDER BY created_at DESC
		LIMIT ?`, traceMaxRecords)
	if err != nil {
		return err
	}
	defer errutil.IgnoreDeferred(rows.Close)

	var loaded []*RoutingDecisionTrace
	for rows.Next() {
		trace, err := scanTraceRow(rows)
		if err != nil {
			log.Printf("[TraceStore-LoadRecent] 跳过损坏行: %v", err)
			continue
		}
		loaded = append(loaded, trace)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	// 反转为时间升序（SQL 用 DESC 取最新，内存按 ASC 存储）
	s.mu.Lock()
	for i := len(loaded) - 1; i >= 0; i-- {
		s.records = append(s.records, loaded[i])
	}
	s.mu.Unlock()

	return nil
}

// scanTraceRow 扫描一行并返回 RoutingDecisionTrace。
func scanTraceRow(rows *sql.Rows) (*RoutingDecisionTrace, error) {
	var t RoutingDecisionTrace
	var matchInt, fallbackInt, outcomeRecordedInt, successInt, channelFallbackInt int
	var createdAt, completedAt string
	var candidatesJSON string

	err := rows.Scan(
		&t.TraceUID, &t.RequestKind, &t.TaskClass, &t.TaskDomain,
		&t.RequestedModel, &t.AgentRole, &t.Mode,
		&t.ShadowChannelUID, &t.ActualChannelUID, &matchInt,
		&t.SelectedChannelUID, &fallbackInt, &t.DurationMs,
		&t.PromptHash, &candidatesJSON,
		&outcomeRecordedInt, &t.Outcome, &successInt, &channelFallbackInt,
		&t.StatusCode, &t.RequestDurationMs, &t.FirstByteLatencyMs, &completedAt,
		&createdAt,
	)
	if err != nil {
		return nil, err
	}

	t.Match = matchInt != 0
	t.FallbackUsed = fallbackInt != 0
	t.OutcomeRecorded = outcomeRecordedInt != 0
	t.Success = successInt != 0
	t.ChannelFallback = channelFallbackInt != 0
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	if parsed, err := time.Parse(time.RFC3339, completedAt); err == nil {
		t.CompletedAt = &parsed
	}

	if candidatesJSON != "" && candidatesJSON != "[]" {
		_ = json.Unmarshal([]byte(candidatesJSON), &t.Candidates)
	}

	return &t, nil
}

// ── CRUD ──

// Record 记录一条路由追踪。自动填充 TraceUID / CreatedAt。
// 超出上限时淘汰最旧记录。SQLite 落盘遵循 1/10 抽样 + 全部 mismatch。
// 未落盘的 trace 登记到 in-flight 索引，终态回填时可提升。
func (s *TraceStore) Record(t *RoutingDecisionTrace) {
	if t.TraceUID == "" {
		t.TraceUID = GenerateTraceUID(t.RequestKind, t.TaskClass, time.Now())
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}

	// 写入内存
	s.mu.Lock()
	s.records = append(s.records, t)

	// 淘汰最旧
	if len(s.records) > traceMaxRecords {
		excess := len(s.records) - traceMaxRecords
		s.records = s.records[excess:]
	}
	s.mu.Unlock()

	// SQLite 落盘：1/10 抽样 + 全部 mismatch
	if s.db != nil {
		count := s.counter.Add(1)
		isMismatch := !t.Match && t.ActualChannelUID != "" && t.ShadowChannelUID != ""
		shouldPersist := isMismatch || count%traceSampleRate == 0
		// 必落盘类别：manual/advisor/dry-run/失败/fail-open
		if t.ManualIntentUID != "" || t.AdvisorDecisionUID != "" || t.Source == "dry_run" {
			shouldPersist = true
		}
		if shouldPersist {
			if err := s.persistTrace(t); err != nil {
				log.Printf("[TraceStore-Record] 警告: 落盘失败 uid=%s: %v", t.TraceUID, err)
			}
		} else {
			// 未落盘 → 登记 in-flight 索引，终态回填时可提升
			s.registerInflight(t)
		}
	}
}

// UpdateActualChannel 按 TraceUID 回填 channel 级决策实际尝试的渠道。
// shadow 额外计算推荐与实际是否一致；endpoint trace 没有 SelectedChannelUID，会被忽略。
func (s *TraceStore) UpdateActualChannel(traceUID, actualChannelUID string) error {
	if traceUID == "" || actualChannelUID == "" {
		return nil
	}

	var updated *RoutingDecisionTrace
	s.mu.Lock()
	for i := len(s.records) - 1; i >= 0; i-- {
		trace := s.records[i]
		if trace.TraceUID != traceUID {
			continue
		}
		if trace.SelectedChannelUID == "" {
			break
		}
		trace.ActualChannelUID = actualChannelUID
		if trace.Mode == RoutingModeShadow && trace.ShadowChannelUID != "" {
			trace.Match = trace.ShadowChannelUID == actualChannelUID
		}
		copy := *trace
		updated = &copy
		break
	}
	s.mu.Unlock()

	if updated == nil || s.db == nil {
		return nil
	}
	if updated.Mode == RoutingModeShadow && updated.ShadowChannelUID != "" && !updated.Match {
		return s.persistTrace(updated)
	}

	// 非 mismatch 样本保持 1/10 抽样：仅更新已抽样落盘的记录，不插入新行。
	matchInt := 0
	if updated.Match {
		matchInt = 1
	}
	_, err := s.db.Exec(`
UPDATE autopilot_routing_traces
SET actual_uid = ?, match = ?
WHERE trace_uid = ?
`, updated.ActualChannelUID, matchInt, updated.TraceUID)
	if err != nil {
		return fmt.Errorf("[TraceStore-UpdateActual] 更新失败 uid=%s: %w", updated.TraceUID, err)
	}
	return nil
}

// ListRecent 返回最近 N 条记录（脱敏副本，时间降序）。
func (s *TraceStore) ListRecent(n int) []*RoutingDecisionTrace {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := len(s.records)
	if n <= 0 || n > total {
		n = total
	}

	// 从末尾取最近 N 条，反转为时间降序
	start := total - n
	result := make([]*RoutingDecisionTrace, n)
	for i := 0; i < n; i++ {
		cp := *s.records[start+i]
		result[n-1-i] = &cp
	}

	// 脱敏
	SanitizeTracesInPlace(result)
	return result
}

// ListRecentWithFilter 返回最近 N 条记录，支持 mismatch 过滤。
// mismatch=true 时只返回 shadow 与实际不一致的记录。
func (s *TraceStore) ListRecentWithFilter(n int, mismatchOnly bool) []*RoutingDecisionTrace {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := len(s.records)

	// 从末尾向前遍历，收集符合条件的记录
	var result []*RoutingDecisionTrace
	for i := total - 1; i >= 0 && len(result) < n; i-- {
		t := s.records[i]
		if mismatchOnly && t.Match {
			continue
		}
		if mismatchOnly && (t.ActualChannelUID == "" || t.ShadowChannelUID == "") {
			continue
		}
		cp := *t
		result = append(result, &cp)
	}

	// 脱敏
	SanitizeTracesInPlace(result)
	return result
}

// SanitizeTracesInPlace 就地脱敏（修改指针指向的副本）。
func SanitizeTracesInPlace(traces []*RoutingDecisionTrace) {
	for _, t := range traces {
		SanitizeTrace(t)
	}
}

// ── 统计 ──

// TraceStats 路由追踪统计汇总。
type TraceStats struct {
	TotalCount    int            `json:"totalCount"`
	ComparedCount int            `json:"comparedCount"`
	MismatchCount int            `json:"mismatchCount"`
	MismatchRate  float64        `json:"mismatchRate"`
	TaskClassDist map[string]int `json:"taskClassDist"` // key=TaskClass, value=数量
	ModeDist      map[string]int `json:"modeDist"`      // key=RoutingMode, value=数量
}

// GetStats 计算统计汇总。
func (s *TraceStore) GetStats() TraceStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := TraceStats{
		TotalCount:    len(s.records),
		TaskClassDist: make(map[string]int),
		ModeDist:      make(map[string]int),
	}

	for _, t := range s.records {
		if t.ActualChannelUID != "" && t.ShadowChannelUID != "" {
			stats.ComparedCount++
			if !t.Match {
				stats.MismatchCount++
			}
		}
		stats.TaskClassDist[string(t.TaskClass)]++
		stats.ModeDist[string(t.Mode)]++
	}

	if stats.ComparedCount > 0 {
		stats.MismatchRate = float64(stats.MismatchCount) / float64(stats.ComparedCount)
	}

	return stats
}

// ── 持久化辅助 ──

// persistTrace 将追踪记录写入 SQLite（v2 列）。
func (s *TraceStore) persistTrace(t *RoutingDecisionTrace) error {
	candidatesJSON, err := json.Marshal(t.Candidates)
	if err != nil {
		candidatesJSON = []byte("[]")
	}

	// 构建 v2 details_json
	detail := t.ToTraceDetailV2(nil, t.TraceRevision, PersistenceSampled)
	SanitizeForPersistence(detail)
	detailsJSON, err := json.Marshal(detail)
	if err != nil {
		detailsJSON = []byte("{}")
	}

	matchInt := 0
	if t.Match {
		matchInt = 1
	}
	fallbackInt := 0
	if t.FallbackUsed {
		fallbackInt = 1
	}
	outcomeRecordedInt := 0
	if t.OutcomeRecorded {
		outcomeRecordedInt = 1
	}
	successInt := 0
	if t.Success {
		successInt = 1
	}
	channelFallbackInt := 0
	if t.ChannelFallback {
		channelFallbackInt = 1
	}
	completedAt := ""
	if t.CompletedAt != nil {
		completedAt = t.CompletedAt.UTC().Format(time.RFC3339)
	}

	_, err = s.db.Exec(`
INSERT INTO autopilot_routing_traces
    (trace_uid, schema_version, trace_revision, request_correlation_id,
     request_kind, task_class, task_domain,
     requested_model, agent_role, mode,
     release_id, policy_fingerprint, persistence_class,
     shadow_uid, actual_uid, match,
     selected_uid, fallback_used, duration_ms,
     prompt_hash, candidates_json, details_json,
     outcome_recorded, outcome, success, channel_fallback,
     status_code, request_duration_ms, first_byte_latency_ms, completed_at,
     created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(trace_uid) DO UPDATE SET
    schema_version  = excluded.schema_version,
    trace_revision  = excluded.trace_revision,
    request_correlation_id = excluded.request_correlation_id,
    release_id      = excluded.release_id,
    policy_fingerprint = excluded.policy_fingerprint,
    persistence_class = excluded.persistence_class,
    shadow_uid  = excluded.shadow_uid,
    actual_uid  = excluded.actual_uid,
    match       = excluded.match,
    duration_ms = excluded.duration_ms,
    candidates_json = excluded.candidates_json,
    details_json = excluded.details_json,
    outcome_recorded = excluded.outcome_recorded,
    outcome = excluded.outcome,
    success = excluded.success,
    channel_fallback = excluded.channel_fallback,
    status_code = excluded.status_code,
    request_duration_ms = excluded.request_duration_ms,
    first_byte_latency_ms = excluded.first_byte_latency_ms,
    completed_at = excluded.completed_at
`,
		t.TraceUID,
		t.SchemaVersion,
		t.TraceRevision,
		t.RequestCorrelationId,
		t.RequestKind,
		string(t.TaskClass),
		string(t.TaskDomain),
		t.RequestedModel,
		t.AgentRole,
		string(t.Mode),
		t.ReleaseID,
		t.PolicyFingerprint,
		string(persistenceClassForTrace(t)),
		t.ShadowChannelUID,
		t.ActualChannelUID,
		matchInt,
		t.SelectedChannelUID,
		fallbackInt,
		t.DurationMs,
		t.PromptHash,
		string(candidatesJSON),
		string(detailsJSON),
		outcomeRecordedInt,
		t.Outcome,
		successInt,
		channelFallbackInt,
		t.StatusCode,
		t.RequestDurationMs,
		t.FirstByteLatencyMs,
		completedAt,
		t.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("[TraceStore-Persist] 写入失败 uid=%s: %w", t.TraceUID, err)
	}
	return nil
}

// persistenceClassForTrace 根据 trace 状态确定保留类别。
func persistenceClassForTrace(t *RoutingDecisionTrace) PersistenceClass {
	if t.ManualIntentUID != "" {
		return PersistenceManual
	}
	if t.AdvisorDecisionUID != "" {
		return PersistenceAdvisor
	}
	if t.Source == "dry_run" {
		return PersistenceDryRun
	}
	if !t.Match && t.ActualChannelUID != "" && t.ShadowChannelUID != "" {
		return PersistenceMismatch
	}
	if t.OutcomeRecorded {
		if !t.Success || t.FallbackUsed || t.ChannelFallback {
			if t.Outcome == "exhausted" || t.Outcome == "cancelled" {
				return PersistenceFailure
			}
			return PersistenceFallback
		}
	}
	return PersistenceSampled
}

// ListTraceSummary 返回最近 N 条 TraceSummary，支持过滤。
// 按 (created_at, trace_uid) 排序降序。
// 返回 (summaries, partial, hasMore, err)。
func (s *TraceStore) ListTraceSummary(limit int, mismatchOnly bool, release, cohort, mode string) ([]TraceSummary, bool, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, false, nil
	}

	query := `
SELECT trace_uid, schema_version, request_correlation_id, release_id, policy_fingerprint,
       mode, request_kind, task_class, task_domain, requested_model,
       shadow_uid, actual_uid, match,
       outcome_recorded, outcome, success,
       request_duration_ms, completed_at, created_at,
       details_json
FROM autopilot_routing_traces
WHERE 1=1`
	var args []any

	if mismatchOnly {
		query += ` AND shadow_uid != '' AND actual_uid != '' AND match = 0`
	}
	if release != "" {
		query += ` AND release_id = ?`
		args = append(args, release)
	}
	if cohort != "" {
		query += ` AND persistence_class = ?`
		args = append(args, cohort)
	}
	if mode != "" {
		query += ` AND mode = ?`
		args = append(args, mode)
	}

	query += ` ORDER BY created_at DESC, trace_uid DESC LIMIT ?`
	args = append(args, limit+1) // 多查一条判断 hasMore

	ctx, cancel := context.WithTimeout(context.Background(), traceQueryDeadline)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, false, err
	}
	defer errutil.IgnoreDeferred(rows.Close)

	var summaries []TraceSummary
	partial := false
	for rows.Next() {
		var uid, correlationID, releaseID, policyFp string
		var schemaVer int
		var modeStr, reqKind, taskClassStr, taskDomain, reqModel string
		var shadowUID, actualUID string
		var matchInt int
		var outcomeRecordedInt, successInt int
		var outcome string
		var reqDurationMs int64
		var completedAt, createdAt string
		var detailsJSON string

		err := rows.Scan(
			&uid, &schemaVer, &correlationID, &releaseID, &policyFp,
			&modeStr, &reqKind, &taskClassStr, &taskDomain, &reqModel,
			&shadowUID, &actualUID, &matchInt,
			&outcomeRecordedInt, &outcome, &successInt,
			&reqDurationMs, &completedAt, &createdAt,
			&detailsJSON,
		)
		if err != nil {
			partial = true
			continue
		}

		createdAtTime, _ := time.Parse(time.RFC3339, createdAt)

		summary := TraceSummary{
			TraceUID:           uid,
			SchemaVersion:      schemaVer,
			CreatedAt:          createdAtTime,
			ReleaseID:          releaseID,
			Mode:               RoutingMode(modeStr),
			RequestKind:        reqKind,
			TaskClass:          TaskClass(taskClassStr),
			TaskDomain:         TaskDomain(taskDomain),
			RequestedModel:     reqModel,
			ComparisonStatus:   ComputeComparisonStatus(shadowUID, actualUID, matchInt != 0),
			RecommendedChannel: shadowUID,
			ActualChannelUID:   actualUID,
			Outcome:            outcome,
			Success:            successInt != 0,
			RequestDurationMs:  reqDurationMs,
			HistoricalSchema:   schemaVer < 2,
		}
		summaries = append(summaries, summary)
	}

	if err := rows.Err(); err != nil {
		return nil, partial, false, err
	}

	// 判断 hasMore
	hasMore := false
	if len(summaries) > limit {
		summaries = summaries[:limit]
		hasMore = true
	}

	return summaries, partial, hasMore, nil
}

// GetTraceDetail

// GetTraceDetail 按 UID 获取 trace 的安全详情。
// v1 行通过适配器转换；不存在返回 sql.ErrNoRows。
func (s *TraceStore) GetTraceDetail(traceUID string) (*TraceDetailV2, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("trace store 未初始化")
	}

	// 优先从 SQLite 读取 details_json（权威源，含 v2 字段）
	ctx, cancel := context.WithTimeout(context.Background(), traceQueryDeadline)
	defer cancel()

	var uid, correlationID, releaseID, policyFp string
	var schemaVer int
	var modeStr, reqKind, taskClassStr, taskDomain, reqModel, agentRole string
	var shadowUID, actualUID string
	var matchInt int
	var fallbackUsedInt, durationMs int
	var selectedUID string
	var promptHash, candidatesJSON string
	var outcomeRecordedInt, successInt, channelFallbackInt int
	var outcome string
	var statusCode int
	var reqDurationMs, firstByteMs int64
	var completedAt, createdAt string
	var detailsJSON string
	var persistenceClassStr string
	var traceRevision int64

	err := s.db.QueryRowContext(ctx, `
SELECT trace_uid, schema_version, trace_revision, request_correlation_id,
       release_id, policy_fingerprint, persistence_class,
       request_kind, task_class, task_domain, requested_model, agent_role,
       mode, shadow_uid, actual_uid, match,
       selected_uid, fallback_used, duration_ms,
       prompt_hash, candidates_json, details_json,
       outcome_recorded, outcome, success, channel_fallback,
       status_code, request_duration_ms, first_byte_latency_ms,
       completed_at, created_at
FROM autopilot_routing_traces
WHERE trace_uid = ?`, traceUID).Scan(
		&uid, &schemaVer, &traceRevision, &correlationID,
		&releaseID, &policyFp, &persistenceClassStr,
		&reqKind, &taskClassStr, &taskDomain, &reqModel, &agentRole,
		&modeStr, &shadowUID, &actualUID, &matchInt,
		&selectedUID, &fallbackUsedInt, &durationMs,
		&promptHash, &candidatesJSON, &detailsJSON,
		&outcomeRecordedInt, &outcome, &successInt, &channelFallbackInt,
		&statusCode, &reqDurationMs, &firstByteMs,
		&completedAt, &createdAt,
	)
	if err != nil {
		return nil, err
	}

	createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
	var completedAtTime *time.Time
	if t, err := time.Parse(time.RFC3339, completedAt); err == nil {
		completedAtTime = &t
	}

	// 尝试解析 v2 details_json
	if schemaVer >= 2 && detailsJSON != "" && detailsJSON != "{}" {
		var detail TraceDetailV2
		if err := json.Unmarshal([]byte(detailsJSON), &detail); err == nil {
			// 合并由 RecordOutcome 更新的终态字段（details_json 在 Record 时写入，
			// 终态在 RecordOutcome 时通过 UPDATE 更新顶层列，需要合并到详情）
			if outcomeRecordedInt != 0 {
				detail.Outcome = outcome
				detail.Success = successInt != 0
				detail.ChannelFallback = channelFallbackInt != 0
				detail.StatusCode = statusCode
				detail.RequestDurationMs = reqDurationMs
				detail.FirstByteLatencyMs = firstByteMs
				if completedAtTime != nil {
					detail.CompletedAt = completedAtTime
				}
			}
			SanitizeForResponse(&detail)
			return &detail, nil
		}
		// 解析失败：返回 v1 适配结果
	}

	// v1 行或 details_json 损坏：返回适配结果
	trace := &RoutingDecisionTrace{
		TraceUID:           uid,
		RequestKind:        reqKind,
		TaskClass:          TaskClass(taskClassStr),
		TaskDomain:         TaskDomain(taskDomain),
		RequestedModel:     reqModel,
		AgentRole:          agentRole,
		Mode:               RoutingMode(modeStr),
		ShadowChannelUID:   shadowUID,
		ActualChannelUID:   actualUID,
		Match:              matchInt != 0,
		SelectedChannelUID: selectedUID,
		FallbackUsed:       fallbackUsedInt != 0,
		DurationMs:         int64(durationMs),
		OutcomeRecorded:    outcomeRecordedInt != 0,
		Outcome:            outcome,
		Success:            successInt != 0,
		ChannelFallback:    channelFallbackInt != 0,
		StatusCode:         statusCode,
		RequestDurationMs:  reqDurationMs,
		FirstByteLatencyMs: firstByteMs,
		CompletedAt:        completedAtTime,
		CreatedAt:          createdAtTime,
	}
	detail := AdaptV1ToTraceDetailV2(trace)
	SanitizeForResponse(detail)
	return detail, nil
}

// GetV2Stats 计算 v2 统计汇总，基于内存缓存。
func (s *TraceStore) GetV2Stats() TraceStatsResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := TraceStatsResponse{
		TaskClassDist: make(map[string]int),
		ModeDist:      make(map[string]int),
	}

	for _, t := range s.records {
		stats.TotalCount++
		comparison := ComputeComparisonStatus(t.ShadowChannelUID, t.ActualChannelUID, t.Match)
		switch comparison {
		case ComparisonMatched:
			stats.ComparedCount++
			stats.MatchedCount++
		case ComparisonMismatched:
			stats.ComparedCount++
			stats.MismatchCount++
		case ComparisonUncompared:
			stats.UncomparedCount++
		}
		if t.Success {
			stats.SuccessCount++
		}
		if t.FallbackUsed {
			stats.FailOpenCount++
		}
		stats.TaskClassDist[string(t.TaskClass)]++
		stats.ModeDist[string(t.Mode)]++
	}

	if stats.ComparedCount > 0 {
		stats.MismatchRate = float64(stats.MismatchCount) / float64(stats.ComparedCount)
	}

	return stats
}
func (s *TraceStore) Close() error {
	if s.writer != nil {
		s.writer.close()
	}
	return nil
}

// ── in-flight 索引 ──

// registerInflight 将未采样的 trace 登记到 in-flight 索引。
// 直至请求终结，终态回填时可能提升为异常持久化记录。
// 最大 200 条，超限淘汰最旧。
func (s *TraceStore) registerInflight(t *RoutingDecisionTrace) {
	if s == nil || t == nil || t.TraceUID == "" {
		return
	}
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	s.inflight[t.TraceUID] = t
	// 超限淘汰：最多保留 200 条
	const maxInflight = 200
	if len(s.inflight) > maxInflight {
		// 移除最早的一条（遍历找到 CreatedAt 最早的）
		var oldestUID string
		var oldestTime time.Time
		for uid, trace := range s.inflight {
			if oldestUID == "" || trace.CreatedAt.Before(oldestTime) {
				oldestUID = uid
				oldestTime = trace.CreatedAt
			}
		}
		delete(s.inflight, oldestUID)
	}
}

// removeInflight 从 in-flight 索引移除（请求终结后调用）。
func (s *TraceStore) removeInflight(traceUID string) {
	if s == nil || traceUID == "" {
		return
	}
	s.inflightMu.Lock()
	defer s.inflightMu.Unlock()
	delete(s.inflight, traceUID)
}

// inflightCount 返回当前 in-flight 索引大小。
func (s *TraceStore) inflightCount() int {
	if s == nil {
		return 0
	}
	s.inflightMu.RLock()
	defer s.inflightMu.RUnlock()
	return len(s.inflight)
}

// promoteInflight 从 in-flight 索引中找到未采样 trace 并提升为持久化。
// 用于"先未采样、后变异常"的场景（终态失败/mismatch/fail-open）。
func (s *TraceStore) promoteInflight(traceUID string) *RoutingDecisionTrace {
	if s == nil || traceUID == "" {
		return nil
	}
	s.inflightMu.RLock()
	trace, ok := s.inflight[traceUID]
	s.inflightMu.RUnlock()
	if !ok {
		return nil
	}
	// 提升为持久化
	if s.db != nil {
		if err := s.persistTrace(trace); err != nil {
			log.Printf("[TraceStore-Promote] 提升落盘失败 uid=%s: %v", traceUID, err)
		}
	}
	s.removeInflight(traceUID)
	return trace
}

// ── 清理策略 ──

const (
	// traceRetention 详细 trace 保留期：7 天
	traceRetention = 7 * 24 * time.Hour
	// windowRetention 窗口聚合保留期：30 天
	windowRetention = 30 * 24 * time.Hour
	// cleanupInterval 清理最小间隔：24 小时
	cleanupInterval = 24 * time.Hour
)

// cleanupExpired 清理过期数据。
// 详细 trace 保留 7 天，窗口聚合和 safety event 保留 30 天。
// 至多每 24 小时执行一次；启动时强制执行一次。
// 清理失败只记录告警，不影响代理请求。
func (s *TraceStore) cleanupExpired() {
	if s == nil || s.db == nil {
		return
	}
	now := time.Now().UTC()

	s.mu.Lock()
	if !s.lastCleanupAt.IsZero() && now.Sub(s.lastCleanupAt) < cleanupInterval {
		s.mu.Unlock()
		return
	}
	s.lastCleanupAt = now
	s.mu.Unlock()

	// 清理过期详细 trace（7 天）
	traceCutoff := now.Add(-traceRetention).Format(time.RFC3339)
	if _, err := s.db.Exec(
		`DELETE FROM autopilot_routing_traces WHERE created_at < ?`,
		traceCutoff,
	); err != nil {
		log.Printf("[TraceStore-Cleanup] 清理过期 trace 失败: %v", err)
		return
	}

	// 清理过期窗口聚合（30 天）
	windowCutoff := now.Add(-windowRetention).Format(time.RFC3339)
	if _, err := s.db.Exec(
		`DELETE FROM autopilot_routing_windows WHERE window_start < ?`,
		windowCutoff,
	); err != nil {
		log.Printf("[TraceStore-Cleanup] 清理过期窗口失败: %v", err)
	}

	// 清理过期 safety event（30 天）
	if _, err := s.db.Exec(
		`DELETE FROM autopilot_auto_safety_events WHERE created_at < ?`,
		windowCutoff,
	); err != nil {
		log.Printf("[TraceStore-Cleanup] 清理过期安全事件失败: %v", err)
	}

	// 清理 in-flight 索引中超时的条目（1 小时）
	s.inflightMu.Lock()
	inflightTimeout := now.Add(-time.Hour)
	for uid, trace := range s.inflight {
		if trace.CreatedAt.Before(inflightTimeout) {
			delete(s.inflight, uid)
		}
	}
	s.inflightMu.Unlock()
}

// MaybeCleanup 是公开的清理入口，供外部周期调用。
func (s *TraceStore) MaybeCleanup() {
	s.cleanupExpired()
}
