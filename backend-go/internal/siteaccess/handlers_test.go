package siteaccess

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// 账号列表/详情响应不得泄露管理令牌的明文或密文，只能给脱敏尾部。
func TestAccountsList_DoesNotLeakToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewAccountStore(filepath.Join(t.TempDir(), "accounts.json"))
	uid, err := store.Upsert(SiteAccount{
		SiteType:    "newapi",
		BaseURL:     "https://relay.example.com",
		AccessToken: "super-secret-token-1234",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"), "")
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}

	router := gin.New()
	RegisterRoutes(router.Group("/api"), &Deps{Store: store, CfgManager: cfgManager})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/siteaccess/accounts", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码: %d, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "super-secret-token-1234") || strings.Contains(body, encryptedTokenPrefix) {
		t.Fatalf("响应泄露了令牌明文或密文: %s", body)
	}
	var payload struct {
		Accounts []accountResponse `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应解析: %v", err)
	}
	if len(payload.Accounts) != 1 || payload.Accounts[0].UID != uid {
		t.Fatalf("账号列表不符: %+v", payload)
	}
	if payload.Accounts[0].MaskedToken != "****1234" {
		t.Fatalf("脱敏展示错误: %q", payload.Accounts[0].MaskedToken)
	}
}

func TestProvisionAndReconcileResponsesMatchWebContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	site, uid, store, cfgManager, adapter := newProvisionTestDeps(t)
	router := gin.New()
	RegisterRoutes(router.Group("/api"), &Deps{Store: store, CfgManager: cfgManager, Adapter: adapter})

	provision := httptest.NewRecorder()
	router.ServeHTTP(provision, httptest.NewRequest(http.MethodPost,
		"/api/siteaccess/accounts/"+uid+"/provision",
		strings.NewReader(`{"protocol":"chat","groups":["default"],"models":["gpt-4o"]}`)))
	if provision.Code != http.StatusOK {
		t.Fatalf("接入状态码: %d, body: %s", provision.Code, provision.Body.String())
	}
	var result struct {
		AccountUID string `json:"accountUID"`
		Groups     []struct {
			Group       string `json:"group"`
			KeyName     string `json:"keyName"`
			ChannelName string `json:"channelName"`
			Skipped     string `json:"skipped"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(provision.Body.Bytes(), &result); err != nil {
		t.Fatalf("解析接入响应: %v", err)
	}
	if result.AccountUID != uid || len(result.Groups) != 1 || result.Groups[0].Group != "default" || result.Groups[0].KeyName == "" || result.Groups[0].ChannelName == "" || result.Groups[0].Skipped != "" {
		t.Fatalf("接入响应与 Web 契约不符: %s", provision.Body.String())
	}
	if strings.Contains(provision.Body.String(), `"Groups"`) || strings.Contains(provision.Body.String(), `"KeyName"`) {
		t.Fatalf("接入响应混入大写字段: %s", provision.Body.String())
	}
	unbind := httptest.NewRecorder()
	router.ServeHTTP(unbind, httptest.NewRequest(http.MethodPut,
		"/api/siteaccess/channels/chat/0/binding", strings.NewReader(`{"accountUid":"","group":""}`)))
	if unbind.Code != http.StatusOK || cfgManager.GetConfig().ChatUpstream[0].SiteAccountUID != "" {
		t.Fatalf("解绑端点失败: %d %s", unbind.Code, unbind.Body.String())
	}
	bind := httptest.NewRecorder()
	router.ServeHTTP(bind, httptest.NewRequest(http.MethodPut,
		"/api/siteaccess/channels/chat/0/binding", strings.NewReader(`{"accountUid":"`+uid+`","group":"default"}`)))
	if bind.Code != http.StatusOK || cfgManager.GetConfig().ChatUpstream[0].SiteAccountUID != uid {
		t.Fatalf("绑定端点失败: %d %s", bind.Code, bind.Body.String())
	}
	clean := httptest.NewRecorder()
	router.ServeHTTP(clean, httptest.NewRequest(http.MethodPost,
		"/api/siteaccess/accounts/"+uid+"/reconcile", nil))
	if clean.Code != http.StatusOK || !strings.Contains(clean.Body.String(), `"actions":[]`) {
		t.Fatalf("无动作对账应返回空数组: %d %s", clean.Code, clean.Body.String())
	}

	site.deleteTokenByName(result.Groups[0].KeyName)
	reconcile := httptest.NewRecorder()
	router.ServeHTTP(reconcile, httptest.NewRequest(http.MethodPost,
		"/api/siteaccess/accounts/"+uid+"/reconcile", nil))
	if reconcile.Code != http.StatusOK {
		t.Fatalf("对账状态码: %d, body: %s", reconcile.Code, reconcile.Body.String())
	}
	var actions struct {
		Actions []struct {
			Action  string `json:"action"`
			Detail  string `json:"detail"`
			KeyName string `json:"keyName"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(reconcile.Body.Bytes(), &actions); err != nil {
		t.Fatalf("解析对账响应: %v", err)
	}
	if len(actions.Actions) != 1 || actions.Actions[0].Action != "disabled" || actions.Actions[0].Detail == "" || actions.Actions[0].KeyName != result.Groups[0].KeyName {
		t.Fatalf("对账响应与 Web 契约不符: %s", reconcile.Body.String())
	}
	if strings.Contains(reconcile.Body.String(), `"Action"`) || strings.Contains(reconcile.Body.String(), `"KeyName"`) {
		t.Fatalf("对账响应混入大写字段: %s", reconcile.Body.String())
	}
}
