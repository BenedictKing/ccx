package responses

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/thinkingcache"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

const (
	responsesFoldStep             = 518
	responsesFoldMinN             = 1
	responsesFoldMaxN             = 6
	responsesFoldMaxContinue      = 3
	responsesFoldMarkerText       = "Continue thinking..."
	responsesFoldEncryptedInclude = "reasoning.encrypted_content"
)

type responsesFoldRoundOpener func(body map[string]interface{}) (*http.Response, []byte, error)
type responsesFoldEmitter func(event map[string]interface{}) error

type responsesFoldResult struct {
	AgentUsage  map[string]interface{}
	BilledUsage map[string]interface{}
}

type responsesFoldBufferedOutput struct {
	item   map[string]interface{}
	events []map[string]interface{}
}

func handleFoldedResponsesStreamSuccess(
	c *gin.Context,
	resp *http.Response,
	provider *providers.ResponsesProvider,
	upstream *config.UpstreamConfig,
	apiKey string,
	envCfg *config.EnvConfig,
	sessionManager *session.SessionManager,
	originalReq *types.ResponsesRequest,
	originalRequestJSON []byte,
) (*types.Usage, error) {
	if provider == nil || upstream == nil || apiKey == "" {
		return handleStreamSuccess(c, resp, "responses", envCfg, sessionManager, time.Now(), originalReq, originalRequestJSON, common.StreamPreflightTimeouts{})
	}

	if err := utils.DecompressResponseBodyIfNeeded(resp); err != nil {
		return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
	}

	var baseBody map[string]interface{}
	if err := json.Unmarshal(originalRequestJSON, &baseBody); err != nil {
		return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
	}

	emitter := newResponsesFoldHTTPEmitter(c, resp, sessionManager, originalReq)
	openRound := func(body map[string]interface{}) (*http.Response, []byte, error) {
		bodyBytes, err := utils.MarshalJSONNoEscape(body)
		if err != nil {
			return nil, nil, err
		}
		req, _, err := provider.ConvertBodyToProviderRequest(c, upstream, apiKey, bodyBytes, c.Request.URL.Path)
		if err != nil {
			return nil, nil, err
		}
		req = common.WithRequestLogContext(req, c)
		roundResp, err := common.SendRequest(req, upstream, envCfg, true, "Responses")
		if err != nil {
			return nil, nil, err
		}
		if roundResp.StatusCode < 200 || roundResp.StatusCode >= 300 {
			respBody, _ := io.ReadAll(roundResp.Body)
			roundResp.Body.Close()
			respBody = utils.DecompressGzipIfNeeded(roundResp, respBody)
			return nil, nil, fmt.Errorf("continuation upstream HTTP %d: %s", roundResp.StatusCode, strings.TrimSpace(string(respBody)))
		}
		if err := utils.DecompressResponseBodyIfNeeded(roundResp); err != nil {
			roundResp.Body.Close()
			return nil, nil, err
		}
		return roundResp, bodyBytes, nil
	}

	result, err := runResponsesFold(baseBody, resp, openRound, emitter.emit)
	if err != nil {
		return nil, err
	}
	if !emitter.committed {
		return nil, common.ErrEmptyStreamResponse
	}
	emitter.finish()
	return responsesFoldMetricsUsage(result.BilledUsage), nil
}

func runResponsesFold(
	baseBody map[string]interface{},
	firstResp *http.Response,
	openRound responsesFoldRoundOpener,
	emit responsesFoldEmitter,
) (responsesFoldResult, error) {
	origInput := responsesFoldInputItems(baseBody["input"])
	seq := 0
	dsOutputIndex := 0
	var baseResponse map[string]interface{}
	var finalOutput []interface{}
	var replayTail []interface{}
	summedUsage := make(map[string]interface{})
	var firstUsage map[string]interface{}
	var roundsInfo []map[string]interface{}

	stamp := func(event map[string]interface{}) map[string]interface{} {
		event["sequence_number"] = seq
		seq++
		return event
	}

	currentResp := firstResp
	for roundNo := 1; ; roundNo++ {
		iterator := newResponsesSSEIterator(currentResp.Body)
		outputIndexToDownstream := make(map[string]int)
		outputKind := make(map[string]string)
		bufferedByIndex := make(map[string]*responsesFoldBufferedOutput)
		var bufferedOrder []*responsesFoldBufferedOutput
		var roundReasoning []interface{}
		var terminal map[string]interface{}
		var usage map[string]interface{}

		for {
			frame, ok, err := iterator.next()
			if err != nil {
				_ = currentResp.Body.Close()
				incomplete := responsesFoldTerminalEvent(nil, baseResponse, finalOutput, responsesFoldAgentUsage(firstUsage, summedUsage, usage, false), roundsInfo, summedUsage, "upstream_error", "upstream_error")
				return responsesFoldResult{AgentUsage: responseUsageMap(incomplete), BilledUsage: summedUsage}, emit(stamp(incomplete))
			}
			if !ok {
				break
			}
			if frame.done {
				continue
			}

			eventType, _ := frame.event["type"].(string)
			switch eventType {
			case "response.created", "response.in_progress":
				if roundNo == 1 {
					if eventType == "response.created" {
						baseResponse = mapFromInterface(frame.event["response"])
					}
					if err := emit(stamp(frame.event)); err != nil {
						_ = currentResp.Body.Close()
						return responsesFoldResult{BilledUsage: summedUsage}, err
					}
				}
				continue
			case "response.completed", "response.failed", "response.incomplete":
				terminal = frame.event
				usage = responsesFoldUsageFromTerminal(frame.event)
				goto roundDone
			}

			outputIndex, hasOutputIndex := frame.event["output_index"]
			outputIndexKey := responsesFoldOutputIndexKey(outputIndex)
			if eventType == "response.output_item.added" {
				item := mapFromInterface(frame.event["item"])
				if item["type"] == "reasoning" {
					outputKind[outputIndexKey] = "reasoning"
					outputIndexToDownstream[outputIndexKey] = dsOutputIndex
					frame.event["output_index"] = dsOutputIndex
					dsOutputIndex++
					if err := emit(stamp(frame.event)); err != nil {
						_ = currentResp.Body.Close()
						return responsesFoldResult{BilledUsage: summedUsage}, err
					}
				} else {
					outputKind[outputIndexKey] = "buffered"
					entry := &responsesFoldBufferedOutput{item: item, events: []map[string]interface{}{frame.event}}
					bufferedByIndex[outputIndexKey] = entry
					bufferedOrder = append(bufferedOrder, entry)
				}
				continue
			}

			switch outputKind[outputIndexKey] {
			case "reasoning":
				if downstreamIndex, ok := outputIndexToDownstream[outputIndexKey]; ok && hasOutputIndex {
					frame.event["output_index"] = downstreamIndex
				}
				if eventType == "response.output_item.done" {
					item := mapFromInterface(frame.event["item"])
					roundReasoning = append(roundReasoning, item)
					finalOutput = append(finalOutput, item)
				}
				if err := emit(stamp(frame.event)); err != nil {
					_ = currentResp.Body.Close()
					return responsesFoldResult{BilledUsage: summedUsage}, err
				}
			case "buffered":
				entry := bufferedByIndex[outputIndexKey]
				if entry == nil {
					continue
				}
				entry.events = append(entry.events, frame.event)
				if eventType == "response.output_item.done" {
					entry.item = mapFromInterface(frame.event["item"])
				}
			default:
				if err := emit(stamp(frame.event)); err != nil {
					_ = currentResp.Body.Close()
					return responsesFoldResult{BilledUsage: summedUsage}, err
				}
			}
		}

	roundDone:
		_ = currentResp.Body.Close()
		responsesFoldSumUsage(summedUsage, usage)
		if roundNo == 1 {
			firstUsage = usage
		}

		reasoningTokens, hasReasoningTokens := responsesFoldReasoningTokens(usage)
		n, hasTier := responsesFoldTierN(reasoningTokens, hasReasoningTokens)
		roundInfo := map[string]interface{}{
			"round":            roundNo,
			"reasoning_tokens": nil,
			"n":                nil,
		}
		if hasReasoningTokens {
			roundInfo["reasoning_tokens"] = reasoningTokens
		}
		if hasTier {
			roundInfo["n"] = n
		}
		roundsInfo = append(roundsInfo, roundInfo)

		hasEncrypted := false
		if len(roundReasoning) > 0 {
			lastReasoning := mapFromInterface(roundReasoning[len(roundReasoning)-1])
			hasEncrypted = strings.TrimSpace(stringFromInterface(lastReasoning["encrypted_content"])) != ""
		}
		doContinue := terminal != nil && hasTier && responsesFoldInContinueWindow(n) && hasEncrypted && roundNo <= responsesFoldMaxContinue

		stoppedReason := ""
		if !doContinue && hasTier {
			switch {
			case !hasEncrypted:
				stoppedReason = "no_encrypted_content"
			case roundNo > responsesFoldMaxContinue:
				stoppedReason = "max_continue"
			default:
				stoppedReason = "tier_out_of_window"
			}
		}

		if doContinue {
			replayTail = append(replayTail, roundReasoning...)
			replayTail = append(replayTail, responsesFoldCommentaryNudge())
			nextBody := responsesFoldNextRoundBody(baseBody, append(append([]interface{}{}, origInput...), replayTail...))
			nextResp, _, err := openRound(nextBody)
			if err != nil {
				incomplete := responsesFoldTerminalEvent(nil, baseResponse, finalOutput, responsesFoldAgentUsage(firstUsage, summedUsage, usage, false), roundsInfo, summedUsage, "upstream_error", "upstream_error")
				return responsesFoldResult{AgentUsage: responseUsageMap(incomplete), BilledUsage: summedUsage}, emit(stamp(incomplete))
			}
			currentResp = nextResp
			continue
		}

		if terminal == nil {
			incomplete := responsesFoldTerminalEvent(nil, baseResponse, finalOutput, responsesFoldAgentUsage(firstUsage, summedUsage, usage, false), roundsInfo, summedUsage, "upstream_eof", "upstream_eof")
			return responsesFoldResult{AgentUsage: responseUsageMap(incomplete), BilledUsage: summedUsage}, emit(stamp(incomplete))
		}

		for _, entry := range bufferedOrder {
			for _, event := range entry.events {
				if _, ok := event["output_index"]; ok {
					event["output_index"] = dsOutputIndex
				}
				if err := emit(stamp(event)); err != nil {
					return responsesFoldResult{BilledUsage: summedUsage}, err
				}
			}
			dsOutputIndex++
			finalOutput = append(finalOutput, entry.item)
		}

		agentUsage := responsesFoldAgentUsage(firstUsage, summedUsage, usage, true)
		terminalEvent := responsesFoldTerminalEvent(terminal, baseResponse, finalOutput, agentUsage, roundsInfo, summedUsage, stoppedReason, "")
		if err := emit(stamp(terminalEvent)); err != nil {
			return responsesFoldResult{AgentUsage: agentUsage, BilledUsage: summedUsage}, err
		}
		return responsesFoldResult{AgentUsage: agentUsage, BilledUsage: summedUsage}, nil
	}
}

type responsesFoldHTTPEmitter struct {
	c                  *gin.Context
	resp               *http.Response
	sessionManager     *session.SessionManager
	originalReq        *types.ResponsesRequest
	outputCollector    *streamOutputCollector
	reasoningCollector *thinkingcache.ResponsesStreamCollector
	preflightEvents    []string
	preflightText      bytes.Buffer
	committed          bool
	seenEvent          bool
	seenCompleted      bool
	seenUsageOnly      bool
	seenUnknown        bool
	unknownEventType   string
}

func newResponsesFoldHTTPEmitter(
	c *gin.Context,
	resp *http.Response,
	sessionManager *session.SessionManager,
	originalReq *types.ResponsesRequest,
) *responsesFoldHTTPEmitter {
	return &responsesFoldHTTPEmitter{
		c:                  c,
		resp:               resp,
		sessionManager:     sessionManager,
		originalReq:        originalReq,
		outputCollector:    newStreamOutputCollector(),
		reasoningCollector: thinkingcache.NewResponsesStreamCollector(),
	}
}

func (e *responsesFoldHTTPEmitter) emit(event map[string]interface{}) error {
	eventString := formatResponsesFoldSSE(event)
	if e.committed {
		return e.writeEvent(eventString)
	}

	eventType, _ := event["type"].(string)
	if upstreamErr, ok := detectResponsesStreamError(eventString, eventType); ok {
		if r, m := detectResponsesErrorBlacklist(upstreamErr); r != "" {
			return &common.ErrBlacklistKey{Reason: r, Message: m}
		}
		diagnostic := formatResponsesErrorDiagnostic(upstreamErr)
		if isRetryableResponsesError(upstreamErr) {
			return fmt.Errorf("%w: %s", common.ErrEmptyStreamResponse, diagnostic)
		}
		return fmt.Errorf("upstream Responses error: %s", diagnostic)
	}

	e.preflightEvents = append(e.preflightEvents, eventString)
	e.seenEvent = true
	e.seenCompleted = e.seenCompleted || eventType == "response.completed"
	e.seenUsageOnly = e.seenUsageOnly || isResponsesUsageOnlyEvent(eventString)
	if t, ok := firstUnknownResponsesEventType(eventString); ok {
		e.seenUnknown = true
		if e.unknownEventType == "" {
			e.unknownEventType = t
		}
	}
	extractResponsesTextFromEvent(eventString, &e.preflightText)

	hasContent := common.HasResponsesSemanticContent(eventString) || !common.IsEffectivelyEmptyStreamText(e.preflightText.String())
	if hasContent {
		return e.commit()
	}
	if isResponsesFoldTerminalType(eventType) {
		if isCompactionV2UsageOnlyStream(e.originalReq != nil && hasCompactionTrigger(e.originalReq.Input), e.seenCompleted, e.seenUsageOnly) {
			return e.commit()
		}
		diagnostic := buildResponsesPreflightDiagnostic(e.seenEvent, e.seenCompleted, e.seenUsageOnly, e.seenUnknown, e.unknownEventType, e.preflightText.String())
		return fmt.Errorf("%w: %s", common.ErrEmptyStreamResponse, diagnostic)
	}
	return nil
}

func (e *responsesFoldHTTPEmitter) commit() error {
	if e.committed {
		return nil
	}
	utils.ForwardResponseHeaders(e.resp.Header, e.c.Writer)
	e.c.Header("Content-Type", "text/event-stream")
	e.c.Header("Cache-Control", "no-cache")
	e.c.Header("Connection", "keep-alive")
	e.c.Header("X-Accel-Buffering", "no")
	e.c.Status(e.resp.StatusCode)
	e.committed = true
	for _, event := range e.preflightEvents {
		if err := e.writeEvent(event); err != nil {
			return err
		}
	}
	e.preflightEvents = nil
	return nil
}

func (e *responsesFoldHTTPEmitter) writeEvent(event string) error {
	e.outputCollector.processEvent(event)
	e.reasoningCollector.ProcessEvent(event)
	if _, err := e.c.Writer.Write([]byte(event)); err != nil {
		return err
	}
	if flusher, ok := e.c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (e *responsesFoldHTTPEmitter) finish() {
	streamSessionID := writeStreamSession(e.sessionManager, e.originalReq, e.outputCollector)
	if streamSessionID != "" {
		e.reasoningCollector.Store(streamSessionID)
	}
}

type responsesSSEFrame struct {
	event map[string]interface{}
	done  bool
}

type responsesSSEIterator struct {
	scanner   *bufio.Scanner
	eventName string
	dataLines []string
}

func newResponsesSSEIterator(reader io.Reader) *responsesSSEIterator {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), utils.ResponsesSSEScannerMaxBufferSize)
	return &responsesSSEIterator{scanner: scanner}
}

func (it *responsesSSEIterator) next() (responsesSSEFrame, bool, error) {
	for it.scanner.Scan() {
		line := normalizeResponsesSSEFieldLine(strings.TrimRight(it.scanner.Text(), "\r"))
		if line == "" {
			if frame, ok, err := it.flush(); ok || err != nil {
				return frame, ok, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			it.eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			it.dataLines = append(it.dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := it.scanner.Err(); err != nil {
		return responsesSSEFrame{}, false, err
	}
	return it.flush()
}

func (it *responsesSSEIterator) flush() (responsesSSEFrame, bool, error) {
	if len(it.dataLines) == 0 {
		it.eventName = ""
		return responsesSSEFrame{}, false, nil
	}
	data := strings.Join(it.dataLines, "\n")
	it.dataLines = nil
	eventName := it.eventName
	it.eventName = ""
	if data == "[DONE]" {
		return responsesSSEFrame{done: true}, true, nil
	}
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return responsesSSEFrame{}, false, err
	}
	if _, ok := event["type"].(string); !ok && eventName != "" {
		event["type"] = eventName
	}
	return responsesSSEFrame{event: event}, true, nil
}

func responsesFoldInputItems(input interface{}) []interface{} {
	switch v := input.(type) {
	case []interface{}:
		return append([]interface{}{}, v...)
	case string:
		return []interface{}{map[string]interface{}{
			"type": "message",
			"role": "user",
			"content": []interface{}{map[string]interface{}{
				"type": "input_text",
				"text": v,
			}},
		}}
	default:
		if input == nil {
			return nil
		}
		return []interface{}{input}
	}
}

func responsesFoldNextRoundBody(base map[string]interface{}, inputItems []interface{}) map[string]interface{} {
	body := cloneStringInterfaceMap(base)
	delete(body, "transformer_metadata")
	body["stream"] = true
	body["input"] = inputItems
	include := responsesFoldIncludeValues(body["include"])
	if !stringSliceContains(include, responsesFoldEncryptedInclude) {
		include = append(include, responsesFoldEncryptedInclude)
	}
	body["include"] = include
	delete(body, "previous_response_id")
	return body
}

func responsesFoldIncludeValues(raw interface{}) []string {
	switch v := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := stringFromInterface(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return append([]string{}, v...)
	case string:
		return []string{v}
	default:
		return nil
	}
}

func responsesFoldCommentaryNudge() map[string]interface{} {
	return map[string]interface{}{
		"type": "message",
		"role": "assistant",
		"content": []interface{}{map[string]interface{}{
			"type": "output_text",
			"text": responsesFoldMarkerText,
		}},
		"phase": "commentary",
	}
}

func responsesFoldReasoningTokens(usage map[string]interface{}) (int, bool) {
	if usage == nil {
		return 0, false
	}
	details := mapFromInterface(usage["output_tokens_details"])
	return intFromInterface(details["reasoning_tokens"])
}

func responsesFoldTierN(tokens int, ok bool) (int, bool) {
	if !ok || tokens < responsesFoldStep-2 || (tokens+2)%responsesFoldStep != 0 {
		return 0, false
	}
	return (tokens + 2) / responsesFoldStep, true
}

func responsesFoldInContinueWindow(n int) bool {
	return n >= responsesFoldMinN && (responsesFoldMaxN == 0 || n <= responsesFoldMaxN)
}

func responsesFoldSumUsage(acc map[string]interface{}, usage map[string]interface{}) {
	if usage == nil {
		return
	}
	for _, key := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		if value, ok := intFromInterface(usage[key]); ok {
			acc[key] = intFromInterfaceDefault(acc[key]) + value
		}
	}
	if cached, ok := intFromInterface(mapFromInterface(usage["input_tokens_details"])["cached_tokens"]); ok {
		details := mapFromInterface(acc["input_tokens_details"])
		details["cached_tokens"] = intFromInterfaceDefault(details["cached_tokens"]) + cached
		acc["input_tokens_details"] = details
	}
	if reasoning, ok := responsesFoldReasoningTokens(usage); ok {
		details := mapFromInterface(acc["output_tokens_details"])
		details["reasoning_tokens"] = intFromInterfaceDefault(details["reasoning_tokens"]) + reasoning
		acc["output_tokens_details"] = details
	}
}

func responsesFoldAgentUsage(first map[string]interface{}, summed map[string]interface{}, finalRound map[string]interface{}, flushedFinal bool) map[string]interface{} {
	inputTokens := intFromInterfaceDefault(first["input_tokens"])
	cached, hasCached := intFromInterface(mapFromInterface(first["input_tokens_details"])["cached_tokens"])
	reasoning := intFromInterfaceDefault(mapFromInterface(summed["output_tokens_details"])["reasoning_tokens"])
	finalPart := 0
	if flushedFinal && finalRound != nil {
		outputTokens := intFromInterfaceDefault(finalRound["output_tokens"])
		finalReasoning, _ := responsesFoldReasoningTokens(finalRound)
		if outputTokens > finalReasoning {
			finalPart = outputTokens - finalReasoning
		}
	}
	usage := map[string]interface{}{
		"input_tokens":          inputTokens,
		"output_tokens":         reasoning + finalPart,
		"total_tokens":          inputTokens + reasoning + finalPart,
		"output_tokens_details": map[string]interface{}{"reasoning_tokens": reasoning},
	}
	if hasCached {
		usage["input_tokens_details"] = map[string]interface{}{"cached_tokens": cached}
	}
	return usage
}

func responsesFoldTerminalEvent(
	upstreamTerminal map[string]interface{},
	baseResponse map[string]interface{},
	output []interface{},
	usage map[string]interface{},
	rounds []map[string]interface{},
	billed map[string]interface{},
	stoppedReason string,
	incompleteReason string,
) map[string]interface{} {
	upstreamResponse := map[string]interface{}{}
	if upstreamTerminal != nil {
		upstreamResponse = mapFromInterface(upstreamTerminal["response"])
	}
	response := cloneStringInterfaceMap(upstreamResponse)
	if len(baseResponse) > 0 {
		response = cloneStringInterfaceMap(baseResponse)
	}
	response["output"] = output
	response["usage"] = usage
	metadata := mapFromInterface(response["metadata"])
	metadata["proxy_rounds"] = rounds
	metadata["proxy_billed_usage"] = billed
	if stoppedReason != "" {
		metadata["proxy_stopped_reason"] = stoppedReason
	}
	response["metadata"] = metadata

	if incompleteReason != "" {
		response["status"] = "incomplete"
		response["incomplete_details"] = map[string]interface{}{"reason": incompleteReason}
		return map[string]interface{}{"type": "response.incomplete", "response": response}
	}

	status := stringFromInterface(upstreamResponse["status"])
	if status == "" {
		status = "completed"
	}
	response["status"] = status
	if details, ok := upstreamResponse["incomplete_details"]; ok {
		response["incomplete_details"] = details
	}
	eventType := "response.completed"
	if upstreamTerminal != nil {
		if t := stringFromInterface(upstreamTerminal["type"]); t != "" {
			eventType = t
		}
	}
	return map[string]interface{}{"type": eventType, "response": response}
}

func responsesFoldUsageFromTerminal(event map[string]interface{}) map[string]interface{} {
	if response := mapFromInterface(event["response"]); len(response) > 0 {
		if usage := mapFromInterface(response["usage"]); len(usage) > 0 {
			return usage
		}
	}
	return mapFromInterface(event["usage"])
}

func responseUsageMap(event map[string]interface{}) map[string]interface{} {
	return responsesFoldUsageFromTerminal(event)
}

func responsesFoldOutputIndexKey(value interface{}) string {
	switch v := value.(type) {
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.Itoa(int(v))
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func formatResponsesFoldSSE(event map[string]interface{}) string {
	eventType := stringFromInterface(event["type"])
	if eventType == "" {
		eventType = "message"
	}
	data, _ := utils.MarshalJSONNoEscape(event)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, data)
}

func responsesFoldMetricsUsage(usage map[string]interface{}) *types.Usage {
	if usage == nil {
		return nil
	}
	responsesUsage := types.ResponsesUsage{
		InputTokens:  intFromInterfaceDefault(usage["input_tokens"]),
		OutputTokens: intFromInterfaceDefault(usage["output_tokens"]),
		TotalTokens:  intFromInterfaceDefault(usage["total_tokens"]),
	}
	if cached, ok := intFromInterface(mapFromInterface(usage["input_tokens_details"])["cached_tokens"]); ok {
		responsesUsage.InputTokensDetails = &types.InputTokensDetails{CachedTokens: cached}
	}
	if reasoning, ok := intFromInterface(mapFromInterface(usage["output_tokens_details"])["reasoning_tokens"]); ok {
		responsesUsage.OutputTokensDetails = &types.OutputTokensDetails{ReasoningTokens: reasoning}
	}
	return metricsUsageFromResponsesUsage(responsesUsage, responsesUsage.InputTokens)
}

func isResponsesFoldTerminalType(eventType string) bool {
	switch eventType {
	case "response.completed", "response.failed", "response.incomplete":
		return true
	default:
		return false
	}
}

func mapFromInterface(value interface{}) map[string]interface{} {
	if value == nil {
		return map[string]interface{}{}
	}
	if m, ok := value.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

func cloneStringInterfaceMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func stringFromInterface(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return ""
	}
}

func intFromInterface(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		i, err := v.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func intFromInterfaceDefault(value interface{}) int {
	i, _ := intFromInterface(value)
	return i
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
