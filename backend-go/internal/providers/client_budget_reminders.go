package providers

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/BenedictKing/ccx/internal/utils"
)

// ============== 客户端上下文预算提醒剔除（CC + Codex 统一机制） ==============
//
// 统一语义：客户端（Claude Code / Codex CLI）按「自己认知的模型窗口」向请求注入
// 上下文余量提醒。网关改写实际执行模型（调度器联邦/溢出跨模型改写、跨协议转换）
// 后，这些数字按原模型窗口核算，必然失真；失真数字比没有数字更误导模型决策 →
// 剔除。模型语义未变（原生直通）则保留，此时数字准确，客户端依赖它驱动主动的
// 上下文切换。
//
// 何时剔 / 何时留（按客户端协议 × 换模型层级）：
//   - messages → chat/gemini/responses 转换：无条件剔（跨协议必换模型语义），
//     与 isClaudeCodeSystemHeader 的其他 CC header 一并处理；
//   - messages → Claude 直通 + 调度器联邦/溢出改写模型：剔（failover 改写点）；
//   - responses → chat/claude/gemini 转换：无条件剔（converter 分支）；
//   - 其余（直通且未换模型）：保留。
//
// 剔除范围刻意收窄为「预算提醒」本身：Claude Code 的身份块（You are Claude
// Code...）与 Codex 的 <context_window> 窗口标识（模型靠它调用 history 工具）
// 不在预算清单内——前者由跨协议转换路径的既有清单处理，后者无失真数字、剔除
// 反而破坏记忆层工具调用。

// ccBudgetReminderPatterns 匹配 Claude Code 注入的上下文余量提醒 system 块。
// 前缀匹配：块尾部常附带 output style 提醒，随块一并剔除（与 1c63bbb2 口径一致）。
var ccBudgetReminderPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^<total_tokens>\s*[\d.,]*\s*tokens left</total_tokens>`),
}

// codexBudgetReminderPatterns 匹配 Codex CLI 注入的 token 预算提醒 input item。
// 新版为 developer message 裸文本（无标签）；<token_budget> 包装是旧版持久化
// 兼容形态，同样按整块剔除。
var codexBudgetReminderPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^You have (?:\d+|unknown) tokens left in this context window\.$`),
	regexp.MustCompile(`^<token_budget>[\s\S]*</token_budget>$`),
}

// isCCBudgetReminderText 判断 Claude system 文本是否为预算提醒块。
func isCCBudgetReminderText(text string) bool {
	text = strings.TrimSpace(text)
	for _, pattern := range ccBudgetReminderPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// isCodexBudgetReminderText 判断 Responses input 文本是否为预算提醒。
func isCodexBudgetReminderText(text string) bool {
	text = strings.TrimSpace(text)
	for _, pattern := range codexBudgetReminderPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// StripCCBudgetRemindersFromClaudeSystem 从 messages 协议的 system 字段剔除
// CC 预算提醒块。支持 string 与 [{type:"text",text:...}] 数组两种形态；
// 命中块整块移除（含其 cache_control），未命中时原值返回 changed=false，
// 保证调用方在「模型未变」场景下零改动（保 prompt cache）。
// 全部块被剔除时返回 (nil, true)，调用方应删除 system 键而非置 null。
func StripCCBudgetRemindersFromClaudeSystem(system interface{}) (interface{}, bool) {
	switch v := system.(type) {
	case string:
		if isCCBudgetReminderText(v) {
			return nil, true
		}
		return system, false
	case []interface{}:
		kept := make([]interface{}, 0, len(v))
		changed := false
		for _, raw := range v {
			if block, ok := raw.(map[string]interface{}); ok {
				if blockType, _ := block["type"].(string); blockType == "text" {
					text, _ := block["text"].(string)
					if isCCBudgetReminderText(text) {
						changed = true
						continue
					}
				}
			}
			kept = append(kept, raw)
		}
		if !changed {
			return system, false
		}
		if len(kept) == 0 {
			return nil, true
		}
		return kept, true
	default:
		return system, false
	}
}

// StripCodexBudgetRemindersFromResponsesInput 从 Responses 请求 input 数组剔除
// Codex 预算提醒 item。仅剔除 type=="message" && role=="developer" 且全部
// content 为 input_text、拼接文本命中模式的 item——多用途消息（提醒+其他内容）
// 不命中锚定正则，天然保留。未命中时原值返回 changed=false。
func StripCodexBudgetRemindersFromResponsesInput(input interface{}) (interface{}, bool) {
	items, ok := input.([]interface{})
	if !ok || len(items) == 0 {
		return input, false
	}
	kept := make([]interface{}, 0, len(items))
	changed := false
	for _, raw := range items {
		if isCodexBudgetReminderItem(raw) {
			changed = true
			continue
		}
		kept = append(kept, raw)
	}
	if !changed {
		return input, false
	}
	return kept, true
}

func isCodexBudgetReminderItem(raw interface{}) bool {
	item, ok := raw.(map[string]interface{})
	if !ok {
		return false
	}
	if itemType, _ := item["type"].(string); itemType != "message" {
		return false
	}
	if role, _ := item["role"].(string); role != "developer" {
		return false
	}
	return isCodexBudgetReminderContent(item["content"])
}

func isCodexBudgetReminderContent(content interface{}) bool {
	switch v := content.(type) {
	case string:
		return isCodexBudgetReminderText(v)
	case []interface{}:
		if len(v) == 0 {
			return false
		}
		texts := make([]string, 0, len(v))
		for _, rawBlock := range v {
			block, ok := rawBlock.(map[string]interface{})
			if !ok {
				return false
			}
			if blockType, _ := block["type"].(string); blockType != "input_text" {
				return false
			}
			text, _ := block["text"].(string)
			texts = append(texts, text)
		}
		return isCodexBudgetReminderText(strings.Join(texts, "\n"))
	default:
		return false
	}
}

// StripCCBudgetRemindersFromBody 在 messages 请求体上剥离 CC 预算提醒 system 块。
// 供调度器联邦/溢出跨模型改写点调用（provider 层无法感知该层改写）。
// 解析失败原样返回：剥离是尽力而为，不阻断发送。
func StripCCBudgetRemindersFromBody(body []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return body
	}

	stripped, changed := StripCCBudgetRemindersFromClaudeSystem(data["system"])
	if !changed {
		return body
	}
	if stripped == nil {
		delete(data, "system")
	} else {
		data["system"] = stripped
	}

	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return body
	}
	return newBytes
}

// StripCodexBudgetRemindersFromResponsesBody 在 Responses 请求体上剥离 Codex
// 预算提醒 input item。与 StripCCBudgetRemindersFromBody 同为调度器改写点服务。
func StripCodexBudgetRemindersFromResponsesBody(body []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return body
	}

	stripped, changed := StripCodexBudgetRemindersFromResponsesInput(data["input"])
	if !changed {
		return body
	}
	data["input"] = stripped

	newBytes, err := utils.MarshalJSONNoEscape(data)
	if err != nil {
		return body
	}
	return newBytes
}
