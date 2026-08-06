package autopilot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupSubscriptionAccountRouter(t *testing.T, deps *NewApiRouteDeps) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterSubscriptionAccountRoutes(router.Group("/api"), deps)
	return router
}

func TestHandleUpdateNewApiCredentials_SuccessAndMasked(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	profile := &SubscriptionProfile{
		SubscriptionUID: "sub-main",
		DisplayName:     "主账号",
		Provider:        "new_api",
		BaseURL:         site.URL,
		AccessToken:     "old-secret-token",
		UserID:          "7",
		AuthTokenMode:   NewApiAuthModeBearer,
		Source:          "newapi_provision",
	}
	if err := store.Create(profile); err != nil {
		t.Fatalf("创建订阅失败: %v", err)
	}

	router := setupSubscriptionAccountRouter(t, &NewApiRouteDeps{Store: store})
	payload, _ := json.Marshal(NewApiCredentialsUpdateRequest{
		AccessToken:   stringPtr("new-secret-token"),
		AuthTokenMode: stringPtr(NewApiAuthModeRaw),
	})
	request := httptest.NewRequest(http.MethodPatch, "/api/subscriptions/sub-main/newapi-credentials", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "new-secret-token") {
		t.Fatal("响应不得包含完整 accessToken")
	}
	var item SubscriptionItem
	if err := json.Unmarshal(recorder.Body.Bytes(), &item); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if item.AccessTokenMasked != "****oken" {
		t.Fatalf("AccessTokenMasked=%q", item.AccessTokenMasked)
	}
	if item.Balance != 50000 || item.UsedQuota != 1000 {
		t.Fatalf("余额未更新: balance=%v used=%v", item.Balance, item.UsedQuota)
	}
	updated := store.Get("sub-main")
	if updated.AccessToken != "new-secret-token" || updated.AuthTokenMode != NewApiAuthModeRaw {
		t.Fatalf("凭证未保存: %+v", updated)
	}
}

func TestHandleUpdateNewApiCredentials_InvalidTokenDoesNotPersist(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	profile := &SubscriptionProfile{
		SubscriptionUID: "sub-invalid",
		DisplayName:     "主账号",
		Provider:        "new_api",
		BaseURL:         site.URL,
		AccessToken:     "old-secret-token",
		Source:          "newapi_provision",
	}
	if err := store.Create(profile); err != nil {
		t.Fatalf("创建订阅失败: %v", err)
	}

	router := setupSubscriptionAccountRouter(t, &NewApiRouteDeps{Store: store})
	payload, _ := json.Marshal(NewApiCredentialsUpdateRequest{AccessToken: stringPtr("bad-token")})
	request := httptest.NewRequest(http.MethodPatch, "/api/subscriptions/sub-invalid/newapi-credentials", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("状态码=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := store.Get("sub-invalid").AccessToken; got != "old-secret-token" {
		t.Fatalf("校验失败后不应修改 token，实际=%q", got)
	}
}

func stringPtr(value string) *string {
	return &value
}
