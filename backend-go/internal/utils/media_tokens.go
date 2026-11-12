package utils

import (
	"github.com/tidwall/gjson"
	"strings"
	"unicode/utf8"
)

// extractMediaTokensAndStripBytes 沿用图片精确定位与单次拼接，额外处理内联音频和附件。
// 只扫描消息内容区域，不会把工具参数中的 data 字符串误当媒体。
func extractMediaTokensAndStripBytes(body []byte) ([]byte, int) {
	cleaned, images := extractImageTokensAndStripBytes(body)
	root := gjson.ParseBytes(cleaned)
	var replacements []imageReplacement
	tokens := images
	visit := func(block gjson.Result) {
		field, mime, format := inlineNonImagePayload(block)
		if field.Type != gjson.String {
			return
		}
		value := field.String()
		if value == "" || value == imagePlaceholder {
			return
		}
		if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
			return
		}
		if strings.HasPrefix(value, "data:") {
			comma := strings.IndexByte(value, ',')
			if comma < 0 || !strings.HasSuffix(strings.ToLower(value[:comma]), ";base64") {
				return
			}
			if mime == "" || mime == "application/octet-stream" || mime == "audio/" {
				mime = strings.TrimSuffix(strings.TrimPrefix(value[:comma], "data:"), ";base64")
			}
			value = value[comma+1:]
		}
		start, end, ok := stringLiteralRange(cleaned, field)
		if !ok {
			return
		}
		count := attachmentTokenFallback
		plainText := block.Get("source.type").String() == "text"
		if plainText || strings.HasPrefix(strings.ToLower(mime), "text/") || mime == "application/json" || mime == "application/xml" {
			text := []byte(value)
			if !plainText {
				text = decodeBase64Prefix(value)
			}
			if text != nil && utf8.Valid(text) {
				if root.Get("input").Exists() {
					count = estimateResponsesTextTokens(string(text))
				} else {
					count = EstimateTokens(string(text))
				}
			}
		} else if strings.HasPrefix(strings.ToLower(mime), "audio/") || format != "" || block.Get("type").String() == "input_audio" {
			count = estimateAudioTokensFromBase64(value, mime, format)
			// 损坏或无法识别的音频保留非零预算，避免按长 base64 文本估算。
			if count <= 0 {
				count = attachmentTokenFallback
			}
		}
		replacements = append(replacements, imageReplacement{start: start, end: end, tokens: count})
		tokens += count
	}
	if root.IsArray() {
		walkMessageMediaBlocks(root, visit)
	} else {
		for _, key := range []string{"messages", "input", "contents"} {
			if messages := root.Get(key); messages.IsArray() {
				walkMessageMediaBlocks(messages, visit)
				break
			}
		}
	}
	return applyImageReplacements(cleaned, replacements, tokens)
}

// walkMessageMediaBlocks 只进入协议规定的内容区域，不扫描工具参数和工具定义。
func walkMessageMediaBlocks(messages gjson.Result, visit func(gjson.Result)) {
	var walkBlocks func(gjson.Result, int)
	walkBlocks = func(blocks gjson.Result, depth int) {
		if !blocks.IsArray() || depth > 16 {
			return
		}
		blocks.ForEach(func(_, block gjson.Result) bool {
			visit(block)
			if block.Get("type").String() == "tool_result" {
				walkBlocks(block.Get("content"), depth+1)
			}
			walkBlocks(block.Get("functionResponse.parts"), depth+1)
			return true
		})
	}
	messages.ForEach(func(_, message gjson.Result) bool {
		walkBlocks(message.Get("content"), 0)
		walkBlocks(message.Get("parts"), 0)
		switch message.Get("type").String() {
		case "function_call_output", "custom_tool_call_output":
			walkBlocks(message.Get("output"), 0)
		}
		return true
	})
}

func inlineNonImagePayload(block gjson.Result) (gjson.Result, string, string) {
	switch block.Get("type").String() {
	case "input_audio":
		field := block.Get("input_audio.data")
		if !field.Exists() {
			field = block.Get("data")
		}
		if !field.Exists() {
			field = block.Get("audio_url")
		}
		return field, "audio/" + block.Get("input_audio.format").String(), block.Get("input_audio.format").String()
	case "audio_url":
		field := block.Get("audio_url.url")
		if strings.HasPrefix(field.String(), "data:") {
			return field, "audio/", ""
		}
	case "document":
		if sourceType := block.Get("source.type").String(); sourceType == "base64" || sourceType == "text" {
			return block.Get("source.data"), block.Get("source.media_type").String(), ""
		}
	case "input_file":
		return block.Get("file_data"), "application/octet-stream", ""
	}
	for _, key := range []string{"inlineData", "inline_data"} {
		inline := block.Get(key)
		mime := inline.Get("mimeType").String()
		if mime == "" {
			mime = inline.Get("mime_type").String()
		}
		if mime != "" && !strings.HasPrefix(strings.ToLower(mime), "image/") {
			return inline.Get("data"), mime, ""
		}
	}
	return gjson.Result{}, "", ""
}
