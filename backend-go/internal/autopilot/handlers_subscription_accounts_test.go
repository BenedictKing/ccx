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

// newAccountTestProfile 构造带一个子账号的 new_api 订阅。
func newAccountTestProfile(t *testing.T, store *SubscriptionStore, siteURL string) {
	t.Helper()
	profile := &SubscriptionProfile{
		SubscriptionUID: "sub-acc",
		DisplayName:     "主账号",
		Provider:        "new_api",
		BaseURL:         siteURL,
		AccessToken:     "main-secret-token",
		UserID:          "7",
		AuthTokenMode:   NewApiAuthModeBearer,
		Source:          "newapi_provision",
		Accounts: []NewApiAccount{{
			AccountUID:    "acct_sub_1",
			AccessToken:   "old-account-token",
			UserID:        "8",
			AuthTokenMode: NewApiAuthModeBearer,
			DisplayName:   "second",
			Status:        "active",
		}},
	}
	if err := store.Create(profile); err != nil {
		t.Fatalf("创建订阅失败: %v", err)
	}
}

func TestHandleUpdateSubscriptionAccountCredentials_SuccessAndMasked(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	newAccountTestProfile(t, store, site.URL)

	router := setupSubscriptionAccountRouter(t, &NewApiRouteDeps{Store: store})
	payload, _ := json.Marshal(NewApiAccountCredentialsUpdateRequest{
		AccessToken:   stringPtr("new-account-token"),
		AuthTokenMode: stringPtr(NewApiAuthModeRaw),
	})
	request := httptest.NewRequest(http.MethodPatch, "/api/subscriptions/sub-acc/accounts/acct_sub_1/credentials", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "new-account-token") {
		t.Fatal("响应不得包含完整 accessToken")
	}
	var item NewApiAccountItem
	if err := json.Unmarshal(recorder.Body.Bytes(), &item); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if item.AccessTokenMasked != "****oken" {
		t.Fatalf("AccessTokenMasked=%q", item.AccessTokenMasked)
	}
	if item.Balance != 50000 || item.Status != "active" {
		t.Fatalf("余额/状态未更新: balance=%v status=%s", item.Balance, item.Status)
	}

	updated := store.Get("sub-acc")
	if len(updated.Accounts) != 1 {
		t.Fatalf("账号数量异常: %d", len(updated.Accounts))
	}
	acc := updated.Accounts[0]
	if acc.AccessToken != "new-account-token" || acc.AuthTokenMode != NewApiAuthModeRaw {
		t.Fatalf("子账号凭证未保存: %+v", acc)
	}
	// mock 站点 /api/user/self 返回 ID=7；未提交 userId 时保留原值（对齐主账号端点语义）
	if acc.UserID != "8" {
		t.Fatalf("未提交 userId 时应保留原值, got=%q", acc.UserID)
	}
	// 主账号凭证不受影响
	if updated.AccessToken != "main-secret-token" {
		t.Fatalf("主账号凭证不应被修改, got=%q", updated.AccessToken)
	}

	// 显式清空 userId → 用站点返回的 ID 派生回填
	payload2, _ := json.Marshal(NewApiAccountCredentialsUpdateRequest{UserID: stringPtr("")})
	request2 := httptest.NewRequest(http.MethodPatch, "/api/subscriptions/sub-acc/accounts/acct_sub_1/credentials", bytes.NewReader(payload2))
	request2.Header.Set("Content-Type", "application/json")
	recorder2 := httptest.NewRecorder()
	router.ServeHTTP(recorder2, request2)
	if recorder2.Code != http.StatusOK {
		t.Fatalf("二次更新状态码=%d body=%s", recorder2.Code, recorder2.Body.String())
	}
	if acc := store.Get("sub-acc").Accounts[0]; acc.UserID != "7" {
		t.Fatalf("清空 userId 应回填站点返回值, got=%q", acc.UserID)
	}
}

func TestHandleUpdateSubscriptionAccountCredentials_InvalidTokenDoesNotPersist(t *testing.T) {
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
	newAccountTestProfile(t, store, site.URL)

	router := setupSubscriptionAccountRouter(t, &NewApiRouteDeps{Store: store})
	payload, _ := json.Marshal(NewApiAccountCredentialsUpdateRequest{AccessToken: stringPtr("bad-token")})
	request := httptest.NewRequest(http.MethodPatch, "/api/subscriptions/sub-acc/accounts/acct_sub_1/credentials", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("状态码=%d body=%s", recorder.Code, recorder.Body.String())
	}
	acc := store.Get("sub-acc").Accounts[0]
	if acc.AccessToken != "old-account-token" {
		t.Fatalf("校验失败后不应修改 token，实际=%q", acc.AccessToken)
	}
}

func TestHandleUpdateSubscriptionAccountCredentials_ValidatesInput(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	newAccountTestProfile(t, store, site.URL)
	router := setupSubscriptionAccountRouter(t, &NewApiRouteDeps{Store: store})

	cases := []struct {
		name   string
		body   string
		target string
		code   int
	}{
		{"空请求体", `{}`, "/api/subscriptions/sub-acc/accounts/acct_sub_1/credentials", http.StatusBadRequest},
		{"空 accessToken", `{"accessToken":"  "}`, "/api/subscriptions/sub-acc/accounts/acct_sub_1/credentials", http.StatusBadRequest},
		{"非法 authTokenMode", `{"authTokenMode":"weird"}`, "/api/subscriptions/sub-acc/accounts/acct_sub_1/credentials", http.StatusBadRequest},
		{"账号不存在", `{"userId":"9"}`, "/api/subscriptions/sub-acc/accounts/acct_missing/credentials", http.StatusNotFound},
	}
	for _, tc := range cases {
		request := httptest.NewRequest(http.MethodPatch, tc.target, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != tc.code {
			t.Fatalf("%s: 状态码=%d 期望=%d body=%s", tc.name, recorder.Code, tc.code, recorder.Body.String())
		}
	}
}
