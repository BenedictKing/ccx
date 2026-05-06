package passthrough

import (
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type APIFormat string

const (
	APIFormatUnknown         APIFormat = ""
	APIFormatClaudeMessages  APIFormat = "claude_messages"
	APIFormatOpenAIResponses APIFormat = "openai_responses"
	APIFormatOpenAIChat      APIFormat = "openai_chat"
	APIFormatGeminiContents  APIFormat = "gemini_contents"
)

type Decision struct {
	InboundFormat  APIFormat
	OutboundFormat APIFormat
	StrictBody     bool
	RawResponse    bool
	SkipPreprocess bool
}

func Decide(path string, kind scheduler.ChannelKind, upstream *config.UpstreamConfig, _ string) Decision {
	inbound := InboundFormatFromPath(path)
	decision := Decision{
		InboundFormat: inbound,
	}
	if upstream == nil {
		return decision
	}

	outbound := OutboundFormatForService(upstream.ServiceType, inbound)
	strictBody := AllowsStrictBodyPassthrough(path, upstream)

	decision.OutboundFormat = outbound
	decision.StrictBody = strictBody
	decision.RawResponse = AllowsRawResponsePassthrough(path, upstream)
	decision.SkipPreprocess = kind == scheduler.ChannelKindMessages && strictBody
	return decision
}

func InboundFormatFromPath(path string) APIFormat {
	path = strings.TrimSpace(path)
	switch {
	case strings.HasSuffix(path, "/v1/messages"), strings.HasSuffix(path, "/v1/messages/count_tokens"):
		return APIFormatClaudeMessages
	case strings.HasSuffix(path, "/v1/responses"):
		return APIFormatOpenAIResponses
	case strings.HasSuffix(path, "/v1/chat/completions"):
		return APIFormatOpenAIChat
	case isGeminiContentsPath(path):
		return APIFormatGeminiContents
	default:
		return APIFormatUnknown
	}
}

func isGeminiContentsPath(path string) bool {
	if !strings.Contains(path, "/v1beta/models/") && !strings.Contains(path, "/v1/models/") {
		return false
	}
	return strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent")
}

func OutboundFormatForService(serviceType string, inbound APIFormat) APIFormat {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude":
		return APIFormatClaudeMessages
	case "responses":
		return APIFormatOpenAIResponses
	case "openai":
		return APIFormatOpenAIChat
	case "gemini":
		return APIFormatGeminiContents
	default:
		return APIFormatUnknown
	}
}

func FormatsMatch(inbound, outbound APIFormat) bool {
	return inbound != APIFormatUnknown && inbound == outbound
}

func AllowsStrictBodyPassthrough(path string, upstream *config.UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	inbound := InboundFormatFromPath(path)
	outbound := OutboundFormatForService(upstream.ServiceType, inbound)
	return FormatsMatch(inbound, outbound)
}

func AllowsRawResponsePassthrough(path string, upstream *config.UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	inbound := InboundFormatFromPath(path)
	outbound := OutboundFormatForService(upstream.ServiceType, inbound)
	return FormatsMatch(inbound, outbound)
}

func PatchTopLevelModel(body []byte, upstream *config.UpstreamConfig) []byte {
	if upstream == nil {
		return body
	}
	model := gjson.GetBytes(body, "model")
	if !model.Exists() || model.Type != gjson.String {
		return body
	}
	redirected := config.RedirectModel(model.String(), upstream)
	if redirected == model.String() {
		return body
	}
	patched, err := sjson.SetBytes(body, "model", redirected)
	if err != nil {
		return body
	}
	return patched
}

func PatchPlatformFields(body []byte, upstream *config.UpstreamConfig) []byte {
	originalModel := ""
	if model := gjson.GetBytes(body, "model"); model.Exists() && model.Type == gjson.String {
		originalModel = model.String()
	}
	body = PatchTopLevelModel(body, upstream)
	if upstream == nil {
		return body
	}
	if originalModel != "" {
		if effort := config.ResolveReasoningEffort(originalModel, upstream); effort != "" {
			patched, err := sjson.SetBytes(body, "reasoning", map[string]interface{}{"effort": effort})
			if err == nil {
				body = patched
			}
		}
	}
	if upstream.TextVerbosity != "" {
		patched, err := sjson.SetBytes(body, "text", map[string]interface{}{"verbosity": upstream.TextVerbosity})
		if err == nil {
			body = patched
		}
	}
	if upstream.FastMode {
		patched, err := sjson.SetBytes(body, "service_tier", "priority")
		if err == nil {
			body = patched
		}
	}
	return body
}
