package passthrough

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/tidwall/gjson"
)

func TestFormatsMatchRequiresSameKnownFormat(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		serviceType string
		want        bool
	}{
		{"messages_to_claude", "/v1/messages", "claude", true},
		{"responses_to_responses", "/v1/responses", "responses", true},
		{"chat_to_openai", "/v1/chat/completions", "openai", true},
		{"gemini_stream_to_gemini", "/v1beta/models/gemini-2.0-flash:streamGenerateContent", "gemini", true},
		{"gemini_generate_to_gemini", "/v1/models/gemini-2.0-flash:generateContent", "gemini", true},
		{"messages_to_openai", "/v1/messages", "openai", false},
		{"responses_to_claude", "/v1/responses", "claude", false},
		{"chat_to_gemini", "/v1/chat/completions", "gemini", false},
		{"gemini_to_openai", "/v1beta/models/gemini-2.0-flash:streamGenerateContent", "openai", false},
		{"unknown_path", "/v1/unknown", "claude", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inbound := InboundFormatFromPath(tt.path)
			outbound := OutboundFormatForService(tt.serviceType, inbound)
			if got := FormatsMatch(inbound, outbound); got != tt.want {
				t.Fatalf("FormatsMatch(%q, %q) = %v, want %v", inbound, outbound, got, tt.want)
			}
		})
	}
}

func TestAllowsStrictBodyPassthroughRequiresFormatMatch(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		upstream *config.UpstreamConfig
		want     bool
	}{
		{
			name: "matching",
			path: "/v1/responses",
			upstream: &config.UpstreamConfig{
				ServiceType: "responses",
			},
			want: true,
		},
		{
			name: "cross protocol",
			path: "/v1/responses",
			upstream: &config.UpstreamConfig{
				ServiceType: "claude",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AllowsStrictBodyPassthrough(tt.path, tt.upstream); got != tt.want {
				t.Fatalf("AllowsStrictBodyPassthrough() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecideCoversPassthroughModes(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		kind     scheduler.ChannelKind
		upstream *config.UpstreamConfig
		apiKey   string
		want     Decision
	}{
		{
			name: "same format enables strict body and raw response",
			path: "/v1/responses",
			kind: scheduler.ChannelKindResponses,
			upstream: &config.UpstreamConfig{
				ServiceType: "responses",
			},
			apiKey: "sk-test",
			want: Decision{
				InboundFormat:  APIFormatOpenAIResponses,
				OutboundFormat: APIFormatOpenAIResponses,
				StrictBody:     true,
				RawResponse:    true,
			},
		},
		{
			name: "messages same format skips preprocess",
			path: "/v1/messages",
			kind: scheduler.ChannelKindMessages,
			upstream: &config.UpstreamConfig{
				ServiceType: "claude",
			},
			apiKey: "sk-ant-test",
			want: Decision{
				InboundFormat:  APIFormatClaudeMessages,
				OutboundFormat: APIFormatClaudeMessages,
				StrictBody:     true,
				RawResponse:    true,
				SkipPreprocess: true,
			},
		},
		{
			name: "skip preprocess only applies to messages kind",
			path: "/v1/messages",
			kind: scheduler.ChannelKindResponses,
			upstream: &config.UpstreamConfig{
				ServiceType: "claude",
			},
			apiKey: "sk-ant-test",
			want: Decision{
				InboundFormat:  APIFormatClaudeMessages,
				OutboundFormat: APIFormatClaudeMessages,
				StrictBody:     true,
				RawResponse:    true,
			},
		},
		{
			name: "cross format disables passthrough",
			path: "/v1/responses",
			kind: scheduler.ChannelKindResponses,
			upstream: &config.UpstreamConfig{
				ServiceType: "claude",
			},
			apiKey: "sk-ant-test",
			want: Decision{
				InboundFormat:  APIFormatOpenAIResponses,
				OutboundFormat: APIFormatClaudeMessages,
			},
		},
		{
			name:     "nil upstream keeps inbound format only",
			path:     "/v1/chat/completions",
			kind:     scheduler.ChannelKindChat,
			upstream: nil,
			apiKey:   "sk-test",
			want: Decision{
				InboundFormat: APIFormatOpenAIChat,
			},
		},
		{
			name: "gemini native same format enables raw response",
			path: "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			kind: scheduler.ChannelKindGemini,
			upstream: &config.UpstreamConfig{
				ServiceType: "gemini",
			},
			apiKey: "gemini-key",
			want: Decision{
				InboundFormat:  APIFormatGeminiContents,
				OutboundFormat: APIFormatGeminiContents,
				StrictBody:     true,
				RawResponse:    true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Decide(tt.path, tt.kind, tt.upstream, tt.apiKey); got != tt.want {
				t.Fatalf("Decide() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestPatchPlatformFieldsPreservesUnknownFieldsAndPatchesModel(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":"hi","unknown":{"nested":1},"stream":true}`)
	upstream := &config.UpstreamConfig{
		ModelMapping:     map[string]string{"gpt-5": "gpt-5.2"},
		ReasoningMapping: map[string]string{"gpt-5": "high"},
		TextVerbosity:    "medium",
		FastMode:         true,
	}

	got := PatchPlatformFields(body, upstream)

	if model := gjson.GetBytes(got, "model").String(); model != "gpt-5.2" {
		t.Fatalf("model = %q, want gpt-5.2; body=%s", model, got)
	}
	if nested := gjson.GetBytes(got, "unknown.nested").Int(); nested != 1 {
		t.Fatalf("unknown.nested = %d, want 1; body=%s", nested, got)
	}
	if stream := gjson.GetBytes(got, "stream").Bool(); !stream {
		t.Fatalf("stream = false, want true; body=%s", got)
	}
	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "high" {
		t.Fatalf("reasoning.effort = %q, want high; body=%s", effort, got)
	}
	if verbosity := gjson.GetBytes(got, "text.verbosity").String(); verbosity != "medium" {
		t.Fatalf("text.verbosity = %q, want medium; body=%s", verbosity, got)
	}
	if tier := gjson.GetBytes(got, "service_tier").String(); tier != "priority" {
		t.Fatalf("service_tier = %q, want priority; body=%s", tier, got)
	}
}
