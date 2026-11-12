package utils

import (
	"bytes"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

const imagePlaceholderJSON = `"<image>"`

// imageBase64Marker 是 OpenAI/Anthropic 图片 schema 的共同特征子串：
//   - OpenAI Chat / Responses 的 data URL 形如 "data:image/...;base64,..."
//   - Anthropic 的 source.type == "base64"
//
// 三者都必然包含 "base64"。但 Gemini 内联图 inlineData.data 是裸 base64（无 data URL
// 前缀、body 里不出现 "base64" 字样），故另用字段名特征 inlineData/inline_data 兜住。
// 这些 marker 是短路的"必要条件"过滤，宁可多扫也绝不漏判任何真实图片请求。
const (
	imageBase64Marker           = "base64"
	geminiInlineDataMarker      = "inlinedata"  // 匹配 inlineData（大小写不敏感）
	geminiInlineDataSnakeMarker = "inline_data" // 匹配 inline_data
)

// bodyMayContainInlineImage 判断 body 是否可能含受支持的内联图片 schema。
// 不含任何特征子串则一定无内联图片，可跳过 gjson 全量解析直接返回原 body。
func bodyMayContainInlineImage(body []byte) bool {
	return containsBase64Fold(body, imageBase64Marker) ||
		containsBase64Fold(body, geminiInlineDataMarker) ||
		containsBase64Fold(body, geminiInlineDataSnakeMarker)
}

// containsBase64Fold 在 body 中做 ASCII 大小写不敏感的子串查找，不分配额外内存
// （避免 bytes.ToLower 复制整个 body 抵消短路收益）。marker 必须为全小写。
func containsBase64Fold(body []byte, marker string) bool {
	n, m := len(body), len(marker)
	if m == 0 {
		return true
	}
	for i := 0; i+m <= n; i++ {
		match := true
		for j := 0; j < m; j++ {
			c := body[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A' // 统一转小写后比较
			}
			if c != marker[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// imagePlaceholder 是占位符的解析后值（不含 JSON 引号），
// 用于识别已被本函数剥离过的字段，保证重复调用的幂等性。
const imagePlaceholder = "<image>"

type imageReplacement struct {
	start  int
	end    int
	tokens int
}

// extractImageTokensAndStripBytes 用 gjson 遍历请求体，按 type 字段区分图片 schema：
//   - OpenAI Chat: content[i].type=="image_url"，base64 在 content[i].image_url.url
//   - Responses:   content[i].type=="input_image"，base64 在 content[i].image_url（字符串）
//   - Anthropic:   content[i].type=="image"，base64 在 content[i].source.data
//
// PDF / audio 等非图 base64 会被忽略，不算图片 token。
// 同时用 gjson Result.Index/Raw 精确定位目标 JSON string literal，并把它替换成
// "<image>"，避免 EstimateTokens 把 base64 长字段按文本字符数高估。
func extractImageTokensAndStripBytes(body []byte) ([]byte, int) {
	// 性能短路：受支持的图片 schema 必含 "base64" 或 Gemini 的 inlineData/inline_data 字段名，
	// 都不含则一定无内联图片，直接返回原 body，省掉一次 gjson 全量解析
	// （高频小包如流式 usage 修补尤其受益）。
	if !bodyMayContainInlineImage(body) {
		return body, 0
	}

	var replacements []imageReplacement
	imageTokens := 0

	// 支持根本身就是消息数组（EstimateMessagesTokens 的输入）
	root := gjson.ParseBytes(body)
	if root.IsArray() {
		replacements, imageTokens = collectImageReplacementsFromMessageArray(body, root)
		return applyImageReplacements(body, replacements, imageTokens)
	}

	// messages（Chat/Anthropic）、input（Responses）、contents（Gemini）是互斥的请求格式，
	// 命中其一即返回，避免畸形请求多者并存时被重复计数。
	// Gemini 的图片在 contents[].parts[] 下，结构与 messages[].content[] 不同，单独遍历。
	if arr := gjson.GetBytes(body, "contents"); arr.IsArray() {
		replacements, imageTokens = collectImageReplacementsFromGeminiContents(body, arr)
		return applyImageReplacements(body, replacements, imageTokens)
	}
	for _, rootPath := range []string{"messages", "input"} {
		arr := gjson.GetBytes(body, rootPath)
		if !arr.IsArray() {
			continue
		}
		replacements, imageTokens = collectImageReplacementsFromMessageArray(body, arr)
		return applyImageReplacements(body, replacements, imageTokens)
	}

	return applyImageReplacements(body, replacements, imageTokens)
}

// collectImageReplacementsFromGeminiContents 遍历 Gemini contents[].parts[]，
// 复用 imagePayloadFromBlock 的 Gemini 分支识别 inlineData/inline_data。
func collectImageReplacementsFromGeminiContents(body []byte, contents gjson.Result) ([]imageReplacement, int) {
	return collectImageReplacementsFromMessageArray(body, contents)
}

func collectImageReplacementsFromMessageArray(body []byte, arr gjson.Result) ([]imageReplacement, int) {
	var replacements []imageReplacement
	imageTokens := 0
	walkMessageMediaBlocks(arr, func(block gjson.Result) {
		b64, field := imagePayloadFromBlock(block)
		if b64 == "" {
			return
		}
		start, end, ok := stringLiteralRange(body, field)
		if !ok {
			return
		}
		tokens := estimateImageTokensFromBase64(b64)
		imageTokens += tokens
		replacements = append(replacements, imageReplacement{start: start, end: end, tokens: tokens})
	})
	return replacements, imageTokens
}

func imagePayloadFromBlock(block gjson.Result) (b64 string, field gjson.Result) {
	switch block.Get("type").String() {
	case "image_url":
		// OpenAI Chat: image_url.url 是 data:image/...;base64,...
		// 已剥离的 "<image>" 占位符不是合法 data URL，dataURLPayload 返回空，天然跳过（幂等）。
		if url := block.Get("image_url.url"); url.Type == gjson.String {
			if b := dataURLPayload(url.String()); b != "" {
				return b, url
			}
		}
	case "input_image":
		// Responses: image_url 直接是 data:image/...;base64,... 字符串
		// 同上，占位符经 dataURLPayload 返回空，重复调用幂等。
		if url := block.Get("image_url"); url.Type == gjson.String {
			if b := dataURLPayload(url.String()); b != "" {
				return b, url
			}
		}
	case "image":
		// Anthropic: 仅 media_type=image/* 的 base64 source 才按图片估算
		if src := block.Get("source"); src.Exists() {
			mediaType := strings.ToLower(src.Get("media_type").String())
			if src.Get("type").String() == "base64" && strings.HasPrefix(mediaType, "image/") {
				if data := src.Get("data"); data.Type == gjson.String {
					// 跳过已被本函数剥离过的占位符，保证对已处理 body 的幂等性
					if b := data.String(); b != "" && b != imagePlaceholder {
						return b, data
					}
				}
			}
		}
	default:
		// Gemini: parts[] 元素无 "type" 字段，图片在 inlineData/inline_data 下，
		// data 是裸 base64，mime 在 mimeType/mime_type。两种大小写变体都认。
		return geminiInlineImagePayload(block)
	}
	return "", gjson.Result{}
}

// geminiInlineImagePayload 从 Gemini part 提取内联 base64 图片。
// 仅 mimeType/mime_type 为 image/* 的 inlineData/inline_data 才算图片，
// 排除音频/视频/PDF 等其它内联数据。占位符经判空跳过，保证幂等。
func geminiInlineImagePayload(block gjson.Result) (b64 string, field gjson.Result) {
	for _, key := range []string{"inlineData", "inline_data"} {
		inline := block.Get(key)
		if !inline.Exists() {
			continue
		}
		mime := strings.ToLower(inline.Get("mimeType").String())
		if mime == "" {
			mime = strings.ToLower(inline.Get("mime_type").String())
		}
		if !strings.HasPrefix(mime, "image/") {
			continue
		}
		if data := inline.Get("data"); data.Type == gjson.String {
			if b := data.String(); b != "" && b != imagePlaceholder {
				return b, data
			}
		}
	}
	return "", gjson.Result{}
}

func stringLiteralRange(body []byte, r gjson.Result) (start, end int, ok bool) {
	if r.Raw == "" || r.Index < 0 {
		return 0, 0, false
	}

	start = r.Index
	end = start + len(r.Raw)
	if start < 0 || start >= len(body) || end > len(body) || start >= end {
		return 0, 0, false
	}
	if len(r.Raw) < 2 || r.Raw[0] != '"' || r.Raw[len(r.Raw)-1] != '"' {
		return 0, 0, false
	}
	if !rawMatches(body[start:end], r.Raw) {
		return 0, 0, false
	}
	return start, end, true
}

func rawMatches(body []byte, raw string) bool {
	if len(body) != len(raw) {
		return false
	}
	for i := range body {
		if body[i] != raw[i] {
			return false
		}
	}
	return true
}

func applyImageReplacements(body []byte, replacements []imageReplacement, imageTokens int) ([]byte, int) {
	if len(replacements) == 0 {
		return body, imageTokens
	}

	kept := normalizeImageReplacements(replacements)
	if len(kept) == 0 {
		return body, imageTokens
	}

	// 单趟拼接：normalizeImageReplacements 已按 start 升序、且保证区间互不重叠。
	// 顺序遍历，把「上一段结尾~本占位符起点」的原文 + 占位符依次写入 buffer，
	// 整体 O(bodySize)。旧实现每个 replacement 都整体拷贝一次 out，是 O(图片数 × bodySize)。
	var buf bytes.Buffer
	buf.Grow(len(body))
	cursor := 0
	for _, repl := range kept {
		buf.Write(body[cursor:repl.start])
		buf.WriteString(imagePlaceholderJSON)
		cursor = repl.end
	}
	buf.Write(body[cursor:])
	return buf.Bytes(), imageTokens
}

func normalizeImageReplacements(replacements []imageReplacement) []imageReplacement {
	valid := replacements[:0]
	for _, repl := range replacements {
		if repl.start < repl.end {
			valid = append(valid, repl)
		}
	}
	if len(valid) == 0 {
		return nil
	}

	sort.Slice(valid, func(i, j int) bool {
		if valid[i].start == valid[j].start {
			return valid[i].end < valid[j].end
		}
		return valid[i].start < valid[j].start
	})

	kept := valid[:0]
	lastEnd := -1
	for _, repl := range valid {
		// 理论上不同 image 字段不应重叠；遇到异常/重复 range 时保守跳过，避免 panic 或错替。
		if repl.start < lastEnd {
			continue
		}
		kept = append(kept, repl)
		lastEnd = repl.end
	}
	return kept
}

// dataURLPayload 从 "data:image/...;base64,xxx" 提取 base64 主体；不是图片 data URL 返回空串。
// 按 RFC 2397，";base64" 必须是逗号前的最后一个分号段，故用 HasSuffix 而非 Contains，
// 避免把 "data:image/x;base64xyz,..." 这类畸形 header 误判为图片。
func dataURLPayload(url string) string {
	comma := strings.IndexByte(url, ',')
	if comma < 0 {
		return ""
	}
	header := strings.ToLower(url[:comma])
	if !strings.HasPrefix(header, "data:image/") || !strings.HasSuffix(header, ";base64") {
		return ""
	}
	return url[comma+1:]
}
