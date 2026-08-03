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
// audio/attachment 的 marker 与图片并列：它们也是本函数的剥离对象。
// 这些 marker 是短路的"必要条件"过滤，宁可多扫也绝不漏判任何真实媒体请求。
const (
	imageBase64Marker           = "base64"
	geminiInlineDataMarker      = "inlinedata"  // 匹配 inlineData（大小写不敏感）
	geminiInlineDataSnakeMarker = "inline_data" // 匹配 inline_data
	responsesAudioMarker        = "audio_url"   // Responses/Codex: {"type":"input_audio","audio_url":...}
	chatAudioMarker             = "input_audio" // Chat: {"type":"input_audio","input_audio":{"data":...}}
	audioDataURLMarker          = "data:audio/" // 任意 data URL 音频（含未知包装层）
	anthropicPDFMarker          = "application/pdf"
)

// bodyMayContainInlineMedia 判断 body 是否可能含受支持的内联媒体 schema。
// 不含任何特征子串则一定无内联媒体，可跳过 gjson 全量解析直接返回原 body。
// 纯文本请求的逐字节扫描成本与旧版相同（每字节最多几次比较），无性能退化。
func bodyMayContainInlineMedia(body []byte) bool {
	return containsBase64Fold(body, imageBase64Marker) ||
		containsBase64Fold(body, geminiInlineDataMarker) ||
		containsBase64Fold(body, geminiInlineDataSnakeMarker) ||
		containsBase64Fold(body, responsesAudioMarker) ||
		containsBase64Fold(body, chatAudioMarker) ||
		containsBase64Fold(body, audioDataURLMarker) ||
		containsBase64Fold(body, anthropicPDFMarker)
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

// imagePlaceholder / mediaPlaceholder 是占位符的解析后值（不含 JSON 引号），
// 用于识别已被本函数剥离过的字段，保证重复调用的幂等性。
// 图片与音频/附件分开占位，便于测试区分剥离来源。
const (
	imagePlaceholder = "<image>"
	mediaPlaceholder = "<media>"
)

const mediaPlaceholderJSON = `"<media>"`

type imageReplacement struct {
	start  int
	end    int
	tokens int
	// placeholderJSON 是替换写入的占位符字面量（含引号）。
	// 图片为 "<image>"，音频/附件为 "<media>"；空值按图片处理，兼容既有调用。
	placeholderJSON string
}

// extractImageTokensAndStripBytes 用 gjson 遍历请求体，剥离内联媒体并估算 token：
//
// 图片（按真实尺寸，Qwen3-VL 上界）：
//   - OpenAI Chat: content[i].type=="image_url"，base64 在 content[i].image_url.url
//   - Responses:   content[i].type=="input_image"，base64 在 content[i].image_url（字符串）
//   - Anthropic:   content[i].type=="image"，base64 在 content[i].source.data
//
// 音频（按真实时长 × 32 tokens/s 保守上界，见 audio_tokens.go）：
//   - Responses/Codex: content[i].type=="input_audio"，data URL 在 content[i].audio_url
//   - OpenAI Chat:     content[i].type=="input_audio"，裸 base64 在 content[i].input_audio.data
//   - Gemini:          contents[].parts[].inlineData（mimeType=audio/*）
//
// 其他附件（固定 attachmentTokenFallback）：
//   - Anthropic:  content[i].type=="document"（如 PDF）
//   - Responses:  content[i].type=="input_file"（file_data data URL）
//   - Gemini:     inlineData 非图非音 mime（如 application/pdf）
//
// 返回值 int 是所有媒体 token 之和（历史命名为 imageTokens，语义已扩展为 mediaTokens）。
// 用 gjson Result.Index/Raw 精确定位目标 JSON string literal，并替换成占位符，
// 避免 EstimateTokens 把 base64 长字段按文本字符数高估。
func extractImageTokensAndStripBytes(body []byte) ([]byte, int) {
	// 性能短路：受支持的媒体 schema 必含上述 marker 之一，
	// 都不含则一定无内联媒体，直接返回原 body，省掉一次 gjson 全量解析
	// （高频小包如流式 usage 修补尤其受益）。
	if !bodyMayContainInlineMedia(body) {
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
// 复用 mediaPayloadFromBlock 的 Gemini 分支识别 inlineData/inline_data。
func collectImageReplacementsFromGeminiContents(body []byte, contents gjson.Result) ([]imageReplacement, int) {
	var replacements []imageReplacement
	mediaTokens := 0

	contents.ForEach(func(_, content gjson.Result) bool {
		parts := content.Get("parts")
		if !parts.IsArray() {
			return true
		}
		parts.ForEach(func(_, part gjson.Result) bool {
			payload := mediaPayloadFromBlock(part)
			if payload.b64 == "" {
				return true
			}
			start, end, ok := stringLiteralRange(body, payload.field)
			if !ok {
				return true
			}
			tokens := estimateMediaTokens(payload)
			mediaTokens += tokens
			replacements = append(replacements, imageReplacement{start: start, end: end, tokens: tokens, placeholderJSON: payload.placeholderJSON()})
			return true
		})
		return true
	})

	return replacements, mediaTokens
}

func collectImageReplacementsFromMessageArray(body []byte, arr gjson.Result) ([]imageReplacement, int) {
	var replacements []imageReplacement
	mediaTokens := 0

	arr.ForEach(func(_, msg gjson.Result) bool {
		content := msg.Get("content")
		if !content.IsArray() {
			return true
		}
		content.ForEach(func(_, block gjson.Result) bool {
			payload := mediaPayloadFromBlock(block)
			if payload.b64 == "" {
				return true
			}
			start, end, ok := stringLiteralRange(body, payload.field)
			if !ok {
				// 定位失败时不剥离也不计 token：若只计 token 而 base64 仍留在 body 中，
				// EstimateTokens 会把它按字符数再算一遍，反而退回到本次修复要解决的高估问题。
				return true
			}
			tokens := estimateMediaTokens(payload)
			mediaTokens += tokens
			replacements = append(replacements, imageReplacement{start: start, end: end, tokens: tokens, placeholderJSON: payload.placeholderJSON()})
			return true
		})
		return true
	})

	return replacements, mediaTokens
}

// mediaKind 区分剥离对象类别，决定 token 估算方式与占位符。
type mediaKind int

const (
	mediaKindNone mediaKind = iota
	mediaKindImage
	mediaKindAudio
	mediaKindAttachment
)

// mediaPayload 是从一个 content block 提取出的可剥离载荷。
type mediaPayload struct {
	kind     mediaKind
	b64      string       // 待剥离的 base64 主体（裸 base64 或 data URL 载荷段）
	field    gjson.Result // b64 在 body 中对应的字符串字段，用于精确替换
	mime     string       // data URL header / inlineData.mimeType（可空）
	format   string       // Chat input_audio.format 等外部格式提示（可空）
	stripAll bool         // true 时替换整个字段值（data URL 场景）；false 仅替换 base64 段
}

// placeholderJSON 返回该载荷剥离后写入的占位符字面量。
func (p mediaPayload) placeholderJSON() string {
	if p.kind == mediaKindImage {
		return imagePlaceholderJSON
	}
	return mediaPlaceholderJSON
}

// estimateMediaTokens 按类别估算单个媒体载荷的 token。
func estimateMediaTokens(p mediaPayload) int {
	switch p.kind {
	case mediaKindImage:
		return estimateImageTokensFromBase64(p.b64)
	case mediaKindAudio:
		return estimateAudioTokensFromBase64(p.b64, p.mime, p.format)
	case mediaKindAttachment:
		return attachmentTokenFallback
	default:
		return 0
	}
}

// mediaPayloadFromBlock 识别 content block 中的内联媒体载荷（图片/音频/附件）。
// 已剥离占位符返回空 b64，保证幂等。远程 URL（http/https）不剥离、计 0——
// 字节不随请求体传输，上游抓取失败的成本不属于本请求的上下文占用。
func mediaPayloadFromBlock(block gjson.Result) mediaPayload {
	switch block.Get("type").String() {
	case "image_url":
		// OpenAI Chat: image_url.url 是 data:image/...;base64,...
		// 已剥离的 "<image>" 占位符不是合法 data URL，dataURLPayload 返回空，天然跳过（幂等）。
		if url := block.Get("image_url.url"); url.Type == gjson.String {
			if b, mime := imageDataURLPayload(url.String()); b != "" {
				return mediaPayload{kind: mediaKindImage, b64: b, field: url, mime: mime, stripAll: true}
			}
		}
	case "input_image":
		// Responses: image_url 直接是 data:image/...;base64,... 字符串
		// 同上，占位符经 imageDataURLPayload 返回空，重复调用幂等。
		if url := block.Get("image_url"); url.Type == gjson.String {
			if b, mime := imageDataURLPayload(url.String()); b != "" {
				return mediaPayload{kind: mediaKindImage, b64: b, field: url, mime: mime, stripAll: true}
			}
		}
	case "input_audio":
		// Responses/Codex 0.145+: {"type":"input_audio","audio_url":"data:audio/wav;base64,..."}
		if url := block.Get("audio_url"); url.Type == gjson.String {
			if b, mime := audioDataURLPayload(url.String()); b != "" {
				return mediaPayload{kind: mediaKindAudio, b64: b, field: url, mime: mime, stripAll: true}
			}
		}
		// OpenAI Chat: {"type":"input_audio","input_audio":{"data":"<raw b64>","format":"mp3"}}
		if inner := block.Get("input_audio"); inner.Exists() {
			if data := inner.Get("data"); data.Type == gjson.String {
				if b := data.String(); b != "" && b != mediaPlaceholder && b != imagePlaceholder && !isHTTPURL(b) {
					return mediaPayload{kind: mediaKindAudio, b64: b, field: data, format: inner.Get("format").String(), stripAll: true}
				}
			}
		}
	case "input_file":
		// Responses: {"type":"input_file","file_data":"data:application/pdf;base64,..."}
		if fd := block.Get("file_data"); fd.Type == gjson.String {
			if b := anyDataURLPayload(fd.String()); b != "" {
				return mediaPayload{kind: mediaKindAttachment, b64: b, field: fd, stripAll: true}
			}
		}
	case "image":
		// Anthropic: 仅 media_type=image/* 的 base64 source 才按图片估算
		if src := block.Get("source"); src.Exists() {
			mediaType := strings.ToLower(src.Get("media_type").String())
			if src.Get("type").String() == "base64" && strings.HasPrefix(mediaType, "image/") {
				if data := src.Get("data"); data.Type == gjson.String {
					if b := data.String(); b != "" && b != imagePlaceholder && b != mediaPlaceholder {
						return mediaPayload{kind: mediaKindImage, b64: b, field: data, mime: mediaType, stripAll: true}
					}
				}
			}
		}
	case "document":
		// Anthropic: PDF 等文档附件，固定保守估算
		if src := block.Get("source"); src.Exists() {
			if src.Get("type").String() == "base64" {
				if data := src.Get("data"); data.Type == gjson.String {
					if b := data.String(); b != "" && b != mediaPlaceholder && b != imagePlaceholder {
						return mediaPayload{kind: mediaKindAttachment, b64: b, field: data, mime: strings.ToLower(src.Get("media_type").String()), stripAll: true}
					}
				}
			}
		}
	default:
		// Gemini: parts[] 元素无 "type" 字段，内联数据在 inlineData/inline_data 下，
		// data 是裸 base64，mime 在 mimeType/mime_type。两种大小写变体都认。
		return geminiInlineMediaPayload(block)
	}
	return mediaPayload{}
}

// geminiInlineMediaPayload 从 Gemini part 提取内联 base64 媒体。
// image/* 按图片、audio/* 按音频、其余 mime（pdf/video 等）按通用附件。
// 占位符经判空跳过，保证幂等。
func geminiInlineMediaPayload(block gjson.Result) mediaPayload {
	for _, key := range []string{"inlineData", "inline_data"} {
		inline := block.Get(key)
		if !inline.Exists() {
			continue
		}
		mime := strings.ToLower(inline.Get("mimeType").String())
		if mime == "" {
			mime = strings.ToLower(inline.Get("mime_type").String())
		}
		data := inline.Get("data")
		if data.Type != gjson.String {
			continue
		}
		b := data.String()
		if b == "" || b == imagePlaceholder || b == mediaPlaceholder {
			continue
		}
		switch {
		case strings.HasPrefix(mime, "image/"):
			return mediaPayload{kind: mediaKindImage, b64: b, field: data, mime: mime, stripAll: true}
		case strings.HasPrefix(mime, "audio/"):
			return mediaPayload{kind: mediaKindAudio, b64: b, field: data, mime: mime, stripAll: true}
		case mime != "":
			return mediaPayload{kind: mediaKindAttachment, b64: b, field: data, mime: mime, stripAll: true}
		}
	}
	return mediaPayload{}
}

// isHTTPURL 判断字符串是否为远程 URL（http/https），此类引用不产生请求体字节占用。
func isHTTPURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
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

func applyImageReplacements(body []byte, replacements []imageReplacement, mediaTokens int) ([]byte, int) {
	if len(replacements) == 0 {
		return body, mediaTokens
	}

	kept := normalizeImageReplacements(replacements)
	if len(kept) == 0 {
		return body, mediaTokens
	}

	// 单趟拼接：normalizeImageReplacements 已按 start 升序、且保证区间互不重叠。
	// 顺序遍历，把「上一段结尾~本占位符起点」的原文 + 占位符依次写入 buffer，
	// 整体 O(bodySize)。旧实现每个 replacement 都整体拷贝一次 out，是 O(图片数 × bodySize)。
	var buf bytes.Buffer
	buf.Grow(len(body))
	cursor := 0
	for _, repl := range kept {
		buf.Write(body[cursor:repl.start])
		placeholder := repl.placeholderJSON
		if placeholder == "" {
			placeholder = imagePlaceholderJSON
		}
		buf.WriteString(placeholder)
		cursor = repl.end
	}
	buf.Write(body[cursor:])
	return buf.Bytes(), mediaTokens
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

// imageDataURLPayload 从 "data:image/...;base64,xxx" 提取 base64 主体；不是图片 data URL 返回空串。
// 按 RFC 2397，";base64" 必须是逗号前的最后一个分号段，故用 HasSuffix 而非 Contains，
// 避免把 "data:image/x;base64xyz,..." 这类畸形 header 误判为图片。
func imageDataURLPayload(url string) (b64, mime string) {
	b64, mime = splitDataURL(url)
	if !strings.HasPrefix(mime, "image/") {
		return "", ""
	}
	return b64, mime
}

// audioDataURLPayload 从 "data:audio/...;base64,xxx" 提取 base64 主体与 mime。
func audioDataURLPayload(url string) (b64, mime string) {
	b64, mime = splitDataURL(url)
	if !strings.HasPrefix(mime, "audio/") {
		return "", ""
	}
	return b64, mime
}

// anyDataURLPayload 提取任意 data URL 的 base64 主体（附件场景，不校验 mime 前缀）。
func anyDataURLPayload(url string) string {
	b64, _ := splitDataURL(url)
	return b64
}

// splitDataURL 按 RFC 2397 解析 data URL，返回 base64 主体与小写 mime。
// 要求 header 以 ";base64" 结尾（逗号前最后一个分号段），非 base64 编码返回空。
func splitDataURL(url string) (b64, mime string) {
	comma := strings.IndexByte(url, ',')
	if comma < 0 {
		return "", ""
	}
	header := strings.ToLower(url[:comma])
	if !strings.HasPrefix(header, "data:") || !strings.HasSuffix(header, ";base64") {
		return "", ""
	}
	mime = strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	// 已剥离占位符（"<image>"/"<media>"）不会以 "data:" 开头，天然跳过（幂等）。
	return url[comma+1:], mime
}
