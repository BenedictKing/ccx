package quota

import (
	"net/http"
	"testing"
)

// ── Manager 集成测试 ──

func TestManagerGetChannelState(t *testing.T) {
	m := NewManager()

	// 不存在的渠道 → unknown
	state := m.GetChannelState("ch_nonexistent")
	if state.Status != TruthUnknown {
		t.Errorf("nonexistent channel status = %v, want unknown", state.Status)
	}
	if state.OverallHeadroom() != 0.5 {
		t.Errorf("nonexistent channel headroom = %v, want 0.5", state.OverallHeadroom())
	}

	// nil manager → unknown
	var nilM *Manager
	state = nilM.GetChannelState("ch_test")
	if state.Status != TruthUnknown {
		t.Errorf("nil manager status = %v, want unknown", state.Status)
	}
}

func TestManagerUpdateProviderAPI(t *testing.T) {
	m := NewManager()

	values := []Value{
		{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(8000)},
	}
	m.UpdateChannelProviderAPI("ch_test", "acc_test", values, nil)

	state := m.GetChannelState("ch_test")
	if state.Status != TruthHealthy {
		t.Errorf("status = %v, want healthy", state.Status)
	}
	if state.AccountUID != "acc_test" {
		t.Errorf("accountUID = %v, want acc_test", state.AccountUID)
	}
	if !state.Supported {
		t.Error("should be supported")
	}
	// 验证来源是 provider_api
	if v, ok := state.Values[DimTokens]; !ok || v.Source != SourceProviderAPI {
		t.Error("tokens source should be provider_api")
	}
}

func TestManagerUpdateResponseHeaders(t *testing.T) {
	m := NewManager()

	headers := make(http.Header)
	headers.Set("anthropic-ratelimit-input-tokens-limit", "50000")
	headers.Set("anthropic-ratelimit-input-tokens-remaining", "45000")

	m.UpdateChannelResponseHeaders("ch_anthropic", "acc_ant", "anthropic", headers)

	state := m.GetChannelState("ch_anthropic")
	if state.Status != TruthHealthy {
		t.Errorf("status = %v, want healthy", state.Status)
	}
	if state.AccountUID != "acc_ant" {
		t.Errorf("accountUID = %v, want acc_ant", state.AccountUID)
	}
}

func TestManagerSourcePriority(t *testing.T) {
	m := NewManager()

	// 先注入 configured 级（用 input_tokens 维度，与 anthropic 响应头维度一致）
	configured := []Value{
		{Dimension: DimInputTokens, Limit: ptrF(10000), Remaining: ptrF(5000), Source: SourceConfigured},
	}
	m.UpdateChannelConfigured("ch_test", "acc_test", configured)

	state := m.GetChannelState("ch_test")
	v := state.Values[DimInputTokens]
	if v.Source != SourceConfigured {
		t.Fatalf("source after configured = %v, want configured", v.Source)
	}

	// 再注入 response_headers 级（更高优先级，同样的 input_tokens 维度）
	headers := make(http.Header)
	headers.Set("anthropic-ratelimit-input-tokens-limit", "8000")
	headers.Set("anthropic-ratelimit-input-tokens-remaining", "2000")
	m.UpdateChannelResponseHeaders("ch_test", "acc_test", "anthropic", headers)

	state = m.GetChannelState("ch_test")
	v = state.Values[DimInputTokens]
	// response_headers 优先级高于 configured，应当被覆盖
	if v.Source != SourceResponseHeaders {
		t.Errorf("source after response_headers = %v, want response_headers", v.Source)
	}
}

func TestManagerIsChannelSaturated(t *testing.T) {
	m := NewManager()

	// 无数据 → 不饱和
	if m.IsChannelSaturated("ch_test", 1000) {
		t.Error("empty channel should not be saturated")
	}

	// 注入接近耗尽的数据
	values := []Value{
		{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(500)},
	}
	m.UpdateChannelProviderAPI("ch_test", "acc_test", values, nil)

	state := m.GetChannelState("ch_test")
	if state.Status != TruthApproachingLimit {
		t.Fatalf("status = %v, want approaching_limit", state.Status)
	}

	// 接近上限时 IsChannelSaturated = true（approaching_limit 也算饱和用于排序）
	if !m.IsChannelSaturated("ch_test", 1000) {
		t.Error("approaching_limit channel should be considered saturated for ranking")
	}
}

func TestManagerChannelSaturationRank(t *testing.T) {
	m := NewManager()

	// unknown → -1
	if rank := m.ChannelSaturationRank("ch_unknown", 1000); rank != -1 {
		t.Errorf("unknown rank = %d, want -1", rank)
	}

	// healthy → 0
	values := []Value{
		{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(8000)},
	}
	m.UpdateChannelProviderAPI("ch_healthy", "acc_healthy", values, nil)
	if rank := m.ChannelSaturationRank("ch_healthy", 1000); rank != 0 {
		t.Errorf("healthy rank = %d, want 0", rank)
	}

	// exhausted → 2
	exhausted := []Value{
		{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(0)},
	}
	m.UpdateChannelProviderAPI("ch_exhausted", "acc_exhausted", exhausted, nil)
	if rank := m.ChannelSaturationRank("ch_exhausted", 1000); rank != 2 {
		t.Errorf("exhausted rank = %d, want 2", rank)
	}
}

func TestManagerHeadroomConsumedBySmartRouter(t *testing.T) {
	// 验证 SmartRouter 消费点（GetChannelHeadroom）的行为
	m := NewManager()

	// 无数据 → 0.5（中性分，不惩罚）
	if h := m.GetChannelHeadroom("ch_new"); h != 0.5 {
		t.Errorf("new channel headroom = %v, want 0.5 (neutral)", h)
	}

	// 健康 → 高 headroom
	healthy := []Value{
		{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(9000)},
	}
	m.UpdateChannelProviderAPI("ch_healthy", "acc_h", healthy, nil)
	if h := m.GetChannelHeadroom("ch_healthy"); h < 0.7 {
		t.Errorf("healthy headroom = %v, want >= 0.7", h)
	}

	// 耗尽 → 低 headroom
	exhausted := []Value{
		{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(100)},
	}
	m.UpdateChannelProviderAPI("ch_low", "acc_l", exhausted, nil)
	if h := m.GetChannelHeadroom("ch_low"); h > 0.3 {
		t.Errorf("low headroom = %v, want <= 0.3", h)
	}
}

func TestManagerNilSafety(t *testing.T) {
	var m *Manager

	// 所有方法都应该 fail-open，不 panic
	m.GetChannelState("ch_test")
	m.GetChannelHeadroom("ch_test")
	m.GetChannelTruth("ch_test")
	m.UpdateChannelProviderAPI("ch_test", "acc_test", nil, nil)
	m.UpdateChannelResponseHeaders("ch_test", "acc_test", "openai", nil)
	m.UpdateChannelConfigured("ch_test", "acc_test", nil)
	m.IsChannelSaturated("ch_test", 0)
	m.ChannelSaturationRank("ch_test", 0)
	if m.Buckets() != nil {
		t.Error("nil manager buckets should be nil")
	}
}
