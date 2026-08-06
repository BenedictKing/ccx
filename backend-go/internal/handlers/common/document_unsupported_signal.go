package common

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// document 能力错误信号识别。
//
// 与 compat_signal.go 的关系：那里识别"上游缺少某项协议能力、可通过改写请求兜底"，
// 这里识别"上游不支持 document（PDF）块"——没有可注入的改写（剥掉 document 等于改变用户意图），
// 结论仅供 SmartRouter 在选渠道阶段规避（见 autopilot/document_capability_memory.go）。
//
// 防误判原则与 compat_signal.go 一致：除"请求确实携带 document 块"这一请求侧门控外，
// 错误文案还要么直接点名 document（强信号），要么是不含任何具体字段名的通用
// invalid_request（弱信号）；出现具体参数名/原因的报错不学习。

// documentUnsupportedStrongPatterns 上游错误文案直接点名 document 块的特征。
// 典型：
//   - Anthropic 系校验："messages.8.content.4: Input tag 'document' found using 'type' does not match any of the expected tags: 'image', 'text', ..."
//   - Responses 系：input_file 类型不被接受
//   - 直述 media type：application/pdf not supported
//
// "document 邻近否定词"两条的间隔字符类取 [a-z ]：允许自然语句中的普通单词间隙
// （"document blocks are not supported"），但不容纳引号包裹的参数名等具体所指，
// 避免把 "document 里某字段有问题" 学成本渠道不支持 document。
var documentUnsupportedStrongPatterns = []*regexp.Regexp{
	regexp.MustCompile("input tag [`\"']?document"),
	regexp.MustCompile("[`\"']?document[`\"']?[a-z ]{0,32}(not supported|unsupported|not allowed|does not match|not permitted)"),
	regexp.MustCompile(`(not supported|unsupported|not allowed|does not match)[a-z ]{0,32}[` + "`\"']?" + `document`),
	regexp.MustCompile(`application/pdf`),
	regexp.MustCompile(`input_file`),
}

// genericInvalidRequestMessages 通用 invalid_request 文案集合（归一化后精确比对）。
// 弱信号命中条件刻意苛刻：错误不含任何具体字段名/原因时，才允许把 400 归因到
// 请求里新出现的 document 块（kimi-for-coding 的 "Invalid request Error" 即属此类）。
var genericInvalidRequestMessages = map[string]bool{
	"invalid request":       true,
	"invalid request error": true,
	"invalid_request_error": true,
	"bad request":           true,
	"request is invalid":    true,
}

// DocumentUnsupportedSignal 一条识别出的 document 不支持信号。
type DocumentUnsupportedSignal struct {
	Strong   bool   // 强信号=错误文案直接点名 document；弱信号=通用 invalid_request 且请求带 document
	Evidence string // 命中的错误文案摘要，写入记忆便于事后追溯
}

// DocumentUnsupportedFromError 从上游错误响应中识别 document 不支持信号。
// hasDocument 由调用方按实际发送的请求体判定（detectDocumentInBody）：
// 请求没带 document 时错误不可能由 document 引起，直接返回 nil。
// 仅处理 400/422：429/5xx/超时属容量问题而非能力问题，不参与学习。
func DocumentUnsupportedFromError(statusCode int, bodyBytes []byte, hasDocument bool) *DocumentUnsupportedSignal {
	if !hasDocument {
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

	// 强信号：任一错误消息点名 document / pdf / input_file
	for _, msg := range messages {
		if matchesAnyPattern(strings.ToLower(msg), documentUnsupportedStrongPatterns) {
			return &DocumentUnsupportedSignal{Strong: true, Evidence: msg}
		}
	}

	// 弱信号：error.type 是通用 invalid_request，且所有消息均为无具体所指的通用文案。
	// 任一消息带有具体内容（参数名、配额、鉴权等）即放弃学习，交由专属学习块或熔断处理。
	if !isGenericInvalidRequestType(errResp) {
		return nil
	}
	sawGeneric := false
	for _, msg := range messages {
		if genericInvalidRequestMessages[strings.ToLower(strings.TrimSpace(msg))] {
			sawGeneric = true
			continue
		}
		return nil
	}
	if !sawGeneric && len(messages) > 0 {
		return nil
	}
	evidence := "generic invalid_request"
	if len(messages) > 0 {
		evidence = messages[0]
	}
	return &DocumentUnsupportedSignal{Strong: false, Evidence: evidence}
}

// isGenericInvalidRequestType 判断 error.type 是否为通用 invalid_request 形态。
// 同时兼容嵌套形态 {"error":{"type":"invalid_request_error",...}} 与
// 顶层形态 {"type":"invalid_request_error","message":...}。
func isGenericInvalidRequestType(errResp map[string]interface{}) bool {
	if errObj, ok := errResp["error"].(map[string]interface{}); ok {
		if errType, ok := errObj["type"].(string); ok {
			return strings.ToLower(strings.TrimSpace(errType)) == "invalid_request_error"
		}
	}
	errType, ok := errResp["type"].(string)
	if !ok {
		return false
	}
	return strings.ToLower(strings.TrimSpace(errType)) == "invalid_request_error"
}
