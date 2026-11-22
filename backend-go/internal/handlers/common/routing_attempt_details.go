package common

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

var routingBearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[^\s,;"']+`)
var routingSecretPattern = regexp.MustCompile(`(?i)\b(authorization|x-api-key|api[_-]?key|access[_-]?token|password|secret|token|key)\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s,;"']+)`)
var routingURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// 只抽取结构化错误字段；未知响应正文不作为历史摘要保存。
func routingErrorSummary(info string, code int, secrets ...string) string {
	info = strings.TrimSpace(info)
	if info == "" {
		return ""
	}
	if strings.HasPrefix(info, "{") || strings.HasPrefix(info, "[") {
		var body map[string]json.RawMessage
		if json.Unmarshal([]byte(info), &body) != nil {
			return fmt.Sprintf("HTTP %d: upstream error (body omitted)", code)
		}
		if nested := body["error"]; len(nested) > 0 {
			var errorBody map[string]json.RawMessage
			if json.Unmarshal(nested, &errorBody) == nil && errorBody != nil {
				body = errorBody
			}
		}
		parts := []string{}
		for _, field := range []string{"type", "code", "message"} {
			var value string
			if json.Unmarshal(body[field], &value) == nil && value != "" {
				parts = append(parts, value)
			}
		}
		info = strings.Join(parts, ": ")
		if info == "" {
			return fmt.Sprintf("HTTP %d: upstream error (body omitted)", code)
		}
	} else if code >= 400 || strings.Contains(strings.ToLower(info), "<html") {
		return fmt.Sprintf("HTTP %d: upstream error (body omitted)", code)
	}
	for _, secret := range secrets {
		if secret != "" {
			info = strings.ReplaceAll(info, secret, "[redacted]")
		}
	}
	// 即便内部错误附带了数据片段，也不把 JSON 请求/响应片段留到历史。
	if at := strings.IndexByte(info, '{'); at >= 0 {
		info = info[:at] + "[body omitted]"
	}
	info = routingBearerPattern.ReplaceAllString(info, "Bearer [redacted]")
	info = routingSecretPattern.ReplaceAllString(info, "$1=[redacted]")
	info = routingURLPattern.ReplaceAllStringFunc(info, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "[url omitted]"
		}
		parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
		return parsed.String()
	})
	runes := []rune(strings.Join(strings.Fields(info), " "))
	if len(runes) > 512 {
		runes = runes[:512]
	}
	return string(runes)
}

func routingAttemptDetails(info string, code int, secrets []string, usage *types.Usage) *metrics.RoutingAttemptDetails {
	details := &metrics.RoutingAttemptDetails{ErrorSummary: routingErrorSummary(info, code, secrets...), UsageObserved: usage != nil}
	if usage != nil {
		details.InputTokens, details.OutputTokens, details.CacheCreationTokens, details.CacheReadTokens = metrics.ExtractUsageTokens(usage)
	}
	return details
}

func routingRequestID(c *gin.Context) string {
	if tracker := routingTracker(c); tracker != nil {
		return tracker.id
	}
	return ""
}

func bindRoutingAttemptKey(tracker *routingObservation, id, apiKey string, req *http.Request) {
	if tracker == nil {
		return
	}
	tracker.store.SetAttemptKey(tracker.id, id, metrics.RoutingKeyID(apiKey), utils.MaskAPIKey(apiKey))
	if tracker.secrets == nil {
		tracker.secrets = make(map[string][]string)
	}
	secrets := []string{apiKey}
	for _, header := range []string{"Authorization", "X-Api-Key", "Api-Key", "X-Goog-Api-Key"} {
		value := req.Header.Get(header)
		if strings.HasPrefix(strings.ToLower(value), "bearer ") {
			value = value[7:]
		}
		secrets = append(secrets, value)
	}
	tracker.secrets[id] = secrets
}

func recordRoutingFirstByte(tracker *routingObservation, id string, latency time.Duration) {
	if tracker != nil {
		tracker.store.RecordAttemptFirstByte(tracker.id, id, latency)
	}
}
