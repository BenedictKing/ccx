package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
)

// 能力测试的工具调用探针项（docs/specs/tool-call-capability.md §4.2）。
//
// 基础能力测试只验证"模型能回话"，无法发现"渠道不执行工具调用"的假渠道——
// 这类上游对带 tools 的请求照常 200 回纯文本。本探针在基础测试成功后附加执行：
// 用被测模型的实际模型名发送强制 tool_choice 的最小请求，检查 SSE 是否返回
// ccx_probe 工具调用。请求体构建与 SSE 判定复用渠道发现探针的同源函数，
// 两处口径不会漂移。
//
// 结论口径：
//   - SSE 出现 ccx_probe 工具调用 → Supported=true；
//   - 2xx 且有有效 SSE 内容但无工具调用 → Supported=false（可学习：上游明确收到
//     强制工具指令却未执行）；
//   - 超时 / 非 2xx / 空或不可识别响应 → inconclusive（不学习：失败可能是容量或
//     网关问题，不是能力问题）。

// ToolCallProbeSummary 单个模型工具调用探针的结论。
// Supported=false 且 ConfirmedUnsupported=true 才是"实测确认不执行工具调用"；
// Tested=true 但非确认不支持属于不确定（超时/上游报错/响应不可识别），不参与学习。
type ToolCallProbeSummary struct {
	Tested               bool   `json:"tested"`
	Supported            bool   `json:"supported"`
	ConfirmedUnsupported bool   `json:"confirmedUnsupported,omitempty"`
	StatusCode           int    `json:"statusCode,omitempty"`
	Evidence             string `json:"evidence,omitempty"`
	Error                string `json:"error,omitempty"`
}

// capabilityToolCallProbeTimeout 工具探针的独立超时；基础测试已耗时一轮，
// 探针只发一条最小请求，沿用渠道发现探针的 12s 上限。
const capabilityToolCallProbeTimeout = 12 * time.Second

// runCapabilityToolCallProbe 对指定渠道×实际模型执行工具调用探针。
// protocol 为能力测试的执行协议（messages/chat/responses/gemini）；
// 不在覆盖范围时返回 Tested=false。
func runCapabilityToolCallProbe(ctx context.Context, channel *config.UpstreamConfig, protocol, actualModel, apiKey string) ToolCallProbeSummary {
	switch protocol {
	case "messages", "claude", "chat", "responses", "gemini":
	default:
		return ToolCallProbeSummary{}
	}
	baseURL := capabilityTestBaseURL(channel)
	if baseURL == "" || actualModel == "" {
		return ToolCallProbeSummary{}
	}

	probeCtx, cancel := context.WithTimeout(ctx, capabilityToolCallProbeTimeout)
	defer cancel()

	req, err := buildCapabilityToolCallProbeRequest(protocol, baseURL, actualModel, channel, apiKey)
	if err != nil {
		return ToolCallProbeSummary{Tested: true, Error: err.Error(), Evidence: "工具调用探针请求构建失败"}
	}

	events, statusCode, body, sendErr := sendCompatProbe(probeCtx, req, channel)
	result := ToolCallProbeSummary{Tested: true, StatusCode: statusCode}
	if isCompatProbeTimeout(sendErr, probeCtx) {
		result.Error = "timeout"
		result.Evidence = "工具调用探针超时，无法确认上游是否执行工具调用"
		return result
	}
	if sendErr != nil || statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		result.Error = discoveryProbeDiagnostic(statusCode, body, sendErr)
		result.Evidence = fmt.Sprintf("上游拒绝工具调用探针请求（HTTP %d）", statusCode)
		return result
	}
	if discoverySSEHasToolCall(events, protocol, discoveryToolProbeName) {
		result.Supported = true
		result.Evidence = "上游按强制 tool_choice 返回了 ccx_probe 工具调用"
		return result
	}
	if hasMeaningfulCompatSSE(events, protocol) {
		// 2xx + 有效输出 + 无工具调用：假渠道/剥离 tools 的典型形态，唯一可学习结论。
		result.ConfirmedUnsupported = true
		result.Evidence = "上游返回了有效内容，但未按强制 tool_choice 产生工具调用"
		return result
	}
	result.Evidence = "工具调用探针响应为空或无法识别"
	return result
}

// buildCapabilityToolCallProbeRequest 与渠道发现的 buildDiscoveryToolCallProbeRequest
// 同构，区别是不做默认探测模型替换——能力测试按被测模型的实际模型名逐模型实测，
// 模型名缺失时才回退默认探测模型。
func buildCapabilityToolCallProbeRequest(protocol, baseURL, actualModel string, channel *config.UpstreamConfig, apiKey string) (*http.Request, error) {
	switch protocol {
	case "messages", "claude":
		return buildClaudeCompatRequest(baseURL, buildClaudeToolCallProbeBody(compatProbeModel(capabilityProbeModelClaudeFable5, actualModel)), channel, apiKey)
	case "chat":
		return buildOpenAIChatCompatRequest(baseURL, buildOpenAIChatToolCallProbeBody(actualModel), channel, apiKey)
	case "responses":
		return buildResponsesCompatRequest(baseURL, buildResponsesToolCallProbeBody(actualModel), channel, apiKey)
	case "gemini":
		model := compatProbeModel("gemini-3.5-flash", actualModel)
		return buildGeminiCompatRequest(baseURL, "/v1beta/models/"+model+":streamGenerateContent?alt=sse", buildGeminiToolCallProbeBody(), channel, apiKey)
	default:
		return nil, fmt.Errorf("unsupported tool call probe protocol: %s", protocol)
	}
}

// recordToolCallProbeResult 把探针的可学习结论写入共享兼容性记忆。
// 仅实测确认不支持（ConfirmedUnsupported）且渠道有 ChannelUID 时记录；仅首次记录时打日志。
func recordToolCallProbeResult(channel *config.UpstreamConfig, apiKey, actualModel string, summary ToolCallProbeSummary) {
	if channel == nil || channel.ChannelUID == "" || actualModel == "" {
		return
	}
	if !summary.Tested || summary.Supported || !summary.ConfirmedUnsupported {
		return
	}
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return
	}
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	if cache.Record(channel.ChannelUID, keyHash, actualModel,
		config.TraitNoToolCallSupport, true, config.CompatSourceProbe, summary.Evidence) {
		log.Printf("[CapabilityTest-ToolCall] 渠道 %s 模型 %s 未按强制 tool_choice 产生工具调用，已记忆并在后续路由中规避该组合（带工具请求）",
			channel.Name, actualModel)
	}
}
