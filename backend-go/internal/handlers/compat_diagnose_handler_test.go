package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestCompatDiagnoseThinkingPassbackDefaults(t *testing.T) {
	tests := []struct {
		name    string
		channel *config.UpstreamConfig
		baseURL string
		want    bool
	}{
		{
			name: "deepseek channel by name",
			channel: &config.UpstreamConfig{
				Name:        "deepseek-xtpamp",
				ServiceType: "claude",
			},
			baseURL: "https://api.example.com/anthropic",
			want:    true,
		},
		{
			name: "volc ark by base url",
			channel: &config.UpstreamConfig{
				Name:        "ark-cn-beijing-volces-119tdg",
				ServiceType: "claude",
			},
			baseURL: "https://ark.cn-beijing.volces.com/api/coding#",
			want:    true,
		},
		{
			name: "glm by model mapping",
			channel: &config.UpstreamConfig{
				ServiceType: "claude",
				ModelMapping: map[string]string{
					"sonnet": "glm-5.2",
				},
			},
			baseURL: "https://api.example.com/messages",
			want:    true,
		},
		{
			name: "dashscope qwen should use probe instead of default",
			channel: &config.UpstreamConfig{
				Name:        "code-gpt5.5-ali-qwen",
				ServiceType: "claude",
				ModelMapping: map[string]string{
					"codex": "qwen3.7-plus",
					"gpt":   "qwen3.7-plus",
				},
			},
			baseURL: "https://coding.dashscope.aliyuncs.com/apps/anthropic",
			want:    false,
		},
		{
			name: "anthropic standard channel",
			channel: &config.UpstreamConfig{
				Name:        "anthropic",
				ServiceType: "claude",
			},
			baseURL: "https://api.anthropic.com",
			want:    false,
		},
		{
			name: "openrouter aggregate channel",
			channel: &config.UpstreamConfig{
				Name:        "deepseek via openrouter",
				ServiceType: "claude",
			},
			baseURL: "https://openrouter.ai/api",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldPassbackThinkingBlocksByDefault(tt.channel, tt.baseURL)
			if got != tt.want {
				t.Fatalf("shouldPassbackThinkingBlocksByDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompatDiagnoseDeepSeekUsesThinkingCacheDefaults(t *testing.T) {
	tests := []struct {
		name    string
		channel *config.UpstreamConfig
		baseURL string
		want    bool
	}{
		{
			name: "deepseek direct by base url",
			channel: &config.UpstreamConfig{
				ServiceType: "claude",
			},
			baseURL: "https://api.deepseek.com/anthropic",
			want:    true,
		},
		{
			name: "deepseek by model mapping",
			channel: &config.UpstreamConfig{
				ServiceType:  "claude",
				ModelMapping: map[string]string{"sonnet": "deepseek-v4-pro"},
			},
			baseURL: "https://api.example.com/anthropic",
			want:    true,
		},
		{
			name: "openrouter aggregate is excluded",
			channel: &config.UpstreamConfig{
				Name:        "deepseek via openrouter",
				ServiceType: "claude",
			},
			baseURL: "https://openrouter.ai/api",
			want:    false,
		},
		{
			name: "glm does not use deepseek cache default",
			channel: &config.UpstreamConfig{
				Name:        "glm",
				ServiceType: "claude",
			},
			baseURL: "https://bigmodel.cn/api",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldUseThinkingCacheForClaudePassback(tt.channel, tt.baseURL)
			if got != tt.want {
				t.Fatalf("shouldUseThinkingCacheForClaudePassback() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompatDiagnoseSystemRoleNormalizeDefaults(t *testing.T) {
	tests := []struct {
		name    string
		channel *config.UpstreamConfig
		baseURL string
		want    bool
	}{
		{
			name: "deepseek channel",
			channel: &config.UpstreamConfig{
				Name:        "deepseek-xtpamp",
				ServiceType: "claude",
			},
			baseURL: "https://api.deepseek.com/anthropic",
			want:    true,
		},
		{
			name: "mimo channel",
			channel: &config.UpstreamConfig{
				Name:        "mimo",
				ServiceType: "claude",
			},
			baseURL: "https://api.xiaomimimo.com/anthropic",
			want:    true,
		},
		{
			name: "qianfan channel",
			channel: &config.UpstreamConfig{
				ServiceType: "claude",
			},
			baseURL: "https://qianfan.baidubce.com/anthropic/coding",
			want:    true,
		},
		{
			name: "anthropic standard channel",
			channel: &config.UpstreamConfig{
				Name:        "anthropic",
				ServiceType: "claude",
			},
			baseURL: "https://api.anthropic.com",
			want:    false,
		},
		{
			name: "runapi aggregate channel",
			channel: &config.UpstreamConfig{
				Name:        "runapi deepseek",
				ServiceType: "claude",
			},
			baseURL: "https://runapi.co/v1",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldNormalizeSystemRoleToTopLevelByDefault(tt.channel, tt.baseURL)
			if got != tt.want {
				t.Fatalf("shouldNormalizeSystemRoleToTopLevelByDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDiagnoseClaudeChannelDisablesThinkingBlocksWhenProbeRejected(t *testing.T) {
	var historicalThinkingProbeSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case strings.Contains(string(body), `"type":"thinking"`):
			historicalThinkingProbeSeen = true
			http.Error(w, `{"error":{"message":"Unexpected item type in content."}}`, http.StatusBadRequest)
		case strings.Contains(string(body), `"thinking":{"budget_tokens":512,"type":"enabled"}`):
			_, _ = w.Write([]byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}` + "\n\n"))
			_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"thinking"}}` + "\n\n"))
		default:
			_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n"))
		}
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{
		Name:        "code-gpt5.5-ali-qwen",
		ServiceType: "claude",
		ModelMapping: map[string]string{
			"codex": "qwen3.7-plus",
			"gpt":   "qwen3.7-plus",
		},
	}

	result := runCompatDiagnose(channel, "messages", "sk-test", server.URL)
	if !historicalThinkingProbeSeen {
		t.Fatal("historical thinking block probe was not sent")
	}
	if got := result.Recommendations["passbackReasoningContent"]; got != false {
		t.Fatalf("passbackReasoningContent = %v, want false; evidence=%q", got, result.Evidence["passbackReasoningContent"])
	}
	if got := result.Recommendations["passbackThinkingBlocks"]; got != false {
		t.Fatalf("passbackThinkingBlocks = %v, want false; evidence=%q", got, result.Evidence["passbackThinkingBlocks"])
	}
}

func TestDiagnoseClaudeChannelEnablesThinkingBlocksWhenProbeAccepted(t *testing.T) {
	var historicalThinkingProbeSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if strings.Contains(string(body), `"type":"thinking"`) {
			historicalThinkingProbeSeen = true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case strings.Contains(string(body), `"thinking":{"budget_tokens":512,"type":"enabled"}`) && !strings.Contains(string(body), `"type":"thinking"`):
			_, _ = w.Write([]byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}` + "\n\n"))
			_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"thinking"}}` + "\n\n"))
		default:
			_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n"))
		}
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{
		Name:        "glm",
		ServiceType: "claude",
	}

	result := runCompatDiagnose(channel, "messages", "sk-test", server.URL)
	if !historicalThinkingProbeSeen {
		t.Fatal("historical thinking block probe was not sent")
	}
	if got := result.Recommendations["passbackThinkingBlocks"]; got != true {
		t.Fatalf("passbackThinkingBlocks = %v, want true; evidence=%q", got, result.Evidence["passbackThinkingBlocks"])
	}
}

func TestDiagnoseClaudeChannelDeepSeekRecommendsCacheCompatibleState(t *testing.T) {
	var historicalThinkingProbeSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if strings.Contains(string(body), `"type":"thinking"`) {
			historicalThinkingProbeSeen = true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case strings.Contains(string(body), `"thinking":{"budget_tokens":512,"type":"enabled"}`) && !strings.Contains(string(body), `"type":"thinking"`):
			_, _ = w.Write([]byte(`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}` + "\n\n"))
			_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"thinking"}}` + "\n\n"))
		default:
			_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n"))
		}
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{
		Name:        "deepseek",
		ServiceType: "claude",
	}

	result := runCompatDiagnose(channel, "messages", "sk-test", server.URL)
	if historicalThinkingProbeSeen {
		t.Fatal("historical thinking block probe should be skipped for DeepSeek cache mode")
	}

	wants := map[string]bool{
		"passbackReasoningContent": false,
		"passbackThinkingBlocks":   false,
		"stripEmptyTextBlocks":     true,
		"normalizeMetadataUserId":  true,
		"stripBillingHeader":       true,
	}
	for key, want := range wants {
		if got := result.Recommendations[key]; got != want {
			t.Fatalf("%s = %v, want %v; evidence=%q", key, got, want, result.Evidence[key])
		}
	}
}

func TestDiagnoseBaseURLHashRequiresFailedOriginalAndWorkingCandidate(t *testing.T) {
	var originalHits, candidateHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/messages":
			originalHits++
			http.Error(w, "not found", http.StatusNotFound)
		case "/messages":
			candidateHits++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data:{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n"))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{BaseURL: server.URL, ServiceType: "claude"}
	rec := diagnoseBaseURLHash(channel, "messages", "sk-test", server.URL)
	if rec == nil {
		t.Fatal("diagnoseBaseURLHash() = nil, want recommendation")
	}
	if rec.Current != server.URL || rec.Recommended != server.URL+"#" {
		t.Fatalf("recommendation = %#v, want current %q and recommended %q", rec, server.URL, server.URL+"#")
	}
	if originalHits != 1 || candidateHits != 1 {
		t.Fatalf("probe hits original=%d candidate=%d, want 1/1", originalHits, candidateHits)
	}
}

func TestDiagnoseBaseURLHashResponsesClaudeUpstreamUsesMessagesEndpoint(t *testing.T) {
	var messagesHits, responsesHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/messages":
			messagesHits++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n"))
		case "/messages":
			messagesHits++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n"))
		case "/v1/responses":
			responsesHits++
			http.Error(w, "wrong endpoint", http.StatusNotFound)
		case "/responses":
			responsesHits++
			http.Error(w, "wrong endpoint", http.StatusNotFound)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{BaseURL: server.URL, ServiceType: "claude"}
	if rec := diagnoseBaseURLHash(channel, "responses", "sk-test", server.URL); rec != nil {
		t.Fatalf("diagnoseBaseURLHash() = %#v, want nil", rec)
	}
	if messagesHits == 0 {
		t.Fatal("messages endpoint was not probed")
	}
	if responsesHits != 0 {
		t.Fatalf("responses endpoint hits = %d, want 0", responsesHits)
	}
}

func TestDiagnoseBaseURLHashSkipsCandidateWhenOriginalWorks(t *testing.T) {
	var candidateHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/messages":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n"))
		case "/messages":
			candidateHits++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n"))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{BaseURL: server.URL, ServiceType: "claude"}
	if rec := diagnoseBaseURLHash(channel, "messages", "sk-test", server.URL); rec != nil {
		t.Fatalf("diagnoseBaseURLHash() = %#v, want nil", rec)
	}
	if candidateHits != 0 {
		t.Fatalf("candidate probe hits = %d, want 0", candidateHits)
	}
}

func TestDiagnoseBaseURLHashRejectsCandidateEmptyStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/messages":
			http.Error(w, "not found", http.StatusNotFound)
		case "/messages":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{BaseURL: server.URL, ServiceType: "claude"}
	if rec := diagnoseBaseURLHash(channel, "messages", "sk-test", server.URL); rec != nil {
		t.Fatalf("diagnoseBaseURLHash() = %#v, want nil", rec)
	}
}

func TestDiagnoseResponsesImageGenerationToolRecommendsStripWhenRejected(t *testing.T) {
	var probeSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if strings.Contains(string(body), `"type":"image_generation"`) {
			probeSeen = true
			http.Error(w, `{"error":{"message":"image_generation tool is not enabled"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"ok"}` + "\n\n"))
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{ServiceType: "responses"}
	result := runCompatDiagnose(channel, "responses", "sk-test", server.URL)
	if !probeSeen {
		t.Fatal("image_generation probe was not sent")
	}
	if got := result.Recommendations["stripImageGenerationTool"]; got != true {
		t.Fatalf("stripImageGenerationTool = %v, want true; evidence=%q", got, result.Evidence["stripImageGenerationTool"])
	}
}

func TestDiagnoseResponsesImageGenerationToolDisablesStripWhenAccepted(t *testing.T) {
	var probeSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if strings.Contains(string(body), `"type":"image_generation"`) {
			probeSeen = true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"ok"}` + "\n\n"))
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{ServiceType: "responses"}
	result := runCompatDiagnose(channel, "responses", "sk-test", server.URL)
	if !probeSeen {
		t.Fatal("image_generation probe was not sent")
	}
	if got := result.Recommendations["stripImageGenerationTool"]; got != false {
		t.Fatalf("stripImageGenerationTool = %v, want false; evidence=%q", got, result.Evidence["stripImageGenerationTool"])
	}
}

func TestDiagnoseChatImageGenerationToolRecommendsStripWhenRejected(t *testing.T) {
	var probeSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if strings.Contains(string(body), `"type":"image_generation"`) {
			probeSeen = true
			http.Error(w, `{"error":{"message":"unsupported image_generation tool"}}`, http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n"))
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{ServiceType: "openai"}
	result := runCompatDiagnose(channel, "chat", "sk-test", server.URL)
	if !probeSeen {
		t.Fatal("image_generation probe was not sent")
	}
	if got := result.Recommendations["stripImageGenerationTool"]; got != true {
		t.Fatalf("stripImageGenerationTool = %v, want true; evidence=%q", got, result.Evidence["stripImageGenerationTool"])
	}
}

func TestDiagnoseImageGenerationToolRecommendsStripFromErrorSSE(t *testing.T) {
	var probeSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if strings.Contains(string(body), `"type":"image_generation"`) {
			probeSeen = true
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"error":{"message":"image_generation tool is not supported"}}` + "\n\n"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n"))
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{ServiceType: "chat"}
	result := runCompatDiagnose(channel, "chat", "sk-test", server.URL)
	if !probeSeen {
		t.Fatal("image_generation probe was not sent")
	}
	if got := result.Recommendations["stripImageGenerationTool"]; got != true {
		t.Fatalf("stripImageGenerationTool = %v, want true; evidence=%q", got, result.Evidence["stripImageGenerationTool"])
	}
}

func TestDiagnoseImageGenerationToolSkipsGenericAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{ServiceType: "responses"}
	result := runCompatDiagnose(channel, "responses", "sk-test", server.URL)
	if _, ok := result.Recommendations["stripImageGenerationTool"]; ok {
		t.Fatalf("stripImageGenerationTool recommendation should be absent; got %#v", result.Recommendations)
	}
}

func TestHasMeaningfulCompatSSE(t *testing.T) {
	tests := []struct {
		name        string
		channelKind string
		lines       []string
		want        bool
	}{
		{
			name:        "empty stream",
			channelKind: "messages",
			lines:       nil,
			want:        false,
		},
		{
			name:        "done only",
			channelKind: "messages",
			lines:       []string{"[DONE]"},
			want:        false,
		},
		{
			name:        "claude message start only",
			channelKind: "messages",
			lines:       []string{`{"type":"message_start","message":{"id":"msg_1"}}`},
			want:        false,
		},
		{
			name:        "claude text delta",
			channelKind: "messages",
			lines:       []string{`{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}`},
			want:        true,
		},
		{
			name:        "openai role only",
			channelKind: "chat",
			lines:       []string{`{"choices":[{"delta":{"role":"assistant"}}]}`},
			want:        false,
		},
		{
			name:        "openai content delta",
			channelKind: "chat",
			lines:       []string{`{"choices":[{"delta":{"content":"ok"}}]}`},
			want:        true,
		},
		{
			name:        "responses output delta",
			channelKind: "responses",
			lines:       []string{`{"type":"response.output_text.delta","delta":"ok"}`},
			want:        true,
		},
		{
			name:        "responses failed",
			channelKind: "responses",
			lines:       []string{`{"type":"response.failed","response":{"status":"failed"}}`},
			want:        false,
		},
		{
			name:        "gemini text part",
			channelKind: "gemini",
			lines:       []string{`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`},
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasMeaningfulCompatSSE(tt.lines, tt.channelKind)
			if got != tt.want {
				t.Fatalf("hasMeaningfulCompatSSE() = %v, want %v", got, tt.want)
			}
		})
	}
}
