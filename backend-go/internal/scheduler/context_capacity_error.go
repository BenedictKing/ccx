package scheduler

import (
	"errors"
	"fmt"
)

// ContextCapacityError 表示选路阶段没有任何候选物理路由的上下文窗口
// 能承载当前请求。它与渠道故障语义不同：重试不会成功，客户端（如 Codex）
// 应压缩上下文或换会话；网关侧应引导到更大窗口的模型。
type ContextCapacityError struct {
	// InputTokens 是请求输入 token 估算（调度层比较口径，不含输出预留）。
	InputTokens int
	// TotalBudget 是输入 + 输出预留的总需求，仅用于诊断展示。
	TotalBudget int
	// MaxKnownWindow 是被过滤候选中最大的已知上下文窗口；0 表示全部未知。
	MaxKnownWindow int
	// Detail 是渠道粒度的被过滤原因明细。
	Detail string
}

func (e *ContextCapacityError) Error() string {
	if e.MaxKnownWindow > 0 {
		return fmt.Sprintf("没有候选物理路由可承载当前上下文：输入估算 %d tokens，最大已知窗口 %d tokens（已过滤：%s）",
			e.InputTokens, e.MaxKnownWindow, e.Detail)
	}
	return fmt.Sprintf("没有候选物理路由可承载当前上下文：输入估算 %d tokens（已过滤：%s）",
		e.InputTokens, e.Detail)
}

// AsContextCapacityError 从错误链中提取上下文容量错误。
// 选路错误会被 SelectionTraceError 包装，errors.As 可以穿透。
func AsContextCapacityError(err error) (*ContextCapacityError, bool) {
	var capErr *ContextCapacityError
	if errors.As(err, &capErr) {
		return capErr, true
	}
	return nil, false
}
