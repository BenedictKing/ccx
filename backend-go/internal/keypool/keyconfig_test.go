package keypool

import (
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func ptrBool(v bool) *bool { return &v }

func TestCandidatesForModel_FiltersByModels(t *testing.T) {
	up := &config.UpstreamConfig{
		APIKeys: []string{"k1", "k2", "k3"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "k1", Models: []string{"claude-sonnet-4-5"}},
			{Key: "k2", Models: []string{"gpt-4*"}},
			{Key: "k3"}, // 无 Models，应匹配所有
		},
	}

	cands := CandidatesForModel(up, nil, "claude-sonnet-4-5")
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates for claude-sonnet-4-5, got %d", len(cands))
	}
	keys := map[string]bool{}
	for _, c := range cands {
		keys[c.APIKey] = true
	}
	if !keys["k1"] || !keys["k3"] {
		t.Fatalf("expected k1 and k3, got %v", keys)
	}
}

func TestCandidatesForModel_WildcardPattern(t *testing.T) {
	up := &config.UpstreamConfig{
		APIKeys: []string{"k1", "k2"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "k1", Models: []string{"gpt-4*"}},
			{Key: "k2", Models: []string{"!gpt-*"}},
		},
	}

	cands := CandidatesForModel(up, nil, "gpt-4o")
	if len(cands) != 1 || cands[0].APIKey != "k1" {
		t.Fatalf("want k1 for gpt-4o, got %v", cands)
	}
}

func TestCandidatesForModel_WeightOrdering(t *testing.T) {
	up := &config.UpstreamConfig{
		APIKeys: []string{"k1", "k2", "k3"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "k1", Weight: 1},
			{Key: "k2", Weight: 5},
			{Key: "k3"}, // 默认 weight=0 => 1
		},
	}

	cands := CandidatesForModel(up, nil, "")
	if len(cands) != 3 {
		t.Fatalf("want 3, got %d", len(cands))
	}
	if cands[0].APIKey != "k2" {
		t.Fatalf("first candidate should be k2 (weight=5), got %s", cands[0].APIKey)
	}
}

func TestCandidatesForModel_EnabledFalseFiltered(t *testing.T) {
	up := &config.UpstreamConfig{
		APIKeys: []string{"k1", "k2"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "k1", Enabled: ptrBool(false)},
			{Key: "k2", Enabled: ptrBool(true)},
		},
	}

	cands := CandidatesForModel(up, nil, "")
	if len(cands) != 1 || cands[0].APIKey != "k2" {
		t.Fatalf("want only k2, got %v", cands)
	}
}

func TestCandidatesForModel_FailedKeysFiltered(t *testing.T) {
	up := &config.UpstreamConfig{
		APIKeys: []string{"k1", "k2"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "k1", Name: "a"},
			{Key: "k2", Name: "b"},
		},
	}

	cands := CandidatesForModel(up, map[string]bool{"k1": true}, "")
	if len(cands) != 1 || cands[0].APIKey != "k2" {
		t.Fatalf("want only k2, got %v", cands)
	}
}

func TestConfigForCandidate_UsesWindowSeconds(t *testing.T) {
	up := config.UpstreamConfig{
		RateLimitRPM:           50,
		RateLimitWindowMinutes: 120,
		RateLimitMaxConcurrent: 3,
	}

	got := ConfigForCandidate(up, config.APIKeyConfig{})
	if got.WindowSeconds != 120 {
		t.Fatalf("inherited WindowSeconds = %d, want 120", got.WindowSeconds)
	}
	if got.RPM != 50 {
		t.Fatalf("inherited RPM = %d, want 50", got.RPM)
	}
	if got.MaxConcurrent != 3 {
		t.Fatalf("inherited MaxConcurrent = %d, want 3", got.MaxConcurrent)
	}

	got = ConfigForCandidate(up, config.APIKeyConfig{
		RateLimitRPM:           20,
		RateLimitWindowMinutes: 30,
		RateLimitMaxConcurrent: 1,
	})
	if got.WindowSeconds != 30 {
		t.Fatalf("overridden WindowSeconds = %d, want 30", got.WindowSeconds)
	}
	if got.RPM != 20 {
		t.Fatalf("overridden RPM = %d, want 20", got.RPM)
	}
	if got.MaxConcurrent != 1 {
		t.Fatalf("overridden MaxConcurrent = %d, want 1", got.MaxConcurrent)
	}
}

func TestMatchesModel(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		models []string
		want   bool
	}{
		{"exact match", "claude-sonnet-4-5", []string{"claude-sonnet-4-5"}, true},
		{"prefix suffix wildcard", "gpt-4o", []string{"gpt-4*"}, true},
		{"prefix suffix wildcard long", "gpt-4o-mini", []string{"gpt-4*"}, true},
		{"prefix wildcard miss", "claude-opus-4-8", []string{"gpt-4*"}, false},
		{"leading wildcard", "hello-world", []string{"*world"}, true},
		{"trailing wildcard", "hello-world", []string{"hello-*"}, true},
		{"both ends wildcard", "hello-world", []string{"*lo-wo*"}, true},
		{"single star matches all", "anything", []string{"*"}, true},
		{"double star matches all", "anything", []string{"**"}, true},
		{"empty pattern list allows all", "anything", []string{}, true},
		{"negation excludes", "gpt-4o", []string{"!gpt-*"}, false},
		{"negation does not match keeps allowed", "claude-opus", []string{"!gpt-*", "claude-*"}, true},
		{"negation with exact match", "gpt-4o", []string{"!gpt-4o", "*"}, false},
		{"pure negation when not hit allows", "claude-opus", []string{"!gpt-*"}, true},
		{"empty bang ignored", "claude-opus", []string{"!"}, true},
		{"case insensitive", "Claude-Sonnet", []string{"claude-sonnet"}, true},
	}
	for _, tt := range tests {
		got := matchesModel(tt.model, tt.models)
		if got != tt.want {
			t.Errorf("matchesModel(%q, %v) = %v, want %v (case: %s)", tt.model, tt.models, got, tt.want, tt.name)
		}
	}
}

func TestCandidatesForModel_DisabledKeyModelFiltered(t *testing.T) {
	up := &config.UpstreamConfig{
		APIKeys: []string{"k1", "k2"},
		DisabledKeyModels: []config.DisabledKeyModelInfo{
			{Key: "k1", Model: "gpt-5.6-sol", RecoverAt: time.Now().Add(time.Hour).Format(time.RFC3339)},
		},
	}

	// k1 对受限模型应被跳过，k2 保留
	cands := CandidatesForModel(up, nil, "gpt-5.6-sol")
	if len(cands) != 1 || cands[0].APIKey != "k2" {
		t.Fatalf("want only k2 for restricted model, got %+v", cands)
	}

	// k1 对其他模型不受影响
	cands = CandidatesForModel(up, nil, "gpt-4o")
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates for unrestricted model, got %d", len(cands))
	}

	// 已到期限制不再生效
	up.DisabledKeyModels[0].RecoverAt = time.Now().Add(-time.Hour).Format(time.RFC3339)
	cands = CandidatesForModel(up, nil, "gpt-5.6-sol")
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates after expiry, got %d", len(cands))
	}
}
