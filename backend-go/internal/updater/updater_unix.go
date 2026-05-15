//go:build !windows

package updater

import (
	"log"
	"os"
)

// selfReplace replaces the running binary on Linux/macOS.
//
// Linux allows renaming over a running executable because the kernel
// keeps the old inode alive for the running process. After os.Exit(0)
// the service manager (systemd, launchd) restarts from the new inode.
//
// A backup (.old) is kept for rollback.
func selfReplace(currentPath, newPath string) {
	// Keep a backup for rollback.
	oldPath := currentPath + ".old"
	if err := os.Rename(currentPath, oldPath); err != nil {
		log.Printf("[Updater] warning: backup rename failed (continuing): %v", err)
	}

	// Replace running binary.
	if err := os.Rename(newPath, currentPath); err != nil {
		log.Printf("[Updater] ERROR: replace rename failed: %v", err)
		// Try to restore from backup.
		os.Rename(oldPath, currentPath)
		os.Exit(1)
	}

	// Remove old backup now that the new binary is in place.
	_ = os.Remove(oldPath)

	log.Println("[Updater] replace complete, exiting for service manager restart")
	// Use os.Exit(0) on Unix -- the service manager (systemd) uses
	// Restart=always and will restart the process with the new binary.
	os.Exit(0)
}
