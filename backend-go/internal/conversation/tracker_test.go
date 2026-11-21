package conversation

import (
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestConversationTracker_Track(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("chat", "user123", "claude-sonnet-4-20250514", 0, "primary", "", "", 0, "")

	convs := ct.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}

	conv := convs[0]
	if conv.Kind != "chat" {
		t.Errorf("expected kind=chat, got %s", conv.Kind)
	}
	if conv.RequestCount != 1 {
		t.Errorf("expected requestCount=1, got %d", conv.RequestCount)
	}
	if conv.ChannelName != "primary" {
		t.Errorf("expected channelName=primary, got %s", conv.ChannelName)
	}
	if conv.LastModel != "claude-sonnet-4-20250514" {
		t.Errorf("expected lastModel=claude-sonnet-4-20250514, got %s", conv.LastModel)
	}
}

func TestConversationTracker_UpdateTitle(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("messages", "session-123", "claude-opus-4-7", 0, "primary", "", "", 0, "")
	if !ct.UpdateTitle("messages", "session-123", "Build docs preview") {
		t.Fatal("expected UpdateTitle to update existing conversation")
	}

	convs := ct.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].Title != "Build docs preview" {
		t.Errorf("expected title=Build docs preview, got %s", convs[0].Title)
	}
	if convs[0].RequestCount != 1 {
		t.Errorf("expected requestCount=1, got %d", convs[0].RequestCount)
	}
}

func TestConversationTracker_UpdateTitleMissingConversation(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	// title 请求先于对话创建到达时，应缓存 title 并返回 true
	if !ct.UpdateTitle("messages", "session-123", "Build docs preview") {
		t.Fatal("expected UpdateTitle to accept pending title")
	}
	if convs := ct.GetActiveConversations(""); len(convs) != 0 {
		t.Fatalf("expected no conversation to be created by UpdateTitle, got %d", len(convs))
	}

	// 后续 Track 创建对话时应自动应用 pending title
	ct.Track("messages", "session-123", "claude-opus-4-7", 0, "primary", "", "", 0, "")
	convs := ct.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation after Track, got %d", len(convs))
	}
	if !strings.Contains(convs[0].Title, "Build docs preview") {
		t.Fatalf("expected pending title to be applied, got %q", convs[0].Title)
	}
}

func TestConversationTracker_UpdateRecap(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	if ct.UpdateRecap("messages", "session-123", "should not create a card") {
		t.Fatal("expected UpdateRecap to ignore missing conversation")
	}

	ct.Track("messages", "session-123", "claude-opus-4-8", 0, "primary", "", "原始问题", 1, "")
	if !ct.UpdateRecap("messages", "session-123", "继续发布任务，下一步查看 Release workflow。") {
		t.Fatal("expected UpdateRecap to update existing conversation")
	}

	convs := ct.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].LastRecap != "继续发布任务，下一步查看 Release workflow。" {
		t.Fatalf("unexpected recap: %q", convs[0].LastRecap)
	}
	if convs[0].LastRecapAt == nil || convs[0].LastRecapAt.IsZero() {
		t.Fatal("expected LastRecapAt to be set")
	}
	if convs[0].RequestCount != 1 {
		t.Fatalf("expected recap not to increment requestCount, got %d", convs[0].RequestCount)
	}
}

func TestConversationTracker_PersistAndRestore(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	ct := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	ct.TrackWithMessages("messages", "user-abc", "claude-opus-4-7", 0, "primary", "", "你好世界", []string{"你好世界"}, 1, "")
	ct.UpdateTitle("messages", "user-abc", "确认会话记录保存时长")
	ct.UpdateRecap("messages", "user-abc", "已完成保存逻辑，下一步验证重启恢复。")
	ct.Stop()

	ct2 := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	defer ct2.Stop()

	convs := ct2.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation after restore, got %d", len(convs))
	}
	if convs[0].Title != "确认会话记录保存时长 — 你好世界" {
		t.Errorf("expected restored title, got %q", convs[0].Title)
	}
	if convs[0].CreatedAt.IsZero() {
		t.Error("expected non-zero createdAt after restore")
	}
	if convs[0].LastRecap != "已完成保存逻辑，下一步验证重启恢复。" {
		t.Errorf("expected restored recap, got %q", convs[0].LastRecap)
	}
	if len(convs[0].LastUserMessages) != 1 || convs[0].LastUserMessages[0] != "你好世界" {
		t.Errorf("expected restored structured messages, got %#v", convs[0].LastUserMessages)
	}
	if convs[0].LastRecapAt == nil || convs[0].LastRecapAt.IsZero() {
		t.Error("expected restored LastRecapAt")
	}

	ct2.Track("messages", "user-abc", "claude-opus-4-7", 1, "backup", "", "", 2, "")
	convs2 := ct2.GetActiveConversations("")
	if len(convs2) != 1 {
		t.Fatalf("expected mapping to work after restore, got %d conversations", len(convs2))
	}
	if convs2[0].CurrentChannel != 1 {
		t.Errorf("expected channel=1 after re-track, got %d", convs2[0].CurrentChannel)
	}
}

func TestConversationTracker_PersistAndRestoreSessionMapping(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	ct := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	ct.Track("responses", "user-abc", "model-a", 0, "primary", "sess-1", "第一轮", 1, "")
	ct.Stop()

	ct2 := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	defer ct2.Stop()
	ct2.Track("responses", "user-abc", "model-b", 1, "backup", "sess-1", "第二轮", 2, "")

	convs := ct2.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected restored session mapping to keep one conversation, got %d", len(convs))
	}
	if convs[0].CurrentChannel != 1 {
		t.Errorf("expected updated channel after restored session mapping, got %d", convs[0].CurrentChannel)
	}
}

func TestConversationTracker_StatusUpdatePersists(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	ct := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	ct.Track("messages", "user-abc", "model-a", 0, "primary", "", "第一轮", 1, "")
	ct.Stop()

	ct2 := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	ct2.UpdateStatus("messages", "user-abc", "idle")
	ct2.Stop()

	ct3 := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	defer ct3.Stop()
	convs := ct3.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation after status restore, got %d", len(convs))
	}
	if convs[0].Status != "idle" {
		t.Errorf("expected persisted status idle, got %s", convs[0].Status)
	}
}

func TestConversationTracker_UpdateStatusByID(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("messages", "user-abc", "model-a", 0, "primary", "", "第一轮", 1, "")
	conv := requireConversationByRawUserID(t, ct, "user-abc")

	if !ct.UpdateStatusByID(conv.ID, "streaming") {
		t.Fatal("expected UpdateStatusByID to update existing conversation")
	}
	updated, ok := ct.GetConversation(conv.ID)
	if !ok {
		t.Fatalf("expected conversation %s to exist", conv.ID)
	}
	if updated.Status != "streaming" {
		t.Fatalf("expected status=streaming, got %s", updated.Status)
	}
	if ct.UpdateStatusByID("missing", "idle") {
		t.Fatal("expected UpdateStatusByID to return false for missing conversation")
	}
	if ct.UpdateStatusByID("", "idle") {
		t.Fatal("expected UpdateStatusByID to return false for empty id")
	}
}

func TestConversationTracker_TrackWithStatusFallsBackToActiveTrack(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.TrackWithStatus("messages", "user-abc", "model-a", 0, "primary", "", "第一轮", 1, "", "streaming")
	conv := requireConversationByRawUserID(t, ct, "user-abc")
	if conv.Status != "streaming" {
		t.Fatalf("expected status=streaming, got %s", conv.Status)
	}
	if conv.RequestCount != 1 {
		t.Fatalf("expected requestCount=1, got %d", conv.RequestCount)
	}

	ct.Track("messages", "user-abc", "model-a", 1, "backup", "", "第一轮", 1, "")
	conv = requireConversationByRawUserID(t, ct, "user-abc")
	if conv.Status != "active" {
		t.Fatalf("expected status=active after normal Track, got %s", conv.Status)
	}
	if conv.RequestCount != 1 {
		t.Fatalf("expected requestCount to remain user message count 1, got %d", conv.RequestCount)
	}
	if conv.CurrentChannel != 1 {
		t.Fatalf("expected currentChannel=1, got %d", conv.CurrentChannel)
	}
}

func TestConversationTracker_StreamingSubagentDoesNotDoubleCount(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	agentCtx := &types.AgentContext{AgentRole: "subagent"}
	ct.TrackWithStatus("messages", "user-abc", "model-a", 0, "primary", "", "子任务", 1, "subagent", "streaming", agentCtx)
	conv := requireConversationByRawUserID(t, ct, "user-abc")
	if !conv.HasSubagents {
		t.Fatal("expected streaming pre-track to mark hasSubagents")
	}
	if conv.SubagentCount != 0 {
		t.Fatalf("expected streaming pre-track not to increment subagentCount, got %d", conv.SubagentCount)
	}

	ct.Track("messages", "user-abc", "model-a", 0, "primary", "", "子任务", 1, "subagent", agentCtx)
	conv = requireConversationByRawUserID(t, ct, "user-abc")
	if conv.SubagentCount != 1 {
		t.Fatalf("expected final Track to increment subagentCount once, got %d", conv.SubagentCount)
	}
}

func TestConversationTracker_TTLFilterOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	ct := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	ct.Track("messages", "user-fresh", "model-a", 0, "ch1", "", "新对话", 1, "")
	ct.Track("messages", "user-old", "model-b", 0, "ch2", "", "旧对话", 1, "")

	ct.mu.Lock()
	for _, conv := range ct.conversations {
		if conv.RawUserID == "user-old" {
			conv.LastActiveAt = time.Now().Add(-3 * time.Hour)
		}
	}
	ct.dirty = true
	ct.mu.Unlock()
	ct.Stop()

	ct2 := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	defer ct2.Stop()

	convs := ct2.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation (expired filtered), got %d", len(convs))
	}
	if convs[0].RawUserID != "user-fresh" {
		t.Errorf("expected user-fresh to survive, got %s", convs[0].RawUserID)
	}
}

func TestConversationTracker_ShortTitleConcatenation(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("messages", "user-x", "model", 0, "ch", "", "会话记录保存时长", 1, "")
	ct.UpdateTitle("messages", "user-x", "Hi")

	convs := ct.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].Title != "Hi — 会话记录保存时长" {
		t.Errorf("expected concatenated title, got %q", convs[0].Title)
	}
}

func TestConversationTracker_TitleCompletesWithFallbackUntilLimit(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("messages", "user-x", "model", 0, "ch", "", "怎么给 Go self-update 选择一个合适的实现方案", 1, "")
	ct.UpdateTitle("messages", "user-x", "Evaluate Go self-update options")

	convs := ct.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	want := "Evaluate Go self-update options — 怎么给 Go self-update 选择一个合适的实现方案"
	if convs[0].Title != want {
		t.Errorf("expected completed title %q, got %q", want, convs[0].Title)
	}
}

func TestConversationTracker_UpdatesTitleWhenFallbackChanges(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("messages", "user-x", "model", 0, "ch", "", "第一轮问题", 1, "")
	ct.UpdateTitle("messages", "user-x", "Evaluate Go self-update options")
	ct.Track("messages", "user-x", "model", 0, "ch", "", "第二轮追问如何自动更新", 2, "")

	convs := ct.GetActiveConversations("")
	want := "Evaluate Go self-update options — 第二轮追问如何自动更新"
	if convs[0].Title != want {
		t.Errorf("expected title to use latest fallback %q, got %q", want, convs[0].Title)
	}
}

func TestConversationTracker_ShortTitleNoDuplicate(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("messages", "user-x", "model", 0, "ch", "", "会话记录保存时长", 1, "")
	ct.UpdateTitle("messages", "user-x", "OK")
	first := ct.GetActiveConversations("")[0].Title
	if first != "OK — 会话记录保存时长" {
		t.Fatalf("expected first concatenation, got %q", first)
	}
	ct.UpdateTitle("messages", "user-x", "OK")
	second := ct.GetActiveConversations("")[0].Title
	if second != first {
		t.Errorf("expected no duplicate concatenation, got %q (was %q)", second, first)
	}
}

func TestConversationTracker_StopIdempotent(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	ct.Stop()
	ct.Stop()
}

func TestConversationTracker_TrackMultipleRequests(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("chat", "user123", "claude-sonnet-4-20250514", 0, "primary", "", "", 0, "")
	ct.Track("chat", "user123", "claude-opus-4-20250514", 1, "backup", "", "", 0, "")

	convs := ct.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation (same user), got %d", len(convs))
	}

	conv := convs[0]
	if conv.RequestCount != 2 {
		t.Errorf("expected requestCount=2, got %d", conv.RequestCount)
	}
	if len(conv.Models) != 2 {
		t.Errorf("expected 2 models, got %d", len(conv.Models))
	}
	if conv.CurrentChannel != 1 {
		t.Errorf("expected currentChannel=1, got %d", conv.CurrentChannel)
	}
}

func TestConversationTracker_DifferentUsers(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("chat", "user1", "model-a", 0, "ch1", "", "", 0, "")
	ct.Track("chat", "user2", "model-b", 1, "ch2", "", "", 0, "")

	convs := ct.GetActiveConversations("")
	if len(convs) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(convs))
	}
}

func TestConversationTracker_KindFilter(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("chat", "user1", "model-a", 0, "ch1", "", "", 0, "")
	ct.Track("messages", "user2", "model-b", 1, "ch2", "", "", 0, "")

	chatConvs := ct.GetActiveConversations("chat")
	if len(chatConvs) != 1 {
		t.Errorf("expected 1 chat conversation, got %d", len(chatConvs))
	}

	msgConvs := ct.GetActiveConversations("messages")
	if len(msgConvs) != 1 {
		t.Errorf("expected 1 messages conversation, got %d", len(msgConvs))
	}
}

func TestConversationTracker_SessionID(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("responses", "user1", "model-a", 0, "ch1", "sess_abc123", "", 0, "")
	ct.Track("responses", "user1", "model-a", 0, "ch1", "sess_abc123", "", 0, "")

	convs := ct.GetActiveConversations("")
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].ID != "sess_abc123" {
		t.Errorf("expected ID=sess_abc123, got %s", convs[0].ID)
	}
	if convs[0].RequestCount != 2 {
		t.Errorf("expected requestCount=2, got %d", convs[0].RequestCount)
	}
}

func TestConversationTracker_LinksSubagentToParentConversation(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("responses", "main-thread", "model-a", 0, "main", "", "主对话", 1, "main")
	ct.Track("responses", "child-thread", "model-a", 1, "sub", "", "子任务", 1, "subagent", &types.AgentContext{
		AgentRole:      "subagent",
		ParentThreadID: "main-thread",
	})

	mainConv := requireConversationByRawUserID(t, ct, "main-thread")
	childConv := requireConversationByRawUserID(t, ct, "child-thread")

	if childConv.ParentConversationID != mainConv.ID {
		t.Fatalf("expected child parentConversationId=%s, got %s", mainConv.ID, childConv.ParentConversationID)
	}
	if !containsString(mainConv.ChildConversationIDs, childConv.ID) {
		t.Fatalf("expected parent childConversationIds to contain %s, got %#v", childConv.ID, mainConv.ChildConversationIDs)
	}
}

func TestConversationTracker_LinksParentCreatedAfterSubagent(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("responses", "child-thread", "model-a", 1, "sub", "", "子任务", 1, "subagent", &types.AgentContext{
		AgentRole:      "subagent",
		ParentThreadID: "main-thread",
	})
	ct.Track("responses", "main-thread", "model-a", 0, "main", "", "主对话", 1, "main")

	mainConv := requireConversationByRawUserID(t, ct, "main-thread")
	childConv := requireConversationByRawUserID(t, ct, "child-thread")

	if childConv.ParentConversationID != mainConv.ID {
		t.Fatalf("expected delayed parentConversationId=%s, got %s", mainConv.ID, childConv.ParentConversationID)
	}
	if !containsString(mainConv.ChildConversationIDs, childConv.ID) {
		t.Fatalf("expected delayed childConversationIds to contain %s, got %#v", childConv.ID, mainConv.ChildConversationIDs)
	}
}

func TestConversationTracker_ReparentsSubagentWithoutStaleChildReference(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("responses", "main-a", "model-a", 0, "main-a", "", "主对话 A", 1, "main")
	ct.Track("responses", "main-b", "model-a", 0, "main-b", "", "主对话 B", 1, "main")
	ct.Track("responses", "child-thread", "model-a", 1, "sub", "", "子任务", 1, "subagent", &types.AgentContext{
		AgentRole:      "subagent",
		ParentThreadID: "main-a",
	})
	ct.Track("responses", "child-thread", "model-a", 1, "sub", "", "子任务", 2, "subagent", &types.AgentContext{
		AgentRole:      "subagent",
		ParentThreadID: "main-b",
	})

	firstParent := requireConversationByRawUserID(t, ct, "main-a")
	secondParent := requireConversationByRawUserID(t, ct, "main-b")
	childConv := requireConversationByRawUserID(t, ct, "child-thread")

	if childConv.ParentConversationID != secondParent.ID {
		t.Fatalf("expected child parentConversationId=%s, got %s", secondParent.ID, childConv.ParentConversationID)
	}
	if containsString(firstParent.ChildConversationIDs, childConv.ID) {
		t.Fatalf("expected first parent childConversationIds to not contain %s, got %#v", childConv.ID, firstParent.ChildConversationIDs)
	}
	if !containsString(secondParent.ChildConversationIDs, childConv.ID) {
		t.Fatalf("expected second parent childConversationIds to contain %s, got %#v", childConv.ID, secondParent.ChildConversationIDs)
	}
}

func TestConversationTracker_PersistAndRestoreRelationships(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	ct := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	ct.Track("responses", "main-thread", "model-a", 0, "main", "", "主对话", 1, "main")
	ct.Track("responses", "child-thread", "model-a", 1, "sub", "", "子任务", 1, "subagent", &types.AgentContext{
		AgentRole:      "subagent",
		ParentThreadID: "main-thread",
	})
	ct.Stop()

	ct2 := NewConversationTracker(1*time.Hour, 2*time.Hour, path)
	defer ct2.Stop()

	mainConv := requireConversationByRawUserID(t, ct2, "main-thread")
	childConv := requireConversationByRawUserID(t, ct2, "child-thread")

	if childConv.ParentConversationID != mainConv.ID {
		t.Fatalf("expected restored parentConversationId=%s, got %s", mainConv.ID, childConv.ParentConversationID)
	}
	if !containsString(mainConv.ChildConversationIDs, childConv.ID) {
		t.Fatalf("expected restored childConversationIds to contain %s, got %#v", childConv.ID, mainConv.ChildConversationIDs)
	}
}

func TestConversationTracker_EmptyUserID(t *testing.T) {
	ct := NewConversationTracker(1*time.Hour, 2*time.Hour)
	defer ct.Stop()

	ct.Track("chat", "", "model-a", 0, "ch1", "", "", 0, "")

	convs := ct.GetActiveConversations("")
	if len(convs) != 0 {
		t.Errorf("expected 0 conversations for empty userID, got %d", len(convs))
	}
}

func TestConversationTracker_MaskUserID(t *testing.T) {
	result := maskUserID("short")
	if result != "s***" {
		t.Errorf("expected s***, got %s", result)
	}

	result = maskUserID("longUserIdentifier")
	if result != "long***fier" {
		t.Errorf("expected long***fier, got %s", result)
	}

	result = maskUserID("user_abc123_session_dbf5ffc0-dea5-44ca")
	if result != "sess:dbf5ffc0" {
		t.Errorf("expected sess:dbf5ffc0, got %s", result)
	}

	result = maskUserID("a_very_long_user_id_that_has_no_sess_keyword_1234567890")
	if result != "a_very_l...7890" {
		t.Errorf("expected a_very_l...7890, got %s", result)
	}
}

func requireConversationByRawUserID(t *testing.T, ct *ConversationTracker, rawUserID string) *Conversation {
	t.Helper()

	for _, conv := range ct.GetActiveConversations("") {
		if conv.RawUserID == rawUserID {
			return conv
		}
	}
	t.Fatalf("expected conversation with rawUserId=%s", rawUserID)
	return nil
}
