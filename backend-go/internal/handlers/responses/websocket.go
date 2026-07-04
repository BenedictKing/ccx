package responses

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/net/proxy"
)

const (
	responsesWebSocketWriteTimeout      = 15 * time.Second
	responsesWebSocketReadLimit         = 32 << 20
	responsesWebSocketV2BetaHeader      = "OpenAI-Beta"
	responsesWebSocketV2BetaHeaderValue = "responses_websockets=2026-02-06"
)

var responsesWebSocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

// WebSocketHandler 支持 Codex 原生 Responses WebSocket 协议。
// 对原生 Responses 上游使用 WebSocket v2 透传；其他上游保留 HTTP/SSE bridge。
func WebSocketHandler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	httpHandler := Handler(envCfg, cfgManager, sessionManager, channelScheduler)
	bridgeRouter := gin.New()
	bridgeRouter.POST("/v1/responses", httpHandler)
	bridgeRouter.POST("/:routePrefix/v1/responses", httpHandler)

	return gin.HandlerFunc(func(c *gin.Context) {
		if !isWebSocketUpgrade(c.Request) {
			c.Status(http.StatusMethodNotAllowed)
			return
		}
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		conn, err := responsesWebSocketUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadLimit(responsesWebSocketReadLimit)

		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					closeWithWebSocketError(conn, http.StatusBadRequest, "websocket_read_error", err.Error())
				}
				return
			}
			if messageType != websocket.TextMessage {
				closeWithWebSocketError(conn, http.StatusBadRequest, "invalid_message_type", "only text messages are supported")
				return
			}
			if _, err := parseWebSocketResponseCreatePayload(payload); err != nil {
				writeWebSocketError(conn, http.StatusBadRequest, "invalid_request", err.Error())
				continue
			}

			selection, err := selectResponsesWebSocketChannel(c, cfgManager, channelScheduler, payload)
			if err != nil {
				writeWebSocketError(conn, http.StatusServiceUnavailable, "channel_selection_error", err.Error())
				return
			}
			if selection != nil && selection.Upstream != nil && selection.Upstream.ServiceType == "responses" {
				if err := serveNativeResponsesWebSocket(c, conn, cfgManager, sessionManager, channelScheduler, selection, payload); err != nil {
					if isWebSocketClosed(err) {
						return
					}
					writeWebSocketError(conn, http.StatusBadGateway, "upstream_websocket_error", err.Error())
					return
				}
				return
			}

			serveResponsesWebSocketBridgeLoop(c, conn, bridgeRouter, payload)
			return
		}
	})
}

func isWebSocketUpgrade(r *http.Request) bool {
	return headerContainsToken(r.Header, "Connection", "upgrade") &&
		headerContainsToken(r.Header, "Upgrade", "websocket")
}

func headerContainsToken(header http.Header, key, token string) bool {
	for _, value := range header.Values(key) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func parseWebSocketResponseCreatePayload(payload []byte) (map[string]interface{}, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("解析 response.create 失败: %w", err)
	}

	msgType, _ := req["type"].(string)
	if msgType != "response.create" {
		return nil, fmt.Errorf("不支持的 WebSocket 消息类型: %s", msgType)
	}
	return req, nil
}

func normalizeWebSocketResponseCreatePayload(payload []byte) ([]byte, bool, error) {
	req, err := parseWebSocketResponseCreatePayload(payload)
	if err != nil {
		return nil, false, err
	}
	warmup := req["generate"] == false

	delete(req, "type")
	delete(req, "client_metadata")
	delete(req, "generate")
	req["stream"] = true

	body, err := json.Marshal(req)
	if err != nil {
		return nil, false, fmt.Errorf("序列化 response.create 失败: %w", err)
	}
	return body, warmup, nil
}

func normalizeNativeWebSocketResponseCreatePayload(payload []byte) ([]byte, error) {
	req, err := parseWebSocketResponseCreatePayload(payload)
	if err != nil {
		return nil, err
	}
	req["stream"] = true

	body, err := utils.MarshalJSONNoEscape(req)
	if err != nil {
		return nil, fmt.Errorf("序列化 response.create 失败: %w", err)
	}
	return body, nil
}

func selectResponsesWebSocketChannel(
	c *gin.Context,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	payload []byte,
) (*scheduler.SelectionResult, error) {
	if cfgManager == nil || channelScheduler == nil {
		return nil, errors.New("Responses WebSocket 渠道调度器未初始化")
	}

	req, err := parseWebSocketResponseCreatePayload(payload)
	if err != nil {
		return nil, err
	}
	model, _ := req["model"].(string)
	affinityBody := common.NormalizeMetadataUserID(payload)
	userID := utils.ExtractUnifiedSessionID(c, affinityBody)
	agentCtx := utils.ExtractAgentContext(c, payload)
	agentRole := ""
	if agentCtx != nil {
		agentRole = agentCtx.AgentRole
		c.Set("agentContext", agentCtx)
	}

	cfg := cfgManager.GetConfig()
	contextRequirement := common.BuildResponsesContextRequirement(payload, cfg.ContextRouting)
	common.ApplyAgentModelProfile(contextRequirement, model, cfg)

	return channelScheduler.SelectChannelWithOptions(c.Request.Context(), scheduler.SelectionOptions{
		UserID:             userID,
		Kind:               scheduler.ChannelKindResponses,
		Model:              model,
		RoutePrefix:        c.Param("routePrefix"),
		ChannelName:        c.GetHeader("X-Channel"),
		ContextRequirement: contextRequirement,
		AgentRole:          agentRole,
	})
}

func serveResponsesWebSocketBridgeLoop(
	c *gin.Context,
	conn *websocket.Conn,
	bridgeRouter http.Handler,
	firstPayload []byte,
) {
	payload := firstPayload
	for {
		if payload == nil {
			messageType, nextPayload, err := conn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					closeWithWebSocketError(conn, http.StatusBadRequest, "websocket_read_error", err.Error())
				}
				return
			}
			if messageType != websocket.TextMessage {
				closeWithWebSocketError(conn, http.StatusBadRequest, "invalid_message_type", "only text messages are supported")
				return
			}
			payload = nextPayload
		}

		requestBody, warmup, err := normalizeWebSocketResponseCreatePayload(payload)
		payload = nil
		if err != nil {
			writeWebSocketError(conn, http.StatusBadRequest, "invalid_request", err.Error())
			continue
		}
		if warmup {
			if err := writeWebSocketWarmupResponse(conn); err != nil {
				return
			}
			continue
		}

		if err := serveResponseCreateOverWebSocket(c, conn, bridgeRouter, requestBody); err != nil {
			if isWebSocketClosed(err) {
				return
			}
			writeWebSocketError(conn, http.StatusInternalServerError, "stream_error", err.Error())
		}
	}
}

type nativeResponsesWebSocketRequest struct {
	URL    string
	Header http.Header
	Body   []byte
}

func serveNativeResponsesWebSocket(
	c *gin.Context,
	clientConn *websocket.Conn,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
	channelScheduler *scheduler.ChannelScheduler,
	selection *scheduler.SelectionResult,
	firstPayload []byte,
) error {
	upstream := selection.Upstream
	if upstream == nil {
		return errors.New("Responses WebSocket 未选择到上游渠道")
	}
	if len(upstream.APIKeys) == 0 {
		return fmt.Errorf("Responses 渠道 %q 未配置 API 密钥", upstream.Name)
	}

	upstreamCopy := upstream.Clone()
	baseURL := selectResponsesWebSocketBaseURL(channelScheduler, selection.ChannelIndex, upstreamCopy)
	if baseURL != "" {
		upstreamCopy.BaseURL = baseURL
	}
	apiKey, err := cfgManager.GetNextResponsesAPIKey(upstreamCopy, nil)
	if err != nil {
		return err
	}

	firstRequest, err := buildNativeResponsesWebSocketRequest(c, upstreamCopy, apiKey, firstPayload, sessionManager)
	if err != nil {
		return err
	}
	dialer, err := newResponsesWebSocketDialer(upstreamCopy)
	if err != nil {
		return err
	}

	upstreamConn, resp, err := dialer.DialContext(c.Request.Context(), firstRequest.URL, firstRequest.Header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("上游 WebSocket 握手失败: HTTP %d", resp.StatusCode)
		}
		return err
	}
	defer upstreamConn.Close()
	upstreamConn.SetReadLimit(responsesWebSocketReadLimit)
	if baseURL != "" {
		channelScheduler.MarkURLSuccess(scheduler.ChannelKindResponses, selection.ChannelIndex, baseURL)
	}

	if err := writeWebSocketText(upstreamConn, firstRequest.Body); err != nil {
		return err
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- copyNativeResponsesClientToUpstream(c, clientConn, upstreamConn, upstreamCopy, apiKey, sessionManager)
	}()
	go func() {
		errCh <- copyNativeResponsesUpstreamToClient(upstreamConn, clientConn)
	}()

	select {
	case <-c.Request.Context().Done():
	case <-errCh:
	}

	_ = upstreamConn.Close()
	_ = clientConn.Close()
	select {
	case <-errCh:
	case <-time.After(time.Second):
	}
	return nil
}

func selectResponsesWebSocketBaseURL(
	channelScheduler *scheduler.ChannelScheduler,
	channelIndex int,
	upstream *config.UpstreamConfig,
) string {
	if upstream == nil || channelScheduler == nil {
		return ""
	}
	baseURLs := upstream.GetAllBaseURLs()
	if len(baseURLs) == 0 {
		return ""
	}
	urls := channelScheduler.GetSortedURLsForChannel(scheduler.ChannelKindResponses, channelIndex, baseURLs)
	if len(urls) == 0 {
		return baseURLs[0]
	}
	return urls[0].URL
}

func buildNativeResponsesWebSocketRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	apiKey string,
	payload []byte,
	sessionManager *session.SessionManager,
) (*nativeResponsesWebSocketRequest, error) {
	providerReq, body, err := buildNativeResponsesProviderRequest(c, upstream, apiKey, payload, sessionManager)
	if err != nil {
		return nil, err
	}
	targetURL, err := httpURLToWebSocketURL(providerReq.URL.String())
	if err != nil {
		return nil, err
	}
	header := providerReq.Header.Clone()
	stripWebSocketRequestHeaders(header)
	header.Set(responsesWebSocketV2BetaHeader, responsesWebSocketV2BetaHeaderValue)
	header.Set("Content-Type", "application/json")

	return &nativeResponsesWebSocketRequest{
		URL:    targetURL,
		Header: header,
		Body:   body,
	}, nil
}

func buildNativeResponsesWebSocketBody(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	apiKey string,
	payload []byte,
	sessionManager *session.SessionManager,
) ([]byte, error) {
	_, body, err := buildNativeResponsesProviderRequest(c, upstream, apiKey, payload, sessionManager)
	return body, err
}

func buildNativeResponsesProviderRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	apiKey string,
	payload []byte,
	sessionManager *session.SessionManager,
) (*http.Request, []byte, error) {
	body, err := normalizeNativeWebSocketResponseCreatePayload(payload)
	if err != nil {
		return nil, nil, err
	}
	providerReq, _, err := (&providers.ResponsesProvider{SessionManager: sessionManager}).ConvertBodyToProviderRequest(
		c,
		upstream,
		apiKey,
		body,
		c.Request.URL.Path,
	)
	if err != nil {
		return nil, nil, err
	}
	if providerReq.Body == nil {
		return providerReq, body, nil
	}
	converted, err := io.ReadAll(providerReq.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("读取 Responses WebSocket 上游请求体失败: %w", err)
	}
	return providerReq, converted, nil
}

func copyNativeResponsesClientToUpstream(
	c *gin.Context,
	clientConn *websocket.Conn,
	upstreamConn *websocket.Conn,
	upstream *config.UpstreamConfig,
	apiKey string,
	sessionManager *session.SessionManager,
) error {
	for {
		messageType, payload, err := clientConn.ReadMessage()
		if err != nil {
			return err
		}
		if messageType != websocket.TextMessage {
			return errors.New("only text messages are supported")
		}
		body, err := buildNativeResponsesWebSocketBody(c, upstream, apiKey, payload, sessionManager)
		if err != nil {
			return err
		}
		if err := writeWebSocketText(upstreamConn, body); err != nil {
			return err
		}
	}
}

func copyNativeResponsesUpstreamToClient(upstreamConn *websocket.Conn, clientConn *websocket.Conn) error {
	for {
		messageType, payload, err := upstreamConn.ReadMessage()
		if err != nil {
			return err
		}
		if messageType != websocket.TextMessage {
			continue
		}
		if err := writeWebSocketText(clientConn, payload); err != nil {
			return err
		}
	}
}

func writeWebSocketText(conn *websocket.Conn, payload []byte) error {
	_ = conn.SetWriteDeadline(time.Now().Add(responsesWebSocketWriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func stripWebSocketRequestHeaders(header http.Header) {
	for _, key := range []string{
		"Connection",
		"Upgrade",
		"Sec-WebSocket-Accept",
		"Sec-WebSocket-Extensions",
		"Sec-WebSocket-Key",
		"Sec-WebSocket-Protocol",
		"Sec-WebSocket-Version",
		"Host",
	} {
		header.Del(key)
	}
}

func httpURLToWebSocketURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("无效的 Responses WebSocket URL: %s", rawURL)
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("不支持的 Responses WebSocket URL 协议: %s", u.Scheme)
	}
	return u.String(), nil
}

func newResponsesWebSocketDialer(upstream *config.UpstreamConfig) (*websocket.Dialer, error) {
	dialer := *websocket.DefaultDialer
	if upstream != nil && upstream.InsecureSkipVerify {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if upstream == nil || strings.TrimSpace(upstream.ProxyURL) == "" {
		return &dialer, nil
	}
	if err := applyResponsesWebSocketProxy(&dialer, upstream.ProxyURL); err != nil {
		return nil, err
	}
	return &dialer, nil
}

func applyResponsesWebSocketProxy(dialer *websocket.Dialer, proxyAddr string) error {
	u, err := url.Parse(proxyAddr)
	if err != nil {
		return fmt.Errorf("解析 WebSocket 代理地址失败: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		dialer.Proxy = http.ProxyURL(u)
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: password}
		}
		socksDialer, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if err != nil {
			return fmt.Errorf("创建 WebSocket SOCKS5 代理失败: %w", err)
		}
		if contextDialer, ok := socksDialer.(proxy.ContextDialer); ok {
			dialer.NetDialContext = contextDialer.DialContext
			return nil
		}
		dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return socksDialer.Dial(network, addr)
		}
	default:
		return fmt.Errorf("不支持的 WebSocket 代理协议: %s", u.Scheme)
	}
	return nil
}

func serveResponseCreateOverWebSocket(
	parent *gin.Context,
	conn *websocket.Conn,
	httpHandler http.Handler,
	requestBody []byte,
) error {
	req, err := http.NewRequestWithContext(
		parent.Request.Context(),
		http.MethodPost,
		parent.Request.URL.RequestURI(),
		bytes.NewReader(requestBody),
	)
	if err != nil {
		return err
	}
	copyWebSocketRequestHeaders(parent.Request.Header, req.Header)
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(requestBody))

	writer := newResponsesWebSocketWriter(conn)
	httpHandler.ServeHTTP(writer, req)
	return writer.finish()
}

func copyWebSocketRequestHeaders(src http.Header, dst http.Header) {
	for key, values := range src {
		switch strings.ToLower(key) {
		case "connection", "upgrade", "sec-websocket-key", "sec-websocket-version", "sec-websocket-extensions", "sec-websocket-protocol":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

type responsesWebSocketWriter struct {
	conn        *websocket.Conn
	header      http.Header
	status      int
	size        int
	written     bool
	writeErr    error
	mu          sync.Mutex
	sseBuffer   bytes.Buffer
	currentData strings.Builder
}

func newResponsesWebSocketWriter(conn *websocket.Conn) *responsesWebSocketWriter {
	return &responsesWebSocketWriter{
		conn:   conn,
		header: make(http.Header),
		status: http.StatusOK,
		size:   -1,
	}
}

func (w *responsesWebSocketWriter) Header() http.Header {
	return w.header
}

func (w *responsesWebSocketWriter) WriteHeader(statusCode int) {
	if statusCode <= 0 {
		return
	}
	if w.Written() && w.status != statusCode {
		return
	}
	w.status = statusCode
	if statusCode >= http.StatusBadRequest {
		w.writeErr = writeWebSocketError(w.conn, statusCode, "upstream_error", http.StatusText(statusCode))
	}
}

func (w *responsesWebSocketWriter) WriteHeaderNow() {
	w.written = true
	if w.size < 0 {
		w.size = 0
	}
}

func (w *responsesWebSocketWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.writeErr != nil {
		return 0, w.writeErr
	}
	w.WriteHeaderNow()
	w.size += len(data)
	if w.status >= http.StatusBadRequest {
		return len(data), nil
	}

	w.sseBuffer.Write(data)
	w.flushSSEFrames(false)
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(data), nil
}

func (w *responsesWebSocketWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *responsesWebSocketWriter) Status() int {
	return w.status
}

func (w *responsesWebSocketWriter) Size() int {
	return w.size
}

func (w *responsesWebSocketWriter) Written() bool {
	return w.written
}

func (w *responsesWebSocketWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.WriteHeaderNow()
	w.flushSSEFrames(false)
}

func (w *responsesWebSocketWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("websocket response writer does not support hijack")
}

func (w *responsesWebSocketWriter) CloseNotify() <-chan bool {
	ch := make(chan bool)
	return ch
}

func (w *responsesWebSocketWriter) Pusher() http.Pusher {
	return nil
}

func (w *responsesWebSocketWriter) finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushSSEFrames(true)
	return w.writeErr
}

func (w *responsesWebSocketWriter) flushSSEFrames(final bool) {
	for {
		line, ok := takeSSELine(&w.sseBuffer)
		if !ok {
			break
		}
		w.processSSELine(line)
		if w.writeErr != nil {
			return
		}
	}
	if final && w.sseBuffer.Len() > 0 {
		w.processSSELine(w.sseBuffer.String())
		w.sseBuffer.Reset()
	}
	if final && strings.TrimSpace(w.currentData.String()) != "" {
		w.sendCurrentData()
	}
}

func (w *responsesWebSocketWriter) processSSELine(line string) {
	line = strings.TrimRight(line, "\r")
	if line == "" {
		w.sendCurrentData()
		return
	}
	if strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
		return
	}
	if !strings.HasPrefix(line, "data:") {
		return
	}

	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" || data == "[DONE]" {
		return
	}
	if w.currentData.Len() > 0 {
		w.currentData.WriteByte('\n')
	}
	w.currentData.WriteString(data)
}

func (w *responsesWebSocketWriter) sendCurrentData() {
	data := strings.TrimSpace(w.currentData.String())
	w.currentData.Reset()
	if data == "" || w.writeErr != nil {
		return
	}

	_ = w.conn.SetWriteDeadline(time.Now().Add(responsesWebSocketWriteTimeout))
	w.writeErr = w.conn.WriteMessage(websocket.TextMessage, []byte(data))
}

func takeSSELine(buf *bytes.Buffer) (string, bool) {
	data := buf.Bytes()
	for i, b := range data {
		if b == '\n' {
			lineBytes := make([]byte, i)
			copy(lineBytes, data[:i])
			buf.Next(i + 1)
			return string(lineBytes), true
		}
	}
	return "", false
}

func writeWebSocketError(conn *websocket.Conn, status int, code, message string) error {
	payload, err := json.Marshal(gin.H{
		"type":        "error",
		"status_code": status,
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(responsesWebSocketWriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func writeWebSocketWarmupResponse(conn *websocket.Conn) error {
	created := gin.H{
		"type": "response.created",
		"response": gin.H{
			"id":     "",
			"status": "in_progress",
			"output": []interface{}{},
		},
	}
	completed := gin.H{
		"type": "response.completed",
		"response": gin.H{
			"id":       "",
			"status":   "completed",
			"output":   []interface{}{},
			"end_turn": false,
			"usage": gin.H{
				"input_tokens":  0,
				"output_tokens": 0,
				"total_tokens":  0,
			},
		},
	}
	for _, event := range []gin.H{created, completed} {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		_ = conn.SetWriteDeadline(time.Now().Add(responsesWebSocketWriteTimeout))
		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			return err
		}
	}
	return nil
}

func closeWithWebSocketError(conn *websocket.Conn, status int, code, message string) {
	_ = writeWebSocketError(conn, status, code, message)
	closePayload := websocket.FormatCloseMessage(websocket.CloseUnsupportedData, message)
	_ = conn.WriteControl(websocket.CloseMessage, closePayload, time.Now().Add(responsesWebSocketWriteTimeout))
}

func isWebSocketClosed(err error) bool {
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) ||
		errors.Is(err, net.ErrClosed)
}
