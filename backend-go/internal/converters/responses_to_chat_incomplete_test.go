package converters

import (
	"testing"

	"github.com/tidwall/gjson"
)

// incomplete 的原因决定 finish_reason：content_filter 保持 content_filter，其余为 length。
func TestConvertResponsesResponseToChatResponse_IncompleteReason(t *testing.T) {
	tests := []struct {
		name    string
		details string
		want    string
	}{
		{"max_output_tokens", `{"reason":"max_output_tokens"}`, "length"},
		{"content_filter", `{"reason":"content_filter"}`, "content_filter"},
		{"no details", `null`, "length"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"id":"resp_1","status":"incomplete","incomplete_details":` + tt.details + `,
				"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`)

			out := ConvertResponsesResponseToChatResponse(body, "gpt-5")

			if got := gjson.GetBytes(out, "choices.0.finish_reason").String(); got != tt.want {
				t.Fatalf("finish_reason = %q, want %q", got, tt.want)
			}
		})
	}
}
