package utils

import (
	"crypto/sha256"
	"encoding/hex"
)

// APIKeyID 跨 URL 标识同一把 Key，仅返回不可逆指纹。
func APIKeyID(key string) string {
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])
}
