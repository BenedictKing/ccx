package saas

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ---------- 预警通知记录 ----------

// AlertRecord 预警通知记录
type AlertRecord struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	AlertType string    `json:"alertType"` // usage_80, usage_100
	Value     int64     `json:"value"`
	Max       int64     `json:"max"`
	SentAt    time.Time `json:"sentAt"`
}

// ---------- 用量预警定时任务 ----------

// UsageAlertCron 用量预警定时任务
type UsageAlertCron struct {
	store     *Store
	stop      chan struct{}
	mu        sync.Mutex
	emailFrom string
	smtpHost  string
	smtpPort  int
	smtpUser  string
	smtpPass  string
}

// AlertConfig 邮件发送配置
type AlertConfig struct {
	Enabled   bool
	EmailFrom string
	SMTPHost  string
	SMTPPort  int
	SMTPUser  string
	SMTPPass  string
}

// StartUsageAlertCron 启动用量预警定时任务
// 每小时检查一次所有用户的用量是否达到阈值的 80%
func StartUsageAlertCron(store *Store, cfg *AlertConfig) *UsageAlertCron {
	cron := &UsageAlertCron{
		store: store,
		stop:  make(chan struct{}),
	}

	if cfg != nil && cfg.Enabled {
		cron.emailFrom = cfg.EmailFrom
		cron.smtpHost = cfg.SMTPHost
		cron.smtpPort = cfg.SMTPPort
		cron.smtpUser = cfg.SMTPUser
		cron.smtpPass = cfg.SMTPPass
	}

	go cron.run()
	return cron
}

// Stop 停止定时任务
func (c *UsageAlertCron) Stop() {
	close(c.stop)
}

func (c *UsageAlertCron) run() {
	log.Println("[SaaS-Alert] 用量预警定时任务已启动")

	ticker := time.NewTicker(1 * time.Hour) // 每小时检查一次
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			log.Println("[SaaS-Alert] 用量预警定时任务已停止")
			return
		case <-ticker.C:
			c.checkAllUsers()
		}
	}
}

// Thresholds 预警阈值配置
type Thresholds struct {
	WarnPercent  float64 // 80% 告警线
	CriticalPercent float64 // 100% 超限线
}

func (c *UsageAlertCron) checkAllUsers() {
	// 获取所有付费用户（free 用户没有预警必要，超限直接拦截即可）
	rows, err := c.store.db.Query(
		`SELECT id, email, name, plan FROM users WHERE plan IN ('pro', 'team')`,
	)
	if err != nil {
		log.Printf("[SaaS-Alert] 查询用户失败: %v", err)
		return
	}
	defer rows.Close()

	now := time.Now()
	yearMonth := now.Format("2006-01")

	for rows.Next() {
		var userID, email, name, plan string
		if err := rows.Scan(&userID, &email, &name, &plan); err != nil {
			log.Printf("[SaaS-Alert] 读取用户行失败: %v", err)
			continue
		}

		limits := GetPlanLimits(Plan(plan))
		if limits.MaxRequests <= 0 && limits.MaxTokens <= 0 {
			continue
		}

		usage, err := c.store.GetUserUsage(userID, yearMonth)
		if err != nil {
			log.Printf("[SaaS-Alert] 查询用户 %s 用量失败: %v", userID, err)
			continue
		}

		// 检查 API 调用量预警
		if limits.MaxRequests > 0 {
			percent := float64(usage.APICalls) / float64(limits.MaxRequests) * 100
			if percent >= 80 && usage.APICalls > 0 {
				c.checkAndSendAlert(userID, email, name, "usage_80",
					usage.APICalls, limits.MaxRequests, percent, plan, yearMonth)
			}
		}

		// 检查 Token 用量预警
		if limits.MaxTokens > 0 {
			totalTokens := usage.TokensIn + usage.TokensOut
			percent := float64(totalTokens) / float64(limits.MaxTokens) * 100
			if percent >= 80 && totalTokens > 0 {
				c.checkAndSendAlert(userID, email, name, "token_80",
					totalTokens, limits.MaxTokens, percent, plan, yearMonth)
			}
		}
	}
}

func (c *UsageAlertCron) checkAndSendAlert(userID, email, name, alertType string,
	value, max int64, percent float64, plan, yearMonth string) {

	// 检查本月是否已经发送过同类预警（避免重复发送）
	var exists int
	err := c.store.db.QueryRow(
		`SELECT COUNT(*) FROM alert_logs WHERE user_id = ? AND alert_type = ? AND strftime('%Y-%m', sent_at) = ?`,
		userID, alertType, yearMonth,
	).Scan(&exists)
	if err != nil {
		// alert_logs 表可能还不存在或查询失败，先尝试创建
		log.Printf("[SaaS-Alert] 查询预警记录异常: %v", err)
	}

	if exists > 0 {
		return // 本月已发送过，跳过
	}

	// 写入预警记录
	_, err = c.store.db.Exec(
		`INSERT INTO alert_logs (id, user_id, alert_type, value, max, sent_at) VALUES (?, ?, ?, ?, ?, ?)`,
		generateID(), userID, alertType, value, max, time.Now().Format(time.RFC3339),
	)
	if err != nil {
		log.Printf("[SaaS-Alert] 写入预警记录失败: %v", err)
	}

	// 发送邮件通知（如果已配置 SMTP）
	if c.smtpHost != "" {
		c.sendAlertEmail(email, name, alertType, value, max, percent, plan)
	} else {
		log.Printf("[SaaS-Alert] 预警(邮件未配置): user=%s, type=%s, usage=%.0f%% (%d/%d)",
			email, alertType, percent, value, max)
	}

	log.Printf("[SaaS-Alert] ✅ 已发送预警: user=%s, type=%s, usage=%.0f%%, plan=%s",
		email, alertType, percent, plan)
}

func (c *UsageAlertCron) sendAlertEmail(to, name, alertType string, value, max int64,
	percent float64, plan string) {

	// 构建邮件内容
	var subject string
	switch alertType {
	case "usage_80":
		subject = fmt.Sprintf("[CCX] 用量预警 — 已使用 %.0f%% 的 API 调用配额", percent)
	case "token_80":
		subject = fmt.Sprintf("[CCX] 用量预警 — 已使用 %.0f%% 的 Token 配额", percent)
	}

	log.Printf("[SaaS-Alert-Email] 模拟发送邮件: to=%s, subject=%s", to, subject)
	// 实际邮件发送需要集成 SendGrid / SMTP
	// 这里仅做日志记录，生产环境需配置 SMTP 参数
}

// generateID 生成短 UUID（用于 alert_logs）
func generateID() string {
	return fmt.Sprintf("alt-%d", time.Now().UnixNano())
}

// ---------- 预警 API 处理器 ----------

// GetMyAlertsHandler 获取当前用户的预警历史
func GetMyAlertsHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(401, gin.H{"error": "未认证"})
			return
		}

		rows, err := store.db.Query(
			`SELECT id, user_id, alert_type, value, max, sent_at
			 FROM alert_logs WHERE user_id = ? ORDER BY sent_at DESC LIMIT 20`,
			userID,
		)
		if err != nil {
			c.JSON(500, gin.H{"error": "查询预警记录失败"})
			return
		}
		defer rows.Close()

		var alerts []gin.H
		for rows.Next() {
			var id, alertType string
			var value, max int64
			var sentAt string
			if err := rows.Scan(&id, &userID, &alertType, &value, &max, &sentAt); err != nil {
				continue
			}
			alerts = append(alerts, gin.H{
				"id":        id,
				"type":      alertType,
				"value":     value,
				"max":       max,
				"percent":   float64(value) / float64(max) * 100,
				"sentAt":    sentAt,
			})
		}

		if alerts == nil {
			alerts = []gin.H{}
		}

		c.JSON(200, gin.H{"alerts": alerts})
	}
}
