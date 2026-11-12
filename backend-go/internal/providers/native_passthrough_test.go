package providers

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestNativeResponsesRequestPreservesWireBody(t *testing.T) {
	const body = `{ "model":"native", "input":[{"type":"function_call_output","call_id":"external","output":"ok","status":"completed"},{"type":"message","role":"assistant","content":[{"type":"input_text","text":"history"}]}], "future":{"id":9007199254740993}, "reasoning":{"effort":"xhigh"} }`
	for _, baseURL := range []string{"https://example.com", "https://api.xiaomimimo.com"} {
		c := newGinContext(http.MethodPost, "/v1/responses", []byte(body), context.Background())
		req, _, err := (&ResponsesProvider{}).ConvertToProviderRequest(c, &config.UpstreamConfig{BaseURL: baseURL, ServiceType: "responses"}, "key")
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(req.Body)
		if string(got) != body {
			t.Fatalf("default native body changed: %s", got)
		}
	}
}
