package messages

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
)

// collectClaudeSystemBlockText 从顶层 system（应为 block 数组）里按序拼出各 text block 的文本，
// 仅用于断言内容存在与顺序；同时校验 system 已是数组结构而非被拍平的字符串。
func collectClaudeSystemBlockText(t *testing.T, system interface{}, captured []byte) string {
	t.Helper()
	arr, ok := system.([]interface{})
	if !ok {
		t.Fatalf("top-level system should be a block array, got %T; body=%s", system, string(captured))
	}
	var parts []string
	for _, raw := range arr {
		block, _ := raw.(map[string]interface{})
		if text, _ := block["text"].(string); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// TestMessagesHandler_NormalizeSystemRoleToTopLevel 验证：messages 渠道开启
// NormalizeSystemRoleToTopLevel 后，无论上游 ServiceType 为何，转发前都会把 messages
// 数组里的 system 角色抽回顶层 system 字段。归一化发生在 provider 分发之前的统一入口，
// 因此对 openai/gemini/claude 等所有上游类型一致生效。
func TestMessagesHandler_NormalizeSystemRoleToTopLevel(t *testing.T) {
	const reqBody = `{"model":"test-model","system":"base prompt","messages":[{"role":"system","content":"you are helpful"},{"role":"user","content":[{"type":"text","text":"hello"}]}]}`

	tests := []struct {
		name         string
		serviceType  string
		enabled      bool
		responseBody string
		assertReq    func(t *testing.T, captured []byte)
	}{
		{
			name:         "openai_upstream_enabled_extracts_system",
			serviceType:  "openai",
			enabled:      true,
			responseBody: `{"id":"chatcmpl","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			assertReq: func(t *testing.T, captured []byte) {
				// OpenAI provider 将顶层 system 转为 messages[0].role=system；
				// 归一化后顶层应合并 base prompt + 抽取出的 system 文本。
				var req map[string]interface{}
				if err := json.Unmarshal(captured, &req); err != nil {
					t.Fatalf("unmarshal upstream request: %v", err)
				}
				msgs, ok := req["messages"].([]interface{})
				if !ok || len(msgs) == 0 {
					t.Fatalf("messages shape invalid: %s", string(captured))
				}
				// 上游收到的 messages 中不应再出现独立的 system 角色消息内容 "you are helpful"
				// 作为非首条系统消息；它应已被合并进顶层 system，再由 OpenAI provider 放到首条 system。
				first, _ := msgs[0].(map[string]interface{})
				if role, _ := first["role"].(string); role != "system" {
					t.Fatalf("expected first message to be system, got %v; body=%s", role, string(captured))
				}
				sysText, _ := first["content"].(string)
				if !strings.Contains(sysText, "base prompt") || !strings.Contains(sysText, "you are helpful") {
					t.Fatalf("merged system text missing parts: %q; body=%s", sysText, string(captured))
				}
				// 后续消息里不应再有 role=system
				for i := 1; i < len(msgs); i++ {
					m, _ := msgs[i].(map[string]interface{})
					if role, _ := m["role"].(string); role == "system" {
						t.Fatalf("unexpected leftover system role at index %d; body=%s", i, string(captured))
					}
				}
			},
		},
		{
			name:         "claude_upstream_enabled_extracts_system",
			serviceType:  "claude",
			enabled:      true,
			responseBody: `{"id":"msg","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
			assertReq: func(t *testing.T, captured []byte) {
				var req map[string]interface{}
				if err := json.Unmarshal(captured, &req); err != nil {
					t.Fatalf("unmarshal upstream request: %v", err)
				}
				// claude 直传：顶层 system 应为保留 block 结构的数组（不拍平成字符串），
				// 且按顺序包含 base prompt 与抽取出的 system 文本。
				sysText := collectClaudeSystemBlockText(t, req["system"], captured)
				if !strings.Contains(sysText, "base prompt") || !strings.Contains(sysText, "you are helpful") {
					t.Fatalf("merged top-level system missing parts: %q; body=%s", sysText, string(captured))
				}
				msgs, ok := req["messages"].([]interface{})
				if !ok {
					t.Fatalf("messages shape invalid: %s", string(captured))
				}
				for _, raw := range msgs {
					m, _ := raw.(map[string]interface{})
					if role, _ := m["role"].(string); role == "system" {
						t.Fatalf("system role should be removed from messages; body=%s", string(captured))
					}
				}
			},
		},
		{
			name:         "claude_upstream_disabled_keeps_system_role",
			serviceType:  "claude",
			enabled:      false,
			responseBody: `{"id":"msg","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
			assertReq: func(t *testing.T, captured []byte) {
				var req map[string]interface{}
				if err := json.Unmarshal(captured, &req); err != nil {
					t.Fatalf("unmarshal upstream request: %v", err)
				}
				msgs, ok := req["messages"].([]interface{})
				if !ok {
					t.Fatalf("messages shape invalid: %s", string(captured))
				}
				found := false
				for _, raw := range msgs {
					m, _ := raw.(map[string]interface{})
					if role, _ := m["role"].(string); role == "system" {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("disabled switch should keep system role in messages; body=%s", string(captured))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read upstream request: %v", err)
				}
				captured = body
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer upstream.Close()

			router := newMessagesTestRouter(t, config.UpstreamConfig{
				Name:                          tt.name,
				BaseURL:                       upstream.URL,
				APIKeys:                       []string{"sk-test"},
				ServiceType:                   tt.serviceType,
				Status:                        "active",
				NormalizeSystemRoleToTopLevel: tt.enabled,
			})

			w := performMessagesHandlerRequest(t, router, reqBody)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
			}
			tt.assertReq(t, captured)
		})
	}
}

// TestMessagesHandler_NormalizeSystemRolePreservesCacheControl 验证归一化后
// 顶层 system 数组原有 block 的 cache_control 被原样保留，不会因抽取 inline system 而丢失。
func TestMessagesHandler_NormalizeSystemRolePreservesCacheControl(t *testing.T) {
	const reqBody = `{"model":"claude-sonnet-5","system":[{"type":"text","text":"cached base","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"system","content":"you are helpful"},{"role":"user","content":[{"type":"text","text":"hello"}]}]}`

	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream request: %v", err)
		}
		captured = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	router := newMessagesTestRouter(t, config.UpstreamConfig{
		Name:                          "cache-control-preserve",
		BaseURL:                       upstream.URL,
		APIKeys:                       []string{"sk-test"},
		ServiceType:                   "claude",
		Status:                        "active",
		NormalizeSystemRoleToTopLevel: true,
	})

	w := performMessagesHandlerRequest(t, router, reqBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}

	var req map[string]interface{}
	if err := json.Unmarshal(captured, &req); err != nil {
		t.Fatalf("unmarshal upstream request: %v", err)
	}
	arr, ok := req["system"].([]interface{})
	if !ok {
		t.Fatalf("top-level system should be a block array, got %T; body=%s", req["system"], string(captured))
	}
	if len(arr) == 0 {
		t.Fatalf("system array empty; body=%s", string(captured))
	}
	first, _ := arr[0].(map[string]interface{})
	if text, _ := first["text"].(string); text != "cached base" {
		t.Fatalf("first block text = %q, want %q; body=%s", text, "cached base", string(captured))
	}
	cc, ok := first["cache_control"].(map[string]interface{})
	if !ok {
		t.Fatalf("cache_control lost on first system block; body=%s", string(captured))
	}
	if ccType, _ := cc["type"].(string); ccType != "ephemeral" {
		t.Fatalf("cache_control.type = %q, want ephemeral; body=%s", ccType, string(captured))
	}
	// inline system 内容应作为独立新 block 追加，而不是合并进第一个 block
	merged := collectClaudeSystemBlockText(t, req["system"], captured)
	if !strings.Contains(merged, "you are helpful") {
		t.Fatalf("appended inline system missing; body=%s", string(captured))
	}
}

func TestMessagesHandler_AutoManagedCompshareNormalizesSystemRoles(t *testing.T) {
	const reqBody = `{"model":"claude-sonnet-5","system":[{"type":"text","text":"base prompt"}],"messages":[{"role":"user","content":[{"type":"text","text":"first user"}]},{"role":"system","content":"mid prompt"},{"role":"assistant","content":[{"type":"text","text":"prior answer"}]},{"role":"user","content":[{"type":"text","text":"second user"}]},{"role":"system","content":[{"type":"text","text":"tail prompt"}]}]}`

	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream request: %v", err)
		}
		captured = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	router := newMessagesTestRouter(t, config.UpstreamConfig{
		Name:        "compshare-claude",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "claude",
		ProviderID:  "compshare",
		AutoManaged: true,
		Status:      "active",
	})

	w := performMessagesHandlerRequest(t, router, reqBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}

	var forwarded map[string]interface{}
	if err := json.Unmarshal(captured, &forwarded); err != nil {
		t.Fatalf("unmarshal upstream request: %v", err)
	}
	system := collectClaudeSystemBlockText(t, forwarded["system"], captured)
	for _, want := range []string{"base prompt", "mid prompt", "tail prompt"} {
		if !strings.Contains(system, want) {
			t.Fatalf("top-level system missing %q: %q; body=%s", want, system, string(captured))
		}
	}

	messages, ok := forwarded["messages"].([]interface{})
	if !ok {
		t.Fatalf("messages shape invalid: %s", string(captured))
	}
	wantRoles := []string{"user", "assistant", "user"}
	if len(messages) != len(wantRoles) {
		t.Fatalf("message count = %d, want %d; body=%s", len(messages), len(wantRoles), string(captured))
	}
	for i, wantRole := range wantRoles {
		message, _ := messages[i].(map[string]interface{})
		if role, _ := message["role"].(string); role != wantRole {
			t.Fatalf("messages[%d].role = %q, want %q; body=%s", i, role, wantRole, string(captured))
		}
	}
}

// TestMessagesHandler_AutoNormalizeSystemRoleByMappedModel 验证：渠道未开手动开关，
// 但 ModelMapping 把上线模型重定向为非 claude 家族（deepseek-v4-flash）时，
// 转发前自动把 messages 中的 system 角色抽回顶层 system 字段。
func TestMessagesHandler_AutoNormalizeSystemRoleByMappedModel(t *testing.T) {
	const reqBody = `{"model":"claude-sonnet-5","system":[{"type":"text","text":"base prompt"}],"messages":[{"role":"system","content":"you are helpful"},{"role":"user","content":[{"type":"text","text":"hello"}]}]}`

	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream request: %v", err)
		}
		captured = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	router := newMessagesTestRouter(t, config.UpstreamConfig{
		Name:         "auto-normalize-by-model",
		BaseURL:      upstream.URL,
		APIKeys:      []string{"sk-test"},
		ServiceType:  "claude",
		Status:       "active",
		ModelMapping: map[string]string{"claude-sonnet-5": "deepseek-v4-flash"},
	})

	w := performMessagesHandlerRequest(t, router, reqBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}

	var forwarded map[string]interface{}
	if err := json.Unmarshal(captured, &forwarded); err != nil {
		t.Fatalf("unmarshal upstream request: %v", err)
	}
	sysText := collectClaudeSystemBlockText(t, forwarded["system"], captured)
	if !strings.Contains(sysText, "base prompt") || !strings.Contains(sysText, "you are helpful") {
		t.Fatalf("top-level system missing parts after auto normalize: %q; body=%s", sysText, string(captured))
	}
	msgs, ok := forwarded["messages"].([]interface{})
	if !ok {
		t.Fatalf("messages shape invalid: %s", string(captured))
	}
	for _, raw := range msgs {
		m, _ := raw.(map[string]interface{})
		if role, _ := m["role"].(string); role == "system" {
			t.Fatalf("system role should be auto-extracted for non-claude wire model; body=%s", string(captured))
		}
	}
}

// TestMessagesHandler_AutoNormalizeSystemRoleByConverterFingerprint 验证：渠道未开手动开关
// 且模型为 claude 家族时首个请求保持原样；上游响应头携带 new-api 指纹被学习后，
// 后续请求自动归一化 messages 中的 system 角色。
func TestMessagesHandler_AutoNormalizeSystemRoleByConverterFingerprint(t *testing.T) {
	restore := common.SwapConverterUpstreamCacheForTest(config.NewConverterUpstreamCache())
	defer restore()

	const reqBody = `{"model":"claude-sonnet-5","system":[{"type":"text","text":"base prompt"}],"messages":[{"role":"system","content":"you are helpful"},{"role":"user","content":[{"type":"text","text":"hello"}]}]}`

	var captured [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream request: %v", err)
		}
		captured = append(captured, body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-New-Api-Version", "v1.0.0-rc.25")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	router := newMessagesTestRouter(t, config.UpstreamConfig{
		Name:        "auto-normalize-by-fingerprint",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "claude",
		Status:      "active",
		ChannelUID:  "ch-converter-fingerprint-test",
	})

	hasInlineSystemRole := func(body []byte) bool {
		var parsed map[string]interface{}
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("unmarshal upstream request: %v", err)
		}
		msgs, _ := parsed["messages"].([]interface{})
		for _, raw := range msgs {
			m, _ := raw.(map[string]interface{})
			if role, _ := m["role"].(string); role == "system" {
				return true
			}
		}
		return false
	}

	// 第一次请求：指纹尚未学习，claude 模型不触发自动归一化，inline system 原样透传
	w1 := performMessagesHandlerRequest(t, router, reqBody)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, body=%s", w1.Code, w1.Body.String())
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(captured))
	}
	if !hasInlineSystemRole(captured[0]) {
		t.Fatalf("first request should keep inline system role (not yet learned); body=%s", string(captured[0]))
	}

	// 第二次请求：指纹已学习，自动归一化生效
	w2 := performMessagesHandlerRequest(t, router, reqBody)
	if w2.Code != http.StatusOK {
		t.Fatalf("second request status = %d, body=%s", w2.Code, w2.Body.String())
	}
	if len(captured) != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", len(captured))
	}
	if hasInlineSystemRole(captured[1]) {
		t.Fatalf("second request should be auto-normalized after fingerprint learned; body=%s", string(captured[1]))
	}
	var second map[string]interface{}
	if err := json.Unmarshal(captured[1], &second); err != nil {
		t.Fatalf("unmarshal second upstream request: %v", err)
	}
	if sysText := collectClaudeSystemBlockText(t, second["system"], captured[1]); !strings.Contains(sysText, "you are helpful") {
		t.Fatalf("second request top-level system missing extracted text: %q", sysText)
	}
}
