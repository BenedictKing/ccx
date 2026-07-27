package common

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 已知上游模型的请求参数硬约束表。
//
// 与"弃用参数自学习"（deprecated_param.go，靠上游 400 事后发现）互补：这里是厂商文档已明确公布的
// 约束，无需先失败一次即可在发送前规避。学习路径仍作为未收录上游的兜底。
//
// 数据来源：Moonshot 平台《模型参数参考》。kimi-k3 / kimi-k2.7-code 系列 / kimi-k2.6 三者
// temperature/top_p/n/presence_penalty/frequency_penalty 均为固定值不可改，传入其他值直接
// invalid_request_error；moonshot-v1 系列的 presence_penalty/frequency_penalty 可改，故不收录。

// fixedParamModelPrefixes 参数被硬编码固定、传入即报错的模型前缀。
// 用前缀匹配而非精确匹配：厂商会派生 -highspeed、日期后缀等变体，约束一致。
var fixedParamModelPrefixes = []string{
	"kimi-k3",
	"kimi-k2.7-code",
	"kimi-k2.6",
}

// kimiFixedParams 上述模型不可修改的采样参数。
// 剥离后上游使用自身默认值，请求语义（消息、工具、思考预算）不受影响。
var kimiFixedParams = []string{
	"temperature",
	"top_p",
	"n",
	"presence_penalty",
	"frequency_penalty",
}

// requiredToolChoiceUnsupportedPrefixes 不支持 tool_choice:"required" 的模型前缀。
// kimi-k3 支持 auto/none/required；k2.6 与 k2.7-code 传 required 会报错。
var requiredToolChoiceUnsupportedPrefixes = []string{
	"kimi-k2.7-code",
	"kimi-k2.6",
}

// thinkingKeepAllOnlyPrefixes thinking 仅接受 {"type":"enabled","keep":"all"} 的模型前缀。
// k2.7-code 始终开启思考且不可禁用，传 disabled 或其他 keep 值会报错。
var thinkingKeepAllOnlyPrefixes = []string{
	"kimi-k2.7-code",
}

// matchesModelPrefix 判断模型名是否命中任一前缀。
// 模型名可能带渠道前缀（如 "kimi/kimi-k2.6" 或 "ark:kimi-k3"），故用 Contains 而非 HasPrefix。
func matchesModelPrefix(model string, prefixes []string) bool {
	lower := strings.ToLower(model)
	for _, prefix := range prefixes {
		if strings.Contains(lower, prefix) {
			return true
		}
	}
	return false
}

// ApplyKnownParamConstraints 按已知厂商约束改写请求体，返回改写后的请求体与被改写项列表。
// 未命中任何约束时原样返回。body 非法 JSON 时不做任何改写（fail-open）。
//
// 三类处理方式按语义损失从小到大：
//   - 固定值采样参数：直接删除，上游用自身默认值，语义无损
//   - thinking 非法配置：改写为唯一合法值，保留思考行为
//   - tool_choice:"required"：降级为 "auto"，保留"可调工具"丢弃"强制"，是最小损失降级
func ApplyKnownParamConstraints(body []byte, model string) ([]byte, []string) {
	if len(body) == 0 || model == "" || !gjson.ValidBytes(body) {
		return body, nil
	}

	updated := body
	var applied []string

	if matchesModelPrefix(model, fixedParamModelPrefixes) {
		for _, param := range kimiFixedParams {
			if !gjson.GetBytes(updated, param).Exists() {
				continue
			}
			next, err := sjson.DeleteBytes(updated, param)
			if err != nil {
				continue
			}
			updated = next
			applied = append(applied, "-"+param)
		}
	}

	if matchesModelPrefix(model, requiredToolChoiceUnsupportedPrefixes) {
		if gjson.GetBytes(updated, "tool_choice").String() == "required" {
			if next, err := sjson.SetBytes(updated, "tool_choice", "auto"); err == nil {
				updated = next
				applied = append(applied, "tool_choice=auto")
			}
		}
	}

	if matchesModelPrefix(model, thinkingKeepAllOnlyPrefixes) {
		if thinking := gjson.GetBytes(updated, "thinking"); thinking.Exists() {
			if thinking.Get("type").String() != "enabled" || thinking.Get("keep").String() != "all" {
				next, err := sjson.SetBytes(updated, "thinking", map[string]interface{}{
					"type": "enabled",
					"keep": "all",
				})
				if err == nil {
					updated = next
					applied = append(applied, `thinking={"type":"enabled","keep":"all"}`)
				}
			}
		}
	}

	return updated, applied
}
