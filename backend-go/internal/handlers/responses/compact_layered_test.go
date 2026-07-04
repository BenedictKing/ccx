package responses

import (
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
)

func TestWriteCompactedSession_DefaultCollapsesToTwoMessages(t *testing.T) {
	// 分层模式默认关闭：compact 后 session 坍缩为 2 条消息（user + assistant 摘要）
	sm := session.NewSessionManager(time.Hour, 100, 100000)

	sess, _ := sm.GetOrCreateSession("")
	sm.RecordResponseMapping("resp_prev_default", sess.ID)
	// 写入带 encrypted_content 的 reasoning items 到原 session
	sm.AppendMessage(sess.ID, types.ResponsesItem{Type: "reasoning", ID: "rs_1", EncryptedContent: "BLOB_1"}, 0)
	sm.AppendMessage(sess.ID, types.ResponsesItem{Type: "message", Role: "assistant", Content: "old answer"}, 0)

	resp := &types.ResponsesResponse{
		ID:     "resp_new_default",
		Status: "completed",
		Output: []types.ResponsesItem{
			{Type: "message", Role: "assistant", Content: "compacted summary"},
		},
		Usage: types.ResponsesUsage{InputTokens: 10, OutputTokens: 5},
	}
	originalReq := types.ResponsesRequest{PreviousResponseID: "resp_prev_default"}

	writeCompactedSession(resp, originalReq, sm)

	newSess, err := sm.GetSessionByResponseID("resp_new_default")
	if err != nil {
		t.Fatalf("GetSessionByResponseID err = %v", err)
	}
	if len(newSess.Messages) != 2 {
		t.Fatalf("默认模式应坍缩为 2 条消息，实际 %d 条: %+v", len(newSess.Messages), newSess.Messages)
	}
	// 不应保留 reasoning items
	for _, m := range newSess.Messages {
		if m.Type == "reasoning" {
			t.Fatalf("默认模式不应保留 reasoning items，但发现: %+v", m)
		}
	}
}

func TestWriteCompactedSession_LayeredPreservesReasoningEncryptedContent(t *testing.T) {
	// 分层模式开启：compact 后保留最近 K 条带 encrypted_content 的 reasoning items
	t.Setenv("RESPONSES_COMPACT_LAYERED", "true")
	defer t.Setenv("RESPONSES_COMPACT_LAYERED", "")

	sm := session.NewSessionManager(time.Hour, 100, 100000)

	sess, _ := sm.GetOrCreateSession("")
	sm.RecordResponseMapping("resp_prev_layered", sess.ID)
	// 写入 3 条 reasoning（其中 2 条带 encrypted_content）+ 1 条 message
	sm.AppendMessage(sess.ID, types.ResponsesItem{Type: "reasoning", ID: "rs_1", EncryptedContent: "BLOB_1"}, 0)
	sm.AppendMessage(sess.ID, types.ResponsesItem{Type: "reasoning", ID: "rs_2", EncryptedContent: ""}, 0) // 无 enc，应被跳过
	sm.AppendMessage(sess.ID, types.ResponsesItem{Type: "reasoning", ID: "rs_3", EncryptedContent: "BLOB_3"}, 0)
	sm.AppendMessage(sess.ID, types.ResponsesItem{Type: "message", Role: "assistant", Content: "old answer"}, 0)

	resp := &types.ResponsesResponse{
		ID:     "resp_new_layered",
		Status: "completed",
		Output: []types.ResponsesItem{
			{Type: "message", Role: "assistant", Content: "compacted summary"},
		},
		Usage: types.ResponsesUsage{InputTokens: 10, OutputTokens: 5},
	}
	originalReq := types.ResponsesRequest{PreviousResponseID: "resp_prev_layered"}

	writeCompactedSession(resp, originalReq, sm)

	newSess, err := sm.GetSessionByResponseID("resp_new_layered")
	if err != nil {
		t.Fatalf("GetSessionByResponseID err = %v", err)
	}

	// 期望结构：[user: 已压缩] + [rs_1 reasoning] + [rs_3 reasoning] + [assistant: 摘要]
	// rs_2 无 encrypted_content，应被跳过
	if len(newSess.Messages) != 4 {
		t.Fatalf("分层模式应保留 2 条 reasoning + 2 条 message = 4 条，实际 %d 条: %+v", len(newSess.Messages), newSess.Messages)
	}

	// 验证 reasoning items 原样保留（含 encrypted_content）
	var reasoningIDs []string
	for _, m := range newSess.Messages {
		if m.Type == "reasoning" {
			reasoningIDs = append(reasoningIDs, m.ID)
		}
	}
	if len(reasoningIDs) != 2 || reasoningIDs[0] != "rs_1" || reasoningIDs[1] != "rs_3" {
		t.Fatalf("应保留 rs_1 和 rs_3（原顺序），实际 reasoning IDs: %v", reasoningIDs)
	}

	// 验证 encrypted_content 完整保留
	for _, m := range newSess.Messages {
		if m.Type == "reasoning" && m.ID == "rs_1" && m.EncryptedContent != "BLOB_1" {
			t.Fatalf("rs_1 encrypted_content 应为 BLOB_1，实际 %q", m.EncryptedContent)
		}
		if m.Type == "reasoning" && m.ID == "rs_3" && m.EncryptedContent != "BLOB_3" {
			t.Fatalf("rs_3 encrypted_content 应为 BLOB_3，实际 %q", m.EncryptedContent)
		}
	}
}

func TestWriteCompactedSession_LayeredKeepsOnlyRecentK(t *testing.T) {
	// 分层模式保留最近 K 条（默认 5），超过的不保留
	t.Setenv("RESPONSES_COMPACT_LAYERED", "true")
	defer t.Setenv("RESPONSES_COMPACT_LAYERED", "")

	sm := session.NewSessionManager(time.Hour, 100, 100000)

	sess, _ := sm.GetOrCreateSession("")
	sm.RecordResponseMapping("resp_prev_k", sess.ID)
	// 写入 8 条带 encrypted_content 的 reasoning（超过 K=5）
	for i := 1; i <= 8; i++ {
		sm.AppendMessage(sess.ID, types.ResponsesItem{
			Type:             "reasoning",
			ID:               "rs_" + string(rune('0'+i)),
			EncryptedContent: "BLOB_" + string(rune('0'+i)),
		}, 0)
	}

	resp := &types.ResponsesResponse{
		ID:     "resp_new_k",
		Status: "completed",
		Output: []types.ResponsesItem{
			{Type: "message", Role: "assistant", Content: "summary"},
		},
		Usage: types.ResponsesUsage{InputTokens: 10, OutputTokens: 5},
	}
	originalReq := types.ResponsesRequest{PreviousResponseID: "resp_prev_k"}

	writeCompactedSession(resp, originalReq, sm)

	newSess, err := sm.GetSessionByResponseID("resp_new_k")
	if err != nil {
		t.Fatalf("GetSessionByResponseID err = %v", err)
	}

	// 期望：2 条 message + 5 条 reasoning = 7 条
	if len(newSess.Messages) != 7 {
		t.Fatalf("应保留最近 5 条 reasoning + 2 条 message = 7 条，实际 %d 条", len(newSess.Messages))
	}

	// 期望保留 rs_4 ~ rs_8（最近 5 条）
	var reasoningIDs []string
	for _, m := range newSess.Messages {
		if m.Type == "reasoning" {
			reasoningIDs = append(reasoningIDs, m.ID)
		}
	}
	if len(reasoningIDs) != 5 || reasoningIDs[0] != "rs_4" || reasoningIDs[4] != "rs_8" {
		t.Fatalf("应保留最近 5 条 (rs_4~rs_8)，实际: %v", reasoningIDs)
	}
}

func TestWriteCompactedSession_LayeredNoReasoningFallsBack(t *testing.T) {
	// 分层模式开启但原 session 无带 encrypted_content 的 reasoning：回退到 2 条消息
	t.Setenv("RESPONSES_COMPACT_LAYERED", "true")
	defer t.Setenv("RESPONSES_COMPACT_LAYERED", "")

	sm := session.NewSessionManager(time.Hour, 100, 100000)

	sess, _ := sm.GetOrCreateSession("")
	sm.RecordResponseMapping("resp_prev_noreason", sess.ID)
	sm.AppendMessage(sess.ID, types.ResponsesItem{Type: "reasoning", ID: "rs_1", EncryptedContent: ""}, 0) // 无 enc
	sm.AppendMessage(sess.ID, types.ResponsesItem{Type: "message", Role: "assistant", Content: "old"}, 0)

	resp := &types.ResponsesResponse{
		ID:     "resp_new_noreason",
		Status: "completed",
		Output: []types.ResponsesItem{
			{Type: "message", Role: "assistant", Content: "summary"},
		},
		Usage: types.ResponsesUsage{InputTokens: 10, OutputTokens: 5},
	}
	originalReq := types.ResponsesRequest{PreviousResponseID: "resp_prev_noreason"}

	writeCompactedSession(resp, originalReq, sm)

	newSess, err := sm.GetSessionByResponseID("resp_new_noreason")
	if err != nil {
		t.Fatalf("GetSessionByResponseID err = %v", err)
	}
	if len(newSess.Messages) != 2 {
		t.Fatalf("无 reasoning 时应回退到 2 条消息，实际 %d 条", len(newSess.Messages))
	}
}
