package autopilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/BenedictKing/ccx/internal/errutil"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ─── new-api 订阅集成适配器（§8.5.1）──────────────────────────────────────────
//
// new-api（及其 fork，如 Veloera/voapi/Super-API 等）是中转站常用的开源面板。
// 用户在「个人设置」生成系统访问令牌（access_token）后，CCX 用该令牌自动完成：
// 校验令牌 + 查余额 → 拉分组倍率 → 建代理 key → 建上游渠道并触发 Discovery。
//
// 归类：OriginType=relay、OriginTier=second（中转站），信任等级不因自动化提升。
// AccessToken 是敏感凭据，绝不出现在任何 API 响应或日志中。

// ── 请求/响应信封 ──

// newApiEnvelope 是 new-api 统一响应信封：{success, data, message}。
// data 用 json.RawMessage 延迟解析，便于按端点解出不同结构。
type newApiEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
}

// NewApiUserSelf 对应 GET /api/user/self 的 data 字段。
// fork 可能有额外字段，此处只解析本集成用到的子集，多余字段被忽略。
type NewApiUserSelf struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Quota     int64  `json:"quota"`      // 剩余额度
	UsedQuota int64  `json:"used_quota"` // 已用额度
}

// NewApiGroupInfo 对应 GET /api/user/self/groups 的单个分组信息。
type NewApiGroupInfo struct {
	Desc  string  `json:"desc"`
	Ratio float64 `json:"ratio"`
}

// NewApiToken 对应 GET /api/token/ 列表中的单条记录，及 POST /api/token/ 的响应。
type NewApiToken struct {
	ID     int    `json:"id"`
	Key    string `json:"key"`
	Name   string `json:"name"`
	Status int    `json:"status"`
	Group  string `json:"group"`
}

// NewApiProvisionKeyConflictError 表示同名 Key 已存在但无法安全复用的本地冲突。
type NewApiProvisionKeyConflictError struct {
	err error
}

func (e *NewApiProvisionKeyConflictError) Error() string {
	return e.err.Error()
}

// newApiTokenListData 对应 GET /api/token/?p=&size= 的 data 字段。
// 不同 fork 分页字段可能不同，这里只依赖 items。
type newApiTokenListData struct {
	Items []NewApiToken `json:"items"`
}

// NewApiCreateTokenRequest 对应 POST /api/token/ 的请求体。
type NewApiCreateTokenRequest struct {
	Name               string `json:"name"`
	RemainQuota        int64  `json:"remain_quota"`
	ExpiredTime        int64  `json:"expired_time"`
	UnlimitedQuota     bool   `json:"unlimited_quota"`
	ModelLimitsEnabled bool   `json:"model_limits_enabled"`
	ModelLimits        string `json:"model_limits"`
	AllowIPs           string `json:"allow_ips"`
	Group              string `json:"group"`
}

// DefaultNewApiProvisionKeyName 是 CCX 自动建 key 的默认名称。
const DefaultNewApiProvisionKeyName = "ccx-autopilot"

// NewApiAuthTokenMode 定义令牌注入 Authorization 头的方式。
const (
	NewApiAuthModeBearer  = "bearer"   // 默认：Authorization: Bearer <token>
	NewApiAuthModeRaw     = "raw"      // fork 兼容：Authorization: <token>（不带 Bearer 前缀）
	NewApiAuthModeRawAuth = "raw_auth" // 前端统一值：等价于 raw
)

// NewApiAdapter 是 new-api 家族站点的订阅集成适配器。
// 可注入 HTTPClient 便于测试；零值可用（默认 15s 超时 client）。
type NewApiAdapter struct {
	HTTPClient *http.Client
}

func (a *NewApiAdapter) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// buildAuthHeader 按 authTokenMode 构造 Authorization 头值。
func buildAuthHeader(accessToken, authTokenMode string) string {
	mode := strings.TrimSpace(authTokenMode)
	if strings.EqualFold(mode, NewApiAuthModeRaw) || strings.EqualFold(mode, NewApiAuthModeRawAuth) {
		return accessToken
	}
	return "Bearer " + accessToken
}

// doRequest 是统一的请求辅助函数：注入认证头，发起请求，解析 {success,data,message} 信封。
// success=false 时返回 message 作为 error；HTTP 非 2xx 时返回状态码 + body 摘要。
// result 非 nil 时把 data 字段反序列化进 result（result 须为指针）。
func (a *NewApiAdapter) doRequest(ctx context.Context, method, baseURL, path, accessToken, userID, authTokenMode string, body interface{}, result interface{}) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return fmt.Errorf("[NewApiAdapter] baseURL 不能为空")
	}

	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("[NewApiAdapter] 序列化请求体失败: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("[NewApiAdapter] 构造请求失败: %w", err)
	}

	// 认证头：Authorization + New-API-User（主线）+ User-id（fork 兼容回退）。
	req.Header.Set("Authorization", buildAuthHeader(accessToken, authTokenMode))
	if userID != "" {
		req.Header.Set("New-API-User", userID)
		req.Header.Set("User-id", userID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("[NewApiAdapter] HTTP 请求失败: %w", err)
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 最多读 4MB，防御异常大响应
	if err != nil {
		return fmt.Errorf("[NewApiAdapter] 读取响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("[NewApiAdapter] HTTP %d: %s", resp.StatusCode, truncateForError(respBody))
	}

	var envelope newApiEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		// fork 兼容：响应信封字段名可能不同，解析失败时不暴露原始 body（可能含敏感信息），只报通用错误。
		return fmt.Errorf("[NewApiAdapter] 响应信封解析失败，站点可能非 new-api 兼容格式: %w", err)
	}
	if !envelope.Success {
		msg := envelope.Message
		if msg == "" {
			msg = "未知错误"
		}
		return fmt.Errorf("[NewApiAdapter] 请求失败: %s", msg)
	}

	if result != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, result); err != nil {
			return fmt.Errorf("[NewApiAdapter] data 字段解析失败: %w", err)
		}
	}
	return nil
}

// truncateForError 截断响应体用于错误信息展示，避免日志/错误里出现超长内容。
func truncateForError(body []byte) string {
	const maxLen = 512
	s := strings.TrimSpace(string(body))
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// Verify 校验令牌有效性并查询用户信息/余额。对应 GET /api/user/self。
func (a *NewApiAdapter) Verify(ctx context.Context, baseURL, accessToken, userID, authTokenMode string) (*NewApiUserSelf, error) {
	var self NewApiUserSelf
	if err := a.doRequest(ctx, http.MethodGet, baseURL, "/api/user/self", accessToken, userID, authTokenMode, nil, &self); err != nil {
		return nil, fmt.Errorf("[NewApiAdapter-Verify] %w", err)
	}
	return &self, nil
}

// VerifyWithFallback 两阶段验证：先尝试用提供的 userID（可能为空），
// 若因缺少 New-API-User header 失败，则尝试使用默认 userID="1" 重试。
// 返回 (用户信息, 实际使用的 userID, 错误)。
func (a *NewApiAdapter) VerifyWithFallback(ctx context.Context, baseURL, accessToken, userID, authTokenMode string) (*NewApiUserSelf, string, error) {
	// 第一阶段：使用提供的 userID（可能为空，标准 new-api 支持）
	self, err := a.Verify(ctx, baseURL, accessToken, userID, authTokenMode)
	if err == nil {
		derivedUserID := userID
		if derivedUserID == "" {
			derivedUserID = fmt.Sprintf("%d", self.ID)
		}
		return self, derivedUserID, nil
	}

	// 检查是否是 New-API-User header 缺失错误
	if !isNewApiUserHeaderError(err) {
		return nil, "", err
	}

	// 第二阶段：尝试使用 userID="1"（某些 fork 的默认管理员 ID）
	if userID == "" {
		self, err = a.Verify(ctx, baseURL, accessToken, "1", authTokenMode)
		if err == nil {
			return self, "1", nil
		}
	}

	return nil, "", fmt.Errorf("[NewApiAdapter-VerifyWithFallback] New-API-User header required but no valid userID available: %w", err)
}

// isNewApiUserHeaderError 检查错误是否因缺少 New-API-User header 导致。
func isNewApiUserHeaderError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "new-api-user header not provided") ||
		strings.Contains(errStr, "new-api-user") && strings.Contains(errStr, "unauthorized")
}

// FetchBalance 查询余额（复用 Verify，返回 quota 原值 + 固定 currency="quota"）。
// 满足 SubscriptionBalanceFetcher 风格，但 new-api 需要 userID，因此不直接实现该接口，
// 由订阅 handler 层组合调用。
func (a *NewApiAdapter) FetchBalance(ctx context.Context, baseURL, accessToken, userID, authTokenMode string) (balance float64, currency string, err error) {
	self, err := a.Verify(ctx, baseURL, accessToken, userID, authTokenMode)
	if err != nil {
		return 0, "quota", err
	}
	return float64(self.Quota), "quota", nil
}

// FetchGroups 拉取分组倍率。对应 GET /api/user/self/groups。
// 返回 {分组名: 倍率} 供 SubscriptionProfile.GroupMultipliers 使用；desc 信息不保留（倍率是唯一强需求）。
func (a *NewApiAdapter) FetchGroups(ctx context.Context, baseURL, accessToken, userID, authTokenMode string) (map[string]float64, error) {
	var raw map[string]NewApiGroupInfo
	if err := a.doRequest(ctx, http.MethodGet, baseURL, "/api/user/self/groups", accessToken, userID, authTokenMode, nil, &raw); err != nil {
		return nil, fmt.Errorf("[NewApiAdapter-FetchGroups] %w", err)
	}
	result := make(map[string]float64, len(raw))
	for name, info := range raw {
		result[name] = info.Ratio
	}
	return result, nil
}

// FetchModels 拉取账号可用模型列表。对应 GET /api/user/models。
func (a *NewApiAdapter) FetchModels(ctx context.Context, baseURL, accessToken, userID, authTokenMode string) ([]string, error) {
	var models []string
	if err := a.doRequest(ctx, http.MethodGet, baseURL, "/api/user/models", accessToken, userID, authTokenMode, nil, &models); err != nil {
		return nil, fmt.Errorf("[NewApiAdapter-FetchModels] %w", err)
	}
	return models, nil
}

// ListTokens 拉取 key 列表（用于建 key 前查重）。对应 GET /api/token/?p=&size=。
func (a *NewApiAdapter) ListTokens(ctx context.Context, baseURL, accessToken, userID, authTokenMode string, page, size int) ([]NewApiToken, error) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 100
	}
	path := "/api/token/?p=" + strconv.Itoa(page) + "&size=" + strconv.Itoa(size)

	// 部分 fork 的 data 直接是数组而非 {items:[...]}；先按 {items} 解析，失败则回退按数组解析。
	var listData newApiTokenListData
	err := a.doRequest(ctx, http.MethodGet, baseURL, path, accessToken, userID, authTokenMode, nil, &listData)
	if err == nil && listData.Items != nil {
		return listData.Items, nil
	}
	if err != nil {
		var tokens []NewApiToken
		if fallbackErr := a.doRequest(ctx, http.MethodGet, baseURL, path, accessToken, userID, authTokenMode, nil, &tokens); fallbackErr == nil {
			return tokens, nil
		}
		return nil, fmt.Errorf("[NewApiAdapter-ListTokens] %w", err)
	}
	return listData.Items, nil
}

// FindTokenByName 在 key 列表中查找同名令牌（大小写敏感，new-api 名称精确匹配）。
// new-api 的列表通常分页，必须遍历到末页，避免重复 provision 时遗漏第 2 页以后的同名 Key。
// 未找到返回 nil, nil。
func (a *NewApiAdapter) FindTokenByName(ctx context.Context, baseURL, accessToken, userID, authTokenMode, name string) (*NewApiToken, error) {
	const (
		pageSize = 100
		maxPages = 1000
	)
	seenTokens := make(map[string]struct{}, pageSize)
	for page := 1; page <= maxPages; page++ {
		tokens, err := a.ListTokens(ctx, baseURL, accessToken, userID, authTokenMode, page, pageSize)
		if err != nil {
			return nil, err
		}
		if len(tokens) == 0 {
			return nil, nil
		}

		newTokenCount := 0
		for i := range tokens {
			if tokens[i].Name == name {
				return &tokens[i], nil
			}
			identity := strconv.Itoa(tokens[i].ID) + "\x00" + tokens[i].Name
			if _, exists := seenTokens[identity]; !exists {
				seenTokens[identity] = struct{}{}
				newTokenCount++
			}
		}
		if len(tokens) < pageSize || newTokenCount == 0 {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("[NewApiAdapter-FindTokenByName] token 列表超过 %d 页，已拒绝继续创建以避免重复 key", maxPages)
}

// NewApiProvisionOptions 建代理 key 的模板参数。
type NewApiProvisionOptions struct {
	Name   string   // 空则用 DefaultNewApiProvisionKeyName
	Group  string   // 空=默认分组
	Models []string // model_limits 白名单，空=不限制
}

// normalizeNewApiPlaintextKey 把 new-api 返回的令牌明文规范为可直接调用的 API Key。
// new-api 的创建/列表接口返回的 key 不带 "sk-" 前缀，但其推理端点（/v1/*）按 "sk-<key>" 鉴权，
// 缺前缀会被上游以 401 Invalid token 拒绝（进而触发 key 黑名单、渠道挂起）。已有前缀时保持不变。
func normalizeNewApiPlaintextKey(key string) string {
	if key == "" || strings.HasPrefix(key, "sk-") {
		return key
	}
	return "sk-" + key
}

// ProvisionKey 建代理专用 key：先查重（同名则复用），不存在则新建。
// 返回 (tokenID, keyPlainText, reused, error)；keyPlainText 已规范为带 "sk-" 前缀的可调用形式。
// new-api 建 key 成功后部分 fork 直接在响应里带明文 key，部分需要再查列表；
// 此处优先取创建响应的 data.key，为空则回退查列表按 name 取。
func (a *NewApiAdapter) ProvisionKey(ctx context.Context, baseURL, accessToken, userID, authTokenMode string, opts NewApiProvisionOptions) (tokenID int, keyPlainText string, reused bool, err error) {
	tokenID, keyPlainText, reused, err = a.provisionKey(ctx, baseURL, accessToken, userID, authTokenMode, opts)
	return tokenID, normalizeNewApiPlaintextKey(keyPlainText), reused, err
}

// provisionKey 是 ProvisionKey 的具体实现，返回 new-api 原始形式的 key（可能不带 "sk-" 前缀）。
func (a *NewApiAdapter) provisionKey(ctx context.Context, baseURL, accessToken, userID, authTokenMode string, opts NewApiProvisionOptions) (tokenID int, keyPlainText string, reused bool, err error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = DefaultNewApiProvisionKeyName
	}

	// 1. 查重：已有同名 key 则直接复用（不重复创建，也无法取回旧 key 明文——new-api 不支持明文回显）。
	existing, err := a.FindTokenByName(ctx, baseURL, accessToken, userID, authTokenMode, name)
	if err != nil {
		return 0, "", false, fmt.Errorf("[NewApiAdapter-ProvisionKey] 查重失败: %w", err)
	}
	if existing != nil {
		if expectedGroup := strings.TrimSpace(opts.Group); expectedGroup != "" && existing.Group != expectedGroup {
			return existing.ID, "", true, &NewApiProvisionKeyConflictError{err: fmt.Errorf("[NewApiAdapter-ProvisionKey] 同名 key=%s 的分组为 %q，无法确认其属于目标分组 %q", name, existing.Group, expectedGroup)}
		}
		return existing.ID, existing.Key, true, nil
	}

	// 2. 新建。
	modelLimits := ""
	modelLimitsEnabled := false
	if len(opts.Models) > 0 {
		modelLimits = strings.Join(opts.Models, ",")
		modelLimitsEnabled = true
	}
	createReq := NewApiCreateTokenRequest{
		Name:               name,
		RemainQuota:        0,
		ExpiredTime:        -1,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: modelLimitsEnabled,
		ModelLimits:        modelLimits,
		AllowIPs:           "",
		Group:              opts.Group,
	}

	var created NewApiToken
	createErr := a.doRequest(ctx, http.MethodPost, baseURL, "/api/token/", accessToken, userID, authTokenMode, createReq, &created)
	if createErr != nil {
		return 0, "", false, fmt.Errorf("[NewApiAdapter-ProvisionKey] 创建失败: %w", createErr)
	}

	if expectedGroup := strings.TrimSpace(opts.Group); expectedGroup != "" {
		if created.Group == expectedGroup && created.Key != "" {
			return created.ID, created.Key, false, nil
		}
		fresh, findErr := a.FindTokenByName(ctx, baseURL, accessToken, userID, authTokenMode, name)
		if findErr != nil || fresh == nil || fresh.Group != expectedGroup {
			return created.ID, "", false, fmt.Errorf("[NewApiAdapter-ProvisionKey] 创建后无法确认 key=%s 属于目标分组 %q", name, expectedGroup)
		}
		if fresh.Key != "" {
			return fresh.ID, fresh.Key, false, nil
		}
		if created.Key != "" {
			return fresh.ID, created.Key, false, nil
		}
		return fresh.ID, "", false, fmt.Errorf("[NewApiAdapter-ProvisionKey] 创建成功但无法取回 key 明文，请到站点后台手动核对 name=%s", name)
	}

	if created.Key != "" {
		return created.ID, created.Key, false, nil
	}

	// 3. 容错：部分上游创建响应不带明文 key，回查列表按 name 取。
	fresh, findErr := a.FindTokenByName(ctx, baseURL, accessToken, userID, authTokenMode, name)
	if findErr != nil || fresh == nil {
		return created.ID, "", false, fmt.Errorf("[NewApiAdapter-ProvisionKey] 创建成功但无法取回 key 明文，请到站点后台手动核对 name=%s", name)
	}
	return fresh.ID, fresh.Key, false, nil
}

// DeleteToken 删除本次 provision 新建但尚未绑定到渠道的远端 Key，用于失败补偿。
func (a *NewApiAdapter) DeleteToken(ctx context.Context, baseURL, accessToken, userID, authTokenMode string, tokenID int) error {
	if tokenID <= 0 {
		return fmt.Errorf("[NewApiAdapter-DeleteToken] tokenID 必须大于 0")
	}
	if err := a.doRequest(ctx, http.MethodDelete, baseURL, "/api/token/"+strconv.Itoa(tokenID), accessToken, userID, authTokenMode, nil, nil); err != nil {
		return fmt.Errorf("[NewApiAdapter-DeleteToken] 删除 tokenID=%d 失败: %w", tokenID, err)
	}
	return nil
}
