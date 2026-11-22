package common

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type routingStoreContextKey struct{}
type routingObservation struct {
	store                                *metrics.RoutingStore
	id, model, lastStatus, lastAttemptID string
	secrets                              map[string][]string
}

const routingObservationKey = "ccxRoutingObservation"

// RoutingStoreMiddleware 仅传递依赖；鉴权之后才开始观测业务请求。
func RoutingStoreMiddleware(store *metrics.RoutingStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), routingStoreContextKey{}, store))
		c.Next()
	}
}
func ObserveRoutingRequest(c *gin.Context, apiType string) func() {
	store, _ := c.Request.Context().Value(routingStoreContextKey{}).(*metrics.RoutingStore)
	if store == nil {
		return func() {}
	}
	tracker := &routingObservation{store: store, id: GenerateRequestID()}
	c.Set(routingObservationKey, tracker)
	operation := ""
	if apiType == "images" {
		operation = c.Request.URL.Path[strings.LastIndex(c.Request.URL.Path, "/")+1:]
	}
	store.StartRequest(metrics.RoutingRequest{GatewayRequestID: tracker.id, APIType: apiType, Operation: operation, ReceivedAt: time.Now()})
	return func() {
		panicValue := recover()
		status := tracker.lastStatus
		if panicValue != nil {
			status = metrics.RoutingStatusFailed
		}
		if c.Request.Context().Err() == context.Canceled {
			status = metrics.RoutingStatusCancelled
		}
		if status == "" {
			if c.Writer.Status() >= 200 && c.Writer.Status() < 300 && c.Writer.Written() {
				status = metrics.RoutingStatusSuccess
			} else {
				status = metrics.RoutingStatusFailed
			}
		}
		stage := ""
		if status == metrics.RoutingStatusFailed {
			if tracker.lastStatus == "" {
				stage = "before_upstream"
			} else {
				stage = "upstream"
			}
		}
		store.FinishRequest(tracker.id, status, stage)
		if panicValue != nil {
			panic(panicValue)
		}
	}
}
func routingTracker(c *gin.Context) *routingObservation {
	value, _ := c.Get(routingObservationKey)
	tracker, _ := value.(*routingObservation)
	return tracker
}
func recordRoutingModel(c *gin.Context, body []byte) {
	if tracker := routingTracker(c); tracker != nil {
		tracker.model = requestModel(c.Request.URL.Path, c.Request.Header.Get("Content-Type"), body)
		tracker.store.SetRequestModel(tracker.id, tracker.model)
	}
}
func requestModel(path, contentType string, body []byte) string {
	if index := strings.LastIndex(path, "/models/"); index >= 0 {
		return strings.SplitN(path[index+8:], ":", 2)[0]
	}
	media, params, _ := mime.ParseMediaType(contentType)
	if media == "multipart/form-data" {
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "model" && part.FileName() == "" {
				value, _ := io.ReadAll(io.LimitReader(part, 1024))
				return string(value)
			}
			_ = part.Close()
		}
		return ""
	}
	return gjson.GetBytes(body, "model").String()
}

type RoutingMappingDecision struct{ Model, Source, Match string }

func StartRoutingAttempt(c *gin.Context, req *http.Request, upstream *config.UpstreamConfig, effectiveModel, attemptID, reason string, decisions ...RoutingMappingDecision) {
	startRoutingAttempt(c, req, upstream, effectiveModel, attemptID, reason, false, decisions...)
}

func startRoutingAttempt(c *gin.Context, req *http.Request, upstream *config.UpstreamConfig, effectiveModel, attemptID, reason string, auxiliary bool, decisions ...RoutingMappingDecision) {
	tracker := routingTracker(c)
	var body []byte
	if req.GetBody != nil {
		if reader, err := req.GetBody(); err == nil {
			body, _ = io.ReadAll(reader)
			_ = reader.Close()
		}
	}
	sent := requestModel(req.URL.Path, req.Header.Get("Content-Type"), body)
	if !auxiliary && c != nil {
		c.Set("ccxCurrentSentModel", sent)
	}
	if tracker == nil {
		return
	}
	mapped, source, match := config.RedirectModelWithRule(effectiveModel, upstream)
	if len(decisions) > 0 {
		mapped, source, match = decisions[0].Model, decisions[0].Source, decisions[0].Match
	}
	if !auxiliary && c.GetBool("ccxRoutingPolicy") {
		effectiveModel = c.GetString("ccxRoutingEffectiveModel")
		mapped, source, match = effectiveModel, "", ""
	}
	outcome := metrics.MappingOutcomeUnmatched
	if match != "" {
		outcome = metrics.MappingOutcomeMatchedUnchanged
		if mapped != effectiveModel {
			outcome = metrics.MappingOutcomeChanged
		}
	}
	transform := ""
	modelChangeKind := "model_fallback"
	if c.GetBool("ccxSubagentRoutingApplied") {
		modelChangeKind = "subagent_policy"
	}
	if auxiliary {
		modelChangeKind = "compact_model"
	}
	if effectiveModel != tracker.model {
		transform = modelChangeKind
	}
	if sent != mapped {
		if transform != "" {
			transform += ","
		}
		transform += "gateway_fallback"
	}
	steps := []metrics.RoutingTransform{}
	if effectiveModel != tracker.model {
		steps = append(steps, metrics.RoutingTransform{Kind: modelChangeKind, From: tracker.model, To: effectiveModel})
	}
	if match != "" {
		steps = append(steps, metrics.RoutingTransform{Kind: "model_mapping", From: effectiveModel, To: mapped})
	}
	if sent != mapped {
		steps = append(steps, metrics.RoutingTransform{Kind: "gateway_fallback", From: mapped, To: sent})
	}
	snapshot, _ := json.Marshal(struct {
		Mapping map[string]string
		Vision  string
		Service string
	}{upstream.ModelMapping, upstream.VisionFallbackModel, upstream.ServiceType})
	revision := sha256.Sum256(snapshot)
	transport := c.GetString("ccxRoutingTransport")
	if transport == "" {
		transport = "http"
	}
	tracker.lastAttemptID = attemptID
	tracker.store.StartAttempt(tracker.id, metrics.RoutingAttempt{Transport: transport, Auxiliary: auxiliary, AttemptID: attemptID, EffectiveModel: effectiveModel, MappedModel: mapped, SentModel: sent, ChannelUID: upstream.ChannelUID, ChannelName: upstream.Name, ServiceType: upstream.ServiceType, ConfigRevision: hex.EncodeToString(revision[:8]), MappingOutcome: outcome, MappingSource: source, MappingTarget: mapped, MatchType: match, TransformReason: transform, TransformSteps: steps, ModelChanged: sent != tracker.model, SelectionTraceSummary: c.GetString("ccxRoutingSelectionSummary"), SelectionReason: reason})
}
func completeRoutingLog(c *gin.Context, store *metrics.ChannelLogStore, key, id string, code int, success bool, info string, retry bool, usage ...*types.Usage) {
	if tracker := routingTracker(c); tracker != nil {
		status := metrics.RoutingStatusFailed
		if success {
			status = metrics.RoutingStatusSuccess
		} else if info == "client canceled" {
			status = metrics.RoutingStatusCancelled
		}
		tracker.lastStatus = status
		var requestUsage *types.Usage
		if len(usage) > 0 {
			requestUsage = usage[0]
		}
		details := routingAttemptDetails(info, code, tracker.secrets[id], requestUsage)
		delete(tracker.secrets, id)
		if observer := GetStreamTimeoutObserver(c); observer != nil {
			observer.addRoutingDetails(id, details)
		}
		tracker.store.FinishAttempt(tracker.id, id, status, code, details)
	}
	completeLogWithGatewayID(store, key, id, routingRequestID(c), code, success, info, retry, usage...)
}

// ObserveAuxiliaryAttempt 将网关内部 compact 调用保留在明细，排除默认代理分母。
func ObserveAuxiliaryAttempt(c *gin.Context, req *http.Request, upstream *config.UpstreamConfig, model string) func(bool, int) {
	tracker := routingTracker(c)
	if tracker == nil {
		return func(bool, int) {}
	}
	id := GenerateRequestID()
	startRoutingAttempt(c, req, upstream, model, id, "internal_compact", true)
	return func(success bool, code int) {
		status := metrics.RoutingStatusFailed
		if success && code >= 200 && code < 300 {
			status = metrics.RoutingStatusSuccess
		}
		if c.Request.Context().Err() == context.Canceled {
			status = metrics.RoutingStatusCancelled
		}
		// 原生 compact 失败后可嵌套调用本地 compact；外层 defer 不能覆盖后一次尝试的终态。
		if tracker.lastAttemptID == id {
			tracker.lastStatus = status
		}
		tracker.store.FinishAttempt(tracker.id, id, status, code)
	}
}
