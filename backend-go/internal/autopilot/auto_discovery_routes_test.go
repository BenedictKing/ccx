package autopilot

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestMaybeEnableDiscoveredProtocolRoutesAddsUsableChannels(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts": [],
  "upstream": [], "chatUpstream": [],
  "responsesUpstream": [{"accountUid":"acct_custom","channelUid":"ch_responses","name":"custom-endpoint","serviceType":"responses","baseUrl":"https://example.com","baseUrls":["https://example.com"],"apiKeys":["sk-test"],"apiKeyConfigs":[{"key":"sk-test","credentialUid":"cred_test","baseUrl":"https://example.com"}],"autoManaged":true,"status":"active"}],
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
	runner := NewAutoDiscoveryRunner(store, nil)
	store.ReplaceActiveEndpointUIDs(buildEndpointInventory(cfgManager.GetConfig()).EndpointUIDs)
	source := cfgManager.GetConfig().ResponsesUpstream[0]
	discoveredAt := time.Date(2026, 7, 25, 5, 40, 2, 0, time.UTC)
	endpoints := []EndpointDiscoveryResult{{
		KeyMask: "sk-***est", BaseURL: "https://example.com", Models: []string{"model-a"}, ModelsCount: 1,
		ProtocolOk: true, apiKey: "sk-test", credentialUID: "cred_test",
		ProtocolModels: map[string][]string{
			"messages":  {"model-a", "claude-only"},
			"chat":      {"model-a", "chat-only"},
			"responses": {"model-a"},
		},
		ProtocolDiscoveredAt: map[string]time.Time{
			"messages": discoveredAt, "chat": discoveredAt, "responses": discoveredAt,
		},
		ProtocolDiscoverySource: map[string]string{
			"messages": "protocol_probe", "chat": "protocol_probe", "responses": "models_api",
		},
		ProtocolDiscoveryMessage: map[string]string{
			"messages": "messages ok", "chat": "chat ok", "responses": "responses ok",
		},
	}}

	runner.writeProfiles(source.ChannelUID, &source, endpoints, cfgManager)
	if err := runner.maybeEnableDiscoveredProtocolRoutes(&source, endpoints, cfgManager); err != nil {
		t.Fatalf("maybeEnableDiscoveredProtocolRoutes 失败: %v", err)
	}
	channels := cfgManager.GetAccountChannels("acct_custom")
	if len(channels) != 3 {
		t.Fatalf("渠道数量=%d, want 3: %+v", len(channels), channels)
	}
	kinds := make([]string, 0, len(channels))
	for _, channel := range channels {
		kinds = append(kinds, channel.Kind)
		if !strings.HasPrefix(channel.Upstream.Name, "custom-endpoint-") {
			t.Fatalf("多协议渠道名称未归一化: kind=%s name=%s", channel.Kind, channel.Upstream.Name)
		}
		if len(channel.Upstream.APIKeys) != 1 || channel.Upstream.APIKeys[0] != "sk-test" {
			t.Fatalf("新增渠道未复用账号凭证: kind=%s keys=%v", channel.Kind, channel.Upstream.APIKeys)
		}
	}
	sort.Strings(kinds)
	if got := strings.Join(kinds, ","); got != "chat,messages,responses" {
		t.Fatalf("渠道类型=%q", got)
	}

	for _, channel := range channels {
		profiles := store.ListActiveByChannel(channel.Upstream.ChannelUID)
		if len(profiles) != 1 {
			t.Fatalf("kind=%s profiles=%d, want 1", channel.Kind, len(profiles))
		}
		if channel.Kind == "messages" && strings.Join(profiles[0].AvailableModels, ",") != "model-a,claude-only" {
			t.Fatalf("Messages 画像模型错误: %v", profiles[0].AvailableModels)
		}
		if channel.Kind == "chat" && strings.Join(profiles[0].AvailableModels, ",") != "model-a,chat-only" {
			t.Fatalf("Chat 画像模型错误: %v", profiles[0].AvailableModels)
		}
	}

	if err := runner.maybeEnableDiscoveredProtocolRoutes(&source, endpoints, cfgManager); err != nil {
		t.Fatalf("重复执行应幂等: %v", err)
	}
	if got := len(cfgManager.GetAccountChannels("acct_custom")); got != 3 {
		t.Fatalf("重复执行后渠道数量=%d, want 3", got)
	}
}

// TestReconcileUnsupportedProtocolRoutesDisablesAndRestores 验证协议 0 个可用模型时
// 渠道被置为 disabled（备用池，退出调度但保留配置），重新探到模型后自动恢复 active。
func TestReconcileUnsupportedProtocolRoutesDisablesAndRestores(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts": [],
  "upstream": [{"accountUid":"acct_custom","channelUid":"ch_messages","name":"custom-endpoint-messages","serviceType":"claude","baseUrl":"https://example.com","baseUrls":["https://example.com"],"apiKeys":["sk-test"],"apiKeyConfigs":[{"key":"sk-test","credentialUid":"cred_test","baseUrl":"https://example.com"}],"autoManaged":true,"status":"active"}],
  "chatUpstream": [{"accountUid":"acct_custom","channelUid":"ch_chat","name":"custom-endpoint-chat","serviceType":"openai","baseUrl":"https://example.com","baseUrls":["https://example.com"],"apiKeys":["sk-test"],"apiKeyConfigs":[{"key":"sk-test","credentialUid":"cred_test","baseUrl":"https://example.com"}],"autoManaged":true,"status":"active"}],
  "responsesUpstream": [], "geminiUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
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
	runner := NewAutoDiscoveryRunner(store, nil)

	statusOf := func(kind string) string {
		for _, channel := range cfgManager.GetAccountChannels("acct_custom") {
			if channel.Kind == kind {
				return channel.Upstream.Status
			}
		}
		return ""
	}

	// chat 探到模型、messages 一个都没有：messages 下掉，chat 保持启用。
	runner.reconcileUnsupportedProtocolRoutes("acct_custom", map[string]bool{"chat": true}, cfgManager)
	if got := statusOf("messages"); got != "disabled" {
		t.Fatalf("messages 无可用模型应置为备用池, got=%q", got)
	}
	if got := statusOf("chat"); got != "active" {
		t.Fatalf("chat 有可用模型应保持启用, got=%q", got)
	}

	// 渠道配置必须保留，只是退出调度。
	if got := len(cfgManager.GetAccountChannels("acct_custom")); got != 2 {
		t.Fatalf("下掉协议不应删除渠道, 渠道数=%d", got)
	}

	// 下一轮重新探到 messages 模型：自动恢复。
	runner.reconcileUnsupportedProtocolRoutes("acct_custom", map[string]bool{"chat": true, "messages": true}, cfgManager)
	if got := statusOf("messages"); got != "active" {
		t.Fatalf("重新探到模型应恢复启用, got=%q", got)
	}
}

// TestReconcileUnsupportedProtocolRoutesKeepsManualSuspend 验证人工 suspended 的渠道
// 不被自动流程改写，避免覆盖用户意图。
func TestReconcileUnsupportedProtocolRoutesKeepsManualSuspend(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts": [],
  "upstream": [{"accountUid":"acct_custom","channelUid":"ch_messages","name":"custom-endpoint-messages","serviceType":"claude","baseUrl":"https://example.com","baseUrls":["https://example.com"],"apiKeys":["sk-test"],"apiKeyConfigs":[{"key":"sk-test","credentialUid":"cred_test","baseUrl":"https://example.com"}],"autoManaged":true,"status":"suspended"}],
  "chatUpstream": [], "responsesUpstream": [], "geminiUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
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
	runner := NewAutoDiscoveryRunner(store, nil)
	runner.reconcileUnsupportedProtocolRoutes("acct_custom", map[string]bool{"chat": true}, cfgManager)

	for _, channel := range cfgManager.GetAccountChannels("acct_custom") {
		if channel.Kind == "messages" && channel.Upstream.Status != "suspended" {
			t.Fatalf("人工 suspended 状态不应被自动流程改写, got=%q", channel.Upstream.Status)
		}
	}
}

func TestTriggerDiscoveryEnablesDetectedProtocolRoutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
		case req.Method == http.MethodPost && req.URL.Path == "/v1/messages":
			w.WriteHeader(http.StatusOK)
		case req.Method == http.MethodPost && req.URL.Path == "/v1/chat/completions":
			w.WriteHeader(http.StatusOK)
		case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/v1beta/models/"):
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("未预期的发现请求: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts": [],
  "upstream": [], "chatUpstream": [],
  "responsesUpstream": [{"accountUid":"acct_trigger","channelUid":"ch_trigger_responses","name":"trigger-endpoint","serviceType":"responses","baseUrl":"` + server.URL + `","baseUrls":["` + server.URL + `"],"apiKeys":["sk-test"],"apiKeyConfigs":[{"key":"sk-test","credentialUid":"cred_trigger","baseUrl":"` + server.URL + `"}],"autoManaged":true,"status":"active"}],
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

	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}
	modelStore, err := NewModelProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewModelProfileStoreWithDB 失败: %v", err)
	}
	runner := NewAutoDiscoveryRunner(store, nil)
	runner.ModelProfileStore = modelStore
	runner.client = server.Client()
	taskStore, err := NewDiscoveryTaskStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewDiscoveryTaskStoreWithDB 失败: %v", err)
	}
	runner.SetTaskStore(taskStore)
	t.Cleanup(runner.Stop)
	inventory := buildEndpointInventory(cfgManager.GetConfig())
	store.ReplaceActiveEndpointUIDs(inventory.EndpointUIDs)
	modelStore.ReplaceActiveBindings(inventory.ModelProfileBindings)

	source := cfgManager.GetConfig().ResponsesUpstream[0]
	if !runner.TriggerDiscovery(source.ChannelUID, &source, cfgManager) {
		t.Fatal("TriggerDiscovery 应启动发现")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task := runner.GetTask(source.ChannelUID)
		if task != nil && task.Status != DiscoveryStatusRunning {
			if task.Status != DiscoveryStatusDone {
				t.Fatalf("发现状态=%s, error=%s", task.Status, task.Error)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if task := runner.GetTask(source.ChannelUID); task == nil || task.Status == DiscoveryStatusRunning {
		t.Fatal("发现任务未在超时前完成")
	}

	channels := cfgManager.GetAccountChannels("acct_trigger")
	if len(channels) != 3 {
		t.Fatalf("发现完成后渠道数量=%d, want 3", len(channels))
	}
	kinds := make([]string, 0, len(channels))
	for _, channel := range channels {
		kinds = append(kinds, channel.Kind)
		if channel.Upstream.Status != "active" {
			t.Fatalf("协议渠道未启用: kind=%s status=%s", channel.Kind, channel.Upstream.Status)
		}
		profiles := store.ListActiveByChannel(channel.Upstream.ChannelUID)
		if len(profiles) != 1 || strings.Join(profiles[0].AvailableModels, ",") != "model-a" {
			t.Fatalf("协议渠道未立即写入可用画像: kind=%s profiles=%+v", channel.Kind, profiles)
		}
		modelProfiles := modelStore.ListActiveByChannel(channel.Upstream.ChannelUID)
		if len(modelProfiles) != 1 || modelProfiles[0].ModelID != "model-a" {
			t.Fatalf("协议渠道未立即进入模型路由清单: kind=%s profiles=%+v", channel.Kind, modelProfiles)
		}
	}
	sort.Strings(kinds)
	if got := strings.Join(kinds, ","); got != "chat,messages,responses" {
		t.Fatalf("自动启用协议=%q", got)
	}
}
