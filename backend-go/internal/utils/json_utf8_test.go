package utils

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateStringForLogPreservesUTF8(t *testing.T) {
	tests := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{name: "中文", value: "甲乙丙", limit: 2, want: "甲乙..."},
		{name: "emoji", value: "A😀B", limit: 2, want: "A😀..."},
		{name: "短字符串", value: "中文", limit: 2, want: "中文"},
		{name: "不截断", value: "中文", limit: 0, want: "中文"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateStringForLog(tt.value, tt.limit)
			if got != tt.want {
				t.Fatalf("TruncateStringForLog() = %q, want %q", got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("TruncateStringForLog() returned invalid UTF-8: %q", got)
			}
		})
	}
}

func TestFormatJSONForLogDoesNotSplitMultibyteCharacters(t *testing.T) {
	longText := strings.Repeat("中", 250)
	input := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": longText},
					map[string]interface{}{"type": "tool_result", "tool_use_id": "toolu_1", "content": longText},
					map[string]interface{}{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]interface{}{"command": longText}},
				},
			},
		},
	}

	result := FormatJSONForLog(input, 500)
	if !utf8.ValidString(result) || strings.Contains(result, "�") {
		t.Fatalf("formatted log contains invalid UTF-8: %q", result)
	}
	want := strings.Repeat("中", 200) + "..."
	if got := strings.Count(result, want); got != 3 {
		t.Fatalf("rune-safe truncated text count = %d, want 3", got)
	}
}
