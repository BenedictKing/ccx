package responses

import "testing"

// TestResponsesDeltaRepetitionText 验证重复检测的增量文本提取：
// 只提取 delta 字段的纯文本（sequence_number 等包装字段不得进入检测窗口），
// 非文本行（事件行、DONE、无 delta）返回 false。
func TestResponsesDeltaRepetitionText(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantText string
		wantOK   bool
	}{
		{
			name:     "output_text.delta 提取纯文本",
			line:     `data: {"type":"response.output_text.delta","sequence_number":41,"delta":"重复的输出片段"}`,
			wantText: "重复的输出片段",
			wantOK:   true,
		},
		{
			name:     "reasoning 摘要 delta 同样提取",
			line:     `data: {"type":"response.reasoning_summary_text.delta","sequence_number":7,"delta":"思考片段"}`,
			wantText: "思考片段",
			wantOK:   true,
		},
		{
			name:   "event 行不提取",
			line:   "event: response.output_text.delta",
			wantOK: false,
		},
		{
			name:   "DONE 不提取",
			line:   "data: [DONE]",
			wantOK: false,
		},
		{
			name:   "无 delta 字段的事件不提取",
			line:   `data: {"type":"response.output_item.added","sequence_number":3}`,
			wantOK: false,
		},
		{
			name:   "空 delta 不提取",
			line:   `data: {"type":"response.output_text.delta","delta":""}`,
			wantOK: false,
		},
		{
			name:   "非 JSON 不提取",
			line:   "data: not-json",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := responsesDeltaRepetitionText(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.wantText {
				t.Fatalf("text = %q, want %q", got, tt.wantText)
			}
		})
	}
}
