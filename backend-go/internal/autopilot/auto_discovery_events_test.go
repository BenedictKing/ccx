package autopilot

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── auto_mapping_applied 事件测试（直接调用 maybeAutoWriteChannelConfig，不涉及网络） ──

func TestMaybeAutoWriteChannelConfig_PublishesAutoMappingAppliedOnWrite(t *testing.T) {
	channelUID := "ch_event_write_001"
	cfgManager := setupTestConfigManagerForDiscovery(t, channelUID, nil)

	hub := NewEventHub()
	sub, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	runner := &AutoDiscoveryRunner{tasks: make(map[string]*DiscoveryTask), hub: hub}
	channel := &config.UpstreamConfig{ChannelUID: channelUID}
	endpoints := []EndpointDiscoveryResult{
		{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{"gpt-4o"}, ModelsCount: 1},
	}

	runner.maybeAutoWriteChannelConfig(channelUID, channel, endpoints, cfgManager)

	select {
	case ev := <-sub:
		if ev.EventType != EventTypeAutoMappingApply {
			t.Errorf("EventType = %q, want %q", ev.EventType, EventTypeAutoMappingApply)
		}
		if ev.ChannelUID != channelUID {
			t.Errorf("ChannelUID = %q, want %q", ev.ChannelUID, channelUID)
		}
		if ev.ChannelKind != "messages" {
			t.Errorf("ChannelKind = %q, want messages", ev.ChannelKind)
		}
	case <-time.After(time.Second):
		t.Fatal("写入成功后应发布 auto_mapping_applied 事件")
	}
}

func TestMaybeAutoWriteChannelConfig_NoEventWhenSkipped(t *testing.T) {
	channelUID := "ch_event_skip_001"
	// 用户已配置 SupportedModels，写入应被跳过
	cfgManager := setupTestConfigManagerForDiscovery(t, channelUID, []string{"existing-model"})

	hub := NewEventHub()
	sub, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	runner := &AutoDiscoveryRunner{tasks: make(map[string]*DiscoveryTask), hub: hub}
	channel := &config.UpstreamConfig{ChannelUID: channelUID, SupportedModels: []string{"existing-model"}}
	endpoints := []EndpointDiscoveryResult{
		{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{"gpt-4o"}, ModelsCount: 1},
	}

	runner.maybeAutoWriteChannelConfig(channelUID, channel, endpoints, cfgManager)

	select {
	case ev := <-sub:
		t.Fatalf("跳过写入时不应发布事件，got %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// 预期：无事件
	}
}

func TestMaybeAutoWriteChannelConfig_NilHub_NoPanic(t *testing.T) {
	channelUID := "ch_event_nilhub_001"
	cfgManager := setupTestConfigManagerForDiscovery(t, channelUID, nil)

	runner := NewAutoDiscoveryRunner(nil, nil) // hub=nil
	channel := &config.UpstreamConfig{ChannelUID: channelUID}
	endpoints := []EndpointDiscoveryResult{
		{KeyMask: "sk-****a", BaseURL: "https://a.example.com", ProtocolOk: true, Models: []string{"gpt-4o"}, ModelsCount: 1},
	}

	// 不应 panic
	runner.maybeAutoWriteChannelConfig(channelUID, channel, endpoints, cfgManager)
}

// ── discovery_completed 事件测试（走完整 runDiscovery，用 httptest 桩上游） ──

func TestRunDiscovery_PublishesDiscoveryCompletedOnSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	}))
	defer server.Close()

	channelUID := "ch_event_discovery_001"
	cfgManager := setupTestConfigManagerForDiscovery(t, channelUID, nil)

	hub := NewEventHub()
	sub, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	runner := NewAutoDiscoveryRunner(nil, hub)
	channel := &config.UpstreamConfig{
		ChannelUID:  channelUID,
		ServiceType: "claude",
		BaseURL:     server.URL,
		BaseURLs:    []string{server.URL},
		APIKeys:     []string{"sk-test"},
	}

	if !runner.TriggerDiscovery(channelUID, channel, cfgManager) {
		t.Fatal("TriggerDiscovery 应返回 true")
	}

	// 等待任务完成（轮询，最多 5 秒）
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task := runner.GetTask(channelUID)
		if task != nil && task.Status != DiscoveryStatusRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 该场景会同时触发 auto_mapping_applied（模型列表一致且渠道未手动配置）
	// 和 discovery_completed 两个事件，顺序取决于代码执行顺序，因此收集全部
	// 事件后再断言 discovery_completed 是否出现，而不是假设第一条就是它。
	var gotDiscoveryCompleted bool
	collectDeadline := time.After(2 * time.Second)
collectLoop:
	for {
		select {
		case ev := <-sub:
			if ev.EventType == EventTypeDiscoveryComplete {
				if ev.ChannelUID != channelUID {
					t.Errorf("ChannelUID = %q, want %q", ev.ChannelUID, channelUID)
				}
				gotDiscoveryCompleted = true
				break collectLoop
			}
		case <-collectDeadline:
			break collectLoop
		}
	}
	if !gotDiscoveryCompleted {
		t.Fatal("发现完成后应发布 discovery_completed 事件")
	}
}

func TestRunDiscovery_NilHub_NoPanic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer server.Close()

	channelUID := "ch_event_discovery_nilhub_001"
	runner := NewAutoDiscoveryRunner(nil, nil) // hub=nil
	channel := &config.UpstreamConfig{
		ChannelUID:  channelUID,
		ServiceType: "claude",
		BaseURL:     server.URL,
		BaseURLs:    []string{server.URL},
		APIKeys:     []string{"sk-test"},
	}

	if !runner.TriggerDiscovery(channelUID, channel, nil) {
		t.Fatal("TriggerDiscovery 应返回 true")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task := runner.GetTask(channelUID)
		if task != nil && task.Status != DiscoveryStatusRunning {
			return // 不 panic 即通过
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("发现任务未在超时前完成")
}
