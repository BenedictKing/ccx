package config

import "testing"

func TestRedirectModelWithMatch(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		mapping     map[string]string
		wantModel   string
		wantMatched bool
	}{
		{name: "nil upstream", model: "sonnet", wantModel: "sonnet"},
		{name: "exact match", model: "claude-sonnet-5", mapping: map[string]string{"sonnet": "partial", "claude-sonnet-5": "exact"}, wantModel: "exact", wantMatched: true},
		{name: "longest substring", model: "claude-sonnet-5-20260101", mapping: map[string]string{"sonnet": "short", "sonnet-5": "long"}, wantModel: "long", wantMatched: true},
		{name: "same length deterministic", model: "prefix-ab", mapping: map[string]string{"b": "target-b", "a": "target-a"}, wantModel: "target-a", wantMatched: true},
		{name: "no match", model: "opus", mapping: map[string]string{"sonnet": "target"}, wantModel: "opus"},
		{name: "no chain", model: "a", mapping: map[string]string{"a": "b", "b": "c"}, wantModel: "b", wantMatched: true},
		{name: "identity still matches", model: "sonnet", mapping: map[string]string{"sonnet": "sonnet"}, wantModel: "sonnet", wantMatched: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var upstream *UpstreamConfig
			if tt.mapping != nil {
				upstream = &UpstreamConfig{ModelMapping: tt.mapping}
			}
			gotModel, gotMatched := RedirectModelWithMatch(tt.model, upstream)
			if gotModel != tt.wantModel || gotMatched != tt.wantMatched {
				t.Fatalf("RedirectModelWithMatch() = (%q, %v), want (%q, %v)", gotModel, gotMatched, tt.wantModel, tt.wantMatched)
			}
			if gotModel != RedirectModel(tt.model, upstream) {
				t.Fatalf("RedirectModel() = %q, want %q", RedirectModel(tt.model, upstream), gotModel)
			}
		})
	}
}
