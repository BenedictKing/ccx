package images

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

func TestImageResponseContentType(t *testing.T) {
	for _, tt := range []struct {
		name, body, contentType, want string
		stream, convert               bool
	}{
		{"binary", "\x89PNG\r\n", "image/png", "image/png", false, false},
		{"json", `{"data":[]}`, "application/custom+json", "application/custom+json", false, false},
		{"stream", "data: test\n\n", "text/event-stream", "text/event-stream", true, false},
		{"converted", `{"data":[{"b64_json":"already"}]}`, "text/plain", "application/json", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {tt.contentType}}, Body: io.NopCloser(strings.NewReader(tt.body))}
			_, err := handleSuccess(c, resp, &config.EnvConfig{}, time.Now(), tt.stream, common.StreamPreflightTimeouts{}, &config.UpstreamConfig{ConvertImageURLToB64JSON: tt.convert}, []byte(`{"response_format":"b64_json"}`), "application/json")
			if err != nil {
				t.Fatal(err)
			}
			if got := w.Header().Get("Content-Type"); got != tt.want {
				t.Errorf("Content-Type = %q, want %q", got, tt.want)
			}
			if !tt.convert && w.Body.String() != tt.body {
				t.Error("透传响应体发生变化")
			}
		})
	}
}
