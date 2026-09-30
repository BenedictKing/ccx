package gemini

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/gin-gonic/gin"
)

// Gemini 直通以未终结的 data 行收尾时（单换行或完全没有换行）必须补齐空行，
// 否则严格 SSE 解析器不会派发最后一帧（finish / usage / [DONE]）。
func TestStreamGeminiToGeminiTerminatesUnfinishedTail(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataLine := `data: {"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`
	properlyTerminated := dataLine + "\n\n"

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "单换行收尾补空行",
			body: dataLine + "\n",
			want: dataLine + "\n\n",
		},
		{
			name: "完全没有换行收尾补空行",
			body: dataLine,
			want: dataLine + "\n\n",
		},
		{
			name: "已经以空行收尾则不再补",
			body: properlyTerminated,
			want: properlyTerminated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			chunkChan := make(chan []byte, 1)
			errChan := make(chan error, 1)
			chunkChan <- []byte(tt.body)
			close(chunkChan)

			flusher, ok := interface{}(w).(http.Flusher)
			if !ok {
				t.Fatal("recorder should implement http.Flusher")
			}

			_, err := streamGeminiToGemini(
				c,
				newStreamLineReader(chunkChan, errChan),
				flusher,
				common.NewLimitedLogBuffer(1024),
				false,
				common.StreamPreflightTimeouts{InactivityTimeoutMs: 1000},
				common.NewStreamProgressLogger("Gemini-Test", time.Now(), false),
			)
			if err != nil {
				t.Fatalf("stream failed: %v", err)
			}
			if got := w.Body.String(); got != tt.want {
				t.Fatalf("client output mismatch:\ngot:  %q\nwant: %q", got, tt.want)
			}
			if !strings.HasSuffix(w.Body.String(), "\n\n") {
				t.Fatalf("输出必须以空行收尾，got %q", w.Body.String())
			}
		})
	}
}
