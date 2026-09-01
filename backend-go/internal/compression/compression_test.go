package compression

import (
	"strings"
	"testing"
)

func TestFidelityGate_DiffHunks(t *testing.T) {
	cases := []struct {
		name     string
		original string
		modified string
		wantPass bool
	}{
		{
			name: "all_hunks_survive",
			original: `@@ -1,5 +1,6 @@
+added line
 unchanged
@@ -100,20 +110,25 @@
+another new line
 still here`,
			modified: `@@ -1,5 +1,6 @@
+added line
@@ -100,20 +110,25 @@
+another new line`,
			wantPass: true,
		},
		{
			name: "hunk_missing",
			original: `@@ -1,5 +1,6 @@
foo
@@ -10,3 +11,4 @@
bar`,
			modified: `@@ -1,5 +1,6 @@
foo`,
			wantPass: false,
		},
		{
			name:     "no_hunks_pass",
			original: `just some text`,
			modified: `some text`,
			wantPass: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := CheckFidelity(tc.original, tc.modified)
			if result.Passed != tc.wantPass {
				t.Errorf("Passed=%v, want %v; reason=%q", result.Passed, tc.wantPass, result.Reason)
			}
		})
	}
}

func TestFidelityGate_NumericIntegrity(t *testing.T) {
	cases := []struct {
		name     string
		original string
		modified string
		wantPass bool
	}{
		{
			name:     "numbers_survive",
			original: "count: 1234, ratio: 0.95, value: 1000000",
			modified: "count: 1234, ratio: 0.95, value: 1000000",
			wantPass: true,
		},
		{
			name:     "number_lost",
			original: "total: 9999 and 42",
			modified: "total: 9999 and removed",
			wantPass: false,
		},
		{
			name:     "number_comma_form_survives",
			original: "value: 1,234,567.89 ok",
			modified: "value: 1,234,567.89 ok",
			wantPass: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := CheckFidelity(tc.original, tc.modified)
			if result.Passed != tc.wantPass {
				t.Errorf("Passed=%v, want %v; reason=%q", result.Passed, tc.wantPass, result.Reason)
			}
		})
	}
}

func TestCalcJSONKeySurvival(t *testing.T) {
	original := `{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7, "h": 8, "i": 9, "j": 10}`
	survival := calcJSONKeySurvival(original, original)
	if survival < 99.9 {
		t.Errorf("100%% match expected, got %.1f%%", survival)
	}

	// 只保留 2/10 keys
	modified := `{"a": 1, "b": 2}`
	survival = calcJSONKeySurvival(original, modified)
	if survival > 25.0 || survival < 15.0 {
		t.Errorf("expected ~20%% survival (2/10), got %.1f%%", survival)
	}
}

func TestCalcProtectedTokenSurvival(t *testing.T) {
	original := "https://example.com/path and MAX_VALUE and v1.2.3 and $HOME and func_call()"
	survival := calcProtectedTokenSurvival(original, original)
	if survival < 99.9 {
		t.Errorf("100%% match expected, got %.1f%%", survival)
	}

	// 去掉 URL 和几个 token
	modified := "some plain text without special tokens"
	survival = calcProtectedTokenSurvival(original, modified)
	if survival > 50.0 {
		t.Errorf("expected low survival, got %.1f%%", survival)
	}
}

func TestFidelityGate_ProtectedTokenSurvival(t *testing.T) {
	// 端到端测试：构造一个全部 token 都保留的场景 → 通过
	original := "Error: failed to connect to https://api.example.com/v2/endpoint code=ERROR_TIMEOUT"
	modified := "Error: failed to connect to https://api.example.com/v2/endpoint code=ERROR_TIMEOUT"
	result := CheckFidelity(original, modified)
	if !result.Passed {
		t.Errorf("identical text should pass, reason=%q", result.Reason)
	}

	// 端到端测试：去掉大部分受保护 token → 失败
	original = "https://api.example.com MAX_RETRY_COUNT v2.3.1 utils.DoWork()"
	modified = "plain text with no significant tokens here just words"
	result = CheckFidelity(original, modified)
	if result.Passed {
		t.Error("most tokens lost should fail fidelity check")
	}
}

func TestFidelityGate_JSONKeys(t *testing.T) {
	// 端到端：全部保留 → 通过
	original := `{"key1": "val1", "key2": "val2"}`
	result := CheckFidelity(original, original)
	if !result.Passed {
		t.Errorf("identical JSON should pass, reason=%q", result.Reason)
	}
}

func TestFidelityGate_EmptyInputs(t *testing.T) {
	result := CheckFidelity("", "")
	if !result.Passed {
		t.Error("empty strings should pass")
	}
	if result.TokenSurvival != 100.0 {
		t.Errorf("expected 100%% token survival, got %.1f%%", result.TokenSurvival)
	}
}

func TestInflationGuard(t *testing.T) {
	// 构造一个 tool_result 内容很短，压缩后反而更大（被加了截断标记等）
	body := []byte(`{
  "model": "claude-3",
  "messages": [
    {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "1", "content": "short"}]}
  ]
}`)

	plan := Plan{
		Enabled:           true,
		Level:             LevelAggressive,
		MaxToolResults:    10,
		MaxBytesPerResult: 1024,
	}

	result, err := CompressRequestBody(body, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 短内容通常不会被压缩（没有 strip 空间），所以 Compressed=false 是合理的
	// 重要的是：如果 Compressed=false 且 FallbackReason="inflation"，说明膨胀回退生效
	if result.Compressed && result.SavingsPercent < 0 {
		t.Error("compressed result should not have negative savings")
	}
}

func TestFailOpen_InvalidJSON(t *testing.T) {
	body := []byte(`not valid json {{{`)
	plan := Plan{Enabled: true, Level: LevelStandard}

	result, err := CompressRequestBody(body, plan)
	if err != nil {
		t.Fatalf("fail-open should not return error: %v", err)
	}
	if result.Compressed {
		t.Error("invalid JSON should not be compressed (fail-open)")
	}
}

func TestFailOpen_PanicRecovery(t *testing.T) {
	// 深度嵌套可能触发大内存，但我们验证不会 panic
	body := []byte(`{"model": "x", "messages": [{"role": "user", "content": []}]}`)
	plan := Plan{Enabled: true, Level: LevelStandard}

	result, err := CompressRequestBody(body, plan)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}
	_ = result
	// 不 panic 即通过
}

func TestCompressRequestBody_SkipsLastMessage(t *testing.T) {
	body := []byte(`{
  "model": "claude-3",
  "messages": [
    {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "tool1", "content": "tool result 1\nline2\nline3\nline4\nline5"}]},
    {"role": "assistant", "content": [{"type": "text", "text": "reply"}]},
    {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "tool2", "content": "LAST MESSAGE TOOL RESULT"}]},
    {"role": "user", "content": "final question"}
  ]
}`)

	plan := Plan{
		Enabled:           true,
		Level:             LevelAggressive,
		MaxToolResults:    10,
		MaxBytesPerResult: 1024,
	}

	result, err := CompressRequestBody(body, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 最后一条消息（final question）不应被触碰
	bodyStr := string(result.Body)
	if !strings.Contains(bodyStr, "final question") {
		t.Error("last message should not be modified")
	}
}

func TestCompressRequestBody_NoToolResults(t *testing.T) {
	body := []byte(`{
  "model": "claude-3",
  "messages": [
    {"role": "user", "content": "hello"},
    {"role": "assistant", "content": "hi"}
  ]
}`)

	plan := Plan{Enabled: true, Level: LevelStandard}
	result, err := CompressRequestBody(body, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Compressed {
		t.Error("no tool_results should not be compressed")
	}
}

func TestClassifier_CommandDetection(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		command  string
		wantCat  CommandCategory
		wantName string
	}{
		{
			name:    "git_status",
			text:    "On branch main\nYour branch is up to date.\n\nnothing to commit, working tree clean",
			command: "git status",
			wantCat: CategoryGit,
		},
		{
			name:    "go_test",
			text:    "ok   github.com/example/pkg 0.5s\n--- FAIL: TestSomething",
			command: "go test ./...",
			wantCat: CategoryTest,
		},
		{
			name:    "npm_install",
			text:    "added 150 packages in 3s\n\n3 packages are looking for funding",
			command: "npm install",
			wantCat: CategoryPackage,
		},
		{
			name:    "docker_logs",
			text:    "ERROR: connection refused\nWARN: retrying...\nINFO: starting up",
			command: "docker logs mycontainer",
			wantCat: CategoryDocker,
		},
		{
			name:    "error_traceback",
			text:    "Traceback (most recent call last):\n  File \"test.py\", line 1, in <module>\n    raise ValueError(\"oops\")\nValueError: oops",
			command: "",
			wantCat: CategoryError,
		},
		{
			name:    "unknown_plain_text",
			text:    "The quick brown fox jumps over the lazy dog.",
			command: "",
			wantCat: CategoryUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat, name, conf := ClassifyCommand(tc.text, tc.command)
			if cat != tc.wantCat {
				t.Errorf("category=%q, want %q (detector=%q, confidence=%.2f)", cat, tc.wantCat, name, conf)
			}
		})
	}
}

func TestApplyFilter_RemovesNoise(t *testing.T) {
	gitDiff := `diff --git a/file.go b/file.go
index abc1234..def5678 100644
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 line1
 line2
+newline
 line3
@@ -10,2 +11,3 @@
 old
+inserted
`

	filter := GetFilter(CategoryGit)
	result, modified := ApplyFilter(gitDiff, filter)
	if !modified {
		t.Fatal("expected modification")
	}
	// hunk 头应保留
	if !strings.Contains(result, "@@ -1,3 +1,4 @@") {
		t.Error("hunk header should be preserved")
	}
	// diff --git 和 index 行应被剥离
	if strings.Contains(result, "diff --git") {
		t.Error("diff --git header should be stripped")
	}
}

func TestApplyFilter_GenericKeepsErrors(t *testing.T) {
	generic := `line 1 of output
line 2 of output
Error: something went wrong
line 4: more output
line 5: ok
line 6: also ok
line 7: fine
line 8: good
line 9: nice
line 10: great`

	filter := GetFilter(CategoryGeneric)
	filter.MaxLines = 5
	filter.HeadLines = 1
	filter.TailLines = 1
	result, _ := ApplyFilter(generic, filter)

	// Error 行应作为 priority 被保留
	if !strings.Contains(result, "Error: something went wrong") {
		t.Error("error line should be preserved as priority")
	}
}

func TestPlan_Resolve(t *testing.T) {
	cases := []struct {
		name     string
		header   string
		scenario string
		global   bool
		channel  bool
		wantOn   bool
		wantSrc  string
	}{
		{"default_off", "", "", false, false, false, "default_off"},
		{"global_on", "", "", true, false, true, "global"},
		{"channel_on", "", "", false, true, true, "channel"},
		{"opt_out_overrides_all", "off", "batch_cheap", true, true, false, "opt_out_header"},
		{"batch_cheap_scenario", "", "batch_cheap", false, false, true, "scenario_preset:batch_cheap"},
		{"scenario_over_global", "", "batch_cheap", false, false, true, "scenario_preset:batch_cheap"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := ResolvePlan(tc.header, tc.scenario, tc.global, tc.channel)
			if plan.Enabled != tc.wantOn {
				t.Errorf("Enabled=%v, want %v", plan.Enabled, tc.wantOn)
			}
			if plan.Source != tc.wantSrc {
				t.Errorf("Source=%q, want %q", plan.Source, tc.wantSrc)
			}
		})
	}
}
