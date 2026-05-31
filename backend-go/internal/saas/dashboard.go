package saas

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ---------- 仪表盘统计 ----------

// DashboardStats 管理员仪表盘统计数据
type DashboardStats struct {
	TotalUsers      int64            `json:"totalUsers"`
	ActiveUsers     int64            `json:"activeUsers"`    // 本月有 API 调用的用户
	PaidUsers       int64            `json:"paidUsers"`      // 付费用户 (pro + team)
	TotalAPIKeys    int64            `json:"totalApiKeys"`
	MonthlyAPIUsage int64            `json:"monthlyApiUsage"` // 本月总 API 调用量
	MonthlyTokens   int64            `json:"monthlyTokens"`   // 本月总 Token 用量
	PlanBreakdown   []PlanBreakdown  `json:"planBreakdown"`
	Revenue         *RevenueStats    `json:"revenue"`
}

// PlanBreakdown 套餐分布
type PlanBreakdown struct {
	Plan  string `json:"plan"`
	Count int64  `json:"count"`
}

// RevenueStats 收入统计
type RevenueStats struct {
	CurrentMonth int64  `json:"currentMonth"` // 本月收入（分）
	TotalRevenue int64  `json:"totalRevenue"` // 总收入（分）
	LastUpdate   string `json:"lastUpdate"`
}

// UsageTrend 用量趋势数据点
type UsageTrend struct {
	Date     string `json:"date"`     // YYYY-MM-DD
	Calls    int64  `json:"calls"`
	Tokens   int64  `json:"tokens"`
}

// AdminDashboardHandler 管理员仪表盘 API
func AdminDashboardHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		stats, err := getDashboardStats(store)
		if err != nil {
			log.Printf("[SaaS-Dashboard] 获取统计数据失败: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "获取统计数据失败"})
			return
		}

		c.JSON(http.StatusOK, stats)
	}
}

// AdminUsageTrendHandler 用量趋势 API（近30天）
func AdminUsageTrendHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := store.db.Query(
			`SELECT date, SUM(api_calls) as total_calls, SUM(tokens_in + tokens_out) as total_tokens
			 FROM usage_records
			 WHERE date >= date('now', '-30 days')
			 GROUP BY date
			 ORDER BY date ASC`,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用量趋势失败"})
			return
		}
		defer rows.Close()

		var trends []UsageTrend
		for rows.Next() {
			var t UsageTrend
			if err := rows.Scan(&t.Date, &t.Calls, &t.Tokens); err != nil {
				continue
			}
			trends = append(trends, t)
		}

		if trends == nil {
			trends = []UsageTrend{}
		}

		c.JSON(http.StatusOK, gin.H{"trends": trends})
	}
}

// AdminTopUsersHandler 用量最高的 TOP 用户
func AdminTopUsersHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := store.db.Query(
			`SELECT u.id, u.email, u.name, u.plan,
			        COALESCE(SUM(r.api_calls), 0) as calls,
			        COALESCE(SUM(r.tokens_in + r.tokens_out), 0) as tokens
			 FROM users u
			 LEFT JOIN usage_records r ON r.user_id = u.id
			  AND r.date LIKE strftime('%Y-%m', 'now') || '%'
			 GROUP BY u.id
			 ORDER BY calls DESC
			 LIMIT 20`,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询 Top 用户失败"})
			return
		}
		defer rows.Close()

		type TopUser struct {
			ID     string `json:"id"`
			Email  string `json:"email"`
			Name   string `json:"name"`
			Plan   string `json:"plan"`
			Calls  int64  `json:"calls"`
			Tokens int64  `json:"tokens"`
		}

		var users []TopUser
		for rows.Next() {
			var u TopUser
			if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Plan, &u.Calls, &u.Tokens); err != nil {
				continue
			}
			users = append(users, u)
		}

		if users == nil {
			users = []TopUser{}
		}

		c.JSON(http.StatusOK, gin.H{"users": users})
	}
}

func getDashboardStats(store *Store) (*DashboardStats, error) {
	stats := &DashboardStats{}

	// 1. 总用户数
	_ = store.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&stats.TotalUsers)

	// 2. 付费用户数
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM users WHERE plan IN ('pro', 'team')`).Scan(&stats.PaidUsers)

	// 3. 本月活跃用户（有用量记录）
	yearMonth := time.Now().Format("2006-01")
	_ = store.db.QueryRow(
		`SELECT COUNT(DISTINCT user_id) FROM usage_records WHERE date LIKE ? || '%'`,
		yearMonth,
	).Scan(&stats.ActiveUsers)

	// 4. Total API Keys (non-null, non-empty api_key)
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM users WHERE api_key != ''`).Scan(&stats.TotalAPIKeys)

	// 5. 本月总 API 调用量
	_ = store.db.QueryRow(
		`SELECT COALESCE(SUM(api_calls), 0) FROM usage_records WHERE date LIKE ? || '%'`,
		yearMonth,
	).Scan(&stats.MonthlyAPIUsage)

	// 6. 本月总 Token 用量
	_ = store.db.QueryRow(
		`SELECT COALESCE(SUM(tokens_in + tokens_out), 0) FROM usage_records WHERE date LIKE ? || '%'`,
		yearMonth,
	).Scan(&stats.MonthlyTokens)

	// 7. 套餐分布
	planRows, err := store.db.Query(
		`SELECT plan, COUNT(*) as cnt FROM users GROUP BY plan ORDER BY cnt DESC`,
	)
	if err == nil {
		defer planRows.Close()
		for planRows.Next() {
			var pb PlanBreakdown
			if err := planRows.Scan(&pb.Plan, &pb.Count); err != nil {
				continue
			}
			stats.PlanBreakdown = append(stats.PlanBreakdown, pb)
		}
	}
	if stats.PlanBreakdown == nil {
		stats.PlanBreakdown = []PlanBreakdown{}
	}

	// 8. 收入统计
	stats.Revenue = getRevenueStats(store)

	return stats, nil
}

func getRevenueStats(store *Store) *RevenueStats {
	rs := &RevenueStats{}

	// 订阅收入（基于付费用户数 × 套餐价格）
	rows, err := store.db.Query(`SELECT plan, COUNT(*) as cnt FROM users WHERE plan IN ('pro', 'team') GROUP BY plan`)
	if err != nil {
		return rs
	}
	defer rows.Close()

	for rows.Next() {
		var plan string
		var count int64
		if err := rows.Scan(&plan, &count); err != nil {
			continue
		}
		limits := GetPlanLimits(Plan(plan))
		rs.CurrentMonth += limits.PriceMonthly * count
	}

	rs.TotalRevenue = rs.CurrentMonth // 简化：按当前订阅估算
	rs.LastUpdate = time.Now().Format(time.RFC3339)

	return rs
}
