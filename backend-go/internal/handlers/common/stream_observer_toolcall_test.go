package common

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// SawToolCall 初始为 false，工具调用活动标记后置真（docs/specs/tool-call-capability.md §4.4）。
func TestStreamObserverSawToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	observer := &StreamTimeoutObserver{}
	c.Set(streamTimeoutObservationKey, observer)

	if observer.SawToolCall() {
		t.Fatal("新建 observer 的 SawToolCall 应为 false")
	}

	observer.MarkToolCallActivity(time.Now())
	if !observer.SawToolCall() {
		t.Fatal("工具调用活动标记后 SawToolCall 应为 true")
	}
}

// nil observer 安全返回 false（failover 成功路径在 observer 缺失时不应 panic）。
func TestStreamObserverSawToolCallNilSafe(t *testing.T) {
	var observer *StreamTimeoutObserver
	if observer.SawToolCall() {
		t.Fatal("nil observer 的 SawToolCall 应为 false")
	}
}
