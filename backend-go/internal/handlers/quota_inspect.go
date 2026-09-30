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
//
// 快照、headroom 与饱和判定在同一次读取内产出：早前实现先取快照、再逐渠道重读
// 计算派生值（每个渠道多 2 次加锁 + 一次惰性剪除），既拖长管理端点耗时，也会出现
// 「status=unknown 但 saturated=true」这类跨瞬间矛盾的行。
// 渠道删除后其配额状态由 DeleteChannelMetrics 调用 Manager.RemoveChannel 清理。
func GetQuotaChannels(qm *quota.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if qm == nil {
			c.JSON(http.StatusOK, gin.H{"channels": []quotaChannelView{}})
			return
		}
		snapshots := qm.SnapshotAllWithStatus(time.Now().UnixMilli())
		views := make([]quotaChannelView, 0, len(snapshots))
		for _, snapshot := range snapshots {
			views = append(views, quotaChannelView{
				ChannelState: snapshot.State,
				Headroom:     snapshot.Headroom,
				Saturated:    snapshot.Saturated,
			})
		}
		c.JSON(http.StatusOK, gin.H{"channels": views})
	}
}
