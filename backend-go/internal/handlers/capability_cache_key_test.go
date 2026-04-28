package handlers

import "testing"

func TestBuildCapabilityCacheKeyIncludesModelsDimension(t *testing.T) {
	keyA := buildCapabilityCacheKey(
		"https://example.com",
		"sk-test",
		"responses",
		[]string{"responses", "messages"},
		[]string{"gpt-4o", "claude-3-7-sonnet"},
	)
	keyB := buildCapabilityCacheKey(
		"https://example.com",
		"sk-test",
		"responses",
		[]string{"messages", "responses"},
		[]string{"claude-3-7-sonnet", "gpt-4o"},
	)
	keyC := buildCapabilityCacheKey(
		"https://example.com",
		"sk-test",
		"responses",
		[]string{"messages", "responses"},
		[]string{"gpt-4o-mini"},
	)

	if keyA != keyB {
		t.Fatalf("same model set should yield same key, got %q != %q", keyA, keyB)
	}
	if keyA == keyC {
		t.Fatalf("different model set should yield different key, got %q", keyA)
	}
}
