package common_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestHandleMultiChannelFailoverRecordsOneTerminalOutcome(t *testing.T) {
	cfg := config.Config{Upstream: []config.UpstreamConfig{
		{Name: "first", ChannelUID: "ch_first", BaseURL: "https://first.example.com", APIKeys: []string{"sk-first"}, Status: "active"},
		{Name: "second", ChannelUID: "ch_second", BaseURL: "https://second.example.com", APIKeys: []string{"sk-second"}, Status: "active"},
	}}
	env := newAffinityTestEnv(t, cfg)
	defer env.cleanup()

	traceIndex := 0
	env.scheduler.SetCandidateFilterProvider(func(context.Context, scheduler.ChannelKind, string) (scheduler.CandidateFilterFunc, scheduler.CandidateSelectionObserver) {
		traceIndex++
		traceUID := fmt.Sprintf("rt_%d", traceIndex)
		filter := func(channels []scheduler.ChannelInfo, _ func(scheduler.ChannelInfo) *config.UpstreamConfig, _ func(scheduler.ChannelInfo, *config.UpstreamConfig) bool) ([]scheduler.ChannelInfo, error) {
			return channels, nil
		}
		return filter, func(string) string { return traceUID }
	})

	var outcomes []autopilot.RoutingOutcome
	common.SetRoutingOutcomeRecorderHook(func(_ string, outcome autopilot.RoutingOutcome) {
		outcomes = append(outcomes, outcome)
	})
	t.Cleanup(func() { common.SetRoutingOutcomeRecorderHook(nil) })

	c := newTestGinContext(httptest.NewRecorder())
	attempt := 0
	common.HandleMultiChannelFailover(
		c, &config.EnvConfig{}, env.scheduler, scheduler.ChannelKindMessages,
		"Messages", "user", "model", "",
		func(*scheduler.SelectionResult) common.MultiChannelAttemptResult {
			attempt++
			if attempt == 1 {
				return common.MultiChannelAttemptResult{Attempted: true, LastError: errors.New("first failed")}
			}
			return common.MultiChannelAttemptResult{Handled: true, Attempted: true, SuccessKey: "sk-second"}
		},
		nil, nil,
	)

	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2: %+v", len(outcomes), outcomes)
	}
	if outcomes[0].Terminal || outcomes[0].Outcome != "attempt_failed" {
		t.Fatalf("first outcome = %+v, want non-terminal attempt_failed", outcomes[0])
	}
	if !outcomes[1].Terminal || !outcomes[1].Success || !outcomes[1].ChannelFallback {
		t.Fatalf("final outcome = %+v, want successful fallback", outcomes[1])
	}
}

func TestHandleMultiChannelFailoverRecordsFailedRouteWithoutLegacyIndexPollution(t *testing.T) {
	cfg := config.Config{Upstream: []config.UpstreamConfig{
		{Name: "first", ChannelUID: "ch_first", BaseURL: "https://first.example.com", APIKeys: []string{"sk-first"}, Status: "active"},
		{Name: "second", ChannelUID: "ch_second", BaseURL: "https://second.example.com", APIKeys: []string{"sk-second"}, Status: "active"},
	}}
	env := newAffinityTestEnv(t, cfg)
	defer env.cleanup()

	var selected []scheduler.ChannelRouteRef
	common.HandleMultiChannelFailover(
		newTestGinContext(httptest.NewRecorder()), &config.EnvConfig{}, env.scheduler,
		scheduler.ChannelKindMessages, "Messages", "user", "model", "",
		func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			selected = append(selected, selection.Route)
			if len(selected) == 1 {
				return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: errors.New("failed")}
			}
			return common.MultiChannelAttemptResult{Route: selection.Route, Handled: true, Attempted: true, SuccessKey: "sk-second"}
		}, nil, nil,
	)

	if len(selected) != 2 || selected[0].Key() == selected[1].Key() {
		t.Fatalf("selected routes = %+v, want two distinct routes", selected)
	}
}

type affinityTestEnv struct {
	scheduler *scheduler.ChannelScheduler
	cleanup   func()
	affinity  *session.TraceAffinityManager
}

func newAffinityTestEnv(t *testing.T, cfg config.Config) affinityTestEnv {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "multi-channel-affinity-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}

	configFile := filepath.Join(tmpDir, "config.json")
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("序列化测试配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, cfgData, 0644); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("写入临时配置失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("创建配置管理器失败: %v", err)
	}

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	affinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(30*time.Second, 3)

	s := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, affinity, urlManager)

	return affinityTestEnv{
		scheduler: s,
		affinity:  affinity,
		cleanup: func() {
			messagesMetrics.Stop()
			responsesMetrics.Stop()
			geminiMetrics.Stop()
			chatMetrics.Stop()
			imagesMetrics.Stop()
			_ = cfgManager.Close()
			_ = os.RemoveAll(tmpDir)
		},
	}
}

func newTestGinContext(w *httptest.ResponseRecorder) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	return c
}

func TestHandleMultiChannelFailover_SkipsAffinityForVisionRequest(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:    "deepseek",
				BaseURL: "https://deepseek.example.com",
				APIKeys: []string{"sk-deepseek"},
				Status:  "active",
			},
			{
				Name:    "vision",
				BaseURL: "https://vision.example.com",
				APIKeys: []string{"sk-vision"},
				Status:  "active",
			},
		},
	}

	env := newAffinityTestEnv(t, cfg)
	defer env.cleanup()

	env.affinity.SetPreferredChannel(string(scheduler.ChannelKindMessages)+":user-1", 0)

	c := newTestGinContext(httptest.NewRecorder())
	c.Set("lastUserMessage", "describe the image")
	c.Set("userMessageCount", 1)
	common.HasImageContent(c, []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"abc"}}]}]}`))

	var attempts []int
	trySelectedChannel := func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		attempts = append(attempts, selection.ChannelIndex)
		if selection.ChannelIndex == 0 {
			return common.MultiChannelAttemptResult{
				Handled:   false,
				Attempted: true,
			}
		}
		return common.MultiChannelAttemptResult{
			Handled:    true,
			SuccessKey: "ok",
		}
	}

	envCfg := config.NewEnvConfig()
	common.HandleMultiChannelFailover(c, envCfg, env.scheduler, scheduler.ChannelKindMessages, "Messages", "user-1", "gpt-4o", "", trySelectedChannel, nil, nil)

	if len(attempts) != 2 {
		t.Fatalf("期望尝试 2 个渠道，实际尝试 %d 个", len(attempts))
	}
	if attempts[0] != 0 || attempts[1] != 1 {
		t.Fatalf("期望依次尝试 [0,1]，实际尝试 %v", attempts)
	}
	if _, ok := env.affinity.GetPreferredChannel(string(scheduler.ChannelKindMessages) + ":user-1"); !ok {
		t.Fatal("已有文本 affinity 不应被含图请求删除")
	}
	idx, _ := env.affinity.GetPreferredChannel(string(scheduler.ChannelKindMessages) + ":user-1")
	if idx != 0 {
		t.Fatalf("已有文本 affinity 不应被含图请求覆盖，期望 0，实际 %d", idx)
	}
}

func TestHandleMultiChannelFailover_KeepsAffinityForTextRequest(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:    "deepseek",
				BaseURL: "https://deepseek.example.com",
				APIKeys: []string{"sk-deepseek"},
				Status:  "active",
			},
			{
				Name:    "text",
				BaseURL: "https://text.example.com",
				APIKeys: []string{"sk-text"},
				Status:  "active",
			},
		},
	}

	env := newAffinityTestEnv(t, cfg)
	defer env.cleanup()

	c := newTestGinContext(httptest.NewRecorder())
	c.Set("lastUserMessage", "hello")
	c.Set("userMessageCount", 1)

	var attempts []int
	trySelectedChannel := func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		attempts = append(attempts, selection.ChannelIndex)
		if selection.ChannelIndex == 0 {
			return common.MultiChannelAttemptResult{
				Handled:   false,
				Attempted: true,
			}
		}
		return common.MultiChannelAttemptResult{
			Handled:    true,
			SuccessKey: "ok",
		}
	}

	envCfg := config.NewEnvConfig()
	common.HandleMultiChannelFailover(c, envCfg, env.scheduler, scheduler.ChannelKindMessages, "Messages", "user-2", "gpt-4o", "", trySelectedChannel, nil, nil)

	if len(attempts) != 2 {
		t.Fatalf("期望尝试 2 个渠道，实际尝试 %d 个", len(attempts))
	}
	idx, ok := env.affinity.GetPreferredChannel(string(scheduler.ChannelKindMessages) + ":user-2")
	if !ok {
		t.Fatal("纯文本请求成功应写入 Trace affinity")
	}
	if idx != 1 {
		t.Fatalf("纯文本请求应亲和到成功渠道，期望 1，实际 %d", idx)
	}
}

func TestHandleMultiChannelFailoverLogsSelectionTraceInDebug(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "suspended",
				BaseURL:  "https://suspended.example.com",
				APIKeys:  []string{"sk-suspended"},
				Status:   "suspended",
				Priority: 1,
			},
			{
				Name:     "active",
				BaseURL:  "https://active.example.com",
				APIKeys:  []string{"sk-active"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	env := newAffinityTestEnv(t, cfg)
	defer env.cleanup()

	var logs bytes.Buffer
	oldOutput := log.Writer()
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	log.SetOutput(&logs)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(oldOutput)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	}()

	c := newTestGinContext(httptest.NewRecorder())
	envCfg := config.NewEnvConfig()
	envCfg.LogLevel = "debug"

	common.HandleMultiChannelFailover(
		c,
		envCfg,
		env.scheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		"user-debug",
		"gpt-4o",
		"",
		func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			return common.MultiChannelAttemptResult{Handled: true, SuccessKey: "ok"}
		},
		nil,
		nil,
	)

	output := logs.String()
	for _, want := range []string{
		"[Messages-Select-Trace]",
		"stages=active_model_filter:2",
		"0:suspended@priority_order/inactive_status",
		"selected=1:active/priority_order",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("logs = %q, want contains %q", output, want)
		}
	}
}

func TestHandleMultiChannelFailover_KeepsExistingTextAffinityOnVisionFallback(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "deepseek",
				BaseURL:  "https://deepseek.example.com",
				APIKeys:  []string{"sk-deepseek"},
				Status:   "active",
				Priority: 1,
				NoVision: true,
			},
			{
				Name:    "vision",
				BaseURL: "https://vision.example.com",
				APIKeys: []string{"sk-vision"},
				Status:  "active",
			},
		},
	}

	env := newAffinityTestEnv(t, cfg)
	defer env.cleanup()

	env.affinity.SetPreferredChannel(string(scheduler.ChannelKindMessages)+":user-3", 0)

	c := newTestGinContext(httptest.NewRecorder())
	c.Set("lastUserMessage", "describe the image")
	c.Set("userMessageCount", 1)
	common.HasImageContent(c, []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"abc"}}]}]}`))

	trySelectedChannel := func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		if selection.ChannelIndex == 0 {
			return common.MultiChannelAttemptResult{Handled: false, Attempted: true}
		}
		return common.MultiChannelAttemptResult{Handled: true, SuccessKey: "ok"}
	}

	envCfg := config.NewEnvConfig()
	common.HandleMultiChannelFailover(c, envCfg, env.scheduler, scheduler.ChannelKindMessages, "Messages", "user-3", "gpt-4o", "", trySelectedChannel, nil, nil)

	idx, ok := env.affinity.GetPreferredChannel(string(scheduler.ChannelKindMessages) + ":user-3")
	if !ok {
		t.Fatal("已有文本 affinity 不应被含图请求删除")
	}
	if idx != 0 {
		t.Fatalf("已有文本 affinity 不应被含图请求覆盖，期望 0，实际 %d", idx)
	}
}
