package common

import (
	"strings"

	"github.com/tidwall/gjson"
)

func RequiresResponsesSourceAffinity(body []byte) bool {
	return HasResponsesEncryptedContent(body) || strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String()) != ""
}

// HasResponsesEncryptedContent 仅检查 input 条目的密文，避免将 include 声明或普通文本误判为加密续轮。
func HasResponsesEncryptedContent(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		encrypted := item.Get("encrypted_content")
		found = encrypted.Type == gjson.String && strings.TrimSpace(encrypted.Str) != ""
		return !found
	})
	return found
}
