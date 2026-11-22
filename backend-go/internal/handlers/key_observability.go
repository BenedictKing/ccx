package handlers

import (
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

type keyOverview struct {
	KeyID         string                  `json:"keyId"`
	KeyMask       string                  `json:"keyMask"`
	State         string                  `json:"state"`
	Stats         metrics.TimeWindowStats `json:"stats"`
	LatestFailure *metrics.KeyFailure     `json:"latestFailure,omitempty"`
}

func RegisterKeyObservabilityRoutes(group *gin.RouterGroup, cfgManager *config.ConfigManager, routing *metrics.RoutingStore, operations *metrics.KeyOperationStore, managers map[string]*metrics.MetricsManager) {
	for _, apiType := range []string{"messages", "chat", "responses", "gemini", "images", "vectors"} {
		kind := scheduler.ChannelKind(apiType)
		group.GET("/"+apiType+"/keys/overview", func(c *gin.Context) {
			uid := c.Query("channelUid")
			if uid == "" || len(uid) > 160 {
				c.JSON(400, gin.H{"error": "Invalid channel identity"})
				return
			}
			channels := channelUpstreamsByKind(cfgManager.GetConfig(), kind)
			for index, channel := range channels {
				if channel.ChannelUID != uid {
					continue
				}
				failures, coverage, err := routing.LatestKeyFailures(apiType, uid)
				if err != nil {
					c.JSON(500, gin.H{"error": "Could not query Key failures"})
					return
				}
				keys := []keyOverview{}
				for _, key := range channelStatsAPIKeys(channel) {
					id := utils.APIKeyID(key)
					item := keyOverview{KeyID: id, KeyMask: utils.MaskAPIKey(key), State: config.KeyState(&channel, key)}
					if manager := managers[apiType]; manager != nil {
						item.Stats = manager.ToResponseMultiURL(index, channel.GetAllBaseURLs(), []string{key}, scheduler.NormalizedMetricsServiceType(kind, channel.ServiceType), 0).TimeWindows["15m"]
					}
					if failure, ok := failures[id]; ok {
						copy := failure
						item.LatestFailure = &copy
					}
					keys = append(keys, item)
				}
				c.JSON(200, gin.H{"channelUid": uid, "keys": keys, "failureCoverage": coverage})
				return
			}
			c.JSON(404, gin.H{"error": "Channel not found"})
		})
		group.GET("/"+apiType+"/keys/operations", func(c *gin.Context) {
			uid, keyID := c.Query("channelUid"), c.Query("keyId")
			if uid == "" || len(uid) > 160 || !validObservabilityKeyID(keyID) {
				c.JSON(400, gin.H{"error": "Invalid operation filter"})
				return
			}
			before, err := strconv.ParseInt(c.DefaultQuery("before", "0"), 10, 64)
			limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "20"))
			if err != nil || before < 0 || limitErr != nil || limit < 1 || limit > 100 {
				c.JSON(400, gin.H{"error": "Invalid operation cursor or limit"})
				return
			}
			events, more, coverage, err := operations.Query(apiType, uid, keyID, before, limit)
			if err != nil {
				c.JSON(500, gin.H{"error": "Could not query Key operations"})
				return
			}
			next := int64(0)
			if more && len(events) > 0 {
				next = events[len(events)-1].Sequence
			}
			c.JSON(200, gin.H{"events": events, "nextCursor": next, "coverage": coverage})
		})
	}
}

func validObservabilityKeyID(id string) bool {
	if id == "" {
		return true
	}
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 32 && strings.ToLower(id) == id
}
