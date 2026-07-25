package ratelimit

import (
	"net/http"
	"sync/atomic"
	"testing"
)

// TestNotifySignal_NoCallback 验证无回调时安全跳过。
func TestNotifySignal_NoCallback(t *testing.T) {
	// 确保回调为 nil
	original := UpstreamSignalCallback
	UpstreamSignalCallback = nil
	defer func() { UpstreamSignalCallback = original }()

	headers := http.Header{}
	headers.Set("x-ratelimit-limit-requests", "100")

	// 不应 panic
	NotifySignal("ep-001", "mk-001", "messages", false, 100, headers, http.StatusOK, "")
}

// TestNotifySignal_WithCallback 验证回调被正确调用。
func TestNotifySignal_WithCallback(t *testing.T) {
	original := UpstreamSignalCallback
	defer func() { UpstreamSignalCallback = original }()

	var called int32
	var gotEndpointUID, gotMetricsKey, gotServiceType string
	var gotStatusCode int

	UpstreamSignalCallback = func(endpointUID, metricsKey, serviceType string, isStream bool, latencyMs int64, headers http.Header, statusCode int, reason string) {
		atomic.StoreInt32(&called, 1)
		gotEndpointUID = endpointUID
		gotMetricsKey = metricsKey
		gotServiceType = serviceType
		gotStatusCode = statusCode
	}

	headers := http.Header{}
	headers.Set("x-ratelimit-limit-requests", "100")

	NotifySignal("ep-test", "mk-test", "chat", true, 250, headers, http.StatusOK, "")

	if atomic.LoadInt32(&called) != 1 {
		t.Fatal("回调未被调用")
	}
	if gotEndpointUID != "ep-test" {
		t.Errorf("endpointUID = %s, want ep-test", gotEndpointUID)
	}
	if gotMetricsKey != "mk-test" {
		t.Errorf("metricsKey = %s, want mk-test", gotMetricsKey)
	}
	if gotServiceType != "chat" {
		t.Errorf("serviceType = %s, want chat", gotServiceType)
	}
	if gotStatusCode != http.StatusOK {
		t.Errorf("statusCode = %d, want %d", gotStatusCode, http.StatusOK)
	}
}

// TestNotifySignal_NilHeadersSkips 验证 nil headers 安全跳过。
func TestNotifySignal_NilHeadersSkips(t *testing.T) {
	original := UpstreamSignalCallback
	defer func() { UpstreamSignalCallback = original }()

	var called int32
	UpstreamSignalCallback = func(endpointUID, metricsKey, serviceType string, isStream bool, latencyMs int64, headers http.Header, statusCode int, reason string) {
		atomic.StoreInt32(&called, 1)
	}

	NotifySignal("ep", "mk", "messages", false, 100, nil, http.StatusOK, "")

	if atomic.LoadInt32(&called) != 0 {
		t.Error("nil headers 时回调不应被调用")
	}
}

// TestSetUpstreamSignalCallback_NilClears 验证传 nil 清除回调。
func TestSetUpstreamSignalCallback_NilClears(t *testing.T) {
	original := UpstreamSignalCallback
	defer func() { UpstreamSignalCallback = original }()

	SetUpstreamSignalCallback(func(endpointUID, metricsKey, serviceType string, isStream bool, latencyMs int64, headers http.Header, statusCode int, reason string) {
	})

	if UpstreamSignalCallback == nil {
		t.Fatal("回调应已设置")
	}

	SetUpstreamSignalCallback(nil)

	if UpstreamSignalCallback != nil {
		t.Error("回调应被清除")
	}
}
