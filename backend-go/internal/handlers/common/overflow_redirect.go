package common

import (
	"context"
	"encoding/json"

	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// 溢出跨模型重定向：provider 与请求级标记。
// 触发与重试逻辑在 multi_channel_failover.go；发送层模型改写与
// encrypted_content 剥离在 upstream_failover.go。

// ccxOverflowRedirectKey gin context 键：本次请求已发生的溢出重定向目标模型。
const ccxOverflowRedirectKey = "ccx.overflow_redirect_model"

// OverflowRedirectProviderFunc 返回可承载 inputTokens 的替代模型（全池按质量档）。
// 由 main.go 在 autopilot Manager 初始化后注册。
type OverflowRedirectProviderFunc func(ctx context.Context, channelKind, model string, inputTokens int) (string, bool)

// overflowRedirectProvider 默认空实现（无重定向能力）；启动期由 SetOverflowRedirectProvider 注入。
var overflowRedirectProvider OverflowRedirectProviderFunc = func(context.Context, string, string, int) (string, bool) {
	return "", false
}

// SetOverflowRedirectProvider 注册溢出重定向模型提供器。
func SetOverflowRedirectProvider(fn OverflowRedirectProviderFunc) {
	overflowRedirectProvider = fn
}

// canOverflowRedirect 判定当前请求是否允许溢出重定向：
// 未重定向过、非 pin 路由（routePrefix/X-Channel 显式指定的渠道不偷换模型）。
func canOverflowRedirect(c *gin.Context, kind scheduler.ChannelKind) bool {
	if _, redirected := c.Get(ccxOverflowRedirectKey); redirected {
		return false
	}
	if c.Param("routePrefix") != "" || c.GetHeader("X-Channel") != "" {
		return false
	}
	return kind != ""
}

// OverflowRedirectTarget 供发送层读取本次请求的溢出重定向目标模型；空 = 未重定向。
func OverflowRedirectTarget(c *gin.Context) string {
	if c == nil {
		return ""
	}
	target, _ := c.Get(ccxOverflowRedirectKey)
	model, _ := target.(string)
	return model
}

// stripResponsesEncryptedContent 剥离 Responses 请求 input items 中 reasoning
// 项的 encrypted_content（保留 summary）。跨模型改写后上游无法解密其他模型
// 产生的密文；保留 summary 是语义损失最小的降级，与 chat 转换器的处理对齐。
// 解析失败时原样返回：剥离是尽力而为，不阻断发送。
func stripResponsesEncryptedContent(body []byte) []byte {
	var req struct {
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	var items []json.RawMessage
	if json.Unmarshal(req.Input, &items) != nil {
		// input 是字符串或其他非数组形态：没有 reasoning items 可剥
		return body
	}
	modified := false
	for i, item := range items {
		var m map[string]json.RawMessage
		if json.Unmarshal(item, &m) != nil {
			continue
		}
		var itemType string
		if raw, ok := m["type"]; ok {
			_ = json.Unmarshal(raw, &itemType)
		}
		if itemType != "reasoning" {
			continue
		}
		if _, has := m["encrypted_content"]; !has {
			continue
		}
		delete(m, "encrypted_content")
		rewritten, err := json.Marshal(m)
		if err != nil {
			continue
		}
		items[i] = rewritten
		modified = true
	}
	if !modified {
		return body
	}
	newItems, err := json.Marshal(items)
	if err != nil {
		return body
	}
	// 用偏移量替换 "input" 数组，避免整体序列化改动键序
	return replaceJSONField(body, "input", newItems)
}

// replaceJSONField 在 JSON 对象顶层替换指定键的值为新原始 JSON。
// 解析失败时返回原 body。
func replaceJSONField(body []byte, field string, value json.RawMessage) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	m[field] = value
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}
