package handlers

import (
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/quota"
	"github.com/gin-gonic/gin"
)

// quotaChannelView 配额真相快照的对外视图：在 ChannelState 基础上补充
// 调度实际消费的两个派生值（headroom 分数与饱和判定），便于直接对照
// 「渠道为何被压分/沉底」。
type quotaChannelView struct {
	*quota.ChannelState
	Headroom  float64 `json:"headroom"`
	Saturated bool    `json:"saturated"`
}

// GetQuotaChannels 返回全部渠道的配额真相快照。
// 配额观测（来源分级、headroom、窗口重置时间、饱和状态）此前是纯后端
// 内存暗状态，无任何 API/UI 出口；本端点为排查与观测提供统一出口。
// 快照读取前会惰性剪除窗口已翻转的陈旧观测。
func GetQuotaChannels(qm *quota.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if qm == nil {
			c.JSON(http.StatusOK, gin.H{"channels": []quotaChannelView{}})
			return
		}
		states := qm.SnapshotAll()
		nowMs := time.Now().UnixMilli()
		views := make([]quotaChannelView, 0, len(states))
		for _, state := range states {
			views = append(views, quotaChannelView{
				ChannelState: state,
				Headroom:     qm.GetChannelHeadroom(state.ChannelUID),
				Saturated:    qm.IsChannelSaturated(state.ChannelUID, nowMs),
			})
		}
		c.JSON(http.StatusOK, gin.H{"channels": views})
	}
}
