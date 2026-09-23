package common

import (
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const visionDetectedContextKey = "ccx_has_image_content"

// HasImageContentCached 返回已缓存的图片内容检测结果，不触发请求体解析。
func HasImageContentCached(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if cached, exists := c.Get(visionDetectedContextKey); exists {
		if detected, ok := cached.(bool); ok {
			return detected
		}
	}
	return false
}

// HasImageContent 检测请求体是否包含图片内容（覆盖 Claude/OpenAI/Responses/Gemini 四种协议格式）。
// 结果缓存在 gin.Context 中，failover 重试时不重复解析。
func HasImageContent(c *gin.Context, bodyBytes []byte) bool {
	if cached, exists := c.Get(visionDetectedContextKey); exists {
		return cached.(bool)
	}
	detected := detectImageInBody(bodyBytes)
	c.Set(visionDetectedContextKey, detected)
	return detected
}

func detectImageInBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	hasImageBlock := func(block gjson.Result) bool {
		return block.Get("type").String() == "image" ||
			block.Get("type").String() == "image_url" ||
			block.Get("type").String() == "input_image"
	}

	var hasImageInContent func(gjson.Result) bool
	hasImageInContent = func(content gjson.Result) bool {
		if !content.IsArray() {
			return false
		}
		for _, block := range content.Array() {
			if hasImageBlock(block) {
				return true
			}
			// 递归遍历任意深度的 content 嵌套
			// Claude Messages: tool_result.content[*] 可继续嵌套 tool_result → content → image
			if hasImageInContent(block.Get("content")) {
				return true
			}
		}
		return false
	}

	// Claude Messages / OpenAI Chat: messages[*].content[*] 可能直接是图片，
	// 也可能在 tool_result.content[*] 等嵌套 content 数组中包含图片。
	messages := gjson.GetBytes(body, "messages")
	if messages.Exists() && messages.IsArray() {
		for _, msg := range messages.Array() {
			if hasImageInContent(msg.Get("content")) {
				return true
			}
		}
	}

	// Responses API: input[*].type == "input_image" 或嵌套 content 中的 input_image
	input := gjson.GetBytes(body, "input")
	if input.Exists() && input.IsArray() {
		for _, item := range input.Array() {
			if hasImageBlock(item) || hasImageInContent(item.Get("content")) {
				return true
			}
		}
	}

	// Gemini: contents[*].parts[*].inlineData 或 fileData（含 image MIME）
	contents := gjson.GetBytes(body, "contents")
	if contents.Exists() && contents.IsArray() {
		for _, c := range contents.Array() {
			parts := c.Get("parts")
			if parts.IsArray() {
				for _, part := range parts.Array() {
					if part.Get("inlineData").Exists() || part.Get("fileData").Exists() {
						return true
					}
				}
			}
		}
	}

	return false
}
