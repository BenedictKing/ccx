package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResponsesReasoningFoldDefaultsAndPersistsUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"responsesUpstream":[{"baseUrl":"https://example.com","apiKeys":["test"],"serviceType":"responses"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	if cm.GetConfig().ResponsesUpstream[0].ResponsesReasoningFold {
		t.Fatal("folding 应默认关闭")
	}
	for _, enabled := range []bool{true, false} {
		if _, err := cm.UpdateResponsesUpstream(0, UpstreamUpdate{ResponsesReasoningFold: &enabled}); err != nil {
			t.Fatal(err)
		}
		if got := cm.GetConfig().ResponsesUpstream[0].ResponsesReasoningFold; got != enabled {
			t.Fatalf("folding = %v, want %v", got, enabled)
		}
		stored, err := NewConfigManager(path, "")
		if err != nil {
			t.Fatal(err)
		}
		got := stored.GetConfig().ResponsesUpstream[0].ResponsesReasoningFold
		_ = stored.Close()
		if got != enabled {
			t.Fatalf("落盘 folding = %v, want %v", got, enabled)
		}
	}
}
