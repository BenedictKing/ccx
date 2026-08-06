package common

import (
	"testing"
)

func TestHasDocumentContent_ClaudeMessages(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "claude document block base64 pdf",
			body:     `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"abc"}}]}]}`,
			expected: true,
		},
		{
			name:     "claude text only",
			body:     `{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
			expected: false,
		},
		{
			name:     "claude image block is not document",
			body:     `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}]}]}`,
			expected: false,
		},
		{
			name:     "claude tool_result nested document block",
			body:     `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"abc"}}]}]}]}`,
			expected: true,
		},
		{
			name: "claude deeply nested document (tool_result → content → tool_result → content → document)",
			body: `{"messages":[{"role":"user","content":[
				{"type":"tool_result","content":[
					{"type":"tool_result","content":[
						{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"abc"}}
					]}
				]}
			]}]}`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestContext()
			got := HasDocumentContent(c, []byte(tt.body))
			if got != tt.expected {
				t.Errorf("HasDocumentContent() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHasDocumentContent_Responses(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "responses input_file top level",
			body:     `{"input":[{"type":"input_file","file_data":"data:application/pdf;base64,abc"}]}`,
			expected: true,
		},
		{
			name:     "responses input_file nested in content",
			body:     `{"input":[{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:application/pdf;base64,abc"}]}]}`,
			expected: true,
		},
		{
			name:     "responses input_image is not document",
			body:     `{"input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://example.com/img.png"}]}]}`,
			expected: false,
		},
		{
			name:     "responses text only",
			body:     `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestContext()
			got := HasDocumentContent(c, []byte(tt.body))
			if got != tt.expected {
				t.Errorf("HasDocumentContent() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHasDocumentContent_Gemini(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "gemini inlineData pdf",
			body:     `{"contents":[{"parts":[{"inlineData":{"mimeType":"application/pdf","data":"abc"}}]}]}`,
			expected: true,
		},
		{
			name:     "gemini fileData pdf",
			body:     `{"contents":[{"parts":[{"fileData":{"mimeType":"application/pdf","fileUri":"gs://bucket/doc.pdf"}}]}]}`,
			expected: true,
		},
		{
			name:     "gemini inlineData image is not document",
			body:     `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"abc"}}]}]}`,
			expected: false,
		},
		{
			name:     "gemini inlineData audio is not document",
			body:     `{"contents":[{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"abc"}}]}]}`,
			expected: false,
		},
		{
			name:     "gemini text only",
			body:     `{"contents":[{"parts":[{"text":"hello"}]}]}`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestContext()
			got := HasDocumentContent(c, []byte(tt.body))
			if got != tt.expected {
				t.Errorf("HasDocumentContent() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHasDocumentContent_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "empty body",
			body:     "",
			expected: false,
		},
		{
			name:     "empty json",
			body:     "{}",
			expected: false,
		},
		{
			name:     "malformed json",
			body:     "{invalid",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestContext()
			got := HasDocumentContent(c, []byte(tt.body))
			if got != tt.expected {
				t.Errorf("HasDocumentContent() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHasDocumentContent_ContextCaching(t *testing.T) {
	c := newTestContext()
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"abc"}}]}]}`)

	result1 := HasDocumentContent(c, body)
	if !result1 {
		t.Fatal("first call should detect document")
	}

	// 第二次调用即使传空 body 也应返回缓存结果
	result2 := HasDocumentContent(c, nil)
	if !result2 {
		t.Fatal("second call should return cached result")
	}

	if !HasDocumentContentCached(c) {
		t.Fatal("HasDocumentContentCached should return cached result")
	}
}
