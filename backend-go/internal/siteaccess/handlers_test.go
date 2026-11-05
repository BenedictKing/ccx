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
