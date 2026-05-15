//go:build windows

package main

import (
	"fmt"
	"log"
	"os/exec"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// installService registers the current executable as a Windows service.
func installService(name, displayName, exePath string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("SCM connect failed: %w", err)
	}
	defer m.Disconnect()

	s, err := m.CreateService(name, exePath, mgr.Config{
		StartType:   mgr.StartAutomatic,
		DisplayName: displayName,
		Description: "CCX API Gateway - AI API Proxy and Protocol Translation Gateway",
	})
	if err != nil {
		return fmt.Errorf("service create failed: %w", err)
	}
	defer s.Close()

	// Set recovery actions via sc.exe (3 restarts at 30s intervals, then stop)
	// mgr.Config does not expose recovery actions directly.
	if err := exec.Command("sc", "failure", name,
		"reset=86400",
		"actions=restart/30000/restart/30000/restart/30000",
	).Run(); err != nil {
		log.Printf("[Service] warning: failed to set recovery actions: %v", err)
	}

	log.Printf("[Service] service %q installed: %s", name, exePath)
	return nil
}

// removeService deletes the service from SCM.
func removeService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("SCM connect failed: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", name, err)
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return fmt.Errorf("service delete failed: %w", err)
	}
	log.Printf("[Service] service %q removed", name)
	return nil
}

// startService starts the service via SCM.
func startService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("SCM connect failed: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", name, err)
	}
	defer s.Close()

	if err := s.Start(); err != nil {
		return fmt.Errorf("service start failed: %w", err)
	}
	log.Printf("[Service] service %q started", name)
	return nil
}

// stopService stops the service via SCM.
func stopService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("SCM connect failed: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", name, err)
	}
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("service stop failed: %w", err)
	}

	// Wait for the service to actually stop
	timeout := time.Now().Add(30 * time.Second)
	for time.Now().Before(timeout) {
		if status.State == svc.Stopped {
			break
		}
		time.Sleep(500 * time.Millisecond)
		var err2 error
		status, err2 = s.Query()
		if err2 != nil {
			break
		}
	}

	log.Printf("[Service] service %q stopped", name)
	return nil
}

// isWindowsService returns true when the process was started by SCM.
func isWindowsService() bool {
	ok, err := svc.IsWindowsService()
	return ok && err == nil
}

// runService enters the SCM event loop and blocks until the service is stopped.
// When a stop/shutdown signal arrives it closes shutdownCh to trigger
// graceful server shutdown in the main goroutine.
func runService(name string) error {
	return svc.Run(name, &serviceHandler{})
}

type serviceHandler struct{}

func (h *serviceHandler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	log.Println("[Service] SCM event loop started")

	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			s <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			log.Println("[Service] received stop/shutdown from SCM")
			s <- svc.Status{State: svc.StopPending}
			// Signal server goroutine to shut down via shutdownCh
			select {
			case <-shutdownCh:
			default:
				close(shutdownCh)
			}
			return false, 0
		}
	}
	return false, 0
}
