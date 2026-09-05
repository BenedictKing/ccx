package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// ── compat-cache 窗口分区运维闭环测试（C1/E1）──

type compatCacheTestEnv struct {
	router *gin.Engine
	cache  *config.ChannelCompatCache
	path   string
}

func newCompatCacheTestEnv(t *testing.T, withPersistence bool) *compatCacheTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var cache *config.ChannelCompatCache
	path := ""
	if withPersistence {
		path = filepath.Join(t.TempDir(), "channel_compat.json")
		cache = config.NewChannelCompatCacheWithPersistence(path)
	} else {
		cache = config.NewChannelCompatCache()
	}
	restore := config.SwapSharedChannelCompatCacheForTest(cache)
	t.Cleanup(restore)

	router := gin.New()
	RegisterCompatCacheRoutes(router)
	return &compatCacheTestEnv{router: router, cache: cache, path: path}
}

func (e *compatCacheTestEnv) get(t *testing.T) CompatCacheResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/compat-cache", nil)
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET 状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	var resp CompatCacheResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return resp
}

func (e *compatCacheTestEnv) delete(t *testing.T, query string) ClearCompatCacheResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/compat-cache"+query, nil)
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE %s 状态码 = %d, body=%s", query, w.Code, w.Body.String())
	}
	var resp ClearCompatCacheResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return resp
}

// seedCompatCacheWindows 写入窗口学习与 trait 学习各一条，覆盖两个分区。
func seedCompatCacheWindows(t *testing.T, cache *config.ChannelCompatCache) {
	t.Helper()
	now := time.Now()
	cache.RecordContextWindowProven("ch_w", "responses", "gpt-5.6-sol", 500_000, now)
	cache.RecordModelsAPIContextWindow("ch_w", "chat", "kimi-k3", 900_000, now)
	cache.Record("ch_w", "kh", "gpt-5.6-sol", config.TraitDowngradeDeveloperRole, true,
		config.CompatSourceErrorSignal, "test evidence")
}

func TestCompatCacheSnapshotIncludesContextWindows(t *testing.T) {
	env := newCompatCacheTestEnv(t, false)
	seedCompatCacheWindows(t, env.cache)

	resp := env.get(t)
	if len(resp.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(resp.Entries))
	}
	if len(resp.ContextWindows) != 2 {
		t.Fatalf("contextWindows = %d, want 2", len(resp.ContextWindows))
	}
	if resp.Total != 1 {
		t.Fatalf("total = %d, want 1（沿用以 entries 计数）", resp.Total)
	}
	for _, w := range resp.ContextWindows {
		if w.ChannelUID == "" || w.Kind == "" || w.Model == "" {
			t.Fatalf("窗口项必须拆开复合键: %+v", w)
		}
		if w.ChannelUID == "ch_w" && w.Model == "gpt-5.6-sol" {
			if w.ProvenInputTokens != 500_000 || !w.ProvenFresh {
				t.Fatalf("proven 字段不符: %+v", w)
			}
		}
		if w.ChannelUID == "ch_w" && w.Model == "kimi-k3" {
			if w.ModelsAPIWindow != 900_000 || !w.ModelsAPIFresh {
				t.Fatalf("modelsApi 字段不符: %+v", w)
			}
		}
	}
}

func TestCompatCacheClearAllRemovesEverySection(t *testing.T) {
	env := newCompatCacheTestEnv(t, false)
	seedCompatCacheWindows(t, env.cache)

	resp := env.delete(t, "")
	if resp.Removed != 1 || resp.Trait != "" || resp.Section != "" {
		t.Fatalf("全清响应不符: %+v", resp)
	}
	after := env.get(t)
	if len(after.Entries) != 0 || len(after.ContextWindows) != 0 {
		t.Fatalf("全清后两分区都必须为空: entries=%d windows=%d", len(after.Entries), len(after.ContextWindows))
	}
}

func TestCompatCacheClearTraitKeepsWindows(t *testing.T) {
	env := newCompatCacheTestEnv(t, false)
	seedCompatCacheWindows(t, env.cache)

	resp := env.delete(t, "?trait=downgrade_developer_role")
	if resp.Trait != "downgrade_developer_role" || resp.Removed != 1 {
		t.Fatalf("按 trait 清除响应不符: %+v", resp)
	}
	after := env.get(t)
	if len(after.Entries) != 0 {
		t.Fatalf("trait 清除后 entries 应为空: %d", len(after.Entries))
	}
	if len(after.ContextWindows) != 2 {
		t.Fatalf("按 trait 清除不得误删窗口分区: %d", len(after.ContextWindows))
	}
}

func TestCompatCacheClearContextWindowSection(t *testing.T) {
	env := newCompatCacheTestEnv(t, false)
	seedCompatCacheWindows(t, env.cache)

	resp := env.delete(t, "?section=context-window")
	if resp.Section != "context-window" || resp.Removed != 2 {
		t.Fatalf("窗口分区清除响应不符: %+v", resp)
	}
	after := env.get(t)
	if len(after.ContextWindows) != 0 {
		t.Fatalf("窗口分区应为空: %d", len(after.ContextWindows))
	}
	if len(after.Entries) != 1 {
		t.Fatalf("窗口分区清除不得误删 traits: %d", len(after.Entries))
	}
}

func TestCompatCacheClearPersistsToFile(t *testing.T) {
	env := newCompatCacheTestEnv(t, true)
	seedCompatCacheWindows(t, env.cache)
	if err := env.cache.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// 文件里已有窗口学习；全清后文件内容必须同步（重启不复活）
	env.delete(t, "")
	data, err := os.ReadFile(env.path)
	if err != nil {
		t.Fatalf("读持久化文件: %v", err)
	}
	var persisted struct {
		ContextWindows map[string]json.RawMessage `json:"contextWindows"`
		Entries        map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("解析持久化文件: %v", err)
	}
	if len(persisted.ContextWindows) != 0 || len(persisted.Entries) != 0 {
		t.Fatalf("清除后文件仍含数据: entries=%d windows=%d", len(persisted.Entries), len(persisted.ContextWindows))
	}

	// 只清窗口分区：traits 仍在文件
	seedCompatCacheWindows(t, env.cache)
	if err := env.cache.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	env.delete(t, "?section=context-window")
	data, _ = os.ReadFile(env.path)
	persisted.ContextWindows, persisted.Entries = nil, nil
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("解析持久化文件: %v", err)
	}
	if len(persisted.ContextWindows) != 0 {
		t.Fatalf("窗口清除后文件仍含窗口: %d", len(persisted.ContextWindows))
	}
	if len(persisted.Entries) != 1 {
		t.Fatalf("trait 学习应保留在文件: %d", len(persisted.Entries))
	}
}
