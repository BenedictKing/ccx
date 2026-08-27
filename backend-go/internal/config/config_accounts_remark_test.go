package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// 回归：托管账号编辑器保存走 PUT /api/accounts，请求体历史上不含 remark，
// 用户对备注的修改被整体丢弃。现在 AccountChannelUpdate 支持 Remark 指针语义
// （nil=不改），应用后需整组同步到逻辑渠道与兄弟物理卡。
func TestUpdateAccountChannelsSyncsRemark(t *testing.T) {
	dir := t.TempDir()
	cm, err := NewConfigManager(filepath.Join(dir, "config.json"), filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}

	const uid = "acct_remark_test"
	card := func(kind, chUID string) UpstreamConfig {
		return UpstreamConfig{
			ChannelUID:        chUID,
			AccountUID:        uid,
			LogicalChannelUID: "lc_" + uid,
			Name:              "example-org",
			LogicalName:       "example-org",
			Remark:            "oldremark",
			BaseURL:           "https://example.org",
			BaseURLs:          []string{"https://example.org"},
			ServiceType:       "claude",
			APIKeys:           []string{"sk-k1"},
			Status:            "active",
			Priority:          1,
		}
	}
	cm.config.Upstream = append(cm.config.Upstream, card("messages", "ch_m"))
	cm.config.ResponsesUpstream = append(cm.config.ResponsesUpstream, card("responses", "ch_r"))
	cm.config.LogicalChannels = append(cm.config.LogicalChannels, LogicalChannel{
		LogicalChannelUID: "lc_" + uid,
		Name:              "example-org",
		Remark:            "oldremark",
		Kind:              "llm",
	})

	empty := ""
	if err := cm.UpdateAccountChannels(uid, []AccountChannelUpdate{
		{ChannelUID: "ch_m", Name: "example-org", APIKeys: []string{"sk-k1"}, Remark: &empty},
		{ChannelUID: "ch_r", Name: "example-org", APIKeys: []string{"sk-k1"}, Remark: &empty},
	}); err != nil {
		t.Fatalf("UpdateAccountChannels 失败: %v", err)
	}

	checks := map[string]string{
		"messages 卡":  cm.config.Upstream[0].Remark,
		"responses 卡": cm.config.ResponsesUpstream[0].Remark,
		"逻辑渠道":        cm.config.LogicalChannels[0].Remark,
	}
	for label, got := range checks {
		if strings.TrimSpace(got) != "" {
			t.Errorf("[%s] 备注未被同步清除: %q", label, got)
		}
	}

	// nil 语义：不带 Remark 的更新不得触碰既有备注
	cm2, err := NewConfigManager(filepath.Join(dir, "config2.json"), filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	cm2.config.Upstream = append(cm2.config.Upstream, card("messages", "ch_m2"))
	if err := cm2.UpdateAccountChannels(uid, []AccountChannelUpdate{
		{ChannelUID: "ch_m2", Name: "example-org", APIKeys: []string{"sk-k1"}},
	}); err != nil {
		t.Fatalf("UpdateAccountChannels(nil remark) 失败: %v", err)
	}
	if got := cm2.config.Upstream[0].Remark; got != "oldremark" {
		t.Errorf("nil Remark 不应修改备注, got %q", got)
	}
}
