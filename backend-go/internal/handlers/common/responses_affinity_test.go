package common_test

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/handlers/common"
)

func TestHasResponsesEncryptedContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"reasoning", `{"input":[{"type":"reasoning","encrypted_content":"sealed"}]}`, true},
		{"compaction", `{"input":[{"type":"message"},{"type":"compaction","encrypted_content":"sealed"}]}`, true},
		{"include_only", `{"include":["reasoning.encrypted_content"],"input":"hello"}`, false},
		{"text_mention", `{"input":[{"role":"user","content":"encrypted_content"}]}`, false},
		{"empty", `{"input":[{"type":"reasoning","encrypted_content":" \t"}]}`, false},
		{"null", `{"input":[{"type":"reasoning","encrypted_content":null}]}`, false},
		{"invalid_type", `{"input":[{"type":"reasoning","encrypted_content":123}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := common.HasResponsesEncryptedContent([]byte(tc.body)); got != tc.want {
				t.Fatalf("HasResponsesEncryptedContent() = %v, want %v", got, tc.want)
			}
		})
	}
}
