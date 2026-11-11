package siteaccess

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestErrorTextTruncationPreservesUTF8(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "响应体摘要按字符截断",
			got:  truncateForError([]byte(strings.Repeat("错", 600))),
			want: strings.Repeat("错", 512) + "...",
		},
		{
			name: "短响应体不截断",
			got:  truncateForError([]byte("  余额不足  ")),
			want: "余额不足",
		},
		{
			name: "HTML 标题按字符截断",
			got:  htmlTitleOf("<html><title>A" + strings.Repeat("访", 130) + "</title></html>"),
			want: "A" + strings.Repeat("访", 119) + "...",
		},
		{
			name: "短 HTML 标题不截断",
			got:  htmlTitleOf("<title> 访问受限 </title>"),
			want: "访问受限",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !utf8.ValidString(tt.got) {
				t.Fatalf("截断结果包含非法 UTF-8: %q", tt.got)
			}
			if tt.got != tt.want {
				t.Fatalf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
