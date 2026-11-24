package responses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
)

// 使用明确的上游 usage 核对公开出口；这些假上游数据不代表 llama.cpp 实测。
func TestResponsesContextUsageAcrossCachedTurns(t *testing.T) {
	for _, upstreamType := range []string{"openai", "responses"} {
		for _, stream := range []bool{false, true} {
			for _, lowQuality := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/lowQuality=%t", upstreamType, stream, lowQuality), func(t *testing.T) {
					var calls atomic.Int32
					promptTokens := []int{18000, 38451, 64256}
					cachedTokens := []int{12000, 36000, 60000}
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						round := int(calls.Add(1)) - 1
						if round >= len(promptTokens) {
							t.Errorf("unexpected upstream retry: %d", round)
							w.WriteHeader(http.StatusInternalServerError)
							return
						}
						var request map[string]interface{}
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Errorf("decode upstream request: %v", err)
						}
						if upstreamType == "openai" {
							messages, _ := request["messages"].([]interface{})
							if len(messages) != 2*round+4 {
								t.Errorf("round %d messages = %d, want %d", round, len(messages), 2*round+4)
							}
							developers := 0
							for _, raw := range messages {
								if message, ok := raw.(map[string]interface{}); ok && message["role"] == "developer" {
									developers++
									if message["content"] != "keep these instructions" {
										t.Error("developer instructions changed")
									}
								}
							}
							if developers != 2 {
								t.Errorf("developer messages = %d, want exactly the two supplied messages", developers)
							}
						}
						writeContextUsageResponse(w, upstreamType, stream, promptTokens[round], cachedTokens[round])
					}))
					defer upstream.Close()
					router := newResponsesTestRouter(t, config.UpstreamConfig{
						BaseURL: upstream.URL, APIKeys: []string{"key"}, ServiceType: upstreamType, LowQuality: lowQuality,
					}, session.NewSessionManager(time.Hour, 100, 100000))
					input := []types.ResponsesItem{
						{Type: "message", Role: "developer", Content: "keep these instructions"},
						{Type: "message", Role: "developer", Content: "keep these instructions"},
					}
					for round, wantInput := range promptTokens {
						input = append(input, types.ResponsesItem{Type: "message", Role: "user", Content: fmt.Sprintf("turn %d", round)})
						body, err := json.Marshal(map[string]interface{}{"model": "test", "instructions": "fixed system instructions", "input": input, "stream": stream, "store": false})
						if err != nil {
							t.Fatal(err)
						}
						w := performResponsesHandlerRequest(t, router, string(body))
						if w.Code != http.StatusOK {
							t.Fatalf("round %d status = %d: %s", round, w.Code, w.Body.String())
						}
						var usage types.ResponsesUsage
						if stream {
							raw, err := json.Marshal(usageField(t, w.Body.String(), ""))
							if err != nil {
								t.Fatal(err)
							}
							if err := json.Unmarshal(raw, &usage); err != nil {
								t.Fatal(err)
							}
						} else {
							var response types.ResponsesResponse
							if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
								t.Fatal(err)
							}
							usage = response.Usage
						}
						if usage.InputTokens != wantInput || usage.InputTokensDetails == nil || usage.InputTokensDetails.CachedTokens != cachedTokens[round] {
							t.Fatalf("round %d usage = %+v, want input=%d cached=%d", round, usage, wantInput, cachedTokens[round])
						}
						if usage.OutputTokens <= 0 || usage.TotalTokens != usage.InputTokens+usage.OutputTokens {
							t.Fatalf("round %d inconsistent total: %+v", round, usage)
						}
						input = append(input, types.ResponsesItem{Type: "message", Role: "assistant", Content: "reply"})
					}
					if calls.Load() != 3 {
						t.Fatalf("upstream calls = %d, want 3", calls.Load())
					}
				})
			}
		}
	}
}

func writeContextUsageResponse(w http.ResponseWriter, upstreamType string, stream bool, input, cached int) {
	content := strings.Repeat("reply ", 70)
	if upstreamType == "openai" {
		usage := fmt.Sprintf(`{"prompt_tokens":%d,"completion_tokens":100,"total_tokens":%d,"prompt_tokens_details":{"cached_tokens":%d}}`, input, input+100, cached)
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"chat1\",\"choices\":[{\"delta\":{\"content\":%q}}]}\n\ndata: {\"id\":\"chat1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":%s}\n\ndata: [DONE]\n\n", content, usage)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"chat1","choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":%s}`, content, usage)
		return
	}
	response := fmt.Sprintf(`{"id":"resp1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":%d,"output_tokens":100,"total_tokens":%d,"input_tokens_details":{"cached_tokens":%d}}}`, content, input, input+100, cached)
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", content, response)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, response)
}
