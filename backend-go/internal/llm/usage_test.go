package llm

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestUsage_IsZero(t *testing.T) {
	if !(Usage{}).IsZero() {
		t.Fatal("empty Usage should be zero")
	}
	u := Usage{Inner: types.Usage{InputTokens: 1}}
	if u.IsZero() {
		t.Fatal("Usage with InputTokens should not be zero")
	}
	u2 := Usage{Inner: types.Usage{PromptTokensTotal: 7}}
	if u2.IsZero() {
		t.Fatal("Usage with PromptTokensTotal should not be zero")
	}
}

func TestRequest_CloneIsolatesMetadata(t *testing.T) {
	stream := true
	orig := &Request{
		Format: FormatOpenAIChat,
		Model:  "gpt-4o",
		Stream: &stream,
		Metadata: map[string]any{
			"foo": "bar",
		},
	}
	cp := orig.Clone()
	cp.Metadata["foo"] = "baz"

	if orig.Metadata["foo"] != "bar" {
		t.Fatalf("Clone should not affect original Metadata; got %v", orig.Metadata["foo"])
	}
	// Stream 指针共享是 OK 的（指向不可变 bool）
	if !cp.IsStream() {
		t.Fatal("Cloned request should preserve Stream true")
	}
}

func TestRequest_IsStreamHandlesNilSafely(t *testing.T) {
	var r *Request
	if r.IsStream() {
		t.Fatal("nil request should return false")
	}
	if (&Request{}).IsStream() {
		t.Fatal("Request without Stream should return false")
	}
	v := false
	if (&Request{Stream: &v}).IsStream() {
		t.Fatal("Stream=&false should return false")
	}
}
