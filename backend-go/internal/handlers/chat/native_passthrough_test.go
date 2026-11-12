package chat

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestDefaultChatRequestPreservesWireBody(t *testing.T) {
	const body = `{ "model":"native", "messages":[{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"exec","arguments":"{}"}}]}], "future":{"id":9007199254740993} }`
	for _, service := range []string{"openai", "gemini"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req, err := buildProviderRequest(c, &config.UpstreamConfig{ServiceType: service}, "https://example.com", "key", []byte(body), "native", false)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(req.Body)
		if string(got) != body {
			t.Fatalf("%s default body changed: %s", service, got)
		}
	}
}
