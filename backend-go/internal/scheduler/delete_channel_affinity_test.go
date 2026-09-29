package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/session"
)

// 渠道删除时应同步清理指向它的会话亲和条目：亲和原本只能等 TTL
// （默认 30 分钟）过期，期间 sweep 路径（alpha 端点按亲和渠道名 pin）
// 仍会指向已删渠道。其他渠道的亲和不受影响。
func TestDeleteChannelMetrics_RemovesTraceAffinityForDeletedChannel(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.json")
	cfg := config.Config{
		Upstream:                  []config.UpstreamConfig{},
		UpstreamModelCapabilities: map[string]config.UpstreamModelCapability{},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cm, err := config.NewConfigManager(cfgPath, "")
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	defer func() { _ = cm.Close() }()

	ta := session.NewTraceAffinityManager()
	sch := NewChannelScheduler(cm, nil, nil, nil, nil, nil, ta, nil)

	deletedRoute := ChannelRouteRef{Kind: string(ChannelKindMessages), Index: 2, ChannelUID: "ch_deleted_uid"}
	keptRoute := ChannelRouteRef{Kind: string(ChannelKindMessages), Index: 5, ChannelUID: "ch_kept_uid"}

	// 两个用户分别亲和到将被删除与保留的渠道
	sch.SetTraceAffinityRouteForRequirement("user-a", deletedRoute, ChannelKindMessages, nil)
	sch.SetTraceAffinityRouteForRequirement("user-b", keptRoute, ChannelKindMessages, nil)

	// 跨协议同物理渠道的亲和也应一并清理（UID 是跨协议身份）
	crossKindRoute := ChannelRouteRef{Kind: string(ChannelKindResponses), Index: 1, ChannelUID: "ch_deleted_uid"}
	sch.SetTraceAffinityRouteForRequirement("user-c", crossKindRoute, ChannelKindResponses, nil)

	sch.DeleteChannelMetrics(&config.UpstreamConfig{
		Name:       "to-delete",
		ChannelUID: "ch_deleted_uid",
		APIKeys:    []string{"sk-x"},
	}, ChannelKindMessages)

	if _, ok := ta.GetPreferredRoute(string(ChannelKindMessages)+":user-a", string(ChannelKindMessages)); ok {
		t.Error("被删除渠道的亲和条目应被清理")
	}
	if _, ok := ta.GetPreferredRoute(string(ChannelKindResponses)+":user-c", string(ChannelKindResponses)); ok {
		t.Error("跨协议指向被删除渠道的亲和条目应被清理")
	}
	if _, ok := ta.GetPreferredRoute(string(ChannelKindMessages)+":user-b", string(ChannelKindMessages)); !ok {
		t.Error("其他渠道的亲和条目不应受影响")
	}
}
