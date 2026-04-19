package config

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestModelsHealthCheckDefaults(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		upstream := &UpstreamConfig{}
		if upstream.IsModelsHealthCheckEnabled() {
			t.Fatal("IsModelsHealthCheckEnabled() should default to false")
		}
	})

	t.Run("interval defaults to 60", func(t *testing.T) {
		upstream := &UpstreamConfig{}
		if got := upstream.GetModelsHealthCheckIntervalMinutes(); got != 60 {
			t.Fatalf("GetModelsHealthCheckIntervalMinutes() = %d, want 60", got)
		}
	})

	t.Run("invalid interval should normalize to 60", func(t *testing.T) {
		invalid := 0
		upstream := &UpstreamConfig{ModelsHealthCheckIntervalMinutes: &invalid}
		upstream.NormalizeModelsHealthCheckOptions()
		if got := upstream.GetModelsHealthCheckIntervalMinutes(); got != 60 {
			t.Fatalf("GetModelsHealthCheckIntervalMinutes() = %d, want 60", got)
		}
	})
}

func TestBuildModelsHealthCheckURL(t *testing.T) {
	testCases := []struct {
		name        string
		baseURL     string
		serviceType string
		want        string
	}{
		{
			name:        "append v1 by default",
			baseURL:     "https://api.example.com",
			serviceType: "claude",
			want:        "https://api.example.com/v1/models",
		},
		{
			name:        "keep existing version suffix",
			baseURL:     "https://api.example.com/v1",
			serviceType: "openai",
			want:        "https://api.example.com/v1/models",
		},
		{
			name:        "skip version when baseUrl ends with hash",
			baseURL:     "https://api.example.com#",
			serviceType: "responses",
			want:        "https://api.example.com/models",
		},
		{
			name:        "gemini uses v1beta",
			baseURL:     "https://generativelanguage.googleapis.com",
			serviceType: "gemini",
			want:        "https://generativelanguage.googleapis.com/v1beta/models",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildModelsHealthCheckURL(tc.baseURL, tc.serviceType); got != tc.want {
				t.Fatalf("buildModelsHealthCheckURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunModelsHealthCheckOnceBlacklistsNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		auth := r.Header.Get("Authorization")
		if strings.Contains(auth, "good-key") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer server.Close()

	cm := newConfigManagerForTestJSON(t, `{
		"upstream": [
			{
				"name": "models-health",
				"baseUrl": "`+server.URL+`",
				"apiKeys": ["good-key", "bad-key"],
				"serviceType": "responses",
				"modelsHealthCheckEnabled": true,
				"modelsHealthCheckIntervalMinutes": 1
			}
		]
	}`)

	lastRunAt := make(map[string]time.Time)
	cm.runModelsHealthCheckOnce(lastRunAt, time.Now())

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 {
		t.Fatalf("len(cfg.Upstream) = %d, want 1", len(cfg.Upstream))
	}
	upstream := cfg.Upstream[0]
	if !slices.Contains(upstream.APIKeys, "good-key") {
		t.Fatalf("good-key should stay active, got %#v", upstream.APIKeys)
	}
	if slices.Contains(upstream.APIKeys, "bad-key") {
		t.Fatalf("bad-key should be removed from active keys, got %#v", upstream.APIKeys)
	}
	if len(upstream.DisabledAPIKeys) != 1 {
		t.Fatalf("len(DisabledAPIKeys) = %d, want 1", len(upstream.DisabledAPIKeys))
	}
	if upstream.DisabledAPIKeys[0].Key != "bad-key" {
		t.Fatalf("disabled key = %q, want bad-key", upstream.DisabledAPIKeys[0].Key)
	}
	if upstream.DisabledAPIKeys[0].Reason != modelsHealthCheckReason {
		t.Fatalf("disabled reason = %q, want %q", upstream.DisabledAPIKeys[0].Reason, modelsHealthCheckReason)
	}
}

func TestRunModelsHealthCheckOnceSkipsOnTransportError(t *testing.T) {
	cm := newConfigManagerForTestJSON(t, `{
		"upstream": [
			{
				"name": "models-health",
				"baseUrl": "http://127.0.0.1:1",
				"apiKeys": ["bad-key"],
				"serviceType": "responses",
				"modelsHealthCheckEnabled": true,
				"modelsHealthCheckIntervalMinutes": 1
			}
		]
	}`)

	lastRunAt := make(map[string]time.Time)
	cm.runModelsHealthCheckOnce(lastRunAt, time.Now())

	cfg := cm.GetConfig()
	upstream := cfg.Upstream[0]
	if len(upstream.APIKeys) != 1 || upstream.APIKeys[0] != "bad-key" {
		t.Fatalf("transport error should not blacklist key, got %#v", upstream.APIKeys)
	}
	if len(upstream.DisabledAPIKeys) != 0 {
		t.Fatalf("transport error should not move key to blacklist, got %#v", upstream.DisabledAPIKeys)
	}
}
