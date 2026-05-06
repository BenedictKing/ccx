package gemini

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

// handleStreamSuccess 处理流式响应
func handleStreamSuccess(
	c *gin.Context,
	resp *http.Response,
	upstream *config.UpstreamConfig,
	apiKey string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	model string,
) (*types.Usage, error) {
	upstreamType := upstream.ServiceType
	if common.ShouldDirectPassthroughForRequest(c.Request.URL.Path, upstream, apiKey) {
		totalUsage, err := common.HandleRawGeminiStreamPassthrough(c, resp, upstream)
		if envCfg.EnableResponseLogs {
			responseTime := time.Since(startTime).Milliseconds()
			log.Printf("[Gemini-Stream-Timing] 流式响应完成: %dms", responseTime)
		}
		return totalUsage, err
	}

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("[Gemini-Stream] 警告: ResponseWriter 不支持 Flusher")
	}

	var totalUsage *types.Usage

	switch upstreamType {
	case "gemini":
		totalUsage = streamGeminiToGemini(c, resp, flusher, envCfg)
	case "claude":
		totalUsage = streamClaudeToGemini(c, resp, flusher, envCfg, model)
	case "openai":
		totalUsage = streamOpenAIToGemini(c, resp, flusher, envCfg, model)
	case "responses":
		totalUsage = streamResponsesToGemini(c, resp, flusher, envCfg, model)
	default:
		// 默认透传
		totalUsage = streamGeminiToGemini(c, resp, flusher, envCfg)
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Gemini-Stream-Timing] 流式响应完成: %dms", responseTime)
	}

	return totalUsage, nil
}

// streamGeminiToGemini Gemini 上游直接透传
func streamGeminiToGemini(
	c *gin.Context,
	resp *http.Response,
	flusher http.Flusher,
	envCfg *config.EnvConfig,
) *types.Usage {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	var totalUsage *types.Usage

	for scanner.Scan() {
		line := scanner.Text()

		// 直接转发 SSE 数据
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")

			// 尝试解析 usage
			var chunk types.GeminiStreamChunk
			if err := json.Unmarshal([]byte(jsonData), &chunk); err == nil {
				if chunk.UsageMetadata != nil {
					totalUsage = &types.Usage{
						InputTokens:  chunk.UsageMetadata.PromptTokenCount - chunk.UsageMetadata.CachedContentTokenCount,
						OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
					}
				}
			}

			_, _ = fmt.Fprintf(c.Writer, "%s\n", line)
		} else if line != "" {
			_, _ = fmt.Fprintf(c.Writer, "%s\n", line)
		} else {
			_, _ = fmt.Fprintf(c.Writer, "\n")
		}

		if flusher != nil {
			flusher.Flush()
		}
	}

	return totalUsage
}

// streamClaudeToGemini Claude 流式响应转换为 Gemini 格式
func streamClaudeToGemini(
	c *gin.Context,
	resp *http.Response,
	flusher http.Flusher,
	envCfg *config.EnvConfig,
	model string,
) *types.Usage {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var totalUsage *types.Usage
	var currentText strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonData := strings.TrimPrefix(line, "data: ")
		if jsonData == "[DONE]" {
			break
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "content_block_delta":
			// 文本增量
			delta, ok := event["delta"].(map[string]interface{})
			if !ok {
				continue
			}
			deltaType, _ := delta["type"].(string)
			if deltaType == "text_delta" {
				text, _ := delta["text"].(string)
				currentText.WriteString(text)

				// 转换为 Gemini 格式
				geminiChunk := types.GeminiStreamChunk{
					Candidates: []types.GeminiCandidate{
						{
							Content: &types.GeminiContent{
								Parts: []types.GeminiPart{
									{Text: text},
								},
								Role: "model",
							},
						},
					},
				}

				chunkBytes, _ := json.Marshal(geminiChunk)
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
				if flusher != nil {
					flusher.Flush()
				}
			}

		case "message_delta":
			// 消息完成，包含 usage
			if usage, ok := event["usage"].(map[string]interface{}); ok {
				inputTokens := 0
				outputTokens := 0
				if v, ok := usage["input_tokens"].(float64); ok {
					inputTokens = int(v)
				}
				if v, ok := usage["output_tokens"].(float64); ok {
					outputTokens = int(v)
				}
				totalUsage = &types.Usage{
					InputTokens:  inputTokens,
					OutputTokens: outputTokens,
				}

				// 发送带 finishReason 和 usage 的最终块
				geminiChunk := types.GeminiStreamChunk{
					Candidates: []types.GeminiCandidate{
						{
							FinishReason: "STOP",
						},
					},
					UsageMetadata: &types.GeminiUsageMetadata{
						PromptTokenCount:     inputTokens,
						CandidatesTokenCount: outputTokens,
						TotalTokenCount:      inputTokens + outputTokens,
					},
				}
				chunkBytes, _ := json.Marshal(geminiChunk)
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}

	return totalUsage
}

// streamOpenAIToGemini OpenAI 流式响应转换为 Gemini 格式
func streamOpenAIToGemini(
	c *gin.Context,
	resp *http.Response,
	flusher http.Flusher,
	envCfg *config.EnvConfig,
	model string,
) *types.Usage {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var totalUsage *types.Usage
	var currentText strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonData := strings.TrimPrefix(line, "data: ")
		if jsonData == "[DONE]" {
			break
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
			continue
		}

		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			// 检查是否有 usage（某些 OpenAI 兼容 API 在最后发送）
			if usage, ok := chunk["usage"].(map[string]interface{}); ok {
				promptTokens := 0
				completionTokens := 0
				if v, ok := usage["prompt_tokens"].(float64); ok {
					promptTokens = int(v)
				}
				if v, ok := usage["completion_tokens"].(float64); ok {
					completionTokens = int(v)
				}
				totalUsage = &types.Usage{
					InputTokens:  promptTokens,
					OutputTokens: completionTokens,
				}

				// 发送带 usage 的最终块
				geminiChunk := types.GeminiStreamChunk{
					UsageMetadata: &types.GeminiUsageMetadata{
						PromptTokenCount:     promptTokens,
						CandidatesTokenCount: completionTokens,
						TotalTokenCount:      promptTokens + completionTokens,
					},
				}
				chunkBytes, _ := json.Marshal(geminiChunk)
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
				if flusher != nil {
					flusher.Flush()
				}
			}
			continue
		}

		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}

		// 检查 finish_reason
		finishReason, hasFinish := choice["finish_reason"].(string)

		// 获取 delta
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			if hasFinish && finishReason != "" {
				// 发送 finishReason
				geminiFinishReason := openaiFinishReasonToGemini(finishReason)
				geminiChunk := types.GeminiStreamChunk{
					Candidates: []types.GeminiCandidate{
						{
							FinishReason: geminiFinishReason,
						},
					},
				}
				chunkBytes, _ := json.Marshal(geminiChunk)
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
				if flusher != nil {
					flusher.Flush()
				}
			}
			continue
		}

		// 提取文本内容
		content, _ := delta["content"].(string)
		if content != "" {
			currentText.WriteString(content)

			geminiChunk := types.GeminiStreamChunk{
				Candidates: []types.GeminiCandidate{
					{
						Content: &types.GeminiContent{
							Parts: []types.GeminiPart{
								{Text: content},
							},
							Role: "model",
						},
					},
				},
			}

			chunkBytes, _ := json.Marshal(geminiChunk)
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
			if flusher != nil {
				flusher.Flush()
			}
		}

		// 如果有 finish_reason，发送
		if hasFinish && finishReason != "" {
			geminiFinishReason := openaiFinishReasonToGemini(finishReason)
			geminiChunk := types.GeminiStreamChunk{
				Candidates: []types.GeminiCandidate{
					{
						FinishReason: geminiFinishReason,
					},
				},
			}
			chunkBytes, _ := json.Marshal(geminiChunk)
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}

	return totalUsage
}

// openaiFinishReasonToGemini 将 OpenAI 停止原因转换为 Gemini 格式
func openaiFinishReasonToGemini(finishReason string) string {
	switch finishReason {
	case "stop":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "tool_calls":
		return "STOP"
	case "content_filter":
		return "SAFETY"
	default:
		return "STOP"
	}
}

// streamResponsesToGemini Responses 流式响应转换为 Gemini 格式
func streamResponsesToGemini(
	c *gin.Context,
	resp *http.Response,
	flusher http.Flusher,
	envCfg *config.EnvConfig,
	model string,
) *types.Usage {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var totalUsage *types.Usage
	var converterState any

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// 使用转换器将 Responses SSE 转换为 Gemini SSE
		events := converters.ConvertResponsesToGeminiStream(
			c.Request.Context(),
			model,
			[]byte(line),
			&converterState,
		)

		for _, event := range events {
			// 尝试从事件中提取 usage
			if strings.HasPrefix(event, "data: ") {
				jsonData := strings.TrimPrefix(event, "data: ")
				jsonData = strings.TrimSuffix(jsonData, "\n\n")
				var chunk types.GeminiStreamChunk
				if err := json.Unmarshal([]byte(jsonData), &chunk); err == nil {
					if chunk.UsageMetadata != nil {
						totalUsage = &types.Usage{
							InputTokens:  chunk.UsageMetadata.PromptTokenCount - chunk.UsageMetadata.CachedContentTokenCount,
							OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
						}
					}
				}
			}

			_, _ = fmt.Fprint(c.Writer, event)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[Gemini-Stream] Responses流式转换读取错误: %v", err)
	}

	return totalUsage
}
