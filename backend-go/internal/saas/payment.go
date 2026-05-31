package saas

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ============================================================
// Stripe 支付集成 — 使用 Stripe REST API（无需 SDK）
// 支持两种模式:
//   1. production: 真实 Stripe 支付（需要 STRIPE_SECRET_KEY）
//   2. mock: 模拟支付，用于开发和演示
// ============================================================

// StripeClient Stripe 支付客户端
type StripeClient struct {
	SecretKey      string
	WebhookSecret  string
	PricePro       string // Stripe Price ID for Pro plan
	PriceTeam      string // Stripe Price ID for Team plan
	SuccessURL     string
	CancelURL      string
	IsConfigured   bool
	Mode           string // "production" 或 "mock"
}

// NewStripeClient 创建 Stripe 客户端
// 如果环境变量未配置，自动进入 mock 模式
func NewStripeClient() *StripeClient {
	secretKey := os.Getenv("STRIPE_SECRET_KEY")
	webhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	pricePro := os.Getenv("STRIPE_PRICE_PRO")
	priceTeam := os.Getenv("STRIPE_PRICE_TEAM")
	baseURL := os.Getenv("PUBLIC_URL")
	if baseURL == "" {
		baseURL = "http://localhost:3688"
	}

	client := &StripeClient{
		SecretKey:     secretKey,
		WebhookSecret: webhookSecret,
		PricePro:      pricePro,
		PriceTeam:     priceTeam,
		SuccessURL:    baseURL + "/profile?checkout=success",
		CancelURL:     baseURL + "/pricing?checkout=canceled",
	}

	if secretKey != "" && webhookSecret != "" && pricePro != "" && priceTeam != "" {
		client.IsConfigured = true
		client.Mode = "production"
		log.Println("[Stripe] 支付模式已配置为生产模式")
	} else {
		client.IsConfigured = false
		client.Mode = "mock"
		log.Println("[Stripe] 支付模式为 MOCK（开发模式），设置 STRIPE_SECRET_KEY/STRIPE_WEBHOOK_SECRET/STRIPE_PRICE_* 以启用真实支付")
	}

	return client
}

// ============================================================
// CheckoutSession 创建支付会话
// ============================================================

// CreateCheckoutSessionRequest 创建支付会话请求
type CreateCheckoutSessionRequest struct {
	Plan   string `json:"plan" binding:"required"`
	UserID string `json:"-"` // 由中间件填充，不来自客户端
}

// CreateCheckoutSessionResponse 创建支付会话响应
type CreateCheckoutSessionResponse struct {
	URL    string `json:"url"`    // 跳转 URL（Stripe Checkout 或 Mock 页面）
	ID     string `json:"id"`     // 会话 ID
	Mode   string `json:"mode"`   // "production" | "mock"
}

// CreateCheckoutSession 创建 Stripe Checkout Session
func (c *StripeClient) CreateCheckoutSession(userID, plan string) (*CreateCheckoutSessionResponse, error) {
	if plan != "pro" && plan != "team" {
		return nil, fmt.Errorf("无效的套餐: %s", plan)
	}

	if c.Mode == "mock" {
		return c.createMockSession(userID, plan)
	}

	return c.createStripeSession(userID, plan)
}

// createStripeSession 创建真实 Stripe Checkout Session
func (c *StripeClient) createStripeSession(userID, plan string) (*CreateCheckoutSessionResponse, error) {
	priceID := c.PricePro
	if plan == "team" {
		priceID = c.PriceTeam
	}

	// 构建 Stripe API 请求
	body := map[string]interface{}{
		"mode": "subscription",
		"line_items": []map[string]interface{}{
			{
				"price":    priceID,
				"quantity": 1,
			},
		},
		"client_reference_id": userID,
		"metadata": map[string]string{
			"user_id": userID,
			"plan":    plan,
		},
		"success_url": c.SuccessURL + "&session_id={CHECKOUT_SESSION_ID}",
		"cancel_url":  c.CancelURL,
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", "https://api.stripe.com/v1/checkout/sessions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("创建 Stripe 请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.SecretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 Stripe API 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Stripe API 返回错误 (%d): %s", resp.StatusCode, string(respBody))
	}

	var stripeResp struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(respBody, &stripeResp); err != nil {
		return nil, fmt.Errorf("解析 Stripe 响应失败: %w", err)
	}

	return &CreateCheckoutSessionResponse{
		URL:  stripeResp.URL,
		ID:   stripeResp.ID,
		Mode: "production",
	}, nil
}

// createMockSession 创建模拟支付会话
func (c *StripeClient) createMockSession(userID, plan string) (*CreateCheckoutSessionResponse, error) {
	// 生成一个 mock session ID
	mockID := fmt.Sprintf("cs_mock_%s_%s_%d", userID[:8], plan, time.Now().Unix())

	// 计算价格显示
	limits := GetPlanLimits(Plan(plan))
	priceStr := fmt.Sprintf("$%.2f", float64(limits.PriceMonthly)/100)

	log.Printf("[Stripe-Mock] 创建支付会话: user=%s plan=%s price=%s session=%s",
		userID[:8], plan, priceStr, mockID)

	// Mock 模式：返回模拟确认页面
	mockURL := fmt.Sprintf("%s/api/saas/mock-checkout?session_id=%s&plan=%s&user_id=%s",
		c.SuccessURL[:strings.Index(c.SuccessURL, "/profile")], mockID, plan, userID)

	return &CreateCheckoutSessionResponse{
		URL:  mockURL,
		ID:   mockID,
		Mode: "mock",
	}, nil
}

// ============================================================
// Webhook 处理（Stripe 事件）
// ============================================================

// StripeEvent Stripe Webhook 事件
type StripeEvent struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Data   struct {
		Object struct {
			ID               string `json:"id"`
			ClientReferenceID string `json:"client_reference_id"`
			Metadata         map[string]string `json:"metadata"`
			Customer         string `json:"customer"`
			Subscription     string `json:"subscription"`
			Status           string `json:"status"`
			PaymentStatus    string `json:"payment_status"`
		} `json:"object"`
	} `json:"data"`
}

// HandleWebhook 处理 Stripe Webhook
func (c *StripeClient) HandleWebhook(store *Store, payload []byte, sigHeader string) error {
	if c.Mode == "production" {
		// 验证签名
		if err := verifyStripeSignature(payload, sigHeader, c.WebhookSecret); err != nil {
			return fmt.Errorf("签名验证失败: %w", err)
		}
	}

	var event StripeEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("解析事件失败: %w", err)
	}

	log.Printf("[Stripe-Webhook] 收到事件: type=%s id=%s", event.Type, event.ID)

	switch event.Type {
	case "checkout.session.completed":
		return handleCheckoutCompleted(store, &event)
	case "invoice.paid":
		return handleInvoicePaid(store, &event)
	case "customer.subscription.deleted", "customer.subscription.updated":
		return handleSubscriptionChanged(store, &event)
	default:
		log.Printf("[Stripe-Webhook] 忽略事件: %s", event.Type)
	}

	return nil
}

// handleCheckoutCompleted 处理支付完成
func handleCheckoutCompleted(store *Store, event *StripeEvent) error {
	userID := event.Data.Object.ClientReferenceID
	if userID == "" {
		// 从 metadata 获取
		if meta, ok := event.Data.Object.Metadata["user_id"]; ok {
			userID = meta
		}
	}
	if userID == "" {
		return fmt.Errorf("无法获取用户 ID")
	}

	plan := event.Data.Object.Metadata["plan"]
	if plan == "" {
		plan = "pro" // 默认 Pro
	}

	log.Printf("[Stripe-Webhook] 支付完成: user=%s plan=%s", userID[:8], plan)

	// 升级用户套餐并创建发票
	return store.UpdateUserPlanWithInvoice(userID, plan)
}

// handleInvoicePaid 处理发票支付成功（续费）
func handleInvoicePaid(store *Store, event *StripeEvent) error {
	userID := event.Data.Object.ClientReferenceID
	if userID == "" {
		if meta, ok := event.Data.Object.Metadata["user_id"]; ok {
			userID = meta
		}
	}
	if userID == "" {
		return nil // 静默忽略
	}

	log.Printf("[Stripe-Webhook] 续费成功: user=%s", userID[:8])
	return nil
}

// handleSubscriptionChanged 处理订阅变更/取消
func handleSubscriptionChanged(store *Store, event *StripeEvent) error {
	userID := event.Data.Object.ClientReferenceID
	if userID == "" {
		if meta, ok := event.Data.Object.Metadata["user_id"]; ok {
			userID = meta
		}
	}
	if userID == "" {
		return nil
	}

	if event.Type == "customer.subscription.deleted" {
		log.Printf("[Stripe-Webhook] 订阅已取消: user=%s", userID[:8])
		// 降级到 Free 套餐
		return store.UpdateUserPlan(userID, "free")
	}

	return nil
}

// verifyStripeSignature 验证 Stripe Webhook 签名
func verifyStripeSignature(payload []byte, sigHeader, secret string) error {
	// 解析 Stripe-Signature header
	// 格式: t=timestamp,v1=signature
	var timestamp string
	var signature string

	parts := strings.Split(sigHeader, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "t=") {
			timestamp = part[2:]
		} else if strings.HasPrefix(part, "v1=") {
			signature = part[3:]
		}
	}

	if timestamp == "" || signature == "" {
		return fmt.Errorf("无效的签名 header")
	}

	// 计算期望签名
	signedPayload := fmt.Sprintf("%s.%s", timestamp, string(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	// 比较签名（恒定时间比较防止时序攻击）
	if !hmac.Equal([]byte(expectedSig), []byte(signature)) {
		return fmt.Errorf("签名不匹配")
	}

	return nil
}

// ============================================================
// Mock Checkout UI
// ============================================================

// MockCheckoutHandler 模拟支付确认页面
func MockCheckoutHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("session_id")
		plan := r.URL.Query().Get("plan")
		userID := r.URL.Query().Get("user_id")

		if sessionID == "" || plan == "" || userID == "" {
			http.Error(w, "参数缺失", http.StatusBadRequest)
			return
		}

		// 如果是 GET 请求，显示确认页面
		if r.Method == "GET" {
			limits := GetPlanLimits(Plan(plan))
			priceStr := fmt.Sprintf("$%.2f", float64(limits.PriceMonthly)/100)

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="zh-CN">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>模拟支付 - CCX SaaS</title>
	<style>
		body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; display: flex; justify-content: center; align-items: center; min-height: 100vh; margin: 0; background: #f5f5f5; }
		.card { background: white; border-radius: 16px; padding: 40px; max-width: 420px; width: 90%%; box-shadow: 0 2px 20px rgba(0,0,0,0.1); text-align: center; }
		h2 { margin-top: 0; color: #1a1a2e; }
		.price { font-size: 36px; font-weight: bold; color: #6C63FF; margin: 20px 0; }
		.plan-name { font-size: 18px; color: #666; text-transform: uppercase; letter-spacing: 2px; }
		.desc { color: #888; margin: 20px 0; line-height: 1.6; }
		.btn { display: inline-block; padding: 14px 40px; border-radius: 8px; font-size: 16px; font-weight: 600; text-decoration: none; cursor: pointer; border: none; transition: all 0.2s; margin: 8px; }
		.btn-primary { background: #6C63FF; color: white; }
		.btn-primary:hover { background: #5A52D5; }
		.btn-secondary { background: #eee; color: #666; }
		.btn-secondary:hover { background: #ddd; }
		.badge { display: inline-block; background: #FFF3E0; color: #E65100; padding: 4px 12px; border-radius: 20px; font-size: 12px; margin-bottom: 16px; }
		.features { text-align: left; margin: 24px 0; }
		.features li { padding: 6px 0; color: #555; list-style: none; }
		.features li:before { content: "✅ "; }
	</style>
</head>
<body>
	<div class="card">
		<div class="badge">🔒 MOCK 模式 — 仅用于开发和演示</div>
		<div class="plan-name">` + strings.ToUpper(plan) + `</div>
		<div class="price">` + priceStr + `<span style="font-size:16px;color:#888;">/月</span></div>
		<ul class="features">
			<li>每月最多 %d 次 API 调用</li>
			<li>最多 %d 个 API Key</li>
			<li>最多 %d 个渠道</li>
		</ul>
		<form method="POST">
			<input type="hidden" name="session_id" value="` + sessionID + `">
			<input type="hidden" name="plan" value="` + plan + `">
			<input type="hidden" name="user_id" value="` + userID + `">
			<button type="submit" class="btn btn-primary">确认支付 (Mock) — $0.00</button>
			<br>
			<a href="/pricing" class="btn btn-secondary">取消</a>
		</form>
	</div>
</body>
</html>`, limits.MaxRequests, limits.MaxAPIKeys, limits.MaxChannels)
			return
		}

		// POST 请求：确认支付（mock）
		if r.Method == "POST" {
			log.Printf("[Stripe-Mock] 支付确认: user=%s plan=%s session=%s", userID[:8], plan, sessionID)

			// 升级用户套餐
			if err := store.UpdateUserPlanWithInvoice(userID, plan); err != nil {
				http.Error(w, "升级失败", http.StatusInternalServerError)
				return
			}

			// 重定向到个人中心
			http.Redirect(w, r, "/profile?checkout=success&mock=true", http.StatusSeeOther)
		}
	}
}

// ============================================================
// 全局 Stripe 客户端
// ============================================================

var globalStripeClient *StripeClient

// InitStripeClient 初始化全局 Stripe 客户端
// 在 main.go 中 SaaS 初始化后调用
func InitStripeClient() {
	globalStripeClient = NewStripeClient()
	log.Printf("[Stripe] 支付客户端已初始化 (mode=%s)", globalStripeClient.Mode)
}

// GetStripeClient 获取全局 Stripe 客户端
func GetStripeClient() *StripeClient {
	return globalStripeClient
}

// ============================================================
// Gin HTTP Handler
// ============================================================

// StripeWebhookHandler 处理 Stripe Webhook 的 Gin Handler
func StripeWebhookHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if globalStripeClient == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "支付系统未初始化"})
			return
		}

		payload, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败"})
			return
		}

		sigHeader := c.GetHeader("Stripe-Signature")

		if err := globalStripeClient.HandleWebhook(store, payload, sigHeader); err != nil {
			log.Printf("[Stripe-Webhook] 处理失败: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"received": true})
	}
}

// CreateCheckoutSessionHandler 创建支付会话的 Gin Handler
func CreateCheckoutSessionHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未认证"})
			return
		}

		var req CreateCheckoutSessionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
			return
		}

		req.UserID = userID

		if globalStripeClient == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "支付系统未初始化"})
			return
		}

		session, err := globalStripeClient.CreateCheckoutSession(userID, req.Plan)
		if err != nil {
			log.Printf("[Stripe] 创建支付会话失败: user=%s plan=%s err=%v", userID[:8], req.Plan, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建支付会话失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, session)
	}
}
