package configservice

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) getClaudeStatus(port int) (AgentConfigStatus, error) {
	path := s.claudeSettingsPath()
	target := s.claudeBaseURL(port)
	status := AgentConfigStatus{
		Platform:       PlatformClaude,
		Provider:       ProviderCustom,
		TargetProvider: ProviderCCX,
		TargetBaseURL:  target,
		ConfigPath:     path,
		HasState:       fileExists(s.claudeStatePath()),
	}
	data, _, err := readJSONMap(path)
	if err != nil {
		status.LastError = err.Error()
		return status, nil
	}
	baseURL, _ := getNestedString(data, "env", "ANTHROPIC_BASE_URL")
	status.CurrentBaseURL = baseURL
	status.Provider = detectClaudeProvider(baseURL)
	status.MatchesCurrentPort = baseURL == target
	status.Configured = status.MatchesCurrentPort || status.Provider == ProviderDeepSeek || status.Provider == ProviderMiMo || status.Provider == ProviderXFyun || status.Provider == ProviderSenseNova
	status.NeedsUpdate = baseURL != "" && isLocalBaseURL(baseURL) && !status.MatchesCurrentPort
	return status, nil
}

func (s *Service) applyClaude(req ApplyAgentConfigRequest, port int, accessKey string) error {
	provider := normalizeClaudeProvider(req.Provider)
	baseURL, authToken, apiKey, err := resolveClaudeProvider(req, port, accessKey, s.localHTTPS())
	if err != nil {
		return err
	}
	path := s.claudeSettingsPath()
	data, existed, err := readJSONMap(path)
	if err != nil {
		return err
	}
	env, envExisted := data["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
		data["env"] = env
	}
	originalBaseURL, baseOK := env["ANTHROPIC_BASE_URL"].(string)
	originalAuthToken, authOK := env["ANTHROPIC_AUTH_TOKEN"].(string)
	originalAPIKey, apiOK := env["ANTHROPIC_API_KEY"].(string)
	originalModel, modelOK := env["ANTHROPIC_MODEL"].(string)
	originalSmallFast, smallFastOK := env["ANTHROPIC_SMALL_FAST_MODEL"].(string)
	state := ClaudeProxyState{
		Version:           stateVersion,
		TargetPath:        path,
		FileExisted:       existed,
		EnvExisted:        envExisted,
		OriginalBaseURL:   optionalString(originalBaseURL, baseOK),
		OriginalAuthToken: optionalString(originalAuthToken, authOK),
		OriginalAPIKey:    optionalString(originalAPIKey, apiOK),
		OriginalModel:     optionalString(originalModel, modelOK),
		OriginalSmallFast: optionalString(originalSmallFast, smallFastOK),
		InjectedProvider:  provider,
		InjectedBaseURL:   baseURL,
		InjectedAuthToken: authToken,
		InjectedAPIKey:    apiKey,
	}
	if existing, ok := s.readClaudeState(); ok {
		state = existing
		state.InjectedProvider = provider
		state.InjectedBaseURL = baseURL
		state.InjectedAuthToken = authToken
		state.InjectedAPIKey = apiKey
	}
	if provider != ProviderCCX && req.APIKey != "" {
		if err := s.saveProviderKey(PlatformClaude, provider, req.APIKey); err != nil {
			return err
		}
	}
	if err := writeJSONAtomic(s.claudeStatePath(), state); err != nil {
		return err
	}
	env["ANTHROPIC_BASE_URL"] = state.InjectedBaseURL
	if state.InjectedAuthToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = state.InjectedAuthToken
	} else {
		delete(env, "ANTHROPIC_AUTH_TOKEN")
	}
	if state.InjectedAPIKey != "" {
		env["ANTHROPIC_API_KEY"] = state.InjectedAPIKey
	} else {
		delete(env, "ANTHROPIC_API_KEY")
	}
	applyClaudeProviderModelEnv(env, provider, state.OriginalModel, state.OriginalSmallFast)
	if err := writeJSONAtomic(path, data); err != nil {
		return err
	}
	if provider != ProviderCCX {
		return nil
	}
	return mergeJSONFile(s.claudeRootConfigPath(), map[string]any{"hasCompletedOnboarding": true})
}

func (s *Service) restoreClaude() error {
	var state ClaudeProxyState
	if err := readJSONFile(s.claudeStatePath(), &state); err != nil {
		return err
	}
	if !state.FileExisted {
		if err := os.Remove(state.TargetPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return os.Remove(s.claudeStatePath())
	}
	data, _, err := readJSONMap(state.TargetPath)
	if err != nil {
		return err
	}
	env, _ := data["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
		data["env"] = env
	}
	restoreStringField(env, "ANTHROPIC_BASE_URL", state.OriginalBaseURL)
	restoreStringField(env, "ANTHROPIC_AUTH_TOKEN", state.OriginalAuthToken)
	restoreStringField(env, "ANTHROPIC_API_KEY", state.OriginalAPIKey)
	restoreStringField(env, "ANTHROPIC_MODEL", state.OriginalModel)
	restoreStringField(env, "ANTHROPIC_SMALL_FAST_MODEL", state.OriginalSmallFast)
	if !state.EnvExisted && len(env) == 0 {
		delete(data, "env")
	}
	if err := writeJSONAtomic(state.TargetPath, data); err != nil {
		return err
	}
	return os.Remove(s.claudeStatePath())
}

func applyClaudeProviderModelEnv(env map[string]any, provider string, originalModel *string, originalSmallFast *string) {
	if provider == ProviderXFyun {
		env["ANTHROPIC_MODEL"] = "astron-code-latest"
		env["ANTHROPIC_SMALL_FAST_MODEL"] = "astron-code-latest"
		return
	}
	if provider == ProviderSenseNova {
		env["ANTHROPIC_MODEL"] = "sensenova-6.7-flash-lite"
		env["ANTHROPIC_SMALL_FAST_MODEL"] = "sensenova-6.7-flash-lite"
		return
	}
	restoreStringField(env, "ANTHROPIC_MODEL", originalModel)
	restoreStringField(env, "ANTHROPIC_SMALL_FAST_MODEL", originalSmallFast)
}

func (s *Service) readClaudeState() (ClaudeProxyState, bool) {
	var state ClaudeProxyState
	if err := readJSONFile(s.claudeStatePath(), &state); err != nil {
		return ClaudeProxyState{}, false
	}
	return state, true
}

func claudeBaseURL(port int) string {
	return localBaseURL(port, false)
}

func (s *Service) claudeBaseURL(port int) string {
	return localBaseURL(port, s.localHTTPS())
}

func localBaseURL(port int, https bool) string {
	scheme := "http"
	if https {
		scheme = "https"
	}
	return fmt.Sprintf("%s://127.0.0.1:%d", scheme, port)
}

func (s *Service) localHTTPS() bool {
	dataDir := filepath.Dir(s.stateDir)
	value := readEnvValueFromFile(filepath.Join(dataDir, ".env"), "ENABLE_HTTPS")
	if value != "" {
		return strings.EqualFold(value, "true")
	}
	return strings.EqualFold(os.Getenv("ENABLE_HTTPS"), "true")
}

func normalizeClaudeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", ProviderCCX:
		return ProviderCCX
	case ProviderDeepSeek:
		return ProviderDeepSeek
	case ProviderMiMo:
		return ProviderMiMo
	case ProviderKimi:
		return ProviderKimi
	case ProviderGLM:
		return ProviderGLM
	case ProviderSenseNova:
		return ProviderSenseNova
	case ProviderMiniMax:
		return ProviderMiniMax
	case ProviderDashScope:
		return ProviderDashScope
	case ProviderOpenCodeZen:
		return ProviderOpenCodeZen
	case ProviderOpenCodeGo:
		return ProviderOpenCodeGo
	case ProviderXFyun:
		return ProviderXFyun
	case ProviderTencentLkeap:
		return ProviderTencentLkeap
	case ProviderVolcArk:
		return ProviderVolcArk
	case ProviderQianfan:
		return ProviderQianfan
	default:
		return provider
	}
}

func resolveClaudeProvider(req ApplyAgentConfigRequest, port int, accessKey string, localHTTPS bool) (string, string, string, error) {
	provider := normalizeClaudeProvider(req.Provider)
	switch provider {
	case ProviderCCX:
		if port == 0 {
			return "", "", "", fmt.Errorf("CCX 端口未设置")
		}
		if accessKey == "" {
			return "", "", "", fmt.Errorf("PROXY_ACCESS_KEY 为空")
		}
		return localBaseURL(port, localHTTPS), accessKey, "", nil
	case ProviderDeepSeek:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("DeepSeek API Key 不能为空")
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = deepSeekClaudeBaseURL
		}
		return baseURL, apiKey, "", nil
	case ProviderMiMo:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("MiMo API Key 不能为空")
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = defaultMiMoBaseURL
		}
		return baseURL, apiKey, "", nil
	case ProviderKimi:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("Kimi API Key 不能为空")
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = kimiClaudeBaseURL
		}
		return baseURL, apiKey, "", nil
	case ProviderGLM:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("GLM API Key 不能为空")
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = glmClaudeBaseURL
		}
		return baseURL, apiKey, "", nil
	case ProviderSenseNova:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("SenseNova API Key 不能为空")
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = sensenovaClaudeBaseURL
		}
		return baseURL, apiKey, "", nil
	case ProviderMiniMax:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("MiniMax API Key 不能为空")
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = miniMaxClaudeBaseURL
		}
		return baseURL, apiKey, "", nil
	case ProviderDashScope:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("DashScope API Key 不能为空")
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = dashScopeClaudeBaseURL
		}
		return baseURL, apiKey, "", nil
	case ProviderOpenCodeZen:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("OpenCode Zen API Key 不能为空")
		}
		return openCodeZenClaudeBaseURL, apiKey, "", nil
	case ProviderOpenCodeGo:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("OpenCode Go API Key 不能为空")
		}
		return openCodeGoClaudeBaseURL, apiKey, "", nil
	case ProviderXFyun:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("讯飞星辰 API Key 不能为空")
		}
		return xfyunClaudeBaseURL, apiKey, "", nil
	case ProviderTencentLkeap:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("腾讯云 TokenHub API Key 不能为空")
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = tencentLkeapClaudeBaseURL
		}
		return baseURL, apiKey, "", nil
	case ProviderVolcArk:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("火山方舟 API Key 不能为空")
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = volcArkClaudeBaseURL
		}
		return baseURL, apiKey, "", nil
	case ProviderQianfan:
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", fmt.Errorf("百度千帆 API Key 不能为空")
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = qianfanClaudeBaseURL
		}
		return baseURL, apiKey, "", nil
	default:
		return "", "", "", fmt.Errorf("不支持的 Claude Code provider: %s", provider)
	}
}

func detectClaudeProvider(baseURL string) string {
	value := strings.ToLower(strings.TrimSpace(baseURL))
	switch {
	case value == "":
		return ""
	case isLocalBaseURL(value):
		return ProviderCCX
	case strings.Contains(value, "deepseek.com"):
		return ProviderDeepSeek
	case strings.Contains(value, "mimo.xiaomi.com") || strings.Contains(value, "xiaomimimo.com"):
		return ProviderMiMo
	case strings.Contains(value, "moonshot.cn"):
		return ProviderKimi
	case strings.Contains(value, "bigmodel.cn"):
		return ProviderGLM
	case strings.Contains(value, "sensenova.cn"):
		return ProviderSenseNova
	case strings.Contains(value, "minimaxi.com") || strings.Contains(value, "minimax.chat"):
		return ProviderMiniMax
	case strings.Contains(value, "dashscope.aliyuncs.com"):
		return ProviderDashScope
	case strings.Contains(value, "opencode.ai/zen/go"):
		return ProviderOpenCodeGo
	case strings.Contains(value, "opencode.ai/zen"):
		return ProviderOpenCodeZen
	case strings.Contains(value, "xf-yun.com"):
		return ProviderXFyun
	case strings.Contains(value, "lkeap.cloud.tencent.com"):
		return ProviderTencentLkeap
	case strings.Contains(value, "volces.com"):
		return ProviderVolcArk
	case strings.Contains(value, "baidubce.com"):
		return ProviderQianfan
	default:
		return ProviderCustom
	}
}
