package healthcheck

import (
	"context"
	"log"
	"math"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
)

// supportsL2 仅 messages/chat/responses/gemini 四协议支持 L2 真实调用验活；
// images/vectors 直接跳过（不写记录）
func supportsL2(channelType string) bool {
	switch channelType {
	case "messages", "chat", "responses", "gemini":
		return true
	}
	return false
}

// checkKeyL2 单 key L2 真实调用验活，结果处置与 L1 同构：
// 流正常 → ok（detail 记录所用模型名）；401/403 → auth_failed（拉黑回调）；
// 其他 → error（喂熔断）。落 check_kind='l2' 记录，consecutive_failures 基于该 (key,l2) 上次记录。
func (m *Manager) checkKeyL2(
	channelType string, channelIndex int, channelID string,
	u *config.UpstreamConfig, apiKey string, keyBaseURLs []string, l1Models []string,
	policy config.ResolvedHealthCheckPolicy,
	prev map[string]metrics.KeyHealthRecord,
) {
	keyMask := utils.MaskAPIKey(apiKey)

	// 选模型：显式 VerifyModel 优先，其次从 L1 模型列表自动选最便宜
	model := strings.TrimSpace(policy.VerifyModel)
	if model == "" {
		model = m.selectCheapestModel(u, l1Models)
		if model == "" {
			// 全部模型都无定价信息且未指定验活模型：跳过不写记录，避免污染状态
			log.Printf("[HealthCheck] L2 跳过: 渠道=%s, key=%s, 无定价信息且未指定验活模型",
				channelKey(channelType, channelID), keyMask)
			return
		}
	}

	// 按 key 裁剪渠道副本（能力测试请求构建取 APIKeys[0] 作为认证 key）。
	// 同时覆盖 BaseURL/BaseURLs 为该 Key 绑定的端点，避免把其他凭证配置或跨套餐地址带入探针。
	probeChannel := *u
	probeChannel.APIKeys = []string{apiKey}
	probeChannel.DisabledAPIKeys = nil
	if len(keyBaseURLs) > 0 {
		probeChannel.BaseURL = keyBaseURLs[0]
		probeChannel.BaseURLs = append([]string(nil), keyBaseURLs...)
	}

	start := time.Now()
	rec := metrics.KeyHealthRecord{
		ChannelType: channelType,
		ChannelID:   channelID,
		KeyMask:     keyMask,
		CheckKind:   CheckKindL2,
		LastCheckAt: start,
	}

	req, err := handlers.BuildHealthCheckL2Request(channelType, &probeChannel, model)
	if err != nil {
		// 请求构建失败属于本地配置问题而非上游健康度，只打日志不写记录
		log.Printf("[HealthCheck] L2 跳过: 渠道=%s, key=%s, 构建验活请求失败: %v",
			channelKey(channelType, channelID), keyMask, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), policy.Timeout)
	success, _, statusCode, respBody, sendErr := handlers.SendHealthCheckL2Stream(ctx, &probeChannel, req.WithContext(ctx), channelType)
	cancel()

	rec.LatencyMs = time.Since(start).Milliseconds()
	prevFailures := prev[keyMask].ConsecutiveFailures

	switch {
	case success:
		rec.LastStatus = StatusOK
		rec.ConsecutiveFailures = 0
		rec.Detail = "model=" + model
	case statusCode == 401 || statusCode == 403:
		rec.LastStatus = StatusAuthFailed
		rec.ConsecutiveFailures = prevFailures + 1
		rec.Detail = summarizeDetail(statusCode, respBody, nil)
		// 鉴权失败拉黑：与 L1 同一 ShouldBlacklistKey 语义
		if m.blacklist != nil {
			if bl := common.ShouldBlacklistKey(statusCode, respBody); bl.ShouldBlacklist {
				m.blacklist(channelType, channelIndex, apiKey, bl.Reason, bl.Message, bl.RecoverAt)
			}
		}
	default:
		rec.LastStatus = StatusError
		rec.ConsecutiveFailures = prevFailures + 1
		rec.Detail = summarizeDetail(statusCode, respBody, sendErr)
		// 失败喂熔断：归因到该 Key 实际绑定的 BaseURL（历史未绑定 Key 回退到渠道首个地址，保持原口径）
		if m.recordFailure != nil && len(keyBaseURLs) > 0 {
			m.recordFailure(channelType, channelIndex, keyBaseURLs[0], apiKey, u.ServiceType, model, rec.Detail)
		}
	}

	if err := m.store.UpsertKeyHealth(rec); err != nil {
		log.Printf("[HealthCheck] 写入 key 健康记录失败 (%s): %v", channelKey(channelType, channelID), err)
	}
	log.Printf("[HealthCheck] L2 验证完成: 渠道=%s, key=%s, 模型=%s, 结果=%s, 延迟=%dms",
		channelKey(channelType, channelID), keyMask, model, rec.LastStatus, rec.LatencyMs)
}

// selectCheapestModel 从 L1 模型列表中按模型注册表定价选 input+output 单价最低者。
// 遵循渠道 SupportedModels 约束；能力解析内部已处理 ModelMapping 重定向。
// 全部候选都无定价信息时返回空。
func (m *Manager) selectCheapestModel(u *config.UpstreamConfig, models []string) string {
	cfg := m.getConfig()
	global := cfg.UpstreamModelCapabilities

	best := ""
	bestCost := math.MaxFloat64
	for _, model := range models {
		if !u.SupportsModel(model) {
			continue
		}
		resolved := config.ResolveUpstreamCapability(model, u, global)
		cost, ok := pricingCost(resolved.Capability.Pricing)
		if !ok {
			continue
		}
		if cost < bestCost {
			bestCost = cost
			best = model
		}
	}
	return best
}

// pricingCost 估算模型单次调用相对成本（input 未命中缓存价 + output 价）。
// 两个单价都缺失时视为无定价信息。
func pricingCost(p *config.ModelPricing) (float64, bool) {
	if p == nil {
		return 0, false
	}
	var in, out float64
	has := false
	if p.InputCacheMissPrice != nil {
		in = *p.InputCacheMissPrice
		has = true
	}
	if p.OutputPrice != nil {
		out = *p.OutputPrice
		has = true
	}
	return in + out, has
}
