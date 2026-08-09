package handlers

import (
	"fmt"
	"strings"
)

// ⚠️ 修改此处时必须同步修改前端 frontend/src/App.vue 的 capabilityPlaceholderModels
// 支持多个候选模型，用逗号分隔，按优先级从高到低排列
// 测试时会按顺序分批启动，并汇总所有候选模型的结果
const (
	capabilityProbeModelClaudeFable5 = "claude-fable-5"
	capabilityProbeModelClaudeOpus48 = "claude-opus-4-8"
	capabilityProbeModelMessages     = capabilityProbeModelClaudeFable5 + "," + capabilityProbeModelClaudeOpus48 + ",claude-opus-4-7,claude-opus-4-6,claude-sonnet-4-6,claude-sonnet-4-5-20250929,claude-haiku-4-5-20251001"
	capabilityProbeModelChat         = "gpt-5.5,gpt-5.4,gpt-5.6-sol,gpt-5.6-terra,gpt-5.6-luna,gpt-5.4-mini,codex-auto-review"
	capabilityProbeModelGemini       = "gemini-3.5-flash,gemini-3.1-pro-preview,gemini-3-pro-preview,gemini-3-flash-preview,gemini-3.1-flash-lite"
	capabilityProbeModelResponses    = "gpt-5.5,gpt-5.4,gpt-5.6-sol,gpt-5.6-terra,gpt-5.6-luna,gpt-5.4-mini,codex-auto-review"
)

// capabilityProbeSchemaVersion 能力探测特征集版本。
// 当探测模型列表、prompts、参数约束或协议头发生变化时必须递增，
// 以使缓存/执行键失效并避免新旧探测特征混用。
const capabilityProbeSchemaVersion = 1

var capabilityProbeModels = map[string]string{
	"messages":  capabilityProbeModelMessages,
	"chat":      capabilityProbeModelChat,
	"gemini":    capabilityProbeModelGemini,
	"responses": capabilityProbeModelResponses,
}

// getCapabilityProbeModels 获取协议的候选模型列表（按优先级排序）
func getCapabilityProbeModels(protocol string) ([]string, error) {
	modelsStr, ok := capabilityProbeModels[protocol]
	if !ok {
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}

	// 按逗号分隔，去除空白
	models := strings.Split(modelsStr, ",")
	result := make([]string, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m != "" {
			result = append(result, m)
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no models configured for protocol: %s", protocol)
	}

	return result, nil
}

// getCapabilityProbeModel 获取协议的首选模型（兼容旧接口）
func getCapabilityProbeModel(protocol string) (string, error) {
	models, err := getCapabilityProbeModels(protocol)
	if err != nil {
		return "", err
	}
	return models[0], nil
}
