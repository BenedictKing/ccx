package utils

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

// ============== 测试夹具 ==============

// makeWAV 构造合法的 RIFF/WAVE PCM 文件头 + data 载荷，返回完整文件字节。
// dataBytes 是 PCM 数据长度（可截断——只填前 sniff 范围，size 字段仍写完整值，
// 验证「data chunk 超出 sniff 仍按 size 字段计算」的路径）。
func makeWAV(sampleRate uint32, channels, bitsPerSample uint16, dataBytes int, fillBytes int) []byte {
	byteRate := sampleRate * uint32(channels) * uint32(bitsPerSample) / 8
	blockAlign := channels * bitsPerSample / 8

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	writeU32LE(&buf, uint32(36+dataBytes))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	writeU32LE(&buf, 16)
	writeU16LE(&buf, 1) // PCM
	writeU16LE(&buf, channels)
	writeU32LE(&buf, sampleRate)
	writeU32LE(&buf, byteRate)
	writeU16LE(&buf, blockAlign)
	writeU16LE(&buf, bitsPerSample)
	buf.WriteString("data")
	writeU32LE(&buf, uint32(dataBytes))
	if fillBytes > dataBytes {
		fillBytes = dataBytes
	}
	buf.Write(make([]byte, fillBytes))
	return buf.Bytes()
}

// makeFLAC 构造 fLaC + STREAMINFO 头（34 字节 metadata block body）。
func makeFLAC(sampleRate uint32, totalSamples uint64) []byte {
	var buf bytes.Buffer
	buf.WriteString("fLaC")
	buf.WriteByte(0x80) // last-metadata-block + type 0 (STREAMINFO)
	buf.Write([]byte{0, 0, 34})
	body := make([]byte, 34)
	// offset 10 起 8 字节：sampleRate(20) | channels-1(3) | bps-1(5) | totalSamples(36)
	v := uint64(sampleRate&0xFFFFF)<<44 | uint64(0)<<41 | uint64(15)<<36 | (totalSamples & 0xFFFFFFFFF)
	binary.BigEndian.PutUint64(body[10:18], v)
	buf.Write(body)
	return buf.Bytes()
}

// makeMP3FrameHeader 构造一个 MPEG Layer III 帧头。
// version: "1"=MPEG1, "2"=MPEG2；bitrateKbps 需存在于对应表；sampleRate 为实际采样率。
func makeMP3FrameHeader(version string, bitrateKbps, sampleRate int, stereo bool) []byte {
	var versionID, srIdx, bitrateIdx uint32
	var table [16]int
	switch version {
	case "1":
		versionID = 3
		table = mp3BitrateV1L3
	case "2":
		versionID = 2
		table = mp3BitrateV2L3
		srIdx = uint32(indexOfInt([]int{44100, 48000, 32000}, sampleRate*2))
	default:
		panic("unsupported version")
	}
	if version == "1" {
		srIdx = uint32(indexOfInt([]int{44100, 48000, 32000}, sampleRate))
	}
	for i, b := range table {
		if b == bitrateKbps {
			bitrateIdx = uint32(i)
			break
		}
	}
	var channelMode uint32 = 0 // stereo
	if !stereo {
		channelMode = 3 // mono
	}
	h := uint32(0x7FF)<<21 | versionID<<19 | 1<<17 | bitrateIdx<<12 | srIdx<<10 | channelMode<<6
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, h)
	return out
}

// makeMP3CBR 构造 CBR MP3：ID3 跳过路径可选，帧头 + 填充至 totalBytes。
func makeMP3CBR(version string, bitrateKbps, sampleRate int, totalBytes int, withID3 bool) []byte {
	var buf bytes.Buffer
	if withID3 {
		buf.WriteString("ID3")
		buf.Write([]byte{4, 0, 0, 0, 0, 0, 0}) // version + flags + synchsafe size 0
	}
	buf.Write(makeMP3FrameHeader(version, bitrateKbps, sampleRate, true))
	for buf.Len() < totalBytes {
		buf.WriteByte(0)
	}
	return buf.Bytes()[:totalBytes]
}

// makeMP3VBR 构造带 Xing tag 的 VBR MP3（MPEG1 stereo）。
func makeMP3VBR(sampleRate int, frames uint32) []byte {
	var buf bytes.Buffer
	buf.Write(makeMP3FrameHeader("1", 128, sampleRate, true))
	// Xing tag 位于帧头后 4+32=36 字节处（MPEG1 stereo）
	for buf.Len() < 36 {
		buf.WriteByte(0)
	}
	buf.WriteString("Xing")
	writeU32BE(&buf, 0x1) // flags: frames present
	writeU32BE(&buf, frames)
	// 填充到一帧大小（128kbps @44100 = 417 bytes），不影响时长解析
	for buf.Len() < 417 {
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

// makeM4A 构造 ftyp + moov>trak>mdia>mdhd(v0) 的最小 MP4 头。
func makeM4A(timescale, duration uint32) []byte {
	var buf bytes.Buffer
	// ftyp
	writeU32BE(&buf, 24)
	buf.WriteString("ftyp")
	buf.WriteString("M4A ")
	writeU32BE(&buf, 0)
	buf.WriteString("M4A ")
	buf.WriteString("mp42")
	// mdhd body (v0): version/flags(4) ctime(4) mtime(4) timescale(4) duration(4) lang(2) quality(2)
	var mdhd bytes.Buffer
	mdhd.WriteByte(0)
	mdhd.Write([]byte{0, 0, 0})
	writeU32BE(&mdhd, 0)
	writeU32BE(&mdhd, 0)
	writeU32BE(&mdhd, timescale)
	writeU32BE(&mdhd, duration)
	writeU16BE(&mdhd, 0)
	writeU16BE(&mdhd, 0)
	mdiaBody := wrapBox("mdhd", mdhd.Bytes())
	trakBody := wrapBox("mdia", mdiaBody)
	moovBody := wrapBox("trak", trakBody)
	buf.Write(wrapBox("moov", moovBody))
	return buf.Bytes()
}

// makeWebM 构造 EBML + Segment>Info>TimecodeScale/Duration 的最小 WebM 头。
func makeWebM(timecodeScaleNs uint64, durationTicks float64) []byte {
	var buf bytes.Buffer
	// EBML header 元素：id 0x1A45DFA3 + size + body（body 内容对扫描无意义，用空 body）
	buf.Write([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x80}) // size=0 (1字节 EBML size: 0x80|0)
	// Segment（unknown-size 之外用定长，便于扫描）
	var info bytes.Buffer
	// TimecodeScale: id 0x2AD7B1, size 3, value
	info.Write([]byte{0x2A, 0xD7, 0xB1, 0x83})
	info.Write([]byte{byte(timecodeScaleNs >> 16), byte(timecodeScaleNs >> 8), byte(timecodeScaleNs)})
	// Duration: id 0x4489, size 8, float64 BE
	info.Write([]byte{0x44, 0x89, 0x88})
	fb := make([]byte, 8)
	binary.BigEndian.PutUint64(fb, math.Float64bits(durationTicks))
	info.Write(fb)
	// Info element: id 0x1549A966
	var segment bytes.Buffer
	segment.Write([]byte{0x15, 0x49, 0xA9, 0x66})
	writeEBMLSize(&segment, uint64(info.Len()))
	segment.Write(info.Bytes())
	// Segment element: id 0x18538067
	buf.Write([]byte{0x18, 0x53, 0x80, 0x67})
	writeEBMLSize(&buf, uint64(segment.Len()))
	buf.Write(segment.Bytes())
	return buf.Bytes()
}

func wrapBox(typ string, body []byte) []byte {
	var buf bytes.Buffer
	writeU32BE(&buf, uint32(8+len(body)))
	buf.WriteString(typ)
	buf.Write(body)
	return buf.Bytes()
}

func writeEBMLSize(buf *bytes.Buffer, size uint64) {
	// 4 字节 EBML size：0x10 | size(28bit)
	buf.Write([]byte{0x10 | byte(size>>24), byte(size >> 16), byte(size >> 8), byte(size)})
}

func writeU16LE(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}
func writeU32LE(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}
func writeU16BE(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}
func writeU32BE(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func indexOfInt(list []int, v int) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return 0
}

func b64Of(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

// ============== 时长解析单元测试 ==============

func TestWavDurationSeconds(t *testing.T) {
	// 24kHz/16bit/mono：byteRate = 48000；1,048,576 字节 ≈ 21.845s
	wav := makeWAV(24000, 1, 16, 1_048_576, 4096) // data 只填 4KB，size 字段写完整
	got := wavDurationSeconds(wav)
	want := 1_048_576.0 / 48000.0
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("wavDurationSeconds = %v, want %v", got, want)
	}
}

func TestWavDurationSeconds_TruncatedDataChunk(t *testing.T) {
	// data chunk size 巨大但 sniff 内只有头部——必须仍按 size 字段计算
	wav := makeWAV(48000, 2, 16, 50_000_000, 512)
	got := wavDurationSeconds(wav)
	byteRate := 48000.0 * 2 * 2
	want := 50_000_000.0 / byteRate
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("wavDurationSeconds(truncated) = %v, want %v", got, want)
	}
}

func TestFLACDurationSeconds(t *testing.T) {
	// 44.1kHz、5,292,000 samples = 120s
	flac := makeFLAC(44100, 5_292_000)
	got := flacDurationSeconds(flac)
	if math.Abs(got-120.0) > 1e-9 {
		t.Errorf("flacDurationSeconds = %v, want 120", got)
	}
}

func TestMP3DurationSeconds_CBR(t *testing.T) {
	// 128kbps CBR、1MB：时长 ≈ 1,000,000×8/128,000 = 62.5s
	mp3 := makeMP3CBR("1", 128, 44100, 1_000_000, false)
	got := mp3DurationSeconds(mp3, int64(len(mp3)))
	if math.Abs(got-62.5) > 0.5 {
		t.Errorf("mp3DurationSeconds(CBR) = %v, want ~62.5", got)
	}
}

func TestMP3DurationSeconds_ID3Skip(t *testing.T) {
	mp3 := makeMP3CBR("1", 128, 44100, 100_000, true)
	got := mp3DurationSeconds(mp3, int64(len(mp3)))
	if got <= 0 {
		t.Errorf("mp3DurationSeconds(ID3) = %v, want > 0", got)
	}
}

func TestMP3DurationSeconds_XingVBR(t *testing.T) {
	// Xing frames=1000 @44100：1000×1152/44100 ≈ 26.122s（与文件大小无关）
	mp3 := makeMP3VBR(44100, 1000)
	got := mp3DurationSeconds(mp3, int64(len(mp3)))
	want := 1000.0 * 1152 / 44100
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("mp3DurationSeconds(Xing) = %v, want %v", got, want)
	}
}

func TestM4ADurationSeconds(t *testing.T) {
	// timescale=16000, duration=960000 → 60s
	m4a := makeM4A(16000, 960000)
	got := m4aDurationSeconds(m4a)
	if math.Abs(got-60.0) > 1e-9 {
		t.Errorf("m4aDurationSeconds = %v, want 60", got)
	}
}

func TestWebMDurationSeconds(t *testing.T) {
	// TimecodeScale=1ms, Duration=90000 ticks → 90s
	webm := makeWebM(1_000_000, 90000)
	got := webmDurationSeconds(webm)
	if math.Abs(got-90.0) > 1e-9 {
		t.Errorf("webmDurationSeconds = %v, want 90", got)
	}
}

func TestAudioDurationFallback(t *testing.T) {
	// 未知/损坏头：1MB 按 128kbps 回退 ≈ 62.5s
	junk := bytes.Repeat([]byte{0xAB}, 1_000_000)
	got := estimateAudioDurationSeconds(b64Of(junk), "audio/mp3", "")
	want := 1_000_000.0 * 8 / 128_000
	if math.Abs(got-want) > 1.0 {
		t.Errorf("fallback duration = %v, want ~%v", got, want)
	}
}

func TestAudioFormatFromMime(t *testing.T) {
	cases := map[string]string{
		"audio/wav":             "wav",
		"audio/x-wav":           "x-wav",
		"audio/mpeg":            "mpeg",
		"audio/mp4":             "mp4",
		"audio/ogg":             "ogg",
		"audio/webm":            "webm",
		"audio/flac":            "flac",
		"audio/mp4;codecs=mp4a": "mp4",
		"image/png":             "",
		"":                      "",
	}
	for mime, want := range cases {
		if got := audioFormatFromMime(mime); got != want {
			t.Errorf("audioFormatFromMime(%q) = %q, want %q", mime, got, want)
		}
	}
}

// ============== token 估算端到端 ==============

// 24kHz/16bit/mono WAV 1MB ≈ 21.85s → ceil(21.85×32) ≈ 700 tokens
func TestEstimateAudioTokensFromBase64_WAV(t *testing.T) {
	wav := makeWAV(24000, 1, 16, 1_048_576, 4096)
	got := estimateAudioTokensFromBase64(b64Of(wav), "audio/wav", "")
	wantSec := 1_048_576.0 / 48000.0
	want := int(math.Ceil(wantSec * audioTokensPerSecond))
	if got != want {
		t.Errorf("estimateAudioTokensFromBase64(WAV) = %d, want %d (%.1fs × %.0f t/s)", got, want, wantSec, audioTokensPerSecond)
	}
	// 回归保护：绝不退回 base64-as-text（1MB base64 ≈ 400K chars ≈ 114K tokens）
	if got > 5000 {
		t.Errorf("WAV tokens = %d, base64-as-text regression", got)
	}
}

// ============== 各 schema 端到端剥离 ==============

// TestEstimateResponsesRequestTokens_Audio 是本次修复的核心回归：
// Codex 0.145 的 input_audio data URL 必须按时长估算，不能按 base64 字符数。
func TestEstimateResponsesRequestTokens_Audio(t *testing.T) {
	wav := makeWAV(24000, 1, 16, 1_048_576, 4096)
	b64 := b64Of(wav) // 完整 1MB 文件 base64（duration 从 size 字段读）
	body := []byte(fmt.Sprintf(
		`{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_audio","audio_url":"data:audio/wav;base64,%s"}]}]}`,
		b64))
	got := EstimateResponsesRequestTokens(body)
	if got >= 200000 {
		t.Fatalf("EstimateResponsesRequestTokens(audio) = %d ≥ 200K, base64-as-text bug", got)
	}
	// 21.85s×32 ≈ 700 tokens + 少量结构开销
	if got < 600 || got > 3000 {
		t.Errorf("EstimateResponsesRequestTokens(audio) = %d, want ~700+开销", got)
	}
}

func TestEstimateRequestTokens_ChatInputAudio(t *testing.T) {
	wav := makeWAV(24000, 1, 16, 1_048_576, 4096)
	b64 := b64Of(wav)
	body := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"%s","format":"wav"}}]}]}`,
		b64))
	got := EstimateRequestTokens(body)
	if got >= 200000 {
		t.Fatalf("EstimateRequestTokens(chat audio) = %d ≥ 200K, base64-as-text bug", got)
	}
	if got < 600 || got > 3000 {
		t.Errorf("EstimateRequestTokens(chat audio) = %d, want ~700+开销", got)
	}
}

func TestEstimateGeminiRequestTokens_Audio(t *testing.T) {
	mp3 := makeMP3CBR("1", 128, 44100, 1_000_000, false)
	b64 := b64Of(mp3)
	body := []byte(fmt.Sprintf(
		`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"audio/mpeg","data":"%s"}}]}]}`,
		b64))
	got := EstimateGeminiRequestTokens(body)
	if got >= 200000 {
		t.Fatalf("EstimateGeminiRequestTokens(audio) = %d ≥ 200K, base64-as-text bug", got)
	}
	// 62.5s×32 = 2000 tokens + 少量结构开销
	if got < 1800 || got > 4000 {
		t.Errorf("EstimateGeminiRequestTokens(audio) = %d, want ~2000+开销", got)
	}
}

func TestEstimateRequestTokens_ClaudePDFDocument(t *testing.T) {
	// 1MB 伪 PDF：按附件固定估算 4000，不按 base64 字符数
	big := bytes.Repeat([]byte("%PDF-1.4 fake "), 72_000) // ≈ 1MB
	b64 := b64Of(big)
	body := []byte(fmt.Sprintf(
		`{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"%s"}}]}]}`,
		b64))
	got := EstimateRequestTokens(body)
	if got >= 200000 {
		t.Fatalf("EstimateRequestTokens(pdf) = %d ≥ 200K, base64-as-text bug", got)
	}
	if got < attachmentTokenFallback || got > attachmentTokenFallback+500 {
		t.Errorf("EstimateRequestTokens(pdf) = %d, want ~%d+开销", got, attachmentTokenFallback)
	}
}

func TestEstimateResponsesRequestTokens_InputFile(t *testing.T) {
	big := bytes.Repeat([]byte("%PDF fake "), 100_000) // ≈ 1MB
	b64 := b64Of(big)
	body := []byte(fmt.Sprintf(
		`{"input":[{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:application/pdf;base64,%s"}]}]}`,
		b64))
	got := EstimateResponsesRequestTokens(body)
	if got >= 200000 {
		t.Fatalf("EstimateResponsesRequestTokens(input_file) = %d ≥ 200K", got)
	}
	if got < attachmentTokenFallback || got > attachmentTokenFallback+500 {
		t.Errorf("EstimateResponsesRequestTokens(input_file) = %d, want ~%d+开销", got, attachmentTokenFallback)
	}
}

// ============== 幂等与占位符 ==============

func TestExtractMedia_Idempotent(t *testing.T) {
	wav := makeWAV(24000, 1, 16, 1_048_576, 4096)
	b64 := b64Of(wav)
	bodies := map[string]string{
		"responses_audio": fmt.Sprintf(`{"input":[{"content":[{"type":"input_audio","audio_url":"data:audio/wav;base64,%s"}]}]}`, b64),
		"chat_audio":      fmt.Sprintf(`{"messages":[{"content":[{"type":"input_audio","input_audio":{"data":"%s","format":"wav"}}]}]}`, b64),
		"claude_pdf":      fmt.Sprintf(`{"messages":[{"content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"%s"}}]}]}`, b64),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			cleaned, tok1 := extractImageTokensAndStripBytes([]byte(body))
			if tok1 <= 0 {
				t.Fatalf("首次提取 tokens = %d, want > 0", tok1)
			}
			_, tok2 := extractImageTokensAndStripBytes(cleaned)
			if tok2 != 0 {
				t.Errorf("二次提取 tokens = %d, want 0 (占位符不应被重复识别)", tok2)
			}
		})
	}
}

func TestMediaPayload_RemoteURLIgnored(t *testing.T) {
	// 远程 URL 不随请求体传输字节，不应剥离也不计 token
	body := []byte(`{"input":[{"content":[{"type":"input_audio","audio_url":"https://example.com/a.mp3"}]}]}`)
	cleaned, tokens := extractImageTokensAndStripBytes(body)
	if tokens != 0 {
		t.Errorf("远程 URL 应计 0 token, got %d", tokens)
	}
	if !bytes.Equal(cleaned, body) {
		t.Errorf("远程 URL body 不应被改动")
	}
}

// ============== 基准 ==============

func BenchmarkEstimateRequestTokens_Audio(b *testing.B) {
	wav := makeWAV(24000, 1, 16, 1_048_576, 4096)
	body := []byte(fmt.Sprintf(
		`{"input":[{"content":[{"type":"input_audio","audio_url":"data:audio/wav;base64,%s"}]}]}`,
		b64Of(wav)))
	b.ReportMetric(float64(len(body))/1024, "body-KB")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EstimateResponsesRequestTokens(body)
	}
}
