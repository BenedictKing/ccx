package common

import (
	"bytes"
	"net/http"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// SocketRoutingObserver 按 response.create 记录逻辑请求，连接本身不算请求。
type SocketRoutingObserver struct {
	mu      sync.Mutex
	request *http.Request
	store   *metrics.RoutingStore
	pending []*SocketRoutingTurn
	closed  bool
}
type SocketRoutingTurn struct {
	context                   *gin.Context
	id, attemptID, responseID string
}

func NewSocketRoutingObserver(parent *gin.Context) *SocketRoutingObserver {
	store, _ := parent.Request.Context().Value(routingStoreContextKey{}).(*metrics.RoutingStore)
	return &SocketRoutingObserver{request: parent.Request, store: store}
}
func (o *SocketRoutingObserver) Begin(payload []byte) *SocketRoutingTurn {
	if o.store == nil || gjson.GetBytes(payload, "generate").Exists() && !gjson.GetBytes(payload, "generate").Bool() {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	id := GenerateRequestID()
	model := gjson.GetBytes(payload, "model").String()
	o.store.StartRequest(metrics.RoutingRequest{GatewayRequestID: id, APIType: "responses", RequestedModel: model, ReceivedAt: time.Now()})
	c := &gin.Context{Request: o.request}
	c.Set("ccxRoutingTransport", "websocket")
	c.Set(routingObservationKey, &routingObservation{store: o.store, id: id, model: model})
	turn := &SocketRoutingTurn{context: c, id: id}
	o.pending = append(o.pending, turn)
	return turn
}
func (o *SocketRoutingObserver) Sending(turn *SocketRoutingTurn, body []byte, upstream *config.UpstreamConfig, reason string, decision RoutingMappingDecision) {
	if turn == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	turn.attemptID = GenerateRequestID()
	req, _ := http.NewRequest(http.MethodPost, "http://routing-observation.invalid/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	StartRoutingAttempt(turn.context, req, upstream, routingTracker(turn.context).model, turn.attemptID, reason, decision)
}
func (o *SocketRoutingObserver) finishLocked(turn *SocketRoutingTurn, status string, code int) {
	if turn.attemptID != "" {
		o.store.FinishAttempt(turn.id, turn.attemptID, status, code)
	}
	stage := ""
	if status == metrics.RoutingStatusFailed {
		stage = "websocket"
	}
	o.store.FinishRequest(turn.id, status, stage)
	for i, candidate := range o.pending {
		if candidate == turn {
			o.pending = append(o.pending[:i], o.pending[i+1:]...)
			break
		}
	}
}

// Event 在事件成功写回客户端后调用；依据协议终态而非 HTTP 101 判定成功。
func (o *SocketRoutingObserver) Event(payload []byte) {
	if o.store == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.pending) == 0 {
		return
	}
	event := gjson.GetBytes(payload, "type").String()
	id := gjson.GetBytes(payload, "response.id").String()
	var turn *SocketRoutingTurn
	if id != "" {
		for _, candidate := range o.pending {
			if candidate.responseID == id {
				turn = candidate
				break
			}
		}
	}
	if turn == nil {
		for _, candidate := range o.pending {
			if candidate.responseID == "" {
				turn = candidate
				break
			}
		}
	}
	if turn == nil && len(o.pending) == 1 {
		turn = o.pending[0]
	}
	if turn == nil {
		return
	}
	if id != "" {
		turn.responseID = id
	}
	switch event {
	case "response.completed":
		o.finishLocked(turn, metrics.RoutingStatusSuccess, 101)
	case "response.failed", "response.incomplete", "error":
		o.finishLocked(turn, metrics.RoutingStatusFailed, 101)
	case "response.cancelled":
		o.finishLocked(turn, metrics.RoutingStatusCancelled, 101)
	case "response.done":
		status := metrics.RoutingStatusFailed
		switch gjson.GetBytes(payload, "response.status").String() {
		case "completed":
			status = metrics.RoutingStatusSuccess
		case "cancelled":
			status = metrics.RoutingStatusCancelled
		}
		o.finishLocked(turn, status, 101)
	}
}
func (o *SocketRoutingObserver) Close(status string) {
	if o.store == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	for len(o.pending) > 0 {
		o.finishLocked(o.pending[0], status, 0)
	}
}

// PinSocketModelMapping 固定本轮实际执行的模型规则，不修改持久化渠道配置。
func PinSocketModelMapping(upstream *config.UpstreamConfig, payload []byte) (*config.UpstreamConfig, RoutingMappingDecision) {
	model := gjson.GetBytes(payload, "model").String()
	mapped, source, match := config.RedirectModelWithRule(model, upstream)
	clone := upstream.Clone()
	if match != "" {
		clone.ModelMapping[model] = mapped
	}
	return clone, RoutingMappingDecision{Model: mapped, Source: source, Match: match}
}
