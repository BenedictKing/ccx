package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestDiagnoseSchedulerSelectionReturnsSelectionTrace(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sch, cleanup := newSchedulerDiagnoseTestScheduler(t, config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "skipped",
				BaseURL:  "https://skipped.example.com",
				APIKeys:  []string{"sk-skip"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "selected",
				BaseURL:  "https://selected.example.com",
				APIKeys:  []string{"sk-select"},
				Status:   "active",
				Priority: 2,
			},
		},
	})
	defer cleanup()

	body := []byte(`{"model":"gpt-test","failedChannels":[0]}`)
	resp := postSchedulerDiagnose(t, sch, scheduler.ChannelKindMessages, body)

	if !resp.OK {
		t.Fatalf("ok = false, error = %q", resp.Error)
	}
	if resp.Kind != string(scheduler.ChannelKindMessages) {
		t.Fatalf("kind = %q, want messages", resp.Kind)
	}
	if resp.Selected.ChannelIndex != 1 || resp.Selected.ChannelName != "selected" {
		t.Fatalf("selected = %#v, want channel 1 selected", resp.Selected)
	}
	if resp.Reason != "priority_order" {
		t.Fatalf("reason = %q, want priority_order", resp.Reason)
	}
	if !strings.Contains(resp.Summary, "0:skipped@priority_order/failed_in_request") {
		t.Fatalf("summary = %q, want failed channel skip", resp.Summary)
	}
	if resp.Trace.Selected == nil || resp.Trace.Selected.ChannelIndex != 1 {
		t.Fatalf("trace.selected = %#v, want channel 1", resp.Trace.Selected)
	}
	if got := sch.GetCurrentChannelIndex(scheduler.ChannelKindMessages); got != 0 {
		t.Fatalf("diagnose should not update current channel, got %d want 0", got)
	}
}

func TestDiagnoseSchedulerSelectionReturnsContextError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sch, cleanup := newSchedulerDiagnoseTestScheduler(t, config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:    "small-window",
				BaseURL: "https://small.example.com",
				APIKeys: []string{"sk-small"},
				Status:  "active",
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"gpt-large": {ContextWindowTokens: 1000},
				},
			},
		},
	})
	defer cleanup()

	body := []byte(`{"model":"gpt-large","contextRequirement":{"inputTokens":5000,"requiredTokens":5000}}`)
	resp := postSchedulerDiagnose(t, sch, scheduler.ChannelKindMessages, body)

	if resp.OK {
		t.Fatalf("ok = true, want false")
	}
	if !strings.Contains(resp.Error, "上下文") {
		t.Fatalf("error = %q, want context routing error", resp.Error)
	}
	if !strings.Contains(resp.Summary, "small-window@context_filter/context_window_exceeded") {
		t.Fatalf("summary = %q, want context filter skip", resp.Summary)
	}
	if len(resp.Trace.Candidates) != 1 || resp.Trace.Candidates[0].Reason != "context_window_exceeded" {
		t.Fatalf("trace.candidates = %#v, want context_window_exceeded", resp.Trace.Candidates)
	}
}

func TestDiagnoseSchedulerSelectionReturnsModelFilterTrace(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sch, cleanup := newSchedulerDiagnoseTestScheduler(t, config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:            "claude-only",
				BaseURL:         "https://claude.example.com",
				APIKeys:         []string{"sk-claude"},
				Status:          "active",
				SupportedModels: []string{"claude-*"},
			},
			{
				Name:            "disabled-gpt",
				BaseURL:         "https://disabled.example.com",
				APIKeys:         []string{"sk-disabled"},
				Status:          "disabled",
				SupportedModels: []string{"gpt-*"},
			},
		},
	})
	defer cleanup()

	resp := postSchedulerDiagnose(t, sch, scheduler.ChannelKindMessages, []byte(`{"model":"gpt-test"}`))

	if resp.OK {
		t.Fatalf("ok = true, want false")
	}
	if !strings.Contains(resp.Error, "支持模型") {
		t.Fatalf("error = %q, want model support error", resp.Error)
	}
	if !strings.Contains(resp.Summary, "claude-only@active_model_filter/unsupported_model") {
		t.Fatalf("summary = %q, want unsupported model skip", resp.Summary)
	}
	if len(resp.Trace.Candidates) != 2 {
		t.Fatalf("trace.candidates len = %d, want 2: %#v", len(resp.Trace.Candidates), resp.Trace.Candidates)
	}
	if resp.Trace.Candidates[0].Reason != "unsupported_model" {
		t.Fatalf("first candidate = %#v, want unsupported_model", resp.Trace.Candidates[0])
	}
	if resp.Trace.Candidates[1].Reason != "disabled_status" {
		t.Fatalf("second candidate = %#v, want disabled_status", resp.Trace.Candidates[1])
	}
}

func TestDiagnoseSchedulerSelectionAttachesAutopilotRequestProfile(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sch, cleanup := newSchedulerDiagnoseTestScheduler(t, config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "incapable-auto",
				BaseURL:     "https://incapable.example.com",
				APIKeys:     []string{"sk-incapable"},
				Status:      "active",
				Priority:    1,
				AutoManaged: true,
			},
			{
				Name:            "capable",
				BaseURL:         "https://capable.example.com",
				APIKeys:         []string{"sk-capable"},
				Status:          "active",
				Priority:        2,
				SupportedModels: []string{"gpt-5.6-sol"},
			},
		},
	})
	defer cleanup()

	profileChecked := false
	sch.SetModelSupportResolverProvider(func(ctx context.Context, _ scheduler.ChannelKind, upstream *config.UpstreamConfig, _ string) (bool, string, string, string) {
		profile, ok := autopilot.RequestProfileFromContext(ctx)
		if !ok {
			t.Fatal("diagnose request did not attach autopilot request profile")
		}
		if profile.Model != "gpt-5.6-sol" || profile.ChannelKind != "responses" ||
			profile.AgentRole != "subagent" || profile.ContextNeed != 1234 ||
			!profile.VisionNeed || profile.QualityNeed != autopilot.QualityTierPremium {
			t.Fatalf("unexpected diagnose request profile: %+v", profile)
		}
		profileChecked = true
		if upstream.Name == "incapable-auto" {
			return false, "", scheduler.ModelSupportSourceAuthoritativeDeny, "no_capable_model"
		}
		return false, "", "", "not handled by resolver"
	})

	resp := postSchedulerDiagnose(t, sch, scheduler.ChannelKindResponses, []byte(`{
		"model":"gpt-5.6-sol",
		"agentRole":"subagent",
		"hasImageContent":true,
		"contextRequirement":{"inputTokens":1234,"requiredTokens":1234}
	}`))
	if !resp.OK {
		t.Fatalf("ok = false, error = %q", resp.Error)
	}
	if !profileChecked {
		t.Fatal("model support resolver did not inspect diagnose request profile")
	}
	if resp.Selected.ChannelName != "capable" {
		t.Fatalf("selected = %#v, want capable channel", resp.Selected)
	}
}

func TestDiagnoseSchedulerSelectionRejectsInvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sch, cleanup := newSchedulerDiagnoseTestScheduler(t, config.Config{})
	defer cleanup()

	r := gin.New()
	r.POST("/messages/channels/scheduler/diagnose", DiagnoseSchedulerSelection(sch, scheduler.ChannelKindMessages))

	req := httptest.NewRequest(http.MethodPost, "/messages/channels/scheduler/diagnose", bytes.NewReader([]byte(`{`)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

type schedulerDiagnoseTestResponse struct {
	OK       bool                      `json:"ok"`
	Kind     string                    `json:"kind"`
	Reason   string                    `json:"reason"`
	Summary  string                    `json:"summary"`
	Error    string                    `json:"error"`
	Selected schedulerDiagnoseSelected `json:"selected"`
	Trace    scheduler.SelectionTrace  `json:"trace"`
}

type schedulerDiagnoseSelected struct {
	ChannelIndex int    `json:"channelIndex"`
	ChannelName  string `json:"channelName"`
	ServiceType  string `json:"serviceType"`
}

func postSchedulerDiagnose(t *testing.T, sch *scheduler.ChannelScheduler, kind scheduler.ChannelKind, body []byte) schedulerDiagnoseTestResponse {
	t.Helper()

	r := gin.New()
	r.POST("/messages/channels/scheduler/diagnose", DiagnoseSchedulerSelection(sch, kind))

	req := httptest.NewRequest(http.MethodPost, "/messages/channels/scheduler/diagnose", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	var resp schedulerDiagnoseTestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func newSchedulerDiagnoseTestScheduler(t *testing.T, cfg config.Config) (*scheduler.ChannelScheduler, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	vectorsMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(30*time.Second, 3)

	sch := scheduler.NewChannelScheduler(
		cfgManager,
		messagesMetrics,
		responsesMetrics,
		geminiMetrics,
		chatMetrics,
		imagesMetrics,
		traceAffinity,
		urlManager,
		vectorsMetrics,
	)

	cleanup := func() {
		_ = cfgManager.Close()
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		imagesMetrics.Stop()
		vectorsMetrics.Stop()
		traceAffinity.Stop()
	}
	return sch, cleanup
}
