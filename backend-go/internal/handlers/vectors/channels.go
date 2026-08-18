// Package vectors provides channel management for the OpenAI-compatible embeddings API.
package vectors

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	handlers "github.com/BenedictKing/ccx/internal/handlers"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/httpclient"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// endpointPathPattern matches a trailing version segment such as "/v1" or
// "/v1beta" so buildEndpointURL can skip appending another version prefix.
var endpointPathPattern = regexp.MustCompile(`/v\d+[a-z]*$`)

func GetUpstreams(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()
		upstreams := make([]gin.H, len(cfg.VectorsUpstream))
		for i, up := range cfg.VectorsUpstream {
			upstreams[i] = common.BuildChannelView(up, i)
		}
		c.JSON(http.StatusOK, gin.H{"channels": upstreams})
	}
}

func AddUpstream(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			config.UpstreamConfig
			Placement string `json:"placement"` // 故障转移位置：front（首位）| back（末尾，默认）
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := cfgManager.AddVectorsUpstream(req.UpstreamConfig, req.Placement); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, config.ErrUnsupportedServiceType) || errors.Is(err, config.ErrInvalidEmbeddingCapability) {
				status = http.StatusBadRequest
			} else if errors.Is(err, config.ErrDuplicateChannelName) {
				status = http.StatusConflict
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Vectors upstream added successfully"})
	}
}

func UpdateUpstream(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid upstream ID"})
			return
		}

		var updates config.UpstreamUpdate
		if err := c.ShouldBindJSON(&updates); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		oldName := ""
		if updates.Name != nil {
			cfg := cfgManager.GetConfig()
			if id >= 0 && id < len(cfg.VectorsUpstream) {
				oldName = cfg.VectorsUpstream[id].Name
			}
		}

		shouldResetMetrics, err := cfgManager.UpdateVectorsUpstream(id, updates)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, config.ErrDuplicateChannelName) {
				status = http.StatusConflict
			} else if errors.Is(err, config.ErrUnsupportedServiceType) || errors.Is(err, config.ErrInvalidEmbeddingCapability) || strings.Contains(err.Error(), "无效") {
				status = http.StatusBadRequest
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}

		if updates.Name != nil && oldName != "" && oldName != *updates.Name {
			if logStore := sch.GetChannelLogStore(scheduler.ChannelKindVectors); logStore != nil {
				logStore.RenameChannel(oldName, *updates.Name)
			}
		}
		if shouldResetMetrics {
			sch.ResetChannelMetrics(id, scheduler.ChannelKindVectors)
		}

		c.JSON(http.StatusOK, gin.H{"message": "Vectors upstream updated successfully"})
	}
}

func DeleteUpstream(cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid upstream ID"})
			return
		}

		removed, err := cfgManager.RemoveVectorsUpstream(id)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "无效") {
				status = http.StatusNotFound
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}

		channelScheduler.DeleteChannelLogs(removed, scheduler.ChannelKindVectors)
		channelScheduler.DeleteChannelMetrics(removed, scheduler.ChannelKindVectors)
		c.JSON(http.StatusOK, gin.H{"message": "Vectors upstream deleted successfully"})
	}
}

func AddApiKey(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid upstream ID"})
			return
		}

		var req struct {
			APIKey string `json:"apiKey"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		if err := cfgManager.AddVectorsAPIKey(id, req.APIKey); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "无效") {
				status = http.StatusNotFound
			} else if strings.Contains(err.Error(), "已存在") {
				status = http.StatusBadRequest
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "API key added", "success": true})
	}
}

func DeleteApiKey(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid upstream ID"})
			return
		}
		apiKey := c.Param("apiKey")
		if apiKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "API key is required"})
			return
		}

		if err := cfgManager.RemoveVectorsAPIKey(id, apiKey); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "无效") || strings.Contains(err.Error(), "不存在") {
				status = http.StatusNotFound
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "API key deleted"})
	}
}

func MoveApiKeyToTop(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid upstream ID"})
			return
		}
		if err := cfgManager.MoveVectorsAPIKeyToTop(id, c.Param("apiKey")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "API key moved to top"})
	}
}

func MoveApiKeyToBottom(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid upstream ID"})
			return
		}
		if err := cfgManager.MoveVectorsAPIKeyToBottom(id, c.Param("apiKey")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "API key moved to bottom"})
	}
}

func ReorderChannels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Order      []int `json:"order"`
			Priorities []int `json:"priorities"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}
		if err := cfgManager.ReorderVectorsUpstreams(req.Order, req.Priorities); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "Vectors channel priority updated"})
	}
}

func SetChannelStatus(cfgManager *config.ConfigManager) gin.HandlerFunc {
	adapter := handlers.ChannelStatusConfigManagerFunc(func(index int, status string) error {
		return cfgManager.SetVectorsChannelStatus(index, status)
	})
	return handlers.NamedChannelStatusHandler(adapter, "Vectors channel status updated")
}

func SetChannelPromotion(cfgManager *config.ConfigManager) gin.HandlerFunc {
	adapter := handlers.PromotionConfigManagerFunc(func(index int, duration time.Duration) error {
		return cfgManager.SetVectorsChannelPromotion(index, duration)
	})
	return handlers.NamedChannelPromotionHandler(adapter, "Invalid channel ID", "Invalid request body", "Vectors promotion cleared", "Vectors promotion set")
}

func PingChannel(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}
		cfg := cfgManager.GetConfig()
		if id < 0 || id >= len(cfg.VectorsUpstream) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
			return
		}
		c.JSON(http.StatusOK, common.PingSingleBaseURLUpstream(cfg.VectorsUpstream[id], buildPingRequest))
	}
}

func PingAllChannels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()
		c.JSON(http.StatusOK, gin.H{"channels": common.PingAllSingleBaseURLUpstreams(cfg.VectorsUpstream, buildPingRequest, true)["channels"]})
	}
}

func buildPingRequest(upstream config.UpstreamConfig, baseURL string) (*http.Request, error) {
	req, _ := http.NewRequest(http.MethodGet, buildModelsURL(baseURL), nil)
	if len(upstream.APIKeys) > 0 {
		utils.SetAuthenticationHeaderWithOverride(req.Header, upstream.APIKeys[0], upstream.AuthHeader)
	}
	req.Header.Set("Content-Type", "application/json")
	utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)
	return req, nil
}

func buildEndpointURL(baseURL, versionPrefix, endpoint string) string {
	trimmed := strings.TrimSpace(baseURL)
	skipVersionPrefix := strings.HasSuffix(trimmed, "#")
	if skipVersionPrefix {
		trimmed = strings.TrimSuffix(trimmed, "#")
	}
	trimmed = strings.TrimRight(trimmed, "/")

	if !skipVersionPrefix && !endpointPathPattern.MatchString(trimmed) {
		trimmed += versionPrefix
	}
	return trimmed + endpoint
}

func buildModelsURL(baseURL string) string {
	return buildEndpointURL(baseURL, "/v1", "/models")
}

type GetModelsRequest struct {
	Key                string            `json:"key"`
	BaseURL            string            `json:"baseUrl"`
	BaseURLs           []string          `json:"baseUrls"`
	ProxyURL           string            `json:"proxyUrl"`
	InsecureSkipVerify *bool             `json:"insecureSkipVerify"`
	CustomHeaders      map[string]string `json:"customHeaders"`
	AuthHeader         string            `json:"authHeader"`
	// LearnedClientFingerprint 上游存在客户端指纹校验（保活 L1 等无渠道上下文的
	// 调用方传入渠道学习到的标记），命中时请求带 Claude Code 客户端伪装头。
	LearnedClientFingerprint bool `json:"learnedClientFingerprint,omitempty"`
}

func GetChannelModels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}

		var req GetModelsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		var baseURL string
		channelName := "temporary channel"
		insecureSkipVerify := false
		proxyURL := req.ProxyURL
		authHeader := req.AuthHeader
		learnedClientFingerprint := req.LearnedClientFingerprint

		if strings.TrimSpace(req.BaseURL) != "" {
			if err := utils.ValidateBaseURL(req.BaseURL); err != nil {
				log.Printf("[Vectors-Models] SSRF guard blocked baseUrl: caller=%s error=%v", c.ClientIP(), err)
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid baseUrl: %v", err)})
				return
			}
			baseURL = req.BaseURL
			if req.InsecureSkipVerify != nil {
				insecureSkipVerify = *req.InsecureSkipVerify
			}
			log.Printf("[Vectors-Models] using temporary baseUrl: caller=%s", c.ClientIP())
		} else {
			cfg := cfgManager.GetConfig()
			if id < 0 || id >= len(cfg.VectorsUpstream) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
				return
			}
			channel := cfg.VectorsUpstream[id]
			baseURL = channel.BaseURL
			channelName = channel.Name
			insecureSkipVerify = channel.InsecureSkipVerify
			proxyURL = channel.ProxyURL
			authHeader = channel.AuthHeader
			learnedClientFingerprint = channel.LearnedClientFingerprint || req.LearnedClientFingerprint
			if req.InsecureSkipVerify != nil {
				insecureSkipVerify = *req.InsecureSkipVerify
			}
			if req.ProxyURL != "" {
				proxyURL = req.ProxyURL
			}
			if req.AuthHeader != "" {
				authHeader = req.AuthHeader
			}
		}

		apiKey := req.Key
		if apiKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No API key provided"})
			return
		}

		url := buildModelsURL(baseURL)
		client := httpclient.GetManager().GetStandardClient(10*time.Second, insecureSkipVerify, proxyURL)
		if req.BaseURL != "" && req.ProxyURL != "" {
			client = httpclient.GetManager().NewStandardClient(10*time.Second, insecureSkipVerify, proxyURL)
		}

		httpReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, url, nil)
		if err != nil {
			log.Printf("[Vectors-Models] create request failed: channel=%s, error=%s", channelName, sanitizeModelsProbeError(err, url))
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create request: %v", err)})
			return
		}
		utils.SetAuthenticationHeaderWithOverride(httpReq.Header, apiKey, authHeader)
		// 已学习客户端伪装标记的上游：请求带 Claude Code 探针头，
		// 避免被上游客户端指纹校验拒绝。
		if learnedClientFingerprint {
			utils.ApplyClaudeCodeProbeHeaders(httpReq.Header, "")
		}
		httpReq.Header.Set("Content-Type", "application/json")
		utils.ApplyCustomHeaders(httpReq.Header, req.CustomHeaders)

		log.Printf("[Vectors-Models] requesting models: channel=%s, key=%s", channelName, utils.MaskAPIKey(apiKey))
		resp, err := client.Do(httpReq)
		if err != nil {
			log.Printf("[Vectors-Models] request failed: channel=%s, key=%s, error=%s", channelName, utils.MaskAPIKey(apiKey), sanitizeModelsProbeError(err, url))
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("Failed to fetch models: %v", err)})
			return
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to read response: %v", err)})
			return
		}
		if resp.StatusCode == http.StatusUnauthorized {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Upstream API key is invalid", "statusCode": http.StatusUnauthorized, "details": string(body)})
			return
		}
		c.Data(resp.StatusCode, "application/json", body)
	}
}

func sanitizeModelsProbeError(err error, requestURL string) string {
	if err == nil {
		return ""
	}
	var urlErr *neturl.Error
	if errors.As(err, &urlErr) {
		if inner := sanitizeDiagnosticError(urlErr.Err); inner != "" {
			return urlErr.Op + ": " + inner
		}
		return urlErr.Op
	}
	msg := sanitizeDiagnosticError(err)
	if requestURL != "" {
		msg = strings.ReplaceAll(msg, requestURL, "[redacted-url]")
	}
	return msg
}

func UpdateModelMapping(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}

		var req struct {
			SourcePattern string `json:"source_pattern"`
			TargetModel   string `json:"target_model"`
			Reasoning     string `json:"reasoning"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}
		if req.SourcePattern == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "source_pattern is required"})
			return
		}
		if req.TargetModel == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "target_model is required"})
			return
		}
		if err := cfgManager.UpdateVectorsModelMapping(id, req.SourcePattern, req.TargetModel, req.Reasoning); err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "无效") || strings.Contains(err.Error(), "不存在") {
				status = http.StatusNotFound
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		cfg := cfgManager.GetConfig()
		c.JSON(http.StatusOK, gin.H{"message": "Model mapping updated", "upstream": cfg.VectorsUpstream[id]})
	}
}
