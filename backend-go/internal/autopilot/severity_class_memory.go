package autopilot

import (
	"bytes"

	"github.com/tidwall/gjson"

	"github.com/BenedictKing/ccx/internal/config"
)

// 格式约束型"安全分类"请求的识别与能力记忆（docs/specs/severity-class-capability.md）。
//
// 背景：Claude Code 的安全监控子请求要求模型仅输出 <severity>N</severity>（以
// </severity> 为停止序列、max_tokens 通常仅 64、不带 tools）。自动映射把这类请求
// 换成"基准等价"模型后，等价模型可能完全不遵守格式约束（输出闲聊文本甚至幻觉工具
// 调用），客户端无法解析只能反复重试。这类事实与工具调用能力同构：静态注册表无法
// 表达"能否严格遵守输出格式"，只能按 渠道×Key×模型 从真实流量自学习。

// severityClassStopMarker 安全分类请求的特征停止序列（客户端期望的输出终止标记）。
const severityClassStopMarker = "</severity>"

// severityClassStopMarkerBytes 同上，bytes 形态用于廉价预筛。
var severityClassStopMarkerBytes = []byte(severityClassStopMarker)

// severityClassStopMarkerEscapedBytes 标记的 JSON 转义形态（\u003c/severity\u003e）。
// failover 链路会多次经 Go json.Marshal 重写出站体，< > 默认被转义为 \u003c \u003e，
// 字节级预筛若只认原始形态会漏判，导致能力学习永远不触发。
var severityClassStopMarkerEscapedBytes = []byte(`\u003c/severity\u003e`)

// SeverityClassRequestShape 判定请求体是否为"格式约束型安全分类"请求：
// 停止序列包含 </severity>。覆盖 messages（stop_sequences 数组）与 openai chat
// （stop 字符串/数组）两种形态；协议转换（messages→chat）会保留该标记，
// 因此对转换后的出站请求体同样有效。
//
// 刻意只认停止序列这一个无歧义信号：system 提示词内容属于弱特征（易误判普通
// 安全类问答），而 </severity> 是客户端为精确截断输出而设置的机器标记。
func SeverityClassRequestShape(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	// 廉价预筛：正文不含标记字节序列（原始或 JSON 转义形态）时直接排除，
	// 避免绝大多数请求解析 JSON。
	if !bytes.Contains(body, severityClassStopMarkerBytes) &&
		!bytes.Contains(body, severityClassStopMarkerEscapedBytes) {
		return false
	}
	for _, field := range []string{"stop_sequences", "stop"} {
		value := gjson.GetBytes(body, field)
		if !value.Exists() {
			continue
		}
		switch value.Type {
		case gjson.String:
			if value.String() == severityClassStopMarker {
				return true
			}
		case gjson.JSON:
			for _, item := range value.Array() {
				if item.String() == severityClassStopMarker {
					return true
				}
			}
		}
	}
	return false
}

// learnedSeverityClassUnsupportedLookup 供测试替换的查询入口（同 tool_capability_memory 模式）。
var learnedSeverityClassUnsupportedLookup = func(channelUID, model string) bool {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return false
	}
	return cache.IsSeverityClassUnsupportedForChannelModel(channelUID, model)
}

// learnedSeverityClassUnsupported 返回该渠道-模型是否实测无法完成安全分类请求。
//
// 口径与 learnedToolCallUnsupported 一致：任一 Key 命中即按不支持处理（路由先于选 Key，
// 保守处理），无记忆时 fail-open 返回 false，不影响新渠道。
func learnedSeverityClassUnsupported(channelUID, model string) bool {
	if channelUID == "" || model == "" {
		return false
	}
	return learnedSeverityClassUnsupportedLookup(channelUID, model)
}
