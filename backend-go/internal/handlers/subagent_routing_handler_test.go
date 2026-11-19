package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestSubagentRoutingHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Config{Upstream: []config.UpstreamConfig{{Name: "a", ChannelUID: "uid-a", BaseURL: "https://a.example", APIKeys: []string{"secret"}, Status: "active"}}}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := config.NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	router := gin.New()
	RegisterSubagentRoutingRoutes(router.Group("/api"), cm)
	router.GET("/api/responses/channels/:id/logs", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/messages/channels/subagent-routing", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", get.Code, get.Body.String())
	}
	var view config.SubagentRoutingView
	if err := json.Unmarshal(get.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Revision == "" || view.Activated || bytes.Contains(get.Body.Bytes(), []byte("secret")) {
		t.Fatalf("GET 应返回 revision 且不泄露 Key: %+v", view)
	}

	payload, _ := json.Marshal(subagentRoutingUpdateRequest{Revision: view.Revision, Policy: config.SubagentRoutingPolicy{Model: " sol ", Channels: view.Policy.Channels}})
	put := httptest.NewRecorder()
	router.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/api/messages/channels/subagent-routing", bytes.NewReader(payload)))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", put.Code, put.Body.String())
	}
	stale := httptest.NewRecorder()
	router.ServeHTTP(stale, httptest.NewRequest(http.MethodPut, "/api/messages/channels/subagent-routing", bytes.NewReader(payload)))
	if stale.Code != http.StatusConflict {
		t.Fatalf("过期 revision status=%d body=%s", stale.Code, stale.Body.String())
	}
	unsupported := httptest.NewRecorder()
	router.ServeHTTP(unsupported, httptest.NewRequest(http.MethodGet, "/api/images/channels/subagent-routing", nil))
	if unsupported.Code != http.StatusNotFound {
		t.Fatalf("Images 不注册独立子代理策略，status=%d", unsupported.Code)
	}
}
