package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/session"
)

func newTestSchedulerWithMessagesChannels(t *testing.T, upstreams []config.UpstreamConfig) (*ChannelScheduler, *session.TraceAffinityManager) {
	t.Helper()

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.json")
	cfg := config.Config{
		Upstream:                  upstreams,
		UpstreamModelCapabilities: map[string]config.UpstreamModelCapability{},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cm, err := config.NewConfigManager(cfgPath, "")
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	ta := session.NewTraceAffinityManager()
	return NewChannelScheduler(cm, nil, nil, nil, nil, nil, ta, nil), ta
}

// 旧调用形态 SetTraceAffinity(userID, index, kind) 只有列表位置，也必须落库 ChannelUID：
// index 只是列表位置，渠道增删后会指向另一个渠道，只有 UID 能让删除时的
// RemoveByRoute(UID) 命中，读取侧也才能按稳定身份解析。
func TestSetTraceAffinityWritesChannelUIDForIndexOnlyCallers(t *testing.T) {
	sch, ta := newTestSchedulerWithMessagesChannels(t, []config.UpstreamConfig{
		{Name: "first", ChannelUID: "uid-first", APIKeys: []string{"sk-a"}},
		{Name: "second", ChannelUID: "uid-second", APIKeys: []string{"sk-b"}},
	})

	sch.SetTraceAffinity("user-x", 1, ChannelKindMessages)

	route, ok := ta.GetPreferredRoute(string(ChannelKindMessages)+":user-x", string(ChannelKindMessages))
	if !ok {
		t.Fatal("亲和条目应已写入")
	}
	if route.ChannelUID != "uid-second" {
		t.Fatalf("亲和写入必须带 ChannelUID，got %q", route.ChannelUID)
	}

	// 删除该渠道后，按 index 写入的条目也必须能被 UID 清理命中
	sch.DeleteChannelMetrics(&config.UpstreamConfig{Name: "second", ChannelUID: "uid-second"}, ChannelKindMessages)
	if _, ok := ta.GetPreferredRoute(string(ChannelKindMessages)+":user-x", string(ChannelKindMessages)); ok {
		t.Error("按 index 写入的亲和必须带 UID 并被删除清理命中")
	}
}

// 路由解析以 ChannelUID 为准：渠道删除或重排后 index 会指向另一个渠道，
// 只按 index 解析会把请求 pin 到错误的渠道。UID 不存在时回退 index（兼容旧记录）。
func TestGetUpstreamByRoutePrefersChannelUID(t *testing.T) {
	sch, _ := newTestSchedulerWithMessagesChannels(t, []config.UpstreamConfig{
		{Name: "first", ChannelUID: "uid-first", APIKeys: []string{"sk-a"}},
		{Name: "second", ChannelUID: "uid-second", APIKeys: []string{"sk-b"}},
	})

	// index 指向 first，但 UID 是 second：必须以 UID 为准
	upstream := sch.getUpstreamByRoute(ChannelRouteRef{Kind: string(ChannelKindMessages), Index: 0, ChannelUID: "uid-second"})
	if upstream == nil || upstream.Name != "second" {
		t.Fatalf("按 UID 解析应命中 second，got %+v", upstream)
	}

	// UID 已不存在（渠道被删/换）：回退按 index 解析，保持旧记录可用
	fallback := sch.getUpstreamByRoute(ChannelRouteRef{Kind: string(ChannelKindMessages), Index: 0, ChannelUID: "uid-gone"})
	if fallback == nil || fallback.Name != "first" {
		t.Fatalf("UID 未命中应回退 index 解析，got %+v", fallback)
	}
}
