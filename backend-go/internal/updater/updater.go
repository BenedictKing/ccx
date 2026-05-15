// Package updater provides binary self-update for CCX.
//
// Flow: CheckLatest -> DownloadBinary -> preFlightCheck -> selfReplace -> exit.
// Linux and Windows share all logic except the final selfReplace step.
package updater

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	githubAPI        = "https://api.github.com/repos/%s/%s/releases?per_page=10"
	downloadTimeout  = 300 * time.Second
	preFlightTimeout = 5 * time.Second
)

// ReleaseInfo describes a GitHub Release suitable for updating.
type ReleaseInfo struct {
	Version     string `json:"version"`     // e.g. "v2.7.0"
	DownloadURL string `json:"downloadUrl"` // asset download URL for this platform
	PublishedAt string `json:"publishedAt"` // ISO-8601
	HTMLURL     string `json:"htmlUrl"`     // GitHub release page
}

// githubRelease is the raw JSON structure returned by the GitHub API.
type githubRelease struct {
	TagName     string        `json:"tag_name"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt string        `json:"published_at"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// AssetName returns the expected release asset filename for the running OS/arch.
func AssetName() string {
	arch := runtime.GOARCH
	switch runtime.GOOS {
	case "linux":
		return "ccx-linux-" + arch
	case "windows":
		return "ccx-windows-" + arch + ".exe"
	case "darwin":
		return "ccx-darwin-" + arch
	default:
		return ""
	}
}

// CheckLatest queries GitHub for the latest stable release of the given repo.
// It skips prereleases and returns the first stable release found.
func CheckLatest(owner, repo string) (*ReleaseInfo, error) {
	url := fmt.Sprintf(githubAPI, owner, repo)
	log.Printf("[Updater] checking latest release: %s/%s", owner, repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request failed: %w", err)
	}
	req.Header.Set("User-Agent", "ccx/"+runtime.GOOS+"-"+runtime.GOARCH)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return nil, fmt.Errorf("GitHub API rate limit exceeded (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading GitHub response failed: %w", err)
	}

	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("parsing GitHub response failed: %w", err)
	}

	// Find the first stable release with a matching asset.
	myAsset := AssetName()
	if myAsset == "" {
		return nil, fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	for _, rel := range releases {
		if rel.Prerelease || isPrereleaseTag(rel.TagName) {
			continue
		}
		for _, asset := range rel.Assets {
			if asset.Name == myAsset {
				return &ReleaseInfo{
					Version:     rel.TagName,
					DownloadURL: asset.BrowserDownloadURL,
					PublishedAt: rel.PublishedAt,
					HTMLURL:     rel.HTMLURL,
				}, nil
			}
		}
	}
	return nil, fmt.Errorf("no stable release with asset %q found", myAsset)
}

// DownloadBinary streams a binary from downloadURL to destPath.
// It verifies minimum file size and sets executable permission.
func DownloadBinary(downloadURL, destPath string) error {
	log.Printf("[Updater] downloading %s -> %s", downloadURL, destPath)

	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	tmpPath := destPath + ".download" // download to .download then rename atomically
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("creating temp file failed: %w", err)
	}

	written, err := io.Copy(f, resp.Body)
	if err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("download failed: %w", err)
	}
	f.Close()

	if written < 1024*1024 { // refuse files smaller than 1 MB
		os.Remove(tmpPath)
		return fmt.Errorf("downloaded file too small (%d bytes), likely an error page", written)
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod failed: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename failed: %w", err)
	}

	log.Printf("[Updater] download complete: %d bytes", written)
	return nil
}

// preFlightCheck starts the binary at binaryPath in --health-check mode,
// waits for the READY signal, sends a GET /health, and verifies the version.
// It kills the child process and returns nil on success.
func preFlightCheck(binaryPath, expectedVersion string) error {
	log.Printf("[Updater] pre-flight check: %s (expecting %s)", binaryPath, expectedVersion)

	cmd := exec.Command(binaryPath, "--health-check")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe failed: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting health-check binary failed: %w", err)
	}
	defer cmd.Process.Kill()

	// Read the port from stdout (READY:<port>\n)
	portCh := make(chan int, 1)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		var line string
		for scanner.Scan() {
			line = scanner.Text()
			if strings.HasPrefix(line, "READY:") {
				break
			}
		}
		err := scanner.Err()
		if err != nil {
			errCh <- fmt.Errorf("reading READY signal failed: %w", err)
			return
		}
		if !strings.HasPrefix(line, "READY:") {
			// Child process does not support --health-check mode.
			// This happens when updating to a version released before
			// the self-update feature was added. Skip pre-flight check
			// but still verify the binary exists and is executable.
			log.Printf("[Updater] pre-flight: binary does not support --health-check, skipping health verification")
			portCh <- -1 // sentinel: skip health check
			return
		}
		port, err := strconv.Atoi(line[6:])
		if err != nil {
			errCh <- fmt.Errorf("parsing port from %q failed: %w", line, err)
			return
		}
		portCh <- port
	}()

	var port int
	select {
	case port = <-portCh:
		if port == -1 {
			// Binary doesn't support --health-check mode;
			// accept the downloaded binary as valid (it passed size check).
			log.Println("[Updater] pre-flight check skipped (binary predates health-check support)")
			return nil
		}
	case err := <-errCh:
		return err
	case <-time.After(preFlightTimeout):
		cmd.Process.Kill()
		return fmt.Errorf("timed out waiting for health-check to start")
	}

	// GET /health
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(healthURL)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}

	var healthData struct {
		Status  string `json:"status"`
		Version struct {
			Version string `json:"version"`
		} `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&healthData); err != nil {
		return fmt.Errorf("parsing health response failed: %w", err)
	}
	if healthData.Status != "ok" {
		return fmt.Errorf("health check status is %q", healthData.Status)
	}
	if healthData.Version.Version != expectedVersion {
		return fmt.Errorf("version mismatch: got %q, expected %q",
			healthData.Version.Version, expectedVersion)
	}

	log.Printf("[Updater] pre-flight check passed: version %s", expectedVersion)
	return nil
}

// DoUpdate orchestrates the full update: check, download, pre-flight, replace.
// On success it calls selfReplace which terminates the process; on error
// it returns the error to the caller.

// ProgressFunc is called at key milestones during an update.
// status: "downloading" | "verifying" | "done"
// progress: 0-100
type ProgressFunc func(status string, progress int)

// DoUpdate orchestrates the full update: check, download, pre-flight, replace.
// onProgress is called at milestones; may be nil.
// On success it calls selfReplace which terminates the process; on error
// it returns the error to the caller.
func DoUpdate(owner, repo, currentVersion string, onProgress ProgressFunc) error {
	// 1. Check latest
	release, err := CheckLatest(owner, repo)
	if err != nil {
		return fmt.Errorf("version check failed: %w", err)
	}

	if onProgress != nil {
		onProgress("downloading", 5)
	}

	// 2. Compare versions
	if currentVersion != "v0.0.0-dev" && semver.Compare(currentVersion, release.Version) >= 0 {
		log.Printf("[Updater] already at latest version: %s", currentVersion)
		return nil
	}
	log.Printf("[Updater] updating %s -> %s", currentVersion, release.Version)

	// 3. Resolve paths
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	exeDir := filepath.Dir(exePath)
	newPath := filepath.Join(exeDir, filepath.Base(exePath)+".new")

	// 4. Download
	if err := DownloadBinary(release.DownloadURL, newPath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if onProgress != nil {
		onProgress("verifying", 65)
	}

	// 5. Pre-flight verification
	if err := preFlightCheck(newPath, release.Version); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("pre-flight verification failed: %v", err)
	}

	if onProgress != nil {
		onProgress("done", 100)
	}

	// 6. Self-replace (platform-specific, terminates process)
	selfReplace(exePath, newPath)
	return nil
}

// isPrereleaseTag returns true if the tag contains prerelease indicators.
func isPrereleaseTag(tag string) bool {
	lower := strings.ToLower(tag)
	for _, s := range []string{"-alpha", "-beta", "-rc", "-dev", "-pre", "-canary", "-nightly"} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}
