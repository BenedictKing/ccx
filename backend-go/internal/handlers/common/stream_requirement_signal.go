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
// 典型（实测）：{"error":{"message":"streaming is required: this endpoint only
// accepts \"stream\": true"}}——文案含 "is required"，若不显式放行会被错误分类
// 判成 schema 校验类不可重试 400，既不学习也不 failover。
var streamRequirementPatterns = []*regexp.Regexp{
	regexp.MustCompile(`streaming is required`),
	regexp.MustCompile(`only accepts ["'\x60]?stream["'\x60]?\s*:\s*true`),
	regexp.MustCompile(`["'\x60]?stream["'\x60]?\s*[:=]\s*true.{0,32}(is|was) required`),
	regexp.MustCompile(`["'\x60]?stream["'\x60]?\s*is required`),
	regexp.MustCompile(`仅(支持|接受)流式`),
	regexp.MustCompile(`(需要|必须)(开启|使用)?流式`),
}

// isStreamRequirementMessage 判断单条错误文案是否命中流式要求特征。
func isStreamRequirementMessage(msgLower string) bool {
	return matchesAnyPattern(msgLower, streamRequirementPatterns)
}

// isStreamRequirementError 判断 error 对象是否命中流式要求特征。
// 检查 error.message / detail / msg 与嵌套 upstream_error 的同名字段，
// 字段口径与 isResponsesToolsProtocolError 一致。
func isStreamRequirementError(errObj map[string]interface{}) bool {
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
