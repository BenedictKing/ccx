package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// ErrStoreClosed Append/flush 在 store 已关闭后被调用时返回。
var ErrStoreClosed = errors.New("usage: store is closed")

// fileNameLayout NDJSON 日切文件名模板（UTC，固定 ISO 日期）。
const fileNameLayout = "usage-2006-01-02.ndjson"

// fileNameRegexp 匹配合法 NDJSON 文件名，从中抽出日期部分。
var fileNameRegexp = regexp.MustCompile(`^usage-(\d{4}-\d{2}-\d{2})\.ndjson$`)

// clock 抽象时间源，便于测试中注入 mock clock。
type clock interface {
	Now() time.Time
}

// realClock 默认的 wall-clock 实现。
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// NDJSONStore 基于 NDJSON 的 UsageStore 实现。
//
// 并发模型：
//   - Append 走互斥锁保护 bufio.Writer + fd；
//   - 后台 goroutine 每 FlushInterval flush 一次，并检查 UTC 日期变化触发 reopen；
//   - Close 通过 closeCh 通知 goroutine 退出，等其结束后做最后一次 flush。
type NDJSONStore struct {
	cfg   Config
	clock clock

	mu          sync.Mutex
	file        *os.File
	writer      *bufio.Writer
	currentDate string // 当前活跃文件对应的 UTC 日期 (YYYY-MM-DD)
	closed      bool

	closeCh chan struct{} // 通知 flush goroutine 退出
	doneCh  chan struct{} // flush goroutine 完成
}

// NewNDJSONStore 构造一个 NDJSONStore，并启动后台 flush goroutine。
//
// 行为：
//  1. 创建/打开 cfg.Dir 目录；
//  2. 按当前 UTC 日期 reopen 当日文件（O_APPEND | O_CREATE）；
//  3. 清理超过 RetentionDays 的旧文件；
//  4. 启动 ticker，每 FlushInterval flush 一次 + 检查日切。
func NewNDJSONStore(cfg Config) (*NDJSONStore, error) {
	return newNDJSONStoreWithClock(cfg, realClock{})
}

// newNDJSONStoreWithClock 内部构造，允许测试注入 mock clock。
func newNDJSONStoreWithClock(cfg Config, c clock) (*NDJSONStore, error) {
	if cfg.Dir == "" {
		cfg.Dir = DefaultDir
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	if c == nil {
		c = realClock{}
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("usage: 创建目录 %s 失败: %w", cfg.Dir, err)
	}

	s := &NDJSONStore{
		cfg:     cfg,
		clock:   c,
		closeCh: make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	if err := s.reopenLocked(c.Now()); err != nil {
		return nil, err
	}
	if cfg.RetentionDays > 0 {
		s.cleanupExpired(c.Now())
	}
	go s.flushLoop()
	return s, nil
}

// Append 写一条用量记录。
func (s *NDJSONStore) Append(ctx context.Context, rec UsageRecord) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("usage: marshal record: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	// 写入前检查日期变化（兜底；常规由 flushLoop 触发）。
	now := s.clock.Now().UTC()
	if dateString(now) != s.currentDate {
		if err := s.reopenLocked(now); err != nil {
			return err
		}
	}

	if _, err := s.writer.Write(data); err != nil {
		return fmt.Errorf("usage: write: %w", err)
	}
	if err := s.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("usage: write newline: %w", err)
	}
	return nil
}

// Close 优雅关闭：停 ticker、flush 剩余数据、关闭 fd。
func (s *NDJSONStore) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	close(s.closeCh)
	<-s.doneCh

	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	if s.writer != nil {
		if err := s.writer.Flush(); err != nil {
			firstErr = err
		}
		s.writer = nil
	}
	if s.file != nil {
		if err := s.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.file = nil
	}
	return firstErr
}

// flushLoop 后台 goroutine：周期性 flush 缓冲区，并检查是否需要日切。
func (s *NDJSONStore) flushLoop() {
	defer close(s.doneCh)
	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.closeCh:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// tick flush 当前 buffer，并在日期变化时 reopen 文件。
func (s *NDJSONStore) tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.writer != nil {
		if err := s.writer.Flush(); err != nil {
			log.Printf("[Usage-NDJSON] flush 失败: %v", err)
		}
	}
	now := s.clock.Now().UTC()
	if dateString(now) != s.currentDate {
		if err := s.reopenLocked(now); err != nil {
			log.Printf("[Usage-NDJSON] 日切 reopen 失败: %v", err)
		}
	}
}

// reopenLocked 关闭旧 fd（如有）并按给定时间打开当天文件。调用者必须持有 s.mu。
func (s *NDJSONStore) reopenLocked(now time.Time) error {
	if s.writer != nil {
		if err := s.writer.Flush(); err != nil {
			log.Printf("[Usage-NDJSON] reopen 前 flush 失败: %v", err)
		}
	}
	if s.file != nil {
		if err := s.file.Close(); err != nil {
			log.Printf("[Usage-NDJSON] reopen 前 close 失败: %v", err)
		}
		s.file = nil
		s.writer = nil
	}

	utc := now.UTC()
	name := utc.Format(fileNameLayout)
	path := filepath.Join(s.cfg.Dir, name)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("usage: open %s: %w", path, err)
	}
	s.file = f
	s.writer = bufio.NewWriter(f)
	s.currentDate = dateString(utc)
	log.Printf("[Usage-NDJSON] 打开文件: %s", path)
	return nil
}

// cleanupExpired 删除目录内超过保留期的文件。失败仅记录日志，不返回错误（启动期避免崩）。
func (s *NDJSONStore) cleanupExpired(now time.Time) {
	cutoff := now.UTC().AddDate(0, 0, -s.cfg.RetentionDays)
	cutoffDate := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, time.UTC)

	entries, err := os.ReadDir(s.cfg.Dir)
	if err != nil {
		log.Printf("[Usage-NDJSON] 清理读取目录失败: %v", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		match := fileNameRegexp.FindStringSubmatch(e.Name())
		if len(match) != 2 {
			continue
		}
		fdate, err := time.ParseInLocation("2006-01-02", match[1], time.UTC)
		if err != nil {
			continue
		}
		if fdate.Before(cutoffDate) {
			path := filepath.Join(s.cfg.Dir, e.Name())
			if err := os.Remove(path); err != nil {
				log.Printf("[Usage-NDJSON] 删除超期文件 %s 失败: %v", path, err)
				continue
			}
			log.Printf("[Usage-NDJSON] 已删除超期文件: %s", path)
		}
	}
}

// dateString 将 UTC 时间格式化为 YYYY-MM-DD 用于比较当前活跃文件归属日期。
func dateString(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}
