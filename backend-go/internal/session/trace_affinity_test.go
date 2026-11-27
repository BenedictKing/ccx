package session

import (
	"testing"
	"time"
)

func TestCredentialAffinityIsolatedAndUpdatedOnlyByCurrentSuccess(t *testing.T) {
	m := NewTraceAffinityManager()
	defer m.Stop()
	scope := CredentialAffinityScope{Protocol: "messages", ChannelUID: "channel-a", Role: "main", SessionID: "session-a"}
	m.RecordSuccessfulCredential(scope, "", "key-a")
	m.RecordSuccessfulCredential(scope, "", "key-b") // 两个新请求并发时，后完成的不会覆盖已建立的亲和。
	if got := m.PreferredCredential(scope); got != "key-a" {
		t.Fatalf("preferred credential = %q, want key-a", got)
	}
	m.RecordSuccessfulCredential(scope, "key-a", "key-b")
	m.RecordSuccessfulCredential(scope, "key-a", "key-a") // 已切换后，迟到的原 Key 成功不回切。
	if got := m.PreferredCredential(scope); got != "key-b" {
		t.Fatalf("preferred credential = %q, want key-b", got)
	}
	for _, other := range []CredentialAffinityScope{
		{Protocol: "chat", ChannelUID: "channel-a", Role: "main", SessionID: "session-a"},
		{Protocol: "messages", ChannelUID: "channel-b", Role: "main", SessionID: "session-a"},
		{Protocol: "messages", ChannelUID: "channel-a", Role: "subagent", SessionID: "session-a"},
		{Protocol: "messages", ChannelUID: "channel-a", Role: "main", SessionID: "session-b"},
	} {
		if got := m.PreferredCredential(other); got != "" {
			t.Fatalf("affinity leaked to %+v: %q", other, got)
		}
	}
	m.mu.Lock()
	m.credentialAffinities[scope] = credentialAffinity{id: "key-b", lastUsedAt: time.Now().Add(-m.ttl - time.Second)}
	m.mu.Unlock()
	if got := m.PreferredCredential(scope); got != "" {
		t.Fatalf("expired affinity = %q, want none", got)
	}
	m.Cleanup()
	m.RecordSuccessfulCredential(scope, "", "key-c")
	if got := m.PreferredCredential(scope); got != "key-c" {
		t.Fatalf("affinity after expiration = %q, want key-c", got)
	}
}
