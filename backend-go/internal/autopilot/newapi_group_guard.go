package autopilot

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// DefaultNewApiMaxGroupMultiplier 让自动接入默认只使用正常倍率分组，用户可显式提高上限。
const DefaultNewApiMaxGroupMultiplier = 1.0

type newApiResolvedGroup struct {
	Name          string
	Ratio         float64
	MaxMultiplier float64
}

// resolveNewApiProvisionGroups 解析本次允许自动建 Key 的分组。
//
// 保持旧 API 的空 ProvisionGroup=default 语义；新 UI 通过 allEligible=true 明确要求
// 为每个不超过倍率阈值的分组创建独立 Key，避免无提示地改变旧客户端的分组选择。
func resolveNewApiProvisionGroups(groups map[string]float64, requested string, allEligible bool, requestedMax *float64) ([]newApiResolvedGroup, error) {
	maxMultiplier := DefaultNewApiMaxGroupMultiplier
	if requestedMax != nil {
		maxMultiplier = *requestedMax
	}
	if math.IsNaN(maxMultiplier) || math.IsInf(maxMultiplier, 0) || maxMultiplier < 0 {
		return nil, fmt.Errorf("分组倍率上限必须是大于等于 0 的有限数字")
	}

	requested = strings.TrimSpace(requested)
	if requested != "" && allEligible {
		return nil, fmt.Errorf("不能同时指定 provisionGroup 和 allEligibleGroups")
	}

	if requested != "" {
		group, err := resolveNewApiSingleProvisionGroup(groups, requested, maxMultiplier)
		if err != nil {
			return nil, err
		}
		return []newApiResolvedGroup{group}, nil
	}

	// 空分组请求统一按倍率阈值接入上游返回的全部合格分组。
	// 不再猜测 default；单分组模式必须由调用方显式传 provisionGroup。

	eligible := make([]string, 0, len(groups))
	for name, ratio := range groups {
		if strings.TrimSpace(name) != "" && validNewApiGroupRatio(ratio) && ratio <= maxMultiplier {
			eligible = append(eligible, name)
		}
	}
	if len(eligible) == 0 {
		return nil, fmt.Errorf("没有倍率不高于 %.4g 的可用分组", maxMultiplier)
	}
	sort.Slice(eligible, func(i, j int) bool {
		left, right := groups[eligible[i]], groups[eligible[j]]
		return left < right || (left == right && eligible[i] < eligible[j])
	})
	resolved := make([]newApiResolvedGroup, 0, len(eligible))
	for _, name := range eligible {
		resolved = append(resolved, newApiResolvedGroup{
			Name:          name,
			Ratio:         groups[name],
			MaxMultiplier: maxMultiplier,
		})
	}
	return resolved, nil
}

func resolveNewApiSingleProvisionGroup(groups map[string]float64, requested string, maxMultiplier float64) (newApiResolvedGroup, error) {
	ratio, ok := groups[requested]
	if !ok {
		return newApiResolvedGroup{}, fmt.Errorf("分组 %q 不在当前账户可用分组中", requested)
	}
	if !validNewApiGroupRatio(ratio) {
		return newApiResolvedGroup{}, fmt.Errorf("分组 %q 的倍率无效", requested)
	}
	if ratio > maxMultiplier {
		return newApiResolvedGroup{}, fmt.Errorf("分组 %q 的倍率 %.4g 超过上限 %.4g", requested, ratio, maxMultiplier)
	}
	return newApiResolvedGroup{Name: requested, Ratio: ratio, MaxMultiplier: maxMultiplier}, nil
}

func validNewApiGroupRatio(ratio float64) bool {
	return !math.IsNaN(ratio) && !math.IsInf(ratio, 0) && ratio >= 0
}

func defaultNewApiProvisionKeyNameForGroup(group string) string {
	suffix := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return '-'
	}, strings.TrimSpace(group))
	suffix = strings.Trim(suffix, "-")
	if suffix == "" {
		return DefaultNewApiProvisionKeyName
	}
	return DefaultNewApiProvisionKeyName + "-" + suffix
}
