package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// 直测：document 硬约束理由（与 vision 约束同语义）。
func TestRoutingHardConstraintReasonsDocument(t *testing.T) {
	entry := &channelScoreEntry{SupportsDocument: false}
	reasons := routingHardConstraintReasons(&RequestProfile{DocumentNeed: true}, entry)
	if len(reasons) != 1 || reasons[0] != "document_unsupported" {
		t.Fatalf("routingHardConstraintReasons() = %v, want [document_unsupported]", reasons)
	}

	entry.SupportsDocument = true
	if reasons := routingHardConstraintReasons(&RequestProfile{DocumentNeed: true}, entry); len(reasons) != 0 {
		t.Fatalf("routingHardConstraintReasons() = %v, want []", reasons)
	}

	// 无文档需求时不触发约束
	entry.SupportsDocument = false
	if reasons := routingHardConstraintReasons(&RequestProfile{}, entry); len(reasons) != 0 {
		t.Fatalf("routingHardConstraintReasons() = %v, want []", reasons)
	}
}

// 注册表 document 能力经由 buildChannelEntry 注入硬约束（镜像 context window 接线测试）。
func TestResolvedDocumentCapabilityFeedsAutoHardConstraint(t *testing.T) {
	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{
		ChannelUID: "ch_doc",
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"doc-model": {Capabilities: map[string]bool{"document": true}},
		},
	}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "doc", Status: "active"},
		upstream,
		"messages",
		"doc-model",
		nil,
	)
	if !entry.SupportsDocument {
		t.Fatal("SupportsDocument should be true from registry capability")
	}
	if reasons := routingHardConstraintReasons(&RequestProfile{DocumentNeed: true}, &entry); len(reasons) != 0 {
		t.Fatalf("routingHardConstraintReasons() = %v, want []", reasons)
	}
}

// auto 模式：不支持 document 的渠道被硬约束过滤，支持渠道保留。
func TestAutoFiltersDocumentHardConstraintCandidates(t *testing.T) {
	const (
		model    = "auto-document-model"
		noDocUID = "ch_doc_missing"
		docUID   = "ch_doc_capable"
	)

	cfg := baseTestConfig()
	cfg.Upstream = cfg.Upstream[:2]
	cfg.Upstream[0].ChannelUID = noDocUID
	cfg.Upstream[0].ModelCapabilities = map[string]config.UpstreamModelCapability{
		model: {Capabilities: map[string]bool{"vision": true}},
	}
	cfg.Upstream[1].ChannelUID = docUID
	cfg.Upstream[1].ModelCapabilities = map[string]config.UpstreamModelCapability{
		model: {Capabilities: map[string]bool{"document": true}},
	}
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{RoutingMode: "auto"}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	traceStore, err := NewTraceStoreWithDB(nil)
	if err != nil {
		t.Fatalf("NewTraceStoreWithDB() error = %v", err)
	}
	router := NewSmartRouter(nil, nil, traceStore, cfgManager)
	filter, _ := router.CandidateFilterForWithActual(&RequestProfile{
		Model: model, ChannelKind: "messages", Operation: "completion",
		EstTokens: 1000, DocumentNeed: true,
	})
	if filter == nil {
		t.Fatal("auto mode should return filter")
	}

	processed := cfgManager.GetConfig()
	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: processed.Upstream[0].Name, Status: "active"},
		{Index: 1, Name: processed.Upstream[1].Name, Status: "active"},
	}
	result, err := filter(
		channels,
		func(ch scheduler.ChannelInfo) *config.UpstreamConfig { return &processed.Upstream[ch.Index] },
		func(_ scheduler.ChannelInfo, upstream *config.UpstreamConfig) bool { return upstream != nil },
	)
	if err != nil {
		t.Fatalf("auto filter error = %v", err)
	}
	if len(result) != 1 || result[0].Name != "ch-standard" {
		t.Fatalf("auto document filter result = %v, want only ch-standard", result)
	}

	traces := traceStore.ListRecent(1)
	if len(traces) != 1 {
		t.Fatalf("trace count = %d, want 1", len(traces))
	}
	trace := traces[0]
	if trace.SelectedChannelUID != docUID {
		t.Fatalf("selected channel = %q, want %q", trace.SelectedChannelUID, docUID)
	}
	if len(trace.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(trace.Candidates))
	}
	if trace.Candidates[0].ChannelUID != noDocUID || trace.Candidates[0].Selected ||
		len(trace.Candidates[0].FilterReasons) != 1 || trace.Candidates[0].FilterReasons[0] != "document_unsupported" {
		t.Fatalf("no-doc candidate trace = %+v", trace.Candidates[0])
	}
	if trace.Candidates[1].ChannelUID != docUID || !trace.Candidates[1].Selected {
		t.Fatalf("doc candidate trace = %+v", trace.Candidates[1])
	}
}

// auto 模式 fail-open：全部候选都不支持 document 时回退为只重排不过滤。
func TestAutoDocumentHardConstraintFailOpen(t *testing.T) {
	const model = "auto-document-model"

	cfg := baseTestConfig()
	cfg.Upstream = cfg.Upstream[:2]
	cfg.Upstream[0].ModelCapabilities = map[string]config.UpstreamModelCapability{
		model: {Capabilities: map[string]bool{"vision": true}},
	}
	cfg.Upstream[1].ModelCapabilities = map[string]config.UpstreamModelCapability{
		model: {Capabilities: map[string]bool{"vision": true}},
	}
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{RoutingMode: "auto"}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	traceStore, err := NewTraceStoreWithDB(nil)
	if err != nil {
		t.Fatalf("NewTraceStoreWithDB() error = %v", err)
	}
	router := NewSmartRouter(nil, nil, traceStore, cfgManager)
	filter, _ := router.CandidateFilterForWithActual(&RequestProfile{
		Model: model, ChannelKind: "messages", Operation: "completion",
		EstTokens: 1000, DocumentNeed: true,
	})
	if filter == nil {
		t.Fatal("auto mode should return filter")
	}

	processed := cfgManager.GetConfig()
	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: processed.Upstream[0].Name, Status: "active"},
		{Index: 1, Name: processed.Upstream[1].Name, Status: "active"},
	}
	result, err := filter(
		channels,
		func(ch scheduler.ChannelInfo) *config.UpstreamConfig { return &processed.Upstream[ch.Index] },
		func(_ scheduler.ChannelInfo, upstream *config.UpstreamConfig) bool { return upstream != nil },
	)
	if err != nil {
		t.Fatalf("auto filter error = %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("fail-open result = %v, want all 2 candidates kept", result)
	}
}

// stubLearnedDocumentUnsupported 用内存桩替换实测 document 不支持查询，避免测试依赖落盘的共享兼容性记忆。
func stubLearnedDocumentUnsupported(t *testing.T, unsupported map[string]bool) {
	t.Helper()
	original := learnedDocumentUnsupportedLookup
	learnedDocumentUnsupportedLookup = func(channelUID, model string) bool {
		return unsupported[channelUID+"|"+model]
	}
	t.Cleanup(func() { learnedDocumentUnsupportedLookup = original })
}

// 注册表支持 + 实测拒绝 → 收紧为不支持（中转商剥离附件正是此类场景）。
func TestLearnedDocumentUnsupportedOverridesRegistrySupport(t *testing.T) {
	stubLearnedDocumentUnsupported(t, map[string]bool{"ch_relay|doc-model": true})

	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{
		ChannelUID: "ch_relay",
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"doc-model": {Capabilities: map[string]bool{"document": true}},
		},
	}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "relay", Status: "active"},
		upstream,
		"messages",
		"doc-model",
		nil,
	)

	if entry.SupportsDocument {
		t.Fatal("SupportsDocument = true, want false（实测拒绝应覆盖注册表支持）")
	}
	reasons := routingHardConstraintReasons(&RequestProfile{DocumentNeed: true}, &entry)
	if len(reasons) != 1 || reasons[0] != "document_unsupported" {
		t.Fatalf("routingHardConstraintReasons() = %v, want [document_unsupported]", reasons)
	}

	// 无文档需求的请求不受学习结论影响
	if reasons := routingHardConstraintReasons(&RequestProfile{}, &entry); len(reasons) != 0 {
		t.Errorf("无文档需求不应被过滤, got %v", reasons)
	}
}

// 无学习记录 → 注册表结论不变（fail-open）。
func TestNoLearnedDocumentUnsupportedKeepsRegistry(t *testing.T) {
	stubLearnedDocumentUnsupported(t, nil)

	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{
		ChannelUID: "ch_fresh",
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"doc-model": {Capabilities: map[string]bool{"document": true}},
		},
	}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "fresh", Status: "active"},
		upstream,
		"messages",
		"doc-model",
		nil,
	)

	if !entry.SupportsDocument {
		t.Fatal("SupportsDocument = false, want true（无学习记录时注册表结论不应被改变）")
	}
	if reasons := routingHardConstraintReasons(&RequestProfile{DocumentNeed: true}, &entry); len(reasons) != 0 {
		t.Errorf("fail-open: got %v", reasons)
	}
}
