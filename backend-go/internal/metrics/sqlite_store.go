package metrics

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/utils"

	"github.com/BenedictKing/ccx/internal/errutil"
	_ "modernc.org/sqlite"
)

// SQLiteStore SQLite 持久化存储
type SQLiteStore struct {
	db     *sql.DB
	dbPath string

	// 写入缓冲区
	writeBuffer []PersistentRecord
	bufferMu    sync.Mutex

	// 配置
	batchSize     int           // 批量写入阈值（记录数）
	flushInterval time.Duration // 定时刷新间隔
	retentionDays int           // 数据保留天数

	// 控制
	stopCh       chan struct{}
	wg           sync.WaitGroup
	closed       bool           // 是否已关闭
	flushMu      sync.Mutex     // 串行化 flush 与 delete 操作，避免并发竞态
	asyncFlushWg sync.WaitGroup // 追踪 AddRecord 触发的异步 flush goroutine
	flushing     atomic.Bool    // 原子标记：是否有 flush goroutine 正在运行/排队
}

// SQLiteStoreConfig SQLite 存储配置
type SQLiteStoreConfig struct {
	DBPath        string // 数据库文件路径
	RetentionDays int    // 数据保留天数（3-90）
}

// 硬编码的内部配置
const (
	defaultBatchSize     = 100              // 批量写入阈值
	defaultFlushInterval = 30 * time.Second // 定时刷新间隔
)

// NewSQLiteStore 创建 SQLite 存储
func NewSQLiteStore(cfg *SQLiteStoreConfig) (*SQLiteStore, error) {
	if cfg == nil {
		cfg = &SQLiteStoreConfig{
			DBPath:        ".config/metrics.db",
			RetentionDays: 366,
		}
	}

	// 验证保留天数范围
	if cfg.RetentionDays < 3 {
		cfg.RetentionDays = 3
	} else if cfg.RetentionDays > 366 {
		cfg.RetentionDays = 366
	}

	// 确保目录存在
	dir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	// 打开数据库连接（WAL 模式 + NORMAL 同步）
	// modernc.org/sqlite 使用 _pragma= 语法设置 PRAGMA
	dsn := cfg.DBPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 设置连接池参数
	db.SetMaxOpenConns(1) // SQLite 单写入连接
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // 不限制连接生命周期

	// 初始化表结构
	if err := initSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("初始化数据库 schema 失败: %w", err)
	}

	store := &SQLiteStore{
		db:            db,
		dbPath:        cfg.DBPath,
		writeBuffer:   make([]PersistentRecord, 0, defaultBatchSize),
		batchSize:     defaultBatchSize,
		flushInterval: defaultFlushInterval,
		retentionDays: cfg.RetentionDays,
		stopCh:        make(chan struct{}),
	}

	// 启动后台任务
	store.wg.Add(2)
	go store.flushLoop()
	go store.cleanupLoop()

	log.Printf("[SQLite-Init] 指标存储已初始化: %s (保留 %d 天)", cfg.DBPath, cfg.RetentionDays)
	return store, nil
}

// initSchema 初始化数据库表结构
func initSchema(db *sql.DB) error {
	schema := `
		-- 请求记录表
		CREATE TABLE IF NOT EXISTS request_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			metrics_key TEXT NOT NULL,
			base_url TEXT NOT NULL,
			key_mask TEXT NOT NULL,
			timestamp INTEGER NOT NULL,
			success INTEGER NOT NULL,
			failure_class TEXT NOT NULL DEFAULT '',
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cache_creation_tokens INTEGER DEFAULT 0,
			cache_read_tokens INTEGER DEFAULT 0,
			api_type TEXT NOT NULL DEFAULT 'messages'
		);

		CREATE TABLE IF NOT EXISTS circuit_states (
			metrics_key TEXT NOT NULL,
			api_type TEXT NOT NULL,
			base_url TEXT NOT NULL,
			key_mask TEXT NOT NULL,
			circuit_state TEXT NOT NULL DEFAULT 'closed',
			circuit_opened_at INTEGER,
			half_open_at INTEGER,
			next_retry_at INTEGER,
			backoff_level INTEGER NOT NULL DEFAULT 0,
			half_open_successes INTEGER NOT NULL DEFAULT 0,
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (metrics_key, api_type)
		);

		-- 渠道保活验证结果表（幂等创建，无需迁移版本号）
		CREATE TABLE IF NOT EXISTS key_health (
			channel_type TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			key_mask TEXT NOT NULL,
			check_kind TEXT NOT NULL,
			last_check_at INTEGER NOT NULL,
			last_status TEXT NOT NULL DEFAULT 'ok',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			model_count INTEGER NOT NULL DEFAULT 0,
			detail TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (channel_type, channel_id, key_mask, check_kind)
		);

		-- 索引：按 api_type 和时间查询
		CREATE INDEX IF NOT EXISTS idx_records_api_type_timestamp
			ON request_records(api_type, timestamp);

		-- 索引：按 metrics_key 查询
		CREATE INDEX IF NOT EXISTS idx_records_metrics_key
			ON request_records(metrics_key);

		-- 索引：按 api_type + metrics_key + timestamp 查询（渠道级长时间范围聚合）
		CREATE INDEX IF NOT EXISTS idx_records_api_type_metrics_key_timestamp
			ON request_records(api_type, metrics_key, timestamp);
	`

	if _, err := db.Exec(schema); err != nil {
		return err
	}

	// 版本迁移：使用 user_version PRAGMA 检测 schema 版本
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}

	if version < 1 {
		// v0 -> v1: 添加 model 列
		migrations := []string{
			"ALTER TABLE request_records ADD COLUMN model TEXT DEFAULT ''",
			"CREATE INDEX IF NOT EXISTS idx_records_model ON request_records(model)",
			"PRAGMA user_version = 1",
		}
		for _, sql := range migrations {
			if _, err := db.Exec(sql); err != nil {
				return fmt.Errorf("migration v0->v1 failed: %w", err)
			}
		}
		log.Printf("[SQLite-Migration] schema 升级: v0 -> v1 (添加 model 列)")
		version = 1
	}

	if version < 2 {
		migrations := []string{
			"ALTER TABLE request_records ADD COLUMN failure_class TEXT NOT NULL DEFAULT ''",
			"PRAGMA user_version = 2",
		}
		for _, sql := range migrations {
			if _, err := db.Exec(sql); err != nil {
				if !strings.Contains(err.Error(), "duplicate column name") {
					return fmt.Errorf("migration v1->v2 failed: %w", err)
				}
			}
		}
		log.Printf("[SQLite-Migration] schema 升级: v1 -> v2 (添加 failure_class 列与 circuit_states 表)")
	}

	// 复合索引：无论 schema 版本如何，幂等创建（不改变 user_version，避免干扰 v2→v3 数据迁移流程）
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_records_api_type_metrics_key_timestamp ON request_records(api_type, metrics_key, timestamp)"); err != nil {
		return fmt.Errorf("create composite index failed: %w", err)
	}

	// v4: 添加 proxy_key_mask 列（成本报表按用户维度分组）
	var curVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&curVersion); err != nil {
		return fmt.Errorf("读取 schema 版本失败: %w", err)
	}
	if curVersion < 4 {
		v4Migrations := []string{
			"ALTER TABLE request_records ADD COLUMN proxy_key_mask TEXT NOT NULL DEFAULT ''",
			"CREATE INDEX IF NOT EXISTS idx_records_proxy_key_mask ON request_records(proxy_key_mask)",
			"PRAGMA user_version = 4",
		}
		for _, q := range v4Migrations {
			if _, err := db.Exec(q); err != nil {
				if strings.Contains(err.Error(), "duplicate column name") {
					continue
				}
				return fmt.Errorf("migration v3->v4 failed: %w", err)
			}
		}
		log.Printf("[SQLite-Migration] schema 升级: v3 -> v4 (添加 proxy_key_mask 列)")
	}

	// v5: 添加 channel_uid 与 route_model 列（breaker 三元组身份）
	{
		var cv int
		if err := db.QueryRow("PRAGMA user_version").Scan(&cv); err != nil {
			return fmt.Errorf("读取 schema 版本失败: %w", err)
		}
		if cv < 5 {
			v5Migrations := []string{
				"ALTER TABLE request_records ADD COLUMN channel_uid TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE request_records ADD COLUMN route_model TEXT NOT NULL DEFAULT ''",
				"PRAGMA user_version = 5",
			}
			for _, q := range v5Migrations {
				if _, err := db.Exec(q); err != nil {
					if strings.Contains(err.Error(), "duplicate column name") {
						continue
					}
					return fmt.Errorf("migration v4->v5 failed: %w", err)
				}
			}
			log.Printf("[SQLite-Migration] schema 升级: v4 -> v5 (添加 channel_uid 与 route_model 列)")
		}
	}

	// v6: 固化请求级 effective USD 成本；旧行保持 NULL，禁止按当前配置重算。
	var costVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&costVersion); err != nil {
		return fmt.Errorf("读取 schema 版本失败: %w", err)
	}
	if costVersion < 6 {
		migrations := []string{
			"ALTER TABLE request_records ADD COLUMN key_uid TEXT",
			"ALTER TABLE request_records ADD COLUMN subscription_uid TEXT",
			"ALTER TABLE request_records ADD COLUMN exchange_snapshot_version INTEGER",
			"ALTER TABLE request_records ADD COLUMN list_cost_usd REAL",
			"ALTER TABLE request_records ADD COLUMN effective_cost_usd REAL",
			"ALTER TABLE request_records ADD COLUMN effective_cost_available INTEGER",
			"ALTER TABLE request_records ADD COLUMN effective_cost_reason TEXT",
			"PRAGMA user_version = 6",
		}
		for _, q := range migrations {
			if _, err := db.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("migration v5->v6 failed: %w", err)
			}
		}
		log.Printf("[SQLite-Migration] schema 升级: v5 -> v6 (固化 effective USD 成本)")
	}

	// v7: 记录 Key 级 ConsumptionPolicy，用于 opportunistic 消耗分析。
	var policyVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&policyVersion); err != nil {
		return fmt.Errorf("读取 schema 版本失败: %w", err)
	}
	if policyVersion < 7 {
		migrations := []string{
			"ALTER TABLE request_records ADD COLUMN consumption_policy TEXT NOT NULL DEFAULT ''",
			"PRAGMA user_version = 7",
		}
		for _, q := range migrations {
			if _, err := db.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("migration v6->v7 failed: %w", err)
			}
		}
		log.Printf("[SQLite-Migration] schema 升级: v6 -> v7 (添加 consumption_policy 列)")
	}

	return nil
}

func (s *SQLiteStore) schemaVersion() (int, error) {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

type metricsKeyMigrationTarget struct {
	MetricsKey string
	BaseURL    string
}

type metricsKeyMigrationCandidates struct {
	Primary   metricsKeyMigrationTarget
	Conflicts []metricsKeyMigrationTarget
}

type persistedCircuitStateRow struct {
	PersistentCircuitState
	UpdatedAt int64
}

func (s *SQLiteStore) MigrateMetricsKeysToIdentity(cfg config.Config) error {
	version, err := s.schemaVersion()
	if err != nil {
		return fmt.Errorf("读取 schema 版本失败: %w", err)
	}
	if version >= 3 {
		return nil
	}

	mapping := buildMetricsKeyMigrationMap(cfg)

	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.flushBufferLocked()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开始 metrics key 迁移事务失败: %w", err)
	}
	defer errutil.IgnoreDeferred(tx.Rollback)

	updatedRecords, err := migrateRequestRecordsTx(tx, mapping)
	if err != nil {
		return err
	}
	migratedStates, mergedStates, err := migrateCircuitStatesTx(tx, mapping)
	if err != nil {
		return err
	}
	if _, err := tx.Exec("PRAGMA user_version = 3"); err != nil {
		return fmt.Errorf("写入 schema 版本失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 metrics key 迁移失败: %w", err)
	}

	log.Printf("[SQLite-Migration] schema/data 升级: v2 -> v3 (迁移 request_records=%d, circuit_states=%d, merged_states=%d)", updatedRecords, migratedStates, mergedStates)
	return nil
}

func buildMetricsKeyMigrationMap(cfg config.Config) map[string]map[string]metricsKeyMigrationCandidates {
	mapping := map[string]map[string]metricsKeyMigrationCandidates{
		"messages":  {},
		"responses": {},
		"gemini":    {},
		"chat":      {},
		"images":    {},
		"vectors":   {},
	}

	addUpstreamMetricsKeyMappings(mapping["messages"], cfg.Upstream, "claude")
	addUpstreamMetricsKeyMappings(mapping["responses"], cfg.ResponsesUpstream, "responses")
	addUpstreamMetricsKeyMappings(mapping["gemini"], cfg.GeminiUpstream, "gemini")
	addUpstreamMetricsKeyMappings(mapping["chat"], cfg.ChatUpstream, "openai")
	addUpstreamMetricsKeyMappings(mapping["images"], cfg.ImagesUpstream, "openai")
	addUpstreamMetricsKeyMappings(mapping["vectors"], cfg.VectorsUpstream, "openai")

	return mapping
}

func legacyMetricsKeysForMigration(baseURL, apiKey, serviceType string) []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0, 6)
	add := func(rawBaseURL string) {
		if rawBaseURL == "" {
			return
		}
		metricsKey := GenerateMetricsKey(rawBaseURL, apiKey)
		if _, exists := seen[metricsKey]; exists {
			return
		}
		seen[metricsKey] = struct{}{}
		keys = append(keys, metricsKey)
	}

	for _, variant := range utils.EquivalentBaseURLVariants(baseURL, serviceType) {
		add(variant)
	}
	add(utils.MetricsIdentityBaseURL(baseURL, serviceType))
	return keys
}

func addUpstreamMetricsKeyMappings(targets map[string]metricsKeyMigrationCandidates, upstreams []config.UpstreamConfig, defaultServiceType string) {
	for _, upstream := range upstreams {
		serviceType := upstream.ServiceType
		if serviceType == "" {
			serviceType = defaultServiceType
		}
		baseURLs := upstream.GetAllBaseURLs()
		if len(baseURLs) == 0 {
			continue
		}
		keys := deduplicateMetricMigrationKeys(upstream.APIKeys, upstream.HistoricalAPIKeys)
		for _, baseURL := range baseURLs {
			identityBaseURL := utils.MetricsIdentityBaseURL(baseURL, serviceType)
			for _, apiKey := range keys {
				if apiKey == "" {
					continue
				}
				migrationTarget := metricsKeyMigrationTarget{
					MetricsKey: GenerateMetricsIdentityKey(baseURL, apiKey, serviceType),
					BaseURL:    identityBaseURL,
				}
				for _, legacyKey := range legacyMetricsKeysForMigration(baseURL, apiKey, serviceType) {
					candidate, exists := targets[legacyKey]
					if !exists {
						targets[legacyKey] = metricsKeyMigrationCandidates{Primary: migrationTarget}
						continue
					}
					if candidate.Primary == migrationTarget || containsMigrationConflict(candidate.Conflicts, migrationTarget) {
						continue
					}
					candidate.Conflicts = append(candidate.Conflicts, migrationTarget)
					targets[legacyKey] = candidate
				}
			}
		}
	}
}

func containsMigrationConflict(conflicts []metricsKeyMigrationTarget, target metricsKeyMigrationTarget) bool {
	for _, conflict := range conflicts {
		if conflict == target {
			return true
		}
	}
	return false
}

func deduplicateMetricMigrationKeys(groups ...[]string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, group := range groups {
		for _, key := range group {
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, key)
		}
	}
	return result
}

func migrateRequestRecordsTx(tx *sql.Tx, mapping map[string]map[string]metricsKeyMigrationCandidates) (int64, error) {
	var totalUpdated int64
	for apiType, targets := range mapping {
		for legacyKey, candidate := range targets {
			if len(candidate.Conflicts) > 0 {
				var count int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM request_records WHERE api_type = ? AND metrics_key = ?`, apiType, legacyKey).Scan(&count); err != nil {
					return totalUpdated, fmt.Errorf("检查 request_records 迁移冲突失败(apiType=%s, metricsKey=%s): %w", apiType, legacyKey, err)
				}
				if count > 0 {
					return totalUpdated, fmt.Errorf("legacy metrics key %s 在 apiType=%s 的 request_records 中存在 %d 条待迁移记录，但映射到多个 identity target: primary=%+v conflicts=%+v", legacyKey, apiType, count, candidate.Primary, candidate.Conflicts)
				}
			}
			result, err := tx.Exec(`
				UPDATE request_records
				SET metrics_key = ?, base_url = ?
				WHERE api_type = ? AND metrics_key = ? AND (metrics_key != ? OR base_url != ?)
			`, candidate.Primary.MetricsKey, candidate.Primary.BaseURL, apiType, legacyKey, candidate.Primary.MetricsKey, candidate.Primary.BaseURL)
			if err != nil {
				return totalUpdated, fmt.Errorf("迁移 request_records 失败(apiType=%s, metricsKey=%s): %w", apiType, legacyKey, err)
			}
			affected, _ := result.RowsAffected()
			totalUpdated += affected
		}
	}
	return totalUpdated, nil
}

func migrateCircuitStatesTx(tx *sql.Tx, mapping map[string]map[string]metricsKeyMigrationCandidates) (int, int, error) {
	rows, err := tx.Query(`
		SELECT metrics_key, api_type, base_url, key_mask, circuit_state,
		       circuit_opened_at, half_open_at, next_retry_at,
		       backoff_level, half_open_successes, consecutive_failures, updated_at
		FROM circuit_states
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("查询 circuit_states 失败: %w", err)
	}
	defer errutil.IgnoreDeferred(rows.Close)

	merged := make(map[string]*persistedCircuitStateRow)
	migratedCount := 0
	mergedCount := 0
	for rows.Next() {
		var row persistedCircuitStateRow
		var openedAt, halfOpenAt, nextRetryAt sql.NullInt64
		if err := rows.Scan(
			&row.MetricsKey,
			&row.APIType,
			&row.BaseURL,
			&row.KeyMask,
			&row.CircuitState,
			&openedAt,
			&halfOpenAt,
			&nextRetryAt,
			&row.BackoffLevel,
			&row.HalfOpenSuccesses,
			&row.ConsecutiveFailures,
			&row.UpdatedAt,
		); err != nil {
			return migratedCount, mergedCount, fmt.Errorf("扫描 circuit_states 失败: %w", err)
		}
		if openedAt.Valid {
			t := time.Unix(openedAt.Int64, 0)
			row.CircuitOpenedAt = &t
		}
		if halfOpenAt.Valid {
			t := time.Unix(halfOpenAt.Int64, 0)
			row.HalfOpenAt = &t
		}
		if nextRetryAt.Valid {
			t := time.Unix(nextRetryAt.Int64, 0)
			row.NextRetryAt = &t
		}

		if candidate, ok := mapping[row.APIType][row.MetricsKey]; ok {
			if len(candidate.Conflicts) > 0 {
				return migratedCount, mergedCount, fmt.Errorf("legacy metrics key %s 在 apiType=%s 的 circuit_states 中存在待迁移记录，但映射到多个 identity target: primary=%+v conflicts=%+v", row.MetricsKey, row.APIType, candidate.Primary, candidate.Conflicts)
			}
			if row.MetricsKey != candidate.Primary.MetricsKey || row.BaseURL != candidate.Primary.BaseURL {
				migratedCount++
			}
			row.MetricsKey = candidate.Primary.MetricsKey
			row.BaseURL = candidate.Primary.BaseURL
		}

		mergedKey := row.APIType + "|" + row.MetricsKey
		if existing, exists := merged[mergedKey]; exists {
			mergePersistedCircuitState(existing, &row)
			mergedCount++
			continue
		}
		copy := row
		merged[mergedKey] = &copy
	}
	if err := rows.Err(); err != nil {
		return migratedCount, mergedCount, fmt.Errorf("遍历 circuit_states 失败: %w", err)
	}

	if _, err := tx.Exec("DELETE FROM circuit_states"); err != nil {
		return migratedCount, mergedCount, fmt.Errorf("清空 circuit_states 失败: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO circuit_states (
			metrics_key, api_type, base_url, key_mask, circuit_state,
			circuit_opened_at, half_open_at, next_retry_at,
			backoff_level, half_open_successes, consecutive_failures, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return migratedCount, mergedCount, fmt.Errorf("准备写入 circuit_states 失败: %w", err)
	}
	defer errutil.IgnoreDeferred(stmt.Close)

	for _, row := range merged {
		var openedAt any
		if row.CircuitOpenedAt != nil {
			openedAt = row.CircuitOpenedAt.Unix()
		}
		var halfOpenAt any
		if row.HalfOpenAt != nil {
			halfOpenAt = row.HalfOpenAt.Unix()
		}
		var nextRetryAt any
		if row.NextRetryAt != nil {
			nextRetryAt = row.NextRetryAt.Unix()
		}
		if _, err := stmt.Exec(
			row.MetricsKey,
			row.APIType,
			row.BaseURL,
			row.KeyMask,
			row.CircuitState,
			openedAt,
			halfOpenAt,
			nextRetryAt,
			row.BackoffLevel,
			row.HalfOpenSuccesses,
			row.ConsecutiveFailures,
			time.Now().Unix(),
		); err != nil {
			return migratedCount, mergedCount, fmt.Errorf("重建 circuit_states 失败(metricsKey=%s, apiType=%s): %w", row.MetricsKey, row.APIType, err)
		}
	}

	return migratedCount, mergedCount, nil
}

func mergePersistedCircuitState(dst, src *persistedCircuitStateRow) {
	if dst == nil || src == nil {
		return
	}
	if circuitStateSeverity(src.CircuitState) > circuitStateSeverity(dst.CircuitState) {
		dst.CircuitState = src.CircuitState
	}
	if src.BackoffLevel > dst.BackoffLevel {
		dst.BackoffLevel = src.BackoffLevel
	}
	if src.HalfOpenSuccesses > dst.HalfOpenSuccesses {
		dst.HalfOpenSuccesses = src.HalfOpenSuccesses
	}
	if src.ConsecutiveFailures > dst.ConsecutiveFailures {
		dst.ConsecutiveFailures = src.ConsecutiveFailures
	}
	if src.UpdatedAt > dst.UpdatedAt {
		dst.UpdatedAt = src.UpdatedAt
		if src.CircuitOpenedAt != nil {
			dst.CircuitOpenedAt = src.CircuitOpenedAt
		}
		if src.HalfOpenAt != nil {
			dst.HalfOpenAt = src.HalfOpenAt
		}
		if src.NextRetryAt != nil {
			dst.NextRetryAt = src.NextRetryAt
		}
	}
}

func circuitStateSeverity(state string) int {
	switch state {
	case "open":
		return 3
	case "half_open":
		return 2
	default:
		return 1
	}
}

// AddRecord 添加记录到写入缓冲区（非阻塞）
func (s *SQLiteStore) AddRecord(record PersistentRecord) {
	s.bufferMu.Lock()
	if s.closed {
		s.bufferMu.Unlock()
		return // 已关闭，忽略新记录
	}
	s.writeBuffer = append(s.writeBuffer, record)
	shouldFlush := len(s.writeBuffer) >= s.batchSize
	s.bufferMu.Unlock()

	// 使用原子标记确保同一时间只有一个 flush goroutine 被调度
	// 避免高并发下产生大量 goroutine 排队等待 flushMu
	if shouldFlush && s.flushing.CompareAndSwap(false, true) {
		s.asyncFlushWg.Add(1)
		go func() {
			defer s.asyncFlushWg.Done()
			defer s.flushing.Store(false)
			// 获取 flush 锁，与 DeleteRecordsByMetricsKeys 串行化
			s.flushMu.Lock()
			s.flush()
			s.flushMu.Unlock()
		}()
	}
}

// flush 刷新缓冲区到数据库
func (s *SQLiteStore) flush() {
	s.bufferMu.Lock()
	if len(s.writeBuffer) == 0 {
		s.bufferMu.Unlock()
		return
	}

	// 取出缓冲区数据
	records := s.writeBuffer
	s.writeBuffer = make([]PersistentRecord, 0, s.batchSize)
	s.bufferMu.Unlock()

	// 批量写入
	if err := s.batchInsertRecords(records); err != nil {
		log.Printf("[SQLite-Flush] 警告: 批量写入指标记录失败: %v", err)
		s.requeueRecords(records, "[SQLite-Flush]")
	}
}

// batchInsertRecords 批量插入记录
func (s *SQLiteStore) batchInsertRecords(records []PersistentRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer errutil.IgnoreDeferred(tx.Rollback)
	stmt, err := tx.Prepare(`
		INSERT INTO request_records
		(metrics_key, base_url, key_mask, timestamp, success, failure_class,
		 input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, api_type, model, proxy_key_mask,
		 channel_uid, route_model, key_uid, subscription_uid, exchange_snapshot_version,
		 list_cost_usd, effective_cost_usd, effective_cost_available, effective_cost_reason, consumption_policy)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer errutil.IgnoreDeferred(stmt.Close)
	for _, r := range records {
		success, effectiveAvailable := 0, 0
		if r.Success {
			success = 1
		}
		if r.EffectiveCostAvailable {
			effectiveAvailable = 1
		}
		_, err := stmt.Exec(
			r.MetricsKey, r.BaseURL, r.KeyMask, r.Timestamp.Unix(), success, string(r.FailureClass),
			r.InputTokens, r.OutputTokens, r.CacheCreationTokens, r.CacheReadTokens, r.APIType, r.Model, r.ProxyKeyMask,
			r.ChannelUID, r.RouteModel, r.KeyUID, r.SubscriptionUID, r.ExchangeSnapshotVersion,
			r.ListCostUSD, r.EffectiveCostUSD, effectiveAvailable, r.EffectiveCostReason, r.ConsumptionPolicy,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadRecords 加载指定时间范围内的记录
func (s *SQLiteStore) LoadRecords(since time.Time, apiType string) ([]PersistentRecord, error) {
	rows, err := s.db.Query(`
		SELECT metrics_key, base_url, key_mask, timestamp, success, failure_class,
		       input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, model, proxy_key_mask, channel_uid, route_model,
		       key_uid, subscription_uid, exchange_snapshot_version, list_cost_usd, effective_cost_usd,
		       effective_cost_available, effective_cost_reason, consumption_policy
		FROM request_records
		WHERE timestamp >= ? AND api_type = ?
		ORDER BY timestamp ASC
	`, since.Unix(), apiType)
	if err != nil {
		return nil, err
	}
	defer errutil.IgnoreDeferred(rows.Close)
	var records []PersistentRecord
	for rows.Next() {
		var r PersistentRecord
		var ts int64
		var success int
		var failureClass string
		var keyUID, subscriptionUID, effectiveReason, consumptionPolicy sql.NullString
		var version sql.NullInt64
		var listCost, effectiveCost sql.NullFloat64
		var effectiveAvailable sql.NullInt64
		err := rows.Scan(
			&r.MetricsKey, &r.BaseURL, &r.KeyMask, &ts, &success, &failureClass,
			&r.InputTokens, &r.OutputTokens, &r.CacheCreationTokens, &r.CacheReadTokens, &r.Model, &r.ProxyKeyMask,
			&r.ChannelUID, &r.RouteModel, &keyUID, &subscriptionUID, &version, &listCost, &effectiveCost,
			&effectiveAvailable, &effectiveReason, &consumptionPolicy,
		)
		if err != nil {
			return nil, err
		}
		r.Timestamp = time.Unix(ts, 0)
		r.Success = success == 1
		r.FailureClass = FailureClass(failureClass)
		r.APIType = apiType
		r.KeyUID = keyUID.String
		r.SubscriptionUID = subscriptionUID.String
		r.ConsumptionPolicy = consumptionPolicy.String
		if version.Valid && version.Int64 > 0 {
			r.ExchangeSnapshotVersion = uint64(version.Int64)
		}
		if listCost.Valid {
			r.ListCostUSD = listCost.Float64
		}
		if effectiveCost.Valid {
			r.EffectiveCostUSD = effectiveCost.Float64
		}
		r.EffectiveCostAvailable = effectiveAvailable.Valid && effectiveAvailable.Int64 == 1
		r.EffectiveCostReason = effectiveReason.String
		records = append(records, r)
	}
	return records, rows.Err()
}

// LoadCircuitStates 加载指定 API 类型的 breaker 状态。
func (s *SQLiteStore) LoadCircuitStates(apiType string) (map[string]*PersistentCircuitState, error) {
	rows, err := s.db.Query(`
		SELECT metrics_key, base_url, key_mask, circuit_state,
		       circuit_opened_at, half_open_at, next_retry_at,
		       backoff_level, half_open_successes, consecutive_failures
		FROM circuit_states
		WHERE api_type = ?
	`, apiType)
	if err != nil {
		return nil, err
	}
	defer errutil.IgnoreDeferred(rows.Close)

	result := make(map[string]*PersistentCircuitState)
	for rows.Next() {
		var state PersistentCircuitState
		var openedAt, halfOpenAt, nextRetryAt sql.NullInt64
		if err := rows.Scan(
			&state.MetricsKey,
			&state.BaseURL,
			&state.KeyMask,
			&state.CircuitState,
			&openedAt,
			&halfOpenAt,
			&nextRetryAt,
			&state.BackoffLevel,
			&state.HalfOpenSuccesses,
			&state.ConsecutiveFailures,
		); err != nil {
			return nil, err
		}
		state.APIType = apiType
		if openedAt.Valid {
			t := time.Unix(openedAt.Int64, 0)
			state.CircuitOpenedAt = &t
		}
		if halfOpenAt.Valid {
			t := time.Unix(halfOpenAt.Int64, 0)
			state.HalfOpenAt = &t
		}
		if nextRetryAt.Valid {
			t := time.Unix(nextRetryAt.Int64, 0)
			state.NextRetryAt = &t
		}
		result[state.MetricsKey] = &state
	}

	return result, rows.Err()
}

// UpsertCircuitState 写入或更新 breaker 状态。
func (s *SQLiteStore) UpsertCircuitState(state PersistentCircuitState) error {
	var openedAt any
	if state.CircuitOpenedAt != nil {
		openedAt = state.CircuitOpenedAt.Unix()
	}
	var halfOpenAt any
	if state.HalfOpenAt != nil {
		halfOpenAt = state.HalfOpenAt.Unix()
	}
	var nextRetryAt any
	if state.NextRetryAt != nil {
		nextRetryAt = state.NextRetryAt.Unix()
	}
	_, err := s.db.Exec(`
		INSERT INTO circuit_states (
			metrics_key, api_type, base_url, key_mask, circuit_state,
			circuit_opened_at, half_open_at, next_retry_at,
			backoff_level, half_open_successes, consecutive_failures, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(metrics_key, api_type) DO UPDATE SET
			base_url = excluded.base_url,
			key_mask = excluded.key_mask,
			circuit_state = excluded.circuit_state,
			circuit_opened_at = excluded.circuit_opened_at,
			half_open_at = excluded.half_open_at,
			next_retry_at = excluded.next_retry_at,
			backoff_level = excluded.backoff_level,
			half_open_successes = excluded.half_open_successes,
			consecutive_failures = excluded.consecutive_failures,
			updated_at = excluded.updated_at
	`, state.MetricsKey, state.APIType, state.BaseURL, state.KeyMask, state.CircuitState, openedAt, halfOpenAt, nextRetryAt, state.BackoffLevel, state.HalfOpenSuccesses, state.ConsecutiveFailures, time.Now().Unix())
	return err
}

// LoadLatestTimestamps 从全量历史记录中查询每个 key 的最后成功/失败时间
func (s *SQLiteStore) LoadLatestTimestamps(apiType string) (map[string]*KeyLatestTimestamps, error) {
	rows, err := s.db.Query(`
		SELECT
			metrics_key,
			base_url,
			key_mask,
			MAX(CASE WHEN success = 1 THEN timestamp END) AS last_success,
			MAX(CASE WHEN success = 0 THEN timestamp END) AS last_failure
		FROM request_records
		WHERE api_type = ?
		GROUP BY metrics_key
	`, apiType)
	if err != nil {
		return nil, err
	}
	defer errutil.IgnoreDeferred(rows.Close)

	result := make(map[string]*KeyLatestTimestamps)
	for rows.Next() {
		var metricsKey, baseURL, keyMask string
		var lastSuccessTS, lastFailureTS sql.NullInt64

		if err := rows.Scan(&metricsKey, &baseURL, &keyMask, &lastSuccessTS, &lastFailureTS); err != nil {
			return nil, err
		}

		kt := &KeyLatestTimestamps{
			BaseURL: baseURL,
			KeyMask: keyMask,
		}
		if lastSuccessTS.Valid {
			t := time.Unix(lastSuccessTS.Int64, 0)
			kt.LastSuccessAt = &t
		}
		if lastFailureTS.Valid {
			t := time.Unix(lastFailureTS.Int64, 0)
			kt.LastFailureAt = &t
		}
		result[metricsKey] = kt
	}

	return result, rows.Err()
}

// CleanupOldRecords 清理过期数据
func (s *SQLiteStore) CleanupOldRecords(before time.Time) (int64, error) {
	result, err := s.db.Exec(
		"DELETE FROM request_records WHERE timestamp < ?",
		before.Unix(),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DeleteRecordsByMetricsKeys 按 metrics_key 和 api_type 批量删除记录
// apiType: 接口类型（messages/responses/gemini），避免误删其他接口的数据
func (s *SQLiteStore) DeleteRecordsByMetricsKeys(metricsKeys []string, apiType string) (int64, error) {
	if len(metricsKeys) == 0 {
		return 0, nil
	}

	// 获取 flush 锁，确保删除期间不会有后台 flush 写入新记录
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	// 先刷新缓冲区，确保待删除的记录已写入数据库
	s.flush()

	// 分批删除，避免触发 SQLite 变量上限（默认 999）
	const batchSize = 500
	var totalDeleted int64

	for i := 0; i < len(metricsKeys); i += batchSize {
		end := i + batchSize
		if end > len(metricsKeys) {
			end = len(metricsKeys)
		}
		batch := metricsKeys[i:end]

		// 构建 IN 子句的占位符
		placeholders := make([]string, len(batch))
		args := make([]interface{}, 0, len(batch)+1)
		args = append(args, apiType) // 第一个参数是 api_type
		for j, key := range batch {
			placeholders[j] = "?"
			args = append(args, key)
		}

		query := fmt.Sprintf(
			"DELETE FROM request_records WHERE api_type = ? AND metrics_key IN (%s)",
			strings.Join(placeholders, ","),
		)

		result, err := s.db.Exec(query, args...)
		if err != nil {
			return totalDeleted, fmt.Errorf("batch %d-%d failed: %w", i, end, err)
		}
		affected, _ := result.RowsAffected()
		totalDeleted += affected
	}

	return totalDeleted, nil
}

// DeleteCircuitStatesByMetricsKeys 按 metrics_key 和 api_type 批量删除 breaker 状态。
func (s *SQLiteStore) DeleteCircuitStatesByMetricsKeys(metricsKeys []string, apiType string) (int64, error) {
	if len(metricsKeys) == 0 {
		return 0, nil
	}

	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	const batchSize = 500
	var totalDeleted int64

	for i := 0; i < len(metricsKeys); i += batchSize {
		end := i + batchSize
		if end > len(metricsKeys) {
			end = len(metricsKeys)
		}
		batch := metricsKeys[i:end]
		placeholders := make([]string, len(batch))
		args := make([]interface{}, 0, len(batch)+1)
		args = append(args, apiType)
		for j, key := range batch {
			placeholders[j] = "?"
			args = append(args, key)
		}
		query := fmt.Sprintf(
			"DELETE FROM circuit_states WHERE api_type = ? AND metrics_key IN (%s)",
			strings.Join(placeholders, ","),
		)
		result, err := s.db.Exec(query, args...)
		if err != nil {
			return totalDeleted, fmt.Errorf("delete circuit states batch %d-%d failed: %w", i, end, err)
		}
		affected, _ := result.RowsAffected()
		totalDeleted += affected
	}

	return totalDeleted, nil
}

// flushLoop 定时刷新循环
func (s *SQLiteStore) flushLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 获取 flush 锁，与 DeleteRecordsByMetricsKeys 串行化
			s.flushMu.Lock()
			s.flush()
			s.flushMu.Unlock()
		case <-s.stopCh:
			// 关闭前最后一次刷新
			s.flushMu.Lock()
			s.flush()
			s.flushMu.Unlock()
			return
		}
	}
}

// cleanupLoop 定期清理循环
func (s *SQLiteStore) cleanupLoop() {
	defer s.wg.Done()

	// 启动时先清理一次
	s.doCleanup()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.doCleanup()
		case <-s.stopCh:
			return
		}
	}
}

// doCleanup 执行清理
func (s *SQLiteStore) doCleanup() {
	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	deleted, err := s.CleanupOldRecords(cutoff)
	if err != nil {
		log.Printf("[SQLite-Cleanup] 警告: 清理过期指标记录失败: %v", err)
	} else if deleted > 0 {
		log.Printf("[SQLite-Cleanup] 已清理 %d 条过期指标记录（超过 %d 天）", deleted, s.retentionDays)
	}
}

// Close 关闭存储
func (s *SQLiteStore) Close() error {
	// 标记为已关闭，阻止新记录
	s.bufferMu.Lock()
	s.closed = true
	s.bufferMu.Unlock()

	// 停止后台循环（flushLoop 会在退出前执行最后一次 flush）
	close(s.stopCh)
	s.wg.Wait()

	// 等待所有 AddRecord 触发的异步 flush goroutine 完成
	s.asyncFlushWg.Wait()

	return s.db.Close()
}

// GetRecordCount 获取记录总数（用于调试）
func (s *SQLiteStore) GetRecordCount() (int64, error) {
	var count int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM request_records").Scan(&count)
	return count, err
}

// AggregatedBucket 聚合时间桶
type AggregatedBucket struct {
	Timestamp           time.Time
	TotalRequests       int64
	SuccessCount        int64
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
}

// ModelAggregatedBucket 按模型聚合的时间桶。
type ModelAggregatedBucket struct {
	Model               string
	Timestamp           time.Time
	TotalRequests       int64
	SuccessCount        int64
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
}

// QueryAggregatedHistory 从 SQLite 查询聚合历史数据
// 按指定时间间隔聚合，可选按 apiType、metricsKey、baseURL 过滤
func (s *SQLiteStore) QueryAggregatedHistory(apiType string, since time.Time, intervalSeconds int64, metricsKey string, baseURL string) ([]AggregatedBucket, error) {
	// 先等待在途 flush 完成，再刷新当前缓冲区，确保查询视图尽可能完整。
	// 这里串行化查询前刷新，可避免高并发下异步 flush 已取走 writeBuffer
	// 但尚未提交事务时，查询漏读最新数据。
	s.flushMu.Lock()
	s.flushBufferLocked()
	s.flushMu.Unlock()

	query := `
		SELECT
			(timestamp / ?) * ? AS bucket,
			COUNT(*) AS total,
			SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END) AS success_count,
			SUM(input_tokens) AS input_tokens,
			SUM(output_tokens) AS output_tokens,
			SUM(cache_creation_tokens) AS cache_creation_tokens,
			SUM(cache_read_tokens) AS cache_read_tokens
		FROM request_records
		WHERE api_type = ? AND timestamp >= ?`

	args := []any{intervalSeconds, intervalSeconds, apiType, since.Unix()}

	if metricsKey != "" {
		query += " AND metrics_key = ?"
		args = append(args, metricsKey)
	}
	if baseURL != "" {
		query += " AND base_url = ?"
		args = append(args, baseURL)
	}

	query += " GROUP BY bucket ORDER BY bucket"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询聚合历史失败: %w", err)
	}
	defer errutil.IgnoreDeferred(rows.Close)

	var results []AggregatedBucket
	for rows.Next() {
		var bucket int64
		var b AggregatedBucket
		if err := rows.Scan(&bucket, &b.TotalRequests, &b.SuccessCount, &b.InputTokens, &b.OutputTokens, &b.CacheCreationTokens, &b.CacheReadTokens); err != nil {
			return nil, fmt.Errorf("扫描聚合结果失败: %w", err)
		}
		b.Timestamp = time.Unix(bucket, 0)
		results = append(results, b)
	}
	return results, rows.Err()
}

// QueryModelAggregatedHistory 从 SQLite 查询按模型聚合的历史数据。
func (s *SQLiteStore) QueryModelAggregatedHistory(apiType string, since time.Time, intervalSeconds int64, metricsKey string, baseURL string) ([]ModelAggregatedBucket, error) {
	// 先等待在途 flush 完成，再刷新当前缓冲区，确保查询视图尽可能完整。
	s.flushMu.Lock()
	s.flushBufferLocked()
	s.flushMu.Unlock()

	query := `
		SELECT
			model,
			(timestamp / ?) * ? AS bucket,
			COUNT(*) AS total,
			SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END) AS success_count,
			SUM(input_tokens) AS input_tokens,
			SUM(output_tokens) AS output_tokens,
			SUM(cache_creation_tokens) AS cache_creation_tokens,
			SUM(cache_read_tokens) AS cache_read_tokens
		FROM request_records
		WHERE api_type = ? AND timestamp >= ? AND model <> ''`

	args := []any{intervalSeconds, intervalSeconds, apiType, since.Unix()}

	if metricsKey != "" {
		query += " AND metrics_key = ?"
		args = append(args, metricsKey)
	}
	if baseURL != "" {
		query += " AND base_url = ?"
		args = append(args, baseURL)
	}

	query += " GROUP BY model, bucket ORDER BY model, bucket"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询模型聚合历史失败: %w", err)
	}
	defer errutil.IgnoreDeferred(rows.Close)

	var results []ModelAggregatedBucket
	for rows.Next() {
		var bucket int64
		var b ModelAggregatedBucket
		if err := rows.Scan(&b.Model, &bucket, &b.TotalRequests, &b.SuccessCount, &b.InputTokens, &b.OutputTokens, &b.CacheCreationTokens, &b.CacheReadTokens); err != nil {
			return nil, fmt.Errorf("扫描模型聚合结果失败: %w", err)
		}
		b.Timestamp = time.Unix(bucket, 0)
		results = append(results, b)
	}
	return results, rows.Err()
}

// flushBuffer 手动刷新写入缓冲区（查询前调用，确保数据完整性）

// flushBufferLocked 在调用方已持有 flushMu 时刷新写入缓冲区
func (s *SQLiteStore) flushBufferLocked() {
	s.bufferMu.Lock()
	records := make([]PersistentRecord, len(s.writeBuffer))
	copy(records, s.writeBuffer)
	s.writeBuffer = s.writeBuffer[:0]
	s.bufferMu.Unlock()

	if len(records) > 0 {
		if err := s.batchInsertRecords(records); err != nil {
			log.Printf("[SQLite-Flush] 手动刷新失败: %v", err)
			s.requeueRecords(records, "[SQLite-Flush]")
		}
	}
}

func (s *SQLiteStore) requeueRecords(records []PersistentRecord, logPrefix string) {
	if len(records) == 0 {
		return
	}

	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()

	if len(s.writeBuffer) < s.batchSize*10 {
		s.writeBuffer = append(records, s.writeBuffer...)
		return
	}

	log.Printf("%s 警告: 写入缓冲区已满，丢弃 %d 条记录", logPrefix, len(records))
}

// CostReportRow 成本报表聚合行
type CostReportRow struct {
	GroupKey                  string  `json:"groupKey"`
	TotalRequests             int64   `json:"totalRequests"`
	SuccessCount              int64   `json:"successCount"`
	InputTokens               int64   `json:"inputTokens"`
	OutputTokens              int64   `json:"outputTokens"`
	CacheCreationTokens       int64   `json:"cacheCreationTokens"`
	CacheReadTokens           int64   `json:"cacheReadTokens"`
	ListCostUSD               float64 `json:"listCostUSD"`
	EffectiveCostUSD          float64 `json:"effectiveCostUSD"`
	EffectivePricedCount      int64   `json:"effectivePricedCount"`
	EffectiveUnavailableCount int64   `json:"effectiveUnavailableCount"`
	// 成本 reason 分类计数，用于区分已确认零成本与成本证据缺失。
	ZeroCostCount        int64 `json:"zeroCostCount"`
	ConfiguredMultiplierCount int64 `json:"configuredMultiplierCount"`
	SubscriptionCostCount     int64 `json:"subscriptionCostCount"`
	UnpricedCostCount         int64 `json:"unpricedCostCount"`
}

// QueryCostReport 按指定维度聚合成本数据。
// groupBy 支持 "user"（按 proxy_key_mask）、"model"（按 model）、"key"（按 key_mask）。
// 返回聚合行列表，按总请求数降序排列。
func (s *SQLiteStore) QueryCostReport(apiType string, since time.Time, groupBy string) ([]CostReportRow, error) {
	s.flushMu.Lock()
	s.flushBufferLocked()
	s.flushMu.Unlock()
	var groupExpr string
	switch groupBy {
	case "user":
		groupExpr = "proxy_key_mask"
	case "model":
		groupExpr = "model"
	case "key":
		groupExpr = "key_mask"
	default:
		groupExpr = "proxy_key_mask"
	}
	query := fmt.Sprintf(`
		SELECT %s AS group_key,
			COUNT(*), SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END),
			SUM(input_tokens), SUM(output_tokens), SUM(cache_creation_tokens), SUM(cache_read_tokens),
			COALESCE(SUM(list_cost_usd), 0), COALESCE(SUM(CASE WHEN effective_cost_available = 1 THEN effective_cost_usd ELSE 0 END), 0),
			SUM(CASE WHEN effective_cost_available = 1 THEN 1 ELSE 0 END),
			SUM(CASE WHEN effective_cost_available IS NULL OR effective_cost_available <> 1 THEN 1 ELSE 0 END),
			SUM(CASE WHEN effective_cost_available = 1 AND effective_cost_reason = 'manual_zero_cost' THEN 1 ELSE 0 END),
			SUM(CASE WHEN effective_cost_available = 1 AND effective_cost_reason = 'configured_group_multiplier' THEN 1 ELSE 0 END),
			SUM(CASE WHEN effective_cost_available = 1 AND effective_cost_reason LIKE 'subscription_%%' THEN 1 ELSE 0 END),
			SUM(CASE WHEN effective_cost_available = 1 AND effective_cost_reason NOT IN ('manual_zero_cost', 'configured_group_multiplier') AND effective_cost_reason NOT LIKE 'subscription_%%' THEN 1 ELSE 0 END)
		FROM request_records
		WHERE api_type = ? AND timestamp >= ?
		GROUP BY group_key HAVING group_key <> '' ORDER BY COUNT(*) DESC
	`, groupExpr)
	rows, err := s.db.Query(query, apiType, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("查询成本报表失败: %w", err)
	}
	defer errutil.IgnoreDeferred(rows.Close)
	var results []CostReportRow
	for rows.Next() {
		var row CostReportRow
		if err := rows.Scan(&row.GroupKey, &row.TotalRequests, &row.SuccessCount,
			&row.InputTokens, &row.OutputTokens, &row.CacheCreationTokens, &row.CacheReadTokens,
			&row.ListCostUSD, &row.EffectiveCostUSD, &row.EffectivePricedCount, &row.EffectiveUnavailableCount,
			&row.ZeroCostCount, &row.ConfiguredMultiplierCount, &row.SubscriptionCostCount, &row.UnpricedCostCount); err != nil {
			return nil, fmt.Errorf("扫描成本报表结果失败: %w", err)
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// ModelCostBreakdownRow 按模型分组的 token 聚合行（用于成本计算）
type ModelCostBreakdownRow struct {
	Model               string `json:"model"`
	InputTokens         int64  `json:"inputTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	CacheCreationTokens int64  `json:"cacheCreationTokens"`
	CacheReadTokens     int64  `json:"cacheReadTokens"`
}

// QueryModelCostBreakdown 按指定维度筛选后，再按 model 分组返回 token 明细。
// 用于 handler 层计算 ListCostUSD / EffectiveCostUSD（需要逐模型定价）。
// filterGroupKey 按 groupBy 对应列精确匹配；空字符串表示不过滤。
func (s *SQLiteStore) QueryModelCostBreakdown(apiType string, since time.Time, groupBy string, filterGroupKey string) ([]ModelCostBreakdownRow, error) {
	s.flushMu.Lock()
	s.flushBufferLocked()
	s.flushMu.Unlock()

	var groupExpr string
	switch groupBy {
	case "user":
		groupExpr = "proxy_key_mask"
	case "model":
		groupExpr = "model"
	case "key":
		groupExpr = "key_mask"
	default:
		groupExpr = "proxy_key_mask"
	}

	var whereExtra string
	var args []interface{}
	args = append(args, apiType, since.Unix())
	if filterGroupKey != "" {
		whereExtra = fmt.Sprintf(" AND %s = ?", groupExpr)
		args = append(args, filterGroupKey)
	}

	query := fmt.Sprintf(`
		SELECT
			model,
			SUM(input_tokens) AS input_tokens,
			SUM(output_tokens) AS output_tokens,
			SUM(cache_creation_tokens) AS cache_creation_tokens,
			SUM(cache_read_tokens) AS cache_read_tokens
		FROM request_records
		WHERE api_type = ? AND timestamp >= ?%s
		GROUP BY model
		HAVING model <> ''
	`, whereExtra)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询模型成本明细失败: %w", err)
	}
	defer errutil.IgnoreDeferred(rows.Close)

	var results []ModelCostBreakdownRow
	for rows.Next() {
		var row ModelCostBreakdownRow
		if err := rows.Scan(&row.Model, &row.InputTokens, &row.OutputTokens, &row.CacheCreationTokens, &row.CacheReadTokens); err != nil {
			return nil, fmt.Errorf("扫描模型成本明细失败: %w", err)
		}
		results = append(results, row)
	}
	return results, rows.Err()
}
