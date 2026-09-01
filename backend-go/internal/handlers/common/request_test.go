package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
)

func TestReadRequestBodyDecompressesZstd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := []byte(`{"model":"gpt-5","stream":true}`)

	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatalf("zstd.NewWriter() err = %v", err)
	}
	if _, err := encoder.Write(original); err != nil {
		t.Fatalf("zstd write err = %v", err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatalf("zstd close err = %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(compressed.Bytes()))
	c.Request.Header.Set("Content-Encoding", "zstd")

	got, err := ReadRequestBody(c, 1024*1024)
	if err != nil {
		t.Fatalf("ReadRequestBody() err = %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("decompressed body = %s, want %s", string(got), string(original))
	}
	if got := c.Request.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding header should be cleared after decompression, got %q", got)
	}
	restored, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("read restored body err = %v", err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("restored body = %s, want %s", string(restored), string(original))
	}
}

func TestNormalizeMetadataUserID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string // expected user_id value after normalization, empty means unchanged
	}{
		{
			name:     "v2.1.78 JSON - only device_id",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":"{\"device_id\":\"b854c106939c\",\"account_uuid\":\"\",\"session_id\":\"\"}"},"stream":true}`,
			expected: "user_b854c106939c",
		},
		{
			name:     "v2.1.78 JSON - device_id + session_id",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":"{\"device_id\":\"b854c106939c\",\"account_uuid\":\"\",\"session_id\":\"e692f803-4767\"}"}}`,
			expected: "user_b854c106939c_session_e692f803-4767",
		},
		{
			name:     "v2.1.78 JSON - device_id + account_uuid",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":"{\"device_id\":\"dev1\",\"account_uuid\":\"acc1\",\"session_id\":\"\"}"}}`,
			expected: "user_dev1_account_acc1",
		},
		{
			name:     "v2.1.78 JSON - all fields populated",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":"{\"device_id\":\"abc123\",\"account_uuid\":\"uuid-456\",\"session_id\":\"sess-789\"}"}}`,
			expected: "user_abc123_account_uuid-456_session_sess-789",
		},
		{
			name:     "v2.1.78 JSON - empty device_id, fallback to generic format",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":"{\"device_id\":\"\",\"account_uuid\":\"acc1\",\"session_id\":\"sess1\"}"}}`,
			expected: "account_uuid_acc1_session_id_sess1",
		},
		{
			name:     "v2.1.77 flat string user_id - no change",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":"user_67bad5_account__session_7581b58b"},"stream":true}`,
			expected: "user_67bad5_account__session_7581b58b",
		},
		{
			name:     "no metadata - no change",
			input:    `{"model":"claude-opus-4-6","stream":true}`,
			expected: "",
		},
		{
			name:     "empty user_id - no change",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":""},"stream":true}`,
			expected: "",
		},
		{
			name:     "invalid JSON in user_id - no change",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":"{invalid json"}}`,
			expected: "{invalid json",
		},
		{
			name:     "non-claude JSON object - generic key_value format",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":"{\"uid\":\"abc123\"}"}}`,
			expected: "uid_abc123",
		},
		{
			name:     "non-claude JSON object - multiple fields sorted",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":"{\"session\":\"xyz\",\"uid\":\"abc123\"}"}}`,
			expected: "session_xyz_uid_abc123",
		},
		{
			name:     "preserves other fields",
			input:    `{"model":"claude-opus-4-6","metadata":{"user_id":"{\"device_id\":\"dev1\",\"account_uuid\":\"acc1\",\"session_id\":\"sess1\"}"},"stream":true,"max_tokens":1024}`,
			expected: "user_dev1_account_acc1_session_sess1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeMetadataUserID([]byte(tt.input))

			if tt.expected == "" {
				// Should be unchanged or no user_id
				var data map[string]interface{}
				if err := json.Unmarshal(result, &data); err != nil {
					t.Fatalf("failed to parse result: %v", err)
				}
				metadata, ok := data["metadata"].(map[string]interface{})
				if !ok {
					return // no metadata, as expected
				}
				userID, _ := metadata["user_id"].(string)
				if userID != "" {
					var origData map[string]interface{}
					_ = json.Unmarshal([]byte(tt.input), &origData)
					origMeta, _ := origData["metadata"].(map[string]interface{})
					origUID, _ := origMeta["user_id"].(string)
					if userID != origUID {
						t.Errorf("user_id changed unexpectedly: got %q, want %q", userID, origUID)
					}
				}
				return
			}

			var data map[string]interface{}
			if err := json.Unmarshal(result, &data); err != nil {
				t.Fatalf("failed to parse result: %v", err)
			}
			metadata, ok := data["metadata"].(map[string]interface{})
			if !ok {
				t.Fatal("metadata not found in result")
			}
			userID, ok := metadata["user_id"].(string)
			if !ok {
				t.Fatal("user_id not found in metadata")
			}
			if userID != tt.expected {
				t.Errorf("user_id = %q, want %q", userID, tt.expected)
			}

			// Verify other fields are preserved
			var origData map[string]interface{}
			_ = json.Unmarshal([]byte(tt.input), &origData)
			if origModel, ok := origData["model"].(string); ok {
				if resultModel, ok := data["model"].(string); ok {
					if origModel != resultModel {
						t.Errorf("model changed: got %q, want %q", resultModel, origModel)
					}
				}
			}
		})
	}
}

func TestExtractUnifiedSessionID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		headers  map[string]string
		body     string
		expected string
	}{
		{
			name:     "conversation header has highest priority",
			headers:  map[string]string{"Conversation_id": "conv_1", "Session_id": "sess_1"},
			body:     `{"user":"body_user","prompt_cache_key":"cache_1","metadata":{"user_id":"meta_1"}}`,
			expected: "conv_1",
		},
		{
			name:     "session header outranks claude code session header",
			headers:  map[string]string{"Session_id": "sess_2", "X-Claude-Code-Session-Id": "claude_2"},
			body:     `{"user":"body_user"}`,
			expected: "sess_2",
		},
		{
			name:     "claude code session header outranks client request id",
			headers:  map[string]string{"X-Claude-Code-Session-Id": "claude_3", "X-Client-Request-Id": "req_3"},
			body:     `{"prompt_cache_key":"cache_3"}`,
			expected: "claude_3",
		},
		{
			name:     "body user outranks client request id",
			headers:  map[string]string{"X-Client-Request-Id": "req_4"},
			body:     `{"user":"body_user"}`,
			expected: "body_user",
		},
		{
			name:     "prompt cache key outranks gemini privileged user",
			headers:  map[string]string{"X-Gemini-Api-Privileged-User-Id": "gemini_4"},
			body:     `{"prompt_cache_key":"cache_4"}`,
			expected: "cache_4",
		},
		{
			name:     "gemini privileged user outranks client request id as header fallback",
			headers:  map[string]string{"X-Client-Request-Id": "req_4", "X-Gemini-Api-Privileged-User-Id": "gemini_4"},
			body:     `{}`,
			expected: "gemini_4",
		},
		{
			name:     "body user outranks prompt cache key and metadata user id",
			headers:  map[string]string{},
			body:     `{"user":"body_user","prompt_cache_key":"cache_5","metadata":{"user_id":"meta_5"}}`,
			expected: "body_user",
		},
		{
			name:     "prompt cache key outranks metadata user id",
			headers:  map[string]string{},
			body:     `{"prompt_cache_key":"cache_6","metadata":{"user_id":"meta_6"}}`,
			expected: "cache_6",
		},
		{
			name:     "metadata user id is final fallback",
			headers:  map[string]string{},
			body:     `{"metadata":{"user_id":"meta_7"}}`,
			expected: "meta_7",
		},
		{
			name:     "metadata user id object falls back to flattened value after user and prompt cache key",
			headers:  map[string]string{},
			body:     `{"metadata":{"user_id":{"device_id":"dev1","account_uuid":"acc1","session_id":"sess1"}}}`,
			expected: "user_dev1_account_acc1_session_sess1",
		},
		{
			name:     "invalid metadata user id type does not discard valid user",
			headers:  map[string]string{},
			body:     `{"user":"body_user","metadata":{"user_id":{"device_id":"dev1"}}}`,
			expected: "body_user",
		},
		{
			name:     "invalid metadata user id type does not discard valid prompt cache key",
			headers:  map[string]string{},
			body:     `{"prompt_cache_key":"cache_8","metadata":{"user_id":{"device_id":"dev1"}}}`,
			expected: "cache_8",
		},
		{
			name:     "empty request returns empty string",
			headers:  map[string]string{},
			body:     `{}`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(tt.body))
			for k, v := range tt.headers {
				c.Request.Header.Set(k, v)
			}

			if got := utils.ExtractUnifiedSessionID(c, []byte(tt.body)); got != tt.expected {
				t.Fatalf("ExtractUnifiedSessionID() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestPassthroughResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	resp := &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     http.Header{"X-Test": []string{"ok"}},
		Body:       io.NopCloser(bytes.NewBufferString(`{"ok":true}`)),
	}

	if err := PassthroughResponse(c, resp); err != nil {
		t.Fatalf("PassthroughResponse() err = %v", err)
	}

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}
	if got := w.Header().Get("X-Test"); got != "ok" {
		t.Fatalf("header X-Test = %q, want ok", got)
	}
	if got := w.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
}

func TestPassthroughJSONResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("decode success", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-Test": []string{"ok"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"usage":{"prompt_tokens":12,"completion_tokens":34}}`)),
		}

		var got map[string]interface{}
		if err := PassthroughJSONResponse(c, resp, &got); err != nil {
			t.Fatalf("PassthroughJSONResponse() err = %v", err)
		}

		if w.Body.String() != `{"usage":{"prompt_tokens":12,"completion_tokens":34}}` {
			t.Fatalf("unexpected body: %q", w.Body.String())
		}
		usage, ok := got["usage"].(map[string]interface{})
		if !ok || usage["prompt_tokens"].(float64) != 12 {
			t.Fatalf("decoded usage = %#v", got["usage"])
		}
	})

	t.Run("decode failure still writes full body", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"usage": invalid-json}`)),
		}

		var got map[string]interface{}
		err := PassthroughJSONResponse(c, resp, &got)
		if err == nil {
			t.Fatal("expected decode error")
		}
		var syntaxErr *json.SyntaxError
		if !errors.As(err, &syntaxErr) {
			t.Fatalf("err = %T, want *json.SyntaxError", err)
		}
		if w.Body.String() != `{"usage": invalid-json}` {
			t.Fatalf("unexpected body: %q", w.Body.String())
		}
	})
}

func TestWithLifecycleTrace_AttachesClientTraceCallbacks(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest() err = %v", err)
	}

	var connected atomic.Int32
	var firstByte atomic.Int32
	tracedReq := withLifecycleTrace(
		req,
		&RequestLifecycleTrace{
			OnConnected: func() {
				connected.Add(1)
			},
			OnFirstResponseByte: func() {
				firstByte.Add(1)
			},
		},
	)

	trace := httptrace.ContextClientTrace(tracedReq.Context())
	if trace == nil {
		t.Fatal("client trace was not attached")
	}

	trace.GotConn(httptrace.GotConnInfo{})
	trace.GotFirstResponseByte()

	if connected.Load() != 1 {
		t.Fatalf("OnConnected calls = %d, want 1", connected.Load())
	}
	if firstByte.Load() != 1 {
		t.Fatalf("OnFirstResponseByte calls = %d, want 1", firstByte.Load())
	}
}

func TestRemoveEmptySignatures_DoesNotStripThinkingSignatures(t *testing.T) {
	input := `{
		"messages": [
			{
				"role": "assistant",
				"content": [
					{"type": "thinking", "thinking": "keep", "signature": ""},
					{"type": "redacted_thinking", "data": "opaque", "signature": null},
					{"type": "text", "text": "ok", "signature": ""}
				]
			}
		]
	}`

	gotBytes, modified := RemoveEmptySignatures([]byte(input), false, "Messages")
	if !modified {
		t.Fatal("expected modified=true")
	}

	var got map[string]interface{}
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	messages, _ := got["messages"].([]interface{})
	msg, _ := messages[0].(map[string]interface{})
	content, _ := msg["content"].([]interface{})

	thinking, _ := content[0].(map[string]interface{})
	if _, exists := thinking["signature"]; !exists {
		t.Fatal("thinking signature should be preserved for later thinking sanitization")
	}

	redacted, _ := content[1].(map[string]interface{})
	if _, exists := redacted["signature"]; exists {
		t.Fatal("redacted_thinking empty signature should be removed")
	}

	textBlock, _ := content[2].(map[string]interface{})
	if _, exists := textBlock["signature"]; exists {
		t.Fatal("text signature should be removed")
	}
}

func TestSanitizeMalformedThinkingBlocks_StripsEmptySignatureButKeepsThinking(t *testing.T) {
	input := `{
		"messages": [
			{
				"role": "assistant",
				"content": [
					{"type": "thinking", "thinking": "keep", "signature": ""},
					{"type": "text", "text": "ok"}
				]
			},
			{
				"role": "assistant",
				"content": [
					{"type": "thinking", "thinking": "keep-null-signature", "signature": null},
					{"type": "thinking", "thinking": "keep"},
					{"type": "thinking", "thinking": "keep signed", "signature": "sig_123"}
				]
			}
		]
	}`

	gotBytes, modified := SanitizeMalformedThinkingBlocks([]byte(input), false, "Messages")
	if !modified {
		t.Fatal("expected modified=true")
	}

	var got map[string]interface{}
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	messages, _ := got["messages"].([]interface{})
	firstMsg, _ := messages[0].(map[string]interface{})
	firstContent, _ := firstMsg["content"].([]interface{})
	if len(firstContent) != 2 {
		t.Fatalf("first content len = %d, want 2", len(firstContent))
	}
	firstThinking, _ := firstContent[0].(map[string]interface{})
	if firstThinking["type"] != "thinking" || firstThinking["thinking"] != "keep" {
		t.Fatalf("first thinking block = %v, want preserved non-empty thinking", firstThinking)
	}
	if _, exists := firstThinking["signature"]; exists {
		t.Fatalf("first thinking signature should be removed, got %v", firstThinking["signature"])
	}

	secondMsg, _ := messages[1].(map[string]interface{})
	secondContent, _ := secondMsg["content"].([]interface{})
	if len(secondContent) != 3 {
		t.Fatalf("second content len = %d, want 3", len(secondContent))
	}
	firstSecondThinking, _ := secondContent[0].(map[string]interface{})
	if firstSecondThinking["thinking"] != "keep-null-signature" {
		t.Fatalf("first second thinking = %v, want keep-null-signature", firstSecondThinking["thinking"])
	}
	if _, exists := firstSecondThinking["signature"]; exists {
		t.Fatalf("null signature should be removed from thinking block")
	}
}

func TestSanitizeMalformedThinkingBlocks(t *testing.T) {
	input := `{
		"messages": [
			{
				"role": "assistant",
				"content": [
					{"type": "thinking"},
					{"type": "text", "text": "ok"}
				]
			},
			{
				"role": "assistant",
				"content": [
					{"type": "thinking", "thinking": {"foo": "bar"}}
				]
			},
			{
				"role": "assistant",
				"content": [
					{"type": "thinking", "thinking": "keep me"}
				]
			},
			{
				"role": "assistant",
				"content": [
					{"type": "thinking", "thinking": "signed", "signature": "sig_123"}
				]
			},
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "hello"}
				]
			}
		]
	}`

	gotBytes, modified := SanitizeMalformedThinkingBlocks([]byte(input), false, "Messages")
	if !modified {
		t.Fatal("expected modified=true")
	}

	var got map[string]interface{}
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	messages, ok := got["messages"].([]interface{})
	if !ok {
		t.Fatalf("messages type = %T, want []interface{}", got["messages"])
	}

	// 仅畸形 thinking 的 assistant 消息保留骨架（content 清空），不删除整条消息，共 5 条
	if len(messages) != 5 {
		t.Fatalf("messages len = %d, want 5", len(messages))
	}

	firstMsg, _ := messages[0].(map[string]interface{})
	firstContent, _ := firstMsg["content"].([]interface{})
	if len(firstContent) != 1 {
		t.Fatalf("first message content len = %d, want 1", len(firstContent))
	}
	firstBlock, _ := firstContent[0].(map[string]interface{})
	if firstBlock["type"] != "text" {
		t.Fatalf("first message content[0].type = %v, want text", firstBlock["type"])
	}

	// 第二条：仅含 thinking，保留骨架 content=[]
	secondMsg, _ := messages[1].(map[string]interface{})
	secondContent, _ := secondMsg["content"].([]interface{})
	if len(secondContent) != 0 {
		t.Fatalf("second message content len = %d, want 0 (thinking-only, kept as empty)", len(secondContent))
	}

	// 第三条：合法 thinking 保留
	thirdMsg, _ := messages[2].(map[string]interface{})
	thirdContent, _ := thirdMsg["content"].([]interface{})
	if len(thirdContent) != 1 {
		t.Fatalf("third message content len = %d, want 1", len(thirdContent))
	}
	thirdBlock, _ := thirdContent[0].(map[string]interface{})
	if thirdBlock["type"] != "thinking" {
		t.Fatalf("third message content[0].type = %v, want thinking", thirdBlock["type"])
	}
	if thirdBlock["thinking"] != "keep me" {
		t.Fatalf("third message content[0].thinking = %v, want keep me", thirdBlock["thinking"])
	}

	// 第四条：带 signature 的合法 thinking 也保留
	fourthMsg, _ := messages[3].(map[string]interface{})
	fourthContent, _ := fourthMsg["content"].([]interface{})
	if len(fourthContent) != 1 {
		t.Fatalf("fourth message content len = %d, want 1", len(fourthContent))
	}
	fourthBlock, _ := fourthContent[0].(map[string]interface{})
	if fourthBlock["type"] != "thinking" {
		t.Fatalf("fourth message content[0].type = %v, want thinking", fourthBlock["type"])
	}
	if fourthBlock["thinking"] != "signed" {
		t.Fatalf("fourth message content[0].thinking = %v, want signed", fourthBlock["thinking"])
	}
	if fourthBlock["signature"] != "sig_123" {
		t.Fatalf("fourth message content[0].signature = %v, want sig_123", fourthBlock["signature"])
	}

	// 最后一条：user 文本消息
	lastMsg, _ := messages[4].(map[string]interface{})
	if lastMsg["role"] != "user" {
		t.Fatalf("last message role = %v, want user", lastMsg["role"])
	}
}

func TestSanitizeMalformedThinkingBlocks_InvalidJSON_NoChange(t *testing.T) {
	input := []byte(`{"messages":[`)
	got, modified := SanitizeMalformedThinkingBlocks(input, false, "Messages")
	if modified {
		t.Fatal("expected modified=false")
	}
	if string(got) != string(input) {
		t.Fatalf("unexpected output change: got %q, want %q", string(got), string(input))
	}
}

func TestSanitizeMalformedThinkingBlocks_ContentObject(t *testing.T) {
	input := `{
		"messages": [
			{
				"role": "assistant",
				"content": {"type": "thinking", "thinking": {}}
			},
			{
				"role": "assistant",
				"content": {"type": "text", "text": "ok", "thinking": {"thinking": "noise"}}
			}
		]
	}`

	gotBytes, modified := SanitizeMalformedThinkingBlocks([]byte(input), false, "Messages")
	if !modified {
		t.Fatal("expected modified=true")
	}

	var got map[string]interface{}
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		t.Fatalf("unmarshal result failed: %v", err)
	}

	messages, _ := got["messages"].([]interface{})
	if len(messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(messages))
	}

	msg, _ := messages[0].(map[string]interface{})
	content, _ := msg["content"].(map[string]interface{})
	if _, exists := content["thinking"]; exists {
		t.Fatalf("unexpected thinking field in non-thinking content: %v", content["thinking"])
	}
	if content["type"] != "text" {
		t.Fatalf("content.type = %v, want text", content["type"])
	}
}

func TestSendRequestWithLifecycleTraceUsesChannelRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest() err = %v", err)
	}

	upstream := &config.UpstreamConfig{RequestTimeoutMs: 20}
	envCfg := &config.EnvConfig{RequestTimeout: 500}
	_, err = SendRequestWithLifecycleTrace(req, upstream, envCfg, false, "Messages", nil)
	if err == nil {
		t.Fatal("expected request to timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		if netErr, ok := err.(interface{ Timeout() bool }); !ok || !netErr.Timeout() {
			t.Fatalf("error = %v, want timeout", err)
		}
	}
}

func TestSendRequestWithLifecycleTraceFallsBackToGlobalRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest() err = %v", err)
	}

	upstream := &config.UpstreamConfig{}
	envCfg := &config.EnvConfig{RequestTimeout: 500}
	resp, err := SendRequestWithLifecycleTrace(req, upstream, envCfg, false, "Messages", nil)
	if err != nil {
		t.Fatalf("SendRequestWithLifecycleTrace() err = %v", err)
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSendRequestWithLifecycleTraceUsesChannelResponseHeaderTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest() err = %v", err)
	}

	upstream := &config.UpstreamConfig{ResponseHeaderTimeoutMs: 20}
	envCfg := &config.EnvConfig{RequestTimeout: 500, ResponseHeaderTimeout: 1}
	_, err = SendRequestWithLifecycleTrace(req, upstream, envCfg, false, "Messages", nil)
	if err == nil {
		t.Fatal("expected response header timeout")
	}
	if netErr, ok := err.(interface{ Timeout() bool }); !ok || !netErr.Timeout() {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestRestoreRequestBodyAndContextCacheUseAttemptBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	original := []byte(`{"model":"claude-3","metadata":{"user_id":"{\"device_id\":\"abc\"}"}}`)
	attemptBody := NormalizeMetadataUserID(original)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(original))

	RestoreRequestBody(c, attemptBody)
	c.Set("requestBodyBytes", attemptBody)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("ReadAll() err = %v", err)
	}
	if string(body) != string(attemptBody) {
		t.Fatalf("request body = %s, want %s", string(body), string(attemptBody))
	}

	cached, ok := c.Get("requestBodyBytes")
	if !ok {
		t.Fatal("requestBodyBytes not found in context")
	}
	if string(cached.([]byte)) != string(attemptBody) {
		t.Fatalf("cached body = %s, want %s", string(cached.([]byte)), string(attemptBody))
	}
}

func TestGetEffectiveRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fallback := []byte(`{"original":true}`)

	t.Run("returns context body when set", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		updated := []byte(`{"updated":true}`)
		c.Set("requestBodyBytes", updated)
		if got := GetEffectiveRequestBody(c, fallback); string(got) != string(updated) {
			t.Fatalf("got %s, want %s", string(got), string(updated))
		}
	})

	t.Run("returns fallback when context empty", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		if got := GetEffectiveRequestBody(c, fallback); string(got) != string(fallback) {
			t.Fatalf("got %s, want fallback %s", string(got), string(fallback))
		}
	})

	t.Run("returns fallback when context value wrong type", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("requestBodyBytes", "not-bytes")
		if got := GetEffectiveRequestBody(c, fallback); string(got) != string(fallback) {
			t.Fatalf("got %s, want fallback %s", string(got), string(fallback))
		}
	})

	t.Run("returns fallback when context nil", func(t *testing.T) {
		if got := GetEffectiveRequestBody(nil, fallback); string(got) != string(fallback) {
			t.Fatalf("got %s, want fallback %s", string(got), string(fallback))
		}
	})
}

func TestDeduplicateTotalTokensSystemBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("重复块只保留最后一份", func(t *testing.T) {
		body := []byte(`{"model":"m","system":[` +
			`{"type":"text","text":"You are Claude Code"},` +
			`{"type":"text","text":"<total_tokens>100 tokens left</total_tokens>\n\nstyle active"},` +
			`{"type":"text","text":"Contents of CLAUDE.md"},` +
			`{"type":"text","text":"<total_tokens>90 tokens left</total_tokens>\n\nstyle active"},` +
			`{"type":"text","text":"<total_tokens>80 tokens left</total_tokens>\n\nstyle active","cache_control":{"type":"ephemeral"}}` +
			`],"messages":[]}`)

		out, changed := DeduplicateTotalTokensSystemBlocks(nil, body, false, "Messages")
		if !changed {
			t.Fatal("expected changed=true")
		}
		var data map[string]interface{}
		if err := json.Unmarshal(out, &data); err != nil {
			t.Fatalf("output is not valid JSON: %v", err)
		}
		sys := data["system"].([]interface{})
		if len(sys) != 3 {
			t.Fatalf("system len = %d, want 3", len(sys))
		}
		last := sys[2].(map[string]interface{})
		text := last["text"].(string)
		if !strings.Contains(text, "80 tokens left") {
			t.Errorf("kept block = %q, want the last (newest) copy", text)
		}
		if _, ok := last["cache_control"]; !ok {
			t.Error("kept block should retain its own cache_control")
		}
	})

	t.Run("被删块的 cache_control 转移给保留块", func(t *testing.T) {
		body := []byte(`{"system":[` +
			`{"type":"text","text":"identity"},` +
			`{"type":"text","text":"<total_tokens>10 tokens left</total_tokens>","cache_control":{"type":"ephemeral"}},` +
			`{"type":"text","text":"<total_tokens>5 tokens left</total_tokens>"}` +
			`]}`)

		out, changed := DeduplicateTotalTokensSystemBlocks(nil, body, false, "Messages")
		if !changed {
			t.Fatal("expected changed=true")
		}
		var data map[string]interface{}
		json.Unmarshal(out, &data)
		sys := data["system"].([]interface{})
		if len(sys) != 2 {
			t.Fatalf("system len = %d, want 2", len(sys))
		}
		kept := sys[1].(map[string]interface{})
		if _, ok := kept["cache_control"]; !ok {
			t.Error("orphan cache_control should be transferred to the kept block")
		}
	})

	t.Run("单份或无重复不动", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"<total_tokens>5 tokens left</total_tokens>"}]}`)
		if _, changed := DeduplicateTotalTokensSystemBlocks(nil, body, false, "Messages"); changed {
			t.Error("single block should not be modified")
		}
	})

	t.Run("普通 system 块不误伤", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"tokens left in budget"},{"type":"text","text":"another"}]}`)
		if _, changed := DeduplicateTotalTokensSystemBlocks(nil, body, false, "Messages"); changed {
			t.Error("non-total_tokens blocks should not be touched")
		}
	})
}
