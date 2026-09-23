package handlers

import (
	"fmt"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
)

// ============== 复合协议类型定义 ==============

const compositeProtocolSep = "->"

// CapabilityBaseProtocol 基础协议枚举（messages / chat / responses / gemini）
type CapabilityBaseProtocol string

const (
	CapabilityProtocolMessages  CapabilityBaseProtocol = "messages"
	CapabilityProtocolChat      CapabilityBaseProtocol = "chat"
	CapabilityProtocolResponses CapabilityBaseProtocol = "responses"
	CapabilityProtocolGemini    CapabilityBaseProtocol = "gemini"
)

// allBaseProtocols 全部基础协议，用于 4×4 矩阵遍历

// normalizeServiceTypeToProtocol 将渠道 ServiceType 归一化为 CapabilityBaseProtocol。
// 返回 (protocol, ok)；ok=false 表示 serviceType 无法映射（如 images）。
func normalizeServiceTypeToProtocol(serviceType string) (CapabilityBaseProtocol, bool) {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "claude", "messages":
		return CapabilityProtocolMessages, true
	case "openai", "openai-chat", "chat":
		return CapabilityProtocolChat, true
	case "responses", "codex", "copilot":
		return CapabilityProtocolResponses, true
	case "gemini":
		return CapabilityProtocolGemini, true
	default:
		return "", false
	}
}

// buildCompositeProtocol 组装复合协议键，形如 "messages->responses"。
func buildCompositeProtocol(from, to CapabilityBaseProtocol) string {
	return string(from) + compositeProtocolSep + string(to)
}

// parseCompositeProtocol 解析复合协议键，返回 (from, to, ok)。
// 若 protocol 不含分隔符，ok=false。
func parseCompositeProtocol(protocol string) (CapabilityBaseProtocol, CapabilityBaseProtocol, bool) {
	idx := strings.Index(protocol, compositeProtocolSep)
	if idx < 0 {
		return "", "", false
	}
	from := CapabilityBaseProtocol(protocol[:idx])
	to := CapabilityBaseProtocol(protocol[idx+len(compositeProtocolSep):])
	if from == "" || to == "" {
		return "", "", false
	}
	return from, to, true
}

// isCompositeProtocol 判断 protocol 是否为复合协议（含 "->" 分隔符）。

// getProbeModelsForCapabilityProtocol 获取指定协议的探测模型列表。
// 普通协议：直接查 capabilityProbeModels 表。
// 复合协议：使用 from 方向的探测模型（复合协议行展示的是入口侧探测模型）。
func getProbeModelsForCapabilityProtocol(protocol string) ([]string, error) {
	if from, _, ok := parseCompositeProtocol(protocol); ok {
		return getCapabilityProbeModels(string(from))
	}
	return getCapabilityProbeModels(protocol)
}

func targetProtocolForCapabilityProtocol(protocol string) string {
	if _, to, ok := parseCompositeProtocol(protocol); ok {
		return string(to)
	}
	return protocol
}

// compositePathRegistry 4×4 复合协议请求构造器注册表。
// key = "from->to"，value = 构造上游请求的函数。
// 若 key 不在表中，视为 unsupported_composite_path。
type compositePathBuilder func(
	channel *config.UpstreamConfig,
	apiKey string,
	probeModel string,
	global map[string]config.UpstreamModelCapability,
) (reqURL string, reqBody []byte, targetProtocol string, err error)

var compositePathRegistry = map[string]compositePathBuilder{}

// registerCompositePath 注册一条复合协议路径。
func registerCompositePath(from, to CapabilityBaseProtocol, builder compositePathBuilder) {
	key := buildCompositeProtocol(from, to)
	compositePathRegistry[key] = builder
}

// getCompositePathBuilder 获取复合协议请求构造器。
func getCompositePathBuilder(from, to CapabilityBaseProtocol) (compositePathBuilder, bool) {
	key := buildCompositeProtocol(from, to)
	builder, ok := compositePathRegistry[key]
	return builder, ok
}

// unsupportedCompositePathErr 返回复合协议路径不支持的错误。
func unsupportedCompositePathErr(from, to CapabilityBaseProtocol) error {
	return fmt.Errorf("unsupported_composite_path: %s->%s", from, to)
}

func init() {
	// ============== messages -> messages（原生协议重定向测试）==============
	registerCompositePath(CapabilityProtocolMessages, CapabilityProtocolMessages,
		func(channel *config.UpstreamConfig, apiKey, probeModel string, global map[string]config.UpstreamModelCapability) (string, []byte, string, error) {
			// 构造 messages 入口最小请求体
			messagesBody := buildMessagesProbeBody(probeModel, global, channel)
			// 使用 ClaudeProvider 将 messages 请求转换为 messages 上游请求
			return buildCompositeRequestViaProvider("messages", channel, apiKey, messagesBody, "/v1/messages")
		},
	)

	// ============== messages -> responses（首要路径）==============
	registerCompositePath(CapabilityProtocolMessages, CapabilityProtocolResponses,
		func(channel *config.UpstreamConfig, apiKey, probeModel string, global map[string]config.UpstreamModelCapability) (string, []byte, string, error) {
			// 构造 messages 入口最小请求体
			messagesBody := buildMessagesProbeBody(probeModel, global, channel)
			// 使用 ResponsesProvider 将 messages 请求转换为 responses 上游请求
			return buildCompositeRequestViaProvider("responses", channel, apiKey, messagesBody, "/v1/messages")
		},
	)

	// ============== messages -> chat ==============
	registerCompositePath(CapabilityProtocolMessages, CapabilityProtocolChat,
		func(channel *config.UpstreamConfig, apiKey, probeModel string, global map[string]config.UpstreamModelCapability) (string, []byte, string, error) {
			messagesBody := buildMessagesProbeBody(probeModel, global, channel)
			return buildCompositeRequestViaProvider("chat", channel, apiKey, messagesBody, "/v1/messages")
		},
	)

	// ============== chat -> messages ==============
	registerCompositePath(CapabilityProtocolChat, CapabilityProtocolMessages,
		func(channel *config.UpstreamConfig, apiKey, probeModel string, global map[string]config.UpstreamModelCapability) (string, []byte, string, error) {
			chatBody := buildChatProbeBody(probeModel, global, channel)
			return buildCompositeRequestViaProvider("messages", channel, apiKey, chatBody, "/v1/chat/completions")
		},
	)

	// ============== chat -> responses ==============
	registerCompositePath(CapabilityProtocolChat, CapabilityProtocolResponses,
		func(channel *config.UpstreamConfig, apiKey, probeModel string, global map[string]config.UpstreamModelCapability) (string, []byte, string, error) {
			chatBody := buildChatProbeBody(probeModel, global, channel)
			return buildCompositeRequestViaProvider("responses", channel, apiKey, chatBody, "/v1/chat/completions")
		},
	)

	// ============== responses -> messages ==============
	registerCompositePath(CapabilityProtocolResponses, CapabilityProtocolMessages,
		func(channel *config.UpstreamConfig, apiKey, probeModel string, global map[string]config.UpstreamModelCapability) (string, []byte, string, error) {
			responsesBody := buildResponsesProbeBody(probeModel, global, channel)
			return buildCompositeRequestViaProvider("messages", channel, apiKey, responsesBody, "/v1/responses")
		},
	)

	// ============== responses -> chat ==============
	registerCompositePath(CapabilityProtocolResponses, CapabilityProtocolChat,
		func(channel *config.UpstreamConfig, apiKey, probeModel string, global map[string]config.UpstreamModelCapability) (string, []byte, string, error) {
			responsesBody := buildResponsesProbeBody(probeModel, global, channel)
			return buildCompositeRequestViaProvider("chat", channel, apiKey, responsesBody, "/v1/responses")
		},
	)

	// ============== chat -> chat（原生协议重定向测试）==============
	registerCompositePath(CapabilityProtocolChat, CapabilityProtocolChat,
		func(channel *config.UpstreamConfig, apiKey, probeModel string, global map[string]config.UpstreamModelCapability) (string, []byte, string, error) {
			chatBody := buildChatProbeBody(probeModel, global, channel)
			return buildCompositeRequestViaProvider("chat", channel, apiKey, chatBody, "/v1/chat/completions")
		},
	)

	// ============== responses -> responses（原生协议重定向测试）==============
	registerCompositePath(CapabilityProtocolResponses, CapabilityProtocolResponses,
		func(channel *config.UpstreamConfig, apiKey, probeModel string, global map[string]config.UpstreamModelCapability) (string, []byte, string, error) {
			responsesBody := buildResponsesProbeBody(probeModel, global, channel)
			return buildCompositeRequestViaProvider("responses", channel, apiKey, responsesBody, "/v1/responses")
		},
	)

	// ============== gemini -> gemini（原生协议重定向测试）==============
	registerCompositePath(CapabilityProtocolGemini, CapabilityProtocolGemini,
		func(channel *config.UpstreamConfig, apiKey, probeModel string, global map[string]config.UpstreamModelCapability) (string, []byte, string, error) {
			geminiBody := buildGeminiProbeBody(probeModel, global, channel)
			return buildCompositeRequestViaProvider("gemini", channel, apiKey, geminiBody, "/v1beta/models/probe:streamGenerateContent?alt=sse")
		},
	)
}
