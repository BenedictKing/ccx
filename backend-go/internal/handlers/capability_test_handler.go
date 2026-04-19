package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// ============== 缓存定义 ==============

const (
	capabilityCacheTTL    = 30 * time.Minute // 缓存 TTL（每次命中续期）
	capabilityCacheMaxTTL = 4 * time.Hour    // 缓存最大生存时间（从首次创建算起）
)

// capabilityCacheEntry 缓存条目
type capabilityCacheEntry struct {
	response  CapabilityTestResponse
	createdAt time.Time // 首次创建时间（用于计算最大生存期）
	expiresAt time.Time // 过期时间（每次命中续期）
}

// capabilityCache 全局能力测试缓存
var capabilityCache = struct {
	sync.RWMutex
	entries map[string]*capabilityCacheEntry
}{
	entries: make(map[string]*capabilityCacheEntry),
}

// capability test 专用共享 provider：避免在 ResponsesProvider 上重复创建 SessionManager 清理协程
var (
	capabilityClaudeProvider    providers.Provider = &providers.ClaudeProvider{}
	capabilityOpenAIProvider    providers.Provider = &providers.OpenAIProvider{}
	capabilityGeminiProvider    providers.Provider = &providers.GeminiProvider{}
	capabilityResponsesProvider providers.Provider = &providers.ResponsesProvider{}
)

// buildCapabilityCacheKey 构建缓存 key（基于 baseURL + apiKey、协议列表、模型列表）
func buildCapabilityCacheKey(baseURL string, apiKey string, protocols []string, models []string) string {
	sorted := make([]string, len(protocols))
	copy(sorted, protocols)
	sort.Strings(sorted)

	normalizedModels := normalizeCapabilityModels(models)
	metricsKey := metrics.GenerateMetricsKey(baseURL, apiKey)
	return fmt.Sprintf("%s:%s:%s", metricsKey, strings.Join(sorted, ","), strings.Join(normalizedModels, ","))
}

func normalizeCapabilityModels(models []string) []string {
	if len(models) == 0 {
		return nil
	}

	unique := make(map[string]struct{}, len(models))
	normalized := make([]string, 0, len(models))
	for _, model := range models {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" {
			continue
		}
		if _, exists := unique[trimmed]; exists {
			continue
		}
		unique[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	sort.Strings(normalized)
	return normalized
}

func buildCapabilityTestURL(baseURL, versionPrefix, endpoint string) string {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	versionPattern := regexp.MustCompile(`/v\d+[a-z]*$`)
	hasVersionSuffix := versionPattern.MatchString(baseURL)
	if !hasVersionSuffix && !skipVersionPrefix {
		baseURL += versionPrefix
	}

	return baseURL + endpoint
}

// getCapabilityCache 读取缓存，命中时自动续期（不超过最大生存期）
// 全程持有写锁以避免并发读写 expiresAt 的竞态
func getCapabilityCache(key string) (*CapabilityTestResponse, bool) {
	capabilityCache.Lock()
	defer capabilityCache.Unlock()

	entry, ok := capabilityCache.entries[key]
	if !ok {
		return nil, false
	}

	now := time.Now()
	if now.After(entry.expiresAt) {
		// 已过期，删除
		delete(capabilityCache.entries, key)
		return nil, false
	}

	// 命中，续期（不超过最大生存期）
	newExpiry := now.Add(capabilityCacheTTL)
	maxExpiry := entry.createdAt.Add(capabilityCacheMaxTTL)
	if newExpiry.After(maxExpiry) {
		newExpiry = maxExpiry
	}
	entry.expiresAt = newExpiry

	return &entry.response, true
}

// setCapabilityCache 写入缓存
func setCapabilityCache(key string, resp CapabilityTestResponse) {
	now := time.Now()
	capabilityCache.Lock()
	capabilityCache.entries[key] = &capabilityCacheEntry{
		response:  resp,
		createdAt: now,
		expiresAt: now.Add(capabilityCacheTTL),
	}
	capabilityCache.Unlock()
}

// ============== 类型定义 ==============

// CapabilityTestRequest 能力测试请求体
type CapabilityTestRequest struct {
	TargetProtocols []string `json:"targetProtocols"`
	Models          []string `json:"models"`        // 可选：用户指定要测试的模型列表，为空时使用预定义列表
	Timeout         int      `json:"timeout"`       // 毫秒
	PreviousJobID   string   `json:"previousJobId"` // 可选：上次测试的 jobId，用于复用成功结果
}

type ModelTestResult struct {
	Model              string  `json:"model"`
	Success            bool    `json:"success"`
	Skipped            bool    `json:"skipped,omitempty"`
	Latency            int64   `json:"latency"` // 毫秒
	StreamingSupported bool    `json:"streamingSupported"`
	Error              *string `json:"error,omitempty"`
	StartedAt          string  `json:"startedAt,omitempty"`
	TestedAt           string  `json:"testedAt"`
}

// ProtocolTestResult 单个协议测试结果
type ProtocolTestResult struct {
	Protocol           string            `json:"protocol"`
	Success            bool              `json:"success"`
	Latency            int64             `json:"latency"` // 毫秒
	StreamingSupported bool              `json:"streamingSupported"`
	TestedModel        string            `json:"testedModel"` // 优先返回首个成功模型名称，兼容旧字段
	ModelResults       []ModelTestResult `json:"modelResults,omitempty"`
	SuccessCount       int               `json:"successCount,omitempty"`
	AttemptedModels    int               `json:"attemptedModels,omitempty"`
	Error              *string           `json:"error"`
	TestedAt           string            `json:"testedAt"`
}

// CapabilityTestResponse 能力测试响应体
type CapabilityTestResponse struct {
	ChannelID           int                  `json:"channelId"`
	ChannelName         string               `json:"channelName"`
	SourceType          string               `json:"sourceType"`
	Tests               []ProtocolTestResult `json:"tests"`
	CompatibleProtocols []string             `json:"compatibleProtocols"`
	TotalDuration       int64                `json:"totalDuration"` // 毫秒
}

// ============== 主处理器 ==============

// TestChannelCapability 渠道能力测试处理器
// channelKind 决定从哪个配置获取渠道：messages/responses/gemini/chat
func TestChannelCapability(cfgManager *config.ConfigManager, channelLogStore *metrics.ChannelLogStore, channelKind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}

		channel, err := getCapabilityTestChannel(cfgManager, channelKind, id)
		if err != nil {
			statusCode := http.StatusBadRequest
			if err.Error() == "channel not found" {
				statusCode = http.StatusNotFound
			}
			c.JSON(statusCode, gin.H{"error": err.Error()})
			return
		}

		var req CapabilityTestRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		timeout := 10 * time.Second
		if req.Timeout > 0 {
			timeout = time.Duration(req.Timeout) * time.Millisecond
		}

		protocols := req.TargetProtocols
		if len(protocols) == 0 {
			protocols = []string{"messages", "chat", "gemini", "responses"}
		}

		effectiveRPM := channel.RPM
		if effectiveRPM <= 0 {
			effectiveRPM = 10
		}
		channel.RPM = effectiveRPM

		if len(channel.APIKeys) == 0 && len(channel.DisabledAPIKeys) == 0 {
			errMsg := "no_api_key"
			resp := CapabilityTestResponse{
				ChannelID:           id,
				ChannelName:         channel.Name,
				SourceType:          channel.ServiceType,
				Tests:               []ProtocolTestResult{},
				CompatibleProtocols: []string{},
				TotalDuration:       0,
			}
			job := createCapabilityJobFromResponse(id, channel.Name, channelKind, channel.ServiceType, protocols, timeout, resp, false)
			job.Lifecycle = CapabilityLifecycleDone
			job.Outcome = CapabilityOutcomeFailed
			job.Status = deriveCapabilityJobStatus(job.Lifecycle, job.Outcome)
			job.RunMode = CapabilityRunModeFresh
			job.Error = &errMsg
			capabilityJobs.create(job)
			c.JSON(http.StatusOK, gin.H{"jobId": job.JobID, "resumed": false, "job": job})
			return
		}

		baseURL := ""
		if len(channel.GetAllBaseURLs()) > 0 {
			baseURL = channel.GetAllBaseURLs()[0]
		}
		apiKey := ""
		if len(channel.APIKeys) > 0 {
			apiKey = channel.APIKeys[0]
		} else if len(channel.DisabledAPIKeys) > 0 {
			apiKey = channel.DisabledAPIKeys[0].Key
		}

		normalizedModels := normalizeCapabilityModels(req.Models)
		cacheKey := buildCapabilityCacheKey(baseURL, apiKey, protocols, normalizedModels)
		lookupKey := buildCapabilityJobLookupKey(cacheKey, channelKind, id)

		if cached, ok := getCapabilityCache(cacheKey); ok {
			log.Printf("[CapabilityTest-Cache] 渠道 %s (ID:%d) 命中缓存，创建已完成任务", channel.Name, id)
			cached.ChannelID = id
			cached.ChannelName = channel.Name
			cached.SourceType = channel.ServiceType
			job, reused := capabilityJobs.getOrCreateByLookupKey(lookupKey, func() *CapabilityTestJob {
				return createCapabilityJobFromResponse(id, channel.Name, channelKind, channel.ServiceType, protocols, timeout, *cached, true)
			})
			job.RunMode = CapabilityRunModeCacheHit
			job.CacheHit = true
			job.SummaryReason = "cache_hit"
			job.IsResumed = reused
			c.JSON(http.StatusOK, gin.H{"jobId": job.JobID, "resumed": reused, "job": job})
			return
		}

		job, reused := capabilityJobs.getOrCreateByLookupKey(lookupKey, func() *CapabilityTestJob {
			return newCapabilityTestJob(id, channel.Name, channelKind, channel.ServiceType, protocols, timeout)
		})
		job.IsResumed = reused

		// 检测到 cancelled job，恢复进度
		if reused && job.Lifecycle == CapabilityLifecycleCancelled {
			log.Printf("[CapabilityTest-Job] 恢复已取消的任务 %s，渠道 %s (ID:%d)", job.JobID, channel.Name, id)

			// 提取已成功的模型作为 previousResults
			previousResults := make(map[string]map[string]ModelTestResult)
			for _, test := range job.Tests {
				modelMap := make(map[string]ModelTestResult)
				for _, mr := range test.ModelResults {
					if mr.Outcome == CapabilityOutcomeSuccess {
						modelMap[mr.Model] = ModelTestResult{
							Model:              mr.Model,
							Success:            mr.Success,
							Latency:            mr.Latency,
							StreamingSupported: mr.StreamingSupported,
							Error:              mr.Error,
							StartedAt:          mr.StartedAt,
							TestedAt:           mr.TestedAt,
						}
					}
				}
				if len(modelMap) > 0 {
					previousResults[test.Protocol] = modelMap
				}
			}

			// 重置 failed/skipped 模型为 queued，准备重测
			capabilityJobs.update(job.JobID, func(j *CapabilityTestJob) {
				j.Lifecycle = CapabilityLifecyclePending
				j.Outcome = CapabilityOutcomeUnknown
				j.Status = deriveCapabilityJobStatus(j.Lifecycle, j.Outcome)
				j.RunMode = CapabilityRunModeResumedCancelled
				j.SummaryReason = "resumed_cancelled"
				j.IsResumed = true
				j.HasReusedResults = len(previousResults) > 0
				j.FinishedAt = ""
				for i := range j.Tests {
					if j.Tests[i].Lifecycle == CapabilityLifecycleCancelled || j.Tests[i].Outcome == CapabilityOutcomeFailed {
						j.Tests[i].Lifecycle = CapabilityLifecyclePending
						j.Tests[i].Outcome = CapabilityOutcomeUnknown
						j.Tests[i].Reason = nil
					}
					for k := range j.Tests[i].ModelResults {
						if j.Tests[i].ModelResults[k].Outcome == CapabilityOutcomeFailed ||
							j.Tests[i].ModelResults[k].Status == CapabilityModelStatusSkipped {
							j.Tests[i].ModelResults[k].Lifecycle = CapabilityLifecyclePending
							j.Tests[i].ModelResults[k].Outcome = CapabilityOutcomeUnknown
							j.Tests[i].ModelResults[k].Error = nil
							j.Tests[i].ModelResults[k].Reason = nil
						}
					}
				}
			})

			go runCapabilityTestJob(job.JobID, channelKind, id, *channel, protocols, timeout, cacheKey, lookupKey, previousResults, normalizedModels, cfgManager, channelLogStore)

			c.JSON(http.StatusOK, gin.H{"jobId": job.JobID, "resumed": true, "job": job})
			return
		}

		// 复用正在运行的 job
		if reused {
			log.Printf("[CapabilityTest-Job] 复用能力测试任务 %s，渠道 %s (ID:%d, 类型:%s)", job.JobID, channel.Name, id, channel.ServiceType)
			job.RunMode = CapabilityRunModeReusedRunning
			job.SummaryReason = "reused_running"
			job.IsResumed = true
			c.JSON(http.StatusOK, gin.H{"jobId": job.JobID, "resumed": true, "job": job})
			return
		}

		// 创建新 job
		log.Printf("[CapabilityTest-Job] 创建能力测试任务 %s，渠道 %s (ID:%d, 类型:%s)，协议: %v", job.JobID, channel.Name, id, channel.ServiceType, protocols)

		// 提取上次成功的结果用于复用（从 previousJobID）
		var previousResults map[string]map[string]ModelTestResult
		if req.PreviousJobID != "" {
			if prevJob, ok := capabilityJobs.get(req.PreviousJobID); ok && prevJob.ChannelID == id && prevJob.ChannelKind == channelKind {
				previousResults = make(map[string]map[string]ModelTestResult)
				for _, test := range prevJob.Tests {
					modelMap := make(map[string]ModelTestResult)
					for _, mr := range test.ModelResults {
						if mr.Status == CapabilityModelStatusSuccess {
							modelMap[mr.Model] = ModelTestResult{
								Model:              mr.Model,
								Success:            mr.Success,
								Latency:            mr.Latency,
								StreamingSupported: mr.StreamingSupported,
								Error:              mr.Error,
								StartedAt:          mr.StartedAt,
								TestedAt:           mr.TestedAt,
							}
						}
					}
					if len(modelMap) > 0 {
						previousResults[test.Protocol] = modelMap
					}
				}
				if len(previousResults) > 0 {
					job.RunMode = CapabilityRunModeReusedPreviousResult
					job.SummaryReason = "reused_previous_results"
					job.HasReusedResults = true
					log.Printf("[CapabilityTest-Job] 复用上次测试 %s 的成功结果，跳过 %d 个协议的成功模型",
						req.PreviousJobID, len(previousResults))
				}
			}
		}

		go runCapabilityTestJob(job.JobID, channelKind, id, *channel, protocols, timeout, cacheKey, lookupKey, previousResults, normalizedModels, cfgManager, channelLogStore)

		c.JSON(http.StatusOK, gin.H{"jobId": job.JobID, "resumed": false, "job": job})
		return
	}
}

// ============== 核心测试逻辑 ==============

// testItem 代表一个 (协议, 模型) 测试单元
type testItem struct {
	protocol string
	model    string
	index    int // 模型在其协议列表中的索引
}

// buildRoundRobinQueue 构建交错队列
// 输出: messages[0], chat[0], gemini[0], responses[0], messages[1], chat[1], ...
func buildRoundRobinQueue(protocolModels map[string][]string, protocols []string) []testItem {
	maxModels := 0
	for _, models := range protocolModels {
		if len(models) > maxModels {
			maxModels = len(models)
		}
	}

	queue := make([]testItem, 0)
	for round := 0; round < maxModels; round++ {
		for _, protocol := range protocols {
			models := protocolModels[protocol]
			if round < len(models) {
				queue = append(queue, testItem{
					protocol: protocol,
					model:    models[round],
					index:    round,
				})
			}
		}
	}
	return queue
}

func runCapabilityTestJob(jobID, channelKind string, channelID int, channel config.UpstreamConfig, protocols []string, timeout time.Duration, cacheKey, lookupKey string, previousResults map[string]map[string]ModelTestResult, userModels []string, cfgManager *config.ConfigManager, channelLogStore *metrics.ChannelLogStore) {
	// 创建可取消的 context，用于支持前端取消操作
	ctx, cancel := context.WithCancel(context.Background())
	capabilityJobs.setCancelFunc(jobID, cancel)

	// 检查是否在 queued 期间已被取消
	if ctx.Err() != nil {
		if lookupKey != "" {
			capabilityJobs.clearLookupKey(lookupKey)
		}
		return
	}

	updatedJob, _ := capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
		if job.Lifecycle == CapabilityLifecycleCancelled {
			return
		}
		job.Lifecycle = CapabilityLifecycleActive
		job.Outcome = CapabilityOutcomeUnknown
		job.Status = deriveCapabilityJobStatus(job.Lifecycle, job.Outcome)
		job.StartedAt = time.Now().Format(time.RFC3339Nano)
		if job.RunMode == "" {
			job.RunMode = CapabilityRunModeFresh
		}
	})

	if updatedJob != nil && updatedJob.Lifecycle == CapabilityLifecycleCancelled {
		log.Printf("[CapabilityTest-Job] 任务 %s 在 queued 期间已被取消，跳过执行", jobID)
		if lookupKey != "" {
			capabilityJobs.clearLookupKey(lookupKey)
		}
		return
	}

	log.Printf("[CapabilityTest-Job] 开始执行能力测试任务 %s，渠道 %s (ID:%d, 类型:%s)，协议: %v", jobID, channel.Name, channelID, channel.ServiceType, protocols)

	totalStart := time.Now()
	apiKey := ""
	if len(channel.APIKeys) > 0 {
		apiKey = channel.APIKeys[0]
	} else if len(channel.DisabledAPIKeys) > 0 {
		apiKey = channel.DisabledAPIKeys[0].Key
	}
	results := runRoundRobinTests(ctx, &channel, protocols, timeout, jobID, previousResults, userModels, cfgManager, channelID, channelKind, apiKey, channelLogStore)
	totalDuration := time.Since(totalStart).Milliseconds()

	compatible := make([]string, 0)
	for _, r := range results {
		if r.Success {
			compatible = append(compatible, r.Protocol)
		}
	}

	resp := CapabilityTestResponse{
		ChannelID:           channelID,
		ChannelName:         channel.Name,
		SourceType:          channel.ServiceType,
		Tests:               results,
		CompatibleProtocols: compatible,
		TotalDuration:       totalDuration,
	}

	// 编排器已在执行过程中通过 capabilityJobs.update 实时维护 job.Tests，
	// 这里只更新最终元数据，不重建 Tests（避免覆盖编排器维护的 skipped 等中间状态）
	capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
		if job.Lifecycle == CapabilityLifecycleCancelled {
			job.TotalDuration = totalDuration
			return
		}
		job.ChannelName = channel.Name
		job.SourceType = channel.ServiceType
		job.CompatibleProtocols = append([]string(nil), compatible...)
		job.TotalDuration = totalDuration
		job.FinishedAt = time.Now().Format(time.RFC3339Nano)
	})

	// 仅在未被取消且有兼容协议时写入缓存
	if len(compatible) > 0 && ctx.Err() == nil {
		setCapabilityCache(cacheKey, resp)
		log.Printf("[CapabilityTest-Cache] 渠道 %s (ID:%d) 写入缓存，兼容协议: %v", channel.Name, channelID, compatible)
	}

	// 取消时保留 lookupKey，允许后续恢复进度
	if lookupKey != "" && ctx.Err() == nil {
		capabilityJobs.clearLookupKey(lookupKey)
	}

	log.Printf("[CapabilityTest-Job] 能力测试任务 %s 完成，渠道 %s，兼容协议: %v，总耗时: %dms", jobID, channel.Name, compatible, totalDuration)
}

// runRoundRobinTests 核心编排器，串行按 round-robin 顺序逐个调度
// 所有模型都会被测试，不会在首次成功后跳过后续模型
// previousResults 可选：上次测试中成功的结果，跳过这些模型
func runRoundRobinTests(ctx context.Context, channel *config.UpstreamConfig, protocols []string, perModelTimeout time.Duration, jobID string, previousResults map[string]map[string]ModelTestResult, userModels []string, cfgManager *config.ConfigManager, channelID int, channelKind, apiKey string, channelLogStore *metrics.ChannelLogStore) []ProtocolTestResult {
	// 1. 收集各协议模型列表，初始化 job 状态
	protocolModels := make(map[string][]string)
	protocolTimedOut := make(map[string]bool) // true = 全局超时强制终止
	results := make(map[string]*ProtocolTestResult)

	for _, protocol := range protocols {
		var models []string
		var err error
		if len(userModels) > 0 {
			// 用户指定模型列表，所有协议共用
			models = userModels
		} else {
			models, err = getCapabilityProbeModels(protocol)
		}
		if err != nil {
			errMsg := "no_models_configured"
			results[protocol] = &ProtocolTestResult{
				Protocol: protocol,
				Error:    &errMsg,
				TestedAt: time.Now().Format(time.RFC3339),
			}
			protocolTimedOut[protocol] = true // 无模型配置，视为已终止
			capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
				for i := range job.Tests {
					if job.Tests[i].Protocol == protocol {
						job.Tests[i].Lifecycle = CapabilityLifecycleDone
						job.Tests[i].Outcome = CapabilityOutcomeFailed
						job.Tests[i].Reason = &errMsg
						job.Tests[i].Error = &errMsg
						job.Tests[i].Status = deriveCapabilityProtocolStatus(job.Tests[i].Lifecycle, job.Tests[i].Outcome)
						job.Tests[i].TestedAt = time.Now().Format(time.RFC3339Nano)
						break
					}
				}
			})
			log.Printf("[CapabilityTest-Protocol] 渠道 %s 获取 %s 协议测试模型失败: %v", channel.Name, protocol, err)
			continue
		}

		protocolModels[protocol] = models
		results[protocol] = &ProtocolTestResult{
			Protocol:        protocol,
			TestedAt:        time.Now().Format(time.RFC3339),
			AttemptedModels: len(models),
			ModelResults:    make([]ModelTestResult, len(models)),
		}

		// 初始化协议状态和模型列表
		capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
			for i := range job.Tests {
				if job.Tests[i].Protocol == protocol {
					job.Tests[i].Status = CapabilityProtocolStatusRunning
					job.Tests[i].Lifecycle = CapabilityLifecycleActive
					job.Tests[i].Outcome = CapabilityOutcomeUnknown
					job.Tests[i].Reason = nil
					job.Tests[i].AttemptedModels = len(models)
					job.Tests[i].ModelResults = make([]CapabilityModelJobResult, len(models))
					for idx, modelName := range models {
						job.Tests[i].ModelResults[idx] = CapabilityModelJobResult{
							Model:     modelName,
							Status:    CapabilityModelStatusQueued,
							Lifecycle: CapabilityLifecyclePending,
							Outcome:   CapabilityOutcomeUnknown,
						}
					}
					job.Tests[i].TestedAt = time.Now().Format(time.RFC3339Nano)
					break
				}
			}
		})
		log.Printf("[CapabilityTest-Protocol] 开始测试渠道 %s 的 %s 协议兼容性", channel.Name, protocol)
	}

	// 1.5 预填充上次成功的结果
	if len(previousResults) > 0 {
		for protocol, modelMap := range previousResults {
			models := protocolModels[protocol]
			if len(models) == 0 {
				continue
			}
			result := results[protocol]
			if result == nil {
				continue
			}
			for i, modelName := range models {
				if prevResult, ok := modelMap[modelName]; ok && prevResult.Success {
					result.ModelResults[i] = prevResult
					result.SuccessCount++
					if result.SuccessCount == 1 {
						result.TestedModel = prevResult.Model
						result.StreamingSupported = prevResult.StreamingSupported
					}
					// 更新 job 中对应模型状态
					capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
						updateCapabilityJobModelResult(job, protocol, modelName, CapabilityModelStatusSuccess, prevResult)
					})
				}
			}
		}
	}

	// 2. 构建交错队列（排除已有成功结果的模型）
	// 需要保留原始索引，因为 result.ModelResults 按原始顺序排列
	filteredProtocolModels := make(map[string][]string)
	originalIndexMap := make(map[string]map[string]int) // protocol -> model -> original index
	for protocol, models := range protocolModels {
		originalIndexMap[protocol] = make(map[string]int)
		var filtered []string
		for origIdx, model := range models {
			if prevModels, ok := previousResults[protocol]; ok {
				if prevResult, ok := prevModels[model]; ok && prevResult.Success {
					continue // 跳过已成功的模型
				}
			}
			originalIndexMap[protocol][model] = origIdx
			filtered = append(filtered, model)
		}
		filteredProtocolModels[protocol] = filtered
	}
	queue := buildRoundRobinQueue(filteredProtocolModels, protocols)

	// 修正 queue 中的 index 为原始列表中的索引
	for i := range queue {
		if idxMap, ok := originalIndexMap[queue[i].protocol]; ok {
			if origIdx, ok := idxMap[queue[i].model]; ok {
				queue[i].index = origIdx
			}
		}
	}

	// 3. 计算全局超时
	// 串行执行中每个模型最多耗时 max(interval, perModelTimeout)，累加所有模型 + 缓冲
	totalModels := len(queue)
	interval := time.Minute / time.Duration(channel.RPM)
	if interval <= 0 {
		interval = time.Minute / 10
	}
	perModelBudget := interval
	if perModelTimeout > perModelBudget {
		perModelBudget = perModelTimeout
	}
	globalTimeout := time.Duration(totalModels)*perModelBudget + 10*time.Second
	globalCtx, globalCancel := context.WithTimeout(ctx, globalTimeout)
	defer globalCancel()

	// 4. 逐项执行（所有模型都测，不早退）
	protocolStartTime := make(map[string]time.Time)
	protocolEndTime := make(map[string]time.Time)
	for _, item := range queue {
		// 检查全局超时
		if globalCtx.Err() != nil {
			log.Printf("[CapabilityTest-RoundRobin] 全局超时，终止测试")
			protocolTimedOut[item.protocol] = true
			break
		}

		// 记录协议首次测试时间
		if _, ok := protocolStartTime[item.protocol]; !ok {
			protocolStartTime[item.protocol] = time.Now()
		}

		// AcquireSendSlot（限流）
		if err := GetCapabilityTestDispatcher().AcquireSendSlot(globalCtx, interval); err != nil {
			log.Printf("[CapabilityTest-RoundRobin] 获取发送槽位失败: %v", err)
			break
		}

		// executeModelTest（单模型测试）
		modelResult := executeModelTest(globalCtx, channel, item.protocol, item.model, perModelTimeout, jobID, cfgManager, channelID, channelKind, apiKey, channelLogStore)
		result := results[item.protocol]
		result.ModelResults[item.index] = modelResult
		protocolEndTime[item.protocol] = time.Now() // 每次模型完成时更新协议结束时间

		if modelResult.Success {
			result.SuccessCount++
			// 首个成功模型：记录代表性字段，但继续测其余模型
			if result.SuccessCount == 1 {
				result.TestedModel = modelResult.Model
				result.StreamingSupported = modelResult.StreamingSupported
				result.Latency = protocolEndTime[item.protocol].Sub(protocolStartTime[item.protocol]).Milliseconds()
				log.Printf("[CapabilityTest-Protocol] 渠道 %s 的 %s 协议首个成功模型: %s (耗时: %dms)",
					channel.Name, item.protocol, result.TestedModel, result.Latency)
			}
		}
	}

	// 5. 收尾：标记残留 queued 模型为 skipped（仅超时时出现），更新协议最终状态
	for protocol, result := range results {
		models := protocolModels[protocol]

		// 回填未被调度到的模型（超时导致）为 skipped
		for i := range result.ModelResults {
			if result.ModelResults[i].Model == "" && i < len(models) {
				result.ModelResults[i] = ModelTestResult{
					Model:    models[i],
					Success:  false,
					Skipped:  true,
					TestedAt: time.Now().Format(time.RFC3339),
				}
				capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
					updateCapabilityJobModelResult(job, protocol, models[i], CapabilityModelStatusSkipped, result.ModelResults[i])
				})
			}
		}

		// 计算延迟：用协议的实际开始/结束时间，避免被其他协议的执行时间污染
		if startTime, ok := protocolStartTime[protocol]; ok {
			if endTime, ok := protocolEndTime[protocol]; ok {
				result.Latency = endTime.Sub(startTime).Milliseconds()
			} else {
				result.Latency = time.Since(startTime).Milliseconds()
			}
		}

		// 判断是否有实际测试过（至少有一个非 skipped 模型）
		hasTestedModel := false
		for _, mr := range result.ModelResults {
			if !mr.Skipped && mr.Model != "" {
				hasTestedModel = true
				break
			}
		}

		if result.SuccessCount > 0 {
			// 有至少一个成功模型
			result.Success = true
			capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
				for i := range job.Tests {
					if job.Tests[i].Protocol == protocol {
						job.Tests[i].Status = CapabilityProtocolStatusCompleted
						job.Tests[i].Success = true
						job.Tests[i].Latency = result.Latency
						job.Tests[i].StreamingSupported = result.StreamingSupported
						job.Tests[i].TestedModel = result.TestedModel
						job.Tests[i].SuccessCount = result.SuccessCount
						job.Tests[i].Error = nil
						job.Tests[i].TestedAt = time.Now().Format(time.RFC3339Nano)
						break
					}
				}
			})
			log.Printf("[CapabilityTest-Protocol] 渠道 %s 的 %s 协议测试完成 (成功: %d/%d, 耗时: %dms)",
				channel.Name, protocol, result.SuccessCount, result.AttemptedModels, result.Latency)
		} else if !hasTestedModel {
			// 协议完全未测试（超时）
			result.Success = false
			capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
				for i := range job.Tests {
					if job.Tests[i].Protocol == protocol {
						job.Tests[i].Status = CapabilityProtocolStatusFailed
						job.Tests[i].Success = false
						job.Tests[i].Latency = result.Latency
						job.Tests[i].SuccessCount = 0
						job.Tests[i].TestedAt = time.Now().Format(time.RFC3339Nano)
						for j := range job.Tests[i].ModelResults {
							if job.Tests[i].ModelResults[j].Status == CapabilityModelStatusQueued || job.Tests[i].ModelResults[j].Status == CapabilityModelStatusRunning {
								job.Tests[i].ModelResults[j].Status = CapabilityModelStatusSkipped
							}
						}
						break
					}
				}
			})
			log.Printf("[CapabilityTest-Protocol] 渠道 %s 的 %s 协议未实际测试（调度超时）", channel.Name, protocol)
		} else {
			// 全部模型测试失败
			result.Success = false
			errMsg := "all_models_failed"
			result.Error = &errMsg
			capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
				for i := range job.Tests {
					if job.Tests[i].Protocol == protocol {
						job.Tests[i].Status = CapabilityProtocolStatusFailed
						job.Tests[i].Success = false
						job.Tests[i].Latency = result.Latency
						job.Tests[i].SuccessCount = result.SuccessCount
						job.Tests[i].Error = result.Error
						job.Tests[i].TestedAt = time.Now().Format(time.RFC3339Nano)
						for j := range job.Tests[i].ModelResults {
							if job.Tests[i].ModelResults[j].Status == CapabilityModelStatusQueued || job.Tests[i].ModelResults[j].Status == CapabilityModelStatusRunning {
								job.Tests[i].ModelResults[j].Status = CapabilityModelStatusSkipped
							}
						}
						break
					}
				}
			})
			log.Printf("[CapabilityTest-Protocol] 渠道 %s 的 %s 协议全部模型测试失败 (尝试: %d, 总耗时: %dms)",
				channel.Name, protocol, result.AttemptedModels, result.Latency)
		}
	}

	// 6. 转换为有序结果
	orderedResults := make([]ProtocolTestResult, 0, len(protocols))
	for _, protocol := range protocols {
		if result, ok := results[protocol]; ok {
			orderedResults = append(orderedResults, *result)
		}
	}

	return orderedResults
}

// executeModelTest 单模型测试（不调用 AcquireSendSlot，由编排器负责限流）
func executeModelTest(ctx context.Context, channel *config.UpstreamConfig, protocol, model string, timeout time.Duration, jobID string, cfgManager *config.ConfigManager, channelID int, channelKind, apiKey string, channelLogStore *metrics.ChannelLogStore) ModelTestResult {
	startedAt := time.Now()
	modelResult := ModelTestResult{
		Model:     model,
		StartedAt: startedAt.Format(time.RFC3339Nano),
	}

	req, err := buildTestRequestWithModel(protocol, channel, model)
	if err != nil {
		errMsg := fmt.Sprintf("build_request_failed: %v", err)
		modelResult.Error = &errMsg
		modelResult.TestedAt = time.Now().Format(time.RFC3339Nano)
		capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
			updateCapabilityJobModelResult(job, protocol, model, CapabilityModelStatusFailed, modelResult)
		})
		log.Printf("[CapabilityTest-Model] 渠道 %s 构建 %s 测试请求失败 (模型: %s): %v", channel.Name, protocol, model, err)
		return modelResult
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req = req.WithContext(reqCtx)

	capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
		updateCapabilityJobModelResult(job, protocol, model, CapabilityModelStatusRunning, modelResult)
	})

	client := httpclient.GetManager().GetStandardClient(timeout, channel.InsecureSkipVerify, channel.ProxyURL)

	startTime := time.Now()
	log.Printf("[CapabilityTest-Model] 渠道 %s 启动 %s 协议模型测试 (模型: %s, startedAt: %s)",
		channel.Name, protocol, model, modelResult.StartedAt)
	success, streamingSupported, statusCode, respBody, sendErr := sendAndCheckStream(reqCtx, client, req, protocol)
	modelResult.Latency = time.Since(startTime).Milliseconds()
	modelResult.TestedAt = time.Now().Format(time.RFC3339Nano)
	baseURL := req.URL.String()
	recordCapabilityTestLog := func(success bool, statusCode int, errorInfo string) {
		common.RecordChannelLogWithSource(
			channelLogStore,
			channelID,
			model,
			"",
			statusCode,
			modelResult.Latency,
			success,
			apiKey,
			baseURL,
			errorInfo,
			protocol,
			false,
			metrics.RequestSourceCapabilityTest,
		)
	}

	// 拉黑判定：非 2xx 响应时检查是否需要永久拉黑该 Key
	if !success && cfgManager != nil && apiKey != "" && respBody != nil {
		blacklistResult := common.ShouldBlacklistKey(statusCode, respBody)
		if blacklistResult.ShouldBlacklist {
			isBalanceError := blacklistResult.Reason == "insufficient_balance"
			if !isBalanceError || channel.IsAutoBlacklistBalanceEnabled() {
				apiType := channelKindToApiType(channelKind)
				log.Printf("[CapabilityTest-Blacklist] 渠道 %s 的 %s 协议触发 Key 拉黑 (模型: %s, 原因: %s, 状态码: %d)",
					channel.Name, protocol, model, blacklistResult.Reason, statusCode)
				if err := cfgManager.BlacklistKey(apiType, channelID, apiKey, blacklistResult.Reason, blacklistResult.Message); err != nil {
					log.Printf("[CapabilityTest-Blacklist] 拉黑 Key 失败: %v", err)
				}
			}
		}
	}

	if success {
		modelResult.Success = true
		modelResult.StreamingSupported = streamingSupported
		recordCapabilityTestLog(true, statusCode, "")
		capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
			updateCapabilityJobModelResult(job, protocol, model, CapabilityModelStatusSuccess, modelResult)
		})
		log.Printf("[CapabilityTest-Model] 渠道 %s 的 %s 协议测试成功 (模型: %s, 流式: %v, 耗时: %dms)",
			channel.Name, protocol, model, streamingSupported, modelResult.Latency)
		return modelResult
	}

	errMsg := classifyError(sendErr, statusCode, reqCtx)
	if len(respBody) > 0 {
		errMsg = string(respBody)
	}
	errMsg = truncateCapabilityError(errMsg)
	modelResult.Error = &errMsg
	recordCapabilityTestLog(false, statusCode, errMsg)
	capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
		updateCapabilityJobModelResult(job, protocol, model, CapabilityModelStatusFailed, modelResult)
	})
	log.Printf("[CapabilityTest-Model] 渠道 %s 的 %s 协议测试失败 (模型: %s, 耗时: %dms): %s",
		channel.Name, protocol, model, modelResult.Latency, errMsg)
	return modelResult
}

func truncateCapabilityError(msg string) string {
	if len(msg) > 200 {
		return msg[:200]
	}
	return msg
}

// testProtocolCompatibility 并发测试多个协议的兼容性（已废弃，保留用于兼容）
func testProtocolCompatibility(ctx context.Context, channel *config.UpstreamConfig, protocols []string, timeout time.Duration, jobID string) []ProtocolTestResult {
	// 已废弃，直接调用新实现
	return runRoundRobinTests(ctx, channel, protocols, timeout, jobID, nil, nil, nil, 0, "", "", nil)
}

// testSingleProtocol 已废弃，保留用于兼容
func testSingleProtocol(ctx context.Context, channel *config.UpstreamConfig, protocol string, timeout time.Duration, jobID string) ProtocolTestResult {
	// 已废弃，直接调用新实现
	results := runRoundRobinTests(ctx, channel, []string{protocol}, timeout, jobID, nil, nil, nil, 0, "", "", nil)
	if len(results) > 0 {
		return results[0]
	}
	return ProtocolTestResult{Protocol: protocol, TestedAt: time.Now().Format(time.RFC3339)}
}

// testSingleModel 已废弃，保留用于兼容
func testSingleModel(ctx context.Context, channel *config.UpstreamConfig, protocol, model string, timeout time.Duration, jobID string) ModelTestResult {
	// 已废弃，直接调用 executeModelTest
	return executeModelTest(ctx, channel, protocol, model, timeout, jobID, nil, 0, "", "", nil)
}

func updateCapabilityJobModelResult(job *CapabilityTestJob, protocol, model string, status CapabilityModelStatus, result ModelTestResult) {
	for i := range job.Tests {
		if job.Tests[i].Protocol != protocol {
			continue
		}
		for j := range job.Tests[i].ModelResults {
			if job.Tests[i].ModelResults[j].Model != model {
				continue
			}
			job.Tests[i].ModelResults[j].Status = status
			job.Tests[i].ModelResults[j].Success = result.Success
			job.Tests[i].ModelResults[j].Latency = result.Latency
			job.Tests[i].ModelResults[j].StreamingSupported = result.StreamingSupported
			job.Tests[i].ModelResults[j].Error = result.Error
			job.Tests[i].ModelResults[j].StartedAt = result.StartedAt
			job.Tests[i].ModelResults[j].TestedAt = result.TestedAt
			switch status {
			case CapabilityModelStatusQueued:
				job.Tests[i].ModelResults[j].Lifecycle = CapabilityLifecyclePending
				job.Tests[i].ModelResults[j].Outcome = CapabilityOutcomeUnknown
				job.Tests[i].ModelResults[j].Reason = nil
			case CapabilityModelStatusRunning:
				job.Tests[i].ModelResults[j].Lifecycle = CapabilityLifecycleActive
				job.Tests[i].ModelResults[j].Outcome = CapabilityOutcomeUnknown
				job.Tests[i].ModelResults[j].Reason = nil
			case CapabilityModelStatusSuccess:
				job.Tests[i].ModelResults[j].Lifecycle = CapabilityLifecycleDone
				job.Tests[i].ModelResults[j].Outcome = CapabilityOutcomeSuccess
				job.Tests[i].ModelResults[j].Reason = nil
			case CapabilityModelStatusFailed:
				job.Tests[i].ModelResults[j].Lifecycle = CapabilityLifecycleDone
				job.Tests[i].ModelResults[j].Outcome = CapabilityOutcomeFailed
				job.Tests[i].ModelResults[j].Reason = result.Error
			case CapabilityModelStatusSkipped:
				job.Tests[i].ModelResults[j].Lifecycle = CapabilityLifecycleDone
				job.Tests[i].ModelResults[j].Outcome = CapabilityOutcomeUnknown
				reason := "not_run"
				if result.Error != nil && *result.Error == "cancelled" {
					job.Tests[i].ModelResults[j].Lifecycle = CapabilityLifecycleCancelled
					job.Tests[i].ModelResults[j].Outcome = CapabilityOutcomeCancelled
					reason = "cancelled"
				}
				job.Tests[i].ModelResults[j].Reason = &reason
			}
			return
		}
	}
}

// channelKindToApiType 将小写 channelKind 转为 BlacklistKey 需要的大写 apiType
func channelKindToApiType(channelKind string) string {
	switch channelKind {
	case "messages":
		return "Messages"
	case "chat":
		return "Chat"
	case "gemini":
		return "Gemini"
	case "responses":
		return "Responses"
	default:
		return "Messages"
	}
}

// ============== 请求构建 ==============

// buildTestRequestWithModel 构建最小化测试请求（指定模型）
func buildTestRequestWithModel(protocol string, channel *config.UpstreamConfig, model string) (*http.Request, error) {
	// 获取 BaseURL
	urls := channel.GetAllBaseURLs()
	if len(urls) == 0 {
		return nil, fmt.Errorf("no base URL configured")
	}
	baseURL := urls[0]

	apiKey := ""
	if len(channel.APIKeys) > 0 {
		apiKey = channel.APIKeys[0]
	} else if len(channel.DisabledAPIKeys) > 0 {
		// 活跃 key 已被拉黑清空，临时借用被拉黑的 key 完成能力测试（不恢复到活跃列表）
		apiKey = channel.DisabledAPIKeys[0].Key
	} else {
		return nil, fmt.Errorf("no_api_key")
	}

	var (
		requestURL string
		body       []byte
		err        error
		isGemini   bool
	)

	switch protocol {
	case "messages":
		requestURL = buildCapabilityTestURL(baseURL, "/v1", "/messages")
		body, err = json.Marshal(map[string]interface{}{
			"model": model,
			"system": []map[string]interface{}{
				{
					"type": "text",
					"text": "x-anthropic-billing-header: cc_version=2.1.71.2f9; cc_entrypoint=cli;",
				},
				{
					"type": "text",
					"text": "You are a Claude agent, built on Anthropic's Claude Agent SDK.",
					"cache_control": map[string]string{
						"type": "ephemeral",
					},
				},
			},
			"messages":   []map[string]string{{"role": "user", "content": "What are you best at: code generation, creative writing, or math problem solving?"}},
			"max_tokens": 100,
			"stream":     true,
			"thinking": map[string]interface{}{
				"type": "disabled",
			},
		})

	case "chat":
		requestURL = buildCapabilityTestURL(baseURL, "/v1", "/chat/completions")
		body, err = json.Marshal(map[string]interface{}{
			"model": model,
			"messages": []map[string]string{
				{"role": "system", "content": "You are a helpful assistant."},
				{"role": "user", "content": "What are you best at: code generation, creative writing, or math problem solving?"},
			},
			"max_tokens":       100,
			"stream":           true,
			"reasoning_effort": "none",
		})

	case "gemini":
		requestURL = buildCapabilityTestURL(baseURL, "/v1beta", "/models/"+model+":streamGenerateContent?alt=sse")
		body, err = json.Marshal(map[string]interface{}{
			"contents": []map[string]interface{}{
				{
					"role":  "user",
					"parts": []map[string]string{{"text": "What are you best at: code generation, creative writing, or math problem solving?"}},
				},
			},
			"systemInstruction": map[string]interface{}{
				"parts": []map[string]string{{"text": "You are Gemini CLI, an interactive CLI agent specializing in software engineering tasks."}},
			},
			"generationConfig": map[string]interface{}{
				"maxOutputTokens": 100,
				"thinkingConfig": map[string]interface{}{
					"thinkingLevel": "low",
				},
			},
		})
		isGemini = true

	case "responses":
		requestURL = buildCapabilityTestURL(baseURL, "/v1", "/responses")
		body, err = json.Marshal(map[string]interface{}{
			"model":             model,
			"input":             "What are you best at: code generation, creative writing, or math problem solving?",
			"instructions":      "You are Codex, a coding agent based on GPT-5.",
			"max_output_tokens": 100,
			"stream":            true,
			"reasoning": map[string]interface{}{
				"effort": "low",
			},
		})

	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}

	if err != nil {
		return nil, fmt.Errorf("marshal request body failed: %w", err)
	}

	req, err := http.NewRequest("POST", requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	// 设置通用头部
	req.Header.Set("Content-Type", "application/json")

	// 设置认证头部
	if isGemini {
		utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
	} else {
		utils.SetAuthenticationHeader(req.Header, apiKey)
		// Messages 协议需要 anthropic-version、anthropic-beta、User-Agent 和 X-App 头部
		if protocol == "messages" {
			req.Header.Set("anthropic-version", "2023-06-01")
			req.Header.Set("anthropic-beta", "claude-code-20250219,adaptive-thinking-2026-01-28,prompt-caching-scope-2026-01-05,effort-2025-11-24")
			req.Header.Set("User-Agent", "claude-cli/2.1.71 (external, cli)")
			req.Header.Set("X-App", "cli")
		}
		// Responses 协议需要 Originator 和 User-Agent 头部
		if protocol == "responses" {
			req.Header.Set("Originator", "codex_cli_rs")
			req.Header.Set("User-Agent", "codex_cli_rs/0.111.0 (Mac OS 26.3.0; arm64) iTerm.app/3.6.6")
		}
	}

	// 应用自定义请求头
	if channel.CustomHeaders != nil {
		for key, value := range channel.CustomHeaders {
			req.Header.Set(key, value)
		}
	}

	return req, nil
}

// buildTestRequest 构建最小化测试请求（使用首选模型，兼容旧接口）
func buildTestRequest(protocol string, channel *config.UpstreamConfig) (*http.Request, error) {
	model, err := getCapabilityProbeModel(protocol)
	if err != nil {
		return nil, err
	}
	return buildTestRequestWithModel(protocol, channel, model)
}

// getCapabilityStreamProvider 为能力测试返回不带长期后台依赖的流解析 provider
func getCapabilityStreamProvider(protocol string) providers.Provider {
	switch protocol {
	case "messages":
		return capabilityClaudeProvider
	case "chat":
		return capabilityOpenAIProvider
	case "gemini":
		return capabilityGeminiProvider
	case "responses":
		return capabilityResponsesProvider
	default:
		return nil
	}
}

// ============== 流式响应检测 ==============

// sendAndCheckStream 发送请求并检查流式响应能力
// 复用代理侧的规范化流预检：必须包含实际文本或语义内容才算成功
// 返回: success（HTTP 2xx 且有有效流内容）, streamingSupported（流内容非空）, statusCode, responseBody, error
func sendAndCheckStream(ctx context.Context, client *http.Client, req *http.Request, protocol string) (bool, bool, int, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return false, false, 0, nil, err
	}
	defer resp.Body.Close()

	// 非 2xx 视为不兼容，读取响应体用于拉黑判定
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return false, false, resp.StatusCode, bodyBytes, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	provider := getCapabilityStreamProvider(protocol)
	if provider == nil {
		return false, false, resp.StatusCode, nil, fmt.Errorf("unsupported protocol provider: %s", protocol)
	}

	// 通过 provider 将上游原始 SSE 规范化为代理侧使用的事件格式，
	// 再复用 common.PreflightStreamEvents 做统一的空响应判定。
	eventChan, errChan, err := provider.HandleStreamResponse(resp.Body)
	if err != nil {
		return false, false, resp.StatusCode, nil, err
	}

	type streamResult struct {
		preflight *common.StreamPreflightResult
		err       error
	}
	doneCh := make(chan streamResult, 1)

	go func() {
		preflight := common.PreflightStreamEvents(eventChan, errChan, nil)
		if preflight.HasError {
			doneCh <- streamResult{err: preflight.Error}
			return
		}
		doneCh <- streamResult{preflight: preflight}
	}()

	readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
	defer readCancel()

	var result streamResult
	select {
	case result = <-doneCh:
	case <-readCtx.Done():
		return false, false, resp.StatusCode, nil, fmt.Errorf("流式响应读取超时")
	}

	if result.err != nil {
		return false, false, resp.StatusCode, nil, result.err
	}

	if result.preflight == nil {
		return false, false, resp.StatusCode, nil, fmt.Errorf("流式响应预检失败")
	}

	if result.preflight.IsEmpty {
		if result.preflight.Diagnostic != "" {
			return false, false, 0, nil, fmt.Errorf("上游返回空响应 (%s)", result.preflight.Diagnostic)
		}
		return false, false, 0, nil, common.ErrEmptyStreamResponse
	}

	if isTimedOutPreflightResult(result.preflight) {
		return false, false, 0, nil, fmt.Errorf("流式响应预检超时，未收到任何 SSE 事件")
	}

	go func(eventChan <-chan string) {
		for range eventChan {
		}
	}(eventChan)

	return true, true, resp.StatusCode, nil, nil
}

func isTimedOutPreflightResult(preflight *common.StreamPreflightResult) bool {
	if preflight == nil {
		return false
	}
	return !preflight.HasError && !preflight.IsEmpty && len(preflight.BufferedEvents) == 0 && preflight.Diagnostic == "" && preflight.UnknownEventType == ""
}

// ============== 取消与重测 ==============

// CancelCapabilityTestJob 取消正在进行的能力测试
func CancelCapabilityTestJob(cfgManager *config.ConfigManager, channelKind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseCapabilityChannelID(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}

		jobID := c.Param("jobId")
		job, ok := capabilityJobs.get(jobID)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Capability test job not found"})
			return
		}

		if job.ChannelID != id || job.ChannelKind != channelKind {
			c.JSON(http.StatusNotFound, gin.H{"error": "Capability test job not found"})
			return
		}

		// 只能取消正在运行或排队中的 job
		if job.Status != CapabilityJobStatusRunning && job.Status != CapabilityJobStatusQueued {
			c.JSON(http.StatusConflict, gin.H{"error": "Job is not running"})
			return
		}

		// 调用 CancelFunc 取消 goroutine
		if cancelFn, ok := capabilityJobs.getCancelFunc(jobID); ok {
			cancelFn()
		}

		// 更新 job 状态
		capabilityJobs.update(jobID, func(job *CapabilityTestJob) {
			job.Status = CapabilityJobStatusCancelled
			job.Lifecycle = CapabilityLifecycleCancelled
			job.Outcome = CapabilityOutcomeCancelled
			job.FinishedAt = time.Now().Format(time.RFC3339Nano)
			for i := range job.Tests {
				for j := range job.Tests[i].ModelResults {
					switch job.Tests[i].ModelResults[j].Status {
					case CapabilityModelStatusQueued:
						job.Tests[i].ModelResults[j].Status = CapabilityModelStatusSkipped
						job.Tests[i].ModelResults[j].Lifecycle = CapabilityLifecycleDone
						job.Tests[i].ModelResults[j].Outcome = CapabilityOutcomeUnknown
						reason := "not_run"
						job.Tests[i].ModelResults[j].Reason = &reason
					case CapabilityModelStatusRunning:
						job.Tests[i].ModelResults[j].Status = CapabilityModelStatusSkipped
						job.Tests[i].ModelResults[j].Lifecycle = CapabilityLifecycleCancelled
						job.Tests[i].ModelResults[j].Outcome = CapabilityOutcomeCancelled
						reason := "cancelled"
						job.Tests[i].ModelResults[j].Reason = &reason
						job.Tests[i].ModelResults[j].Error = &reason
					}
				}
				job.Tests[i].Lifecycle = CapabilityLifecycleCancelled
				job.Tests[i].Outcome = CapabilityOutcomeCancelled
				reason := "cancelled"
				job.Tests[i].Reason = &reason
			}
		})

		log.Printf("[CapabilityTest-Cancel] 能力测试任务 %s 已取消", jobID)
		c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
	}
}

// RetryCapabilityTestModel 重测单个模型
func RetryCapabilityTestModel(cfgManager *config.ConfigManager, channelLogStore *metrics.ChannelLogStore, channelKind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := parseCapabilityChannelID(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}

		jobID := c.Param("jobId")
		job, ok := capabilityJobs.get(jobID)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Capability test job not found"})
			return
		}

		if job.ChannelID != id || job.ChannelKind != channelKind {
			c.JSON(http.StatusNotFound, gin.H{"error": "Capability test job not found"})
			return
		}

		var req struct {
			Protocol string `json:"protocol"`
			Model    string `json:"model"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Protocol == "" || req.Model == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "protocol and model are required"})
			return
		}

		// 仅允许在终态 job 上执行单模型重测，避免与主任务并发冲突导致状态抖动
		if job.Lifecycle == CapabilityLifecyclePending || job.Lifecycle == CapabilityLifecycleActive ||
			job.Status == CapabilityJobStatusQueued || job.Status == CapabilityJobStatusRunning {
			c.JSON(http.StatusConflict, gin.H{"error": "Capability test job is still running"})
			return
		}

		// 检查模型是否存在于 job 中
		modelFound := false
		modelRetryable := false
		for _, test := range job.Tests {
			if test.Protocol != req.Protocol {
				continue
			}
			for _, mr := range test.ModelResults {
				if mr.Model == req.Model {
					modelFound = true
					if mr.Status == CapabilityModelStatusFailed ||
						mr.Status == CapabilityModelStatusSkipped ||
						mr.Outcome == CapabilityOutcomeCancelled ||
						mr.Lifecycle == CapabilityLifecycleCancelled {
						modelRetryable = true
					}
					break
				}
			}
		}
		if !modelFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Model not found in job"})
			return
		}
		if !modelRetryable {
			c.JSON(http.StatusConflict, gin.H{"error": "Model is not retryable"})
			return
		}

		// 获取渠道配置
		channel, chErr := getCapabilityTestChannel(cfgManager, channelKind, id)
		if chErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": chErr.Error()})
			return
		}

		timeout := 10 * time.Second
		if job.TimeoutMilliseconds > 0 {
			timeout = time.Duration(job.TimeoutMilliseconds) * time.Millisecond
		}

		// 将 job/协议/模型切换到单模型重测态
		retryStartedAt := time.Now().Format(time.RFC3339Nano)
		capabilityJobs.update(jobID, func(j *CapabilityTestJob) {
			for i := range j.Tests {
				if j.Tests[i].Protocol != req.Protocol {
					continue
				}
				j.Tests[i].Lifecycle = CapabilityLifecycleActive
				j.Tests[i].Outcome = CapabilityOutcomeUnknown
				j.Tests[i].Status = CapabilityProtocolStatusRunning
				j.Tests[i].Reason = nil
				j.Tests[i].Error = nil
				updateCapabilityJobModelResult(j, req.Protocol, req.Model, CapabilityModelStatusRunning, ModelTestResult{
					Model:     req.Model,
					StartedAt: retryStartedAt,
				})
				break
			}
			j.Lifecycle = CapabilityLifecycleActive
			j.Outcome = CapabilityOutcomeUnknown
			j.Status = CapabilityJobStatusRunning
			j.FinishedAt = ""
		})

		// 异步执行单模型测试（使用独立可取消 context）
		// 不覆盖 job 的 CancelFunc，避免影响主任务的取消能力
		retryCtx, retryCancel := context.WithCancel(context.Background())

		go func() {
			defer retryCancel()
			apiKey := ""
			if len(channel.APIKeys) > 0 {
				apiKey = channel.APIKeys[0]
			} else if len(channel.DisabledAPIKeys) > 0 {
				apiKey = channel.DisabledAPIKeys[0].Key
			}
			modelResult := executeModelTest(retryCtx, channel, req.Protocol, req.Model, timeout, jobID, cfgManager, id, channelKind, apiKey, channelLogStore)

			// 更新协议测试时间戳；协议/任务整体状态由统一重算逻辑维护
			capabilityJobs.update(jobID, func(j *CapabilityTestJob) {
				for i := range j.Tests {
					if j.Tests[i].Protocol != req.Protocol {
						continue
					}
					j.Tests[i].TestedAt = time.Now().Format(time.RFC3339Nano)
					break
				}
			})

			log.Printf("[CapabilityTest-Retry] 单模型重测完成: job=%s, protocol=%s, model=%s, success=%v",
				jobID, req.Protocol, req.Model, modelResult.Success)
		}()

		c.JSON(http.StatusOK, gin.H{"status": "accepted"})
	}
}

// ============== 错误分类 ==============

// classifyError 对错误进行分类
func classifyError(err error, statusCode int, ctx context.Context) string {
	if ctx.Err() == context.DeadlineExceeded {
		return "timeout"
	}

	errStr := ""
	if err != nil {
		errStr = err.Error()
		if errors.Is(err, common.ErrEmptyStreamResponse) || strings.Contains(errStr, "上游返回空响应") {
			return "empty_response"
		}
	}

	if statusCode == 429 {
		return "rate_limited"
	}

	if statusCode > 0 {
		return fmt.Sprintf("http_error_%d", statusCode)
	}

	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") {
		return "timeout"
	}

	if errStr == "" {
		return "request_failed"
	}
	return "request_failed: " + errStr
}
