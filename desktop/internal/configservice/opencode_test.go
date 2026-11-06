package configservice

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func assertOpenCodeBaseURL(t *testing.T, content string, providerID string, want string) {
	t.Helper()
	if !json.Valid([]byte(content)) {
		t.Fatalf("OpenCode config is invalid JSON:\n%s", content)
	}
	var root struct {
		Provider map[string]struct {
			Options struct {
				BaseURL string `json:"baseURL"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		t.Fatalf("unmarshal OpenCode config: %v", err)
	}
	provider, ok := root.Provider[providerID]
	if !ok {
		t.Fatalf("provider %q is missing:\n%s", providerID, content)
	}
	if provider.Options.BaseURL != want {
		t.Fatalf("provider %q baseURL = %q, want %q", providerID, provider.Options.BaseURL, want)
	}
}

func TestPreviewApplyOpenCode_PreservesProviderIndentation(t *testing.T) {
	svc := newTestService(t)
	configPath := svc.openCodeConfigPath()
	before := `{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "openai": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {
        "baseURL": "http://localhost:5080/v1"
      }
    }
  },
  "permission": {}
}
`
	writeTextForTest(t, configPath, before)

	result, err := svc.PreviewApply(ApplyAgentConfigRequest{
		Platform: PlatformOpenCode,
		Provider: ProviderCCX,
	}, 3399, "test-key")
	if err != nil {
		t.Fatalf("PreviewApply failed: %v", err)
	}
	if len(result.Files) == 0 || result.Files[0].Path != configPath {
		t.Fatalf("OpenCode config diff is missing: %+v", result.Files)
	}
	var updated strings.Builder
	for _, line := range result.Files[0].Lines {
		if line.Type != "removed" {
			updated.WriteString(line.Content)
			updated.WriteString("\n")
		}
	}

	want := `{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "openai": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {
        "baseURL": "http://localhost:5080/v1"
      }
    },
    "ccx": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "ccx",
      "options": {
        "baseURL": "http://127.0.0.1:3399/v1"
      },
      "models": {}
    }
  },
  "permission": {}
}
`
	if updated.String() != want {
		t.Fatalf("preview config mismatch:\n--- got ---\n%s\n--- want ---\n%s", updated.String(), want)
	}
	assertOpenCodeBaseURL(t, updated.String(), ProviderCCX, "http://127.0.0.1:3399/v1")
}

func TestApplyOpenCode_CreatesNestedProvider(t *testing.T) {
	svc := newTestService(t)
	writeTextForTest(t, svc.openCodeConfigPath(), "{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n")

	if err := svc.Apply(ApplyAgentConfigRequest{
		Platform: PlatformOpenCode,
		Provider: ProviderCCX,
	}, 3399, "test-key"); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	content, err := os.ReadFile(svc.openCodeConfigPath())
	if err != nil {
		t.Fatalf("read OpenCode config: %v", err)
	}
	assertOpenCodeBaseURL(t, string(content), ProviderCCX, "http://127.0.0.1:3399/v1")
	if !strings.Contains(string(content), "  \"provider\": {\n    \"ccx\": {") {
		t.Fatalf("provider nesting or indentation is wrong:\n%s", content)
	}
}

func TestPatchOpenCodeProviderJSONC_UpdateKeepsProviderKey(t *testing.T) {
	before := `{
  "provider": {
    "ccx": {
      "npm": "@ai-sdk/openai-compatible",
      "options": {
        "baseURL": "http://127.0.0.1:3688/v1"
      }
    }
  }
}
`
	providerJSON := openCodeProviderBlockJSON(ProviderCCX, ProviderCCX, "http://127.0.0.1:3399/v1")
	updated := patchOpenCodeProviderJSONC(before, ProviderCCX, providerJSON)
	assertOpenCodeBaseURL(t, updated, ProviderCCX, "http://127.0.0.1:3399/v1")
	if strings.Count(updated, `"ccx":`) != 1 {
		t.Fatalf("provider key should occur exactly once:\n%s", updated)
	}
}
