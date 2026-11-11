package upstreamprobe

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateBalanceErrPreservesUTF8(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "长错误按字符截断",
			msg:  "HTTP 451: " + strings.Repeat("访", 130),
			want: "HTTP 451: " + strings.Repeat("访", 110) + "...",
		},
		{name: "短错误不截断", msg: "余额查询失败", want: "余额查询失败"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateBalanceErr(errors.New(tt.msg)).Error()
			if !utf8.ValidString(got) {
				t.Fatalf("截断结果包含非法 UTF-8: %q", got)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
