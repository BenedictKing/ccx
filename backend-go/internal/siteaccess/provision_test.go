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
		writeEnvelope(w, true, map[string]interface{}{
			"default": map[string]interface{}{"desc": "", "ratio": f.groups["default"]},
			"vip":     map[string]interface{}{"desc": "", "ratio": f.groups["vip"]},
			"empty":   map[string]interface{}{"desc": "", "ratio": f.groups["empty"]},
		}, "")
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
		token := &NewApiToken{ID: f.nextID, Key: "plain-" + req.Name, Name: req.Name, Group: req.Group, Status: 1}
		f.tokens[f.nextID] = token
		f.nextID++
		writeEnvelope(w, true, token, "")
	case strings.HasPrefix(path, "/api/token/") && r.Method == http.MethodDelete:
		idStr := strings.TrimPrefix(path, "/api/token/")
		id, _ := strconv.Atoi(idStr)
		delete(f.tokens, id)
		writeEnvelope(w, true, nil, "")
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

	// 远端删除 ccx-default（chat 使用的 Key）；messages 与 chat 共用该 Key 名
	site.deleteTokenByName("ccx-default")

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
	if disabledCount != 2 {
		t.Fatalf("两个协议渠道中同名远端 Key 都应停用，实际 %d: %+v", disabledCount, actions)
	}

	cfg := cfgManager.GetConfig()
	if len(cfg.ChatUpstream[0].APIKeys) != 0 || len(cfg.ChatUpstream[0].DisabledAPIKeys) != 1 {
		t.Fatalf("chat 渠道 Key 应被停用: %+v", cfg.ChatUpstream[0])
	}
	if len(cfg.Upstream[0].APIKeys) != 0 || len(cfg.Upstream[0].DisabledAPIKeys) != 1 {
		t.Fatalf("messages 渠道 Key 应被停用: %+v", cfg.Upstream[0])
	}
}
