package siteaccess

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestEvidenceLevels_ProgressionAndNoDowngrade(t *testing.T) {
	_, uid, store, cfgManager, adapter := newProvisionTestDeps(t)

	if _, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"},
	}); err != nil {
		t.Fatalf("接入: %v", err)
	}
	account, _, _ := store.Get(uid)
	if len(account.Evidence) != 1 || account.Evidence[0].Level != EvidenceConfigured {
		t.Fatalf("接入后应为 configured 证据: %+v", account.Evidence)
	}

	// 管理面信息验证成功 → info_verified
	if err := VerifyAccountInfo(context.Background(), store, adapter, uid); err != nil {
		t.Fatalf("信息验证: %v", err)
	}
	account, _, _ = store.Get(uid)
	if account.Evidence[0].Level != EvidenceInfoVerified || account.Evidence[0].InfoVerifiedAt == nil {
		t.Fatalf("信息验证后应升级为 info_verified: %+v", account.Evidence[0])
	}

	// 真实请求验证成功（假站点不实现推理端点，这里先验证失败路径不写死）；
	// 直接通过记录函数模拟真实流量成功 → real_request
	RecordRealRequest(store, uid, "chat", "default", true, "")
	account, _, _ = store.Get(uid)
	if account.Evidence[0].Level != EvidenceRealRequest || account.Evidence[0].RealRequestAt == nil {
		t.Fatalf("真实请求成功后应升级为 real_request: %+v", account.Evidence[0])
	}

	// 之后探针失败：只写诊断，等级不降、Key 不禁用
	RecordInfoVerified(store, uid, "chat", "default", context.DeadlineExceeded)
	account, _, _ = store.Get(uid)
	if account.Evidence[0].Level != EvidenceRealRequest {
		t.Fatalf("探针失败不应降级证据: %+v", account.Evidence[0])
	}
	if account.Evidence[0].LastDiagnostic == "" {
		t.Fatalf("探针失败应记录诊断")
	}
	if len(cfgManager.GetConfig().ChatUpstream[0].APIKeys) != 1 {
		t.Fatalf("探针失败不应禁用 Key")
	}
}

func TestVerifyRealRequest_FailureOnlyRecordsDiagnostic(t *testing.T) {
	_, uid, store, cfgManager, adapter := newProvisionTestDeps(t)

	if _, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"},
	}); err != nil {
		t.Fatalf("接入: %v", err)
	}

	// 把渠道指到始终 500 的推理端点，模拟真实请求失败
	failSrv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream broken"}}`))
	})
	url := failSrv.URL
	if _, err := cfgManager.UpdateChatUpstream(0, config.UpstreamUpdate{BaseURL: &url}); err != nil {
		t.Fatalf("更新渠道地址: %v", err)
	}

	err := VerifyRealRequest(context.Background(), store, cfgManager, uid, "chat", "default", "gpt-4o")
	if err == nil {
		t.Fatalf("500 推理端点应验证失败")
	}
	account, _, _ := store.Get(uid)
	if account.Evidence[0].Level != EvidenceConfigured {
		t.Fatalf("真实请求失败不应升级证据: %+v", account.Evidence[0])
	}
	if account.Evidence[0].LastDiagnostic == "" {
		t.Fatalf("失败应记录诊断")
	}
	if len(cfgManager.GetConfig().ChatUpstream[0].APIKeys) != 1 {
		t.Fatalf("验证失败不应禁用 Key")
	}
}

func TestVerifyRealRequest_Success(t *testing.T) {
	_, uid, store, cfgManager, adapter := newProvisionTestDeps(t)

	if _, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"},
	}); err != nil {
		t.Fatalf("接入: %v", err)
	}

	// 用真实推理假端点替换渠道 BaseURL
	inferenceSrv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk-") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
	})
	url := inferenceSrv.URL
	if _, err := cfgManager.UpdateChatUpstream(0, config.UpstreamUpdate{BaseURL: &url}); err != nil {
		t.Fatalf("更新渠道地址: %v", err)
	}

	if err := VerifyRealRequest(context.Background(), store, cfgManager, uid, "chat", "default", "gpt-4o"); err != nil {
		t.Fatalf("真实请求验证应成功: %v", err)
	}
	account, _, _ := store.Get(uid)
	if account.Evidence[0].Level != EvidenceRealRequest {
		t.Fatalf("真实请求成功后应为 real_request: %+v", account.Evidence[0])
	}
}

func TestReconcileLoop_PeriodicDisable(t *testing.T) {
	site, uid, store, cfgManager, adapter := newProvisionTestDeps(t)

	if _, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"},
	}); err != nil {
		t.Fatalf("接入: %v", err)
	}

	account, _, _ := store.Get(uid)
	site.deleteTokenByName(account.ProvisionedKeys[0].Name)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() { defer close(done); reconcileLoop(ctx, store, cfgManager, adapter, 20*time.Millisecond) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(cfgManager.GetConfig().ChatUpstream[0].DisabledAPIKeys) == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("定期对账未在期限内停用远端已删 Key")
}

func TestRecordRealTrafficByChannelName(t *testing.T) {
	_, uid, store, cfgManager, adapter := newProvisionTestDeps(t)
	if _, err := ProvisionProtocol(context.Background(), store, cfgManager, adapter, ProvisionRequest{
		AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"},
	}); err != nil {
		t.Fatalf("接入: %v", err)
	}

	SetDefaultStore(store)
	defer SetDefaultStore(nil)

	channelName := cfgManager.GetConfig().ChatUpstream[0].Name
	RecordRealTrafficByChannelName(channelName, true, "")
	account, _, _ := store.Get(uid)
	if account.Evidence[0].Level != EvidenceRealRequest {
		t.Fatalf("真实流量成功应回填 real_request: %+v", account.Evidence[0])
	}

	// 非托管渠道名不应产生任何记录
	before := len(account.Evidence)
	RecordRealTrafficByChannelName("user-created-channel", true, "")
	account, _, _ = store.Get(uid)
	if len(account.Evidence) != before {
		t.Fatalf("非托管渠道不应产生证据记录")
	}
}
