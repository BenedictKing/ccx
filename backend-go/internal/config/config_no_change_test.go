package config

import (
	"github.com/BenedictKing/ccx/internal/errutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestUpdateUpstream_NoChangeSkipsSave 验证配置未改变时不会触发保存
func TestUpdateUpstream_NoChangeSkipsSave(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-test"],
			"serviceType": "claude",
			"priority": 1
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("初始化配置管理器失败: %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	// 等待初始化完成
	time.Sleep(100 * time.Millisecond)

	// 获取初始文件修改时间
	initialStat, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("获取文件状态失败: %v", err)
	}
	initialModTime := initialStat.ModTime()

	// 等待确保时间戳可以区分
	time.Sleep(10 * time.Millisecond)

	// 渠道 name 由 baseURL 派生。首次更新会把旧名 test-channel 规范为 example 并落盘；
	// 再次用相同字段更新则应为 no-change，不再触发保存。
	desc := ""
	if _, err = cm.UpdateUpstream(0, UpstreamUpdate{Description: &desc}); err != nil {
		t.Fatalf("UpdateUpstream() error = %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// 记录派生落盘后的文件时间作为基准
	statAfterDerive, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("获取文件状态失败: %v", err)
	}
	initialModTime = statAfterDerive.ModTime()
	time.Sleep(10 * time.Millisecond)

	// 执行一个不改变配置的更新（描述与现有值相同）
	_, err = cm.UpdateUpstream(0, UpstreamUpdate{
		Description: &desc,
	})
	if err != nil {
		t.Fatalf("UpdateUpstream() error = %v", err)
	}

	// 等待可能的文件写入
	time.Sleep(100 * time.Millisecond)

	// 检查文件修改时间是否未改变
	afterStat, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("获取文件状态失败: %v", err)
	}
	afterModTime := afterStat.ModTime()

	if !afterModTime.Equal(initialModTime) {
		t.Errorf("配置文件被修改了，但配置实际上没有改变。初始时间: %v, 修改后时间: %v",
			initialModTime, afterModTime)
	}
}
