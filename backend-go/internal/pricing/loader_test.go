package pricing

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 辅助：等待条件满足或超时（用于 fsnotify 异步 reload）。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitFor 超时")
}

// embed 路径加载：所有 12 模型应都在。
func TestLoader_EmbedDefault(t *testing.T) {
	l, err := NewLoader("")
	require.NoError(t, err)
	defer l.Close()

	require.Equal(t, "embed", l.Source())
	require.NotEmpty(t, l.Version())

	want := []string{
		"gpt-4o", "gpt-4o-mini", "gpt-4-turbo",
		"claude-3-5-sonnet", "claude-3-5-haiku", "claude-3-opus",
		"gemini-2.0-flash", "gemini-1.5-pro", "gemini-1.5-flash",
		"o1", "o1-mini", "o3-mini",
	}
	for _, m := range want {
		mp, ok := l.GetPrice(m)
		require.True(t, ok, "缺失模型 %s", m)
		require.Equal(t, m, mp.ModelID)
		require.NotEmpty(t, mp.Items, "模型 %s items 为空", m)
	}
}

// 文件 override 加载：自定义 prices.json 替换 embed。
func TestLoader_FileOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.json")
	custom := `{
		"_lastVerified": "test",
		"models": [
			{
				"modelId": "test-model",
				"items": [
					{"itemCode": "prompt_tokens", "pricing": {"mode": "usage_per_unit", "usagePerUnit": "9.99"}}
				]
			}
		]
	}`
	require.NoError(t, os.WriteFile(path, []byte(custom), 0o644))

	l, err := NewLoader(path)
	require.NoError(t, err)
	defer l.Close()

	require.Equal(t, path, l.Source())
	mp, ok := l.GetPrice("test-model")
	require.True(t, ok)
	require.Equal(t, "test-model", mp.ModelID)

	// embed 中的模型不应再可见（已被 override 整张表替换）
	_, ok = l.GetPrice("gpt-4o")
	require.False(t, ok, "override 模式下不应保留 embed 模型")
}

// 文件不存在 / 解析失败：启动应返回错误。
func TestLoader_FileMissing(t *testing.T) {
	_, err := NewLoader(filepath.Join(t.TempDir(), "does-not-exist.json"))
	require.Error(t, err)
}

func TestLoader_FileInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.json")
	require.NoError(t, os.WriteFile(path, []byte("not-json"), 0o644))
	_, err := NewLoader(path)
	require.Error(t, err)
}

// 启动文件 → 修改文件 → 触发热重载。
func TestLoader_HotReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.json")

	v1 := `{
		"models": [
			{"modelId": "alpha", "items": [
				{"itemCode": "prompt_tokens", "pricing": {"mode": "usage_per_unit", "usagePerUnit": "1.00"}}
			]}
		]
	}`
	require.NoError(t, os.WriteFile(path, []byte(v1), 0o644))

	l, err := NewLoader(path)
	require.NoError(t, err)
	defer l.Close()

	mp, ok := l.GetPrice("alpha")
	require.True(t, ok)
	require.Equal(t, "alpha", mp.ModelID)
	v1Ver := l.Version()
	require.NotEmpty(t, v1Ver)

	// 改写：替换为新模型 beta
	v2 := `{
		"models": [
			{"modelId": "beta", "items": [
				{"itemCode": "prompt_tokens", "pricing": {"mode": "usage_per_unit", "usagePerUnit": "2.00"}}
			]}
		]
	}`
	require.NoError(t, os.WriteFile(path, []byte(v2), 0o644))

	waitFor(t, 3*time.Second, func() bool {
		_, found := l.GetPrice("beta")
		return found && l.Version() != v1Ver
	})

	_, ok = l.GetPrice("alpha")
	require.False(t, ok, "热重载后旧模型应消失")
	mp2, ok := l.GetPrice("beta")
	require.True(t, ok)
	require.Equal(t, "beta", mp2.ModelID)
}

// 热重载失败时保留旧表（写入非法 JSON 不应清空）。
func TestLoader_HotReloadFallbackOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.json")
	good := `{
		"models": [
			{"modelId": "keep", "items": [
				{"itemCode": "prompt_tokens", "pricing": {"mode": "usage_per_unit", "usagePerUnit": "1.00"}}
			]}
		]
	}`
	require.NoError(t, os.WriteFile(path, []byte(good), 0o644))
	l, err := NewLoader(path)
	require.NoError(t, err)
	defer l.Close()

	// 写入坏 JSON
	require.NoError(t, os.WriteFile(path, []byte("garbage"), 0o644))
	// 给 watcher 一些时间处理
	time.Sleep(300 * time.Millisecond)
	// 旧表仍可查
	_, ok := l.GetPrice("keep")
	require.True(t, ok, "reload 失败时应保留旧价格表")
}

// NewLoaderFromEnv 行为：未设置环境变量时等价于 embed-only。
func TestLoader_FromEnvUnset(t *testing.T) {
	t.Setenv(CCXPricesFileEnv, "")
	l, err := NewLoaderFromEnv()
	require.NoError(t, err)
	defer l.Close()
	require.Equal(t, "embed", l.Source())
}
