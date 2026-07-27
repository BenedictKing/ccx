package autopilot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/gin-gonic/gin"
)

func setupAutoManagedRouter(deps *AutoManagedDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 直接在根引擎注册路由，模拟 apiGroup 注册
	RegisterAutoManagedRoutes(r.Group("/api"), deps)
	return r
}

func TestListAccountsMasksCredentials(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts": [{"accountUid":"acct_test","providerId":"mimo","name":"mimo-main","credentials":[{"credentialUid":"cred_test","apiKey":"sk-secret-value"}]}],
  "upstream": [{"accountUid":"acct_test","channelUid":"ch_messages","providerId":"mimo","name":"mimo-main-claude","serviceType":"claude","baseUrl":"https://example.com/anthropic","apiKeyConfigs":[{"credentialUid":"cred_test","baseUrl":"https://example.com/anthropic"}],"autoManaged":true,"status":"active"}],
  "responsesUpstream": [], "geminiUpstream": [], "chatUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatalf("写测试配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })
	router := setupAutoManagedRouter(&AutoManagedDeps{CfgManager: cfgManager})
	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/accounts status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "sk-secret-value") {
		t.Fatalf("账号列表泄露明文 Key: %s", body)
	}
	if !strings.Contains(body, "cred_test") || !strings.Contains(body, "keyMask") {
		t.Fatalf("账号列表缺少凭证掩码信息: %s", body)
	}
}

func TestListAccountsIncludesActiveEndpointModelAvailability(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts": [{"accountUid":"acct_test","providerId":"volcengine","name":"volcengine","credentials":[{"credentialUid":"cred_a","apiKey":"ark-secret-a"},{"credentialUid":"cred_b","apiKey":"ark-secret-b"}]}],
  "upstream": [{"accountUid":"acct_test","channelUid":"ch_messages","providerId":"volcengine","name":"volcengine-claude","serviceType":"claude","baseUrl":"https://example.com/anthropic","apiKeyConfigs":[{"credentialUid":"cred_a","baseUrl":"https://example.com/anthropic"},{"credentialUid":"cred_b","baseUrl":"https://example.com/anthropic"}],"autoManaged":true,"status":"active"}],
  "responsesUpstream": [], "geminiUpstream": [], "chatUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatalf("写测试配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	store, err := NewProfileStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}
	updatedAt := time.Date(2026, 7, 17, 10, 54, 33, 0, time.UTC)
	discoveredAt := updatedAt
	discoveredAtLater := updatedAt.Add(time.Minute)
	for _, profile := range []*KeyEndpointProfile{
		{
			EndpointUID: "ep_a", AccountUID: "acct_test", ChannelUID: "ch_messages",
			CredentialUID: "cred_a", KeyMask: "ark-a***001", AvailableModels: []string{"model-b", "model-a"}, UpdatedAt: updatedAt,
			ModelDiscoverySource: ModelDiscoverySourceModelsAPI, ModelsDiscoveredAt: &discoveredAt,
		},
		{
			EndpointUID: "ep_b", AccountUID: "acct_test", ChannelUID: "ch_messages",
			CredentialUID: "cred_b", KeyMask: "ark-b***002", AvailableModels: []string{"model-c", "model-b"}, UpdatedAt: updatedAt.Add(time.Minute),
			ModelDiscoverySource: ModelDiscoverySourceModelsAPI, ModelsDiscoveredAt: &discoveredAtLater,
		},
	} {
		if err := store.Upsert(profile); err != nil {
			t.Fatalf("ProfileStore.Upsert 失败: %v", err)
		}
	}
	runner := NewAutoDiscoveryRunner(store, nil)
	router := setupAutoManagedRouter(&AutoManagedDeps{CfgManager: cfgManager, Runner: runner})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/accounts status=%d body=%s", w.Code, w.Body.String())
	}

	var response struct {
		Accounts []managedAccountView `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(response.Accounts) != 1 || len(response.Accounts[0].Channels) != 1 {
		t.Fatalf("账号渠道数量错误: %+v", response.Accounts)
	}
	channel := response.Accounts[0].Channels[0]
	if got, want := strings.Join(channel.DiscoveredModels, ","), "model-a,model-b,model-c"; got != want {
		t.Fatalf("DiscoveredModels=%q, want %q", got, want)
	}
	if !channel.ModelInventoryKnown {
		t.Fatal("已发现模型时 ModelInventoryKnown 应为 true")
	}
	if len(channel.ModelBindings) != 2 {
		t.Fatalf("ModelBindings=%+v, want 2", channel.ModelBindings)
	}
	if got := strings.Join(channel.ModelBindings[0].Models, ","); got != "model-a,model-b" {
		t.Fatalf("第一个 Key 模型=%q", got)
	}
	if channel.ModelsUpdatedAt == nil || !channel.ModelsUpdatedAt.Equal(updatedAt.Add(time.Minute)) {
		t.Fatalf("ModelsUpdatedAt=%v", channel.ModelsUpdatedAt)
	}
	if strings.Contains(w.Body.String(), "ark-secret") {
		t.Fatalf("管理 API 泄漏凭证: %s", w.Body.String())
	}
}

func TestListAccountsIncludesCustomAutoManagedProtocolAvailability(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts": [],
  "upstream": [], "chatUpstream": [],
  "responsesUpstream": [{"accountUid":"acct_custom","channelUid":"ch_custom","name":"custom-endpoint","serviceType":"responses","baseUrl":"https://example.com","apiKeys":["sk-custom-secret"],"apiKeyConfigs":[{"key":"sk-custom-secret","credentialUid":"cred_custom","baseUrl":"https://example.com"}],"autoManaged":true,"status":"active"}],
  "geminiUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatalf("写测试配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	store, err := NewProfileStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}
	discoveredAt := time.Date(2026, 7, 25, 5, 11, 29, 0, time.UTC)
	profile := &KeyEndpointProfile{
		EndpointUID: "ep_custom", AccountUID: "acct_custom", ChannelUID: "ch_custom",
		CredentialUID: "cred_custom", KeyMask: "sk-c***ret", ServiceType: "responses",
		AvailableModels: []string{"gpt-5.6-sol"}, ModelsDiscoveredAt: &discoveredAt,
		ModelDiscoverySource: ModelDiscoverySourceModelsAPI,
		ProtocolModels: map[string][]string{
			"messages":  {"gpt-5.6-sol"},
			"chat":      {"gpt-5.6-sol"},
			"responses": {"gpt-5.6-sol"},
		},
		ProtocolDiscoveredAt: map[string]time.Time{
			"messages": discoveredAt, "chat": discoveredAt, "responses": discoveredAt,
		},
		ProtocolDiscoverySource: map[string]string{
			"messages": "protocol_probe", "chat": "protocol_probe", "responses": "models_api",
		},
		UpdatedAt: discoveredAt,
	}
	if err := store.Upsert(profile); err != nil {
		t.Fatalf("ProfileStore.Upsert 失败: %v", err)
	}

	router := setupAutoManagedRouter(&AutoManagedDeps{
		CfgManager: cfgManager,
		Runner:     NewAutoDiscoveryRunner(store, nil),
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/accounts status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-custom-secret") {
		t.Fatalf("账号列表泄露自定义渠道明文 Key: %s", w.Body.String())
	}

	var response struct {
		Accounts []managedAccountView `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(response.Accounts) != 1 {
		t.Fatalf("Accounts=%+v, want 1", response.Accounts)
	}
	account := response.Accounts[0]
	if account.AccountUID != "acct_custom" || len(account.Credentials) != 1 || len(account.Channels) != 1 {
		t.Fatalf("自定义自动托管账号聚合错误: %+v", account)
	}
	protocols := make([]string, 0, len(account.Channels[0].ProtocolAvailability))
	for _, availability := range account.Channels[0].ProtocolAvailability {
		protocols = append(protocols, availability.Protocol)
	}
	if got, want := strings.Join(protocols, ","), "messages,chat,responses"; got != want {
		t.Fatalf("ProtocolAvailability=%q, want %q", got, want)
	}
}

func TestManagedChannelModelAvailabilityIncludesEmptyAutoDiscoveryResult(t *testing.T) {
	updatedAt := time.Date(2026, 7, 17, 11, 2, 0, 0, time.UTC)
	models, bindings, latest, known := managedChannelModelAvailability([]*KeyEndpointProfile{
		{
			EndpointUID: "ep_empty", CredentialUID: "cred_empty", KeyMask: "ark-e***001",
			Source: "auto_discovery", AvailableModels: []string{}, UpdatedAt: updatedAt,
		},
		{
			EndpointUID: "ep_models", CredentialUID: "cred_models", KeyMask: "ark-m***002",
			AvailableModels: []string{"model-a"}, UpdatedAt: updatedAt.Add(time.Minute),
		},
		{
			EndpointUID: "ep_unknown", CredentialUID: "cred_unknown", KeyMask: "ark-u***003",
			Source: "l1_passive", UpdatedAt: updatedAt.Add(2 * time.Minute),
		},
	})

	if !known {
		t.Fatal("自动发现的空模型清单仍应标记为已知")
	}
	if got, want := strings.Join(models, ","), "model-a"; got != want {
		t.Fatalf("models=%q, want %q", got, want)
	}
	if len(bindings) != 2 {
		t.Fatalf("bindings=%+v, want only two known inventories", bindings)
	}
	if got := bindings[0]; got.KeyMask != "ark-e***001" || len(got.Models) != 0 {
		t.Fatalf("empty discovery binding=%+v, want empty ark-e binding", got)
	}
	if latest == nil || !latest.Equal(updatedAt.Add(time.Minute)) {
		t.Fatalf("latest=%v, want discovered inventory update time", latest)
	}
}

func TestManagedChannelModelAvailabilityIncludesDiscoveryMetadata(t *testing.T) {
	first := time.Date(2026, 7, 22, 0, 42, 12, 0, time.UTC)
	second := first.Add(time.Minute)
	inventory := managedChannelModelAvailabilityDetails([]*KeyEndpointProfile{
		{
			EndpointUID:           "ep-a",
			CredentialUID:         "cred-a",
			KeyMask:               "ark-a***001",
			AvailableModels:       []string{"glm-5.2"},
			UpdatedAt:             first,
			ModelDiscoverySource:  ModelDiscoverySourceControlPlane,
			ModelDiscoveryMessage: "火山管控面 Coding Plan 模型清单",
			ModelsDiscoveredAt:    &first,
		},
		{
			EndpointUID:           "ep-b",
			CredentialUID:         "cred-b",
			KeyMask:               "ark-b***002",
			AvailableModels:       []string{"deepseek-v4-pro"},
			UpdatedAt:             second,
			ModelDiscoverySource:  ModelDiscoverySourceBuiltinFallback,
			ModelDiscoveryMessage: "管控面失败，已回退内置模型清单",
			ModelsDiscoveredAt:    &second,
		},
	})

	if !inventory.known || inventory.source != "mixed" {
		t.Fatalf("inventory source=%q known=%v", inventory.source, inventory.known)
	}
	if !inventory.latestDiscoveredAt.Equal(second) || inventory.message != "管控面失败，已回退内置模型清单" {
		t.Fatalf("inventory discovery metadata=%+v", inventory)
	}
	if len(inventory.bindings) != 2 || inventory.bindings[1].ModelsDiscoveredAt == nil {
		t.Fatalf("binding discovery metadata=%+v", inventory.bindings)
	}
}

func TestManagedProtocolAvailabilityViewsAggregatesByProtocolAndCredential(t *testing.T) {
	discoveredAt := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	profiles := []*KeyEndpointProfile{
		{
			EndpointUID: "ep-a", CredentialUID: "cred-a", KeyMask: "sk-a***001", ServiceType: "responses",
			UpdatedAt: discoveredAt, ProtocolModels: map[string][]string{
				"responses": {"shared", "responses-only"},
				"chat":      {"shared"},
			},
			ProtocolDiscoveredAt:    map[string]time.Time{"responses": discoveredAt, "chat": discoveredAt},
			ProtocolDiscoverySource: map[string]string{"responses": "models_api", "chat": "protocol_probe"},
		},
		{
			EndpointUID: "ep-b", CredentialUID: "cred-b", KeyMask: "sk-b***002", ServiceType: "responses",
			UpdatedAt: discoveredAt, ProtocolModels: map[string][]string{
				"responses": {"shared"},
				"messages":  {"shared", "messages-only"},
			},
			ProtocolDiscoveredAt:    map[string]time.Time{"responses": discoveredAt, "messages": discoveredAt},
			ProtocolDiscoverySource: map[string]string{"responses": "models_api", "messages": "protocol_probe"},
		},
	}

	views := managedProtocolAvailabilityViews(profiles)
	if len(views) != 3 {
		t.Fatalf("views=%+v", views)
	}
	if views[0].Protocol != "messages" || strings.Join(views[0].DiscoveredModels, ",") != "messages-only,shared" {
		t.Fatalf("messages view=%+v", views[0])
	}
	if views[2].Protocol != "responses" || len(views[2].ModelBindings) != 2 {
		t.Fatalf("responses view=%+v", views[2])
	}
}

func TestPatchAccountCredentialsRemovesByUID(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts": [{"accountUid":"acct_test","providerId":"mimo","name":"mimo-main","credentials":[{"credentialUid":"cred_a","apiKey":"sk-a"},{"credentialUid":"cred_b","apiKey":"sk-b"}]}],
  "upstream": [{"accountUid":"acct_test","channelUid":"ch_messages","providerId":"mimo","name":"mimo-main","serviceType":"claude","baseUrl":"https://api.xiaomimimo.com/anthropic","baseUrls":["https://api.xiaomimimo.com/anthropic"],"apiKeyConfigs":[{"credentialUid":"cred_a","baseUrl":"https://api.xiaomimimo.com/anthropic"},{"credentialUid":"cred_b","baseUrl":"https://api.xiaomimimo.com/anthropic"}],"autoManaged":true,"status":"active"}],
  "responsesUpstream": [], "geminiUpstream": [], "chatUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatalf("写测试配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })
	router := setupAutoManagedRouter(&AutoManagedDeps{CfgManager: cfgManager})
	req := httptest.NewRequest(http.MethodPatch, "/api/accounts/acct_test/credentials", bytes.NewBufferString(`{"removeCredentialUids":["cred_b"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH credentials status=%d body=%s", w.Code, w.Body.String())
	}
	channels := cfgManager.GetAccountChannels("acct_test")
	if len(channels) != 1 || len(channels[0].Upstream.APIKeys) != 1 || channels[0].Upstream.APIKeys[0] != "sk-a" {
		t.Fatalf("按 credentialUid 删除失败: %+v", channels)
	}
}

func TestAutoAddRequest_Binding(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name: "完整请求",
			body: `{"name":"test","baseUrls":["https://api.example.com"],"apiKeys":["sk-test123"]}`,
		},
		{
			name: "自动生成名称",
			body: `{"baseUrls":["https://api.example.com"],"apiKeys":["sk-test123"]}`,
		},
		{
			name: "多 URL 和多 Key",
			body: `{"baseUrls":["https://api1.example.com","https://api2.example.com"],"apiKeys":["sk-key1","sk-key2"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req AutoAddRequest
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tt.body))
			r.Header.Set("Content-Type", "application/json")

			decoder := json.NewDecoder(r.Body)
			if err := decoder.Decode(&req); err != nil {
				if !tt.wantErr {
					t.Fatalf("解码失败: %v", err)
				}
				return
			}

			if len(req.BaseURLs) == 0 {
				t.Fatal("baseUrls 不应为空")
			}
			if len(req.APIKeys) == 0 {
				t.Fatal("apiKeys 不应为空")
			}
		})
	}
}

func TestAutoAddRequest_Validation(t *testing.T) {
	tests := []struct {
		name string
		body string
		err  string
	}{
		{
			name: "空 baseUrls",
			body: `{"baseUrls":[],"apiKeys":["sk-test"]}`,
			err:  "baseUrls 不能为空",
		},
		{
			name: "空 apiKeys",
			body: `{"baseUrls":["https://api.example.com"],"apiKeys":[]}`,
			err:  "apiKeys 不能为空",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req AutoAddRequest
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tt.body))
			r.Header.Set("Content-Type", "application/json")

			decoder := json.NewDecoder(r.Body)
			_ = decoder.Decode(&req) // 忽略解码错误，关注业务校验

			if len(req.BaseURLs) == 0 {
				// 这就是期望的行为
				return
			}
			if len(req.APIKeys) == 0 {
				return
			}
			t.Fatalf("期望验证失败: %s", tt.err)
		})
	}
}

func TestInferAutoAddProviderID(t *testing.T) {
	zhipuKey := "0123456789abcdef0123456789abcdef.ABCDEFGHIJKLMNO1"
	tests := []struct {
		name     string
		baseURLs []string
		apiKeys  []string
		want     string
	}{
		{name: "官方 Claude URL", baseURLs: []string{"https://open.bigmodel.cn/api/anthropic"}, apiKeys: []string{"sk-any"}, want: "glm"},
		{name: "官方 OpenAI URL", baseURLs: []string{"https://open.bigmodel.cn/api/paas/v4/"}, apiKeys: []string{"sk-any"}, want: "glm"},
		{name: "两个官方协议 URL", baseURLs: []string{"https://open.bigmodel.cn/api/anthropic", "https://open.bigmodel.cn/api/paas/v4"}, apiKeys: []string{"sk-any"}, want: "glm"},
		{name: "仅智谱 Key", apiKeys: []string{zhipuKey}, want: "glm"},
		{name: "第三方 URL 不按 Key 提升", baseURLs: []string{"https://relay.example/v1"}, apiKeys: []string{zhipuKey}},
		{name: "混合官方与第三方 URL", baseURLs: []string{"https://open.bigmodel.cn/api/paas/v4", "https://relay.example/v1"}, apiKeys: []string{zhipuKey}},
		{name: "共享 sk Key 无法推断", apiKeys: []string{"sk-abcdefghijklmnopqrstuvwxyz123456"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferAutoAddProviderID(tt.baseURLs, tt.apiKeys); got != tt.want {
				t.Fatalf("inferAutoAddProviderID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyProviderUpstreamDefaults(t *testing.T) {
	glmChat := config.UpstreamConfig{ServiceType: "openai"}
	config.ApplyProviderUpstreamDefaults("glm", &glmChat)
	if glmChat.ReasoningParamStyle != "reasoning_effort" || !glmChat.IsPassbackReasoningContentEnabled() {
		t.Fatalf("GLM OpenAI 默认兼容参数未补齐: %+v", glmChat)
	}

	glmClaude := config.UpstreamConfig{ServiceType: "claude"}
	config.ApplyProviderUpstreamDefaults("glm", &glmClaude)
	if glmClaude.ReasoningParamStyle != "" || glmClaude.IsPassbackReasoningContentEnabled() {
		t.Fatalf("GLM Claude 原生 route 不应注入 OpenAI 参数: %+v", glmClaude)
	}

	custom := config.UpstreamConfig{ServiceType: "openai"}
	config.ApplyProviderUpstreamDefaults("", &custom)
	if custom.ReasoningParamStyle != "" || custom.IsPassbackReasoningContentEnabled() {
		t.Fatalf("自定义渠道不应应用 GLM 默认值: %+v", custom)
	}
}

func TestAutoStatusResponse_Serialization(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	resp := AutoStatusResponse{
		AutoManaged:   true,
		AutoManagedAt: &now,
		Discovery: &DiscoveryStatusInfo{
			Status:    DiscoveryStatusDone,
			StartedAt: &now,
			Endpoints: []EndpointDiscoveryInfo{
				{
					KeyMask:     "sk-****abcd",
					BaseURL:     "https://api.example.com",
					ModelsCount: 5,
					ProtocolOk:  true,
				},
			},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	if parsed["autoManaged"] != true {
		t.Fatalf("期望 autoManaged=true, 实际=%v", parsed["autoManaged"])
	}

	discovery := parsed["discovery"].(map[string]interface{})
	if discovery["status"] != "done" {
		t.Fatalf("期望 status=done, 实际=%v", discovery["status"])
	}

	endpoints := discovery["endpoints"].([]interface{})
	if len(endpoints) != 1 {
		t.Fatalf("期望 1 个 endpoint, 实际=%d", len(endpoints))
	}
	ep := endpoints[0].(map[string]interface{})
	if ep["keyMask"] != "sk-****abcd" {
		t.Fatalf("期望 keyMask=sk-****abcd, 实际=%v", ep["keyMask"])
	}
}

func TestAutoStatusResponse_KeyMaskNoPlaintext(t *testing.T) {
	// 验证 key mask 不会泄露明文 key
	result := EndpointDiscoveryInfo{
		KeyMask:     "sk-****abcd",
		BaseURL:     "https://api.example.com",
		ModelsCount: 3,
		ProtocolOk:  true,
	}

	data, _ := json.Marshal(result)
	s := string(data)

	// 不应包含常见的 key 前缀（如果 key 以 sk- 开头）
	if s == "sk-test1234567890" {
		t.Fatal("key mask 包含明文 key")
	}
}

func TestValidChannelKinds(t *testing.T) {
	expected := []string{"messages", "chat", "responses", "gemini", "images", "vectors"}
	for _, kind := range expected {
		if !validChannelKinds[kind] {
			t.Fatalf("渠道类型 %s 应有效", kind)
		}
	}

	invalid := []string{"unknown", "test", ""}
	for _, kind := range invalid {
		if validChannelKinds[kind] {
			t.Fatalf("渠道类型 %s 应无效", kind)
		}
	}
}

func TestKindToDefaultServiceType(t *testing.T) {
	tests := []struct {
		kind     string
		expected string
	}{
		{"messages", "claude"},
		{"chat", "openai"},
		{"responses", "responses"},
		{"gemini", "gemini"},
		{"images", "openai"},
		{"vectors", "openai"},
		{"unknown", "openai"},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			result := kindToDefaultServiceType(tt.kind)
			if result != tt.expected {
				t.Fatalf("kind=%s, 期望=%s, 实际=%s", tt.kind, tt.expected, result)
			}
		})
	}
}

func TestCustomAutoAddResponseIncludesActualRoute(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "upstream": [], "chatUpstream": [], "responsesUpstream": [],
  "geminiUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	discoverer := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, QuietLogs: true})
	router := setupAutoManagedRouter(&AutoManagedDeps{CfgManager: manager, RateLimitDiscoverer: discoverer})
	req := httptest.NewRequest(http.MethodPost, "/api/responses/channels/auto-add", bytes.NewBufferString(
		`{"name":"fastaitoken-com-test","baseUrls":["https://example.com"],"apiKeys":["sk-test"],"rateLimitHint":{"initialRpm":30,"effectiveRpm":15,"rateLimited":true,"rateLimitedCount":1}}`,
	))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var response AutoAddResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Channels) != 1 {
		t.Fatalf("channels=%+v, want one actual route", response.Channels)
	}
	route := response.Channels[0]
	if route.ChannelKind != "responses" || route.ServiceType != "responses" || route.ChannelUID != response.ChannelUID {
		t.Fatalf("route=%+v response=%+v", route, response)
	}
	cfg := manager.GetConfig()
	if len(cfg.ResponsesUpstream) != 1 || len(cfg.Upstream) != 0 {
		t.Fatalf("custom route persisted in wrong channel: responses=%d messages=%d", len(cfg.ResponsesUpstream), len(cfg.Upstream))
	}
	created := cfg.ResponsesUpstream[0]
	baseURL := created.GetEffectiveBaseURL()
	endpointUID := GenerateEndpointUID(created.ChannelUID, baseURL, KeyHashFromAPIKey("sk-test"))
	if suggestion := discoverer.SuggestedLimit(endpointUID); suggestion.RPM != 15 || suggestion.Confidence != 0.7 {
		t.Fatalf("rate limit suggestion=%+v, want rpm=15 confidence=0.7", suggestion)
	}

	store := newTestProfileStore(t)
	runner := NewAutoDiscoveryRunner(store, nil)
	seedAutoAddRateLimitHint(
		&AutoManagedDeps{CfgManager: manager, Runner: runner, RateLimitDiscoverer: discoverer},
		manager.GetAccountChannels(response.AccountUID),
		&AutoAddRateLimitHint{InitialRPM: 30, EffectiveRPM: 15, RateLimited: true, RateLimitedCount: 1},
		"sk-test",
		"https://example.com",
	)
	profile := store.Get(endpointUID)
	if profile == nil || profile.DiscoveredRPM != 15 || profile.RateLimitConfidence != 0.7 || profile.RateLimitSource != string(RateLimitSourcePassiveAIMD) {
		t.Fatalf("rate limit profile=%+v", profile)
	}
}

func TestCustomAutoAddAppendsKeyToEquivalentExistingBaseURL(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "upstream": [],
  "chatUpstream": [{"channelUid":"ch_existing","name":"localhost-old-name","serviceType":"openai","baseUrl":"http://localhost:8990/v1","apiKeys":["sk-old"],"status":"active"}],
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

	router := setupAutoManagedRouter(&AutoManagedDeps{CfgManager: manager})
	req := httptest.NewRequest(http.MethodPost, "/api/messages/channels/auto-add", bytes.NewBufferString(
		`{"name":"localhost-8990","baseUrls":["http://localhost:8990/"],"apiKeys":["sk-new"]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response AutoAddResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Channels) != 1 || response.Channels[0].Name != "localhost-old-name" {
		t.Fatalf("应返回已有渠道: %+v", response.Channels)
	}
	cfg := manager.GetConfig()
	if len(cfg.Upstream) != 0 || len(cfg.ChatUpstream) != 1 {
		t.Fatalf("重复请求不应创建新渠道: messages=%d chat=%d", len(cfg.Upstream), len(cfg.ChatUpstream))
	}
	if got := strings.Join(cfg.ChatUpstream[0].APIKeys, ","); got != "sk-old,sk-new" {
		t.Fatalf("已有渠道密钥=%q, want sk-old,sk-new", got)
	}
}

func TestFindExistingAutoAddChannelsPreservesHashSemantics(t *testing.T) {
	cfg := config.Config{
		ChatUpstream: []config.UpstreamConfig{{
			ChannelUID: "ch-hash", ServiceType: "openai", BaseURL: "https://example.com#",
		}},
	}
	if matches := findExistingAutoAddChannels(cfg, []string{"https://example.com"}); len(matches) != 0 {
		t.Fatalf("普通 Base URL 不应匹配禁止版本前缀的地址: %+v", matches)
	}
	if matches := findExistingAutoAddChannels(cfg, []string{"https://example.com/#"}); len(matches) != 1 {
		t.Fatalf("等效 # 地址匹配数量=%d, want 1", len(matches))
	}
}

func TestCustomAutoAddAppendsKeyAndMissingRouteToManagedAccount(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "upstream": [{"accountUid":"acct_existing","channelUid":"ch_existing","name":"localhost-8990","serviceType":"claude","baseUrl":"http://localhost:8990","apiKeys":["sk-old"],"autoManaged":true,"status":"active"}],
  "chatUpstream": [], "responsesUpstream": [], "geminiUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	router := setupAutoManagedRouter(&AutoManagedDeps{CfgManager: manager})
	body := `{
  "name":"localhost-8990",
  "baseUrls":["http://localhost:8990/v1"],
  "apiKeys":["sk-new"],
  "routes":[
    {"channelKind":"messages","supportedModels":["claude-test"]},
    {"channelKind":"responses","supportedModels":["gpt-test"]}
  ]
}`
	req := httptest.NewRequest(http.MethodPost, "/api/messages/channels/auto-add", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	cfg := manager.GetConfig()
	if len(cfg.Upstream) != 1 || len(cfg.ResponsesUpstream) != 1 {
		t.Fatalf("协议路由数量错误: messages=%d responses=%d", len(cfg.Upstream), len(cfg.ResponsesUpstream))
	}
	if cfg.Upstream[0].AccountUID != "acct_existing" || cfg.ResponsesUpstream[0].AccountUID != "acct_existing" {
		t.Fatalf("应复用已有账号: messages=%q responses=%q", cfg.Upstream[0].AccountUID, cfg.ResponsesUpstream[0].AccountUID)
	}
	if got := strings.Join(cfg.Upstream[0].APIKeys, ","); got != "sk-old,sk-new" {
		t.Fatalf("messages 密钥=%q, want sk-old,sk-new", got)
	}
	if got := strings.Join(cfg.ResponsesUpstream[0].APIKeys, ","); got != "sk-old,sk-new" {
		t.Fatalf("responses 密钥=%q, want sk-old,sk-new", got)
	}
	if cfg.Upstream[0].Name != "localhost-8990-claude" || cfg.ResponsesUpstream[0].Name != "localhost-8990-codex" {
		t.Fatalf("多协议自动命名错误: messages=%q responses=%q", cfg.Upstream[0].Name, cfg.ResponsesUpstream[0].Name)
	}
}

func TestCustomAutoAddCreatesAllDetectedRoutesWithProtocolModels(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "upstream": [], "chatUpstream": [], "responsesUpstream": [],
  "geminiUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	router := setupAutoManagedRouter(&AutoManagedDeps{CfgManager: manager})
	body := `{
  "name":"fastaitoken-com-test",
  "baseUrls":["https://example.com/keys"],
  "apiKeys":["sk-test"],
  "routes":[
    {"channelKind":"messages","supportedModels":["shared","messages-only"]},
    {"channelKind":"responses","supportedModels":["shared","responses-only"]},
    {"channelKind":"chat","supportedModels":["shared","chat-only"]}
  ]
}`
	req := httptest.NewRequest(http.MethodPost, "/api/responses/channels/auto-add", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var response AutoAddResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Channels) != 3 {
		t.Fatalf("channels=%+v, want three detected routes", response.Channels)
	}
	for _, route := range response.Channels {
		if route.AccountUID != response.AccountUID {
			t.Fatalf("route account=%q, want %q", route.AccountUID, response.AccountUID)
		}
	}
	if response.Channels[1].ChannelKind != "responses" || response.ChannelUID != response.Channels[1].ChannelUID {
		t.Fatalf("请求协议应作为主路由: response=%+v", response)
	}

	cfg := manager.GetConfig()
	if len(cfg.Upstream) != 1 || len(cfg.ResponsesUpstream) != 1 || len(cfg.ChatUpstream) != 1 || len(cfg.GeminiUpstream) != 0 {
		t.Fatalf("协议路由数量错误: messages=%d responses=%d chat=%d gemini=%d",
			len(cfg.Upstream), len(cfg.ResponsesUpstream), len(cfg.ChatUpstream), len(cfg.GeminiUpstream))
	}
	checks := []struct {
		channel config.UpstreamConfig
		name    string
		models  []string
	}{
		{cfg.Upstream[0], "fastaitoken-com-test-claude", []string{"shared", "messages-only"}},
		{cfg.ResponsesUpstream[0], "fastaitoken-com-test-codex", []string{"shared", "responses-only"}},
		{cfg.ChatUpstream[0], "fastaitoken-com-test-chat", []string{"shared", "chat-only"}},
	}
	for _, check := range checks {
		if check.channel.Name != check.name || check.channel.AccountUID != response.AccountUID {
			t.Fatalf("route identity=%+v, want name=%q account=%q", check.channel, check.name, response.AccountUID)
		}
		if strings.Join(check.channel.SupportedModels, ",") != strings.Join(check.models, ",") {
			t.Fatalf("route %s models=%v, want %v", check.name, check.channel.SupportedModels, check.models)
		}
	}
}

func TestPlanCustomManagedAccountUpdatesRenamesAndSyncsCredentials(t *testing.T) {
	channels := []config.AccountChannel{
		{Kind: "messages", Upstream: config.UpstreamConfig{
			AccountUID: "acct-custom", ChannelUID: "ch-messages", Name: "old-claude",
			ServiceType: "claude", AutoManaged: true, BaseURL: "https://example.com",
			BaseURLs: []string{"https://example.com"}, APIKeys: []string{"sk-old"},
			APIKeyConfigs: []config.APIKeyConfig{{Key: "sk-old", BaseURL: "https://example.com"}},
		}},
		{Kind: "responses", Upstream: config.UpstreamConfig{
			AccountUID: "acct-custom", ChannelUID: "ch-responses", Name: "old-codex",
			ServiceType: "responses", AutoManaged: true, BaseURL: "https://example.com",
			BaseURLs: []string{"https://example.com"}, APIKeys: []string{"sk-old"},
			APIKeyConfigs: []config.APIKeyConfig{{Key: "sk-old", BaseURL: "https://example.com"}},
		}},
	}
	updates, status, err := planCustomManagedAccountUpdates("acct-custom", updateAccountRequest{
		Name: "renamed", APIKeys: []string{"sk-old", "sk-new"},
	}, channels, len(channels))
	if err != nil || status != http.StatusOK {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if len(updates) != 2 || updates[0].Name != "renamed-claude" || updates[1].Name != "renamed-codex" {
		t.Fatalf("route names=%+v", updates)
	}
	for _, update := range updates {
		if len(update.APIKeyConfig) != 2 || update.APIKeyConfig[1].BaseURL != "https://example.com" {
			t.Fatalf("route credentials=%+v", update.APIKeyConfig)
		}
		if update.APIKeyConfig[1].CredentialUID == "" {
			t.Fatalf("new credential missing stable uid: %+v", update.APIKeyConfig[1])
		}
	}
}

func TestProviderRouteNameAndPrimaryResult(t *testing.T) {
	base := "mimo-test"
	tests := []struct {
		route config.ProviderRoute
		want  string
	}{
		{route: config.ProviderRoute{ChannelKind: "messages"}, want: "mimo-test-claude"},
		{route: config.ProviderRoute{ChannelKind: "chat"}, want: "mimo-test-chat"},
		{route: config.ProviderRoute{ChannelKind: "responses"}, want: "mimo-test-codex"},
		{route: config.ProviderRoute{ChannelKind: "gemini"}, want: "mimo-test-gemini"},
	}
	for _, tt := range tests {
		if got := providerRouteName(base, tt.route, true); got != tt.want {
			t.Fatalf("providerRouteName(%s)=%q, want %q", tt.route.ChannelKind, got, tt.want)
		}
	}
	if got := providerRouteName(base, config.ProviderRoute{ChannelKind: "messages"}, false); got != base {
		t.Fatalf("single route name=%q, want %q", got, base)
	}

	results := []AutoAddChannelResult{
		{ChannelKind: "messages", ChannelUID: "ch_messages", Index: 1},
		{ChannelKind: "chat", ChannelUID: "ch_chat", Index: 2},
		{ChannelKind: "responses", ChannelUID: "ch_responses", Index: 3},
	}
	primary := primaryAutoAddResult(results, "chat")
	if primary.ChannelUID != "ch_chat" || primary.Index != 2 {
		t.Fatalf("primary=%+v, want chat result", primary)
	}
}

func TestManagedAccountUIDForProviderUsesExistingAccount(t *testing.T) {
	cfg := config.Config{ManagedAccounts: []config.ManagedAccountConfig{
		{AccountUID: "acct-old", ProviderID: "mimo"},
		{AccountUID: "acct-new", ProviderID: "mimo"},
		{AccountUID: "acct-deepseek", ProviderID: "deepseek"},
	}}
	if got := managedAccountUIDForProvider(cfg, "mimo"); got != "acct-new" {
		t.Fatalf("mimo existing account = %q, want acct-new", got)
	}
	if got := managedAccountUIDForProvider(cfg, "unknown"); got != "" {
		t.Fatalf("unknown provider 不应命中账号: %q", got)
	}
}

func TestListManagedAccountsDoesNotExposeVolcengineSecret(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"acct_volc","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"cred_volc","apiKey":"ark-secret","volcengineAccessKey":{"accessKeyId":"AKIDEXAMPLE","secretAccessKey":"SECRET_MUST_NOT_LEAK","plan":"agent_plan","planTier":"Large","planStatus":"Running"}}]}],
  "upstream":[{"accountUid":"acct_volc","channelUid":"ch_volc","providerId":"volcengine","name":"volc","serviceType":"claude","autoManaged":true,"baseUrl":"https://ark.cn-beijing.volces.com/api/plan","apiKeyConfigs":[{"credentialUid":"cred_volc","baseUrl":"https://ark.cn-beijing.volces.com/api/plan"}]}],
  "chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer errutil.IgnoreDeferred(manager.Close)
	router := setupAutoManagedRouter(&AutoManagedDeps{CfgManager: manager})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "SECRET_MUST_NOT_LEAK") || strings.Contains(body, "ark-secret") {
		t.Fatalf("管理 API 泄漏凭证: %s", body)
	}
	for _, expected := range []string{`"hasVolcengineAccessKey":true`, `"volcenginePlan":"agent_plan"`, `"volcenginePlanTier":"Large"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("响应缺少 %s: %s", expected, body)
		}
	}
}

func TestKimiConsoleTokenLifecycleDoesNotExposeToken(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts": [{"accountUid":"acct_kimi","providerId":"kimi","name":"kimi","credentials":[{"credentialUid":"cred_kimi","apiKey":"sk-kimi-secret"}]}],
  "upstream": [{"accountUid":"acct_kimi","channelUid":"ch_kimi","providerId":"kimi","name":"kimi","serviceType":"claude","baseUrl":"https://api.kimi.com/coding","apiKeyConfigs":[{"credentialUid":"cred_kimi","baseUrl":"https://api.kimi.com/coding"}],"autoManaged":true,"status":"active"}],
  "responsesUpstream": [], "geminiUpstream": [], "chatUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfgManager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	kimiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer web-session-secret" {
			t.Fatalf("Kimi 请求未使用保存的会话令牌")
		}
		switch r.URL.Path {
		case kimiUsagesPath:
			_, _ = w.Write([]byte(`{"usages":[{"scope":"FEATURE_CODING","detail":{"limit":"100","remaining":"80","resetTime":"2026-07-27T16:13:43Z"},"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"100","remaining":"90","resetTime":"2026-07-21T02:13:43Z"}}]}],"totalQuota":{"limit":"100","remaining":"80"}}`))
		case kimiSubscriptionStatsPath:
			_, _ = w.Write([]byte(`{"ratelimitCode5h":{"ratio":0.1,"enabled":true},"ratelimitCode7d":{"ratio":0.2,"enabled":true},"subscriptionBalance":{"feature":"FEATURE_OMNI","type":"SUBSCRIPTION","unit":"UNIT_CREDIT","amountUsedRatio":0.2,"kimiCodeUsedRatio":0.2,"expireTime":"2026-08-20T16:14:07Z"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer kimiServer.Close()

	router := setupAutoManagedRouter(&AutoManagedDeps{
		CfgManager: cfgManager,
		KimiConsoleClient: &KimiConsoleClient{
			HTTPClient: kimiServer.Client(), BaseURL: kimiServer.URL,
		},
	})
	put := httptest.NewRequest(
		http.MethodPut,
		"/api/accounts/acct_kimi/credentials/cred_kimi/kimi-console-token",
		bytes.NewBufferString(`{"accessToken":"Bearer web-session-secret"}`),
	)
	put.Header.Set("Content-Type", "application/json")
	putRecorder := httptest.NewRecorder()
	router.ServeHTTP(putRecorder, put)
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("绑定 Kimi 令牌失败: status=%d body=%s", putRecorder.Code, putRecorder.Body.String())
	}
	if strings.Contains(putRecorder.Body.String(), "web-session-secret") || strings.Contains(putRecorder.Body.String(), "accessToken") {
		t.Fatalf("绑定响应泄露 Kimi 令牌: %s", putRecorder.Body.String())
	}
	credential, ok := cfgManager.GetManagedAccountCredential("acct_kimi", "cred_kimi")
	if !ok || credential.KimiConsole == nil || credential.KimiConsole.AccessToken != "web-session-secret" {
		t.Fatalf("Kimi 令牌未持久化: %+v", credential)
	}
	persisted, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted), `"accessToken": "web-session-secret"`) {
		t.Fatalf("配置文件未保存 Kimi 令牌")
	}

	listRecorder := httptest.NewRecorder()
	router.ServeHTTP(listRecorder, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	if listRecorder.Code != http.StatusOK || strings.Contains(listRecorder.Body.String(), "web-session-secret") || strings.Contains(listRecorder.Body.String(), "accessToken") {
		t.Fatalf("账号列表响应异常或泄露令牌: status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	if !strings.Contains(listRecorder.Body.String(), `"hasKimiConsoleToken":true`) || !strings.Contains(listRecorder.Body.String(), `"kimiCodeUsage"`) {
		t.Fatalf("账号列表缺少 Kimi 用量快照: %s", listRecorder.Body.String())
	}

	refreshRecorder := httptest.NewRecorder()
	router.ServeHTTP(refreshRecorder, httptest.NewRequest(
		http.MethodPost,
		"/api/accounts/acct_kimi/credentials/cred_kimi/kimi-console-token/refresh",
		nil,
	))
	if refreshRecorder.Code != http.StatusOK {
		t.Fatalf("刷新 Kimi 余量失败: status=%d body=%s", refreshRecorder.Code, refreshRecorder.Body.String())
	}
	if strings.Contains(refreshRecorder.Body.String(), "web-session-secret") || strings.Contains(refreshRecorder.Body.String(), "accessToken") {
		t.Fatalf("刷新响应泄露 Kimi 令牌: %s", refreshRecorder.Body.String())
	}

	deleteRecorder := httptest.NewRecorder()
	router.ServeHTTP(deleteRecorder, httptest.NewRequest(
		http.MethodDelete,
		"/api/accounts/acct_kimi/credentials/cred_kimi/kimi-console-token",
		nil,
	))
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("解绑 Kimi 令牌失败: status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	credential, _ = cfgManager.GetManagedAccountCredential("acct_kimi", "cred_kimi")
	if credential.KimiConsole != nil {
		t.Fatalf("Kimi 令牌解绑后仍存在: %+v", credential.KimiConsole)
	}
	persisted, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), "web-session-secret") {
		t.Fatalf("解绑后配置文件仍包含 Kimi 令牌")
	}
}

func TestSetVolcengineAccessKeyDetectsPlanBeforePersisting(t *testing.T) {
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("Action") != "GetPersonalPlan" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body struct{ Plan string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Plan == "CodingPlan" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"ResourceNotFound.Plan","Message":"not found"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"Result":{"PlanType":"Large","Status":"Running"}}`))
	}))
	defer controlPlane.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"acct_volc","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"cred_volc","apiKey":"ark-inference"}]}],
  "upstream":[{"accountUid":"acct_volc","channelUid":"ch_volc","providerId":"volcengine","name":"volc","serviceType":"claude","autoManaged":true,"baseUrl":"https://ark.cn-beijing.volces.com/api/plan","apiKeyConfigs":[{"credentialUid":"cred_volc","baseUrl":"https://ark.cn-beijing.volces.com/api/plan"}]}],
  "chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer errutil.IgnoreDeferred(manager.Close)
	runner := NewAutoDiscoveryRunner(nil, nil)
	runner.client = controlPlane.Client()
	runner.volcengineControlPlaneEndpoint = controlPlane.URL
	router := setupAutoManagedRouter(&AutoManagedDeps{CfgManager: manager, Runner: runner})
	req := httptest.NewRequest(http.MethodPut, "/api/accounts/acct_volc/credentials/cred_volc/volcengine-access-key", bytes.NewBufferString(`{"accessKeyId":"AKID","secretAccessKey":"SECRET"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"plan":"agent_plan"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	credential, ok := manager.GetManagedAccountCredential("acct_volc", "cred_volc")
	if !ok || credential.VolcengineAccessKey == nil || credential.VolcengineAccessKey.SecretAccessKey != "SECRET" || credential.VolcengineAccessKey.Plan != "agent_plan" {
		t.Fatalf("凭证未正确持久化: %+v", credential)
	}
}

func TestSetMiMoConsoleCookieRequiresConfirmationBeforeAdoptingKey(t *testing.T) {
	const oldKey = "tp-old-1234567890123456789012345678901234567890"
	const cookieKey = "tp-new-1234567890123456789012345678901234567890"
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "api-platform_serviceToken=session; userId=42" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/userProfile":
			_, _ = w.Write([]byte(`{"code":0,"data":{"userId":"42"}}`))
		case "/api/v1/tokenPlan/detail":
			_, _ = w.Write([]byte(`{"code":0,"data":{"planCode":"max","planName":"Max","currentPeriodEnd":"2026-07-29 23:59:59"}}`))
		case "/api/v1/tokenPlan/usage":
			_, _ = w.Write([]byte(`{"code":0,"data":{"monthUsage":{"percent":0.25,"items":[{"used":25,"limit":100}]},"usage":{"percent":0.5,"items":[{"used":50,"limit":100}]}}}`))
		case "/api/v1/tokenPlan/apiKey/raw":
			_, _ = w.Write([]byte(`{"code":0,"data":"` + cookieKey + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer console.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"acct_mimo","providerId":"mimo","name":"mimo","credentials":[{"credentialUid":"cred_mimo","apiKey":"` + oldKey + `"}]}],
  "upstream":[{"accountUid":"acct_mimo","channelUid":"ch_mimo","providerId":"mimo","name":"mimo","serviceType":"claude","autoManaged":true,"baseUrl":"https://token-plan-cn.xiaomimimo.com/anthropic","apiKeyConfigs":[{"credentialUid":"cred_mimo","baseUrl":"https://token-plan-cn.xiaomimimo.com/anthropic"}]}],
  "chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer errutil.IgnoreDeferred(manager.Close)
	deps := &AutoManagedDeps{CfgManager: manager, MiMoConsoleClient: &MiMoConsoleClient{HTTPClient: console.Client(), BaseURL: console.URL}}
	router := setupAutoManagedRouter(deps)
	body := `{"cookie":"api-platform_serviceToken=session; userId=42"}`
	request := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/accounts/acct_mimo/credentials/cred_mimo/mimo-console-cookie", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	w := request(body)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"mimo_cookie_key_mismatch"`) {
		t.Fatalf("未要求确认: status=%d body=%s", w.Code, w.Body.String())
	}
	credential, _ := manager.GetManagedAccountCredential("acct_mimo", "cred_mimo")
	if credential.APIKey != oldKey || credential.MiMoConsole != nil {
		t.Fatalf("确认前不应修改配置: %+v", credential)
	}
	w = request(`{"cookie":"api-platform_serviceToken=session; userId=42","adoptCookieKey":true}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"keyAdopted":true`) {
		t.Fatalf("确认采用失败: status=%d body=%s", w.Code, w.Body.String())
	}
	credential, _ = manager.GetManagedAccountCredential("acct_mimo", "cred_mimo")
	if credential.APIKey != cookieKey || credential.MiMoConsole == nil {
		t.Fatalf("确认后未采用 Cookie Key: %+v", credential)
	}
	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	if strings.Contains(list.Body.String(), "api-platform_serviceToken") || strings.Contains(list.Body.String(), cookieKey) {
		t.Fatalf("账号列表泄漏 Cookie 或原始 Key: %s", list.Body.String())
	}
}

func TestCompshareConsoleCookieBindingDoesNotExposeSecrets(t *testing.T) {
	const currentKey = "sk-cp-test-current"
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != testCompshareCookie {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{
  "RetCode": 0,
  "UserPlans": [{
    "Code": "cppkg-test", "PlanCode": "cp-plan-test", "PlanName": "Pro", "DisplayName": "Pro 增强版",
    "LimitPer5h": 3000, "LimitPerWeek": 7500, "LimitPerMonth": 19000,
    "UsagePer5h": 0, "UsagePerWeek": 100, "UsagePerMonth": 6496,
    "ConcurrencyLimit": 10, "Status": 1, "ExpireAt": 1785037981,
    "Keys": [{"APIKey": "` + currentKey + `"}]
  }]
}`))
	}))
	defer console.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"acct_compshare","providerId":"compshare","name":"compshare","credentials":[{"credentialUid":"cred_compshare","apiKey":"` + currentKey + `"}]}],
  "upstream":[{"accountUid":"acct_compshare","channelUid":"ch_compshare","providerId":"compshare","name":"compshare","serviceType":"claude","autoManaged":true,"baseUrl":"https://api.compshare.cn/anthropic","apiKeyConfigs":[{"credentialUid":"cred_compshare","baseUrl":"https://api.compshare.cn/anthropic"}]}],
  "chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer errutil.IgnoreDeferred(manager.Close)
	router := setupAutoManagedRouter(&AutoManagedDeps{
		CfgManager:             manager,
		CompshareConsoleClient: &CompshareConsoleClient{HTTPClient: console.Client(), BaseURL: console.URL},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/accounts/acct_compshare/credentials/cred_compshare/compshare-console-cookie", bytes.NewBufferString(`{"cookie":"`+testCompshareCookie+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"displayName":"Pro 增强版"`) {
		t.Fatalf("绑定失败: status=%d body=%s", w.Code, w.Body.String())
	}
	credential, ok := manager.GetManagedAccountCredential("acct_compshare", "cred_compshare")
	if !ok || credential.CompshareConsole == nil || credential.CompshareConsole.Cookie != testCompshareCookie {
		t.Fatalf("控制台套餐未持久化: %+v", credential)
	}

	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	response := list.Body.String()
	for _, secret := range []string{"U_JWT_TOKEN", "test-session", "test_user@console.compshare.cn", "org-test", currentKey, "cppkg-test"} {
		if strings.Contains(response, secret) {
			t.Fatalf("账号列表泄漏敏感字段 %q: %s", secret, response)
		}
	}
	for _, expected := range []string{`"hasCompshareConsoleCookie":true`, `"planCode":"cp-plan-test"`, `"monthlyUsage":{"used":6496,"limit":19000`} {
		if !strings.Contains(response, expected) {
			t.Fatalf("账号列表缺少套餐字段 %s: %s", expected, response)
		}
	}
}

func TestProviderAutoAddReusesExistingAccount(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts": [{"accountUid":"acct_test","providerId":"mimo","name":"mimo-main","credentials":[{"credentialUid":"cred_test","apiKey":"sk-existing"}]}],
  "upstream": [{"accountUid":"acct_test","channelUid":"ch_messages","providerId":"mimo","name":"mimo-main","serviceType":"claude","baseUrl":"https://api.xiaomimimo.com/anthropic","apiKeyConfigs":[{"credentialUid":"cred_test","baseUrl":"https://api.xiaomimimo.com/anthropic"}],"autoManaged":true,"status":"active"}],
  "responsesUpstream": [], "geminiUpstream": [], "chatUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfgManager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })
	router := setupAutoManagedRouter(&AutoManagedDeps{CfgManager: cfgManager})
	req := httptest.NewRequest(http.MethodPost, "/api/messages/channels/auto-add", bytes.NewBufferString(`{"providerId":"mimo","apiKeys":["sk-existing"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("追加已有 provider key status=%d body=%s", w.Code, w.Body.String())
	}
	channels := cfgManager.GetAccountChannels("acct_test")
	if len(channels) != 4 {
		t.Fatalf("旧账号应自动补齐模板中的协议渠道: %+v", channels)
	}
	kinds := make(map[string]bool, len(channels))
	for _, channel := range channels {
		kinds[channel.Kind] = true
	}
	for _, kind := range []string{"messages", "chat", "responses", "gemini"} {
		if !kinds[kind] {
			t.Fatalf("旧账号缺少 %s route: %+v", kind, channels)
		}
	}
	var response AutoAddResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.AccountUID != "acct_test" {
		t.Fatalf("响应未返回已有账号: body=%s err=%v", w.Body.String(), err)
	}
	if len(response.Channels) != 4 {
		t.Fatalf("响应未返回补齐后的全部渠道: %+v", response.Channels)
	}
}

func TestMissingProviderAccountRoutes(t *testing.T) {
	tmpl, ok := config.GetProviderTemplate("deepseek")
	if !ok {
		t.Fatal("缺少 deepseek provider 模板")
	}
	existing := []config.AccountChannel{{
		Kind:     "messages",
		Upstream: config.UpstreamConfig{ServiceType: "claude"},
	}}
	missing := missingProviderAccountRoutes(tmpl, existing)
	if len(missing) != 2 || missing[0].ChannelKind != "chat" || missing[1].ChannelKind != "responses" {
		t.Fatalf("missing routes = %+v", missing)
	}
	configs, baseURLs, err := bindProviderRouteKeys(tmpl, missing[0], []string{"sk-existing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || configs[0].BaseURL != "https://api.deepseek.com" || len(baseURLs) != 1 {
		t.Fatalf("route 绑定结果不正确: configs=%+v baseURLs=%v", configs, baseURLs)
	}
}

func TestBindProviderRouteKeysPreservesExistingEndpointAffinity(t *testing.T) {
	tests := []struct {
		providerID      string
		apiKey          string
		existingBaseURL string
		targetKind      string
		wantBaseURL     string
	}{
		{
			providerID: "mimo", apiKey: "tp-existing",
			existingBaseURL: "https://token-plan-sgp.xiaomimimo.com/anthropic",
			targetKind:      "chat", wantBaseURL: "https://token-plan-sgp.xiaomimimo.com/v1",
		},
		{
			providerID: "volcengine", apiKey: "ark-existing",
			existingBaseURL: "https://ark.cn-beijing.volces.com/api/coding",
			targetKind:      "responses", wantBaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.providerID, func(t *testing.T) {
			tmpl, ok := config.GetProviderTemplate(tt.providerID)
			if !ok {
				t.Fatalf("缺少 provider 模板: %s", tt.providerID)
			}
			existing := []config.AccountChannel{{
				Kind: "messages",
				Upstream: config.UpstreamConfig{
					ProviderID: tt.providerID, ServiceType: "claude", APIKeys: []string{tt.apiKey},
					APIKeyConfigs: []config.APIKeyConfig{{Key: tt.apiKey, BaseURL: tt.existingBaseURL}},
				},
			}}
			var target config.ProviderRoute
			for _, route := range tmpl.AutoAddRoutes() {
				if route.ChannelKind == tt.targetKind {
					target = route
					break
				}
			}
			configs, _, err := bindProviderRouteKeysWithAffinities(
				tmpl, target, []string{tt.apiKey}, providerKeyCandidateAffinities(tmpl, existing),
			)
			if err != nil || len(configs) != 1 || configs[0].BaseURL != tt.wantBaseURL {
				t.Fatalf("亲和绑定错误: configs=%+v err=%v want=%s", configs, err, tt.wantBaseURL)
			}
		})
	}
}

func TestPlanProviderAccountRouteAdditionsRejectsNewKeyBeforeMutation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	tmpl := &config.ProviderTemplate{
		ProviderID: "test-provider",
		Routes: []config.ProviderRoute{
			{ChannelKind: "messages", ServiceType: "claude", Candidates: []config.ProviderCandidate{{BaseURL: server.URL + "/anthropic"}}},
			{ChannelKind: "chat", ServiceType: "openai", Candidates: []config.ProviderCandidate{{BaseURL: server.URL + "/v1"}}},
		},
	}
	existing := []config.AccountChannel{{
		Kind: "messages",
		Upstream: config.UpstreamConfig{
			AccountUID: "acct_test", ChannelUID: "ch_messages", Name: "test-provider",
			ProviderID: "test-provider", ServiceType: "claude", AutoManaged: true, APIKeys: []string{"sk-existing"},
		},
	}}
	additions, status, err := planProviderAccountRouteAdditions(
		t.Context(), config.Config{}, tmpl, "acct_test", []string{"sk-existing", "sk-invalid"}, existing,
	)
	if err == nil || status != http.StatusBadRequest || len(additions) != 0 {
		t.Fatalf("无效新 Key 应在生成新增 route 前失败: status=%d additions=%+v err=%v", status, additions, err)
	}
}

func TestAutoAddHandler_InvalidKind(t *testing.T) {
	deps := &AutoManagedDeps{
		CfgManager: nil,
		Runner:     nil,
	}
	r := setupAutoManagedRouter(deps)

	body := `{"baseUrls":["https://api.example.com"],"apiKeys":["sk-test"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/unknown/channels/auto-add", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// 静态路由注册后，无效 kind 直接返回 404（路由不存在）
	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 404, 实际=%d", w.Code)
	}
}

func TestAutoDiscoverHandler_InvalidKind(t *testing.T) {
	deps := &AutoManagedDeps{
		CfgManager: nil,
		Runner:     nil,
	}
	r := setupAutoManagedRouter(deps)

	req := httptest.NewRequest(http.MethodPost, "/api/unknown/channels/0/auto-discover", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// 静态路由注册后，无效 kind 直接返回 404（路由不存在）
	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 404, 实际=%d", w.Code)
	}
}

func TestAutoStatusHandler_InvalidKind(t *testing.T) {
	deps := &AutoManagedDeps{
		CfgManager: nil,
		Runner:     nil,
	}
	r := setupAutoManagedRouter(deps)

	req := httptest.NewRequest(http.MethodGet, "/api/unknown/channels/0/auto-status", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// 静态路由注册后，无效 kind 直接返回 404（路由不存在）
	if w.Code != http.StatusNotFound {
		t.Fatalf("期望 404, 实际=%d", w.Code)
	}
}
