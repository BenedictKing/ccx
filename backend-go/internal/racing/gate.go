package racing

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrRacingSuperseded 竞速败出：另一渠道更快交付有效响应，本分支被放弃。
// 该错误在失败分类链中必须先于 isClientSideError 拦截：不计渠道失败、不触发
// 熔断/Key 拉黑/自学习，仅完成渠道日志终态。
var ErrRacingSuperseded = errors.New("racing superseded")

// gin context keys（沿代码库 ccx.* 惯例；编排器在分支 context 上设置）。
const (
	ContextKeyGate = "ccx.racing.gate" // *Gate
	ContextKeyRole = "ccx.racing.role" // "primary" | "shadow"
)

// 角色/结果常量。
const (
	RolePrimary = "primary"
	RoleShadow  = "shadow"
)

// Gate 客户端提交闸门（claim-once）：并行竞速分支中唯一赢家获得向客户端
// 写出响应的资格，其余分支在 claim 失败后以 ErrRacingSuperseded 收尾。
//
// claimed/winner 用 atomic 存储，查询无锁；cancelMu 注册各分支的 context
// 取消函数：赢家 claim 时立即取消其余分支，不必等败者自然跑完整个流。
// 分支写出的单写者不变量由分支 writer 保证（handlers/common.racingBranchWriter：
// 真实客户端 writer 只在赢家 Commit 时被触碰）。
type Gate struct {
	claimed  atomic.Bool
	winner   atomic.Int64
	cancelMu sync.Mutex
	cancels  map[int]context.CancelFunc
}

// NewGate 创建闸门。
func NewGate() *Gate {
	return &Gate{cancels: make(map[int]context.CancelFunc)}
}

// RegisterCancel 注册分支取消函数（编排器在各分支启动前调用）。
func (g *Gate) RegisterCancel(ownerID int, cancel context.CancelFunc) {
	g.cancelMu.Lock()
	g.cancels[ownerID] = cancel
	g.cancelMu.Unlock()
}

// ClaimBy 以 ownerID 身份竞争提交权：首次调用返回 true 并取消其余分支。
func (g *Gate) ClaimBy(ownerID int) bool {
	if !g.claimed.CompareAndSwap(false, true) {
		return false
	}
	g.winner.Store(int64(ownerID))
	g.cancelMu.Lock()
	cancels := make(map[int]context.CancelFunc, len(g.cancels))
	for id, cancel := range g.cancels {
		cancels[id] = cancel
	}
	g.cancelMu.Unlock()
	for id, cancel := range cancels {
		if id != ownerID {
			cancel()
		}
	}
	return true
}

// Claim 兼容入口（无分支编号场景）。
func (g *Gate) Claim() bool { return g.ClaimBy(-1) }

// Claimed 是否已有赢家（含跨分支查询，指导败者跳过 pre-commit 副作用）。
func (g *Gate) Claimed() bool { return g.claimed.Load() }

// ClaimedBy 返回赢家分支编号；未 claim 返回 -1。
func (g *Gate) ClaimedBy() int {
	if !g.claimed.Load() {
		return -1
	}
	return int(g.winner.Load())
}

// Participants 已注册分支数（含主分支）；1 表示无对手分支，竞速未实际发生。
func (g *Gate) Participants() int {
	g.cancelMu.Lock()
	defer g.cancelMu.Unlock()
	return len(g.cancels)
}

// CancelExcept 主动取消除 ownerID 外的全部分支（编排器在赢家结算后调用）。
func (g *Gate) CancelExcept(ownerID int) {
	g.cancelMu.Lock()
	cancels := make(map[int]context.CancelFunc, len(g.cancels))
	for id, cancel := range g.cancels {
		cancels[id] = cancel
	}
	g.cancelMu.Unlock()
	for id, cancel := range cancels {
		if id != ownerID {
			cancel()
		}
	}
}

// Semaphore 全局并发影子信号量（TryAcquire，不阻塞主流程）。
type Semaphore struct {
	mu      sync.Mutex
	limiter chan struct{}
}

// NewSemaphore 创建容量 n 的信号量（n<=0 视为 1）。
func NewSemaphore(n int) *Semaphore {
	if n <= 0 {
		n = 1
	}
	return &Semaphore{limiter: make(chan struct{}, n)}
}

// TryAcquire 尝试占一个影子并发额度。
func (s *Semaphore) TryAcquire() bool {
	select {
	case s.limiter <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release 释放一个额度。
func (s *Semaphore) Release() {
	select {
	case <-s.limiter:
	default:
	}
}
