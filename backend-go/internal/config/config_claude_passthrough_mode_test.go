package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeClaudePassthroughMode(t *testing.T) {
	t.Run("nil receiver should be safe", func(t *testing.T) {
		var upstream *UpstreamConfig
		upstream.NormalizeClaudePassthroughMode()
	})

	t.Run("non-claude channel should keep original values", func(t *testing.T) {
		enabled := true
		upstream := &UpstreamConfig{
			ServiceType:               "responses",
			StreamPassthroughEnabled:  &enabled,
			Sub2APIPassthroughEnabled: &enabled,
		}

		upstream.NormalizeClaudePassthroughMode()

		if !upstream.IsStreamPassthroughEnabled() {
			t.Fatal("stream passthrough should remain enabled for non-claude channel")
		}
	})

	t.Run("claude with sub2api enabled should force-disable full passthrough", func(t *testing.T) {
		enabled := true
		upstream := &UpstreamConfig{
			ServiceType:               "claude",
			StreamPassthroughEnabled:  &enabled,
			Sub2APIPassthroughEnabled: &enabled,
		}

		upstream.NormalizeClaudePassthroughMode()

		if !upstream.IsSub2APIPassthroughEnabled() {
			t.Fatal("sub2api passthrough should stay enabled")
		}
		if upstream.IsStreamPassthroughEnabled() {
			t.Fatal("stream passthrough should be disabled when sub2api passthrough is enabled")
		}
	})
}

func TestClaudePassthroughFlagDefaults(t *testing.T) {
	t.Run("stream passthrough defaults to true", func(t *testing.T) {
		upstream := &UpstreamConfig{}
		if !upstream.IsStreamPassthroughEnabled() {
			t.Fatal("IsStreamPassthroughEnabled() should default to true")
		}
	})

	t.Run("sub2api passthrough defaults to false", func(t *testing.T) {
		upstream := &UpstreamConfig{}
		if upstream.IsSub2APIPassthroughEnabled() {
			t.Fatal("IsSub2APIPassthroughEnabled() should default to false")
		}
	})

	t.Run("explicit false and true values should be honored", func(t *testing.T) {
		off := false
		on := true
		upstream := &UpstreamConfig{
			StreamPassthroughEnabled:  &off,
			Sub2APIPassthroughEnabled: &on,
		}
		if upstream.IsStreamPassthroughEnabled() {
			t.Fatal("IsStreamPassthroughEnabled() should be false when explicitly disabled")
		}
		if !upstream.IsSub2APIPassthroughEnabled() {
			t.Fatal("IsSub2APIPassthroughEnabled() should be true when explicitly enabled")
		}
	})
}

func TestMarkKeyAsFailedInitialAndAfterFixedDuration(t *testing.T) {
	cm := &ConfigManager{
		failedKeysCache:     make(map[string]*FailedKey),
		keyBackoffDurations: []time.Duration{time.Minute, 2 * time.Minute},
	}

	cm.MarkKeyAsFailed("test-key", "Messages")
	cacheKey := failedKeyCacheKey("Messages", "test-key")
	failure := cm.failedKeysCache[cacheKey]
	if failure == nil {
		t.Fatal("expected failed key to exist after first failure")
	}
	if failure.FailureCount != 1 {
		t.Fatalf("FailureCount = %d, want 1", failure.FailureCount)
	}
	if failure.FixedDuration != 0 {
		t.Fatalf("FixedDuration = %v, want 0", failure.FixedDuration)
	}

	failure.FixedDuration = 10 * time.Minute
	cm.MarkKeyAsFailed("test-key", "Messages")
	failure = cm.failedKeysCache[cacheKey]
	if failure.FailureCount != 2 {
		t.Fatalf("FailureCount = %d, want 2", failure.FailureCount)
	}
	if failure.FixedDuration != 0 {
		t.Fatalf("FixedDuration = %v, want 0 after normal failure", failure.FixedDuration)
	}
}

func TestClaudePassthroughMutualExclusionOnAddAndUpdate(t *testing.T) {
	testCases := []struct {
		name        string
		initialJSON string
		add         func(*ConfigManager, UpstreamConfig) error
		update      func(*ConfigManager) error
		get         func(Config) UpstreamConfig
	}{
		{
			name:        "messages",
			initialJSON: `{"upstream":[{"name":"msg-ch","baseUrl":"https://api.example.com","apiKeys":["sk-1"],"serviceType":"claude"}]}`,
			add:         (*ConfigManager).AddUpstream,
			update: func(cm *ConfigManager) error {
				enabled := true
				_, err := cm.UpdateUpstream(0, UpstreamUpdate{
					StreamPassthroughEnabled:  &enabled,
					Sub2APIPassthroughEnabled: &enabled,
				})
				return err
			},
			get: func(cfg Config) UpstreamConfig { return cfg.Upstream[0] },
		},
		{
			name:        "responses",
			initialJSON: `{"responsesUpstream":[{"name":"resp-ch","baseUrl":"https://api.example.com","apiKeys":["sk-1"],"serviceType":"claude"}]}`,
			add:         (*ConfigManager).AddResponsesUpstream,
			update: func(cm *ConfigManager) error {
				enabled := true
				_, err := cm.UpdateResponsesUpstream(0, UpstreamUpdate{
					StreamPassthroughEnabled:  &enabled,
					Sub2APIPassthroughEnabled: &enabled,
				})
				return err
			},
			get: func(cfg Config) UpstreamConfig { return cfg.ResponsesUpstream[0] },
		},
		{
			name:        "chat",
			initialJSON: `{"chatUpstream":[{"name":"chat-ch","baseUrl":"https://api.example.com","apiKeys":["sk-1"],"serviceType":"claude"}]}`,
			add:         (*ConfigManager).AddChatUpstream,
			update: func(cm *ConfigManager) error {
				enabled := true
				_, err := cm.UpdateChatUpstream(0, UpstreamUpdate{
					StreamPassthroughEnabled:  &enabled,
					Sub2APIPassthroughEnabled: &enabled,
				})
				return err
			},
			get: func(cfg Config) UpstreamConfig { return cfg.ChatUpstream[0] },
		},
		{
			name:        "gemini",
			initialJSON: `{"geminiUpstream":[{"name":"gem-ch","baseUrl":"https://api.example.com","apiKeys":["sk-1"],"serviceType":"claude"}]}`,
			add:         (*ConfigManager).AddGeminiUpstream,
			update: func(cm *ConfigManager) error {
				enabled := true
				_, err := cm.UpdateGeminiUpstream(0, UpstreamUpdate{
					StreamPassthroughEnabled:  &enabled,
					Sub2APIPassthroughEnabled: &enabled,
				})
				return err
			},
			get: func(cfg Config) UpstreamConfig { return cfg.GeminiUpstream[0] },
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name+"_update", func(t *testing.T) {
			cm := newConfigManagerForTestJSON(t, tc.initialJSON)
			if err := tc.update(cm); err != nil {
				t.Fatalf("update failed: %v", err)
			}

			upstream := tc.get(cm.GetConfig())
			assertClaudePassthroughMutualExclusion(t, upstream)
		})

		t.Run(tc.name+"_add", func(t *testing.T) {
			cm := newConfigManagerForTestJSON(t, `{}`)
			enabled := true
			err := tc.add(cm, UpstreamConfig{
				Name:                            tc.name + "-added",
				BaseURL:                         "https://api.example.com",
				APIKeys:                         []string{"sk-1"},
				ServiceType:                     "claude",
				StreamPassthroughEnabled:        &enabled,
				Sub2APIPassthroughEnabled:       &enabled,
				StrictRequestPassthroughEnabled: &enabled,
			})
			if err != nil {
				t.Fatalf("add failed: %v", err)
			}

			upstream := tc.get(cm.GetConfig())
			assertClaudePassthroughMutualExclusion(t, upstream)
		})
	}
}

func newConfigManagerForTestJSON(t *testing.T, rawJSON string) *ConfigManager {
	t.Helper()
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(configPath, []byte(rawJSON), 0644); err != nil {
		t.Fatalf("write test config failed: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	return cm
}

func assertClaudePassthroughMutualExclusion(t *testing.T, upstream UpstreamConfig) {
	t.Helper()
	if !upstream.IsSub2APIPassthroughEnabled() {
		t.Fatalf("IsSub2APIPassthroughEnabled() = %v, want true", upstream.IsSub2APIPassthroughEnabled())
	}
	if upstream.IsStreamPassthroughEnabled() {
		t.Fatalf("IsStreamPassthroughEnabled() = %v, want false", upstream.IsStreamPassthroughEnabled())
	}
}
