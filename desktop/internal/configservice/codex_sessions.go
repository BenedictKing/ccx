package configservice

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func (s *Service) MigrateCodexSessions(req MigrateCodexSessionsRequest) (MigrateCodexSessionsResult, error) {
	targetProvider, err := resolveCodexSessionModelProvider(req)
	result := MigrateCodexSessionsResult{TargetProvider: targetProvider}
	if err != nil {
		return result, err
	}
	for _, dir := range []string{s.codexSessionsDir(), s.codexArchivedSessionsDir()} {
		if err := s.migrateCodexSessionDir(dir, targetProvider, &result); err != nil {
			return result, err
		}
	}
	s.migrateCodexStateDB(targetProvider, &result)
	return result, nil
}

func resolveCodexSessionModelProvider(req MigrateCodexSessionsRequest) (string, error) {
	provider := normalizeCodexProvider(req.Provider)
	mode := strings.TrimSpace(req.Mode)
	if mode != "plugin" {
		mode = "quick"
	}
	switch provider {
	case ProviderOpenAI:
		return ProviderOpenAI, nil
	case ProviderCCX:
		if mode == "plugin" {
			return ProviderCCX, nil
		}
		return ProviderOpenAI, nil
	case ProviderDashScope, ProviderOpenCodeZen, ProviderOpenCodeGo, ProviderXFyun:
		if mode == "plugin" {
			return provider, nil
		}
		return ProviderOpenAI, nil
	default:
		return "", fmt.Errorf("不支持的 Codex provider: %s", req.Provider)
	}
}

func (s *Service) migrateCodexSessionDir(dir, targetProvider string, result *MigrateCodexSessionsResult) error {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			result.FailedFiles++
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".jsonl") {
			return nil
		}
		result.TotalFiles++
		migrated, err := migrateCodexSessionFile(path, targetProvider)
		if err != nil {
			result.FailedFiles++
			return nil
		}
		if migrated {
			result.MigratedFiles++
		} else {
			result.SkippedFiles++
		}
		return nil
	})
}

func migrateCodexSessionFile(path, targetProvider string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	lineEnd := bytes.IndexByte(content, '\n')
	firstLine := content
	rest := []byte(nil)
	if lineEnd >= 0 {
		firstLine = content[:lineEnd]
		rest = content[lineEnd:]
	}
	lineSuffix := []byte(nil)
	if bytes.HasSuffix(firstLine, []byte("\r")) {
		firstLine = firstLine[:len(firstLine)-1]
		lineSuffix = []byte("\r")
	}
	var meta map[string]any
	if err := json.Unmarshal(firstLine, &meta); err != nil {
		return false, nil
	}
	if typ, ok := meta["type"].(string); !ok || typ != "session_meta" {
		return false, nil
	}
	payload, ok := meta["payload"].(map[string]any)
	if !ok {
		return false, nil
	}
	currentProvider, ok := payload["model_provider"].(string)
	if !ok || currentProvider == targetProvider {
		return false, nil
	}
	payload["model_provider"] = targetProvider
	updatedFirstLine, err := json.Marshal(meta)
	if err != nil {
		return false, err
	}
	updated := make([]byte, 0, len(updatedFirstLine)+len(lineSuffix)+len(rest))
	updated = append(updated, updatedFirstLine...)
	updated = append(updated, lineSuffix...)
	updated = append(updated, rest...)
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return true, writeBytesAtomicWithMode(path, updated, mode)
}

func (s *Service) migrateCodexStateDB(targetProvider string, result *MigrateCodexSessionsResult) {
	path := s.codexStateDBPath()
	if _, err := os.Stat(path); err != nil {
		result.SQLiteSkipped = true
		if !os.IsNotExist(err) {
			result.SQLiteError = err.Error()
		}
		return
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		result.SQLiteSkipped = true
		result.SQLiteError = err.Error()
		return
	}
	defer db.Close()
	_, _ = db.Exec("PRAGMA busy_timeout = 500")
	columns, err := readCodexThreadsColumns(db)
	if err != nil {
		result.SQLiteSkipped = true
		result.SQLiteError = err.Error()
		return
	}
	sqlText, usesProvider := buildCodexThreadsMigrationSQL(columns)
	if sqlText == "" {
		result.SQLiteSkipped = true
		return
	}
	var updateResult sql.Result
	if usesProvider {
		updateResult, err = db.Exec(sqlText, targetProvider)
	} else {
		updateResult, err = db.Exec(sqlText)
	}
	if err != nil {
		result.SQLiteSkipped = true
		result.SQLiteError = err.Error()
		return
	}
	rows, err := updateResult.RowsAffected()
	if err == nil {
		result.SQLiteRowsUpdated = rows
	}
}

func readCodexThreadsColumns(db *sql.DB) (codexThreadsColumns, error) {
	rows, err := db.Query(`PRAGMA table_info(threads)`)
	if err != nil {
		return codexThreadsColumns{}, err
	}
	defer rows.Close()

	names := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return codexThreadsColumns{}, err
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		return codexThreadsColumns{}, err
	}
	return codexThreadsColumns{
		modelProvider:    names["model_provider"],
		preview:          names["preview"],
		firstUserMessage: names["first_user_message"],
		hasUserEvent:     names["has_user_event"],
		threadSource:     names["thread_source"],
		source:           names["source"],
	}, nil
}

func buildCodexThreadsMigrationSQL(columns codexThreadsColumns) (string, bool) {
	assignments := make([]string, 0, 4)
	predicates := make([]string, 0, 4)
	usesProvider := false
	userThreadPredicate := `COALESCE(first_user_message, '') <> ''`
	if columns.source {
		userThreadPredicate = `COALESCE(first_user_message, '') <> '' AND (COALESCE(source, '') = '' OR source = 'user')`
	}
	if columns.modelProvider {
		assignments = append(assignments, `model_provider = ?1`)
		predicates = append(predicates, `model_provider IS NULL OR model_provider <> ?1`)
		usesProvider = true
	}
	if columns.preview && columns.firstUserMessage {
		assignments = append(assignments, fmt.Sprintf(`preview = CASE WHEN COALESCE(preview, '') = '' AND %s THEN first_user_message ELSE preview END`, userThreadPredicate))
		predicates = append(predicates, fmt.Sprintf(`COALESCE(preview, '') = '' AND %s`, userThreadPredicate))
	}
	if columns.hasUserEvent && columns.firstUserMessage {
		assignments = append(assignments, fmt.Sprintf(`has_user_event = CASE WHEN %s THEN 1 ELSE has_user_event END`, userThreadPredicate))
		predicates = append(predicates, fmt.Sprintf(`%s AND COALESCE(has_user_event, 0) <> 1`, userThreadPredicate))
	}
	if columns.threadSource && columns.firstUserMessage {
		assignments = append(assignments, fmt.Sprintf(`thread_source = CASE WHEN COALESCE(thread_source, '') = '' AND %s THEN 'user' ELSE thread_source END`, userThreadPredicate))
		predicates = append(predicates, fmt.Sprintf(`COALESCE(thread_source, '') = '' AND %s`, userThreadPredicate))
	}
	if len(assignments) == 0 || len(predicates) == 0 {
		return "", false
	}
	return fmt.Sprintf(`UPDATE threads SET %s WHERE %s`, strings.Join(assignments, ", "), strings.Join(predicates, " OR ")), usesProvider
}
