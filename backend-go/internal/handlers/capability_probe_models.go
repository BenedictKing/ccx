package handlers

import (
	"fmt"
	"strings"
)

// ⚠️ 修改此处时必须同步修改前端 capabilityPlaceholderModels / PLACEHOLDER_MODELS
// 支持多个候选模型，用逗号分隔，按优先级从高到低排列
// 测试时会按顺序分批启动，并汇总所有候选模型的结果
const (
	capabilityProbeModelClaudeFable5 = "claude-fable-5"
	capabilityProbeModelClaudeOpus48 = "claude-opus-4-8"
	capabilityProbeModelMessages     = "claude-fable-5-2,claude-opus-5-5,claude-sonnet-5-5,claude-haiku-5-5"
	capabilityProbeModelChat         = "gpt-6.2-sol,gpt-6.2-astra,gpt-6.1-terra,gpt-6.1-sol,gpt-6-astra,gpt-6-sol,gpt-6-luna,gpt-5.6-sol,codex-auto-review"
	capabilityProbeModelGemini       = "gemini-4-hydrogen,gemini-4-argon,gemini-4-carbon,gemini-3.8-flash,gemini-3.6-flash,gemini-3.5-flash,gemini-3.5-flash-lite,gemini-3.1-pro-preview,gemini-3-pro-preview,gemini-3-flash-preview,gemini-3.1-flash-lite"
	capabilityProbeModelResponses    = capabilityProbeModelChat
)

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
