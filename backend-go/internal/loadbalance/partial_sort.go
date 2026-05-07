package loadbalance

// partialSortTopK 对 items 做 top-K stable 选择排序。
//
// 算法说明：
//   - 不依赖 github.com/viterin/partial（PRD 第 145 行已批准自实现）；
//   - K >= len(items) 时退化为 O(N) 选择 + 一次完整 stable 排序；
//   - K < len(items) 时使用基于 less 的 K 轮选择（O(N·K)），适合 K 远小于 N 的
//     LB 决策场景（典型 N = 数十个 channel，K = 1~3）。
//
// 复杂度选择理由：相比快速选择 + 末尾排序，简单的 K 轮 selection sort 实现
// 更短、绝对常数低、易于正确性验证；当 K << N 时性能足够。
//
// 排序后的前 K 个位置即"按 less 升序"的 top-K。原 items 切片就地修改。
//
// less(a, b) 返回 true 表示 a 应排在 b 前面。本包内 less 通常是
// "TotalScore 大者前；TotalScore 相同时 OrderingWeight 大者前；最后按 ID 升序保稳定"。
func partialSortTopK[T any](items []T, k int, less func(a, b T) bool) {
	n := len(items)
	if n <= 1 || k <= 0 {
		return
	}
	if k > n {
		k = n
	}
	for i := 0; i < k; i++ {
		minIdx := i
		for j := i + 1; j < n; j++ {
			if less(items[j], items[minIdx]) {
				minIdx = j
			}
		}
		if minIdx != i {
			items[i], items[minIdx] = items[minIdx], items[i]
		}
	}
}

// decisionLess 是 ChannelDecision 的标准 less 函数。
//
// 排序优先级（先满足前者）：
//  1. TotalScore 大者前；
//  2. OrderingWeight 大者前；
//  3. ID 小者前（保证 stable / 可复现）。
func decisionLess(a, b ChannelDecision) bool {
	if a.TotalScore != b.TotalScore {
		return a.TotalScore > b.TotalScore
	}
	if a.Channel != nil && b.Channel != nil {
		if a.Channel.OrderingWeight != b.Channel.OrderingWeight {
			return a.Channel.OrderingWeight > b.Channel.OrderingWeight
		}
		return a.Channel.ID < b.Channel.ID
	}
	return false
}
