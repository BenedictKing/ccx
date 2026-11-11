package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestBuildTransientDiscoveryChannelRequiresBaseURLAndKey(t *testing.T) {
	req := ChannelDiscoveryRequest{ServiceType: "openai"}
	_, err := buildTransientDiscoveryChannel(req)
	if err == nil {
		t.Fatal("expected error for missing base URL and api key")
	}
}

func TestBuildTransientDiscoveryChannelDoesNotNeedConfigManager(t *testing.T) {
	req := ChannelDiscoveryRequest{
		ChannelKind:        "responses",
		ServiceType:        "openai",
		BaseURLs:           []string{"https://api.example.com/v1"},
		APIKey:             "sk-test",
		AuthHeader:         "bearer",
		CustomHeaders:      map[string]string{"X-Test": "yes"},
		ProxyURL:           "http://127.0.0.1:8080",
		InsecureSkipVerify: true,
		ModelMapping:       map[string]string{"gpt": "actual-main"},
	}

	channel, err := buildTransientDiscoveryChannel(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if channel.Name != "临时发现渠道" || channel.ServiceType != "openai" {
		t.Fatalf("unexpected channel identity: %#v", channel)
	}
	if channel.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("baseUrl = %q", channel.BaseURL)
	}
	if got := channel.GetAllBaseURLs(); len(got) != 1 || got[0] != "https://api.example.com" {
		t.Fatalf("canonical base urls = %#v", got)
	}
	if len(channel.APIKeys) != 1 || channel.APIKeys[0] != "sk-test" {
		t.Fatalf("api keys = %#v", channel.APIKeys)
	}
	if !channel.InsecureSkipVerify || channel.ProxyURL == "" || channel.AuthHeader != "bearer" {
		t.Fatalf("transport fields not copied: %#v", channel)
	}
	if channel.ModelMapping["gpt"] != "actual-main" {
		t.Fatalf("model mapping not copied: %#v", channel.ModelMapping)
	}
}

func TestBuildDiscoveryMappingRecommendationUsesOnlySuccessfulModels(t *testing.T) {
	selected := DiscoverySelectedModels{Strong: "actual-pro", Primary: "actual-main", Fast: "actual-mini"}
	successByProtocol := map[string][]string{"responses": {"actual-main", "actual-mini"}}

	rec := buildDiscoveryMappingRecommendation("responses", "", selected, successByProtocol, []string{"codex"})
	if rec.ChannelKind != "responses" {
		t.Fatalf("channelKind=%q", rec.ChannelKind)
	}
	if rec.ModelMapping["sol"] != "actual-main" || rec.ModelMapping["luna"] != "actual-mini" {
		t.Fatalf("unexpected mapping: %#v", rec.ModelMapping)
	}
	if rec.ModelMapping["codex-auto-review"] == "actual-pro" {
		t.Fatalf("codex-auto-review should not map to failed actual-pro: %#v", rec.ModelMapping)
	}
	if _, ok := rec.ModelMapping["gpt-5"]; ok {
		t.Fatalf("codex recommendation should keep stable source aliases only: %#v", rec.ModelMapping)
	}
	if rec.ReasoningMapping["sol"] != "max" || rec.ReasoningMapping["luna"] != "high" || rec.ReasoningMapping["codex-auto-review"] != "high" {
		t.Fatalf("unexpected reasoning mapping: %#v", rec.ReasoningMapping)
	}
	if len(rec.SupportedModels) != 0 {
		t.Fatalf("discovery should not set supportedModels: %#v", rec.SupportedModels)
	}
	if len(rec.Evidence) != 1 || rec.Evidence[0].Type != "reasoning" || !strings.Contains(rec.Evidence[0].Message, "验证工具调用与思考回传") {
		t.Fatalf("reasoning evidence should explain follow-up capability probes: %#v", rec.Evidence)
	}
}

func TestBuildDiscoveryMappingRecommendationUsesStableClaudeSourceAliases(t *testing.T) {
	selected := DiscoverySelectedModels{Strong: "claude-opus-4-7", Primary: "claude-opus-4-7", Fast: "claude-opus-4-7"}
	successByProtocol := map[string][]string{"messages": {"claude-opus-4-7"}}

	rec := buildDiscoveryMappingRecommendation("messages", "", selected, successByProtocol, []string{"claude-code"})
	wantSources := map[string]string{
		"fable":  "claude-opus-4-7",
		"haiku":  "claude-opus-4-7",
		"opus":   "claude-opus-4-7",
		"sonnet": "claude-opus-4-7",
	}
	if len(rec.ModelMapping) != len(wantSources) {
		t.Fatalf("unexpected source alias count: %#v", rec.ModelMapping)
	}
	for source, target := range wantSources {
		if rec.ModelMapping[source] != target {
			t.Fatalf("modelMapping[%q]=%q, want %q; full mapping=%#v", source, rec.ModelMapping[source], target, rec.ModelMapping)
		}
	}
	for _, internalRole := range []string{"strong", "primary", "fast"} {
		if _, ok := rec.ModelMapping[internalRole]; ok {
			t.Fatalf("internal role %q should not be exposed as source alias: %#v", internalRole, rec.ModelMapping)
		}
	}
	if rec.ReasoningMapping["opus"] != "max" || rec.ReasoningMapping["sonnet"] != "max" || rec.ReasoningMapping["haiku"] != "high" || rec.ReasoningMapping["fable"] != "max" {
		t.Fatalf("unexpected Claude reasoning mapping: %#v", rec.ReasoningMapping)
	}
	if len(rec.SupportedModels) != 0 {
		t.Fatalf("discovery should not set supportedModels: %#v", rec.SupportedModels)
	}
}

// 当 messages 协议探测失败但 responses 协议成功时，
// buildDiscoveryMappingRecommendation 应降级使用 responses 的模型，
// 确保 Claude 别名映射仍能生成（避免发现结果空映射）。
func TestBuildDiscoveryMappingRecommendationFallsBackWhenChannelProtocolFails(t *testing.T) {
	selected := DiscoverySelectedModels{Strong: "gpt-5.5", Primary: "gpt-5.4", Fast: "gpt-5.4-mini"}
	// messages 协议失败，responses 协议成功
	successByProtocol := map[string][]string{
		"responses": {"gpt-5.4", "gpt-5.4-mini", "gpt-5.5"},
	}

	rec := buildDiscoveryMappingRecommendation("messages", "", selected, successByProtocol, []string{"claude-code"})

	if len(rec.ModelMapping) == 0 {
		t.Fatal("expected non-empty modelMapping when falling back to successful protocol models; got empty")
	}
	for _, alias := range []string{"opus", "sonnet", "haiku", "fable"} {
		if _, ok := rec.ModelMapping[alias]; !ok {
			t.Fatalf("expected Claude alias %q in modelMapping; got %#v", alias, rec.ModelMapping)
		}
	}
	if len(rec.SupportedModels) != 0 {
		t.Fatalf("discovery should not set supportedModels: %#v", rec.SupportedModels)
	}
}

func TestRecommendDiscoveryChannelKindKeepsExplicitRequestedProtocol(t *testing.T) {
	protocols := []DiscoveryProtocolResult{
		{Protocol: "messages", Success: false},
		{Protocol: "responses", Success: true},
	}

	got := recommendDiscoveryChannelKind("messages", []string{"codex"}, protocols)
	if got != "messages" {
		t.Fatalf("recommended channelKind=%q, want messages", got)
	}
}

func TestRecommendDiscoveryChannelKindFallsBackWithoutExplicitProtocol(t *testing.T) {
	protocols := []DiscoveryProtocolResult{
		{Protocol: "messages", Success: true},
		{Protocol: "responses", Success: true},
	}

	got := recommendDiscoveryChannelKind("", []string{"codex"}, protocols)
	if got != "responses" {
		t.Fatalf("recommended channelKind=%q, want responses", got)
	}
}

func TestSelectDiscoveryModelsPrefersToolCapableModels(t *testing.T) {
	global := map[string]config.UpstreamModelCapability{
		"plain-main": {
			ContextWindowTokens: 1000000,
			Capabilities:        map[string]bool{"toolCalls": false},
		},
		"tool-main": {
			ContextWindowTokens: 200000,
			Capabilities:        map[string]bool{"toolCalls": true},
		},
	}

	selected := selectDiscoveryModels([]string{"plain-main", "tool-main"}, global)
	if selected.Primary != "tool-main" {
		t.Fatalf("primary=%q, want tool-main", selected.Primary)
	}
}

func TestDiscoveryRecommendationUsesMiMoVisionCapabilities(t *testing.T) {
	models := []string{"mimo-v2.5", "mimo-v2.5-pro"}
	selected := selectDiscoveryModels(models, nil)
	successByProtocol := map[string][]string{"messages": models}

	rec := buildDiscoveryMappingRecommendation("messages", "", selected, successByProtocol, []string{"claude-code"})
	applyDiscoveryModelCapabilityRecommendations(&rec, models, successByProtocol["messages"], nil)

	for source, target := range rec.ModelMapping {
		if target != "mimo-v2.5-pro" {
			t.Fatalf("modelMapping[%q]=%q, want mimo-v2.5-pro; full mapping=%#v", source, target, rec.ModelMapping)
		}
	}
	if !sameStringSet(rec.NoVisionModels, []string{"mimo-v2.5-pro"}) {
		t.Fatalf("noVisionModels=%#v", rec.NoVisionModels)
	}
	if rec.VisionFallbackModel != "mimo-v2.5" {
		t.Fatalf("visionFallbackModel=%q, want mimo-v2.5", rec.VisionFallbackModel)
	}
}

func sameStringSet(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, item := range got {
		seen[item]++
	}
	for _, item := range want {
		if seen[item] == 0 {
			return false
		}
		seen[item]--
	}
	return true
}

func TestChannelDiscoveryHandlerDiscoversTransientResponsesChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"actual-main"},{"id":"actual-mini"}]}`))
		case "/v1/responses":
			if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
				t.Fatalf("Authorization header = %q", got)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
			_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery", ChannelDiscovery(nil))

	body := []byte(`{
		"channelKind":"responses",
		"serviceType":"openai",
		"baseUrls":["` + upstream.URL + `"],
		"apiKey":"sk-test",
		"targetClients":["codex"]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var resp ChannelDiscoveryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Models.Source != "models_endpoint" {
		t.Fatalf("models source=%q", resp.Models.Source)
	}
	if resp.Recommendation.ChannelKind != "responses" {
		t.Fatalf("recommended channelKind=%q", resp.Recommendation.ChannelKind)
	}
	if resp.Recommendation.ModelMapping["sol"] != "actual-main" {
		t.Fatalf("modelMapping=%#v", resp.Recommendation.ModelMapping)
	}
	var responsesOK bool
	for _, protocol := range resp.Protocols {
		if protocol.Protocol == "responses" && protocol.Success {
			responsesOK = true
			break
		}
	}
	if !responsesOK {
		t.Fatalf("protocols=%#v", resp.Protocols)
	}
}

func TestChannelDiscoveryUsesInjectedModelsFetcher(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/anthropic/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	fetchers := ChannelDiscoveryModelFetchers{
		"messages": func(_ context.Context, req DiscoveryModelsFetchRequest) (DiscoveryModelsFetchResponse, error) {
			if req.BaseURL != upstream.URL+"/anthropic" {
				t.Fatalf("baseURL=%q", req.BaseURL)
			}
			if req.APIKey != "sk-test" || req.ServiceType != "claude" {
				t.Fatalf("unexpected fetch request: %#v", req)
			}
			return DiscoveryModelsFetchResponse{
				StatusCode: http.StatusOK,
				Body:       []byte(`{"object":"list","data":[{"id":"actual-main"},{"id":"actual-mini"}]}`),
			}, nil
		},
	}

	router := gin.New()
	router.POST("/api/channel-discovery", ChannelDiscoveryWithModelFetchers(nil, fetchers))

	body := []byte(`{
		"channelKind":"messages",
		"serviceType":"claude",
		"baseUrls":["` + upstream.URL + `/anthropic"],
		"apiKey":"sk-test"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var resp ChannelDiscoveryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Models.Source != "messages_models_handler" {
		t.Fatalf("models source=%q", resp.Models.Source)
	}
	if resp.Models.StatusCode != http.StatusOK {
		t.Fatalf("models status=%d", resp.Models.StatusCode)
	}
	if got := strings.Join(resp.Models.Items, ","); got != "actual-main,actual-mini" {
		t.Fatalf("models=%q", got)
	}
	for _, warning := range resp.Models.Warnings {
		if strings.Contains(warning, "404") || strings.Contains(warning, "built-in probe") {
			t.Fatalf("unexpected fallback warning: %#v", resp.Models.Warnings)
		}
	}
}

func TestChannelDiscoveryCompatUsesDiscoveredActualModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var sawDefaultCompatModel bool
	var sawActualCompatToolProbe bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"actual-main"}]}`))
		case "/v1/responses":
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte("gpt-5.4-mini")) {
				sawDefaultCompatModel = true
			}
			if bytes.Contains(body, []byte("image_generation")) && bytes.Contains(body, []byte("actual-main")) {
				sawActualCompatToolProbe = true
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
			_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery", ChannelDiscovery(nil))

	body := []byte(`{
		"channelKind":"responses",
		"serviceType":"responses",
		"baseUrls":["` + upstream.URL + `"],
		"apiKey":"sk-test",
		"targetClients":["codex"]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if sawDefaultCompatModel {
		t.Fatal("compat probe used default gpt-5.4-mini instead of discovered actual model")
	}
	if !sawActualCompatToolProbe {
		t.Fatal("expected image_generation compat probe to use discovered actual model")
	}
}

func TestChannelDiscoveryReportsResponsesToolCallCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var sawToolProbe bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"actual-main"}]}`))
		case "/v1/responses":
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			if bytes.Contains(body, []byte(`"name":"ccx_probe"`)) {
				sawToolProbe = true
				_, _ = w.Write([]byte("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"ccx_probe\"}}\n\n"))
				_, _ = w.Write([]byte("event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"value\\\":\\\"ok\\\"}\"}\n\n"))
				return
			}
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
			_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery", ChannelDiscovery(nil))

	body := []byte(`{
		"channelKind":"responses",
		"serviceType":"responses",
		"baseUrls":["` + upstream.URL + `"],
		"apiKey":"sk-test",
		"targetClients":["codex"]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawToolProbe {
		t.Fatal("expected tool-call probe request")
	}

	var resp ChannelDiscoveryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Capabilities.ToolCalls.Tested || !resp.Capabilities.ToolCalls.Supported {
		t.Fatalf("tool capability=%#v", resp.Capabilities.ToolCalls)
	}
	if !resp.Recommendation.Compat["codexNativeToolPassthrough"] {
		t.Fatalf("expected codexNativeToolPassthrough recommendation, compat=%#v", resp.Recommendation.Compat)
	}
}

func TestChannelDiscoveryReportsVisionCapabilityAndFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var sawVisionProbeFallback bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"mimo-v2.5"},{"id":"mimo-v2.5-pro"}]}`))
		case "/v1/messages":
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte(`"type":"image"`)) {
				if bytes.Contains(body, []byte(`"model":"mimo-v2.5"`)) {
					sawVisionProbeFallback = true
				}
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
			_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery", ChannelDiscovery(nil))

	body := []byte(`{
		"channelKind":"messages",
		"serviceType":"claude",
		"baseUrls":["` + upstream.URL + `"],
		"apiKey":"sk-test",
		"targetClients":["claude-code"]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawVisionProbeFallback {
		t.Fatal("expected vision probe to use mimo-v2.5 fallback model")
	}

	var resp ChannelDiscoveryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Capabilities.Vision.Tested || !resp.Capabilities.Vision.Supported {
		t.Fatalf("vision capability=%#v", resp.Capabilities.Vision)
	}
	for source, target := range resp.Recommendation.ModelMapping {
		if target != "mimo-v2.5-pro" {
			t.Fatalf("modelMapping[%q]=%q, want mimo-v2.5-pro; full mapping=%#v", source, target, resp.Recommendation.ModelMapping)
		}
	}
	if !sameStringSet(resp.Recommendation.NoVisionModels, []string{"mimo-v2.5-pro"}) {
		t.Fatalf("noVisionModels=%#v", resp.Recommendation.NoVisionModels)
	}
	if resp.Recommendation.VisionFallbackModel != "mimo-v2.5" {
		t.Fatalf("visionFallbackModel=%q", resp.Recommendation.VisionFallbackModel)
	}
}

func TestChannelDiscoveryReportsClaudeThinkingPassbackCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var sawToolProbe bool
	var sawHistoricalThinkingProbe bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"actual-claude"}]}`))
		case "/v1/messages":
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			switch {
			case bytes.Contains(body, []byte(`"name":"ccx_probe"`)):
				sawToolProbe = true
				_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"ccx_probe\",\"input\":{}}}\n\n"))
				_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"value\\\":\\\"ok\\\"}\"}}\n\n"))
				_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			case bytes.Contains(body, []byte("previous reasoning")):
				sawHistoricalThinkingProbe = true
				_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
				_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			case bytes.Contains(body, []byte(`"thinking"`)):
				_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n"))
				_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"analysis\"}}\n\n"))
				_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
				_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
				_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			default:
				_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
				_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery", ChannelDiscovery(nil))

	body := []byte(`{
		"channelKind":"messages",
		"serviceType":"claude",
		"baseUrls":["` + upstream.URL + `"],
		"apiKey":"sk-test",
		"targetClients":["claude-code"]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawToolProbe {
		t.Fatal("expected tool-call probe request")
	}
	if !sawHistoricalThinkingProbe {
		t.Fatal("expected historical thinking passback probe request")
	}

	var resp ChannelDiscoveryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Capabilities.ToolCalls.Tested || !resp.Capabilities.ToolCalls.Supported {
		t.Fatalf("tool capability=%#v", resp.Capabilities.ToolCalls)
	}
	if !resp.Capabilities.ThinkingPassback.Tested || !resp.Capabilities.ThinkingPassback.Required {
		t.Fatalf("thinking capability=%#v", resp.Capabilities.ThinkingPassback)
	}
	if !resp.Recommendation.Compat["passbackReasoningContent"] || !resp.Recommendation.Compat["passbackThinkingBlocks"] {
		t.Fatalf("thinking passback recommendations missing: %#v", resp.Recommendation.Compat)
	}
}
