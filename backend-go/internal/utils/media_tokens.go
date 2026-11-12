package utils

import (
	"github.com/tidwall/gjson"
	"strings"
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
		if strings.HasPrefix(strings.ToLower(mime), "audio/") || format != "" || block.Get("type").String() == "input_audio" {
			count = estimateAudioTokensFromBase64(value, mime, format)
			// 损坏或无法识别的音频保留非零预算，避免按长 base64 文本估算。
			if count <= 0 {
				count = attachmentTokenFallback
			}
		}
		replacements = append(replacements, imageReplacement{start: start, end: end, tokens: count})
		tokens += count
	}
	visitMessages := func(messages gjson.Result) {
		messages.ForEach(func(_, message gjson.Result) bool {
			for _, key := range []string{"content", "parts"} {
				blocks := message.Get(key)
				if blocks.IsArray() {
					blocks.ForEach(func(_, block gjson.Result) bool { visit(block); return true })
				}
			}
			return true
		})
	}
	if root.IsArray() {
		visitMessages(root)
	} else {
		for _, key := range []string{"messages", "input", "contents"} {
			if messages := root.Get(key); messages.IsArray() {
				visitMessages(messages)
				break
			}
		}
	}
	return applyImageReplacements(cleaned, replacements, tokens)
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
		if block.Get("source.type").String() == "base64" {
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
