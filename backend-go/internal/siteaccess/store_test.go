package siteaccess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenEncryptionRoundTrip(t *testing.T) {
	enc, err := newTokenEncryption()
	if err != nil {
		t.Fatalf("newTokenEncryption: %v", err)
	}

	ciphertext, err := enc.encryptToken("test-access-token-12345")
	if err != nil {
		t.Fatalf("encryptToken: %v", err)
	}
	if !strings.HasPrefix(ciphertext, encryptedTokenPrefix) {
		t.Fatalf("密文缺少前缀: %q", ciphertext)
	}
	if strings.Contains(ciphertext, "test-access-token") {
		t.Fatalf("密文包含明文: %q", ciphertext)
	}

	plaintext, err := enc.decryptToken(ciphertext)
	if err != nil {
		t.Fatalf("decryptToken: %v", err)
	}
	if plaintext != "test-access-token-12345" {
		t.Fatalf("往返不一致: %q", plaintext)
	}

	// 旧明文无加密标记时原样兼容
	legacy, err := enc.decryptToken("legacy-plain-token")
	if err != nil {
		t.Fatalf("decryptToken(legacy): %v", err)
	}
	if legacy != "legacy-plain-token" {
		t.Fatalf("旧明文兼容失败: %q", legacy)
	}
}

func TestAccountStoreUpsertEncryptsAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store := NewAccountStore(path)

	account := SiteAccount{
		SiteType:    "newapi",
		BaseURL:     "https://relay.example.com/",
		UserID:      "1",
		AccessToken: "secret-token-abcdef",
	}
	uid, err := store.Upsert(account)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取落盘文件: %v", err)
	}
	if strings.Contains(string(raw), "secret-token-abcdef") {
		t.Fatalf("落盘文件包含明文令牌")
	}

	if uid == "" {
		t.Fatalf("Upsert 未派生 UID")
	}
	account.UID = uid

	// 同站点同用户同令牌重复接入幂等：UID 相同，不产生第二条记录
	dup := account
	if _, err := store.Upsert(dup); err != nil {
		t.Fatalf("重复 Upsert: %v", err)
	}
	accounts, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("重复接入产生 %d 条记录，期望 1", len(accounts))
	}

	plain, err := store.PlaintextAccessToken(account.UID)
	if err != nil {
		t.Fatalf("PlaintextAccessToken: %v", err)
	}
	if plain != "secret-token-abcdef" {
		t.Fatalf("明文往返不一致: %q", plain)
	}

	got, found, err := store.Get(account.UID)
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if masked := got.MaskedAccessToken(); masked != "****cdef" {
		t.Fatalf("脱敏展示错误: %q", masked)
	}

	if err := store.Delete(account.UID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := store.Get(account.UID); found {
		t.Fatalf("删除后仍能取到账号")
	}
}

func TestDeriveAccountUIDStable(t *testing.T) {
	a := DeriveAccountUID("newapi", "https://relay.example.com/", "1", "tok")
	b := DeriveAccountUID("newapi", "https://relay.example.com", "1", "tok")
	if a != b {
		t.Fatalf("baseURL 尾斜杠应归一: %q != %q", a, b)
	}
	c := DeriveAccountUID("newapi", "https://relay.example.com", "2", "tok")
	if a == c {
		t.Fatalf("不同用户应派生不同 UID")
	}
}
