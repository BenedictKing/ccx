package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/gin-gonic/gin"
)

func TestNativeGeminiPreservesRequestAndResponse(t *testing.T) {
	const request = `{ "contents":[{"role":"user","parts":[{"text":"hello"},{"text":""}]}], "future":9007199254740993 }`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/native:generateContent?future=a%20b&key=gateway-key", strings.NewReader(request))
	req, err := buildProviderRequest(c, &config.UpstreamConfig{ServiceType: "gemini"}, "https://example.com", "upstream-key", []byte(request), nil, "native", false)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(req.Body)
	if string(got) != request || req.URL.RawQuery != "future=a%20b" {
		t.Fatalf("native request changed: %s %s", req.URL, got)
	}
	const response = `{ "candidates":[{"content":{"role":"model","parts":[{"text":"hi","future":9007199254740993}]}}], "future":{"x":true}, "usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":0} }`
	w := httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = req
	resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json; profile=native"}}, Body: io.NopCloser(strings.NewReader(response))}
	_, err = handleSuccess(c, resp, "gemini", &config.EnvConfig{}, time.Now(), nil, "native", false, false, common.StreamPreflightTimeouts{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Body.String() != response || w.Header().Get("Content-Type") != "application/json; profile=native" {
		t.Fatalf("native response changed: %v %s", w.Header(), w.Body.String())
	}
}
