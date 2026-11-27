package siteaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestSub2APIProvisionSyncAndImportContract(t *testing.T) {
	created := 0
	ratio := 0.0
	var keys []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := func(data any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "success", "data": data})
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get("Authorization") != "Bearer jwt-test" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/v1/auth/me":
			reply(map[string]any{"id": 7, "username": "tester", "status": "active", "balance": 12.5})
		case "/api/v1/groups/available":
			reply([]any{map[string]any{"id": 3, "name": "Codex", "platform": "openai", "status": "active", "rate_multiplier": 1}})
		case "/api/v1/groups/rates":
			reply(map[string]float64{"3": ratio})
		case "/api/v1/keys":
			if r.Method == "GET" {
				reply(map[string]any{"items": keys, "total": len(keys), "page": 1, "page_size": 100, "pages": 1})
				return
			}
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("缺少幂等头")
				w.WriteHeader(400)
				return
			}
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["group_id"] != float64(3) || payload["model_limits"] != nil {
				t.Errorf("不符合 sub2api 创建契约: %+v", payload)
			}
			created++
			key := map[string]any{"id": 12, "name": payload["name"], "key": "sk-test", "group_id": 3, "status": "active", "quota": 0, "quota_used": 0}
			keys = append(keys, key)
			reply(key)
		case "/v1/models":
			if r.Header.Get("Authorization") != "Bearer sk-test" {
				t.Error("推理目录错误使用管理令牌")
			}
			fmt.Fprint(w, `{"data":[{"id":"gpt-test"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	store := NewAccountStore(filepath.Join(t.TempDir(), "accounts.json"))
	uid, err := store.Upsert(SiteAccount{SiteType: "sub2api", BaseURL: srv.URL, UserID: "7", AccessToken: "jwt-test"})
	if err != nil {
		t.Fatal(err)
	}
	cm, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	adapter := &NewApiAdapter{}
	if err := VerifyAccountInfo(context.Background(), store, adapter, uid); err != nil {
		t.Fatal(err)
	}
	request := ProvisionRequest{AccountUID: uid, Agent: "codex", Groups: []string{"3"}, Models: []string{"gpt-test"}}
	for i := 0; i < 2; i++ {
		result, err := ProvisionProtocol(context.Background(), store, cm, adapter, request)
		if err != nil || result.Groups[0].Skipped != "" {
			t.Fatalf("接入: %+v %v", result, err)
		}
	}
	if created != 1 {
		t.Fatalf("重复创建 Key: %d", created)
	}
	ch := cm.GetConfig().ResponsesUpstream[0]
	if ch.ServiceType != "responses" || ch.ModelMapping["sol"] != "gpt-test" || ch.BillingMultiplier() != 0 {
		t.Fatalf("默认 Agent 配置或零倍率丢失: %+v", ch)
	}
	ratio = 2
	if _, err := ReconcileAccount(context.Background(), store, cm, adapter, uid); err != nil {
		t.Fatal(err)
	}
	ch = cm.GetConfig().ResponsesUpstream[0]
	if ch.BillingMultiplier() != 2 || !ch.IsAPIKeyRemoteAvailable(ch.APIKeys[0]) {
		t.Fatal("应更新渠道倍率，不应限制 Key 调度")
	}
}

func TestInferenceKeyImportNeedsNoManagementAPI(t *testing.T) {
	store := NewAccountStore(filepath.Join(t.TempDir(), "accounts.json"))
	uid, err := store.Upsert(SiteAccount{SiteType: "sub2api", CredentialMode: "key", BaseURL: "https://relay.invalid", AccessToken: "sk-inference"})
	if err != nil {
		t.Fatal(err)
	}
	cm, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	result, err := ProvisionProtocol(context.Background(), store, cm, &NewApiAdapter{}, ProvisionRequest{AccountUID: uid, Agent: "claude", Models: []string{"chosen-model"}})
	if err != nil || len(result.Groups) != 1 {
		t.Fatalf("导入失败: %+v %v", result, err)
	}
	ch := cm.GetConfig().Upstream[0]
	if ch.APIKeys[0] != "sk-inference" || ch.ModelMapping["claude"] != "chosen-model" || ch.GroupMultiplier != nil {
		t.Fatal("推理 Key 或模型配置不符，未知倍率不得当作 0")
	}
}
