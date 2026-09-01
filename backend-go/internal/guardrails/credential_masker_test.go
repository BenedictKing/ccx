package guardrails

import (
	"net/http"
	"strings"
	"testing"
)

func TestCredentialMasker_PreCall_Hits(t *testing.T) {
	m := NewCredentialMasker()
	m.SetStaticKeys([]string{"sk-test-gateway-key-1234567890"})
	m.SetChannelKeyPrefixes([]string{"ch-prefix-"})

	cases := []struct {
		name     string
		input    string
		wantHit  string // 应被替换的片段（验证不再出现）
		wantKeep string // 应保留的片段
	}{
		{
			name:    "openai_sk_key",
			input:   `{"content": "my key is sk-abcdefghijklmnopqrstuvwxyz1234567890 end"}`,
			wantHit: "sk-abcdefghijklmnopqrstuvwxyz1234567890",
		},
		{
			name:    "anthropic_key",
			input:   `{"key": "sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890"}`,
			wantHit: "sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890",
		},
		{
			name:    "gateway_static_key",
			input:   `{"text": "leaked: sk-test-gateway-key-1234567890 in prompt"}`,
			wantHit: "sk-test-gateway-key-1234567890",
		},
		{
			name:    "channel_prefix_key",
			input:   `{"text": "key: ch-prefix-abcdefghijklmnopqrstuv"}`,
			wantHit: "ch-prefix-abcdefghijklmnopqrstuv",
		},
		{
			name:    "bearer_token",
			input:   `Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature12345678901234567890` + "\n",
			wantHit: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature12345678901234567890",
		},
		{
			name:    "jwt_format",
			input:   `token: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U`,
			wantHit: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		},
		{
			name:    "aws_key",
			input:   `access_key: AKIAIOSFODNN7EXAMPLE`,
			wantHit: "AKIAIOSFODNN7EXAMPLE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := m.PreCall([]byte(tc.input), nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result (hit)")
			}
			if result.Blocked {
				t.Error("credential-masker should never block")
			}
			if !result.Modified {
				t.Error("expected Modified=true")
			}
			if strings.Contains(string(result.Payload), tc.wantHit) {
				t.Errorf("payload still contains secret: %q", tc.wantHit)
			}
			if tc.wantKeep != "" && !strings.Contains(string(result.Payload), tc.wantKeep) {
				t.Errorf("payload missing expected content: %q", tc.wantKeep)
			}
			if result.Meta == nil {
				t.Fatal("expected meta for audit")
			}
			if count, ok := result.Meta["count"].(int); !ok || count < 1 {
				t.Errorf("expected count >= 1, got %v", result.Meta["count"])
			}
		})
	}
}

func TestCredentialMasker_NoFalsePositives(t *testing.T) {
	m := NewCredentialMasker()

	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "plain_text",
			input: `{"messages": [{"role": "user", "content": "hello world"}]}`,
		},
		{
			name:  "short_random_string",
			input: `{"id": "abc123", "name": "test-user"}`,
		},
		{
			name:  "sk_too_short",
			input: `{"key": "sk-abc"}`,
		},
		{
			name:  "json_numbers",
			input: `{"count": 1234567890123456789, "value": "hello"}`,
		},
		{
			name:  "url_path",
			input: `{"url": "https://api.example.com/v1/endpoint/abc123def456"}`,
		},
		{
			name:  "base64_image_short",
			input: `data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==`,
		},
		{
			name:  "version_string",
			input: `{"version": "3.0.0", "build": "2026.09.01"}`,
		},
		{
			name:  "hex_hash_sha256_like",
			input: `{"hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := m.PreCall([]byte(tc.input), nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != nil && result.Modified {
				t.Errorf("expected no modification, got %q", string(result.Payload))
			}
		})
	}
}

func TestCredentialMasker_SizeLimit(t *testing.T) {
	m := NewCredentialMasker()
	m.SetMaxScanBytes(1024) // 1KB 上限

	// 构造 4KB 文本，尾部放 key
	big := make([]byte, 4*1024)
	for i := range big {
		big[i] = 'a'
	}
	// 在开头放一个 key（应该被扫到）
	input := append([]byte(`secret: sk-abcdefghijklmnopqrstuvwxyz1234567890`), big...)
	// 在尾部放另一个 key（应该在扫描范围外）
	tailKey := "trailing: sk-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	input = append(input, []byte(tailKey)...)

	result, err := m.PreCall(input, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Modified {
		t.Error("expected Modified=true (head key should be masked)")
	}
	// 因为超限时只扫了头部，result.Payload 只有前 1KB + 掩码
	if len(result.Payload) > 1024+100 {
		t.Errorf("result payload too large (%d bytes), expected truncated scan", len(result.Payload))
	}
	// 验证 trimmed 标记
	if trimmed, ok := result.Meta["trimmed"].(bool); !ok || !trimmed {
		t.Error("expected trimmed=true in meta")
	}
}

func TestCredentialMasker_FailOpen(t *testing.T) {
	// 注册一个会 panic 的 guardrail，验证 registry 能 fail-open
	r := NewRegistry()
	r.Register(&panicGuardrail{name: "bad-guardrail", priority: 10})
	r.Register(NewCredentialMasker())

	payload := []byte(`{"key": "sk-abcdefghijklmnopqrstuvwxyz1234567890"}`)
	ctx := &Context{}
	resultPayload, results := r.RunPreCall(payload, ctx)

	// bad-guardrail 应该 fail-open
	badFound := false
	for _, res := range results {
		if res.Guardrail == "bad-guardrail" {
			badFound = true
			if !res.FailOpen {
				t.Error("bad-guardrail should be marked FailOpen")
			}
			if res.Error == "" {
				t.Error("bad-guardrail should have error message")
			}
		}
	}
	if !badFound {
		t.Fatal("bad-guardrail result not found")
	}

	// credential-masker 仍应正常执行（fail-open 不阻断后续 guardrail）
	cmFound := false
	for _, res := range results {
		if res.Guardrail == "credential-masker" {
			cmFound = true
			if !res.Modified {
				t.Error("credential-masker should still run and modify")
			}
		}
	}
	if !cmFound {
		t.Fatal("credential-masker result not found")
	}

	// 最终 payload 应该被掩码
	if strings.Contains(string(resultPayload), "sk-abcdefghijklmnopqrstuvwxyz1234567890") {
		t.Error("final payload should have key masked")
	}
}

func TestRegistry_DisabledByHeader(t *testing.T) {
	r := NewRegistry()
	m := NewCredentialMasker()
	r.Register(m)

	payload := []byte(`{"key": "sk-abcdefghijklmnopqrstuvwxyz1234567890"}`)

	// 没有豁免头 → 应该掩码
	ctx := &Context{Headers: http.Header{}}
	resultPayload, results := r.RunPreCall(payload, ctx)
	if strings.Contains(string(resultPayload), "sk-abcdefghijklmnopqrstuvwxyz1234567890") {
		t.Error("without opt-out header, key should be masked")
	}
	if len(results) != 1 || results[0].Skipped {
		t.Error("expected one non-skipped result")
	}

	// 有豁免头 → 跳过
	h := http.Header{}
	h.Set("X-Ccx-Disabled-Guardrails", "credential-masker")
	ctx2 := &Context{Headers: h}
	resultPayload2, results2 := r.RunPreCall(payload, ctx2)
	if !strings.Contains(string(resultPayload2), "sk-abcdefghijklmnopqrstuvwxyz1234567890") {
		t.Error("with opt-out header, key should NOT be masked")
	}
	if len(results2) != 1 || !results2[0].Skipped {
		t.Error("expected one skipped result")
	}
	if results2[0].Reason != "opt-out" {
		t.Errorf("expected reason=opt-out, got %q", results2[0].Reason)
	}
}

func TestRegistry_PriorityOrder(t *testing.T) {
	r := NewRegistry()
	order := []string{}
	r.Register(&recordingGuardrail{name: "low-priority", priority: 100, record: &order})
	r.Register(&recordingGuardrail{name: "high-priority", priority: 10, record: &order})
	r.Register(&recordingGuardrail{name: "mid-priority", priority: 50, record: &order})

	r.RunPreCall([]byte("test"), nil)

	expected := []string{"high-priority", "mid-priority", "low-priority"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d guardrails run, got %d: %v", len(expected), len(order), order)
	}
	for i, name := range expected {
		if order[i] != name {
			t.Errorf("order[%d] = %q, want %q", i, order[i], name)
		}
	}
}

func TestCredentialMasker_PostCall(t *testing.T) {
	m := NewCredentialMasker()
	m.SetStaticKeys([]string{"admin-secret-key-1234567890"})

	resp := `{"error": {"message": "invalid key admin-secret-key-1234567890 in request"}}`
	result, err := m.PostCall([]byte(resp), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.Modified {
		t.Fatal("expected modified result")
	}
	if strings.Contains(string(result.Response), "admin-secret-key-1234567890") {
		t.Error("response should have static key masked")
	}
	if stage, ok := result.Meta["stage"].(string); !ok || stage != "post" {
		t.Errorf("expected stage=post, got %v", result.Meta["stage"])
	}
}

func TestCredentialMasker_EmptyInput(t *testing.T) {
	m := NewCredentialMasker()
	result, err := m.PreCall(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("nil input should return nil result")
	}

	result2, err := m.PreCall([]byte{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result2 != nil {
		t.Error("empty input should return nil result")
	}
}

// --- test helpers ---

type panicGuardrail struct {
	name     string
	priority int
}

func (g *panicGuardrail) Name() string                       { return g.name }
func (g *panicGuardrail) Priority() int                      { return g.priority }
func (g *panicGuardrail) Enabled() bool                      { return true }
func (g *panicGuardrail) PreCall(_ []byte, _ *Context) (*Result, error) {
	panic("intentional test panic")
}
func (g *panicGuardrail) PostCall(_ []byte, _ *Context) (*Result, error) {
	panic("intentional test panic")
}

type recordingGuardrail struct {
	name     string
	priority int
	record   *[]string
}

func (g *recordingGuardrail) Name() string  { return g.name }
func (g *recordingGuardrail) Priority() int { return g.priority }
func (g *recordingGuardrail) Enabled() bool { return true }
func (g *recordingGuardrail) PreCall(_ []byte, _ *Context) (*Result, error) {
	*g.record = append(*g.record, g.name)
	return nil, nil
}
func (g *recordingGuardrail) PostCall(_ []byte, _ *Context) (*Result, error) {
	*g.record = append(*g.record, g.name)
	return nil, nil
}
