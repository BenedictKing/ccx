package metrics

import (
	"sort"
	"sync"
	"time"
)

// ChannelLog 单次上游请求日志
type ChannelLog struct {
	RequestID               string    `json:"requestId"` // 请求唯一标识
	ChannelIndex            int       `json:"-"`         // 创建时的渠道索引（不序列化，仅用于内部排查）
	ChannelName             string    `json:"-"`         // 创建时的渠道名称（不序列化，用于共享 metricsKey 日志归属）
	MetricsKey              string    `json:"-"`         // 指标身份键（不序列化，仅用于日志分桶）
	Timestamp               time.Time `json:"timestamp"`
	Model                   string    `json:"model"`                             // 实际使用的模型（重定向后）
	OriginalModel           string    `json:"originalModel,omitempty"`           // 原始请求模型（仅当重定向时有值）
	Operation               string    `json:"operation,omitempty"`               // Images 端点（generations/edits/variations）
	OriginalReasoningEffort string    `json:"originalReasoningEffort,omitempty"` // 原始请求的思考强度
	ActualReasoningEffort   string    `json:"actualReasoningEffort,omitempty"`   // 实际发往上游的思考强度
	EffortDecisionSource    string    `json:"effortDecisionSource,omitempty"`    // autopilot | client | passthrough
	EffortClampedByClient   bool      `json:"effortClampedByClient,omitempty"`   // true if effort clamped by client upper bound
	StatusCode              int       `json:"statusCode"`
	DurationMs              int64     `json:"durationMs"`
	Success                 bool      `json:"success"`
	KeyMask                 string    `json:"keyMask"`
	BaseURL                 string    `json:"baseUrl"`
	ErrorInfo               string    `json:"errorInfo"`
	IsRetry                 bool      `json:"isRetry"`
	InterfaceType           string    `json:"interfaceType"`           // 接口类型（Messages/Responses/Gemini）
	RequestSource           string    `json:"requestSource,omitempty"` // 请求来源（proxy/capability_test）
	SelectionReason         string    `json:"selectionReason,omitempty"`
	SelectionTraceSummary   string    `json:"selectionTraceSummary,omitempty"`

	// 请求生命周期状态
	Status      string     `json:"status"`                // pending/connecting/first_byte/streaming/completed/failed/cancelled
	StartTime   time.Time  `json:"startTime"`             // 请求开始时间
	ConnectedAt *time.Time `json:"connectedAt,omitempty"` // 连接建立时间
	FirstByteAt *time.Time `json:"firstByteAt,omitempty"` // 首字节到达时间
	CompletedAt *time.Time `json:"completedAt,omitempty"` // 请求完成时间

	// 流式超时校准观测值
	FirstContentLatencyMs int64 `json:"firstContentLatencyMs,omitempty"` // HTTP 200 后首个有效内容耗时
	MaxStreamIdleMs       int64 `json:"maxStreamIdleMs,omitempty"`       // 首个有效内容后最大上游空闲间隔
	MaxToolCallIdleMs     int64 `json:"maxToolCallIdleMs,omitempty"`     // 工具调用阶段最大上游空闲间隔

	// 代理上下文观测（subagent 识别）
	AgentRole       string `json:"agentRole,omitempty"`       // main | subagent
	AgentType       string `json:"agentType,omitempty"`       // codex_subagent | claude_code_subagent
	ParentThreadID  string `json:"parentThreadId,omitempty"`  // Codex parent thread id
	AgentConfidence string `json:"agentConfidence,omitempty"` // exact | heuristic
	SessionID       string `json:"sessionId,omitempty"`       // 扁平化会话标识（用于驾驶舱关联）

	// 代理 Key 掩码（用于成本报表按用户维度分组，由 ProxyAuthMiddleware 写入 gin context）
	ProxyKeyMask string `json:"proxyKeyMask,omitempty"`

	// Trace 关联（§3.5）
	RequestCorrelationID string `json:"requestCorrelationId,omitempty"` // 服务端逻辑请求关联 ID
	AutopilotTraceUID    string `json:"autopilotTraceUid,omitempty"`    // Autopilot trace UID
}

const (
	RequestSourceProxy          = "proxy"
	RequestSourceCapabilityTest = "capability_test"
	RequestSourceHealthCheck    = "healthcheck"
	maxChannelLogs              = 50

	// 请求状态常量
	StatusPending    = "pending"
	StatusConnecting = "connecting"
	StatusFirstByte  = "first_byte"
	StatusStreaming  = "streaming"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
)

func isTerminalStatus(status string) bool {
	return status == StatusCompleted || status == StatusFailed || status == StatusCancelled
}

// ChannelLogStore 渠道日志存储（内存环形缓冲区）。
// 分桶键与指标统计保持一致：metricsKey = identity(baseURL, apiKey, serviceType)。
type ChannelLogStore struct {
	mu               sync.RWMutex
	logs             map[string][]*ChannelLog // key: metricsKey
	requestLocations map[string]string        // requestID -> current metricsKey；仅跟踪在途请求
}

func NewChannelLogStore() *ChannelLogStore {
	return &ChannelLogStore{
		logs:             make(map[string][]*ChannelLog),
		requestLocations: make(map[string]string),
	}
}

func (s *ChannelLogStore) Record(metricsKey string, log *ChannelLog) {
	if metricsKey == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if log != nil {
		log.MetricsKey = metricsKey
		if log.RequestID != "" {
			if !isTerminalStatus(log.Status) {
				s.requestLocations[log.RequestID] = metricsKey
			} else {
				delete(s.requestLocations, log.RequestID)
			}
		}
	}
	s.logs[metricsKey] = append(s.logs[metricsKey], log)
	if len(s.logs[metricsKey]) > maxChannelLogs {
		s.logs[metricsKey] = s.logs[metricsKey][len(s.logs[metricsKey])-maxChannelLogs:]
	}
}

// Remove 删除指定 metricsKey 集合对应的日志桶与在途请求位置。
func (s *ChannelLogStore) Remove(metricsKeys []string) {
	if len(metricsKeys) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	removeSet := make(map[string]struct{}, len(metricsKeys))
	for _, metricsKey := range metricsKeys {
		if metricsKey == "" {
			continue
		}
		removeSet[metricsKey] = struct{}{}
		delete(s.logs, metricsKey)
	}
	if len(removeSet) == 0 || len(s.requestLocations) == 0 {
		return
	}
	for requestID, metricsKey := range s.requestLocations {
		if _, shouldRemove := removeSet[metricsKey]; shouldRemove {
			delete(s.requestLocations, requestID)
		}
	}
}

func (s *ChannelLogStore) RenameChannel(oldName, newName string) {
	if oldName == "" || newName == "" || oldName == newName {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entries := range s.logs {
		for _, logEntry := range entries {
			if logEntry != nil && logEntry.ChannelName == oldName {
				logEntry.ChannelName = newName
			}
		}
	}
}

// ClearAll 清除所有渠道日志，仅用于需要整体重置日志缓存的场景。
func (s *ChannelLogStore) ClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = make(map[string][]*ChannelLog)
	s.requestLocations = make(map[string]string)
}

func (s *ChannelLogStore) Get(metricsKey string) []*ChannelLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyLogsNewestFirst(s.logs[metricsKey], maxChannelLogs)
}

func (s *ChannelLogStore) GetMerged(metricsKeys []string) []*ChannelLog {
	return s.GetMergedFiltered(metricsKeys, nil)
}

func (s *ChannelLogStore) GetMergedFiltered(metricsKeys []string, includeLog func(*ChannelLog) bool) []*ChannelLog {
	if len(metricsKeys) == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]struct{}, len(metricsKeys))
	merged := make([]*ChannelLog, 0)
	for _, metricsKey := range metricsKeys {
		if metricsKey == "" {
			continue
		}
		if _, exists := seen[metricsKey]; exists {
			continue
		}
		seen[metricsKey] = struct{}{}
		for _, logEntry := range s.logs[metricsKey] {
			if logEntry == nil {
				continue
			}
			if includeLog != nil && !includeLog(logEntry) {
				continue
			}
			logCopy := *logEntry
			merged = append(merged, &logCopy)
		}
	}
	if len(merged) == 0 {
		return nil
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Timestamp.After(merged[j].Timestamp)
	})
	if len(merged) > maxChannelLogs {
		merged = merged[:maxChannelLogs]
	}
	return merged
}

func copyLogsNewestFirst(src []*ChannelLog, limit int) []*ChannelLog {
	if len(src) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(src) {
		limit = len(src)
	}

	result := make([]*ChannelLog, 0, limit)
	for i := len(src) - 1; i >= 0 && len(result) < limit; i-- {
		if src[i] == nil {
			continue
		}
		logCopy := *src[i]
		result = append(result, &logCopy)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// UpdateStatus 描述 Update 的结果
type UpdateStatus int

const (
	UpdateFound UpdateStatus = iota
	UpdateMissingEvicted
	UpdateMissingDeleted
)

// Update 更新指定请求日志（通过 RequestID 匹配）。
// 优先使用在途请求 metricsKey 定位；若请求已不在途，则区分为淘汰或删除。
// 返回值为 (状态, 当前实际 metricsKey)。
func (s *ChannelLogStore) Update(metricsKey string, requestID string, updateFn func(*ChannelLog)) (UpdateStatus, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if requestID == "" {
		return UpdateMissingDeleted, ""
	}

	actualMetricsKey, tracking := s.requestLocations[requestID]
	if !tracking {
		return UpdateMissingDeleted, ""
	}
	if actualMetricsKey == "" {
		actualMetricsKey = metricsKey
	}

	logs, ok := s.logs[actualMetricsKey]
	if !ok {
		delete(s.requestLocations, requestID)
		return UpdateMissingDeleted, ""
	}

	for i := range logs {
		if logs[i] != nil && logs[i].RequestID == requestID {
			updateFn(logs[i])
			if isTerminalStatus(logs[i].Status) {
				delete(s.requestLocations, requestID)
			}
			return UpdateFound, actualMetricsKey
		}
	}

	// 仍被标记为在途，但已不在缓冲区中，说明是环形缓冲淘汰。
	return UpdateMissingEvicted, actualMetricsKey
}
