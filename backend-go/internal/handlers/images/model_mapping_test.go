package images

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestBuildOperationRequestMapsImageModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"model":"image-alias","prompt":"draw"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	upstream := &config.UpstreamConfig{ServiceType: "openai", ModelMapping: map[string]string{"image-alias": "image-actual"}}
	req, err := buildOperationRequest(c, upstream, "https://api.example.com", "sk-test", body, "image-alias", operationGenerations, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	requestBody, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(requestBody), `"model":"image-actual"`) {
		t.Fatalf("request model not mapped: %s", requestBody)
	}
}
