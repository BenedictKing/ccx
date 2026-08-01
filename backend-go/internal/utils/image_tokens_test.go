package utils

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestEstimateImageTokensFromSize(t *testing.T) {
	tests := []struct {
		name       string
		h, w, want int
	}{
		// 真实样本（用户实测）
		{"wechat_screenshot_1988x990", 1988, 990, 2485},
		{"ai_art_2048x3072", 2048, 3072, 8030},

		// 常见标准尺寸
		{"square_512", 512, 512, 324},
		{"square_1024", 1024, 1024, 1369},
		{"square_2048", 2048, 2048, 5329},
		{"hd_landscape_1920x1080", 1080, 1920, 2691},
		{"hd_portrait_1080x1920", 1920, 1080, 2691},
		{"uhd_4k_3840x2160", 2160, 3840, 10549},

		// 边界
		{"tiny_28x28", 28, 28, 4},
		{"subpatch_14x14", 14, 14, 4},
		{"thumbnail_100x100", 100, 100, 16},
		{"max_8192x8192", 8192, 8192, 16384},
		{"huge_16384x16384", 16384, 16384, 16384},

		// 极端长宽比
		{"wide_panorama_500x4000", 500, 4000, 2574},
		{"tall_screenshot_4000x750", 4000, 750, 3861},

		// 病态输入 → fallback
		{"aspect_ratio_overflow", 40, 10000, imageTokenFallback},
		{"zero_height", 0, 100, imageTokenFallback},
		{"zero_width", 100, 0, imageTokenFallback},
		{"both_zero", 0, 0, imageTokenFallback},
		{"negative", -1, 100, imageTokenFallback},

		// 超大尺寸（改进1：int64 乘积防 32 位平台溢出 + 单边上限挡住绝对异常值）
		{"huge_square_50000", 50000, 50000, imageMaxTokenNum},                // 长宽比=1 绕过比例检查，int64 正确缩放后钳到上界
		{"at_dimension_limit_still_valid", 100000, 100000, imageMaxTokenNum}, // 恰好等于上限仍按正常算
		{"over_dimension_height", 100001, 100000, imageTokenFallback},        // 单边超上限、长宽比≈1，纯靠维度守卫拦截
		{"over_dimension_width", 100000, 100001, imageTokenFallback},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateImageTokensFromSize(tt.h, tt.w)
			if got != tt.want {
				t.Errorf("estimateImageTokensFromSize(%d, %d) = %d, want %d",
					tt.h, tt.w, got, tt.want)
			}
		})
	}
}

// makePNG/JPEG/GIF 生成测试用图片的 base64
func makePNG(w, h int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func makeJPEG(w, h, q int) string {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: q})
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func makeGIF(w, h int) string {
	pal := color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 255, 0, 255}}
	img := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	var buf bytes.Buffer
	_ = gif.Encode(&buf, img, nil)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// makeNoisePNG 生成「不可压缩」的随机像素 PNG 的 base64。
// 与纯色填充的 makePNG 不同：随机噪声让 PNG 几乎压不动，base64 体积巨大
// （2048x2048 约百万级字符），从而「按真实尺寸计图」与「按整个 body 字符高估」
// 的 token 差异极大——这是端到端测试能真正区分两者、抓住提取失效回归的前提。
// 用固定随机种子保证可复现。注意：别改现有 makePNG，其它测试依赖它的纯色行为。
func makeNoisePNG(w, h int) string {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{
				uint8(rng.Intn(256)),
				uint8(rng.Intn(256)),
				uint8(rng.Intn(256)),
				255,
			})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestDecodeImageSizeFromBase64(t *testing.T) {
	tests := []struct {
		name         string
		b64          string
		wantH, wantW int
		wantOK       bool
	}{
		// PNG
		{"png_100x200", makePNG(100, 200), 200, 100, true},
		{"png_512x512", makePNG(512, 512), 512, 512, true},
		{"png_1x1", makePNG(1, 1), 1, 1, true},
		{"png_2048x1024", makePNG(2048, 1024), 1024, 2048, true},

		// JPEG 不同质量
		{"jpeg_q90_1024x768", makeJPEG(1024, 768, 90), 768, 1024, true},
		{"jpeg_q50_1024x768", makeJPEG(1024, 768, 50), 768, 1024, true},
		{"jpeg_q10_1024x768", makeJPEG(1024, 768, 10), 768, 1024, true},
		{"jpeg_320x240", makeJPEG(320, 240, 75), 240, 320, true},

		// GIF
		{"gif_100x100", makeGIF(100, 100), 100, 100, true},
		{"gif_500x300", makeGIF(500, 300), 300, 500, true},

		// 失败场景
		{"garbage", "this-is-not-base64!!!", 0, 0, false},
		{"empty", "", 0, 0, false},
		{"whitespace", "  \n\t  ", 0, 0, false},
		{"non_image_base64", base64.StdEncoding.EncodeToString([]byte("hello world")), 0, 0, false},
		{"truncated_header", base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4E, 0x47}), 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, w, ok := decodeImageSizeFromBase64(tt.b64)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v (h=%d w=%d)", ok, tt.wantOK, h, w)
				return
			}
			if ok && (h != tt.wantH || w != tt.wantW) {
				t.Errorf("size = %dx%d, want %dx%d", h, w, tt.wantH, tt.wantW)
			}
		})
	}
}

// 验证大图也只用头部 8KB 即可读到尺寸
func TestDecodeImageSizeFromBase64_LargeImage(t *testing.T) {
	b64 := makeJPEG(2048, 2048, 90)
	if len(b64) < imageHeaderSniffBytes*2 {
		t.Skip("test image not large enough")
	}
	h, w, ok := decodeImageSizeFromBase64(b64)
	if !ok || h != 2048 || w != 2048 {
		t.Errorf("got h=%d w=%d ok=%v, want 2048x2048 ok=true", h, w, ok)
	}
}

func TestExtractImageTokensAndStripBytes(t *testing.T) {
	png1k := makePNG(1024, 1024)      // 1369 token
	png512 := makePNG(512, 512)       // 324 token
	jpeg768 := makeJPEG(768, 768, 80) // 729 token
	nonImageData := strings.Repeat("A", 500)

	tests := []struct {
		name             string
		body             string
		wantTokens       int
		wantStripped     bool // true 表示 cleaned 应该不包含原 base64
		wantPlaceholder  []string
		wantPreserved    []string
		wantValidCleaned bool
	}{
		{
			name: "openai_data_url",
			body: fmt.Sprintf(
				`{"messages":[{"role":"user","metadata":"keep-me","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}`,
				png1k),
			wantTokens:       1369,
			wantStripped:     true,
			wantPlaceholder:  []string{"messages.0.content.0.image_url.url"},
			wantPreserved:    []string{"messages.0.metadata=keep-me"},
			wantValidCleaned: true,
		},
		{
			name: "openai_data_url_case_insensitive_header",
			body: fmt.Sprintf(
				`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:IMAGE/png;BASE64,%s"}}]}]}`,
				png512),
			wantTokens:       324,
			wantStripped:     true,
			wantPlaceholder:  []string{"messages.0.content.0.image_url.url"},
			wantValidCleaned: true,
		},
		{
			name: "responses_input_image_string",
			body: fmt.Sprintf(
				`{"input":[{"role":"user","metadata":{"trace":"abc"},"content":[{"type":"input_image","image_url":"data:image/png;base64,%s"}]}]}`,
				png1k),
			wantTokens:       1369,
			wantStripped:     true,
			wantPlaceholder:  []string{"input.0.content.0.image_url"},
			wantPreserved:    []string{"input.0.metadata.trace=abc"},
			wantValidCleaned: true,
		},
		{
			name: "responses_input_image_case_insensitive_header",
			body: fmt.Sprintf(
				`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;BASE64,%s"}]}]}`,
				png512),
			wantTokens:       324,
			wantStripped:     true,
			wantPlaceholder:  []string{"input.0.content.0.image_url"},
			wantValidCleaned: true,
		},
		{
			name: "anthropic_image_source",
			body: fmt.Sprintf(
				`{"messages":[{"role":"user","metadata":{"keep":"yes"},"content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"%s"}}]}]}`,
				jpeg768),
			wantTokens:       729,
			wantStripped:     true,
			wantPlaceholder:  []string{"messages.0.content.0.source.data"},
			wantPreserved:    []string{"messages.0.metadata.keep=yes", "messages.0.content.0.source.media_type=image/jpeg"},
			wantValidCleaned: true,
		},
		{
			name: "anthropic_image_source_case_insensitive_media_type",
			body: fmt.Sprintf(
				`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"Image/PNG","data":"%s"}}]}]}`,
				png512),
			wantTokens:       324,
			wantStripped:     true,
			wantPlaceholder:  []string{"messages.0.content.0.source.data"},
			wantPreserved:    []string{"messages.0.content.0.source.media_type=Image/PNG"},
			wantValidCleaned: true,
		},
		{
			name: "anthropic_document_attachment_fallback",
			body: fmt.Sprintf(
				`{"messages":[{"role":"user","metadata":"keep-doc","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"%s"}}]}]}`,
				nonImageData),
			// type=document 自媒体泛化修复起按通用附件剥离并给固定保守估算，
			// 防止 1MB+ PDF 被 base64-as-text 估成几十万 token 撞穿渠道窗口。
			wantTokens:       attachmentTokenFallback,
			wantStripped:     true,
			wantPreserved:    []string{"messages.0.metadata=keep-doc"},
			wantValidCleaned: true,
		},
		{
			name: "image_url_non_image_data_url_not_counted",
			body: fmt.Sprintf(
				`{"messages":[{"role":"user","metadata":"keep-pdf","content":[{"type":"image_url","image_url":{"url":"data:application/pdf;base64,%s"}}]}]}`,
				nonImageData),
			wantTokens:       0,
			wantStripped:     false,
			wantPreserved:    []string{"messages.0.metadata=keep-pdf", "messages.0.content.0.image_url.url=data:application/pdf;base64," + nonImageData},
			wantValidCleaned: true,
		},
		{
			name: "anthropic_image_non_image_media_type_not_counted",
			body: fmt.Sprintf(
				`{"messages":[{"role":"user","metadata":"keep-pdf","content":[{"type":"image","source":{"type":"base64","media_type":"application/pdf","data":"%s"}}]}]}`,
				nonImageData),
			wantTokens:       0,
			wantStripped:     false,
			wantPreserved:    []string{"messages.0.metadata=keep-pdf", "messages.0.content.0.source.data=" + nonImageData},
			wantValidCleaned: true,
		},
		{
			name: "multiple_images_sum",
			body: fmt.Sprintf(
				`{"messages":[{"role":"user","metadata":"keep-multi","content":[`+
					`{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}},`+
					`{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}`,
				png512, png1k),
			wantTokens:       324 + 1369,
			wantStripped:     true,
			wantPlaceholder:  []string{"messages.0.content.0.image_url.url", "messages.0.content.1.image_url.url"},
			wantPreserved:    []string{"messages.0.metadata=keep-multi"},
			wantValidCleaned: true,
		},
		{
			name:         "no_images_unchanged",
			body:         `{"messages":[{"role":"user","metadata":"keep-text","content":"plain text"}]}`,
			wantTokens:   0,
			wantStripped: false,
			wantPreserved: []string{
				"messages.0.metadata=keep-text",
				"messages.0.content=plain text",
			},
			wantValidCleaned: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, tokens := extractImageTokensAndStripBytes([]byte(tt.body))
			if tokens != tt.wantTokens {
				t.Errorf("tokens = %d, want %d", tokens, tt.wantTokens)
			}
			if tt.wantStripped {
				// cleaned 长度应该明显小于原 body（base64 被替换为 "<image>"）
				if len(cleaned) >= len(tt.body) {
					t.Errorf("cleaned not stripped: %d vs original %d", len(cleaned), len(tt.body))
				}
			}
			if tt.wantValidCleaned {
				assertCleanedJSON(t, cleaned, tt.wantPlaceholder, tt.wantPreserved)
			}
		})
	}
}

func assertCleanedJSON(t *testing.T, cleaned []byte, placeholderPaths, preservedChecks []string) {
	t.Helper()

	if !json.Valid(cleaned) {
		t.Fatalf("cleaned body is not valid JSON: %s", string(cleaned))
	}
	parsed := gjson.ParseBytes(cleaned)
	for _, path := range placeholderPaths {
		if got := parsed.Get(path).String(); got != "<image>" {
			t.Errorf("cleaned %s = %q, want <image>", path, got)
		}
	}
	for _, check := range preservedChecks {
		path, want, ok := strings.Cut(check, "=")
		if !ok {
			t.Fatalf("bad preserved check %q, want path=value", check)
		}
		if got := parsed.Get(path).String(); got != want {
			t.Errorf("cleaned %s = %q, want preserved %q", path, got, want)
		}
	}
}

// 定位失败（byte-range 校验不通过）时，必须既不计 token 也不剥离：
// 若只计 token 而 base64 仍留在 body 里，EstimateTokens 会把它按字符数再算一遍，
// token 与残留文本叠加反而可能重新撞穿 unknownSafeWindow，退回本次修复要解决的问题。
func TestCollectImageReplacements_RangeFailureSkipsBoth(t *testing.T) {
	b64 := makePNG(512, 512) // 324 token
	body := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}`,
		b64))
	arr := gjson.GetBytes(body, "messages")
	if !arr.IsArray() {
		t.Fatal("test setup failed: messages is not an array")
	}

	// 用一个对不上的 body 触发 stringLiteralRange 校验失败：字段能被识别为图片，
	// 但其 Index/Raw 在该 body 中找不到对应字节。
	mismatchedBody := []byte(`{"messages":[{"role":"user","content":[]}]}`)
	replacements, tokens := collectImageReplacementsFromMessageArray(mismatchedBody, arr)
	if tokens != 0 {
		t.Fatalf("tokens = %d, want 0 (定位失败不应计 token)", tokens)
	}
	if len(replacements) != 0 {
		t.Fatalf("replacements = %d, want 0", len(replacements))
	}
	cleaned, appliedTokens := applyImageReplacements(mismatchedBody, replacements, tokens)
	if appliedTokens != 0 {
		t.Fatalf("applied tokens = %d, want 0", appliedTokens)
	}
	if !bytes.Equal(cleaned, mismatchedBody) {
		t.Fatalf("cleaned body changed despite invalid range: %s", string(cleaned))
	}
}

func TestEstimateMessagesTokens_WithImageArrayRoot(t *testing.T) {
	b64 := makePNG(1024, 1024) // 1369 token
	messages := []interface{}{
		map[string]interface{}{
			"role": "user",
			"content": []interface{}{
				map[string]interface{}{
					"type": "image_url",
					"image_url": map[string]interface{}{
						"url": "data:image/png;base64," + b64,
					},
				},
			},
		},
	}
	got := EstimateMessagesTokens(messages)
	if got < 1369 || got > 1600 {
		t.Errorf("EstimateMessagesTokens = %d, want ~1369-1600", got)
	}
}

func TestExtractImageTokensAndStripBytes_DoesNotReplaceSameTextElsewhere(t *testing.T) {
	b64 := makePNG(512, 512) // 324 token
	body := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","metadata":"%s","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}`,
		b64, b64))
	cleaned, tokens := extractImageTokensAndStripBytes(body)
	if tokens != 324 {
		t.Errorf("tokens = %d, want 324", tokens)
	}
	// 按 gjson Result.Index/Raw 的 byte range 替换，只应替换 image_url.url，metadata 中相同字符串应保留。
	if !strings.Contains(string(cleaned), `"metadata":"`+b64+`"`) {
		t.Errorf("metadata base64 was unexpectedly replaced")
	}
	if strings.Contains(string(cleaned), `"url":"data:image/png;base64,`+b64+`"`) {
		t.Errorf("image_url base64 was not replaced")
	}
}

func TestExtractImageTokensAndStripBytes_EscapedSlashDataURL(t *testing.T) {
	b64 := makePNG(512, 512) // 324 token
	body := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image\/png;base64,%s"}}]}]}`,
		b64))

	cleaned, tokens := extractImageTokensAndStripBytes(body)
	if tokens != 324 {
		t.Fatalf("tokens = %d, want 324", tokens)
	}
	if strings.Contains(string(cleaned), `data:image\/png;base64,`) || strings.Contains(string(cleaned), b64) {
		t.Fatalf("escaped data URL was not stripped: %s", string(cleaned))
	}
	if !strings.Contains(string(cleaned), `"url":"<image>"`) {
		t.Fatalf("cleaned body missing image placeholder: %s", string(cleaned))
	}
}

func TestExtractImageTokensAndStripBytes_TwoIdenticalImages(t *testing.T) {
	b64 := makePNG(512, 512) // 324 token
	body := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":[`+
			`{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}},`+
			`{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}`,
		b64, b64))

	cleaned, tokens := extractImageTokensAndStripBytes(body)
	if tokens != 648 {
		t.Fatalf("tokens = %d, want 648", tokens)
	}
	if strings.Count(string(cleaned), `"url":"<image>"`) != 2 {
		t.Fatalf("got cleaned body %s, want two image placeholders", string(cleaned))
	}
	if strings.Contains(string(cleaned), b64) {
		t.Fatalf("identical image base64 was not fully stripped")
	}
}

// TestExtractImageTokensAndStripBytes_NoImageShortCircuit 验证改进3的性能短路：
// body 不含 "base64" 子串时直接原样返回、0 token，且结果与未短路时完全一致
// （含图请求不受影响，由上面的图片用例覆盖）。
func TestExtractImageTokensAndStripBytes_NoImageShortCircuit(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"plain_chat", `{"messages":[{"role":"user","content":"hello world"}]}`},
		{"chinese_text", `{"messages":[{"role":"user","content":"你好，世界"}]}`},
		{"empty_messages", `{"messages":[]}`},
		{"responses_text_input", `{"input":"just some text","instructions":"be brief"}`},
		{"tools_only", `{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}]}`},
		// 含 image_url 字段名但不是真正的内联 base64 图片（远程 URL），也应短路
		{"remote_image_url", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/cat.png"}}]}]}`},
		// 小写 b 但不构成 "base64"，确认不会误判进入解析
		{"base_word_not_base64", `{"messages":[{"role":"user","content":"the base camp data"}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, tokens := extractImageTokensAndStripBytes([]byte(tt.body))
			if tokens != 0 {
				t.Errorf("tokens = %d, want 0 (无图请求短路应为 0)", tokens)
			}
			// 短路必须原样返回 body，不得改动任何字节
			if string(cleaned) != tt.body {
				t.Errorf("cleaned body changed:\n got %s\nwant %s", string(cleaned), tt.body)
			}
		})
	}
}

// TestContainsBase64Fold 验证大小写不敏感的特征匹配：保证短路对 "BASE64"/"Base64"
// 这类 data URL 头不会漏判（漏判会导致真实图片被当文本高估而重新 503）。
func TestContainsBase64Fold(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"lower", "data:image/png;base64,AAAA", true},
		{"upper", "data:image/png;BASE64,AAAA", true},
		{"mixed", "data:image/png;Base64,AAAA", true},
		{"anthropic_source", `{"type":"base64","data":"AAAA"}`, true},
		{"absent", "the base camp", false},
		{"empty", "", false},
		{"partial_prefix", "bas", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsBase64Fold([]byte(tt.body), imageBase64Marker); got != tt.want {
				t.Errorf("containsBase64Fold(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

// 端到端：含图请求估算不应超过 unknownSafeWindow 阈值（200K 是 ccx 默认）
func TestEstimateRequestTokens_WithImage(t *testing.T) {
	tests := []struct {
		name   string
		image  string
		minTok int
		maxTok int
	}{
		{"small_512", makePNG(512, 512), 324, 500},
		{"medium_1024", makePNG(1024, 1024), 1369, 1600},
		{"large_4096_hits_max", makePNG(4096, 4096), 16384, 17000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(
				`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}`,
				tt.image))
			got := EstimateRequestTokens(body)
			if got < tt.minTok || got > tt.maxTok {
				t.Errorf("EstimateRequestTokens = %d, want in [%d, %d]", got, tt.minTok, tt.maxTok)
			}
			// 回归保护：base64 字符当文本数会让结果 ≥ 200K
			if got >= 200000 {
				t.Errorf("EstimateRequestTokens = %d ≥ 200K, base64-as-text bug regressed", got)
			}
		})
	}
}

// Responses API 通过 EstimateResponsesRequestTokens 走另一条估算路径
func TestEstimateResponsesRequestTokens_WithImage(t *testing.T) {
	b64 := makePNG(1024, 1024) // 1369 token
	// Responses input_image 用 image_url 字符串形式
	body := []byte(fmt.Sprintf(
		`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,%s"}]}]}`,
		b64))
	got := EstimateResponsesRequestTokens(body)
	if got < 1369 || got > 1600 {
		t.Errorf("EstimateResponsesRequestTokens = %d, want ~1369-1500", got)
	}
	if got >= 200000 {
		t.Errorf("EstimateResponsesRequestTokens = %d ≥ 200K, base64-as-text bug regressed", got)
	}
}

// TestEstimateImageTokensFromSize_FuzzyOracle 用 100 个随机尺寸验证算法
// 与 Qwen3-VL Python 官方实现 1:1 对齐。
// 数据来源：使用 Qwen Python smart_resize 算法离线生成。
func TestEstimateImageTokensFromSize_FuzzyOracle(t *testing.T) {
	cases := []struct{ h, w, want int }{
		{3649, 820, 3770}, {9013, 8025, 16200}, {7315, 4573, 16261}, {3359, 2849, 12240},
		{13826, 1042, 16310}, {977, 3071, 3850}, {7165, 7624, 16368}, {870, 6516, 7223},
		{13747, 7224, 16192}, {14720, 9116, 16200}, {213, 5232, 1496}, {13849, 11150, 16188},
		{9106, 5095, 16245}, {7056, 11030, 16320}, {3350, 3040, 13080}, {12450, 3170, 16192},
		{11764, 11271, 16250}, {8668, 1424, 15810}, {15055, 4091, 16170}, {12404, 2583, 16240},
		{9607, 11851, 16330}, {6301, 2280, 16112}, {1502, 7468, 14418}, {9483, 2615, 16281},
		{7629, 3310, 16296}, {12456, 9109, 16241}, {14858, 11955, 16188}, {5330, 12131, 16212},
		{11642, 6866, 16268}, {8749, 2340, 16302}, {5608, 8022, 16371}, {5355, 15148, 16340},
		{12434, 8846, 16157}, {7197, 10627, 16275}, {1833, 7506, 16317}, {1052, 10337, 14022},
		{13146, 8774, 16224}, {2169, 6914, 16188}, {10312, 6968, 16275}, {16359, 12965, 16159},
		{15036, 4682, 16259}, {8680, 4576, 16192}, {8082, 8610, 16368}, {14039, 13088, 16236},
		{11862, 7187, 16236}, {4533, 16172, 16147}, {2979, 1544, 5830}, {3593, 5009, 16308},
		{5243, 13834, 16146}, {2082, 12609, 16328}, {12505, 15338, 16215}, {8239, 377, 3822},
		{3754, 8744, 16185}, {11147, 3656, 16279}, {9618, 14247, 16275}, {5183, 14868, 16200},
		{107, 8631, 1232}, {5855, 3487, 16170}, {9780, 6518, 16224}, {5009, 12253, 16200},
		{10622, 16011, 16328}, {639, 3666, 3013}, {11895, 10077, 16263}, {7847, 1899, 16120},
		{7893, 2581, 16279}, {2807, 15925, 16112}, {2268, 4121, 11907}, {4208, 15575, 16236},
		{5411, 8686, 16362}, {13866, 6941, 16200}, {6592, 10215, 16218}, {13075, 12237, 16236},
		{14356, 14795, 16254}, {3966, 8124, 16287}, {7363, 2099, 16252}, {11079, 690, 9900},
		{7541, 7217, 16250}, {236, 2327, 664}, {1930, 7502, 16128}, {2209, 1030, 2923},
		{10828, 2322, 16284}, {7799, 9126, 16284}, {15907, 7021, 16320}, {4336, 15489, 16147},
		{7963, 15499, 16198}, {13339, 6240, 16269}, {3091, 3177, 12430}, {14125, 11610, 16356},
		{13880, 13471, 16254}, {15304, 1776, 16125}, {3225, 1987, 8165}, {13194, 11119, 16263},
		{3581, 8148, 16212}, {6279, 6233, 16256}, {14701, 4594, 16188}, {13825, 6013, 16296},
		{9128, 15160, 16236}, {8186, 2471, 16240}, {14521, 3209, 16320}, {1658, 484, 1003},
	}

	for _, c := range cases {
		got := estimateImageTokensFromSize(c.h, c.w)
		if got != c.want {
			t.Errorf("h=%d w=%d: got %d, want %d (oracle)", c.h, c.w, got, c.want)
		}
	}
}

// 性能基线：单图请求体 ~770KB (1024x1024 PNG)
func BenchmarkEstimateRequestTokens_SingleImage(b *testing.B) {
	b64 := makePNG(1024, 1024)
	body := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}`,
		b64))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EstimateRequestTokens(body)
	}
}

// 性能基线：纯文本（验证 base64 改动不影响纯文本路径）
func BenchmarkEstimateRequestTokens_TextOnly(b *testing.B) {
	body := []byte(`{"messages":[{"role":"user","content":"Hello world, this is a typical chat message."}]}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EstimateRequestTokens(body)
	}
}

// 验证大图场景内存放大倍数
func BenchmarkEstimateRequestTokens_LargeImage(b *testing.B) {
	b64 := makePNG(2048, 2048) // ~3MB base64
	body := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}`,
		b64))
	b.ReportMetric(float64(len(body))/1024, "body-KB")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EstimateRequestTokens(body)
	}
}

func TestDataURLPayload(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantHit bool
	}{
		{"standard_png", "data:image/png;base64,AAAA", true},
		{"with_param", "data:image/jpeg;charset=utf-8;base64,AAAA", true},
		{"svg_plus", "data:image/svg+xml;base64,AAAA", true},
		{"uppercase", "DATA:IMAGE/PNG;BASE64,AAAA", true},
		{"empty_payload", "data:image/png;base64,", false},
		{"no_comma", "data:image/png;base64", false},
		{"not_base64", "data:image/png,raw", false},
		{"not_image", "data:text/plain;base64,AAAA", false},
		{"fake_base64_substring", "data:image/x;base64xyz,AAAA", false}, // ";base64" 非结尾，不应误判
		{"leading_space", "  data:image/png;base64,AAAA", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if b, _ := imageDataURLPayload(tt.url); (b != "") != tt.wantHit {
				t.Errorf("imageDataURLPayload(%q) hit=%v, want %v", tt.url, b != "", tt.wantHit)
			}
		})
	}
}

func TestExtractImageTokensAndStripBytes_MessagesAndInputMutuallyExclusive(t *testing.T) {
	b64 := makePNG(512, 512) // 单图 324 token
	// 畸形请求：messages 和 input 同时存在，只应计一边，不双算
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + b64 + `"}}]}],` +
		`"input":[{"content":[{"type":"input_image","image_url":"data:image/png;base64,` + b64 + `"}]}]}`)
	_, tokens := extractImageTokensAndStripBytes(body)
	if tokens != 324 {
		t.Errorf("tokens=%d, want 324 (messages 优先，input 不应再计)", tokens)
	}
}

// extractImageTokensAndStripBytes 必须幂等：对已剥离（含 "<image>" 占位符）的
// body 再次提取，不得重复累加图片 token。回归 Anthropic image schema 占位符
// 被二次识别导致每图多算一次 fallback 的 bug。
func TestExtractImageTokensAndStripBytes_Idempotent(t *testing.T) {
	b64 := makePNG(512, 512) // 324 token
	bodies := map[string]string{
		"anthropic": `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + b64 + `"}}]}]}`,
		"openai":    `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + b64 + `"}}]}]}`,
		"responses": `{"input":[{"content":[{"type":"input_image","image_url":"data:image/png;base64,` + b64 + `"}]}]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			cleaned, tok1 := extractImageTokensAndStripBytes([]byte(body))
			if tok1 != 324 {
				t.Errorf("首次提取 tok=%d, want 324", tok1)
			}
			_, tok2 := extractImageTokensAndStripBytes(cleaned)
			if tok2 != 0 {
				t.Errorf("二次提取 tok=%d, want 0 (占位符不应被重复识别)", tok2)
			}
		})
	}
}

// EstimateRequestTokens 不得因内部调用链对图片重复计数。
func TestEstimateRequestTokens_NoImageDoubleCount(t *testing.T) {
	b64 := makePNG(512, 512) // 324 token
	cases := map[string]string{
		"anthropic": `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + b64 + `"}}]}]}`,
		"openai":    `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + b64 + `"}}]}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got := EstimateRequestTokens([]byte(body))
			// 真实图 324 + 少量 JSON 结构/消息开销，远小于 324*2
			if got < 324 || got > 500 {
				t.Errorf("EstimateRequestTokens=%d, want ~324+小开销 (双重计数会接近 648+)", got)
			}
		})
	}
}

// Gemini 内联图（contents[].parts[].inlineData）必须被识别、按真实尺寸计 token 并剥离 base64。
// camelCase(inlineData/mimeType) 与 snake_case(inline_data/mime_type) 两种变体都要覆盖。
func TestExtractImageTokensAndStripBytes_GeminiInlineData(t *testing.T) {
	b64 := makePNG(512, 512) // 324 token
	tests := []struct {
		name string
		body string
	}{
		{
			name: "camelCase",
			body: `{"contents":[{"role":"user","parts":[{"text":"hi"},{"inlineData":{"mimeType":"image/png","data":"` + b64 + `"}}]}]}`,
		},
		{
			name: "snake_case",
			body: `{"contents":[{"role":"user","parts":[{"inline_data":{"mime_type":"image/png","data":"` + b64 + `"}}]}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, tokens := extractImageTokensAndStripBytes([]byte(tt.body))
			if tokens != 324 {
				t.Errorf("tokens=%d, want 324", tokens)
			}
			if !json.Valid(cleaned) {
				t.Fatalf("cleaned 非合法 JSON: %s", string(cleaned))
			}
			if bytes.Contains(cleaned, []byte(b64)) {
				t.Errorf("base64 未被剥离: %s", string(cleaned))
			}
		})
	}
}

// Gemini 非图非音 inlineData（如 PDF）自媒体泛化修复起按通用附件剥离并给固定保守估算，
// 不再保持原样——否则 1MB+ PDF 会被 base64-as-text 估成几十万 token 撞穿渠道窗口。
func TestExtractImageTokensAndStripBytes_GeminiNonImageAttachment(t *testing.T) {
	b64 := makePNG(512, 512)
	body := `{"contents":[{"parts":[{"inlineData":{"mimeType":"application/pdf","data":"` + b64 + `"}}]}]}`
	cleaned, tokens := extractImageTokensAndStripBytes([]byte(body))
	if tokens != attachmentTokenFallback {
		t.Errorf("PDF inlineData 应按附件固定估算, got %d, want %d", tokens, attachmentTokenFallback)
	}
	if bytes.Contains(cleaned, []byte(b64)) {
		t.Errorf("附件 base64 未被剥离: %s", string(cleaned))
	}
}

// EstimateGeminiRequestTokens 端到端：含大图的 Gemini 请求必须「按真实尺寸计图」，
// 绝不能退回「把整个 body（含巨大 base64）按字符数估算」的高估老路。
//
// 这里用 makeNoisePNG 造不可压缩的随机像素图：base64 体积巨大，于是
//   - 真实尺寸估算（smart_resize）只有几千 token；
//   - 而按整个 body 字符估算（提取失效时的行为）会高达 base64长度/3.5 量级，差出一个数量级。
//
// 正因差异巨大，下面的强断言才能真正区分「提取生效」与「提取失效」，
// 守住本次修复要守的回归（旧版纯色图 + 宽松区间无法区分，是假阳性）。
//
// 守护范围（重要，避免后人误解）：本测试只守「图片提取已接入 Gemini 入口
// + base64 已被剥离出文本 + 未退回按整个 body 字符高估」这三件事。它**不**守
// sizing 公式本身的正确性——因为参考值 imageTokens 同样来自 estimateImageTokensFromBase64，
// 与生产入口内部计图走同一条函数链（decodeImageSizeFromBase64 / estimateImageTokensFromSize），
// 二者锁步：若 sizing 算法回归，本测试断言1 仍满足、照样绿。sizing 正确性由
// TestEstimateImageTokensFromSize 用硬编码 want 值单独守住。
func TestEstimateGeminiRequestTokens_WithImage(t *testing.T) {
	// fixture 用 1024x1024：base64 约 4MB，仍不可压缩、charEstimate/5 余量仍 100×+，
	// 断言完全站得住；相比 2048x2048（base64≈16.7MB、PNG 编码 419 万像素）耗时大降。
	b64 := makeNoisePNG(1024, 1024) // 随机噪声，PNG 压不动，base64 体积巨大
	body := `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"` + b64 + `"}}]}],"generationConfig":{"maxOutputTokens":1024}}`
	got := EstimateGeminiRequestTokens([]byte(body))

	// 期望值：图片真实 token（按 base64 解出的尺寸 smart_resize）+ 一点剥离后文本开销。
	imageTokens := estimateImageTokensFromBase64(b64)
	if imageTokens <= 0 {
		t.Fatalf("测试 fixture 异常：估算不出图片 token (imageTokens=%d)", imageTokens)
	}

	// 断言1：got 必须紧贴「图片真实 token」附近。剥离后 body 只剩极短的 JSON 骨架，
	// 文本开销只有几十 token，所以 got 应落在 [imageTokens, imageTokens+200] 内。
	//
	// 这里的 +200：剥离后 cleaned 仅约 141 字节的 JSON 骨架（contents/role/parts/
	// inlineData + "<image>" 占位）加上 generationConfig，实测文本开销恒为约 40 token；
	// 取 200 作为该骨架文本开销的宽松上界，留足余量（约 5×）。
	// 若后续往 Gemini body 骨架增删字段，需相应复核此上界。
	if got < imageTokens || got > imageTokens+200 {
		t.Errorf("EstimateGeminiRequestTokens=%d，期望紧贴图片真实估算 %d(+少量文本开销)；"+
			"偏离过大说明未按真实尺寸计图", got, imageTokens)
	}

	// 断言2：got 必须显著小于「把整个 body 按字符估算」的值。
	// 提取失效退回 EstimateTokens(string(body)) 时，巨大的 base64 会被按字符高估，
	// 该值至少是 got 的数倍。这里要求 got < charEstimate/5，确保没退回字符高估老路。
	// 与断言1 略有重叠（断言1 上界已能抓住提取失效），但保留作防御纵深：
	// 缩 fixture 后该断言耗时已可忽略，换一道独立维度的兜底是划算的。
	charEstimate := EstimateTokens(string(body))
	if got >= charEstimate/5 {
		t.Errorf("EstimateGeminiRequestTokens=%d 未显著低于按字符估算 %d(got>=1/5)，"+
			"疑似退回把整个 body 当文本高估（图片提取失效）", got, charEstimate)
	}
}

// JPEG 的 SOF 尺寸标记被前置大段（EXIF/COM）推到 8KB 之后时，
// 提高 sniff 上限后仍应能读出真实尺寸，而非回退 fallback。
func TestDecodeImageSizeFromBase64_LargeHeaderBeforeSOF(t *testing.T) {
	// 生成一张正常 JPEG，再在 SOI(FFD8) 之后插入一个大的 COM 段(FFFE + 长度 + 数据)，
	// 把 SOF 推到 8KB 之后、64KB 之内，验证现在能读出尺寸。
	raw, err := base64.StdEncoding.DecodeString(makeJPEG(1024, 768, 90))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 2 || raw[0] != 0xFF || raw[1] != 0xD8 {
		t.Fatal("makeJPEG 未以 SOI 开头")
	}
	// COM 段：FF FE <2字节长度(含自身)> <数据>。长度上限 65535。
	const comPayload = 20000
	com := make([]byte, 0, comPayload+4)
	com = append(com, 0xFF, 0xFE)
	segLen := comPayload + 2
	com = append(com, byte(segLen>>8), byte(segLen&0xFF))
	com = append(com, bytes.Repeat([]byte{0x20}, comPayload)...)

	withBigHeader := make([]byte, 0, len(raw)+len(com))
	withBigHeader = append(withBigHeader, raw[:2]...) // SOI
	withBigHeader = append(withBigHeader, com...)     // 大 COM 段
	withBigHeader = append(withBigHeader, raw[2:]...) // 其余

	b64 := base64.StdEncoding.EncodeToString(withBigHeader)
	h, w, ok := decodeImageSizeFromBase64(b64)
	if !ok {
		t.Fatal("SOF 在大头之后仍应能读出尺寸（sniff 上限已提高）")
	}
	if h != 768 || w != 1024 {
		t.Errorf("读出尺寸 %dx%d, want 1024x768", w, h)
	}
}
