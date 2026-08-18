package utils

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestIsClientFingerprintRejection(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantMatch bool
	}{
		{"agentrouter unauthorized client", http.StatusUnauthorized,
			`{"error":{"message":"unauthorized client detected, contact support"},"type":"unauthorized_client_error"}`, true},
		{"underscore type field", http.StatusUnauthorized,
			`{"type":"unauthorized_client_error"}`, true},
		{"client detected uppercase", http.StatusForbidden,
			`{"message":"CLIENT DETECTED"}`, true},
		{"plain invalid key", http.StatusUnauthorized,
			`{"error":{"message":"无效的令牌","type":"new_api_error"}}`, false},
		{"wrong status code", http.StatusBadRequest,
			`{"error":{"message":"unauthorized client detected"}}`, false},
		{"ok status", http.StatusOK, `{}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsClientFingerprintRejection(tt.status, []byte(tt.body)); got != tt.wantMatch {
				t.Fatalf("IsClientFingerprintRejection(%d, %s) = %v, want %v", tt.status, tt.body, got, tt.wantMatch)
			}
		})
	}
}

func TestFetchUpstreamModelsProbeHeadersFirst(t *testing.T) {
	var gotUserAgents []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgents = append(gotUserAgents, r.Header.Get("User-Agent"))
		if r.Header.Get("User-Agent") != ClaudeCodeProbeUserAgent {
			t.Errorf("User-Agent = %q, want %q", r.Header.Get("User-Agent"), ClaudeCodeProbeUserAgent)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-4-6"}]}`))
	}))
	defer server.Close()

	status, body, learned, err := FetchUpstreamModels(context.Background(), server.Client(), server.URL+"/v1/models",
		func(h http.Header) { h.Set("Authorization", "Bearer sk-test") }, nil, true)
	if err != nil {
		t.Fatalf("FetchUpstreamModels: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if len(gotUserAgents) != 1 {
		t.Fatalf("server received %d requests, want 1", len(gotUserAgents))
	}
	if learned {
		t.Fatal("useProbeHeaders=true is protocol behavior, learned should be false")
	}
	if len(body) == 0 {
		t.Fatal("body should not be empty")
	}
}

func TestFetchUpstreamModelsRetriesWithProbeHeadersOnFingerprintRejection(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requestCount, 1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"unauthorized client detected"},"type":"unauthorized_client_error"}`))
			return
		}
		if r.Header.Get("User-Agent") != ClaudeCodeProbeUserAgent {
			t.Errorf("retry User-Agent = %q, want %q", r.Header.Get("User-Agent"), ClaudeCodeProbeUserAgent)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-4-6"}]}`))
	}))
	defer server.Close()

	status, _, learned, err := FetchUpstreamModels(context.Background(), server.Client(), server.URL+"/v1/models",
		func(h http.Header) { h.Set("Authorization", "Bearer sk-test") }, nil, false)
	if err != nil {
		t.Fatalf("FetchUpstreamModels: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !learned {
		t.Fatal("retry succeeded, learned should be true")
	}
	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("server received %d requests, want 2", got)
	}
}

func TestFetchUpstreamModelsNoRetryForPlainAuthFailure(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"无效的令牌"}}`))
	}))
	defer server.Close()

	status, _, learned, err := FetchUpstreamModels(context.Background(), server.Client(), server.URL+"/v1/models",
		func(h http.Header) { h.Set("Authorization", "Bearer sk-test") }, nil, false)
	if err != nil {
		t.Fatalf("FetchUpstreamModels: %v", err)
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
	if learned {
		t.Fatal("plain auth failure should not be learned")
	}
	if got := atomic.LoadInt32(&requestCount); got != 1 {
		t.Fatalf("server received %d requests, want 1", got)
	}
}

func TestFetchUpstreamModelsRetryStillFailsNotLearned(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"unauthorized client detected"}}`))
	}))
	defer server.Close()

	status, _, learned, err := FetchUpstreamModels(context.Background(), server.Client(), server.URL+"/v1/models",
		func(h http.Header) { h.Set("Authorization", "Bearer sk-test") }, nil, false)
	if err != nil {
		t.Fatalf("FetchUpstreamModels: %v", err)
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
	if learned {
		t.Fatal("retry did not succeed, learned should be false")
	}
	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("server received %d requests, want 2", got)
	}
}

func TestFetchUpstreamModelsCustomHeadersOverrideProbeHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "my-custom-agent/1.0" {
			t.Errorf("User-Agent = %q, want custom header to win", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	_, _, _, err := FetchUpstreamModels(context.Background(), server.Client(), server.URL+"/v1/models",
		func(h http.Header) { h.Set("Authorization", "Bearer sk-test") },
		map[string]string{"User-Agent": "my-custom-agent/1.0"}, true)
	if err != nil {
		t.Fatalf("FetchUpstreamModels: %v", err)
	}
}
