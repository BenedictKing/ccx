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
	mu            sync.Mutex
	request       *http.Request
	store         *metrics.RoutingStore
	pending       []*SocketRoutingTurn
	finishedIDs   map[string]bool
	finishedOrder []string
	closed        bool
	onSuccess     func(string, string)
}
type SocketRoutingTurn struct {
	context                   *gin.Context
	id, attemptID, responseID string
	requestedModel            string
	sentModel                 string
}

func NewSocketRoutingObserver(parent *gin.Context, onSuccess ...func(string, string)) *SocketRoutingObserver {
	store, _ := parent.Request.Context().Value(routingStoreContextKey{}).(*metrics.RoutingStore)
	observer := &SocketRoutingObserver{request: parent.Request, store: store, finishedIDs: make(map[string]bool)}
	if len(onSuccess) > 0 {
		observer.onSuccess = onSuccess[0]
	}
	return observer
}
func (o *SocketRoutingObserver) Begin(payload []byte) *SocketRoutingTurn {
	if o.store == nil && o.onSuccess == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	if gjson.GetBytes(payload, "generate").Exists() && !gjson.GetBytes(payload, "generate").Bool() {
		// 预热不计入业务，但保留响应槽位，避免预热事件结束下一轮请求。
		o.pending = append(o.pending, &SocketRoutingTurn{})
		return nil
	}
	id := GenerateRequestID()
	model := gjson.GetBytes(payload, "model").String()
	if o.store != nil {
		o.store.StartRequest(metrics.RoutingRequest{GatewayRequestID: id, APIType: "responses", RequestedModel: model, ReceivedAt: time.Now()})
	}
	c := &gin.Context{Request: o.request}
	c.Set("ccxRoutingTransport", "websocket")
	if o.store != nil {
		c.Set(routingObservationKey, &routingObservation{store: o.store, id: id, model: model})
	}
	turn := &SocketRoutingTurn{context: c, id: id, requestedModel: model}
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
	turn.sentModel = gjson.GetBytes(body, "model").String()
	req, _ := http.NewRequest(http.MethodPost, "http://routing-observation.invalid/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	StartRoutingAttempt(turn.context, req, upstream, turn.requestedModel, turn.attemptID, reason, decision)
}
func (o *SocketRoutingObserver) finishLocked(turn *SocketRoutingTurn, status string, code int) {
	if o.store != nil && turn.attemptID != "" {
		o.store.FinishAttempt(turn.id, turn.attemptID, status, code)
	}
	stage := ""
	if status == metrics.RoutingStatusFailed {
		stage = "websocket"
	}
	if o.store != nil && turn.id != "" {
		o.store.FinishRequest(turn.id, status, stage)
	}
	if turn.responseID != "" && !o.finishedIDs[turn.responseID] {
		o.finishedIDs[turn.responseID] = true
		o.finishedOrder = append(o.finishedOrder, turn.responseID)
		if len(o.finishedOrder) > 256 {
			delete(o.finishedIDs, o.finishedOrder[0])
			o.finishedOrder = o.finishedOrder[1:]
		}
	}
	if status == metrics.RoutingStatusSuccess && o.onSuccess != nil && turn.sentModel != "" {
		o.onSuccess(turn.requestedModel, turn.sentModel)
	}
	for i, candidate := range o.pending {
		if candidate == turn {
			o.pending = append(o.pending[:i], o.pending[i+1:]...)
			break
		}
	}
}

// Event 在事件成功写回客户端后调用；依据协议终态而非 HTTP 101 判定成功。
func (o *SocketRoutingObserver) Event(payload []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.pending) == 0 {
		return
	}
	event := gjson.GetBytes(payload, "type").String()
	switch event {
	case "response.created", "response.completed", "response.failed", "response.incomplete", "response.cancelled", "response.done", "error":
	default:
		return
	}
	id := gjson.GetBytes(payload, "response.id").String()
	if id == "" {
		id = gjson.GetBytes(payload, "response_id").String()
	}
	if id != "" && o.finishedIDs[id] {
		return
	}
	var turn *SocketRoutingTurn
	if id != "" {
		for _, candidate := range o.pending {
			if candidate.responseID == id {
				turn = candidate
				break
			}
		}
	}
	if turn == nil && id != "" {
		for _, candidate := range o.pending {
			if candidate.responseID == "" {
				turn = candidate
				break
			}
		}
	}
	if turn == nil && id == "" && len(o.pending) == 1 {
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
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	for len(o.pending) > 0 {
		o.finishLocked(o.pending[0], status, 0)
	}
}

// PinSocketModelMapping 固定本轮实际执行的模型规则，不修改持久化渠道配置。
func PinSocketModelMapping(upstream *config.UpstreamConfig, payload []byte, agentRoles ...string) (*config.UpstreamConfig, RoutingMappingDecision) {
	model := gjson.GetBytes(payload, "model").String()
	if len(agentRoles) > 0 {
		upstream = config.WithAgentModel(model, upstream, agentRoles[0])
	}
	mapped, source, match := config.RedirectModelWithRule(model, upstream)
	clone := upstream.Clone()
	if match != "" {
		clone.ModelMapping[model] = mapped
	}
	return clone, RoutingMappingDecision{Model: mapped, Source: source, Match: match}
}
