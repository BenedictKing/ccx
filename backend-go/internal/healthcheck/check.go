package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
)

const maxDetailLen = 200

// channelKey 渠道标识（去重/记录分组用）
func channelKey(channelType, channelID string) string {
	return channelType + "/" + channelID
}

// UpstreamsFor 按渠道类型取配置中的 upstream slice（导出供 main.go 接线复用）
func UpstreamsFor(cfg *config.Config, channelType string) []config.UpstreamConfig {
	switch channelType {
	case "messages":
		return cfg.Upstream
	case "responses":
		return cfg.ResponsesUpstream
	case "gemini":
		return cfg.GeminiUpstream
	case "chat":
		return cfg.ChatUpstream
	case "images":
		return cfg.ImagesUpstream
	case "vectors":
		return cfg.VectorsUpstream
	}
	return nil
}

// channelStatus 渠道有效状态（空值视为 active，与调度器口径一致）
func channelStatus(u *config.UpstreamConfig) string {
	if u.Status == "" {
		return "active"
	}
	return u.Status
}

// eligibleKeys 过滤可参与保活验证的 key：跳过 DisabledAPIKeys 禁用期内与 APIKeyConfigs Enabled=false 的 key
func eligibleKeys(u *config.UpstreamConfig, now time.Time) []string {
	disabledByConfig := make(map[string]bool, len(u.APIKeyConfigs))
	for _, kc := range u.APIKeyConfigs {
		if kc.Enabled != nil && !*kc.Enabled {
			disabledByConfig[kc.Key] = true
		}
	}
	out := make([]string, 0, len(u.APIKeys))
	for _, key := range u.APIKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if u.IsKeyDisabledNow(key, now) {
			continue
		}
		if disabledByConfig[key] {
			continue
		}
		out = append(out, key)
	}
	return out
}

// groupL1Records 按渠道分组 L1 验证记录
func groupL1Records(records []metrics.KeyHealthRecord) map[string][]metrics.KeyHealthRecord {
	grouped := make(map[string][]metrics.KeyHealthRecord)
	for _, r := range records {
		if r.CheckKind != CheckKindL1 {
			continue
		}
		key := channelKey(r.ChannelType, r.ChannelID)
		grouped[key] = append(grouped[key], r)
	}
	return grouped
}

// channelDue 判定渠道是否到期：
//  1. 从未验证过（无 L1 记录）→ 立即到期
//  2. 存在无记录的 eligible key（新增 key）→ 到期
//  3. 最近一次验证时间 + 加 jitter 的间隔已到 → 到期
func channelDue(channelType, channelID string, keys []string, records []metrics.KeyHealthRecord, interval time.Duration, now time.Time) bool {
	if len(records) == 0 {
		return true
	}
	checked := make(map[string]bool, len(records))
	var maxLast time.Time
	for _, r := range records {
		checked[r.KeyMask] = true
		if r.LastCheckAt.After(maxLast) {
			maxLast = r.LastCheckAt
		}
	}
	for _, key := range keys {
		if !checked[utils.MaskAPIKey(key)] {
			return true
		}
	}
	return !now.Before(maxLast.Add(jitteredInterval(channelType, channelID, interval)))
}

// jitteredInterval 对 (channelType, channelID) 做 hash 得到 ±10% 内的确定偏移，
// 避免整点齐发与每次扫描漂移
func jitteredInterval(channelType, channelID string, interval time.Duration) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(channelKey(channelType, channelID)))
	frac := float64(h.Sum32()%1000) / 1000.0 // [0, 1)
	factor := 0.9 + 0.2*frac                 // [0.9, 1.1)
	return time.Duration(float64(interval) * factor)
}

// l1KeyOutcome L1 单 key 验证结果（供 L2 分派使用）
type l1KeyOutcome struct {
	ok               bool     // L1 是否成功（只有成功的 key 才做 L2）
	models           []string // L1 成功时拉到的模型 ID 列表（L2 自动选模型用）
	realCallVerified bool     // L1 已发起过真实推理调用（如火山套餐探针），同周期应跳过等价 L2
}

// checkKeyL1 单 key L1 流程：对每个 BaseURL 尝试拉 models，任一成功即 ok；
// 401/403 → auth_failed（按 ShouldBlacklistKey 语义判断是否拉黑）；
// 其他错误/超时/5xx → error（喂熔断）。每次结果都 UpsertKeyHealth。
func (m *Manager) checkKeyL1(
	channelType string, channelIndex int, channelID string,
	u *config.UpstreamConfig, baseURLs []string, apiKey string,
	policy config.ResolvedHealthCheckPolicy,
	prev map[string]metrics.KeyHealthRecord,
	fetcher L1Fetcher,
) l1KeyOutcome {
	var outcome l1KeyOutcome
	keyMask := utils.MaskAPIKey(apiKey)
	start := time.Now()
	rec := metrics.KeyHealthRecord{
		ChannelType: channelType,
		ChannelID:   channelID,
		KeyMask:     keyMask,
		CheckKind:   CheckKindL1,
		LastCheckAt: start,
	}

	var lastStatus int
	var lastBody []byte
	var lastErr error
	var lastBaseURL string
	var lastModel string
	succeeded := false
	authFailed := false

	for _, baseURL := range baseURLs {
		lastBaseURL = baseURL
		ctx, cancel := context.WithTimeout(context.Background(), policy.Timeout)
		resp, err := fetcher(ctx, L1Request{
			BaseURL:            baseURL,
			APIKey:             apiKey,
			ServiceType:        u.ServiceType,
			AuthHeader:         u.AuthHeader,
			CustomHeaders:      u.CustomHeaders,
			ProxyURL:           u.ProxyURL,
			InsecureSkipVerify: u.InsecureSkipVerify,
		})
		cancel()
		if resp.RealCallVerified {
			outcome.realCallVerified = true
		}
		lastModel = resp.Model
		if err != nil {
			lastErr = err
			continue
		}
		statusCode, body := normalizeWrappedResponse(resp.StatusCode, resp.Body)
		lastStatus = statusCode
		lastBody = body
		lastErr = nil
		if statusCode == http.StatusOK {
			succeeded = true
			rec.ModelCount = countModels(body)
			outcome.models = extractModelIDs(body)
			break
		}
		// 401/403 认证失败不继续尝试其他 BaseURL（与各渠道 GetChannelModels 口径一致）
		if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
			authFailed = true
			break
		}
	}

	rec.LatencyMs = time.Since(start).Milliseconds()
	prevFailures := prev[keyMask].ConsecutiveFailures

	switch {
	case succeeded:
		rec.LastStatus = StatusOK
		rec.ConsecutiveFailures = 0
		outcome.ok = true
	case authFailed:
		rec.LastStatus = StatusAuthFailed
		rec.ConsecutiveFailures = prevFailures + 1
		rec.Detail = summarizeDetail(lastStatus, lastBody, nil)
		// 鉴权失败拉黑：内部用 ShouldBlacklistKey 语义判断，拉黑交注入的回调
		if m.blacklist != nil {
			if bl := common.ShouldBlacklistKey(lastStatus, lastBody); bl.ShouldBlacklist {
				m.blacklist(channelType, channelIndex, apiKey, bl.Reason, bl.Message, bl.RecoverAt)
			}
		}
	default:
		rec.LastStatus = StatusError
		rec.ConsecutiveFailures = prevFailures + 1
		rec.Detail = summarizeDetail(lastStatus, lastBody, lastErr)
		// 失败喂熔断
		if m.recordFailure != nil && lastBaseURL != "" {
			m.recordFailure(channelType, channelIndex, lastBaseURL, apiKey, u.ServiceType, lastModel, rec.Detail)
		}
	}

	if err := m.store.UpsertKeyHealth(rec); err != nil {
		log.Printf("[HealthCheck] 写入 key 健康记录失败 (%s): %v", channelKey(channelType, channelID), err)
	}
	log.Printf("[HealthCheck] L1 验证完成: 渠道=%s, key=%s, 结果=%s, 模型数=%d, 延迟=%dms",
		channelKey(channelType, channelID), keyMask, rec.LastStatus, rec.ModelCount, rec.LatencyMs)
	return outcome
}

// extractModelIDs 从模型列表 JSON 提取模型 ID（OpenAI "data"[].id 或 Gemini "models"[].name）
func extractModelIDs(body []byte) []string {
	var probe struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil
	}
	var out []string
	for _, m := range probe.Data {
		if strings.TrimSpace(m.ID) != "" {
			out = append(out, m.ID)
		}
	}
	for _, m := range probe.Models {
		name := m.Name
		// Gemini name 格式为 "models/gemini-..."，取 "/" 后的部分作为 id
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		if strings.TrimSpace(name) != "" {
			out = append(out, name)
		}
	}
	return out
}

// normalizeWrappedResponse 将各渠道 GetChannelModels 包装 handler 的响应归一化为 (上游状态码, 上游响应体)。
// 包装约定：200=成功（模型列表 JSON）；400+statusCode=上游 401 包装；502/504=网络/超时；其他=上游状态透传。
func normalizeWrappedResponse(code int, body []byte) (int, []byte) {
	switch code {
	case http.StatusOK:
		return http.StatusOK, body
	case http.StatusBadRequest:
		var wrapped struct {
			StatusCode int    `json:"statusCode"`
			Details    string `json:"details"`
		}
		if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.StatusCode > 0 {
			return wrapped.StatusCode, []byte(wrapped.Details)
		}
		return code, body
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return 0, body
	default:
		return code, body
	}
}

// countModels 统计模型列表 JSON 中的模型数（OpenAI "data" 数组或 Gemini "models" 数组）
func countModels(body []byte) int {
	var probe struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return 0
	}
	if len(probe.Data) > 0 {
		return len(probe.Data)
	}
	return len(probe.Models)
}

// summarizeDetail 失败原因摘要（截断，避免响应体撑爆记录）
func summarizeDetail(statusCode int, body []byte, err error) string {
	var s string
	switch {
	case err != nil:
		s = err.Error()
	case len(strings.TrimSpace(string(body))) > 0:
		s = strings.TrimSpace(string(body))
	case statusCode > 0:
		s = fmt.Sprintf("HTTP %d", statusCode)
	default:
		s = "网络错误"
	}
	if len(s) > maxDetailLen {
		s = s[:maxDetailLen] + "..."
	}
	return s
}
