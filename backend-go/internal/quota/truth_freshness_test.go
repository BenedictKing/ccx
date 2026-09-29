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
