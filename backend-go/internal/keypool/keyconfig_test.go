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

func TestCandidatesForModel_DisabledKeyFiltered(t *testing.T) {
	up := &config.UpstreamConfig{
		APIKeys: []string{"k1", "k2"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "k1", Weight: 2},
			{Key: "k2", Weight: 1},
		},
		DisabledAPIKeys: []config.DisabledKeyInfo{
			{Key: "k1", RecoverAt: time.Now().Add(time.Hour).Format(time.RFC3339)},
		},
	}

	cands := CandidatesForModel(up, nil, "gpt-5.6-sol")
	if len(cands) != 1 || cands[0].APIKey != "k2" {
		t.Fatalf("want only k2 while k1 is disabled, got %+v", cands)
	}

	up.DisabledAPIKeys[0].RecoverAt = time.Now().Add(-time.Hour).Format(time.RFC3339)
	cands = CandidatesForModel(up, nil, "gpt-5.6-sol")
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates after disabled record expires, got %d", len(cands))
	}
}

func TestCandidatesForModel_FiltersKeysAboveGroupMultiplierLimit(t *testing.T) {
	safeRatio, unsafeRatio, limit := 1.0, 2.0, 1.0
	up := &config.UpstreamConfig{
		APIKeys: []string{"safe", "unsafe", "legacy", "incomplete"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "safe", GroupMultiplier: &safeRatio, MaxGroupMultiplier: &limit},
			{Key: "unsafe", GroupMultiplier: &unsafeRatio, MaxGroupMultiplier: &limit},
			{Key: "legacy"},
			{Key: "incomplete", GroupMultiplier: &safeRatio},
		},
	}

	cands := CandidatesForModel(up, nil, "gpt-5.6")
	if len(cands) != 2 || cands[0].APIKey != "safe" || cands[1].APIKey != "legacy" {
		t.Fatalf("group multiplier guard should keep only safe and legacy keys, got %+v", cands)
	}
}

func TestCandidatesForModel_FiltersConflictingStableIdentity(t *testing.T) {
	up := &config.UpstreamConfig{
		APIKeys: []string{"shared", "safe"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "shared", KeyUID: "kid-1", CredentialUID: "cred-1"},
			{Key: "shared", KeyUID: "kid-2", CredentialUID: "cred-2"},
			{Key: "safe", KeyUID: "kid-safe", CredentialUID: "cred-safe"},
		},
	}

	cands := CandidatesForModel(up, nil, "")
	if len(cands) != 1 || cands[0].APIKey != "safe" {
		t.Fatalf("expected conflicting shared key to be filtered, got %+v", cands)
	}
}

func TestCandidatesForModel_FiltersConflictingNewAPIOwnership(t *testing.T) {
	one := 1.0
	future := time.Now().Add(time.Hour)
	up := &config.UpstreamConfig{
		APIKeys: []string{"shared"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "shared", GroupMultiplier: &one, MaxGroupMultiplier: &one, MultiplierSource: "new_api", MultiplierSyncStatus: "fresh", MultiplierExpiresAt: &future, SourceSubscriptionUID: "sub-1", SourceRemoteTokenID: 1},
			{Key: "shared", GroupMultiplier: &one, MaxGroupMultiplier: &one, MultiplierSource: "new_api", MultiplierSyncStatus: "fresh", MultiplierExpiresAt: &future, SourceSubscriptionUID: "sub-2", SourceRemoteTokenID: 2},
		},
	}

	if cands := CandidatesForModel(up, nil, ""); len(cands) != 0 {
		t.Fatalf("expected conflicting new-api ownership to be filtered, got %+v", cands)
	}
}

// TestCandidatesForModelFiltered_ModelCircuit 验证渠道-模型级熔断只剔除受影响的
// (Key, 模型) 组合，同 Key 的其他模型与未熔断的 Key 都不受影响。
func TestCandidatesForModelFiltered_ModelCircuit(t *testing.T) {
	up := &config.UpstreamConfig{
		ChannelUID: "ch_test",
		APIKeys:    []string{"k1", "k2"},
	}

	// k1 的 sonnet 熔断，k2 与其他模型不受影响。
	circuitOpen := func(channelUID, apiKey, model string) bool {
		return channelUID == "ch_test" && apiKey == "k1" && model == "claude-sonnet-5"
	}

	cands := CandidatesForModelFiltered(up, nil, "claude-sonnet-5", circuitOpen)
	if len(cands) != 1 || cands[0].APIKey != "k2" {
		t.Fatalf("熔断的 k1 应被剔除，期望只剩 k2, got %v", cands)
	}

	// 同渠道其他模型不受连累——这是本机制的核心保证。
	if got := CandidatesForModelFiltered(up, nil, "claude-opus-5", circuitOpen); len(got) != 2 {
		t.Fatalf("其他模型不应受影响，期望 2 个候选, got %d", len(got))
	}

	// nil checker 时行为与 CandidatesForModel 完全一致（fail-open）。
	if got := CandidatesForModelFiltered(up, nil, "claude-sonnet-5", nil); len(got) != 2 {
		t.Fatalf("nil checker 不应过滤, got %d", len(got))
	}

	// ChannelUID 为空的老渠道无法构造熔断键，不做该项过滤。
	noUID := &config.UpstreamConfig{APIKeys: []string{"k1"}}
	if got := CandidatesForModelFiltered(noUID, nil, "claude-sonnet-5", circuitOpen); len(got) != 1 {
		t.Fatalf("无 ChannelUID 时应 fail-open, got %d", len(got))
	}
}
