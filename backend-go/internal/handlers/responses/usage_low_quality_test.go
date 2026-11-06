package responses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

func TestPatchResponsesUsageLowQualityReplacesOutliersAndPreservesCache(t *testing.T) {
	requestBody := []byte(`{"model":"gpt-5","input":"` + strings.Repeat("prompt ", 240) + `"}`)
	estimatedInput := utils.EstimateResponsesRequestTokens(requestBody)
	outputText := strings.Repeat("answer ", 80)
	estimatedOutput := utils.EstimateTokens(outputText)

	resp := &types.ResponsesResponse{
		Output: []types.ResponsesItem{{Type: "message", Content: outputText}},
		Usage: types.ResponsesUsage{
			InputTokens:  estimatedInput / 2,
			OutputTokens: estimatedOutput / 2,
		},
	}
	patchResponsesUsageWithQuality(nil, resp, requestBody, &config.EnvConfig{}, true)
	if resp.Usage.InputTokens != estimatedInput || resp.Usage.OutputTokens != estimatedOutput {
		t.Fatalf("usage = %+v, want local estimates input=%d output=%d", resp.Usage, estimatedInput, estimatedOutput)
	}
	if resp.Usage.TotalTokens != estimatedInput+estimatedOutput {
		t.Fatalf("TotalTokens = %d, want %d", resp.Usage.TotalTokens, estimatedInput+estimatedOutput)
	}

	closeInput := estimatedInput * 96 / 100
	closeOutput := estimatedOutput * 96 / 100
	withinTolerance := &types.ResponsesResponse{
		Output: []types.ResponsesItem{{Type: "message", Content: outputText}},
		Usage:  types.ResponsesUsage{InputTokens: closeInput, OutputTokens: closeOutput, TotalTokens: closeInput + closeOutput},
	}
	patchResponsesUsageWithQuality(nil, withinTolerance, requestBody, &config.EnvConfig{}, true)
	if withinTolerance.Usage.InputTokens != closeInput || withinTolerance.Usage.OutputTokens != closeOutput {
		t.Fatalf("usage within tolerance was replaced: %+v", withinTolerance.Usage)
	}

	cachedInput := estimatedInput / 2
	cached := &types.ResponsesResponse{
		Output: []types.ResponsesItem{{Type: "message", Content: outputText}},
		Usage: types.ResponsesUsage{
			InputTokens:        cachedInput,
			OutputTokens:       estimatedOutput,
			InputTokensDetails: &types.InputTokensDetails{CachedTokens: 20},
		},
	}
	patchResponsesUsageWithQuality(nil, cached, requestBody, &config.EnvConfig{}, true)
	if cached.Usage.InputTokens != cachedInput {
		t.Fatalf("cached InputTokens = %d, want preserved upstream value %d", cached.Usage.InputTokens, cachedInput)
	}
}

func TestHandleStreamSuccessLowQualityRepairsConvertedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestText := strings.Repeat("prompt ", 240)
	requestBody := []byte(fmt.Sprintf(`{"model":"gpt-5","input":"%s","stream":true}`, requestText))
	estimatedInput := utils.EstimateResponsesRequestTokens(requestBody)
	originalReq := &types.ResponsesRequest{Model: "gpt-5", Input: requestText, Stream: true}
	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"ok"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":80,"completion_tokens":1,"total_tokens":81}}`,
		"data: [DONE]",
		"",
		"",
	}, "\n")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(requestBody)))
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}

	_, err := handleStreamSuccess(c, resp, "chat", &config.EnvConfig{}, nil, time.Now(), originalReq,
		requestBody, common.StreamPreflightTimeouts{}, true)
	if err != nil {
		t.Fatalf("handleStreamSuccess() error = %v", err)
	}
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			Response struct {
				Usage types.ResponsesUsage `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
			continue
		}
		if event.Type == "response.completed" {
			if event.Response.Usage.InputTokens != estimatedInput {
				t.Fatalf("completed input_tokens = %d, want estimate %d", event.Response.Usage.InputTokens, estimatedInput)
			}
			return
		}
	}
	t.Fatalf("response.completed event missing from stream: %s", w.Body.String())
}
