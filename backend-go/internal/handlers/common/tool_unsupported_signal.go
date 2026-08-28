package common

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
)

// 工具调用能力负信号识别与学习（docs/specs/tool-call-capability.md §4.3/§4.4）。
//
// 与 document_unsupported_signal.go 的关系：同为"无请求改写可兜底、仅供 SmartRouter
// 规避"的能力事实，但工具的特殊性在于**假成功**——上游可以 200 + 纯文本回应带工具的
// 请求（模型不会调工具 / 中转剥离了 tools），这绕过一切错误路径学习。因此除错误信号外，
// 还有"强制 tool_choice 却零工具调用"的成功路径信号。
//
// 防误判原则比 document 更严：带 tools 的请求在 agent 流量中占比极高，把无关 400 或
// "模型自发选择不调工具"学成"不支持工具"会误杀健康渠道。因此：
//   - 错误路径只认错误文案点名 tools 的强信号（不做通用 invalid_request 弱信号归因）；
//   - 成功路径只认强制 tool_choice（type=tool/any/function/custom、"required" 字符串、
//     Gemini mode=ANY）——此时不产生工具调用是无歧义的异常。

// toolUnsupportedStrongPatterns 上游错误文案点名工具能力的特征（小写匹配）。
// 允许自然语句中的普通单词间隙与键名冒号（"unsupported parameter: tools"），
// 不容纳引号包裹的具体参数值，避免把"tools 里某个字段有问题"学成本渠道不支持工具。
var toolUnsupportedStrongPatterns = []*regexp.Regexp{
	regexp.MustCompile("tool[_ ]?use[a-z :]{0,32}(not supported|unsupported|not allowed|not permitted|not enabled|disabled)"),
	regexp.MustCompile("(not supported|unsupported|not allowed|not permitted|not enabled|does not support|doesn't support|disabled)[a-z :]{0,32}tool[_ ]?use"),
	regexp.MustCompile("`?[\"']?tools[`\"']?[a-z :]{0,32}(not supported|unsupported|not allowed|not permitted|not enabled|are not|is not)"),
	regexp.MustCompile("(not supported|unsupported|not allowed|not permitted|not enabled|does not support|doesn't support|disabled)[a-z :]{0,32}`?[\"']?tools[`\"']?"),
	regexp.MustCompile("function calling[a-z :]{0,32}(not supported|unsupported|not allowed|not permitted|not enabled|disabled)"),
	regexp.MustCompile("(not supported|unsupported|not allowed|not permitted|not enabled|does not support|doesn't support|disabled)[a-z :]{0,32}function calling"),
}

// ToolUnsupportedSignal 一条识别出的工具调用不支持信号。
type ToolUnsupportedSignal struct {
	Evidence string // 命中的错误文案摘要，写入记忆便于事后追溯
}

// ToolUnsupportedFromError 从上游错误响应中识别工具调用不支持信号。
// hasTools 由调用方按实际发送的请求体判定（BodyHasTools）：
// 请求没带 tools 时错误不可能由工具引起，直接返回 nil。
// 仅处理 400/422：429/5xx/超时属容量问题而非能力问题，不参与学习。
// 与 document 不同：不做通用 invalid_request 弱信号归因（见文件头注释）。
func ToolUnsupportedFromError(statusCode int, bodyBytes []byte, hasTools bool) *ToolUnsupportedSignal {
	if !hasTools {
		return nil
	}
	if statusCode != http.StatusBadRequest && statusCode != http.StatusUnprocessableEntity {
		return nil
	}
	if len(bodyBytes) == 0 {
		return nil
	}

	var errResp map[string]interface{}
	if json.Unmarshal(bodyBytes, &errResp) != nil {
		return nil
	}
	messages := extractErrorMessageFields(errResp)
	for _, msg := range messages {
		if matchesAnyPattern(strings.ToLower(msg), toolUnsupportedStrongPatterns) {
			return &ToolUnsupportedSignal{Evidence: msg}
		}
	}
	return nil
}

// BodyHasTools 检测请求体是否携带工具定义。四类文本协议的工具字段都是顶层 tools 数组
// （Claude Messages / OpenAI Chat / Responses / Gemini）。
func BodyHasTools(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	tools := gjson.GetBytes(body, "tools")
	return tools.Exists() && len(tools.Array()) > 0
}

// ForcedToolChoiceInBody 检测请求是否强制产生工具调用。
//
// 覆盖四种协议的"必须调工具"形态：
//   - Claude Messages: tool_choice {"type":"tool","name":...}（指定）或 {"type":"any"}（任一）
//   - OpenAI Chat: tool_choice {"type":"function",...}（指定）、{"name":...}（legacy 指定）、
//     "required" 字符串（任一）
//   - Responses: tool_choice {"type":"function"|"custom","name":...}、"required" 字符串
//   - Gemini: tool_config.function_calling_config.mode = "ANY"
//
// 仅强制形态的零工具调用才构成负信号；auto/未声明时模型自发回复文本是合法行为。
func ForcedToolChoiceInBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	if mode := gjson.GetBytes(body, "tool_config.function_calling_config.mode").String(); mode != "" {
		return strings.EqualFold(mode, "ANY")
	}

	choice := gjson.GetBytes(body, "tool_choice")
	if !choice.Exists() {
		return false
	}
	if choice.Type == gjson.String {
		return strings.EqualFold(choice.String(), "required")
	}
	if !choice.IsObject() {
		return false
	}
	switch choice.Get("type").String() {
	case "tool", "any", "function", "custom":
		return true
	case "named":
		// 极旧 OpenAI legacy 形态 {"type":"named"/"name"?} 未见主流使用，按无强制处理。
		return false
	}
	// legacy {"name": ...} 对象形态（无 type 字段）
	return choice.Get("name").Exists()
}

// MaybeLearnForcedToolChoiceMiss 运行期成功路径的负信号学习（层3b）。
//
// 调用方约束：仅在流式成功完成后、且 executionKind 为 messages/responses 时调用——
// 只有这两条流式路径接了工具活动标记（common/stream_processor 的 ToolCallTracker /
// responses/stream），chat/gemini 流不标记工具活动，sawToolCall=false 无法区分
// "没调用"与"没观测"，参与学习必然误杀。
//
// 学习条件：请求强制 tool_choice + 上游 2xx 完成 + 全程零工具调用块。
func MaybeLearnForcedToolChoiceMiss(c *gin.Context, upstream *config.UpstreamConfig, apiKey, model string, attemptBody []byte, sawToolCall bool) {
	if c == nil || upstream == nil || upstream.ChannelUID == "" || model == "" {
		return
	}
	if sawToolCall || !ForcedToolChoiceInBody(attemptBody) {
		return
	}
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return
	}
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	evidence := "强制 tool_choice 请求 2xx 完成但全程未产生任何工具调用"
	if cache.Record(upstream.ChannelUID, keyHash, model, config.TraitNoToolCallSupport, true, config.CompatSourceRuntimeSignal, evidence) {
		RequestLogf(c, "[ToolCallCompat] 渠道 %s 模型 %s 强制工具调用未被执行（流式全程无工具调用块），已记忆并将在后续路由中规避",
			upstream.Name, model)
	}
}
