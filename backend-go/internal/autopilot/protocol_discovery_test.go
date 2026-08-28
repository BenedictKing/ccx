package autopilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/utils"
)

// TestDiscoverEndpointProtocolsRecordsOnlyVerifiedModels 验证逐模型探测只把
// 探测成功的模型写入 ProtocolModels，而不是沿用整份 models 清单。
func TestDiscoverEndpointProtocolsRecordsOnlyVerifiedModels(t *testing.T) {
	var mu sync.Mutex
	probedModels := make(map[string]struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		probedModels[body.Model] = struct{}{}
		mu.Unlock()
		if body.Model == "gpt-supported" {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model is unavailable"}`))
	}))
	defer srv.Close()

	runner := NewAutoDiscoveryRunner(nil, nil)
	runner.client = srv.Client()
	result := EndpointDiscoveryResult{
		ProtocolOk: true,
		Models:     []string{"gpt-supported", "gpt-unsupported"},
	}
	// serviceType=responses 让 responses 是原生协议（用 models API 权威清单，不逐模型探测），
	// chat 是非原生协议，走逐模型探测路径。
	channel := &config.UpstreamConfig{ServiceType: "responses"}

	runner.discoverEndpointProtocols(context.Background(), channel, srv.URL, "sk-test", &result, nil)

	if got := strings.Join(result.ProtocolModels["responses"], ","); got != "gpt-supported,gpt-unsupported" {
		t.Fatalf("原生协议 responses 应沿用完整 models 清单，got=%q", got)
	}
	if got := strings.Join(result.ProtocolModels["chat"], ","); got != "gpt-supported" {
		t.Fatalf("chat 协议应只保留探测成功的模型子集，got=%q", got)
	}
	mu.Lock()
	_, supportedProbed := probedModels["gpt-supported"]
	_, unsupportedProbed := probedModels["gpt-unsupported"]
	mu.Unlock()
	if !supportedProbed || !unsupportedProbed {
		t.Fatalf("两个候选模型都应被逐个探测: supported=%v unsupported=%v", supportedProbed, unsupportedProbed)
	}
	if result.ProtocolDiscoverySource["chat"] != "protocol_model_probe" {
		t.Fatalf("chat source=%q, want protocol_model_probe", result.ProtocolDiscoverySource["chat"])
	}
	if !strings.Contains(result.ProtocolDiscoveryMessage["chat"], "1") {
		t.Fatalf("chat message 应包含成功计数, got=%q", result.ProtocolDiscoveryMessage["chat"])
	}
}

// TestDiscoverEndpointProtocolsRateLimitedNotCountedAsFailure 验证 429 不计入失败，
// 且不会污染成功模型子集。
func TestDiscoverEndpointProtocolsRateLimitedNotCountedAsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	runner := NewAutoDiscoveryRunner(nil, nil)
	runner.client = srv.Client()
	result := EndpointDiscoveryResult{
		ProtocolOk: true,
		Models:     []string{"gpt-a"},
	}
	channel := &config.UpstreamConfig{ServiceType: "responses"}

	runner.discoverEndpointProtocols(context.Background(), channel, srv.URL, "sk-test", &result, nil)

	if _, ok := result.ProtocolModels["chat"]; ok {
		t.Fatalf("限流不应产生任何已验证模型，got=%v", result.ProtocolModels["chat"])
	}
	if result.ProtocolDiscoveryError["chat"] == "" {
		t.Fatal("全部限流时应记录错误说明，避免误判为已探测成功")
	}
}

// TestPrioritizeProtocolProbeModelsCapsCandidateCount 验证候选模型截断按优先级前缀排序，
// 并遵守 limit 上限。
func TestPrioritizeProtocolProbeModelsCapsCandidateCount(t *testing.T) {
	models := []string{"unrelated-1", "gpt-5.4", "unrelated-2", "gpt-5.6-luna"}
	got := prioritizeProtocolProbeModels("chat", models, 2)
	if len(got) != 2 {
		t.Fatalf("截断后应剩 2 个模型, got=%v", got)
	}
	for _, model := range got {
		if !strings.HasPrefix(model, "gpt-") {
			t.Fatalf("chat 协议优先级前缀应优先选中 gpt- 模型, got=%v", got)
		}
	}
}

// TestParseModelsDeclaredEndpointTypes 验证解析 new-api 的 supported_endpoint_types
// 并映射为 CCX 协议名。
func TestParseModelsDeclaredEndpointTypes(t *testing.T) {
	body := []byte(`{"data":[
		{"id":"claude-sonnet-4-6-thinking","supported_endpoint_types":["anthropic","openai"]},
		{"id":"gpt-5.5","supported_endpoint_types":["openai","openai-response"]},
		{"id":"gemini-3-pro","supported_endpoint_types":["gemini"]},
		{"id":"text-embed","supported_endpoint_types":["embeddings"]},
		{"id":"no-field"}
	]}`)
	declared := parseModelsDeclaredEndpointTypes(body)

	if got := strings.Join(declared["claude-sonnet-4-6-thinking"], ","); got != "messages,chat" {
		t.Fatalf("anthropic+openai 应映射为 messages,chat, got=%q", got)
	}
	if got := strings.Join(declared["gpt-5.5"], ","); got != "chat,responses" {
		t.Fatalf("openai+openai-response 应映射为 chat,responses, got=%q", got)
	}
	if got := strings.Join(declared["gemini-3-pro"], ","); got != "gemini" {
		t.Fatalf("gemini 应映射为 gemini, got=%q", got)
	}
	// embeddings/jina-rerank 等不参与协议探测的枚举值应被忽略。
	if _, ok := declared["text-embed"]; ok {
		t.Fatalf("非探测协议的 endpoint type 不应产生条目: %v", declared["text-embed"])
	}
	if _, ok := declared["no-field"]; ok {
		t.Fatal("未声明该字段的模型不应产生条目")
	}
}

// TestPrioritizeProtocolProbeModelsPrefersDeclaredSupport 验证上游声明支持该协议的模型
// 在截断前被优先排入探测队列，即使它的名字前缀不在优先级表里。
func TestPrioritizeProtocolProbeModelsPrefersDeclaredSupport(t *testing.T) {
	models := []string{"gpt-5.4", "weird-name-model", "gpt-5.6"}
	declared := map[string][]string{"weird-name-model": {"messages"}}

	got := prioritizeProtocolProbeModelsWithDeclared("messages", models, declared, 1)
	if len(got) != 1 || got[0] != "weird-name-model" {
		t.Fatalf("声明支持 messages 的模型应优先于名字前缀规则, got=%v", got)
	}

	// 无声明信息时退化为既有前缀排序行为。
	if got := prioritizeProtocolProbeModelsWithDeclared("chat", models, nil, 1); got[0] != "gpt-5.4" {
		t.Fatalf("无声明信息时应保持前缀优先级行为, got=%v", got)
	}
}

// TestDiscoverEndpointProtocolsReusesSiblingModelsWhenListEmpty 验证 /v1/models 返回空清单时，
// 复用同一 (identityBaseURL, keyHash) 兄弟协议渠道画像里的模型作为探测候选。
// 同一 baseURL + Key 只有一份上游模型清单，兄弟渠道的已知清单描述的是同一个上游端点。
func TestDiscoverEndpointProtocolsReusesSiblingModelsWhenListEmpty(t *testing.T) {
	var mu sync.Mutex
	probed := make(map[string][]string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		probed[r.URL.Path] = append(probed[r.URL.Path], body.Model)
		mu.Unlock()
		// messages 与 chat 协议均支持 gpt-shared，responses 不支持。
		if r.URL.Path == "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatalf("NewProfileStore 失败: %v", err)
	}
	defer func() { _ = store.Close() }()

	// 兄弟渠道（chat）上一轮已探到模型，identityBaseURL + keyHash 与本渠道一致。
	sibling := newTestProfile("ep-chat", "ch-chat", "openai", srv.URL)
	sibling.KeyHash = KeyHashFromAPIKey("sk-test")
	sibling.IdentityBaseURL = utils.MetricsIdentityBaseURL(srv.URL, "openai")
	sibling.ProtocolModels = map[string][]string{"chat": {"gpt-shared"}}
	if err := store.Upsert(sibling); err != nil {
		t.Fatalf("Upsert 兄弟画像失败: %v", err)
	}

	runner := NewAutoDiscoveryRunner(store, nil)
	runner.client = srv.Client()
	// 本渠道 models API 返回空清单（HTTP 200 但 data 为空数组）。
	result := EndpointDiscoveryResult{ProtocolOk: true, Models: nil}
	channel := &config.UpstreamConfig{ServiceType: "claude", ChannelUID: "ch-claude"}

	runner.discoverEndpointProtocols(context.Background(), channel, srv.URL, "sk-test", &result, nil)

	if got := strings.Join(result.ProtocolModels["messages"], ","); got != "gpt-shared" {
		t.Fatalf("原生协议 messages 应复用兄弟渠道模型并实测通过, got=%q", got)
	}
	if len(result.ProtocolModels["responses"]) != 0 {
		t.Fatalf("responses 探测失败不应记录模型, got=%v", result.ProtocolModels["responses"])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(probed["/v1/messages"]) == 0 {
		t.Fatal("走兄弟渠道兜底时原生协议也必须逐模型探测，不能直接采信非本渠道实测的清单")
	}
}

// TestEnsureConfiguredProtocolDiscoveryKeepsProbedModels 验证空 models 清单不会覆盖
// 逐模型探测已得出的原生协议结论（该函数在探测前后各调用一次）。
func TestEnsureConfiguredProtocolDiscoveryKeepsProbedModels(t *testing.T) {
	result := EndpointDiscoveryResult{
		ProtocolOk:     true,
		Models:         nil,
		ProtocolModels: map[string][]string{"messages": {"gpt-shared"}},
	}
	ensureConfiguredProtocolDiscovery(&config.UpstreamConfig{ServiceType: "claude"}, &result)

	if got := strings.Join(result.ProtocolModels["messages"], ","); got != "gpt-shared" {
		t.Fatalf("空 models 清单不应覆盖已探测出的协议模型, got=%q", got)
	}
}

// TestDiscoverEndpointProtocolsReusesFreshStoredProbe 验证存量画像中 protocolProbeStaleTTL 内的
// 非原生协议实测结论被直接复用：不发探测请求，清单取 存量∩当前 key 级清单，
// 且如实保留存量实测时间/来源/说明（不刷新为当前时间）。
func TestDiscoverEndpointProtocolsReusesFreshStoredProbe(t *testing.T) {
	var mu sync.Mutex
	probedPaths := make(map[string]int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		probedPaths[r.URL.Path]++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatalf("NewProfileStore 失败: %v", err)
	}
	defer func() { _ = store.Close() }()

	// responses 是原生协议（采信清单，不逐模型探测）；chat 是非原生协议，走复用判定。
	channel := &config.UpstreamConfig{ServiceType: "responses", ChannelUID: "ch-reuse"}
	apiKey := "sk-test"
	endpointUID := GenerateEndpointUID(channel.ChannelUID, utils.CanonicalBaseURL(srv.URL, channel.ServiceType), KeyHashFromAPIKey(apiKey))
	storedAt := time.Now().Add(-2 * time.Hour).UTC()
	profile := newTestProfile(endpointUID, channel.ChannelUID, "responses", srv.URL)
	profile.ProtocolModels = map[string][]string{"chat": {"gpt-supported", "gpt-removed"}}
	profile.ProtocolDiscoveredAt = map[string]time.Time{"chat": storedAt}
	profile.ProtocolDiscoverySource = map[string]string{"chat": "protocol_model_probe"}
	profile.ProtocolDiscoveryMessage = map[string]string{"chat": "存量实测说明"}
	if err := store.Upsert(profile); err != nil {
		t.Fatalf("Upsert 存量画像失败: %v", err)
	}

	runner := NewAutoDiscoveryRunner(store, nil)
	runner.client = srv.Client()
	// 当前清单里 gpt-removed 已下线、gpt-new 新上线。
	result := EndpointDiscoveryResult{ProtocolOk: true, Models: []string{"gpt-supported", "gpt-new"}}

	runner.discoverEndpointProtocols(context.Background(), channel, srv.URL, apiKey, &result, nil)

	if got := strings.Join(result.ProtocolModels["chat"], ","); got != "gpt-supported" {
		t.Fatalf("chat 应复用 存量∩当前清单 的交集, got=%q", got)
	}
	if !result.ProtocolDiscoveredAt["chat"].Equal(storedAt) {
		t.Fatalf("复用应保留存量实测时间 %v, got=%v", storedAt, result.ProtocolDiscoveredAt["chat"])
	}
	if result.ProtocolDiscoverySource["chat"] != "protocol_model_probe" {
		t.Fatalf("复用应保留存量来源, got=%q", result.ProtocolDiscoverySource["chat"])
	}
	if result.ProtocolDiscoveryMessage["chat"] != "存量实测说明" {
		t.Fatalf("复用应保留存量说明, got=%q", result.ProtocolDiscoveryMessage["chat"])
	}
	// 原生协议 responses 仍由 ensureConfiguredProtocolDiscovery 采信完整清单，不受复用影响。
	if got := strings.Join(result.ProtocolModels["responses"], ","); got != "gpt-new,gpt-supported" {
		t.Fatalf("原生协议 responses 应采信完整清单, got=%q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if probedPaths["/v1/chat/completions"] != 0 {
		t.Fatalf("chat 在 TTL 内应跳过逐模型探测, 实际探测 %d 次", probedPaths["/v1/chat/completions"])
	}
}

// TestDiscoverEndpointProtocolsReprobesWhenStoredProbeNotReusable 表驱动覆盖存量实测
// 不可复用的情形——超过 TTL、与当前清单无交集、旧画像 map 字段缺失——均回退逐模型探测。
func TestDiscoverEndpointProtocolsReprobesWhenStoredProbeNotReusable(t *testing.T) {
	freshAt := time.Now().Add(-1 * time.Hour).UTC()
	staleAt := time.Now().Add(-(protocolProbeStaleTTL + time.Hour)).UTC()
	tests := []struct {
		name    string
		prepare func(p *KeyEndpointProfile)
	}{
		{
			name: "存量实测超过 7 天 TTL",
			prepare: func(p *KeyEndpointProfile) {
				p.ProtocolModels = map[string][]string{"chat": {"gpt-a"}}
				p.ProtocolDiscoveredAt = map[string]time.Time{"chat": staleAt}
			},
		},
		{
			name: "存量与当前清单无交集",
			prepare: func(p *KeyEndpointProfile) {
				p.ProtocolModels = map[string][]string{"chat": {"gpt-old"}}
				p.ProtocolDiscoveredAt = map[string]time.Time{"chat": freshAt}
			},
		},
		{
			name: "旧版本画像无协议 map 字段",
			prepare: func(p *KeyEndpointProfile) {
				p.ProtocolModels = nil
				p.ProtocolDiscoveredAt = nil
			},
		},
		{
			name: "有实测模型但缺实测时间戳",
			prepare: func(p *KeyEndpointProfile) {
				p.ProtocolModels = map[string][]string{"chat": {"gpt-a"}}
				p.ProtocolDiscoveredAt = nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			chatProbes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/chat/completions" {
					mu.Lock()
					chatProbes++
					mu.Unlock()
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()

			store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
			if err != nil {
				t.Fatalf("NewProfileStore 失败: %v", err)
			}
			defer func() { _ = store.Close() }()

			channel := &config.UpstreamConfig{ServiceType: "responses", ChannelUID: "ch-reprobe"}
			apiKey := "sk-test"
			endpointUID := GenerateEndpointUID(channel.ChannelUID, utils.CanonicalBaseURL(srv.URL, channel.ServiceType), KeyHashFromAPIKey(apiKey))
			profile := newTestProfile(endpointUID, channel.ChannelUID, "responses", srv.URL)
			tt.prepare(profile)
			if err := store.Upsert(profile); err != nil {
				t.Fatalf("Upsert 存量画像失败: %v", err)
			}

			runner := NewAutoDiscoveryRunner(store, nil)
			runner.client = srv.Client()
			result := EndpointDiscoveryResult{ProtocolOk: true, Models: []string{"gpt-a"}}

			runner.discoverEndpointProtocols(context.Background(), channel, srv.URL, apiKey, &result, nil)

			mu.Lock()
			defer mu.Unlock()
			if chatProbes == 0 {
				t.Fatal("存量实测不可复用时应回退逐模型探测")
			}
			// 探测成功（200）后 chat 采信本轮实测结果与新鲜时间戳。
			if got := strings.Join(result.ProtocolModels["chat"], ","); got != "gpt-a" {
				t.Fatalf("chat 应采信本轮探测结果, got=%q", got)
			}
			if time.Since(result.ProtocolDiscoveredAt["chat"]) > time.Minute {
				t.Fatalf("重探后实测时间应为当前时间, got=%v", result.ProtocolDiscoveredAt["chat"])
			}
		})
	}
}

// TestDiscoverEndpointProtocolsSkipsProtocolsWithNativeSibling 表驱动验证：同逻辑渠道组内
// 已存在某协议的原生兄弟上游时，本渠道不再对该协议逐模型探测（含 TTL 内的存量实测复用），
// result 不携带该协议条目（画像整体覆盖写入时清掉历史双口径数据），只留下兄弟渠道说明；
// 组内无原生兄弟时行为与改动前一致。
func TestDiscoverEndpointProtocolsSkipsProtocolsWithNativeSibling(t *testing.T) {
	freshAt := time.Now().Add(-1 * time.Hour).UTC()
	tests := []struct {
		name            string
		withChatSibling bool   // 配置里是否带同账号的 chat 原生兄弟渠道
		withStoredProbe bool   // 存量画像是否带 TTL 内的 chat 实测结论（复用路径同样应被跳过）
		wantChatProbed  bool   // 是否期望本渠道实际探测 chat 协议
		wantChatModels  string // 无兄弟时 chat 应采信的本轮实测模型
	}{
		{name: "有原生兄弟且有新鲜存量实测_不复用不探测", withChatSibling: true, withStoredProbe: true},
		{name: "有原生兄弟且无存量_不探测", withChatSibling: true},
		{name: "组内无兄弟_保持逐模型探测", wantChatProbed: true, wantChatModels: "gpt-a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mu sync.Mutex
			probedPaths := make(map[string]int)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				probedPaths[r.URL.Path]++
				mu.Unlock()
				switch r.URL.Path {
				case "/v1/chat/completions", "/v1/responses":
					_, _ = w.Write([]byte(`{"ok":true}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()

			chatUpstream := ""
			if tt.withChatSibling {
				chatUpstream = `{"accountUid":"acct_sib","channelUid":"ch_chat","name":"site-chat","serviceType":"openai","baseUrl":"` + srv.URL + `","baseUrls":["` + srv.URL + `"],"apiKeys":["sk-test"],"apiKeyConfigs":[{"key":"sk-test","credentialUid":"cred_chat","baseUrl":"` + srv.URL + `"}],"autoManaged":true,"status":"active"}`
			}
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.json")
			data := `{
  "managedAccounts": [],
  "upstream": [{"accountUid":"acct_sib","channelUid":"ch_msg","name":"site-messages","serviceType":"claude","baseUrl":"` + srv.URL + `","baseUrls":["` + srv.URL + `"],"apiKeys":["sk-test"],"apiKeyConfigs":[{"key":"sk-test","credentialUid":"cred_msg","baseUrl":"` + srv.URL + `"}],"autoManaged":true,"status":"active"}],
  "chatUpstream": [` + chatUpstream + `],
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

			store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
			if err != nil {
				t.Fatalf("NewProfileStore 失败: %v", err)
			}
			defer func() { _ = store.Close() }()

			channel := cfgManager.GetConfig().Upstream[0]
			apiKey := "sk-test"
			if tt.withStoredProbe {
				// 存量画像带 TTL 内的 chat 实测：没有兄弟时本应被复用（见
				// TestDiscoverEndpointProtocolsReusesFreshStoredProbe），有兄弟时必须跳过。
				endpointUID := GenerateEndpointUID(channel.ChannelUID, utils.CanonicalBaseURL(srv.URL, channel.ServiceType), KeyHashFromAPIKey(apiKey))
				profile := newTestProfile(endpointUID, channel.ChannelUID, "claude", srv.URL)
				profile.ProtocolModels = map[string][]string{"chat": {"gpt-a"}}
				profile.ProtocolDiscoveredAt = map[string]time.Time{"chat": freshAt}
				profile.ProtocolDiscoverySource = map[string]string{"chat": "protocol_model_probe"}
				profile.ProtocolDiscoveryMessage = map[string]string{"chat": "存量实测说明"}
				if err := store.Upsert(profile); err != nil {
					t.Fatalf("Upsert 存量画像失败: %v", err)
				}
			}

			runner := NewAutoDiscoveryRunner(store, nil)
			runner.client = srv.Client()
			result := EndpointDiscoveryResult{ProtocolOk: true, Models: []string{"gpt-a"}}

			runner.discoverEndpointProtocols(context.Background(), &channel, srv.URL, apiKey, &result, cfgManager)

			mu.Lock()
			chatProbes := probedPaths["/v1/chat/completions"]
			responsesProbes := probedPaths["/v1/responses"]
			mu.Unlock()

			if tt.withChatSibling {
				if chatProbes != 0 {
					t.Fatalf("chat 已有原生兄弟渠道，不应再逐模型探测, 实际探测 %d 次", chatProbes)
				}
				if _, ok := result.ProtocolModels["chat"]; ok {
					t.Fatalf("chat 应由兄弟渠道提供权威清单，本渠道不携带条目, got=%v", result.ProtocolModels["chat"])
				}
				if _, ok := result.ProtocolDiscoveredAt["chat"]; ok {
					t.Fatal("兄弟覆盖的协议不应设置探测时间")
				}
				if _, ok := result.ProtocolDiscoveryError["chat"]; ok {
					t.Fatal("兄弟覆盖的协议不应记录探测错误")
				}
				if msg := result.ProtocolDiscoveryMessage["chat"]; !strings.Contains(msg, "ch_chat") {
					t.Fatalf("应说明由兄弟渠道提供权威清单, got=%q", msg)
				}
			} else {
				if chatProbes == 0 {
					t.Fatal("组内无兄弟时 chat 应保持逐模型探测")
				}
				if got := strings.Join(result.ProtocolModels["chat"], ","); got != tt.wantChatModels {
					t.Fatalf("组内无兄弟时 chat 应采信本轮实测结果, got=%q", got)
				}
			}
			// 无兄弟的 responses 协议在两种情形下都照常探测。
			if responsesProbes == 0 {
				t.Fatal("无原生兄弟的 responses 协议应照常逐模型探测")
			}
			if got := strings.Join(result.ProtocolModels["responses"], ","); got != "gpt-a" {
				t.Fatalf("responses 应采信本轮实测结果, got=%q", got)
			}
			// 原生协议 messages 始终由清单采信。
			if got := strings.Join(result.ProtocolModels["messages"], ","); got != "gpt-a" {
				t.Fatalf("原生协议 messages 应采信完整清单, got=%q", got)
			}
		})
	}
}
