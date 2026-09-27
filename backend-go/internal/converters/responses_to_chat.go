package converters

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ConvertResponsesToOpenAIChatRequest 将 OpenAI Responses 请求格式转换为 OpenAI Chat Completions 格式
// 转换内容包括:
// 1. model 和 stream 配置
// 2. instructions → system message
// 3. input 数组 → messages 数组
// 4. tools 定义转换
// 5. function_call 和 function_call_output 处理
// 6. 生成参数映射 (max_tokens, reasoning 等)
//
// 参数:
//   - modelName: 要使用的模型名称
//   - inputRawJSON: Responses 格式的原始 JSON 请求
//   - stream: 是否为流式请求
//
// 返回:
//   - []byte: Chat Completions 格式的请求 JSON
func ConvertResponsesToOpenAIChatRequest(modelName string, inputRawJSON []byte, stream bool) []byte {
	// 基础 Chat Completions 模板
	out := `{"model":"","messages":[],"stream":false}`

	root := gjson.ParseBytes(inputRawJSON)

	// 设置 model
	out, _ = sjson.Set(out, "model", modelName)

	// 设置 stream
	out, _ = sjson.Set(out, "stream", stream)

	// 如果是流式请求，添加 stream_options 以获取 usage 信息
	if stream {
		out, _ = sjson.Set(out, "stream_options.include_usage", true)
	}

	// 映射生成参数
	if maxTokens := root.Get("max_output_tokens"); maxTokens.Exists() {
		out, _ = sjson.Set(out, "max_tokens", maxTokens.Int())
	}

	if temperature := root.Get("temperature"); temperature.Exists() {
		out, _ = sjson.Set(out, "temperature", temperature.Float())
	}

	if topP := root.Get("top_p"); topP.Exists() {
		out, _ = sjson.Set(out, "top_p", topP.Float())
	}

	if user := root.Get("user"); user.Exists() {
		out, _ = sjson.Set(out, "user", user.String())
	}

	// 转换 instructions → system message
	if instructions := root.Get("instructions"); instructions.Exists() && instructions.String() != "" {
		systemMessage := `{"role":"system","content":""}`
		systemMessage, _ = sjson.Set(systemMessage, "content", instructions.String())
		out, _ = sjson.SetRaw(out, "messages.-1", systemMessage)
	}

	// 转换 input 数组 → messages
	if input := root.Get("input"); input.Exists() {
		if input.IsArray() {
			out = convertInputArrayToMessages(input, out)
		} else if input.Type == gjson.String {
			// 简单字符串输入
			msg := `{"role":"user","content":""}`
			msg, _ = sjson.Set(msg, "content", input.String())
			out, _ = sjson.SetRaw(out, "messages.-1", msg)
		}
	}

	codexToolCompat := root.Get("transformer_metadata.codex_tool_compat_enabled").Bool()

	// 转换 tools
	if tools := root.Get("tools"); tools.Exists() && tools.IsArray() {
		out = convertToolsToOpenAIFormat(tools, out, codexToolCompat)
	}

	// 转换 reasoning.effort → reasoning_effort
	if reasoningEffort := root.Get("reasoning.effort"); reasoningEffort.Exists() {
		effort := reasoningEffort.String()
		switch effort {
		case "none", "off", "disabled":
			out, _ = sjson.Set(out, "reasoning_effort", "none")
		case "auto":
			out, _ = sjson.Set(out, "reasoning_effort", "auto")
		case "minimal":
			out, _ = sjson.Set(out, "reasoning_effort", "low")
		case "low":
			out, _ = sjson.Set(out, "reasoning_effort", "low")
		case "medium":
			out, _ = sjson.Set(out, "reasoning_effort", "medium")
		case "high":
			out, _ = sjson.Set(out, "reasoning_effort", "high")
		case "xhigh":
			out, _ = sjson.Set(out, "reasoning_effort", "xhigh")
		default:
			out, _ = sjson.Set(out, "reasoning_effort", "auto")
		}
	}

	// 转换 tool_choice (仅在 tools 存在时才写入，避免上游拒绝)
	if toolChoice := root.Get("tool_choice"); toolChoice.Exists() {
		if gjson.GetBytes([]byte(out), "tools").Exists() {
			out, _ = sjson.Set(out, "tool_choice", toolChoice.Value())
		}
	}

	// 转换 parallel_tool_calls (仅在 tools 存在时才写入)
	if parallelToolCalls := root.Get("parallel_tool_calls"); parallelToolCalls.Exists() {
		if gjson.GetBytes([]byte(out), "tools").Exists() {
			out, _ = sjson.Set(out, "parallel_tool_calls", parallelToolCalls.Bool())
		}
	}

	return []byte(out)
}

// convertInputArrayToMessages 将 input 数组转换为 messages 数组
func convertInputArrayToMessages(input gjson.Result, out string) string {
	input.ForEach(func(_, item gjson.Result) bool {
		itemType := item.Get("type").String()

		// 如果没有 type 但有 role，则视为 message
		if itemType == "" && item.Get("role").String() != "" {
			itemType = "message"
		}

		switch itemType {
		case "message":
			out = convertMessageItem(item, out)

		case "function_call":
			out = convertFunctionCallItem(item, out)

		case "function_call_output":
			out = convertFunctionCallOutputItem(item, out)
		}

		return true
	})

	return out
}

// convertMessageItem 转换 message 类型的 item
func convertMessageItem(item gjson.Result, out string) string {
	role := item.Get("role").String()
	if role == "" {
		role = "user"
	}

	message := `{"role":"","content":""}`
	message, _ = sjson.Set(message, "role", role)

	content := item.Get("content")
	if content.Exists() {
		if content.IsArray() {
			// 文本-only 保持 string，包含图片时保留 OpenAI Chat 多模态数组
			var messageContent string
			chatContent := `[]`
			hasMedia := false
			var toolCalls []interface{}

			content.ForEach(func(_, contentItem gjson.Result) bool {
				contentType := contentItem.Get("type").String()
				if contentType == "" {
					contentType = "input_text"
				}

				switch contentType {
				case "input_text", "output_text", "text":
					text := contentItem.Get("text").String()
					if text != "" {
						textBlock := `{"type":"text","text":""}`
						textBlock, _ = sjson.Set(textBlock, "text", text)
						chatContent, _ = sjson.SetRaw(chatContent, "-1", textBlock)
					}
					if messageContent != "" {
						messageContent += "\n" + text
					} else {
						messageContent = text
					}
				case "input_image", "image_url":
					if imageBlock := responsesImageContentToChatBlock(contentItem); imageBlock != "" {
						chatContent, _ = sjson.SetRaw(chatContent, "-1", imageBlock)
						hasMedia = true
					}
				default:
					if strings.HasPrefix(contentType, "input_audio") || strings.HasPrefix(contentType, "audio") {
						log.Printf("[Converter-Responses] 音频 content block 在转换路径下被丢弃 (type=%s)，当前 Chat Completions 格式不支持音频输入", contentType)
					}
				}
				return true
			})

			if hasMedia {
				message, _ = sjson.SetRaw(message, "content", chatContent)
			} else if messageContent != "" {
				message, _ = sjson.Set(message, "content", messageContent)
			}

			if len(toolCalls) > 0 {
				message, _ = sjson.Set(message, "tool_calls", toolCalls)
			}
		} else if content.Type == gjson.String {
			// content 是字符串
			message, _ = sjson.Set(message, "content", content.String())
		}
	}

	out, _ = sjson.SetRaw(out, "messages.-1", message)
	return out
}

func responsesImageContentToChatBlock(contentItem gjson.Result) string {
	if block := responsesImageURLToChatBlock(contentItem.Get("image_url"), contentItem.Get("detail")); block != "" {
		return block
	}
	return responsesImageSourceToChatBlock(contentItem.Get("source"), contentItem.Get("detail"))
}

func responsesImageURLToChatBlock(imageURL gjson.Result, detail gjson.Result) string {
	if !imageURL.Exists() {
		return ""
	}
	block := `{"type":"image_url","image_url":{}}`
	if imageURL.Type == gjson.String {
		if imageURL.String() == "" {
			return ""
		}
		block, _ = sjson.Set(block, "image_url.url", imageURL.String())
	} else if imageURL.IsObject() {
		if imageURL.Get("url").String() == "" {
			return ""
		}
		block, _ = sjson.SetRaw(block, "image_url", imageURL.Raw)
	} else {
		return ""
	}
	return responsesImageChatBlockWithDetail(block, detail)
}

func responsesImageSourceToChatBlock(source gjson.Result, detail gjson.Result) string {
	if !source.Exists() {
		return ""
	}
	switch source.Get("type").String() {
	case "base64":
		mediaType := source.Get("media_type").String()
		data := source.Get("data").String()
		if mediaType == "" || data == "" {
			return ""
		}
		return responsesImageURLStringToChatBlock("data:"+mediaType+";base64,"+data, detail)
	case "url":
		url := source.Get("url").String()
		if url == "" {
			return ""
		}
		return responsesImageURLStringToChatBlock(url, detail)
	default:
		return ""
	}
}

func responsesImageURLStringToChatBlock(url string, detail gjson.Result) string {
	block := `{"type":"image_url","image_url":{}}`
	block, _ = sjson.Set(block, "image_url.url", url)
	return responsesImageChatBlockWithDetail(block, detail)
}

func responsesImageChatBlockWithDetail(block string, detail gjson.Result) string {
	if detail.Exists() && detail.String() != "" {
		block, _ = sjson.Set(block, "image_url.detail", detail.String())
	}
	return block
}

// convertFunctionCallItem 转换 function_call 类型的 item
func convertFunctionCallItem(item gjson.Result, out string) string {
	// function_call → assistant message with tool_calls
	assistantMessage := `{"role":"assistant","tool_calls":[]}`

	toolCall := `{"id":"","type":"function","function":{"name":"","arguments":""}}`

	if callID := item.Get("call_id"); callID.Exists() {
		toolCall, _ = sjson.Set(toolCall, "id", callID.String())
	}

	if name := item.Get("name"); name.Exists() {
		toolCall, _ = sjson.Set(toolCall, "function.name", name.String())
	}

	if arguments := item.Get("arguments"); arguments.Exists() {
		toolCall, _ = sjson.Set(toolCall, "function.arguments", arguments.String())
	}

	assistantMessage, _ = sjson.SetRaw(assistantMessage, "tool_calls.0", toolCall)
	out, _ = sjson.SetRaw(out, "messages.-1", assistantMessage)

	return out
}

// convertFunctionCallOutputItem 转换 function_call_output 类型的 item
func convertFunctionCallOutputItem(item gjson.Result, out string) string {
	// function_call_output → tool message
	toolMessage := `{"role":"tool","tool_call_id":"","content":""}`

	if callID := item.Get("call_id"); callID.Exists() {
		toolMessage, _ = sjson.Set(toolMessage, "tool_call_id", callID.String())
	}

	if output := item.Get("output"); output.Exists() {
		toolMessage, _ = sjson.Set(toolMessage, "content", output.String())
	}

	out, _ = sjson.SetRaw(out, "messages.-1", toolMessage)
	return out
}

// convertToolsToOpenAIFormat 将 Responses tools 转换为 OpenAI Chat Completions tools 格式
//
// 兼容性说明：
//  1. Codex 新版 Responses 的 tool schema 可能省略 `required` 字段，部分严格
//     校验 JSONSchema 的上游镜像会报 "None is not of type 'array'"。
//     转换时统一补齐 `required: []`，并确保 `type: object` 与 `properties: {}`。
//  2. codexToolCompat 开启时，将 custom / web_search / namespace 等 Codex
//     扩展工具映射为 function proxy；tool_search 保留原始参数 schema 映射为 function。
//  3. codexToolCompat 关闭时，Responses 的非 function 工具在 Chat Completions 中
//     没有对应概念，直接跳过，避免触发协议错误。
func convertToolsToOpenAIFormat(tools gjson.Result, out string, codexToolCompat bool) string {
	if codexToolCompat {
		rawTools := make([]interface{}, 0, len(tools.Array()))
		for _, tool := range tools.Array() {
			rawTools = append(rawTools, tool.Value())
		}
		converted := ConvertRawToolsToOpenAI(rawTools)
		if len(converted) == 0 {
			return out
		}

		chatCompletionsTools := make([]interface{}, 0, len(converted))
		for _, tool := range converted {
			chatCompletionsTools = append(chatCompletionsTools, tool)
		}
		out, _ = sjson.Set(out, "tools", chatCompletionsTools)
		return out
	}

	var chatCompletionsTools []interface{}

	tools.ForEach(func(_, tool gjson.Result) bool {
		toolType := tool.Get("type").String()
		// Chat Completions 只支持 function tool，其他类型直接跳过
		if toolType != "" && toolType != "function" {
			return true
		}

		chatTool := `{"type":"function","function":{}}`

		function := `{"name":"","description":"","parameters":{}}`

		// 支持两种写法：{name, parameters} 与 {function: {name, parameters}}
		name := tool.Get("name")
		if !name.Exists() {
			name = tool.Get("function.name")
		}
		description := tool.Get("description")
		if !description.Exists() {
			description = tool.Get("function.description")
		}
		parameters := tool.Get("parameters")
		if !parameters.Exists() {
			parameters = tool.Get("function.parameters")
		}

		if !name.Exists() || name.String() == "" {
			return true
		}
		function, _ = sjson.Set(function, "name", name.String())

		if description.Exists() && description.String() != "" {
			function, _ = sjson.Set(function, "description", description.String())
		}

		function, _ = sjson.SetRaw(function, "parameters", normalizeChatToolParameters(parameters))

		chatTool, _ = sjson.SetRaw(chatTool, "function", function)
		chatCompletionsTools = append(chatCompletionsTools, gjson.Parse(chatTool).Value())

		return true
	})

	if len(chatCompletionsTools) > 0 {
		out, _ = sjson.Set(out, "tools", chatCompletionsTools)
	}

	return out
}

// normalizeChatToolParameters 规范化 tool.parameters，补齐严格校验所需字段。
//
// 针对的上游异常：部分镜像在未提供 `required` 时直接按 None 走校验，
// 抛出 "Invalid schema for function ...: None is not of type 'array'"。
// 通过显式填充 required=[] / type=object / properties={} 规避。
func normalizeChatToolParameters(parameters gjson.Result) string {
	raw := "{}"
	if parameters.Exists() && parameters.IsObject() {
		raw = parameters.Raw
	}

	if !gjson.Get(raw, "type").Exists() {
		raw, _ = sjson.Set(raw, "type", "object")
	}
	if !gjson.Get(raw, "properties").Exists() {
		raw, _ = sjson.SetRaw(raw, "properties", "{}")
	}
	if !gjson.Get(raw, "required").Exists() {
		raw, _ = sjson.SetRaw(raw, "required", "[]")
	}

	return raw
}

// ConvertChatRequestToResponsesRequest 将 OpenAI Chat Completions 请求转换为 Responses 请求格式。
// 用于 chat 渠道 + responses 上游场景：客户端发送 Chat 格式，需转换后调用上游 /v1/responses。
//
// 转换映射:
//   - messages → input（system 提取为 instructions）
//   - max_tokens → max_output_tokens
//   - tools → tools（function 格式保持兼容）
//   - reasoning_effort → reasoning.effort
//   - 透传 temperature, top_p, user, stream, tool_choice, parallel_tool_calls
func ConvertChatRequestToResponsesRequest(chatBody []byte) []byte {
	root := gjson.ParseBytes(chatBody)

	out := `{"model":"","input":[],"stream":false}`

	// model
	if model := root.Get("model"); model.Exists() {
		out, _ = sjson.Set(out, "model", model.String())
	}

	// stream
	if stream := root.Get("stream"); stream.Exists() {
		out, _ = sjson.Set(out, "stream", stream.Bool())
	}

	// system message → instructions
	// 提取 messages 中的 system 角色消息，合并为 instructions
	var instructionsParts []string
	if messages := root.Get("messages"); messages.Exists() && messages.IsArray() {
		messages.ForEach(func(_, msg gjson.Result) bool {
			if msg.Get("role").String() == "system" {
				if content := msg.Get("content"); content.Exists() {
					if content.Type == gjson.String {
						instructionsParts = append(instructionsParts, content.String())
					} else if content.IsArray() {
						// system 消息 content 为数组时，提取所有 text 块
						content.ForEach(func(_, block gjson.Result) bool {
							if text := block.Get("text"); text.Exists() {
								instructionsParts = append(instructionsParts, text.String())
							}
							return true
						})
					}
				}
			}
			return true
		})
	}
	if len(instructionsParts) > 0 {
		out, _ = sjson.Set(out, "instructions", strings.Join(instructionsParts, "\n"))
	}

	// messages → input（跳过 system 消息）
	if messages := root.Get("messages"); messages.Exists() && messages.IsArray() {
		messages.ForEach(func(_, msg gjson.Result) bool {
			role := msg.Get("role").String()
			if role == "system" {
				return true // 已提取到 instructions，跳过
			}

			switch role {
			case "user":
				item := convertChatUserMessageToResponsesItem(msg)
				out, _ = sjson.SetRaw(out, "input.-1", item)

			case "assistant":
				items := convertChatAssistantMessageToResponsesItems(msg)
				for _, item := range items {
					out, _ = sjson.SetRaw(out, "input.-1", item)
				}

			case "tool":
				item := convertChatToolMessageToResponsesItem(msg)
				out, _ = sjson.SetRaw(out, "input.-1", item)
			}

			return true
		})
	}

	// max_tokens / max_completion_tokens → max_output_tokens
	if maxTokens := root.Get("max_tokens"); maxTokens.Exists() {
		out, _ = sjson.Set(out, "max_output_tokens", maxTokens.Int())
	} else if maxCompletionTokens := root.Get("max_completion_tokens"); maxCompletionTokens.Exists() {
		out, _ = sjson.Set(out, "max_output_tokens", maxCompletionTokens.Int())
	}

	// temperature
	if temperature := root.Get("temperature"); temperature.Exists() {
		out, _ = sjson.Set(out, "temperature", temperature.Float())
	}

	// top_p
	if topP := root.Get("top_p"); topP.Exists() {
		out, _ = sjson.Set(out, "top_p", topP.Float())
	}

	// user
	if user := root.Get("user"); user.Exists() {
		out, _ = sjson.Set(out, "user", user.String())
	}

	// tools → tools（Responses 格式简化为 {type,name,parameters}）
	if tools := root.Get("tools"); tools.Exists() && tools.IsArray() {
		var responsesTools []interface{}
		tools.ForEach(func(_, tool gjson.Result) bool {
			toolType := tool.Get("type").String()
			if toolType != "function" {
				return true
			}
			respTool := map[string]interface{}{
				"type": "function",
			}
			if name := tool.Get("function.name"); name.Exists() {
				respTool["name"] = name.String()
			}
			if desc := tool.Get("function.description"); desc.Exists() {
				respTool["description"] = desc.String()
			}
			if params := tool.Get("function.parameters"); params.Exists() {
				respTool["parameters"] = params.Value()
			}
			responsesTools = append(responsesTools, respTool)
			return true
		})
		if len(responsesTools) > 0 {
			out, _ = sjson.Set(out, "tools", responsesTools)
		}
	}

	// tool_choice
	if toolChoice := root.Get("tool_choice"); toolChoice.Exists() {
		out, _ = sjson.SetRaw(out, "tool_choice", toolChoice.Raw)
	}

	// parallel_tool_calls
	if ptc := root.Get("parallel_tool_calls"); ptc.Exists() {
		out, _ = sjson.Set(out, "parallel_tool_calls", ptc.Bool())
	}

	// reasoning_effort → reasoning.effort（保留原始 reasoning 字段）
	if effort := root.Get("reasoning_effort"); effort.Exists() {
		normalizedEffort := effort.String()
		if normalizedEffort == "off" || normalizedEffort == "disabled" {
			normalizedEffort = "none"
		}
		if reasoning := root.Get("reasoning"); reasoning.Exists() && reasoning.IsObject() {
			merged := map[string]interface{}{"effort": normalizedEffort}
			reasoning.ForEach(func(key gjson.Result, value gjson.Result) bool {
				if key.String() != "effort" {
					merged[key.String()] = value.Value()
				}
				return true
			})
			out, _ = sjson.Set(out, "reasoning", merged)
		} else {
			out, _ = sjson.Set(out, "reasoning", map[string]interface{}{"effort": normalizedEffort})
		}
	} else if reasoning := root.Get("reasoning"); reasoning.Exists() {
		out, _ = sjson.Set(out, "reasoning", reasoning.Value())
	}

	return []byte(out)
}

// convertChatUserMessageToResponsesItem 将 Chat user 消息转换为 Responses input item
func convertChatUserMessageToResponsesItem(msg gjson.Result) string {
	item := `{"type":"message","role":"user","content":[]}`

	if content := msg.Get("content"); content.Exists() {
		if content.Type == gjson.String {
			// 简单字符串 content
			block := fmt.Sprintf(`{"type":"input_text","text":%s}`, jsonString(content.String()))
			item, _ = sjson.SetRaw(item, "content.-1", block)
		} else if content.IsArray() {
			// 多模态 content 数组
			content.ForEach(func(_, block gjson.Result) bool {
				blockType := block.Get("type").String()
				switch blockType {
				case "text":
					if text := block.Get("text"); text.Exists() {
						tb := fmt.Sprintf(`{"type":"input_text","text":%s}`, jsonString(text.String()))
						item, _ = sjson.SetRaw(item, "content.-1", tb)
					}
				case "image_url":
					// 图片：转换为 Responses input_image 格式（保留 detail 等字段）
					if imageURL := block.Get("image_url"); imageURL.Exists() && imageURL.IsObject() {
						ib, _ := sjson.SetRaw(`{"type":"input_image","image_url":{}}`, "image_url", imageURL.Raw)
						item, _ = sjson.SetRaw(item, "content.-1", ib)
					} else if imageURL := block.Get("image_url.url"); imageURL.Exists() {
						ib := fmt.Sprintf(`{"type":"input_image","image_url":%s}`, jsonString(imageURL.String()))
						item, _ = sjson.SetRaw(item, "content.-1", ib)
					} else if imageURL := block.Get("image_url"); imageURL.Exists() {
						ib := fmt.Sprintf(`{"type":"input_image","image_url":%s}`, jsonString(imageURL.String()))
						item, _ = sjson.SetRaw(item, "content.-1", ib)
					}
				}
				return true
			})
		}
	}

	return item
}

// convertChatAssistantMessageToResponsesItems 将 Chat assistant 消息转换为 Responses input items。
// assistant 消息可能包含文本内容和 tool_calls，需拆分为多个 item。
func convertChatAssistantMessageToResponsesItems(msg gjson.Result) []string {
	var items []string

	// 文本内容 → message item
	if content := msg.Get("content"); content.Exists() {
		item := `{"type":"message","role":"assistant","content":[]}`
		hasContent := false
		if content.IsArray() {
			content.ForEach(func(_, block gjson.Result) bool {
				blockType := block.Get("type").String()
				switch blockType {
				case "text", "input_text", "output_text":
					if text := block.Get("text"); text.Exists() && text.String() != "" {
						tb := fmt.Sprintf(`{"type":"output_text","text":%s}`, jsonString(text.String()))
						item, _ = sjson.SetRaw(item, "content.-1", tb)
						hasContent = true
					}
				default:
					// 保留非文本内容（如 refusal / 结构化块），避免历史信息丢失
					if block.Exists() {
						item, _ = sjson.SetRaw(item, "content.-1", block.Raw)
						hasContent = true
					}
				}
				return true
			})
		} else if content.String() != "" {
			block := fmt.Sprintf(`{"type":"output_text","text":%s}`, jsonString(content.String()))
			item, _ = sjson.SetRaw(item, "content.-1", block)
			hasContent = true
		}
		if hasContent {
			items = append(items, item)
		}
	}

	// tool_calls → function_call items
	if toolCalls := msg.Get("tool_calls"); toolCalls.Exists() && toolCalls.IsArray() {
		toolCalls.ForEach(func(_, tc gjson.Result) bool {
			callID := tc.Get("id").String()
			name := tc.Get("function.name").String()
			arguments := tc.Get("function.arguments").String()
			if arguments == "" {
				arguments = "{}"
			}
			fcItem := fmt.Sprintf(`{"type":"function_call","call_id":%s,"name":%s,"arguments":%s}`,
				jsonString(callID), jsonString(name), jsonString(arguments))
			items = append(items, fcItem)
			return true
		})
	}

	// 如果没有任何内容，添加空 message 以保持对话结构
	if len(items) == 0 {
		items = append(items, `{"type":"message","role":"assistant","content":[]}`)
	}

	return items
}

// convertChatToolMessageToResponsesItem 将 Chat tool 消息转换为 Responses function_call_output item
func convertChatToolMessageToResponsesItem(msg gjson.Result) string {
	callID := msg.Get("tool_call_id").String()
	output := msg.Get("content").String()

	return fmt.Sprintf(`{"type":"function_call_output","call_id":%s,"output":%s}`,
		jsonString(callID), jsonString(output))
}

// ConvertResponsesResponseToChatResponse 将 Responses 响应转换为 OpenAI Chat Completions 响应。
// 用于 chat 渠道 + responses 上游场景：上游返回 Responses 格式，需转换后返回给客户端。
//
// 转换映射:
//   - output[message] → choices[0].message
//   - output[function_call] → choices[0].message.tool_calls
//   - output[reasoning] → choices[0].message.reasoning_content
//   - usage.input_tokens → usage.prompt_tokens
//   - usage.output_tokens → usage.completion_tokens
func ConvertResponsesResponseToChatResponse(responsesBody []byte, model string) []byte {
	root := gjson.ParseBytes(responsesBody)

	out := `{"id":"","object":"chat.completion","created":0,"model":"","choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"stop"}]}`

	// id
	if id := root.Get("id"); id.Exists() {
		out, _ = sjson.Set(out, "id", id.String())
	}

	// created
	if created := root.Get("created_at"); created.Exists() && created.Int() > 0 {
		out, _ = sjson.Set(out, "created", created.Int())
	} else if created := root.Get("created"); created.Exists() && created.Int() > 0 {
		out, _ = sjson.Set(out, "created", created.Int())
	}

	// model
	if model != "" {
		out, _ = sjson.Set(out, "model", model)
	} else if m := root.Get("model"); m.Exists() {
		out, _ = sjson.Set(out, "model", m.String())
	}

	// 转换 output items → choices
	var textParts []string
	var reasoningParts []string
	var toolCalls []interface{}
	toolCallIndex := 0
	hasFunctionCall := false

	output := root.Get("output")
	if output.Exists() && output.IsArray() {
		output.ForEach(func(_, item gjson.Result) bool {
			itemType := item.Get("type").String()
			switch itemType {
			case "message":
				if content := item.Get("content"); content.Exists() && content.IsArray() {
					content.ForEach(func(_, block gjson.Result) bool {
						blockType := block.Get("type").String()
						if blockType == "output_text" || blockType == "input_text" || blockType == "text" {
							if text := block.Get("text"); text.Exists() && text.String() != "" {
								textParts = append(textParts, text.String())
							}
						}
						return true
					})
				}

			case "reasoning":
				if summary := item.Get("summary"); summary.Exists() && summary.IsArray() {
					summary.ForEach(func(_, s gjson.Result) bool {
						if text := s.Get("text"); text.Exists() && text.String() != "" {
							reasoningParts = append(reasoningParts, text.String())
						}
						return true
					})
				}

			case "function_call":
				hasFunctionCall = true
				callID := item.Get("call_id").String()
				name := item.Get("name").String()
				arguments := item.Get("arguments").String()
				if arguments == "" {
					arguments = "{}"
				}
				toolCalls = append(toolCalls, map[string]interface{}{
					"index": toolCallIndex,
					"id":    callID,
					"type":  "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": arguments,
					},
				})
				toolCallIndex++
			}
			return true
		})
	}

	// 设置 message.content
	if len(textParts) > 0 {
		out, _ = sjson.Set(out, "choices.0.message.content", strings.Join(textParts, "\n"))
	}

	// 设置 message.reasoning_content
	if len(reasoningParts) > 0 {
		out, _ = sjson.Set(out, "choices.0.message.reasoning_content", strings.Join(reasoningParts, "\n"))
	}

	// 设置 message.tool_calls
	if len(toolCalls) > 0 {
		out, _ = sjson.Set(out, "choices.0.message.tool_calls", toolCalls)
	}

	// finish_reason
	if hasFunctionCall {
		out, _ = sjson.Set(out, "choices.0.finish_reason", "tool_calls")
	}

	// status → finish_reason 映射
	if status := root.Get("status"); status.Exists() {
		switch status.String() {
		case "incomplete":
			out, _ = sjson.Set(out, "choices.0.finish_reason", "length")
		}
	}

	// usage
	if usage := root.Get("usage"); usage.Exists() {
		inputTokens := usage.Get("input_tokens").Int()
		outputTokens := usage.Get("output_tokens").Int()
		totalTokens := usage.Get("total_tokens").Int()
		if totalTokens == 0 {
			totalTokens = inputTokens + outputTokens
		}
		out, _ = sjson.Set(out, "usage.prompt_tokens", inputTokens)
		out, _ = sjson.Set(out, "usage.completion_tokens", outputTokens)
		out, _ = sjson.Set(out, "usage.total_tokens", totalTokens)
	}

	return []byte(out)
}

// jsonString 将字符串转为 JSON 编码的带引号字符串。
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
