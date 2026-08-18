package config

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatcher_DebouncesRapidWrites 验证 watcher 在 debounce 窗口内的多次写入合并为一次 loadConfig。
// 通过 RegisterOnConfigChange 计数实际触发次数。
func TestWatcher_DebouncesRapidWrites(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initial := `{"upstream":[{"name":"a","baseUrl":"https://x.example","apiKeys":["k1"],"serviceType":"claude"}]}`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatalf("初始配置写入失败: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cm.CloseWatcher()

	// 回调由 fireConfigChangeCallbacks 在独立 goroutine 中异步执行，计数需原子操作。
	var callbackCount atomic.Int32
	done := make(chan struct{}, 1)
	cm.RegisterOnConfigChange(func(_ Config) {
		callbackCount.Add(1)
		select {
		case done <- struct{}{}:
		default:
		}
	})

	// 在 debounce 窗口（100ms）内连续写入多次，期望最终只触发一次重载。
	// 通过修改 name 字段（仍是合法 JSON）来制造多次合法变更。
	for i := 0; i < 5; i++ {
		newName := "ch-" + strings.Repeat("x", i+1)
		updated := strings.Replace(initial, `"name":"a"`, `"name":"`+newName+`"`, 1)
		if err := os.WriteFile(configPath, []byte(updated), 0644); err != nil {
			t.Fatalf("写入 %d 失败: %v", i, err)
		}
		time.Sleep(15 * time.Millisecond)
	}

	// 等待 debounce 触发（最长 1s 兜底）
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("未收到任何 change 回调")
	}

	// 再等待一段时间确保后续没有多次触发
	time.Sleep(200 * time.Millisecond)

	triggered := callbackCount.Load()
	if triggered > 2 {
		// 允许极端环境下 2 次（首批 + 末尾边界），但不应等于 5
		t.Fatalf("callback 触发 %d 次，期望 1-2 次（debounce 合并失败）", triggered)
	}
	if triggered == 0 {
		t.Fatal("callback 未触发")
	}
}

// TestWatcherHandlesAtomicSave 验证 watcher 能处理编辑器常见的原子保存流程：
// 先写临时文件，再 rename 覆盖目标配置文件。
func TestWatcherHandlesAtomicSave(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initial := `{"upstream":[{"name":"a","baseUrl":"https://x.example","apiKeys":["k1"],"serviceType":"claude"}]}`
	if err := os.WriteFile(configPath, []byte(initial), 0644); err != nil {
		t.Fatalf("初始配置写入失败: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cm.CloseWatcher()

	done := make(chan struct{}, 1)
	cm.RegisterOnConfigChange(func(_ Config) {
		select {
		case done <- struct{}{}:
		default:
		}
	})

	updated := `{"upstream":[{"name":"atomic-save","baseUrl":"https://x.example","apiKeys":["k1"],"serviceType":"claude"}]}`
	tmpPath := filepath.Join(tempDir, "config.json.tmp")
	if err := os.WriteFile(tmpPath, []byte(updated), 0644); err != nil {
		t.Fatalf("临时配置写入失败: %v", err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		t.Fatalf("rename 覆盖失败: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("atomic save 未触发配置重载")
	}

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 || cfg.Upstream[0].Name != "atomic-save" {
		t.Fatalf("重载后的配置未更新，got %+v", cfg.Upstream)
	}
}

// lockedLogBuffer 并发安全的日志缓冲：watcher 后台 goroutine 写日志与
// 测试主 goroutine 读取断言之间存在并发。
type lockedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLogBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLogBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// TestWatcherSkipsSelfWrittenReload 自身保存不应触发 watcher 重载（内存已是最新），
// 外部修改仍然正常重载。
func TestWatcherSkipsSelfWrittenReload(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("初始配置写入失败: %v", err)
	}
	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cm.CloseWatcher()

	buf := &lockedLogBuffer{}
	oldWriter := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(oldWriter) })

	// 自写：走 saveConfigLocked 落盘，等待超过 debounce 窗口。
	if err := cm.AddUpstream(UpstreamConfig{
		Name: "self-write", BaseURL: "https://self.example", APIKeys: []string{"k1"}, ServiceType: "claude",
	}); err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(buf.String(), "配置已重载") {
		t.Fatalf("自身保存不应触发 watcher 重载: %s", buf.String())
	}

	// 外部修改：必须触发重载。
	external := `{"upstream":[{"name":"external-edit","baseUrl":"https://ext.example","apiKeys":["k2"],"serviceType":"claude"}]}`
	if err := os.WriteFile(configPath, []byte(external), 0644); err != nil {
		t.Fatalf("外部修改写入失败: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if !strings.Contains(buf.String(), "配置已重载") {
		t.Fatalf("外部修改应触发重载: %s", buf.String())
	}
	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 || cfg.Upstream[0].Name != "external-edit" {
		t.Fatalf("重载后的配置未更新, got %+v", cfg.Upstream)
	}
}
