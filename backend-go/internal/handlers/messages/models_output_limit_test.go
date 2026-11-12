package messages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestModelsHandlerIncludesOutputLimits(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "registry fallback", true: "upstream explicit"}[explicit], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				model := map[string]interface{}{"id": "claude-sonnet-5", "object": "model"}
				if explicit {
					model["max_output_tokens"] = 12345
				}
				if r.URL.Path == "/v1/models/claude-sonnet-5" {
					model["custom_metadata"] = map[string]interface{}{"label": "preserve"}
					_ = json.NewEncoder(w).Encode(model)
				} else {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": "list", "data": []interface{}{model}})
				}
			}))
			defer upstream.Close()
			cm := setupModelsConfigManager(t, config.Config{Upstream: []config.UpstreamConfig{{Name: "claude", BaseURL: upstream.URL, APIKeys: []string{"test-key"}, ServiceType: "claude"}}})
			router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cm, newModelsTestScheduler(cm))
			for _, path := range []string{"/v1/models", "/v1/models/claude-sonnet-5"} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("Authorization", "Bearer test-key")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status=%d, body=%s", path, w.Code, w.Body.String())
				}
				var body map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if path == "/v1/models" {
					items, ok := body["data"].([]interface{})
					if !ok || len(items) != 1 {
						t.Fatalf("unexpected model list: %s", w.Body.String())
					}
					body = items[0].(map[string]interface{})
				} else if metadata, ok := body["custom_metadata"].(map[string]interface{}); !ok || metadata["label"] != "preserve" {
					t.Fatalf("detail metadata was lost: %s", w.Body.String())
				}
				want := float64(128000)
				if explicit {
					want = 12345
				}
				if got := body["max_output_tokens"]; got != want {
					t.Errorf("%s: max_output_tokens=%v, want %v", path, got, want)
				}
			}
		})
	}
}

func TestModelOutputLimitMetadata(t *testing.T) {
	upstream := &config.UpstreamConfig{
		ModelMapping:      map[string]string{"alias": "claude-sonnet-5"},
		ModelCapabilities: map[string]config.UpstreamModelCapability{"claude-sonnet-5": {MaxOutputTokens: 64000}},
	}
	global := map[string]config.UpstreamModelCapability{"claude-sonnet-5": {MaxOutputTokens: 32000}}
	if got := normalizeModelEntry(ModelEntry{ID: "alias"}, upstream, global).MaxOutputTokens; got != 64000 {
		t.Errorf("mapped channel limit = %d, want 64000", got)
	}
	if got := normalizeModelEntry(ModelEntry{ID: "claude-sonnet-5"}, nil, global).MaxOutputTokens; got != 32000 {
		t.Errorf("global limit = %d, want 32000", got)
	}
	if got := normalizeModelEntry(ModelEntry{ID: "alias", MaxOutputTokens: 12345}, upstream, global).MaxOutputTokens; got != 12345 {
		t.Errorf("explicit limit changed to %d", got)
	}
	for _, explicit := range []int{0, 12345} {
		merged := mergeModelEntryMetadata(ModelEntry{ID: "alias", MaxOutputTokens: explicit}, ModelEntry{ID: "alias", MaxOutputTokens: 64000})
		want := explicit
		if want == 0 {
			want = 64000
		}
		if merged.MaxOutputTokens != want {
			t.Errorf("merged limit = %d, want %d", merged.MaxOutputTokens, want)
		}
	}
	unknown := normalizeModelEntry(ModelEntry{ID: "unknown-test-model"}, nil, nil)
	encoded, err := json.Marshal(unknown)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["max_output_tokens"]; ok {
		t.Fatal("unknown model must not advertise an invented output limit")
	}
}
