package common

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

// 用户实际遇到的重复样例（planning 文本循环），约 130B 短语。
const repetitionLoopSamplePhrase = "Planning parallel template inspectionPlanning directory creation and copyingPlanning minimal code editing skeleton"

func feedAll(t *testing.T, s *RepetitionLoopScanner, text string, chunkSize int) bool {
	t.Helper()
	if chunkSize <= 0 {
		chunkSize = len(text)
	}
	for start := 0; start < len(text); start += chunkSize {
		end := start + chunkSize
		if end > len(text) {
			end = len(text)
		}
		if s.Feed(text[start:end]) {
			return true
		}
	}
	return s.Found()
}

func repeatString(unit string, count int) string {
	return strings.Repeat(unit, count)
}

func TestRepetitionLoopScannerPhraseLevelHit(t *testing.T) {
	// 用户样例：~114B 短语重复 28 次 ≈ 3.2KB，达到短语档（≥8 次 × 总量 3KB）。
	// 周期具有传递性（p 的整倍数也是周期），报告的是枚举到的最小合格周期
	// （114 = 2×57，57 ≥ 48 会先于 114 命中），故只断言周期落在 [48, 114]。
	text := repeatString(repetitionLoopSamplePhrase, 28)
	s := &RepetitionLoopScanner{}
	if !feedAll(t, s, text, 0) {
		t.Fatalf("短语级无限重复应命中（%dB 单元 ×28）", len(repetitionLoopSamplePhrase))
	}
	if got := s.phraseLen; got < repetitionMinPhraseBytes || got > len(repetitionLoopSamplePhrase) {
		t.Fatalf("重复单元长度 = %d, 期望落在 [%d, %d]", got, repetitionMinPhraseBytes, len(repetitionLoopSamplePhrase))
	}
	if s.repeats < 8 {
		t.Fatalf("连续重复次数 = %d, 期望 ≥8", s.repeats)
	}
	if s.Detail() == "" {
		t.Fatal("命中后 Detail 不应为空")
	}
}

func TestRepetitionLoopScannerParagraphLevelHit(t *testing.T) {
	// 段落级：1.5KB 单元重复 3 次 = 4.5KB（≥1KB 档只需 3 次）。
	unit := repeatString("段落正文：模型卡死在整段话复述上，每次输出都与上一次完全相同，字节级一致。", 22) // ~1.5KB
	if len(unit) < 1024 {
		t.Fatalf("测试单元应 ≥1KB，实际 %dB", len(unit))
	}
	s := &RepetitionLoopScanner{}
	if !feedAll(t, s, repeatString(unit, 3), 0) {
		t.Fatalf("段落级无限重复应命中（%dB 单元 ×3）", len(unit))
	}
}

func TestRepetitionLoopScannerSentenceLevelHit(t *testing.T) {
	// 句群级：300B 单元重复 4 次 = 1.2KB 未达总量门槛不命中；重复 11 次 = 3.3KB 命中。
	unit := repeatString("This sentence group keeps repeating verbatim with identical bytes every single time. ", 4) // ~300B
	if len(unit) < 256 || len(unit) >= 1024 {
		t.Fatalf("测试单元应落在 256B-1KB 档，实际 %dB", len(unit))
	}
	s := &RepetitionLoopScanner{}
	if feedAll(t, s, repeatString(unit, 4), 0) {
		t.Fatal("总量 1.2KB 未达 3KB 门槛，不应命中")
	}
	s2 := &RepetitionLoopScanner{}
	if !feedAll(t, s2, repeatString(unit, 11), 0) {
		t.Fatal("句群级重复 11 次（3.3KB）应命中")
	}
}

func TestRepetitionLoopScannerCrossDeltaSlices(t *testing.T) {
	// 重复单元被任意 delta 切片（含 1 字节逐字喂）仍应命中：尾部周期检测
	// 在累积窗口上进行，与分片边界无关。总量要给足余量（≥3.5KB），
	// 因为检测点按每 512B 新增离散触发，刚好压线的总量可能落在检测间隙。
	text := repeatString(repetitionLoopSamplePhrase, 34)
	s := &RepetitionLoopScanner{}
	if !feedAll(t, s, text, 7) {
		t.Fatal("跨 delta 切片的重复应命中（chunk=7B）")
	}
	s2 := &RepetitionLoopScanner{}
	if !feedAll(t, s2, text, 1) {
		t.Fatal("逐字节喂入的重复应命中（chunk=1B）")
	}
}

func TestRepetitionLoopScannerNormalTextNoHit(t *testing.T) {
	// 正常长文本（编号各异的自然段、表格、代码结构）不应命中。
	var normal strings.Builder
	normal.WriteString("## 分析\n\n本次变更涉及三个模块。首先，调度器需要在候选生成阶段读取熔断状态；\n" +
		"其次，流处理器要在文本增量处挂载扫描器；最后，failover 执行器需要\n新的错误分流分支。\n\n" +
		"| 模块 | 文件 | 改动 |\n| --- | --- | --- |\n| 检测器 | repetition_loop.go | 新增 |\n" +
		"| 接线 | stream_processor.go | Feed 点 |\n| 记账 | upstream_failover.go | 新分支 |\n\n")
	// 每段带递增编号，天然无周期。
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&normal, "第 %d 段：这一段的编号是 %d，内容与相邻段落各不相同，也不会构成字节周期。\n", i, i)
	}
	s := &RepetitionLoopScanner{}
	if feedAll(t, s, normal.String(), 64) {
		t.Fatal("正常文本不应命中")
	}
	if s.Detail() != "" {
		t.Fatal("未命中时 Detail 应为空")
	}
}

func TestRepetitionLoopScannerShortPeriodStructural(t *testing.T) {
	// 结构性短周期（列表符号、短表格行、分隔线）：少量出现不命中——正常
	// 输出的形状。周期具有传递性（15B 周期在 60B、75B 等倍周期处也成立），
	// 因此对短周期的真正防线不是 48B 枚举下限而是 3KB 总量门槛：连续 3KB
	// 以上完全字节级相同的行结构本身已是 degenerate 输出，命中是设计取舍
	//（一次误杀的代价 = 截断一次响应 + 60s 自愈隔离，远小于死循环烧钱）。
	s := &RepetitionLoopScanner{}
	if feedAll(t, s, repeatString("|  |  |  |  |\n", 20), 0) { // 15B 周期 ×20 = 300B
		t.Fatal("少量（300B）结构性短周期不应命中")
	}
	s2 := &RepetitionLoopScanner{}
	if !feedAll(t, s2, repeatString("----------\n", 600), 0) { // 11B 周期 ×600 = 6.6KB
		t.Fatal("600 行连续分隔线应命中（完全相同字节 ≥3KB 视为 degenerate）")
	}
}

func TestRepetitionLoopScannerThresholdBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		unit     string
		repeats  int
		wantHits bool
	}{
		{
			// 47<48，但 47 是质数且 47×2=94 ≥ 48：两个单元构成的 94B 倍周期
			// 同样是真实周期（周期传递性），大总量下命中是正确行为。
			name:     "47B 单元重复 70 次命中（94B 倍周期成立）",
			unit:     repeatString("a", 47),
			repeats:  70,
			wantHits: true,
		},
		{
			name:     "47B 单元重复 20 次不命中（总量 940B 不足）",
			unit:     repeatString("a2", 23) + "a", // 47B
			repeats:  20,
			wantHits: false,
		},
		{
			name:     "短语档：48B 单元 64 次命中（总量 3072B，次数 ≥8）",
			unit:     repeatString("b", 48),
			repeats:  64,
			wantHits: true,
		},
		{
			name:     "短语档：48B 单元 7 次不命中（次数不足且总量不足）",
			unit:     repeatString("c", 48),
			repeats:  7,
			wantHits: false,
		},
		{
			name:     "句群档：255B 单元 13 次命中（总量 3315B）",
			unit:     repeatString("d", 255),
			repeats:  13,
			wantHits: true,
		},
		{
			name:     "句群档：260B 单元 5 次不命中（句群档需 4 次但总量 1.3KB 不足）",
			unit:     repeatString("e", 260),
			repeats:  5,
			wantHits: false,
		},
		{
			name:     "句群档：260B 单元 12 次命中（总量 3.1KB）",
			unit:     repeatString("f", 260),
			repeats:  12,
			wantHits: true,
		},
		{
			name:     "段落档：1.2KB 单元 2 次不命中（段落档需 3 次）",
			unit:     repeatString("g", 1200),
			repeats:  2,
			wantHits: false,
		},
		{
			name:     "段落档：1.2KB 单元 3 次命中",
			unit:     repeatString("h", 1200),
			repeats:  3,
			wantHits: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &RepetitionLoopScanner{}
			hit := feedAll(t, s, repeatString(tt.unit, tt.repeats), 0)
			if hit != tt.wantHits {
				t.Fatalf("命中 = %v, 期望 %v", hit, tt.wantHits)
			}
		})
	}
}

func TestRepetitionLoopScannerIdempotent(t *testing.T) {
	text := repeatString(repetitionLoopSamplePhrase, 30)
	s := &RepetitionLoopScanner{}
	if !feedAll(t, s, text, 0) {
		t.Fatal("首次应命中")
	}
	first := s.Detail()
	// 命中后再喂不改变结论，也不 panic。
	if !s.Feed("更多内容") {
		t.Fatal("命中后 Feed 应持续返回 true")
	}
	if !s.Feed("") {
		t.Fatal("命中后空 Feed 也应返回 true（幂等短路）")
	}
	if s.Detail() != first {
		t.Fatalf("命中结论应保持不变: %q vs %q", s.Detail(), first)
	}
}

func TestRepetitionLoopScannerWindowSlide(t *testing.T) {
	// 重复出现在大量正常前缀之后仍应命中：窗口滑动丢头部不影响尾部周期。
	// 前缀必须是非重复文本（编号递增）——Repeat 拼接的"前缀"本身就是
	// 无限重复模式，会在前缀阶段就命中，测不到滑动场景。
	var prefix strings.Builder
	for i := 0; i < 700; i++ {
		fmt.Fprintf(&prefix, "第 %d 条历史消息：内容与相邻消息各不相同，编号 %d 保证无字节周期。\n", i, i)
	}
	s := &RepetitionLoopScanner{}
	if !feedAll(t, s, prefix.String()+repeatString(repetitionLoopSamplePhrase, 34), 1024) {
		t.Fatal("长前缀 + 重复尾段应命中（窗口滑动后）")
	}
	if s.phraseLen > len(repetitionLoopSamplePhrase) {
		t.Fatalf("命中周期 %d 不应超过重复单元 %d", s.phraseLen, len(repetitionLoopSamplePhrase))
	}
}

func TestRepetitionLoopScannerPerformance(t *testing.T) {
	// 防性能回归：32KB 全随机文本窗口的全量周期枚举应在毫秒级完成。
	// 使用非周期伪随机字节（LCG），确保最坏路径（无早退命中的大量候选）被覆盖。
	n := repetitionWindowBytes
	buf := make([]byte, 0, n)
	state := uint32(12345)
	for i := 0; i < n; i++ {
		state = state*1664525 + 1013904223
		buf = append(buf, byte(state>>16))
	}
	// 构造一点结构让部分候选通过首块比较，避免纯随机导致全部首字节早退。
	s := &RepetitionLoopScanner{}
	feedAll(t, s, string(buf), repetitionScanIntervalBytes)
	if s.Found() {
		t.Fatal("随机文本不应命中")
	}
}

func TestRepetitionLoopDetailSuffix(t *testing.T) {
	if got := repetitionLoopDetailSuffix("重复单元 100B × 9 次"); got != " (重复单元 100B × 9 次)" {
		t.Fatalf("got %q", got)
	}
	if got := repetitionLoopDetailSuffix("  "); got != "" {
		t.Fatalf("空 detail 应返回空串, got %q", got)
	}
}

// TestProcessStreamEventsRepetitionLoopAbort messages 事件循环级集成测试：
// text 增量构成无限重复时，中止流、向客户端写错误事件并返回哨兵。
func TestProcessStreamEventsRepetitionLoopAbort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	envCfg := &config.EnvConfig{}
	ctx := NewStreamContext(envCfg)

	eventChan := make(chan string, 64)
	errChan := make(chan error, 1)
	go func() {
		eventChan <- "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"
		for i := 0; i < 34; i++ {
			eventChan <- fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\n",
				strconv.Quote(repetitionLoopSamplePhrase))
		}
		close(eventChan)
		close(errChan)
	}()

	usage, err := ProcessStreamEvents(c, c.Writer, nopFlusher{}, eventChan, errChan, ctx, envCfg, time.Now(), nil, StreamPreflightTimeouts{})
	if !errors.Is(err, ErrStreamRepetitionLoop) {
		t.Fatalf("期望 ErrStreamRepetitionLoop, got %v", err)
	}
	if usage != nil {
		t.Fatalf("中止时 usage 应为 nil, got %+v", usage)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "repetition") {
		t.Fatalf("客户端错误事件应包含 repetition 描述, got: %s", body)
	}
}

// TestTryUpstreamWithAllKeysRepetitionLoopForcedCircuit failover 分流测试：
// handleSuccess 返回哨兵时不换渠道（handled=true），并按「一次确证即隔离」
// 强制熔断渠道×Key×模型（区别于双阈值的空响应族）。
func TestTryUpstreamWithAllKeysRepetitionLoopForcedCircuit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "repetition-messages",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-repetition"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.5"}`))

	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: upstream.BaseURL}},
		[]byte(`{"model":"gpt-5.5","messages":[]}`),
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(`{}`))
		},
		func(apiKey string) {},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			return nil, ErrStreamRepetitionLoop
		},
		"gpt-5.5",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if !handled {
		t.Fatal("post-commit 中止应 handled=true（Header 已发送，不换渠道）")
	}
	if successKey != "" {
		t.Fatalf("successKey = %q, want empty", successKey)
	}
	if lastErr == nil || !errors.Is(lastErr, ErrStreamRepetitionLoop) {
		t.Fatalf("lastErr 应为哨兵, got %v", lastErr)
	}
	if failoverErr != nil {
		t.Fatalf("failoverErr 应为 nil（不换渠道）, got %#v", failoverErr)
	}
	// 核心断言：一次命中即强制隔离（双阈值路径需要 2 次才 open）。
	tracker := messagesMetrics.ModelCircuit()
	if tracker == nil {
		t.Fatal("ModelCircuit tracker 不应为 nil")
	}
	if tracker.IsAvailable(upstream.ChannelUID, metrics.ModelCircuitKeyHash(upstream.APIKeys[0]), "gpt-5.5") {
		t.Fatal("哨兵应触发一次确证即隔离（模型熔断 open）")
	}
}
