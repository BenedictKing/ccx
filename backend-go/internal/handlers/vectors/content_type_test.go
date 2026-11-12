package vectors

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestVectorResponsePreservesContentType(t *testing.T) {
	const body = `{"object":"list","data":[],"usage":{"prompt_tokens":1}}`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/custom+json"}}, Body: io.NopCloser(strings.NewReader(body))}
	if _, err := handleSuccess(c, resp, &config.EnvConfig{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := w.Header().Get("Content-Type"); got != "application/custom+json" {
		t.Errorf("Content-Type = %q", got)
	}
	if w.Body.String() != body {
		t.Error("透传响应体发生变化")
	}
}
