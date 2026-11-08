package common_test

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

func TestExplicitFallbackExhaustsPrimaryBeforeBackup(t *testing.T) {
	for _, handled := range []bool{false, true} {
		t.Run(map[bool]string{false: "retries", true: "already_committed"}[handled], func(t *testing.T) {
			cfg := config.Config{ModelFallbackPolicies: map[string]config.ModelFallbackPolicy{"chat:gpt": {Protocol: "chat", RequestModel: "gpt", Models: []string{"primary", "backup"}, MaxAttempts: 6, MaxWaitSeconds: 90}}}
			for _, ch := range []struct{ name, model string }{{"backup-first", "backup"}, {"primary-a", "primary"}, {"primary-b", "primary"}} {
				cfg.ChatUpstream = append(cfg.ChatUpstream, config.UpstreamConfig{Name: ch.name, BaseURL: "https://" + ch.name + ".example", APIKeys: []string{ch.name}, Status: "active", SupportedModels: []string{ch.model}})
			}
			env := newAffinityTestEnv(t, cfg)
			defer env.cleanup()
			c := newTestGinContext(httptest.NewRecorder())
			var attempts []string
			common.HandleMultiChannelFailover(c, config.NewEnvConfig(), env.scheduler, scheduler.ChannelKindChat, "Chat", "", "gpt-client", "", func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
				target := config.RedirectModel("gpt-client", selection.Upstream)
				attempts = append(attempts, selection.Upstream.Name+":"+target)
				return common.MultiChannelAttemptResult{Handled: handled || target == "backup", Attempted: true}
			}, nil, nil)
			want := []string{"primary-a:primary", "primary-b:primary", "backup-first:backup"}
			if handled {
				want = want[:1]
			}
			if !reflect.DeepEqual(attempts, want) {
				t.Fatalf("实际顺序 %v，期望 %v", attempts, want)
			}
		})
	}
}
