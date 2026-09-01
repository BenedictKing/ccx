package quota

import (
	"testing"
)

// ── TruthLevel + Source 基础测试 ──

func TestSourcePriority(t *testing.T) {
	tests := []struct {
		a        Source
		b        Source
		expected bool
	}{
		{SourceProviderAPI, SourceResponseHeaders, true},
		{SourceResponseHeaders, SourceConfigured, true},
		{SourceConfigured, SourceEstimated, true},
		{SourceEstimated, SourceUnknown, true},
		{SourceUnknown, SourceProviderAPI, false},
		{SourceProviderAPI, SourceProviderAPI, false},
	}
	for _, tt := range tests {
		if got := SourceHigherThan(tt.a, tt.b); got != tt.expected {
			t.Errorf("SourceHigherThan(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.expected)
		}
	}
}

func TestParseSource(t *testing.T) {
	tests := []struct {
		input    string
		expected Source
	}{
		{"provider_api", SourceProviderAPI},
		{"provider-api", SourceProviderAPI},
		{"response_headers", SourceResponseHeaders},
		{"headers", SourceResponseHeaders},
		{"configured", SourceConfigured},
		{"config", SourceConfigured},
		{"estimated", SourceEstimated},
		{"unknown", SourceUnknown},
		{"garbage", SourceUnknown},
		{"", SourceUnknown},
	}
	for _, tt := range tests {
		if got := ParseSource(tt.input); got != tt.expected {
			t.Errorf("ParseSource(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseTruthLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected TruthLevel
	}{
		{"healthy", TruthHealthy},
		{"approaching_limit", TruthApproachingLimit},
		{"approaching", TruthApproachingLimit},
		{"exhausted", TruthExhausted},
		{"unavailable", TruthUnavailable},
		{"unknown", TruthUnknown},
		{"garbage", TruthUnknown},
	}
	for _, tt := range tests {
		if got := ParseTruthLevel(tt.input); got != tt.expected {
			t.Errorf("ParseTruthLevel(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// ── Value.Headroom 测试 ──

func ptrF(v float64) *float64 {
	return &v
}

func TestValueHeadroom(t *testing.T) {
	tests := []struct {
		name     string
		value    Value
		expected float64
	}{
		{
			name:     "remaining/limit healthy",
			value:    Value{Remaining: ptrF(800), Limit: ptrF(1000)},
			expected: 0.8,
		},
		{
			name:     "remaining/limit exhausted",
			value:    Value{Remaining: ptrF(0), Limit: ptrF(1000)},
			expected: 0.0,
		},
		{
			name:     "remaining/limit negative",
			value:    Value{Remaining: ptrF(-50), Limit: ptrF(1000)},
			expected: 0.0,
		},
		{
			name:     "remaining/limit over 100%",
			value:    Value{Remaining: ptrF(1500), Limit: ptrF(1000)},
			expected: 1.0, // 钳制到 1.0
		},
		{
			name:     "used/limit healthy",
			value:    Value{Used: ptrF(200), Limit: ptrF(1000)},
			expected: 0.8,
		},
		{
			name:     "used/limit exhausted",
			value:    Value{Used: ptrF(1000), Limit: ptrF(1000)},
			expected: 0.0,
		},
		{
			name:     "used/limit over limit",
			value:    Value{Used: ptrF(1200), Limit: ptrF(1000)},
			expected: 0.0,
		},
		{
			name:     "no data → neutral 0.5",
			value:    Value{},
			expected: 0.5,
		},
		{
			name:     "zero limit → neutral 0.5",
			value:    Value{Remaining: ptrF(0), Limit: ptrF(0)},
			expected: 0.5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.Headroom(); !floatNear(got, tt.expected, 0.001) {
				t.Errorf("Headroom() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestValueIsExhausted(t *testing.T) {
	tests := []struct {
		name  string
		value Value
		want  bool
	}{
		{"remaining zero", Value{Remaining: ptrF(0), Limit: ptrF(1000)}, true},
		{"remaining negative", Value{Remaining: ptrF(-10), Limit: ptrF(1000)}, true},
		{"used >= limit", Value{Used: ptrF(1000), Limit: ptrF(1000)}, true},
		{"used > limit", Value{Used: ptrF(1200), Limit: ptrF(1000)}, true},
		{"healthy remaining", Value{Remaining: ptrF(500), Limit: ptrF(1000)}, false},
		{"healthy used", Value{Used: ptrF(300), Limit: ptrF(1000)}, false},
		{"no data", Value{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.IsExhausted(); got != tt.want {
				t.Errorf("IsExhausted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValueIsApproaching(t *testing.T) {
	tests := []struct {
		name      string
		value     Value
		threshold float64
		want      bool
	}{
		{"at 20% threshold", Value{Remaining: ptrF(200), Limit: ptrF(1000)}, 0.2, true},
		{"below threshold", Value{Remaining: ptrF(100), Limit: ptrF(1000)}, 0.2, true},
		{"above threshold", Value{Remaining: ptrF(500), Limit: ptrF(1000)}, 0.2, false},
		{"no data → false", Value{}, 0.2, false},
		{"exhausted counts as approaching too", Value{Remaining: ptrF(0), Limit: ptrF(1000)}, 0.2, false}, // IsApproaching 不包含已耗尽
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.IsApproaching(tt.threshold); got != tt.want {
				t.Errorf("IsApproaching() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ── ChannelState 测试 ──

func TestChannelStateMergeValues(t *testing.T) {
	cs := NewChannelState("ch_test")

	// 先注入 configured 级数据
	cs.MergeValues([]Value{
		{Dimension: DimTokens, Limit: ptrF(1000), Used: ptrF(300), Source: SourceConfigured},
	})
	if cs.Status != TruthHealthy {
		t.Errorf("after configured: status = %v, want healthy", cs.Status)
	}

	// 再注入更高优先级的 response_headers 数据（同一维度）
	cs.MergeValues([]Value{
		{Dimension: DimTokens, Remaining: ptrF(100), Limit: ptrF(1000), Source: SourceResponseHeaders},
	})
	if cs.Status != TruthApproachingLimit {
		t.Errorf("after response_headers: status = %v, want approaching_limit", cs.Status)
	}
	// 验证 source 升级了
	v := cs.Values[DimTokens]
	if v.Source != SourceResponseHeaders {
		t.Errorf("tokens source = %v, want response_headers", v.Source)
	}

	// 注入 exhausted 的另一个维度（provider_api 级）
	cs.MergeValues([]Value{
		{Dimension: DimRequests, Remaining: ptrF(0), Limit: ptrF(100), Source: SourceProviderAPI},
	})
	if cs.Status != TruthExhausted {
		t.Errorf("after exhausted requests: status = %v, want exhausted", cs.Status)
	}
}

func TestChannelStateOverallHeadroom(t *testing.T) {
	cs := NewChannelState("ch_test")

	// 空状态 → 中性 0.5
	if h := cs.OverallHeadroom(); h != 0.5 {
		t.Errorf("empty state headroom = %v, want 0.5", h)
	}

	// 单维度健康
	cs.MergeValues([]Value{
		{Dimension: DimTokens, Remaining: ptrF(800), Limit: ptrF(1000), Source: SourceConfigured},
	})
	if h := cs.OverallHeadroom(); !floatNear(h, 0.8, 0.001) {
		t.Errorf("healthy headroom = %v, want 0.8", h)
	}

	// 多维度取最小值
	cs.MergeValues([]Value{
		{Dimension: DimRequests, Remaining: ptrF(100), Limit: ptrF(1000), Source: SourceConfigured},
	})
	if h := cs.OverallHeadroom(); !floatNear(h, 0.1, 0.001) {
		t.Errorf("multi-dim headroom = %v, want 0.1 (min of 0.8 and 0.1)", h)
	}
}

func TestChannelStateUnknownVsUnavailable(t *testing.T) {
	// 不支持 → unknown
	cs := NewChannelState("ch_test")
	if cs.Status != TruthUnknown {
		t.Errorf("new state status = %v, want unknown", cs.Status)
	}

	// 支持但无数据 → unavailable
	cs.Supported = true
	cs.recomputeStatus()
	if cs.Status != TruthUnavailable {
		t.Errorf("supported no-data status = %v, want unavailable", cs.Status)
	}
}

// ── 辅助函数 ──

func floatNear(a, b, eps float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= eps
}
