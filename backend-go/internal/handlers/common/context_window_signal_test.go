package common

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestUsageTotalContextTokens(t *testing.T) {
	cases := []struct {
		name  string
		usage *types.Usage
		want  int
	}{
		{
			name:  "OpenAI 总口径优先",
			usage: &types.Usage{InputTokens: 20_000, PromptTokensTotal: 500_000, CacheReadInputTokens: 480_000},
			want:  500_000,
		},
		{
			name:  "Anthropic 三段求和",
			usage: &types.Usage{InputTokens: 20_000, CacheCreationInputTokens: 5_000, CacheReadInputTokens: 475_000},
			want:  500_000,
		},
		{
			name:  "仅 InputTokens",
			usage: &types.Usage{InputTokens: 274_081},
			want:  274_081,
		},
		{
			name:  "兼容字段回退",
			usage: &types.Usage{PromptTokens: 100_000},
			want:  100_000,
		},
		{
			name:  "零值",
			usage: &types.Usage{},
			want:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usageTotalContextTokens(tc.usage); got != tc.want {
				t.Fatalf("usageTotalContextTokens = %d, want %d", got, tc.want)
			}
		})
	}
}
