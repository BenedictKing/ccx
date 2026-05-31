package saas

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ---------- 配额检查 ----------

// QuotaError 配额超限错误
type QuotaError struct {
	LimitName string
	Current   int64
	Max       int64
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("%s 超限: %d / %d", e.LimitName, e.Current, e.Max)
}

// CheckQuota 检查用户是否超出套餐限制
// 返回 nil 表示在限额内，返回 *QuotaError 表示超限
func CheckQuota(store *Store, userID string, plan Plan) error {
	limits := GetPlanLimits(plan)
	if limits.MaxRequests <= 0 && limits.MaxTokens <= 0 {
		return nil // 无限制
	}

	now := time.Now()
	yearMonth := now.Format("2006-01")

	usage, err := store.GetUserUsage(userID, yearMonth)
	if err != nil {
		return fmt.Errorf("查询用量失败: %w", err)
	}

	if limits.MaxRequests > 0 && usage.APICalls >= limits.MaxRequests {
		return NewQuotaError("API 请求数", usage.APICalls, limits.MaxRequests)
	}
	if limits.MaxTokens > 0 && usage.TokensIn+usage.TokensOut >= limits.MaxTokens {
		return NewQuotaError("Token 用量", usage.TokensIn+usage.TokensOut, limits.MaxTokens)
	}

	return nil
}

// NewQuotaError 创建配额超限错误
func NewQuotaError(limitName string, current, max int64) *QuotaError {
	return &QuotaError{LimitName: limitName, Current: current, Max: max}
}

// ---------- 响应记录器 ----------

// saasResponseWriter 包装 gin.ResponseWriter，捕获状态码和写入字节数
type saasResponseWriter struct {
	gin.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (w *saasResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *saasResponseWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.bytesWritten += int64(n)
	return n, err
}

// ---------- 用量记录 ----------

// recordUsage 在请求完成后记录用量
func recordUsage(store *Store, userID string, requestBody []byte, respStatusCode int, bytesWritten int64) {
	// 只记录成功请求 (2xx)
	if respStatusCode < 200 || respStatusCode >= 300 {
		return
	}

	// 估算输入 token
	tokensIn := estimateInputTokens(requestBody)
	// 估算输出 token（粗略：按字符数 / 4）
	tokensOut := estimateOutputTokens(bytesWritten)

	if tokensIn < 1 {
		tokensIn = 1
	}

	if err := store.RecordUsage(userID, tokensIn, tokensOut); err != nil {
		log.Printf("[SaaS-Usage] 记录用量失败: user=%s, err=%v", userID, err)
		return
	}

	if tokensOut > 0 {
		log.Printf("[SaaS-Usage] 已记录用量: user=%s, apiCalls=+1, tokensIn=+%d, tokensOut=+%d",
			userID, tokensIn, tokensOut)
	} else {
		log.Printf("[SaaS-Usage] 已记录用量: user=%s, apiCalls=+1, tokensIn=+%d",
			userID, tokensIn)
	}
}

// estimateInputTokens 估算请求体的 token 数
func estimateInputTokens(body []byte) int64 {
	if len(body) == 0 {
		return 1
	}
	return int64(len(body)/4) + 1
}

// estimateOutputTokens 估算输出 token 数
func estimateOutputTokens(bytesWritten int64) int64 {
	if bytesWritten <= 0 {
		return 0
	}
	return bytesWritten / 4
}

// getAPIKeyFromRequest 从请求中提取 API Key（与 middleware/auth.go 中的逻辑一致）
func getAPIKeyFromRequest(c *gin.Context) string {
	if key := c.GetHeader("x-api-key"); key != "" {
		return key
	}
	if auth := c.GetHeader("Authorization"); auth != "" {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if key := c.GetHeader("x-goog-api-key"); key != "" {
		return key
	}
	return ""
}

// ---------- SaaS 配额中间件 ----------

// SaaSQuotaMiddleware 为 SaaS 用户执行配额检查 + 自动用量记录
//
// 该中间件在 gin 中间件链中运行（先于 handler），因此需要：
// 1. 自行验证 API Key + 获取用户（不依赖 handler 内部的 ProxyAuthMiddleware）
// 2. 检查配额并在超限时拦截
// 3. 包装响应 writer，在请求完成后自动记录用量
//
// 执行流程：
//   gin:  SaaSQuotaMiddleware → ... → Handler
//                                     Handler 内部: ProxyAuthMiddleware → 代理请求
//   SaaSQuotaMiddleware:
//     ├─ 提取 API Key
//     ├─ 查找用户
//     ├─ 检查配额（超限 → 429）
//     ├─ 设置 saas_user_id → 供后续使用
//     ├─ 包装 c.Writer
//     ├─ c.Next() → ... → Handler → 代理请求完成
//     └─ 记录用量
func SaaSQuotaMiddleware(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 非 SaaS 模式直接放行
		if store == nil {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		// 只处理代理端点
		if !strings.HasPrefix(path, "/v1/") && !strings.HasPrefix(path, "/v1beta/") {
			c.Next()
			return
		}

		// 1. 提取 API Key
		apiKey := getAPIKeyFromRequest(c)
		if apiKey == "" {
			// 没有提供 API Key，让后续的 ProxyAuthMiddleware 处理错误
			c.Next()
			return
		}

		// 2. 查找用户
		user, err := store.GetUserByAPIKey(apiKey)
		if err != nil || user == nil {
			// 无效的 Key，让后续的 ProxyAuthMiddleware 处理错误
			c.Next()
			return
		}

		// 3. 设置用户上下文信息（ProxyAuthMiddleware 也会设置，值相同）
		c.Set("saas_user_id", user.ID)
		c.Set("saas_user_plan", string(user.Plan))

		// 4. 检查配额
		if err := CheckQuota(store, user.ID, user.Plan); err != nil {
			if qe, ok := err.(*QuotaError); ok {
				log.Printf("[SaaS-Quota] 配额超限: user=%s, plan=%s, %s",
					user.ID, user.Plan, err.Error())
				c.JSON(429, gin.H{
					"error":   "quota_exceeded",
					"message": fmt.Sprintf("本月配额已用尽: %s", qe.Error()),
					"plan":    user.Plan,
					"usage": gin.H{
						"apiCalls": qe.Current,
						"maxCalls": qe.Max,
					},
					"upgrade_url": "/#/pricing",
				})
				c.Abort()
				return
			}
			// 其他错误放行
			log.Printf("[SaaS-Quota] 配额检查异常: user=%s, err=%v", user.ID, err)
			c.Next()
			return
		}

		// 5. 包装响应 writer 以捕获响应数据
		wrapped := &saasResponseWriter{
			ResponseWriter: c.Writer,
			statusCode:     http.StatusOK,
		}
		c.Writer = wrapped

		// 6. 执行后续中间件 + handler
		c.Next()

		// 7. 请求完成后记录用量
		recordUsage(store, user.ID, readRequestBodyFromContext(c),
			wrapped.statusCode, wrapped.bytesWritten)
	}
}

// readRequestBodyFromContext 尝试从 gin 上下文读取请求体字节
// 部分 handler 在请求处理中已缓存请求体到 context (requestBodyBytes)
func readRequestBodyFromContext(c *gin.Context) []byte {
	if body, exists := c.Get("requestBodyBytes"); exists {
		if b, ok := body.([]byte); ok {
			return b
		}
	}
	// 如果 handler 还未缓存，尝试从请求中消费（仅一次，之后可能不可用）
	return nil
}


// readRequestBodyFromContext 尝试从 gin 上下文读取请求体字节
