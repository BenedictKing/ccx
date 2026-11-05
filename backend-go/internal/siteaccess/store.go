// Package siteaccess 提供中转站（new-api 家族等）账号接入能力：
// 管理面认证、用户/分组/模型读取、远端推理 Key 的查重/创建/揭示，
// 以及管理凭证的加密持久化。独立于渠道调度，供各协议接入流程共用。
package siteaccess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProvisionedKeyMeta 记录已在远端站点创建的推理 Key 元数据（不含明文）。
// 明文只在创建当下经适配器取回并写入对应协议渠道，本存储不持久化明文。
type ProvisionedKeyMeta struct {
	Name     string `json:"name"`
	Group    string `json:"group,omitempty"`
	TokenID  int    `json:"tokenId,omitempty"`
	Protocol string `json:"protocol"` // messages / chat / responses / gemini
	// KeyFingerprint 是推理 Key 的 sha256 指纹（16 hex），用于对账时匹配本地渠道
	// 中的 Key；不明文持久化推理 Key。
	KeyFingerprint string `json:"keyFingerprint,omitempty"`
}

// SiteAccount 站点托管账号：管理凭证 + 已建远端 Key 元数据。
type SiteAccount struct {
	UID           string    `json:"uid"`
	SiteType      string    `json:"siteType"` // newapi / sub2api
	BaseURL       string    `json:"baseUrl"`
	UserID        string    `json:"userId,omitempty"`
	AuthTokenMode string    `json:"authTokenMode,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`

	// AccessToken 是站点系统访问令牌，敏感数据。
	// 落盘时加密（$enc$ 前缀），通过 PlaintextAccessToken 取明文；
	// 列表/详情输出用 MaskedAccessToken 脱敏。
	AccessToken string `json:"accessToken,omitempty"`

	ProvisionedKeys []ProvisionedKeyMeta `json:"provisionedKeys,omitempty"`
}

// DeriveAccountUID 从站点身份派生稳定账号 UID：同站点同用户同令牌重复接入幂等。
func DeriveAccountUID(siteType, baseURL, userID, accessToken string) string {
	normalized := strings.ToLower(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(siteType), normalized, strings.TrimSpace(userID), strings.TrimSpace(accessToken),
	}, "|")))
	return strings.TrimSpace(siteType) + "_" + hex.EncodeToString(sum[:8])
}

// MaskAccessToken 返回脱敏后的令牌展示形式（仅尾部 4 位）。
func MaskAccessToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
}

// MaskedAccessToken 返回脱敏后的令牌展示形式（仅尾部 4 位）。
func (a *SiteAccount) MaskedAccessToken() string {
	plain, err := a.plaintextAccessToken()
	if err != nil {
		return ""
	}
	return MaskAccessToken(plain)
}

// plaintextAccessToken 取管理令牌明文（落盘为密文时透明解密）。
func (a *SiteAccount) plaintextAccessToken() (string, error) {
	enc, err := sharedTokenEncryption()
	if err != nil {
		return "", err
	}
	return enc.decryptToken(a.AccessToken)
}

// AccountStore 是站点账号的 JSON 持久化存储，落盘字段加密、写盘原子化。
type AccountStore struct {
	path string
	mu   sync.RWMutex
}

var (
	sharedEnc     *tokenEncryption
	sharedEncOnce sync.Once
	sharedEncErr  error
)

func sharedTokenEncryption() (*tokenEncryption, error) {
	sharedEncOnce.Do(func() {
		sharedEnc, sharedEncErr = newTokenEncryption()
	})
	return sharedEnc, sharedEncErr
}

// NewAccountStore 创建账号存储；path 为 JSON 文件路径（不存在时按空存储启动）。
func NewAccountStore(path string) *AccountStore {
	return &AccountStore{path: path}
}

// Upsert 写入或更新账号；AccessToken 为明文，落盘前加密。
// UID 为空时按 DeriveAccountUID 派生；同 UID 覆盖更新并保留 CreatedAt。
// 返回实际使用的账号 UID。
func (s *AccountStore) Upsert(account SiteAccount) (string, error) {
	if strings.TrimSpace(account.SiteType) == "" || strings.TrimSpace(account.BaseURL) == "" {
		return "", fmt.Errorf("[SiteAccess-Store] siteType 与 baseUrl 不能为空")
	}
	if account.UID == "" {
		account.UID = DeriveAccountUID(account.SiteType, account.BaseURL, account.UserID, account.AccessToken)
	}
	enc, err := sharedTokenEncryption()
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(account.AccessToken, encryptedTokenPrefix) {
		account.AccessToken, err = enc.encryptToken(account.AccessToken)
		if err != nil {
			return "", err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	accounts, err := s.loadLocked()
	if err != nil {
		return "", err
	}
	now := time.Now()
	for i := range accounts {
		if accounts[i].UID == account.UID {
			account.CreatedAt = accounts[i].CreatedAt
			account.UpdatedAt = now
			accounts[i] = account
			return account.UID, s.saveLocked(accounts)
		}
	}
	account.CreatedAt = now
	account.UpdatedAt = now
	accounts = append(accounts, account)
	return account.UID, s.saveLocked(accounts)
}

// Get 按 UID 取账号；返回副本的 AccessToken 仍为密文，需要明文时用 PlaintextAccessToken。
func (s *AccountStore) Get(uid string) (SiteAccount, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	accounts, err := s.loadLocked()
	if err != nil {
		return SiteAccount{}, false, err
	}
	for _, a := range accounts {
		if a.UID == uid {
			return a, true, nil
		}
	}
	return SiteAccount{}, false, nil
}

// List 返回全部账号（AccessToken 为密文，展示用 MaskedAccessToken）。
func (s *AccountStore) List() ([]SiteAccount, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked()
}

// PlaintextAccessToken 取指定账号的管理令牌明文。
func (s *AccountStore) PlaintextAccessToken(uid string) (string, error) {
	account, found, err := s.Get(uid)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("[SiteAccess-Store] 账号不存在: %s", uid)
	}
	return account.plaintextAccessToken()
}

// Delete 按 UID 删除账号；不存在时不报错。
func (s *AccountStore) Delete(uid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	accounts, err := s.loadLocked()
	if err != nil {
		return err
	}
	kept := accounts[:0]
	for _, a := range accounts {
		if a.UID != uid {
			kept = append(kept, a)
		}
	}
	return s.saveLocked(kept)
}

func (s *AccountStore) loadLocked() ([]SiteAccount, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("[SiteAccess-Store] 读取失败: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var accounts []SiteAccount
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, fmt.Errorf("[SiteAccess-Store] 解析失败: %w", err)
	}
	return accounts, nil
}

// saveLocked 原子写盘：先写临时文件再 rename，避免中途崩溃留下半个 JSON。
func (s *AccountStore) saveLocked(accounts []SiteAccount) error {
	data, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return fmt.Errorf("[SiteAccess-Store] 序列化失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("[SiteAccess-Store] 创建目录失败: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("[SiteAccess-Store] 写临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("[SiteAccess-Store] 替换文件失败: %w", err)
	}
	return nil
}
