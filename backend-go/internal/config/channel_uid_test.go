package config

import "testing"

func TestChannelUIDSurvivesRenameReorderAndDuplicate(t *testing.T) {
	cfg := Config{Upstream: []UpstreamConfig{{Name: "a"}, {Name: "b"}}}
	if !normalizeChannelUIDs(&cfg) {
		t.Fatal("must initialize")
	}
	first, second := cfg.Upstream[0].ChannelUID, cfg.Upstream[1].ChannelUID
	cfg.Upstream[0], cfg.Upstream[1] = cfg.Upstream[1], cfg.Upstream[0]
	cfg.Upstream[1].Name = "renamed"
	if normalizeChannelUIDs(&cfg) || cfg.Upstream[1].ChannelUID != first || cfg.Upstream[0].ChannelUID != second {
		t.Fatal("identity changed")
	}
	cfg.Upstream = append(cfg.Upstream, cfg.Upstream[0])
	if !normalizeChannelUIDs(&cfg) || cfg.Upstream[2].ChannelUID == second {
		t.Fatal("duplicate must receive new identity")
	}
}
