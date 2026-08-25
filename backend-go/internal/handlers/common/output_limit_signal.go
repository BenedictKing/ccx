package common

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// 输出上限识别：与 context_limit_signal.go 学习"输入最多能吃多少 token"相对应，
// 这里学习"max_tokens/max_output_tokens 最多允许填多少"。存在意义相同：模型注册表
// 登记的是模型公开输出上限（如 Kimi K2.6 官方 262144），同一模型在不同部署上可能
// 更低（火山方舟 coding 端点 32768），这类事实只能由真实请求被拒后学到，且不能外溢
// 到同渠道其他 Key / 其他模型。
//
// 与上下文上限的两点差异：
//   - 只采信上游明确自报的数值（upstream_declared）。输出上限是部署配置事实，
//     从"本次被拒"反推的估算值可能仍超限，学习没有意义；
//   - 学到后可以无损修复请求（下调 max_tokens），因此调用方会同 Key 立即重试，
//     而上下文超限只能换渠道。

// outputLimitParamHints 必须命中其一的参数名特征。
// 只认输出 token 语义的参数，防止把 temperature <= 2 之类的无关数值错误成上限。
var outputLimitParamHints = []string{
	"max_tokens",
	"maxtokens",
	"max_completion_tokens",
	"maxcompletiontokens",
	"max_output_tokens",
	"maxoutputtokens",
	"output tokens",
	"output token",
}

// outputLimitExclusions 明确排除的相邻错误。
// 命中任一即放弃学习：这些是"输入太长 / 请求体过大 / 配额"等问题，其中
// "input+max_tokens 超过上下文" 类报错虽提到 max_tokens，但那是输入侧事实，
// 归 context_limit 学习管辖，这里学了会互相打架。
var outputLimitExclusions = []string{
	"context",
	"input tokens",
	"input token",
	"prompt tokens",
	"prompt token",
	"too long",
	"request body",
	"payload",
	"image",
	"file size",
	"rate limit",
	"quota",
}

// outputLimitDeclaredPatterns 从报错中提取上游自报的最大输出 token 数。
// 典型：
//
//	火山方舟:  "integer above maximum value, expected a value <= 32768, but got 64000 instead"
//	Anthropic: "max_tokens: 64000 > 32768, which is the maximum allowed number of output tokens"
//	OpenAI:    "`max_tokens` is too large: 64000. This model supports at most `max_tokens` of 32768."
//	通用:      "max_output_tokens must be less than or equal to 65536" / "must be between 16 and 65536"
//
// 双捕获组模式里第 2 组是上限（第 1 组是被拒的请求值）；单捕获组模式直接取该值。
// 提取结果还要经"必须小于本次被拒值"的校验（见 declaredOutputLimit）。
var outputLimitDeclaredPatterns = []*outputLimitPattern{
	// 火山方舟 InvalidParameter
	{re: regexp.MustCompile(`expected a value <= (\d+)`)},
	// Anthropic "max_tokens: 64000 > 32768"（容忍反引号/引号包裹与空格差异）
	{re: regexp.MustCompile("max_tokens.{0,4}(\\d+)\\s*>\\s*(\\d+)"), capGroup: 2},
	// OpenAI "`max_tokens` is too large: 64000. ... supports at most `max_tokens` of 32768."
	{re: regexp.MustCompile(`too large:?\s*(\d+)[^.]{0,80}?at most[^\d]{0,40}(\d+)`), capGroup: 2},
	{re: regexp.MustCompile(`supports at most[^\d]{0,40}(\d+)`)},
	// 通用 must-be 系（"less than or equal to N" / "at most N" / "no more than N" / "<= N"）
	{re: regexp.MustCompile(`(?:less than or equal to|no more than|cannot exceed|at most)\D{0,20}(\d+)`)},
	{re: regexp.MustCompile(`<=\s*(\d+)`)},
	// "must be between 16 and 65536"：上限是后一个数
	{re: regexp.MustCompile(`between (\d+) and (\d+)`), capGroup: 2},
}

// outputLimitPattern 一条上限提取规则。
type outputLimitPattern struct {
	re *regexp.Regexp
	// capGroup 上限所在的捕获组序号，0 表示第 1 组（默认）
	capGroup int
}

// OutputLimitSignal 一条识别出的输出上限信号。
type OutputLimitSignal struct {
	// MaxOutputTokens 学到的最大输出 token 上限（上游自报，精确值）
	MaxOutputTokens int
	// Evidence 命中的错误文案，写入记忆便于事后追溯
	Evidence string
}

// OutputLimitFromError 从上游错误响应中识别该组合的最大输出 token 上限。
//
// 仅处理 400/422：429/5xx 属容量与可用性问题，不是能力事实。
// requestedMaxOutputTokens 是本次请求实际声明的输出上限（maxOutputTokensInBody 提取），
// 提取到的候选值必须严格小于它才可信（真实上限一定小于被拒值）；传 0 表示请求里
// 没有该字段，无从校验，直接不学习（fail-open：不学习优于学错）。
func OutputLimitFromError(statusCode int, bodyBytes []byte, requestedMaxOutputTokens int) *OutputLimitSignal {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusUnprocessableEntity {
		return nil
	}
	if len(bodyBytes) == 0 || requestedMaxOutputTokens <= 0 {
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

		if containsAny(lower, outputLimitExclusions) {
			continue
		}
		if !containsAny(lower, outputLimitParamHints) {
			continue
		}

		if declared := declaredOutputLimit(lower, requestedMaxOutputTokens); declared > 0 {
			return &OutputLimitSignal{
				MaxOutputTokens: declared,
				Evidence:        msg,
			}
		}
	}
	return nil
}

// declaredOutputLimit 提取上游自报的最大输出 token 数。
//
// 关键防误取（同 declaredContextLimit）：报错里常同时出现"上限"和"你请求了多少"
// 两个数字，正则捕获顺序不保证拿到前者。校验规则：真实上限一定小于本次被拒的
// 请求值，候选值 >= 请求值时必然不是上限，丢弃后继续尝试下一条正则。
// 返回 0 表示未能可靠提取。
func declaredOutputLimit(lowerMsg string, requestedMaxOutputTokens int) int {
	for _, pattern := range outputLimitDeclaredPatterns {
		match := pattern.re.FindStringSubmatch(lowerMsg)
		group := 1
		if pattern.capGroup > 0 {
			group = pattern.capGroup
		}
		if len(match) <= group {
			continue
		}
		value, err := strconv.Atoi(match[group])
		if err != nil || value <= 0 {
			continue
		}
		if value >= requestedMaxOutputTokens {
			continue
		}
		return value
	}
	return 0
}
