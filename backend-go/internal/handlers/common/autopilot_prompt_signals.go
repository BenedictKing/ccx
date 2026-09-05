package common

import (
	"github.com/BenedictKing/ccx/internal/autopilot"
)

// analyzeAutopilotPrompt 从已解码的请求体提取复杂度与领域提示。
// 实现已抽取到 autopilot.AnalyzePromptSignals（与 Route Preview 共用，
// 保证预演画像与真实路由画像的特征提取口径一致），此处仅保留薄委托。
func analyzeAutopilotPrompt(req map[string]interface{}, explicitDomain string) autopilot.PromptAnalysis {
	return autopilot.AnalyzePromptSignals(req, explicitDomain)
}
