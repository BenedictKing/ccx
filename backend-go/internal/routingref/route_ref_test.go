package routingref

import "testing"

func TestRouteRefKeyUsesUIDAcrossKindsAndIndexes(t *testing.T) {
	left := RouteRef{Kind: "messages", Index: 0, ChannelUID: "physical-1"}
	right := RouteRef{Kind: "responses", Index: 7, ChannelUID: "physical-1"}
	if left.Key() != right.Key() {
		t.Fatalf("same UID should identify the same physical route: %#v != %#v", left.Key(), right.Key())
	}
}

func TestRouteRefKeyFallsBackToKindAndIndex(t *testing.T) {
	messages := RouteRef{Kind: "messages", Index: 0}
	responses := RouteRef{Kind: "responses", Index: 0}
	if messages.Key() == responses.Key() {
		t.Fatal("same legacy index in different kinds must remain isolated")
	}
	if messages.Key() != (Key{Kind: "messages", Index: 0}) {
		t.Fatalf("unexpected legacy fallback key: %#v", messages.Key())
	}
	if !messages.Matches(RouteRef{Kind: "messages", Index: 0, ChannelUID: "new-uid"}) {
		t.Fatal("legacy route should match an upgraded UID-bearing route by kind and index")
	}
}

func TestRouteRefZeroValue(t *testing.T) {
	if !(RouteRef{}).IsZero() {
		t.Fatal("zero value should be zero")
	}
	if (RouteRef{Kind: "messages"}).IsZero() {
		t.Fatal("logical kind makes a route non-zero")
	}
}
