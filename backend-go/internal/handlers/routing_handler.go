package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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

type routingHistoryCursor struct {
	From        time.Time
	To          time.Time
	ReceivedAt  time.Time
	ID          string
	Fingerprint string
	IssuedAt    time.Time
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
	if !from.Before(to) || to.Sub(from) > 24*time.Hour || to.After(time.Now().Add(time.Minute)) {
		return bad()
	}
	if to.Before(time.Now().Add(-metrics.RoutingSnapshotMaxAge)) {
		c.JSON(http.StatusGone, gin.H{"error": "Routing snapshot expired; refresh the time range", "code": "routing_snapshot_expired"})
		return filter, time.Time{}, time.Time{}, nil, false
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
	group.GET(prefix+"/history", func(c *gin.Context) {
		filter, cursor, limit, ok := routingHistoryQuery(c, apiType, store)
		if !ok {
			return
		}
		requests, hasMore, coverage, err := store.QueryRequests(apiType, cursor.From, cursor.To, filter, cursor.ReceivedAt, cursor.ID, limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not query request history"})
			return
		}
		markDeletedRoutingChannels(requests, managers)
		next := ""
		if hasMore && len(requests) > 0 {
			last := requests[len(requests)-1]
			cursor.ReceivedAt, cursor.ID = last.ReceivedAt, last.GatewayRequestID
			payload, _ := json.Marshal(cursor)
			next = base64.RawURLEncoding.EncodeToString(payload)
		}
		c.JSON(http.StatusOK, gin.H{"requests": requests, "nextCursor": next, "from": cursor.From, "to": cursor.To, "coverage": coverage})
	})
}

func routingHistoryQuery(c *gin.Context, apiType string, store *metrics.RoutingStore) (metrics.RoutingHistoryFilter, *routingHistoryCursor, int, bool) {
	filter := metrics.RoutingHistoryFilter{
		RoutingFilter: metrics.RoutingFilter{
			RequestedModel: c.Query("requestedModel"),
			ChannelUID:     c.Query("channelUid"),
			SentModel:      c.Query("sentModel"),
			Basis:          "all_attempts",
		},
		Status: c.Query("status"),
		Search: strings.TrimSpace(c.Query("q")),
	}
	bad := func() (metrics.RoutingHistoryFilter, *routingHistoryCursor, int, bool) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request history range, filter or cursor"})
		return filter, nil, 0, false
	}
	switch filter.Status {
	case "", metrics.RoutingStatusSuccess, metrics.RoutingStatusFailed, metrics.RoutingStatusCancelled, metrics.RoutingStatusInProgress, metrics.RoutingStatusUnknown:
	default:
		return bad()
	}
	if len(filter.Search) > 120 || len(filter.RequestedModel) > 160 || len(filter.ChannelUID) > 160 || len(filter.SentModel) > 160 {
		return bad()
	}

	limit := 20
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return bad()
		}
		limit = n
	}

	duration := time.Duration(0)
	switch c.DefaultQuery("duration", "24h") {
	case "15m":
		duration = 15 * time.Minute
	case "1h":
		duration = time.Hour
	case "6h":
		duration = 6 * time.Hour
	case "24h":
		duration = 24 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	case "30d":
		duration = 30 * 24 * time.Hour
	default:
		return bad()
	}
	if duration > store.MaxHistoryAge() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Requested history exceeds the configured retention period", "code": "routing_history_retention_exceeded"})
		return filter, nil, 0, false
	}

	fingerprintBytes := sha256.Sum256([]byte(strings.Join([]string{
		apiType, c.DefaultQuery("duration", "24h"), filter.Status, filter.Search,
		filter.RequestedModel, filter.ChannelUID, filter.SentModel,
	}, "\x00")))
	fingerprint := hex.EncodeToString(fingerprintBytes[:])
	var cursor routingHistoryCursor
	if raw := c.Query("cursor"); raw != "" {
		payload, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(payload, &cursor) != nil || cursor.Fingerprint != fingerprint || cursor.IssuedAt.IsZero() || time.Since(cursor.IssuedAt) > metrics.RoutingSnapshotMaxAge {
			c.JSON(http.StatusGone, gin.H{"error": "Request history cursor expired; run the search again", "code": "routing_history_cursor_expired"})
			return filter, nil, 0, false
		}
	} else {
		cursor.From = time.Now().Add(-duration)
		cursor.To = time.Now()
		cursor.IssuedAt = cursor.To
		cursor.Fingerprint = fingerprint
	}
	if !cursor.From.Before(cursor.To) || cursor.To.After(time.Now().Add(time.Minute)) || time.Since(cursor.From) > store.MaxHistoryAge()+time.Minute {
		return bad()
	}
	return filter, &cursor, limit, true
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
