package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// bitdeerRegressionModels 模拟 bitdeer 目录的 API 原始顺序：免费 chat 模型排在
// 第一位，两个 embedding 模型按 ASCII 序会霸占候选补位窗口（2026-09-05 事件）。
var bitdeerRegressionModels = []string{
	"deepseek-ai/DeepSeek-V4-Flash",
	"BAAI/bge-m3",
	"BAAI/bge-reranker-v2-m3",
	"ByteDance-Seed/Seed-1.6",
	"MiniMaxAI/MiniMax-M3",
	"openai/gpt-oss-20b",
}

func bitdeerModelsBody() []byte {
	var sb strings.Builder
	sb.WriteString(`{"object":"list","data":[`)
	for i, model := range bitdeerRegressionModels {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"object":"model","id":"` + model + `","owned_by":"bitdeerai"}`)
	}
	sb.WriteString(`]}`)
	return []byte(sb.String())
}

func isBitdeerPaidModel(model string) bool {
	switch model {
	case "ByteDance-Seed/Seed-1.6", "MiniMaxAI/MiniMax-M3", "openai/gpt-oss-20b":
		return true
	}
	return false
}

// bitdeerUpstream 模拟 bitdeer：/v1/models 返回原始顺序目录；付费模型对所有协议
// 返回 402 insufficient balance；免费模型 deepseek-ai/DeepSeek-V4-Flash 在 chat
// 协议成功（其余协议 404，与真实行为一致）。
func bitdeerUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(bitdeerModelsBody())
			return
		}
		model := ""
		if strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
			model = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1beta/models/"), ":streamGenerateContent")
		} else if body, err := io.ReadAll(r.Body); err == nil {
			var payload map[string]interface{}
			if json.Unmarshal(body, &payload) == nil {
				if raw, ok := payload["model"].(string); ok {
					model = raw
				}
			}
		}
		if isBitdeerPaidModel(model) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"message":"insufficient balance","type":"insufficient_quota"}}`))
			return
		}
		if r.URL.Path == "/v1/chat/completions" && model == "deepseek-ai/DeepSeek-V4-Flash" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n"))
			return
		}
		http.NotFound(w, r)
	}))
}

func runFastDiscovery(t *testing.T, upstream *httptest.Server) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/channel-discovery-fast", ChannelDiscoveryFast(nil))
	body := []byte(`{"baseUrls":["` + upstream.URL + `"],"apiKeys":["sk-bitdeer-test"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery-fast", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

// TestChannelDiscoveryFastBitdeerFreeModelRegression 锁定 2026-09-05 bitdeer 事件修复：
// API 原始顺序保留 + 非 chat 模型过滤后，限时免费模型（付费候选全 402 时仍可调用）
// 必须进入探测候选并让 fast 发现成功。
func TestChannelDiscoveryFastBitdeerFreeModelRegression(t *testing.T) {
	upstream := bitdeerUpstream()
	defer upstream.Close()

	recorder := runFastDiscovery(t, upstream)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var resp ChannelDiscoveryFastResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PrimaryKind != "chat" {
		t.Fatalf("primaryKind=%q, want chat", resp.PrimaryKind)
	}
	if resp.TestedModel != "deepseek-ai/DeepSeek-V4-Flash" {
		t.Fatalf("testedModel=%q, want deepseek-ai/DeepSeek-V4-Flash", resp.TestedModel)
	}
}

// TestChannelDiscoveryFastAllBalanceFailureMessage 验证所有候选探测均因余额/配额
// 失败时，422 错误信息直说根因（Key 余额不足）而非"无法确定渠道类型"。
func TestChannelDiscoveryFastAllBalanceFailureMessage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(bitdeerModelsBody())
			return
		}
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"insufficient balance","type":"insufficient_quota"}}`))
	}))
	defer upstream.Close()

	recorder := runFastDiscovery(t, upstream)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "余额") {
		t.Fatalf("error message should state balance root cause, got: %s", recorder.Body.String())
	}
}

// TestDiscoveryProbeModelsBitdeerOrdering 单元级锁定：bitdeer 目录下候选必须包含
// 免费模型（API 原始顺序补位），且 embedding/reranker 模型不得进入 4 协议探测候选。
func TestDiscoveryProbeModelsBitdeerOrdering(t *testing.T) {
	selected := selectDiscoveryModels(bitdeerRegressionModels, nil)
	probes := discoveryProbeModels(selected, bitdeerRegressionModels)

	joined := strings.Join(probes, "\x00")
	if !strings.Contains(joined, "deepseek-ai/DeepSeek-V4-Flash") {
		t.Fatalf("probe candidates missing free model: %v", probes)
	}
	for _, nonChat := range []string{"BAAI/bge-m3", "BAAI/bge-reranker-v2-m3"} {
		if strings.Contains(joined, nonChat) {
			t.Fatalf("probe candidates must exclude non-chat model %s: %v", nonChat, probes)
		}
	}
}

// TestUniqueDiscoveryModelsPreservingOrder 验证去重保序：平台目录的原生排序
// （主推/免费模型在前）不得被 ASCII 排序覆盖。
func TestUniqueDiscoveryModelsPreservingOrder(t *testing.T) {
	got := uniqueDiscoveryModelsPreservingOrder([]string{"b-model", "a-model", "b-model", " c-model "})
	want := []string{"b-model", "a-model", "c-model"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order not preserved: got %v, want %v", got, want)
		}
	}
}

// TestIsBalanceQuotaTestFailure 验证余额/配额类失败识别：402 状态码与高置信
// 错误文本均命中，普通错误不命中。
func TestIsBalanceQuotaTestFailure(t *testing.T) {
	msg := "insufficient balance"
	cases := []struct {
		name    string
		result  ModelTestResult
		wantHit bool
	}{
		{"402 status", ModelTestResult{statusCode: 402}, true},
		{"balance message", ModelTestResult{Error: &msg}, true},
		{"quota message", ModelTestResult{Error: strPtr("insufficient_quota for model")}, true},
		{"normal error", ModelTestResult{Error: strPtr("http_error_404")}, false},
		{"no error", ModelTestResult{}, false},
	}
	for _, tc := range cases {
		if got := isBalanceQuotaTestFailure(tc.result); got != tc.wantHit {
			t.Fatalf("%s: isBalanceQuotaTestFailure=%v, want %v", tc.name, got, tc.wantHit)
		}
	}
}

func strPtr(s string) *string { return &s }
