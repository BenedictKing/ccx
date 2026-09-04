package autopilot

import (
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// ── 五元组候选（渠道 × 协议 × key × 模型 × effort）专项测试 ──

func TestRoutingCandidateIdentityKey(t *testing.T) {
	cases := []struct {
		name string
		id   routingCandidateIdentity
		want string
	}{
		{
			name: "完整五元组",
			id:   routingCandidateIdentity{ChannelUID: "ch_a", Protocol: "responses", KeyIdentity: "kuid_1", QuotaGroup: "vip", Model: "Kimi-K3", Effort: EffortHigh},
			want: "ch_a|responses|kuid_1|kimi-k3|high",
		},
		{
			name: "无 key 维（fail-open 行）",
			id:   routingCandidateIdentity{ChannelUID: "ch_a", Protocol: "chat", Model: "m1"},
			want: "ch_a|chat|*|m1|*",
		},
		{
			name: "passthrough effort",
			id:   routingCandidateIdentity{ChannelUID: "ch_a", Protocol: "messages", KeyIdentity: "kh_abc", Model: "m1"},
			want: "ch_a|messages|kh_abc|m1|*",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.id.Key(); got != tc.want {
				t.Fatalf("Key() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeriveEffortChain(t *testing.T) {
	cases := []struct {
		name       string
		decided    EffortLevel
		decidedFlg bool
		supported  []EffortLevel
		want       []EffortLevel
	}{
		{name: "未决 passthrough 返回 nil", decided: "", decidedFlg: false, supported: []EffortLevel{EffortLow, EffortHigh}, want: nil},
		{name: "decided 标记但档位空", decided: "", decidedFlg: true, supported: []EffortLevel{EffortLow}, want: nil},
		{
			name: "已决档无更低档单档链", decided: EffortLow, decidedFlg: true,
			supported: []EffortLevel{EffortLow, EffortMedium}, want: []EffortLevel{EffortLow},
		},
		{
			name: "已决档带相邻降档", decided: EffortMedium, decidedFlg: true,
			supported: []EffortLevel{EffortLow, EffortMedium, EffortHigh}, want: []EffortLevel{EffortMedium, EffortLow},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveEffortChain(tc.decided, tc.decidedFlg, tc.supported)
			if len(got) != len(tc.want) {
				t.Fatalf("deriveEffortChain() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("deriveEffortChain() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestAdjacentEffortDownshift(t *testing.T) {
	if got := adjacentEffortDownshift([]EffortLevel{EffortLow, EffortMedium, EffortHigh, EffortXhigh}, EffortXhigh); got != EffortHigh {
		t.Errorf("xhigh 降档 = %q, want high", got)
	}
	// off 不参与降档（降档到 off 等于关闭思考，无档位语义）。
	if got := adjacentEffortDownshift([]EffortLevel{EffortOff, EffortMinimal}, EffortMinimal); got != "" {
		t.Errorf("minimal 档位池含 off 时降档 = %q, want 空", got)
	}
	if got := adjacentEffortDownshift([]EffortLevel{EffortMedium}, EffortMedium); got != "" {
		t.Errorf("最低档无降档 = %q, want 空", got)
	}
}

func TestResolvePinnedAPIKey(t *testing.T) {
	up := &config.UpstreamConfig{
		ChannelUID: "ch_pin",
		APIKeys:    []string{"sk-a", "sk-b"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-a", KeyUID: "kuid_a"},
			{Key: "sk-b"},
		},
	}
	if got := ResolvePinnedAPIKey(up, "kuid_a"); got != "sk-a" {
		t.Errorf("KeyUID 反查 = %q, want sk-a", got)
	}
	if got := ResolvePinnedAPIKey(up, "kh_"+KeyHashFromAPIKey("sk-b")); got != "sk-b" {
		t.Errorf("kh_ 哈希反查 = %q, want sk-b", got)
	}
	if got := ResolvePinnedAPIKey(up, "kuid_missing"); got != "" {
		t.Errorf("未命中应返回空，got %q", got)
	}
	if got := ResolvePinnedAPIKey(nil, "kuid_a"); got != "" {
		t.Errorf("nil upstream 应返回空，got %q", got)
	}
}

// explicitMultiKeyConfig 构建显式映射多 key 渠道（非 AutoManaged）。
func explicitMultiKeyConfig(t *testing.T, keys []config.APIKeyConfig, mapping map[string]string, supportedModels []string) (config.Config, *config.ConfigManager, func()) {
	t.Helper()
	plainKeys := make([]string, 0, len(keys))
	for _, k := range keys {
		plainKeys = append(plainKeys, k.Key)
	}
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "explicit-multi-key",
			ChannelUID:      "ch_explicit",
			BaseURL:         "https://relay.example.com",
			APIKeys:         plainKeys,
			APIKeyConfigs:   keys,
			Status:          "active",
			ModelMapping:    mapping,
			SupportedModels: supportedModels,
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{
			SchemaVersion: 99,
			RoutingMode:   "auto",
			ModelMapping:  config.ModelMappingRoutingConfig{AutoResolve: true},
		},
	}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	return cfg, cfgManager, cleanup
}

// 显式渠道 2 key：每 (模型, key) 展开独立候选行，CandidateKey 五段、
// KeyIdentity/QuotaGroup 来自 key 配置，显式渠道 effort passthrough（第 5 段 *）。
func TestExpandChannelCandidatesExplicitMultiKey(t *testing.T) {
	g2, g3 := 2.0, 3.0
	cfg, cfgManager, cleanup := explicitMultiKeyConfig(t, []config.APIKeyConfig{
		{Key: "sk-a", KeyUID: "kuid_a", QuotaGroup: "vip"},
		{Key: "sk-b"},
	}, map[string]string{"claude-opus-4-8": "claude-opus-4-8"}, []string{"claude-opus-4-8"})
	defer cleanup()
	_ = g2
	_ = g3
	_ = cfg
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	profile := BuildRequestProfile(RequestProfileFeatures{Model: "claude-opus-4-8", ChannelKind: "messages", Operation: "completion", EstTokens: 1000})
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(&profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)
	if len(resolutions) != 1 {
		t.Fatalf("显式同名映射应产 1 个模型行, got %d", len(resolutions))
	}
	ch := scheduler.ChannelInfo{Index: 0, Name: up.Name, Status: "active"}
	var got []channelScoreEntry
	got = router.expandChannelCandidates(ch, &up, "messages", scheduler.ChannelRouteRef{}, resolutions, nil, got, nil)
	if len(got) != 2 {
		t.Fatalf("2 key 应展开 2 行, got %d", len(got))
	}
	byIdentity := map[string]channelScoreEntry{}
	for _, e := range got {
		byIdentity[e.KeyIdentity] = e
	}
	a, ok := byIdentity["kuid_a"]
	if !ok {
		t.Fatalf("缺少 kuid_a 行: %+v", got)
	}
	if a.QuotaGroup != "vip" {
		t.Errorf("kuid_a 行 QuotaGroup = %q, want vip", a.QuotaGroup)
	}
	if want := "ch_explicit|messages|kuid_a|claude-opus-4-8|*"; a.CandidateKey != want {
		t.Errorf("kuid_a 行 CandidateKey = %q, want %q", a.CandidateKey, want)
	}
	b, ok := byIdentity["kh_"+KeyHashFromAPIKey("sk-b")]
	if !ok {
		t.Fatalf("缺少 sk-b 哈希身份行: %+v", got)
	}
	if b.QuotaGroup != "" {
		t.Errorf("sk-b 行 QuotaGroup 应为空, got %q", b.QuotaGroup)
	}
}

// 单渠道 64 行硬顶：9 模型映射 × 9 key = 81 > 64，截断保序。
func TestExpandChannelCandidatesRowLimit(t *testing.T) {
	keys := make([]config.APIKeyConfig, 0, 9)
	for i := 0; i < 9; i++ {
		keys = append(keys, config.APIKeyConfig{Key: "sk-" + string(rune('a'+i))})
	}
	mapping := map[string]string{}
	for i := 0; i < 9; i++ {
		m := "model-" + string(rune('a'+i))
		mapping[m] = m
	}
	supported := make([]string, 0, 9)
	for m := range mapping {
		supported = append(supported, m)
	}
	_, cfgManager, cleanup := explicitMultiKeyConfig(t, keys, mapping, supported)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	profile := BuildRequestProfile(RequestProfileFeatures{Model: "model-a", ChannelKind: "messages", Operation: "completion", EstTokens: 1000})
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(&profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)
	ch := scheduler.ChannelInfo{Index: 0, Name: up.Name, Status: "active"}
	var got []channelScoreEntry
	got = router.expandChannelCandidates(ch, &up, "messages", scheduler.ChannelRouteRef{}, resolutions, nil, got, nil)
	if len(got) != routingCandidateRowsPerChannelLimit {
		t.Fatalf("81 组合应截断到 %d 行, got %d", routingCandidateRowsPerChannelLimit, len(got))
	}
}

// 渠道无可用 key（APIKeys 空）：fail-open 产单行无 key 维，身份第 3 段 *。
func TestExpandChannelCandidatesNoKeysFailOpen(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "no-keys",
			ChannelUID:      "ch_nokey",
			BaseURL:         "https://x.example.com",
			Status:          "active",
			ModelMapping:    map[string]string{"claude-opus-4-8": "claude-opus-4-8"},
			SupportedModels: []string{"claude-opus-4-8"},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{SchemaVersion: 99, RoutingMode: "auto"},
	}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	profile := BuildRequestProfile(RequestProfileFeatures{Model: "claude-opus-4-8", ChannelKind: "messages", Operation: "completion", EstTokens: 1000})
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(&profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)
	if len(resolutions) != 1 {
		t.Fatalf("无 key 渠道解析行 = %d, want 1", len(resolutions))
	}
	ch := scheduler.ChannelInfo{Index: 0, Name: up.Name, Status: "active"}
	var got []channelScoreEntry
	got = router.expandChannelCandidates(ch, &up, "messages", scheduler.ChannelRouteRef{}, resolutions, nil, got, nil)
	if len(got) != 1 {
		t.Fatalf("fail-open 应产 1 行, got %d", len(got))
	}
	if want := "ch_nokey|messages|*|claude-opus-4-8|*"; got[0].CandidateKey != want {
		t.Errorf("无 key 行 CandidateKey = %q, want %q", got[0].CandidateKey, want)
	}
	if got[0].KeyIdentity != "" {
		t.Errorf("无 key 行 KeyIdentity 应为空, got %q", got[0].KeyIdentity)
	}
}

// testModelPricing 构造基准定价（输入/输出各 10/20 USD/M tok）。
func testModelPricing(t *testing.T) *config.ModelPricing {
	t.Helper()
	in := 10.0
	out := 20.0
	return &config.ModelPricing{InputCacheMissPrice: &in, OutputPrice: &out}
}

// per-key 成本：key 各自倍率进评分，不再取渠道内最小值。
func TestBuildChannelEntryForKeyPerKeyCost(t *testing.T) {
	m2 := 2.0
	m3 := 3.0
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "cost-split",
			ChannelUID:      "ch_cost",
			BaseURL:         "https://cost.example.com",
			APIKeys:         []string{"sk-cheap", "sk-dear"},
			APIKeyConfigs:   []config.APIKeyConfig{{Key: "sk-cheap"}, {Key: "sk-dear", GroupMultiplier: &m2, MaxGroupMultiplier: &m3, MultiplierSource: "manual"}},
			Status:          "active",
			ModelMapping:    map[string]string{"claude-opus-4-8": "claude-opus-4-8"},
			SupportedModels: []string{"claude-opus-4-8"},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{SchemaVersion: 99, RoutingMode: "auto"},
		// 注册表无该模型定价，显式注入 pricing 使成本路径可测。
		UpstreamModelCapabilities: map[string]config.UpstreamModelCapability{
			"claude-opus-4-8": {Pricing: testModelPricing(t)},
		},
	}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	up := cfgManager.GetConfig().Upstream[0]
	ch := scheduler.ChannelInfo{Index: 0, Name: up.Name, Status: "active"}
	caps := cfgManager.GetConfig().UpstreamModelCapabilities

	// 旧口径（无 key 维）：min 只在有倍率配置的 key 里取（cheap 无配置条目被
	// Normalize 过滤），因此 legacy = dear(×2) 的成本——无配置 key 的高性价比
	// 被有配置 key 的高倍率掩盖，这正是 per-key 修复的对象。
	legacy := router.buildChannelEntry(ch, &up, "messages", "claude-opus-4-8", caps)
	if legacy.EstimatedCost <= 0 {
		t.Fatalf("旧口径成本应 > 0, got %v", legacy.EstimatedCost)
	}
	dear := routingKeyCandidate{
		APIKey:      "sk-dear",
		KeyIdentity: "kh_" + KeyHashFromAPIKey("sk-dear"),
		KeyHash:     KeyHashFromAPIKey("sk-dear"),
		Config:      up.APIKeyConfigs[1],
	}
	dearEntry := router.buildChannelEntryForKey(ch, &up, "messages", "claude-opus-4-8", caps, &dear, nil, "")
	if dearEntry.EstimatedCost != legacy.EstimatedCost {
		t.Errorf("dear key(×2) 行成本 %v 应等于旧口径 min %v（dear 是唯一计价 key）", dearEntry.EstimatedCost, legacy.EstimatedCost)
	}
	// per-key：cheap key（无倍率）行成本独立计价，显著低于旧口径。
	cheap := routingKeyCandidate{
		APIKey:      "sk-cheap",
		KeyIdentity: "kh_" + KeyHashFromAPIKey("sk-cheap"),
		KeyHash:     KeyHashFromAPIKey("sk-cheap"),
		Config:      up.APIKeyConfigs[0],
	}
	cheapEntry := router.buildChannelEntryForKey(ch, &up, "messages", "claude-opus-4-8", caps, &cheap, nil, "")
	if cheapEntry.EstimatedCost >= legacy.EstimatedCost {
		t.Errorf("cheap key 行成本 %v 应低于旧口径 min %v（per-key 计价不再被高倍率 key 掩盖）", cheapEntry.EstimatedCost, legacy.EstimatedCost)
	}
}

// trace/plan 候选截断：全局上限 + 每渠道上限。
func TestTruncateTraceCandidates(t *testing.T) {
	// 未超上限原样返回。
	small := make([]RoutingCandidate, routingTraceCandidatesGlobalCap)
	for i := range small {
		small[i].ChannelUID = "ch_a"
	}
	got, truncated := truncateTraceCandidates(small)
	if truncated || len(got) != len(small) {
		t.Fatalf("未超上限不应截断: len=%d truncated=%v", len(got), truncated)
	}
	// 2 渠道 × 30 行 = 60 > 40：每渠道 top-5 → 10 行。
	big := make([]RoutingCandidate, 0, 60)
	for chIdx := 0; chIdx < 2; chIdx++ {
		for i := 0; i < 30; i++ {
			big = append(big, RoutingCandidate{ChannelUID: "ch_" + string(rune('a'+chIdx))})
		}
	}
	got, truncated = truncateTraceCandidates(big)
	if !truncated {
		t.Fatal("超上限应标记 truncated")
	}
	if len(got) != 2*routingTraceCandidatesPerChannelCap {
		t.Fatalf("截断后行数 = %d, want %d", len(got), 2*routingTraceCandidatesPerChannelCap)
	}
	perChannel := map[string]int{}
	for _, cand := range got {
		perChannel[cand.ChannelUID]++
	}
	for uid, n := range perChannel {
		if n != routingTraceCandidatesPerChannelCap {
			t.Fatalf("渠道 %s 保留 %d 行, want %d", uid, n, routingTraceCandidatesPerChannelCap)
		}
	}
	// plan 同口径。
	planBig := make([]RoutingPlanCandidate, 0, 60)
	for chIdx := 0; chIdx < 2; chIdx++ {
		for i := 0; i < 30; i++ {
			planBig = append(planBig, RoutingPlanCandidate{ScoredCandidate: ScoredCandidate{ChannelUID: "ch_" + string(rune('a'+chIdx))}})
		}
	}
	dropped, ok := truncatePlanCandidates(&planBig)
	if !ok || dropped != 60-2*routingTraceCandidatesPerChannelCap {
		t.Fatalf("plan 截断 dropped=%d ok=%v, want dropped=%d ok=true", dropped, ok, 60-2*routingTraceCandidatesPerChannelCap)
	}
}

// 执行 pin 回填：行档透传、意图固定档覆盖。
func TestApplyCandidatePin(t *testing.T) {
	ch := &scheduler.ChannelInfo{}
	entry := channelScoreEntry{KeyIdentity: "kuid_a", Effort: EffortHigh}
	applyCandidatePin(ch, entry, nil)
	if ch.PinnedKeyIdentity != "kuid_a" || ch.PinnedEffort != "high" {
		t.Fatalf("pin = %q/%q, want kuid_a/high", ch.PinnedKeyIdentity, ch.PinnedEffort)
	}
	// 意图固定档覆盖行档。
	pinned := &scheduler.ChannelInfo{}
	profile := &RequestProfile{IntentEffortPin: &IntentEffortPin{Effort: EffortXhigh, Set: true}}
	applyCandidatePin(pinned, entry, profile)
	if pinned.PinnedEffort != "xhigh" {
		t.Fatalf("意图 pin 应覆盖行档: %q, want xhigh", pinned.PinnedEffort)
	}
}

// AutoManaged 渠道 + effort 档：候选行身份第 5 段应携带已决档（解析器最低档路径）。
func TestExpandChannelCandidatesAutoManagedEffortSegment(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "effort-auto",
			ChannelUID:      "ch_effort",
			BaseURL:         "https://e.example.com",
			APIKeys:         []string{"sk-e"},
			Status:          "active",
			AutoManaged:     true,
			SupportedModels: []string{"*"},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{
			SchemaVersion: 99,
			RoutingMode:   "auto",
			ModelMapping:  config.ModelMappingRoutingConfig{AutoResolve: true, CapabilityFloorEnabled: true},
			ReasoningEffort: config.ReasoningEffortConfig{
				Enabled: true,
				PerTaskClass: map[string][]string{
					"worker":      {"medium", "high"},
					"lightweight": {"low"},
					"supervisor":  {"medium", "high"},
				},
			},
		},
	}
	store := &ModelProfileStore{cache: map[string]*ModelProfile{}, dirtyKeys: map[string]struct{}{}}
	upsertProfiles(t, store, ModelProfile{
		ChannelUID: "ch_effort", ChannelKind: "messages", MetricsKey: "me1",
		ModelID: "kimi-k3", ModelFamily: ModelFamilyKimi, QualityTier: QualityTierHigh,
		ContextTokens: 262_144, SupportsToolCalls: true, SupportsReasoning: true, ProbeSuccess: true,
		SupportsEffortControl: true, SupportedEffortLevels: []EffortLevel{EffortLow, EffortMedium, EffortHigh},
	})
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(store, cfgManager))

	profile := BuildRequestProfile(RequestProfileFeatures{Model: "kimi-k3", ChannelKind: "messages", Operation: "completion", EstTokens: 1000})
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(&profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)
	if len(resolutions) != 1 {
		t.Fatalf("AutoManaged 精确命中应产 1 个模型行, got %d", len(resolutions))
	}
	// effort 链已决时应非空（档位取决于 TaskClass 交集；这里只断言维度存在性）。
	if len(resolutions[0].EffortChain) == 0 {
		t.Fatalf("effort 展开应产生档位链: %+v", resolutions[0])
	}
	ch := scheduler.ChannelInfo{Index: 0, Name: up.Name, Status: "active"}
	var got []channelScoreEntry
	got = router.expandChannelCandidates(ch, &up, "messages", scheduler.ChannelRouteRef{}, resolutions, nil, got, nil)
	if len(got) == 0 {
		t.Fatal("展开行数不应为 0")
	}
	for _, e := range got {
		segs := strings.Split(e.CandidateKey, "|")
		if len(segs) != 5 {
			t.Fatalf("CandidateKey 应为五段: %q", e.CandidateKey)
		}
		if segs[4] == "*" {
			t.Fatalf("effort 链已决时候选行 effort 段不应为 *: %q", e.CandidateKey)
		}
	}
}

// per-key 画像匹配：该 key 有 endpoint 画像时行画像直接取该 key 的值（不聚合）；
// 无画像 key 回退渠道聚合口径。
func TestBuildChannelEntryForKeyProfileMatch(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "profile-split",
			ChannelUID:      "ch_profile",
			BaseURL:         "https://p.example.com",
			APIKeys:         []string{"sk-healthy", "sk-dead"},
			Status:          "active",
			ModelMapping:    map[string]string{"claude-opus-4-8": "claude-opus-4-8"},
			SupportedModels: []string{"claude-opus-4-8"},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{SchemaVersion: 99, RoutingMode: "auto"},
	}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	store, err := NewProfileStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	deadHash := KeyHashFromAPIKey("sk-dead")
	if err := store.Upsert(&KeyEndpointProfile{
		ChannelUID: "ch_profile", ChannelKind: "messages",
		BaseURL: "https://p.example.com", KeyHash: deadHash,
		KeyMask: "***dead", MetricsKey: "mk_dead",
		EndpointUID: GenerateEndpointUID("ch_profile", "https://p.example.com", deadHash),
		HealthState: HealthStateDead, OriginTier: "second",
		QualityTier: QualityTierLow, StabilityTier: StabilityTierUnstable, SpeedTier: SpeedTierSlow, CostTier: CostTierExpensive,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	router := NewSmartRouter(store, nil, nil, cfgManager)
	up := cfgManager.GetConfig().Upstream[0]
	ch := scheduler.ChannelInfo{Index: 0, Name: up.Name, Status: "active"}
	caps := cfgManager.GetConfig().UpstreamModelCapabilities
	keyProfiles := router.channelKeyProfiles("ch_profile", "messages")
	if len(keyProfiles) != 1 {
		t.Fatalf("预取画像索引应含 1 个 key, got %d", len(keyProfiles))
	}

	dead := routingKeyCandidate{APIKey: "sk-dead", KeyHash: deadHash, KeyIdentity: "kh_" + deadHash, Config: config.APIKeyConfig{Key: "sk-dead"}}
	deadEntry := router.buildChannelEntryForKey(ch, &up, "messages", "claude-opus-4-8", caps, &dead, keyProfiles, "")
	if deadEntry.HealthState != HealthStateDead {
		t.Errorf("dead key 行 HealthState = %q, want dead（key 级不再被聚合平均）", deadEntry.HealthState)
	}
	if deadEntry.KeyMetricsKey == "" || deadEntry.KeyMask != "***dead" {
		t.Errorf("dead key 行画像身份未透传: metricsKey=%q keyMask=%q", deadEntry.KeyMetricsKey, deadEntry.KeyMask)
	}

	// 无画像 key：回退渠道聚合先验（与旧口径一致）——同渠道其他 key 全灭时
	// 新 key/未探测 key 保守继承渠道状态；per-key 收益只给"有画像的 key"。
	healthyHash := KeyHashFromAPIKey("sk-healthy")
	healthy := routingKeyCandidate{APIKey: "sk-healthy", KeyHash: healthyHash, KeyIdentity: "kh_" + healthyHash, Config: config.APIKeyConfig{Key: "sk-healthy"}}
	healthyEntry := router.buildChannelEntryForKey(ch, &up, "messages", "claude-opus-4-8", caps, &healthy, keyProfiles, "")
	if healthyEntry.HealthState != HealthStateDead {
		t.Errorf("无画像 key 行应回退渠道聚合先验 dead, got %q", healthyEntry.HealthState)
	}
	if healthyEntry.KeyMetricsKey != "" {
		t.Errorf("无画像 key 行 KeyMetricsKey 应为空（无 key 级画像身份）, got %q", healthyEntry.KeyMetricsKey)
	}
}
