package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/utils"
)

func TestKeyOperationEventsCoverManualAutomaticAndFailures(t *testing.T) {
	for _, apiType := range []string{"Messages", "Chat", "Responses", "Gemini", "Images", "Vectors"} {
		t.Run(apiType, func(t *testing.T) {
			key := "sk-private-key-operation-123456"
			cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json"), failedKeysCache: make(map[string]*FailedKey)}
			*cm.getUpstreamSliceLocked(apiType) = []UpstreamConfig{{ChannelUID: "stable-channel", APIKeys: []string{key}}}
			var events []KeyOperationEvent
			cm.SetKeyOperationObserver(func(event KeyOperationEvent) { events = append(events, event) })
			for _, operation := range []func() error{
				func() error { return cm.SuspendKey(apiType, 0, key) },
				func() error { return cm.ResumeKey(apiType, 0, key) },
				func() error {
					return cm.BlacklistKey(apiType, 0, key, "insufficient_balance", "private upstream body "+key)
				},
				func() error { return cm.RestoreKey(apiType, 0, key) },
			} {
				if err := operation(); err != nil {
					t.Fatal(err)
				}
			}
			if len(events) != 4 {
				t.Fatalf("got %d events", len(events))
			}
			for i, action := range []string{"pause", "resume", "blacklist", "restore"} {
				if events[i].Action != action || events[i].Outcome != "success" || events[i].KeyID != utils.APIKeyID(key) || events[i].ChannelUID != "stable-channel" {
					t.Fatalf("event %d: %+v", i, events[i])
				}
			}
			if events[2].Source != "automatic" || events[2].Reason != "insufficient_balance" || events[2].From != "active" || events[2].To != "blacklisted" {
				t.Fatal(events[2])
			}
			if err := cm.BlacklistKey(apiType, 0, key, key, key); err != nil {
				t.Fatal(err)
			}
			count := len(events)
			if err := cm.BlacklistKey(apiType, 0, key, key, key); err != nil || len(events) != count {
				t.Fatal("automatic no-op duplicated an event")
			}
			if _, err := cm.RestoreDisabledKeys(apiType, 0, []string{key, key}); err != nil {
				t.Fatal(err)
			}
			last := events[len(events)-1]
			if last.Source != "automatic" || last.To != "active" {
				t.Fatal(last)
			}
			if err := cm.SuspendKey(apiType, 0, key); err != nil {
				t.Fatal(err)
			}
			if count, err := cm.RestoreAllKeys(apiType, 0); err != nil || count != 1 {
				t.Fatalf("restore all: %d %v", count, err)
			}
			cm.configFile = t.TempDir() // 写入目录必然失败，不修改任何用户配置。
			if err := cm.SuspendKey(apiType, 0, key); err == nil {
				t.Fatal("expected save failure")
			}
			if events[len(events)-1].Outcome != "failed" {
				t.Fatal("save failure was recorded as success")
			}
			encoded, _ := json.Marshal(events)
			if strings.Contains(string(encoded), key) || strings.Contains(string(encoded), "private upstream body") {
				t.Fatal("sensitive data in audit events")
			}
		})
	}
}
