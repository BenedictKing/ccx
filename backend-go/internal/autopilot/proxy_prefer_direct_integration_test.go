package autopilot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// 绑定流程携带代理设置时：profile 与新建渠道都必须持久化 proxyUrl/proxyPreferDirect。
// 代理地址故意指向不存在的服务并开启直连优先：直连成功即不触碰代理，
// 既验证持久化，也覆盖"代理不可用但直连优先可完成绑定"的端到端路径。
func TestHandleNewApiProvision_PersistsProxySettings(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	db := newTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	cfgManager := setupNewApiTestConfigManager(t)
	runner := NewAutoDiscoveryRunner(nil, nil)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager, Runner: runner})

	reqBody := NewApiProvisionRequest{
		SubscriptionUID:   "sub-newapi-proxy",
		DisplayName:       "地域封锁中转站",
		BaseURL:           site.URL,
		AccessToken:       "secret-proxy-token",
		ChannelKind:       "messages",
		ChannelName:       "newapi-proxy-channel",
		ProxyURL:          "http://127.0.0.1:1", // 故意不可达，靠直连优先绕过
		ProxyPreferDirect: true,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("期望 201, got %d, body=%s", w.Code, w.Body.String())
	}
	var resp NewApiProvisionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}

	// profile 持久化代理设置
	profile := store.Get("sub-newapi-proxy")
	if profile == nil {
		t.Fatal("profile 未创建")
	}
	if profile.ProxyURL != "http://127.0.0.1:1" || !profile.ProxyPreferDirect {
		t.Fatalf("profile 代理设置不匹配: proxyUrl=%q preferDirect=%v", profile.ProxyURL, profile.ProxyPreferDirect)
	}
	reloadedStore, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("重载订阅存储失败: %v", err)
	}
	persisted := reloadedStore.Get("sub-newapi-proxy")
	if persisted == nil || persisted.ProxyURL != "http://127.0.0.1:1" || !persisted.ProxyPreferDirect {
		t.Fatalf("重载后代理设置丢失: %+v", persisted)
	}

	// 新建渠道写入代理设置
	cfg := cfgManager.GetConfig()
	found := false
	for _, ch := range cfg.Upstream {
		if ch.ChannelUID == resp.ChannelUID {
			found = true
			if ch.ProxyURL != "http://127.0.0.1:1" || !ch.ProxyPreferDirect {
				t.Fatalf("渠道代理设置不匹配: proxyUrl=%q preferDirect=%v", ch.ProxyURL, ch.ProxyPreferDirect)
			}
		}
	}
	if !found {
		t.Fatal("未在 messages 上游列表中找到新建渠道")
	}
}

// auto-add 自定义路由携带 proxyPreferDirect 时应随渠道持久化。
func TestCustomAutoAddPersistsProxyPreferDirect(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "upstream": [],
  "chatUpstream": [],
  "responsesUpstream": [], "geminiUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	router := setupAutoManagedRouter(&AutoManagedDeps{
		CfgManager:           manager,
		SkipChannelKeyVerify: true,
	})
	body := `{"name":"relay","baseUrls":["https://relay.example.com"],"apiKeys":["sk-relay"],"proxyUrl":"http://127.0.0.1:7890","proxyPreferDirect":true,"routes":[{"channelKind":"chat"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat/channels/auto-add", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	cfg := manager.GetConfig()
	if len(cfg.ChatUpstream) != 1 {
		t.Fatalf("chat channels = %d, want 1", len(cfg.ChatUpstream))
	}
	got := cfg.ChatUpstream[0]
	if got.ProxyURL != "http://127.0.0.1:7890" || !got.ProxyPreferDirect {
		t.Fatalf("ProxyURL=%q ProxyPreferDirect=%v, want http://127.0.0.1:7890/true", got.ProxyURL, got.ProxyPreferDirect)
	}
}
