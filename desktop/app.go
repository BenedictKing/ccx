package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/desktop/internal/backend"
	"github.com/BenedictKing/ccx/desktop/internal/channelpreset"
	"github.com/BenedictKing/ccx/desktop/internal/configservice"
	"github.com/BenedictKing/ccx/desktop/internal/editor"
	"github.com/BenedictKing/ccx/desktop/internal/uipreferences"
	"github.com/pkg/browser"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type DesktopService struct {
	manager       *backend.Manager
	configService *configservice.Service
	app           *application.App
	mainWindow    application.Window
	versionInfo   VersionInfo
}

type VersionInfo struct {
	Version             string `json:"version"`
	BuildTime           string `json:"buildTime"`
	GitCommit           string `json:"gitCommit"`
	Distribution        string `json:"distribution"`
	Platform            string `json:"platform"`
	Arch                string `json:"arch"`
	SupportsInAppUpdate bool   `json:"supportsInAppUpdate"`
}

type EnvFileState struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Exists  bool   `json:"exists"`
}

// FrontendErrorReport 描述桌面 WebView 前端上报的运行时错误。
type FrontendErrorReport struct {
	Source    string `json:"source"`
	Message   string `json:"message"`
	Stack     string `json:"stack"`
	URL       string `json:"url"`
	UserAgent string `json:"userAgent"`
}

// LanguagePreference 描述桌面语言最终选择和原始系统语言。
type LanguagePreference struct {
	Locale       string `json:"locale"`
	Manual       bool   `json:"manual"`
	SystemLocale string `json:"systemLocale"`
}

func NewDesktopService(manager *backend.Manager) *DesktopService {
	configService, err := configservice.New(manager.DataDir())
	if err != nil {
		log.Printf("[Desktop-Init] Agent 配置服务初始化失败: %v", err)
	}
	return &DesktopService{manager: manager, configService: configService}
}

func (s *DesktopService) setApp(app *application.App) {
	s.app = app
}

func (s *DesktopService) setMainWindow(window application.Window) {
	s.mainWindow = window
}

func (s *DesktopService) setVersion(v VersionInfo) {
	if v.Distribution == "" {
		v.Distribution = "github"
	}
	if v.Platform == "" {
		v.Platform = runtime.GOOS
	}
	if v.Arch == "" {
		v.Arch = runtime.GOARCH
	}
	v.SupportsInAppUpdate = supportsInAppUpdate(v.Distribution)
	s.versionInfo = v
}

func (s *DesktopService) isStoreDistribution() bool {
	return strings.EqualFold(s.versionInfo.Distribution, "store")
}

// CopyText 把文本写入系统剪贴板。
func (s *DesktopService) CopyText(text string) error {
	if s.app == nil {
		return fmt.Errorf("应用未初始化")
	}
	if !s.app.Clipboard.SetText(text) {
		return fmt.Errorf("写入剪贴板失败")
	}
	return nil
}

// WebURL 返回当前网关 Web UI 的访问地址（即使服务未启动，也基于配置端口拼接）。
func (s *DesktopService) WebURL() string {
	return s.manager.WebURL()
}

// GetVersion 返回构建时注入的版本信息。
func (s *DesktopService) GetVersion() VersionInfo {
	return s.versionInfo
}

// StartInAppUpdate 打开内置更新窗口，并启动检查、下载、安装流程。
func (s *DesktopService) StartInAppUpdate() error {
	if !supportsInAppUpdate(s.versionInfo.Distribution) {
		return fmt.Errorf("当前分发渠道不支持应用内更新")
	}
	if s.app == nil {
		return fmt.Errorf("应用未初始化")
	}
	go func() {
		if err := s.app.Updater.CheckAndInstall(context.Background()); err != nil {
			log.Printf("[Desktop-Updater] 检查更新失败: %v", err)
			s.app.Event.Emit("desktop:tray-error", fmt.Sprintf("检查更新失败: %v", err))
		}
	}()
	return nil
}

func (s *DesktopService) GetStatus() backend.Status {
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return s.manager.Status(ctx)
}

func (s *DesktopService) GetProxyAccessKey() (string, error) {
	return s.manager.EnsureProxyAccessKey()
}

// GetAdminAccessKey 返回管理 API 访问密钥。
// 优先使用 ADMIN_ACCESS_KEY，未设置时回退到 PROXY_ACCESS_KEY。
func (s *DesktopService) GetAdminAccessKey() (string, error) {
	return s.adminAccessKey()
}

// IsSetupComplete 判断是否已完成初始配置（PROXY_ACCESS_KEY 已存在）。
func (s *DesktopService) IsSetupComplete() bool {
	return s.manager.IsSetupComplete()
}

// GenerateProxyAccessKey 仅生成预览密钥，不写入任何文件。
func (s *DesktopService) GenerateProxyAccessKey() (string, error) {
	return s.manager.GenerateProxyAccessKey()
}

func (s *DesktopService) GetEnvFile() (EnvFileState, error) {
	path := filepath.Join(s.manager.DataDir(), ".env")
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return EnvFileState{Path: path, Exists: false}, nil
		}
		return EnvFileState{}, err
	}
	return EnvFileState{Path: path, Content: string(content), Exists: true}, nil
}

func (s *DesktopService) SaveEnvFile(content string) error {
	path := filepath.Join(s.manager.DataDir(), ".env")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

// DetectEditors 返回系统上可用的文本编辑器列表。
func (s *DesktopService) DetectEditors() []editor.Editor {
	return editor.Detect()
}

// OpenEnvFileInEditor 使用指定编辑器打开 .env 文件。
func (s *DesktopService) OpenEnvFileInEditor(editorPath string) error {
	path := filepath.Join(s.manager.DataDir(), ".env")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return editor.Open(editorPath, path)
}

// OpenDirectory 在系统文件管理器中打开目录。
func (s *DesktopService) OpenDirectory(dirPath string) error {
	info, err := os.Stat(dirPath)
	if err != nil {
		return fmt.Errorf("路径不存在: %w", err)
	}
	if !info.IsDir() {
		dirPath = filepath.Dir(dirPath)
	}
	return editor.OpenDirectory(dirPath)
}

// OpenFileInEditor 使用指定编辑器打开任意文件。
func (s *DesktopService) OpenFileInEditor(editorPath string, filePath string) error {
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("文件不存在: %w", err)
	}
	return editor.Open(editorPath, filePath)
}

func (s *DesktopService) StartService() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return s.manager.Start(ctx)
}

func (s *DesktopService) StopService() error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return s.manager.Stop(ctx)
}

func (s *DesktopService) RestartService() error {
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer stopCancel()
	if err := s.manager.Stop(stopCtx); err != nil {
		return err
	}

	startCtx, startCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer startCancel()
	return s.manager.Start(startCtx)
}

func (s *DesktopService) GetLogs() []string {
	return s.manager.Logs()
}

// ReportFrontendError 将真实 WebView 中捕获的前端错误写入桌面日志。
func (s *DesktopService) ReportFrontendError(report FrontendErrorReport) {
	source := normalizeFrontendLogField(report.Source, 80)
	message := normalizeFrontendLogField(report.Message, 2000)
	stack := normalizeFrontendLogField(report.Stack, 6000)
	pageURL := normalizeFrontendLogField(report.URL, 500)
	userAgent := normalizeFrontendLogField(report.UserAgent, 300)
	if message == "" && stack == "" {
		return
	}
	log.Printf("[Desktop-Frontend] source=%s url=%s userAgent=%s message=%s", source, pageURL, userAgent, message)
	if stack != "" {
		log.Printf("[Desktop-Frontend] stack=%s", stack)
	}
}

func (s *DesktopService) GetAgentConfigStatus(platform string) (configservice.AgentConfigStatus, error) {
	if s.configService == nil {
		return configservice.AgentConfigStatus{}, fmt.Errorf("配置服务未初始化")
	}
	return s.configService.GetStatus(platform, s.manager.ReadConfiguredPort())
}

func (s *DesktopService) ApplyAgentConfig(req configservice.ApplyAgentConfigRequest) error {
	if s.configService == nil {
		return fmt.Errorf("配置服务未初始化")
	}
	platform := req.Platform
	if platform == "" {
		return fmt.Errorf("agent 平台不能为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	status := s.manager.Status(ctx)
	var key string
	if platform == configservice.PlatformCodex || platform == configservice.PlatformOpenCode || (platform == configservice.PlatformClaude && (req.Provider == "" || req.Provider == configservice.ProviderCCX)) {
		if !status.Running {
			return fmt.Errorf("请先启动 CCX 服务")
		}
		var err error
		key, err = s.manager.EnsureProxyAccessKey()
		if err != nil {
			return err
		}
	}
	return s.configService.Apply(req, s.manager.ReadConfiguredPort(), key)
}

func (s *DesktopService) PreviewAgentConfigDiff(req configservice.ApplyAgentConfigRequest) (configservice.ConfigDiffResult, error) {
	if s.configService == nil {
		return configservice.ConfigDiffResult{}, fmt.Errorf("配置服务未初始化")
	}
	platform := req.Platform
	if platform == "" {
		return configservice.ConfigDiffResult{}, fmt.Errorf("agent 平台不能为空")
	}
	var key string
	if platform == configservice.PlatformCodex || platform == configservice.PlatformOpenCode || (platform == configservice.PlatformClaude && (req.Provider == "" || req.Provider == configservice.ProviderCCX)) {
		key, _ = s.manager.ReadProxyAccessKey()
	}
	return s.configService.PreviewApply(req, s.manager.ReadConfiguredPort(), key)
}

func (s *DesktopService) PreviewRestoreConfigDiff(platform string) (configservice.ConfigDiffResult, error) {
	if s.configService == nil {
		return configservice.ConfigDiffResult{}, fmt.Errorf("配置服务未初始化")
	}
	return s.configService.PreviewRestore(platform)
}

func (s *DesktopService) RestoreAgentConfig(platform string) error {
	if s.configService == nil {
		return fmt.Errorf("配置服务未初始化")
	}
	return s.configService.Restore(platform)
}

func (s *DesktopService) MigrateCodexSessions(req configservice.MigrateCodexSessionsRequest) (configservice.MigrateCodexSessionsResult, error) {
	if s.configService == nil {
		return configservice.MigrateCodexSessionsResult{}, fmt.Errorf("配置服务未初始化")
	}
	return s.configService.MigrateCodexSessions(req)
}

func (s *DesktopService) GetSavedProviderKeys() map[string]string {
	if s.configService == nil {
		return map[string]string{}
	}
	return s.configService.GetSavedProviderKeys()
}

func (s *DesktopService) GetProviderPresets(target string) []channelpreset.ProviderPreset {
	presets := channelpreset.Presets()
	target = strings.TrimSpace(target)
	if target == "" {
		return presets
	}
	for i := range presets {
		presets[i].Plans = channelpreset.FilterPlansForTarget(presets[i], target)
	}
	return presets
}

func (s *DesktopService) GetProviderKeyAssets() []configservice.ProviderKeyAsset {
	if s.configService == nil {
		return []configservice.ProviderKeyAsset{}
	}
	return s.configService.GetProviderKeyAssets()
}

func (s *DesktopService) CreateCCXChannelFromPreset(req channelpreset.CreateChannelRequest) (channelpreset.CreateChannelResult, error) {
	if s.configService == nil {
		return channelpreset.CreateChannelResult{}, fmt.Errorf("配置服务未初始化")
	}
	if preset, ok := channelpreset.FindPreset(req.Provider); ok {
		req.Provider = preset.ID
		if strings.TrimSpace(req.Target) == "" {
			req.Target = preset.DefaultTarget
		}
	}
	if strings.TrimSpace(req.APIKey) == "" {
		req.APIKey = s.savedProviderKeyForPlan(req.Provider, req.PlanID)
	}
	payload, err := channelpreset.BuildPayload(req)
	if err != nil {
		return channelpreset.CreateChannelResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.manager.Start(ctx); err != nil {
		return channelpreset.CreateChannelResult{}, err
	}
	if err := s.manager.WaitHealthy(ctx, 15*time.Second); err != nil {
		return channelpreset.CreateChannelResult{}, err
	}
	adminKey, err := s.adminAccessKey()
	if err != nil {
		return channelpreset.CreateChannelResult{}, err
	}
	updated, err := s.createChannel(ctx, req.Target, payload, adminKey)
	if err != nil {
		return channelpreset.CreateChannelResult{}, err
	}
	if err := s.configService.SaveProviderKeyAsset(configservice.ProviderKeyAsset{
		Provider: req.Provider,
		APIKey:   payload.APIKeys[0],
		BaseURL:  payload.BaseURL,
		PlanID:   req.PlanID,
		ProxyURL: payload.ProxyURL,
		Usages:   []string{req.Target + "-channel"},
	}); err != nil {
		return channelpreset.CreateChannelResult{}, err
	}
	message := "渠道已添加到 CCX"
	if updated {
		message = "同名渠道配置已覆盖更新"
	}
	return channelpreset.CreateChannelResult{
		Provider: req.Provider,
		Target:   req.Target,
		Name:     payload.Name,
		BaseURL:  payload.BaseURL,
		Message:  message,
	}, nil
}

func (s *DesktopService) adminAccessKey() (string, error) {
	env, err := s.GetEnvFile()
	if err != nil {
		return "", err
	}
	values := parseEnvContent(env.Content)
	if key := strings.TrimSpace(values["ADMIN_ACCESS_KEY"]); key != "" {
		return key, nil
	}
	if key := strings.TrimSpace(values["PROXY_ACCESS_KEY"]); key != "" {
		return key, nil
	}
	return s.manager.EnsureProxyAccessKey()
}

// createChannel 创建或更新渠道，返回 (是否更新, error)。
func (s *DesktopService) createChannel(ctx context.Context, target string, payload channelpreset.ChannelPayload, adminKey string) (bool, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	baseURL := s.manager.WebURL()
	path := "/api/" + strings.TrimSpace(target) + "/channels"
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 仅连接本机 CCX，兼容本地自签名 HTTPS。
		},
	}

	// 查找同名渠道，存在则覆盖更新
	if existingID, err := s.findChannelIndexByName(ctx, client, baseURL, path, payload.Name, adminKey); err == nil && existingID >= 0 {
		return true, s.putChannel(ctx, client, baseURL, fmt.Sprintf("%s/%d", path, existingID), body, adminKey)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", adminKey)
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(data))
	if message == "" {
		message = resp.Status
	}
	return false, fmt.Errorf("创建 CCX %s 渠道失败: %s", target, message)
}

func (s *DesktopService) findChannelIndexByName(ctx context.Context, client *http.Client, baseURL, path, name, adminKey string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return -1, err
	}
	req.Header.Set("x-api-key", adminKey)
	resp, err := client.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return -1, fmt.Errorf("查询渠道列表失败: %s", resp.Status)
	}
	var result struct {
		Channels []struct {
			Index int    `json:"index"`
			Name  string `json:"name"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return -1, err
	}
	for _, ch := range result.Channels {
		if ch.Name == name {
			return ch.Index, nil
		}
	}
	return -1, nil
}

func (s *DesktopService) savedProviderKeyForPlan(provider string, planID string) string {
	if s.configService == nil {
		return ""
	}
	provider = strings.TrimSpace(provider)
	planID = strings.TrimSpace(planID)
	if provider == "" {
		return ""
	}

	assets := s.configService.GetProviderKeyAssets()
	if key := savedProviderKeyForPlanFromAssets(assets, provider, planID); key != "" {
		return key
	}
	if provider == channelpreset.ProviderOpenCodeZen {
		return savedOpenCodeKeyForPlanFromAssets(assets, planID)
	}
	return ""
}

func savedProviderKeyForPlanFromAssets(assets []configservice.ProviderKeyAsset, provider string, planID string) string {
	var legacyKey string
	var firstKey string
	hasPlanScopedAsset := false
	for _, asset := range assets {
		if asset.Provider != provider || strings.TrimSpace(asset.APIKey) == "" {
			continue
		}
		key := strings.TrimSpace(asset.APIKey)
		assetPlanID := strings.TrimSpace(asset.PlanID)
		if firstKey == "" {
			firstKey = key
		}
		if assetPlanID != "" {
			hasPlanScopedAsset = true
		}
		if planID != "" && assetPlanID == planID {
			return key
		}
		if assetPlanID == "" && legacyKey == "" {
			legacyKey = key
		}
	}

	if planID != "" {
		if hasPlanScopedAsset {
			return ""
		}
		return legacyKey
	}
	if legacyKey != "" {
		return legacyKey
	}
	return firstKey
}

func savedOpenCodeKeyForPlanFromAssets(assets []configservice.ProviderKeyAsset, planID string) string {
	if isOpenCodeGoChannelPlan(planID) {
		if key := savedProviderKeyForPlanFromAssets(assets, channelpreset.ProviderOpenCodeGo, legacyOpenCodeGoPlanID(planID)); key != "" {
			return key
		}
	}
	if key := savedProviderKeyForPlanFromAssets(assets, channelpreset.ProviderOpenCodeZen, ""); key != "" {
		return key
	}
	return savedProviderKeyForPlanFromAssets(assets, channelpreset.ProviderOpenCodeGo, "")
}

func isOpenCodeGoChannelPlan(planID string) bool {
	return strings.HasPrefix(strings.TrimSpace(planID), "go-")
}

func legacyOpenCodeGoPlanID(planID string) string {
	planID = strings.TrimSpace(planID)
	if strings.HasPrefix(planID, "go-") {
		return strings.TrimPrefix(planID, "go-")
	}
	return planID
}

func (s *DesktopService) putChannel(ctx context.Context, client *http.Client, baseURL, path string, body []byte, adminKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", adminKey)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(data))
	if message == "" {
		message = resp.Status
	}
	return fmt.Errorf("更新 CCX 渠道失败: %s", message)
}

func parseEnvContent(content string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if rest, ok := strings.CutPrefix(key, "export "); ok {
			key = strings.TrimSpace(rest)
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		values[key] = value
	}
	return values
}

func (s *DesktopService) ShowStatusTab() error {
	s.showWindow()
	if s.app != nil {
		s.app.Event.Emit("desktop:show-tab", "status")
	}
	return nil
}

func (s *DesktopService) ShowAgentTab() error {
	s.showWindow()
	if s.app != nil {
		s.app.Event.Emit("desktop:show-tab", "agent")
	}
	return nil
}

func (s *DesktopService) ShowWebUITab() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.manager.Start(ctx); err != nil {
		return err
	}
	if err := s.manager.WaitHealthy(ctx, 15*time.Second); err != nil {
		return err
	}
	s.showWindow()
	if s.app != nil {
		s.app.Event.Emit("desktop:show-tab", "dashboard")
	}
	return nil
}

func (s *DesktopService) OpenWebUIInBrowser() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.manager.Start(ctx); err != nil {
		return err
	}
	if err := s.manager.WaitHealthy(ctx, 15*time.Second); err != nil {
		return err
	}
	webURL, err := url.Parse(s.manager.WebURL())
	if err != nil {
		return err
	}
	return browser.OpenURL(webURL.String())
}

func (s *DesktopService) GetAutostartStatus() (bool, error) {
	if s.app == nil {
		return false, fmt.Errorf("应用未初始化")
	}
	return s.app.Autostart.IsEnabled()
}

func (s *DesktopService) SetAutostart(enabled bool) error {
	if s.app == nil {
		return fmt.Errorf("应用未初始化")
	}
	if enabled {
		return s.app.Autostart.Enable()
	}
	return s.app.Autostart.Disable()
}

// GetLanguagePreference 返回当前桌面语言偏好，供前端初始化多语言。
func (s *DesktopService) GetLanguagePreference() (LanguagePreference, error) {
	prefs, exists, err := uipreferences.Load(s.manager.DataDir())
	if err != nil {
		return LanguagePreference{}, err
	}
	sys := detectSystemLocale()
	resolved := uipreferences.NormalizeLocale(sys)
	manual := false
	if exists {
		resolved = prefs.Locale
		manual = prefs.Manual
	}
	return LanguagePreference{
		Locale:       resolved,
		Manual:       manual,
		SystemLocale: sys,
	}, nil
}

// SaveLanguagePreference 手动保存用户选择的语言。
func (s *DesktopService) SaveLanguagePreference(locale string) error {
	normalized := uipreferences.NormalizeLocale(locale)
	if normalized == "" {
		normalized = uipreferences.LocaleEnglish
	}
	return uipreferences.Save(s.manager.DataDir(), uipreferences.Preferences{
		Locale: normalized,
		Manual: true,
	})
}

func (s *DesktopService) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = s.manager.Stop(ctx)
}

func normalizeFrontendLogField(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "\n", " | ")
	if len(value) > maxLen {
		return value[:maxLen] + "...(truncated)"
	}
	return value
}

func (s *DesktopService) showWindow() {
	if s.mainWindow == nil {
		return
	}
	if s.mainWindow.IsMinimised() {
		s.mainWindow.UnMinimise()
	}
	s.mainWindow.Show()
	s.mainWindow.Focus()
}
