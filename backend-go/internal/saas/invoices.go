package saas

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------- 发票/账单模型 ----------

// Invoice 发票/账单记录
type Invoice struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userId"`
	Plan        string    `json:"plan"`
	Amount      int64     `json:"amount"`      // 金额（分）
	Currency    string    `json:"currency"`    // 货币代码
	Status      string    `json:"status"`      // paid, pending, canceled
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
	PaidAt      time.Time `json:"paidAt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ---------- 发票生成 ----------

// CreateInvoice 创建发票/账单记录（在支付成功后调用）
func (s *Store) CreateInvoice(userID, plan string, amount int64, currency string) error {
	now := time.Now()
	periodStart := now
	periodEnd := periodStart.AddDate(0, 1, 0)

	_, err := s.db.Exec(
		`INSERT INTO invoices (id, user_id, plan, amount, currency, status, period_start, period_end, paid_at, created_at)
		 VALUES (?, ?, ?, ?, ?, 'paid', ?, ?, ?, ?)`,
		"inv-"+uuid.New().String(),
		userID, plan, amount, currency,
		periodStart.Format(time.RFC3339),
		periodEnd.Format(time.RFC3339),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		log.Printf("[SaaS-Invoice] 创建发票失败: user=%s, err=%v", userID, err)
		return err
	}

	log.Printf("[SaaS-Invoice] ✅ 发票已创建: user=%s, plan=%s, amount=%d%s",
		userID, plan, amount, currency)
	return nil
}

// GetUserInvoices 获取用户的发票/账单列表
func (s *Store) GetUserInvoices(userID string, limit, offset int) ([]*Invoice, int, error) {
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM invoices WHERE user_id = ?", userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(
		`SELECT id, user_id, plan, amount, currency, status, period_start, period_end, paid_at, created_at
		 FROM invoices WHERE user_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var invoices []*Invoice
	for rows.Next() {
		inv := &Invoice{}
		var periodStart, periodEnd, paidAt, createdAt string
		if err := rows.Scan(&inv.ID, &inv.UserID, &inv.Plan, &inv.Amount, &inv.Currency,
			&inv.Status, &periodStart, &periodEnd, &paidAt, &createdAt); err != nil {
			continue
		}
		inv.PeriodStart, _ = time.Parse(time.RFC3339, periodStart)
		inv.PeriodEnd, _ = time.Parse(time.RFC3339, periodEnd)
		inv.PaidAt, _ = time.Parse(time.RFC3339, paidAt)
		inv.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		invoices = append(invoices, inv)
	}

	if invoices == nil {
		invoices = []*Invoice{}
	}

	return invoices, total, nil
}

// ---------- 自动生成发票（在 UpdateUserPlan 时调用） ----------

// updateUserPlanWithInvoice 更新套餐并自动生成发票
func (s *Store) UpdateUserPlanWithInvoice(userID, plan string) error {
	limits := GetPlanLimits(Plan(plan))
	if limits.PriceMonthly > 0 {
		if err := s.CreateInvoice(userID, plan, limits.PriceMonthly, "cny"); err != nil {
			log.Printf("[SaaS-Invoice] 创建发票失败（不影响套餐升级）: %v", err)
		}
	}
	return s.UpdateUserPlan(userID, plan)
}

// ---------- API 处理器 ----------

// GetMyInvoicesHandler 获取当前用户的账单历史
func GetMyInvoicesHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未认证"})
			return
		}

		limit, offset := parsePagination(c.DefaultQuery("limit", "20"), c.DefaultQuery("offset", "0"))

		invoices, total, err := store.GetUserInvoices(userID, limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询账单失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"invoices": invoices,
			"total":    total,
		})
	}
}

// GetInvoiceDetailHandler 获取单个发票详情
func GetInvoiceDetailHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		invoiceID := c.Param("id")

		inv := &Invoice{}
		var periodStart, periodEnd, paidAt, createdAt string
		err := store.db.QueryRow(
			`SELECT id, user_id, plan, amount, currency, status, period_start, period_end, paid_at, created_at
			 FROM invoices WHERE id = ? AND user_id = ?`,
			invoiceID, userID,
		).Scan(&inv.ID, &inv.UserID, &inv.Plan, &inv.Amount, &inv.Currency,
			&inv.Status, &periodStart, &periodEnd, &paidAt, &createdAt)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "发票不存在"})
			return
		}

		inv.PeriodStart, _ = time.Parse(time.RFC3339, periodStart)
		inv.PeriodEnd, _ = time.Parse(time.RFC3339, periodEnd)
		inv.PaidAt, _ = time.Parse(time.RFC3339, paidAt)
		inv.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)

		c.JSON(http.StatusOK, inv)
	}
}

// 辅助函数：解析分页参数
func parsePagination(limitStr, offsetStr string) (int, int) {
	limit := 20
	offset := 0
	if l, err := parseInt(limitStr); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	if o, err := parseInt(offsetStr); err == nil && o >= 0 {
		offset = o
	}
	return limit, offset
}

func parseInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// 注意：此处 fmt 需要 import
// 由于 Go 不允许循环依赖，我们把 parseInt 放在这里
// 但 alert.go 已经引入了 fmt，这里也需要
// 实际上我们需要在文件顶部添加 "fmt"
