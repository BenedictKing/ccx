// Package common 提供 handlers 模块的公共功能
package common

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
)

// GenerateRequestID 生成唯一的请求标识
func GenerateRequestID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ChannelLogOption 为 pending 渠道日志补充可选观测字段。
type ChannelLogOption func(*metrics.ChannelLog)

// WithEstimatedContextTokens 保留请求上下文估算，供上游未返回 usage 时排查。
func WithEstimatedContextTokens(tokens int) ChannelLogOption {
	return func(log *metrics.ChannelLog) {
		if log != nil && tokens > 0 {
			log.EstimatedContextTokens = tokens
		}
	}
}

// WithChannelSelectionTrace 记录本次渠道选择的可解释性摘要。
func WithSubagentRoutingPolicy(applied bool, model string) ChannelLogOption {
	return func(log *metrics.ChannelLog) {
		log.SubagentPolicyApplied = applied
		log.SubagentPolicyModel = model
	}
}

func WithChannelSelectionTrace(reason, summary string) ChannelLogOption {
	reason = strings.TrimSpace(reason)
	summary = strings.TrimSpace(summary)
	return func(log *metrics.ChannelLog) {
		if log == nil {
			return
		}
		log.SelectionReason = reason
		log.SelectionTraceSummary = summary
	}
}

// CreatePendingLog 创建 pending 状态的日志条目（请求开始时调用）
func CreatePendingLog(
	channelLogStore *metrics.ChannelLogStore,
	metricsKey string,
	channelIndex int,
	channelName string,
	model, originalModel string,
	originalReasoningEffort, actualReasoningEffort string,
	apiKey, baseURL, interfaceType, operation string,
	requestSource string,
	agentCtx *types.AgentContext,
	sessionID string,
	opts ...ChannelLogOption,
) string {
	if channelLogStore == nil || metricsKey == "" {
		return ""
	}
	if requestSource == "" {
		requestSource = metrics.RequestSourceProxy
	}

	requestID := GenerateRequestID()
	now := time.Now()

	log := &metrics.ChannelLog{
		RequestID:               requestID,
		ChannelIndex:            channelIndex, // 记录创建时的渠道索引，便于内部排查
		ChannelName:             channelName,
		Timestamp:               now,
		StartTime:               now,
		Model:                   model,
		OriginalModel:           originalModel,
		Operation:               operation,
		OriginalReasoningEffort: originalReasoningEffort,
		ActualReasoningEffort:   actualReasoningEffort,
		StatusCode:              0,
		DurationMs:              0,
		Success:                 false,
		KeyMask:                 utils.MaskAPIKey(apiKey),
		BaseURL:                 baseURL,
		ErrorInfo:               "",
		IsRetry:                 false,
		InterfaceType:           interfaceType,
		RequestSource:           requestSource,
		Status:                  metrics.StatusPending,
		SessionID:               sessionID,
	}

	if agentCtx != nil {
		log.AgentRole = agentCtx.AgentRole
		log.AgentType = agentCtx.AgentType
		log.ParentThreadID = agentCtx.ParentThreadID
		log.AgentConfidence = agentCtx.Confidence
	}

	for _, opt := range opts {
		if opt != nil {
			opt(log)
		}
	}

	channelLogStore.Record(metricsKey, log)

	return requestID
}

// UpdateLogStatus 更新日志状态（连接建立、首字节、流式传输等）
func UpdateLogStatus(
	channelLogStore *metrics.ChannelLogStore,
	metricsKey string,
	requestID string,
	status string,
) {
	if channelLogStore == nil || metricsKey == "" || requestID == "" {
		return
	}

	now := time.Now()
	channelLogStore.Update(metricsKey, requestID, func(log *metrics.ChannelLog) {
		log.Status = status
		switch status {
		case metrics.StatusConnecting:
			log.ConnectedAt = &now
		case metrics.StatusFirstByte:
			log.FirstByteAt = &now
		case metrics.StatusStreaming:
			if log.FirstByteAt == nil {
				log.FirstByteAt = &now
			}
		}
	})
}

// CompleteLog 完成日志记录（请求结束时调用）
func CompleteLog(
	channelLogStore *metrics.ChannelLogStore,
	metricsKey string,
	requestID string,
	statusCode int,
	success bool,
	errorInfo string,
	isRetry bool,
	usage ...*types.Usage,
) {
	if channelLogStore == nil || metricsKey == "" || requestID == "" {
		return
	}

	errorInfo = normalizeChannelLogErrorInfo(errorInfo)
	if len(errorInfo) > 200 {
		errorInfo = errorInfo[:200]
	}

	status := getStatusFromResult(success, errorInfo)
	now := time.Now()
	var requestUsage *types.Usage
	if len(usage) > 0 {
		requestUsage = usage[0]
	}
	updateStatus, actualMetricsKey := channelLogStore.Update(metricsKey, requestID, func(log *metrics.ChannelLog) {
		log.StatusCode = statusCode
		log.Success = success
		log.ErrorInfo = errorInfo
		log.IsRetry = isRetry
		log.CompletedAt = &now
		log.DurationMs = now.Sub(log.StartTime).Milliseconds()
		log.Status = status
		applyChannelLogUsage(log, requestUsage, now)
	})

	// 仅在确认是环形缓冲淘汰时补写终态日志；若渠道已删除则不补写，避免污染其他渠道。
	if updateStatus == metrics.UpdateMissingEvicted && actualMetricsKey != "" {
		terminalLog := &metrics.ChannelLog{
			RequestID:   requestID,
			Timestamp:   now,
			StatusCode:  statusCode,
			Success:     success,
			ErrorInfo:   errorInfo,
			IsRetry:     isRetry,
			Status:      status,
			StartTime:   now,
			CompletedAt: &now,
			DurationMs:  0,
		}
		applyChannelLogUsage(terminalLog, requestUsage, now)
		channelLogStore.Record(actualMetricsKey, terminalLog)
	}
}

func applyChannelLogUsage(log *metrics.ChannelLog, usage *types.Usage, completedAt time.Time) {
	if log == nil || usage == nil {
		return
	}
	inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens := metrics.ExtractUsageTokens(usage)
	log.InputTokens = int(inputTokens)
	log.OutputTokens = int(outputTokens)
	log.CacheCreationTokens = int(cacheCreationTokens)
	log.CacheReadTokens = int(cacheReadTokens)
	log.ContextTokens = int(inputTokens + cacheCreationTokens + cacheReadTokens)

	if outputTokens <= 0 || log.FirstByteAt == nil {
		return
	}
	generationDuration := completedAt.Sub(*log.FirstByteAt)
	if generationDuration > 0 {
		log.TokensPerSecond = float64(outputTokens) / generationDuration.Seconds()
	}
}

func getStatusFromResult(success bool, errorInfo string) string {
	if success {
		return metrics.StatusCompleted
	}
	if strings.EqualFold(strings.TrimSpace(errorInfo), "client canceled") {
		return metrics.StatusCancelled
	}
	return metrics.StatusFailed
}

// RecordChannelLog 统一记录渠道尝试日志。
// 约束：凡是会进入渠道图表统计的尝试，都应该调用此函数或等价逻辑写入日志，保证图表与日志口径一致。
// 注意：此函数用于兼容旧代码，新代码应使用 CreatePendingLog + UpdateLogStatus + CompleteLog 组合
func RecordChannelLog(
	channelLogStore *metrics.ChannelLogStore,
	metricsKey string,
	channelIndex int,
	model, originalModel string,
	statusCode int,
	durationMs int64,
	success bool,
	apiKey, baseURL, errorInfo, interfaceType string,
	isRetry bool,
	channelName ...string,
) {
	RecordChannelLogWithSource(
		channelLogStore,
		metricsKey,
		channelIndex,
		model,
		originalModel,
		statusCode,
		durationMs,
		success,
		apiKey,
		baseURL,
		errorInfo,
		interfaceType,
		isRetry,
		metrics.RequestSourceProxy,
		channelName...,
	)
}

// RecordChannelLogWithSource 记录带来源标识的渠道尝试日志。
// 注意：此函数用于兼容旧代码，新代码应使用 CreatePendingLog + UpdateLogStatus + CompleteLog 组合
func RecordChannelLogWithSource(
	channelLogStore *metrics.ChannelLogStore,
	metricsKey string,
	channelIndex int,
	model, originalModel string,
	statusCode int,
	durationMs int64,
	success bool,
	apiKey, baseURL, errorInfo, interfaceType string,
	isRetry bool,
	requestSource string,
	channelName ...string,
) {
	if channelLogStore == nil || metricsKey == "" {
		return
	}
	errorInfo = normalizeChannelLogErrorInfo(errorInfo)
	if len(errorInfo) > 200 {
		errorInfo = errorInfo[:200]
	}
	if requestSource == "" {
		requestSource = metrics.RequestSourceProxy
	}

	recordedChannelName := ""
	if len(channelName) > 0 {
		recordedChannelName = channelName[0]
	}
	now := time.Now()
	startTime := now.Add(-time.Duration(durationMs) * time.Millisecond)
	requestID := GenerateRequestID()

	var status string
	if success {
		status = metrics.StatusCompleted
	} else {
		status = metrics.StatusFailed
	}

	channelLogStore.Record(metricsKey, &metrics.ChannelLog{
		RequestID:     requestID,
		ChannelIndex:  channelIndex, // 记录创建时的渠道索引，便于内部排查
		ChannelName:   recordedChannelName,
		Timestamp:     now,
		StartTime:     startTime,
		Model:         model,
		OriginalModel: originalModel,
		StatusCode:    statusCode,
		DurationMs:    durationMs,
		Success:       success,
		KeyMask:       utils.MaskAPIKey(apiKey),
		BaseURL:       baseURL,
		ErrorInfo:     errorInfo,
		IsRetry:       isRetry,
		InterfaceType: interfaceType,
		RequestSource: requestSource,
		Status:        status,
		CompletedAt:   &now,
	})
}

func normalizeChannelLogErrorInfo(errorInfo string) string {
	trimmed := strings.TrimSpace(errorInfo)
	switch {
	case strings.HasPrefix(trimmed, ErrEmptyStreamResponse.Error()):
		diagnostic := strings.TrimSpace(strings.TrimPrefix(trimmed, ErrEmptyStreamResponse.Error()))
		diagnostic = strings.TrimSpace(strings.TrimPrefix(diagnostic, ":"))
		if diagnostic != "" {
			return "空流响应：上游 HTTP 200 返回 SSE 流后结束，但未检测到文本或语义内容（" + diagnostic + "）"
		}
		return "空流响应：上游 HTTP 200 返回 SSE 流后结束，但未检测到文本或语义内容"
	case strings.HasPrefix(trimmed, ErrEmptyNonStreamResponse.Error()):
		return "空响应：上游 HTTP 200 返回非流式响应，但未检测到文本或语义内容"
	case strings.HasPrefix(trimmed, ErrStreamFirstContentTimeout.Error()):
		return "流式首内容超时：上游 HTTP 200 后未在配置窗口内返回有效内容"
	case strings.HasPrefix(trimmed, ErrStreamStalled.Error()):
		return "流式断流：首个有效内容后未在配置窗口内继续返回上游活动"
	default:
		return errorInfo
	}
}
