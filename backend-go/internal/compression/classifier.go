package compression

import (
	"regexp"
	"strings"
)

// CommandCategory 命令输出分类。
type CommandCategory string

const (
	CategoryGit     CommandCategory = "git"
	CategoryTest    CommandCategory = "test"
	CategoryBuild   CommandCategory = "build"
	CategoryPackage CommandCategory = "package"
	CategoryDocker  CommandCategory = "docker"
	CategoryShell   CommandCategory = "shell"
	CategoryError   CommandCategory = "error"
	CategoryGeneric CommandCategory = "generic"
	CategoryUnknown CommandCategory = "unknown"
)

// commandDetector 描述一个命令检测器。
type commandDetector struct {
	category        CommandCategory
	name            string
	commandPatterns []*regexp.Regexp
	contentPatterns []*regexp.Regexp
}

// mustCompile 将模式编译为正则，失败则 panic（仅在 init 时调用）。
func mustCompile(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}

var detectors = []commandDetector{
	// git 类
	{CategoryGit, "git-status",
		[]*regexp.Regexp{mustCompile(`^\s*git\s+status`)},
		[]*regexp.Regexp{mustCompile(`On branch\s`), mustCompile(`^\s*(modified|new file|deleted):`)},
	},
	{CategoryGit, "git-diff",
		[]*regexp.Regexp{mustCompile(`^\s*git\s+diff`)},
		[]*regexp.Regexp{mustCompile(`^diff --git `), mustCompile(`^@@ -\d`)},
	},
	{CategoryGit, "git-log",
		[]*regexp.Regexp{mustCompile(`^\s*git\s+log`)},
		[]*regexp.Regexp{mustCompile(`^commit\s+[0-9a-f]{7,}`)},
	},
	{CategoryGit, "git-branch",
		[]*regexp.Regexp{mustCompile(`^\s*git\s+branch`)},
		[]*regexp.Regexp{mustCompile(`^\*?\s+\w+`)},
	},

	// 测试类
	{CategoryTest, "test-go",
		[]*regexp.Regexp{mustCompile(`^\s*(go\s+test|make\s+test)`)},
		[]*regexp.Regexp{mustCompile(`---\s+(PASS|FAIL|SKIP):`), mustCompile(`^ok\s+`), mustCompile(`FAIL\s+`)},
	},
	{CategoryTest, "test-jest",
		[]*regexp.Regexp{mustCompile(`^\s*(npx|pnpm|bun|npm)\s+(jest|vitest|test)`)},
		[]*regexp.Regexp{mustCompile(`●\s`), mustCompile(`Tests:\s+\d+`), mustCompile(`PASS\s+\S`)},
	},
	{CategoryTest, "test-pytest",
		[]*regexp.Regexp{mustCompile(`^\s*pytest\b`)},
		[]*regexp.Regexp{mustCompile(`^\d+\s+passed`), mustCompile(`FAILED\s+\[`)},
	},
	{CategoryTest, "test-cargo",
		[]*regexp.Regexp{mustCompile(`^\s*cargo\s+test`)},
		[]*regexp.Regexp{mustCompile(`test result:\s+`), mustCompile(`test\s+\S+\s+...\s+(ok|FAILED)`)},
	},

	// 构建类
	{CategoryBuild, "build-typescript",
		[]*regexp.Regexp{mustCompile(`^\s*(tsc|bun\s+build|vite\s+build|webpack\s+build|rollup\s+-c)`)},
		[]*regexp.Regexp{mustCompile(`error\s+TS\d+`), mustCompile(`✓\s+\d+\s+modules?`)},
	},
	{CategoryBuild, "build-eslint",
		[]*regexp.Regexp{mustCompile(`^\s*(eslint|biome\s+lint)`)},
		[]*regexp.Regexp{mustCompile(`\d+\s+error`), mustCompile(`\d+\s+warning`)},
	},

	// 包管理类
	{CategoryPackage, "npm-install",
		[]*regexp.Regexp{mustCompile(`^\s*(npm|pnpm|bun|yarn)\s+(install|add|remove)`)},
		[]*regexp.Regexp{mustCompile(`added\s+\d+`), mustCompile(`removed\s+\d+`), mustCompile(`changed\s+\d+`)},
	},
	{CategoryPackage, "pip",
		[]*regexp.Regexp{mustCompile(`^\s*(pip|pip3|uv\s+pip|poetry)\s+(install|add|remove)`)},
		[]*regexp.Regexp{mustCompile(`Successfully installed`), mustCompile(`Collecting\s`)},
	},

	// Docker 类
	{CategoryDocker, "docker-logs",
		[]*regexp.Regexp{mustCompile(`^\s*docker\s+(logs|compose\s+logs)`)},
		[]*regexp.Regexp{mustCompile(`ERROR`), mustCompile(`WARN`)},
	},
	{CategoryDocker, "docker-ps",
		[]*regexp.Regexp{mustCompile(`^\s*docker\s+(ps|compose\s+ps)`)},
		[]*regexp.Regexp{mustCompile(`CONTAINER ID`)},
	},

	// shell 通用
	{CategoryShell, "shell-ls",
		[]*regexp.Regexp{mustCompile(`^\s*ls\s`)},
		[]*regexp.Regexp{},
	},
	{CategoryShell, "shell-find",
		[]*regexp.Regexp{mustCompile(`^\s*find\s`)},
		[]*regexp.Regexp{},
	},
	{CategoryShell, "shell-grep",
		[]*regexp.Regexp{mustCompile(`^\s*(grep|rg)\s`)},
		[]*regexp.Regexp{},
	},

	// 错误堆栈（通用）
	{CategoryError, "error-stacktrace",
		[]*regexp.Regexp{},
		[]*regexp.Regexp{
			mustCompile(`\bTraceback\s+\(most recent call last\)`),
			mustCompile(`\bpanic:\s`),
			mustCompile(`^\s+at\s+.+:\d+:\d+`),
			mustCompile(`Error:\s`),
			mustCompile(`Exception in thread`),
		},
	},
}

// ClassifyCommand 从文本和可选的命令字符串推断命令输出类别。
//
// 算法：遍历所有 detector，按 commandPatterns 命中（+0.55） + contentPatterns 命中数（每个 +0.25）打分，
// 最高分不超过 1.0；最高分 detector 胜出。未命中则返回 CategoryUnknown。
func ClassifyCommand(text string, command string) (CommandCategory, string, float64) {
	// 解析命令：取最后一段（支持 && / || / ; 分隔）
	resolvedCmd := resolveLastCommand(text, command)

	bestCategory := CategoryUnknown
	bestName := "unknown"
	bestConfidence := 0.0

	for _, d := range detectors {
		confidence := 0.0
		cmdMatched := false
		if resolvedCmd != "" {
			for _, re := range d.commandPatterns {
				if re.MatchString(resolvedCmd) {
					cmdMatched = true
					break
				}
			}
		}
		if cmdMatched {
			confidence += 0.55
		}
		contentHits := 0
		for _, re := range d.contentPatterns {
			if re.MatchString(text) {
				contentHits++
			}
		}
		confidence += float64(contentHits) * 0.25
		if confidence > 1.0 {
			confidence = 1.0
		}
		if confidence > bestConfidence {
			bestConfidence = confidence
			bestCategory = d.category
			bestName = d.name
		}
	}

	if bestConfidence < 0.1 {
		return CategoryUnknown, "unknown", 0.1
	}
	return bestCategory, bestName, bestConfidence
}

// resolveLastCommand 从已知 command 或从文本首行提取命令。
func resolveLastCommand(text string, command string) string {
	if command != "" {
		cmd := strings.TrimSpace(command)
		if idx := strings.LastIndexAny(cmd, "&&||;"); idx >= 0 {
			cmd = strings.TrimSpace(cmd[idx+1:])
		}
		return cmd
	}
	// 从文本前 4 行找 $ 前缀的命令
	lines := strings.SplitN(text, "\n", 5)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "$ ") {
			return strings.TrimPrefix(line, "$ ")
		}
	}
	return ""
}
