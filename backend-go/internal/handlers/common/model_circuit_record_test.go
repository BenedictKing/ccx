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

// TestModelCircuitKeyIsRequestModel 锁定熔断键为客户端请求的原始模型。
//
// 回归防护：记账侧曾用 attemptModel（autopilot 映射后的模型），而读侧用原始 model，
// 导致 AutoManaged 渠道上写入的键永远查不到、熔断完全失效。这个不一致没有任何
// 外部症状——熔断静默失效，看起来就像功能没生效。
func TestModelCircuitKeyIsRequestModel(t *testing.T) {
	mm := metrics.NewMetricsManagerWithConfig(20, 0.7)
	upstream := &config.UpstreamConfig{Name: "auto-ch", ChannelUID: "ch_auto"}
	c := newModelCircuitTestContext()

	const requestModel = "claude-sonnet-5"  // 客户端请求的模型
	const mappedModel = "claude-sonnet-4-5" // autopilot 映射后实际发给上游的模型
	const apiKey = "sk-auto"

	// 记账用原始模型（与实现约定一致）。
	recordModelCircuitFailure(c, mm, upstream, apiKey, requestModel, "HTTP 403", "Messages")
	recordModelCircuitFailure(c, mm, upstream, apiKey, requestModel, "HTTP 403", "Messages")

	// 读侧闭包必须以原始模型命中，即使 keypool 传入的是映射后模型。
	checker := modelCircuitChecker(mm, requestModel)
	if checker == nil {
		t.Fatal("checker 不应为 nil")
	}
	if !checker("ch_auto", apiKey, mappedModel) {
		t.Fatal("读写键不一致：以原始模型记账后，查询未命中熔断状态")
	}

	// 另一个原始模型不应受影响。
	otherChecker := modelCircuitChecker(mm, "claude-opus-5")
	if otherChecker("ch_auto", apiKey, mappedModel) {
		t.Fatal("其他请求模型不应命中熔断")
	}
}

// TestRecordModelCircuitGlobalFailureBlocksAllModels 验证认证类失败进入 global 桶，
// 熔断后同一渠道和 Key 的所有模型都不可调度。
func TestRecordModelCircuitGlobalFailureBlocksAllModels(t *testing.T) {
	mm := metrics.NewMetricsManagerWithConfig(20, 0.7)
	upstream := &config.UpstreamConfig{Name: "ch", ChannelUID: "ch_uid"}
	c := newModelCircuitTestContext()
	const apiKey = "sk-auth-failed"

	recordModelCircuitGlobal(c, mm, upstream, apiKey, "invalid_api_key", "Messages")
	recordModelCircuitGlobal(c, mm, upstream, apiKey, "invalid_api_key", "Messages")

	tracker := mm.ModelCircuit()
	keyHash := metrics.ModelCircuitKeyHash(apiKey)
	if !tracker.IsModelCircuitOpen("ch_uid", keyHash, "") {
		t.Fatal("连续认证失败后 global 桶应处于熔断隔离期")
	}
	for _, model := range []string{"claude-sonnet-5", "claude-opus-5"} {
		if tracker.IsAvailable("ch_uid", keyHash, model) {
			t.Fatalf("global 桶熔断后模型 %s 不应可用", model)
		}
	}
}

// TestRecordModelCircuitGlobalIgnoresMissingIdentity 缺少渠道身份时不应创建 global 条目。
func TestRecordModelCircuitGlobalIgnoresMissingIdentity(t *testing.T) {
	mm := metrics.NewMetricsManagerWithConfig(20, 0.7)
	c := newModelCircuitTestContext()
	upstream := &config.UpstreamConfig{Name: "legacy"}

	recordModelCircuitGlobal(c, mm, upstream, "sk-a", "invalid_api_key", "Messages")
	recordModelCircuitGlobal(c, mm, upstream, "sk-a", "invalid_api_key", "Messages")

	if got := mm.ModelCircuit().TrackedCount(); got != 0 {
		t.Fatalf("缺少 ChannelUID 时不应产生 global 熔断条目, TrackedCount = %d", got)
	}
}

// TestGlobalBlacklistFailureDoesNotAlsoOpenExactBucket 锁定错误 scope 路由：
// 认证类错误只能累计 global 桶，不能同时污染当前模型的 exact 桶。
func TestGlobalBlacklistFailureDoesNotAlsoOpenExactBucket(t *testing.T) {
	mm := metrics.NewMetricsManagerWithConfig(20, 0.7)
	upstream := &config.UpstreamConfig{Name: "ch", ChannelUID: "ch_uid"}
	c := newModelCircuitTestContext()
	const apiKey = "sk-auth-failed"
	const model = "claude-sonnet-5"

	blResult := ShouldBlacklistKey(http.StatusUnauthorized, []byte(`{"error":{"type":"authentication_error","message":"invalid api key"}}`))
	if !blResult.ShouldBlacklist {
		t.Fatal("测试前提不成立：认证错误应被识别为全局拉黑")
	}
	for range 2 {
		recordModelCircuitGlobal(c, mm, upstream, apiKey, blResult.Reason, "Messages")
		if !blResult.ShouldBlacklist {
			recordModelCircuitFailure(c, mm, upstream, apiKey, model, blResult.Reason, "Messages")
		}
	}

	tracker := mm.ModelCircuit()
	keyHash := metrics.ModelCircuitKeyHash(apiKey)
	if !tracker.IsModelCircuitOpen("ch_uid", keyHash, "") {
		t.Fatal("认证错误应打开 global 桶")
	}
	if tracker.IsModelCircuitOpen("ch_uid", keyHash, model) {
		t.Fatal("认证错误不应同时打开当前模型的 exact 桶")
	}
}
