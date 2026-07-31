package common

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/utils"
)

// 渠道-Key-模型 级别的上下文上限识别。
//
// 与 compat_signal.go 的关系：那里学习的是布尔型协议能力（要不要改写请求结构），这里学习的是
// 数值型容量事实（该组合最多能吃多少输入 token）。共用 ChannelCompatCache 的键空间与 TTL，
// 但存成 ContextLimitState 而非 CompatTraitState。
//
// 存在意义：模型注册表登记的是模型公开窗口，个别渠道对某个模型的实际窗口更短（中转商截断、
// 套餐限制）。这类事实只能靠真实请求被拒绝后学到，且不能外溢到同渠道其他 Key / 其他模型。

// contextLimitPatterns 上下文超限的报错特征。
// 只保留明确指向"输入过长"的表述：泛化的 "too large" / "exceeded" 会命中配额、
// 图片体积、请求体大小等无关错误，学错会直接把渠道从候选里永久排除。
var contextLimitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`context[_ ]too[_ ]large`),
	regexp.MustCompile(`context[_ ]window[^.]{0,30}(exceed|too (long|large)|limit)`),
	regexp.MustCompile(`(maximum|max)[^.]{0,30}context[^.]{0,30}(length|window|tokens)`),
	regexp.MustCompile(`(input|prompt)[^.]{0,20}too[_ ]long`),
	regexp.MustCompile(`(prompt|input|request)[^.]{0,30}(exceeds|exceeded)[^.]{0,40}(context|token)`),
	regexp.MustCompile(`too many (input )?tokens`),
	regexp.MustCompile(`reduce the length of the (messages|prompt|input)`),
}

// contextLimitCorroborations 佐证特征：必须同时命中其一。
// 单靠上面的正则仍可能撞上"输出 max_tokens 过大"这类相邻但不同的错误，
// 要求同时出现 token/context/length 类词汇，把误判面再收一层。
var contextLimitCorroborations = []string{
	"token", "context", "length", "too long", "过长", "超出", "上下文",
}

// contextLimitExclusions 明确排除的相邻错误。
// 命中任一即放弃学习：这些是输出上限、请求体字节数、图片尺寸等问题，
// 与"该组合能接受多少输入 token"无关，误当成上下文上限会造成永久性误排除。
var contextLimitExclusions = []string{
	"max_tokens",
	"maxtokens",
	"max_output_tokens",
	"maxoutputtokens",
	"completion tokens",
	"output tokens",
	"request body",
	"payload too large",
	"entity too large",
	"image",
	"file size",
	"rate limit",
	"quota",
}

// declaredLimitPatterns 从报错中提取上游自报的最大输入 token 数。
// 典型：
//
//	"This model's maximum context length is 272000 tokens, however you requested 1050000 tokens"
//	"maximum context length: 200000"
//	"input length exceeds the maximum of 131072 tokens"
//
// 捕获组 1 必须是上限值本身，不能是"你请求了多少"那个更大的数。
var declaredLimitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`maximum context length is (\d{4,})`),
	regexp.MustCompile(`maximum context length[:= ]+(\d{4,})`),
	regexp.MustCompile(`max(?:imum)? (?:input )?tokens?[:= ]+(\d{4,})`),
	regexp.MustCompile(`context (?:window|length)[^.\d]{0,20}(\d{4,})`),
	regexp.MustCompile(`maximum of (\d{4,}) tokens`),
	regexp.MustCompile(`limit(?:ed)? to (\d{4,}) tokens`),
	regexp.MustCompile(`支持最大(?:上下文)?[^0-9]{0,10}(\d{4,})`),
}

// rejectedEstimateDiscountNumerator/Denominator 反推上界时的保守折扣（7/8）。
// 上游只说"太长"不给数值时，唯一确定的事实是"真实上限 < 本次请求量"。取被拒量的 7/8
// 作为上界：留出足够余量避免下次再撞线，又不至于把窗口压得离真实值太远。
const (
	rejectedEstimateDiscountNumerator   = 7
	rejectedEstimateDiscountDenominator = 8
)

// ContextLimitSignal 一条识别出的上下文上限信号。
type ContextLimitSignal struct {
	// MaxInputTokens 学到的输入 token 上限（保守值）
	MaxInputTokens int
	// Source config.CompatSourceUpstreamDeclared / config.CompatSourceRejectedEstimate
	Source string
	// Evidence 命中的错误文案，写入记忆便于事后追溯
	Evidence string
}

// ContextLimitFromError 从上游错误响应中识别该组合的上下文输入上限。
//
// 仅处理 400/422：429/5xx 属容量与可用性问题，不是能力事实。
// estimatedInputTokens 是本次请求的输入估算量，用于在上游未给出具体数值时反推保守上界；
// 传 0 表示无估算，此时只能采信上游明确声明的数值。
// 未命中任何规则时返回 nil（fail-open：不学习优于学错）。
func ContextLimitFromError(statusCode int, bodyBytes []byte, estimatedInputTokens int) *ContextLimitSignal {
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

		if containsAny(lower, contextLimitExclusions) {
			continue
		}
		if !matchesAnyPattern(lower, contextLimitPatterns) {
			continue
		}
		if !containsAny(lower, contextLimitCorroborations) {
			continue
		}

		// 上游明确声明了窗口值：精确事实，直接采信
		if declared := declaredContextLimit(lower, estimatedInputTokens); declared > 0 {
			return &ContextLimitSignal{
				MaxInputTokens: declared,
				Source:         config.CompatSourceUpstreamDeclared,
				Evidence:       msg,
			}
		}

		// 只知道"这次太长了"：由被拒绝的请求量反推保守上界
		if estimatedInputTokens > 0 {
			inferred := estimatedInputTokens * rejectedEstimateDiscountNumerator / rejectedEstimateDiscountDenominator
			return &ContextLimitSignal{
				MaxInputTokens: inferred,
				Source:         config.CompatSourceRejectedEstimate,
				Evidence:       msg,
			}
		}
	}
	return nil
}

// estimatedInputTokensForContextLimit 取本次请求的输入 token 估算量。
//
// 优先复用入口已挂到 context 的 RequestProfile：EstTokens 是纯输入估算，不包含
// 输出预留；ContextNeed 可能包含输出预留，反推上限时优先用前者避免低估。
// profile 缺失（未接线入口）时退化为对实际发送体重算一次；两者都拿不到则返回 0，
// 调用方只采信上游明确声明的数值。
func estimatedInputTokensForContextLimit(c *gin.Context, attemptBody []byte) int {
	if c != nil && c.Request != nil {
		if profile, ok := autopilot.RequestProfileFromContext(c.Request.Context()); ok {
			if profile.EstTokens > 0 {
				return profile.EstTokens
			}
			if profile.ContextNeed > 0 {
				return profile.ContextNeed
			}
		}
	}
	return utils.EstimateRequestTokens(attemptBody)
}

// declaredContextLimit 提取上游自报的最大输入 token 数。
//
// 关键防误取：同一句报错里通常同时出现「上限」和「你请求了多少」两个数字
// （"maximum context length is 272000 tokens, however you requested 1050000 tokens"），
// 正则捕获顺序不保证一定拿到前者。因此对候选值做校验：若候选值 >= 本次被拒绝的请求量，
// 它必然不是真实上限（真实上限一定小于被拒量），丢弃该候选继续尝试下一条正则。
// 返回 0 表示未能可靠提取。
func declaredContextLimit(lowerMsg string, estimatedInputTokens int) int {
	for _, pattern := range declaredLimitPatterns {
		match := pattern.FindStringSubmatch(lowerMsg)
		if len(match) < 2 {
			continue
		}
		value, err := strconv.Atoi(match[1])
		if err != nil || value <= 0 {
			continue
		}
		if estimatedInputTokens > 0 && value >= estimatedInputTokens {
			continue
		}
		return value
	}
	return 0
}
