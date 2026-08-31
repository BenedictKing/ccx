package autopilot

import "testing"

// ── SeverityClassRequestShape 请求形状判定 ──

func TestSeverityClassRequestShape(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		// messages 形态：stop_sequences 数组含 </severity>
		{
			name: "messages-数组含标记",
			body: `{"model":"claude-sonnet-5","max_tokens":64,"stop_sequences":["</severity>"],"messages":[{"role":"user","content":"x"}]}`,
			want: true,
		},
		// openai chat 形态：stop 字符串
		{
			name: "chat-stop字符串",
			body: `{"model":"gpt-x","stop":"</severity>"}`,
			want: true,
		},
		// openai chat 形态：stop 数组
		{
			name: "chat-stop数组",
			body: `{"model":"gpt-x","stop":["END","</severity>"]}`,
			want: true,
		},
		// 其他停止序列：不误判
		{
			name: "其他停止序列",
			body: `{"model":"m","stop_sequences":["\n\nHuman:"]}`,
			want: false,
		},
		// 正文提及标记但停止序列未设置：不误判（预筛通过但字段不命中）
		{
			name: "正文提及但无停止序列",
			body: `{"model":"m","system":"respond with </severity> tag","stop_sequences":["<stop>"]}`,
			want: false,
		},
		// 普通请求（快速路径直接排除）
		{
			name: "普通请求",
			body: `{"model":"m","messages":[{"role":"user","content":"hello"}]}`,
			want: false,
		},
		// 空/非法输入
		{name: "空体", body: "", want: false},
		{name: "非法JSON含标记字节", body: `not json </severity>`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SeverityClassRequestShape([]byte(tt.body)); got != tt.want {
				t.Fatalf("SeverityClassRequestShape() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ── learnedSeverityClassUnsupported 路由侧读取 ──

func TestLearnedSeverityClassUnsupported(t *testing.T) {
	orig := learnedSeverityClassUnsupportedLookup
	defer func() { learnedSeverityClassUnsupportedLookup = orig }()

	learned := map[string]bool{}
	learnedSeverityClassUnsupportedLookup = func(channelUID, model string) bool {
		return learned[channelUID+"|"+model]
	}

	if learnedSeverityClassUnsupported("ch_a", "m1") {
		t.Fatal("无记忆时应 fail-open 返回 false")
	}
	if learnedSeverityClassUnsupported("", "m1") || learnedSeverityClassUnsupported("ch_a", "") {
		t.Fatal("空参数应直接返回 false")
	}

	learned["ch_a|m1"] = true
	if !learnedSeverityClassUnsupported("ch_a", "m1") {
		t.Fatal("有记忆时应返回 true")
	}
	if learnedSeverityClassUnsupported("ch_a", "m2") {
		t.Fatal("其他模型不应命中")
	}
}

// ── filterSeverityClassCapable 候选过滤 ──

func TestFilterSeverityClassCapable(t *testing.T) {
	orig := learnedSeverityClassUnsupportedLookup
	defer func() { learnedSeverityClassUnsupportedLookup = orig }()
	learnedSeverityClassUnsupportedLookup = func(channelUID, model string) bool {
		return channelUID == "ch_a" && model == "bad-model"
	}

	profiles := []ModelProfile{
		{ModelID: "bad-model"},
		{ModelID: "good-model"},
		{ModelID: "also-good"},
	}
	got := filterSeverityClassCapable(profiles, "ch_a")
	if len(got) != 2 || got[0].ModelID != "good-model" || got[1].ModelID != "also-good" {
		t.Fatalf("应剔除 bad-model，保留其余，got %v", got)
	}

	// 全部被剔除时返回空列表（由调用方落入 no_capable_model 语义）
	all := []ModelProfile{{ModelID: "bad-model"}}
	if got := filterSeverityClassCapable(all, "ch_a"); len(got) != 0 {
		t.Fatalf("全部剔除时应为空，got %v", got)
	}
}
