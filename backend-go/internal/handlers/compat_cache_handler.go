package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── 渠道兼容性能力记忆查看/清除 API ──
//
// ChannelCompatCache 落盘在 .config/channel_compat.json，此前没有任何 HTTP 端点，
// 运维查看只能读原始 JSON、清除只能删文件后重启。本组端点提供只读快照与
// 按 trait 粒度的清除（无需重启，立即生效）。

// CompatCacheResponse GET /api/compat-cache 返回结构。
type CompatCacheResponse struct {
	Entries []config.CompatSnapshotEntry `json:"entries"`
	Total   int                          `json:"total"`
}

// ClearCompatCacheResponse DELETE /api/compat-cache 返回结构。
type ClearCompatCacheResponse struct {
	Removed int    `json:"removed"`
	Trait   string `json:"trait,omitempty"` // 空 = 清除全部
}

// RegisterCompatCacheRoutes 注册兼容性记忆管理端点到给定路由组。
func RegisterCompatCacheRoutes(router gin.IRouter) {
	group := router.Group("/compat-cache")
	{
		group.GET("", handleListCompatCache)
		group.DELETE("", handleClearCompatCache)
	}
}

func handleListCompatCache(c *gin.Context) {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		c.JSON(http.StatusOK, CompatCacheResponse{Entries: []config.CompatSnapshotEntry{}, Total: 0})
		return
	}
	entries := cache.Snapshot()
	if entries == nil {
		entries = []config.CompatSnapshotEntry{}
	}
	c.JSON(http.StatusOK, CompatCacheResponse{Entries: entries, Total: len(entries)})
}

func handleClearCompatCache(c *gin.Context) {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		c.JSON(http.StatusOK, ClearCompatCacheResponse{})
		return
	}
	trait := config.CompatTrait(c.Query("trait"))
	removed := cache.ClearTrait(trait)
	if trait != "" {
		// ClearTrait 空参数即全清；这里显式区分两种语义便于响应回显。
		c.JSON(http.StatusOK, ClearCompatCacheResponse{Removed: removed, Trait: string(trait)})
		return
	}
	c.JSON(http.StatusOK, ClearCompatCacheResponse{Removed: removed})
}
