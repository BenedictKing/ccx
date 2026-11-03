package balanceprobe

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// BalancePolicyView 解析后余额探测策略的管理 API 视图。
type BalancePolicyView struct {
	Enabled         bool    `json:"enabled"`
	IntervalMinutes float64 `json:"intervalMinutes"`
	Provider        string  `json:"provider"`
	TimeoutMs       int64   `json:"timeoutMs"`
	QuotaPerUnit    float64 `json:"quotaPerUnit"`
}

// BalanceRecordView 单 key 余额探测记录（key_health balance 行 + detail 解析回显）。
type BalanceRecordView struct {
	KeyMask     string           `json:"keyMask"`
	Status      string           `json:"status"`
	LastCheckAt int64            `json:"lastCheckAtMs"`
	Failures    int64            `json:"consecutiveFailures"`
	Detail      keyBalanceDetail `json:"detail"`
	RateWindows []rateWindowView `json:"rateWindows,omitempty"`
}

type rateWindowView struct {
	Window    string  `json:"window"`
	Limit     float64 `json:"limit"`
	Used      float64 `json:"used"`
	Remaining float64 `json:"remaining"`
	ResetAtMs int64   `json:"resetAtMs,omitempty"`
}

// QuotaValueView 配额真相维度值的只读回显。
type QuotaValueView struct {
	Dimension    string   `json:"dimension"`
	Limit        *float64 `json:"limit,omitempty"`
	Used         *float64 `json:"used,omitempty"`
	Remaining    *float64 `json:"remaining,omitempty"`
	Unit         string   `json:"unit,omitempty"`
	Source       string   `json:"source"`
	ResetAtMs    int64    `json:"resetAtMs,omitempty"`
	ObservedAtMs int64    `json:"observedAtMs,omitempty"`
}

// ChannelBalanceView 渠道余额探测状态（策略 + key 明细 + 配额真相维度值）。
type ChannelBalanceView struct {
	ChannelType string              `json:"channelType"`
	ChannelID   string              `json:"channelId"`
	ChannelUID  string              `json:"channelUid,omitempty"`
	Policy      BalancePolicyView   `json:"policy"`
	Records     []BalanceRecordView `json:"records"`
	QuotaValues []QuotaValueView    `json:"quotaValues"`
}

func toBalancePolicyView(p config.ResolvedBalancePolicy) BalancePolicyView {
	return BalancePolicyView{
		Enabled:         p.Enabled,
		IntervalMinutes: p.Interval.Minutes(),
		Provider:        p.Provider,
		TimeoutMs:       p.Timeout.Milliseconds(),
		QuotaPerUnit:    p.QuotaPerUnit,
	}
}

// ChannelBalance 返回指定渠道的余额探测策略与明细记录；渠道不存在时返回 nil。
func (m *Manager) ChannelBalance(channelType string, channelIndex int) *ChannelBalanceView {
	cfg := m.getConfig()
	upstreams := upstreamsFor(&cfg, channelType)
	if channelIndex < 0 || channelIndex >= len(upstreams) {
		return nil
	}
	u := &upstreams[channelIndex]
	policy := cfg.ResolveBalancePolicy(u)
	channelID := stableChannelID(u, channelIndex)

	view := &ChannelBalanceView{
		ChannelType: channelType,
		ChannelID:   channelID,
		ChannelUID:  u.ChannelUID,
		Policy:      toBalancePolicyView(policy),
		Records:     []BalanceRecordView{},
		QuotaValues: []QuotaValueView{},
	}
	if recs, err := m.store.GetKeyHealthForChannel(channelType, channelID); err == nil {
		for _, r := range recs {
			if r.CheckKind != CheckKindBalance {
				continue
			}
			rv := BalanceRecordView{
				KeyMask:     r.KeyMask,
				Status:      r.LastStatus,
				LastCheckAt: r.LastCheckAt.UnixMilli(),
				Failures:    r.ConsecutiveFailures,
			}
			_ = json.Unmarshal([]byte(r.Detail), &rv.Detail)
			for _, w := range rv.Detail.RateWindows {
				rv.RateWindows = append(rv.RateWindows, rateWindowView{
					Window:    w.Window,
					Limit:     w.Limit,
					Used:      w.Used,
					Remaining: w.Remain,
					ResetAtMs: derefInt64(w.ResetAt),
				})
			}
			rv.Detail.RateWindows = nil // 已展开到顶层
			view.Records = append(view.Records, rv)
		}
	}
	if m.quota != nil && u.ChannelUID != "" {
		if state := m.quota.GetChannelState(u.ChannelUID); state != nil {
			for dim, v := range state.Values {
				view.QuotaValues = append(view.QuotaValues, QuotaValueView{
					Dimension:    string(dim),
					Limit:        v.Limit,
					Used:         v.Used,
					Remaining:    v.Remaining,
					Unit:         v.Unit,
					Source:       string(v.Source),
					ResetAtMs:    v.ResetAtMs,
					ObservedAtMs: v.ObservedAtMs,
				})
			}
		}
	}
	return view
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// ChannelBalanceHandler GET /api/{type}/channels/:id/balance
func (m *Manager) ChannelBalanceHandler(channelType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}
		view := m.ChannelBalance(channelType, id)
		if view == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
			return
		}
		c.JSON(http.StatusOK, view)
	}
}

// TriggerChannelBalanceCheckHandler POST /api/{type}/channels/:id/balance/check
// 异步触发该渠道立即探测（202 返回，结果通过 GET balance 查询）。
func (m *Manager) TriggerChannelBalanceCheckHandler(channelType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}
		cfg := m.getConfig()
		upstreams := upstreamsFor(&cfg, channelType)
		if id < 0 || id >= len(upstreams) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
			return
		}
		accepted := m.TriggerChannelBalanceCheck(channelType, id)
		c.JSON(http.StatusAccepted, gin.H{
			"message": "余额探测已触发，请稍后通过 GET 查询结果",
			"queued":  accepted,
		})
	}
}
