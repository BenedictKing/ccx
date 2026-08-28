// Package messages 提供 Claude Messages API 的渠道管理
package messages

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/copilot"
	handlers "github.com/BenedictKing/ccx/internal/handlers"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// GetUpstreams 获取上游列表 (兼容前端 channels 字段名)
func GetUpstreams(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()

		upstreams := make([]gin.H, len(cfg.Upstream))
		for i, up := range cfg.Upstream {
			upstreams[i] = common.BuildChannelView(up, i)
		}

		c.JSON(200, gin.H{
			"channels": upstreams,
		})
	}
}

// AddUpstream 添加上游
func AddUpstream(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			config.UpstreamConfig
			Placement string `json:"placement"` // 故障转移位置：front（首位）| back（末尾，默认）
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid request body"})
			return
		}

		if err := cfgManager.AddUpstream(req.UpstreamConfig, req.Placement); err != nil {
			c.JSON(500, gin.H{"error": "Failed to save config"})
			return
		}

		c.JSON(200, gin.H{
			"message":  "上游已添加",
			"upstream": req.UpstreamConfig,
		})
	}
}

// UpdateUpstream 更新上游
func UpdateUpstream(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid upstream ID"})
			return
		}

		var updates config.UpstreamUpdate
		if err := c.ShouldBindJSON(&updates); err != nil {
			c.JSON(400, gin.H{"error": "Invalid request body"})
			return
		}

		oldName := ""
		{
			cfg := cfgManager.GetConfig()
			if id >= 0 && id < len(cfg.Upstream) {
				oldName = cfg.Upstream[id].Name
			}
		}

		shouldResetMetrics, err := cfgManager.UpdateUpstream(id, updates)
		if err != nil {
			if strings.Contains(err.Error(), "无效的上游索引") {
				c.JSON(404, gin.H{"error": "Upstream not found"})
			} else {
				c.JSON(500, gin.H{"error": "Failed to save config"})
			}
			return
		}

		// 名称由后端按首个 baseURL 派生：baseURL 顺序或首地址变化可能触发改名，需迁移日志归属
		newName := ""
		{
			cfg := cfgManager.GetConfig()
			if id >= 0 && id < len(cfg.Upstream) {
				newName = cfg.Upstream[id].Name
			}
		}
		if oldName != "" && newName != "" && oldName != newName {
			if logStore := sch.GetChannelLogStore(scheduler.ChannelKindMessages); logStore != nil {
				logStore.RenameChannel(oldName, newName)
			}
		}

		if shouldResetMetrics {
			sch.ResetChannelMetrics(id, scheduler.ChannelKindMessages)
		}

		cfg := cfgManager.GetConfig()
		c.JSON(200, gin.H{
			"message":  "上游已更新",
			"upstream": cfg.Upstream[id],
		})
	}
}

// DeleteUpstream 删除上游
func DeleteUpstream(cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid upstream ID"})
			return
		}

		removed, err := cfgManager.RemoveUpstream(id)
		if err != nil {
			if strings.Contains(err.Error(), "无效的上游索引") {
				c.JSON(404, gin.H{"error": "Upstream not found"})
			} else {
				c.JSON(500, gin.H{"error": "Failed to save config"})
			}
			return
		}

		channelScheduler.DeleteChannelLogs(removed, scheduler.ChannelKindMessages)
		channelScheduler.DeleteChannelMetrics(removed, scheduler.ChannelKindMessages)

		c.JSON(200, gin.H{
			"message": "上游已删除",
			"removed": removed,
		})
	}
}

// AddApiKey 添加 API 密钥
func AddApiKey(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid upstream ID"})
			return
		}

		var req struct {
			APIKey string `json:"apiKey"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid request body"})
			return
		}

		if err := cfgManager.AddAPIKey(id, req.APIKey); err != nil {
			if strings.Contains(err.Error(), "无效的上游索引") {
				c.JSON(404, gin.H{"error": "Upstream not found"})
			} else if strings.Contains(err.Error(), "API密钥已存在") {
				c.JSON(400, gin.H{"error": "API密钥已存在"})
			} else {
				c.JSON(500, gin.H{"error": "Failed to save config"})
			}
			return
		}

		c.JSON(200, gin.H{
			"message": "API密钥已添加",
			"success": true,
		})
	}
}

// DeleteApiKey 删除 API 密钥
func DeleteApiKey(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid upstream ID"})
			return
		}

		apiKey := c.Param("apiKey")
		if apiKey == "" {
			c.JSON(400, gin.H{"error": "API key is required"})
			return
		}

		if err := cfgManager.RemoveAPIKey(id, apiKey); err != nil {
			if strings.Contains(err.Error(), "无效的上游索引") {
				c.JSON(404, gin.H{"error": "Upstream not found"})
			} else if strings.Contains(err.Error(), "API密钥不存在") {
				c.JSON(404, gin.H{"error": "API key not found"})
			} else {
				c.JSON(500, gin.H{"error": "Failed to save config"})
			}
			return
		}

		c.JSON(200, gin.H{
			"message": "API密钥已删除",
		})
	}
}

// MoveApiKeyToTop 将 API 密钥移到顶部
func MoveApiKeyToTop(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid upstream ID"})
			return
		}

		apiKey := c.Param("apiKey")
		if apiKey == "" {
			c.JSON(400, gin.H{"error": "API key is required"})
			return
		}

		if err := cfgManager.MoveAPIKeyToTop(id, apiKey); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{"message": "API密钥已移到顶部"})
	}
}

// MoveApiKeyToBottom 将 API 密钥移到底部
func MoveApiKeyToBottom(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid upstream ID"})
			return
		}

		apiKey := c.Param("apiKey")
		if apiKey == "" {
			c.JSON(400, gin.H{"error": "API key is required"})
			return
		}

		if err := cfgManager.MoveAPIKeyToBottom(id, apiKey); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{"message": "API密钥已移到底部"})
	}
}

// ReorderChannels 重新排序渠道
func ReorderChannels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Order      []int `json:"order"`
			Priorities []int `json:"priorities"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid request body"})
			return
		}

		if err := cfgManager.ReorderUpstreams(req.Order, req.Priorities); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{"message": "渠道顺序已更新"})
	}
}

// SetChannelStatus 设置渠道状态
func SetChannelStatus(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return handlers.NamedChannelStatusHandler(cfgManager, "渠道状态已更新")
}

// SetChannelPromotion 设置渠道促销期
// 促销期内的渠道会被优先选择，忽略 trace 亲和性
func SetChannelPromotion(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return handlers.SetChannelPromotion(cfgManager)
}

// PingChannel Ping单个渠道
func PingChannel(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}

		cfg := cfgManager.GetConfig()
		if id < 0 || id >= len(cfg.Upstream) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
			return
		}

		c.JSON(http.StatusOK, common.PingMultiBaseURLUpstream(cfg.Upstream[id], buildPingRequest))
	}
}

// PingAllChannels Ping所有渠道
func PingAllChannels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()
		c.JSON(http.StatusOK, common.PingAllMultiBaseURLUpstreams(cfg.Upstream, buildPingRequest))
	}
}

func buildPingRequest(_ config.UpstreamConfig, baseURL string) (*http.Request, error) {
	return http.NewRequest(http.MethodHead, baseURL, nil)
}

// GetModelsRequest 获取模型列表的请求体
type GetModelsRequest struct {
	Key         string   `json:"key"`
	BaseURL     string   `json:"baseUrl"`
	BaseURLs    []string `json:"baseUrls"`
	ServiceType string   `json:"serviceType"`
	ProxyURL    string   `json:"proxyUrl"`
	// ProxyPreferDirect 直连优先（nil=未指定，回退渠道配置）；仅在配置了代理时有意义
	ProxyPreferDirect  *bool             `json:"proxyPreferDirect"`
	InsecureSkipVerify *bool             `json:"insecureSkipVerify"`
	CustomHeaders      map[string]string `json:"customHeaders"`
	AuthHeader         string            `json:"authHeader"`
	// LearnedClientFingerprint 上游存在客户端指纹校验（保活 L1 等无渠道上下文的
	// 调用方传入渠道学习到的标记），命中时请求带 Claude Code 客户端伪装头。
	LearnedClientFingerprint bool `json:"learnedClientFingerprint,omitempty"`
}

// GetChannelModels 获取指定渠道的模型列表（支持临时 Key）
func GetChannelModels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 解析渠道 ID
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}

		// 2. 从请求体读取参数
		var req GetModelsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		// 3. 获取 baseUrl（优先使用请求体中的临时 baseUrl，用于新增渠道场景）
		var baseURL string
		var channelName string
		var channel *config.UpstreamConfig
		var serviceType string
		var insecureSkipVerify bool
		var proxyURL string
		var proxyPreferDirect bool
		var authHeader string
		var learnedClientFingerprint bool

		if req.BaseURL != "" {
			// 新增模式：使用临时 baseUrl
			// SSRF 防护：验证用户提供的 baseURL
			if err := utils.ValidateBaseURL(req.BaseURL); err != nil {
				log.Printf("[Messages-Models] SSRF 防护拦截: %v", err)
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("无效的 baseUrl: %v", err)})
				return
			}
			baseURL = req.BaseURL
			channelName = "临时渠道"
			serviceType = req.ServiceType
			insecureSkipVerify = false
			proxyURL = ""
			if req.InsecureSkipVerify != nil {
				insecureSkipVerify = *req.InsecureSkipVerify
			}
			if req.ProxyURL != "" {
				proxyURL = req.ProxyURL
			}
			if req.ProxyPreferDirect != nil {
				proxyPreferDirect = *req.ProxyPreferDirect
			}
			authHeader = req.AuthHeader
			learnedClientFingerprint = req.LearnedClientFingerprint
			log.Printf("[Messages-Models] 使用临时 baseUrl: %s", baseURL)
		} else {
			// 编辑模式：从配置中读取渠道信息
			cfg := cfgManager.GetConfig()
			if id < 0 || id >= len(cfg.Upstream) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
				return
			}

			channel = &cfg.Upstream[id]
			baseURL = channel.BaseURL
			channelName = channel.Name
			serviceType = channel.ServiceType
			insecureSkipVerify = channel.InsecureSkipVerify
			proxyURL = channel.ProxyURL
			proxyPreferDirect = channel.ProxyPreferDirect
			authHeader = channel.AuthHeader
			learnedClientFingerprint = channel.LearnedClientFingerprint || req.LearnedClientFingerprint
			if req.BaseURL != "" {
				if err := utils.ValidateBaseURL(req.BaseURL); err != nil {
					log.Printf("[Messages-Models] SSRF 防护拦截: %v", err)
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("无效的 baseUrl: %v", err)})
					return
				}
				baseURL = req.BaseURL
			}
			if req.InsecureSkipVerify != nil {
				insecureSkipVerify = *req.InsecureSkipVerify
			}
			if req.ProxyURL != "" {
				proxyURL = req.ProxyURL
			}
			if req.ProxyPreferDirect != nil {
				proxyPreferDirect = *req.ProxyPreferDirect
			}
			if req.AuthHeader != "" {
				authHeader = req.AuthHeader
			}
			if req.ServiceType != "" {
				serviceType = req.ServiceType
			}
		}

		// 4. 验证 API Key
		apiKey := req.Key
		if apiKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No API key provided"})
			return
		}

		log.Printf("[Messages-Models] 请求模型列表: channel=%s, key=%s", channelName, utils.MaskAPIKey(apiKey))

		// 火山套餐渠道的数据面 /models 不反映套餐清单（/api/coding 返回全量目录、
		// /api/plan 无此接口），改走管控面套餐模型接口。
		if volcengineModels, handled, err := autopilot.FetchVolcenginePlanModelsForChannel(c.Request.Context(), cfgManager, channel, baseURL, apiKey); handled {
			if err != nil {
				log.Printf("[Messages-Models] 火山套餐模型查询失败: channel=%s, key=%s, error=%v", channelName, utils.MaskAPIKey(apiKey), err)
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "statusCode": 502})
				return
			}
			data := make([]gin.H, 0, len(volcengineModels))
			for _, model := range volcengineModels {
				data = append(data, gin.H{"id": model})
			}
			c.JSON(http.StatusOK, gin.H{"data": data, "statusCode": http.StatusOK})
			return
		}

		// 5. 使用候选 URL 列表发起请求（messages 渠道自动尝试兼容路径）
		candidateURLs := buildClaudeCompatibleModelsURLs(baseURL)
		var copilotRuntimeToken string
		if serviceType == "copilot" {
			var copilotBaseURL string
			copilotRuntimeToken, copilotBaseURL, err = copilot.ResolveTokenWithProxy(c.Request.Context(), apiKey, proxyURL)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("Failed to exchange Copilot token: %v", err)})
				return
			}
			targetBaseURL := strings.TrimSuffix(strings.TrimSuffix(baseURL, "#"), "/")
			if copilotBaseURL != "" {
				targetBaseURL = strings.TrimRight(copilotBaseURL, "/")
			}
			candidateURLs = []string{targetBaseURL + "/models"}
		}

		clientOpts := httpclient.ClientOptions{
			Timeout:           10 * time.Second,
			Insecure:          insecureSkipVerify,
			ProxyURL:          proxyURL,
			ProxyPreferDirect: proxyPreferDirect,
		}
		client := httpclient.GetManager().GetClient(clientOpts)
		if req.BaseURL != "" && req.ProxyURL != "" {
			// 新增模式下的临时代理参数高变，不进缓存
			client = httpclient.GetManager().NewClient(clientOpts)
		}

		var lastStatusCode int
		var lastBody []byte
		// Anthropic 风格端点（claude 系）或已学习客户端伪装标记的上游：
		// 请求带 Claude Code 探针头，避免被上游客户端指纹校验拒绝。
		useProbeHeaders := serviceType != "copilot" &&
			(strings.EqualFold(strings.TrimSpace(serviceType), "claude") || learnedClientFingerprint)
		for _, candidateURL := range candidateURLs {
			httpReq, err := http.NewRequestWithContext(c.Request.Context(), "GET", candidateURL, nil)
			if err != nil {
				log.Printf("[Messages-Models] 创建请求失败: channel=%s, url=%s, error=%v", channelName, candidateURL, err)
				continue
			}
			if serviceType == "copilot" {
				copilot.ApplyRuntimeHeaders(httpReq.Header, copilotRuntimeToken)
			} else {
				utils.SetAuthenticationHeaderWithOverride(httpReq.Header, apiKey, authHeader)
				if useProbeHeaders {
					utils.ApplyClaudeCodeProbeHeaders(httpReq.Header, "")
				}
			}
			httpReq.Header.Set("Content-Type", "application/json")
			if serviceType == "copilot" {
				utils.ApplyCustomHeadersProtected(httpReq.Header, req.CustomHeaders, utils.CopilotProtectedHeaders)
			} else {
				utils.ApplyCustomHeaders(httpReq.Header, req.CustomHeaders)
			}

			resp, err := client.Do(httpReq)
			if err != nil {
				log.Printf("[Messages-Models] 请求失败: channel=%s, key=%s, url=%s, error=%v",
					channelName, utils.MaskAPIKey(apiKey), candidateURL, err)
				continue
			}

			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				log.Printf("[Messages-Models] 读取响应失败: channel=%s, error=%v", channelName, err)
				continue
			}

			lastStatusCode = resp.StatusCode
			lastBody = body

			if resp.StatusCode == http.StatusOK {
				log.Printf("[Messages-Models] 上游响应: channel=%s, key=%s, status=%d, url=%s",
					channelName, utils.MaskAPIKey(apiKey), resp.StatusCode, candidateURL)
				// 透传上游响应体时注入真实状态码，供前端展示（不再硬编码 200）。
				c.Data(resp.StatusCode, "application/json", common.InjectUpstreamStatusCode(body, resp.StatusCode))
				return
			}

			// 401/403 认证失败不继续尝试其他候选 URL
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				log.Printf("[Messages-Models] 上游认证失败: channel=%s, key=%s, status=%d, url=%s",
					channelName, utils.MaskAPIKey(apiKey), resp.StatusCode, candidateURL)
				break
			}

			log.Printf("[Messages-Models] 上游返回非 200: channel=%s, key=%s, status=%d, url=%s",
				channelName, utils.MaskAPIKey(apiKey), resp.StatusCode, candidateURL)
		}

		// 所有候选 URL 均失败，返回最后一次上游响应
		if lastStatusCode == http.StatusUnauthorized {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":      "上游 API Key 无效",
				"statusCode": 401,
				"details":    string(lastBody),
			})
			return
		}
		if lastBody != nil {
			c.Data(lastStatusCode, "application/json", lastBody)
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to fetch models from all candidate URLs"})
	}
}

// UpdateModelMapping 更新渠道的单个模型映射
func UpdateModelMapping(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		var req struct {
			SourcePattern string `json:"source_pattern"`
			TargetModel   string `json:"target_model"`
			Reasoning     string `json:"reasoning"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid request body"})
			return
		}

		if req.SourcePattern == "" {
			c.JSON(400, gin.H{"error": "source_pattern is required"})
			return
		}
		if req.TargetModel == "" {
			c.JSON(400, gin.H{"error": "target_model is required"})
			return
		}

		if err := cfgManager.UpdateModelMapping(id, req.SourcePattern, req.TargetModel, req.Reasoning); err != nil {
			if strings.Contains(err.Error(), "无效的上游索引") {
				c.JSON(404, gin.H{"error": "Channel not found"})
			} else if strings.Contains(err.Error(), "不存在") {
				c.JSON(404, gin.H{"error": err.Error()})
			} else {
				c.JSON(400, gin.H{"error": err.Error()})
			}
			return
		}

		cfg := cfgManager.GetConfig()
		c.JSON(200, gin.H{
			"message":  "模型映射已更新",
			"upstream": cfg.Upstream[id],
		})
	}
}
