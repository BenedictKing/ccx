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
