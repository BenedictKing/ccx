package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func loadSharedPreset(t *testing.T, protocol, provider string) config.UpstreamConfig {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "channel-presets", protocol+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Providers map[string]config.UpstreamConfig `json:"providers"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	upstream, ok := source.Providers[provider]
	if !ok {
		t.Fatalf("preset %s/%s missing", protocol, provider)
	}
	upstream.ServiceType = "openai"
	upstream.BaseURL = "https://example.invalid/v1"
	return upstream
}

func TestLatestPresetsReasoningRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, provider := range []string{"kimi", "glm"} {
		for _, entry := range []struct{ effort, want string }{
			{"low", "low"}, {"high", "high"}, {"max", "max"},
			{"medium", "high"}, {"xhigh", "max"}, {"none", "low"},
		} {
			for _, protocol := range []string{"responses", "messages"} {
				t.Run(provider+"/"+protocol+"/"+entry.effort, func(t *testing.T) {
					presetFile, model, path := "codex-responses", "sol", "/v1/responses"
					body := map[string]interface{}{"model": model, "input": "hello", "reasoning": map[string]string{"effort": entry.effort}}
					var p Provider = &ResponsesProvider{}
					if protocol == "messages" {
						presetFile, model, path = "claude-messages", "sonnet", "/v1/messages"
						body = map[string]interface{}{"model": model, "messages": []map[string]string{{"role": "user", "content": "hello"}}}
						p = &OpenAIProvider{}
					}
					upstream := loadSharedPreset(t, presetFile, provider)
					if protocol == "messages" {
						upstream.ReasoningMapping = map[string]string{model: entry.effort}
					}
					raw, _ := json.Marshal(body)
					c := newGinContext(http.MethodPost, path, raw, context.Background())
					req, _, err := p.ConvertToProviderRequest(c, &upstream, "sk-test")
					if err != nil {
						t.Fatal(err)
					}
					out, err := io.ReadAll(req.Body)
					if err != nil {
						t.Fatal(err)
					}
					if got := gjson.GetBytes(out, "reasoning_effort").String(); got != entry.want {
						t.Fatalf("reasoning_effort=%q want %q; %s", got, entry.want, out)
					}
					if gjson.GetBytes(out, "reasoning").Exists() {
						t.Fatalf("legacy reasoning leaked: %s", out)
					}
					if got := gjson.GetBytes(out, "model").String(); got != upstream.ModelMapping[model] {
						t.Fatalf("model=%q want %q", got, upstream.ModelMapping[model])
					}
				})
			}
		}
	}
}

func TestDeepSeekPresetPreservesImages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, entry := range []struct{ name, body, imagePath, wantURL string }{
		{"base64", `{"model":"sonnet","messages":[{"role":"user","content":[{"type":"text","text":"before"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}},{"type":"text","text":"after"}]}]}`, "messages.0.content.1.image_url.url", "data:image/png;base64,iVBORw0KGgo="},
		{"image-only", `{"model":"sonnet","messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.invalid/image.png"}}]}]}`, "messages.0.content.0.image_url.url", "https://example.invalid/image.png"},
		{"tool-result", `{"model":"sonnet","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call-1","name":"screenshot","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"text","text":"screenshot"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]}]}`, "messages.1.content.1.image_url.url", "data:image/png;base64,iVBORw0KGgo="},
	} {
		t.Run(entry.name, func(t *testing.T) {
			upstream := loadSharedPreset(t, "claude-messages", "deepseek")
			if upstream.NoVision {
				t.Fatal("DeepSeek preset disabled vision")
			}
			c := newGinContext(http.MethodPost, "/v1/messages", []byte(entry.body), context.Background())
			req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, &upstream, "sk-test")
			if err != nil {
				t.Fatal(err)
			}
			out, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			if got := gjson.GetBytes(out, entry.imagePath).String(); got != entry.wantURL {
				t.Fatalf("image=%q want %q; %s", got, entry.wantURL, out)
			}
			if gjson.GetBytes(out, "temperature").Exists() {
				t.Fatalf("unspecified temperature injected: %s", out)
			}
			if entry.name == "base64" && (gjson.GetBytes(out, "messages.0.content.0.text").String() != "before" || gjson.GetBytes(out, "messages.0.content.2.text").String() != "after") {
				t.Fatalf("content order changed: %s", out)
			}
			if entry.name == "tool-result" && gjson.GetBytes(out, "messages.1.tool_call_id").String() != "call-1" {
				t.Fatalf("tool result lost call ID: %s", out)
			}
		})
	}
}
