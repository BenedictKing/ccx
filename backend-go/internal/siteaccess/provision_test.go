package siteaccess

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// fakeNewApiSite 是有状态的 new-api 假站点：支持分组/模型查询与 token 的
// 创建、列表、删除，用于接入与对账的端到端契约测试。
type fakeNewApiSite struct {
	mu     sync.Mutex
	tokens map[int]*NewApiToken
	nextID int
	groups map[string]float64
	models map[string][]string // group → models
}

func newFakeNewApiSite() *fakeNewApiSite {
	return &fakeNewApiSite{
		tokens: make(map[int]*NewApiToken),
		nextID: 1,
		groups: map[string]float64{"default": 1.0, "vip": 2.0, "empty": 1.0},
		models: map[string][]string{
			"default": {"gpt-4o", "claude-sonnet-4"},
			"vip":     {"gpt-4o"},
			"empty":   {},
		},
	}
}

func (f *fakeNewApiSite) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	switch {
	case path == "/api/user/self":
		writeEnvelope(w, true, map[string]interface{}{"id": 1, "username": "tester"}, "")
	case path == "/api/user/self/groups":
		groups := map[string]any{}
		for name, ratio := range f.groups {
			groups[name] = map[string]any{"desc": "", "ratio": ratio}
		}
		writeEnvelope(w, true, groups, "")
	case path == "/api/user/models":
		group := r.URL.Query().Get("group")
		writeEnvelope(w, true, f.models[group], "")
	case path == "/api/token/" && r.Method == http.MethodGet:
		items := []NewApiToken{}
		for _, t := range f.tokens {
			items = append(items, *t)
		}
		writeEnvelope(w, true, map[string]interface{}{"items": items}, "")
	case path == "/api/token/" && r.Method == http.MethodPost:
		var req NewApiCreateTokenRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		token := &NewApiToken{
			ID: f.nextID, Key: "plain-" + req.Name, Name: req.Name, Group: req.Group, Status: 1,
			ModelLimitsEnabled: req.ModelLimitsEnabled, ModelLimits: req.ModelLimits,
		}
		f.tokens[f.nextID] = token
		f.nextID++
		writeEnvelope(w, true, token, "")
	case strings.HasPrefix(path, "/api/token/") && r.Method == http.MethodDelete:
		idStr := strings.TrimPrefix(path, "/api/token/")
		id, _ := strconv.Atoi(idStr)
		delete(f.tokens, id)
		writeEnvelope(w, true, nil, "")
	case path == "/v1/messages" || path == "/v1/chat/completions" || path == "/v1/responses":
		var body struct {
			Model string `json:"model"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		key := r.Header.Get("x-api-key")
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		for _, token := range f.tokens {
			if key != normalizeNewApiPlaintextKey(token.Key) || token.Status != 1 {
				continue
			}
			if token.ModelLimitsEnabled && !strings.Contains(","+token.ModelLimits+",", ","+body.Model+",") {
				continue
			}
			for _, model := range f.models[token.Group] {
				if model == body.Model {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"fake-response","choices":[{"message":{"role":"assistant","content":"pong"}}]}`))
					return
				}
			}
		}
		w.WriteHeader(http.StatusForbidden)
	default:
		writeEnvelope(w, false, nil, "unknown endpoint: "+r.Method+" "+path)
	}
}

func (f *fakeNewApiSite) deleteTokenByName(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, t := range f.tokens {
		if t.Name == name {
			delete(f.tokens, id)
		}
	}
}

func newProvisionTestDeps(t *testing.T) (*fakeNewApiSite, string, *AccountStore, *config.ConfigManager, *NewApiAdapter) {
	t.Helper()
	site := newFakeNewApiSite()
	srv := newMockNewApiServer(t, site.handler)
	store := NewAccountStore(filepath.Join(t.TempDir(), "accounts.json"))
	cfgManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"), "")
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })
	uid, err := store.Upsert(SiteAccount{
		SiteType:    "newapi",
		BaseURL:     srv.URL,
		UserID:      "1",
		AccessToken: "test-access-token",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return site, uid, store, cfgManager, &NewApiAdapter{}
}

func groupResultsByName(result *ProvisionResult) map[string]ProvisionGroupResult {
	out := make(map[string]ProvisionGroupResult)
	for _, g := range result.Groups {
		out[g.Group] = g
	}
	return out
}

func TestProvisionProtocol_CreatesOnlySelectedProtocolChannels(t *testing.T) {
	_, uid, store, cfgManager, adapter := newProvisionTestDeps(t)

	result, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid,
		Protocol:   "chat",
		Models:     []string{"gpt-4o"},
	})
	if err != nil {
		t.Fatalf("ProvisionProtocol: %v", err)
	}
	byGroup := groupResultsByName(result)

	if g := byGroup["default"]; g.Skipped != "" {
		t.Fatalf("default 分组应接入成功，实际跳过: %s", g.Skipped)
	}
	if _, ok := byGroup["vip"]; ok {
		t.Fatalf("vip 倍率 2.0 超过默认上限，应在分组解析时被过滤而不进入接入")
	}
	if g := byGroup["empty"]; g.Skipped == "" {
		t.Fatalf("empty 分组无模型，应被跳过")
	}

	cfg := cfgManager.GetConfig()
	if len(cfg.ChatUpstream) != 1 {
		t.Fatalf("应只建 1 个 chat 渠道，实际 %d", len(cfg.ChatUpstream))
	}
	if len(cfg.Upstream)+len(cfg.ResponsesUpstream)+len(cfg.GeminiUpstream) != 0 {
		t.Fatalf("不应触碰其他协议配置数组")
	}
	channel := cfg.ChatUpstream[0]
	if channel.Name != ProvisionedChannelName(cfg.ChatUpstream[0].BaseURL, "chat", "default") {
		t.Fatalf("渠道命名不符合约定: %q", channel.Name)
	}
	if len(channel.APIKeys) != 1 || !strings.HasPrefix(channel.APIKeys[0], "sk-") {
		t.Fatalf("Key 应带 sk- 前缀: %v", channel.APIKeys)
	}
	if channel.ServiceType != "openai" {
		t.Fatalf("chat 渠道 ServiceType 应为 openai: %q", channel.ServiceType)
	}

	// Key 元数据已记录且含指纹
	account, _, _ := store.Get(uid)
	if len(account.ProvisionedKeys) != 1 || account.ProvisionedKeys[0].KeyFingerprint == "" {
		t.Fatalf("Key 元数据未正确记录: %+v", account.ProvisionedKeys)
	}
}

func TestProvisionProtocol_TargetModelMissingCreatesNoChannel(t *testing.T) {
	_, uid, store, cfgManager, adapter := newProvisionTestDeps(t)

	result, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid,
		Protocol:   "messages",
		Models:     []string{"nonexistent-model"},
	})
	if err != nil {
		t.Fatalf("ProvisionProtocol: %v", err)
	}
	for _, g := range result.Groups {
		if g.Skipped == "" {
			t.Fatalf("分组 %s 不含目标模型，不应接入成功", g.Group)
		}
	}
	if len(cfgManager.GetConfig().Upstream) != 0 {
		t.Fatalf("目标模型缺失时不应创建渠道")
	}
}

func TestProvisionProtocol_IdempotentReprovision(t *testing.T) {
	_, uid, store, cfgManager, adapter := newProvisionTestDeps(t)
	req := ProvisionRequest{AccountUID: uid, Protocol: "chat", Models: []string{"gpt-4o"}}

	first, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, req)
	if err != nil {
		t.Fatalf("首次接入: %v", err)
	}
	second, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, req)
	if err != nil {
		t.Fatalf("重复接入: %v", err)
	}

	if len(cfgManager.GetConfig().ChatUpstream) != 1 {
		t.Fatalf("重复接入不应产生重复渠道")
	}
	firstKeys := cfgManager.GetConfig().ChatUpstream[0].APIKeys
	if len(firstKeys) != 1 {
		t.Fatalf("重复接入不应产生重复 Key: %v", firstKeys)
	}
	firstByGroup := groupResultsByName(first)
	secondByGroup := groupResultsByName(second)
	if firstByGroup["default"].KeyName != secondByGroup["default"].KeyName {
		t.Fatalf("重复接入应复用同名 Key: %q vs %q", firstByGroup["default"].KeyName, secondByGroup["default"].KeyName)
	}
	if !secondByGroup["default"].Reused {
		t.Fatalf("重复接入应标记复用远端 Key")
	}
	account, _, _ := store.Get(uid)
	if len(account.ProvisionedKeys) != 1 {
		t.Fatalf("重复接入元数据应幂等: %+v", account.ProvisionedKeys)
	}
}

func TestProvisionProtocol_CrossProtocolIndependentGroups(t *testing.T) {
	_, uid, store, cfgManager, adapter := newProvisionTestDeps(t)

	maxMultiplier := 2.0
	if _, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid, Protocol: "messages", Groups: []string{"default"}, Models: []string{"claude-sonnet-4"},
	}); err != nil {
		t.Fatalf("messages 接入: %v", err)
	}
	if _, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid, Protocol: "chat", Groups: []string{"vip"}, Models: []string{"gpt-4o"}, MaxMultiplier: &maxMultiplier,
	}); err != nil {
		t.Fatalf("chat 接入: %v", err)
	}

	cfg := cfgManager.GetConfig()
	if len(cfg.Upstream) != 1 || len(cfg.ChatUpstream) != 1 {
		t.Fatalf("跨协议渠道应各自独立: messages=%d chat=%d", len(cfg.Upstream), len(cfg.ChatUpstream))
	}
	messagesKey := cfg.Upstream[0].APIKeys[0]
	chatKey := cfg.ChatUpstream[0].APIKeys[0]
	if messagesKey == chatKey {
		t.Fatalf("不同分组应使用不同远端 Key: %q", messagesKey)
	}
	if cfg.Upstream[0].ServiceType != "claude" {
		t.Fatalf("messages 渠道 ServiceType 应为 claude: %q", cfg.Upstream[0].ServiceType)
	}
	account, _, _ := store.Get(uid)
	if len(account.ProvisionedKeys) != 2 {
		t.Fatalf("应记录两条 Key 元数据: %+v", account.ProvisionedKeys)
	}
}

func TestProvisionProtocol_SameGroupKeepsProtocolKeysIndependent(t *testing.T) {
	site, uid, store, cfgManager, adapter := newProvisionTestDeps(t)
	for _, req := range []ProvisionRequest{
		{AccountUID: uid, Protocol: "messages", Groups: []string{"default"}, Models: []string{"claude-sonnet-4"}},
		{AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"}},
	} {
		result, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, req)
		if err != nil || len(result.Groups) != 1 || result.Groups[0].Skipped != "" {
			t.Fatalf("%s 接入失败: result=%+v err=%v", req.Protocol, result, err)
		}
	}
	cfg := cfgManager.GetConfig()
	if cfg.Upstream[0].APIKeys[0] == cfg.ChatUpstream[0].APIKeys[0] {
		t.Fatal("同一分组的不同协议不应复用模型受限 Key")
	}
	account, _, _ := store.Get(uid)
	if len(account.ProvisionedKeys) != 2 || len(account.Evidence) != 2 {
		t.Fatalf("两个协议的元数据与证据应独立保留: %+v", account)
	}
	if len(site.tokens) != 2 {
		t.Fatalf("远端应有两个独立 Key，实际 %d", len(site.tokens))
	}
}

func TestProvisionProtocol_FiltersModelsPerGroupAndRetainsAllMetadata(t *testing.T) {
	site, uid, store, cfgManager, adapter := newProvisionTestDeps(t)
	maxMultiplier := 2.0
	result, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid, Protocol: "chat", Groups: []string{"default", "vip"}, MaxMultiplier: &maxMultiplier,
		Models:       []string{"gpt-4o", "claude-sonnet-4"},
		ModelMapping: map[string]string{"agent-gpt": "gpt-4o", "agent-claude": "claude-sonnet-4"},
	})
	if err != nil {
		t.Fatalf("接入失败: %v", err)
	}
	for _, group := range result.Groups {
		if group.Skipped != "" {
			t.Fatalf("%s 意外跳过: %s", group.Group, group.Skipped)
		}
	}
	channels := cfgManager.GetConfig().ChatUpstream
	if len(channels) != 2 {
		t.Fatalf("应有两个 chat 渠道，实际 %d", len(channels))
	}
	for _, channel := range channels {
		if strings.HasSuffix(channel.Name, "/ vip") {
			if channel.SupportsModel("agent-claude") || channel.SupportsModel("claude-sonnet-4") || !channel.SupportsModel("agent-gpt") {
				t.Fatalf("vip 渠道模型过滤错误: %+v", channel)
			}
			if _, ok := channel.ModelMapping["agent-claude"]; ok {
				t.Fatalf("vip 渠道不应映射不可用的 claude 模型")
			}
		}
	}
	account, _, _ := store.Get(uid)
	if len(account.ProvisionedKeys) != 2 || len(account.Evidence) != 2 || len(site.tokens) != 2 {
		t.Fatalf("多分组元数据丢失: keys=%d evidence=%d remote=%d", len(account.ProvisionedKeys), len(account.Evidence), len(site.tokens))
	}
}

func TestProvisionProtocol_ChangingModelsReplacesManagedLocalKey(t *testing.T) {
	_, uid, store, cfgManager, adapter := newProvisionTestDeps(t)
	for _, model := range []string{"gpt-4o", "claude-sonnet-4"} {
		result, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
			AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{model},
		})
		if err != nil {
			t.Fatalf("%s 接入失败: %v", model, err)
		}
		if result.Groups[0].Skipped != "" {
			t.Fatalf("%s 意外跳过: %s", model, result.Groups[0].Skipped)
		}
	}
	channel := cfgManager.GetConfig().ChatUpstream[0]
	if len(channel.APIKeys) != 1 || !channel.SupportsModel("claude-sonnet-4") || channel.SupportsModel("gpt-4o") {
		t.Fatalf("重新选模后应只保留新 Key 与模型范围: %+v", channel)
	}
	account, _, _ := store.Get(uid)
	if len(account.ProvisionedKeys) != 1 || account.ProvisionedKeys[0].KeyFingerprint != keyFingerprint(channel.APIKeys[0]) {
		t.Fatalf("Key 元数据未更新: %+v", account.ProvisionedKeys)
	}
}

func TestProvisionProtocol_ThreeProtocolsCompleteLocalInference(t *testing.T) {
	_, uid, store, cfgManager, adapter := newProvisionTestDeps(t)
	for _, tc := range []struct {
		protocol string
		model    string
	}{
		{"messages", "claude-sonnet-4"},
		{"responses", "gpt-4o"},
		{"chat", "gpt-4o"},
	} {
		result, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
			AccountUID: uid, Protocol: tc.protocol, Groups: []string{"default"}, Models: []string{tc.model},
		})
		if err != nil {
			t.Fatalf("%s 接入失败: %v", tc.protocol, err)
		}
		if result.Groups[0].Skipped != "" {
			t.Fatalf("%s 接入跳过: %s", tc.protocol, result.Groups[0].Skipped)
		}
		if err := VerifyRealRequest(context.Background(), store, cfgManager, uid, tc.protocol, "default", tc.model); err != nil {
			t.Fatalf("%s 最小推理失败: %v", tc.protocol, err)
		}
	}
	account, _, _ := store.Get(uid)
	if len(account.ProvisionedKeys) != 3 || len(account.Evidence) != 3 {
		t.Fatalf("三个协议的接入记录不完整: keys=%d evidence=%d", len(account.ProvisionedKeys), len(account.Evidence))
	}
	for _, evidence := range account.Evidence {
		if evidence.Level != EvidenceRealRequest {
			t.Fatalf("%s/%s 未达到真实请求证据: %+v", evidence.Protocol, evidence.Group, evidence)
		}
	}
}

func TestReconcileAccount_RemoteDeletedKeyDisabledLocally(t *testing.T) {
	site, uid, store, cfgManager, adapter := newProvisionTestDeps(t)

	if _, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"},
	}); err != nil {
		t.Fatalf("接入: %v", err)
	}
	if _, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid, Protocol: "messages", Groups: []string{"default"}, Models: []string{"claude-sonnet-4"},
	}); err != nil {
		t.Fatalf("messages 接入: %v", err)
	}

	// 对账前：远端存在，无动作
	actions, err := ReconcileAccount(context.Background(), store, cfgManager, adapter, uid)
	if err != nil {
		t.Fatalf("对账: %v", err)
	}
	for _, a := range actions {
		if a.Action == "disabled" {
			t.Fatalf("远端存在时不应停用: %+v", a)
		}
	}

	account, _, _ := store.Get(uid)
	var chatKeyName string
	for _, meta := range account.ProvisionedKeys {
		if meta.Protocol == "chat" {
			chatKeyName = meta.Name
		}
	}
	site.deleteTokenByName(chatKeyName)

	actions, err = ReconcileAccount(context.Background(), store, cfgManager, adapter, uid)
	if err != nil {
		t.Fatalf("对账: %v", err)
	}
	disabledCount := 0
	for _, a := range actions {
		if a.Action == "disabled" {
			disabledCount++
		}
	}
	if disabledCount != 1 {
		t.Fatalf("仅删除的 chat Key 应停用，实际 %d: %+v", disabledCount, actions)
	}

	cfg := cfgManager.GetConfig()
	if len(cfg.ChatUpstream[0].APIKeys) != 0 || len(cfg.ChatUpstream[0].DisabledAPIKeys) != 1 {
		t.Fatalf("chat 渠道 Key 应被停用: %+v", cfg.ChatUpstream[0])
	}
	if len(cfg.Upstream[0].APIKeys) != 1 || len(cfg.Upstream[0].DisabledAPIKeys) != 0 {
		t.Fatalf("messages 渠道 Key 应保持可用: %+v", cfg.Upstream[0])
	}
}
