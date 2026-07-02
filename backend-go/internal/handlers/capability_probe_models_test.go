package handlers

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestGetCapabilityProbeModels_ContainsCodexAutoReview(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		models, err := getCapabilityProbeModels(protocol)
		if err != nil {
			t.Fatalf("protocol=%s unexpected error: %v", protocol, err)
		}
		found := false
		for _, m := range models {
			if m == "codex-auto-review" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("protocol=%s probe models should contain codex-auto-review, got %v", protocol, models)
		}
	}
}

func TestGetCapabilityProbeModels_ContainsGPT56Models(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		models, err := getCapabilityProbeModels(protocol)
		if err != nil {
			t.Fatalf("protocol=%s unexpected error: %v", protocol, err)
		}
		for _, want := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
			found := false
			for _, model := range models {
				if model == want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("protocol=%s probe models should contain %s, got %v", protocol, want, models)
			}
		}
	}
}

func TestGetCapabilityProbeModels_CodexAutoReviewRedirect(t *testing.T) {
	channel := &config.UpstreamConfig{
		ServiceType: "openai",
		ModelMapping: map[string]string{
			"codex-auto-review": "deepseek-v4-flash",
		},
	}

	models, err := getCapabilityProbeModels("responses")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// codex-auto-review 应该通过 ModelMapping 映射到实际模型
	for _, m := range models {
		if m == "codex-auto-review" {
			actual := config.RedirectModel(m, channel)
			if actual != "deepseek-v4-flash" {
				t.Fatalf("codex-auto-review redirect=%s, want deepseek-v4-flash", actual)
			}
			return
		}
	}
	t.Fatal("codex-auto-review not found in responses probe models")
}

func TestGetCapabilityProbeModel(t *testing.T) {
	cases := []struct {
		protocol string
		want     string
		wantErr  bool
	}{
		{protocol: "messages", want: "claude-fable-5"},
		{protocol: "chat", want: "gpt-5.5"},
		{protocol: "gemini", want: "gemini-3.5-flash"},
		{protocol: "responses", want: "gpt-5.5"},
		{protocol: "unknown", wantErr: true},
	}

	for _, tc := range cases {
		got, err := getCapabilityProbeModel(tc.protocol)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("protocol=%s should return error", tc.protocol)
			}
			continue
		}
		if err != nil {
			t.Fatalf("protocol=%s unexpected error: %v", tc.protocol, err)
		}
		if got != tc.want {
			t.Fatalf("protocol=%s model=%s want=%s", tc.protocol, got, tc.want)
		}
	}
}

func TestBuildTestRequest_UsesCentralizedProbeModels(t *testing.T) {
	channel := &config.UpstreamConfig{
		BaseURL: "https://api.example.com",
		APIKeys: []string{"test-key"},
	}

	cases := []struct {
		protocol      string
		expectedURL   string
		expectedModel string
		modelInURL    bool
	}{
		{
			protocol:      "messages",
			expectedURL:   "https://api.example.com/v1/messages",
			expectedModel: "claude-fable-5",
		},
		{
			protocol:      "chat",
			expectedURL:   "https://api.example.com/v1/chat/completions",
			expectedModel: "gpt-5.5",
		},
		{
			protocol:      "gemini",
			expectedURL:   "https://api.example.com/v1beta/models/gemini-3.5-flash:streamGenerateContent?alt=sse",
			expectedModel: "gemini-3.5-flash",
			modelInURL:    true,
		},
		{
			protocol:      "responses",
			expectedURL:   "https://api.example.com/v1/responses",
			expectedModel: "gpt-5.5",
		},
	}

	for _, tc := range cases {
		req, err := buildTestRequest(tc.protocol, channel)
		if err != nil {
			t.Fatalf("protocol=%s build request failed: %v", tc.protocol, err)
		}

		if got := req.URL.String(); got != tc.expectedURL {
			t.Fatalf("protocol=%s url=%s want=%s", tc.protocol, got, tc.expectedURL)
		}

		if tc.modelInURL {
			if !strings.Contains(req.URL.Path, tc.expectedModel) {
				t.Fatalf("protocol=%s url path=%s should contain model=%s", tc.protocol, req.URL.Path, tc.expectedModel)
			}
			continue
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("protocol=%s read body failed: %v", tc.protocol, err)
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("protocol=%s unmarshal body failed: %v, body=%s", tc.protocol, err, string(body))
		}

		model, ok := payload["model"].(string)
		if !ok {
			t.Fatalf("protocol=%s body missing model field", tc.protocol)
		}
		if model != tc.expectedModel {
			t.Fatalf("protocol=%s model=%s want=%s", tc.protocol, model, tc.expectedModel)
		}
	}
}

func TestBuildTestRequest_UsesExistingVersionSuffix(t *testing.T) {
	cases := []struct {
		name        string
		protocol    string
		baseURL     string
		expectedURL string
	}{
		{
			name:        "messages with v1 base url",
			protocol:    "messages",
			baseURL:     "https://api.example.com/codex/v1",
			expectedURL: "https://api.example.com/codex/v1/messages",
		},
		{
			name:        "chat with v1 base url",
			protocol:    "chat",
			baseURL:     "https://api.example.com/codex/v1",
			expectedURL: "https://api.example.com/codex/v1/chat/completions",
		},
		{
			name:        "responses with v1 base url",
			protocol:    "responses",
			baseURL:     "https://api.example.com/codex/v1",
			expectedURL: "https://api.example.com/codex/v1/responses",
		},
		{
			name:        "gemini with v1beta base url",
			protocol:    "gemini",
			baseURL:     "https://api.example.com/codex/v1beta",
			expectedURL: "https://api.example.com/codex/v1beta/models/gemini-3.5-flash:streamGenerateContent?alt=sse",
		},
		{
			name:        "responses with explicit skip marker",
			protocol:    "responses",
			baseURL:     "https://api.example.com/codex/v1#",
			expectedURL: "https://api.example.com/codex/v1/responses",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channel := &config.UpstreamConfig{
				BaseURL: tc.baseURL,
				APIKeys: []string{"test-key"},
			}

			req, err := buildTestRequest(tc.protocol, channel)
			if err != nil {
				t.Fatalf("protocol=%s build request failed: %v", tc.protocol, err)
			}

			if got := req.URL.String(); got != tc.expectedURL {
				t.Fatalf("protocol=%s url=%s want=%s", tc.protocol, got, tc.expectedURL)
			}
		})
	}
}

func TestBuildTestRequestWithModel_RespectsXAPIKeyAuthHeader(t *testing.T) {
	channel := &config.UpstreamConfig{
		BaseURL:    "https://opencode.ai/zen/go",
		APIKeys:    []string{"sk-opencode-go"},
		AuthHeader: "x-api-key",
	}

	req, err := buildTestRequestWithModel("messages", channel, capabilityProbeModelClaudeFable5)
	if err != nil {
		t.Fatalf("buildTestRequestWithModel() error = %v", err)
	}

	if got := req.Header.Get("x-api-key"); got != "sk-opencode-go" {
		t.Fatalf("x-api-key = %q, want %q", got, "sk-opencode-go")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
}
