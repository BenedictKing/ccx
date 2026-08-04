package autopilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// ── 测试辅助：mock new-api 服务端 ──

// newMockNewApiServer 启动一个模拟 new-api 站点，按路径分发响应。
// handler 里可通过 r.Header 校验认证头是否正确注入。
func newMockNewApiServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func writeEnvelope(w http.ResponseWriter, success bool, data interface{}, message string) {
	envelope := map[string]interface{}{
		"success": success,
		"data":    data,
		"message": message,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope)
}

// ── Verify ──

func TestNewApiAdapter_Verify_Success(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			t.Fatalf("意外路径: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("意外方法: %s", r.Method)
		}
		// 默认 bearer 模式
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization 头不匹配: got=%s", got)
		}
		if got := r.Header.Get("New-API-User"); got != "42" {
			t.Fatalf("New-API-User 头不匹配: got=%s", got)
		}
		if got := r.Header.Get("User-id"); got != "42" {
			t.Fatalf("User-id 头不匹配: got=%s", got)
		}
		writeEnvelope(w, true, NewApiUserSelf{ID: 42, Username: "alice", Quota: 100000, UsedQuota: 5000}, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	self, err := adapter.Verify(context.Background(), srv.URL, "test-token", "42", "")
	if err != nil {
		t.Fatalf("Verify 失败: %v", err)
	}
	if self.ID != 42 || self.Username != "alice" || self.Quota != 100000 || self.UsedQuota != 5000 {
		t.Fatalf("解析结果不符: %+v", self)
	}
}

func TestNewApiAdapter_Verify_RawAuthMode(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		// raw 模式不带 "Bearer " 前缀
		if got := r.Header.Get("Authorization"); got != "test-token" {
			t.Fatalf("raw 模式 Authorization 头不匹配: got=%s", got)
		}
		writeEnvelope(w, true, NewApiUserSelf{ID: 1}, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	if _, err := adapter.Verify(context.Background(), srv.URL, "test-token", "1", NewApiAuthModeRaw); err != nil {
		t.Fatalf("Verify 失败: %v", err)
	}
	if _, err := adapter.Verify(context.Background(), srv.URL, "test-token", "1", NewApiAuthModeRawAuth); err != nil {
		t.Fatalf("Verify(raw_auth) 失败: %v", err)
	}
}

func TestNewApiAdapter_Verify_InvalidToken(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, false, nil, "无效的令牌")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	_, err := adapter.Verify(context.Background(), srv.URL, "bad-token", "1", "")
	if err == nil {
		t.Fatal("期望返回错误，实际未报错")
	}
	if !containsSubstr(err.Error(), "无效的令牌") {
		t.Fatalf("错误信息未包含 message: %v", err)
	}
}

func TestNewApiAdapter_Verify_HTTPError(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	_, err := adapter.Verify(context.Background(), srv.URL, "bad-token", "1", "")
	if err == nil {
		t.Fatal("期望返回错误，实际未报错")
	}
}

func TestNewApiAdapter_Verify_MalformedEnvelope(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not-json`))
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	_, err := adapter.Verify(context.Background(), srv.URL, "token", "1", "")
	if err == nil {
		t.Fatal("期望信封解析失败报错")
	}
}

// ── FetchBalance ──

func TestNewApiAdapter_FetchBalance(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, NewApiUserSelf{ID: 1, Quota: 250000}, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	balance, currency, err := adapter.FetchBalance(context.Background(), srv.URL, "token", "1", "")
	if err != nil {
		t.Fatalf("FetchBalance 失败: %v", err)
	}
	if balance != 250000 {
		t.Fatalf("balance 不符: got=%v", balance)
	}
	if currency != "quota" {
		t.Fatalf("currency 不符: got=%s", currency)
	}
}

// ── FetchGroups ──

func TestNewApiAdapter_FetchGroups(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self/groups" {
			t.Fatalf("意外路径: %s", r.URL.Path)
		}
		writeEnvelope(w, true, map[string]NewApiGroupInfo{
			"default": {Desc: "默认分组", Ratio: 1.0},
			"vip":     {Desc: "VIP 分组", Ratio: 0.5},
		}, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	groups, err := adapter.FetchGroups(context.Background(), srv.URL, "token", "1", "")
	if err != nil {
		t.Fatalf("FetchGroups 失败: %v", err)
	}
	if groups["default"] != 1.0 || groups["vip"] != 0.5 {
		t.Fatalf("分组倍率不符: %+v", groups)
	}
}

// ── FetchModels ──

func TestNewApiAdapter_FetchModels(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/models" {
			t.Fatalf("意外路径: %s", r.URL.Path)
		}
		writeEnvelope(w, true, []string{"gpt-4o", "claude-3-5-sonnet"}, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	models, err := adapter.FetchModels(context.Background(), srv.URL, "token", "1", "")
	if err != nil {
		t.Fatalf("FetchModels 失败: %v", err)
	}
	if len(models) != 2 || models[0] != "gpt-4o" || models[1] != "claude-3-5-sonnet" {
		t.Fatalf("模型列表不符: %+v", models)
	}
}

// ── ListTokens / FindTokenByName ──

func TestNewApiAdapter_ListTokens_ItemsShape(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/token/" {
			t.Fatalf("意外路径: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("p"); got != "1" {
			t.Fatalf("分页参数 p 不符: got=%s", got)
		}
		if got := r.URL.Query().Get("size"); got != "100" {
			t.Fatalf("分页参数 size 不符: got=%s", got)
		}
		writeEnvelope(w, true, map[string]interface{}{
			"items": []NewApiToken{
				{ID: 1, Key: "sk-aaa", Name: "ccx-autopilot", Status: 1},
				{ID: 2, Key: "sk-bbb", Name: "other-key", Status: 1},
			},
		}, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	tokens, err := adapter.ListTokens(context.Background(), srv.URL, "token", "1", "", 1, 100)
	if err != nil {
		t.Fatalf("ListTokens 失败: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("token 数量不符: got=%d", len(tokens))
	}
}

func TestNewApiAdapter_ListTokens_ArrayShape(t *testing.T) {
	// 部分 fork data 直接是数组而非 {items:[...]}
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, []NewApiToken{
			{ID: 5, Key: "sk-ccc", Name: "ccx-autopilot", Status: 1},
		}, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	tokens, err := adapter.ListTokens(context.Background(), srv.URL, "token", "1", "", 1, 100)
	if err != nil {
		t.Fatalf("ListTokens(数组兼容) 失败: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Name != "ccx-autopilot" {
		t.Fatalf("数组兼容解析不符: %+v", tokens)
	}
}

func TestNewApiAdapter_FindTokenByName_Found(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, map[string]interface{}{
			"items": []NewApiToken{
				{ID: 1, Key: "sk-aaa", Name: "ccx-autopilot", Status: 1},
				{ID: 2, Key: "sk-bbb", Name: "other-key", Status: 1},
			},
		}, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	token, err := adapter.FindTokenByName(context.Background(), srv.URL, "token", "1", "", "ccx-autopilot")
	if err != nil {
		t.Fatalf("FindTokenByName 失败: %v", err)
	}
	if token == nil || token.ID != 1 {
		t.Fatalf("未找到期望的 token: %+v", token)
	}
}

func TestNewApiAdapter_FindTokenByName_NotFound(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, map[string]interface{}{
			"items": []NewApiToken{
				{ID: 2, Key: "sk-bbb", Name: "other-key", Status: 1},
			},
		}, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	token, err := adapter.FindTokenByName(context.Background(), srv.URL, "token", "1", "", "ccx-autopilot")
	if err != nil {
		t.Fatalf("FindTokenByName 失败: %v", err)
	}
	if token != nil {
		t.Fatalf("期望未找到，实际找到: %+v", token)
	}
}

func TestNewApiAdapter_FindTokenByName_SearchesLaterPages(t *testing.T) {
	firstPage := make([]NewApiToken, 100)
	for i := range firstPage {
		firstPage[i] = NewApiToken{ID: i + 1, Name: "other-" + strconv.Itoa(i)}
	}
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("p") {
		case "1":
			writeEnvelope(w, true, newApiTokenListData{Items: firstPage}, "")
		case "2":
			writeEnvelope(w, true, newApiTokenListData{Items: []NewApiToken{{ID: 101, Key: "sk-target", Name: "ccx-autopilot-default", Group: "default"}}}, "")
		default:
			t.Fatalf("意外页码: %s", r.URL.Query().Get("p"))
		}
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	token, err := adapter.FindTokenByName(context.Background(), srv.URL, "token", "1", "", "ccx-autopilot-default")
	if err != nil {
		t.Fatalf("FindTokenByName 失败: %v", err)
	}
	if token == nil || token.ID != 101 || token.Group != "default" {
		t.Fatalf("未从后续页面找到同名 key: %+v", token)
	}
}

func TestNewApiAdapter_DeleteToken(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/token/42" {
			t.Fatalf("删除请求不符: %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(w, true, nil, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	if err := adapter.DeleteToken(context.Background(), srv.URL, "token", "1", "", 42); err != nil {
		t.Fatalf("DeleteToken 失败: %v", err)
	}
}

// ── ProvisionKey ──

func TestNewApiAdapter_ProvisionKey_CreateNew(t *testing.T) {
	listCalls := 0
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/":
			listCalls++
			// 查重：无同名 key
			writeEnvelope(w, true, map[string]interface{}{"items": []NewApiToken{}}, "")
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/":
			var req NewApiCreateTokenRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("解析请求体失败: %v", err)
			}
			if req.Name != "ccx-autopilot" {
				t.Fatalf("建 key 名称不符: %s", req.Name)
			}
			if !req.UnlimitedQuota || req.ExpiredTime != -1 || req.RemainQuota != 0 {
				t.Fatalf("建 key 模板字段不符: %+v", req)
			}
			writeEnvelope(w, true, NewApiToken{ID: 99, Key: "sk-new-key", Name: req.Name, Status: 1}, "")
		default:
			t.Fatalf("意外请求: %s %s", r.Method, r.URL.Path)
		}
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	tokenID, key, reused, err := adapter.ProvisionKey(context.Background(), srv.URL, "token", "1", "", NewApiProvisionOptions{})
	if err != nil {
		t.Fatalf("ProvisionKey 失败: %v", err)
	}
	if reused {
		t.Fatal("期望新建，实际标记为复用")
	}
	if tokenID != 99 || key != "sk-new-key" {
		t.Fatalf("建 key 结果不符: id=%d key=%s", tokenID, key)
	}
	if listCalls != 1 {
		t.Fatalf("期望仅查重一次列表, got=%d", listCalls)
	}
}

func TestNewApiAdapter_ProvisionKey_ReuseExisting(t *testing.T) {
	postCalled := false
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/":
			writeEnvelope(w, true, map[string]interface{}{
				"items": []NewApiToken{
					{ID: 7, Key: "sk-existing", Name: "ccx-autopilot", Status: 1},
				},
			}, "")
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/":
			postCalled = true
			writeEnvelope(w, true, NewApiToken{ID: 999, Key: "sk-should-not-be-created"}, "")
		default:
			t.Fatalf("意外请求: %s %s", r.Method, r.URL.Path)
		}
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	tokenID, key, reused, err := adapter.ProvisionKey(context.Background(), srv.URL, "token", "1", "", NewApiProvisionOptions{})
	if err != nil {
		t.Fatalf("ProvisionKey 失败: %v", err)
	}
	if !reused {
		t.Fatal("期望复用已存在的 key，实际未标记复用")
	}
	if tokenID != 7 || key != "sk-existing" {
		t.Fatalf("复用结果不符: id=%d key=%s", tokenID, key)
	}
	if postCalled {
		t.Fatal("已存在同名 key 时不应再调用创建接口（不能重复创建）")
	}
}

func TestNewApiAdapter_ProvisionKey_RejectsExistingKeyInDifferentGroup(t *testing.T) {
	postCalled := false
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/":
			writeEnvelope(w, true, map[string]interface{}{
				"items": []NewApiToken{{ID: 7, Key: "sk-existing", Name: "ccx-autopilot-default", Group: "premium", Status: 1}},
			}, "")
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/":
			postCalled = true
			writeEnvelope(w, true, NewApiToken{ID: 999, Key: "sk-should-not-be-created"}, "")
		default:
			t.Fatalf("意外请求: %s %s", r.Method, r.URL.Path)
		}
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	_, _, _, err := adapter.ProvisionKey(context.Background(), srv.URL, "token", "1", "", NewApiProvisionOptions{
		Name:  "ccx-autopilot-default",
		Group: "default",
	})
	if err == nil {
		t.Fatal("同名但不同分组的 key 必须被拒绝")
	}
	if postCalled {
		t.Fatal("分组不匹配时不应新建或复用 key")
	}
}

func TestNewApiAdapter_ProvisionKey_WithModelsAndGroup(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/":
			writeEnvelope(w, true, map[string]interface{}{"items": []NewApiToken{}}, "")
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/":
			var req NewApiCreateTokenRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("解析请求体失败: %v", err)
			}
			if req.Group != "vip" {
				t.Fatalf("分组未透传: %s", req.Group)
			}
			if !req.ModelLimitsEnabled || req.ModelLimits != "gpt-4o,claude-3-5-sonnet" {
				t.Fatalf("model_limits 未按预期拼接: enabled=%v limits=%s", req.ModelLimitsEnabled, req.ModelLimits)
			}
			writeEnvelope(w, true, NewApiToken{ID: 1, Key: "sk-x", Group: req.Group}, "")
		}
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	_, _, _, err := adapter.ProvisionKey(context.Background(), srv.URL, "token", "1", "", NewApiProvisionOptions{
		Group:  "vip",
		Models: []string{"gpt-4o", "claude-3-5-sonnet"},
	})
	if err != nil {
		t.Fatalf("ProvisionKey 失败: %v", err)
	}
}

func TestNewApiAdapter_ProvisionKey_CreateResponseMissingKey_FallbackToList(t *testing.T) {
	// 部分上游创建响应不带明文 key，需要回查列表按 name 取。
	callCount := 0
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/":
			callCount++
			if callCount == 1 {
				// 第一次查重：无同名
				writeEnvelope(w, true, map[string]interface{}{"items": []NewApiToken{}}, "")
			} else {
				// 第二次回查：能找到刚创建的
				writeEnvelope(w, true, map[string]interface{}{
					"items": []NewApiToken{
						{ID: 55, Key: "sk-fallback", Name: "ccx-autopilot", Status: 1},
					},
				}, "")
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/":
			// 创建响应不带 key
			writeEnvelope(w, true, NewApiToken{ID: 55, Name: "ccx-autopilot"}, "")
		}
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	tokenID, key, reused, err := adapter.ProvisionKey(context.Background(), srv.URL, "token", "1", "", NewApiProvisionOptions{})
	if err != nil {
		t.Fatalf("ProvisionKey 失败: %v", err)
	}
	if reused {
		t.Fatal("期望标记为新建（非复用）")
	}
	if tokenID != 55 || key != "sk-fallback" {
		t.Fatalf("回查 fallback 结果不符: id=%d key=%s", tokenID, key)
	}
}

func TestNewApiAdapter_ProvisionKey_DefaultName(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/":
			writeEnvelope(w, true, map[string]interface{}{"items": []NewApiToken{}}, "")
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/":
			var req NewApiCreateTokenRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("解析请求体失败: %v", err)
			}
			if req.Name != DefaultNewApiProvisionKeyName {
				t.Fatalf("默认名称不符: got=%s want=%s", req.Name, DefaultNewApiProvisionKeyName)
			}
			writeEnvelope(w, true, NewApiToken{ID: 1, Key: "sk-x"}, "")
		}
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	if _, _, _, err := adapter.ProvisionKey(context.Background(), srv.URL, "token", "1", "", NewApiProvisionOptions{}); err != nil {
		t.Fatalf("ProvisionKey 失败: %v", err)
	}
}

// ── 认证头/信封解析边界 ──

func TestNewApiAdapter_UserIDOmitted_NoUserHeaders(t *testing.T) {
	srv := newMockNewApiServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("New-API-User") != "" || r.Header.Get("User-id") != "" {
			t.Fatal("userID 为空时不应带用户头")
		}
		writeEnvelope(w, true, NewApiUserSelf{ID: 1}, "")
	})

	adapter := &NewApiAdapter{HTTPClient: srv.Client()}
	if _, err := adapter.Verify(context.Background(), srv.URL, "token", "", ""); err != nil {
		t.Fatalf("Verify 失败: %v", err)
	}
}

func TestNewApiAdapter_EmptyBaseURL(t *testing.T) {
	adapter := &NewApiAdapter{}
	if _, err := adapter.Verify(context.Background(), "", "token", "1", ""); err == nil {
		t.Fatal("baseURL 为空时应报错")
	}
}

// ── maskAccessToken ──

func TestMaskAccessToken(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"abc", "****"},
		{"abcdefgh", "****efgh"},
		{"sk-1234567890", "****7890"},
	}
	for _, c := range cases {
		got := maskAccessToken(c.in)
		if got != c.want {
			t.Errorf("maskAccessToken(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ── 辅助 ──

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
