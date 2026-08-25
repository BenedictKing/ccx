package config

import "testing"

// 直连优先（ProxyPreferDirect）字段的部分更新语义
func TestApplyUpstreamUpdateFields_ProxyPreferDirect(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	t.Run("nil keeps existing", func(t *testing.T) {
		u := &UpstreamConfig{ProxyURL: "http://127.0.0.1:7890", ProxyPreferDirect: true}
		if _, err := applyUpstreamUpdateFields(u, UpstreamUpdate{}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if !u.ProxyPreferDirect {
			t.Fatal("ProxyPreferDirect 应在 nil 时保持 true")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		u := &UpstreamConfig{}
		if _, err := applyUpstreamUpdateFields(u, UpstreamUpdate{ProxyPreferDirect: boolPtr(true)}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if !u.ProxyPreferDirect {
			t.Fatal("ProxyPreferDirect 应被置为 true")
		}
	})

	t.Run("explicit false overrides", func(t *testing.T) {
		u := &UpstreamConfig{ProxyURL: "http://127.0.0.1:7890", ProxyPreferDirect: true}
		if _, err := applyUpstreamUpdateFields(u, UpstreamUpdate{ProxyPreferDirect: boolPtr(false)}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if u.ProxyPreferDirect {
			t.Fatal("ProxyPreferDirect 应被显式覆盖为 false")
		}
	})
}
