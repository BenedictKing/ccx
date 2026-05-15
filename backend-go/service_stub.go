//go:build !windows

package main

import "fmt"

func installService(name, displayName, exePath string) error {
	return fmt.Errorf("service commands (--install/--uninstall/--start/--stop) are only supported on Windows")
}

func removeService(name string) error {
	return fmt.Errorf("service commands are only supported on Windows")
}

func startService(name string) error {
	return fmt.Errorf("service commands are only supported on Windows")
}

func stopService(name string) error {
	return fmt.Errorf("service commands are only supported on Windows")
}

func isWindowsService() bool { return false }

func runService(name string) error {
	return fmt.Errorf("service mode is only supported on Windows")
}
