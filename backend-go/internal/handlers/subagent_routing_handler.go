package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

type subagentRoutingUpdateRequest struct {
	Revision string                       `json:"revision"`
	Policy   config.SubagentRoutingPolicy `json:"policy"`
}

func RegisterSubagentRoutingRoutes(group *gin.RouterGroup, cfgManager *config.ConfigManager) {
	for _, protocol := range []string{"messages", "responses", "chat", "gemini"} {
		path := "/" + protocol + "/channels/subagent-routing"
		group.GET(path, func(c *gin.Context) {
			c.Params = append(c.Params, gin.Param{Key: "type", Value: protocol})
			GetSubagentRouting(cfgManager)(c)
		})
		group.PUT(path, func(c *gin.Context) {
			c.Params = append(c.Params, gin.Param{Key: "type", Value: protocol})
			UpdateSubagentRouting(cfgManager)(c)
		})
	}
}

func GetSubagentRouting(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		view, err := cfgManager.GetSubagentRouting(c.Param("type"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, view)
	}
}

func UpdateSubagentRouting(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req subagentRoutingUpdateRequest
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}
		if req.Revision == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "revision 不能为空"})
			return
		}
		view, err := cfgManager.UpdateSubagentRouting(c.Param("type"), req.Revision, req.Policy)
		if errors.Is(err, config.ErrSubagentRoutingRevisionConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "SUBAGENT_ROUTING_CONFLICT"})
			return
		}
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, config.ErrInvalidSubagentRouting) {
				status = http.StatusBadRequest
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, view)
	}
}
