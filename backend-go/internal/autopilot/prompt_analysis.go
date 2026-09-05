package autopilot

import (
	"regexp"
	"strings"

	"github.com/BenedictKing/ccx/internal/utils"
)

// ── 请求 prompt 信号分析（复杂度 + 领域提示）──
//
// 由真实入口（handlers/common AttachAutopilotRequestProfile）与 Route Preview
// 共用，保证预演画像与真实路由画像的特征提取口径一致；禁止在调用方
// 另行维护复制实现。

const maxPromptSignalRunes = 32_768

var promptFileExtensionPattern = regexp.MustCompile(`(?i)\.[a-z][a-z0-9]{0,9}\b`)
var promptSystemReminderPattern = regexp.MustCompile(`(?is)<system-reminder>.*?</system-reminder>`)

// PromptAnalysis 是请求文本信号的分析结论。
type PromptAnalysis struct {
	Complexity  TaskComplexity
	DomainHints DomainHints
}

// AnalyzePromptSignals 从解码后的请求体提取复杂度与领域提示。
// req 为 nil 时返回未知复杂度（与真实入口的空 body 行为一致）。
func AnalyzePromptSignals(req map[string]interface{}, explicitDomain string) PromptAnalysis {
	if req == nil {
		return PromptAnalysis{Complexity: TaskComplexityUnknown}
	}

	systemTexts := make([]string, 0, 4)
	for _, key := range []string{"system", "instructions", "system_instruction", "systemInstruction"} {
		appendPromptText(&systemTexts, req[key])
	}

	userTexts := make([]string, 0, 8)
	messageCount := 0
	collectPromptRoleTexts(req["messages"], &userTexts, &messageCount)
	collectPromptRoleTexts(req["contents"], &userTexts, &messageCount)
	if input, ok := req["input"].(string); ok && strings.TrimSpace(input) != "" {
		before := len(userTexts)
		appendPromptText(&userTexts, input)
		if len(userTexts) > before {
			messageCount++
		}
	} else {
		collectPromptRoleTexts(req["input"], &userTexts, &messageCount)
	}
	if prompt, ok := req["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
		before := len(userTexts)
		appendPromptText(&userTexts, prompt)
		if len(userTexts) > before {
			messageCount++
		}
	}

	toolNames := extractPromptToolNames(req["tools"])
	complexityTexts := userTexts
	if len(complexityTexts) > 3 {
		complexityTexts = complexityTexts[len(complexityTexts)-3:]
	}
	complexityText := joinPromptSignalText(complexityTexts)
	domainText := joinPromptSignalText(append(append([]string{}, systemTexts...), userTexts...))
	hasDiff := strings.Contains(complexityText, "diff --git") || strings.Contains(complexityText, "@@ -")

	return PromptAnalysis{
		Complexity: InferTaskComplexity(ComplexitySignals{
			PromptText:     complexityText,
			MessageCount:   messageCount,
			PromptTokens:   utils.EstimateTokens(complexityText),
			HasDiffContext: hasDiff,
		}),
		DomainHints: DomainHints{
			ExplicitDomain: strings.TrimSpace(explicitDomain),
			SystemPrompt:   domainText,
			ToolNames:      toolNames,
			FileExtensions: extractPromptFileExtensions(domainText),
			HasDiffContext: hasDiff,
		},
	}
}

func collectPromptRoleTexts(value interface{}, texts *[]string, count *int) {
	items, ok := value.([]interface{})
	if !ok {
		return
	}
	for _, item := range items {
		message, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(promptStringValue(message["role"])))
		if role != "" && role != "user" {
			continue
		}
		before := len(*texts)
		if content, exists := message["content"]; exists {
			appendPromptText(texts, content)
		} else if parts, exists := message["parts"]; exists {
			appendPromptText(texts, parts)
		} else {
			appendPromptText(texts, message)
		}
		if len(*texts) > before {
			(*count)++
		}
	}
}

func appendPromptText(texts *[]string, value interface{}) {
	switch typed := value.(type) {
	case string:
		if text := stripPromptHarnessContext(typed); text != "" {
			*texts = append(*texts, text)
		}
	case []interface{}:
		for _, item := range typed {
			appendPromptText(texts, item)
		}
	case map[string]interface{}:
		blockType := strings.ToLower(strings.TrimSpace(promptStringValue(typed["type"])))
		if strings.Contains(blockType, "image") || strings.Contains(blockType, "tool_result") || blockType == "tool" {
			return
		}
		if text, ok := typed["text"].(string); ok {
			appendPromptText(texts, text)
			return
		}
		for _, key := range []string{"content", "parts"} {
			if nested, exists := typed[key]; exists {
				appendPromptText(texts, nested)
			}
		}
	}
}

// stripPromptHarnessContext 去掉 Claude Code 以 user content 注入的通用运行时提醒。
// 这些文本仍占上下文窗口，但不描述当前用户任务，不能参与复杂度判断。
func stripPromptHarnessContext(text string) string {
	return strings.TrimSpace(promptSystemReminderPattern.ReplaceAllString(text, " "))
}

func extractPromptToolNames(value interface{}) []string {
	seen := make(map[string]bool)
	var names []string
	var visit func(interface{})
	visit = func(current interface{}) {
		switch typed := current.(type) {
		case []interface{}:
			for _, item := range typed {
				visit(item)
			}
		case map[string]interface{}:
			if name := strings.TrimSpace(promptStringValue(typed["name"])); name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			for _, key := range []string{"function", "functionDeclarations"} {
				if nested, exists := typed[key]; exists {
					visit(nested)
				}
			}
		}
	}
	visit(value)
	return names
}

func extractPromptFileExtensions(text string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, 8)
	for _, extension := range promptFileExtensionPattern.FindAllString(text, 32) {
		extension = strings.ToLower(extension)
		if !seen[extension] {
			seen[extension] = true
			result = append(result, extension)
		}
	}
	return result
}

func joinPromptSignalText(parts []string) string {
	text := strings.Join(parts, "\n")
	runes := []rune(text)
	if len(runes) <= maxPromptSignalRunes {
		return text
	}
	return string(runes[len(runes)-maxPromptSignalRunes:])
}

func promptStringValue(value interface{}) string {
	text, _ := value.(string)
	return text
}
