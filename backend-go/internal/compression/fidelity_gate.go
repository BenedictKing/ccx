package compression

import (
	"regexp"
	"strings"
)

// FidelityCheckResult 保真门校验结果。
type FidelityCheckResult struct {
	Passed          bool
	Reason          string  // 不通过的原因
	TokenSurvival   float64 // 受保护 token 存活率（0-100）
	JSONKeySurvival float64 // JSON key 存活率（0-100）
	NumDiffHunks    int     // 找到的 diff hunk 数
}

const (
	// MinTokenSurvivalPercent 受保护 token 最低存活率阈值。
	MinTokenSurvivalPercent = 95.0
	// MinJSONKeyPercent JSON key 最低存活率阈值。
	MinJSONKeyPercent = 90.0
)

// 受保护 token 种类的匹配模式。
var protectedTokenPatterns = map[string]*regexp.Regexp{
	"url":         regexp.MustCompile(`https?://[^\s"')]+`),
	"const_case":  regexp.MustCompile(`\b[A-Z][A-Z0-9_]{2,}\b`),
	"env_var":     regexp.MustCompile(`\$[A-Z][A-Z0-9_]{2,}`),
	"version":     regexp.MustCompile(`\bv\d+\.\d+\.\d+[a-zA-Z0-9_-]*\b`),
	"dotted_id":   regexp.MustCompile(`\b[a-z][a-zA-Z0-9]*\.[a-z][a-zA-Z0-9]*(\.[a-z][a-zA-Z0-9]*)*\b`),
	"func_call":   regexp.MustCompile(`\b[a-zA-Z_][a-zA-Z0-9_]*\(`),
	"file_path":   regexp.MustCompile(`(?:^|\s)(?:\.?\.?/)?[\w./-]+\.[a-zA-Z0-9]{1,10}(?:[:\s]|$)`),
	"inline_code": regexp.MustCompile("`[^`\n]{1,200}`"),
}

var (
	// diffHunkPattern 匹配 diff hunk 头：@@ -123,45 +678,90 @@
	diffHunkPattern = regexp.MustCompile(`@@ -\d{1,9}(?:,\d{1,9})? \+\d{1,9}(?:,\d{1,9})? @@`)
	// numericPattern 匹配数字字面量（数字 + 最多 40 位数字/点/逗号）。
	// 有界量词防 ReDoS。
	numericPattern = regexp.MustCompile(`\d[\d.,]{0,40}`)
	// jsonKeyPattern 匹配 JSON key 名（双引号括起，后面跟冒号）。
	jsonKeyPattern = regexp.MustCompile(`"([A-Za-z_$][\w$-]{0,80})"\s*:`)
)

// CheckFidelity 校验压缩输出是否满足保真约束。
// 检查项（按从便宜到贵的顺序，任一失败即返回不通过）：
//  1. Diff hunk 完整：每个唯一 hunk 头必须存活（100%）
//  2. 数字字面量完整：每个唯一数字必须存活（100%）
//  3. 受保护 token 存活率 ≥ 95%
//  4. JSON key 存活率 ≥ 90%
//
// fail-open：内部 panic 由调用方 recover，此处不捕获。
func CheckFidelity(original, compressed string) FidelityCheckResult {
	// 1. Diff hunk 完整性（100%）
	origHunks := diffHunkPattern.FindAllString(original, -1)
	if len(origHunks) > 0 {
		hunkSet := make(map[string]struct{}, len(origHunks))
		for _, h := range origHunks {
			hunkSet[h] = struct{}{}
		}
		for hunk := range hunkSet {
			if !strings.Contains(compressed, hunk) {
				return FidelityCheckResult{
					Passed:          false,
					Reason:          "diff hunk missing: " + hunk,
					NumDiffHunks:    len(hunkSet),
					TokenSurvival:   100,
					JSONKeySurvival: 100,
				}
			}
		}
	}

	// 2. 数字字面量完整性（100%）
	origNums := numericPattern.FindAllString(original, -1)
	if len(origNums) > 0 {
		numSet := make(map[string]struct{}, len(origNums))
		for _, n := range origNums {
			numSet[n] = struct{}{}
		}
		for num := range numSet {
			if !strings.Contains(compressed, num) {
				return FidelityCheckResult{
					Passed:          false,
					Reason:          "numeric literal missing: " + num,
					NumDiffHunks:    len(origHunks),
					TokenSurvival:   100,
					JSONKeySurvival: 100,
				}
			}
		}
	}

	// 3. 受保护 token 存活率
	tokenSurvival := calcProtectedTokenSurvival(original, compressed)
	if tokenSurvival < MinTokenSurvivalPercent {
		return FidelityCheckResult{
			Passed:          false,
			Reason:          "protected token survival too low",
			NumDiffHunks:    len(origHunks),
			TokenSurvival:   tokenSurvival,
			JSONKeySurvival: 100,
		}
	}

	// 4. JSON key 存活率
	jsonKeySurvival := calcJSONKeySurvival(original, compressed)
	if jsonKeySurvival < MinJSONKeyPercent {
		return FidelityCheckResult{
			Passed:          false,
			Reason:          "JSON key survival too low",
			NumDiffHunks:    len(origHunks),
			TokenSurvival:   tokenSurvival,
			JSONKeySurvival: jsonKeySurvival,
		}
	}

	return FidelityCheckResult{
		Passed:          true,
		NumDiffHunks:    len(origHunks),
		TokenSurvival:   tokenSurvival,
		JSONKeySurvival: jsonKeySurvival,
	}
}

// calcProtectedTokenSurvival 计算受保护 token 的存活率。
func calcProtectedTokenSurvival(original, compressed string) float64 {
	origTokens := make(map[string]struct{})
	for _, re := range protectedTokenPatterns {
		matches := re.FindAllString(original, -1)
		for _, m := range matches {
			origTokens[m] = struct{}{}
		}
	}
	if len(origTokens) == 0 {
		return 100.0
	}

	survived := 0
	for tok := range origTokens {
		if strings.Contains(compressed, tok) {
			survived++
		}
	}
	return float64(survived) / float64(len(origTokens)) * 100.0
}

// calcJSONKeySurvival 计算 JSON key 名的存活率。
func calcJSONKeySurvival(original, compressed string) float64 {
	origMatches := jsonKeyPattern.FindAllStringSubmatch(original, -1)
	if len(origMatches) == 0 {
		return 100.0
	}

	origKeys := make(map[string]struct{}, len(origMatches))
	for _, m := range origMatches {
		if len(m) >= 2 {
			origKeys[m[1]] = struct{}{}
		}
	}
	if len(origKeys) == 0 {
		return 100.0
	}

	// 在压缩后文本中查找
	survived := 0
	for key := range origKeys {
		// 用 "key" 形式查找（带引号包围，减少误判）
		if strings.Contains(compressed, `"`+key+`"`) {
			survived++
		}
	}
	return float64(survived) / float64(len(origKeys)) * 100.0
}
