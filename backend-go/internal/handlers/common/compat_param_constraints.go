package common

import (
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/BenedictKing/ccx/internal/config"
)

// 已知上游模型的请求参数硬约束由 model-registry 承载（config.ModelParamConstraints），不再是
// 本文件里的硬编码前缀表：约束数据随 shared/model-registry/ccx_model_registry.json 走 presetstore
// 的定期刷新链路发布，运营者核对厂商文档后更新该 JSON 即可让线上实例在下一个轮询周期生效，
// 不需要重新编译发版。数据来源示例：Moonshot 平台《模型参数参考》记录的 kimi-k3/k2.7-code/k2.6
// temperature/top_p/n/presence_penalty/frequency_penalty 固定值、tool_choice/thinking 约束。
//
// 与"弃用参数自学习"（deprecated_param.go，靠上游 400 事后发现）互补：这里是厂商文档已明确公布的
// 约束，无需先失败一次即可在发送前规避。学习路径仍作为未收录上游的兜底。

// ApplyKnownParamConstraints 按已知厂商约束改写请求体，返回改写后的请求体与被改写项列表。
// 未命中任何约束时原样返回。body 非法 JSON 时不做任何改写（fail-open）。
//
// 三类处理方式按语义损失从小到大：
//   - 固定值采样参数：直接删除，上游用自身默认值，语义无损
//   - thinking 非法配置：改写为唯一合法值，保留思考行为
//   - tool_choice:"required"：降级为 "auto"，保留"可调工具"丢弃"强制"，是最小损失降级
//
// ApplyKnownParamConstraints 按模型注册表里的厂商级参数约束改写请求体，返回改写后的请求体与
// 被改写项列表。constraints 为 nil（未收录该模型或未配置约束）时原样返回。body 非法 JSON 时
// 不做任何改写（fail-open）。
//
// 约束数据来自 config.ResolveUpstreamCapability().Capability.ParamConstraints，随 model-registry
// 走 presetstore 的定期刷新链路（见 shared/model-registry/ccx_model_registry.json），不需要重新
// 编译发版即可更新 Kimi 等厂商的参数约束。
//
// 三类处理方式按语义损失从小到大：
//   - 固定值采样参数：直接删除，上游用自身默认值，语义无损
//   - thinking 非法配置：改写为唯一合法值，保留思考行为
//   - tool_choice:"required"：降级为 "auto"，保留"可调工具"丢弃"强制"，是最小损失降级
func ApplyKnownParamConstraints(body []byte, constraints *config.ModelParamConstraints) ([]byte, []string) {
	if len(body) == 0 || constraints == nil || !gjson.ValidBytes(body) {
		return body, nil
	}

	updated := body
	var applied []string

	for _, param := range constraints.FixedParams {
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

	if constraints.ToolChoiceRequiredUnsupported && gjson.GetBytes(updated, "tool_choice").String() == "required" {
		if next, err := sjson.SetBytes(updated, "tool_choice", "auto"); err == nil {
			updated = next
			applied = append(applied, "tool_choice=auto")
		}
	}

	if len(constraints.ThinkingFixedValue) > 0 {
		if thinking := gjson.GetBytes(updated, "thinking"); thinking.Exists() && !thinkingMatchesFixedValue(thinking, constraints.ThinkingFixedValue) {
			if next, err := sjson.SetBytes(updated, "thinking", constraints.ThinkingFixedValue); err == nil {
				updated = next
				applied = append(applied, fmt.Sprintf("thinking=%v", constraints.ThinkingFixedValue))
			}
		}
	}

	return updated, applied
}

// thinkingMatchesFixedValue 判断请求里的 thinking 是否已经等于约束要求的固定值，
// 避免值本就正确时仍产生一次无意义改写。
func thinkingMatchesFixedValue(thinking gjson.Result, fixed map[string]interface{}) bool {
	for k, v := range fixed {
		if thinking.Get(k).String() != fmt.Sprint(v) {
			return false
		}
	}
	return true
}
