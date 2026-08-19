package healthcheck

import (
	"math"
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
		[]string{"glm-5.3", "glm-latest", "deepseek-v4-flash"},
		nil, nil, policy, now)

	seen := map[string]bool{}
	for _, model := range got {
		seen[model] = true
	}
	if seen["glm-5.3"] && seen["glm-latest"] {
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

// fakeUsageResolver 实现 ProbeUsageResolver，用于测试 AFP 余额联动。
type fakeUsageResolver struct {
	usage *config.VolcenginePlanUsage
}

func (f *fakeUsageResolver) ResolveVolcenginePlanUsage(accountUID, credentialUID string) *config.VolcenginePlanUsage {
	return f.usage
}

func TestVolcengineRemainingAFP(t *testing.T) {
	cases := []struct {
		name  string
		usage *config.VolcenginePlanUsage
		want  float64
	}{
		{"nil 快照返回 0", nil, 0},
		{
			name:  "全窗口无 Quota 返回未知",
			usage: &config.VolcenginePlanUsage{FiveHour: &config.VolcenginePlanUsageWindow{UsedPercent: floatPtr(0.5)}},
			want:  math.MaxFloat64,
		},
		{
			name: "取多窗口剩余最小值",
			usage: &config.VolcenginePlanUsage{
				FiveHour: &config.VolcenginePlanUsageWindow{Quota: 100, Used: 30},  // 剩 70
				Daily:    &config.VolcenginePlanUsageWindow{Quota: 200, Used: 190}, // 剩 10
				Weekly:   &config.VolcenginePlanUsageWindow{Quota: 500, Used: 100}, // 剩 400
			},
			want: 10,
		},
		{
			name: "耗尽窗口返回 0",
			usage: &config.VolcenginePlanUsage{
				FiveHour: &config.VolcenginePlanUsageWindow{Quota: 100, Used: 120}, // 负剩余
			},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := volcengineRemainingAFP(tc.usage)
			if got != tc.want {
				t.Fatalf("volcengineRemainingAFP = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClampCostByVolcengineBalance(t *testing.T) {
	u := &config.UpstreamConfig{ProviderID: "volcengine", AccountUID: "acct_volc"}

	cases := []struct {
		name     string
		resolver ProbeUsageResolver
		maxCost  float64
		want     float64
	}{
		{
			name:     "无 resolver 保持原上限",
			resolver: nil,
			maxCost:  12,
			want:     12,
		},
		{
			name:     "余额未知（无快照）保持原上限",
			resolver: &fakeUsageResolver{usage: nil},
			maxCost:  12,
			want:     12,
		},
		{
			name: "余额充裕不收紧（5% 上界 > maxCost）",
			resolver: &fakeUsageResolver{usage: &config.VolcenginePlanUsage{
				FiveHour: &config.VolcenginePlanUsageWindow{Quota: 10000, Used: 0}, // 剩 10000, 5% = 500
			}},
			maxCost: 12,
			want:    12,
		},
		{
			name: "余额紧张收紧到 5% 比例",
			resolver: &fakeUsageResolver{usage: &config.VolcenginePlanUsage{
				FiveHour: &config.VolcenginePlanUsageWindow{Quota: 100, Used: 0}, // 剩 100, 5% = 5
			}},
			maxCost: 12,
			want:    5,
		},
		{
			name: "余额耗尽关闭 AFP 探测预算",
			resolver: &fakeUsageResolver{usage: &config.VolcenginePlanUsage{
				FiveHour: &config.VolcenginePlanUsageWindow{Quota: 100, Used: 100}, // 剩 0
			}},
			maxCost: 12,
			want:    0,
		},
		{
			name:     "maxCost 为 0 直接返回 0",
			resolver: &fakeUsageResolver{usage: &config.VolcenginePlanUsage{FiveHour: &config.VolcenginePlanUsageWindow{Quota: 100, Used: 0}}},
			maxCost:  0,
			want:     0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(func() config.Config { return config.Config{} }, newFakeKeyHealthStore(), nil, nil, Options{})
			if tc.resolver != nil {
				m.SetProbeUsageResolver(tc.resolver)
			}
			got := m.clampCostByVolcengineBalance(u, tc.maxCost)
			if got != tc.want {
				t.Fatalf("clampCostByVolcengineBalance = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSelectL2ProbeModelsAFP余额收紧预算(t *testing.T) {
	m := NewManager(func() config.Config { return config.Config{} }, newFakeKeyHealthStore(), nil, nil, Options{})
	u := &config.UpstreamConfig{ProviderID: "volcengine", AccountUID: "acct_volc"}
	// 余额紧张：剩余 40 AFP，5% = 2，仅够 flash(2)/glm(2) 之一
	m.SetProbeUsageResolver(&fakeUsageResolver{usage: &config.VolcenginePlanUsage{
		FiveHour: &config.VolcenginePlanUsageWindow{Quota: 40, Used: 0},
	}})
	policy := config.ResolvedHealthCheckPolicy{
		SparseL2MaxModels:  3,
		SparseL2MaxCostAFP: 6,
		L2ModelQuietPeriod: time.Hour,
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))

	got := m.selectL2ProbeModels("ch-volc", "keyhash", u,
		[]string{"kimi-k3", "deepseek-v4-pro", "glm-5.2", "deepseek-v4-flash"},
		nil, nil, policy, now)

	// 成本上限被余额收紧到 2 AFP：仅第一个便宜模型（glm 或 flash，均 2 AFP）可入选，
	// 累计后其余非失败模型超预算被跳过。
	if len(got) != 1 {
		t.Fatalf("余额收紧后应仅选中 1 个模型，结果 = %v", got)
	}
	if got[0] != "glm-5.2" && got[0] != "deepseek-v4-flash" {
		t.Fatalf("应选中最便宜的 2 AFP 模型，结果 = %v", got)
	}
}

func TestSelectL2ProbeModelsAFP余额充裕不收紧(t *testing.T) {
	m := NewManager(func() config.Config { return config.Config{} }, newFakeKeyHealthStore(), nil, nil, Options{})
	u := &config.UpstreamConfig{ProviderID: "volcengine", AccountUID: "acct_volc"}
	// 余额充裕：剩余 100000 AFP，5% = 5000，远大于静态预算
	m.SetProbeUsageResolver(&fakeUsageResolver{usage: &config.VolcenginePlanUsage{
		FiveHour: &config.VolcenginePlanUsageWindow{Quota: 100000, Used: 0},
	}})
	policy := config.ResolvedHealthCheckPolicy{
		SparseL2MaxModels:  3,
		SparseL2MaxCostAFP: 6,
		L2ModelQuietPeriod: time.Hour,
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))

	got := m.selectL2ProbeModels("ch-volc", "keyhash", u,
		[]string{"kimi-k3", "deepseek-v4-pro", "glm-5.2", "deepseek-v4-flash"},
		nil, nil, policy, now)

	// 与无 resolver 时同构：动态放宽后成本上限 12，选中 glm(2)+flash(2)+pro(5)=9<=12。
	want := []string{"glm-5.2", "deepseek-v4-flash", "deepseek-v4-pro"}
	if len(got) != len(want) {
		t.Fatalf("余额充裕不应收紧预算，结果 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("余额充裕不应收紧预算，结果 = %v，期望 %v", got, want)
		}
	}
}
