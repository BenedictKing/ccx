package scheduler

import (
	"encoding/json"
	"testing"
)

func TestChannelInfoFailedIsolatesSameIndexAcrossKinds(t *testing.T) {
	messages := ChannelInfo{Route: ChannelRouteRef{Kind: string(ChannelKindMessages), Index: 0}, Index: 0}
	responses := ChannelInfo{Route: ChannelRouteRef{Kind: string(ChannelKindResponses), Index: 0}, Index: 0}
	failedRoutes := map[ChannelRouteKey]bool{messages.Route.Key(): true}

	if !channelInfoFailed(messages, nil, failedRoutes) {
		t.Fatal("failed physical route was not excluded")
	}
	if channelInfoFailed(responses, nil, failedRoutes) {
		t.Fatal("same index in another kind was incorrectly excluded")
	}
}

func TestChannelInfoFailedSupportsLegacyIndexMap(t *testing.T) {
	channel := ChannelInfo{Route: ChannelRouteRef{Kind: string(ChannelKindMessages), Index: 3}, Index: 3}
	if !channelInfoFailed(channel, map[int]bool{3: true}, nil) {
		t.Fatal("legacy failed-channel index should remain supported")
	}
}

func TestSelectionTraceCarriesRouteIdentityAndLegacyIndex(t *testing.T) {
	trace := &SelectionTrace{}
	channel := ChannelInfo{
		Route: ChannelRouteRef{Kind: string(ChannelKindResponses), Index: 2, ChannelUID: "route-uid"},
		Index: 2,
		Name:  "route",
	}
	trace.skipChannel(channel, "filter", "reason", "")
	trace.selectChannel(channel.Route, channel.Name, "selected")

	if trace.Candidates[0].Route != channel.Route || trace.Candidates[0].ChannelIndex != channel.Index {
		t.Fatalf("candidate identity mismatch: %#v", trace.Candidates[0])
	}
	if trace.Selected == nil || trace.Selected.Route != channel.Route || trace.Selected.ChannelIndex != channel.Index {
		t.Fatalf("selection identity mismatch: %#v", trace.Selected)
	}

	encoded, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SelectionTrace
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Selected == nil || decoded.Selected.Route.Key() != channel.Route.Key() {
		t.Fatalf("trace route identity did not roundtrip: %#v", decoded.Selected)
	}
}
