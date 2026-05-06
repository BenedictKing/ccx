package common

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func TestShouldDirectClaudePassthroughForKey(t *testing.T) {
	tests := []struct {
		name     string
		upstream *config.UpstreamConfig
		apiKey   string
		want     bool
	}{
		{
			name:     "messages to claude",
			upstream: &config.UpstreamConfig{ServiceType: "claude"},
			apiKey:   "any-key",
			want:     true,
		},
		{
			name:     "non-claude channel",
			upstream: &config.UpstreamConfig{ServiceType: "responses"},
			apiKey:   "sk-ant-example",
			want:     false,
		},
		{
			name:     "nil upstream",
			upstream: nil,
			apiKey:   "sk-ant-example",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldDirectClaudePassthroughForKey(tt.upstream, tt.apiKey)
			if got != tt.want {
				t.Fatalf("ShouldDirectClaudePassthroughForKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldDirectPassthroughForRequestRequiresProtocolConsistency(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		upstream *config.UpstreamConfig
		apiKey   string
		want     bool
	}{
		{
			name:     "messages to claude",
			path:     "/v1/messages",
			upstream: &config.UpstreamConfig{ServiceType: "claude"},
			apiKey:   "sk-any",
			want:     true,
		},
		{
			name:     "responses to responses",
			path:     "/v1/responses",
			upstream: &config.UpstreamConfig{ServiceType: "responses"},
			apiKey:   "sk-any",
			want:     true,
		},
		{
			name:     "chat to openai",
			path:     "/v1/chat/completions",
			upstream: &config.UpstreamConfig{ServiceType: "openai"},
			apiKey:   "sk-any",
			want:     true,
		},
		{
			name:     "gemini native to gemini",
			path:     "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			upstream: &config.UpstreamConfig{ServiceType: "gemini"},
			apiKey:   "sk-any",
			want:     true,
		},
		{
			name:     "responses to claude is cross protocol",
			path:     "/v1/responses",
			upstream: &config.UpstreamConfig{ServiceType: "claude"},
			apiKey:   "sk-any",
			want:     false,
		},
		{
			name:     "chat to gemini is cross protocol",
			path:     "/v1/chat/completions",
			upstream: &config.UpstreamConfig{ServiceType: "gemini"},
			apiKey:   "sk-any",
			want:     false,
		},
		{
			name:     "gemini native to openai is cross protocol",
			path:     "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			upstream: &config.UpstreamConfig{ServiceType: "openai"},
			apiKey:   "sk-any",
			want:     false,
		},
		{
			name:     "messages to openai is cross protocol",
			path:     "/v1/messages",
			upstream: &config.UpstreamConfig{ServiceType: "openai"},
			apiKey:   "sk-any",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldDirectPassthroughForRequest(tt.path, tt.upstream, tt.apiKey)
			if got != tt.want {
				t.Fatalf("ShouldDirectPassthroughForRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassifyAxonHubForwardingUsage(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		kind        scheduler.ChannelKind
		upstream    *config.UpstreamConfig
		wantEnabled bool
		wantFamily  string
		wantMode    metrics.AxonHubForwardingMode
	}{
		{
			name:        "messages to claude is same-format raw",
			path:        "/v1/messages",
			kind:        scheduler.ChannelKindMessages,
			upstream:    &config.UpstreamConfig{ServiceType: "claude"},
			wantEnabled: true,
			wantFamily:  "messages",
			wantMode:    metrics.AxonHubForwardingModeSameFormatRaw,
		},
		{
			name:        "responses to claude is cross-format converted",
			path:        "/v1/responses",
			kind:        scheduler.ChannelKindResponses,
			upstream:    &config.UpstreamConfig{ServiceType: "claude"},
			wantEnabled: true,
			wantFamily:  "responses",
			wantMode:    metrics.AxonHubForwardingModeCrossFormatConverted,
		},
		{
			name:        "images are outside AxonHub forwarding stats",
			path:        "/v1/images/generations",
			kind:        scheduler.ChannelKindImages,
			upstream:    &config.UpstreamConfig{ServiceType: "openai"},
			wantEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &gin.Context{Request: httptest.NewRequest("POST", tt.path, nil)}
			got := classifyAxonHubForwardingUsage(c, tt.kind, tt.upstream, "sk-test")
			if got.enabled != tt.wantEnabled {
				t.Fatalf("enabled = %v, want %v", got.enabled, tt.wantEnabled)
			}
			if !tt.wantEnabled {
				return
			}
			if got.inboundFamily != tt.wantFamily || got.mode != tt.wantMode {
				t.Fatalf("class = family:%q mode:%q, want %q/%q", got.inboundFamily, got.mode, tt.wantFamily, tt.wantMode)
			}
		})
	}
}

func TestPrepareRequestBodyForUpstream_PassthroughAndPreprocess(t *testing.T) {
	requestBody := []byte(`{"metadata":{"user_id":"{\"device_id\":\"dev\",\"account_uuid\":\"acc\",\"session_id\":\"sid\"}"},"system":[{"type":"text","text":"cc_version=1;cch=user_xxx;cc_entrypoint=chat"}],"messages":[]}`)

	t.Run("same format messages passthrough should keep request body untouched", func(t *testing.T) {
		upstream := &config.UpstreamConfig{ServiceType: "claude"}

		got := prepareRequestBodyForUpstream(
			requestBody,
			scheduler.ChannelKindMessages,
			"Messages",
			upstream,
			nil,
			&config.EnvConfig{},
			"sk-any",
		)
		if !bytes.Equal(got, requestBody) {
			t.Fatalf("same-format passthrough should keep body unchanged, got=%s", string(got))
		}
	})

	t.Run("same format messages passthrough still honors strip billing header", func(t *testing.T) {
		cfgManager := newConfigManagerForCommonTest(t, `{"stripBillingHeader":true}`)
		upstream := &config.UpstreamConfig{ServiceType: "claude"}

		got := prepareRequestBodyForUpstream(
			requestBody,
			scheduler.ChannelKindMessages,
			"Messages",
			upstream,
			cfgManager,
			&config.EnvConfig{},
			"sk-any",
		)
		if strings.Contains(string(got), "cch=") {
			t.Fatalf("billing header marker should be removed when stripBillingHeader is enabled, got=%s", string(got))
		}
		if userID := gjson.GetBytes(got, "metadata.user_id").String(); userID != `{"device_id":"dev","account_uuid":"acc","session_id":"sid"}` {
			t.Fatalf("same-format passthrough should not normalize metadata.user_id, got %q", userID)
		}
	})

	t.Run("cross protocol messages request keeps preprocessing active", func(t *testing.T) {
		cfgManager := newConfigManagerForCommonTest(t, `{"stripBillingHeader":true}`)
		upstream := &config.UpstreamConfig{ServiceType: "responses"}

		got := prepareRequestBodyForUpstream(
			requestBody,
			scheduler.ChannelKindMessages,
			"Messages",
			upstream,
			cfgManager,
			&config.EnvConfig{},
			"sk-any",
		)
		if bytes.Equal(got, requestBody) {
			t.Fatal("expected messages-to-responses mismatch to run preprocessing")
		}
		if userID := gjson.GetBytes(got, "metadata.user_id").String(); userID != "user_dev_account_acc_session_sid" {
			t.Fatalf("metadata.user_id = %q, want %q", userID, "user_dev_account_acc_session_sid")
		}
		if strings.Contains(string(got), "cch=") {
			t.Fatalf("billing header marker should be removed when preprocessing runs, got=%s", string(got))
		}
	})

	t.Run("responses path still normalizes metadata on cross protocol", func(t *testing.T) {
		upstream := &config.UpstreamConfig{ServiceType: "claude"}

		got := prepareRequestBodyForUpstream(
			requestBody,
			scheduler.ChannelKindResponses,
			"Responses",
			upstream,
			nil,
			&config.EnvConfig{},
			"sk-any",
		)
		if userID := gjson.GetBytes(got, "metadata.user_id").String(); userID != "user_dev_account_acc_session_sid" {
			t.Fatalf("responses metadata.user_id = %q, want %q", userID, "user_dev_account_acc_session_sid")
		}
	})
}

func newConfigManagerForCommonTest(t *testing.T, rawJSON string) *config.ConfigManager {
	t.Helper()
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(configPath, []byte(rawJSON), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cm, err := config.NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	return cm
}
