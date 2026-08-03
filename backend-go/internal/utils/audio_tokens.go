package utils

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
)

// 音频 token 估算：按音频真实时长 × 固定速率，绝不按 base64 字符数。
//
// 速率取 32 tokens/s 作为全上游保守上界：
//   - Gemini 官方：audio = 32 tokens/s（https://ai.google.dev/gemini-api/docs/tokens）
//   - OpenAI 对话音频输入：1 token / 100ms = 10 tokens/s
//
// 与图片模块选 Qwen3-VL 16384 上界同一哲学——路由估算是保守上界而非计费精度，
// 宁可偏高也不能低到让长音频撞穿窗口后被静默截断或 503。
// 后续若要 per-model 精确率，应在模型注册表加数值能力（如 audioInputTokensPerSecond），
// 由调用方传入；本模块签名刻意不依赖 config，避免 utils→config 反向依赖。
//
// 时长解析零第三方依赖，只读容器头（sniff 策略与图片模块一致）：
// WAV/FLAC 精确；MP3/M4A/WebM 尽力解析头，失败按格式码率表回退；未知格式按 128kbps 回退。
// 回退目标是「时长量级正确」，彻底消除 base64-as-text 的 ~2000× 高估，而非逐 token 精确。
const (
	audioTokensPerSecond = 32.0
	// audioSniffBytes 是从 base64 头部截取的「base64 字符数」上限（约 192KB 解码字节）。
	// WAV fmt/data chunk 头、FLAC STREAMINFO、MP3 首帧+Xing、M4A 头部 moov、
	// WebM Info element 基本都落在该范围内；moov 在文件尾的 M4A 会走码率回退。
	audioSniffBytes = 262144
	// audioMaxDurationSeconds 是时长合法性上限（12 小时）。Gemini 单 prompt 音频上限 9.5h，
	// 超过 12h 的解析结果几乎必是容器损坏/误识别，按回退处理避免天文数字。
	audioMaxDurationSeconds = 12 * 3600.0
	// attachmentTokenFallback 是 PDF 等非音/图内联附件的固定保守估算。
	// 依据：1MB PDF 通常几十页，Anthropic PDF 计费约 1.5-3K tokens/15 页，
	// 取 4000 覆盖常见文档量级；路由用途不需要计费精度，只需要不为零、不为天文数字。
	attachmentTokenFallback = 4000
)

// audioBitrateFallbackKbps 是各格式无法解析时长时的回退码率（kbps）。
// 取该格式常见配置的中位偏高值：既不会因取极低码率把时长放大几个数量级，
// 也保持保守（真实码率更低时估算会偏高，与模块哲学一致）。
var audioBitrateFallbackKbps = map[string]float64{
	"mp3":  128,
	"mpeg": 128,
	"m4a":  128,
	"mp4":  128,
	"aac":  128,
	"ogg":  112, // vorbis 常见 96-128
	"oga":  112,
	"opus": 64,
	"webm": 48, // webm 音频轨几乎总是 Opus 低码率
	"flac": 800,
	"wav":  384, // 仅容器损坏时兜底：24kHz×16bit×mono
}

// estimateAudioTokensFromBase64 从音频 base64（裸 base64 或 data URL 主体）估算 token 数。
// mime 可为空；format 是 Chat input_audio.format 等外部给出的格式提示，优先于 sniff。
func estimateAudioTokensFromBase64(b64, mime, format string) int {
	durationSec := estimateAudioDurationSeconds(b64, mime, format)
	if durationSec <= 0 {
		return 0
	}
	return int(math.Ceil(durationSec * audioTokensPerSecond))
}

// estimateAudioDurationSeconds 估算音频时长（秒）。永远不会返回负数；
// 所有路径都经过合法性钳制，解析失败按码率表回退。
func estimateAudioDurationSeconds(b64, mime, format string) float64 {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return 0
	}
	// 容错：万一上游把整个 data URL 传进来，先剥头。
	if strings.HasPrefix(b64, "data:") {
		if comma := strings.IndexByte(b64, ','); comma >= 0 {
			b64 = b64[comma+1:]
		}
	}

	totalDecodedBytes := int64(len(b64)) * 3 / 4

	sniffLen := len(b64)
	if sniffLen > audioSniffBytes {
		sniffLen = audioSniffBytes
	}
	sniffLen -= sniffLen % 4
	head := decodeBase64Prefix(b64[:sniffLen])
	if len(head) == 0 {
		return 0
	}

	kind := normalizeAudioFormat(format)
	if kind == "" {
		kind = audioFormatFromMime(mime)
	}

	var duration float64
	switch kind {
	case "wav":
		duration = wavDurationSeconds(head)
	case "flac":
		duration = flacDurationSeconds(head)
	case "mp3", "mpeg":
		duration = mp3DurationSeconds(head, totalDecodedBytes)
	case "m4a", "mp4", "aac":
		duration = m4aDurationSeconds(head)
	case "webm":
		duration = webmDurationSeconds(head)
	case "ogg", "oga", "opus":
		duration = 0 // Ogg 时长需尾部 granule，sniff 头拿不到，直接走码率表
	default:
		duration = sniffAudioDuration(head, totalDecodedBytes)
	}

	if duration > 0 && duration <= audioMaxDurationSeconds {
		return duration
	}
	return audioDurationFallbackSeconds(kind, totalDecodedBytes)
}

// sniffAudioDuration 未知格式时按 magic bytes 识别容器后重试精确解析。
func sniffAudioDuration(head []byte, totalDecodedBytes int64) float64 {
	switch {
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		return wavDurationSeconds(head)
	case len(head) >= 4 && string(head[:4]) == "fLaC":
		return flacDurationSeconds(head)
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		return m4aDurationSeconds(head)
	case len(head) >= 4 && binary.BigEndian.Uint32(head[:4]) == 0x1A45DFA3:
		return webmDurationSeconds(head)
	case len(head) >= 4 && string(head[:4]) == "OggS":
		return 0
	case len(head) >= 3 && string(head[:3]) == "ID3":
		return mp3DurationSeconds(head, totalDecodedBytes)
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return mp3DurationSeconds(head, totalDecodedBytes)
	}
	return 0
}

// audioDurationFallbackSeconds 码率表回退：时长 = 解码后字节数 × 8 / 码率。
func audioDurationFallbackSeconds(kind string, totalDecodedBytes int64) float64 {
	kbps, ok := audioBitrateFallbackKbps[kind]
	if !ok {
		kbps = 128
	}
	if kbps <= 0 || totalDecodedBytes <= 0 {
		return 0
	}
	d := float64(totalDecodedBytes) * 8 / (kbps * 1000)
	if d <= 0 || d > audioMaxDurationSeconds {
		return 0
	}
	return d
}

// decodeBase64Prefix 解码 base64 前缀，容错 URL-safe 与空白字符（与图片模块同策略）。
func decodeBase64Prefix(b64 string) []byte {
	head, err := base64.StdEncoding.DecodeString(b64)
	if err == nil {
		return head
	}
	alt := strings.NewReplacer("-", "+", "_", "/", "\n", "", "\r", "", " ", "").Replace(b64)
	alt = strings.TrimRight(alt, "=")
	if pad := len(alt) % 4; pad != 0 {
		alt += strings.Repeat("=", 4-pad)
	}
	head, err = base64.StdEncoding.DecodeString(alt)
	if err != nil {
		return nil
	}
	return head
}

func normalizeAudioFormat(format string) string {
	f := strings.ToLower(strings.TrimSpace(format))
	f = strings.TrimPrefix(f, ".")
	switch f {
	case "wave":
		return "wav"
	case "x-m4a":
		return "m4a"
	default:
		return f
	}
}

// audioFormatFromMime 从 "audio/wav"、"audio/x-wav"、"audio/mp4;codecs=..." 提取格式。
func audioFormatFromMime(mime string) string {
	m := strings.ToLower(strings.TrimSpace(mime))
	if semi := strings.IndexByte(m, ';'); semi >= 0 {
		m = m[:semi]
	}
	if !strings.HasPrefix(m, "audio/") {
		return ""
	}
	return normalizeAudioFormat(strings.TrimPrefix(m, "audio/"))
}

// wavDurationSeconds 解析 RIFF/WAVE：遍历 chunk，用 fmt 的 byteRate 除 data size。
// data chunk 只需 8 字节头（id+size）即可读出 size，不要求整个 data 都在 sniff 内。
func wavDurationSeconds(head []byte) float64 {
	if len(head) < 12 || string(head[:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return 0
	}
	var byteRate uint32
	var dataSize uint32
	for off := 12; off+8 <= len(head); {
		id := string(head[off : off+4])
		size := binary.LittleEndian.Uint32(head[off+4 : off+8])
		body := off + 8
		switch id {
		case "fmt ":
			if body+16 <= len(head) {
				byteRate = binary.LittleEndian.Uint32(head[body+8 : body+12])
			}
		case "data":
			dataSize = size
		}
		if byteRate > 0 && dataSize > 0 {
			break
		}
		// chunk 按 2 字节对齐；size 字段超过剩余 sniff 时 data chunk 的 size 已读到，可直接跳出
		next := body + int(size)
		if size%2 == 1 {
			next++
		}
		if next <= off || next > len(head) {
			if id == "data" && dataSize > 0 {
				break
			}
			break
		}
		off = next
	}
	if byteRate == 0 || dataSize == 0 {
		return 0
	}
	return float64(dataSize) / float64(byteRate)
}

// flacDurationSeconds 解析 fLaC + STREAMINFO：sampleRate 20bit + totalSamples 36bit。
func flacDurationSeconds(head []byte) float64 {
	if len(head) < 4+4+34 || string(head[:4]) != "fLaC" {
		return 0
	}
	// 第一个 metadata block 应为 STREAMINFO(type 0)；跳过 magic(4) + block header(4)
	meta := head[8:]
	if len(meta) < 34 || meta[0]&0x7F != 0 {
		return 0
	}
	// STREAMINFO 布局：minBlock(2) maxBlock(2) minFrame(3) maxFrame(3)
	// sampleRate(20) channels-1(3) bps-1(5) totalSamples(36)，从 offset 10 起共 8 字节
	b := meta[10:18]
	v := binary.BigEndian.Uint64(b)
	sampleRate := float64(v >> 44)
	totalSamples := float64(v & 0xFFFFFFFFF)
	if sampleRate <= 0 || totalSamples <= 0 {
		return 0
	}
	return totalSamples / sampleRate
}

// MP3 帧头 bitrate 表：mpeg1 layer3 与 mpeg2/2.5 layer3（kbps），index 0/15 非法。
var mp3BitrateV1L3 = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
var mp3BitrateV2L3 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
var mp3SampleRates = [3]int{44100, 48000, 32000} // index 3 保留

// mp3DurationSeconds 解析 MP3：跳过 ID3v2，找首帧同步；Xing/Info tag 给出精确帧数，
// 否则按 CBR 公式 总字节×8/bitrate。
func mp3DurationSeconds(head []byte, totalDecodedBytes int64) float64 {
	off := 0
	// 跳过 ID3v2 header（10 字节 + synchsafe size）
	if len(head) >= 10 && string(head[:3]) == "ID3" {
		size := int(head[6]&0x7F)<<21 | int(head[7]&0x7F)<<14 | int(head[8]&0x7F)<<7 | int(head[9]&0x7F)
		off = 10 + size
	}
	// 同步搜索首帧
	frameOff := -1
	for i := off; i+4 <= len(head); i++ {
		if head[i] == 0xFF && head[i+1]&0xE0 == 0xE0 {
			frameOff = i
			break
		}
	}
	if frameOff < 0 {
		return 0
	}
	h := binary.BigEndian.Uint32(head[frameOff : frameOff+4])
	versionID := (h >> 19) & 0x3 // 3=MPEG1, 2=MPEG2, 0=MPEG2.5
	layer := (h >> 17) & 0x3
	bitrateIdx := int((h >> 12) & 0xF)
	srIdx := int((h >> 10) & 0x3)
	if layer != 1 || versionID == 1 || srIdx == 3 { // layer 01b = Layer III；version 01b 保留
		return 0
	}
	v1 := versionID == 3
	var bitrateKbps int
	if v1 {
		bitrateKbps = mp3BitrateV1L3[bitrateIdx]
	} else {
		bitrateKbps = mp3BitrateV2L3[bitrateIdx]
	}
	if bitrateKbps == 0 {
		return 0
	}
	sampleRate := mp3SampleRates[srIdx]
	if !v1 {
		sampleRate /= 2
		if versionID == 0 {
			sampleRate /= 2
		}
	}
	// Xing/Info tag：MPEG1 立体声 offset 32 / 单声道 17；MPEG2 立体声 17 / 单声道 9
	channelMode := (h >> 6) & 0x3
	xingOff := frameOff + 4
	if v1 {
		if channelMode == 3 {
			xingOff += 17
		} else {
			xingOff += 32
		}
	} else {
		if channelMode == 3 {
			xingOff += 9
		} else {
			xingOff += 17
		}
	}
	samplesPerFrame := 1152.0
	if !v1 {
		samplesPerFrame = 576
	}
	if xingOff+8 <= len(head) {
		tag := string(head[xingOff : xingOff+4])
		if tag == "Xing" || tag == "Info" {
			flags := binary.BigEndian.Uint32(head[xingOff+4 : xingOff+8])
			if flags&0x1 != 0 && xingOff+12 <= len(head) {
				frames := binary.BigEndian.Uint32(head[xingOff+8 : xingOff+12])
				if frames > 0 {
					return float64(frames) * samplesPerFrame / float64(sampleRate)
				}
			}
		}
	}
	// CBR 近似：总字节×8/bitrate
	return float64(totalDecodedBytes) * 8 / (float64(bitrateKbps) * 1000)
}

// m4aDurationSeconds 遍历 MP4 box 找 moov>trak>mdia>mdhd 的 timescale/duration。
// moov 在文件尾时 sniff 内找不到，返回 0 走码率回退。
func m4aDurationSeconds(head []byte) float64 {
	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		return 0
	}
	var timescale, duration uint64
	walkBoxes(head, 0, len(head), 0, func(boxType string, body []byte) bool {
		if boxType == "mdhd" && timescale == 0 && len(body) >= 24 {
			version := body[0]
			if version == 1 && len(body) >= 36 {
				timescale = uint64(binary.BigEndian.Uint32(body[20:24]))
				duration = binary.BigEndian.Uint64(body[24:32])
			} else if version == 0 && len(body) >= 24 {
				timescale = uint64(binary.BigEndian.Uint32(body[12:16]))
				duration = uint64(binary.BigEndian.Uint32(body[16:20]))
			}
		}
		return timescale == 0 || duration == 0
	})
	if timescale == 0 || duration == 0 {
		return 0
	}
	return float64(duration) / float64(timescale)
}

// walkBoxes 递归遍历 MP4 container box。container 类型下钻，其余只读 body。
// cb 返回 false 时停止遍历。
func walkBoxes(data []byte, start, end, depth int, cb func(boxType string, body []byte) bool) bool {
	if depth > 6 {
		return true
	}
	for off := start; off+8 <= end; {
		size := uint64(binary.BigEndian.Uint32(data[off : off+4]))
		boxType := string(data[off+4 : off+8])
		headerSize := 8
		switch size {
		case 1:
			if off+16 > end {
				return true
			}
			size = binary.BigEndian.Uint64(data[off+8 : off+16])
			headerSize = 16
		case 0:
			size = uint64(end - off)
		}
		if size < uint64(headerSize) || off+int(size) > end {
			return true
		}
		bodyStart := off + headerSize
		bodyEnd := off + int(size)
		switch boxType {
		case "moov", "trak", "mdia":
			if !walkBoxes(data, bodyStart, bodyEnd, depth+1, cb) {
				return false
			}
		default:
			if !cb(boxType, data[bodyStart:bodyEnd]) {
				return false
			}
		}
		off = bodyEnd
	}
	return true
}

// webmDurationSeconds 解析 EBML Segment>Info>TimecodeScale/Duration（float，单位 ms 基准）。
func webmDurationSeconds(head []byte) float64 {
	if len(head) < 4 || binary.BigEndian.Uint32(head[:4]) != 0x1A45DFA3 {
		return 0
	}
	var scaleNs uint64 = 1000000 // 默认 TimecodeScale 1ms
	var durationTicks float64
	found := false
	scanEBML(head, 0, len(head), 0, func(id uint32, payload []byte) bool {
		switch id {
		case 0x2AD7B1: // TimecodeScale
			scaleNs = ebmlUint(payload)
			if scaleNs == 0 {
				scaleNs = 1000000
			}
		case 0x4489: // Duration (float)
			durationTicks = ebmlFloat(payload)
			found = true
		}
		return !found
	})
	if !found || durationTicks <= 0 {
		return 0
	}
	return durationTicks * float64(scaleNs) / 1e9
}

// scanEBML 浅层扫描 EBML 元素（只下钻 Segment/Info 两个 master），cb 返回 false 停止。
func scanEBML(data []byte, start, end, depth int, cb func(id uint32, payload []byte) bool) bool {
	if depth > 4 {
		return true
	}
	off := start
	for off+2 <= end {
		id, idLen := ebmlVINT(data[off:], false)
		if idLen == 0 {
			return true
		}
		sizePos := off + idLen
		if sizePos >= end {
			return true
		}
		size, sizeLen := ebmlVINT(data[sizePos:], true)
		if sizeLen == 0 {
			return true
		}
		// unknown-size（全 1，live 流写法）无法定位元素边界，停止扫描；
		// 注意区分合法的 size=0 空元素（如空 EBML header），后者应跳过继续。
		if isEBMLUnknownSize(size, sizeLen) {
			return true
		}
		bodyStart := sizePos + sizeLen
		bodyEnd := bodyStart + int(size)
		if bodyEnd > end {
			bodyEnd = end
		}
		if bodyStart > bodyEnd {
			return true
		}
		switch uint32(id) {
		case 0x18538067, 0x1549A966: // Segment, Info
			if !scanEBML(data, bodyStart, bodyEnd, depth+1, cb) {
				return false
			}
		default:
			if !cb(uint32(id), data[bodyStart:bodyEnd]) {
				return false
			}
		}
		off = bodyStart + int(size)
	}
	return true
}

// isEBMLUnknownSize 判断 size 是否为 unknown-size（值位全部置 1）。
func isEBMLUnknownSize(size uint64, sizeLen int) bool {
	if sizeLen <= 0 || sizeLen > 8 {
		return false
	}
	return size == (uint64(1)<<(7*uint(sizeLen)))-1
}

// ebmlVINT 解析 EBML 变长整数。value=true 时去掉长度前缀位（size 语义），
// false 时保留（id 语义）。返回 (值, 占用字节数)，失败返回 (0,0)。
func ebmlVINT(data []byte, stripMarker bool) (uint64, int) {
	if len(data) == 0 {
		return 0, 0
	}
	b0 := data[0]
	var mask byte
	length := 1
	for mask = 0x80; mask > 0 && b0&mask == 0; mask >>= 1 {
		length++
	}
	if mask == 0 || length > 8 || len(data) < length {
		return 0, 0
	}
	var v uint64
	if stripMarker {
		v = uint64(b0 &^ mask)
	} else {
		v = uint64(b0)
	}
	for i := 1; i < length; i++ {
		v = v<<8 | uint64(data[i])
	}
	return v, length
}

func ebmlUint(payload []byte) uint64 {
	var v uint64
	for _, b := range payload {
		v = v<<8 | uint64(b)
	}
	return v
}

func ebmlFloat(payload []byte) float64 {
	switch len(payload) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(payload)))
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(payload))
	default:
		return 0
	}
}
