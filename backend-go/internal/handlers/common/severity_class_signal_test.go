package common

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── SeverityTagScanner 跨增量检测 ──

func TestSeverityTagScanner(t *testing.T) {
	tests := []struct {
		name  string
		feeds []string
		want  bool
	}{
		{name: "整体命中", feeds: []string{"<severity>1</severity>"}, want: true},
		{name: "跨增量切开", feeds: []string{"评分结果：<sev", "erity>2</sev", "erity>"}, want: true},
		{name: "无标记", feeds: []string{"分类器暂时不可用，稍等片刻后重试。", "Tool Call: Bash({})"}, want: false},
		{name: "空输入", feeds: []string{""}, want: false},
		{name: "命中后幂等", feeds: []string{"<severity>", "后续不再扫描也保持命中"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := &SeverityTagScanner{}
			got := false
			for _, feed := range tt.feeds {
				if scanner.Feed(feed) {
					got = true
				}
			}
			if got != tt.want || scanner.Found() != tt.want {
				t.Fatalf("Feed 累计=%v Found=%v, want %v", got, scanner.Found(), tt.want)
			}
		})
	}
}

// ── MaybeLearnSeverityClassOutcome 学习口径 ──

func TestMaybeLearnSeverityClassOutcome(t *testing.T) {
	restore := config.SwapSharedChannelCompatCacheForTest(config.NewChannelCompatCache())
	defer restore()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	upstream := &config.UpstreamConfig{ChannelUID: "ch_test", Name: "test-channel"}
	shapeBody := []byte(`{"model":"m1","max_tokens":64,"stop_sequences":["</severity>"],"messages":[]}`)
	normalBody := []byte(`{"model":"m1","messages":[]}`)

	cache := config.SharedChannelCompatCache()

	// 流式出错：不学习
	MaybeLearnSeverityClassOutcome(c, upstream, "sk-key", "m1", shapeBody, false, errNonNil())
	if cache.IsSeverityClassUnsupportedForChannelModel("ch_test", "m1") {
		t.Fatal("流式出错时不应学习")
	}
	// 非分类形状：不学习
	MaybeLearnSeverityClassOutcome(c, upstream, "sk-key", "m1", normalBody, false, nil)
	if cache.IsSeverityClassUnsupportedForChannelModel("ch_test", "m1") {
		t.Fatal("非分类形状请求不应学习")
	}
	// 分类形状 + 正常完成 + 无标记：学负结论
	MaybeLearnSeverityClassOutcome(c, upstream, "sk-key", "m1", shapeBody, false, nil)
	if !cache.IsSeverityClassUnsupportedForChannelModel("ch_test", "m1") {
		t.Fatal("分类请求正常完成但无标记应学负结论")
	}
	// 分类形状 + 正常完成 + 有标记：翻转能力确认
	MaybeLearnSeverityClassOutcome(c, upstream, "sk-key", "m1", shapeBody, true, nil)
	if cache.IsSeverityClassUnsupportedForChannelModel("ch_test", "m1") {
		t.Fatal("输出含标记应清除负结论")
	}
	// 缺渠道身份：不学习
	MaybeLearnSeverityClassOutcome(c, &config.UpstreamConfig{}, "sk-key", "m1", shapeBody, false, nil)
	if cache.IsSeverityClassUnsupportedForChannelModel("", "m1") {
		t.Fatal("空 ChannelUID 不应学习")
	}
}

// errNonNil 返回任意非 nil 错误，避免测试文件引入 errors 仅为这一处。
func errNonNil() error { return &fakeStreamError{} }

type fakeStreamError struct{}

func (*fakeStreamError) Error() string { return "stream aborted" }

// ── 非流式扫描入口 ──

func TestMarkNonStreamSeverityScan(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 出站体经 Go json.Marshal 重写后 stop_sequences 为转义形态（\u003c/severity\u003e）
	shapeReqEscaped := []byte(`{"model":"m1","max_tokens":64,"stop_sequences":["\u003c/severity\u003e"],"messages":[]}`)
	shapeReqRaw := []byte(`{"model":"m1","max_tokens":64,"stop_sequences":["</severity>"],"messages":[]}`)
	normalReq := []byte(`{"model":"m1","messages":[]}`)

	tests := []struct {
		name        string
		req, resp   []byte
		wantScanned bool
		wantFound   bool
	}{
		{name: "分类形状+响应含原始标记", req: shapeReqRaw, resp: []byte(`{"content":[{"type":"text","text":"<severity>1"}]}`), wantScanned: true, wantFound: true},
		{name: "分类形状+响应含转义标记", req: shapeReqEscaped, resp: []byte(`{"content":[{"type":"text","text":"\u003cseverity\u003e1"}]}`), wantScanned: true, wantFound: true},
		{name: "分类形状+罐头响应无标记", req: shapeReqEscaped, resp: []byte(`{"content":[{"type":"text","text":"分类器暂时不可用，稍等片刻后重试。"}]}`), wantScanned: true, wantFound: false},
		{name: "非分类形状不扫描", req: normalReq, resp: []byte(`{"content":[{"type":"text","text":"<severity>1"}]}`), wantScanned: false, wantFound: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			MarkNonStreamSeverityScan(c, tt.req, tt.resp)
			scanned, found := NonStreamSeverityOutcome(c)
			if scanned != tt.wantScanned || found != tt.wantFound {
				t.Fatalf("scanned=%v found=%v, want %v/%v", scanned, found, tt.wantScanned, tt.wantFound)
			}
		})
	}

	// 未调用扫描入口（chat 等未接线路径）：保持未置位，挂载点据此跳过
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if scanned, found := NonStreamSeverityOutcome(c); scanned || found {
		t.Fatal("未扫描不应置位")
	}
	// nil context 防御
	if scanned, found := NonStreamSeverityOutcome(nil); scanned || found {
		t.Fatal("nil context 应返回未扫描")
	}
	// failover 后同渠道重试：后一次扫描覆盖前一次结论
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	MarkNonStreamSeverityScan(c2, shapeReqEscaped, []byte(`{"content":[{"type":"text","text":"罐头"}]}`))
	MarkNonStreamSeverityScan(c2, shapeReqEscaped, []byte(`{"content":[{"type":"text","text":"<severity>2"}]}`))
	if _, found := NonStreamSeverityOutcome(c2); !found {
		t.Fatal("重试后的扫描结论应覆盖前一次")
	}
}
