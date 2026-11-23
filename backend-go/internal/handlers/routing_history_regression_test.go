package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

func TestRoutingHistoryFullRetentionCursorKeepsFifteenMinuteLifetime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []struct {
		name       string
		persistent bool
	}{{"memory", false}, {"persistent", true}} {
		t.Run(mode.name, func(t *testing.T) {
			var db *metrics.SQLiteStore
			if mode.persistent {
				var err error
				db, err = metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 30})
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
			}
			store, err := metrics.NewRoutingStoreWithRetention(db, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			for _, tc := range []struct {
				name, duration string
				age, excess    time.Duration
				want           int
			}{
				{"retained-after-two-minutes", "retained", 2 * time.Minute, 0, 200},
				{"retained-near-expiry", "retained", 14 * time.Minute, 0, 200},
				{"24h-after-two-minutes", "24h", 2 * time.Minute, 0, 200},
				{"expired", "retained", 16 * time.Minute, 0, 410},
				{"future-issued-at", "retained", -3 * time.Minute, 0, 410},
				{"over-retention", "retained", 0, time.Hour, 400},
			} {
				t.Run(tc.name, func(t *testing.T) {
					issued := time.Now().Add(-tc.age)
					duration := store.MaxHistoryAge()
					if tc.duration == "24h" {
						duration = 24 * time.Hour
					}
					hash := sha256.Sum256([]byte(strings.Join([]string{"chat", tc.duration, "", "", "", "", "", ""}, "\x00")))
					cursor := routingHistoryCursor{From: issued.Add(-duration - tc.excess), To: issued, ReceivedAt: issued.Add(-time.Hour), ID: "last-item", Fingerprint: hex.EncodeToString(hash[:]), IssuedAt: issued}
					payload, err := json.Marshal(cursor)
					if err != nil {
						t.Fatal(err)
					}
					response := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(response)
					c.Request = httptest.NewRequest("GET", "/history?duration="+tc.duration+"&cursor="+url.QueryEscape(base64.RawURLEncoding.EncodeToString(payload)), nil)
					_, _, _, ok := routingHistoryQuery(c, "chat", store)
					if ok != (tc.want == 200) || response.Code != tc.want {
						t.Fatalf("code=%d ok=%v body=%s", response.Code, ok, response.Body.String())
					}
				})
			}
		})
	}
}
