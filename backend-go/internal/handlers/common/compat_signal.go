package common

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/BenedictKing/ccx/internal/config"
)

// 渠道兼容性错误信号识别。
//
// 与 deprecated_param.go 的关系：那里识别"某个参数被弃用"（改写方式固定为删字段），这里识别
// "该上游缺少某项协议能力"（改写方式因 trait 而异，由各 provider 在构造请求时执行）。
//
// 防误判原则：每条规则都要求两个以上独立特征同时命中。只要单个宽泛关键词就下结论，会把无关报错
// 学成永久记忆，比不学更糟。

// developerRolePatterns 上游不识别 developer role 的报错特征。
// 典型：{"error":{"message":"Failed to deserialize the JSON body into the target type:
// messages[0].role: unknown variant `developer`, expected one of `system`, `user`, ...
var developerRolePatterns = []*regexp.Regexp{
	regexp.MustCompile("unknown variant [`\"']?developer"),
	regexp.MustCompile("invalid value [`\"']?developer"),
	regexp.MustCompile("[`\"']?developer[`\"']? is not (a )?valid"),
	regexp.MustCompile("unsupported role[^a-z]{0,4}developer"),
}

// developerRoleCorroborations 佐证特征：必须同时命中其一，才认定是 role 枚举错误而非其他含
// "developer" 字样的报错（如某上游把 API key 描述成 developer key）。
var developerRoleCorroborations = []string{
	"expected one of",
	"role",
	"variant",
}

// codexToolPatterns 上游不接受 Codex 客户端专属工具的报错特征。
// Codex 工具兼容没有现成的真实错误样本，因此这里刻意保守：正则命中之外，调用方还必须证明
// 请求确实携带了 Codex 专属工具（见 CompatTraitFromError 的 hasCodexClientTools 参数）。
var codexToolPatterns = []*regexp.Regexp{
	regexp.MustCompile(`unknown tool`),
	regexp.MustCompile(`invalid tool`),
	regexp.MustCompile(`unrecognized tool`),
	regexp.MustCompile(`unsupported tool`),
	regexp.MustCompile(`tool[^.]{0,40}not supported`),
	regexp.MustCompile(`tools\[\d+\][^.]{0,40}(invalid|unknown|unsupported)`),
}

// thinkingBlockPatterns 严格 Claude thinking 上游要求历史 thinking 块回传的报错特征。
// 与 compat_diagnose_handler.go 的主动探测判据一致（400/422），此处补上被动识别路径。
var thinkingBlockPatterns = []*regexp.Regexp{
	regexp.MustCompile(`thinking`),
}

var thinkingBlockCorroborations = []string{
	"expected",
	"required",
	"must",
	"missing",
	"first block",
}

// BodyHasDeveloperRole 判断原始请求体中是否存在 developer role。
// 同时检查 Responses 协议的 input[] 与 Chat 协议的 messages[]：failover 层拿到的是入口原始 body
// （Responses 请求为 input），而下游 Chat 请求体由 provider 转换后才产生 messages，两处都要覆盖。
func BodyHasDeveloperRole(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	for _, path := range []string{"input.#.role", "messages.#.role"} {
		for _, role := range gjson.GetBytes(body, path).Array() {
			if role.String() == "developer" {
				return true
			}
		}
	}
	return false
}

// BodyHasHistoricalThinking 判断请求体中是否携带历史 thinking / reasoning_content 块。
// 仅在携带时才允许学习 thinking 回传类兼容项：请求本身没有历史思考内容时，
// 上游报的 thinking 相关错误与该能力无关。
func BodyHasHistoricalThinking(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	if gjson.GetBytes(body, `messages.#.reasoning_content`).Exists() {
		return true
	}
	// Claude 协议：content 为 block 数组，thinking 块嵌在其中
	for _, msg := range gjson.GetBytes(body, "messages").Array() {
		for _, block := range msg.Get("content").Array() {
			switch block.Get("type").String() {
			case "thinking", "redacted_thinking":
				return true
			}
		}
	}
	return false
}

// CompatSignal 一条识别出的兼容性信号。
type CompatSignal struct {
	Trait    config.CompatTrait
	Enabled  bool   // 学到的结论；当前所有错误驱动信号都是"需要启用该兼容改写"
	Evidence string // 命中的错误文案摘要，写入记忆便于事后追溯
}

// CompatSignalContext 调用方提供的请求侧事实，用于把"错误文案像"收紧为"确实是这个问题"。
type CompatSignalContext struct {
	// HasDeveloperRole 请求确实携带 developer role（Responses input 或 Chat messages）
	HasDeveloperRole bool
	// HasCodexClientTools 请求确实携带 Codex 客户端专属工具
	HasCodexClientTools bool
	// HasHistoricalThinking 请求确实携带历史 thinking / reasoning_content 块
	HasHistoricalThinking bool
}

// CompatTraitFromError 从上游错误响应中识别兼容性信号。
// 仅处理 400/422：429/5xx/超时属容量问题而非能力问题，不参与兼容性学习。
// 未命中任何规则时返回 nil。
func CompatTraitFromError(statusCode int, bodyBytes []byte, ctx CompatSignalContext) *CompatSignal {
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
	if len(messages) == 0 {
		return nil
	}

	for _, msg := range messages {
		lower := strings.ToLower(msg)

		// developer role：请求确实带 developer role + 正则命中 + 佐证词命中
		if ctx.HasDeveloperRole &&
			matchesAnyPattern(lower, developerRolePatterns) &&
			containsAny(lower, developerRoleCorroborations) {
			return &CompatSignal{
				Trait:    config.TraitDowngradeDeveloperRole,
				Enabled:  true,
				Evidence: msg,
			}
		}

		// Codex 专属工具：请求确实带 Codex 工具 + 正则命中（双门控，见 codexToolPatterns 注释）
		if ctx.HasCodexClientTools && matchesAnyPattern(lower, codexToolPatterns) {
			return &CompatSignal{
				Trait:    config.TraitStripCodexClientTools,
				Enabled:  true,
				Evidence: msg,
			}
		}

		// 历史 thinking 块被拒：请求确实带历史 thinking + 正则命中 + 佐证词命中
		if ctx.HasHistoricalThinking &&
			matchesAnyPattern(lower, thinkingBlockPatterns) &&
			containsAny(lower, thinkingBlockCorroborations) {
			return &CompatSignal{
				Trait:    config.TraitPassbackThinkingBlocks,
				Enabled:  true,
				Evidence: msg,
			}
		}
	}
	return nil
}

func matchesAnyPattern(text string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
