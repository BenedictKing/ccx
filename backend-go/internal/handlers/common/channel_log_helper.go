// Package common provides shared handler helpers.
package common

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
)

func GenerateRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b)
}

func CreatePendingLog(
	channelLogStore *metrics.ChannelLogStore,
	channelIndex int,
	model, originalModel string,
	apiKey, baseURL, interfaceType string,
	requestSource string,
) string {
	if channelLogStore == nil {
		return ""
	}
	if requestSource == "" {
		requestSource = metrics.RequestSourceProxy
	}

	requestID := GenerateRequestID()
	now := time.Now()
	channelLogStore.Record(channelIndex, &metrics.ChannelLog{
		RequestID:     requestID,
		ChannelIndex:  channelIndex,
		Timestamp:     now,
		StartTime:     now,
		Model:         model,
		OriginalModel: originalModel,
		StatusCode:    0,
		DurationMs:    0,
		Success:       false,
		KeyMask:       utils.MaskAPIKey(apiKey),
		BaseURL:       baseURL,
		ErrorInfo:     "",
		IsRetry:       false,
		InterfaceType: interfaceType,
		RequestSource: requestSource,
		Status:        metrics.StatusPending,
	})
	return requestID
}

func UpdateLogStatus(
	channelLogStore *metrics.ChannelLogStore,
	channelIndex int,
	requestID string,
	status string,
) {
	if channelLogStore == nil || requestID == "" {
		return
	}

	now := time.Now()
	channelLogStore.Update(channelIndex, requestID, func(log *metrics.ChannelLog) {
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

func CompleteLog(
	channelLogStore *metrics.ChannelLogStore,
	channelIndex int,
	requestID string,
	statusCode int,
	success bool,
	errorInfo string,
	isRetry bool,
) {
	if channelLogStore == nil || requestID == "" {
		return
	}
	if len(errorInfo) > 200 {
		errorInfo = errorInfo[:200]
	}

	status := getStatusFromResult(success, errorInfo)
	now := time.Now()
	updateStatus, actualChannelIndex := channelLogStore.Update(channelIndex, requestID, func(log *metrics.ChannelLog) {
		log.StatusCode = statusCode
		log.Success = success
		log.ErrorInfo = errorInfo
		log.IsRetry = isRetry
		log.CompletedAt = &now
		if !log.StartTime.IsZero() {
			log.DurationMs = now.Sub(log.StartTime).Milliseconds()
		}
		log.Status = status
	})

	if updateStatus == metrics.UpdateMissingEvicted && actualChannelIndex >= 0 {
		channelLogStore.Record(actualChannelIndex, &metrics.ChannelLog{
			RequestID:    requestID,
			ChannelIndex: actualChannelIndex,
			Timestamp:    now,
			StatusCode:   statusCode,
			Success:      success,
			ErrorInfo:    errorInfo,
			IsRetry:      isRetry,
			Status:       status,
			StartTime:    now,
			CompletedAt:  &now,
			DurationMs:   0,
		})
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

// RecordChannelLog keeps compatibility for older call sites.
func RecordChannelLog(
	channelLogStore *metrics.ChannelLogStore,
	channelIndex int,
	model, originalModel string,
	statusCode int,
	durationMs int64,
	success bool,
	apiKey, baseURL, errorInfo, interfaceType string,
	isRetry bool,
) {
	RecordChannelLogWithSource(
		channelLogStore,
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
	)
}

func RecordChannelLogWithSource(
	channelLogStore *metrics.ChannelLogStore,
	channelIndex int,
	model, originalModel string,
	statusCode int,
	durationMs int64,
	success bool,
	apiKey, baseURL, errorInfo, interfaceType string,
	isRetry bool,
	requestSource string,
) {
	if channelLogStore == nil {
		return
	}
	if len(errorInfo) > 200 {
		errorInfo = errorInfo[:200]
	}
	if requestSource == "" {
		requestSource = metrics.RequestSourceProxy
	}

	now := time.Now()
	startTime := now.Add(-time.Duration(durationMs) * time.Millisecond)
	requestID := GenerateRequestID()
	status := getStatusFromResult(success, errorInfo)

	channelLogStore.Record(channelIndex, &metrics.ChannelLog{
		RequestID:     requestID,
		ChannelIndex:  channelIndex,
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
