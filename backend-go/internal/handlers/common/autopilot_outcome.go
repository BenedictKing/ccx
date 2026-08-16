package common

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

const (
	autopilotFirstByteAtKey  = "ccx.autopilot.first_byte_at"
	autopilotActualModelKey  = "ccx.autopilot.actual_model"
	autopilotActualEffortKey = "ccx.autopilot.actual_effort"
)

var routingOutcomeRecorderHook func(traceUID string, outcome autopilot.RoutingOutcome)

// SetRoutingOutcomeRecorderHook 注入请求终态记录器；nil 时保持原有请求路径。
func SetRoutingOutcomeRecorderHook(hook func(traceUID string, outcome autopilot.RoutingOutcome)) {
	routingOutcomeRecorderHook = hook
}

var attemptRecorderHook func(traceUID string, attempt autopilot.EndpointAttemptSummary)

// SetAttemptRecorderHook 注入 endpoint 尝试摘要记录器；nil 时保持原有请求路径。
func SetAttemptRecorderHook(hook func(traceUID string, attempt autopilot.EndpointAttemptSummary)) {
	attemptRecorderHook = hook
}

var schedulerDecisionHook func(traceUID string, trace *scheduler.SelectionTrace)

// SetSchedulerDecisionHook 注入 scheduler 裁决记录器；nil 时保持原有请求路径。
func SetSchedulerDecisionHook(hook func(traceUID string, trace *scheduler.SelectionTrace)) {
	schedulerDecisionHook = hook
}

// notifySchedulerDecision 将本次选择的调度 trace 附加到对应 autopilot trace。
// traceUID 为空或 hook 未注入时静默跳过（fail-open：不影响请求）。
func notifySchedulerDecision(selection *scheduler.SelectionResult) {
	if schedulerDecisionHook == nil || selection == nil || selection.AutopilotTraceUID == "" || selection.Trace == nil {
		return
	}
	defer func() {
		// 观测失败绝不影响代理请求
		_ = recover()
	}()
	schedulerDecisionHook(selection.AutopilotTraceUID, selection.Trace)
}

// recordEndpointAttempt 向对应 trace 追加一条安全的 endpoint 尝试摘要。
// traceUID 为空或 hook 未注入时静默跳过（fail-open：不影响请求）。
func recordEndpointAttempt(traceUID string, attempt autopilot.EndpointAttemptSummary) {
	if attemptRecorderHook == nil || traceUID == "" {
		return
	}
	defer func() {
		// 观测失败绝不影响代理请求
		_ = recover()
	}()
	attemptRecorderHook(traceUID, attempt)
}

// recordAttemptCompleted 记录一次 endpoint 尝试的终态摘要。
// attemptUID 对应 "started" 时登记的 logRequestID，result 为 success/upstream_error/cancelled/attempt_failed。
func recordAttemptCompleted(c *gin.Context, attemptUID, channelUID string, result string, statusCode int, durationMs int64) {
	if attemptRecorderHook == nil {
		return
	}
	traceUIDVal, _ := c.Get("ccx.autopilot_trace_uid")
	uid, _ := traceUIDVal.(string)
	if uid == "" {
		return
	}
	actualModel, actualEffort := autopilotTraceActualDetails(c)
	consumptionPolicy, _ := c.Get("ccx.autopilot_consumption_policy")
	configuredCostMultiplier, _ := c.Get("ccx.autopilot_configured_cost_multiplier")
	policy, _ := consumptionPolicy.(string)
	multiplier, _ := configuredCostMultiplier.(float64)
	recordEndpointAttempt(uid, autopilot.EndpointAttemptSummary{
		AttemptUID:               attemptUID,
		Status:                   "completed",
		ChannelUID:               channelUID,
		EndpointLabel:            autopilot.DeriveEndpointLabel(channelUID, 0),
		ActualModel:              actualModel,
		ActualEffort:             actualEffort,
		Result:                   result,
		StatusCode:               statusCode,
		DurationMs:               durationMs,
		ConsumptionPolicy:        policy,
		ConfiguredCostMultiplier: multiplier,
	})
}

func resetAutopilotAttemptTelemetry(c *gin.Context) {
	if c != nil {
		c.Set(autopilotFirstByteAtKey, time.Time{})
		c.Set(autopilotActualModelKey, "")
		c.Set(autopilotActualEffortKey, "")
	}
}

func recordAutopilotFirstByte(c *gin.Context, at time.Time) {
	if c != nil {
		c.Set(autopilotFirstByteAtKey, at)
	}
}

func autopilotTraceActualDetails(c *gin.Context) (model, effort string) {
	if c == nil {
		return "", ""
	}
	if value, ok := c.Get(autopilotActualModelKey); ok {
		model, _ = value.(string)
	}
	if value, ok := c.Get(autopilotActualEffortKey); ok {
		effort, _ = value.(string)
	}
	return model, effort
}

func autopilotFirstByteLatency(c *gin.Context, requestStartedAt time.Time) int64 {
	if c == nil {
		return 0
	}
	value, ok := c.Get(autopilotFirstByteAtKey)
	if !ok {
		return 0
	}
	at, ok := value.(time.Time)
	if !ok || at.IsZero() || at.Before(requestStartedAt) {
		return 0
	}
	return at.Sub(requestStartedAt).Milliseconds()
}

func buildRoutingOutcome(
	c *gin.Context,
	selection *scheduler.SelectionResult,
	result MultiChannelAttemptResult,
	terminal bool,
	channelFallback bool,
	requestStartedAt time.Time,
	duration time.Duration,
) autopilot.RoutingOutcome {
	statusCode := 0
	if result.FailoverError != nil {
		statusCode = result.FailoverError.Status
	}
	if terminal && c != nil && c.Writer.Status() > 0 {
		statusCode = c.Writer.Status()
	}

	outcomeName := "attempt_failed"
	success := terminal && result.SuccessKey != ""
	switch {
	case errors.Is(result.LastError, context.Canceled):
		outcomeName = "cancelled"
	case success:
		outcomeName = "success"
	case terminal && !result.Handled:
		outcomeName = "exhausted"
	case terminal:
		outcomeName = "upstream_error"
	}
	if success && statusCode == 0 {
		statusCode = http.StatusOK
	}

	actualModel, actualEffort := autopilotTraceActualDetails(c)
	return autopilot.RoutingOutcome{
		Terminal:           terminal,
		Success:            success,
		ChannelFallback:    terminal && channelFallback,
		StatusCode:         statusCode,
		RequestDurationMs:  duration.Milliseconds(),
		FirstByteLatencyMs: autopilotFirstByteLatency(c, requestStartedAt),
		ActualModel:        actualModel,
		ActualEffort:       actualEffort,
		Outcome:            outcomeName,
		CompletedAt:        time.Now().UTC(),
	}
}

func notifyRoutingOutcome(selection *scheduler.SelectionResult, outcome autopilot.RoutingOutcome) {
	if routingOutcomeRecorderHook == nil || selection == nil || selection.AutopilotTraceUID == "" {
		return
	}
	routingOutcomeRecorderHook(selection.AutopilotTraceUID, outcome)
}
