package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/ratelimit"
)

// activeRoutingCfg 返回 active 模式的路由配置，用于注入类用例。
func activeRoutingCfg() config.AutopilotRoutingConfig {
	cfg := config.DefaultAutopilotRoutingConfig()
	cfg.RoutingMode = config.AutopilotModeAuto
	cfg.RateLimitDiscovery.Enabled = true
	cfg.RateLimitDiscovery.ConfidenceThreshold = 0.7
	return cfg
}

// observeHeaderRPM 用 header 信号喂入高置信度（0.9）建议 RPM。
func observeHeaderRPM(d *RateLimitDiscoverer, endpointUID string, rpm int) {
	d.Observe(endpointUID, RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         rpm,
		WindowSeconds: 60,
	})
}

func TestRateLimitApplier_MultipleEndpointsSameLimiterTakesMin(t *testing.T) {
	cfg := activeRoutingCfg()
	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{})

	observeHeaderRPM(discoverer, "ep-a", 30)
	observeHeaderRPM(discoverer, "ep-b", 50)

	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: "ep-a", LimiterKey: "messages:0"},
		{EndpointUID: "ep-b", LimiterKey: "messages:0"},
	})
	applier.Apply()

	l := limiterMgr.Get("messages", 0)
	if l.GetRPM() != 30 {
		t.Fatalf("多 endpoint 同 limiter 应取最小 RPM=30, got %d", l.GetRPM())
	}
}

func TestRateLimitApplier_ScopedLimiterCreatedOnDemand(t *testing.T) {
	cfg := activeRoutingCfg()
	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	// 不预创建 scoped limiter

	observeHeaderRPM(discoverer, "ep-scoped", 30)
	scope := "key:abc123"
	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: "ep-scoped", LimiterKey: "messages:0:" + scope, LimiterConfig: ratelimit.Config{}},
	})
	applier.Apply()

	l := limiterMgr.GetScoped("messages", 0, scope)
	if l == nil {
		t.Fatal("scoped limiter 应被按需创建")
	}
	if l.GetRPM() != 30 {
		t.Fatalf("scoped limiter RPM=%d, want 30", l.GetRPM())
	}
	if !l.HasDiscoveredRPM() {
		t.Fatal("HasDiscoveredRPM should be true")
	}
}

func TestRateLimitApplier_ExplicitRPMNotOverwritten(t *testing.T) {
	cfg := activeRoutingCfg()
	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	// 显式配置 RPM=60
	l := limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{RPM: 60})

	observeHeaderRPM(discoverer, "ep-explicit", 30)
	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: "ep-explicit", LimiterKey: "messages:0", ExplicitRPM: true},
	})
	applier.Apply()

	if l.GetRPM() != 60 {
		t.Fatalf("显式 RPM 不应被覆盖: got %d, want 60", l.GetRPM())
	}
	if l.HasDiscoveredRPM() {
		t.Fatal("显式 RPM limiter 不应注入 discovered RPM")
	}
}

func TestRateLimitApplier_ShadowModeDoesNotInject(t *testing.T) {
	cfg := config.DefaultAutopilotRoutingConfig()
	cfg.RoutingMode = config.AutopilotModeShadow // shadow：只展示不注入
	cfg.RateLimitDiscovery.Enabled = true
	cfg.RateLimitDiscovery.ConfidenceThreshold = 0.7

	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{})

	observeHeaderRPM(discoverer, "ep-shadow", 30)
	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: "ep-shadow", LimiterKey: "messages:0"},
	})
	applier.Apply()

	l := limiterMgr.Get("messages", 0)
	if l.GetRPM() != 0 {
		t.Fatalf("shadow 模式不应注入 discovered RPM, got %d", l.GetRPM())
	}
}

func TestRateLimitApplier_EmptyInventoryClearsExisting(t *testing.T) {
	cfg := activeRoutingCfg()
	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{})

	observeHeaderRPM(discoverer, "ep-clear", 30)
	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: "ep-clear", LimiterKey: "messages:0"},
	})
	applier.Apply()
	l := limiterMgr.Get("messages", 0)
	if l.GetRPM() != 30 {
		t.Fatalf("pre: RPM=%d, want 30", l.GetRPM())
	}

	// inventory 为空：mapping 设空，Apply 应清理旧 limiterKey
	applier.SetEndpointMappings(nil)
	applier.Apply()

	if l.GetRPM() != 0 {
		t.Fatalf("空 inventory 应清理旧 discovered RPM, got %d", l.GetRPM())
	}
	if l.HasDiscoveredRPM() {
		t.Fatal("HasDiscoveredRPM should be false after clear")
	}
}

func TestRateLimitApplier_RemovedEndpointClearsOldLimiterKey(t *testing.T) {
	cfg := activeRoutingCfg()
	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{})
	limiterMgr.GetOrCreateScoped("messages", 0, "key:old", ratelimit.Config{})

	observeHeaderRPM(discoverer, "ep-old", 30)
	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: "ep-old", LimiterKey: "messages:0:key:old", LimiterConfig: ratelimit.Config{}},
	})
	applier.Apply()
	oldL := limiterMgr.GetScoped("messages", 0, "key:old")
	if oldL.GetRPM() != 30 {
		t.Fatalf("pre: old scoped RPM=%d, want 30", oldL.GetRPM())
	}

	// Key 轮换：mapping 改为新 scope
	observeHeaderRPM(discoverer, "ep-new", 40)
	limiterMgr.GetOrCreateScoped("messages", 0, "key:new", ratelimit.Config{})
	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: "ep-new", LimiterKey: "messages:0:key:new", LimiterConfig: ratelimit.Config{}},
	})
	applier.Apply()

	// 旧 limiterKey 的 discovered RPM 应被清理
	if oldL.GetRPM() != 0 {
		t.Fatalf("旧 limiterKey 应被清理, got RPM=%d", oldL.GetRPM())
	}
	if oldL.HasDiscoveredRPM() {
		t.Fatal("旧 limiterKey HasDiscoveredRPM should be false")
	}
	// 新 limiterKey 已注入
	newL := limiterMgr.GetScoped("messages", 0, "key:new")
	if newL.GetRPM() != 40 {
		t.Fatalf("新 limiterKey RPM=%d, want 40", newL.GetRPM())
	}
}

func TestRateLimitApplier_LastAppliedDedupedByLimiterKey(t *testing.T) {
	cfg := activeRoutingCfg()
	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{})

	observeHeaderRPM(discoverer, "ep-1", 30)
	observeHeaderRPM(discoverer, "ep-2", 30)
	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: "ep-1", LimiterKey: "messages:0"},
		{EndpointUID: "ep-2", LimiterKey: "messages:0"},
	})
	applier.Apply()

	applier.mu.Lock()
	applied := len(applier.lastApplied)
	applier.mu.Unlock()
	if applied != 1 {
		t.Fatalf("lastApplied 应按 limiterKey 去重为 1, got %d", applied)
	}
}

func TestRateLimitApplier_LowConfidenceSkipped(t *testing.T) {
	cfg := activeRoutingCfg()
	cfg.RateLimitDiscovery.ConfidenceThreshold = 0.7
	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{})

	// 用 429 无 Retry-After → confidence=0.5 < 0.7
	discoverer.Observe("ep-low", RateLimitSignal{Source: SignalSource429})
	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: "ep-low", LimiterKey: "messages:0"},
	})
	applier.Apply()

	l := limiterMgr.Get("messages", 0)
	if l.GetRPM() != 0 {
		t.Fatalf("低置信度应跳过, got RPM=%d", l.GetRPM())
	}
}

func TestRateLimitApplier_SixAPITypesScopedKeys(t *testing.T) {
	// 验证六类 API type 的 limiterKey 格式都能被 parseLimiterKey 正确解析
	cases := []struct {
		key     string
		apiType string
		idx     int
		scope   string
	}{
		{"Messages:0:key:abc", "Messages", 0, "key:abc"},
		{"Chat:1:quota:xyz", "Chat", 1, "quota:xyz"},
		{"Responses:2:key:abc", "Responses", 2, "key:abc"},
		{"Gemini:3:quota:xyz", "Gemini", 3, "quota:xyz"},
		{"Images:4:key:abc", "Images", 4, "key:abc"},
		{"Vectors:5:quota:xyz", "Vectors", 5, "quota:xyz"},
	}
	for _, c := range cases {
		apiType, idx, scope := parseLimiterKey(c.key)
		if apiType != c.apiType || idx != c.idx || scope != c.scope {
			t.Errorf("parseLimiterKey(%q) = (%q,%d,%q), want (%q,%d,%q)",
				c.key, apiType, idx, scope, c.apiType, c.idx, c.scope)
		}
	}
}
