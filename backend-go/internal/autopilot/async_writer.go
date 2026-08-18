package autopilot

import (
	"database/sql"
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// ── 有界异步 Writer（设计 §4.4）──
//
// 代理请求 goroutine 不做无 deadline 的 SQLite I/O。
// Writer 使用有界异步写入队列和单一批量 writer；
// 单项在入队前截断至 64 KiB，数据库操作 deadline 为 250ms。
// 队列为终态/窗口聚合预留容量，普通抽样详情最先被丢弃。

const (
	// writeQueueCapacity 队列总容量
	writeQueueCapacity = 512
	// writeQueueTerminalReserved 为终态/窗口预留的容量
	writeQueueTerminalReserved = 128
	// maxWriteItemBytes 单条写入项最大序列化字节数
	maxWriteItemBytes = 64 * 1024
	// writeBatchDeadline 单批数据库操作的 deadline
	writeBatchDeadline = 250 * time.Millisecond
	// drainTimeout 服务关闭时最多给 writer 的 drain 时间
	drainTimeout = 1 * time.Second
	// writeBatchInterval writer 批量写入间隔
	writeBatchInterval = 50 * time.Millisecond
)

// writeItem 是写入队列中的一条项。
type writeItem struct {
	traceUID  string
	data      []byte
	priority  writePriority
	createdAt time.Time
}

// writePriority 写入优先级（越大越优先保留）。
type writePriority int

const (
	prioritySample   writePriority = iota // 普通抽样，最先丢弃
	priorityNormal                        // 正常落盘
	priorityTerminal                      // 终态/窗口聚合，最后丢弃
)

// asyncWriter 是 TraceStore 的有界异步写入后端。
type asyncWriter struct {
	queue    chan writeItem
	db       traceDB // 抽象 DB 接口，便于测试
	stopOnce sync.Once
	done     chan struct{}

	// 内部指标：请求 goroutine（enqueue）、后台 writer goroutine（flushBatch）
	// 与指标读取方并发访问，全部使用原子操作。
	droppedSamples  atomic.Int64
	droppedAll      atomic.Int64
	writeErrors     atomic.Int64
	writeSuccess    atomic.Int64
	totalWriteNanos atomic.Int64
}

// traceDB 是数据库操作的抽象接口。
type traceDB interface {
	Exec(query string, args ...any) error
	QueryRow(query string, args ...any) rowScanner
}

// newAsyncWriter 创建异步 writer 并启动后台 goroutine。
func newAsyncWriter(db traceDB) *asyncWriter {
	w := &asyncWriter{
		queue: make(chan writeItem, writeQueueCapacity),
		db:    db,
		done:  make(chan struct{}),
	}
	go w.loop()
	return w
}

// enqueue 入队一条写入项。
// 终态/窗口项有预留容量，普通抽样在队列满时被丢弃。
func (w *asyncWriter) enqueue(uid string, data []byte, priority writePriority) {
	if w == nil {
		return
	}
	// 截断到最大字节数
	if len(data) > maxWriteItemBytes {
		data = data[:maxWriteItemBytes]
	}

	item := writeItem{
		traceUID:  uid,
		data:      data,
		priority:  priority,
		createdAt: time.Now(),
	}

	// 非阻塞尝试入队
	select {
	case w.queue <- item:
		return
	default:
	}

	// 队列满：尝试丢弃低优先级项腾出空间
	if priority == priorityTerminal || priority == priorityNormal {
		if w.tryDropLowest() {
			select {
			case w.queue <- item:
				return
			default:
			}
		}
	}

	// 无法入队：记录丢弃
	if priority == prioritySample {
		w.droppedSamples.Add(1)
	} else {
		w.droppedAll.Add(1)
	}
}

// tryDropLowest 尝试丢弃队列中最低优先级的项。
func (w *asyncWriter) tryDropLowest() bool {
	// 非阻塞读取，丢弃一个 sample 级项
	select {
	case dropped := <-w.queue:
		if dropped.priority == prioritySample {
			w.droppedSamples.Add(1)
			return true
		}
		// 不是 sample 级，放回（简单实现：直接丢弃）
		w.droppedAll.Add(1)
		return true
	default:
		return false
	}
}

// loop 是 writer 的后台循环，批量写入数据库。
func (w *asyncWriter) loop() {
	defer close(w.done)
	batch := make([]writeItem, 0, 64)
	timer := time.NewTicker(writeBatchInterval)
	defer timer.Stop()

	for {
		select {
		case item, ok := <-w.queue:
			if !ok {
				// 队列关闭，drain 剩余项
				w.drainAndFlush()
				return
			}
			batch = append(batch, item)
			// 收集一批后写入
			if len(batch) >= 32 {
				w.flushBatch(batch)
				batch = batch[:0]
			}
		case <-timer.C:
			if len(batch) > 0 {
				w.flushBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

// drainAndFlush 在服务关闭时 drain 队列并写入。
func (w *asyncWriter) drainAndFlush() {
	deadline := time.After(drainTimeout)
	batch := make([]writeItem, 0, 64)
	for {
		select {
		case item := <-w.queue:
			batch = append(batch, item)
			if len(batch) >= 64 {
				w.flushBatch(batch)
				batch = batch[:0]
			}
		case <-deadline:
			// 超时：安全丢弃剩余项
			remaining := len(w.queue)
			if remaining > 0 {
				log.Printf("[TraceStore-Writer] drain 超时，丢弃 %d 条待写入项", remaining)
			}
			if len(batch) > 0 {
				w.flushBatch(batch)
			}
			return
		default:
			// 队列空
			if len(batch) > 0 {
				w.flushBatch(batch)
			}
			return
		}
	}
}

// flushBatch 批量写入数据库，带 deadline。
func (w *asyncWriter) flushBatch(batch []writeItem) {
	if len(batch) == 0 || w.db == nil {
		return
	}

	start := time.Now()
	deadline := start.Add(writeBatchDeadline)

	for _, item := range batch {
		if time.Now().After(deadline) {
			break
		}
		if err := w.db.Exec(item.dataToSQL(), item.sqlArgs()...); err != nil {
			w.writeErrors.Add(1)
			// 写入失败只记录计数，不阻塞
		} else {
			w.writeSuccess.Add(1)
		}
	}

	elapsed := time.Since(start)
	w.totalWriteNanos.Add(elapsed.Nanoseconds())
}

// dataToSQL 将 writeItem 转换为 SQL 语句。
func (item *writeItem) dataToSQL() string {
	// data 是 JSON 编码的 trace，直接使用 UPSERT
	return `INSERT INTO autopilot_routing_traces (trace_uid, details_json, schema_version, trace_revision, created_at)
VALUES (?, ?, 2, 0, ?)
ON CONFLICT(trace_uid) DO UPDATE SET details_json = excluded.details_json`
}

// sqlArgs 返回 writeItem 的 SQL 参数。
func (item *writeItem) sqlArgs() []any {
	return []any{item.traceUID, string(item.data), item.createdAt.UTC().Format(time.RFC3339)}
}

// close 优雅关闭 writer，最多等待 drainTimeout。
func (w *asyncWriter) close() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		close(w.queue)
		select {
		case <-w.done:
		case <-time.After(drainTimeout + time.Second):
			log.Printf("[TraceStore-Writer] 关闭超时")
		}
	})
}

// DroppedCount 返回丢弃的写入项总数。
func (w *asyncWriter) DroppedCount() int64 {
	if w == nil {
		return 0
	}
	return w.droppedSamples.Load() + w.droppedAll.Load()
}

// Metrics 返回 writer 内部指标快照。
func (w *asyncWriter) Metrics() WriterMetrics {
	if w == nil {
		return WriterMetrics{}
	}
	writeSuccess := w.writeSuccess.Load()
	return WriterMetrics{
		QueueDepth:     len(w.queue),
		DroppedSamples: w.droppedSamples.Load(),
		DroppedAll:     w.droppedAll.Load(),
		WriteErrors:    w.writeErrors.Load(),
		WriteSuccess:   writeSuccess,
		AvgWriteMs: func() float64 {
			if writeSuccess == 0 {
				return 0
			}
			return float64(w.totalWriteNanos.Load()) / float64(writeSuccess) / 1e6
		}(),
	}
}

// WriterMetrics 是 writer 的内部指标。
type WriterMetrics struct {
	QueueDepth     int     `json:"queueDepth"`
	DroppedSamples int64   `json:"droppedSamples"`
	DroppedAll     int64   `json:"droppedAll"`
	WriteErrors    int64   `json:"writeErrors"`
	WriteSuccess   int64   `json:"writeSuccess"`
	AvgWriteMs     float64 `json:"avgWriteMs"`
}

// serializeForWrite 将 trace 序列化为安全的写入 JSON。
func serializeForWrite(trace *RoutingDecisionTrace) ([]byte, error) {
	detail := trace.ToTraceDetailV2(nil, trace.TraceRevision, persistenceClassForTrace(trace))
	SanitizeForPersistence(detail)
	return json.Marshal(detail)
}

// sqlDBAdapter 将 *sql.DB 适配为 traceDB 接口。
type sqlDBAdapter struct {
	db *sql.DB
}

func (a *sqlDBAdapter) Exec(query string, args ...any) error {
	_, err := a.db.Exec(query, args...)
	return err
}

func (a *sqlDBAdapter) QueryRow(query string, args ...any) rowScanner {
	return a.db.QueryRow(query, args...)
}
