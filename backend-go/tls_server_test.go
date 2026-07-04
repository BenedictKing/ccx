package main

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestEndpointForEnv(t *testing.T) {
	tests := []struct {
		name string
		env  *config.EnvConfig
		want string
	}{
		{
			name: "http by default",
			env:  &config.EnvConfig{Port: 3688},
			want: "http://localhost:3688/v1",
		},
		{
			name: "https when enabled",
			env:  &config.EnvConfig{Port: 8443, EnableHTTPS: true},
			want: "https://localhost:8443/v1",
		},
		{
			name: "configured bind host",
			env:  &config.EnvConfig{Port: 3688, BindHost: "127.0.0.1"},
			want: "http://127.0.0.1:3688/v1",
		},
		{
			name: "wildcard bind host shows localhost endpoint",
			env:  &config.EnvConfig{Port: 3688, BindHost: "0.0.0.0"},
			want: "http://localhost:3688/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := endpointForEnv(tt.env).URL("/v1"); got != tt.want {
				t.Fatalf("URL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListenAddressForEnv(t *testing.T) {
	tests := []struct {
		name string
		env  *config.EnvConfig
		want string
	}{
		{
			name: "empty bind host listens on all interfaces",
			env:  &config.EnvConfig{Port: 3688},
			want: ":3688",
		},
		{
			name: "ipv4 loopback",
			env:  &config.EnvConfig{Port: 3688, BindHost: "127.0.0.1"},
			want: "127.0.0.1:3688",
		},
		{
			name: "ipv6 loopback",
			env:  &config.EnvConfig{Port: 3688, BindHost: "::1"},
			want: "[::1]:3688",
		},
		{
			name: "trims whitespace",
			env:  &config.EnvConfig{Port: 3688, BindHost: " 127.0.0.1 "},
			want: "127.0.0.1:3688",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listenAddressForEnv(tt.env); got != tt.want {
				t.Fatalf("listenAddressForEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfigureServerTLSRequiresCertificateSource(t *testing.T) {
	srv := &http.Server{}
	envCfg := &config.EnvConfig{EnableHTTPS: true, TLSAutoCert: false}

	if err := configureServerTLS(srv, envCfg); err == nil {
		t.Fatal("configureServerTLS() error = nil, want error")
	}
}

func TestConfigureServerTLSRequiresCertAndKeyPair(t *testing.T) {
	srv := &http.Server{}
	envCfg := &config.EnvConfig{EnableHTTPS: true, TLSCertFile: "/tmp/localhost.pem"}

	if err := configureServerTLS(srv, envCfg); err == nil {
		t.Fatal("configureServerTLS() error = nil, want error")
	}
}

func TestConfigureServerTLSAutoCert(t *testing.T) {
	srv := &http.Server{}
	envCfg := &config.EnvConfig{EnableHTTPS: true, TLSAutoCert: true}

	if err := configureServerTLS(srv, envCfg); err != nil {
		t.Fatalf("configureServerTLS() error = %v", err)
	}
	if srv.TLSConfig == nil || len(srv.TLSConfig.Certificates) != 1 {
		t.Fatalf("TLSConfig certificates = %#v, want one certificate", srv.TLSConfig)
	}
}

func TestConfigureServerTLSLoadsCertFiles(t *testing.T) {
	certFile, keyFile := writeTestCertificateFiles(t)
	srv := &http.Server{}
	envCfg := &config.EnvConfig{
		EnableHTTPS: true,
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
	}

	if err := configureServerTLS(srv, envCfg); err != nil {
		t.Fatalf("configureServerTLS() error = %v", err)
	}
	if srv.TLSConfig == nil || len(srv.TLSConfig.Certificates) != 1 {
		t.Fatalf("TLSConfig certificates = %#v, want one certificate", srv.TLSConfig)
	}
}

func TestConfigureServerTLSRelativePathErrorIncludesAbsolutePathHint(t *testing.T) {
	srv := &http.Server{}
	envCfg := &config.EnvConfig{
		EnableHTTPS: true,
		TLSCertFile: "backend-go/.config/certs/localhost.pem",
		TLSKeyFile:  "backend-go/.config/certs/localhost-key.pem",
	}

	err := configureServerTLS(srv, envCfg)
	if err == nil {
		t.Fatal("configureServerTLS() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("error = %q, want absolute path hint", err)
	}
}

func TestServeHTTPAndHTTPSOnSamePort(t *testing.T) {
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
		}),
		ReadHeaderTimeout: time.Second,
	}
	envCfg := &config.EnvConfig{EnableHTTPS: true, TLSAutoCert: true}
	if err := configureServerTLS(srv, envCfg); err != nil {
		t.Fatalf("configureServerTLS() error = %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTPAndHTTPS(srv, ln)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		select {
		case <-errCh:
		case <-time.After(time.Second):
			t.Error("server did not shut down")
		}
	})

	addr := ln.Addr().String()
	assertGETStatus(t, &http.Client{Timeout: 2 * time.Second}, "http://"+addr+"/health", http.StatusOK)
	httpsClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	assertGETStatus(t, httpsClient, "https://"+addr+"/health", http.StatusOK)
}

func TestEnsureDefaultNextProtos(t *testing.T) {
	cfg := &tls.Config{NextProtos: []string{"custom", "h2"}}

	ensureDefaultNextProtos(cfg)

	want := []string{"custom", "h2", "http/1.1"}
	if len(cfg.NextProtos) != len(want) {
		t.Fatalf("NextProtos = %#v, want %#v", cfg.NextProtos, want)
	}
	for i := range want {
		if cfg.NextProtos[i] != want[i] {
			t.Fatalf("NextProtos = %#v, want %#v", cfg.NextProtos, want)
		}
	}
}

func TestEnsureDefaultNextProtosDoesNotEnableHTTP2(t *testing.T) {
	cfg := &tls.Config{}

	ensureDefaultNextProtos(cfg)

	want := []string{"http/1.1"}
	if len(cfg.NextProtos) != len(want) {
		t.Fatalf("NextProtos = %#v, want %#v", cfg.NextProtos, want)
	}
	for i := range want {
		if cfg.NextProtos[i] != want[i] {
			t.Fatalf("NextProtos = %#v, want %#v", cfg.NextProtos, want)
		}
	}
}

func TestGenerateLocalhostCertificateSANs(t *testing.T) {
	cert, err := generateLocalhostCertificate(time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("generateLocalhostCertificate() error = %v", err)
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	if err := parsed.VerifyHostname("localhost"); err != nil {
		t.Fatalf("localhost hostname verification failed: %v", err)
	}
	if err := parsed.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatalf("127.0.0.1 hostname verification failed: %v", err)
	}
}

func writeTestCertificateFiles(t *testing.T) (string, string) {
	t.Helper()
	cert, err := generateLocalhostCertificate(time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("generateLocalhostCertificate() error = %v", err)
	}
	privateKey, ok := cert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("certificate private key type = %T, want *rsa.PrivateKey", cert.PrivateKey)
	}
	dir := t.TempDir()
	certFile := filepath.Join(dir, "localhost.pem")
	keyFile := filepath.Join(dir, "localhost-key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", certFile, err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", keyFile, err)
	}
	return certFile, keyFile
}

func assertGETStatus(t *testing.T, client *http.Client, url string, want int) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("GET %s status = %d, want %d", url, resp.StatusCode, want)
	}
	if resp.TLS != nil {
		return
	}
	if want == http.StatusOK && len(url) >= len("https://") && url[:len("https://")] == "https://" {
		t.Fatalf("GET %s returned no TLS state", url)
	}
}
