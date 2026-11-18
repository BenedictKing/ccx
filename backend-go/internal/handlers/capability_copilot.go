package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

// Copilot 探测复用模型路由和请求转换，不能根据 serviceType 固定解析 Responses。
func buildCopilotCapabilityRequest(ctx context.Context, protocol string, channel *config.UpstreamConfig, apiKey, model, effort string, global map[string]config.UpstreamModelCapability) (*http.Request, error) {
	from := CapabilityBaseProtocol(protocol)
	if parsed, _, ok := parseCompositeProtocol(protocol); ok {
		from = parsed
	}
	var body []byte
	switch from {
	case CapabilityProtocolMessages:
		body = buildMessagesProbeBody(model, global, channel)
	case CapabilityProtocolChat:
		body = buildChatProbeBody(model, global, channel)
	case CapabilityProtocolResponses:
		body = buildResponsesProbeBody(model, global, channel)
	case CapabilityProtocolGemini:
		body = buildGeminiProbeBody(model, global, channel)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
	copy := channel.Clone()
	if effort != "" {
		if copy.ReasoningMapping == nil {
			copy.ReasoningMapping = make(map[string]string)
		}
		copy.ReasoningMapping[model] = effort
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, entryPathForProtocol(from), bytes.NewReader(body))
	c.Request = c.Request.WithContext(ctx)
	c.Set("requestBodyBytes", body)
	route, token, err := providers.PrepareCopilotRoute(c, copy, apiKey, model, string(from), false)
	if err != nil {
		return nil, err
	}
	var req *http.Request
	if from == CapabilityProtocolMessages {
		req, _, err = providers.GetProvider(copy.ServiceType).ConvertToProviderRequest(c, copy, token)
	} else {
		if from == CapabilityProtocolChat {
			body = converters.ConvertChatRequestToResponsesRequest(body)
		}
		if from == CapabilityProtocolGemini {
			var geminiReq types.GeminiRequest
			if err = json.Unmarshal(body, &geminiReq); err != nil {
				return nil, err
			}
			converted, convertErr := converters.GeminiToResponsesRequest(&geminiReq, model)
			if convertErr != nil {
				return nil, convertErr
			}
			body, err = json.Marshal(converted)
			if err != nil {
				return nil, err
			}
		}
		req, _, err = (&providers.ResponsesProvider{}).ConvertBodyToProviderRequest(c, copy, token, body, "/v1/responses")
	}
	if err != nil {
		return nil, err
	}
	if err = providers.ApplyCopilotRoute(req, route, channel.CustomHeaders); err != nil {
		return nil, err
	}
	return req, nil
}

func copilotProtocolForRequest(req *http.Request, fallback string) string {
	for suffix, protocol := range map[string]string{"/responses": "responses", "/chat/completions": "chat", "/v1/messages": "messages"} {
		if strings.HasSuffix(req.URL.Path, suffix) {
			return protocol
		}
	}
	return fallback
}
