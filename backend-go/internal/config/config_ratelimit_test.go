package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateUpstream_RateLimitFields(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	os.WriteFile(cfgPath, []byte(`{"upstream":[],"chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[]}`), 0644)

	cm, err := NewConfigManager(cfgPath, tmpDir)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	defer cm.Close()

	rpm := 60
	window := 120
	burst := 10
	concurrent := 3
	auto := true

	tests := []struct {
		name   string
		update func(UpstreamUpdate) error
	}{
		{"messages", func(u UpstreamUpdate) error { _, err := cm.UpdateUpstream(0, u); return err }},
		{"chat", func(u UpstreamUpdate) error { _, err := cm.UpdateChatUpstream(0, u); return err }},
		{"responses", func(u UpstreamUpdate) error { _, err := cm.UpdateResponsesUpstream(0, u); return err }},
		{"gemini", func(u UpstreamUpdate) error { _, err := cm.UpdateGeminiUpstream(0, u); return err }},
		{"images", func(u UpstreamUpdate) error { _, err := cm.UpdateImagesUpstream(0, u); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 先添加渠道到对应类型
			upstream := UpstreamConfig{
				Name:        "test-" + tt.name,
				ServiceType: "claude",
				BaseURL:     "http://localhost",
				APIKeys:     []string{"key1"},
			}
			if tt.name == "images" {
				upstream.ServiceType = "openai"
			}
			var addErr error
			switch tt.name {
			case "messages":
				addErr = cm.AddUpstream(upstream)
			case "chat":
				addErr = cm.AddChatUpstream(upstream)
			case "responses":
				addErr = cm.AddResponsesUpstream(upstream)
			case "gemini":
				addErr = cm.AddGeminiUpstream(upstream)
			case "images":
				addErr = cm.AddImagesUpstream(upstream)
			}
			if addErr != nil {
				t.Fatalf("AddUpstream(%s): %v", tt.name, addErr)
			}

			// 获取对应渠道的 index（最新添加的）
			cfg := cm.GetConfig()
			var idx int
			switch tt.name {
			case "messages":
				idx = len(cfg.Upstream) - 1
			case "chat":
				idx = len(cfg.ChatUpstream) - 1
			case "responses":
				idx = len(cfg.ResponsesUpstream) - 1
			case "gemini":
				idx = len(cfg.GeminiUpstream) - 1
			case "images":
				idx = len(cfg.ImagesUpstream) - 1
			}

			// 更新限速字段
			if err := tt.update(UpstreamUpdate{
				RateLimitRPM:             &rpm,
				RateLimitWindowMinutes:   &window,
				RateLimitBurst:           &burst,
				RateLimitMaxConcurrent:   &concurrent,
				RateLimitAutoFromHeaders: &auto,
			}); err != nil {
				t.Fatalf("UpdateUpstream(%s): %v", tt.name, err)
			}

			// 验证
			cfg = cm.GetConfig()
			var got UpstreamConfig
			switch tt.name {
			case "messages":
				got = cfg.Upstream[idx]
			case "chat":
				got = cfg.ChatUpstream[idx]
			case "responses":
				got = cfg.ResponsesUpstream[idx]
			case "gemini":
				got = cfg.GeminiUpstream[idx]
			case "images":
				got = cfg.ImagesUpstream[idx]
			}

			if got.RateLimitRPM != 60 {
				t.Errorf("%s RateLimitRPM = %d, want 60", tt.name, got.RateLimitRPM)
			}
			if got.RateLimitWindowMinutes != 120 {
				t.Errorf("%s RateLimitWindowMinutes = %d, want 120", tt.name, got.RateLimitWindowMinutes)
			}
			if got.RateLimitBurst != 10 {
				t.Errorf("%s RateLimitBurst = %d, want 10", tt.name, got.RateLimitBurst)
			}
			if got.RateLimitMaxConcurrent != 3 {
				t.Errorf("%s RateLimitMaxConcurrent = %d, want 3", tt.name, got.RateLimitMaxConcurrent)
			}
			if got.RateLimitAutoFromHeaders == nil || !*got.RateLimitAutoFromHeaders {
				t.Errorf("%s RateLimitAutoFromHeaders = %v, want true", tt.name, got.RateLimitAutoFromHeaders)
			}

			// 测试清零
			zero := 0
			falseVal := false
			if err := tt.update(UpstreamUpdate{
				RateLimitRPM:             &zero,
				RateLimitWindowMinutes:   &zero,
				RateLimitBurst:           &zero,
				RateLimitMaxConcurrent:   &zero,
				RateLimitAutoFromHeaders: &falseVal,
			}); err != nil {
				t.Fatalf("UpdateUpstream(%s) clear: %v", tt.name, err)
			}

			cfg = cm.GetConfig()
			switch tt.name {
			case "messages":
				got = cfg.Upstream[idx]
			case "chat":
				got = cfg.ChatUpstream[idx]
			case "responses":
				got = cfg.ResponsesUpstream[idx]
			case "gemini":
				got = cfg.GeminiUpstream[idx]
			case "images":
				got = cfg.ImagesUpstream[idx]
			}
			if got.RateLimitRPM != 0 || got.RateLimitWindowMinutes != 0 || got.RateLimitBurst != 0 || got.RateLimitMaxConcurrent != 0 {
				t.Errorf("%s after clear: RPM=%d Window=%d Burst=%d Concurrent=%d, want all 0",
					tt.name, got.RateLimitRPM, got.RateLimitWindowMinutes, got.RateLimitBurst, got.RateLimitMaxConcurrent)
			}
		})
	}
}

func TestRateLimitWindowSecondsKeepsSecondUnit(t *testing.T) {
	if got := RateLimitWindowSeconds(120); got != 120 {
		t.Fatalf("RateLimitWindowSeconds(120) = %d, want 120", got)
	}
	if got := RateLimitWindowSeconds(0); got != 0 {
		t.Fatalf("RateLimitWindowSeconds(0) = %d, want 0", got)
	}
}
