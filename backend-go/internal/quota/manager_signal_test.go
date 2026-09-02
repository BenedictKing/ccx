package quota

import (
	"net/http"
	"testing"
	"time"
)

// UpdateFromUpstreamSignal 按 serviceType 推导 provider 头映射后更新渠道配额状态。
// 未映射的协议族（gemini/images/vectors 无已确认头映射）必须 fail-open 跳过。
func TestUpdateFromUpstreamSignal(t *testing.T) {
	anthropicHeaders := http.Header{}
	anthropicHeaders.Set("anthropic-ratelimit-requests-limit", "50")
	anthropicHeaders.Set("anthropic-ratelimit-requests-remaining", "45")

	openAIHeaders := http.Header{}
	openAIHeaders.Set("x-ratelimit-limit-requests", "200")
	openAIHeaders.Set("x-ratelimit-remaining-requests", "10")

	tests := []struct {
		name        string
		channelUID  string
		serviceType string
		headers     http.Header
		wantDim     Dimension
		wantNil     bool
		wantLimit   float64
		wantRemain  float64
	}{
		{
			name:        "Messages 渠道解析 anthropic 头",
			channelUID:  "ch-msg",
			serviceType: "Messages",
			headers:     anthropicHeaders,
			wantDim:     DimRequests,
			wantLimit:   50,
			wantRemain:  45,
		},
		{
			name:        "Chat 渠道解析 openai 头",
			channelUID:  "ch-chat",
			serviceType: "Chat",
			headers:     openAIHeaders,
			wantDim:     DimRequests,
			wantLimit:   200,
			wantRemain:  10,
		},
		{
			name:        "Responses 渠道解析 openai 头",
			channelUID:  "ch-resp",
			serviceType: "Responses",
			headers:     openAIHeaders,
			wantDim:     DimRequests,
			wantLimit:   200,
			wantRemain:  10,
		},
		{
			name:        "Gemini 渠道无已确认头映射，跳过",
			channelUID:  "ch-gem",
			serviceType: "Gemini",
			headers:     openAIHeaders,
			wantNil:     true,
		},
		{
			name:        "Images 渠道跳过",
			channelUID:  "ch-img",
			serviceType: "Images",
			headers:     anthropicHeaders,
			wantNil:     true,
		},
		{
			name:        "空 channelUID 跳过",
			channelUID:  "",
			serviceType: "Messages",
			headers:     anthropicHeaders,
			wantNil:     true,
		},
		{
			name:        "nil headers 跳过",
			channelUID:  "ch-nil",
			serviceType: "Messages",
			headers:     nil,
			wantNil:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManager()
			m.UpdateFromUpstreamSignal(tt.channelUID, "ep-test", tt.serviceType, tt.headers)

			if tt.wantNil {
				state := m.GetChannelState(tt.channelUID)
				if len(state.Values) != 0 {
					t.Fatalf("不应产生配额数据, got %+v", state.Values)
				}
				return
			}

			state := m.GetChannelState(tt.channelUID)
			v, ok := state.Values[tt.wantDim]
			if !ok {
				t.Fatalf("维度 %s 缺失: %+v", tt.wantDim, state.Values)
			}
			if v.Limit == nil || *v.Limit != tt.wantLimit {
				t.Fatalf("limit = %+v, want %v", v.Limit, tt.wantLimit)
			}
			if v.Remaining == nil || *v.Remaining != tt.wantRemain {
				t.Fatalf("remaining = %+v, want %v", v.Remaining, tt.wantRemain)
			}
			if v.Source != SourceResponseHeaders {
				t.Fatalf("source = %v, want response_headers", v.Source)
			}
			if state.AccountUID != "ep-test" {
				t.Fatalf("accountUID = %q, want ep-test（饱和桶按 endpoint 聚合）", state.AccountUID)
			}
		})
	}
}

// 协议族映射错误方向的数据不得串味：Messages 渠道收到 openai 头不应产生数据，
// 反之亦然（只信显式确认的头名归属）。
func TestUpdateFromUpstreamSignal_ProtocolMismatchIgnored(t *testing.T) {
	m := NewManager()

	openAIHeaders := http.Header{}
	openAIHeaders.Set("x-ratelimit-limit-requests", "200")
	openAIHeaders.Set("x-ratelimit-remaining-requests", "10")
	m.UpdateFromUpstreamSignal("ch-x", "ep-x", "Messages", openAIHeaders)

	if state := m.GetChannelState("ch-x"); len(state.Values) != 0 {
		t.Fatalf("Messages 渠道不应解析 openai 头: %+v", state.Values)
	}
}

// nil manager 安全（fail-open 红线：配额系统故障不影响调用方）。
func TestUpdateFromUpstreamSignal_NilManager(t *testing.T) {
	var m *Manager
	m.UpdateFromUpstreamSignal("ch", "ep", "Messages", http.Header{})
}

// 耗尽信号经信号挂点进入后，饱和桶按 endpoint 聚合且尊重 reset 懒重置。
func TestUpdateFromUpstreamSignal_ExhaustedBucketLazyReset(t *testing.T) {
	m := NewManager()
	resetAt := time.Now().Add(time.Minute)

	headers := http.Header{}
	headers.Set("anthropic-ratelimit-requests-limit", "100")
	headers.Set("anthropic-ratelimit-requests-remaining", "0")
	headers.Set("anthropic-ratelimit-requests-reset", resetAt.Format(time.RFC3339))
	m.UpdateFromUpstreamSignal("ch-ex", "ep-ex", "Messages", headers)

	if truth := m.GetChannelTruth("ch-ex"); truth != TruthExhausted {
		t.Fatalf("truth = %v, want exhausted", truth)
	}
	if !m.IsChannelSaturated("ch-ex", time.Now().UnixMilli()) {
		t.Fatal("窗口内应饱和")
	}
	if m.IsChannelSaturated("ch-ex", resetAt.Add(time.Second).UnixMilli()) {
		t.Fatal("窗口翻过去后应懒重置恢复")
	}
}
