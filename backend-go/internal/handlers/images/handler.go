package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

const (
	operationGenerations = "generations"
	operationEdits       = "edits"
	operationVariations  = "variations"
)

// Handler Images API ?????
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		operation := extractOperation(c.Request.URL.Path)
		if operation == "" {
			imagesErrorResponse(c, http.StatusNotFound, "Images endpoint not found", "invalid_request_error", "endpoint_not_found")
			return
		}

		startTime := time.Now()
		bodyBytes, err := common.ReadRequestBody(c, envCfg.MaxRequestBodySize)
		if err != nil {
			return
		}
		c.Set("requestBodyBytes", bodyBytes)

		contentType := c.GetHeader("Content-Type")
		model, isStream, ok := parseImagesRequest(c, bodyBytes, contentType, operation)
		if !ok {
			return
		}

		userID := utils.ExtractUnifiedSessionID(c, bodyBytes)
		logImagesOriginalRequest(c, bodyBytes, contentType, envCfg)

		if channelScheduler.IsMultiChannelMode(scheduler.ChannelKindImages) {
			handleMultiChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, model, userID, startTime, operation, contentType, isStream)
		} else {
			handleSingleChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, model, startTime, operation, contentType, isStream)
		}
	})
}

func logImagesOriginalRequest(c *gin.Context, bodyBytes []byte, contentType string, envCfg *config.EnvConfig) {
	if isMultipartContentType(contentType) {
		if envCfg.EnableRequestLogs {
			log.Printf("[Request-Receive] 收到Images请求: %s %s", c.Request.Method, c.Request.URL.Path)
			if envCfg.IsDevelopment() {
				log.Printf("[Images-Request] multipart request body omitted from logs")
			}
		}
		return
	}

	common.LogOriginalRequest(c, bodyBytes, envCfg, "Images")
}

func extractOperation(path string) string {
	if strings.Contains(path, "/images/generations") {
		return operationGenerations
	}
	if strings.Contains(path, "/images/edits") {
		return operationEdits
	}
	if strings.Contains(path, "/images/variations") {
		return operationVariations
	}
	return ""
}

func parseImagesRequest(c *gin.Context, bodyBytes []byte, contentType string, operation string) (string, bool, bool) {
	if operation != operationGenerations {
		if isJSONContentType(contentType) {
			var reqMap map[string]interface{}
			if len(bodyBytes) > 0 {
				if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
					imagesErrorResponse(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err), "invalid_request_error", "invalid_json")
					return "", false, false
				}
			}
			model, _ := reqMap["model"].(string)
			if strings.TrimSpace(model) == "" {
				model = "gpt-image-2"
			}
			return model, isImagesStreamRequest(c, bodyBytes, contentType), true
		}
		if isMultipartContentType(contentType) {
			if err := validateMultipartBody(bodyBytes, contentType); err != nil {
				imagesErrorResponse(c, http.StatusBadRequest, fmt.Sprintf("Invalid multipart body: %v", err), "invalid_request_error", "invalid_multipart")
				return "", false, false
			}
		}
		model := extractImagesModel(bodyBytes, contentType)
		if strings.TrimSpace(model) == "" {
			model = "gpt-image-2"
		}
		return model, isImagesStreamRequest(c, bodyBytes, contentType), true
	}

	var reqMap map[string]interface{}
	if len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
			imagesErrorResponse(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err), "invalid_request_error", "invalid_json")
			return "", false, false
		}
	}

	model, _ := reqMap["model"].(string)
	if model == "" {
		imagesErrorResponse(c, http.StatusBadRequest, "model is required", "invalid_request_error", "missing_parameter")
		return "", false, false
	}
	prompt, _ := reqMap["prompt"].(string)
	if prompt == "" {
		imagesErrorResponse(c, http.StatusBadRequest, "prompt is required", "invalid_request_error", "missing_parameter")
		return "", false, false
	}

	return model, isImagesStreamRequest(c, bodyBytes, contentType), true
}

func handleMultiChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	model string,
	userID string,
	startTime time.Time,
	operation string,
	contentType string,
	isStream bool,
) {
	metricsManager := channelScheduler.GetImagesMetricsManager()
	common.HandleMultiChannelFailover(
		c,
		envCfg,
		channelScheduler,
		scheduler.ChannelKindImages,
		"Images",
		userID,
		model,
		func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			upstream := selection.Upstream
			channelIndex := selection.ChannelIndex
			if upstream == nil {
				return common.MultiChannelAttemptResult{}
			}

			baseURLs := upstream.GetAllBaseURLs()
			sortedURLResults := channelScheduler.GetSortedURLsForChannel(scheduler.ChannelKindImages, channelIndex, baseURLs)
			handled, successKey, successBaseURLIdx, failoverErr, usage, lastErr := common.TryUpstreamWithAllKeys(
				c,
				envCfg,
				cfgManager,
				channelScheduler,
				scheduler.ChannelKindImages,
				"Images",
				metricsManager,
				upstream,
				sortedURLResults,
				bodyBytes,
				isStream,
				func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
					return cfgManager.GetNextImagesAPIKey(upstream, failedKeys)
				},
				func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
					return buildOperationRequest(c, upstreamCopy, upstreamCopy.BaseURL, apiKey, bodyBytes, model, operation, contentType)
				},
				func(apiKey string) {
					_ = cfgManager.DeprioritizeAPIKey(apiKey)
				},
				func(url string) {
					channelScheduler.MarkURLFailure(scheduler.ChannelKindImages, channelIndex, url)
				},
				func(url string) {
					channelScheduler.MarkURLSuccess(scheduler.ChannelKindImages, channelIndex, url)
				},
				func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
					return handleSuccess(c, resp, startTime, isStream)
				},
				model,
				selection.ChannelIndex,
				channelScheduler.GetChannelLogStore(scheduler.ChannelKindImages),
			)

			return common.MultiChannelAttemptResult{
				Handled:           handled,
				Attempted:         true,
				SuccessKey:        successKey,
				SuccessBaseURLIdx: successBaseURLIdx,
				FailoverError:     failoverErr,
				Usage:             usage,
				LastError:         lastErr,
			}
		},
		nil,
		func(ctx *gin.Context, failoverErr *common.FailoverError, lastError error) {
			handleAllChannelsFailed(ctx, failoverErr, lastError)
		},
	)
}

func handleSingleChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	model string,
	startTime time.Time,
	operation string,
	contentType string,
	isStream bool,
) {
	upstream, channelIndex, err := cfgManager.GetCurrentImagesUpstreamWithIndex()
	if err != nil {
		imagesErrorResponse(c, http.StatusServiceUnavailable, "No Images upstream configured", "service_unavailable", "service_unavailable")
		return
	}
	if len(upstream.APIKeys) == 0 {
		imagesErrorResponse(c, http.StatusServiceUnavailable, fmt.Sprintf("No API keys configured for upstream \"%s\"", upstream.Name), "service_unavailable", "service_unavailable")
		return
	}

	metricsManager := channelScheduler.GetImagesMetricsManager()
	baseURLs := upstream.GetAllBaseURLs()
	urlResults := common.BuildDefaultURLResults(baseURLs)
	handled, _, _, lastFailoverError, _, lastError := common.TryUpstreamWithAllKeys(
		c,
		envCfg,
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindImages,
		"Images",
		metricsManager,
		upstream,
		urlResults,
		bodyBytes,
		isStream,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextImagesAPIKey(upstream, failedKeys)
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return buildOperationRequest(c, upstreamCopy, upstreamCopy.BaseURL, apiKey, bodyBytes, model, operation, contentType)
		},
		func(apiKey string) {
			_ = cfgManager.DeprioritizeAPIKey(apiKey)
		},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			return handleSuccess(c, resp, startTime, isStream)
		},
		model,
		channelIndex,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindImages),
	)
	if handled {
		return
	}

	log.Printf("[Images-Error] ?? API??????")
	handleAllKeysFailed(c, lastFailoverError, lastError)
}

func buildProviderRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	bodyBytes []byte,
	model string,
) (*http.Request, error) {
	return buildOperationRequest(c, upstream, baseURL, apiKey, bodyBytes, model, operationGenerations, "application/json")
}

func buildOperationRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	bodyBytes []byte,
	model string,
	operation string,
	contentType string,
) (*http.Request, error) {
	serviceType, err := config.NormalizeImagesServiceTypeForProxy(upstream.ServiceType)
	if err != nil {
		return nil, err
	}
	upstream.ServiceType = serviceType

	requestBody := bodyBytes
	requestContentType := contentType
	redirectedModel := config.RedirectModel(model, upstream)

	if isMultipartContentType(contentType) {
		originalModel, hasModelField := extractMultipartField(bodyBytes, contentType, "model")
		if redirectedModel != "" && (!hasModelField || strings.TrimSpace(originalModel) == "" || redirectedModel != originalModel) {
			requestBody, requestContentType, err = rewriteMultipartFormField(bodyBytes, contentType, "model", redirectedModel)
			if err != nil {
				return nil, err
			}
		}
	} else if operation == operationGenerations || len(bodyBytes) > 0 {
		requestBody, requestContentType, err = buildJSONRequestBody(bodyBytes, model, redirectedModel, operation)
		if err != nil {
			return nil, err
		}
	}

	url := buildOperationURL(baseURL, operation)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	if c.Request.URL != nil {
		req.URL.RawQuery = c.Request.URL.RawQuery
	}
	req.Header = utils.PrepareUpstreamHeadersWithContentType(c, req.URL.Host, requestContentType)
	utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)
	utils.SetAuthenticationHeader(req.Header, apiKey)
	return req, nil
}

func buildOperationURL(baseURL string, operation string) string {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	baseURL = strings.TrimSuffix(baseURL, "#")
	baseURL = strings.TrimRight(baseURL, "/")
	if skipVersionPrefix {
		return fmt.Sprintf("%s/images/%s", baseURL, operation)
	}
	return fmt.Sprintf("%s/v1/images/%s", baseURL, operation)
}

func buildJSONRequestBody(bodyBytes []byte, model string, redirectedModel string, operation string) ([]byte, string, error) {
	var reqMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
		if operation == operationGenerations {
			return nil, "", err
		}
		return bodyBytes, "application/json", nil
	}
	if reqMap == nil {
		reqMap = make(map[string]interface{})
	}
	if model != "" && redirectedModel != "" {
		reqMap["model"] = redirectedModel
	}
	requestBody, err := json.Marshal(reqMap)
	if err != nil {
		return nil, "", err
	}
	return requestBody, "application/json", nil
}

func handleSuccess(c *gin.Context, resp *http.Response, startTime time.Time, isStream bool) (*types.Usage, error) {
	defer func() { _ = resp.Body.Close() }()
	if isStream {
		return nil, passthroughStreamingResponse(c, resp)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		imagesErrorResponse(c, http.StatusInternalServerError, "Failed to read response", "server_error", "server_error")
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	var respMap map[string]interface{}
	if err := common.PassthroughJSONResponse(c, resp, &respMap); err != nil {
		return nil, nil
	}
	if u, ok := respMap["usage"].(map[string]interface{}); ok {
		inputTokens, _ := u["input_tokens"].(float64)
		outputTokens, _ := u["output_tokens"].(float64)
		return &types.Usage{InputTokens: int(inputTokens), OutputTokens: int(outputTokens)}, nil
	}
	_ = startTime
	return nil, nil
}

func passthroughStreamingResponse(c *gin.Context, resp *http.Response) error {
	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	c.Status(resp.StatusCode)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		_, err := io.Copy(c.Writer, resp.Body)
		return err
	}

	buffer := make([]byte, 4*1024)
	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := c.Writer.Write(buffer[:n]); writeErr != nil {
				if common.IsClientDisconnectError(writeErr) || writeErr == io.ErrClosedPipe || strings.Contains(strings.ToLower(writeErr.Error()), "closed pipe") {
					return context.Canceled
				}
				return writeErr
			}
			flusher.Flush()
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func imagesErrorResponse(c *gin.Context, statusCode int, message, errorType, code string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errorType,
			"code":    code,
		},
	})
}

func handleAllChannelsFailed(c *gin.Context, failoverErr *common.FailoverError, lastError error) {
	if failoverErr != nil {
		c.Data(failoverErr.Status, "application/json", failoverErr.Body)
		return
	}
	errMsg := "All channels failed"
	if lastError != nil {
		errMsg = lastError.Error()
	}
	imagesErrorResponse(c, http.StatusServiceUnavailable, errMsg, "service_unavailable", "service_unavailable")
}

func handleAllKeysFailed(c *gin.Context, failoverErr *common.FailoverError, lastError error) {
	if failoverErr != nil {
		c.Data(failoverErr.Status, "application/json", failoverErr.Body)
		return
	}
	errMsg := "All API keys failed"
	if lastError != nil {
		errMsg = lastError.Error()
	}
	imagesErrorResponse(c, http.StatusServiceUnavailable, errMsg, "service_unavailable", "service_unavailable")
}
