//go:build windows

package updater

import (
	"log"
	"os"
)

// selfReplace replaces the running binary on Windows.
//
// Windows does not allow deleting or overwriting a running .exe, but
// it *does* allow renaming it. The strategy:
//  1. Rename the current exe to .old (releases the original file name).
//  2. Rename .new to the original exe name.
//  3. Exit with code 1 so that SCM triggers its failure restart action
//     and relaunches the new binary.
//
// A backup (.old) is kept for rollback.
func selfReplace(currentPath, newPath string) {
	// Rename current -> .old (releases the filename lock).
	oldPath := currentPath + ".old"
	if err := os.Rename(currentPath, oldPath); err != nil {
		log.Printf("[Updater] warning: backup rename failed (continuing): %v", err)
	}

	// Rename .new -> current.
	if err := os.Rename(newPath, currentPath); err != nil {
		log.Printf("[Updater] ERROR: replace rename failed: %v", err)
		// Try to restore from backup.
		if err2 := os.Rename(oldPath, currentPath); err2 != nil {
			log.Printf("[Updater] FATAL: restore from backup also failed: %v. Manual recovery required.", err2)
		}
		os.Exit(1)
	}

	// Remove backup.
	_ = os.Remove(oldPath)

	log.Println("[Updater] replace complete, exiting with code 1 for SCM restart")
	// Use os.Exit(1) on Windows so SCM detects a failure and triggers
	// its restart action, launching the new binary.
	os.Exit(1)
}
