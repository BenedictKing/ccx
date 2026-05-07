package usage

import "context"

// UsageStore 用量记录存储接口。
//
// PR3 范围仅含 Append/Close；查询能力（聚合/分组）不在本 PR 范围，由 dashboard
// 通过内存 channel_metrics 提供，NDJSON 仅作审计/复算。
type UsageStore interface {
	// Append 写一条用量记录。线程安全；写入路径会先入内存 buffer，由后台
	// flush goroutine 周期性 fsync 到磁盘。Append 本身不会 fsync。
	Append(ctx context.Context, rec UsageRecord) error

	// Close 优雅关闭：停 ticker、flush 残余 buffer、关闭 fd。Close 后再调用
	// Append 会返回 ErrStoreClosed。
	Close() error
}
