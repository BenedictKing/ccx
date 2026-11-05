package siteaccess

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

// tokenEncryption 封装站点管理凭证（AccessToken 等敏感字段）的对称加解密。
// 设计目标：持久化层落盘时加密，读取时透明解密；旧明文数据无加密标记时原样兼容。
// 密钥来源：1) 环境变量 CCX_SECRET_KEY（推荐生产部署显式配置）；
// 2) 未设置时按机器派生稳定密钥（由 os.Hostname + 固定 salt 经 PBKDF2 派生）。
type tokenEncryption struct {
	secretKey []byte // 32 字节 AES-256 密钥
}

// encryptedTokenPrefix 标记已加密字符串的前缀，便于向后兼容旧明文。
const encryptedTokenPrefix = "$enc$"

// newTokenEncryption 从环境变量或机器派生创建 tokenEncryption。
func newTokenEncryption() (*tokenEncryption, error) {
	secretKey, err := deriveTokenEncryptionKey([]byte("ccx-siteaccess-token-v1"))
	if err != nil {
		return nil, fmt.Errorf("[SiteAccess-TokenEncryption] 派生加密密钥失败: %w", err)
	}
	return &tokenEncryption{secretKey: secretKey}, nil
}

// deriveTokenEncryptionKey 优先读 CCX_SECRET_KEY，未设置则机器派生。
func deriveTokenEncryptionKey(salt []byte) ([]byte, error) {
	if envKey := strings.TrimSpace(os.Getenv("CCX_SECRET_KEY")); envKey != "" {
		return pbkdf2.Key([]byte(envKey), salt, 100000, 32, sha256.New), nil
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "ccx-default-host"
	}
	machineID := hostname + "|" + runtime.GOOS + "|" + runtime.GOARCH
	return pbkdf2.Key([]byte(machineID), salt, 10000, 32, sha256.New), nil
}

// encryptToken 对敏感明文加密并返回带前缀的密文字串；空字符串直接返回空。
func (te *tokenEncryption) encryptToken(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if te == nil || len(te.secretKey) != 32 {
		return "", fmt.Errorf("[SiteAccess-TokenEncryption] 加密密钥未初始化")
	}
	block, err := aes.NewCipher(te.secretKey)
	if err != nil {
		return "", fmt.Errorf("[SiteAccess-TokenEncryption] 创建 cipher 失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("[SiteAccess-TokenEncryption] 创建 GCM 失败: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("[SiteAccess-TokenEncryption] 读取 nonce 失败: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encryptedTokenPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptToken 解密带前缀的密文；无前缀时视为旧明文原样返回。
func (te *tokenEncryption) decryptToken(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, encryptedTokenPrefix) {
		return value, nil
	}
	if te == nil || len(te.secretKey) != 32 {
		return "", fmt.Errorf("[SiteAccess-TokenEncryption] 解密密钥未初始化")
	}
	encoded := strings.TrimPrefix(value, encryptedTokenPrefix)
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("[SiteAccess-TokenEncryption] base64 解码失败: %w", err)
	}
	block, err := aes.NewCipher(te.secretKey)
	if err != nil {
		return "", fmt.Errorf("[SiteAccess-TokenEncryption] 创建 cipher 失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("[SiteAccess-TokenEncryption] 创建 GCM 失败: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("[SiteAccess-TokenEncryption] 密文长度不足")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("[SiteAccess-TokenEncryption] GCM 解密失败: %w", err)
	}
	return string(plaintext), nil
}
