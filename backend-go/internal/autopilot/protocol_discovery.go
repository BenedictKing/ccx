package autopilot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/utils"
)

const (
	protocolDiscoveryProbeTimeout = 8 * time.Second
	protocolDiscoveryMaxTokens    = 64
	// protocolDiscoveryMaxModels 限制每个协议单轮探测的候选模型数量上限，避免模型数量过多时
	// 探测任务 = 协议数 x 模型数 造成探测风暴；未探测到的模型留给下一轮 discovery 补充。
	protocolDiscoveryMaxModels = 30
	// protocolDiscoveryProbeConcurrency 是逐模型探测的并发 worker 数，低于进程内查询场景
	// （如 handlers_auto_managed.go 的 DeepSeek 余额查询），因为探测请求打的是真实上游。
	protocolDiscoveryProbeConcurrency = 3
)

var discoverableProtocols = []string{"messages", "chat", "responses", "gemini"}

// protocolProbeOutcome 描述单次 (protocol, model) 探测的三态结果。
type protocolProbeOutcome int

const (
	protocolProbeSuccess protocolProbeOutcome = iota
	protocolProbeRateLimited
	protocolProbeFailed
)

type protocolProbeTask struct {
	protocol string
	model    string
}

type protocolProbeAttempt struct {
	outcome protocolProbeOutcome
	err     error
}

type protocolProbeResult struct {
	protocol string
	model    string
	result   protocolProbeAttempt
}

// protocolProbeSummary 聚合某个协议下所有候选模型的探测结果。
type protocolProbeSummary struct {
	attempted   int
	models      []string
	rateLimited int
	failed      int
	firstError  string
}

// protocolProbeMessage 生成带计数的探测结果说明，供前端展示。
func protocolProbeMessage(protocol string, summary protocolProbeSummary, attempted, totalModels int) string {
	if len(summary.models) == 0 {
		return fmt.Sprintf("%d 个模型均未验证通过 %s 协议", attempted, protocol)
	}
	msg := fmt.Sprintf("%d 个模型中 %d 个验证 %s 协议可用", attempted, len(summary.models), protocol)
	if summary.rateLimited > 0 {
		msg += fmt.Sprintf("，%d 个因限流未验证，将于下次发现重试", summary.rateLimited)
	}
	if summary.failed > 0 {
		msg += fmt.Sprintf("，%d 个不支持", summary.failed)
	}
	if attempted < totalModels {
		msg += fmt.Sprintf("；剩余 %d 个模型将于下次发现补充探测", totalModels-attempted)
	}
	return msg
}

// discoverEndpointProtocols 对每个非原生协议的候选模型逐个探测，只把探测成功的模型
// 写入该协议的 ProtocolModels，从而反映"渠道-key-模型-协议"四元组的真实可用性，
// 而不是用单个代表模型的探测结果代表全部模型。
func (r *AutoDiscoveryRunner) discoverEndpointProtocols(
	ctx context.Context,
	channel *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	result *EndpointDiscoveryResult,
) {
	if channel == nil || result == nil || !result.ProtocolOk {
		return
	}
	ensureConfiguredProtocolDiscovery(channel, result)

	// 按 CapabilityUID 周期去重：同站点同分组同协议在本发现周期内已探测过，则直接复用
	// 已共享的能力认知，避免跨账号 key 重复发探测请求。凭证 auth 仍按 key 各自在 /models 阶段验证。
	capUID := r.capabilityUIDForEndpoint(channel, baseURL)
	if capUID != "" && r.capabilityProbeLedger != nil && !r.capabilityProbeLedger.ClaimProbe(capUID) {
		if existing, ok := r.lookupExistingCapability(capUID); ok {
			log.Printf("[ProtocolDiscovery-LedgerSkip] 渠道 %s: CapabilityUID=%s 本周期已探测，复用共享能力",
				channel.ChannelUID, capUID)
			r.applyExistingCapabilityToResult(existing, result)
			return
		}
	}

	models := normalizeProtocolModels(result.Models)
	if len(models) == 0 {
		models = normalizeProtocolModels(channel.SupportedModels)
	}
	// /v1/models 空响应兜底：同一 baseURL + Key 的兄弟协议渠道画像里已知的清单
	// 描述的是同一个上游端点，可直接作为本轮协议探测的候选模型。
	usedSharedModels := false
	if len(models) == 0 {
		if shared := r.sharedUpstreamModels(channel, baseURL, apiKey); len(shared) > 0 {
			models = shared
			usedSharedModels = true
			log.Printf("[ProtocolDiscovery-SharedModels] 渠道 %s: models 清单为空，复用同上游兄弟渠道的 %d 个模型作为探测候选",
				channel.ChannelUID, len(shared))
		}
	}

	configuredProtocol := protocolForServiceType(channel.ServiceType)
	if len(models) == 0 {
		for _, protocol := range discoverableProtocols {
			if protocol != configuredProtocol {
				result.ProtocolDiscoveryError[protocol] = "没有可用于协议探测的模型"
			}
		}
		return
	}

	// 实测层 TTL 复用：逐模型 POST 探测真实消耗上游额度，存量画像中
	// protocolProbeStaleTTL 内的非原生协议实测结论本轮直接复用（交集收敛到当前 key 级清单），
	// 不满足条件或交集为空的协议仍走下面的逐模型探测。原生协议由 ensureConfiguredProtocolDiscovery 采信，不参与复用。
	reusedProtocols := r.reusableProtocolProbeResults(channel, baseURL, apiKey, models, configuredProtocol, time.Now())

	tasks := make([]protocolProbeTask, 0, len(discoverableProtocols)*len(models))
	attemptedCounts := make(map[string]int, len(discoverableProtocols))
	for _, protocol := range discoverableProtocols {
		// 原生协议通常直接采信 models 清单，无需逐模型探测；但走兄弟渠道兜底时，
		// 该清单并非本渠道实测所得，必须连原生协议一起探测，否则会保留空清单。
		if protocol == configuredProtocol && !usedSharedModels {
			continue
		}
		if reused, ok := reusedProtocols[protocol]; ok {
			// 复用存量实测结论：如实保留存量实测时间/来源/说明，不刷新成 now。
			result.ProtocolModels[protocol] = reused.models
			result.ProtocolDiscoveredAt[protocol] = reused.discoveredAt
			result.ProtocolDiscoverySource[protocol] = reused.source
			result.ProtocolDiscoveryMessage[protocol] = reused.message
			delete(result.ProtocolDiscoveryError, protocol)
			continue
		}
		probeModels := prioritizeProtocolProbeModelsWithDeclared(protocol, models, result.declaredEndpointTypes, protocolDiscoveryMaxModels)
		attemptedCounts[protocol] = len(probeModels)
		for _, model := range probeModels {
			tasks = append(tasks, protocolProbeTask{protocol: protocol, model: model})
		}
	}
	if len(tasks) == 0 {
		return
	}

	results := make(chan protocolProbeResult, len(tasks))
	workers := min(protocolDiscoveryProbeConcurrency, len(tasks))
	jobs := make(chan protocolProbeTask)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				probeCtx, cancel := context.WithTimeout(ctx, protocolDiscoveryProbeTimeout)
				probe := r.probeProtocolModel(probeCtx, channel, baseURL, apiKey, task.protocol, task.model)
				cancel()
				results <- protocolProbeResult{protocol: task.protocol, model: task.model, result: probe}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, task := range tasks {
			select {
			case jobs <- task:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	totals := make(map[string]protocolProbeSummary, len(attemptedCounts))
	for probe := range results {
		summary := totals[probe.protocol]
		summary.attempted++
		switch probe.result.outcome {
		case protocolProbeSuccess:
			summary.models = append(summary.models, probe.model)
		case protocolProbeRateLimited:
			summary.rateLimited++
		default:
			summary.failed++
			if summary.firstError == "" && probe.result.err != nil {
				summary.firstError = probe.result.err.Error()
			}
		}
		totals[probe.protocol] = summary
	}

	for protocol, attempted := range attemptedCounts {
		summary := totals[protocol]
		discoveredAt := time.Now().UTC()
		result.ProtocolDiscoveredAt[protocol] = discoveredAt
		result.ProtocolDiscoverySource[protocol] = "protocol_model_probe"
		result.ProtocolDiscoveryMessage[protocol] = protocolProbeMessage(protocol, summary, attempted, len(models))
		if len(summary.models) > 0 {
			result.ProtocolModels[protocol] = normalizeProtocolModels(summary.models)
			delete(result.ProtocolDiscoveryError, protocol)
			continue
		}
		delete(result.ProtocolModels, protocol)
		if summary.firstError != "" {
			result.ProtocolDiscoveryError[protocol] = summary.firstError
		} else {
			result.ProtocolDiscoveryError[protocol] = fmt.Sprintf("%s 协议没有可用模型", protocol)
		}
	}
}

// protocolProbeReuse 是一份可复用的存量协议实测结论（来自端点画像）。
type protocolProbeReuse struct {
	models       []string
	discoveredAt time.Time
	source       string
	message      string
}

// reusableProtocolProbeResults 返回存量画像中仍在 protocolProbeStaleTTL 内、可直接复用的
// 非原生协议实测结论（协议 -> 结论）。复用清单取 存量实测 ∩ 当前 key 级清单：
// 清单层（24h TTL）本轮刚刷新过，交集顺带剔除已下线模型；交集为空说明存量结论
// 与当前现实脱节，该协议不落条目（视为需要重探，由调用方走逐模型探测）。
// 存量画像可能来自旧版本（map 字段为 nil），逐项防御；画像缺失或 store 为 nil 时返回 nil。
func (r *AutoDiscoveryRunner) reusableProtocolProbeResults(channel *config.UpstreamConfig, baseURL, apiKey string, currentModels []string, configuredProtocol string, now time.Time) map[string]protocolProbeReuse {
	if r == nil || r.store == nil || channel == nil || len(currentModels) == 0 {
		return nil
	}
	endpointUID := GenerateEndpointUID(channel.ChannelUID, utils.CanonicalBaseURL(baseURL, channel.ServiceType), KeyHashFromAPIKey(apiKey))
	profile := r.store.Get(endpointUID)
	if profile == nil || len(profile.ProtocolModels) == 0 || len(profile.ProtocolDiscoveredAt) == 0 {
		return nil
	}
	reused := make(map[string]protocolProbeReuse)
	for _, protocol := range discoverableProtocols {
		if protocol == configuredProtocol {
			continue
		}
		stored := normalizeProtocolModels(profile.ProtocolModels[protocol])
		if len(stored) == 0 {
			continue
		}
		probedAt, ok := profile.ProtocolDiscoveredAt[protocol]
		if !ok || probedAt.IsZero() || now.Sub(probedAt) > protocolProbeStaleTTL {
			continue
		}
		intersection := intersectProtocolModels(stored, currentModels)
		if len(intersection) == 0 {
			continue
		}
		reused[protocol] = protocolProbeReuse{
			models:       intersection,
			discoveredAt: probedAt,
			source:       profile.ProtocolDiscoverySource[protocol],
			message:      profile.ProtocolDiscoveryMessage[protocol],
		}
	}
	if len(reused) == 0 {
		return nil
	}
	return reused
}

// intersectProtocolModels 返回两个模型清单的交集（normalize 后排序去重）。
func intersectProtocolModels(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	inB := make(map[string]struct{}, len(b))
	for _, model := range b {
		inB[model] = struct{}{}
	}
	intersection := make([]string, 0, min(len(a), len(b)))
	for _, model := range a {
		if _, ok := inB[model]; ok {
			intersection = append(intersection, model)
		}
	}
	return normalizeProtocolModels(intersection)
}

// capabilityUIDForEndpoint 根据渠道、baseURL 生成对应 CapabilityUID。
// 同一 CapabilityUID 跨账号共享协议/模型探测结论。
func (r *AutoDiscoveryRunner) capabilityUIDForEndpoint(channel *config.UpstreamConfig, baseURL string) string {
	if channel == nil {
		return ""
	}
	canonical := utils.CanonicalBaseURL(baseURL, channel.ServiceType)
	identityBaseURL := utils.MetricsIdentityBaseURL(canonical, channel.ServiceType)
	siteIdentity := config.SiteIdentityForBaseURL(baseURL)
	groupIdentity := config.NormalizeGroupIdentity(quotaGroupForChannelKey(channel, ""))
	protocol := protocolForServiceType(channel.ServiceType)
	return config.GenerateCapabilityUID(siteIdentity, groupIdentity, identityBaseURL, protocol)
}

// quotaGroupForChannelKey 返回渠道在指定 key 下的配额组；key 为空时返回首个 key 的配额组。
func quotaGroupForChannelKey(channel *config.UpstreamConfig, apiKey string) string {
	if channel == nil {
		return ""
	}
	for _, cfg := range config.NormalizeAPIKeyConfigsForView(*channel) {
		if apiKey != "" && strings.TrimSpace(cfg.Key) != apiKey {
			continue
		}
		return strings.TrimSpace(cfg.QuotaGroup)
	}
	return ""
}

// capabilityUIDForResult 返回 EndpointDiscoveryResult 对应的能力 UID（与 capabilityUIDForEndpoint 同语义）。
func (r *AutoDiscoveryRunner) capabilityUIDForResult(channel *config.UpstreamConfig, result *EndpointDiscoveryResult) string {
	if channel == nil || result == nil {
		return ""
	}
	return r.capabilityUIDForEndpoint(channel, result.BaseURL)
}

// lookupExistingCapability 从能力台账或共享能力画像中查找已探测过的 CapabilityUID。
// 复用逻辑只关注跨账号共享的协议/模型能力，不读取 key 级凭证状态。
func (r *AutoDiscoveryRunner) lookupExistingCapability(capUID string) (config.EndpointCapability, bool) {
	if r == nil || capUID == "" {
		return config.EndpointCapability{}, false
	}
	// 优先从 ProfileStore 查找同能力（任意 key）的最新探测结论。
	if r.store != nil {
		if profile := r.store.GetByCapabilityUID(capUID); profile != nil && len(profile.ProtocolModels) > 0 {
			return config.EndpointCapability{
				CapabilityUID:   capUID,
				IdentityBaseURL: profile.IdentityBaseURL,
				Protocol:        profile.ServiceType,
				Models:          profile.AvailableModels,
			}, true
		}
	}
	return config.EndpointCapability{}, false
}

// applyExistingCapabilityToResult 把已有能力认知写入协议探测结果，避免重复发请求。
func (r *AutoDiscoveryRunner) applyExistingCapabilityToResult(cap config.EndpointCapability, result *EndpointDiscoveryResult) {
	if cap.CapabilityUID == "" || result == nil {
		return
	}
	ensureProtocolDiscoveryMaps(result)
	discoveredAt := time.Now().UTC()
	for _, protocol := range discoverableProtocols {
		if protocol == cap.Protocol {
			continue
		}
		if _, exists := result.ProtocolModels[protocol]; exists {
			continue
		}
		result.ProtocolDiscoveryError[protocol] = "本周期已由同能力探测覆盖，无可用结论"
	}
	if cap.Protocol != "" {
		result.ProtocolModels[cap.Protocol] = normalizeProtocolModels(cap.Models)
		result.ProtocolDiscoveredAt[cap.Protocol] = discoveredAt
		result.ProtocolDiscoverySource[cap.Protocol] = "capability_ledger_reuse"
		result.ProtocolDiscoveryMessage[cap.Protocol] = fmt.Sprintf("本周期已由同能力 %s 探测覆盖，复用 %d 个模型", cap.CapabilityUID, len(cap.Models))
		delete(result.ProtocolDiscoveryError, cap.Protocol)
	}
}

// sharedUpstreamModels 返回同一上游端点（identityBaseURL + keyHash）在兄弟协议渠道画像中
// 已知的模型清单并集。
//
// 存在意义：同一个 baseURL + Key 只有一份 /v1/models 清单，但中转站的该接口并不可靠——
// 可能临时返回空 data 数组却仍是 HTTP 200。此时本渠道会得到 0 个模型，而兄弟渠道上一轮
// 探到的清单仍是关于这个上游的有效事实，用它兜底可以避免协议探测因"没有候选模型"
// 整体跳过，也避免把空清单当成"上游没有模型"。
func (r *AutoDiscoveryRunner) sharedUpstreamModels(channel *config.UpstreamConfig, baseURL, apiKey string) []string {
	if r == nil || r.store == nil || channel == nil {
		return nil
	}
	canonical := utils.CanonicalBaseURL(baseURL, channel.ServiceType)
	identityBaseURL := utils.MetricsIdentityBaseURL(canonical, channel.ServiceType)
	siblings := r.store.ListByUpstreamIdentity(identityBaseURL, KeyHashFromAPIKey(apiKey), channel.ChannelUID)
	if len(siblings) == 0 {
		return nil
	}
	merged := make([]string, 0)
	for _, sibling := range siblings {
		merged = append(merged, sibling.AvailableModels...)
		for _, models := range sibling.ProtocolModels {
			merged = append(merged, models...)
		}
	}
	return normalizeProtocolModels(merged)
}

func ensureConfiguredProtocolDiscovery(channel *config.UpstreamConfig, result *EndpointDiscoveryResult) {
	if channel == nil || result == nil || !result.ProtocolOk {
		return
	}
	ensureProtocolDiscoveryMaps(result)
	protocol := protocolForServiceType(channel.ServiceType)
	if protocol == "" {
		return
	}
	models := normalizeProtocolModels(result.Models)
	// 本函数在协议探测前后各调用一次（探测前打底，写画像前兜底）。models 清单为空而
	// 探测已为原生协议得出结论时不能回填空值，否则会把逐模型探测的结果覆盖掉。
	if len(models) == 0 && len(result.ProtocolModels[protocol]) > 0 {
		return
	}
	result.ProtocolModels[protocol] = models
	discoveredAt := time.Now().UTC()
	if result.ModelsDiscoveredAt != nil {
		discoveredAt = result.ModelsDiscoveredAt.UTC()
	}
	result.ProtocolDiscoveredAt[protocol] = discoveredAt
	result.ProtocolDiscoverySource[protocol] = result.ModelDiscoverySource
	result.ProtocolDiscoveryMessage[protocol] = result.ModelDiscoveryMessage
	delete(result.ProtocolDiscoveryError, protocol)
}

func ensureProtocolDiscoveryMaps(result *EndpointDiscoveryResult) {
	if result.ProtocolModels == nil {
		result.ProtocolModels = make(map[string][]string)
	}
	if result.ProtocolDiscoveredAt == nil {
		result.ProtocolDiscoveredAt = make(map[string]time.Time)
	}
	if result.ProtocolDiscoverySource == nil {
		result.ProtocolDiscoverySource = make(map[string]string)
	}
	if result.ProtocolDiscoveryMessage == nil {
		result.ProtocolDiscoveryMessage = make(map[string]string)
	}
	if result.ProtocolDiscoveryError == nil {
		result.ProtocolDiscoveryError = make(map[string]string)
	}
}

func protocolForServiceType(serviceType string) string {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "messages":
		return "messages"
	case "openai", "openai-chat", "chat":
		return "chat"
	case "responses", "codex", "copilot":
		return "responses"
	case "gemini":
		return "gemini"
	default:
		return ""
	}
}

// protocolProbePreferredPrefixes 定义每个协议的候选模型排序优先级，前缀命中越靠前的家族越先被探测。
var protocolProbePreferredPrefixes = map[string][]string{
	"messages":  {"claude-", "mimo-", "kimi-", "glm-", "deepseek-", "minimax-"},
	"chat":      {"gpt-", "o1", "o3", "o4", "codex-", "mimo-", "kimi-", "glm-", "deepseek-", "minimax-"},
	"responses": {"gpt-", "o1", "o3", "o4", "codex-", "mimo-", "kimi-", "glm-", "deepseek-", "minimax-"},
	"gemini":    {"gemini-", "mimo-", "kimi-", "glm-", "deepseek-", "minimax-"},
}

// prioritizeProtocolProbeModels 按协议的前缀优先级表对候选模型排序，并截断到最多 limit 个，
// 用于逐模型探测时限制单轮任务数量，避免模型数量过多造成探测风暴。
func prioritizeProtocolProbeModels(protocol string, models []string, limit int) []string {
	return prioritizeProtocolProbeModelsWithDeclared(protocol, models, nil, limit)
}

// prioritizeProtocolProbeModelsWithDeclared 在前缀优先级之上叠加上游声明的协议支持信息。
//
// declared 来自 new-api 的 supported_endpoint_types（模型 -> 协议集合）。它只抬高排序，
// 不做过滤：上游存在少报（声明 ["openai"] 的模型实测也能走 /v1/messages），
// 按它过滤会漏掉真实可用的模型。当模型数超过 limit 被截断时，这个提示能让确实声明
// 支持该协议的模型不被名字前缀规则挤掉。
func prioritizeProtocolProbeModelsWithDeclared(protocol string, models []string, declared map[string][]string, limit int) []string {
	if len(models) == 0 {
		return nil
	}
	prefixes := protocolProbePreferredPrefixes[protocol]
	declaresProtocol := func(model string) bool {
		return slices.Contains(declared[model], protocol)
	}
	rank := func(model string) int {
		// 已声明支持该协议的模型整体排在未声明的模型之前。
		base := 0
		if len(declared) > 0 && !declaresProtocol(model) {
			base = len(prefixes) + 1
		}
		lower := strings.ToLower(model)
		for i, prefix := range prefixes {
			if strings.HasPrefix(lower, prefix) {
				return base + i
			}
		}
		return base + len(prefixes)
	}
	ordered := append([]string(nil), models...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return rank(ordered[i]) < rank(ordered[j])
	})
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[:limit]
	}
	return ordered
}

// probeProtocolModel 探测单个 (protocol, model) 组合，按 HTTP 状态码分三态返回：
// 2xx 视为该模型在该协议下可用；429 视为限流（不代表不支持，留给下次发现周期重试）；
// 其余非 2xx 视为该模型明确不支持该协议或存在凭证/配置问题。
func (r *AutoDiscoveryRunner) probeProtocolModel(
	ctx context.Context,
	channel *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	protocol string,
	model string,
) protocolProbeAttempt {
	requestURL, body, sessionID, err := buildProtocolDiscoveryRequest(baseURL, protocol, model)
	if err != nil {
		return protocolProbeAttempt{outcome: protocolProbeFailed, err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return protocolProbeAttempt{outcome: protocolProbeFailed, err: fmt.Errorf("构建 %s 探测请求失败: %w", protocol, err)}
	}
	req.Header.Set("Content-Type", "application/json")
	if protocol == "gemini" && !utils.HasAuthenticationHeaderOverride(channel.AuthHeader) {
		utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
	} else {
		utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, channel.AuthHeader)
	}
	if protocol == "messages" {
		utils.ApplyClaudeCodeProbeHeaders(req.Header, sessionID)
	}
	if protocol == "responses" {
		req.Header.Set("Originator", "codex_cli_rs")
		req.Header.Set("User-Agent", "codex_cli_rs/0.111.0")
	}
	utils.ApplyCustomHeaders(req.Header, channel.CustomHeaders)

	client := r.client
	if client == nil {
		client = httpclient.GetManager().GetClient(httpclient.ClientOptions{
			Timeout:           protocolDiscoveryProbeTimeout,
			Insecure:          channel.InsecureSkipVerify,
			ProxyURL:          channel.ProxyURL,
			ProxyPreferDirect: channel.ProxyPreferDirect,
		})
	}
	resp, err := client.Do(req)
	if err != nil {
		return protocolProbeAttempt{outcome: protocolProbeFailed, err: fmt.Errorf("%s 协议请求失败: %w", protocol, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return protocolProbeAttempt{outcome: protocolProbeSuccess}
	}
	detail := strings.TrimSpace(string(bodyBytes))
	if len(detail) > 512 {
		detail = detail[:512]
	}
	var probeErr error
	if detail == "" {
		probeErr = fmt.Errorf("%s 协议返回 HTTP %d", protocol, resp.StatusCode)
	} else {
		probeErr = fmt.Errorf("%s 协议返回 HTTP %d: %s", protocol, resp.StatusCode, detail)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return protocolProbeAttempt{outcome: protocolProbeRateLimited, err: probeErr}
	}
	return protocolProbeAttempt{outcome: protocolProbeFailed, err: probeErr}
}

func buildProtocolDiscoveryRequest(baseURL, protocol, model string) (string, []byte, string, error) {
	var (
		requestURL string
		body       any
		sessionID  string
	)
	switch protocol {
	case "messages":
		metadata, generatedSessionID := utils.NewClaudeCodeProbeMetadata()
		sessionID = generatedSessionID
		requestURL = buildProtocolDiscoveryURL(baseURL, "/v1", "/messages")
		body = map[string]any{
			"model":      model,
			"system":     []any{utils.NewClaudeCodeProbeBillingBlock(), utils.NewClaudeCodeProbeIdentityBlock()},
			"messages":   []map[string]string{{"role": "user", "content": "Reply with OK."}},
			"metadata":   metadata,
			"max_tokens": protocolDiscoveryMaxTokens,
			"stream":     false,
		}
	case "chat":
		requestURL = buildProtocolDiscoveryURL(baseURL, "/v1", "/chat/completions")
		body = map[string]any{
			"model":      model,
			"messages":   []map[string]string{{"role": "user", "content": "Reply with OK."}},
			"max_tokens": protocolDiscoveryMaxTokens,
			"stream":     false,
		}
	case "responses":
		requestURL = buildProtocolDiscoveryURL(baseURL, "/v1", "/responses")
		body = map[string]any{
			"model":             model,
			"input":             "Reply with OK.",
			"max_output_tokens": protocolDiscoveryMaxTokens,
			"stream":            false,
		}
	case "gemini":
		requestURL = buildProtocolDiscoveryURL(baseURL, "/v1beta", "/models/"+model+":generateContent")
		body = map[string]any{
			"contents": []map[string]any{{
				"role":  "user",
				"parts": []map[string]string{{"text": "Reply with OK."}},
			}},
			"generationConfig": map[string]any{"maxOutputTokens": protocolDiscoveryMaxTokens},
		}
	default:
		return "", nil, "", fmt.Errorf("不支持的协议: %s", protocol)
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", nil, "", fmt.Errorf("序列化 %s 探测请求失败: %w", protocol, err)
	}
	return requestURL, bodyBytes, sessionID, nil
}

func buildProtocolDiscoveryURL(baseURL, versionPrefix, endpoint string) string {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	baseURL = strings.TrimSuffix(baseURL, "#")
	baseURL = strings.TrimRight(baseURL, "/")
	if !skipVersionPrefix && !verifyVersionPattern.MatchString(baseURL) {
		baseURL += versionPrefix
	}
	return baseURL + endpoint
}

func normalizeProtocolModels(models []string) []string {
	if len(models) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(models))
	result := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}

func hashProtocolModels(protocolModels map[string][]string) map[string]string {
	if len(protocolModels) == 0 {
		return nil
	}
	result := make(map[string]string, len(protocolModels))
	for protocol, models := range protocolModels {
		normalized := normalizeProtocolModels(models)
		sum := sha256.Sum256([]byte(strings.Join(normalized, ",")))
		result[protocol] = hex.EncodeToString(sum[:8])
	}
	return result
}

func cloneProtocolModels(source map[string][]string) map[string][]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string][]string, len(source))
	for protocol, models := range source {
		result[protocol] = append([]string(nil), models...)
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneTimeMap(source map[string]time.Time) map[string]time.Time {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]time.Time, len(source))
	for key, value := range source {
		result[key] = value.UTC()
	}
	return result
}

func cloneTimePointer(source *time.Time) *time.Time {
	if source == nil {
		return nil
	}
	value := source.UTC()
	return &value
}
