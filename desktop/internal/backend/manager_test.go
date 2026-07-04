package backend

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewManagerDefaults(t *testing.T) {
	m := NewManager(Options{RootDir: t.TempDir()})
	if m.port != defaultPort {
		t.Errorf("port = %d, want %d", m.port, defaultPort)
	}
	if m.rootDir == "" {
		t.Error("rootDir should not be empty")
	}
	if m.dataDir == "" {
		t.Error("dataDir should not be empty")
	}
	if m.client == nil {
		t.Error("client should not be nil")
	}
}

func TestNewManagerCustomPort(t *testing.T) {
	m := NewManager(Options{RootDir: t.TempDir(), DefaultPort: 9999, DataDir: t.TempDir()})
	if m.port != 9999 {
		t.Errorf("port = %d, want 9999", m.port)
	}
}

func TestDetectRootDirFallsBackToCwd(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	// macOS 下 /var 是 /private/var 的符号链接，t.TempDir() 返回前者但 os.Getwd() 解析到后者，
	// 用 EvalSymlinks 保证期望值与 os.Getwd() 语义一致。
	want, _ := filepath.EvalSymlinks(dir)
	if got := detectRootDir(); got != want {
		t.Errorf("detectRootDir() = %q, want %q", got, want)
	}
}

func TestBinaryCandidates(t *testing.T) {
	root := t.TempDir()
	m := NewManager(Options{RootDir: root})
	candidates := m.binaryCandidates()
	if len(candidates) == 0 {
		t.Fatal("binaryCandidates should not be empty")
	}
	name := "ccx-go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	// 应包含 rootDir 下的候选路径
	found := false
	for _, c := range candidates {
		if strings.Contains(c, root) && strings.HasSuffix(c, name) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("binaryCandidates should contain rootDir-based path; got: %v", candidates)
	}
	// 不应有重复
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c] {
			t.Errorf("duplicate candidate: %s", c)
		}
		seen[c] = true
	}
}

func TestFetchHealth(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"healthy","version":"2.7.12"}`))
		}))
		defer srv.Close()

		port := srv.Listener.Addr().(*net.TCPAddr).Port
		m := NewManager(Options{RootDir: t.TempDir()})
		data, err := m.fetchHealth(t.Context(), port)
		if err != nil {
			t.Fatalf("expected success, got: %v", err)
		}
		if data["status"] != "healthy" {
			t.Errorf("status = %v", data["status"])
		}
	})

	t.Run("healthy https self-signed", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"healthy","version":"2.7.12"}`))
		}))
		defer srv.Close()

		dataDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dataDir, ".env"), []byte("ENABLE_HTTPS=true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		port := srv.Listener.Addr().(*net.TCPAddr).Port
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: dataDir})
		data, err := m.fetchHealth(t.Context(), port)
		if err != nil {
			t.Fatalf("expected success, got: %v", err)
		}
		if data["status"] != "healthy" {
			t.Errorf("status = %v", data["status"])
		}
	})

	t.Run("unhealthy status code", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		port := srv.Listener.Addr().(*net.TCPAddr).Port
		m := NewManager(Options{RootDir: t.TempDir()})
		_, err := m.fetchHealth(t.Context(), port)
		if err == nil {
			t.Error("expected error for 503")
		}
	})

	t.Run("non-healthy body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"degraded"}`))
		}))
		defer srv.Close()

		port := srv.Listener.Addr().(*net.TCPAddr).Port
		m := NewManager(Options{RootDir: t.TempDir()})
		_, err := m.fetchHealth(t.Context(), port)
		if err == nil {
			t.Error("expected error for non-healthy status")
		}
	})

	t.Run("port zero", func(t *testing.T) {
		m := NewManager(Options{RootDir: t.TempDir()})
		_, err := m.fetchHealth(t.Context(), 0)
		if err == nil {
			t.Error("expected error for port 0")
		}
	})
}

func TestSelectPort(t *testing.T) {
	t.Run("first port available", func(t *testing.T) {
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: t.TempDir(), DefaultPort: 19800})
		port, err := m.selectPort(t.Context())
		if err != nil {
			t.Fatalf("selectPort failed: %v", err)
		}
		if port < 19800 || port >= 19820 {
			t.Errorf("port = %d, want in [19800, 19820)", port)
		}
	})

	t.Run("skip occupied port", func(t *testing.T) {
		// 占用第一个端口
		ln, err := net.Listen("tcp", ":19850")
		if err != nil {
			t.Skipf("cannot bind port 19850: %v", err)
		}
		defer ln.Close()

		m := NewManager(Options{RootDir: t.TempDir(), DataDir: t.TempDir(), DefaultPort: 19850})
		port, err := m.selectPort(t.Context())
		if err != nil {
			t.Fatalf("selectPort failed: %v", err)
		}
		if port == 19850 {
			t.Error("should skip occupied port")
		}
	})
}

func TestFindHealthyPort(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"status":"healthy"}`))
		}))
		defer srv.Close()

		port := srv.Listener.Addr().(*net.TCPAddr).Port
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: t.TempDir(), DefaultPort: port})
		found, ok := m.findHealthyPort(t.Context())
		if !ok {
			t.Fatal("expected to find healthy port")
		}
		if found != port {
			t.Errorf("found port = %d, want %d", found, port)
		}
	})

	t.Run("not found", func(t *testing.T) {
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: t.TempDir(), DefaultPort: 19900})
		_, ok := m.findHealthyPort(t.Context())
		if ok {
			t.Error("should not find healthy port on unused port range")
		}
	})
}

func TestEnsureProxyAccessKey(t *testing.T) {
	t.Run("from env", func(t *testing.T) {
		t.Setenv("PROXY_ACCESS_KEY", "env-key-123")
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: t.TempDir()})
		key, err := m.EnsureProxyAccessKey()
		if err != nil {
			t.Fatalf("EnsureProxyAccessKey failed: %v", err)
		}
		if key != "env-key-123" {
			t.Errorf("key = %q, want %q", key, "env-key-123")
		}
	})

	t.Run("configured desktop env before process env", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dataDir, ".env"), []byte("PROXY_ACCESS_KEY=desktop-key\n"), 0o600); err != nil {
			t.Fatalf("write desktop .env failed: %v", err)
		}
		t.Setenv("PROXY_ACCESS_KEY", "env-key-123")
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: dataDir})
		key, err := m.EnsureProxyAccessKey()
		if err != nil {
			t.Fatalf("EnsureProxyAccessKey failed: %v", err)
		}
		if key != "desktop-key" {
			t.Errorf("key = %q, want %q", key, "desktop-key")
		}
	})

	t.Run("invalid configured env falls back to process env", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dataDir, ".env"), 0o755); err != nil {
			t.Fatalf("create invalid .env failed: %v", err)
		}
		t.Setenv("PROXY_ACCESS_KEY", "env-key-123")
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: dataDir})
		key, err := m.EnsureProxyAccessKey()
		if err != nil {
			t.Fatalf("EnsureProxyAccessKey failed: %v", err)
		}
		if key != "env-key-123" {
			t.Errorf("key = %q, want %q", key, "env-key-123")
		}
	})

	t.Run("generate new", func(t *testing.T) {
		dataDir := t.TempDir()
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: dataDir})
		key, err := m.EnsureProxyAccessKey()
		if err != nil {
			t.Fatalf("EnsureProxyAccessKey failed: %v", err)
		}
		if !strings.HasPrefix(key, "ccx-") {
			t.Errorf("key should start with ccx-, got %q", key)
		}
		// 验证写入了 .env 文件
		envPath := filepath.Join(dataDir, ".env")
		content, _ := os.ReadFile(envPath)
		if !strings.Contains(string(content), "PROXY_ACCESS_KEY=") {
			t.Error(".env should contain PROXY_ACCESS_KEY")
		}
	})
}

func TestIsSetupCompleteUsesDesktopEnvOnly(t *testing.T) {
	dataDir := t.TempDir()
	rootDir := t.TempDir()

	t.Setenv("PROXY_ACCESS_KEY", "env-key-123")
	if err := os.WriteFile(filepath.Join(rootDir, ".env"), []byte("PROXY_ACCESS_KEY=root-key\n"), 0o600); err != nil {
		t.Fatalf("write root .env failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, ".env"), []byte("PROXY_ACCESS_KEY=\n"), 0o600); err != nil {
		t.Fatalf("write desktop .env failed: %v", err)
	}

	m := NewManager(Options{RootDir: rootDir, DataDir: dataDir})
	if m.IsSetupComplete() {
		t.Fatal("empty desktop PROXY_ACCESS_KEY should require setup")
	}

	if err := os.WriteFile(filepath.Join(dataDir, ".env"), []byte("PROXY_ACCESS_KEY=desktop-key\n"), 0o600); err != nil {
		t.Fatalf("write desktop .env failed: %v", err)
	}
	if !m.IsSetupComplete() {
		t.Fatal("desktop PROXY_ACCESS_KEY should complete setup")
	}
}

func TestGenerateProxyAccessKey(t *testing.T) {
	key, err := generateProxyAccessKey()
	if err != nil {
		t.Fatalf("generateProxyAccessKey failed: %v", err)
	}
	if !strings.HasPrefix(key, "ccx-") {
		t.Errorf("key should start with ccx-, got %q", key)
	}
	// 8 bytes hex = 16 chars + "ccx-" prefix = 20 total
	if len(key) != 20 {
		t.Errorf("key length = %d, want 20", len(key))
	}
	// 两次生成不同
	key2, _ := generateProxyAccessKey()
	if key == key2 {
		t.Error("two generated keys should differ")
	}
}

func TestReadProxyAccessKey(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		os.WriteFile(path, []byte("OTHER=value\nPROXY_ACCESS_KEY=my-secret-key\n"), 0o644)
		key, err := readProxyAccessKey(path)
		if err != nil {
			t.Fatalf("readProxyAccessKey failed: %v", err)
		}
		if key != "my-secret-key" {
			t.Errorf("key = %q, want %q", key, "my-secret-key")
		}
	})

	t.Run("quoted value", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		os.WriteFile(path, []byte(`PROXY_ACCESS_KEY="quoted-key"`), 0o644)
		key, _ := readProxyAccessKey(path)
		if key != "quoted-key" {
			t.Errorf("key = %q, want %q", key, "quoted-key")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		key, err := readProxyAccessKey("/nonexistent/.env")
		if err != nil {
			t.Fatalf("should not error for missing file: %v", err)
		}
		if key != "" {
			t.Errorf("key = %q, want empty", key)
		}
	})
}

func TestReadPortFromEnvFile(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		os.WriteFile(path, []byte("PROXY_ACCESS_KEY=key\nPORT=3699\nENV=production\n"), 0o644)
		port, err := readPortFromEnvFile(path)
		if err != nil {
			t.Fatalf("readPortFromEnvFile failed: %v", err)
		}
		if port != 3699 {
			t.Errorf("port = %d, want 3699", port)
		}
	})

	t.Run("quoted value", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		os.WriteFile(path, []byte(`PORT="8080"`), 0o644)
		port, err := readPortFromEnvFile(path)
		if err != nil {
			t.Fatalf("readPortFromEnvFile failed: %v", err)
		}
		if port != 8080 {
			t.Errorf("port = %d, want 8080", port)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		port, err := readPortFromEnvFile("/nonexistent/.env")
		if err != nil {
			t.Fatalf("should not error for missing file: %v", err)
		}
		if port != 0 {
			t.Errorf("port = %d, want 0", port)
		}
	})

	t.Run("no PORT entry", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		os.WriteFile(path, []byte("ENV=production\n"), 0o644)
		port, err := readPortFromEnvFile(path)
		if err != nil {
			t.Fatalf("readPortFromEnvFile failed: %v", err)
		}
		if port != 0 {
			t.Errorf("port = %d, want 0", port)
		}
	})

	t.Run("invalid port", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		os.WriteFile(path, []byte("PORT=abc\n"), 0o644)
		_, err := readPortFromEnvFile(path)
		if err == nil {
			t.Error("expected error for invalid port")
		}
	})

	t.Run("out of range port", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		os.WriteFile(path, []byte("PORT=99999\n"), 0o644)
		_, err := readPortFromEnvFile(path)
		if err == nil {
			t.Error("expected error for out of range port")
		}
	})
}

func TestSetEnv(t *testing.T) {
	t.Run("replace existing", func(t *testing.T) {
		env := []string{"PATH=/usr/bin", "PORT=3000", "HOME=/home/user"}
		got := setEnv(env, "PORT", "4000")
		found := false
		for _, e := range got {
			if e == "PORT=4000" {
				found = true
			}
			if e == "PORT=3000" {
				t.Error("old value should be replaced")
			}
		}
		if !found {
			t.Error("PORT=4000 not found")
		}
	})

	t.Run("append new", func(t *testing.T) {
		env := []string{"PATH=/usr/bin"}
		got := setEnv(env, "NEW_KEY", "new_value")
		found := false
		for _, e := range got {
			if e == "NEW_KEY=new_value" {
				found = true
			}
		}
		if !found {
			t.Error("NEW_KEY=new_value not found")
		}
	})
}

func TestBuildEnvIncludesHTTPSSettings(t *testing.T) {
	dataDir := t.TempDir()
	content := strings.Join([]string{
		"PROXY_ACCESS_KEY=desktop-key",
		"BIND_HOST=127.0.0.1",
		"ENABLE_HTTPS=true",
		"TLS_AUTO_CERT=false",
		"TLS_CERT_FILE=/tmp/localhost.pem",
		"TLS_KEY_FILE=/tmp/localhost-key.pem",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dataDir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Options{RootDir: t.TempDir(), DataDir: dataDir})

	got := m.buildEnv(8443)
	for _, want := range []string{
		"PORT=8443",
		"BIND_HOST=127.0.0.1",
		"ENABLE_HTTPS=true",
		"TLS_AUTO_CERT=false",
		"TLS_CERT_FILE=/tmp/localhost.pem",
		"TLS_KEY_FILE=/tmp/localhost-key.pem",
	} {
		if !containsEnv(got, want) {
			t.Fatalf("buildEnv() missing %s in %#v", want, got)
		}
	}
}

func TestBuildEnvIncludesBlankBindHost(t *testing.T) {
	dataDir := t.TempDir()
	content := strings.Join([]string{
		"PROXY_ACCESS_KEY=desktop-key",
		"BIND_HOST=",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dataDir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BIND_HOST", "127.0.0.1")
	m := NewManager(Options{RootDir: t.TempDir(), DataDir: dataDir})

	got := m.buildEnv(8443)
	if !containsEnv(got, "BIND_HOST=") {
		t.Fatalf("buildEnv() should preserve explicit blank BIND_HOST in %#v", got)
	}
}

func containsEnv(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}

func TestUniquePaths(t *testing.T) {
	got := uniquePaths([]string{"/a", "/b", "/a", "/c", "/b"})
	if len(got) != 3 {
		t.Errorf("expected 3 unique paths, got %d: %v", len(got), got)
	}
}

func TestLogs(t *testing.T) {
	m := NewManager(Options{RootDir: t.TempDir(), DataDir: t.TempDir()})
	logs := m.Logs()
	if len(logs) != 0 {
		t.Errorf("expected empty logs, got %d", len(logs))
	}
}

func TestWebURL(t *testing.T) {
	m := NewManager(Options{RootDir: t.TempDir(), DataDir: t.TempDir(), DefaultPort: 7777})
	if got := m.WebURL(); got != "http://127.0.0.1:7777" {
		t.Errorf("WebURL = %q, want %q", got, "http://127.0.0.1:7777")
	}
}

func TestWebURLHTTPS(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, ".env"), []byte("ENABLE_HTTPS=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Options{RootDir: t.TempDir(), DataDir: dataDir, DefaultPort: 7777})
	if got := m.WebURL(); got != "https://127.0.0.1:7777" {
		t.Errorf("WebURL = %q, want %q", got, "https://127.0.0.1:7777")
	}
}

func TestWebURLUsesBindHost(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, ".env"), []byte("BIND_HOST=192.168.1.23\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Options{RootDir: t.TempDir(), DataDir: dataDir, DefaultPort: 7777})
	if got := m.WebURL(); got != "http://192.168.1.23:7777" {
		t.Errorf("WebURL = %q, want %q", got, "http://192.168.1.23:7777")
	}
}

func TestWebURLUsesLoopbackForUnspecifiedBindHost(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, ".env"), []byte("BIND_HOST=0.0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Options{RootDir: t.TempDir(), DataDir: dataDir, DefaultPort: 7777})
	if got := m.WebURL(); got != "http://127.0.0.1:7777" {
		t.Errorf("WebURL = %q, want %q", got, "http://127.0.0.1:7777")
	}
}

func TestDataDir(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(Options{RootDir: t.TempDir(), DataDir: dir})
	if m.DataDir() != dir {
		t.Errorf("DataDir = %q, want %q", m.DataDir(), dir)
	}
}

func TestCurrentPort(t *testing.T) {
	m := NewManager(Options{RootDir: t.TempDir(), DataDir: t.TempDir(), DefaultPort: 5555})
	if m.CurrentPort() != 5555 {
		t.Errorf("CurrentPort = %d, want 5555", m.CurrentPort())
	}
}

func TestReadConfiguredPort(t *testing.T) {
	t.Run("env file has PORT", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".env"), []byte("PORT=4000\n"), 0o644)
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: dir, DefaultPort: 3000})
		if got := m.ReadConfiguredPort(); got != 4000 {
			t.Errorf("ReadConfiguredPort = %d, want 4000", got)
		}
	})

	t.Run("env file missing PORT falls back to cached", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".env"), []byte("ENV=production\n"), 0o644)
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: dir, DefaultPort: 5555})
		if got := m.ReadConfiguredPort(); got != 5555 {
			t.Errorf("ReadConfiguredPort = %d, want 5555", got)
		}
	})

	t.Run("env file missing falls back to cached", func(t *testing.T) {
		m := NewManager(Options{RootDir: t.TempDir(), DataDir: t.TempDir(), DefaultPort: 7777})
		if got := m.ReadConfiguredPort(); got != 7777 {
			t.Errorf("ReadConfiguredPort = %d, want 7777", got)
		}
	})
}
