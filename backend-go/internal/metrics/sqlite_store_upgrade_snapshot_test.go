package metrics

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// 历史库演练只操作临时副本，避免 schema 检查或 WAL 模式触碰来源文件。
func TestLegacyMetricsSnapshotUpgrade(t *testing.T) {
	sourcePath := os.Getenv("CCX_UPGRADE_METRICS_SNAPSHOT")
	if sourcePath == "" {
		t.Skip("未指定 2.9 指标库快照")
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("读取指标库快照失败: %v", err)
	}
	path := filepath.Join(t.TempDir(), "metrics.db")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatalf("复制指标库快照失败: %v", err)
	}
	readCounts := func(db *sql.DB) [2]int64 {
		t.Helper()
		var counts [2]int64
		for i, table := range []string{"request_records", "circuit_states"} {
			if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&counts[i]); err != nil {
				t.Fatalf("读取 %s 记录数失败: %v", table, err)
			}
		}
		return counts
	}
	initial, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var initialVersion int
	if err := initial.QueryRow("PRAGMA user_version").Scan(&initialVersion); err != nil {
		t.Fatal(err)
	}
	initialCounts := readCounts(initial)
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}

	open := func() *SQLiteStore {
		t.Helper()
		store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 366})
		if err != nil {
			t.Fatalf("加载隔离指标库失败: %v", err)
		}
		return store
	}
	for _, stage := range []string{"首次加载", "重启加载"} {
		store := open()
		version, err := store.schemaVersion()
		if err != nil {
			t.Fatal(err)
		}
		if version != initialVersion {
			t.Errorf("%s: 指标库版本变化: %d -> %d", stage, initialVersion, version)
		}
		if counts := readCounts(store.db); counts != initialCounts {
			t.Errorf("%s: 指标记录数变化: %v -> %v", stage, initialCounts, counts)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	unchanged, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, unchanged) {
		t.Fatal("源指标库快照被改写")
	}
}
