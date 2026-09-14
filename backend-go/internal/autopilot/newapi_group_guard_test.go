package autopilot

import (
	"fmt"
	"testing"
)

func TestResolveNewApiProvisionGroups_AllEligible(t *testing.T) {
	limit := 1.0
	resolved, err := resolveNewApiProvisionGroups(map[string]float64{
		"default": 1,
		"cheap":   0.5,
		"premium": 2,
	}, "", true, &limit)
	if err != nil {
		t.Fatalf("resolveNewApiProvisionGroups 返回错误: %v", err)
	}
	if len(resolved) != 2 || resolved[0].Name != "cheap" || resolved[0].Ratio != 0.5 || resolved[1].Name != "default" || resolved[1].MaxMultiplier != 1 {
		t.Fatalf("自动选择的分组不匹配: %+v", resolved)
	}
}

func TestResolveNewApiProvisionGroups_EmptyRequestAutoSelectsAllEligible(t *testing.T) {
	limit := 1.0
	resolved, err := resolveNewApiProvisionGroups(map[string]float64{
		"vip":     0.5,
		"default": 1.2,
	}, "", false, &limit)
	if err != nil {
		t.Fatalf("resolveNewApiProvisionGroups 返回错误: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Name != "vip" {
		t.Fatalf("空请求应按上游合格分组接入，实际: %+v", resolved)
	}
}

func TestResolveNewApiProvisionGroups_EmptyRequestRejectsAllOverLimit(t *testing.T) {
	limit := 1.0
	if _, err := resolveNewApiProvisionGroups(map[string]float64{
		"premium": 2.0,
		"vip":     1.5,
	}, "", false, &limit); err == nil {
		t.Fatal("全部超限应返回错误")
	}
}

func TestResolveNewApiProvisionGroupsRejectsUnsafeOrUnknownGroups(t *testing.T) {
	limit := 1.0
	groups := map[string]float64{"default": 1, "premium": 2}
	for _, group := range []string{"premium", "missing"} {
		if _, err := resolveNewApiProvisionGroups(groups, group, false, &limit); err == nil {
			t.Fatalf("分组 %q 应被拒绝", group)
		}
	}
}

func TestResolveNewApiProvisionGroupsRejectsAmbiguousMode(t *testing.T) {
	limit := 1.0
	if _, err := resolveNewApiProvisionGroups(map[string]float64{"default": 1}, "default", true, &limit); err == nil {
		t.Fatal("显式分组和自动全部模式不能同时存在")
	}
}

func TestDefaultNewApiProvisionKeyNameForGroup(t *testing.T) {
	if got := defaultNewApiProvisionKeyNameForGroup("Premium Group"); got != "ccx-premium-group" {
		t.Fatalf("分组 Key 名称 = %q", got)
	}
}

func TestPartitionNewApiEmptyGroups(t *testing.T) {
	cases := []struct {
		name        string
		groups      []newApiResolvedGroup
		counts      map[string]int
		wantKept    []string
		wantSkipped []string
	}{
		{
			name: "0 模型分组被剔除",
			groups: []newApiResolvedGroup{
				{Name: "default", Ratio: 1},
				{Name: "画图", Ratio: 1},
			},
			counts:      map[string]int{"default": 3, "画图": 0},
			wantKept:    []string{"default"},
			wantSkipped: []string{"画图"},
		},
		{
			name: "无计数记录的分组保守保留（fork 忽略 group 参数或查询失败）",
			groups: []newApiResolvedGroup{
				{Name: "default", Ratio: 1},
				{Name: "premium", Ratio: 1},
			},
			counts:   map[string]int{"default": 1},
			wantKept: []string{"default", "premium"},
		},
		{
			name: "计数全空时 kept 为空",
			groups: []newApiResolvedGroup{
				{Name: "a", Ratio: 1},
				{Name: "b", Ratio: 1},
			},
			counts:      map[string]int{"a": 0, "b": 0},
			wantKept:    []string{},
			wantSkipped: []string{"a", "b"},
		},
		{
			name: "负数计数同样视为空分组",
			groups: []newApiResolvedGroup{
				{Name: "a", Ratio: 1},
			},
			counts:      map[string]int{"a": -1},
			wantKept:    []string{},
			wantSkipped: []string{"a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, skipped := partitionNewApiEmptyGroups(tc.groups, tc.counts)
			gotKept := make([]string, 0, len(kept))
			for _, g := range kept {
				gotKept = append(gotKept, g.Name)
			}
			if fmt.Sprintf("%v", gotKept) != fmt.Sprintf("%v", tc.wantKept) {
				t.Fatalf("kept = %v, want %v", gotKept, tc.wantKept)
			}
			if fmt.Sprintf("%v", skipped) != fmt.Sprintf("%v", tc.wantSkipped) {
				t.Fatalf("skipped = %v, want %v", skipped, tc.wantSkipped)
			}
		})
	}
}
