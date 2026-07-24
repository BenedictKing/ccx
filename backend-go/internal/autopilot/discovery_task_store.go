package autopilot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/errutil"
)

// ── DiscoveryTaskStore: 后台 discovery 任务落盘与断点续传 ──
//
// 复用 ProfileStore 打开的同一个 *sql.DB（autopilot schema v7）。
// 与 AutoDiscoveryRunner 的内存 tasks map 互补：内存用于快速读与 running 去重，
// 本 store 负责把 running/done/failed 状态与端点级 checkpoint 持久化，支撑重启续传。
//
// 写入顺序约束（由 runner 保证）：画像 Flush 成功后才调用 UpsertEndpointCheckpoint
// 标记 profilePersisted=true。崩溃恢复时未持久化的端点会重试，不会出现
// “checkpoint 已成功但画像不存在”。

// discoveryTasksSchemaSQL discovery 任务表 DDL。幂等：CREATE TABLE/INDEX IF NOT EXISTS。
const discoveryTasksSchemaSQL = `
CREATE TABLE IF NOT EXISTS autopilot_discovery_tasks (
  channel_uid       TEXT PRIMARY KEY,
  account_uid       TEXT NOT NULL DEFAULT '',
  channel_kind      TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL,
  started_at        INTEGER,
  finished_at       INTEGER,
  error             TEXT NOT NULL DEFAULT '',
  endpoints_payload TEXT NOT NULL DEFAULT '[]',
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_discovery_tasks_status
  ON autopilot_discovery_tasks(status);
`

// CheckpointedEndpoint 端点级 checkpoint（脱敏，不含明文 API Key）。
type CheckpointedEndpoint struct {
	EndpointUID           string   `json:"endpointUid"`
	KeyHash               string   `json:"keyHash"`
	CredentialUID         string   `json:"credentialUid"`
	BaseURL               string   `json:"baseUrl"`
	Models                []string `json:"models,omitempty"`
	ModelsCount           int      `json:"modelsCount"`
	ProtocolOk            bool     `json:"protocolOk"`
	Error                 string   `json:"error,omitempty"`
	ModelDiscoverySource  string   `json:"modelDiscoverySource,omitempty"`
	ModelDiscoveryMessage string   `json:"modelDiscoveryMessage,omitempty"`
	// ProfilePersisted 表示该端点对应的画像已成功 Flush。
	// 仅 true 且 endpointUID 仍匹配当前配置时，恢复期才跳过该端点。
	ProfilePersisted bool `json:"profilePersisted"`
}

// PersistedDiscoveryTask 持久化的 discovery 任务记录。
type PersistedDiscoveryTask struct {
	ChannelUID   string
	AccountUID   string
	ChannelKind  string
	Status       DiscoveryStatus
	StartedAtMs  int64
	FinishedAtMs int64
	Error        string
	Endpoints    []CheckpointedEndpoint
	CreatedAtMs  int64
	UpdatedAtMs  int64
}

// DiscoveryTaskStore 后台 discovery 任务的 SQLite 持久化层。
// 无内存缓存：runner 维护内存 map，本 store 按需读写 DB。
type DiscoveryTaskStore struct {
	db *sql.DB
	mu sync.Mutex // 串行化 checkpoint 的 read-modify-write，避免 payload 覆盖
}

// NewDiscoveryTaskStoreWithDB 使用外部 *sql.DB 创建 store（db 可为 nil，nil 时为 no-op）。
func NewDiscoveryTaskStoreWithDB(db *sql.DB) (*DiscoveryTaskStore, error) {
	if db == nil {
		return &DiscoveryTaskStore{}, nil
	}
	if err := initDiscoveryTaskStoreSchema(db); err != nil {
		return nil, fmt.Errorf("[DiscoveryTaskStore-Init] 建表失败: %w", err)
	}
	log.Printf("[DiscoveryTaskStore-Init] 初始化完成")
	return &DiscoveryTaskStore{db: db}, nil
}

// initDiscoveryTaskStoreSchema 建表（幂等）。
func initDiscoveryTaskStoreSchema(db *sql.DB) error {
	_, err := db.Exec(discoveryTasksSchemaSQL)
	return err
}

// initDiscoveryTaskStoreSchemaTx 事务内建表（供 schema 迁移复用）。
func initDiscoveryTaskStoreSchemaTx(tx *sql.Tx) error {
	_, err := tx.Exec(discoveryTasksSchemaSQL)
	return err
}

// Start 创建/重置一条 running 任务记录（清空旧 payload）。
// 已存在同 channelUID 记录则覆盖为 running、endpoints=[]。
func (s *DiscoveryTaskStore) Start(channelUID, accountUID, channelKind string) error {
	if s == nil || s.db == nil {
		return nil
	}
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`INSERT INTO autopilot_discovery_tasks
		(channel_uid, account_uid, channel_kind, status, started_at, finished_at, error, endpoints_payload, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NULL, '', '[]', ?, ?)
		ON CONFLICT(channel_uid) DO UPDATE SET
			account_uid=excluded.account_uid,
			channel_kind=excluded.channel_kind,
			status=excluded.status,
			started_at=excluded.started_at,
			finished_at=NULL,
			error='',
			endpoints_payload='[]',
			updated_at=excluded.updated_at`,
		channelUID, accountUID, channelKind, string(DiscoveryStatusRunning), now, now, now)
	if err != nil {
		return fmt.Errorf("[DiscoveryTaskStore-Start] 写入失败: %w", err)
	}
	return nil
}

// UpsertEndpointCheckpoint 在 endpoints_payload 中按 endpointUID upsert 一条端点 checkpoint。
// read-modify-write 在事务 + store 串行锁内完成，避免并发覆盖。
func (s *DiscoveryTaskStore) UpsertEndpointCheckpoint(channelUID string, ep CheckpointedEndpoint) error {
	if s == nil || s.db == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("[DiscoveryTaskStore-Checkpoint] 开始事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var payload string
	err = tx.QueryRow(`SELECT endpoints_payload FROM autopilot_discovery_tasks WHERE channel_uid=?`, channelUID).Scan(&payload)
	if err == sql.ErrNoRows {
		return fmt.Errorf("[DiscoveryTaskStore-Checkpoint] 任务记录不存在 channel=%s", channelUID)
	}
	if err != nil {
		return fmt.Errorf("[DiscoveryTaskStore-Checkpoint] 读取 payload 失败: %w", err)
	}

	var eps []CheckpointedEndpoint
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &eps); err != nil {
			log.Printf("[DiscoveryTaskStore-Checkpoint] payload 反序列化失败 channel=%s: %v，将重置为空数组", channelUID, err)
			eps = nil
		}
	}
	upserted := false
	for i, e := range eps {
		if e.EndpointUID == ep.EndpointUID {
			eps[i] = ep
			upserted = true
			break
		}
	}
	if !upserted {
		eps = append(eps, ep)
	}
	b, err := json.Marshal(eps)
	if err != nil {
		return fmt.Errorf("[DiscoveryTaskStore-Checkpoint] 序列化失败: %w", err)
	}
	now := time.Now().UnixMilli()
	if _, err := tx.Exec(`UPDATE autopilot_discovery_tasks SET endpoints_payload=?, updated_at=? WHERE channel_uid=?`, string(b), now, channelUID); err != nil {
		return fmt.Errorf("[DiscoveryTaskStore-Checkpoint] 写入失败: %w", err)
	}
	return tx.Commit()
}

// Finish 更新任务终态（done/failed），写入 finished_at 与 error。
func (s *DiscoveryTaskStore) Finish(channelUID string, status DiscoveryStatus, errMsg string) error {
	if s == nil || s.db == nil {
		return nil
	}
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`UPDATE autopilot_discovery_tasks SET status=?, finished_at=?, error=?, updated_at=? WHERE channel_uid=?`,
		string(status), now, errMsg, now, channelUID)
	if err != nil {
		return fmt.Errorf("[DiscoveryTaskStore-Finish] 写入失败: %w", err)
	}
	return nil
}

// LoadRunning 加载所有 status='running' 的任务记录（供重启续传）。
func (s *DiscoveryTaskStore) LoadRunning() ([]PersistedDiscoveryTask, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT channel_uid, account_uid, channel_kind, started_at, endpoints_payload
		FROM autopilot_discovery_tasks WHERE status=?`, string(DiscoveryStatusRunning))
	if err != nil {
		return nil, fmt.Errorf("[DiscoveryTaskStore-LoadRunning] 查询失败: %w", err)
	}
	defer errutil.IgnoreDeferred(rows.Close)

	var result []PersistedDiscoveryTask
	for rows.Next() {
		var task PersistedDiscoveryTask
		var startedAt sql.NullInt64
		var payload string
		if err := rows.Scan(&task.ChannelUID, &task.AccountUID, &task.ChannelKind, &startedAt, &payload); err != nil {
			log.Printf("[DiscoveryTaskStore-LoadRunning] 跳过损坏行: %v", err)
			continue
		}
		task.Status = DiscoveryStatusRunning
		task.StartedAtMs = startedAt.Int64
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &task.Endpoints); err != nil {
				log.Printf("[DiscoveryTaskStore-LoadRunning] payload 反序列化失败 channel=%s: %v", task.ChannelUID, err)
			}
		}
		result = append(result, task)
	}
	return result, rows.Err()
}

// GC 删除 done/failed 且 finished_at 早于 (now - olderThan) 的记录，不删 running。
// 返回删除行数。
func (s *DiscoveryTaskStore) GC(olderThan time.Duration) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	cutoff := time.Now().Add(-olderThan).UnixMilli()
	res, err := s.db.Exec(`DELETE FROM autopilot_discovery_tasks
		WHERE status IN (?, ?) AND finished_at IS NOT NULL AND finished_at < ?`,
		string(DiscoveryStatusDone), string(DiscoveryStatusFailed), cutoff)
	if err != nil {
		return 0, fmt.Errorf("[DiscoveryTaskStore-GC] 删除失败: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
