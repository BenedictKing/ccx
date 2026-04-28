// Package images ?? Images API ?????
package images

import (
	"fmt"
	"io"
	"log"
	"net/http"
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

// GetUpstreams ?? Images ????
func GetUpstreams(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()

		upstreams := make([]gin.H, len(cfg.ImagesUpstream))
		for i, up := range cfg.ImagesUpstream {
			upstreams[i] = common.BuildChannelView(up, i)
		}

		c.JSON(200, gin.H{
			"channels": upstreams,
		})
	}
}

// AddUpstream ?? Images ??
func AddUpstream(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var upstream config.UpstreamConfig
		if err := c.ShouldBindJSON(&upstream); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		if err := cfgManager.AddImagesUpstream(upstream); err != nil {
			if strings.Contains(err.Error(), "openai serviceType") {
				c.JSON(400, gin.H{"error": err.Error()})
			} else {
				c.JSON(500, gin.H{"error": err.Error()})
			}
			return
		}

		c.JSON(200, gin.H{"message": "Images upstream added successfully"})
	}
}

// UpdateUpstream ?? Images ??
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
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		shouldResetMetrics, err := cfgManager.UpdateImagesUpstream(id, updates)
		if err != nil {
			if strings.Contains(err.Error(), "openai serviceType") {
				c.JSON(400, gin.H{"error": err.Error()})
			} else {
				c.JSON(500, gin.H{"error": err.Error()})
			}
			return
		}

		// ? key ?????????
		if shouldResetMetrics {
			sch.ResetChannelMetrics(id, scheduler.ChannelKindImages)
		}

		c.JSON(200, gin.H{"message": "Images upstream updated successfully"})
	}
}

// DeleteUpstream ?? Images ??
func DeleteUpstream(cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid upstream ID"})
			return
		}

		removed, err := cfgManager.RemoveImagesUpstream(id)
		if err != nil {
			if strings.Contains(err.Error(), "???") {
				c.JSON(404, gin.H{"error": "Upstream not found"})
			} else {
				c.JSON(500, gin.H{"error": err.Error()})
			}
			return
		}

		channelScheduler.GetChannelLogStore(scheduler.ChannelKindImages).RemoveAndShift(id)
		channelScheduler.DeleteChannelMetrics(removed, scheduler.ChannelKindImages)

		c.JSON(200, gin.H{"message": "Images upstream deleted successfully"})
	}
}

// AddApiKey ?? Images ?? API ??
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

		if err := cfgManager.AddImagesAPIKey(id, req.APIKey); err != nil {
			if strings.Contains(err.Error(), "???????") {
				c.JSON(404, gin.H{"error": "Upstream not found"})
			} else if strings.Contains(err.Error(), "API?????") {
				c.JSON(400, gin.H{"error": "API?????"})
			} else {
				c.JSON(500, gin.H{"error": "Failed to save config"})
			}
			return
		}

		c.JSON(200, gin.H{
			"message": "API?????",
			"success": true,
		})
	}
}

// DeleteApiKey ?? Images ?? API ??
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

		if err := cfgManager.RemoveImagesAPIKey(id, apiKey); err != nil {
			if strings.Contains(err.Error(), "???????") {
				c.JSON(404, gin.H{"error": "Upstream not found"})
			} else if strings.Contains(err.Error(), "API?????") {
				c.JSON(404, gin.H{"error": "API key not found"})
			} else {
				c.JSON(500, gin.H{"error": "Failed to save config"})
			}
			return
		}

		c.JSON(200, gin.H{
			"message": "API?????",
		})
	}
}

// MoveApiKeyToTop ? Images ?? API ???????
func MoveApiKeyToTop(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid upstream ID"})
			return
		}
		apiKey := c.Param("apiKey")

		if err := cfgManager.MoveImagesAPIKeyToTop(id, apiKey); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"message": "API?????"})
	}
}

// MoveApiKeyToBottom ? Images ?? API ???????
func MoveApiKeyToBottom(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid upstream ID"})
			return
		}
		apiKey := c.Param("apiKey")

		if err := cfgManager.MoveImagesAPIKeyToBottom(id, apiKey); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"message": "API?????"})
	}
}

// ReorderChannels ???? Images ?????
func ReorderChannels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Order []int `json:"order"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid request body"})
			return
		}

		if err := cfgManager.ReorderImagesUpstreams(req.Order); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{
			"success": true,
			"message": "Images ????????",
		})
	}
}

// SetChannelStatus ?? Images ????
func SetChannelStatus(cfgManager *config.ConfigManager) gin.HandlerFunc {
	adapter := handlers.ChannelStatusConfigManagerFunc(func(index int, status string) error {
		return cfgManager.SetImagesChannelStatus(index, status)
	})
	return handlers.NamedChannelStatusHandler(adapter, "Images ???????")
}

// SetChannelPromotion ?? Images ?????
func SetChannelPromotion(cfgManager *config.ConfigManager) gin.HandlerFunc {
	adapter := handlers.PromotionConfigManagerFunc(func(index int, duration time.Duration) error {
		return cfgManager.SetImagesChannelPromotion(index, duration)
	})
	return handlers.NamedChannelPromotionHandler(adapter, "Invalid channel ID", "Invalid request body", "Images ????????", "Images ????????")
}

// PingChannel ?? Images ?????
func PingChannel(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		cfg := cfgManager.GetConfig()
		if id < 0 || id >= len(cfg.ImagesUpstream) {
			c.JSON(404, gin.H{"error": "Channel not found"})
			return
		}

		c.JSON(200, common.PingSingleBaseURLUpstream(cfg.ImagesUpstream[id], buildPingRequest))
	}
}

// PingAllChannels ???? Images ?????
func PingAllChannels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()
		c.JSON(200, gin.H{"channels": common.PingAllSingleBaseURLUpstreams(cfg.ImagesUpstream, buildPingRequest, true)["channels"]})
	}
}

func buildPingRequest(upstream config.UpstreamConfig, baseURL string) (*http.Request, error) {
	var req *http.Request
	switch upstream.ServiceType {
	case "claude":
		req, _ = http.NewRequest(http.MethodOptions, buildMessagesURL(baseURL), nil)
		if len(upstream.APIKeys) > 0 {
			utils.SetAuthenticationHeader(req.Header, upstream.APIKeys[0])
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	default:
		req, _ = http.NewRequest(http.MethodGet, buildModelsURL(baseURL), nil)
		if len(upstream.APIKeys) > 0 {
			utils.SetAuthenticationHeader(req.Header, upstream.APIKeys[0])
		}
	}
	return req, nil
}

// buildEndpointURL ?????????? URL
func buildEndpointURL(baseURL, versionPrefix, endpoint string) string {
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

func buildMessagesURL(baseURL string) string {
	return buildEndpointURL(baseURL, "/v1", "/messages")
}

// buildModelsURL ?? models ??? URL
func buildModelsURL(baseURL string) string {
	return buildEndpointURL(baseURL, "/v1", "/models")
}

// GetModelsRequest ??????????
type GetModelsRequest struct {
	Key                string            `json:"key"`
	BaseURL            string            `json:"baseUrl"`
	BaseURLs           []string          `json:"baseUrls"`
	ProxyURL           string            `json:"proxyUrl"`
	InsecureSkipVerify *bool             `json:"insecureSkipVerify"`
	CustomHeaders      map[string]string `json:"customHeaders"`
}

// GetChannelModels ???????????????? Key?
func GetChannelModels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. ???? ID
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid channel ID"})
			return
		}

		// 2. ????????
		var req GetModelsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		// 3. ?? baseUrl???????????? baseUrl??????????
		var baseURL string
		var channelName string
		var insecureSkipVerify bool
		var proxyURL string

		if req.BaseURL != "" {
			// ????????? baseUrl
			// SSRF ?????????? baseURL
			if err := utils.ValidateBaseURL(req.BaseURL); err != nil {
				log.Printf("[Images-Models] SSRF ????: %v", err)
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("??? baseUrl: %v", err)})
				return
			}
			baseURL = req.BaseURL
			channelName = "????"
			insecureSkipVerify = false
			proxyURL = ""
			if req.InsecureSkipVerify != nil {
				insecureSkipVerify = *req.InsecureSkipVerify
			}
			if req.ProxyURL != "" {
				proxyURL = req.ProxyURL
			}
			log.Printf("[Images-Models] ???? baseUrl: %s", baseURL)
		} else {
			// ???????????????
			cfg := cfgManager.GetConfig()
			if id < 0 || id >= len(cfg.ImagesUpstream) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
				return
			}

			channel := cfg.ImagesUpstream[id]
			baseURL = channel.BaseURL
			channelName = channel.Name
			insecureSkipVerify = channel.InsecureSkipVerify
			proxyURL = channel.ProxyURL
			if req.BaseURL != "" {
				if err := utils.ValidateBaseURL(req.BaseURL); err != nil {
					log.Printf("[Images-Models] SSRF ????: %v", err)
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("??? baseUrl: %v", err)})
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
		}

		// 4. ?? API Key
		apiKey := req.Key
		if apiKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No API key provided"})
			return
		}

		log.Printf("[Images-Models] ??????: channel=%s, key=%s", channelName, utils.MaskAPIKey(apiKey))

		// 5. ????
		url := buildModelsURL(baseURL)
		client := httpclient.GetManager().GetStandardClient(10*time.Second, insecureSkipVerify, proxyURL)
		httpReq, err := http.NewRequestWithContext(c.Request.Context(), "GET", url, nil)
		if err != nil {
			log.Printf("[Images-Models] ??????: channel=%s, url=%s, error=%v", channelName, url, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create request: %v", err)})
			return
		}
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		utils.ApplyCustomHeaders(httpReq.Header, req.CustomHeaders)

		resp, err := client.Do(httpReq)
		if err != nil {
			log.Printf("[Images-Models] ????: channel=%s, key=%s, url=%s, error=%v",
				channelName, utils.MaskAPIKey(apiKey), url, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("Failed to fetch models: %v", err)})
			return
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("[Images-Models] ??????: channel=%s, error=%v", channelName, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to read response: %v", err)})
			return
		}

		log.Printf("[Images-Models] ????: channel=%s, key=%s, status=%d, url=%s",
			channelName, utils.MaskAPIKey(apiKey), resp.StatusCode, url)
		// ???? 401 ???????????? API ????
		if resp.StatusCode == 401 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":      "?? API Key ??",
				"statusCode": 401,
				"details":    string(body),
			})
			return
		}

		c.Data(resp.StatusCode, "application/json", body)
	}
}
