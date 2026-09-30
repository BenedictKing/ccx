package quota

import (
	"testing"
	"time"
)

// 快照必须与内部状态完全隔离：Value 里的 Limit/Used/Remaining 都是指针，
// 只浅拷 map 会让调用方一改指针就穿透到 Manager 内部（与 DeepCopy 的契约相反）。
func TestSnapshotDeepCopyIsolatesPointers(t *testing.T) {
	cs := NewChannelState("ch_deep")
	cs.MergeValues([]Value{{
		Dimension: DimTokens,
		Limit:     ptrF(10000),
		Used:      ptrF(500),
		Remaining: ptrF(500),
		Source:    SourceResponseHeaders,
	}})

	snapshot := cs.DeepCopy()
	*snapshot.Values[DimTokens].Remaining = 0
	*snapshot.Values[DimTokens].Limit = 1
	*snapshot.Values[DimTokens].Used = 9999

	internal := cs.Values[DimTokens]
	if internal.Remaining == nil || *internal.Remaining != 500 {
		t.Fatalf("快照改 Remaining 穿透到内部: %v", internal.Remaining)
	}
	if internal.Limit == nil || *internal.Limit != 10000 {
		t.Fatalf("快照改 Limit 穿透到内部: %v", internal.Limit)
	}
	if internal.Used == nil || *internal.Used != 500 {
		t.Fatalf("快照改 Used 穿透到内部: %v", internal.Used)
	}
}

// 派生值（headroom / 饱和判定）必须与快照来自同一瞬间，且与逐渠道读接口结论一致；
// 已失效的观测不得进入快照。
func TestSnapshotAllWithStatusIsConsistent(t *testing.T) {
	m := NewManager()
	now := time.Now().UnixMilli()

	m.UpdateChannelResponseHeaders("ch_a", "ep_a", "anthropic", headersWith(
		"anthropic-ratelimit-input-tokens-limit", "10000",
		"anthropic-ratelimit-input-tokens-remaining", "500",
	))
	m.UpdateChannelResponseHeaders("ch_b", "ep_b", "anthropic", headersWith(
		"anthropic-ratelimit-input-tokens-limit", "10000",
		"anthropic-ratelimit-input-tokens-remaining", "9000",
	))

	snapshots := m.SnapshotAllWithStatus(now)
	if len(snapshots) != 2 {
		t.Fatalf("快照数量 = %d, want 2", len(snapshots))
	}
	if snapshots[0].State.ChannelUID != "ch_a" || snapshots[1].State.ChannelUID != "ch_b" {
		t.Fatalf("快照应按 ChannelUID 排序: %s, %s", snapshots[0].State.ChannelUID, snapshots[1].State.ChannelUID)
	}

	approaching := snapshots[0]
	if approaching.State.Status != TruthApproachingLimit || !approaching.Saturated {
		t.Fatalf("ch_a status=%v saturated=%v, want approaching_limit/true",
			approaching.State.Status, approaching.Saturated)
	}
	if diff := approaching.Headroom - 0.05; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("ch_a headroom = %v, want 0.05", approaching.Headroom)
	}

	healthy := snapshots[1]
	if healthy.State.Status != TruthHealthy || healthy.Saturated {
		t.Fatalf("ch_b status=%v saturated=%v, want healthy/false", healthy.State.Status, healthy.Saturated)
	}

	// 与逐渠道读接口同口径，避免端点与调度给出相反结论
	for _, snapshot := range snapshots {
		if got := m.IsChannelSaturated(snapshot.State.ChannelUID, now); got != snapshot.Saturated {
			t.Fatalf("渠道 %s 端点派生值(%v) 与 IsChannelSaturated(%v) 不一致",
				snapshot.State.ChannelUID, snapshot.Saturated, got)
		}
	}

	// 失效观测既不留在内部状态，也不进快照
	m.mu.Lock()
	for dim, v := range m.states["ch_a"].Values {
		v.ResetAtMs = now - 1000
		m.states["ch_a"].Values[dim] = v
	}
	m.mu.Unlock()

	for _, snapshot := range m.SnapshotAllWithStatus(now) {
		if snapshot.State.ChannelUID != "ch_a" {
			continue
		}
		if len(snapshot.State.Values) != 0 {
			t.Fatalf("失效观测进入快照: %+v", snapshot.State.Values)
		}
		if snapshot.State.Status != TruthUnknown || snapshot.Saturated || snapshot.Headroom != 0.5 {
			t.Fatalf("自愈后派生值应回到中性: status=%v saturated=%v headroom=%v",
				snapshot.State.Status, snapshot.Saturated, snapshot.Headroom)
		}
	}
}

// 渠道删除后配额状态必须真的消失，否则端点会一直列出幽灵渠道。
func TestRemoveChannelDropsQuotaState(t *testing.T) {
	m := NewManager()
	m.UpdateChannelResponseHeaders("ch_gone", "ep_a", "anthropic", headersWith(
		"anthropic-ratelimit-input-tokens-limit", "10000",
		"anthropic-ratelimit-input-tokens-remaining", "500",
	))
	m.UpdateChannelResponseHeaders("ch_kept", "ep_b", "anthropic", headersWith(
		"anthropic-ratelimit-input-tokens-limit", "10000",
		"anthropic-ratelimit-input-tokens-remaining", "9000",
	))
	if len(m.SnapshotAll()) != 2 {
		t.Fatalf("前置快照数量 = %d, want 2", len(m.SnapshotAll()))
	}

	m.RemoveChannel("ch_gone")

	kept := m.SnapshotAll()
	if len(kept) != 1 || kept[0].ChannelUID != "ch_kept" {
		t.Fatalf("删除后快照 = %+v, want 只留 ch_kept", kept)
	}
	if truth := m.GetChannelTruth("ch_gone"); truth != TruthUnknown {
		t.Fatalf("已删渠道 truth = %v, want unknown", truth)
	}

	// 空 UID 不得误删
	m.RemoveChannel("")
	if len(m.SnapshotAll()) != 1 {
		t.Fatalf("空 UID 不应删除任何状态，got %d", len(m.SnapshotAll()))
	}
}

// ChannelSaturationRank 必须与 IsChannelSaturated 同一剪除口径，
// 否则同一渠道在两处会给出相反结论。
func TestChannelSaturationRankPrunesLikeIsChannelSaturated(t *testing.T) {
	m := NewManager()
	now := time.Now().UnixMilli()
	m.UpdateChannelResponseHeaders("ch_rank", "ep", "anthropic", headersWith(
		"anthropic-ratelimit-input-tokens-limit", "10000",
		"anthropic-ratelimit-input-tokens-remaining", "500",
	))

	if got := m.ChannelSaturationRank("ch_rank", now); got != 1 {
		t.Fatalf("rank = %d, want 1 (approaching_limit)", got)
	}

	m.mu.Lock()
	for dim, v := range m.states["ch_rank"].Values {
		v.ResetAtMs = now - 1000
		m.states["ch_rank"].Values[dim] = v
	}
	m.mu.Unlock()

	if got := m.ChannelSaturationRank("ch_rank", now); got != -1 {
		t.Fatalf("剪除后 rank = %d, want -1（不再参与饱和排序）", got)
	}
	if m.IsChannelSaturated("ch_rank", now) {
		t.Fatal("rank 与 IsChannelSaturated 口径必须一致")
	}
}
