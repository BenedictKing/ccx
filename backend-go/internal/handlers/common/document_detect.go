package common

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const documentDetectedContextKey = "ccx_has_document_content"

// HasDocumentContentCached 返回已缓存的文档内容检测结果，不触发请求体解析。
func HasDocumentContentCached(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if cached, exists := c.Get(documentDetectedContextKey); exists {
		if detected, ok := cached.(bool); ok {
			return detected
		}
	}
	return false
}

// HasDocumentContent 检测请求体是否包含文档附件（覆盖 Claude/Responses/Gemini 三种协议格式，
// OpenAI Chat 无标准 document 块）。结果缓存在 gin.Context 中，failover 重试时不重复解析。
func HasDocumentContent(c *gin.Context, bodyBytes []byte) bool {
	if cached, exists := c.Get(documentDetectedContextKey); exists {
		return cached.(bool)
	}
	detected := detectDocumentInBody(bodyBytes)
	c.Set(documentDetectedContextKey, detected)
	return detected
}

func detectDocumentInBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	hasDocumentBlock := func(block gjson.Result) bool {
		return block.Get("type").String() == "document" ||
			block.Get("type").String() == "input_file"
	}

	var hasDocumentInContent func(gjson.Result) bool
	hasDocumentInContent = func(content gjson.Result) bool {
		if !content.IsArray() {
			return false
		}
		for _, block := range content.Array() {
			if hasDocumentBlock(block) {
				return true
			}
			// 递归遍历任意深度的 content 嵌套
			// Claude Messages: tool_result.content[*] 可继续嵌套 tool_result → content → document
			if hasDocumentInContent(block.Get("content")) {
				return true
			}
		}
		return false
	}

	// Claude Messages / OpenAI Chat: messages[*].content[*] 可能直接是文档，
	// 也可能在 tool_result.content[*] 等嵌套 content 数组中包含文档。
	messages := gjson.GetBytes(body, "messages")
	if messages.Exists() && messages.IsArray() {
		for _, msg := range messages.Array() {
			if hasDocumentInContent(msg.Get("content")) {
				return true
			}
		}
	}

	// Responses API: input[*].type == "input_file" 或嵌套 content 中的 input_file
	input := gjson.GetBytes(body, "input")
	if input.Exists() && input.IsArray() {
		for _, item := range input.Array() {
			if hasDocumentBlock(item) || hasDocumentInContent(item.Get("content")) {
				return true
			}
		}
	}

	// Gemini: contents[*].parts[*].inlineData 或 fileData，mimeType 非图非音（如 application/pdf）
	contents := gjson.GetBytes(body, "contents")
	if contents.Exists() && contents.IsArray() {
		for _, c := range contents.Array() {
			parts := c.Get("parts")
			if parts.IsArray() {
				for _, part := range parts.Array() {
					if isDocumentMime(part.Get("inlineData").Get("mimeType").String()) ||
						isDocumentMime(part.Get("fileData").Get("mimeType").String()) {
						return true
					}
				}
			}
		}
	}

	return false
}

// isDocumentMime 判断 Gemini 附件 MIME 是否为文档类（非图片、非音频）。
func isDocumentMime(mimeType string) bool {
	if mimeType == "" {
		return false
	}
	return !strings.HasPrefix(mimeType, "image/") && !strings.HasPrefix(mimeType, "audio/")
}
