package configservice

import (
	"strings"
	"testing"
)

func TestExtractTopLevelTomlString(t *testing.T) {
	cases := []struct {
		name    string
		content string
		key     string
		wantVal string
		wantOK  bool
	}{
		{"正常提取", `model_provider = "ccx"`, "model_provider", "ccx", true},
		{"不存在 key", `model_provider = "ccx"`, "other", "", false},
		{"空内容", "", "key", "", false},
		{"值含特殊字符", `key = "http://127.0.0.1:3688/v1"`, "key", "http://127.0.0.1:3688/v1", true},
		{"带注释", `model_provider = "ccx"  # comment`, "model_provider", "ccx", true},
		{"多行取第一个", "a = \"1\"\na = \"2\"", "a", "1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := extractTopLevelTomlString(c.content, c.key)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if got != c.wantVal {
				t.Errorf("value = %q, want %q", got, c.wantVal)
			}
		})
	}
}

func TestExtractNamedTomlBlock(t *testing.T) {
	cases := []struct {
		name    string
		content string
		table   string
		wantOK  bool
	}{
		{
			"正常提取",
			"[model_providers.ccx]\nname = \"CCX\"\nbase_url = \"http://localhost\"\n\n[model_providers.openai]\nname = \"OpenAI\"\n",
			"model_providers.ccx",
			true,
		},
		{"不存在", "[other]\nkey = \"val\"\n", "model_providers.ccx", false},
		{"空内容", "", "model_providers.ccx", false},
		{
			"最后一个 block",
			"[other]\nkey = \"val\"\n[model_providers.ccx]\nname = \"CCX\"\n",
			"model_providers.ccx",
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := extractNamedTomlBlock(c.content, c.table)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
		})
	}
}

func TestFindNamedTomlBlock(t *testing.T) {
	content := "[other]\nkey = \"val\"\n[model_providers.ccx]\nname = \"CCX\"\nbase_url = \"x\"\n\n[model_providers.openai]\nname = \"OpenAI\"\n"
	start, end, ok := findNamedTomlBlock(content, "model_providers.ccx")
	if !ok {
		t.Fatal("expected to find block")
	}
	block := content[start:end]
	if !strings.Contains(block, "name = \"CCX\"") {
		t.Errorf("block does not contain expected content: %q", block)
	}
	if strings.Contains(block, "model_providers.openai") {
		t.Errorf("block should not contain next table")
	}
}

func TestUpsertTopLevelTomlString(t *testing.T) {
	t.Run("替换已有", func(t *testing.T) {
		got := upsertTopLevelTomlString(`model_provider = "openai"`, "model_provider", "ccx")
		if !strings.Contains(got, `"ccx"`) {
			t.Errorf("expected ccx, got %q", got)
		}
		if strings.Contains(got, `"openai"`) {
			t.Errorf("should not contain old value")
		}
	})
	t.Run("新增 key", func(t *testing.T) {
		got := upsertTopLevelTomlString("other = \"val\"\n", "model_provider", "ccx")
		if !strings.Contains(got, `model_provider = "ccx"`) {
			t.Errorf("expected new key, got %q", got)
		}
	})
	t.Run("空内容", func(t *testing.T) {
		got := upsertTopLevelTomlString("", "key", "val")
		if !strings.Contains(got, `key = "val"`) {
			t.Errorf("expected key in empty content, got %q", got)
		}
	})
}

func TestUpsertNamedTomlBlock(t *testing.T) {
	t.Run("替换已有", func(t *testing.T) {
		content := "[model_providers.ccx]\nold = \"data\"\n\n[other]\nk = \"v\"\n"
		block := "[model_providers.ccx]\nnew = \"data\"\n"
		got := upsertNamedTomlBlock(content, "model_providers.ccx", block)
		if !strings.Contains(got, `new = "data"`) {
			t.Errorf("expected new block, got %q", got)
		}
		if strings.Contains(got, `old = "data"`) {
			t.Errorf("should not contain old block")
		}
	})
	t.Run("新增 block", func(t *testing.T) {
		got := upsertNamedTomlBlock("existing = \"val\"\n", "model_providers.ccx", "[model_providers.ccx]\nname = \"CCX\"\n")
		if !strings.Contains(got, `[model_providers.ccx]`) {
			t.Errorf("expected new block, got %q", got)
		}
	})
}

func TestRestoreTopLevelTomlString(t *testing.T) {
	t.Run("恢复原值", func(t *testing.T) {
		orig := "original"
		got := restoreTopLevelTomlString(`model_provider = "ccx"`, "model_provider", &orig)
		if !strings.Contains(got, `"original"`) {
			t.Errorf("expected original, got %q", got)
		}
	})
	t.Run("nil 删除行", func(t *testing.T) {
		got := restoreTopLevelTomlString("model_provider = \"ccx\"\nother = \"val\"\n", "model_provider", nil)
		if strings.Contains(got, "model_provider") {
			t.Errorf("should have removed line, got %q", got)
		}
		if !strings.Contains(got, "other") {
			t.Errorf("should keep other lines")
		}
	})
}

func TestRestoreNamedTomlBlock(t *testing.T) {
	t.Run("nil 删除 block", func(t *testing.T) {
		content := "[model_providers.ccx]\nname = \"CCX\"\n\n[other]\nk = \"v\"\n"
		got := restoreNamedTomlBlock(content, "model_providers.ccx", nil)
		if strings.Contains(got, "model_providers.ccx") {
			t.Errorf("should have removed block, got %q", got)
		}
		if !strings.Contains(got, "[other]") {
			t.Errorf("should keep other block")
		}
	})
	t.Run("恢复原 block", func(t *testing.T) {
		orig := "[model_providers.ccx]\nname = \"Original\"\n"
		content := "[model_providers.ccx]\nname = \"CCX\"\n"
		got := restoreNamedTomlBlock(content, "model_providers.ccx", &orig)
		if !strings.Contains(got, `"Original"`) {
			t.Errorf("expected original, got %q", got)
		}
	})
}

func TestDetectClaudeProvider(t *testing.T) {
	cases := []struct {
		baseURL string
		want    string
	}{
		{"", ""},
		{"http://127.0.0.1:3688", ProviderCCX},
		{"http://localhost:3688", ProviderCCX},
		{"https://api.deepseek.com/anthropic", ProviderDeepSeek},
		{"https://api.mimo.xiaomi.com/v1", ProviderMiMo},
		{"https://xiaomimimo.com/v1", ProviderMiMo},
		{"https://custom-api.example.com/v1", ProviderCustom},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			got := detectClaudeProvider(c.baseURL)
			if got != c.want {
				t.Errorf("detectClaudeProvider(%q) = %q, want %q", c.baseURL, got, c.want)
			}
		})
	}
}

func TestNormalizeClaudeProvider(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ProviderCCX},
		{"ccx", ProviderCCX},
		{"CCX", ProviderCCX},
		{"deepseek", ProviderDeepSeek},
		{"DeepSeek", ProviderDeepSeek},
		{"mimo", ProviderMiMo},
		{"MIMO", ProviderMiMo},
		{"custom-provider", "custom-provider"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			if got := normalizeClaudeProvider(c.input); got != c.want {
				t.Errorf("normalizeClaudeProvider(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestIsLocalBaseURL(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"http://127.0.0.1:3688", true},
		{"http://localhost:3688", true},
		{"https://api.deepseek.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isLocalBaseURL(c.value); got != c.want {
			t.Errorf("isLocalBaseURL(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}

func TestClaudeBaseURL(t *testing.T) {
	if got := claudeBaseURL(3688); got != "http://127.0.0.1:3688" {
		t.Errorf("got %q", got)
	}
}

func TestCodexBaseURL(t *testing.T) {
	if got := codexBaseURL(3688); got != "http://127.0.0.1:3688/v1" {
		t.Errorf("got %q", got)
	}
}

func TestCodexProviderBlock(t *testing.T) {
	block := codexProviderBlock("http://127.0.0.1:3688/v1")
	if !strings.Contains(block, `[model_providers.ccx]`) {
		t.Errorf("missing table header")
	}
	if !strings.Contains(block, `base_url = "http://127.0.0.1:3688/v1"`) {
		t.Errorf("missing base_url")
	}
}

func TestAppendUnique(t *testing.T) {
	got := appendUnique([]string{"a", "b"}, "c")
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	got = appendUnique(got, "b")
	if len(got) != 3 {
		t.Errorf("should not add duplicate, got %d", len(got))
	}
	got = appendUnique(got, "")
	if len(got) != 3 {
		t.Errorf("should not add empty, got %d", len(got))
	}
}

func TestAppendUniqueMany(t *testing.T) {
	got := appendUniqueMany([]string{"a"}, []string{"b", "a", "c"})
	if len(got) != 3 {
		t.Errorf("expected 3, got %d", len(got))
	}
}

func TestProviderFromStoreKey(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"claude:deepseek", "deepseek"},
		{"channel:mimo", "mimo"},
		{"standalone", "standalone"},
	}
	for _, c := range cases {
		if got := providerFromStoreKey(c.input); got != c.want {
			t.Errorf("providerFromStoreKey(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestUsageFromStoreKey(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"claude:deepseek", "agent-direct"},
		{"codex:openai", "codex-direct"},
		{"channel:mimo", "channel"},
		{"standalone", "manual"},
	}
	for _, c := range cases {
		if got := usageFromStoreKey(c.input); got != c.want {
			t.Errorf("usageFromStoreKey(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestGetNestedString(t *testing.T) {
	data := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL": "http://localhost",
		},
	}
	if v, ok := getNestedString(data, "env", "ANTHROPIC_BASE_URL"); !ok || v != "http://localhost" {
		t.Errorf("got (%q, %v)", v, ok)
	}
	if _, ok := getNestedString(data, "env", "MISSING"); ok {
		t.Error("expected not found")
	}
	if _, ok := getNestedString(data, "missing"); ok {
		t.Error("expected not found for top-level")
	}
}

func TestOptionalString(t *testing.T) {
	if got := optionalString("val", true); got == nil || *got != "val" {
		t.Errorf("expected non-nil")
	}
	if got := optionalString("val", false); got != nil {
		t.Errorf("expected nil")
	}
}

func TestRestoreStringField(t *testing.T) {
	data := map[string]any{"key": "old"}
	restoreStringField(data, "key", strPtr("new"))
	if data["key"] != "new" {
		t.Errorf("expected new, got %v", data["key"])
	}
	restoreStringField(data, "key", nil)
	if _, ok := data["key"]; ok {
		t.Error("expected deleted")
	}
}
