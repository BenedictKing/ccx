package config

import (
	"path/filepath"
	"testing"
)

func TestSharedChannelUpdateProjectsToSiblingRoutes(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json"), config: Config{
		ChatUpstream: []UpstreamConfig{{
			ChannelUID:        "ch-chat",
			LogicalChannelUID: "lc-shared",
			APIKeys:           []string{"sk-1"},
			RoutePrefix:       "chat",
		}},
		ResponsesUpstream: []UpstreamConfig{{
			ChannelUID:        "ch-responses",
			LogicalChannelUID: "lc-shared",
			APIKeys:           []string{"sk-1"},
			RoutePrefix:       "responses",
		}},
	}}

	proxy := "http://127.0.0.1:7890"
	maxMultiplier := 1.5
	routePrefix := "new-chat-prefix"
	_, err := UpdateUpstreamByKind(cm, ChannelLocation{Kind: ChannelKindChat, Index: 0}, UpstreamUpdate{
		ProxyURL:           &proxy,
		MaxGroupMultiplier: &maxMultiplier,
		RoutePrefix:        &routePrefix,
	})
	if err != nil {
		t.Fatalf("UpdateUpstreamByKind() error = %v", err)
	}

	sibling := cm.config.ResponsesUpstream[0]
	if sibling.ProxyURL != proxy {
		t.Fatalf("expected shared proxy projected to sibling, got %q", sibling.ProxyURL)
	}
	if sibling.MaxGroupMultiplier == nil || *sibling.MaxGroupMultiplier != maxMultiplier {
		t.Fatalf("expected shared max multiplier projected to sibling, got %+v", sibling.MaxGroupMultiplier)
	}
	if sibling.RoutePrefix != "responses" {
		t.Fatalf("protocol RoutePrefix must remain route-local, got %q", sibling.RoutePrefix)
	}
}

func TestConvergeSharedChannelFieldsUsesPrimaryRoute(t *testing.T) {
	maxMultiplier := 1.25
	cfg := Config{
		ChatUpstream: []UpstreamConfig{{
			LogicalChannelUID:  "lc-converge",
			ProxyURL:           "http://primary-proxy",
			MaxGroupMultiplier: &maxMultiplier,
			RoutePrefix:        "chat",
		}},
		ResponsesUpstream: []UpstreamConfig{{
			LogicalChannelUID: "lc-converge",
			ProxyURL:          "http://stale-proxy",
			RoutePrefix:       "responses",
		}},
	}

	if !convergeSharedChannelFields(&cfg) {
		t.Fatal("expected divergent shared fields to converge")
	}
	sibling := cfg.ResponsesUpstream[0]
	if sibling.ProxyURL != "http://primary-proxy" {
		t.Fatalf("expected primary proxy to win, got %q", sibling.ProxyURL)
	}
	if sibling.MaxGroupMultiplier == nil || *sibling.MaxGroupMultiplier != 1.25 {
		t.Fatalf("expected primary max multiplier to win, got %+v", sibling.MaxGroupMultiplier)
	}
	if sibling.RoutePrefix != "responses" {
		t.Fatalf("protocol route prefix must remain unchanged, got %q", sibling.RoutePrefix)
	}
}

func TestSyncLogicalChannelSettingsProjectsAndIsIdempotent(t *testing.T) {
	cfg := Config{
		ChatUpstream: []UpstreamConfig{{
			ChannelUID:        "ch-chat",
			LogicalChannelUID: "lc-sync",
			ProxyURL:          "http://settings-proxy",
			RoutePrefix:       "chat",
		}},
		ResponsesUpstream: []UpstreamConfig{{
			ChannelUID:        "ch-responses",
			LogicalChannelUID: "lc-sync",
			ProxyURL:          "http://stale-proxy",
			RoutePrefix:       "responses",
		}},
		LogicalChannels: []LogicalChannel{{
			LogicalChannelUID: "lc-sync",
			Protocols: []LogicalChannelProtocol{
				{Kind: string(ChannelKindChat), ChannelUID: "ch-chat"},
				{Kind: string(ChannelKindResponses), ChannelUID: "ch-responses"},
			},
			Settings: &LogicalChannelSettings{ProxyURL: "http://settings-proxy"},
		}},
	}

	if !syncLogicalChannelSettings(&cfg) {
		t.Fatal("expected divergent sibling route to be projected from settings")
	}
	if sibling := cfg.ResponsesUpstream[0]; sibling.ProxyURL != "http://settings-proxy" {
		t.Fatalf("expected settings proxy to win, got %q", sibling.ProxyURL)
	}
	if syncLogicalChannelSettings(&cfg) {
		t.Fatal("second sync must be a no-op (idempotent)")
	}
}

func TestRefreshLogicalChannelSettingsFromRoutes(t *testing.T) {
	maxMultiplier := 1.5
	cfg := Config{
		ChatUpstream: []UpstreamConfig{{
			ChannelUID:         "ch-chat",
			LogicalChannelUID:  "lc-refresh",
			ProxyURL:           "http://primary-proxy",
			MaxGroupMultiplier: &maxMultiplier,
		}},
		LogicalChannels: []LogicalChannel{{
			LogicalChannelUID: "lc-refresh",
			Protocols: []LogicalChannelProtocol{
				{Kind: string(ChannelKindChat), ChannelUID: "ch-chat"},
			},
		}},
	}

	refreshLogicalChannelSettingsFromRoutes(&cfg)
	lc := cfg.LogicalChannels[0]
	if lc.Settings == nil {
		t.Fatal("expected settings backfilled from primary route")
	}
	if lc.Settings.ProxyURL != "http://primary-proxy" {
		t.Fatalf("expected primary proxy mirrored, got %q", lc.Settings.ProxyURL)
	}
	if lc.Settings.MaxGroupMultiplier == nil || *lc.Settings.MaxGroupMultiplier != 1.5 {
		t.Fatalf("expected primary max multiplier mirrored, got %+v", lc.Settings.MaxGroupMultiplier)
	}

	cfg.ChatUpstream[0].ProxyURL = "http://updated-proxy"
	refreshLogicalChannelSettingsFromRoutes(&cfg)
	if cfg.LogicalChannels[0].Settings.ProxyURL != "http://updated-proxy" {
		t.Fatalf("expected stale settings overwritten by primary route, got %q", cfg.LogicalChannels[0].Settings.ProxyURL)
	}
}
