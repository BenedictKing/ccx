package saas

import (
	"log"
	"sync"
	"time"
)

// StartUsageResetCron 启动每月用量重置定时任务
// 每天检查是否进入新月份，如果是则重置上个月的用量记录
func StartUsageResetCron(store *Store) *UsageResetCron {
	cron := &UsageResetCron{
		store: store,
		stop:  make(chan struct{}),
	}

	go cron.run()
	return cron
}

// UsageResetCron 用量重置定时器
type UsageResetCron struct {
	store *Store
	stop  chan struct{}
	mu    sync.Mutex
	// 记录上一个月份，用于检测月份变化
	lastMonth string
}

// Stop 停止定时任务
func (c *UsageResetCron) Stop() {
	close(c.stop)
}

func (c *UsageResetCron) run() {
	// 初始检查：记录当前月份
	c.mu.Lock()
	c.lastMonth = time.Now().Format("2006-01")
	c.mu.Unlock()

	log.Printf("[SaaS-Cron] 用量重置定时任务已启动，当前月份: %s", c.lastMonth)

	ticker := time.NewTicker(1 * time.Hour) // 每小时检查一次
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			log.Println("[SaaS-Cron] 用量重置定时任务已停止")
			return
		case <-ticker.C:
			c.checkAndReset()
		}
	}
}

func (c *UsageResetCron) checkAndReset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	currentMonth := now.Format("2006-01")

	if currentMonth == c.lastMonth {
		return // 月份未变化
	}

	log.Printf("[SaaS-Cron] 检测到月份变更: %s → %s，正在重置用量...", c.lastMonth, currentMonth)

	// 重置所有用户的当月用量记录
	if err := c.store.ResetMonthlyUsage(c.lastMonth); err != nil {
		log.Printf("[SaaS-Cron] 用量重置失败: %v", err)
		return
	}

	c.lastMonth = currentMonth
	log.Printf("[SaaS-Cron] 月份 %s 的用量已重置完成", currentMonth)
}
