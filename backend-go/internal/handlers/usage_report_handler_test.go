package handlers

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

func TestUsageReportCSVChannelSelectionAndEscaping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	cfgManager, err := config.NewConfigManager(filepath.Join(dir, "config.json"), filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	defer cfgManager.Close()
	upstream := config.UpstreamConfig{Name: "=SUM(1,1)", BaseURL: "https://example.com", APIKeys: []string{"sk-test"}, ServiceType: "claude"}
	if err := cfgManager.AddUpstream(upstream); err != nil {
		t.Fatal(err)
	}
	store, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{DBPath: filepath.Join(dir, "metrics.db"), RetentionDays: 366})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key := metrics.GenerateMetricsIdentityKey("https://example.com", "sk-test", "claude")
	for _, item := range []struct{ key, model string }{{key, "=MODEL"}, {"deleted-key", "old-model"}} {
		store.AddRecord(metrics.PersistentRecord{MetricsKey: item.key, BaseURL: "https://example.com", APIType: "messages",
			Timestamp: time.Date(2026, 10, 2, 12, 30, 0, 0, time.UTC), Success: true, InputTokens: 120,
			OutputTokens: 60, Model: item.model})
	}
	router := gin.New()
	router.GET("/report", GetUsageReportCSV(store, cfgManager))
	for _, test := range []struct {
		name, channel string
		wantRows      int
	}{
		{"all", "all", 3},
		{"selected", "messages:0", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/report?month=2026-10&timezone=UTC&channel="+test.channel, nil)
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			body := bytes.TrimPrefix(response.Body.Bytes(), []byte("\xef\xbb\xbf"))
			rows, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != test.wantRows {
				t.Fatalf("rows = %v, want %d", rows, test.wantRows)
			}
			if test.channel == "messages:0" && (rows[1][4] != "'=SUM(1,1)" || rows[1][6] != "'=MODEL" || rows[1][7] != "1" || rows[1][15] != "0.0167") {
				t.Fatalf("unexpected selected row: %v", rows[1])
			}
		})
	}
}

func TestUsageReportRejectsInvalidMonth(t *testing.T) {
	router := gin.New()
	router.GET("/report", GetUsageReportCSV(&metrics.SQLiteStore{}, nil))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/report?month=2026-13", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}
