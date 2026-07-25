package autopilot

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

// ── 辅助函数 ──

// newTestManualIntentStore 创建内存 SQLite 测试用 Store。
func newTestManualIntentStore(t *testing.T) *ManualIntentStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store, err := NewManualIntentStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 ManualIntentStore 失败: %v", err)
	}
	return store
}

// newTestRouter 创建测试用 gin 路由器。
func newTestRouter(store *ManualIntentStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterManualIntentRoutes(r, store)
	return r
}

// sampleIntent 返回一个用于测试的基础意图。
func sampleIntent(expiresIn time.Duration) *ManualRoutingIntent {
	return &ManualRoutingIntent{
		IntentType:  IntentTypeModelTrial,
		ChannelKind: "messages",
		Model:       "fable-5",
		ExpiresAt:   time.Now().UTC().Add(expiresIn),
	}
}

// ── 类型与状态推导测试 ──

func TestManualRoutingIntent_DeriveStatus(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name       string
		intent     *ManualRoutingIntent
		now        time.Time
		wantStatus IntentStatus
		wantChange bool
	}{
		{
			name: "active 未过期未超预算",
			intent: &ManualRoutingIntent{
				Status:    IntentStatusActive,
				ExpiresAt: now.Add(1 * time.Hour),
			},
			now:        now,
			wantStatus: IntentStatusActive,
			wantChange: false,
		},
		{
			name: "过期自动转 expired",
			intent: &ManualRoutingIntent{
				Status:    IntentStatusActive,
				ExpiresAt: now.Add(-1 * time.Minute),
			},
			now:        now,
			wantStatus: IntentStatusExpired,
			wantChange: true,
		},
		{
			name: "请求预算耗尽自动转 exhausted",
			intent: &ManualRoutingIntent{
				Status:      IntentStatusActive,
				ExpiresAt:   now.Add(1 * time.Hour),
				MaxRequests: 10,
				TrialResult: TrialResult{HitCount: 10},
			},
			now:        now,
			wantStatus: IntentStatusExhausted,
			wantChange: true,
		},
		{
			name: "成本预算耗尽自动转 exhausted",
			intent: &ManualRoutingIntent{
				Status:           IntentStatusActive,
				ExpiresAt:        now.Add(1 * time.Hour),
				MaxEstimatedCost: 5.0,
				TrialResult:      TrialResult{EstimatedCost: 5.5},
			},
			now:        now,
			wantStatus: IntentStatusExhausted,
			wantChange: true,
		},
		{
			name: "disabled 不自动变更（即使已过期）",
			intent: &ManualRoutingIntent{
				Status:    IntentStatusDisabled,
				ExpiresAt: now.Add(-1 * time.Hour),
			},
			now:        now,
			wantStatus: IntentStatusDisabled,
			wantChange: false,
		},
		{
			name: "空状态默认推导为 active",
			intent: &ManualRoutingIntent{
				Status:    "",
				ExpiresAt: now.Add(1 * time.Hour),
			},
			now:        now,
			wantStatus: IntentStatusActive,
			wantChange: true,
		},
		{
			name: "过期且已有 expired 状态不重复变更",
			intent: &ManualRoutingIntent{
				Status:    IntentStatusExpired,
				ExpiresAt: now.Add(-2 * time.Hour),
			},
			now:        now,
			wantStatus: IntentStatusExpired,
			wantChange: false,
		},
		{
			name: "预算刚好未超不转 exhausted",
			intent: &ManualRoutingIntent{
				Status:      IntentStatusActive,
				ExpiresAt:   now.Add(1 * time.Hour),
				MaxRequests: 10,
				TrialResult: TrialResult{HitCount: 9},
			},
			now:        now,
			wantStatus: IntentStatusActive,
			wantChange: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, changed := tt.intent.DeriveStatus(tt.now)
			if status != tt.wantStatus {
				t.Errorf("DeriveStatus() status = %q, want %q", status, tt.wantStatus)
			}
			if changed != tt.wantChange {
				t.Errorf("DeriveStatus() changed = %v, want %v", changed, tt.wantChange)
			}
		})
	}
}

// ── Store CRUD 测试 ──

func TestManualIntentStore_Create(t *testing.T) {
	store := newTestManualIntentStore(t)

	intent := sampleIntent(1 * time.Hour)
	intent.Name = "试用 fable-5"

	if err := store.Create(intent); err != nil {
		t.Fatalf("Create() 失败: %v", err)
	}

	if intent.IntentUID == "" {
		t.Fatal("Create() 后 IntentUID 应自动生成")
	}
	if intent.Status != IntentStatusActive {
		t.Errorf("Create() 后 Status = %q, want %q", intent.Status, IntentStatusActive)
	}
	if intent.TrafficPercent != 100 {
		t.Errorf("Create() 后 TrafficPercent = %d, want 100", intent.TrafficPercent)
	}
	if intent.CreatedAt.IsZero() {
		t.Error("Create() 后 CreatedAt 不应为零值")
	}
}

func TestManualIntentStore_CreateValidation(t *testing.T) {
	store := newTestManualIntentStore(t)

	tests := []struct {
		name    string
		intent  *ManualRoutingIntent
		wantErr bool
	}{
		{
			name:    "空 intentType 失败",
			intent:  &ManualRoutingIntent{ChannelKind: "messages", ExpiresAt: time.Now().Add(time.Hour)},
			wantErr: true,
		},
		{
			name:    "空 channelKind 失败",
			intent:  &ManualRoutingIntent{IntentType: IntentTypeModelTrial, ExpiresAt: time.Now().Add(time.Hour)},
			wantErr: true,
		},
		{
			name:    "空 expiresAt 失败",
			intent:  &ManualRoutingIntent{IntentType: IntentTypeModelTrial, ChannelKind: "messages"},
			wantErr: true,
		},
		{
			name: "session_pin 无 sessionId 失败",
			intent: &ManualRoutingIntent{
				IntentType:  IntentTypeSessionPin,
				ChannelKind: "messages",
				ExpiresAt:   time.Now().Add(time.Hour),
			},
			wantErr: true,
		},
		{
			name: "trafficPercent 超出范围失败",
			intent: &ManualRoutingIntent{
				IntentType:     IntentTypeModelTrial,
				ChannelKind:    "messages",
				ExpiresAt:      time.Now().Add(time.Hour),
				TrafficPercent: 150,
			},
			wantErr: true,
		},
		{
			name: "合法意图成功",
			intent: &ManualRoutingIntent{
				IntentType:  IntentTypeModelTrial,
				ChannelKind: "messages",
				ExpiresAt:   time.Now().Add(time.Hour),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.Create(tt.intent)
			if (err != nil) != tt.wantErr {
				t.Errorf("Create() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManualIntentStore_Get(t *testing.T) {
	store := newTestManualIntentStore(t)

	intent := sampleIntent(1 * time.Hour)
	intent.Name = "test get"
	_ = store.Create(intent)

	got := store.Get(intent.IntentUID)
	if got == nil {
		t.Fatal("Get() 返回 nil")
	}
	if got.IntentUID != intent.IntentUID {
		t.Errorf("Get() IntentUID = %q, want %q", got.IntentUID, intent.IntentUID)
	}
	if got.Name != "test get" {
		t.Errorf("Get() Name = %q, want %q", got.Name, "test get")
	}

	// 不存在的 uid
	if store.Get("nonexistent") != nil {
		t.Error("Get() 不存在的 uid 应返回 nil")
	}
}

func TestManualIntentStore_Delete(t *testing.T) {
	store := newTestManualIntentStore(t)

	intent := sampleIntent(1 * time.Hour)
	_ = store.Create(intent)

	if err := store.Delete(intent.IntentUID); err != nil {
		t.Fatalf("Delete() 失败: %v", err)
	}
	if store.Get(intent.IntentUID) != nil {
		t.Error("Delete() 后 Get() 应返回 nil")
	}

	// 重复删除应返回 ErrIntentNotFound
	if err := store.Delete(intent.IntentUID); err != ErrIntentNotFound {
		t.Errorf("重复 Delete() error = %v, want ErrIntentNotFound", err)
	}
}

// ── TTL 过期测试 ──

func TestManualIntentStore_TTLExpiry(t *testing.T) {
	store := newTestManualIntentStore(t)

	// 创建一个已过期的意图（直接设置 expiresAt 在过去）
	intent := &ManualRoutingIntent{
		IntentType:  IntentTypeChannelTrial,
		ChannelKind: "chat",
		ExpiresAt:   time.Now().UTC().Add(-1 * time.Second),
	}
	if err := store.Create(intent); err != nil {
		t.Fatalf("Create() 失败: %v", err)
	}

	// ListActive 应不包含已过期的意图
	active := store.ListActive()
	for _, a := range active {
		if a.IntentUID == intent.IntentUID {
			t.Error("ListActive() 不应包含已过期的意图")
		}
	}

	// Get 应返回该意图，但状态应为 expired
	got := store.Get(intent.IntentUID)
	if got == nil {
		t.Fatal("Get() 返回 nil")
	}
	if got.Status != IntentStatusExpired {
		t.Errorf("过期意图 Status = %q, want %q", got.Status, IntentStatusExpired)
	}

	// ListAll 应包含
	all := store.ListAll()
	found := false
	for _, a := range all {
		if a.IntentUID == intent.IntentUID {
			found = true
		}
	}
	if !found {
		t.Error("ListAll() 应包含已过期的意图")
	}
}

// ── 预算耗尽测试 ──

func TestManualIntentStore_BudgetExhaustion(t *testing.T) {
	store := newTestManualIntentStore(t)

	intent := sampleIntent(1 * time.Hour)
	intent.MaxRequests = 3
	_ = store.Create(intent)

	// 记录 3 次命中
	for i := 0; i < 3; i++ {
		if err := store.RecordHit(intent.IntentUID, true, 100); err != nil {
			t.Fatalf("RecordHit() 第 %d 次失败: %v", i+1, err)
		}
	}

	// 第 3 次命中后应自动转为 exhausted
	got := store.Get(intent.IntentUID)
	if got == nil {
		t.Fatal("Get() 返回 nil")
	}
	if got.Status != IntentStatusExhausted {
		t.Errorf("预算耗尽后 Status = %q, want %q", got.Status, IntentStatusExhausted)
	}
	if got.TrialResult.HitCount != 3 {
		t.Errorf("HitCount = %d, want 3", got.TrialResult.HitCount)
	}

	// ListActive 不应包含
	active := store.ListActive()
	for _, a := range active {
		if a.IntentUID == intent.IntentUID {
			t.Error("ListActive() 不应包含预算耗尽的意图")
		}
	}
}

func TestManualIntentStore_CostBudgetExhaustion(t *testing.T) {
	store := newTestManualIntentStore(t)

	intent := sampleIntent(1 * time.Hour)
	intent.MaxEstimatedCost = 1.0
	_ = store.Create(intent)

	// 模拟估算成本达到 1.0
	intent.TrialResult.EstimatedCost = 1.0
	// 手动在缓存中更新（RecordHit 不直接更新成本，由调度层设置）
	store.mu.Lock()
	store.cache[intent.IntentUID].TrialResult.EstimatedCost = 1.0
	store.mu.Unlock()

	// 触发惰性检查
	got := store.Get(intent.IntentUID)
	if got == nil {
		t.Fatal("Get() 返回 nil")
	}
	if got.Status != IntentStatusExhausted {
		t.Errorf("成本预算耗尽后 Status = %q, want %q", got.Status, IntentStatusExhausted)
	}
}

// ── 命中统计测试 ──

func TestManualIntentStore_RecordHit(t *testing.T) {
	store := newTestManualIntentStore(t)

	intent := sampleIntent(24 * time.Hour)
	_ = store.Create(intent)

	// 记录多次命中
	testCases := []struct {
		success   bool
		latencyMs int64
	}{
		{true, 150},
		{true, 200},
		{false, 5000},
		{true, 100},
	}

	for _, tc := range testCases {
		if err := store.RecordHit(intent.IntentUID, tc.success, tc.latencyMs); err != nil {
			t.Fatalf("RecordHit() 失败: %v", err)
		}
	}

	got := store.Get(intent.IntentUID)
	if got == nil {
		t.Fatal("Get() 返回 nil")
	}

	tr := got.TrialResult
	if tr.HitCount != 4 {
		t.Errorf("HitCount = %d, want 4", tr.HitCount)
	}
	if tr.SuccessCount != 3 {
		t.Errorf("SuccessCount = %d, want 3", tr.SuccessCount)
	}
	if tr.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", tr.FailureCount)
	}
	if tr.TotalLatencyMs != 5450 {
		t.Errorf("TotalLatencyMs = %d, want 5450", tr.TotalLatencyMs)
	}
	expectedAvg := float64(5450) / float64(4)
	if tr.AvgLatencyMs != expectedAvg {
		t.Errorf("AvgLatencyMs = %f, want %f", tr.AvgLatencyMs, expectedAvg)
	}
}

func TestManualIntentStore_RecordHit_NotFound(t *testing.T) {
	store := newTestManualIntentStore(t)

	err := store.RecordHit("nonexistent", true, 100)
	if err != ErrIntentNotFound {
		t.Errorf("RecordHit() 不存在的 uid error = %v, want ErrIntentNotFound", err)
	}
}

func TestManualIntentStore_RecordFallback(t *testing.T) {
	store := newTestManualIntentStore(t)

	intent := sampleIntent(24 * time.Hour)
	_ = store.Create(intent)
	_ = store.RecordFallback(intent.IntentUID)
	_ = store.RecordFallback(intent.IntentUID)

	got := store.Get(intent.IntentUID)
	if got == nil {
		t.Fatal("Get() 返回 nil")
	}
	if got.TrialResult.FallbackCount != 2 {
		t.Errorf("FallbackCount = %d, want 2", got.TrialResult.FallbackCount)
	}
}

// ── 持久化+重载测试 ──

func TestManualIntentStore_Persistence(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}

	// 创建并写入
	store1, err := NewManualIntentStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}

	intent := sampleIntent(1 * time.Hour)
	intent.Name = "持久化测试"
	_ = store1.Create(intent)
	_ =

		// 记录一次命中
		store1.RecordHit(intent.IntentUID, true, 200)

	// 重新加载（模拟重启）
	store2, err := NewManualIntentStoreWithDB(db)
	if err != nil {
		t.Fatalf("重载 Store 失败: %v", err)
	}

	got := store2.Get(intent.IntentUID)
	if got == nil {
		t.Fatal("重载后 Get() 返回 nil")
	}
	if got.Name != "持久化测试" {
		t.Errorf("重载后 Name = %q, want %q", got.Name, "持久化测试")
	}
	if got.TrialResult.HitCount != 1 {
		t.Errorf("重载后 HitCount = %d, want 1", got.TrialResult.HitCount)
	}
	if got.TrialResult.SuccessCount != 1 {
		t.Errorf("重载后 SuccessCount = %d, want 1", got.TrialResult.SuccessCount)
	}
}

// ── Handler 测试 ──

func TestHandler_CreateIntent(t *testing.T) {
	store := newTestManualIntentStore(t)
	r := newTestRouter(store)

	body := `{
		"intentType": "model_trial",
		"channelKind": "messages",
		"model": "fable-5",
		"name": "试用 fable-5",
		"ttlMinutes": 60,
		"maxRequests": 100,
		"reason": "新模型测试"
	}`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/manual-intents", nil)
	req.Header.Set("Content-Type", "application/json")
	// 使用 httptest 方式直接设置 body
	req.Body = newStringReader(body)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("POST /manual-intents 状态码 = %d, want %d, body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
}

func TestHandler_CreateIntent_ValidationError(t *testing.T) {
	store := newTestManualIntentStore(t)
	r := newTestRouter(store)

	// 缺少 channelKind
	body := `{"intentType": "model_trial"}`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/manual-intents", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = newStringReader(body)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("校验失败时状态码 = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandler_ListIntents(t *testing.T) {
	store := newTestManualIntentStore(t)
	r := newTestRouter(store)
	_ =

		// 创建一个活跃意图
		store.Create(sampleIntent(1 * time.Hour))

	// 创建一个已过期意图
	expired := &ManualRoutingIntent{
		IntentType:  IntentTypeChannelTrial,
		ChannelKind: "chat",
		ExpiresAt:   time.Now().UTC().Add(-1 * time.Hour),
	}
	_ = store.Create(expired)

	// 默认只返回 active
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/manual-intents", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /manual-intents 状态码 = %d, want %d", w.Code, http.StatusOK)
	}

	// all=true 返回全部
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/manual-intents?all=true", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /manual-intents?all=true 状态码 = %d, want %d", w.Code, http.StatusOK)
	}
}

// TestHandler_ListIntentsNeverReturnsNullSlice 锁定 intents 字段必须是 JSON 数组。
// nil slice 会被序列化成 null，前端对 null 取 .length 会抛错并导致驾驶舱整页空白。
func TestHandler_ListIntentsNeverReturnsNullSlice(t *testing.T) {
	tests := []struct {
		name  string
		query string
		seed  func(store *ManualIntentStore)
	}{
		{
			name:  "空 store 默认查询",
			query: "/manual-intents",
			seed:  func(_ *ManualIntentStore) {},
		},
		{
			name:  "空 store all=true",
			query: "/manual-intents?all=true",
			seed:  func(_ *ManualIntentStore) {},
		},
		{
			name:  "仅有过期意图时默认查询",
			query: "/manual-intents",
			seed: func(store *ManualIntentStore) {
				_ = store.Create(sampleIntent(-1 * time.Hour))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestManualIntentStore(t)
			tt.seed(store)
			r := newTestRouter(store)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", tt.query, nil)
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("GET %s 状态码 = %d, want %d", tt.query, w.Code, http.StatusOK)
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
				t.Fatalf("解析响应失败: %v, body=%s", err, w.Body.String())
			}

			got := string(raw["intents"])
			if got == "null" {
				t.Errorf("intents 序列化为 null，前端会渲染失败; body=%s", w.Body.String())
			}
			if !strings.HasPrefix(got, "[") {
				t.Errorf("intents = %s, want JSON 数组", got)
			}
		})
	}
}

func TestHandler_GetIntent(t *testing.T) {
	store := newTestManualIntentStore(t)
	r := newTestRouter(store)

	intent := sampleIntent(1 * time.Hour)
	_ = store.Create(intent)

	// 正常获取
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/manual-intents/"+intent.IntentUID, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /manual-intents/:uid 状态码 = %d, want %d", w.Code, http.StatusOK)
	}

	// 不存在
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/manual-intents/nonexistent", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /manual-intents/nonexistent 状态码 = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandler_DeleteIntent(t *testing.T) {
	store := newTestManualIntentStore(t)
	r := newTestRouter(store)

	intent := sampleIntent(1 * time.Hour)
	_ = store.Create(intent)

	// 删除
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/manual-intents/"+intent.IntentUID, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("DELETE /manual-intents/:uid 状态码 = %d, want %d", w.Code, http.StatusOK)
	}

	// 重复删除
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("DELETE", "/manual-intents/"+intent.IntentUID, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("重复 DELETE 状态码 = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// ── 辅助 ──

// newStringReader 从字符串构造 http 请求的 body。
type stringReader struct {
	data []byte
	pos  int
}

func newStringReader(s string) *stringReader {
	return &stringReader{data: []byte(s)}
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, nil
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *stringReader) Close() error {
	return nil
}
