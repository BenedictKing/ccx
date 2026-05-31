package saas

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Store SaaS 数据存储
type Store struct {
	db *sql.DB
}

// NewStore 创建 SaaS 存储（SQLite）
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("打开 SaaS 数据库失败: %w", err)
	}

	// 设置连接池
	db.SetMaxOpenConns(1) // SQLite WAL 模式下单写者
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("SaaS 数据库迁移失败: %w", err)
	}

	log.Println("[SaaS-Init] SaaS 数据库已初始化")
	return store, nil
}

// migrate 数据库迁移
func (s *Store) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			name TEXT NOT NULL,
			api_key TEXT UNIQUE NOT NULL,
			plan TEXT NOT NULL DEFAULT 'free',
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS usage_records (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			date TEXT NOT NULL,
			api_calls INTEGER NOT NULL DEFAULT 0,
			tokens_in INTEGER NOT NULL DEFAULT 0,
			tokens_out INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			FOREIGN KEY (user_id) REFERENCES users(id),
			UNIQUE(user_id, date)
		)`,
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL UNIQUE,
			plan TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			current_period_start TEXT NOT NULL,
			current_period_end TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_users_api_key ON users(api_key)`,
		`CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_records_user_date ON usage_records(user_id, date)`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_user ON subscriptions(user_id)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("迁移失败: %w\nSQL: %s", err, m)
		}
	}
	return nil
}

// Close 关闭数据库
func (s *Store) Close() error {
	return s.db.Close()
}

// DB 暴露原始 *sql.DB（供高级查询）
func (s *Store) DB() *sql.DB {
	return s.db
}

// GetUserByAPIKey 通过 API Key 获取用户
func (s *Store) GetUserByAPIKey(apiKey string) (*User, error) {
	return getUserByAPIKey(s.db, apiKey)
}

// GetUserByID 通过 ID 获取用户
func (s *Store) GetUserByID(id string) (*User, error) {
	return getUserByID(s.db, id)
}

// UpdateUserAPIKey 更新用户的 API Key
func (s *Store) UpdateUserAPIKey(userID, newKey string) error {
	now := time.Now()
	_, err := s.db.Exec(
		`UPDATE users SET api_key = ?, updated_at = ? WHERE id = ?`,
		newKey, now.Format(time.RFC3339), userID,
	)
	return err
}

// GetUserUsage 获取用户某月的用量
func (s *Store) GetUserUsage(userID, yearMonth string) (*UsageStats, error) {
	stats := &UsageStats{}
	// 日期范围: YYYY-MM-01 到 YYYY-MM-31
	row := s.db.QueryRow(
		`SELECT COALESCE(SUM(api_calls), 0), COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0)
		 FROM usage_records WHERE user_id = ? AND date LIKE ? || '%'`,
		userID, yearMonth,
	)
	err := row.Scan(&stats.APICalls, &stats.TokensIn, &stats.TokensOut)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

// ResetMonthlyUsage 重置某个月份的所有用量记录（在月初调用）
// 返回该月受影响的行数
func (s *Store) ResetMonthlyUsage(yearMonth string) error {
	_, err := s.db.Exec(
		`DELETE FROM usage_records WHERE date LIKE ? || '%'`,
		yearMonth,
	)
	return err
}

// RecordUsage 记录一次 API 调用
func (s *Store) RecordUsage(userID string, tokensIn, tokensOut int64) error {
	today := time.Now().Format("2006-01-02")
	_, err := s.db.Exec(
		`INSERT INTO usage_records (id, user_id, date, api_calls, tokens_in, tokens_out, created_at)
		 VALUES (?, ?, ?, 1, ?, ?, ?)
		 ON CONFLICT(user_id, date) DO UPDATE SET
		 api_calls = api_calls + 1,
		 tokens_in = tokens_in + ?,
		 tokens_out = tokens_out + ?`,
		uuid.New().String(), userID, today, tokensIn, tokensOut, time.Now().Format(time.RFC3339),
		tokensIn, tokensOut,
	)
	return err
}

// ListUsers 获取用户列表（管理员用）
func (s *Store) ListUsers(limit, offset int) ([]*User, int, error) {
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(
		`SELECT id, email, password_hash, name, api_key, plan, is_admin, created_at, updated_at
		 FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u := &User{}
		var createdAt, updatedAt string
		err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.APIKey, &u.Plan, &u.IsAdmin, &createdAt, &updatedAt)
		if err != nil {
			return nil, 0, err
		}
		u.PasswordHash = ""
		u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		users = append(users, u)
	}
	return users, total, nil
}

// UpdateUserPlan 更新用户套餐
func (s *Store) UpdateUserPlan(userID string, plan string) error {
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`UPDATE users SET plan = ?, updated_at = ? WHERE id = ?`, plan, now.Format(time.RFC3339), userID)
	if err != nil {
		return err
	}

	// 创建或更新 subscription 记录
	periodStart := time.Now()
	periodEnd := periodStart.AddDate(0, 1, 0) // 一个月
	_, err = tx.Exec(
		`INSERT INTO subscriptions (id, user_id, plan, status, current_period_start, current_period_end, created_at, updated_at)
		 VALUES (?, ?, ?, 'active', ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		 plan = excluded.plan,
		 status = 'active',
		 current_period_start = excluded.current_period_start,
		 current_period_end = excluded.current_period_end,
		 updated_at = excluded.updated_at`,
		uuid.New().String(), userID, plan,
		periodStart.Format(time.RFC3339), periodEnd.Format(time.RFC3339),
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// DeleteUserByID 删除用户
func (s *Store) DeleteUserByID(userID string) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, userID)
	return err
}

// globalStore 包级全局存储实例
var globalStore *Store

// SetGlobalStore 设置全局 Store 实例
func SetGlobalStore(s *Store) {
	globalStore = s
}

// GetGlobalStore 获取全局 Store 实例
func GetGlobalStore() *Store {
	return globalStore
}
