package config

import "testing"

func TestNewEnvConfigParsesHTTPS(t *testing.T) {
	t.Setenv("ENABLE_HTTPS", "true")
	t.Setenv("TLS_CERT_FILE", "/tmp/localhost.pem")
	t.Setenv("TLS_KEY_FILE", "/tmp/localhost-key.pem")
	t.Setenv("TLS_AUTO_CERT", "false")

	envCfg := NewEnvConfig()

	if !envCfg.EnableHTTPS {
		t.Fatal("EnableHTTPS = false, want true")
	}
	if envCfg.TLSCertFile != "/tmp/localhost.pem" {
		t.Fatalf("TLSCertFile = %q", envCfg.TLSCertFile)
	}
	if envCfg.TLSKeyFile != "/tmp/localhost-key.pem" {
		t.Fatalf("TLSKeyFile = %q", envCfg.TLSKeyFile)
	}
	if envCfg.TLSAutoCert {
		t.Fatal("TLSAutoCert = true, want false")
	}
}

func TestNewEnvConfigHTTPSDefaults(t *testing.T) {
	envCfg := NewEnvConfig()

	if envCfg.EnableHTTPS {
		t.Fatal("EnableHTTPS = true, want false")
	}
	if !envCfg.TLSAutoCert {
		t.Fatal("TLSAutoCert = false, want true")
	}
}

func TestNewEnvConfigParsesBindHost(t *testing.T) {
	t.Setenv("BIND_HOST", " 127.0.0.1 ")

	envCfg := NewEnvConfig()

	if envCfg.BindHost != "127.0.0.1" {
		t.Fatalf("BindHost = %q, want 127.0.0.1", envCfg.BindHost)
	}
}
