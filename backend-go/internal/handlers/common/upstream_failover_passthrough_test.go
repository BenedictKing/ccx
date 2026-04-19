package common

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/tidwall/gjson"
)

func TestIsClaudeSub2APIPassthroughForKey(t *testing.T) {
	enabled := true
	tests := []struct {
		name     string
		upstream *config.UpstreamConfig
		apiKey   string
		want     bool
	}{
		{
			name: "claude + sub2api + anthropic key",
			upstream: &config.UpstreamConfig{
				ServiceType:               "claude",
				Sub2APIPassthroughEnabled: &enabled,
			},
			apiKey: "sk-ant-example",
			want:   true,
		},
		{
			name: "claude + sub2api + non-anthropic key",
			upstream: &config.UpstreamConfig{
				ServiceType:               "claude",
				Sub2APIPassthroughEnabled: &enabled,
			},
			apiKey: "sk-not-ant",
			want:   false,
		},
		{
			name: "non-claude channel",
			upstream: &config.UpstreamConfig{
				ServiceType:               "responses",
				Sub2APIPassthroughEnabled: &enabled,
			},
			apiKey: "sk-ant-example",
			want:   false,
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
			got := IsClaudeSub2APIPassthroughForKey(tt.upstream, tt.apiKey)
			if got != tt.want {
				t.Fatalf("IsClaudeSub2APIPassthroughForKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldDirectClaudePassthroughForKey(t *testing.T) {
	on := true
	off := false
	tests := []struct {
		name     string
		upstream *config.UpstreamConfig
		apiKey   string
		want     bool
	}{
		{
			name: "full passthrough on",
			upstream: &config.UpstreamConfig{
				ServiceType:              "claude",
				StreamPassthroughEnabled: &on,
			},
			apiKey: "any-key",
			want:   true,
		},
		{
			name: "sub2api passthrough on with anthropic key",
			upstream: &config.UpstreamConfig{
				ServiceType:               "claude",
				StreamPassthroughEnabled:  &off,
				Sub2APIPassthroughEnabled: &on,
			},
			apiKey: "sk-ant-example",
			want:   true,
		},
		{
			name: "sub2api passthrough on but non-anthropic key",
			upstream: &config.UpstreamConfig{
				ServiceType:               "claude",
				StreamPassthroughEnabled:  &off,
				Sub2APIPassthroughEnabled: &on,
			},
			apiKey: "sk-not-ant",
			want:   false,
		},
		{
			name: "non-claude channel",
			upstream: &config.UpstreamConfig{
				ServiceType:              "responses",
				StreamPassthroughEnabled: &on,
			},
			apiKey: "sk-ant-example",
			want:   false,
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

func TestPrepareRequestBodyForUpstream_PassthroughAndPreprocess(t *testing.T) {
	requestBody := []byte(`{"metadata":{"user_id":"{\"device_id\":\"dev\",\"account_uuid\":\"acc\",\"session_id\":\"sid\"}"},"system":[{"type":"text","text":"cc_version=1;cch=user_xxx;cc_entrypoint=chat"}],"messages":[]}`)
	enabled := true
	disabled := false

	t.Run("strict passthrough should keep request body untouched", func(t *testing.T) {
		upstream := &config.UpstreamConfig{
			ServiceType:                     "claude",
			StreamPassthroughEnabled:        &enabled,
			StrictRequestPassthroughEnabled: &enabled,
		}

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
			t.Fatalf("strict passthrough should keep body unchanged, got=%s", string(got))
		}
	})

	t.Run("sub2api passthrough should keep request body untouched", func(t *testing.T) {
		upstream := &config.UpstreamConfig{
			ServiceType:               "claude",
			StreamPassthroughEnabled:  &disabled,
			Sub2APIPassthroughEnabled: &enabled,
		}

		got := prepareRequestBodyForUpstream(
			requestBody,
			scheduler.ChannelKindMessages,
			"Messages",
			upstream,
			nil,
			&config.EnvConfig{},
			"sk-ant-example",
		)
		if !bytes.Equal(got, requestBody) {
			t.Fatalf("sub2api passthrough should keep body unchanged, got=%s", string(got))
		}
	})

	t.Run("non-passthrough should keep preprocessing chain active", func(t *testing.T) {
		cfgManager := newConfigManagerForCommonTest(t, `{"stripBillingHeader":true}`)
		upstream := &config.UpstreamConfig{
			ServiceType:               "claude",
			StreamPassthroughEnabled:  &disabled,
			Sub2APIPassthroughEnabled: &disabled,
		}

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
			t.Fatal("expected body to be normalized when passthrough is disabled")
		}
		if userID := gjson.GetBytes(got, "metadata.user_id").String(); userID != "user_dev_account_acc_session_sid" {
			t.Fatalf("metadata.user_id = %q, want %q", userID, "user_dev_account_acc_session_sid")
		}
		if strings.Contains(string(got), "cch=") {
			t.Fatalf("billing header marker should be removed when stripBillingHeader is enabled, got=%s", string(got))
		}
	})

	t.Run("responses path should still normalize metadata", func(t *testing.T) {
		upstream := &config.UpstreamConfig{
			ServiceType:               "claude",
			StreamPassthroughEnabled:  &disabled,
			Sub2APIPassthroughEnabled: &disabled,
		}

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
