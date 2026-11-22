package metrics

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPersistenceDiagnosticsExposeFailuresAndCoverageWithoutSecrets(t *testing.T) {
	db, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "secret-path", "metrics.db"), RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.AddRecord(PersistentRecord{MetricsKey: "safe", BaseURL: "https://secret.example", KeyMask: "masked", Timestamp: time.Now(), APIType: "chat"})
	db.flushMu.Lock()
	db.flush()
	db.flushMu.Unlock()
	db.health.recordWrite(fmt.Errorf("Authorization: secret-value"))
	db.health.recordCleanup(fmt.Errorf("private database path"))
	db.health.recordDrop(2)
	status := db.Diagnostics()
	if status.RecordCount != 1 || status.OldestRecordAt == nil || status.LastWriteAt == nil || status.WriteFailures != 1 || status.CleanupFailures != 1 || status.LastCleanupOK || status.DroppedWrites != 2 {
		t.Fatalf("incomplete diagnostics: %+v", status)
	}
	encoded, _ := json.Marshal(status)
	for _, secret := range []string{"secret.example", "secret-path", "secret-value", "private database path", "Authorization"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("diagnostics contained sensitive details")
		}
	}
	*status.LastWriteAt = time.Time{}
	if db.Diagnostics().LastWriteAt.IsZero() {
		t.Fatal("diagnostic snapshot mutated internal state")
	}
	if _, err := db.db.Exec("DROP TABLE request_records"); err != nil {
		t.Fatal(err)
	}
	if !db.Diagnostics().QueryFailed {
		t.Fatal("unreadable records reported as empty")
	}
}
