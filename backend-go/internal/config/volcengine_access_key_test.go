package config

import (
	"github.com/BenedictKing/ccx/internal/errutil"
	"os"
	"path/filepath"
	"testing"
)

func TestVolcengineAccessKeySurvivesAccountSyncAndReload(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"acct_volc","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"cred_volc","apiKey":"ark-inference"}]}],
  "upstream":[{"accountUid":"acct_volc","channelUid":"ch_volc","providerId":"volcengine","name":"volc-claude","serviceType":"claude","autoManaged":true,"baseUrl":"https://ark.cn-beijing.volces.com/api/plan","apiKeyConfigs":[{"credentialUid":"cred_volc","baseUrl":"https://ark.cn-beijing.volces.com/api/plan"}]}],
  "chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetManagedAccountVolcengineAccessKey("acct_volc", "cred_volc", "AKID", "SECRET"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetManagedAccountVolcenginePlan("acct_volc", "cred_volc", "agent_plan", "Large", "Running"); err != nil {
		t.Fatal(err)
	}
	usedPercent := 12.5
	if err := manager.SetManagedAccountVolcenginePlanUsage("acct_volc", "cred_volc", &VolcenginePlanUsage{
		FiveHour: &VolcenginePlanUsageWindow{UsedPercent: &usedPercent},
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.RenameManagedAccount("acct_volc", "volc-renamed"); err != nil {
		t.Fatal(err)
	}
	_ = manager.Close()

	reloaded, err := NewConfigManager(configPath, filepath.Join(dir, "backups-reload"))
	if err != nil {
		t.Fatal(err)
	}
	defer errutil.IgnoreDeferred(reloaded.Close)
	credential, ok := reloaded.GetManagedAccountCredential("acct_volc", "cred_volc")
	if !ok || credential.VolcengineAccessKey == nil {
		t.Fatalf("AK/SK 未持久化: %+v", credential)
	}
	if pair := credential.VolcengineAccessKey; pair.AccessKeyID != "AKID" || pair.SecretAccessKey != "SECRET" || pair.Plan != "agent_plan" || pair.PlanTier != "Large" {
		t.Fatalf("持久化内容不匹配: %+v", pair)
	}
	usage := credential.VolcengineAccessKey.Usage
	if usage == nil || usage.FiveHour == nil || usage.FiveHour.UsedPercent == nil || *usage.FiveHour.UsedPercent != usedPercent {
		t.Fatalf("用量百分比未持久化: %+v", usage)
	}
}

func TestVolcenginePlanBucketsPersistAndKeepPrimary(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"acct_volc","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"cred_volc","apiKey":"ark-inference"}]}],
  "upstream":[{"accountUid":"acct_volc","channelUid":"ch_volc","providerId":"volcengine","name":"volc-claude","serviceType":"claude","autoManaged":true,"baseUrl":"https://ark.cn-beijing.volces.com/api/plan","apiKeyConfigs":[{"credentialUid":"cred_volc","baseUrl":"https://ark.cn-beijing.volces.com/api/plan"}]}],
  "chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetManagedAccountVolcengineAccessKey("acct_volc", "cred_volc", "AKID", "SECRET"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetManagedAccountVolcenginePlan("acct_volc", "cred_volc", "coding_plan", "Pro", "Running"); err != nil {
		t.Fatal(err)
	}
	buckets := []VolcenginePlanBucket{
		{Product: "coding_plan", Edition: "personal", Tier: "Pro", Status: "Running"},
		{Product: "agent_plan", Edition: "team", SeatID: "seat-001", Status: "Running", Usage: &VolcenginePlanUsage{Monthly: &VolcenginePlanUsageWindow{Quota: 2000, Used: 100}}},
	}
	if err := manager.SetManagedAccountVolcenginePlanBuckets("acct_volc", "cred_volc", buckets); err != nil {
		t.Fatal(err)
	}
	_ = manager.Close()

	reloaded, err := NewConfigManager(configPath, filepath.Join(dir, "backups-reload"))
	if err != nil {
		t.Fatal(err)
	}
	defer errutil.IgnoreDeferred(reloaded.Close)
	credential, ok := reloaded.GetManagedAccountCredential("acct_volc", "cred_volc")
	if !ok || credential.VolcengineAccessKey == nil {
		t.Fatalf("凭证丢失: %+v", credential)
	}
	pair := credential.VolcengineAccessKey
	// 团队版桶不得覆盖主桶字段。
	if pair.Plan != "coding_plan" || pair.PlanTier != "Pro" {
		t.Fatalf("主桶被覆盖: plan=%s tier=%s", pair.Plan, pair.PlanTier)
	}
	if len(pair.Plans) != 2 {
		t.Fatalf("buckets=%+v", pair.Plans)
	}
	team := pair.Plans[1]
	if team.Edition != "team" || team.SeatID != "seat-001" || team.Usage == nil || team.Usage.Monthly == nil || team.Usage.Monthly.Quota != 2000 {
		t.Fatalf("team bucket=%+v", team)
	}
}
