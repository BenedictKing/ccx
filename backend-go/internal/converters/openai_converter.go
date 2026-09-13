package converters

import (
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
)

// ============== OpenAI Chat Completions 转换器 ==============

// OpenAIChatConverter 实现 Responses → OpenAI Chat Completions 转换
type OpenAIChatConverter struct{}

// ToProviderRequest 将 Responses 请求转换为 OpenAI Chat Completions 格式
func (c *OpenAIChatConverter) ToProviderRequest(sess *session.Session, req *types.ResponsesRequest) (interface{}, error) {
	// 转换 messages
	messages, err := ResponsesToOpenAIChatMessages(sess, req.Input, req.Instructions)
	if err != nil {
		return nil, err
	}

	// 构建 OpenAI 请求
	openaiReq := map[string]interface{}{
		"model":    req.Model,
		"messages": messages,
		"stream":   req.Stream,
	}

	// 复制其他参数
	if req.MaxTokens > 0 {
		openaiReq["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		openaiReq["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		openaiReq["top_p"] = req.TopP
	}
	if req.FrequencyPenalty != 0 {
		openaiReq["frequency_penalty"] = req.FrequencyPenalty
	}
	if req.PresencePenalty != 0 {
		openaiReq["presence_penalty"] = req.PresencePenalty
	}
	if req.Stop != nil {
		openaiReq["stop"] = req.Stop
	}
	if req.User != "" {
		openaiReq["user"] = req.User
	}
	if req.StreamOptions != nil {
		openaiReq["stream_options"] = req.StreamOptions
	}

	// Tools conversion with Codex custom tool support.
	codexEnabled := false
	if req.TransformerMetadata != nil {
		if v, ok := req.TransformerMetadata["codex_tool_compat_enabled"].(bool); ok {
			codexEnabled = v
		}
	}
	var codexToolCtx CodexToolContext
	rawTools := req.RawTools
	if codexEnabled {
		rawTools = MergeCodexToolSearchOutputTools(req.RawTools, sess, req.Input)
		// additional_tools 提升场景：providers 层已把提升前（namespace/custom 形态）
		// 的 ctx 放入 TransformerMetadata——rawTools 是提升后的扁平形态，据此重建
		// 会丢 custom 代理语义，优先复用已有 ctx。
		if existing, ok := req.TransformerMetadata["codex_tool_context"].(CodexToolContext); ok && (existing.HasCustomTools || existing.HasNamespaceTools) {
			codexToolCtx = existing
		} else {
			codexToolCtx = BuildCodexToolContextFromRaw(rawTools)
		}
		if len(rawTools) > 0 {
			if req.TransformerMetadata == nil {
				req.TransformerMetadata = make(map[string]interface{})
			}
			// additional_tools 提升场景下该键已是提升前原始形态（providers 层写入），
			// 不用提升后的扁平 rawTools 覆盖（响应侧 remap 依赖原始 namespace/custom 语义）。
			if _, exists := req.TransformerMetadata["codex_merged_raw_tools"]; !exists {
				req.TransformerMetadata["codex_merged_raw_tools"] = rawTools
			}
		}
	}
	if len(rawTools) > 0 {
		if codexEnabled {
			if tools := responsesRawToolsToOpenAIWithContext(rawTools, codexToolCtx); len(tools) > 0 {
				openaiReq["tools"] = tools
			}
		} else if tools := responsesRawToolsToOpenAI(rawTools); len(tools) > 0 {
			openaiReq["tools"] = tools
		}
	} else if len(req.Tools) > 0 {
		if codexEnabled {
			if tools := responsesToolsToOpenAIWithContext(req.Tools, codexToolCtx); len(tools) > 0 {
				openaiReq["tools"] = tools
			}
		} else if tools := responsesToolsToOpenAI(req.Tools); len(tools) > 0 {
			openaiReq["tools"] = tools
		}
	}
	if req.ToolChoice != nil {
		if _, hasTools := openaiReq["tools"]; hasTools {
			if codexEnabled {
				if converted := ConvertToolChoiceForCodex(req.ToolChoice, codexToolCtx); converted != nil {
					openaiReq["tool_choice"] = converted
				} else {
					openaiReq["tool_choice"] = req.ToolChoice
				}
			} else {
				openaiReq["tool_choice"] = req.ToolChoice
			}
		}
	}
	if req.ParallelToolCalls != nil {
		if _, hasTools := openaiReq["tools"]; hasTools {
			openaiReq["parallel_tool_calls"] = *req.ParallelToolCalls
		}
	}

	// Store CodexToolContext in TransformerMetadata for response conversion.
	if codexToolCtx.HasCustomTools || codexToolCtx.HasNamespaceTools || codexToolCtx.HasBuiltinFunctionTools {
		if req.TransformerMetadata == nil {
			req.TransformerMetadata = make(map[string]interface{})
		}
		req.TransformerMetadata["codex_tool_context"] = codexToolCtx
	}

	return openaiReq, nil
}

// FromProviderResponse 将 OpenAI Chat 响应转换为 Responses 格式
func (c *OpenAIChatConverter) FromProviderResponse(resp map[string]interface{}, sessionID string) (*types.ResponsesResponse, error) {
	return OpenAIChatResponseToResponses(resp, sessionID)
}

// GetProviderName 获取上游服务名称
func (c *OpenAIChatConverter) GetProviderName() string {
	return "OpenAI Chat Completions"
}

// ============== OpenAI Completions 转换器 ==============

// OpenAICompletionsConverter 实现 Responses → OpenAI Completions 转换
type OpenAICompletionsConverter struct{}

// ToProviderRequest 将 Responses 请求转换为 OpenAI Completions 格式
func (c *OpenAICompletionsConverter) ToProviderRequest(sess *session.Session, req *types.ResponsesRequest) (interface{}, error) {
	// 提取纯文本（Completions API 不支持 messages）
	prompt, err := ExtractTextFromResponses(sess, req.Input)
	if err != nil {
		return nil, err
	}

	// 如果有 instructions，添加到 prompt 前面
	if req.Instructions != "" {
		prompt = req.Instructions + "\n\n" + prompt
	}

	// 构建 OpenAI Completions 请求
	completionsReq := map[string]interface{}{
		"model":  req.Model,
		"prompt": prompt,
		"stream": req.Stream,
	}

	// 复制其他参数
	if req.MaxTokens > 0 {
		completionsReq["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		completionsReq["temperature"] = req.Temperature
	}
	if req.TopP > 0 {
		completionsReq["top_p"] = req.TopP
	}
	if req.FrequencyPenalty != 0 {
		completionsReq["frequency_penalty"] = req.FrequencyPenalty
	}
	if req.PresencePenalty != 0 {
		completionsReq["presence_penalty"] = req.PresencePenalty
	}
	if req.Stop != nil {
		completionsReq["stop"] = req.Stop
	}
	if req.User != "" {
		completionsReq["user"] = req.User
	}

	return completionsReq, nil
}

// FromProviderResponse 将 OpenAI Completions 响应转换为 Responses 格式
func (c *OpenAICompletionsConverter) FromProviderResponse(resp map[string]interface{}, sessionID string) (*types.ResponsesResponse, error) {
	return OpenAICompletionsResponseToResponses(resp, sessionID)
}

// GetProviderName 获取上游服务名称
func (c *OpenAICompletionsConverter) GetProviderName() string {
	return "OpenAI Completions"
}
