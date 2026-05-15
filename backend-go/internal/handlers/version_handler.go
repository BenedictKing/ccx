package handlers

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/updater"
	"github.com/gin-gonic/gin"
	"golang.org/x/mod/semver"
)

var (
	updateInProgress atomic.Bool

	updateStatus   = "idle"
	updateErrorMsg = ""
	updateProgress = 0
	updateMu       sync.RWMutex
)

func setUpdateState(status, errMsg string, progress int) {
	updateMu.Lock()
	updateStatus = status
	updateErrorMsg = errMsg
	updateProgress = progress
	updateMu.Unlock()
}

func getUpdateState() (string, string, int) {
	updateMu.RLock()
	defer updateMu.RUnlock()
	return updateStatus, updateErrorMsg, updateProgress
}

// VersionCheckHandler returns the current version and the latest GitHub release.
func VersionCheckHandler(envCfg *config.EnvConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentVersion := versionString

		info, err := updater.CheckLatest("BenedictKing", "ccx")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"current": gin.H{
					"version":   currentVersion,
					"buildTime": buildTime,
					"gitCommit": gitCommit,
				},
				"latest":    nil,
				"hasUpdate": false,
				"error":     err.Error(),
			})
			return
		}

		hasUpdate := true
		if currentVersion != "v0.0.0-dev" {
			if semver.Compare(currentVersion, info.Version) >= 0 {
				hasUpdate = false
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"current": gin.H{
				"version":   currentVersion,
				"buildTime": buildTime,
				"gitCommit": gitCommit,
			},
			"latest": gin.H{
				"version":     info.Version,
				"publishedAt": info.PublishedAt,
				"url":         info.HTMLURL,
			},
			"hasUpdate": hasUpdate,
		})
	}
}

// VersionStatusHandler returns the current update progress for frontend polling.
func VersionStatusHandler(envCfg *config.EnvConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		status, errMsg, progress := getUpdateState()
		c.JSON(http.StatusOK, gin.H{
			"status":   status,
			"error":    errMsg,
			"progress": progress,
		})
	}
}

// VersionUpdateHandler triggers the update process.
func VersionUpdateHandler(envCfg *config.EnvConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !updateInProgress.CompareAndSwap(false, true) {
			c.JSON(http.StatusConflict, gin.H{
				"status":  "error",
				"message": "更新已在进行中",
			})
			return
		}

		currentVersion := versionString
		log.Printf("[VersionUpdate] starting update from %s", currentVersion)

		c.JSON(http.StatusOK, gin.H{
			"status":  "updating",
			"message": "更新已开始，服务即将重启",
		})
		c.Writer.Flush()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[VersionUpdate] panic: %v", r)
					setUpdateState("failed", fmt.Sprintf("panic: %v", r), 0)
				}
				updateInProgress.Store(false)
			}()

			setUpdateState("downloading", "", 0)

			// Progress ticker: advances during download, self-terminates
			// when status changes away from "downloading".
			go func() {
				ticker := time.NewTicker(800 * time.Millisecond)
				defer ticker.Stop()
				for range ticker.C {
					st, _, p := getUpdateState()
					if st != "downloading" {
						return
					}
					if p < 60 {
						setUpdateState("downloading", "", p+4)
					}
				}
			}()

			onProgress := func(status string, progress int) {
				setUpdateState(status, "", progress)
			}

			err := updater.DoUpdate("BenedictKing", "ccx", currentVersion, onProgress)

			if err != nil {
				log.Printf("[VersionUpdate] update failed: %v", err)
				setUpdateState("failed", err.Error(), 0)
				return
			}

			// DoUpdate returned nil: already at latest version
			log.Println("[VersionUpdate] already up-to-date")
			setUpdateState("idle", "", 100)
		}()
	}
}
