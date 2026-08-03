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
	"github.com/gin-gonic/gin"
)

// ── 测试辅助 ──

func setupNewApiRouter(t *testing.T, deps *NewApiRouteDeps) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterNewApiSubscriptionRoutes(r.Group("/api"), deps)
	return r
}

func setupNewApiTestConfigManager(t *testing.T) *config.ConfigManager {
	t.Helper()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	cfg := config.Config{}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("创建 ConfigManager 失败: %v", err)
	}
	return cfgManager
}

// mockNewApiSite 启动一个模拟 new-api 站点，支持 verify + list/create token 流程。
// tokens 用闭包状态模拟服务端持久化，便于测试 ProvisionKey 的查重/创建/回退逻辑。
// existingTokenKey 为空字符串时模拟"列表接口对已存在 key 做了脱敏、不回显明文"的上游行为
// （§8.5.1 设计文档中 key 列表 data.items[] 含 key 字段，但部分 fork 可能脱敏）。
func mockNewApiSite(t *testing.T, existingTokenName string, existingTokenKey string, createRespHasKey bool) *httptest.Server {
	return mockNewApiSiteWithGroups(t, existingTokenName, existingTokenKey, "default", createRespHasKey, map[string]NewApiGroupInfo{
		"default": {Desc: "默认", Ratio: 1.0},
	})
}

func mockNewApiSiteWithGroups(t *testing.T, existingTokenName string, existingTokenKey string, existingTokenGroup string, createRespHasKey bool, groups map[string]NewApiGroupInfo) *httptest.Server {
	t.Helper()
	var created []NewApiToken
	nextID := 100
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, NewApiUserSelf{ID: 7, Username: "bob", Quota: 50000, UsedQuota: 1000}, "")
	})
	mux.HandleFunc("/api/user/self/groups", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, groups, "")
	})
	mux.HandleFunc("/api/user/models", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, []string{"gpt-4o", "claude-3-5-sonnet"}, "")
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			items := []NewApiToken{}
			if existingTokenName != "" {
				items = append(items, NewApiToken{ID: 1, Key: existingTokenKey, Name: existingTokenName, Group: existingTokenGroup, Status: 1})
			}
			items = append(items, created...)
			writeEnvelope(w, true, newApiTokenListData{Items: items}, "")
		case http.MethodPost:
			var req NewApiCreateTokenRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			nextID++
			tok := NewApiToken{ID: nextID, Name: req.Name, Group: req.Group, Status: 1}
			if createRespHasKey {
				tok.Key = "sk-newly-created-key"
			}
			created = append(created, tok)
			writeEnvelope(w, true, tok, "")
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// ── handleNewApiVerify ──

func TestHandleNewApiVerify_Success(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store})

	body, _ := json.Marshal(NewApiVerifyRequest{
		BaseURL:     site.URL,
		AccessToken: "secret-token-value",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200, got %d, body=%s", w.Code, w.Body.String())
	}
	var resp NewApiVerifyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if resp.Username != "bob" || resp.UserID != 7 {
		t.Fatalf("用户信息不匹配: %+v", resp)
	}
	if resp.Groups["default"] != 1.0 {
		t.Fatalf("分组倍率不匹配: %+v", resp.Groups)
	}
	if len(resp.AvailableModels) != 2 {
		t.Fatalf("模型列表不匹配: %+v", resp.AvailableModels)
	}
	// AccessToken 绝不完整出响应
	if resp.AccessTokenMasked == "secret-token-value" {
		t.Fatal("AccessToken 未脱敏就出现在响应中")
	}
	if w.Body.String() == "" || bytesContains(w.Body.Bytes(), []byte("secret-token-value")) {
		t.Fatal("响应体中出现了完整 AccessToken 明文")
	}
}

func TestHandleNewApiVerify_ReportsGroupFetchError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, NewApiUserSelf{ID: 7, Username: "bob"}, "")
	})
	mux.HandleFunc("/api/user/self/groups", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, false, nil, "groups unavailable")
	})
	mux.HandleFunc("/api/user/models", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, []string{}, "")
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store})
	body, _ := json.Marshal(NewApiVerifyRequest{BaseURL: site.URL, AccessToken: "token"})
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200, got %d, body=%s", w.Code, w.Body.String())
	}
	var resp NewApiVerifyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if resp.GroupFetchError == "" {
		t.Fatalf("分组拉取失败应明确返回错误，got %+v", resp)
	}
}

func TestHandleNewApiVerify_InvalidToken(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, false, nil, "invalid token")
	})
	badSite := httptest.NewServer(mux)
	t.Cleanup(badSite.Close)
	_ = site

	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store})

	body, _ := json.Marshal(NewApiVerifyRequest{BaseURL: badSite.URL, AccessToken: "bad-token"})
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("期望 502, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestHandleNewApiVerify_MissingFields(t *testing.T) {
	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store})

	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/verify", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400, got %d, body=%s", w.Code, w.Body.String())
	}
}

// ── handleNewApiProvision ──

func TestHandleNewApiProvision_FullFlow_CreateNewKey(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	db := newTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	cfgManager := setupNewApiTestConfigManager(t)
	runner := NewAutoDiscoveryRunner(nil, nil) // profile store/hub 为 nil，只验证渠道创建路径
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager, Runner: runner})

	reqBody := NewApiProvisionRequest{
		SubscriptionUID: "sub-newapi-1",
		DisplayName:     "测试中转站",
		BaseURL:         site.URL,
		AccessToken:     "secret-provision-token",
		ChannelKind:     "messages",
		ChannelName:     "newapi-test-channel",
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
	if resp.ProvisionedKey != "sk-newly-created-key" {
		t.Fatalf("建 key 结果不匹配: %+v", resp)
	}
	if resp.Reused {
		t.Fatal("期望新建，但标记为 reused")
	}
	if resp.ChannelUID == "" {
		t.Fatal("channelUID 为空")
	}

	// profile 已落库，且 AccessToken 不在响应中完整出现
	profile := store.Get("sub-newapi-1")
	if profile == nil {
		t.Fatal("profile 未创建")
	}
	if profile.AccessToken != "secret-provision-token" {
		t.Fatalf("profile 持久化的 AccessToken 不匹配: got=%s", profile.AccessToken)
	}
	if profile.ProvisionGroup != "default" || profile.ProvisionGroupRatio == nil || *profile.ProvisionGroupRatio != 1 {
		t.Fatalf("profile 分组快照不匹配: %+v", profile)
	}
	if profile.MaxGroupMultiplier == nil || *profile.MaxGroupMultiplier != DefaultNewApiMaxGroupMultiplier {
		t.Fatalf("profile 分组倍率上限不匹配: %+v", profile.MaxGroupMultiplier)
	}
	reloadedStore, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("重载订阅存储失败: %v", err)
	}
	persisted := reloadedStore.Get("sub-newapi-1")
	if persisted == nil || persisted.MaxGroupMultiplier == nil || *persisted.MaxGroupMultiplier != DefaultNewApiMaxGroupMultiplier {
		t.Fatalf("重载后分组倍率上限丢失: %+v", persisted)
	}
	if bytesContains(w.Body.Bytes(), []byte("secret-provision-token")) {
		t.Fatal("响应体中出现了完整 AccessToken 明文")
	}

	// 渠道确实建到了 messages 上游列表
	cfg := cfgManager.GetConfig()
	found := false
	for _, ch := range cfg.Upstream {
		if ch.Name == "newapi-test-channel" {
			found = true
			if len(ch.APIKeys) != 1 || ch.APIKeys[0] != "sk-newly-created-key" {
				t.Fatalf("渠道 APIKeys 不匹配: %+v", ch.APIKeys)
			}
			if len(ch.APIKeyConfigs) != 1 || ch.APIKeyConfigs[0].QuotaGroup != "default" {
				t.Fatalf("渠道 Key 分组元数据不匹配: %+v", ch.APIKeyConfigs)
			}
			if ch.ChannelUID != resp.ChannelUID {
				t.Fatalf("渠道 UID 不匹配: cfg=%s resp=%s", ch.ChannelUID, resp.ChannelUID)
			}
		}
	}
	if !found {
		t.Fatal("未在 messages 上游列表中找到新建渠道")
	}
}

// 真实 new-api 站点建 key 响应的明文不带 "sk-" 前缀。回归：此前明文直接写入渠道，
// 调用 /v1/* 被上游以 401 Invalid token 拒绝，全部 key 被黑名单禁用、渠道挂起。
// provision 必须把明文规范为带 "sk-" 前缀的可调用形式。
func TestHandleNewApiProvision_UnprefixedKey_GetsSkPrefix(t *testing.T) {
	var created []NewApiToken
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, NewApiUserSelf{ID: 7, Username: "bob", Quota: 50000, UsedQuota: 1000}, "")
	})
	mux.HandleFunc("/api/user/self/groups", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, map[string]NewApiGroupInfo{"default": {Desc: "默认", Ratio: 1.0}}, "")
	})
	mux.HandleFunc("/api/user/models", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, []string{"gpt-4o"}, "")
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeEnvelope(w, true, newApiTokenListData{Items: created}, "")
		case http.MethodPost:
			var req NewApiCreateTokenRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			tok := NewApiToken{ID: 101, Name: req.Name, Group: req.Group, Status: 1, Key: "rawkey-no-prefix"}
			created = append(created, tok)
			writeEnvelope(w, true, tok, "")
		}
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	cfgManager := setupNewApiTestConfigManager(t)
	runner := NewAutoDiscoveryRunner(nil, nil)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager, Runner: runner})

	reqBody := NewApiProvisionRequest{
		SubscriptionUID: "sub-newapi-unprefixed",
		DisplayName:     "无前缀 key 站点",
		BaseURL:         site.URL,
		AccessToken:     "secret-provision-token",
		ChannelKind:     "messages",
		ChannelName:     "newapi-unprefixed-channel",
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
	if resp.ProvisionedKey != "sk-rawkey-no-prefix" {
		t.Fatalf("明文 key 未补齐 sk- 前缀: %q", resp.ProvisionedKey)
	}
	for _, ch := range cfgManager.GetConfig().Upstream {
		if ch.Name == "newapi-unprefixed-channel" {
			if len(ch.APIKeys) != 1 || ch.APIKeys[0] != "sk-rawkey-no-prefix" {
				t.Fatalf("渠道 APIKeys 未补齐 sk- 前缀: %+v", ch.APIKeys)
			}
			return
		}
	}
	t.Fatal("未找到新建渠道")
}

// 同站点已有渠道时，provision 应把 key 并入该渠道而非新建（多订阅/纯 key 整合为同一渠道）。
func TestHandleNewApiProvision_MergesIntoExistingChannel(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	cfgManager := setupNewApiTestConfigManager(t)
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		Name:          "existing-metapi",
		ChannelUID:    "ch_existing001",
		BaseURL:       site.URL,
		APIKeys:       []string{"sk-plain-old"},
		APIKeyConfigs: []config.APIKeyConfig{{Key: "sk-plain-old"}},
		ServiceType:   "claude",
		Status:        "active",
	}); err != nil {
		t.Fatalf("预置渠道失败: %v", err)
	}
	runner := NewAutoDiscoveryRunner(nil, nil)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager, Runner: runner})

	reqBody := NewApiProvisionRequest{
		SubscriptionUID: "sub-merge-1",
		DisplayName:     "同站点第二订阅",
		BaseURL:         site.URL,
		AccessToken:     "secret-provision-token",
		ChannelKind:     "messages",
		ChannelName:     "should-not-be-created",
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
	if !resp.MergedChannel {
		t.Fatalf("期望 mergedChannel=true: %+v", resp)
	}
	if resp.ChannelUID != "ch_existing001" || resp.ChannelName != "existing-metapi" || resp.ChannelIndex != 0 {
		t.Fatalf("合并目标信息不匹配: %+v", resp)
	}

	// 不新增渠道；纯 key 保留、新 key 追加且带分组元数据
	channels := cfgManager.GetConfig().Upstream
	if len(channels) != 1 {
		t.Fatalf("合并后渠道数应为 1: %+v", channels)
	}
	ch := channels[0]
	if len(ch.APIKeys) != 2 || ch.APIKeys[0] != "sk-plain-old" || ch.APIKeys[1] != "sk-newly-created-key" {
		t.Fatalf("合并后 APIKeys 不匹配: %+v", ch.APIKeys)
	}
	if len(ch.APIKeyConfigs) != 2 || ch.APIKeyConfigs[1].QuotaGroup != "default" || ch.APIKeyConfigs[1].Name != "new-api:default" {
		t.Fatalf("合并后 APIKeyConfigs 不匹配: %+v", ch.APIKeyConfigs)
	}
	if !ch.AutoManaged {
		t.Fatal("合并后渠道应为 autoManaged")
	}

	// 订阅链接到已有渠道
	profile := store.Get("sub-merge-1")
	if profile == nil {
		t.Fatal("profile 未创建")
	}
	linked := false
	for _, uid := range profile.LinkedChannelUIDs {
		if uid == "ch_existing001" {
			linked = true
		}
	}
	if !linked {
		t.Fatalf("订阅未链接到已有渠道: %+v", profile.LinkedChannelUIDs)
	}
}

// 合并时已有渠道已含相同 key（含 apiKeyConfigs 中的记录）则不重复追加。
func TestHandleNewApiProvision_MergeDeduplicatesKeys(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	cfgManager := setupNewApiTestConfigManager(t)
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		Name:          "existing-dup",
		ChannelUID:    "ch_dup001",
		BaseURL:       site.URL,
		APIKeys:       []string{"sk-newly-created-key"},
		APIKeyConfigs: []config.APIKeyConfig{{Key: "sk-newly-created-key"}},
		ServiceType:   "claude",
		Status:        "active",
	}); err != nil {
		t.Fatalf("预置渠道失败: %v", err)
	}
	runner := NewAutoDiscoveryRunner(nil, nil)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager, Runner: runner})

	reqBody := NewApiProvisionRequest{
		SubscriptionUID: "sub-merge-dup",
		DisplayName:     "重复 key 合并",
		BaseURL:         site.URL,
		AccessToken:     "secret-provision-token",
		ChannelKind:     "messages",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("期望 201, got %d, body=%s", w.Code, w.Body.String())
	}
	ch := cfgManager.GetConfig().Upstream[0]
	if len(ch.APIKeys) != 1 || ch.APIKeys[0] != "sk-newly-created-key" {
		t.Fatalf("相同 key 被重复追加: %+v", ch.APIKeys)
	}
	if len(ch.APIKeyConfigs) != 1 {
		t.Fatalf("相同 key 的配置被重复追加: %+v", ch.APIKeyConfigs)
	}
}

// 同站点多个渠道命中时优先合并进 active 渠道，跳过 suspended。
func TestHandleNewApiProvision_MergePrefersActiveChannel(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	cfgManager := setupNewApiTestConfigManager(t)
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		Name:        "suspended-ch",
		ChannelUID:  "ch_susp001",
		BaseURL:     site.URL,
		APIKeys:     []string{"sk-old-1"},
		ServiceType: "claude",
		Status:      "suspended",
	}); err != nil {
		t.Fatalf("预置挂起渠道失败: %v", err)
	}
	if err := cfgManager.AddUpstream(config.UpstreamConfig{
		Name:        "active-ch",
		ChannelUID:  "ch_active001",
		BaseURL:     site.URL,
		APIKeys:     []string{"sk-old-2"},
		ServiceType: "claude",
		Status:      "active",
	}); err != nil {
		t.Fatalf("预置活跃渠道失败: %v", err)
	}
	runner := NewAutoDiscoveryRunner(nil, nil)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager, Runner: runner})

	reqBody := NewApiProvisionRequest{
		SubscriptionUID: "sub-merge-active",
		DisplayName:     "优先合并活跃渠道",
		BaseURL:         site.URL,
		AccessToken:     "secret-provision-token",
		ChannelKind:     "messages",
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
	channels := cfgManager.GetConfig().Upstream
	suspIdx, actIdx := -1, -1
	for i, ch := range channels {
		switch ch.Name {
		case "suspended-ch":
			suspIdx = i
		case "active-ch":
			actIdx = i
		}
	}
	if suspIdx < 0 || actIdx < 0 {
		t.Fatalf("预置渠道丢失: %+v", channels)
	}
	if resp.ChannelUID != "ch_active001" || resp.ChannelIndex != actIdx {
		t.Fatalf("应合并进 active 渠道: %+v", resp)
	}
	if len(channels[suspIdx].APIKeys) != 1 || channels[suspIdx].APIKeys[0] != "sk-old-1" {
		t.Fatalf("suspended 渠道不应被改动: %+v", channels[suspIdx].APIKeys)
	}
	if len(channels[actIdx].APIKeys) != 2 || channels[actIdx].APIKeys[1] != "sk-newly-created-key" {
		t.Fatalf("active 渠道合并结果不匹配: %+v", channels[actIdx].APIKeys)
	}
}

func TestHandleNewApiProvision_AutoCreatesOnlyEligibleGroupKeys(t *testing.T) {
	var created []NewApiToken
	var createdGroups []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, NewApiUserSelf{ID: 7, Username: "bob", Quota: 50000}, "")
	})
	mux.HandleFunc("/api/user/self/groups", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, map[string]NewApiGroupInfo{
			"default":  {Desc: "默认", Ratio: 1},
			"discount": {Desc: "优惠", Ratio: 0.5},
			"premium":  {Desc: "高倍率", Ratio: 2},
		}, "")
	})
	mux.HandleFunc("/api/user/models", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, []string{"gpt-5.6"}, "")
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeEnvelope(w, true, newApiTokenListData{Items: created}, "")
		case http.MethodPost:
			var createReq NewApiCreateTokenRequest
			if err := json.NewDecoder(r.Body).Decode(&createReq); err != nil {
				t.Fatalf("解析建 key 请求失败: %v", err)
			}
			createdGroups = append(createdGroups, createReq.Group)
			token := NewApiToken{
				ID:     len(created) + 1,
				Key:    "sk-" + createReq.Group,
				Name:   createReq.Name,
				Group:  createReq.Group,
				Status: 1,
			}
			created = append(created, token)
			writeEnvelope(w, true, token, "")
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	cfgManager := setupNewApiTestConfigManager(t)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager})
	limit := 1.0
	body, _ := json.Marshal(NewApiProvisionRequest{
		SubscriptionUID:            "sub-auto-groups",
		DisplayName:                "自动安全分组",
		BaseURL:                    site.URL,
		AccessToken:                "token",
		ChannelKind:                "messages",
		ChannelName:                "newapi-auto-safe-groups",
		ProvisionAllEligibleGroups: true,
		MaxGroupMultiplier:         &limit,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("期望 201, got %d, body=%s", w.Code, w.Body.String())
	}
	if len(createdGroups) != 2 || createdGroups[0] != "discount" || createdGroups[1] != "default" {
		t.Fatalf("只应为阈值内分组建 key，got %v", createdGroups)
	}
	var resp NewApiProvisionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if len(resp.ProvisionedKeys) != 2 || resp.ProvisionedKeys[0].Group != "discount" || resp.ProvisionedKeys[1].Group != "default" {
		t.Fatalf("响应分组 key 元数据不匹配: %+v", resp.ProvisionedKeys)
	}
	if bytesContains(w.Body.Bytes(), []byte("sk-discount")) || bytesContains(w.Body.Bytes(), []byte("sk-default")) {
		t.Fatal("批量接入响应不应回传任何分组 key 明文")
	}

	profile := store.Get("sub-auto-groups")
	if profile == nil || len(profile.ProvisionedKeys) != 2 || profile.MaxGroupMultiplier == nil || *profile.MaxGroupMultiplier != limit {
		t.Fatalf("订阅未持久化完整的分组安全策略: %+v", profile)
	}
	cfg := cfgManager.GetConfig()
	if len(cfg.Upstream) != 1 || len(cfg.Upstream[0].APIKeys) != 2 || len(cfg.Upstream[0].APIKeyConfigs) != 2 {
		t.Fatalf("渠道未绑定全部合格分组 key: %+v", cfg.Upstream)
	}
	for _, keyConfig := range cfg.Upstream[0].APIKeyConfigs {
		if keyConfig.QuotaGroup == "premium" || keyConfig.GroupMultiplier == nil || keyConfig.MaxGroupMultiplier == nil || *keyConfig.GroupMultiplier > *keyConfig.MaxGroupMultiplier {
			t.Fatalf("渠道包含超限或不受保护的 key 配置: %+v", keyConfig)
		}
	}
}

func TestHandleNewApiProvision_ChannelNameConflictPreventsRemoteKeyCreation(t *testing.T) {
	postCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, NewApiUserSelf{ID: 7}, "")
	})
	mux.HandleFunc("/api/user/self/groups", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, map[string]NewApiGroupInfo{"default": {Ratio: 1}}, "")
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postCalls++
		}
		writeEnvelope(w, true, newApiTokenListData{}, "")
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	cfgManager := setupNewApiTestConfigManager(t)
	if err := cfgManager.AddUpstream(config.UpstreamConfig{Name: "already-exists", BaseURL: "https://example.com", APIKeys: []string{"sk-existing"}, ServiceType: "claude"}); err != nil {
		t.Fatalf("预置渠道失败: %v", err)
	}
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager})
	requestBody, _ := json.Marshal(NewApiProvisionRequest{
		SubscriptionUID:            "sub-channel-conflict",
		DisplayName:                "冲突渠道",
		BaseURL:                    site.URL,
		AccessToken:                "token",
		ChannelKind:                "messages",
		ChannelName:                "already-exists",
		ProvisionAllEligibleGroups: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("期望 409, got %d, body=%s", w.Code, w.Body.String())
	}
	if postCalls != 0 || store.Get("sub-channel-conflict") != nil {
		t.Fatalf("渠道名冲突不得创建远端 key 或订阅: postCalls=%d", postCalls)
	}
}

func TestHandleNewApiProvision_SecondGroupFailureRollsBackCreatedKey(t *testing.T) {
	postCalls := 0
	var deleted []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, NewApiUserSelf{ID: 7}, "")
	})
	mux.HandleFunc("/api/user/self/groups", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, map[string]NewApiGroupInfo{
			"first":  {Ratio: 0.5},
			"second": {Ratio: 1},
		}, "")
	})
	mux.HandleFunc("/api/user/models", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, []string{}, "")
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeEnvelope(w, true, newApiTokenListData{}, "")
		case http.MethodPost:
			postCalls++
			if postCalls == 1 {
				var createReq NewApiCreateTokenRequest
				if err := json.NewDecoder(r.Body).Decode(&createReq); err != nil {
					t.Fatalf("解析建 key 请求失败: %v", err)
				}
				writeEnvelope(w, true, NewApiToken{ID: 42, Key: "sk-first", Name: createReq.Name, Group: createReq.Group}, "")
				return
			}
			writeEnvelope(w, false, nil, "second group failed")
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/token/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		deleted = append(deleted, r.URL.Path)
		writeEnvelope(w, true, nil, "")
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	cfgManager := setupNewApiTestConfigManager(t)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager})
	limit := 1.0
	requestBody, _ := json.Marshal(NewApiProvisionRequest{
		SubscriptionUID:            "sub-rollback",
		DisplayName:                "失败回收",
		BaseURL:                    site.URL,
		AccessToken:                "token",
		ChannelKind:                "messages",
		ProvisionAllEligibleGroups: true,
		MaxGroupMultiplier:         &limit,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("期望 502, got %d, body=%s", w.Code, w.Body.String())
	}
	if postCalls != 2 || len(deleted) != 1 || deleted[0] != "/api/token/42" {
		t.Fatalf("分组创建失败后应回收第一把新建 key: post=%d deleted=%v", postCalls, deleted)
	}
	if store.Get("sub-rollback") != nil || len(cfgManager.GetConfig().Upstream) != 0 {
		t.Fatal("批量创建失败不应保留订阅或渠道")
	}
}

func TestHandleNewApiProvision_RejectsGroupAboveMultiplierLimit(t *testing.T) {
	postCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, NewApiUserSelf{ID: 7, Username: "bob"}, "")
	})
	mux.HandleFunc("/api/user/self/groups", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, map[string]NewApiGroupInfo{
			"default": {Desc: "正常", Ratio: 1},
			"premium": {Desc: "高倍率", Ratio: 3},
		}, "")
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postCalls++
		}
		writeEnvelope(w, true, newApiTokenListData{}, "")
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	cfgManager := setupNewApiTestConfigManager(t)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager})
	limit := 1.0
	body, _ := json.Marshal(NewApiProvisionRequest{
		SubscriptionUID:    "sub-high-group",
		DisplayName:        "高倍率分组",
		BaseURL:            site.URL,
		AccessToken:        "token",
		ChannelKind:        "messages",
		ProvisionGroup:     "premium",
		MaxGroupMultiplier: &limit,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("期望 422, got %d, body=%s", w.Code, w.Body.String())
	}
	if postCalls != 0 {
		t.Fatalf("高倍率分组必须在建 key 前拦截，POST /api/token/ 调用次数=%d", postCalls)
	}
	if store.Get("sub-high-group") != nil || len(cfgManager.GetConfig().Upstream) != 0 {
		t.Fatal("高倍率分组被拒绝后不应创建订阅或渠道")
	}
}

func TestHandleNewApiProvision_GroupLookupFailureBlocksProvision(t *testing.T) {
	postCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, NewApiUserSelf{ID: 7, Username: "bob"}, "")
	})
	mux.HandleFunc("/api/user/self/groups", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, false, nil, "groups unavailable")
	})
	mux.HandleFunc("/api/token/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postCalls++
		}
		writeEnvelope(w, true, newApiTokenListData{}, "")
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	cfgManager := setupNewApiTestConfigManager(t)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager})
	body, _ := json.Marshal(NewApiProvisionRequest{
		SubscriptionUID: "sub-no-groups",
		DisplayName:     "无分组信息",
		BaseURL:         site.URL,
		AccessToken:     "token",
		ChannelKind:     "messages",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("期望 502, got %d, body=%s", w.Code, w.Body.String())
	}
	if postCalls != 0 || store.Get("sub-no-groups") != nil || len(cfgManager.GetConfig().Upstream) != 0 {
		t.Fatal("无法读取分组时不得创建或绑定 key")
	}
}

func TestHandleNewApiProvision_ReuseExistingKey_Succeeds(t *testing.T) {
	// 站点 key 列表按 §8.5.1 设计返回明文 key（data.items[].key），复用同名 key 时应直接成功建渠道。
	site := mockNewApiSite(t, defaultNewApiProvisionKeyNameForGroup("default"), "sk-existing-key", true)
	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	cfgManager := setupNewApiTestConfigManager(t)
	runner := NewAutoDiscoveryRunner(nil, nil)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager, Runner: runner})

	reqBody := NewApiProvisionRequest{
		SubscriptionUID: "sub-newapi-2",
		DisplayName:     "测试中转站2",
		BaseURL:         site.URL,
		AccessToken:     "secret-token-2",
		ChannelKind:     "messages",
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
	if !resp.Reused {
		t.Fatal("期望复用已存在 key，但标记为新建")
	}
	if resp.ProvisionedKey != "sk-existing-key" {
		t.Fatalf("复用 key 不匹配: %+v", resp)
	}
	if store.Get("sub-newapi-2") == nil {
		t.Fatal("复用成功后应创建 profile")
	}
}

func TestHandleNewApiProvision_ExistingKeyInDifferentGroupReturnsConflict(t *testing.T) {
	site := mockNewApiSiteWithGroups(
		t,
		defaultNewApiProvisionKeyNameForGroup("default"),
		"sk-existing-key",
		"premium",
		true,
		map[string]NewApiGroupInfo{
			"default": {Ratio: 1},
			"premium": {Ratio: 2},
		},
	)
	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	cfgManager := setupNewApiTestConfigManager(t)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager})
	body, _ := json.Marshal(NewApiProvisionRequest{
		SubscriptionUID: "sub-group-mismatch",
		DisplayName:     "分组冲突",
		BaseURL:         site.URL,
		AccessToken:     "token",
		ChannelKind:     "messages",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("期望 409, got %d, body=%s", w.Code, w.Body.String())
	}
	if store.Get("sub-group-mismatch") != nil || len(cfgManager.GetConfig().Upstream) != 0 {
		t.Fatal("分组冲突不得创建订阅或渠道")
	}
}

func TestHandleNewApiProvision_ReuseExistingKey_MaskedKey_ReturnsConflict(t *testing.T) {
	// 部分 fork 的 key 列表接口不回显明文 key（脱敏/空字符串），此时无法拿到可用 key，应返回 409 让用户手动处理。
	site := mockNewApiSite(t, defaultNewApiProvisionKeyNameForGroup("default"), "", true)
	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	cfgManager := setupNewApiTestConfigManager(t)
	runner := NewAutoDiscoveryRunner(nil, nil)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager, Runner: runner})

	reqBody := NewApiProvisionRequest{
		SubscriptionUID: "sub-newapi-2",
		DisplayName:     "测试中转站2",
		BaseURL:         site.URL,
		AccessToken:     "secret-token-2",
		ChannelKind:     "messages",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("期望 409, got %d, body=%s", w.Code, w.Body.String())
	}
	// profile 不应残留
	if store.Get("sub-newapi-2") != nil {
		t.Fatal("建 key 失败后不应创建 profile")
	}
}

func TestHandleNewApiProvision_DuplicateSubscriptionUID_Rejected(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	cfgManager := setupNewApiTestConfigManager(t)
	runner := NewAutoDiscoveryRunner(nil, nil)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager, Runner: runner})

	existing := &SubscriptionProfile{
		SubscriptionUID: "sub-dup",
		DisplayName:     "已存在",
		Provider:        "manual",
	}
	if err := store.Create(existing); err != nil {
		t.Fatalf("预置 profile 失败: %v", err)
	}

	reqBody := NewApiProvisionRequest{
		SubscriptionUID: "sub-dup",
		DisplayName:     "重复",
		BaseURL:         site.URL,
		AccessToken:     "tok",
		ChannelKind:     "messages",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("期望 409, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestHandleNewApiProvision_InvalidChannelKind(t *testing.T) {
	site := mockNewApiSite(t, "", "", true)
	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	cfgManager := setupNewApiTestConfigManager(t)
	runner := NewAutoDiscoveryRunner(nil, nil)
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: cfgManager, Runner: runner})

	reqBody := NewApiProvisionRequest{
		SubscriptionUID: "sub-bad-kind",
		DisplayName:     "非法渠道类型",
		BaseURL:         site.URL,
		AccessToken:     "tok",
		ChannelKind:     "not-a-real-kind",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("期望 400, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestHandleNewApiProvision_MissingCfgManager(t *testing.T) {
	store, _ := NewSubscriptionStoreWithDB(newTestDB(t))
	router := setupNewApiRouter(t, &NewApiRouteDeps{Store: store, CfgManager: nil})

	reqBody := NewApiProvisionRequest{
		SubscriptionUID: "sub-no-cfg",
		DisplayName:     "无配置管理器",
		BaseURL:         "https://example.com",
		AccessToken:     "tok",
		ChannelKind:     "messages",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/subscriptions/newapi/provision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("期望 500, got %d, body=%s", w.Code, w.Body.String())
	}
}

// bytesContains 是 bytes.Contains 的语义化包装，方便断言"响应体不应包含明文令牌"。
func bytesContains(haystack, needle []byte) bool {
	return bytes.Contains(haystack, needle)
}
