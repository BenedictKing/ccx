package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestContextWindowCache() *ChannelCompatCache {
	return NewChannelCompatCache()
}

func TestRecordContextWindowProvenRatchet(t *testing.T) {
	cache := newTestContextWindowCache()
	now := time.Now()

	if !cache.RecordContextWindowProven("ch_a", "responses", "gpt-5.6-sol", 274_000, now) {
		t.Fatal("首次实证应返回 true")
	}
	if cache.RecordContextWindowProven("ch_a", "responses", "gpt-5.6-sol", 100_000, now) {
		t.Fatal("更小输入不应更新棘轮")
	}
	if !cache.RecordContextWindowProven("ch_a", "responses", "gpt-5.6-sol", 500_000, now) {
		t.Fatal("更大输入应更新棘轮")
	}
	proven, _ := cache.LearnedContextWindow("ch_a", "responses", "gpt-5.6-sol")
	if proven != 500_000 {
		t.Fatalf("proven = %d, want 500000（宁大勿小）", proven)
	}

	// 协议维度隔离：同渠道同模型不同协议互不污染
	proven, _ = cache.LearnedContextWindow("ch_a", "chat", "gpt-5.6-sol")
	if proven != 0 {
		t.Fatalf("chat 协议不应继承 responses 的实证值, got %d", proven)
	}
}

func TestRecordContextWindowProvenTTL(t *testing.T) {
	cache := newTestContextWindowCache()
	stale := time.Now().Add(-contextWindowLearnedTTL - time.Hour)
	cache.RecordContextWindowProven("ch_b", "responses", "m", 300_000, stale)

	proven, _ := cache.LearnedContextWindow("ch_b", "responses", "m")
	if proven != 0 {
		t.Fatalf("过期实证应失效, got %d", proven)
	}
}

// TestRecordContextWindowProvenRelearnAfterExpiry 过期实证必须可以重新学习（O4）：
// 未过期时较小成功值不降棘轮；过期后较小成功值以本次值重新起算，
// 不保留一个已无法重新证明的历史高值。
func TestRecordContextWindowProvenRelearnAfterExpiry(t *testing.T) {
	cache := newTestContextWindowCache()
	base := time.Now()

	// 建立高棘轮（500K），未过期时 200K 成功不得下调
	cache.RecordContextWindowProven("ch_c", "responses", "m", 500_000, base)
	if updated := cache.RecordContextWindowProven("ch_c", "responses", "m", 200_000, base.Add(time.Hour)); updated {
		t.Fatal("未过期时较小成功值不应触发更新")
	}
	proven, _ := cache.LearnedContextWindow("ch_c", "responses", "m")
	if proven != 500_000 {
		t.Fatalf("未过期时棘轮应保持 500000, got %d", proven)
	}

	// 过期后较小成功值重新起算棘轮与新鲜度
	afterExpiry := base.Add(contextWindowLearnedTTL + time.Minute)
	if updated := cache.RecordContextWindowProven("ch_c", "responses", "m", 200_000, afterExpiry); !updated {
		t.Fatal("过期后的成功实证应允许重新学习")
	}
	proven, _ = cache.LearnedContextWindow("ch_c", "responses", "m")
	if proven != 200_000 {
		t.Fatalf("过期重学习后棘轮应以本次成功值起算, got %d, want 200000", proven)
	}

	// 重学习后的棘轮恢复正常只升不降语义
	if updated := cache.RecordContextWindowProven("ch_c", "responses", "m", 100_000, afterExpiry.Add(time.Minute)); updated {
		t.Fatal("重学习后未过期时较小成功值不应再触发更新")
	}
	proven, _ = cache.LearnedContextWindow("ch_c", "responses", "m")
	if proven != 200_000 {
		t.Fatalf("重学习后棘轮应保持 200000, got %d", proven)
	}
}

func TestEffectiveContextWindowFormula(t *testing.T) {
	cases := []struct {
		name      string
		proven    int
		modelsAPI int
		declared  int
		registry  int
		want      int
	}{
		{
			name:     "无证据等于注册表",
			registry: 272_000,
			want:     272_000,
		},
		{
			name:     "实证顶开过期注册表",
			proven:   500_000,
			registry: 272_000,
			want:     500_000,
		},
		{
			name:      "models API 声明顶开注册表",
			modelsAPI: 1_050_000,
			registry:  272_000,
			want:      1_050_000,
		},
		{
			name:     "实测收紧上限压过注册表",
			declared: 200_000,
			registry: 1_050_000,
			want:     200_000,
		},
		{
			name:     "渠道降级时收紧上限压过陈旧实证",
			proven:   500_000,
			declared: 272_000,
			registry: 1_050_000,
			want:     272_000,
		},
		{
			name:      "多源取最强放宽再统一收紧",
			proven:    600_000,
			modelsAPI: 700_000,
			declared:  650_000,
			registry:  272_000,
			want:      650_000,
		},
		{
			name:     "注册表未知时证据生效",
			proven:   128_000,
			registry: 0,
			want:     128_000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := newTestContextWindowCache()
			now := time.Now()
			if tc.proven > 0 {
				cache.RecordContextWindowProven("ch_f", "responses", "m", tc.proven, now)
			}
			if tc.modelsAPI > 0 {
				cache.RecordModelsAPIContextWindow("ch_f", "responses", "m", tc.modelsAPI, now)
			}
			if tc.declared > 0 {
				// 走真实收紧学习路径：渠道×key×模型，跨 Key 取最小
				cache.RecordContextLimit("ch_f", "k1", "m", tc.declared, CompatSourceUpstreamDeclared, "ev", tc.declared+1000)
			}
			if got := cache.EffectiveContextWindow("ch_f", "responses", "m", tc.registry); got != tc.want {
				t.Fatalf("EffectiveContextWindow = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseModelsAPIContextWindows(t *testing.T) {
	body := []byte(`{
	  "data": [
	    {"id": "gpt-5.6-sol", "context_window": 1050000},
	    {"id": "qwen-max", "top_provider": {"context_length": 1000000}},
	    {"id": "no-meta"},
	    {"id": "vllm-model", "max_model_len": 131072}
	  ]
	}`)
	windows := ParseModelsAPIContextWindows(body)
	want := map[string]int{
		"gpt-5.6-sol": 1050000,
		"qwen-max":    1000000,
		"vllm-model":  131072,
	}
	if len(windows) != len(want) {
		t.Fatalf("windows = %v, want %v", windows, want)
	}
	for model, window := range want {
		if windows[model] != window {
			t.Errorf("windows[%s] = %d, want %d", model, windows[model], window)
		}
	}

	if got := ParseModelsAPIContextWindows([]byte(`{"data":[{"id":"x"}]}`)); got != nil {
		t.Fatalf("无元数据应返回 nil, got %v", got)
	}
	if got := ParseModelsAPIContextWindows([]byte(`not json`)); got != nil {
		t.Fatalf("非法 JSON 应返回 nil, got %v", got)
	}
}

func TestContextWindowPersistenceRoundtripAndLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "channel_compat.json")

	// 新格式 roundtrip：学习窗口与 entries 共存，重载后窗口可用。
	cache := NewChannelCompatCacheWithPersistence(path)
	now := time.Now()
	cache.RecordContextWindowProven("ch_p", "responses", "gpt-5.6-sol", 480_000, now)
	cache.RecordContextLimit("ch_p", "k1", "gpt-5.6-sol", 300_000, CompatSourceUpstreamDeclared, "ev", 320_000)

	reloaded := NewChannelCompatCacheWithPersistence(path)
	proven, _ := reloaded.LearnedContextWindow("ch_p", "responses", "gpt-5.6-sol")
	if proven != 480_000 {
		t.Fatalf("重载后 proven = %d, want 480000", proven)
	}
	if got := reloaded.EffectiveContextWindow("ch_p", "responses", "gpt-5.6-sol", 272_000); got != 300_000 {
		t.Fatalf("重载后有效窗口 = %d, want 300000（收紧上限仍在）", got)
	}

	// 旧格式（顶层即 entries map）迁移：不带 contextWindows 分区也能加载。
	legacyPath := filepath.Join(dir, "legacy_compat.json")
	legacy := map[string]*ChannelCompatEntry{
		"ch_old:k1:m": {
			Traits: map[CompatTrait]CompatTraitState{
				TraitDowngradeDeveloperRole: {Enabled: true, LearnedAt: now},
			},
			DetectedAt: now,
		},
	}
	legacyBytes, _ := json.Marshal(legacy)
	if err := os.WriteFile(legacyPath, legacyBytes, 0644); err != nil {
		t.Fatal(err)
	}
	migrated := NewChannelCompatCacheWithPersistence(legacyPath)
	if _, ok := migrated.Trait("ch_old", "k1", "m", TraitDowngradeDeveloperRole); !ok {
		t.Fatal("旧格式条目应迁移加载")
	}
	if _, modelsAPI := migrated.LearnedContextWindow("ch_old", "responses", "m"); modelsAPI != 0 {
		t.Fatalf("旧格式不应带出学习窗口, got %d", modelsAPI)
	}
}
