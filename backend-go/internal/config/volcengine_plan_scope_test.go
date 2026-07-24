package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testConfigWithVolcengineAK 创建包含火山托管账号和 AccessKey 的测试 ConfigManager。
func testConfigWithVolcengineAK(t *testing.T, accountUID, credentialUID, plan, tier, status string) *ConfigManager {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"` + accountUID + `","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"` + credentialUID + `","apiKey":"ark-test"}]}],
  "upstream":[{"accountUid":"` + accountUID + `","channelUid":"ch_test","providerId":"volcengine","name":"volc-claude","serviceType":"claude","autoManaged":true,"baseUrl":"https://ark.cn-beijing.volces.com/api/plan","apiKeyConfigs":[{"credentialUid":"` + credentialUID + `","baseUrl":"https://ark.cn-beijing.volces.com/api/plan"}]}],
  "chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.SetManagedAccountVolcengineAccessKey(accountUID, credentialUID, "AKID", "SECRET"); err != nil {
		t.Fatal(err)
	}
	if plan != "" {
		if err := cm.SetManagedAccountVolcenginePlan(accountUID, credentialUID, plan, tier, status); err != nil {
			t.Fatal(err)
		}
	}
	return cm
}

func TestResolveVolcenginePlanScope_AgentPlan(t *testing.T) {
	cm := testConfigWithVolcengineAK(t, "acct_volc", "cred_volc_1", "agent_plan", "Large", "Running")

	scope := ResolveVolcenginePlanScope(cm, "acct_volc", "cred_volc_1")

	if scope.ScopeID == "" {
		t.Fatal("ScopeID should not be empty")
	}
	if scope.Plan != "agent_plan" {
		t.Fatalf("Plan = %q, want agent_plan", scope.Plan)
	}
	if scope.PlanTier != "Large" {
		t.Fatalf("PlanTier = %q, want Large", scope.PlanTier)
	}
	if !scope.AFPComparable {
		t.Fatal("AFPComparable should be true for Running agent_plan")
	}
	if scope.AFPComparableReason != "" {
		t.Fatalf("AFPComparableReason should be empty, got %q", scope.AFPComparableReason)
	}
}

func TestResolveVolcenginePlanScope_CodingPlan(t *testing.T) {
	cm := testConfigWithVolcengineAK(t, "acct_volc", "cred_volc_coding", "coding_plan", "Medium", "Running")

	scope := ResolveVolcenginePlanScope(cm, "acct_volc", "cred_volc_coding")

	if scope.Plan != "coding_plan" {
		t.Fatalf("Plan = %q, want coding_plan", scope.Plan)
	}
	if scope.AFPComparable {
		t.Fatal("coding_plan should not be AFPComparable")
	}
	if scope.AFPComparableReason == "" {
		t.Fatal("AFPComparableReason should explain why coding_plan is not comparable")
	}
}

func TestResolveVolcenginePlanScope_ExpiredPlan(t *testing.T) {
	cm := testConfigWithVolcengineAK(t, "acct_volc", "cred_volc_exp", "agent_plan", "Large", "Expired")

	scope := ResolveVolcenginePlanScope(cm, "acct_volc", "cred_volc_exp")

	if scope.AFPComparable {
		t.Fatal("Expired plan should not be AFPComparable")
	}
	if scope.AFPComparableReason == "" {
		t.Fatal("expected reason for expired plan")
	}
}

func TestResolveVolcenginePlanScope_UnknownPlan(t *testing.T) {
	cm := testConfigWithVolcengineAK(t, "acct_volc", "cred_volc_unknown", "", "", "")

	scope := ResolveVolcenginePlanScope(cm, "acct_volc", "cred_volc_unknown")

	if scope.AFPComparable {
		t.Fatal("Unknown plan should not be AFPComparable")
	}
	if scope.AFPComparableReason == "" {
		t.Fatal("expected reason for unknown plan")
	}
}

func TestResolveVolcenginePlanScope_NoAccessKey(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"acct_volc","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"cred_volc_no_ak","apiKey":"ark-test"}]}],
  "upstream":[],"chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}

	scope := ResolveVolcenginePlanScope(cm, "acct_volc", "cred_volc_no_ak")

	if scope.AFPComparable {
		t.Fatal("credential without access key should not be AFPComparable")
	}
	if scope.AFPComparableReason == "" {
		t.Fatal("expected reason")
	}
}

func TestResolveVolcenginePlanScope_MissingCredential(t *testing.T) {
	cm := testConfigWithVolcengineAK(t, "acct_volc", "cred_volc_1", "agent_plan", "Large", "Running")

	scope := ResolveVolcenginePlanScope(cm, "acct_volc", "non_existent")

	if scope.AFPComparable {
		t.Fatal("missing credential should not be AFPComparable")
	}
	if scope.AFPComparableReason == "" {
		t.Fatal("expected reason")
	}
}

func TestResolveVolcenginePlanScope_NilConfigManager(t *testing.T) {
	scope := ResolveVolcenginePlanScope(nil, "acct_volc", "cred_volc")
	if scope.AFPComparable {
		t.Fatal("nil ConfigManager should not be AFPComparable")
	}
}

func TestResolveVolcenginePlanScope_QuotaExhausted(t *testing.T) {
	cm := testConfigWithVolcengineAK(t, "acct_volc", "cred_volc_exhausted", "agent_plan", "Large", "Running")
	usedPercent := 100.0
	if err := cm.SetManagedAccountVolcenginePlanUsage("acct_volc", "cred_volc_exhausted", &VolcenginePlanUsage{
		FiveHour:  &VolcenginePlanUsageWindow{Quota: 1000, Used: 1000, UsedPercent: &usedPercent},
		FetchedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	scope := ResolveVolcenginePlanScope(cm, "acct_volc", "cred_volc_exhausted")

	if scope.AFPComparable {
		t.Fatal("exhausted quota should not be AFPComparable")
	}
	if scope.AFPComparableReason != "current quota window exhausted" {
		t.Fatalf("reason = %q, want 'current quota window exhausted'", scope.AFPComparableReason)
	}
	if !scope.UsageExhausted {
		t.Fatal("UsageExhausted should be true")
	}
}

func TestResolveVolcenginePlanScopeFromUpstream_Volcengine(t *testing.T) {
	cm := testConfigWithVolcengineAK(t, "acct_volc", "cred_volc_upstream", "agent_plan", "Large", "Running")

	upstream := &UpstreamConfig{
		AccountUID: "acct_volc",
		ProviderID: "volcengine",
		APIKeys:    []string{"ark-test"},
		APIKeyConfigs: []APIKeyConfig{
			{Key: "ark-test", CredentialUID: "cred_volc_upstream"},
		},
	}

	scope := ResolveVolcenginePlanScopeFromUpstream(cm, upstream, "ark-test")

	if !scope.AFPComparable {
		t.Fatalf("AFPComparable should be true, reason: %s", scope.AFPComparableReason)
	}
	if scope.ScopeID == "" {
		t.Fatal("ScopeID should not be empty")
	}
}

func TestResolveVolcenginePlanScopeFromUpstream_NonVolcengine(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{"upstream":[],"chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}

	upstream := &UpstreamConfig{
		AccountUID: "acct_openai",
		ProviderID: "openai",
		APIKeys:    []string{"sk-xxx"},
	}

	scope := ResolveVolcenginePlanScopeFromUpstream(cm, upstream, "sk-xxx")

	if scope.AFPComparable {
		t.Fatal("non-volcengine provider should not be AFPComparable")
	}
	if scope.AFPComparableReason == "" {
		t.Fatal("expected reason")
	}
}

func TestGenerateVolcengineScopeID_Stable(t *testing.T) {
	id1 := generateVolcengineScopeID("acct_a", "cred_b", "agent_plan")
	id2 := generateVolcengineScopeID("acct_a", "cred_b", "agent_plan")
	if id1 != id2 {
		t.Fatalf("ScopeID not stable: %s vs %s", id1, id2)
	}
	if len(id1) < 15 { // "vp_" + 12 hex chars
		t.Fatalf("ScopeID too short: %s", id1)
	}
}

func TestGenerateVolcengineScopeID_DifferentPlan(t *testing.T) {
	id1 := generateVolcengineScopeID("acct_a", "cred_b", "agent_plan")
	id2 := generateVolcengineScopeID("acct_a", "cred_b", "coding_plan")
	if id1 == id2 {
		t.Fatal("different plan should produce different ScopeID")
	}
}

func TestGenerateVolcengineScopeID_NoSecret(t *testing.T) {
	id := generateVolcengineScopeID("acct_a", "cred_b", "agent_plan")
	if id == "acct_a" || id == "cred_b" || id == "agent_plan" {
		t.Fatal("ScopeID should not contain raw account/credential/plan strings")
	}
}

func TestCheckUsageExhausted(t *testing.T) {
	tests := []struct {
		name     string
		usage    *VolcenginePlanUsage
		expected bool
	}{
		{
			name:     "nil usage",
			usage:    nil,
			expected: false,
		},
		{
			name: "five hour exhausted",
			usage: &VolcenginePlanUsage{
				FiveHour: &VolcenginePlanUsageWindow{Quota: 100, Used: 100},
			},
			expected: true,
		},
		{
			name: "five hour available",
			usage: &VolcenginePlanUsage{
				FiveHour: &VolcenginePlanUsageWindow{Quota: 100, Used: 50},
			},
			expected: false,
		},
		{
			name: "daily exhausted",
			usage: &VolcenginePlanUsage{
				Daily: &VolcenginePlanUsageWindow{Quota: 100, Used: 100},
			},
			expected: true,
		},
		{
			name: "used percent only (coding plan)",
			usage: &VolcenginePlanUsage{
				FiveHour: &VolcenginePlanUsageWindow{UsedPercent: floatPtr(1.0)},
			},
			expected: false, // 保守返回 false
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkUsageExhausted(tt.usage)
			if got != tt.expected {
				t.Fatalf("checkUsageExhausted() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsVolcengineProvider(t *testing.T) {
	tests := []struct {
		providerID string
		expected   bool
	}{
		{"volcengine", true},
		{"volc-ark", true},
		{"openai", false},
		{"deepseek", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.providerID, func(t *testing.T) {
			upstream := &UpstreamConfig{ProviderID: tt.providerID}
			got := IsVolcengineProvider(upstream)
			if got != tt.expected {
				t.Fatalf("IsVolcengineProvider(%q) = %v, want %v", tt.providerID, got, tt.expected)
			}
		})
	}
}

func floatPtr(f float64) *float64 {
	return &f
}
