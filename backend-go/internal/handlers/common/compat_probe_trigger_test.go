package common

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
)

// keyHashForTest 与生产路径使用同一套 keyHash 计算，确保缓存键一致。
func keyHashForTest(apiKey string) string {
	return autopilot.KeyHashFromAPIKey(apiKey)
}

// useInMemoryCompatCache 让测试使用纯内存记忆：避免在源码树里写状态文件，
// 也避免上一次运行的记忆命中 HasAnyTrait 短路而改变本次结果。
func useInMemoryCompatCache(t *testing.T) {
	t.Helper()
	t.Cleanup(swapChannelCompatCacheForTest(config.NewChannelCompatCache()))
}

func TestMaybeTriggerCompatProbeSkipsWhenAlreadyLearned(t *testing.T) {
	useInMemoryCompatCache(t)
	t.Cleanup(func() { SetCompatProbeHook(nil) })

	var calls atomic.Int32
	SetCompatProbeHook(func(*config.UpstreamConfig, string, string, string) map[config.CompatTrait]bool {
		calls.Add(1)
		return map[config.CompatTrait]bool{config.TraitStripEmptyTextBlocks: true}
	})

	upstream := &config.UpstreamConfig{
		Name:        "test",
		ChannelUID:  "ch_probe_skip",
		ServiceType: "claude",
	}

	// 已有学习记录时不应再探测（避免每次请求都消耗一次上游调用）
	model := "m-skip"
	keyHash := keyHashForTest("sk-test")
	channelCompatCache.Record(upstream.ChannelUID, keyHash, model,
		config.TraitStripEmptyTextBlocks, true, config.CompatSourceProbe, "existing")

	maybeTriggerCompatProbe(upstream, "sk-test", "https://api.example.com", model)
	time.Sleep(100 * time.Millisecond)

	if got := calls.Load(); got != 0 {
		t.Errorf("已有记录时不应探测, calls = %d", got)
	}
}

func TestMaybeTriggerCompatProbeDeduplicatesConcurrent(t *testing.T) {
	useInMemoryCompatCache(t)
	t.Cleanup(func() { SetCompatProbeHook(nil) })

	var calls atomic.Int32
	entered := make(chan struct{}, 32)
	release := make(chan struct{})
	SetCompatProbeHook(func(*config.UpstreamConfig, string, string, string) map[config.CompatTrait]bool {
		calls.Add(1)
		entered <- struct{}{}
		<-release // 阻塞住，确保并发调用都在探测进行中发起
		return map[config.CompatTrait]bool{config.TraitStripEmptyTextBlocks: true}
	})

	upstream := &config.UpstreamConfig{
		Name:        "test",
		ChannelUID:  "ch_probe_dedup",
		ServiceType: "claude",
	}

	model := "m-dedup"
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			maybeTriggerCompatProbe(upstream, "sk-test", "https://api.example.com", model)
		}()
	}
	wg.Wait()

	// 等探测真正进入 hook，而不是靠固定 sleep 猜时序（goroutine 启动慢会误判为 0 次）
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("探测未在超时内进入")
	}

	// 此时探测仍被 release 阻塞：若去重失效，其他并发调用会再次进入 hook
	select {
	case <-entered:
		t.Fatalf("并发触发应只探测一次, calls = %d", calls.Load())
	case <-time.After(200 * time.Millisecond):
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("并发触发应只探测一次, calls = %d", got)
	}
	close(release)

	// 等探测 goroutine 真正写完记忆再结束测试：否则它会在 t.Cleanup 还原全局记忆之后
	// 才写入，落到带落盘的真实单例上，在源码树里留下状态文件。
	deadline := time.After(5 * time.Second)
	for {
		if _, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHashForTest("sk-test"), model, config.TraitStripEmptyTextBlocks); ok {
			return
		}
		select {
		case <-deadline:
			t.Fatal("探测结论未在超时内写入")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestMaybeTriggerCompatProbeSkipsNonClaudeChannel(t *testing.T) {
	useInMemoryCompatCache(t)
	t.Cleanup(func() { SetCompatProbeHook(nil) })

	var calls atomic.Int32
	SetCompatProbeHook(func(*config.UpstreamConfig, string, string, string) map[config.CompatTrait]bool {
		calls.Add(1)
		return nil
	})

	// 这几个兼容项都是 Claude 协议形态差异，openai 渠道不应触发探测
	upstream := &config.UpstreamConfig{
		Name:        "test",
		ChannelUID:  "ch_probe_openai",
		ServiceType: "openai",
	}
	maybeTriggerCompatProbe(upstream, "sk-test", "https://api.example.com", "m-openai")
	time.Sleep(100 * time.Millisecond)

	if got := calls.Load(); got != 0 {
		t.Errorf("非 Claude 渠道不应探测, calls = %d", got)
	}
}
