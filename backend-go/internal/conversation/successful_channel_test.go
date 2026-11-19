package conversation

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSuccessfulChannelProvenanceSurvivesStreamingAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation_state.json")
	tracker := NewConversationTracker(time.Hour, 24*time.Hour, path)
	tracker.Track("responses", "child", "astra", 0, "source", "", "hello", 1, "subagent")
	source := SuccessfulChannel{
		ChannelEntry: ChannelEntry{ChannelIndex: 0, ChannelName: "source"},
		ChannelUID:   "uid-source", RequestModel: "sol", Model: "cheap-actual",
		BaseURL: "https://source.example", ServiceType: "responses",
	}
	tracker.RecordSuccessfulChannel("responses", "child", "subagent", source)
	tracker.TrackWithStatus("responses", "child", "astra", 1, "trying", "", "continue", 2, "subagent", "streaming")
	tracker.Stop()
	restored := NewConversationTracker(time.Hour, 24*time.Hour, path)
	defer restored.Stop()
	got, ok := restored.GetSuccessfulChannel("responses", "child", "subagent")
	if !ok || got != source {
		t.Fatalf("成功模型和来源未完整恢复: %+v", got)
	}
	if _, ok := restored.GetSuccessfulChannel("responses", "child", "main"); ok {
		t.Fatal("成功来源跨角色串用")
	}
}
