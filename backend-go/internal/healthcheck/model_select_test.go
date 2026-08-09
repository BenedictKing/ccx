package healthcheck

import (
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
)

func TestEffectiveSparseBudget(t *testing.T) {
	cases := []struct {
		name                string
		baseModels          int
		baseCost            float64
		modelCount          int
		recentlyFailedCount int
		loadRatio           float64
		wantModels          int
		wantCost            float64
	}{
		{"基线无放宽", 3, 6, 4, 0, 0, 3, 12},
		{"模型多可放宽", 3, 6, 9, 0, 0, 5, 12},
		{"失败过多不占宽", 3, 6, 9, 5, 0, 3, 12},
		{"负载高收缩", 3, 6, 9, 0, 2, 3, 6},
		{"无成本限制保持0", 3, 0, 9, 0, 0, 5, 0},
		{"base为0关闭", 0, 6, 9, 0, 0, 0, 6},
		{"max extra cap", 3, 6, 100, 0, 0, 8, 12},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := config.ResolvedHealthCheckPolicy{
				SparseL2MaxModels:  tc.baseModels,
				SparseL2MaxCostAFP: tc.baseCost,
			}
			maxModels, maxCostAFP := effectiveSparseBudget(policy, tc.modelCount, tc.recentlyFailedCount, tc.loadRatio)
			if maxModels != tc.wantModels {
				t.Fatalf("maxModels = %d, want %d", maxModels, tc.wantModels)
			}
			if maxCostAFP != tc.wantCost {
				t.Fatalf("maxCostAFP = %v, want %v", maxCostAFP, tc.wantCost)
			}
		})
	}
}

func TestSelectL2ProbeModels成本预算与数量上限(t *testing.T) {
	m := NewManager(func() config.Config { return config.Config{} }, newFakeKeyHealthStore(), nil, nil, Options{})
	u := &config.UpstreamConfig{ProviderID: "volcengine"}
	policy := config.ResolvedHealthCheckPolicy{
		SparseL2MaxModels:  3,
		SparseL2MaxCostAFP: 6,
		L2ModelQuietPeriod: time.Hour,
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))

	got := m.selectL2ProbeModels("ch-volc", "keyhash", u,
		[]string{"kimi-k3", "deepseek-v4-pro", "glm-5.2", "deepseek-v4-flash"},
		nil, nil, policy, now)

	// flash=2 AFP，glm=2 AFP（活动期），pro=5 AFP，k3=8 AFP；动态放宽后数量上限 3、成本上限 12，
	// 因此选中 glm + flash + pro。
	want := []string{"glm-5.2", "deepseek-v4-flash", "deepseek-v4-pro"}
	if len(got) != len(want) {
		t.Fatalf("选择结果 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("选择结果 = %v，期望 %v", got, want)
		}
	}
}

func TestSelectL2ProbeModels失败优先可突破成本预算(t *testing.T) {
	m := NewManager(func() config.Config { return config.Config{} }, newFakeKeyHealthStore(), nil, nil, Options{})
	u := &config.UpstreamConfig{ProviderID: "volcengine"}
	policy := config.ResolvedHealthCheckPolicy{
		SparseL2MaxModels:  2,
		SparseL2MaxCostAFP: 2,
		L2ModelQuietPeriod: time.Hour,
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	prev := map[string]metrics.KeyHealthRecord{
		"kimi-k3": {LastStatus: StatusError, LastCheckAt: now.Add(-10 * time.Minute), ConsecutiveFailures: 1},
	}

	got := m.selectL2ProbeModels("ch-volc", "keyhash", u,
		[]string{"deepseek-v4-flash", "kimi-k3", "glm-5.2"},
		nil, prev, policy, now)

	if len(got) != 1 || got[0] != "kimi-k3" {
		t.Fatalf("失败模型应突破预算并优先，结果 = %v", got)
	}
}

func TestSelectL2ProbeModels近期成功降级(t *testing.T) {
	m := NewManager(func() config.Config { return config.Config{} }, newFakeKeyHealthStore(), nil, nil, Options{})
	u := &config.UpstreamConfig{ProviderID: "volcengine"}
	policy := config.ResolvedHealthCheckPolicy{
		SparseL2MaxModels:  2,
		SparseL2MaxCostAFP: 10,
		L2ModelQuietPeriod: time.Hour,
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	prev := map[string]metrics.KeyHealthRecord{
		"deepseek-v4-flash": {LastStatus: StatusOK, LastCheckAt: now.Add(-10 * time.Minute)},
	}

	got := m.selectL2ProbeModels("ch-volc", "keyhash", u,
		[]string{"deepseek-v4-flash", "glm-5.2", "deepseek-v4-pro"},
		nil, prev, policy, now)

	// flash 虽更便宜，但近期成功，应排在无成功记录的 glm/pro 之后
	if len(got) < 1 || got[0] != "glm-5.2" {
		t.Fatalf("无成功记录模型应优先，结果 = %v", got)
	}
}

func TestSelectL2ProbeModels别名去重(t *testing.T) {
	m := NewManager(func() config.Config { return config.Config{} }, newFakeKeyHealthStore(), nil, nil, Options{})
	u := &config.UpstreamConfig{ProviderID: "volcengine"}
	policy := config.ResolvedHealthCheckPolicy{
		SparseL2MaxModels:  3,
		SparseL2MaxCostAFP: 20,
		L2ModelQuietPeriod: time.Hour,
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))

	got := m.selectL2ProbeModels("ch-volc", "keyhash", u,
		[]string{"glm-5.2", "glm-latest", "deepseek-v4-flash"},
		nil, nil, policy, now)

	seen := map[string]bool{}
	for _, model := range got {
		seen[model] = true
	}
	if seen["glm-5.2"] && seen["glm-latest"] {
		t.Fatalf("glm alias 不应重复探测: %v", got)
	}
}

func TestL2ModelCheckKind解析(t *testing.T) {
	kind := l2ModelCheckKind("kimi-k3")
	if kind != "l2:kimi-k3" {
		t.Fatalf("check kind = %q", kind)
	}
	model, ok := parseL2ModelCheckKind(kind)
	if !ok || model != "kimi-k3" {
		t.Fatalf("解析结果 = %q/%v", model, ok)
	}
	if _, ok := parseL2ModelCheckKind("l2"); ok {
		t.Fatal("普通 l2 不应解析为模型级记录")
	}
}
