package backend

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/desktop/internal/appdirs"
)

const (
	defaultPort = 3688
	maxLogLines = 500
)

type Options struct {
	RootDir     string
	DataDir     string
	DefaultPort int
}

type Status struct {
	Running    bool           `json:"running"`
	Starting   bool           `json:"starting"`
	Attached   bool           `json:"attached"`
	Port       int            `json:"port"`
	URL        string         `json:"url"`
	PID        int            `json:"pid"`
	BinaryPath string         `json:"binaryPath"`
	DataDir    string         `json:"dataDir"`
	Health     map[string]any `json:"health"`
	LastError  string         `json:"lastError"`
	Logs       []string       `json:"logs"`
}

type Manager struct {
	mu         sync.Mutex
	rootDir    string
	dataDir    string
	port       int
	binaryPath string
	cmd        *exec.Cmd
	done       chan error
	starting   bool
	startDone  chan struct{}
	startErr   error
	attached   bool
	lastError  string
	logs       []string
	client     *http.Client
}

func NewManager(options Options) *Manager {
	port := options.DefaultPort
	if port == 0 {
		port = defaultPort
	}
	rootDir := options.RootDir
	if rootDir == "" {
		rootDir = detectRootDir()
	}
	dataDir := options.DataDir
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	// 优先使用 .env 中用户配置的端口，确保未启动时监控面板也显示正确端口
	if envPort, err := readPortFromEnvFile(filepath.Join(dataDir, ".env")); err == nil && envPort > 0 {
		port = envPort
	}
	m := &Manager{
		rootDir: rootDir,
		dataDir: dataDir,
		port:    port,
		client: &http.Client{
			Timeout: 900 * time.Millisecond,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 仅用于本机健康检查，支持 CCX 本地自签名 HTTPS。
			},
		},
	}
	// 提前探测二进制路径，使 Status() 在未启动时也能返回路径
	if p, err := m.findBinary(); err == nil {
		m.binaryPath = p
	}
	return m
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.mu.Unlock()
		return nil
	}
	if m.attached {
		if _, err := m.fetchHealth(ctx, m.port); err == nil {
			m.mu.Unlock()
			return nil
		}
		m.attached = false
		m.addLogLocked("[Desktop-Backend] 外部 CCX 实例不可用，准备重新启动")
	}
	if m.starting {
		wait := m.startDone
		m.mu.Unlock()
		if wait == nil {
			return fmt.Errorf("CCX 正在启动")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
			m.mu.Lock()
			err := m.startErr
			m.mu.Unlock()
			return err
		}
	}
	done := make(chan struct{})
	m.starting = true
	m.startDone = done
	m.startErr = nil
	m.lastError = ""
	m.mu.Unlock()

	err := m.doStart(ctx)
	m.mu.Lock()
	m.starting = false
	m.startErr = err
	m.startDone = nil
	m.mu.Unlock()
	close(done)
	return err
}

func (m *Manager) doStart(ctx context.Context) error {
	if err := os.MkdirAll(m.dataDir, 0o755); err != nil {
		m.recordStartError(err)
		return err
	}

	if port, ok := m.findHealthyPort(ctx); ok {
		m.mu.Lock()
		m.attached = true
		m.port = port
		m.mu.Unlock()
		m.appendLog(fmt.Sprintf("[Desktop-Backend] 复用已有 CCX 实例（外部进程），port=%d", port))
		return nil
	}

	binaryPath, err := m.findBinary()
	if err != nil {
		m.recordStartError(err)
		return err
	}

	// 优先使用 .env 中用户配置的端口
	envPath := filepath.Join(m.dataDir, ".env")
	if preferredPort, err := readPortFromEnvFile(envPath); err == nil && preferredPort > 0 {
		m.mu.Lock()
		m.port = preferredPort
		m.mu.Unlock()
	}

	port, err := m.selectPort(ctx)
	if err != nil {
		m.recordStartError(err)
		return err
	}

	cmd := exec.Command(binaryPath)
	cmd.Dir = m.dataDir
	cmd.Env = m.buildEnv(port)
	applyPlatformAttrs(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.recordStartError(err)
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.recordStartError(err)
		return err
	}

	if err := cmd.Start(); err != nil {
		m.recordStartError(err)
		return err
	}

	done := make(chan error, 1)
	m.mu.Lock()
	m.cmd = cmd
	m.done = done
	m.attached = false
	m.port = port
	m.binaryPath = binaryPath
	m.addLogLocked(fmt.Sprintf("[Desktop-Backend] 已启动 CCX，pid=%d port=%d", cmd.Process.Pid, port))
	m.mu.Unlock()

	go m.scanLogs("stdout", stdout)
	go m.scanLogs("stderr", stderr)
	go m.waitProcess(cmd, done)

	healthCtx, healthCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				healthCancel()
			case <-healthCtx.Done():
			}
		}()
	}
	defer healthCancel()
	if err := m.WaitHealthy(healthCtx, 15*time.Second); err != nil {
		m.setError(err)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.Stop(cleanupCtx)
		return err
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	cmd := m.cmd
	done := m.done
	attached := m.attached
	m.mu.Unlock()
	if attached {
		return fmt.Errorf("当前 CCX 实例由外部进程托管，无法通过桌面外壳停止")
	}
	if cmd == nil || cmd.Process == nil {
		if _, err := m.fetchHealth(ctx, m.port); err == nil {
			return fmt.Errorf("当前端口 %d 上的 CCX 由外部进程托管，无法通过桌面外壳停止", m.port)
		}
		return nil
	}

	m.appendLog(fmt.Sprintf("[Desktop-Backend] 正在停止 CCX，pid=%d", cmd.Process.Pid))
	if err := terminateProcess(cmd); err != nil && !errors.Is(err, os.ErrProcessDone) {
		m.setError(err)
	}

	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return ctx.Err()
	case <-time.After(5 * time.Second):
		m.appendLog("[Desktop-Backend] 优雅停止超时，强制结束进程")
		_ = cmd.Process.Kill()
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
			m.done = nil
		}
		m.mu.Unlock()
		return nil
	case <-done:
		return nil
	}
}

func (m *Manager) Restart(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		return err
	}
	return m.Start(ctx)
}

func (m *Manager) ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (m *Manager) Status(ctx context.Context) Status {
	ctx = m.ensureContext(ctx)
	m.mu.Lock()
	status := Status{
		Running:    m.isRunningLocked(),
		Starting:   m.starting,
		Attached:   m.attached,
		Port:       m.port,
		URL:        m.urlLocked(),
		BinaryPath: m.binaryPath,
		DataDir:    m.dataDir,
		LastError:  m.lastError,
		Logs:       append([]string(nil), m.logs...),
	}
	if m.cmd != nil && m.cmd.Process != nil {
		status.PID = m.cmd.Process.Pid
	}
	m.mu.Unlock()

	if health, err := m.fetchHealth(ctx, status.Port); err == nil {
		status.Health = health
		status.Running = true
		status.LastError = ""
		if status.PID == 0 {
			status.Attached = true
		}
		m.mu.Lock()
		m.lastError = ""
		m.mu.Unlock()
	} else {
		m.mu.Lock()
		if m.attached && m.cmd == nil {
			m.attached = false
			status.Attached = false
			status.Running = false
			m.addLogLocked("[Desktop-Backend] 外部 CCX 实例已不可达，清除附着状态")
		}
		m.mu.Unlock()
	}
	return status
}

func (m *Manager) Logs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.logs...)
}

func (m *Manager) WebURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.urlLocked()
}

func (m *Manager) DataDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dataDir
}

func (m *Manager) CurrentPort() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.port
}

// ReadConfiguredPort 从 .env 文件读取用户配置的端口，读不到时返回运行时缓存端口。
func (m *Manager) ReadConfiguredPort() int {
	m.mu.Lock()
	dataDir := m.dataDir
	port := m.port
	m.mu.Unlock()
	if p, err := readPortFromEnvFile(filepath.Join(dataDir, ".env")); err == nil && p > 0 {
		return p
	}
	return port
}

func (m *Manager) EnsureProxyAccessKey() (string, error) {
	m.mu.Lock()
	dataDir := m.dataDir
	rootDir := m.rootDir
	m.mu.Unlock()
	return ensureProxyAccessKey(dataDir, rootDir)
}

// ReadProxyAccessKey 只读方式获取 PROXY_ACCESS_KEY，不生成新 key、不写入 .env。
func (m *Manager) ReadProxyAccessKey() (string, error) {
	m.mu.Lock()
	dataDir := m.dataDir
	rootDir := m.rootDir
	m.mu.Unlock()
	key, err := readConfiguredProxyAccessKey(dataDir, rootDir)
	if key != "" {
		return key, nil
	}
	if envKey := os.Getenv("PROXY_ACCESS_KEY"); strings.TrimSpace(envKey) != "" {
		return strings.TrimSpace(envKey), nil
	}
	if err != nil {
		return "", err
	}
	return "", nil
}

// IsSetupComplete 判断初始配置是否完成：仅当 PROXY_ACCESS_KEY 已存在时为 true。
func (m *Manager) IsSetupComplete() bool {
	m.mu.Lock()
	dataDir := m.dataDir
	m.mu.Unlock()
	key, err := readProxyAccessKey(filepath.Join(dataDir, ".env"))
	if err != nil {
		return false
	}
	return strings.TrimSpace(key) != ""
}

// GenerateProxyAccessKey 生成一个新的 PROXY_ACCESS_KEY 但不写入任何文件，仅供 UI 预览使用。
func (m *Manager) GenerateProxyAccessKey() (string, error) {
	return generateProxyAccessKey()
}

func (m *Manager) WaitHealthy(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		m.mu.Lock()
		port := m.port
		m.mu.Unlock()
		if _, err := m.fetchHealth(ctx, port); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("等待 CCX /health 超时: %w", lastErr)
			}
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	if lastErr != nil {
		return fmt.Errorf("等待 CCX /health 超时: %w", lastErr)
	}
	return fmt.Errorf("等待 CCX /health 超时")
}

func (m *Manager) findBinary() (string, error) {
	candidates := m.binaryCandidates()
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("未找到 CCX 二进制，请先运行 make build；已检查: %s", strings.Join(candidates, ", "))
}

func (m *Manager) binaryCandidates() []string {
	name := "ccx-go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	cwd, _ := os.Getwd()
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)
	return uniquePaths([]string{
		filepath.Join(exeDir, name),
		filepath.Join(exeDir, "..", name),
		filepath.Join(cwd, name),
		filepath.Join(cwd, "..", "dist", name),
		filepath.Join(cwd, "..", "backend-go", name),
		filepath.Join(m.rootDir, "dist", name),
		filepath.Join(m.rootDir, "backend-go", name),
	})
}

func (m *Manager) selectPort(ctx context.Context) (int, error) {
	conflicts := []string{}
	bindHost := configuredBindHost(m.dataDir)
	for port := m.port; port < m.port+20; port++ {
		if _, err := m.fetchHealth(ctx, port); err == nil {
			conflicts = append(conflicts, fmt.Sprintf("%d(已被另一个 CCX 健康实例占用)", port))
			continue
		}
		ln, err := net.Listen("tcp", listenAddress(bindHost, port))
		if err != nil {
			conflicts = append(conflicts, fmt.Sprintf("%d(端口被其他进程占用: %v)", port, err))
			continue
		}
		_ = ln.Close()
		return port, nil
	}
	return 0, fmt.Errorf("未找到可用端口，候选端口冲突: %s", strings.Join(conflicts, ", "))
}

func (m *Manager) findHealthyPort(ctx context.Context) (int, bool) {
	for port := m.port; port < m.port+20; port++ {
		if _, err := m.fetchHealth(ctx, port); err == nil {
			return port, true
		}
	}
	return 0, false
}

func (m *Manager) fetchHealth(ctx context.Context, port int) (map[string]any, error) {
	if port == 0 {
		return nil, fmt.Errorf("端口未设置")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.healthURL(port), nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("health 状态码异常: %d", resp.StatusCode)
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if data["status"] != "healthy" {
		return nil, fmt.Errorf("health 状态异常")
	}
	return data, nil
}

func (m *Manager) buildEnv(port int) []string {
	env := os.Environ()
	env = setEnv(env, "PORT", strconv.Itoa(port))
	env = setEnv(env, "ENABLE_WEB_UI", "true")

	// 尊重用户在 .env 中显式设置的 ENV（用于开启 debug 日志等场景）
	envPath := filepath.Join(m.dataDir, ".env")
	if userEnv := readEnvValueFromFile(envPath, "ENV"); userEnv != "" {
		env = setEnv(env, "ENV", userEnv)
	} else {
		env = setEnv(env, "ENV", "production")
	}
	for _, key := range []string{"ENABLE_HTTPS", "TLS_AUTO_CERT", "TLS_CERT_FILE", "TLS_KEY_FILE"} {
		if value := readEnvValueFromFile(envPath, key); value != "" {
			env = setEnv(env, key, value)
		}
	}
	if value, ok := readEnvValueFromFileWithPresence(envPath, "BIND_HOST"); ok {
		env = setEnv(env, "BIND_HOST", strings.TrimSpace(value))
	}

	if key, err := m.EnsureProxyAccessKey(); err == nil && key != "" {
		env = setEnv(env, "PROXY_ACCESS_KEY", key)
	} else if err != nil {
		m.appendLog("[Desktop-Backend] 生成 PROXY_ACCESS_KEY 失败: " + err.Error())
	}
	return env
}

func (m *Manager) recordStartError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = err.Error()
	m.addLogLocked("[Desktop-Backend] 启动失败: " + err.Error())
}

func (m *Manager) waitProcess(cmd *exec.Cmd, done chan error) {
	err := cmd.Wait()
	done <- err
	close(done)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != cmd {
		return
	}
	m.cmd = nil
	m.done = nil
	if err != nil && !strings.Contains(err.Error(), "signal") {
		m.lastError = err.Error()
		m.addLogLocked("[Desktop-Backend] CCX 进程退出: " + err.Error())
		return
	}
	m.addLogLocked("[Desktop-Backend] CCX 进程已退出")
}

func (m *Manager) scanLogs(stream string, pipe io.Reader) {
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		m.appendLog(fmt.Sprintf("[%s] %s", stream, scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		m.appendLog(fmt.Sprintf("[Desktop-Backend] 读取 %s 日志失败: %v", stream, err))
	}
}

func (m *Manager) appendLog(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addLogLocked(line)
}

func (m *Manager) addLogLocked(line string) {
	m.logs = append(m.logs, fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), line))
	if len(m.logs) > maxLogLines {
		m.logs = m.logs[len(m.logs)-maxLogLines:]
	}
}

func (m *Manager) setError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = err.Error()
	m.addLogLocked("[Desktop-Backend] " + err.Error())
}

func (m *Manager) isRunningLocked() bool {
	if m.attached {
		return true
	}
	return m.cmd != nil && m.cmd.Process != nil
}

func (m *Manager) urlLocked() string {
	port := m.port
	if port == 0 {
		port = defaultPort
	}
	return fmt.Sprintf("%s://%s", m.schemeLocked(), net.JoinHostPort(healthHost(configuredBindHost(m.dataDir)), strconv.Itoa(port)))
}

func (m *Manager) scheme() string {
	if isEnvHTTPS(m.dataDir) {
		return "https"
	}
	return "http"
}

func (m *Manager) schemeLocked() string {
	if isEnvHTTPS(m.dataDir) {
		return "https"
	}
	return "http"
}

func (m *Manager) healthURL(port int) string {
	return fmt.Sprintf("%s://%s/health", m.scheme(), net.JoinHostPort(healthHost(configuredBindHost(m.dataDir)), strconv.Itoa(port)))
}

func configuredBindHost(dataDir string) string {
	if value, ok := readEnvValueFromFileWithPresence(filepath.Join(dataDir, ".env"), "BIND_HOST"); ok {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(os.Getenv("BIND_HOST"))
}

func listenAddress(bindHost string, port int) string {
	host := strings.TrimSpace(bindHost)
	if host == "" {
		return fmt.Sprintf(":%d", port)
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func healthHost(bindHost string) string {
	switch strings.TrimSpace(bindHost) {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return strings.TrimSpace(bindHost)
	}
}

func isEnvHTTPS(dataDir string) bool {
	value := readEnvValueFromFile(filepath.Join(dataDir, ".env"), "ENABLE_HTTPS")
	if value != "" {
		return strings.EqualFold(value, "true")
	}
	return strings.EqualFold(os.Getenv("ENABLE_HTTPS"), "true")
}

func ensureProxyAccessKey(dataDir string, rootDir string) (string, error) {
	key, err := readConfiguredProxyAccessKey(dataDir, rootDir)
	if key != "" {
		return key, nil
	}
	if envKey := os.Getenv("PROXY_ACCESS_KEY"); strings.TrimSpace(envKey) != "" {
		return strings.TrimSpace(envKey), nil
	}
	if err != nil {
		return "", err
	}
	key, err = generateProxyAccessKey()
	if err != nil {
		return "", err
	}
	if err := appendEnvValue(filepath.Join(dataDir, ".env"), "PROXY_ACCESS_KEY", key); err != nil {
		return "", err
	}
	return key, nil
}

func readConfiguredProxyAccessKey(dataDir string, rootDir string) (string, error) {
	candidates := uniquePaths([]string{
		filepath.Join(dataDir, ".env"),
		filepath.Join(rootDir, ".env"),
		filepath.Join(rootDir, "backend-go", ".env"),
	})
	for _, candidate := range candidates {
		key, err := readProxyAccessKey(candidate)
		if err != nil {
			return "", err
		}
		if key != "" {
			return key, nil
		}
	}
	return "", nil
}

func readProxyAccessKey(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if rest, ok := strings.CutPrefix(key, "export "); ok {
			key = strings.TrimSpace(rest)
		}
		if key != "PROXY_ACCESS_KEY" {
			continue
		}
		value = strings.TrimSpace(value)
		return strings.Trim(value, `"'`), nil
	}
	return "", nil
}

func readPortFromEnvFile(path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "PORT=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "PORT="))
		value = strings.Trim(value, `"'`)
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return 0, fmt.Errorf("无效端口号: %s", value)
		}
		return port, nil
	}
	return 0, nil
}

// readEnvValueFromFile 从 .env 文件中读取指定 key 的值。
// 找不到或文件不存在时返回空字符串。
func readEnvValueFromFile(path, key string) string {
	value, _ := readEnvValueFromFileWithPresence(path, key)
	return value
}

func readEnvValueFromFileWithPresence(path, key string) (string, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	prefix := key + "="
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(rest)
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		return strings.Trim(value, `"'`), true
	}
	return "", false
}

func appendEnvValue(path string, key string, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := ""
	if existing, err := os.ReadFile(path); err == nil {
		content = string(existing)
	} else if !os.IsNotExist(err) {
		return err
	}
	line := fmt.Sprintf("%s=%s", key, value)
	if strings.TrimSpace(content) == "" {
		return os.WriteFile(path, []byte(line+"\n"), 0o600)
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += line + "\n"
	return os.WriteFile(path, []byte(content), 0o600)
}

func generateProxyAccessKey() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "ccx-" + hex.EncodeToString(buf), nil
}

func detectRootDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "backend-go", "main.go")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir || dir == "." {
			break
		}
	}
	return cwd
}

// defaultDataDir 返回与 cwd / 安装路径解耦的稳定数据目录。
// 早期版本使用 sha1(rootDir)[:10] 作为子目录，会因启动方式（Dock/Spotlight/终端 dev）
// 不同导致 cwd 变化，从而生成多个互不相通的 hash 目录，造成数据丢失。
// 现在直接落到稳定的应用数据目录，dev 与 prod 共用同一目录；Linux 存放 .env
// 与本地状态/密钥，按 XDG state 约定使用 ~/.local/state/ccx。已存在的 hash 子目录
// 不做迁移，由用户自行处理。
func defaultDataDir() string {
	return appdirs.DataDir()
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func uniquePaths(paths []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		result = append(result, clean)
	}
	return result
}
