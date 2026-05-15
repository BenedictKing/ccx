package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// runHealthCheck starts a minimal HTTP server that only exposes /health
// on a random localhost port. Used by the updater's pre-flight validation
// to verify that a newly downloaded binary starts correctly.
//
// This mode initializes nothing (no config, no logger, no metrics) so it
// starts and responds quickly. It exits automatically after 5 seconds.
func runHealthCheck() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": "ok",
			"version": gin.H{
				"version": Version,
			},
		})
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: cannot bind health check port: %v\n", err)
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	// Write directly to stdout and sync to ensure the parent process
	// receives the READY signal immediately (required on Windows where
	// pipe output may be buffered).
	fmt.Fprintf(os.Stdout, "READY:%d\n", port)
	os.Stdout.Sync()

	// Auto-exit after 5 seconds so the process doesn't orphan.
	go func() {
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}()

	if err := http.Serve(listener, r); err != nil {
		fmt.Fprintf(os.Stderr, "health check serve error: %v\n", err)
		os.Exit(1)
	}
}
