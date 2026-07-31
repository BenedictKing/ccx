package session

import (
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/routingref"
)

func TestTraceAffinityRouteRoundTrip(t *testing.T) {
	manager := NewTraceAffinityManagerWithTTL(time.Minute)
	defer manager.Stop()

	want := routingref.RouteRef{Kind: "responses", Index: 3, ChannelUID: "channel-shared"}
	manager.SetPreferredRoute("user-route", want)

	got, ok := manager.GetPreferredRoute("user-route", "messages")
	if !ok {
		t.Fatal("expected route affinity")
	}
	if got != want {
		t.Fatalf("route roundtrip mismatch: got %+v, want %+v", got, want)
	}
	if index, ok := manager.GetPreferredChannel("user-route"); !ok || index != want.Index {
		t.Fatalf("legacy index mirror mismatch: index=%d ok=%v", index, ok)
	}
}

func TestTraceAffinitySameIndexIsolatedAcrossKinds(t *testing.T) {
	manager := NewTraceAffinityManagerWithTTL(time.Minute)
	defer manager.Stop()

	messages := routingref.RouteRef{Kind: "messages", Index: 1}
	responses := routingref.RouteRef{Kind: "responses", Index: 1}
	manager.SetPreferredRoute("messages-user", messages)
	manager.SetPreferredRoute("responses-user", responses)

	manager.RemoveByRoute(messages)

	if _, ok := manager.GetPreferredRoute("messages-user", "messages"); ok {
		t.Fatal("messages route should have been removed")
	}
	got, ok := manager.GetPreferredRoute("responses-user", "responses")
	if !ok || got != responses {
		t.Fatalf("responses route with same index was removed: got %+v, ok=%v", got, ok)
	}
}

func TestTraceAffinityLegacyIndexDecodesWithLogicalKind(t *testing.T) {
	manager := NewTraceAffinityManagerWithTTL(time.Minute)
	defer manager.Stop()

	manager.affinity["legacy-user"] = &TraceAffinity{
		ChannelIndex: 7,
		LastUsedAt:   time.Now(),
	}

	got, ok := manager.GetPreferredRoute("legacy-user", "gemini")
	if !ok {
		t.Fatal("expected legacy affinity")
	}
	want := routingref.RouteRef{Kind: "gemini", Index: 7}
	if got != want {
		t.Fatalf("legacy route mismatch: got %+v, want %+v", got, want)
	}
}

func TestTraceAffinityLegacySetRoundTrip(t *testing.T) {
	manager := NewTraceAffinityManagerWithTTL(time.Minute)
	defer manager.Stop()

	manager.SetPreferredChannel("legacy-api", 4)

	got, ok := manager.GetPreferredRoute("legacy-api", "chat")
	if !ok {
		t.Fatal("expected legacy API affinity")
	}
	want := routingref.RouteRef{Kind: "chat", Index: 4}
	if got != want {
		t.Fatalf("legacy API route mismatch: got %+v, want %+v", got, want)
	}
}
