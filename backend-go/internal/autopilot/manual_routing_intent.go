package autopilot

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// ── 意图类型 ──

// IntentType 人工路由意图的类型。
type IntentType string

const (
	IntentTypeModelTrial    IntentType = "model_trial"    // 模型试用：未知模型进入指定 channel/endpoint 探测
	IntentTypeChannelTrial  IntentType = "channel_trial"  // 渠道试用：调整候选优先级，不永久提升
	IntentTypeEndpointTrial IntentType = "endpoint_trial" // endpoint 试用：精确到 baseURL+key
	IntentTypeSessionPin    IntentType = "session_pin"    // 会话级排障：只影响指定 session
)

// ── 任务类别 ──

// TaskClass 标记请求的任务类别，用于 ManualRoutingIntent 的作用范围过滤。
type TaskClass string

const (
	TaskClassSupervisor  TaskClass = "supervisor"       // 主代理/监工
	TaskClassWorker      TaskClass = "worker"           // 子代理/干活
	TaskClassLightweight TaskClass = "lightweight"      // 轻任务（摘要/标题）
	TaskClassVision      TaskClass = "vision"           // 识图任务
	TaskClassLongContext TaskClass = "long_context"     // 长上下文任务
	TaskClassImageGen    TaskClass = "image_generation" // 原生生图任务
	TaskClassEmbedding   TaskClass = "embedding"        // 原生向量/embedding 任务
)

// AllTaskClasses 返回所有合法 TaskClass 枚举值。
func AllTaskClasses() []TaskClass {
	return []TaskClass{
		TaskClassSupervisor, TaskClassWorker, TaskClassLightweight,
		TaskClassVision, TaskClassLongContext, TaskClassImageGen, TaskClassEmbedding,
	}
}

// ── 意图状态 ──

// IntentStatus 意图生命周期状态。
type IntentStatus string

const (
	IntentStatusActive    IntentStatus = "active"    // 生效中
	IntentStatusExpired   IntentStatus = "expired"   // TTL 已过期
	IntentStatusExhausted IntentStatus = "exhausted" // 预算耗尽（maxRequests / maxEstimatedCost）
	IntentStatusDisabled  IntentStatus = "disabled"  // 手动禁用
)

// ── 试用结果统计 ──

// TrialResult 汇总一次 ManualRoutingIntent 的命中与试用结果。
// Phase 1 shadow：仅记录统计，不影响真实调度。
type TrialResult struct {
	HitCount       int     `json:"hitCount"`                // 总命中次数
	SuccessCount   int     `json:"successCount"`            // 成功次数
	FailureCount   int     `json:"failureCount"`            // 失败次数
	TotalLatencyMs int64   `json:"totalLatencyMs"`          // 总延迟（毫秒）
	AvgLatencyMs   float64 `json:"avgLatencyMs"`            // 平均延迟
	FallbackCount  int     `json:"fallbackCount,omitempty"` // 回退到默认路由的次数
	EstimatedCost  float64 `json:"estimatedCost,omitempty"` // 累计估算成本
}

// ── ManualRoutingIntent ──

// ManualRoutingIntent 是用户显式表达的短期路由意图。
// Phase 1 shadow：仅存储意图与命中统计，绝不影响真实调度。
// 它比 SmartRouter 默认排序优先，但不能绕过协议、鉴权、上下文、vision/tool 等硬约束。
type ManualRoutingIntent struct {
	// 身份
	IntentUID  string     `json:"intentUid"`
	Name       string     `json:"name,omitempty"`
	IntentType IntentType `json:"intentType"`

	// 目标
	ChannelKind string      `json:"channelKind"`           // messages/chat/responses/gemini/images/vectors
	ChannelUID  string      `json:"channelUid,omitempty"`  // 可选：指定渠道
	MetricsKey  string      `json:"metricsKey,omitempty"`  // 可选：精确到 baseURL+key endpoint
	Model       string      `json:"model,omitempty"`       // 请求模型，例如 fable-5
	MappedModel string      `json:"mappedModel,omitempty"` // 可选：上游实际模型
	Effort      EffortLevel `json:"effort,omitempty"`      // 可选：固定思考档位；空=不覆盖，由 Autopilot 决定

	// 作用范围
	AgentRoles     []string    `json:"agentRoles,omitempty"`     // main/subagent；为空表示全部
	TaskClasses    []TaskClass `json:"taskClasses,omitempty"`    // 可选：限制任务类别
	SessionID      string      `json:"sessionId,omitempty"`      // 可选：只影响当前会话
	TrafficPercent int         `json:"trafficPercent,omitempty"` // 1-100；默认 100

	// 安全边界
	ExpiresAt              time.Time `json:"expiresAt"`
	MaxRequests            int       `json:"maxRequests,omitempty"`
	MaxEstimatedCost       float64   `json:"maxEstimatedCost,omitempty"`
	FallbackOnFailure      bool      `json:"fallbackOnFailure"`
	RequireHardConstraints bool      `json:"requireHardConstraints"` // 默认 true

	// 观测
	CreatedBy string       `json:"createdBy,omitempty"`
	CreatedAt time.Time    `json:"createdAt"`
	Reason    string       `json:"reason,omitempty"`
	Status    IntentStatus `json:"status"`

	// 试用结果统计（Phase 1 shadow）
	TrialResult TrialResult `json:"trialResult"`
}

// ── 状态推导 ──

// DeriveStatus 根据 TTL 过期和预算耗尽推导实际状态。
// 如果当前状态已经是 disabled，则不自动改变（disabled 是手动终态）。
// 返回推导后的状态，以及是否发生了变更。
func (m *ManualRoutingIntent) DeriveStatus(now time.Time) (IntentStatus, bool) {
	// disabled 是手动终态，不自动变更
	if m.Status == IntentStatusDisabled {
		return m.Status, false
	}

	// 检查 TTL 过期
	if !m.ExpiresAt.IsZero() && now.After(m.ExpiresAt) {
		if m.Status != IntentStatusExpired {
			return IntentStatusExpired, true
		}
		return m.Status, false
	}

	// 检查请求预算耗尽
	if m.MaxRequests > 0 && m.TrialResult.HitCount >= m.MaxRequests {
		if m.Status != IntentStatusExhausted {
			return IntentStatusExhausted, true
		}
		return m.Status, false
	}

	// 检查成本预算耗尽
	if m.MaxEstimatedCost > 0 && m.TrialResult.EstimatedCost >= m.MaxEstimatedCost {
		if m.Status != IntentStatusExhausted {
			return IntentStatusExhausted, true
		}
		return m.Status, false
	}

	// 未过期且未超预算，维持当前状态
	if m.Status == "" {
		return IntentStatusActive, true
	}
	return m.Status, false
}

// ── IntentUID 生成 ──

// GenerateIntentUID 生成稳定的意图唯一标识。
// 基于 intentType + channelKind + model + channelUID + createdAt 的哈希。
func GenerateIntentUID(intentType IntentType, channelKind, model, channelUID string, createdAt time.Time) string {
	h := sha256.New()
	h.Write([]byte(string(intentType) + "|" + channelKind + "|" + model + "|" + channelUID + "|" + createdAt.Format(time.RFC3339Nano)))
	return "mi_" + hex.EncodeToString(h.Sum(nil))[:16]
}

// ── 合法性校验 ──

// Validate 校验 ManualRoutingIntent 字段合法性。
func (m *ManualRoutingIntent) Validate() error {
	switch m.IntentType {
	case IntentTypeModelTrial, IntentTypeChannelTrial, IntentTypeEndpointTrial, IntentTypeSessionPin:
	default:
		return ErrInvalidIntentType
	}

	if m.ChannelKind == "" {
		return ErrEmptyChannelKind
	}

	if m.ExpiresAt.IsZero() {
		return ErrMissingExpiresAt
	}

	if m.TrafficPercent < 0 || m.TrafficPercent > 100 {
		return ErrInvalidTrafficPercent
	}

	if m.IntentType == IntentTypeSessionPin && m.SessionID == "" {
		return ErrSessionPinRequiresSessionID
	}

	// Effort 非空时必须是 EffortLevel 枚举合法值之一，拒绝未知值而非静默丢弃。
	if m.Effort != "" && !isValidEffortLevel(m.Effort) {
		return ErrInvalidEffort
	}

	return nil
}

// isValidEffortLevel 检查给定值是否是 EffortLevel 枚举中的合法档位。
func isValidEffortLevel(level EffortLevel) bool {
	for _, lv := range AllEffortLevels() {
		if lv == level {
			return true
		}
	}
	return false
}

// ── 错误定义 ──

var (
	ErrInvalidIntentType           = &IntentValidationError{Field: "intentType", Message: "必须是 model_trial/channel_trial/endpoint_trial/session_pin 之一"}
	ErrEmptyChannelKind            = &IntentValidationError{Field: "channelKind", Message: "不能为空"}
	ErrMissingExpiresAt            = &IntentValidationError{Field: "expiresAt", Message: "必须设置过期时间"}
	ErrInvalidTrafficPercent       = &IntentValidationError{Field: "trafficPercent", Message: "必须在 0-100 之间"}
	ErrSessionPinRequiresSessionID = &IntentValidationError{Field: "sessionId", Message: "session_pin 类型必须指定 sessionId"}
	ErrIntentNotFound              = &IntentValidationError{Field: "intentUid", Message: "意图不存在"}
	ErrInvalidEffort               = &IntentValidationError{Field: "effort", Message: "必须是 off/minimal/low/medium/high/max 之一"}
)

// IntentValidationError 意图校验错误。
type IntentValidationError struct {
	Field   string
	Message string
}

func (e *IntentValidationError) Error() string {
	return e.Field + ": " + e.Message
}
