package handlers

import (
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

type DiagnosticVersion struct {
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	BuildTime string `json:"buildTime"`
}

// GetDiagnostics 仅返回明确定义的运维字段，由现有管理鉴权保护。
func GetDiagnostics(env *config.EnvConfig, store *metrics.SQLiteStore, routing *metrics.RoutingStore, operations *metrics.KeyOperationStore, version DiagnosticVersion) gin.HandlerFunc {
	return func(c *gin.Context) {
		status := store.Diagnostics()
		status.RetentionDays = env.MetricsRetentionDays
		c.JSON(200, gin.H{"generatedAt": time.Now(), "version": version, "persistenceConfigured": env.MetricsPersistenceEnabled,
			"metrics": status, "requestHistory": routing.Diagnostics(), "keyOperations": operations.Diagnostics()})
	}
}
