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

// BodyHasTools 检测请求体是否携带工具语义。两种形态：
//  1. 顶层 tools 数组（Claude Messages / OpenAI Chat / Responses / Gemini 通用）；
//  2. 顶层 tool_choice 字段——codex 等客户端不发送明文 tools 数组（工具定义经
//     Responses 协议内置/加密协商，实测 2026-09-12 codex 0.153.4 请求体 84KB
//     仅含 tool_choice:"auto"），该字段的存在即声明了工具语义。
func BodyHasTools(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	tools := gjson.GetBytes(body, "tools")
	if tools.Exists() && len(tools.Array()) > 0 {
		return true
	}
	return gjson.GetBytes(body, "tool_choice").Exists()
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
// kind 为该次尝试的执行协议（executionKind），与 upstream 一起构成稳定路由身份。
func MaybeLearnForcedToolChoiceMiss(c *gin.Context, upstream *config.UpstreamConfig, apiKey, model string, attemptBody []byte, sawToolCall bool, kind string) {
	if c == nil || upstream == nil || model == "" {
		return
	}
	routeIdentity := config.ToolRouteIdentity(upstream, kind)
	if routeIdentity == "" {
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
	if cache.Record(routeIdentity, keyHash, model, config.TraitNoToolCallSupport, true, config.CompatSourceRuntimeSignal, evidence) {
		RequestLogf(c, "[ToolCallCompat] 渠道 %s 模型 %s 强制工具调用未被执行（流式全程无工具调用块），已记忆并将在后续路由中规避",
			upstream.Name, model)
	}
}

// MaybeLearnVerifiedToolCalls 运行期成功路径的正向证据学习。
//
// 条件：请求携带 tools + 上游 2xx 完成 + 流中观察到真实 function_call 事件
// （SawToolCall 由流式路径的工具活动标记供给）。真实流量里的成功工具调用是
// 强于探针的正向证据（覆盖 tool_choice=auto 场景——探针只测强制形态）。
// 记入 TraitVerifiedToolCalls 供白名单模式消费：路由内存在任一验证组合时，
// 带工具请求的候选只从验证组合产生。
// kind 为该次尝试的执行协议（executionKind）；与 upstream 一起构成稳定路由身份
// （逻辑渠道×协议，物理 UID 重铸后学习不失效）。
func MaybeLearnVerifiedToolCalls(c *gin.Context, upstream *config.UpstreamConfig, apiKey, model string, attemptBody []byte, sawToolCall bool, kind string) {
	if c == nil || upstream == nil || model == "" {
		return
	}
	routeIdentity := config.ToolRouteIdentity(upstream, kind)
	if routeIdentity == "" {
		return
	}
	if !sawToolCall || !BodyHasTools(attemptBody) {
		return
	}
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return
	}
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	if cache.Record(routeIdentity, keyHash, model, config.TraitVerifiedToolCalls, true, config.CompatSourceRuntimeSignal, "带工具请求 2xx 完成且流中出现真实 function_call 事件") {
		RequestLogf(c, "[ToolCallCompat] 渠道 %s 模型 %s 真实工具调用成功，已记入正向白名单（agentic 流量优先）",
			upstream.Name, model)
	}
	// 真实工具调用是白名单有效的对偶证据：重置伪标记连续计数（撤销语义为「连续」）
	cache.ClearVerifiedToolCallPseudoMiss(routeIdentity, keyHash, model)
}

// MaybeForgetVerifiedToolCalls 白名单的失败撤销（学习闭环的负向对偶）。
//
// 背景：渠道间排他把带工具流量锁定到白名单渠道；白名单渠道自身故障
// （上游空流/限流）时若无撤销机制会无路可退（2026-09-13 ark 空流事故）。
// 带工具请求在该组合上以无效响应（空流/无效响应体）失败时撤销 verified
// 记录——渠道级集合随即摘牌，排他 fail-open 放开全部渠道；后续真实成功
// 经 MaybeLearnVerifiedToolCalls 重建。白名单由此成为动态自愈集合。
// kind 为该次尝试的执行协议，撤销必须与学习落在同一路由身份上。
func MaybeForgetVerifiedToolCalls(c *gin.Context, upstream *config.UpstreamConfig, apiKey, model string, attemptBody []byte, kind string) {
	if c == nil || upstream == nil || model == "" {
		return
	}
	routeIdentity := config.ToolRouteIdentity(upstream, kind)
	if routeIdentity == "" {
		return
	}
	if !BodyHasTools(attemptBody) {
		return
	}
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return
	}
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	if cache.Record(routeIdentity, keyHash, model, config.TraitVerifiedToolCalls, false, config.CompatSourceRuntimeSignal, "带工具请求收到空/无效响应，撤销正向白名单记录") {
		RequestLogf(c, "[ToolCallCompat] 渠道 %s 模型 %s 带工具请求无效响应，已撤销正向白名单（排他将 fail-open 放开候选）",
			upstream.Name, model)
	}
}

// MaybeCountPseudoToolCallMiss 白名单负反馈补盲：agentic 干净 200 却零真实工具调用、
// 且输出命中伪工具调用标记文本（模型用纯文本"扮演"工具调用）时，对该组合的 verified
// 条目计一次连续 miss，连续达阈值即撤销（与 MaybeForgetVerifiedToolCalls 的失败路径
// 撤销互补，覆盖"假成功"形态）。
//
// 防误判约束（缺一不可）：
//   - 流式 2xx 干净完成（streamErr == nil）——出错流不算证据；
//   - 请求带工具且非强制 tool_choice——强制形态走 MaybeLearnForcedToolChoiceMiss，不双算；
//   - 全程零真实工具调用（!sawToolCall）且有伪标记命中——纯文本正常回答（无标记）是
//     模型合法选择，不计数。
//
// 无启用中的 verified 条目时同样计数：白名单按协议 fail-open，集合为空的窗口内
// 伪标记 miss 是唯一的劣化证据留存，达阈值后竞速不再对该组合派影子
// （ChannelCompatCache.VerifiedToolCallPseudoMissed），窗口自动收敛。
//
// kind 为该次尝试的执行协议（executionKind），与 MaybeLearnVerifiedToolCalls 同路由身份。
func MaybeCountPseudoToolCallMiss(c *gin.Context, upstream *config.UpstreamConfig, apiKey, model string, attemptBody []byte, sawToolCall, sawPseudoMarker bool, streamErr error, kind string) {
	if c == nil || upstream == nil || model == "" {
		return
	}
	if streamErr != nil || sawToolCall || !sawPseudoMarker {
		return
	}
	if !BodyHasTools(attemptBody) || ForcedToolChoiceInBody(attemptBody) {
		return
	}
	countVerifiedToolCallPseudoMiss(c, upstream, apiKey, model, kind, "成功路径")
}

// countVerifiedToolCallPseudoMiss RecordVerifiedToolCallPseudoMiss 的公共收尾：
// 路由身份归一 + 计数 + 日志。origin 标注证据来源（成功路径/竞速让出）。
func countVerifiedToolCallPseudoMiss(c *gin.Context, upstream *config.UpstreamConfig, apiKey, model, kind, origin string) {
	routeIdentity := config.ToolRouteIdentity(upstream, kind)
	if routeIdentity == "" {
		return
	}
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return
	}
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	streak, revoked := cache.RecordVerifiedToolCallPseudoMiss(routeIdentity, keyHash, model)
	if revoked {
		RequestLogf(c, "[ToolCallCompat] 渠道 %s 模型 %s 连续伪工具调用标记文本（%s），已撤销正向白名单（排他将 fail-open 放开候选）",
			upstream.Name, model, origin)
	} else if streak > 0 {
		RequestLogf(c, "[ToolCallCompat] 渠道 %s 模型 %s 输出伪工具调用标记文本（%s，连续 %d/%d 次）",
			upstream.Name, model, origin, streak, 3)
	}
}

// toolCallLearningContextKey 本次尝试的工具学习身份（竞速闸门「让出即证据」用）。
const toolCallLearningContextKey = "ccx.tool_call_learning_identity"

// toolCallLearningIdentity 一次渠道尝试的工具学习身份：上游配置（路由锚）×Key×模型×执行协议。
type toolCallLearningIdentity struct {
	upstream *config.UpstreamConfig
	apiKey   string
	model    string
	kind     string
}

// setToolCallLearningIdentity 在每次渠道尝试执行前把学习身份写入 gin context。
// 竞速分支持有独立 context 副本（startShadow 的 c.Copy 之后各自设置），互不串扰；
// 成功路径的学习调用在尝试收尾处直接使用词法作用域变量，不读此值。
func setToolCallLearningIdentity(c *gin.Context, upstream *config.UpstreamConfig, apiKey, model, kind string) {
	if c == nil || upstream == nil {
		return
	}
	c.Set(toolCallLearningContextKey, toolCallLearningIdentity{upstream: upstream, apiKey: apiKey, model: model, kind: kind})
}

func toolCallLearningIdentityFromContext(c *gin.Context) (toolCallLearningIdentity, bool) {
	if c == nil {
		return toolCallLearningIdentity{}, false
	}
	v, ok := c.Get(toolCallLearningContextKey)
	if !ok {
		return toolCallLearningIdentity{}, false
	}
	id, ok := v.(toolCallLearningIdentity)
	return id, ok && id.upstream != nil
}

// notePseudoToolCallYield 竞速质量闸门让出路径的白名单负反馈（让出即证据）。
//
// 「竞速败出分支不参与自学习」红线（upstream_failover.go）针对的是被取消而未
// 跑完的分支——部分流的部分标记不代表渠道真实能力；闸门让出不同：伪标记已在
// 该分支自身首包缓冲中实测命中，是已完成的观察，与成功路径
// MaybeCountPseudoToolCallMiss 同强度，不应因竞速语境丢弃。白名单 fail-open
// 窗口（协议集合为空）由此获得收敛能力：劣化组合每命中一次计一次 miss，达
// 阈值即不再对其派影子（racing.go toolWhitelistAllows）。
//
// 守卫与成功路径对齐：仅非强制 tool_choice 计数（强制形态由直连完成路径的
// MaybeLearnForcedToolChoiceMiss 覆盖，竞速让出不双算）。首包时刻无法预知后续
// 是否出现真实 function_call，接受这一理论误计——真实成功经正向学习即时清零
// 重建（MaybeLearnVerifiedToolCalls）。
func notePseudoToolCallYield(c *gin.Context) {
	id, ok := toolCallLearningIdentityFromContext(c)
	if !ok {
		return
	}
	body := GetEffectiveRequestBody(c, nil)
	if !BodyHasTools(body) || ForcedToolChoiceInBody(body) {
		return
	}
	countVerifiedToolCallPseudoMiss(c, id.upstream, id.apiKey, id.model, id.kind, "竞速让出")
}
