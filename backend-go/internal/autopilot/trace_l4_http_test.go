package autopilot

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	_ "modernc.org/sqlite"
)

// ── L4 HTTP 契约测试 ──

// newTestTraceRouter 创建测试用的 gin router + TraceStore。
func newTestTraceRouter(t *testing.T) (*gin.Engine, *TraceStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store, err := NewTraceStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}

	router := gin.New()
	RegisterTraceRoutes(router, store)
	return router, store
}

func TestHTTP_ListTraces_Empty(t *testing.T) {
	router, _ := newTestTraceRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/traces", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp TraceListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Traces) != 0 {
		t.Errorf("traces count = %d, want 0", len(resp.Traces))
	}
	if resp.HasMore {
		t.Error("空列表不应标记 hasMore")
	}
}

func TestHTTP_ListTraces_WithFilters(t *testing.T) {
	router, store := newTestTraceRouter(t)

	// 写入不同模式的 trace
	modes := []RoutingMode{RoutingModeShadow, RoutingModeAuto}
	for _, mode := range modes {
		store.Record(&RoutingDecisionTrace{
			TraceUID:        GenerateTraceUIDv2(),
			RequestKind:     "chat",
			TaskClass:       TaskClassWorker,
			Mode:            mode,
			ManualIntentUID: "mi_http",
			CreatedAt:       time.Now().UTC(),
		})
	}

	// 过滤 auto 模式
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/traces?mode=auto", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp TraceListResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Traces) != 1 {
		t.Errorf("auto 过滤应返回 1 条，实际: %d", len(resp.Traces))
	}
}

func TestHTTP_GetTraceDetail_NotFound(t *testing.T) {
	router, _ := newTestTraceRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/traces/rt_nonexistent", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["traceUid"] == nil {
		t.Error("404 响应应包含 traceUid")
	}
}

func TestHTTP_GetTraceDetail_Found(t *testing.T) {
	router, store := newTestTraceRouter(t)

	traceUID := GenerateTraceUIDv2()
	store.Record(&RoutingDecisionTrace{
		TraceUID:        traceUID,
		SchemaVersion:   2,
		RequestKind:     "messages",
		TaskClass:       TaskClassSupervisor,
		Mode:            RoutingModeShadow,
		ManualIntentUID: "mi_detail",
		CreatedAt:       time.Now().UTC(),
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/traces/"+traceUID, nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp TraceDetailResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.Trace == nil {
		t.Fatal("响应应包含 trace")
	}
	if resp.Trace.TraceUID != traceUID {
		t.Errorf("TraceUID = %q, want %q", resp.Trace.TraceUID, traceUID)
	}
}

func TestHTTP_GetTraceDetail_NoSensitiveFields(t *testing.T) {
	router, store := newTestTraceRouter(t)

	traceUID := GenerateTraceUIDv2()
	store.Record(&RoutingDecisionTrace{
		TraceUID:           traceUID,
		SchemaVersion:      2,
		RequestKind:        "chat",
		TaskClass:          TaskClassWorker,
		Mode:               RoutingModeShadow,
		ManualIntentUID:    "mi_san",
		SelectedMetricsKey: "https://api.example.com|sk-secret12345678",
		Candidates: []RoutingCandidate{
			{ChannelUID: "ch_a", MetricsKey: "https://leak.example.com|sk-bad999999999"},
		},
		CreatedAt: time.Now().UTC(),
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/traces/"+traceUID, nil)
	router.ServeHTTP(w, req)

	// 扫描响应 JSON 不含敏感字段
	sensitive := ScanJSONForSensitive(w.Body.Bytes())
	if len(sensitive) > 0 {
		t.Errorf("响应 JSON 含敏感字段: %v", sensitive)
	}
}

func TestHTTP_Stats_Empty(t *testing.T) {
	router, _ := newTestTraceRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/traces/stats", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var stats TraceStatsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if stats.TotalCount != 0 {
		t.Errorf("TotalCount = %d, want 0", stats.TotalCount)
	}
}

func TestHTTP_Stats_WithTraces(t *testing.T) {
	router, store := newTestTraceRouter(t)

	// 写入三态比较的 trace
	store.Record(&RoutingDecisionTrace{
		TraceUID:         "rt_match",
		ShadowChannelUID: "ch_a",
		ActualChannelUID: "ch_a",
		Match:            true,
		Mode:             RoutingModeShadow,
		CreatedAt:        time.Now().UTC(),
	})
	store.Record(&RoutingDecisionTrace{
		TraceUID:         "rt_mismatch",
		ShadowChannelUID: "ch_a",
		ActualChannelUID: "ch_b",
		Match:            false,
		Mode:             RoutingModeShadow,
		CreatedAt:        time.Now().UTC(),
	})
	store.Record(&RoutingDecisionTrace{
		TraceUID:  "rt_uncompared",
		Mode:      RoutingModeShadow,
		CreatedAt: time.Now().UTC(),
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/traces/stats", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var stats TraceStatsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if stats.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", stats.TotalCount)
	}
	if stats.ComparedCount != 2 {
		t.Errorf("ComparedCount = %d, want 2", stats.ComparedCount)
	}
	if stats.MatchedCount != 1 {
		t.Errorf("MatchedCount = %d, want 1", stats.MatchedCount)
	}
	if stats.UncomparedCount != 1 {
		t.Errorf("UncomparedCount = %d, want 1", stats.UncomparedCount)
	}
}

func TestHTTP_ListTraces_LimitDefault(t *testing.T) {
	router, store := newTestTraceRouter(t)

	// 写入 60 条 trace（超过默认 50）
	for i := 0; i < 60; i++ {
		store.Record(&RoutingDecisionTrace{
			TraceUID:        GenerateTraceUIDv2(),
			RequestKind:     "chat",
			TaskClass:       TaskClassWorker,
			Mode:            RoutingModeShadow,
			ManualIntentUID: "mi_limit",
			CreatedAt:       time.Now().Add(time.Duration(i) * time.Millisecond).UTC(),
		})
	}

	// 不带 limit 参数，应使用默认 50
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/traces", nil)
	router.ServeHTTP(w, req)

	var resp TraceListResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Traces) > 50 {
		t.Errorf("默认应限制到 50，实际: %d", len(resp.Traces))
	}
}

func TestHTTP_ListTraces_LimitMax(t *testing.T) {
	router, store := newTestTraceRouter(t)

	for i := 0; i < 250; i++ {
		store.Record(&RoutingDecisionTrace{
			TraceUID:        GenerateTraceUIDv2(),
			RequestKind:     "chat",
			TaskClass:       TaskClassWorker,
			Mode:            RoutingModeShadow,
			ManualIntentUID: "mi_max",
			CreatedAt:       time.Now().Add(time.Duration(i) * time.Millisecond).UTC(),
		})
	}

	// 请求 limit=500，应限制到 200
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/traces?limit=500", nil)
	router.ServeHTTP(w, req)

	var resp TraceListResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Traces) > 200 {
		t.Errorf("最大应限制到 200，实际: %d", len(resp.Traces))
	}
}
