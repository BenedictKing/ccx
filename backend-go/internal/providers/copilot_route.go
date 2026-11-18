package providers

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/copilot"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// PrepareCopilotRoute 只修改本次尝试的配置副本，认证与指标仍属于原 Copilot 渠道。
func PrepareCopilotRoute(c *gin.Context, upstream *config.UpstreamConfig, apiKey, model, protocol string, websocket bool) (*copilot.ModelRoute, string, error) {
	c.Set("copilot_route_source", "")
	c.Set("copilot_upstream_protocol", "")
	c.Set("copilot_model_route", (*copilot.ModelRoute)(nil))
	if upstream.ServiceType != "copilot" {
		return nil, apiKey, nil
	}
	route, err := copilot.ResolveModelRoute(c.Request.Context(), apiKey, copilot.RouteOptions{
		BaseURL: upstream.GetEffectiveBaseURL(), ProxyURL: upstream.ProxyURL,
		InsecureSkipVerify: upstream.InsecureSkipVerify, Model: config.RedirectModel(model, upstream),
		PreferredProtocol: protocol, WebSocket: websocket,
	})
	if err != nil {
		return nil, "", err
	}
	if protocol == "responses" && route.ServiceType != "responses" {
		if body, ok := c.Get(requestBodyBytesContextKey); ok {
			if bytes, ok := body.([]byte); ok && hasOpaqueResponsesState(bytes) {
				return nil, "", fmt.Errorf("无法将 Responses 加密历史无损传给 Copilot 模型 %q 的 %s endpoint", model, route.Path)
			}
		}
	}
	upstream.ServiceType = route.ServiceType
	// 构造器可以追加协议后缀，但不能补 /v1；最终 URL 由同一个路由结果固定。
	upstream.BaseURL = route.BaseURL + "#"
	upstream.BaseURLs = nil
	c.Set("copilot_route_source", route.Source)
	c.Set("copilot_upstream_protocol", route.ServiceType)
	c.Set("copilot_model_route", route)
	return route, route.RuntimeToken, nil
}

// ApplyPreparedCopilotRoute 让显式推理续轮继续使用已固定的 endpoint 与受保护认证。
func ApplyPreparedCopilotRoute(c *gin.Context, req *http.Request, customHeaders map[string]string) error {
	value, _ := c.Get("copilot_model_route")
	route, _ := value.(*copilot.ModelRoute)
	return ApplyCopilotRoute(req, route, customHeaders)
}

func hasOpaqueResponsesState(body []byte) bool {
	opaque := false
	gjson.GetBytes(body, "input").ForEach(func(_, item gjson.Result) bool {
		opaque = item.Get("encrypted_content").String() != "" || item.Get("encrypted_function_args").String() != ""
		return !opaque
	})
	return opaque
}

// ApplyCopilotRoute 固定 endpoint，并在转换器之后恢复受保护的 Copilot 认证头。
func ApplyCopilotRoute(req *http.Request, route *copilot.ModelRoute, customHeaders map[string]string) error {
	if route == nil {
		return nil
	}
	target, err := url.Parse(route.BaseURL + route.Path)
	if err != nil {
		return err
	}
	utils.ForwardRequestQuery(target, req.URL)
	req.URL = target
	req.Host = ""
	req.Header.Del("Authorization")
	req.Header.Del("x-api-key")
	req.Header.Del("x-goog-api-key")
	utils.ApplyCustomHeadersProtected(req.Header, customHeaders, utils.CopilotProtectedHeaders)
	copilot.ApplyRuntimeHeaders(req.Header, route.RuntimeToken)
	return nil
}
