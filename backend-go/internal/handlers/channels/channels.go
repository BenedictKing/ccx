// Package channels 提供 Channel Data Model v2 的只读 REST API。
//
// 返回"渠道 → key → (baseURL,协议) endpoint → 模型"粒度的实时视图与跨账号共享能力，
// 基于运行时权威（六个 Upstream 数组）现算，供前端展示新粒度。写路径仍走既有 upstream/
// logical-channels 接口。详见 docs/specs/channel-data-model-v2.md。
package channels

import (
	"net/http"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// Handler 渠道 v2 只读处理器。
type Handler struct {
	cm *config.ConfigManager
}

// RegisterRoutes 注册渠道 v2 只读路由。
func RegisterRoutes(apiGroup *gin.RouterGroup, cfgManager *config.ConfigManager) {
	h := &Handler{cm: cfgManager}
	apiGroup.GET("/channels", h.List)
	apiGroup.GET("/channels/:uid", h.Get)
}

// ListResponse 列表响应。
type ListResponse struct {
	SchemaVersion int                         `json:"schemaVersion"`
	Channels      []config.ChannelView        `json:"channels"`
	Capabilities  []config.EndpointCapability `json:"capabilities"`
}

// List 返回全部渠道视图与共享能力。
func (h *Handler) List(c *gin.Context) {
	views, caps := h.cm.GetChannelViews()
	c.JSON(http.StatusOK, ListResponse{
		SchemaVersion: config.ChannelSchemaVersion,
		Channels:      views,
		Capabilities:  caps,
	})
}

// GetResponse 单渠道响应，仅附带该渠道 key 引用到的能力子集。
type GetResponse struct {
	Channel      config.ChannelView          `json:"channel"`
	Capabilities []config.EndpointCapability `json:"capabilities"`
}

// Get 按聚合 ChannelUID 返回单个渠道视图及其引用的能力子集。
func (h *Handler) Get(c *gin.Context) {
	uid := c.Param("uid")
	views, caps := h.cm.GetChannelViews()
	capByUID := make(map[string]config.EndpointCapability, len(caps))
	for _, cap := range caps {
		capByUID[cap.CapabilityUID] = cap
	}
	for _, v := range views {
		if v.ChannelUID != uid {
			continue
		}
		c.JSON(http.StatusOK, GetResponse{Channel: v, Capabilities: referencedCapabilities(v, capByUID)})
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "channel not found", "uid": uid})
}

// referencedCapabilities 收集某渠道所有 key endpoint 引用到的能力（保序去重）。
func referencedCapabilities(v config.ChannelView, byUID map[string]config.EndpointCapability) []config.EndpointCapability {
	seen := make(map[string]struct{})
	out := make([]config.EndpointCapability, 0)
	for _, k := range v.Keys {
		for _, e := range k.Endpoints {
			if _, ok := seen[e.CapabilityUID]; ok {
				continue
			}
			if cap, ok := byUID[e.CapabilityUID]; ok {
				seen[e.CapabilityUID] = struct{}{}
				out = append(out, cap)
			}
		}
	}
	return out
}
