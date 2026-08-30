package autopilot

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

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
)

func TestApplyVolcengineSignature(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://ark.cn-beijing.volcengineapi.com/?Version=2024-01-01&Action=ListArkAgentPlanModel", bytes.NewBufferString("{}"))
	applyVolcengineSignature(req, []byte("{}"), "AKIDTEST", "test-secret", "ark", time.Date(2026, 4, 24, 12, 20, 3, 0, time.UTC))
	want := "HMAC-SHA256 Credential=AKIDTEST/20260424/cn-beijing/ark/request, SignedHeaders=host;x-content-sha256;x-date, Signature=7f13d6f457c76d2fb9d1c3f2be1165eaa90d681bcfd231db0d223994190217aa"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization 签名不匹配\ngot:  %s\nwant: %s", got, want)
	}
	if got := req.Header.Get("Content-Type"); got != volcengineContentType {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := req.Header.Get("X-Date"); got != "20260424T122003Z" {
		t.Fatalf("X-Date=%q", got)
	}
}

func TestVolcenginePlanClientDetectAndFetchModels(t *testing.T) {
	tests := []struct {
		name       string
		activePlan string
		wantAction string
	}{
		{name: "Agent Plan", activePlan: volcenginePlanAgent, wantAction: "ListArkAgentPlanModel"},
		{name: "Coding Plan", activePlan: volcenginePlanCoding, wantAction: "ListArkCodingPlanModel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var modelAction string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Authorization"), "/cn-beijing/") {
					t.Errorf("缺少签名: %s", r.Header.Get("Authorization"))
				}
				switch action := r.URL.Query().Get("Action"); action {
				case "GetPersonalPlan":
					var body struct{ Plan string }
					_ = json.NewDecoder(r.Body).Decode(&body)
					matched := (body.Plan == "AgentPlan" && tt.activePlan == volcenginePlanAgent) ||
						(body.Plan == "CodingPlan" && tt.activePlan == volcenginePlanCoding)
					if !matched {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"ResourceNotFound.Plan","Message":"not found"}}}`))
						return
					}
					_, _ = w.Write([]byte(`{"Result":{"PlanType":"Pro","Status":"Running"}}`))
				case "GetSeatInfo":
					// 团队版席位未绑定：空 SeatID 静默。
					_, _ = w.Write([]byte(`{"Result":{}}`))
				case "ListArkAgentPlanModel", "ListArkCodingPlanModel":
					modelAction = action
					_, _ = w.Write([]byte(`{"Result":{"Datas":[{"ModelID":"model-b"},{"ModelID":"model-a"},{"ModelID":"model-a"}]}}`))
				default:
					t.Errorf("未知 action: %s", action)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()

			client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
			pair := &config.VolcengineAccessKeyPair{AccessKeyID: "AKID", SecretAccessKey: "SECRET"}
			plan, err := client.DetectPlan(context.Background(), pair, "")
			if err != nil {
				t.Fatal(err)
			}
			if plan.Plan != tt.activePlan || plan.Tier != "Pro" || plan.Status != "Running" {
				t.Fatalf("plan=%+v", plan)
			}
			models, err := client.FetchModels(context.Background(), pair, plan.Plan)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(models, ",") != "model-a,model-b" || modelAction != tt.wantAction {
				t.Fatalf("models=%v action=%s", models, modelAction)
			}
		})
	}
}

func TestVolcenginePlanClientUsesBaseURLHintWhenBothPlansExist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Result":{"PlanType":"Large","Status":"Running"}}`))
	}))
	defer server.Close()
	client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
	plan, err := client.DetectPlan(context.Background(), &config.VolcengineAccessKeyPair{AccessKeyID: "AK", SecretAccessKey: "SK"}, volcenginePlanCoding)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Plan != volcenginePlanCoding {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestVolcenginePlanClientUsageEndpoint(t *testing.T) {
	client := &volcenginePlanClient{}
	for _, action := range []string{"GetAFPUsage", "GetCodingPlanUsage"} {
		if got := client.endpointFor(action, "ark"); got != "https://open.volcengineapi.com/" {
			t.Fatalf("%s endpoint=%q", action, got)
		}
	}
}

func TestVolcenginePlanClientFetchUsage(t *testing.T) {
	t.Run("Agent Plan 返回 AFP 四窗口含额度", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if action := r.URL.Query().Get("Action"); action != "GetAFPUsage" {
				t.Errorf("action=%s", action)
			}
			_, _ = w.Write([]byte(`{"Result":{"PlanType":"Large",
				"AFPFiveHour":{"Quota":50,"Used":12.5,"ResetTime":1778806800000},
				"AFPDaily":{"Quota":100,"Used":22.5,"ResetTime":1778803200000},
				"AFPWeekly":{"Quota":500,"Used":150,"ResetTime":1779062400000},
				"AFPMonthly":{"Quota":2000,"Used":850.5,"ResetTime":1780531200000}}}`))
		}))
		defer server.Close()
		client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
		usage, err := client.FetchUsage(context.Background(), &config.VolcengineAccessKeyPair{AccessKeyID: "AK", SecretAccessKey: "SK"}, volcenginePlanAgent)
		if err != nil {
			t.Fatal(err)
		}
		if usage.FiveHour == nil || usage.FiveHour.Quota != 50 || usage.FiveHour.Used != 12.5 {
			t.Fatalf("fiveHour=%+v", usage.FiveHour)
		}
		if usage.Monthly == nil || usage.Monthly.Quota != 2000 || usage.Monthly.Used != 850.5 {
			t.Fatalf("monthly=%+v", usage.Monthly)
		}
	})

	t.Run("Coding Plan 返回三个窗口的已用百分比", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if action := r.URL.Query().Get("Action"); action != "GetCodingPlanUsage" {
				t.Errorf("action=%s", action)
			}
			if r.ContentLength != 0 {
				t.Errorf("contentLength=%d, want 0", r.ContentLength)
			}
			if auth := r.Header.Get("Authorization"); !strings.Contains(auth, "/cn-beijing/ark/request") {
				t.Errorf("签名 scope 错误: %s", auth)
			}
			_, _ = w.Write([]byte(`{"Result":{"Status":"Running","QuotaUsage":[
				{"Level":"session","Percent":12.5,"ResetTimestamp":1782226478},
				{"Level":"weekly","Percent":24,"ResetTimestamp":1782662400},
				{"Level":"monthly","Percent":7.5,"ResetTimestamp":-1}]}}`))
		}))
		defer server.Close()
		client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
		usage, err := client.FetchUsage(context.Background(), &config.VolcengineAccessKeyPair{AccessKeyID: "AK", SecretAccessKey: "SK"}, volcenginePlanCoding)
		if err != nil {
			t.Fatal(err)
		}
		if usage.FiveHour == nil || usage.FiveHour.UsedPercent == nil || *usage.FiveHour.UsedPercent != 12.5 || usage.FiveHour.ResetTime != 1782226478000 {
			t.Fatalf("fiveHour=%+v", usage.FiveHour)
		}
		if usage.Weekly == nil || usage.Weekly.UsedPercent == nil || *usage.Weekly.UsedPercent != 24 || usage.Monthly == nil || usage.Monthly.UsedPercent == nil || *usage.Monthly.UsedPercent != 7.5 {
			t.Fatalf("weekly=%+v monthly=%+v", usage.Weekly, usage.Monthly)
		}
		if usage.Monthly.ResetTime != 0 {
			t.Fatalf("monthly resetTime=%d, want 0", usage.Monthly.ResetTime)
		}
		if usage.Daily != nil {
			t.Fatalf("Coding Plan 不应有 daily 窗口: %+v", usage.Daily)
		}
	})

	t.Run("接口 4xx 返回错误", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"AccessDenied","Message":"missing ark:GetCodingPlanUsage permission"}}}`))
		}))
		defer server.Close()
		client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
		if _, err := client.FetchUsage(context.Background(), &config.VolcengineAccessKeyPair{AccessKeyID: "AK", SecretAccessKey: "SK"}, volcenginePlanCoding); err == nil {
			t.Fatal("期望返回错误")
		}
	})
}

func TestVolcengineAutoDiscoveryResolvesKeysFromCredentials(t *testing.T) {
	// 验证当 APIKeys 为空时，discoverEndpoints 从 apiKeyConfigs 和凭证系统获取实际密钥。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("Action") {
		case "GetPersonalPlan":
			_, _ = w.Write([]byte(`{"Result":{"PlanType":"Large","Status":"Running"}}`))
		case "ListArkAgentPlanModel":
			_, _ = w.Write([]byte(`{"Result":{"Datas":[{"ModelID":"doubao-seed-code"}]}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	// 模拟 APIKeys 被脱敏后的配置：apiKeys 为空，仅靠 apiKeyConfigs 维持凭证关联。
	data := `{
  "managedAccounts":[{"accountUid":"acct_volc","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"cred_volc","apiKey":"ark-inference","volcengineAccessKey":{"accessKeyId":"AKID","secretAccessKey":"SECRET"}}]}],
  "upstream":[{"accountUid":"acct_volc","channelUid":"ch_volc","providerId":"volcengine","name":"volc","serviceType":"claude","autoManaged":true,"baseUrl":"https://ark.cn-beijing.volces.com/api/plan","apiKeyConfigs":[{"credentialUid":"cred_volc","baseUrl":"https://ark.cn-beijing.volces.com/api/plan"}]}],
  "chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer errutil.IgnoreDeferred(manager.Close)
	channels := manager.GetAccountChannels("acct_volc")
	if len(channels) != 1 {
		t.Fatalf("channels=%d", len(channels))
	}
	// 确保 APIKeys 为空，模拟脱敏场景。
	channel := channels[0].Upstream
	channel.APIKeys = nil

	runner := NewAutoDiscoveryRunner(nil, nil)
	runner.client = server.Client()
	runner.volcengineControlPlaneEndpoint = server.URL
	results := runner.discoverEndpoints(context.Background(), &channel, manager)
	if len(results) != 1 || !results[0].ProtocolOk || strings.Join(results[0].Models, ",") != "doubao-seed-code" {
		t.Fatalf("results=%+v", results)
	}
	if results[0].ModelDiscoverySource != ModelDiscoverySourceControlPlane {
		t.Fatalf("期望管控面来源，实际: %s", results[0].ModelDiscoverySource)
	}
}

func TestVolcengineAutoDiscoveryUsesControlPlaneModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("Action") {
		case "GetPersonalPlan":
			var body struct{ Plan string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Plan != "AgentPlan" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"ResourceNotFound.Plan","Message":"not found"}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"Result":{"PlanType":"Large","Status":"Running"}}`))
		case "ListArkAgentPlanModel":
			_, _ = w.Write([]byte(`{"Result":{"Datas":[{"ModelID":"doubao-seed-code"},{"ModelID":"ark-code-latest"}]}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"acct_volc","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"cred_volc","apiKey":"ark-inference","volcengineAccessKey":{"accessKeyId":"AKID","secretAccessKey":"SECRET"}}]}],
  "upstream":[{"accountUid":"acct_volc","channelUid":"ch_volc","providerId":"volcengine","name":"volc","serviceType":"claude","autoManaged":true,"baseUrl":"https://ark.cn-beijing.volces.com/api/plan","apiKeyConfigs":[{"credentialUid":"cred_volc","baseUrl":"https://ark.cn-beijing.volces.com/api/plan"}]}],
  "chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer errutil.IgnoreDeferred(manager.Close)
	channels := manager.GetAccountChannels("acct_volc")
	if len(channels) != 1 {
		t.Fatalf("channels=%d", len(channels))
	}
	runner := NewAutoDiscoveryRunner(nil, nil)
	runner.client = server.Client()
	runner.volcengineControlPlaneEndpoint = server.URL
	results := runner.discoverEndpoints(context.Background(), &channels[0].Upstream, manager)
	if len(results) != 1 || !results[0].ProtocolOk || strings.Join(results[0].Models, ",") != "ark-code-latest,doubao-seed-code" {
		t.Fatalf("results=%+v", results)
	}
	credential, ok := manager.GetManagedAccountCredential("acct_volc", "cred_volc")
	if !ok || credential.VolcengineAccessKey == nil || credential.VolcengineAccessKey.Plan != volcenginePlanAgent {
		t.Fatalf("套餐识别结果未持久化: %+v", credential)
	}
}

func TestFetchVolcenginePlanModelsForChannel(t *testing.T) {
	newManager := func(t *testing.T, withAccessKey bool) *config.ConfigManager {
		t.Helper()
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		accessKey := ""
		if withAccessKey {
			accessKey = `,"volcengineAccessKey":{"accessKeyId":"AKID","secretAccessKey":"SECRET"}`
		}
		data := `{
  "managedAccounts":[{"accountUid":"acct_volc","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"cred_volc","apiKey":"ark-inference"` + accessKey + `}]}],
  "upstream":[{"accountUid":"acct_volc","channelUid":"ch_volc","providerId":"volcengine","name":"volc","serviceType":"claude","autoManaged":true,"baseUrl":"https://ark.cn-beijing.volces.com/api/plan","apiKeyConfigs":[{"credentialUid":"cred_volc","key":"ark-inference","baseUrl":"https://ark.cn-beijing.volces.com/api/plan"}]}],"chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
		if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = manager.Close() })
		return manager
	}
	planServer := func(t *testing.T, wantAction string) *httptest.Server {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if action := r.URL.Query().Get("Action"); action != wantAction {
				t.Errorf("action=%s, want %s", action, wantAction)
			}
			_, _ = w.Write([]byte(`{"Result":{"Datas":[{"ModelID":"ark-code-latest"},{"ModelID":"doubao-seed-2-1-turbo"}]}}`))
		}))
		t.Cleanup(server.Close)
		return server
	}

	tests := []struct {
		name        string
		baseURL     string
		apiKey      string
		withChannel bool
		withAK      bool
		wantHandled bool
		wantModels  string
		wantErr     bool
	}{
		{name: "非火山端点不接管", baseURL: "https://ark.cn-beijing.volces.com/api/v3", apiKey: "ark-inference", wantHandled: false},
		{name: "coding 端点未绑 AK 回退内置清单", baseURL: "https://ark.cn-beijing.volces.com/api/coding/v3", apiKey: "ark-inference", withAK: false, wantHandled: true, wantModels: strings.Join(config.VolcengineCodingPlanModelIDs(), ",")},
		{name: "agent 端点未绑 AK 回退内置清单", baseURL: "https://ark.cn-beijing.volces.com/api/plan/v3", apiKey: "ark-inference", withAK: false, wantHandled: true, wantModels: strings.Join(config.VolcengineAgentPlanModelIDs(), ",")},
		{name: "coding 端点带渠道上下文走管控面", baseURL: "https://ark.cn-beijing.volces.com/api/coding/v1", apiKey: "ark-inference", withChannel: true, withAK: true, wantHandled: true, wantModels: "ark-code-latest,doubao-seed-2-1-turbo"},
		{name: "agent 端点无渠道上下文按 Key 定位凭证", baseURL: "https://ark.cn-beijing.volces.com/api/plan/v3", apiKey: "ark-inference", withChannel: false, withAK: true, wantHandled: true, wantModels: "ark-code-latest,doubao-seed-2-1-turbo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newManager(t, tt.withAK)
			server := planServer(t, map[bool]string{true: "ListArkCodingPlanModel", false: "ListArkAgentPlanModel"}[strings.Contains(tt.baseURL, "/api/coding")])
			channel := (*config.UpstreamConfig)(nil)
			if tt.withChannel {
				channel = &config.UpstreamConfig{AccountUID: "acct_volc", APIKeyConfigs: []config.APIKeyConfig{{Key: "ark-inference", CredentialUID: "cred_volc"}}}
			}
			models, handled, err := fetchVolcenginePlanModelsForChannel(context.Background(), manager, channel, tt.baseURL, tt.apiKey, server.URL, server.Client())
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v", err)
			}
			if handled != tt.wantHandled {
				t.Fatalf("handled=%v, want %v", handled, tt.wantHandled)
			}
			if !tt.wantHandled {
				return
			}
			if strings.Join(models, ",") != tt.wantModels {
				t.Fatalf("models=%v, want %s", models, tt.wantModels)
			}
		})
	}
}

func TestFetchVolcenginePlanModelsForChannelControlPlaneError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"InvalidCredential","Message":"Invalid credential"}}}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"acct_volc","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"cred_volc","apiKey":"ark-inference","volcengineAccessKey":{"accessKeyId":"AKID","secretAccessKey":"SECRET"}}]}],
  "upstream":[{"accountUid":"acct_volc","channelUid":"ch_volc","providerId":"volcengine","name":"volc","serviceType":"claude","autoManaged":true,"baseUrl":"https://ark.cn-beijing.volces.com/api/plan","apiKeyConfigs":[{"credentialUid":"cred_volc","key":"ark-inference","baseUrl":"https://ark.cn-beijing.volces.com/api/plan"}]}],"chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer errutil.IgnoreDeferred(manager.Close)
	models, handled, err := fetchVolcenginePlanModelsForChannel(context.Background(), manager, nil, "https://ark.cn-beijing.volces.com/api/coding/v3", "ark-inference", server.URL, server.Client())
	if !handled || err == nil || models != nil {
		t.Fatalf("handled=%v err=%v models=%v", handled, err, models)
	}
	if !strings.Contains(err.Error(), "InvalidCredential") {
		t.Fatalf("错误应透传管控面错误码: %v", err)
	}
}

// TestVolcengineAutoDiscoveryFiltersNonChatModels 验证管控面套餐权益清单里的非对话模型
// （embedding/seedance/seedream/tts 命名族）按内置清单 ExcludeModelPatterns 被过滤，
// kimi-k3 等对话模型保留，过滤数量写入发现说明。
func TestVolcengineAutoDiscoveryFiltersNonChatModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("Action") {
		case "GetPersonalPlan":
			_, _ = w.Write([]byte(`{"Result":{"PlanType":"Large","Status":"Running"}}`))
		case "ListArkAgentPlanModel":
			_, _ = w.Write([]byte(`{"Result":{"Datas":[
				{"ModelID":"kimi-k3"},
				{"ModelID":"doubao-seed-2.1-turbo"},
				{"ModelID":"doubao-seedance-1-5-pro-250528"},
				{"ModelID":"doubao-seedream-4-0-250828"},
				{"ModelID":"doubao-embedding-vision-250615"},
				{"ModelID":"doubao-tts-1"}
			]}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
  "managedAccounts":[{"accountUid":"acct_volc","providerId":"volcengine","name":"volc","credentials":[{"credentialUid":"cred_volc","apiKey":"ark-inference","volcengineAccessKey":{"accessKeyId":"AKID","secretAccessKey":"SECRET"}}]}],
  "upstream":[{"accountUid":"acct_volc","channelUid":"ch_volc","providerId":"volcengine","name":"volc","serviceType":"claude","autoManaged":true,"baseUrl":"https://ark.cn-beijing.volces.com/api/plan","apiKeyConfigs":[{"credentialUid":"cred_volc","key":"ark-inference","baseUrl":"https://ark.cn-beijing.volces.com/api/plan"}]}],
  "chatUpstream":[],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer errutil.IgnoreDeferred(manager.Close)
	channels := manager.GetAccountChannels("acct_volc")
	if len(channels) != 1 {
		t.Fatalf("channels=%d", len(channels))
	}

	runner := NewAutoDiscoveryRunner(nil, nil)
	runner.volcengineControlPlaneEndpoint = server.URL
	result := runner.discoverVolcenginePlanEndpoint(context.Background(), server.Client(), &channels[0].Upstream,
		"https://ark.cn-beijing.volces.com/api/plan", "ark-inference", manager)

	if !result.ProtocolOk {
		t.Fatalf("发现应成功: %+v", result)
	}
	if got := strings.Join(result.Models, ","); got != "doubao-seed-2.1-turbo,kimi-k3" {
		t.Fatalf("非对话模型应被过滤、对话模型保留, got=%q", got)
	}
	if result.ModelsCount != 2 {
		t.Fatalf("ModelsCount = %d, want 2", result.ModelsCount)
	}
	if !strings.Contains(result.ModelDiscoveryMessage, "已过滤 4 个非对话模型") {
		t.Fatalf("发现说明应含过滤数量, got=%q", result.ModelDiscoveryMessage)
	}
	if result.ModelDiscoverySource != ModelDiscoverySourceControlPlane {
		t.Fatalf("来源应为管控面, got=%q", result.ModelDiscoverySource)
	}
}

func TestVolcenginePlanClientDetectPlansTeamOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch action := r.URL.Query().Get("Action"); action {
		case "GetPersonalPlan":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"ResourceNotFound.Plan","Message":"not found"}}}`))
		case "GetSeatInfo":
			var body struct{ Scene string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Scene == volcengineAgentPlanEnterpriseScene {
				_, _ = w.Write([]byte(`{"Result":{"SeatID":"seat-001"}}`))
				return
			}
			// Coding Plan 企业版：未绑席位（空 SeatID 不报错）。
			_, _ = w.Write([]byte(`{"Result":{"SeatID":""}}`))
		default:
			t.Errorf("未知 action: %s", action)
		}
	}))
	defer server.Close()
	client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
	pair := &config.VolcengineAccessKeyPair{AccessKeyID: "AK", SecretAccessKey: "SK"}
	buckets, primary, err := client.DetectPlans(context.Background(), pair, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 {
		t.Fatalf("buckets=%+v, want 1 team agent bucket", buckets)
	}
	bucket := buckets[0]
	if bucket.Product != volcenginePlanAgent || bucket.Edition != volcengineEditionTeam || bucket.SeatID != "seat-001" {
		t.Fatalf("bucket=%+v", bucket)
	}
	if primary.Plan != volcenginePlanAgent || primary.Status != "Running" {
		t.Fatalf("primary=%+v", primary)
	}
}

func TestVolcenginePlanClientDetectPlansPersonalAndTeamCoexist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch action := r.URL.Query().Get("Action"); action {
		case "GetPersonalPlan":
			var body struct{ Plan string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Plan != "CodingPlan" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"ResourceNotFound.Plan","Message":"not found"}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"Result":{"PlanType":"Pro","Status":"Running"}}`))
		case "GetSeatInfo":
			var body struct{ Scene string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Scene == volcengineAgentPlanEnterpriseScene {
				// 多字段路径形态：Seat 嵌套。
				_, _ = w.Write([]byte(`{"Result":{"Seat":{"SeatID":"seat-a"}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"Result":{}}`))
		default:
			t.Errorf("未知 action: %s", action)
		}
	}))
	defer server.Close()
	client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
	pair := &config.VolcengineAccessKeyPair{AccessKeyID: "AK", SecretAccessKey: "SK"}
	buckets, primary, err := client.DetectPlans(context.Background(), pair, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets=%+v, want personal coding + team agent", buckets)
	}
	// 个人版优先于团队版：primary 应为 personal coding。
	if primary.Plan != volcenginePlanCoding {
		t.Fatalf("primary=%+v, want personal coding_plan", primary)
	}
	// hint 指向 agent_plan 时应选中 team agent 桶。
	_, primary, err = client.DetectPlans(context.Background(), pair, "agent_plan")
	if err != nil {
		t.Fatal(err)
	}
	if primary.Plan != volcenginePlanAgent {
		t.Fatalf("hint=agent_plan primary=%+v", primary)
	}
}

func TestVolcenginePlanClientFetchSeatAFPUsage(t *testing.T) {
	t.Run("窗口在 Result 顶层", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if action := r.URL.Query().Get("Action"); action != "GetSeatAFPUsage" {
				t.Errorf("action=%s", action)
			}
			var body struct{ SeatIDs []string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.SeatIDs) != 1 || body.SeatIDs[0] != "seat-001" {
				t.Errorf("SeatIDs=%v", body.SeatIDs)
			}
			_, _ = w.Write([]byte(`{"Result":{
				"AFPFiveHour":{"Quota":50,"Used":12.5,"ResetTime":1778806800000},
				"AFPMonthly":{"Quota":2000,"Used":850.5,"ResetTime":1780531200000}}}`))
		}))
		defer server.Close()
		client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
		usage, err := client.fetchSeatAFPUsage(context.Background(), &config.VolcengineAccessKeyPair{AccessKeyID: "AK", SecretAccessKey: "SK"}, "seat-001")
		if err != nil {
			t.Fatal(err)
		}
		if usage.FiveHour == nil || usage.FiveHour.Quota != 50 || usage.Monthly == nil || usage.Monthly.Used != 850.5 {
			t.Fatalf("usage=%+v", usage)
		}
	})
	t.Run("窗口在 Seats 数组并按席位匹配", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"Result":{"Seats":[
				{"SeatID":"seat-other","AFPFiveHour":{"Quota":1,"Used":1,"ResetTime":1}},
				{"SeatID":"seat-001","AFPFiveHour":{"Quota":60,"Used":30,"ResetTime":1778806800000},"AFPWeekly":{"Quota":600,"Used":300,"ResetTime":1779062400000}}]}}`))
		}))
		defer server.Close()
		client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
		usage, err := client.fetchSeatAFPUsage(context.Background(), &config.VolcengineAccessKeyPair{AccessKeyID: "AK", SecretAccessKey: "SK"}, "seat-001")
		if err != nil {
			t.Fatal(err)
		}
		if usage.FiveHour == nil || usage.FiveHour.Quota != 60 || usage.Weekly == nil || usage.Weekly.Quota != 600 {
			t.Fatalf("usage=%+v", usage)
		}
	})
}

func TestVolcenginePlanClientFetchSeatInfoUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if action := r.URL.Query().Get("Action"); action != "GetSeatInfoUsage" {
			t.Errorf("action=%s", action)
		}
		var body struct {
			SeatID string
			Scene  string
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.SeatID != "seat-001" || body.Scene != "" {
			t.Errorf("body=%+v, want SeatID=seat-001 Scene 为空", body)
		}
		_, _ = w.Write([]byte(`{"Result":{"QuotaUsage":[
			{"Level":"session","Percent":42.5,"ResetTimestamp":1782226478},
			{"Level":"monthly","Percent":9.5,"ResetTimestamp":-1}]}}`))
	}))
	defer server.Close()
	client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
	usage, err := client.fetchSeatInfoUsage(context.Background(), &config.VolcengineAccessKeyPair{AccessKeyID: "AK", SecretAccessKey: "SK"}, "seat-001")
	if err != nil {
		t.Fatal(err)
	}
	if usage.FiveHour == nil || usage.FiveHour.UsedPercent == nil || *usage.FiveHour.UsedPercent != 42.5 {
		t.Fatalf("fiveHour=%+v", usage.FiveHour)
	}
	if usage.Monthly == nil || usage.Monthly.UsedPercent == nil || *usage.Monthly.UsedPercent != 9.5 {
		t.Fatalf("monthly=%+v", usage.Monthly)
	}
}

func TestVolcenginePlanClientFetchBucketsUsageIsolation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch action := r.URL.Query().Get("Action"); action {
		case "GetCodingPlanUsage":
			_, _ = w.Write([]byte(`{"Result":{"QuotaUsage":[{"Level":"weekly","Percent":20,"ResetTimestamp":-1}]}}`))
		case "GetSeatInfo":
			var body struct{ Scene string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Scene == volcengineAgentPlanEnterpriseScene {
				_, _ = w.Write([]byte(`{"Result":{"SeatID":"seat-x"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"Result":{}}`))
		case "GetSeatAFPUsage":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"AccessDenied","Message":"denied"}}}`))
		default:
			t.Errorf("未知 action: %s", action)
		}
	}))
	defer server.Close()
	client := &volcenginePlanClient{Endpoint: server.URL, HTTPClient: server.Client()}
	pair := &config.VolcengineAccessKeyPair{AccessKeyID: "AK", SecretAccessKey: "SK"}
	buckets := []config.VolcenginePlanBucket{
		{Product: volcenginePlanCoding, Edition: volcengineEditionPersonal, Status: "Running"},
		{Product: volcenginePlanAgent, Edition: volcengineEditionTeam, SeatID: "seat-x", Status: "Running"},
	}
	out := client.FetchBucketsUsage(context.Background(), pair, buckets)
	if len(out) != 2 {
		t.Fatalf("len=%d", len(out))
	}
	if out[0].Usage == nil || out[0].Usage.Error != "" || out[0].Usage.Weekly == nil {
		t.Fatalf("personal coding usage=%+v", out[0].Usage)
	}
	if out[1].Usage == nil || out[1].Usage.Error == "" {
		t.Fatalf("team agent usage 应记录错误: %+v", out[1].Usage)
	}
	// 主桶用量：personal coding 正常返回。
	if usage := primaryUsageFromBuckets(out, volcenginePlanCoding); usage == nil || usage.Error != "" {
		t.Fatalf("primary usage=%+v", usage)
	}
}
