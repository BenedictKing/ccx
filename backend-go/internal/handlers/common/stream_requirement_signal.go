package common

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
)

// 流式要求错误信号识别。
//
// 与 compat_signal.go 的关系：那里识别"上游缺少某项协议能力、可通过改写请求兜底"，
// 这里识别"上游端点仅接受 stream:true"——非流式请求没有可自动改写之处（强制
// stream:true 需要把上游 SSE 合成回非流式响应，不属于兼容改写）。结论有两处消费：
// failover 错误分类放行（isNonRetryableError，保证首次请求能换渠道重试）与
// attempt 循环发送前跳过（后续非流式请求不再消耗必然失败的上游往返）。
//
// 防误判原则与 compat_signal.go 一致：正则命中之外，调用方还必须证明请求确实是
// 非流式（StreamRequirementFromError 的 requestIsStream 参数）——流式请求携带
// stream:true，正常上游不会报这个错。放行路径（isStreamRequirementError）不依赖
// 请求侧门控：流式请求误收该错误时换渠道重试同样是正确行为。

// streamRequirementPatterns 上游错误文案点名「仅接受流式请求」的特征（小写匹配）。
// 典型（实测）：
//   - {"error":{"message":"streaming is required: this endpoint only accepts \"stream\": true"}}
//     ——文案含 "is required"，若不显式放行会被错误分类判成 schema 校验类不可重试 400。
//   - {"error":{"type":"stream_required","message":"本渠道禁止非流请求"}}
//     ——中文文案从反面表达（禁止非流），错误码 stream_required 是结构化强信号。
var streamRequirementPatterns = []*regexp.Regexp{
	regexp.MustCompile(`streaming is required`),
	regexp.MustCompile(`only accepts ["'\x60]?stream["'\x60]?\s*:\s*true`),
	regexp.MustCompile(`["'\x60]?stream["'\x60]?\s*[:=]\s*true.{0,32}(is|was) required`),
	regexp.MustCompile(`["'\x60]?stream["'\x60]?\s*is required`),
	regexp.MustCompile(`仅(支持|接受|限)流式`),
	regexp.MustCompile(`(需要|必须)(开启|使用)?流式`),
	regexp.MustCompile(`(禁止|不支持|拒绝)非流`),
	regexp.MustCompile(`non-?stream(?:ing)? .{0,32}(not supported|forbidden|disallowed|disabled|rejected)`),
	regexp.MustCompile(`only supports? streaming`),
}

// streamRequiredCodeNormalizations 错误码/type 强信号的归一化精确匹配集合。
// normalizeAlnum 后比对，防反义 code（如 not_stream_required）contains 误命中。
var streamRequiredCodeNormalizations = map[string]bool{
	"streamrequired": true,
}

// isStreamRequiredCode 判断错误码/type 字段值是否为流式要求强信号。
func isStreamRequiredCode(value string) bool {
	return streamRequiredCodeNormalizations[normalizeAlnum(strings.ToLower(strings.TrimSpace(value)))]
}

// hasStreamRequiredCode 检查顶层与 error 对象（及嵌套 upstream_error）的 code/type 字段。
// 逐字段独立检查而非短路：顶层 type 可能是通用值（如 "error"），不能挡住 error.type 强信号。
func hasStreamRequiredCode(obj map[string]interface{}) bool {
	for _, field := range []string{"code", "type"} {
		if v, ok := obj[field].(string); ok && isStreamRequiredCode(v) {
			return true
		}
	}
	if errObj, ok := obj["error"].(map[string]interface{}); ok {
		return hasStreamRequiredCode(errObj)
	}
	if upstreamErr, ok := obj["upstream_error"].(map[string]interface{}); ok {
		return hasStreamRequiredCode(upstreamErr)
	}
	return false
}

// isStreamRequirementMessage 判断单条错误文案是否命中流式要求特征。
func isStreamRequirementMessage(msgLower string) bool {
	return matchesAnyPattern(msgLower, streamRequirementPatterns)
}

// isStreamRequirementError 判断 error 对象是否命中流式要求特征。
// 检查 code/type 结构化强信号与 error.message / detail / msg 文案，
// 以及嵌套 upstream_error 的同名字段，字段口径与 isResponsesToolsProtocolError 一致。
func isStreamRequirementError(errObj map[string]interface{}) bool {
	if hasStreamRequiredCode(errObj) {
		return true
	}
	fields := []string{"message", "detail", "msg"}
	for _, field := range fields {
		if isStreamRequirementMessage(strings.ToLower(toStringField(errObj, field))) {
			return true
		}
	}
	if upstreamErr, ok := errObj["upstream_error"].(map[string]interface{}); ok {
		for _, field := range fields {
			if isStreamRequirementMessage(strings.ToLower(toStringField(upstreamErr, field))) {
				return true
			}
		}
	}
	return false
}

// StreamRequirementFromError 从上游错误响应中识别「仅接受流式请求」信号。
// requestIsStream 由调用方按入口请求的 stream 意图传入：流式请求不会触发该错误，
// 请求侧门控是防误判的核心（与 codexToolPatterns 的双门控同型）。
// 仅处理 400/422：429/5xx/超时属容量问题而非能力问题，不参与学习。
func StreamRequirementFromError(statusCode int, bodyBytes []byte, requestIsStream bool) *CompatSignal {
	if requestIsStream {
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
	// 错误码/type 强信号（结构化字段比文案可靠，实测 new-api 系 "stream_required"）
	if hasStreamRequiredCode(errResp) {
		evidence := "error code/type: stream_required"
		if messages := extractErrorMessageFields(errResp); len(messages) > 0 {
			evidence = messages[0]
		}
		return &CompatSignal{
			Trait:    config.TraitRequiresStream,
			Enabled:  true,
			Evidence: evidence,
		}
	}
	for _, msg := range extractErrorMessageFields(errResp) {
		if isStreamRequirementMessage(strings.ToLower(msg)) {
			return &CompatSignal{
				Trait:    config.TraitRequiresStream,
				Enabled:  true,
				Evidence: msg,
			}
		}
	}
	return nil
}
