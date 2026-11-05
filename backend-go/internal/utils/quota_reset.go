package utils

import (
	"regexp"
	"strings"
	"time"
)

var quotaResetTimePattern = regexp.MustCompile(`(?i)(?:will\s+reset|quota\s+reset(?:\s+time)?|resets?)\s*(?:at|:|is)?\s*(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:Z|\s*[+-]\d{2}:?\d{2}(?:\s+[A-Z]{2,5})?)?)`)

// ExtractQuotaRecoverAt 从上游额度错误消息中提取明确的重置时间。
// 返回 RFC3339；无法识别时返回空字符串。
func ExtractQuotaRecoverAt(message string) string {
	match := quotaResetTimePattern.FindStringSubmatch(message)
	if len(match) != 2 {
		return ""
	}

	value := strings.TrimSpace(match[1])
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05 -07:00 MST",
		"2006-01-02 15:04:05 -07:00",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format(time.RFC3339)
		}
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.Local); err == nil {
		return parsed.Format(time.RFC3339)
	}
	return ""
}
