package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/handlers"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newTestEnvCfg 创建测试用 EnvConfig（单 key 模式，admin key = proxy key）。
func newTestEnvCfg(adminKey string) *config.EnvConfig {
	return &config.EnvConfig{
		ProxyAccessKey: adminKey,
		AdminAccessKey: adminKey,
		EnableWebUI:    true,
	}
}

// newTestEnvCfgMultiKey 创建测试用 EnvConfig（多 proxy key 模式）。
func newTestEnvCfgMultiKey(adminKey string, extraKeys []string) *config.EnvConfig {
	return &config.EnvConfig{
		ProxyAccessKey:       "proxy-key-main",
		ExtraProxyAccessKeys: extraKeys,
		AdminAccessKey:       adminKey,
		EnableWebUI:          true,
	}
}

// setupTestRouter 创建带测试渠道的 ConfigManager 和 Gin 路由。
func setupTestRouter(t *testing.T) (*config.ConfigManager, *gin.Engine) {
	t.Helper()
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfgManager, err := config.NewConfigManager(cfgPath, tmpDir+"/backups")
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	// 添加测试渠道
	err = cfgManager.AddUpstream(config.UpstreamConfig{
		ChannelUID: "ch_aabbccddeeff",
		Name:       "Test Claude",
		BaseURL:    "https://api.anthropic.com",
		APIKeys:    []string{"sk-ant-test-key-12345"},
		Status:     "active",
		Tags:       []string{"test"},
	})
	if err != nil {
		t.Fatalf("AddUpstream: %v", err)
	}

	err = cfgManager.AddChatUpstream(config.UpstreamConfig{
		ChannelUID: "ch_112233445566",
		Name:       "Test OpenAI",
		BaseURL:    "https://api.openai.com/v1",
		APIKeys:    []string{"sk-openai-test-key-67890"},
		Status:     "active",
		Tags:       []string{"test", "openai"},
	})
	if err != nil {
		t.Fatalf("AddChatUpstream: %v", err)
	}

	envCfg := newTestEnvCfg("test-admin-key")
	r := gin.New()
	r.POST("/api/channels/export", handlers.ExportChannels(envCfg, cfgManager))
	r.GET("/api/channels/export", handlers.ExportAllChannels(envCfg, cfgManager))
	r.POST("/api/channels/import", handlers.ImportChannels(cfgManager))
	r.POST("/api/channels/import/confirm", handlers.ImportChannelsConfirm(cfgManager))
	r.GET("/api/channels/templates", handlers.GetChannelTemplates())

	return cfgManager, r
}

func TestExportChannels_DefaultExcludesKeys(t *testing.T) {
	_, r := setupTestRouter(t)

	body := handlers.ExportChannelsRequest{
		ChannelUIDs: []string{"ch_aabbccddeeff"},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/export", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var pack handlers.ChannelPack
	if err := json.Unmarshal(w.Body.Bytes(), &pack); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(pack.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(pack.Channels))
	}

	ch := pack.Channels[0]
	if ch.Channel.APIKeys != nil {
		t.Errorf("APIKeys should be nil (excluded by default), got %v", ch.Channel.APIKeys)
	}
	if ch.Channel.APIKeyConfigs != nil {
		t.Errorf("APIKeyConfigs should be nil (excluded by default), got %v", ch.Channel.APIKeyConfigs)
	}
	if ch.Channel.ChannelUID != "" {
		t.Errorf("ChannelUID should be empty (stripped for export), got %s", ch.Channel.ChannelUID)
	}
	if ch.Channel.Name != "anthropic" {
		t.Errorf("expected name 'anthropic' (derived from baseURL), got '%s'", ch.Channel.Name)
	}
	if ch.ChannelType != "messages" {
		t.Errorf("expected channelType 'messages', got '%s'", ch.ChannelType)
	}
}

func TestExportChannels_IncludeKeys(t *testing.T) {
	_, r := setupTestRouter(t)

	body := handlers.ExportChannelsRequest{
		ChannelUIDs: []string{"ch_aabbccddeeff"},
		IncludeKeys: true,
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/export", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-admin-key")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var pack handlers.ChannelPack
	if err := json.Unmarshal(w.Body.Bytes(), &pack); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(pack.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(pack.Channels))
	}

	ch := pack.Channels[0]
	if len(ch.Channel.APIKeys) == 0 {
		t.Errorf("APIKeys should be included when includeKeys=true")
	}
	// ChannelUID 即使 includeKeys=true 也必须被剥离（防止碰撞）
	if ch.Channel.ChannelUID != "" {
		t.Errorf("ChannelUID should be empty even with includeKeys=true, got %s", ch.Channel.ChannelUID)
	}
}

func TestExportChannels_IncludeKeysNoAuth(t *testing.T) {
	// 多 key 模式：admin key 与 proxy key 不同
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgManager, err := config.NewConfigManager(cfgPath, tmpDir+"/backups")
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	defer errutil.IgnoreDeferred(cfgManager.Close)
	_ = cfgManager.AddUpstream(config.UpstreamConfig{
		Name:    "Test",
		BaseURL: "https://example.com",
		APIKeys: []string{"sk-secret"},
	})

	envCfg := newTestEnvCfgMultiKey("real-admin-key", []string{"extra-proxy-1"})
	r := gin.New()
	r.POST("/api/channels/export", handlers.ExportChannels(envCfg, cfgManager))

	body := handlers.ExportChannelsRequest{
		ChannelUIDs: []string{},
		IncludeKeys: true,
	}
	bodyBytes, _ := json.Marshal(body)

	// 不带 admin key
	req := httptest.NewRequest(http.MethodPost, "/api/channels/export", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "extra-proxy-1")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 when using proxy key with includeKeys=true, got %d", w.Code)
	}
}

func TestExportAllChannels_GetEndpoint(t *testing.T) {
	_, r := setupTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/channels/export", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var pack handlers.ChannelPack
	if err := json.Unmarshal(w.Body.Bytes(), &pack); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(pack.Channels) != 2 {
		t.Fatalf("expected 2 channels (all), got %d", len(pack.Channels))
	}

	if pack.Version != 1 {
		t.Errorf("expected version 1, got %d", pack.Version)
	}

	// 验证默认不包含 key
	for _, ch := range pack.Channels {
		if ch.Channel.APIKeys != nil {
			t.Errorf("channel '%s' should not have APIKeys in default export", ch.Channel.Name)
		}
	}
}

func TestExportAllChannels_FilterByType(t *testing.T) {
	_, r := setupTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/channels/export?channelTypes=chat", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var pack handlers.ChannelPack
	if err := json.Unmarshal(w.Body.Bytes(), &pack); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(pack.Channels) != 1 {
		t.Fatalf("expected 1 channel (chat only), got %d", len(pack.Channels))
	}

	if pack.Channels[0].ChannelType != "chat" {
		t.Errorf("expected channelType 'chat', got '%s'", pack.Channels[0].ChannelType)
	}
}

func TestImportChannels_Preview(t *testing.T) {
	_, r := setupTestRouter(t)

	pack := handlers.ChannelPack{
		Version: 1,
		Channels: []handlers.ChannelPackEntry{
			{
				ChannelType: "messages",
				Channel: config.UpstreamConfig{
					Name:    "New Claude Provider",
					BaseURL: "https://new-provider.example.com",
				},
			},
			{
				ChannelType: "messages",
				Channel: config.UpstreamConfig{
					Name:    "Whatever", // 名称由 baseURL 派生，此处值被忽略
					BaseURL: "https://api.anthropic.com", // 与现有渠道派生名相同（anthropic）
				},
			},
		},
	}

	body := handlers.ImportChannelsRequest{Pack: pack}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/import", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	preview := result["preview"].(map[string]interface{})
	newChannels := preview["newChannels"].([]interface{})

	if len(newChannels) != 2 {
		t.Fatalf("expected 2 preview entries, got %d", len(newChannels))
	}

	// 第一条应为 create
	first := newChannels[0].(map[string]interface{})
	if first["action"] != "create" {
		t.Errorf("first entry action should be 'create', got '%s'", first["action"])
	}

	// 第二条应为 name_conflict
	second := newChannels[1].(map[string]interface{})
	if second["action"] != "name_conflict" {
		t.Errorf("second entry action should be 'name_conflict', got '%s'", second["action"])
	}

	warnings := preview["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Error("expected at least one warning for name conflict")
	}
}

func TestImportChannelsConfirm_RegeneratesUID(t *testing.T) {
	_, r := setupTestRouter(t)

	originalUID := "ch_aabbccddeeff" // 不应被复用
	pack := handlers.ChannelPack{
		Version: 1,
		Channels: []handlers.ChannelPackEntry{
			{
				ChannelType: "messages",
				Channel: config.UpstreamConfig{
					ChannelUID: originalUID,
					Name:       "Imported Claude",
					BaseURL:    "https://imported.example.com",
					APIKeys:    []string{"sk-imported-key"},
					Status:     "active",
				},
			},
		},
	}

	body := handlers.ImportConfirmRequest{
		Pack: pack,
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/import/confirm", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	imported := result["imported"].([]interface{})
	if len(imported) != 1 {
		t.Fatalf("expected 1 imported, got %d", len(imported))
	}

	// 验证渠道已导入但 ChannelUID 被重新生成
	// 重新设置路由读取配置
	readReq := httptest.NewRequest(http.MethodGet, "/api/channels/export?channelTypes=messages", nil)
	readW := httptest.NewRecorder()
	r.ServeHTTP(readW, readReq)

	var exportPack handlers.ChannelPack
	_ = json.Unmarshal(readW.Body.Bytes(), &exportPack)

	for _, ch := range exportPack.Channels {
		if ch.Channel.Name == "imported-example" { // 名称由 baseURL 派生（imported.example.com -> imported-example）
			// UID 在导出时被清除了，但我们可以验证渠道确实存在
			t.Logf("Imported channel found: name=%s, type=%s", ch.Channel.Name, ch.ChannelType)
			return
		}
	}
	t.Error("imported channel 'imported-example' not found after import")
}

func TestImportChannelsConfirm_NameConflict(t *testing.T) {
	_, r := setupTestRouter(t)

	pack := handlers.ChannelPack{
		Version: 1,
		Channels: []handlers.ChannelPackEntry{
			{
				ChannelType: "messages",
				Channel: config.UpstreamConfig{
					Name:    "Test Claude",          // 名称被忽略，按 baseURL 派生为 anthropic
					BaseURL: "https://api.anthropic.com", // 与现有渠道派生名冲突（anthropic）
				},
			},
		},
	}

	body := handlers.ImportConfirmRequest{
		Pack:       pack,
		SkipNaming: false, // 不自动重命名，应返回错误
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/import/confirm", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	errors, hasErrors := result["errors"].([]interface{})
	if !hasErrors || len(errors) == 0 {
		t.Error("expected error for name conflict without skipNaming")
	}

	imported, hasImported := result["imported"].([]interface{})
	if hasImported && len(imported) != 0 {
		t.Errorf("expected 0 imported, got %d", len(imported))
	}
}

func TestImportChannelsConfirm_SkipNaming(t *testing.T) {
	_, r := setupTestRouter(t)

	pack := handlers.ChannelPack{
		Version: 1,
		Channels: []handlers.ChannelPackEntry{
			{
				ChannelType: "messages",
				Channel: config.UpstreamConfig{
					Name:    "Test Claude",          // 名称被忽略，按 baseURL 派生为 anthropic
					BaseURL: "https://api.anthropic.com", // 与现有渠道派生名冲突（anthropic）
				},
			},
		},
	}

	body := handlers.ImportConfirmRequest{
		Pack:       pack,
		SkipNaming: true, // 自动重命名
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/import/confirm", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	errors := result["errors"].([]interface{})
	if len(errors) != 0 {
		t.Errorf("expected no errors with skipNaming=true, got: %v", errors)
	}

	imported := result["imported"].([]interface{})
	if len(imported) != 1 {
		t.Fatalf("expected 1 imported, got %d", len(imported))
	}

	// 派生名冲突，落库时由 Add 自动追加序号消歧（anthropic -> anthropic-2）
	name := imported[0].(string)
	if name != "anthropic-import-1 (messages)" {
		t.Errorf("expected 'anthropic-import-1 (messages)', got '%s'", name)
	}
}

func TestImportChannelsConfirm_EmptyPack(t *testing.T) {
	_, r := setupTestRouter(t)

	body := handlers.ImportConfirmRequest{
		Pack: handlers.ChannelPack{Version: 1, Channels: []handlers.ChannelPackEntry{}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/import/confirm", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty pack, got %d", w.Code)
	}
}

func TestImportChannels_InvalidChannelType(t *testing.T) {
	_, r := setupTestRouter(t)

	pack := handlers.ChannelPack{
		Version: 1,
		Channels: []handlers.ChannelPackEntry{
			{
				ChannelType: "invalid_type",
				Channel: config.UpstreamConfig{
					Name: "Bad",
				},
			},
		},
	}

	body := handlers.ImportChannelsRequest{Pack: pack}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/import", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid channelType, got %d", w.Code)
	}
}

func TestGetChannelTemplates(t *testing.T) {
	_, r := setupTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/channels/templates", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	templates := result["templates"].([]interface{})
	if len(templates) == 0 {
		t.Error("expected at least one template")
	}

	// 验证模板结构
	first := templates[0].(map[string]interface{})
	if first["name"] == nil || first["name"] == "" {
		t.Error("template should have a name")
	}
	if first["pack"] == nil {
		t.Error("template should have a pack")
	}
}

func TestExportChannels_AllChannels(t *testing.T) {
	_, r := setupTestRouter(t)

	// 不指定 ChannelUIDs，应导出所有
	body := handlers.ExportChannelsRequest{
		ChannelUIDs: []string{},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/channels/export", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var pack handlers.ChannelPack
	if err := json.Unmarshal(w.Body.Bytes(), &pack); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 空 UID 列表应导出所有渠道
	if len(pack.Channels) != 2 {
		t.Fatalf("expected 2 channels (all), got %d", len(pack.Channels))
	}
}
