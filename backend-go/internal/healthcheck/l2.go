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
//
// 能力台账去重（CapabilityProbeLedger）：自动选模型场景下，L2 真实调用是"站点+分组+协议"
// 级事实，同 CapabilityUID 本扫描周期由首个 key 探测成功后，其余 key（含跨账号）复用结论
// 写 ok 记录；失败结论不复用（无法区分 key 级/站点级失败，后续 key 各自再探）。
// 凭证 auth 已由 L1 按 key 各自校验（L2 仅对 L1 成功的 key 执行），不因去重而漏验。
// 显式 VerifyModel 时各渠道指定模型可能不同，不参与去重。
func (m *Manager) checkKeyL2(
	channelType string, channelIndex int, channelID string,
	u *config.UpstreamConfig, apiKey string, keyBaseURLs []string, l1Models []string,
	policy config.ResolvedHealthCheckPolicy,
	prev map[string]metrics.KeyHealthRecord,
) {
	keyMask := utils.MaskAPIKey(apiKey)

	// 选模型：显式 VerifyModel 优先，其次从 L1 模型列表自动选最便宜
	verifyModel := strings.TrimSpace(policy.VerifyModel)
	model := verifyModel
	if model == "" {
		model = m.selectCheapestModel(u, l1Models)
		if model == "" {
			// 全部模型都无定价信息且未指定验活模型：跳过不写记录，避免污染状态
			log.Printf("[HealthCheck] L2 跳过: 渠道=%s, key=%s, 无定价信息且未指定验活模型",
				channelKey(channelType, channelID), keyMask)
			return
		}
	}

	capUID := ""
	if verifyModel == "" {
		capUID = capabilityUIDForL2(channelType, u, apiKey, keyBaseURLs)
	}
	if capUID != "" && m.capabilityProbeLedger != nil && !m.capabilityProbeLedger.ClaimProbe(capUID) {
		m.l2ReuseMu.Lock()
		reusedModel, ok := m.l2Reuse[capUID]
		m.l2ReuseMu.Unlock()
		if ok {
			m.writeReusedL2Record(channelType, channelID, apiKey, reusedModel, capUID)
			return
		}
	}

	prevFailures := int64(0)
	if r, ok := prev[keyMask]; ok {
		prevFailures = r.ConsecutiveFailures
	}
	if m.probeOneModel(channelType, channelIndex, channelID, u, apiKey, keyBaseURLs, model, CheckKindL2, prevFailures, policy) && capUID != "" {
		m.l2ReuseMu.Lock()
		m.l2Reuse[capUID] = model
		m.l2ReuseMu.Unlock()
	}
}

// capabilityUIDForL2 按 L2 实际探测端点计算能力 UID（与 autopilot 发现侧同口径：
// 站点+配额组+身份 baseURL+协议；protocol 直接取 channelType）。keyBaseURLs 为空时
// 返回 ""（不参与去重，ClaimProbe 对空 UID 恒放行）。
func capabilityUIDForL2(channelType string, u *config.UpstreamConfig, apiKey string, keyBaseURLs []string) string {
	if u == nil || len(keyBaseURLs) == 0 {
		return ""
	}
	baseURL := keyBaseURLs[0]
	canonical := utils.CanonicalBaseURL(baseURL, u.ServiceType)
	identityBaseURL := utils.MetricsIdentityBaseURL(canonical, u.ServiceType)
	siteIdentity := config.SiteIdentityForBaseURL(baseURL)
	groupIdentity := config.NormalizeGroupIdentity(quotaGroupForL2Key(u, apiKey))
	return config.GenerateCapabilityUID(siteIdentity, groupIdentity, identityBaseURL, channelType)
}

// quotaGroupForL2Key 返回渠道在指定 key 下的配额组（与 autopilot quotaGroupForChannelKey 同口径）。
func quotaGroupForL2Key(u *config.UpstreamConfig, apiKey string) string {
	if u == nil {
		return ""
	}
	for _, cfg := range config.NormalizeAPIKeyConfigsForView(*u) {
		if apiKey != "" && strings.TrimSpace(cfg.Key) != apiKey {
			continue
		}
		return strings.TrimSpace(cfg.QuotaGroup)
	}
	return ""
}

// writeReusedL2Record 为复用本周期同能力 L2 成功结论的 key 写一条 ok 记录（未发真实请求）。
// detail 标注复用来源，保持"每 key 每周期有 L2 记录"不变量，熔断/到期读取口径不受影响。
func (m *Manager) writeReusedL2Record(channelType, channelID, apiKey, model, capUID string) {
	rec := metrics.KeyHealthRecord{
		ChannelType: channelType,
		ChannelID:   channelID,
		KeyMask:     utils.MaskAPIKey(apiKey),
		CheckKind:   CheckKindL2,
		LastCheckAt: m.now(),
		LastStatus:  StatusOK,
		Detail:      "model=" + model + " (capability-ledger-reused)",
	}
	if err := m.store.UpsertKeyHealth(rec); err != nil {
		log.Printf("[HealthCheck] 写入 key 健康记录失败 (%s): %v", channelKey(channelType, channelID), err)
	}
	log.Printf("[HealthCheck] L2 复用: 渠道=%s, key=%s, 模型=%s, CapabilityUID=%s 本周期已探测",
		channelKey(channelType, channelID), rec.KeyMask, model, capUID)
}

// probeOneModel 对单个模型执行一次 L2 真实调用并写入 key_health。
// checkKind 通常为 "l2" 或 "l2:<model>"；prevFailures 取自该 checkKind 的上次记录。
// 返回是否探测成功（供调用方登记能力复用结论）。
func (m *Manager) probeOneModel(
	channelType string, channelIndex int, channelID string,
	u *config.UpstreamConfig, apiKey string, keyBaseURLs []string, model, checkKind string,
	prevFailures int64,
	policy config.ResolvedHealthCheckPolicy,
) bool {
	keyMask := utils.MaskAPIKey(apiKey)

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
		CheckKind:   checkKind,
		LastCheckAt: start,
	}

	req, err := handlers.BuildHealthCheckL2Request(channelType, &probeChannel, model)
	if err != nil {
		// 请求构建失败属于本地配置问题而非上游健康度，只打日志不写记录
		log.Printf("[HealthCheck] L2 跳过: 渠道=%s, key=%s, 构建验活请求失败: %v",
			channelKey(channelType, channelID), keyMask, err)
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), policy.Timeout)
	success, _, statusCode, respBody, sendErr := handlers.SendHealthCheckL2Stream(ctx, &probeChannel, req.WithContext(ctx), channelType)
	cancel()

	rec.LatencyMs = time.Since(start).Milliseconds()

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
	log.Printf("[HealthCheck] L2 验证完成: 渠道=%s, key=%s, 模型=%s, check_kind=%s, 结果=%s, 延迟=%dms",
		channelKey(channelType, channelID), keyMask, model, checkKind, rec.LastStatus, rec.LatencyMs)
	return success
}

// checkKeyL2Sparse 对已由 L1 做过真实调用的火山套餐 key 做预算受限的模型级探测。
// 每个模型独立落 check_kind="l2:<model>"，避免模型状态互相覆盖。
//
// 注意：稀疏 L2 暂不接 CapabilityProbeLedger——其探测选择含 per-key 预算/熔断输入
// （selectL2ProbeModels），跨 key 复用结论会与预算口径冲突；且套餐 L1 已完成一次
// 真实调用，稀疏探测本身是低频补充，去重收益有限。
func (m *Manager) checkKeyL2Sparse(
	channelType string, channelIndex int, channelID string,
	u *config.UpstreamConfig, apiKey string, keyBaseURLs []string, l1Models []string,
	policy config.ResolvedHealthCheckPolicy,
	prevL2ByModel map[string]metrics.KeyHealthRecord,
) {
	if policy.SparseL2MaxModels <= 0 {
		return
	}

	m.mu.Lock()
	lookup := m.modelCircuitLookup
	m.mu.Unlock()
	var circuit *metrics.ModelCircuitTracker
	if lookup != nil {
		circuit = lookup(channelType)
	}

	keyHash := metrics.ModelCircuitKeyHash(apiKey)
	models := m.selectL2ProbeModels(u.ChannelUID, keyHash, u, l1Models, circuit, prevL2ByModel, policy, m.now())
	if len(models) == 0 {
		log.Printf("[HealthCheck] 稀疏 L2 跳过: 渠道=%s, key=%s, 本周期无预算内待探测模型",
			channelKey(channelType, channelID), utils.MaskAPIKey(apiKey))
		return
	}

	for _, model := range models {
		checkKind := l2ModelCheckKind(model)
		prevFailures := int64(0)
		if r, ok := prevL2ByModel[model]; ok {
			prevFailures = r.ConsecutiveFailures
		}
		m.probeOneModel(channelType, channelIndex, channelID, u, apiKey, keyBaseURLs, model, checkKind, prevFailures, policy)
	}
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
