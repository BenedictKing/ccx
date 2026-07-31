package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

func newModelCircuitTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	return c
}

// TestRecordModelCircuitFailureOnHTMLError 复现生产场景：上游网关返回 HTML 403，
// 无法被 keyModelRestrictionReason 的 JSON 解析识别，必须仍能计入模型级熔断。
func TestRecordModelCircuitFailureOnHTMLError(t *testing.T) {
	const htmlBody = `<html>
<head><title>403 Forbidden</title></head>
<body><center><h1>403 Forbidden</h1></center><hr><center>openresty/1.29.2.5</center></body>
</html>`

	mm := metrics.NewMetricsManagerWithConfig(20, 0.7)
	upstream := &config.UpstreamConfig{Name: "gorouter-app", ChannelUID: "ch_gorouter"}
	c := newModelCircuitTestContext()
	const model = "claude-sonnet-5"
	const apiKey = "sk-broken"

	// 快速通道：连续 2 次即熔断。
	recordModelCircuitFailure(c, mm, upstream, apiKey, model, htmlBody, "Messages")
	recordModelCircuitFailure(c, mm, upstream, apiKey, model, htmlBody, "Messages")

	tracker := mm.ModelCircuit()
	keyHash := metrics.ModelCircuitKeyHash(apiKey)
	if !tracker.IsModelCircuitOpen("ch_gorouter", keyHash, model) {
		t.Fatal("HTML 403 连续 2 次后该组合应处于熔断隔离期")
	}
	// 同渠道同 Key 的其他模型必须不受影响。
	if tracker.IsModelCircuitOpen("ch_gorouter", keyHash, "claude-opus-5") {
		t.Fatal("同渠道其他模型不应被连累")
	}
}

// TestRecordModelCircuitSuccessClearsFailures 成功应解除失败累积。
func TestRecordModelCircuitSuccessClearsFailures(t *testing.T) {
	mm := metrics.NewMetricsManagerWithConfig(20, 0.7)
	upstream := &config.UpstreamConfig{Name: "ch", ChannelUID: "ch_uid"}
	c := newModelCircuitTestContext()
	const model = "claude-sonnet-5"
	const apiKey = "sk-a"

	recordModelCircuitFailure(c, mm, upstream, apiKey, model, "boom", "Messages")
	recordModelCircuitSuccess(mm, upstream, apiKey, model)
	// 序列已清空，再失败一次不应熔断。
	recordModelCircuitFailure(c, mm, upstream, apiKey, model, "boom", "Messages")

	if mm.ModelCircuit().IsModelCircuitOpen("ch_uid", metrics.ModelCircuitKeyHash(apiKey), model) {
		t.Fatal("成功已清空失败序列，单次失败不应熔断")
	}
}

// TestRecordModelCircuitIgnoresMissingIdentity 缺少 ChannelUID 或模型名时不记账，
// 避免把无法归因的失败攒到空键上造成误伤。
func TestRecordModelCircuitIgnoresMissingIdentity(t *testing.T) {
	mm := metrics.NewMetricsManagerWithConfig(20, 0.7)
	c := newModelCircuitTestContext()

	noUID := &config.UpstreamConfig{Name: "legacy"}
	recordModelCircuitFailure(c, mm, noUID, "sk-a", "claude-sonnet-5", "boom", "Messages")
	recordModelCircuitFailure(c, mm, noUID, "sk-a", "claude-sonnet-5", "boom", "Messages")

	withUID := &config.UpstreamConfig{Name: "ch", ChannelUID: "ch_uid"}
	recordModelCircuitFailure(c, mm, withUID, "sk-a", "", "boom", "Messages")
	recordModelCircuitFailure(c, mm, withUID, "sk-a", "", "boom", "Messages")

	if got := mm.ModelCircuit().TrackedCount(); got != 0 {
		t.Fatalf("身份不全时不应产生熔断条目, TrackedCount = %d", got)
	}
}
