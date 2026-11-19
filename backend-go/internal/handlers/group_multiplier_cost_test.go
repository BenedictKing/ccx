package handlers

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

func TestGroupMultiplierCostHistoryAndCSV(t *testing.T) {
	gin.SetMode(gin.TestMode)
	zero, discount, price := 0.0, 0.5, 2.0
	upstream := config.UpstreamConfig{
		Name: "mixed", BaseURL: "https://relay.example", APIKeys: []string{"sk-free", "sk-discount", "sk-standard"},
		APIKeyConfigs:   []config.APIKeyConfig{{Key: "sk-free", GroupMultiplier: &zero}, {Key: "sk-discount", GroupMultiplier: &discount}},
		DisabledAPIKeys: []config.DisabledKeyInfo{{Key: "sk-disabled-free", Config: &config.APIKeyConfig{Key: "sk-disabled-free", GroupMultiplier: &zero}}},
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"private-model": {Pricing: &config.ModelPricing{Currency: "USD", InputCacheMissPrice: &price}},
		},
	}
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{upstream}, ChatUpstream: []config.UpstreamConfig{upstream},
		ResponsesUpstream: []config.UpstreamConfig{upstream}, GeminiUpstream: []config.UpstreamConfig{upstream},
		ImagesUpstream: []config.UpstreamConfig{upstream}, VectorsUpstream: []config.UpstreamConfig{upstream},
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cm, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	wantByKey := map[string]float64{"sk-free": 0, "sk-discount": 1, "sk-standard": 2, "sk-disabled-free": 0}
	for _, kind := range []scheduler.ChannelKind{
		scheduler.ChannelKindMessages, scheduler.ChannelKindChat, scheduler.ChannelKindResponses,
		scheduler.ChannelKindGemini, scheduler.ChannelKindImages, scheduler.ChannelKindVectors,
	} {
		t.Run(string(kind), func(t *testing.T) {
			store, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 366})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			mm := metrics.NewMetricsManagerWithPersistence(50, 0.5, store, string(kind))
			t.Cleanup(mm.Stop)
			serviceType := scheduler.NormalizedMetricsServiceType(kind, upstream.ServiceType)
			wantByMask := make(map[string]float64)
			for key, want := range wantByKey {
				mm.RecordSuccessWithUsage(upstream.BaseURL, key, serviceType, &types.Usage{InputTokens: 1_000_000}, "private-model")
				wantByMask[truncateKeyMask(utils.MaskAPIKey(key), 8)] = want
			}
			router := gin.New()
			router.GET("/global", GetGlobalStatsHistory(mm, cm, kind))
			router.GET("/keys/:id", getChannelKeyMetricsHistoryWithKind(mm, cm, kind, true, 0))
			router.GET("/csv", GetUsageReportCSV(store, cm))
			// 6h 查询内存，7d 查询真实 SQLite；两条路径都必须逐 Key 计算倍率。
			for _, duration := range []string{"6h", "7d"} {
				t.Run(duration, func(t *testing.T) {
					assertGroupMultiplierHistory(t, router, duration, wantByMask)
				})
			}
			assertGroupMultiplierCSV(t, router, string(kind))
		})
	}
}

func assertGroupMultiplierHistory(t *testing.T, router *gin.Engine, duration string, wantByMask map[string]float64) {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/global?duration="+duration, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("global status=%d body=%s", response.Code, response.Body.String())
	}
	var global metrics.GlobalStatsHistoryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &global); err != nil {
		t.Fatal(err)
	}
	if global.Summary.TotalCostUSD != 3 || global.Summary.TotalRequests != 4 || global.Summary.TotalInputTokens != 4_000_000 {
		t.Fatalf("global summary = %+v, want USD 3 and 4M tokens across 4 requests", global.Summary)
	}
	var pointCost, modelCost float64
	for _, point := range global.DataPoints {
		pointCost += point.CostUSD
	}
	for _, point := range global.ModelDataPoints["private-model"] {
		modelCost += point.CostUSD
	}
	if pointCost != 3 || modelCost != 3 {
		t.Fatalf("global points cost=%v model cost=%v, want 3 each", pointCost, modelCost)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/keys/0?duration="+duration, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("keys status=%d body=%s", response.Code, response.Body.String())
	}
	var keys ChannelKeyMetricsHistoryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	if keys.Summary.TotalCostUSD != 3 || keys.Summary.TotalRequests != 4 || len(keys.Keys) != 4 {
		t.Fatalf("keys summary = %+v, keys=%d", keys.Summary, len(keys.Keys))
	}
	for _, key := range keys.Keys {
		want, exists := wantByMask[key.KeyMask]
		if !exists || key.Model != "private-model" {
			t.Fatalf("unexpected key/model: %s/%s", key.KeyMask, key.Model)
		}
		var cost float64
		var input, requests int64
		for _, point := range key.DataPoints {
			cost += point.CostUSD
			input += point.InputTokens
			requests += point.RequestCount
		}
		if cost != want || input != 1_000_000 || requests != 1 {
			t.Errorf("key %s: cost=%v input=%d requests=%d, want %v/1M/1", key.KeyMask, cost, input, requests, want)
		}
	}
}

func assertGroupMultiplierCSV(t *testing.T, router *gin.Engine, apiType string) {
	t.Helper()
	for _, granularity := range []string{"hour", "day"} {
		response := httptest.NewRecorder()
		url := "/csv?month=" + time.Now().UTC().Format("2006-01") + "&timezone=UTC&channel=" + apiType + ":0&granularity=" + granularity
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, url, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("CSV status=%d body=%s", response.Code, response.Body.String())
		}
		body := bytes.TrimPrefix(response.Body.Bytes(), []byte("\xef\xbb\xbf"))
		rows, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 5 {
			t.Fatalf("CSV rows=%d, want header and 4 keys", len(rows))
		}
		costCounts := make(map[float64]int)
		for _, row := range rows[1:] {
			if row[3] != apiType || row[4] != "mixed" || row[6] != "private-model" || row[11] != "1000000" || row[18] != "matched" {
				t.Fatalf("unexpected CSV row: %v", row)
			}
			cost, err := strconv.ParseFloat(row[17], 64)
			if err != nil {
				t.Fatal(err)
			}
			costCounts[cost]++
		}
		if costCounts[0] != 2 || costCounts[1] != 1 || costCounts[2] != 1 {
			t.Fatalf("CSV costs=%v, want 2 free keys, USD 1 and USD 2", costCounts)
		}
	}
}
