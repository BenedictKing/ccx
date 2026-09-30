package quota

import (
	"net/http"
	"testing"
	"time"
)

// TestPruneExpiredValues verifies cross-window staleness invalidation:
// after the window flips, stale observations no longer participate in status
// calculation, and the channel returns to fail-open neutral.
func TestPruneExpiredValues(t *testing.T) {
	now := time.Now().UnixMilli()

	t.Run("窗口已过期的观测被剪除", func(t *testing.T) {
		cs := NewChannelState("ch_a")
		cs.MergeValues([]Value{
			{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(500), ResetAtMs: now - 1000, Source: SourceResponseHeaders},
		})
		if cs.Status != TruthApproachingLimit {
			t.Fatalf("before prune status = %v, want approaching_limit", cs.Status)
		}
		if !cs.hasExpiredValues(now) {
			t.Fatal("should detect expired values")
		}
		cs.pruneExpiredValues(now)
		if len(cs.Values) != 0 {
			t.Fatalf("expected empty values after prune, got %v", cs.Values)
		}
		if cs.Status != TruthUnknown {
			t.Fatalf("after prune status = %v, want unknown (fail-open)", cs.Status)
		}
	})

	t.Run("未过期的观测保留", func(t *testing.T) {
		cs := NewChannelState("ch_b")
		cs.MergeValues([]Value{
			{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(500), ResetAtMs: now + int64(time.Hour/time.Millisecond), Source: SourceResponseHeaders},
		})
		if cs.hasExpiredValues(now) {
			t.Fatal("future reset should not be expired")
		}
		cs.pruneExpiredValues(now)
		if _, ok := cs.Values[DimTokens]; !ok {
			t.Fatal("unexpired value should survive prune")
		}
	})

	t.Run("无窗口语义的余额观测永不剪除", func(t *testing.T) {
		cs := NewChannelState("ch_c")
		cs.MergeValues([]Value{
			{Dimension: DimCurrency, Remaining: ptrF(0), Source: SourceConfigured},
		})
		if cs.Status != TruthExhausted {
			t.Fatalf("balance=0 status = %v, want exhausted", cs.Status)
		}
		if cs.hasExpiredValues(now) {
			t.Fatal("ResetAtMs=0 should never expire")
		}
		cs.pruneExpiredValues(now)
		if cs.Status != TruthExhausted {
			t.Fatalf("balance exhaustion is cross-window, status = %v, want exhausted", cs.Status)
		}
	})
}

// TestManagerPruneOnRead verifies the read-path lazy pruning: a channel stuck
// in approaching_limit (no bucket recovery path) recovers automatically once
// its observation window has flipped.
func TestManagerPruneOnRead(t *testing.T) {
	m := NewManager()
	now := time.Now().UnixMilli()

	m.UpdateChannelResponseHeaders("ch_stale", "ep_1", "anthropic", headersWith(
		"anthropic-ratelimit-input-tokens-limit", "10000",
		"anthropic-ratelimit-input-tokens-remaining", "500",
	))
	if m.GetChannelTruth("ch_stale") != TruthApproachingLimit {
		t.Fatalf("status = %v, want approaching_limit", m.GetChannelTruth("ch_stale"))
	}

	// 人为把窗口翻到过去（模拟窗口重置后无新请求）
	m.mu.Lock()
	for dim, v := range m.states["ch_stale"].Values {
		v.ResetAtMs = now - 1000
		m.states["ch_stale"].Values[dim] = v
	}
	m.mu.Unlock()

	if saturated := m.IsChannelSaturated("ch_stale", now); saturated {
		t.Fatal("approaching channel should self-heal after window flip")
	}
	if got := m.GetChannelTruth("ch_stale"); got != TruthUnknown {
		t.Fatalf("truth = %v, want unknown after lazy prune", got)
	}
	if h := m.GetChannelHeadroom("ch_stale"); h != 0.5 {
		t.Fatalf("headroom = %v, want neutral 0.5 after prune", h)
	}
}

// TestSnapshotAll verifies the observability snapshot: sorted by ChannelUID,
// stale values pruned, fresh states preserved.
func TestSnapshotAll(t *testing.T) {
	m := NewManager()
	now := time.Now().UnixMilli()

	m.UpdateChannelResponseHeaders("ch_b", "ep_1", "anthropic", headersWith(
		"anthropic-ratelimit-input-tokens-limit", "10000",
		"anthropic-ratelimit-input-tokens-remaining", "9000",
	))
	m.UpdateChannelResponseHeaders("ch_a", "ep_2", "anthropic", headersWith(
		"anthropic-ratelimit-input-tokens-limit", "10000",
		"anthropic-ratelimit-input-tokens-remaining", "300",
	))

	// ch_a 窗口翻转
	m.mu.Lock()
	for dim, v := range m.states["ch_a"].Values {
		v.ResetAtMs = now - 1000
		m.states["ch_a"].Values[dim] = v
	}
	m.mu.Unlock()

	states := m.SnapshotAll()
	if len(states) != 2 {
		t.Fatalf("len = %d, want 2", len(states))
	}
	if states[0].ChannelUID != "ch_a" || states[1].ChannelUID != "ch_b" {
		t.Fatalf("snapshot not sorted: %s, %s", states[0].ChannelUID, states[1].ChannelUID)
	}
	if states[0].Status != TruthUnknown {
		t.Fatalf("ch_a status = %v, want unknown (pruned)", states[0].Status)
	}
	if states[1].Status != TruthHealthy {
		t.Fatalf("ch_b status = %v, want healthy", states[1].Status)
	}
}

func headersWith(kv ...string) http.Header {
	h := make(http.Header, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

// 窗口未知（无 reset 头）的 response_headers 观测按新鲜度兜底剪除。
// 该来源每次响应都会重写：停止更新等价于该渠道不再拿到流量，而它多半正是被这条
// 陈旧读数压分导致的，没有第二条恢复路径。provider_api（订阅轮询，默认 24h 一轮）
// 与 configured（静态声明）必须永不按 TTL 剪除。
func TestPruneStaleObservationTTL(t *testing.T) {
	now := time.Now().UnixMilli()
	ttl := staleResponseHeaderObservationTTL
	stale := now - ttl - 1
	fresh := now - ttl + 1000

	tests := []struct {
		name       string
		value      Value
		wantExpire bool
	}{
		{
			name:       "response_headers 无 reset 头且超 TTL → 剪除",
			value:      Value{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(500), ObservedAtMs: stale, Source: SourceResponseHeaders},
			wantExpire: true,
		},
		{
			name:       "TTL 边界（恰好等于 TTL）保留，方向写死为只在严格超过时剪除",
			value:      Value{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(500), ObservedAtMs: now - ttl, Source: SourceResponseHeaders},
			wantExpire: false,
		},
		{
			name:       "response_headers 未超 TTL → 保留",
			value:      Value{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(500), ObservedAtMs: fresh, Source: SourceResponseHeaders},
			wantExpire: false,
		},
		{
			name:       "response_headers 有未来 reset 时间 → 不受 TTL 影响",
			value:      Value{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(500), ResetAtMs: now + int64(time.Hour/time.Millisecond), ObservedAtMs: stale, Source: SourceResponseHeaders},
			wantExpire: false,
		},
		{
			name:       "response_headers 窗口已翻转 → 仍按 reset 剪除",
			value:      Value{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(500), ResetAtMs: now - 1000, ObservedAtMs: fresh, Source: SourceResponseHeaders},
			wantExpire: true,
		},
		{
			name:       "provider_api 观测远超 TTL 也永不剪除（订阅 24h 轮询）",
			value:      Value{Dimension: DimCurrency, Remaining: ptrF(0), ObservedAtMs: now - int64(48*time.Hour/time.Millisecond), Source: SourceProviderAPI},
			wantExpire: false,
		},
		{
			name:       "configured 静态声明永不剪除",
			value:      Value{Dimension: DimCurrency, Remaining: ptrF(0), ObservedAtMs: now - int64(48*time.Hour/time.Millisecond), Source: SourceConfigured},
			wantExpire: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := NewChannelState("ch_ttl")
			cs.Values[tt.value.Dimension] = tt.value
			cs.recomputeStatus()

			if got := cs.hasExpiredValues(now); got != tt.wantExpire {
				t.Fatalf("hasExpiredValues = %v, want %v", got, tt.wantExpire)
			}
			cs.pruneExpiredValues(now)
			_, kept := cs.Values[tt.value.Dimension]
			if kept == tt.wantExpire {
				t.Fatalf("value kept = %v, want kept = %v", kept, !tt.wantExpire)
			}
		})
	}
}

// MergeValues 是唯一的状态写入入口，必须为每条观测打上观测戳；调用方自带观测时刻时尊重原值。
func TestMergeValuesStampsObservedAt(t *testing.T) {
	cs := NewChannelState("ch_stamp")
	before := time.Now().UnixMilli()
	cs.MergeValues([]Value{
		{Dimension: DimTokens, Limit: ptrF(10), Remaining: ptrF(5), Source: SourceResponseHeaders},
	})
	after := time.Now().UnixMilli()

	got := cs.Values[DimTokens].ObservedAtMs
	if got < before || got > after {
		t.Fatalf("ObservedAtMs = %d, want within [%d, %d]", got, before, after)
	}

	explicit := Value{Dimension: DimRequests, Limit: ptrF(10), Remaining: ptrF(5), ObservedAtMs: 12345, Source: SourceResponseHeaders}
	cs.MergeValues([]Value{explicit})
	if got := cs.Values[DimRequests].ObservedAtMs; got != 12345 {
		t.Fatalf("显式观测时刻被覆盖: %d, want 12345", got)
	}
}

// 端到端：上游不再返回 reset 头、且渠道停止拿量的情况下，陈旧读数必须在读路径自愈，
// 否则 approaching_limit 会永久沉底（该状态没有任何其他恢复路径）。
func TestManagerStaleResponseHeadersSelfHealOnRead(t *testing.T) {
	m := NewManager()
	now := time.Now().UnixMilli()

	// 只给 limit/remaining，不给任何 reset 头 → ResetAtMs=0（窗口未知）
	m.UpdateChannelResponseHeaders("ch_no_reset", "ep_1", "anthropic", headersWith(
		"anthropic-ratelimit-input-tokens-limit", "10000",
		"anthropic-ratelimit-input-tokens-remaining", "500",
	))
	if m.GetChannelTruth("ch_no_reset") != TruthApproachingLimit {
		t.Fatalf("status = %v, want approaching_limit", m.GetChannelTruth("ch_no_reset"))
	}

	// 把观测时刻推到 TTL 之前（模拟该渠道此后没有拿到任何流量）
	m.mu.Lock()
	for dim, v := range m.states["ch_no_reset"].Values {
		if v.Source == SourceResponseHeaders {
			v.ObservedAtMs = now - staleResponseHeaderObservationTTL - 1
			m.states["ch_no_reset"].Values[dim] = v
		}
	}
	m.mu.Unlock()

	if m.IsChannelSaturated("ch_no_reset", now) {
		t.Fatal("超过新鲜度上界的无窗口观测必须让渠道自愈，否则永久沉底")
	}
	if got := m.GetChannelTruth("ch_no_reset"); got != TruthUnknown {
		t.Fatalf("truth = %v, want unknown after TTL prune", got)
	}
	if h := m.GetChannelHeadroom("ch_no_reset"); h != 0.5 {
		t.Fatalf("headroom = %v, want neutral 0.5 after prune", h)
	}
}
