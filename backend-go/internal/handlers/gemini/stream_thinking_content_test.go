package gemini

import "testing"

// hasGeminiDeliverableThinkingContent 的判定必须与三个转换器的实际交付能力严格一致：
// 只有真正写出 thought part 的事件才算语义内容。判定过宽会让预检放行并写出空 200，
// 取代本应发生的 failover（改前这些流会走首内容超时正常换渠道）。
func TestHasGeminiDeliverableThinkingContent(t *testing.T) {
	tests := []struct {
		name         string
		upstreamType string
		jsonData     string
		want         bool
	}{
		{
			name:         "claude 非空 thinking_delta 可交付",
			upstreamType: "claude",
			jsonData:     `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me think"}}`,
			want:         true,
		},
		{
			name:         "claude 空 thinking_delta 不可交付",
			upstreamType: "claude",
			jsonData:     `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`,
			want:         false,
		},
		{
			name:         "claude redacted_thinking 开场块转换器零输出",
			upstreamType: "claude",
			jsonData:     `{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"abc"}}`,
			want:         false,
		},
		{
			name:         "claude 文本增量不是思考交付",
			upstreamType: "claude",
			jsonData:     `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
			want:         false,
		},
		{
			name:         "claude 非法 JSON 不命中",
			upstreamType: "claude",
			jsonData:     `{"type":"content_block_delta"`,
			want:         false,
		},
		{
			name:         "openai reasoning_content 可交付",
			upstreamType: "openai",
			jsonData:     `{"choices":[{"delta":{"reasoning_content":"thinking"}}]}`,
			want:         true,
		},
		{
			name:         "openai reasoning 可交付",
			upstreamType: "openai",
			jsonData:     `{"choices":[{"delta":{"reasoning":"thinking"}}]}`,
			want:         true,
		},
		{
			name:         "openai 空 reasoning_content 不可交付",
			upstreamType: "openai",
			jsonData:     `{"choices":[{"delta":{"reasoning_content":""}}]}`,
			want:         false,
		},
		{
			name:         "openai 仅正文不是思考交付",
			upstreamType: "openai",
			jsonData:     `{"choices":[{"delta":{"content":"hi"}}]}`,
			want:         false,
		},
		{
			name:         "openai 无 choices 不命中",
			upstreamType: "openai",
			jsonData:     `{"usage":{"prompt_tokens":1}}`,
			want:         false,
		},
		{
			name:         "responses reasoning_summary_text.delta 可交付",
			upstreamType: "responses",
			jsonData:     `{"type":"response.reasoning_summary_text.delta","text":"thinking"}`,
			want:         true,
		},
		{
			name:         "responses reasoning_summary_text.delta 空文本不可交付",
			upstreamType: "responses",
			jsonData:     `{"type":"response.reasoning_summary_text.delta","text":""}`,
			want:         false,
		},
		{
			name:         "responses reasoning_summary_text.done 转换器零输出",
			upstreamType: "responses",
			jsonData:     `{"type":"response.reasoning_summary_text.done","text":"thinking"}`,
			want:         false,
		},
		{
			name:         "responses reasoning_summary_part.added 转换器零输出",
			upstreamType: "responses",
			jsonData:     `{"type":"response.reasoning_summary_part.added"}`,
			want:         false,
		},
		{
			name:         "responses reasoning_text.delta 转换器零输出",
			upstreamType: "responses",
			jsonData:     `{"type":"response.reasoning_text.delta","delta":"thinking"}`,
			want:         false,
		},
		{
			name:         "未知上游类型不命中",
			upstreamType: "unknown",
			jsonData:     `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"x"}}`,
			want:         false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasGeminiDeliverableThinkingContent(tt.jsonData, tt.upstreamType); got != tt.want {
				t.Fatalf("hasGeminiDeliverableThinkingContent(%s, %s) = %v, want %v",
					tt.jsonData, tt.upstreamType, got, tt.want)
			}
		})
	}
}
