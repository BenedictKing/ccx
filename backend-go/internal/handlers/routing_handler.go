package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

type routingCursor struct {
	From, To        time.Time
	ReceivedAt      time.Time
	ID, Fingerprint string
}

func routingQuery(c *gin.Context, apiType string) (metrics.RoutingFilter, time.Time, time.Time, *routingCursor, bool) {
	filter := metrics.RoutingFilter{RequestedModel: c.Query("requestedModel"), ChannelUID: c.Query("channelUid"), SentModel: c.Query("sentModel"), Basis: c.DefaultQuery("basis", "successful_requests")}
	bad := func() (metrics.RoutingFilter, time.Time, time.Time, *routingCursor, bool) {
		c.JSON(400, gin.H{"error": "Invalid routing range, basis or cursor"})
		return filter, time.Time{}, time.Time{}, nil, false
	}
	if filter.Basis != "successful_requests" && filter.Basis != "all_attempts" {
		return bad()
	}
	fingerprintBytes := sha256.Sum256([]byte(apiType + "\x00" + filter.RequestedModel + "\x00" + filter.ChannelUID + "\x00" + filter.SentModel + "\x00" + filter.Basis))
	fingerprint := hex.EncodeToString(fingerprintBytes[:])
	from, to := time.Time{}, time.Now()
	var cursor *routingCursor
	if raw := c.Query("cursor"); raw != "" {
		cursor = &routingCursor{}
		payload, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(payload, cursor) != nil || cursor.Fingerprint != fingerprint {
			return bad()
		}
		from, to = cursor.From, cursor.To
	} else if c.Query("from") != "" || c.Query("to") != "" {
		var err error
		from, err = time.Parse(time.RFC3339Nano, c.Query("from"))
		if err != nil {
			return bad()
		}
		to, err = time.Parse(time.RFC3339Nano, c.Query("to"))
		if err != nil {
			return bad()
		}
	} else {
		switch c.DefaultQuery("duration", "6h") {
		case "1h":
			from = to.Add(-time.Hour)
		case "6h":
			from = to.Add(-6 * time.Hour)
		case "24h":
			from = to.Add(-24 * time.Hour)
		case "today":
			// 与原全局统计复用今日边界，避免浏览器与服务端时区不同。
			duration, _ := parseExtendedDuration("today")
			from = to.Add(-duration)
		default:
			return bad()
		}
	}
	if !from.Before(to) || to.Sub(from) > 24*time.Hour || to.After(time.Now().Add(time.Minute)) || from.Before(time.Now().Add(-24*time.Hour-time.Minute)) {
		return bad()
	}
	if cursor == nil {
		cursor = &routingCursor{From: from, To: to, Fingerprint: fingerprint}
	}
	return filter, from, to, cursor, true
}
func RegisterRoutingRoutes(group *gin.RouterGroup, store *metrics.RoutingStore, managers ...*config.ConfigManager) {
	// 精确注册六类协议，复用管理鉴权；不扩大未知路由的匹配范围。
	for _, apiType := range []string{"messages", "chat", "responses", "gemini", "images", "vectors"} {
		registerRoutingProtocol(group, store, apiType, managers...)
	}
}
func registerRoutingProtocol(group *gin.RouterGroup, store *metrics.RoutingStore, apiType string, managers ...*config.ConfigManager) {
	prefix := "/" + apiType + "/models/routing"
	handler := func(list bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			filter, from, to, cursor, ok := routingQuery(c, apiType)
			if !ok {
				return
			}
			requests, coverage := store.Snapshot(apiType, from, to)
			markDeletedRoutingChannels(requests, managers)
			distribution, matched := metrics.AggregateRouting(requests, filter)
			distribution.From, distribution.To, distribution.Coverage = from, to, coverage
			if !list {
				c.JSON(200, distribution)
				return
			}
			limit := 20
			if raw := c.Query("limit"); raw != "" {
				n, err := strconv.Atoi(raw)
				if err != nil || n < 1 || n > 100 {
					c.JSON(400, gin.H{"error": "Invalid limit"})
					return
				}
				limit = n
			}
			page := []metrics.RoutingRequest{}
			for _, request := range matched {
				if cursor.ID != "" && (request.ReceivedAt.After(cursor.ReceivedAt) || request.ReceivedAt.Equal(cursor.ReceivedAt) && request.GatewayRequestID >= cursor.ID) {
					continue
				}
				page = append(page, request)
				if len(page) > limit {
					break
				}
			}
			next := ""
			if len(page) > limit {
				page = page[:limit]
				last := page[len(page)-1]
				cursor.ReceivedAt, cursor.ID = last.ReceivedAt, last.GatewayRequestID
				payload, _ := json.Marshal(cursor)
				next = base64.RawURLEncoding.EncodeToString(payload)
			}
			c.JSON(200, gin.H{"requests": page, "nextCursor": next, "from": from, "to": to, "coverage": coverage})
		}
	}
	group.GET(prefix, handler(false))
	group.GET(prefix+"/requests", handler(true))
	group.GET(prefix+"/requests/:requestId", func(c *gin.Context) {
		request, ok := store.Get(apiType, c.Param("requestId"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Request not found"})
			return
		}
		requests := []metrics.RoutingRequest{request}
		markDeletedRoutingChannels(requests, managers)
		c.JSON(200, requests[0])
	})
}

func markDeletedRoutingChannels(requests []metrics.RoutingRequest, managers []*config.ConfigManager) {
	if len(managers) == 0 || managers[0] == nil {
		return
	}
	cfg := managers[0].GetConfig()
	current := map[string]bool{}
	for _, channels := range [][]config.UpstreamConfig{cfg.Upstream, cfg.ChatUpstream, cfg.ResponsesUpstream, cfg.GeminiUpstream, cfg.ImagesUpstream, cfg.VectorsUpstream} {
		for _, channel := range channels {
			current[channel.ChannelUID] = true
		}
	}
	for i := range requests {
		for j := range requests[i].Attempts {
			attempt := &requests[i].Attempts[j]
			attempt.ChannelDeleted = !current[attempt.ChannelUID]
		}
	}
}
