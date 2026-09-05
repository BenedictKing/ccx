package quota

import (
	"errors"
	"net/http"
	"sync"
	"testing"
)

// TestManagerSameSourceRefresh 验证同来源刷新语义（Q1）：
// 新维度直接写入、更高优先级覆盖、相同来源由最新观测覆盖、
// 更低优先级保持忽略、查询失败不清除最后一次成功数据。
func TestManagerSameSourceRefresh(t *testing.T) {
	t.Run("provider api refresh 80pct to 5pct", func(t *testing.T) {
		m := NewManager()
		m.UpdateChannelProviderAPI("ch_p", "acc_p", []Value{
			{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(8000)},
		}, nil)
		if h := m.GetChannelHeadroom("ch_p"); h < 0.7 || h > 0.85 {
			t.Fatalf("initial headroom = %v, want ~0.8", h)
		}

		// 同来源新快照：余量降到 5%，必须覆盖旧值
		m.UpdateChannelProviderAPI("ch_p", "acc_p", []Value{
			{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(500)},
		}, nil)
		if h := m.GetChannelHeadroom("ch_p"); h > 0.1 {
			t.Fatalf("refreshed headroom = %v, want ~0.05（同来源新观测必须覆盖）", h)
		}
		if s := m.GetChannelTruth("ch_p"); s != TruthApproachingLimit {
			t.Fatalf("truth = %v, want approaching_limit", s)
		}
	})

	t.Run("response headers reset to full", func(t *testing.T) {
		m := NewManager()
		headersDepleted := make(http.Header)
		headersDepleted.Set("anthropic-ratelimit-input-tokens-limit", "50000")
		headersDepleted.Set("anthropic-ratelimit-input-tokens-remaining", "0")
		m.UpdateChannelResponseHeaders("ch_h", "acc_h", "anthropic", headersDepleted)
		if s := m.GetChannelTruth("ch_h"); s != TruthExhausted {
			t.Fatalf("initial truth = %v, want exhausted", s)
		}

		// 窗口重置后响应头报 100%：同来源新观测覆盖
		headersReset := make(http.Header)
		headersReset.Set("anthropic-ratelimit-input-tokens-limit", "50000")
		headersReset.Set("anthropic-ratelimit-input-tokens-remaining", "50000")
		m.UpdateChannelResponseHeaders("ch_h", "acc_h", "anthropic", headersReset)
		if s := m.GetChannelTruth("ch_h"); s != TruthHealthy {
			t.Fatalf("reset truth = %v, want healthy", s)
		}
	})

	t.Run("configured multiplier update", func(t *testing.T) {
		m := NewManager()
		m.UpdateChannelConfigured("ch_c", "acc_c", []Value{
			{Dimension: DimCredits, Limit: ptrF(100), Remaining: ptrF(90)},
		})
		// 配置更新：额度翻倍
		m.UpdateChannelConfigured("ch_c", "acc_c", []Value{
			{Dimension: DimCredits, Limit: ptrF(200), Remaining: ptrF(190)},
		})
		v := m.GetChannelState("ch_c").Values[DimCredits]
		if v.Limit == nil || *v.Limit != 200 {
			t.Fatalf("configured limit = %v, want 200（配置更新须生效）", v.Limit)
		}
	})

	t.Run("lower priority never overrides higher", func(t *testing.T) {
		m := NewManager()
		m.UpdateChannelResponseHeaders("ch_s", "acc_s", "anthropic", func() http.Header {
			h := make(http.Header)
			h.Set("anthropic-ratelimit-input-tokens-limit", "8000")
			h.Set("anthropic-ratelimit-input-tokens-remaining", "4000")
			return h
		}())
		// 更低优先级的 configured 不覆盖 response_headers
		m.UpdateChannelConfigured("ch_s", "acc_s", []Value{
			{Dimension: DimInputTokens, Limit: ptrF(8000), Remaining: ptrF(8000)},
		})
		v := m.GetChannelState("ch_s").Values[DimInputTokens]
		if v.Source != SourceResponseHeaders {
			t.Fatalf("source = %v, want response_headers（低优先级不得覆盖高优先级）", v.Source)
		}
	})

	t.Run("fetch error keeps last successful values", func(t *testing.T) {
		m := NewManager()
		m.UpdateChannelProviderAPI("ch_e", "acc_e", []Value{
			{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(7000)},
		}, nil)
		// 查询失败：只更新 Error/FetchedAtMs，不清除成功数据
		m.UpdateChannelProviderAPI("ch_e", "acc_e", nil, errors.New("provider timeout"))
		state := m.GetChannelState("ch_e")
		if v, ok := state.Values[DimTokens]; !ok || v.Remaining == nil || *v.Remaining != 7000 {
			t.Fatalf("values after fetch error = %+v, want 保留 7000", state.Values)
		}
		if state.Error == "" {
			t.Fatal("fetch error 应记录到 state.Error")
		}
		if state.Status != TruthHealthy {
			t.Fatalf("truth after fetch error = %v, want healthy（fail-open）", state.Status)
		}
	})
}

// TestManagerSnapshotIsolation 验证快照隔离（Q2）：调用方通过返回快照的修改
// 不影响 Manager 内部状态。
func TestManagerSnapshotIsolation(t *testing.T) {
	m := NewManager()
	m.UpdateChannelProviderAPI("ch_iso", "acc_iso", []Value{
		{Dimension: DimTokens, Limit: ptrF(10000), Remaining: ptrF(8000)},
	}, nil)

	snap := m.GetChannelState("ch_iso")
	snap.Values[DimTokens] = Value{Dimension: DimTokens, Limit: ptrF(1), Remaining: ptrF(0)}
	snap.Status = TruthExhausted
	snap.AccountUID = "tampered"

	fresh := m.GetChannelState("ch_iso")
	if v := fresh.Values[DimTokens]; v.Remaining == nil || *v.Remaining != 8000 {
		t.Fatalf("internal state was mutated via snapshot: %+v", fresh.Values)
	}
	if fresh.Status != TruthHealthy {
		t.Fatalf("internal status was mutated via snapshot: %v", fresh.Status)
	}
	if fresh.AccountUID != "acc_iso" {
		t.Fatalf("internal accountUID was mutated via snapshot: %q", fresh.AccountUID)
	}
	if h := m.GetChannelHeadroom("ch_iso"); h < 0.7 {
		t.Fatalf("headroom after snapshot tampering = %v, want ~0.8", h)
	}
}

// TestManagerConcurrentReadWrite 多 goroutine 同时写 provider/header/configured
// 数据并读取 headroom/truth（Q2，配合 go test -race 验证无 data race）。
func TestManagerConcurrentReadWrite(t *testing.T) {
	m := NewManager()

	var wg sync.WaitGroup

	// 写侧：三个来源交替更新同一渠道
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rem := float64(1000 + j*10 + i)
				switch i % 3 {
				case 0:
					m.UpdateChannelProviderAPI("ch_race", "acc_race", []Value{
						{Dimension: DimTokens, Limit: ptrF(100000), Remaining: ptrF(rem)},
					}, nil)
				case 1:
					h := make(http.Header)
					h.Set("anthropic-ratelimit-input-tokens-limit", "100000")
					h.Set("anthropic-ratelimit-input-tokens-remaining", "90000")
					m.UpdateChannelResponseHeaders("ch_race", "acc_race", "anthropic", h)
				case 2:
					m.UpdateChannelConfigured("ch_race", "acc_race", []Value{
						{Dimension: DimCredits, Limit: ptrF(1000), Remaining: ptrF(rem)},
					})
				}
			}
		}(i)
	}

	// 读侧：headroom/truth/saturation 快照读取（固定迭代，随写侧一同收敛）
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 400; j++ {
				m.GetChannelHeadroom("ch_race")
				m.GetChannelTruth("ch_race")
				m.IsChannelSaturated("ch_race", 0)
				m.ChannelSaturationRank("ch_race", 0)
				snap := m.GetChannelState("ch_race")
				// 读侧修改快照也不得影响内部状态
				snap.Values[DimTokens] = Value{}
			}
		}()
	}

	wg.Wait()

	// 最终读一次确认状态有效
	if s := m.GetChannelTruth("ch_race"); s == TruthUnknown {
		t.Fatal("concurrent updates must leave a valid state")
	}
}
