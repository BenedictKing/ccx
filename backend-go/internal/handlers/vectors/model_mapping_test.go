package vectors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestBuildProviderRequestMapsEmbeddingModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)
	upstream := &config.UpstreamConfig{ServiceType: "openai", ModelMapping: map[string]string{"embed-alias": "embed-actual"}}
	body := []byte(`{"model":"embed-alias","input":"hello"}`)
	req, err := buildProviderRequest(c, upstream, "https://api.example.com/v1", "sk-test", body, "embed-alias")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != "embed-actual" {
		t.Fatalf("model = %v, want embed-actual", payload["model"])
	}
}
