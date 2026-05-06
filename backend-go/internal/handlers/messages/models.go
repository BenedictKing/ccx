// Package messages 提供 Claude Messages API 的处理器
package messages

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

const modelsRequestTimeout = 30 * time.Second

var errNoManualModelsChannel = errors.New("no manual models channel")

type ModelsResponse = common.ModelsResponse
type ModelEntry = common.ModelEntry

// ModelsHandler 处理 /v1/models 请求，从 Messages 和 Responses 渠道获取并合并模型列表
func ModelsHandler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		// 并行从两种渠道获取模型列表
		messagesModels := fetchModelsFromChannels(c, cfgManager, channelScheduler, false)
		responsesModels := fetchModelsFromChannels(c, cfgManager, channelScheduler, true)

		// 合并去重
		mergedModels := mergeModels(messagesModels, responsesModels)

		if len(mergedModels) == 0 {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"message": "models endpoint not available from any upstream",
					"type":    "not_found_error",
				},
			})
			return
		}

		response := ModelsResponse{
			Object: "list",
			Data:   mergedModels,
		}

		log.Printf("[Models] 合并完成: messages=%d, responses=%d, merged=%d",
			len(messagesModels), len(responsesModels), len(mergedModels))

		c.JSON(http.StatusOK, response)
	}
}

// ModelsDetailHandler 处理 /v1/models/:model 请求，转发到上游
func ModelsDetailHandler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		modelID := c.Param("model")
		if modelID == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"message": "model id is required",
					"type":    "invalid_request_error",
				},
			})
			return
		}

		// 先尝试 Messages 渠道
		if body, ok := tryModelsRequest(c, cfgManager, channelScheduler, "GET", "/"+modelID, false); ok {
			c.Data(http.StatusOK, "application/json", body)
			return
		}

		// 再尝试 Responses 渠道
		if body, ok := tryModelsRequest(c, cfgManager, channelScheduler, "GET", "/"+modelID, true); ok {
			c.Data(http.StatusOK, "application/json", body)
			return
		}

		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"message": "model not found",
				"type":    "not_found_error",
			},
		})
	}
}

// fetchModelsFromChannels 从指定类型的渠道获取模型列表
func fetchModelsFromChannels(c *gin.Context, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, isResponses bool) []ModelEntry {
	body, ok := tryModelsRequest(c, cfgManager, channelScheduler, "GET", "", isResponses)
	if !ok {
		return nil
	}

	var resp ModelsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		channelType := "Messages"
		if isResponses {
			channelType = "Responses"
		}
		log.Printf("[%s-Models] 解析渠道响应失败: %v", channelType, err)
		return nil
	}

	return resp.Data
}

// mergeModels 合并两个模型列表并去重（按 ID）
func mergeModels(models1, models2 []ModelEntry) []ModelEntry {
	seen := make(map[string]bool)
	var result []ModelEntry

	// 先添加第一个列表的模型
	for _, m := range models1 {
		if !seen[m.ID] {
			seen[m.ID] = true
			result = append(result, m)
		}
	}

	// 再添加第二个列表中不重复的模型
	for _, m := range models2 {
		if !seen[m.ID] {
			seen[m.ID] = true
			result = append(result, m)
		}
	}

	return result
}

// tryModelsRequest 使用调度器选择渠道，按故障转移顺序尝试请求 models 端点
func tryModelsRequest(c *gin.Context, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler, method, suffix string, isResponses bool) ([]byte, bool) {
	failedChannels := make(map[int]bool)
	maxChannelRetries := 10 // 最多尝试 10 个渠道

	channelType := "Messages"
	if isResponses {
		channelType = "Responses"
	}

	for attempt := 0; attempt < maxChannelRetries; attempt++ {
		kind := scheduler.ChannelKindMessages
		if isResponses {
			kind = scheduler.ChannelKindResponses
		}

		selection, err := channelScheduler.SelectChannel(c.Request.Context(), "", failedChannels, kind, "", c.Param("routePrefix"))
		if err != nil {
			manualSelection, manualErr := selectManualModelsChannel(cfgManager, failedChannels, kind, c.Param("routePrefix"))
			if manualErr == nil {
				selection = manualSelection
				log.Printf("[%s-Models] 回退到手工模型列表渠道: channel=%s, reason=%s", channelType, selection.Upstream.Name, selection.Reason)
			} else {
				log.Printf("[%s-Models] 渠道无可用: %v", channelType, err)
				break
			}
		}

		upstream := selection.Upstream
		if upstream.UsesManualModels() {
			if suffix == "" {
				body, err := common.MarshalManualModelsResponse(upstream.ManualModels)
				if err != nil {
					log.Printf("[%s-Models] 构造手工模型列表失败: channel=%s, error=%v", channelType, upstream.Name, err)
					failedChannels[selection.ChannelIndex] = true
					continue
				}
				log.Printf("[%s-Models] 返回手工模型列表: channel=%s, count=%d, reason=%s", channelType, upstream.Name, len(upstream.ManualModels), selection.Reason)
				return body, true
			}

			modelID := strings.TrimPrefix(suffix, "/")
			entry, ok := common.FindManualModelEntry(upstream.ManualModels, modelID)
			if !ok {
				log.Printf("[%s-Models] 手工模型列表未命中模型: channel=%s, model=%s", channelType, upstream.Name, modelID)
				failedChannels[selection.ChannelIndex] = true
				continue
			}

			body, err := json.Marshal(entry)
			if err != nil {
				log.Printf("[%s-Models] 构造手工模型详情失败: channel=%s, model=%s, error=%v", channelType, upstream.Name, modelID, err)
				failedChannels[selection.ChannelIndex] = true
				continue
			}
			log.Printf("[%s-Models] 返回手工模型详情: channel=%s, model=%s, reason=%s", channelType, upstream.Name, modelID, selection.Reason)
			return body, true
		}

		url := buildModelsURL(upstream.BaseURL) + suffix
		client := httpclient.GetManager().GetStandardClient(modelsRequestTimeout, upstream.InsecureSkipVerify)

		apiKey, _, err := cfgManager.GetAdminAPIKey(upstream, nil, channelType)
		if err != nil {
			log.Printf("[%s-Models] 获取 API Key 失败: channel=%s, error=%v", channelType, upstream.Name, err)
			failedChannels[selection.ChannelIndex] = true
			continue
		}

		req, err := http.NewRequestWithContext(c.Request.Context(), method, url, nil)
		if err != nil {
			log.Printf("[%s-Models] 创建请求失败: channel=%s, url=%s, error=%v", channelType, upstream.Name, url, err)
			failedChannels[selection.ChannelIndex] = true
			continue
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[%s-Models] 请求失败: channel=%s, key=%s, url=%s, error=%v",
				channelType, upstream.Name, utils.MaskAPIKey(apiKey), url, err)
			failedChannels[selection.ChannelIndex] = true
			continue
		}

		if resp.StatusCode == http.StatusOK {
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				log.Printf("[%s-Models] 读取响应失败: channel=%s, error=%v", channelType, upstream.Name, err)
				failedChannels[selection.ChannelIndex] = true
				continue
			}
			log.Printf("[%s-Models] 请求成功: method=%s, channel=%s, key=%s, url=%s, reason=%s",
				channelType, method, upstream.Name, utils.MaskAPIKey(apiKey), url, selection.Reason)
			return body, true
		}

		log.Printf("[%s-Models] 上游返回非 200: channel=%s, key=%s, status=%d, url=%s",
			channelType, upstream.Name, utils.MaskAPIKey(apiKey), resp.StatusCode, url)
		_ = resp.Body.Close()
		failedChannels[selection.ChannelIndex] = true
	}

	log.Printf("[%s-Models] 所有渠道均失败: method=%s, suffix=%s", channelType, method, suffix)
	return nil, false
}

func selectManualModelsChannel(cfgManager *config.ConfigManager, failedChannels map[int]bool, kind scheduler.ChannelKind, routePrefix string) (*scheduler.SelectionResult, error) {
	cfg := cfgManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case scheduler.ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case scheduler.ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case scheduler.ChannelKindChat:
		upstreams = cfg.ChatUpstream
	default:
		upstreams = cfg.Upstream
	}

	type candidate struct {
		index    int
		upstream config.UpstreamConfig
		priority int
	}

	candidates := make([]candidate, 0)
	for i, upstream := range upstreams {
		if failedChannels[i] || !upstream.UsesManualModels() {
			continue
		}
		if config.GetChannelStatus(&upstream) == "disabled" {
			continue
		}
		if routePrefix != "" {
			if upstream.RoutePrefix != routePrefix {
				continue
			}
		} else if upstream.RoutePrefix != "" {
			continue
		}
		candidates = append(candidates, candidate{
			index:    i,
			upstream: upstream,
			priority: config.GetChannelPriority(&upstream, i),
		})
	}

	if len(candidates) == 0 {
		return nil, errNoManualModelsChannel
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].priority < candidates[j].priority
	})

	selected := candidates[0]
	upstreamCopy := selected.upstream
	return &scheduler.SelectionResult{
		Upstream:     &upstreamCopy,
		ChannelIndex: selected.index,
		Reason:       "manual_models_fallback",
	}, nil
}

// buildModelsURL 构建 models 端点的 URL
func buildModelsURL(baseURL string) string {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	versionPattern := regexp.MustCompile(`/v\d+[a-z]*$`)
	hasVersionSuffix := versionPattern.MatchString(baseURL)

	endpoint := "/models"
	if !hasVersionSuffix && !skipVersionPrefix {
		endpoint = "/v1" + endpoint
	}

	return baseURL + endpoint
}
