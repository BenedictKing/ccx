package compression

import (
	"regexp"
	"strings"
)

// FilterRule 描述一个命令输出 filter。
type FilterRule struct {
	Category CommandCategory
	Name     string
	// StripPatterns 匹配的行被移除
	StripPatterns []*regexp.Regexp
	// KeepPatterns 非空时，只有匹配的行被保留（优先级高于 strip）
	KeepPatterns []*regexp.Regexp
	// CollapsePatterns 匹配的连续重复行被折叠为一行
	CollapsePatterns []*regexp.Regexp
	// PriorityPatterns 匹配的行在截断时优先保留（错误/警告/摘要）
	PriorityPatterns []*regexp.Regexp
	// Replace 替换规则 {pattern, replacement}，先于 strip/keep 执行
	Replace []replaceRule
	// StripAnsi 是否剥离 ANSI 转义码
	StripAnsi bool
	// Deduplicate 是否做行级去重（截断前）
	Deduplicate bool
	// MaxLines 最大保留行数（硬截断）
	MaxLines int
	// HeadLines 头部保留行数
	HeadLines int
	// TailLines 尾部保留行数
	TailLines int
	// MaxChars 单文本块最大字符数
	MaxChars int
}

type replaceRule struct {
	pattern     *regexp.Regexp
	replacement string
}

// ansiRegex 匹配 ANSI 转义序列。
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// filters 是内置 filter 表，按 Category 索引。
var filters = map[CommandCategory]FilterRule{
	CategoryGit: {
		Category:  CategoryGit,
		Name:      "git",
		StripAnsi: true,
		StripPatterns: []*regexp.Regexp{
			regexp.MustCompile(`^index\s`),
			regexp.MustCompile(`^--- a/`),
			regexp.MustCompile(`^\+\+\+ b/`),
			regexp.MustCompile(`^diff --git\s`), // 保留 @@ 和实际变更行，但 diff --git 头也可以保留
		},
		CollapsePatterns: []*regexp.Regexp{
			regexp.MustCompile(`^\s*$`),
		},
		PriorityPatterns: []*regexp.Regexp{
			regexp.MustCompile(`^@@\s`),
			regexp.MustCompile(`^[+-]`),
			regexp.MustCompile(`^On branch\s`),
		},
		MaxLines:  180,
		HeadLines: 40,
		TailLines: 50,
		MaxChars:  12000,
	},

	CategoryTest: {
		Category:  CategoryTest,
		Name:      "test",
		StripAnsi: true,
		KeepPatterns: []*regexp.Regexp{
			regexp.MustCompile(`FAIL`),
			regexp.MustCompile(`●\s`),
			regexp.MustCompile(`Expected:`),
			regexp.MustCompile(`Received:`),
			regexp.MustCompile(`^\s+at\s+.+:\d+`),
			regexp.MustCompile(`Tests:\s+\d+`),
			regexp.MustCompile(`Test Suites:\s+\d+`),
			regexp.MustCompile(`test result:`),
			regexp.MustCompile(`^ok\s+`),
			regexp.MustCompile(`^---\s+(PASS|FAIL|SKIP):`),
			regexp.MustCompile(`passed|failed|skipped`),
		},
		CollapsePatterns: []*regexp.Regexp{
			regexp.MustCompile(`^\s+at\s`), // 堆栈帧折叠
			regexp.MustCompile(`^\s*$`),
		},
		PriorityPatterns: []*regexp.Regexp{
			regexp.MustCompile(`FAIL`),
			regexp.MustCompile(`●\s`),
			regexp.MustCompile(`Error:\s`),
		},
		MaxLines:  140,
		HeadLines: 24,
		TailLines: 50,
		MaxChars:  12000,
	},

	CategoryBuild: {
		Category:  CategoryBuild,
		Name:      "build",
		StripAnsi: true,
		KeepPatterns: []*regexp.Regexp{
			regexp.MustCompile(`error`),
			regexp.MustCompile(`warning`),
			regexp.MustCompile(`ERROR`),
			regexp.MustCompile(`WARN`),
			regexp.MustCompile(`✓`),
			regexp.MustCompile(`✗`),
			regexp.MustCompile(`built in`),
			regexp.MustCompile(`Compiled successfully`),
			regexp.MustCompile(`Failed to compile`),
		},
		CollapsePatterns: []*regexp.Regexp{
			regexp.MustCompile(`^\s*$`),
		},
		PriorityPatterns: []*regexp.Regexp{
			regexp.MustCompile(`error`),
			regexp.MustCompile(`ERROR`),
			regexp.MustCompile(`Failed to compile`),
		},
		MaxLines:  100,
		HeadLines: 20,
		TailLines: 40,
		MaxChars:  10000,
	},

	CategoryPackage: {
		Category:  CategoryPackage,
		Name:      "package",
		StripAnsi: true,
		KeepPatterns: []*regexp.Regexp{
			regexp.MustCompile(`added|removed|changed|upgraded|downgraded`),
			regexp.MustCompile(`packages?`),
			regexp.MustCompile(`audited`),
			regexp.MustCompile(`vulnerabilit`),
			regexp.MustCompile(`WARN`),
			regexp.MustCompile(`ERR!`),
			regexp.MustCompile(`Successfully installed`),
			regexp.MustCompile(`Collecting\s`),
		},
		StripPatterns: []*regexp.Regexp{
			regexp.MustCompile(`^[│├└─\s]+$`), // 进度条/树形边框
		},
		PriorityPatterns: []*regexp.Regexp{
			regexp.MustCompile(`vulnerabilit`),
			regexp.MustCompile(`ERR!`),
			regexp.MustCompile(`WARN`),
		},
		MaxLines:  80,
		HeadLines: 10,
		TailLines: 30,
		MaxChars:  8000,
	},

	CategoryDocker: {
		Category:  CategoryDocker,
		Name:      "docker",
		StripAnsi: true,
		KeepPatterns: []*regexp.Regexp{
			regexp.MustCompile(`ERROR`),
			regexp.MustCompile(`WARN`),
			regexp.MustCompile(`Exception`),
			regexp.MustCompile(`Traceback`),
			regexp.MustCompile(`failed`),
			regexp.MustCompile(`listening on`),
			regexp.MustCompile(`started`),
			regexp.MustCompile(`CONTAINER`),
		},
		CollapsePatterns: []*regexp.Regexp{
			regexp.MustCompile(`^\s*$`),
		},
		PriorityPatterns: []*regexp.Regexp{
			regexp.MustCompile(`ERROR`),
			regexp.MustCompile(`WARN`),
			regexp.MustCompile(`failed`),
		},
		MaxLines:  120,
		HeadLines: 20,
		TailLines: 60,
		MaxChars:  12000,
	},

	CategoryShell: {
		Category:  CategoryShell,
		Name:      "shell",
		StripAnsi: true,
		CollapsePatterns: []*regexp.Regexp{
			regexp.MustCompile(`^\s*$`),
		},
		PriorityPatterns: []*regexp.Regexp{
			regexp.MustCompile(`error`),
			regexp.MustCompile(`failed`),
			regexp.MustCompile(`exception`),
		},
		MaxLines:  120,
		HeadLines: 35,
		TailLines: 45,
		MaxChars:  12000,
	},

	CategoryError: {
		Category:  CategoryError,
		Name:      "error",
		StripAnsi: true,
		KeepPatterns: []*regexp.Regexp{
			regexp.MustCompile(`Error:`),
			regexp.MustCompile(`Exception`),
			regexp.MustCompile(`Traceback`),
			regexp.MustCompile(`^\s+at\s`),
			regexp.MustCompile(`panic:`),
			regexp.MustCompile(`Caused by:`),
		},
		CollapsePatterns: []*regexp.Regexp{
			regexp.MustCompile(`^\s+at\s`),
		},
		PriorityPatterns: []*regexp.Regexp{
			regexp.MustCompile(`Error:`),
			regexp.MustCompile(`Exception`),
			regexp.MustCompile(`panic:`),
		},
		MaxLines:  80,
		HeadLines: 20,
		TailLines: 30,
		MaxChars:  8000,
	},

	CategoryGeneric: {
		Category:  CategoryGeneric,
		Name:      "generic",
		StripAnsi: true,
		StripPatterns: []*regexp.Regexp{
			regexp.MustCompile(`^\s*$`),
		},
		PriorityPatterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)error`),
			regexp.MustCompile(`(?i)failed`),
			regexp.MustCompile(`(?i)exception`),
			regexp.MustCompile(`(?i)traceback`),
		},
		MaxLines:  120,
		HeadLines: 35,
		TailLines: 45,
		MaxChars:  12000,
	},
}

// GetFilter 返回指定分类的 filter；未知分类返回 generic filter。
func GetFilter(category CommandCategory) FilterRule {
	if f, ok := filters[category]; ok {
		return f
	}
	return filters[CategoryGeneric]
}

// ApplyFilter 对文本应用 filter 规则，返回过滤后的文本和是否修改。
func ApplyFilter(text string, filter FilterRule) (string, bool) {
	if text == "" {
		return "", false
	}
	original := text

	// 1. 剥离 ANSI
	if filter.StripAnsi {
		text = ansiRegex.ReplaceAllString(text, "")
	}

	lines := strings.Split(text, "\n")

	// 2. 替换规则
	if len(filter.Replace) > 0 {
		for i, line := range lines {
			for _, r := range filter.Replace {
				line = r.pattern.ReplaceAllString(line, r.replacement)
			}
			lines[i] = line
		}
	}

	// 3. strip patterns
	if len(filter.StripPatterns) > 0 {
		filtered := lines[:0]
		for _, line := range lines {
			strip := false
			for _, re := range filter.StripPatterns {
				if re.MatchString(line) {
					strip = true
					break
				}
			}
			if !strip {
				filtered = append(filtered, line)
			}
		}
		lines = filtered
	}

	// 4. keep patterns（非空时只保留匹配行）
	if len(filter.KeepPatterns) > 0 {
		filtered := make([]string, 0, len(lines))
		for _, line := range lines {
			keep := false
			for _, re := range filter.KeepPatterns {
				if re.MatchString(line) {
					keep = true
					break
				}
			}
			if keep {
				filtered = append(filtered, line)
			}
		}
		lines = filtered
	}

	// 5. collapse patterns（连续相同行折叠为一行）
	if len(filter.CollapsePatterns) > 0 {
		collapsed := make([]string, 0, len(lines))
		for _, line := range lines {
			if len(collapsed) == 0 {
				collapsed = append(collapsed, line)
				continue
			}
			last := collapsed[len(collapsed)-1]
			// 判断当前行是否匹配 collapse pattern 且与上一行相同
			matchesCollapse := false
			for _, re := range filter.CollapsePatterns {
				if re.MatchString(line) {
					matchesCollapse = true
					break
				}
			}
			if matchesCollapse && line == last {
				continue // 折叠
			}
			collapsed = append(collapsed, line)
		}
		lines = collapsed
	}

	// 6. 去重（Deduplicate 选项）
	if filter.Deduplicate {
		seen := make(map[string]struct{})
		deduped := make([]string, 0, len(lines))
		for _, line := range lines {
			key := strings.TrimSpace(line)
			if key == "" {
				deduped = append(deduped, line)
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			deduped = append(deduped, line)
		}
		lines = deduped
	}

	// 7. 智能截断（head + priority + tail）
	if filter.MaxLines > 0 && len(lines) > filter.MaxLines {
		lines = smartTruncate(lines, filter)
	}

	result := strings.Join(lines, "\n")

	// 8. 总字符数硬限制
	if filter.MaxChars > 0 && len(result) > filter.MaxChars {
		// 保留头部 55% + 尾部 45%
		headChars := int(float64(filter.MaxChars) * 0.55)
		tailChars := filter.MaxChars - headChars
		if headChars < 0 || tailChars < 0 {
			result = result[:filter.MaxChars]
		} else {
			result = result[:headChars] + "\n[compression: truncated by chars]\n" + result[len(result)-tailChars:]
		}
	}

	return result, result != original
}

// smartTruncate 智能截断：保留 head + 所有 priority 行 + tail，中间插入省略标记。
func smartTruncate(lines []string, filter FilterRule) []string {
	head := filter.HeadLines
	tail := filter.TailLines
	if head <= 0 {
		head = 20
	}
	if tail <= 0 {
		tail = 20
	}
	if head+tail >= len(lines) {
		return lines
	}

	// 收集 priority 行（在 head+tail 范围之外的）
	prioritySet := make(map[int]struct{})
	if len(filter.PriorityPatterns) > 0 {
		for i := head; i < len(lines)-tail; i++ {
			for _, re := range filter.PriorityPatterns {
				if re.MatchString(lines[i]) {
					prioritySet[i] = struct{}{}
					break
				}
			}
		}
	}

	result := make([]string, 0, filter.MaxLines)
	// head
	result = append(result, lines[:head]...)

	// priority 行（按顺序加入，预算内）
	budget := filter.MaxLines - head - tail
	if budget < 0 {
		budget = 0
	}
	priorityAdded := 0
	lastPriorityIdx := head - 1
	for i := range prioritySet {
		if i < head || i >= len(lines)-tail {
			continue
		}
		if priorityAdded >= budget {
			break
		}
		// 加入（按原顺序）
		// 用一个简单的选择：只保留前 budget 个 priority 行
		_ = lastPriorityIdx
		priorityAdded++
	}

	// 改为更简单的实现：收集所有 priority 行索引，排序，按预算选取
	priorityIndices := make([]int, 0, len(prioritySet))
	for i := range prioritySet {
		priorityIndices = append(priorityIndices, i)
	}
	// 选择前 budget 个（按升序，即越靠前的 priority 行越优先保留）
	// 但我们想分布均匀一些，简单起见按顺序加入
	// 简化：直接把 priority 行加入 result（在 head 和 tail 之间），控制总数不超 MaxLines
	// 为了简化，直接重建：head + 中间 priority 行(按预算) + "[truncated]" + tail
	middleStart := head
	middleEnd := len(lines) - tail

	// 收集中间 priority 行并排序
	var priLines []int
	for i := middleStart; i < middleEnd; i++ {
		if _, ok := prioritySet[i]; ok {
			priLines = append(priLines, i)
		}
	}
	// 预算内保留
	if len(priLines) > budget {
		priLines = priLines[:budget]
	}

	// 组装
	result = lines[:head]
	if len(priLines) > 0 {
		// 如果 priority 行不是紧跟 head，加省略
		if priLines[0] > head {
			result = append(result, "[compression: truncated middle]")
		}
		for _, idx := range priLines {
			result = append(result, lines[idx])
		}
	} else if middleEnd > head {
		result = append(result, "[compression: truncated middle]")
	}

	// tail
	result = append(result, lines[middleEnd:]...)

	return result
}
