package configservice

import (
	"fmt"
	"os"
	"strings"
)

func (s *Service) getCodexStatus(port int) (AgentConfigStatus, error) {
	path := s.codexConfigPath()
	authPath := s.codexAuthPath()
	target := codexBaseURL(port)
	status := AgentConfigStatus{
		Platform:         PlatformCodex,
		Provider:         ProviderCustom,
		TargetProvider:   ProviderCCX,
		TargetBaseURL:    target,
		ConfigPath:       path,
		AuthPath:         authPath,
		HasState:         fileExists(s.codexStatePath()),
		ConfigConsistent: true,
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return status, nil
		}
		status.LastError = err.Error()
		return status, nil
	}
	text := string(content)
	ccxBlockBaseURL, _ := extractTomlStringField(extractCodexProviderBlock(text), "base_url")
	modelProvider, _ := extractTopLevelTomlString(text, "model_provider")
	openaiBaseURL, _ := extractTopLevelTomlString(text, "openai_base_url")

	// 兼容新旧两种 CCX proxy 格式
	isNewStyleCCX := strings.EqualFold(modelProvider, "openai") && isLocalBaseURL(openaiBaseURL)
	isOldStyleCCX := strings.EqualFold(modelProvider, ProviderCCX)

	// 检测插件模式
	isNativeCCX := false
	isLegacyQuickCCX := false
	ccxBearerToken := ""
	if isOldStyleCCX {
		ccxBlock, hasBlock := extractNamedTomlBlock(text, "model_providers.ccx")
		if hasBlock {
			requiresOpenAIAuth, hasRequiresOpenAIAuth := extractTomlBoolField(ccxBlock, "requires_openai_auth")
			if hasRequiresOpenAIAuth && requiresOpenAIAuth {
				isNativeCCX = true
			}
			if _, hasEnvKey := extractTomlStringField(ccxBlock, "env_key"); hasEnvKey || (hasRequiresOpenAIAuth && !requiresOpenAIAuth) {
				isLegacyQuickCCX = true
			}
			if bearerToken, hasBearerToken := extractTomlStringField(ccxBlock, "experimental_bearer_token"); hasBearerToken {
				ccxBearerToken = strings.TrimSpace(bearerToken)
			}
		}
	}

	// 检测第三方 provider 快捷模式：model_provider="openai" + 非本地 openai_base_url
	isThirdPartyQuickMode := false
	var thirdPartyProvider string
	if strings.EqualFold(modelProvider, "openai") && openaiBaseURL != "" && !isLocalBaseURL(openaiBaseURL) {
		if tp, ok := codexThirdPartyQuickBaseURL(openaiBaseURL); ok {
			isThirdPartyQuickMode = true
			thirdPartyProvider = tp
		}
	}

	// 旧格式优先取 [model_providers.ccx].base_url，新格式取 openai_base_url
	effectiveBaseURL := ccxBlockBaseURL
	if effectiveBaseURL == "" {
		effectiveBaseURL = openaiBaseURL
	}

	normalized := normalizeCodexProvider(modelProvider)
	if isNewStyleCCX || isNativeCCX || isOldStyleCCX {
		status.Provider = ProviderCCX
		if isNativeCCX || ccxBearerToken != "" {
			status.Mode = "plugin"
		} else if isOldStyleCCX {
			status.Mode = "quick"
		}
	} else if isThirdPartyQuickMode {
		status.Provider = thirdPartyProvider
		status.Mode = "quick"
		status.TargetProvider = thirdPartyProvider
	} else {
		status.Provider = normalized
	}
	if status.Provider != ProviderCCX && !isThirdPartyQuickMode {
		status.TargetProvider = status.Provider
	}
	if normalized == ProviderOpenAI && !isNewStyleCCX && !isThirdPartyQuickMode {
		status.TargetBaseURL = ""
	}
	if isThirdPartyQuickMode {
		status.TargetBaseURL = openaiBaseURL
	}
	status.CurrentBaseURL = effectiveBaseURL
	status.MatchesCurrentPort = (isNewStyleCCX || isOldStyleCCX) && effectiveBaseURL == target
	status.Configured = status.MatchesCurrentPort || (normalized == ProviderOpenAI && !isNewStyleCCX && !isThirdPartyQuickMode) || isCodexThirdPartyProvider(normalized) || isThirdPartyQuickMode
	status.NeedsUpdate = (isOldStyleCCX || isLocalBaseURL(effectiveBaseURL)) && !status.MatchesCurrentPort

	s.diagnoseCodexConfigAuth(&status, text, modelProvider, openaiBaseURL, isNewStyleCCX, isOldStyleCCX, isNativeCCX, isLegacyQuickCCX, ccxBearerToken)
	return status, nil
}

// diagnoseCodexConfigAuth 识别 config.toml 与 auth.json 的常见语义冲突。
// 这类冲突多由 CCS 等工具或手工编辑导致：config.toml 改成指向本地 CCX，
// 但 auth.json 的 auth_mode / OPENAI_API_KEY 没有同步，运行时表现为上游 503/鉴权失败。
// 仅针对“当前意图指向本地 CCX”的配置做判定，避免误报第三方直连。

func (s *Service) diagnoseCodexConfigAuth(
	status *AgentConfigStatus,
	configText string,
	modelProvider string,
	openaiBaseURL string,
	isNewStyleCCX bool,
	isOldStyleCCX bool,
	isNativeCCX bool,
	isLegacyQuickCCX bool,
	ccxBearerToken string,
) {
	// 默认视为一致，无法读取 auth.json 时不武断报错。
	status.ConfigConsistent = true

	authData, authExisted, err := readJSONMap(status.AuthPath)
	if err != nil {
		// auth.json 存在但解析失败本身就是污染信号。
		status.ConfigConsistent = false
		status.DiagnosticCode = "codex.auth_unreadable"
		status.DiagnosticMessage = "auth.json 解析失败，可能已损坏；建议重新应用 Codex -> CCX 配置"
		return
	}

	authMode, _ := authData["auth_mode"].(string)
	status.AuthMode = authMode
	apiKey, _ := authData["OPENAI_API_KEY"].(string)
	apiKey = strings.TrimSpace(apiKey)
	expectedProxyKey := strings.TrimSpace(s.readCurrentProxyAccessKey())

	// 仅诊断“指向本地 CCX”的两类配置；第三方/OpenAI 直连不在此判定范围。
	pointsToLocalCCX := isNewStyleCCX || isOldStyleCCX

	switch {
	case isNativeCCX || ccxBearerToken != "":
		// 插件模式用 experimental_bearer_token 调用上游；保留用户已有的 Codex 认证方式。
		if ccxBearerToken == "" {
			status.ConfigConsistent = false
			status.DiagnosticCode = "codex.plugin_missing_bearer"
			status.DiagnosticMessage = "插件模式缺少 experimental_bearer_token；建议重新应用 Codex -> CCX 插件模式"
			return
		}
		if expectedProxyKey != "" && ccxBearerToken != expectedProxyKey {
			status.ConfigConsistent = false
			status.DiagnosticCode = "codex.proxy_key_mismatch"
			status.DiagnosticMessage = "config.toml 中的 experimental_bearer_token 与当前 CCX 代理密钥不一致；建议重新应用 Codex -> CCX 配置"
			return
		}
		if strings.EqualFold(authMode, "apikey") && apiKey == "" {
			status.ConfigConsistent = false
			status.DiagnosticCode = "codex.missing_api_key"
			status.DiagnosticMessage = "Codex API Key 认证缺少 OPENAI_API_KEY；请在 Codex 中完成认证"
			return
		}
		if !strings.EqualFold(authMode, "chatgpt") && !strings.EqualFold(authMode, "apikey") {
			status.ConfigConsistent = false
			status.DiagnosticCode = "codex.auth_mode_mismatch"
			status.DiagnosticMessage = "Codex 认证方式无效：" + authMode + "；请在 Codex 中完成认证"
			return
		}
	case isLegacyQuickCCX:
		// 旧式 quick 配置：model_provider="ccx" + [model_providers.ccx]（env_key/requires_openai_auth=false），
		// 仍由 OPENAI_API_KEY 驱动，可正常工作；缺 key 或 auth_mode 缺失/错误时才需提示。
		if !authExisted || apiKey == "" {
			status.ConfigConsistent = false
			status.DiagnosticCode = "codex.missing_api_key"
			status.DiagnosticMessage = "config.toml 指向本地 CCX，但 auth.json 缺少 OPENAI_API_KEY；建议重新应用 Codex -> CCX 配置"
			return
		}
		if expectedProxyKey != "" && apiKey != expectedProxyKey {
			status.ConfigConsistent = false
			status.DiagnosticCode = "codex.proxy_key_mismatch"
			status.DiagnosticMessage = "auth.json 中的 OPENAI_API_KEY 与当前 CCX 代理密钥不一致；建议重新应用 Codex -> CCX 配置"
			return
		}
		if !strings.EqualFold(authMode, "apikey") {
			status.ConfigConsistent = false
			status.DiagnosticCode = "codex.auth_mode_mismatch"
			status.DiagnosticMessage = "快捷模式要求 auth.json 的 auth_mode 为 apikey，但当前为 " + authMode + "；建议重新应用 Codex -> CCX 配置"
			return
		}
	case isNewStyleCCX:
		// 快捷模式：openai_base_url 指向本地 CCX，需要 OPENAI_API_KEY + auth_mode=apikey。
		if !authExisted || apiKey == "" {
			status.ConfigConsistent = false
			status.DiagnosticCode = "codex.missing_api_key"
			status.DiagnosticMessage = "config.toml 指向本地 CCX，但 auth.json 缺少 OPENAI_API_KEY；建议重新应用 Codex -> CCX 配置"
			return
		}
		if expectedProxyKey != "" && apiKey != expectedProxyKey {
			status.ConfigConsistent = false
			status.DiagnosticCode = "codex.proxy_key_mismatch"
			status.DiagnosticMessage = "auth.json 中的 OPENAI_API_KEY 与当前 CCX 代理密钥不一致；建议重新应用 Codex -> CCX 配置"
			return
		}
		if !strings.EqualFold(authMode, "apikey") {
			status.ConfigConsistent = false
			status.DiagnosticCode = "codex.auth_mode_mismatch"
			status.DiagnosticMessage = "快捷模式要求 auth.json 的 auth_mode 为 apikey，但当前为 " + authMode + "；建议重新应用 Codex -> CCX 配置"
			return
		}
	case isOldStyleCCX:
		// 旧格式 model_provider="ccx"，但既不是插件块也不是旧式 quick 块，说明配置残缺。
		status.ConfigConsistent = false
		status.DiagnosticCode = "codex.legacy_incomplete"
		status.DiagnosticMessage = "config.toml 使用旧式 ccx provider 但缺少必要字段；建议重新应用 Codex -> CCX 配置"
		return
	}

	// model_provider 指向本地但格式无法识别为任何已知 CCX 形态：典型的 CCS 污染。
	if !pointsToLocalCCX && isLocalBaseURL(openaiBaseURL) && !strings.EqualFold(modelProvider, "openai") {
		status.ConfigConsistent = false
		status.DiagnosticCode = "codex.config_polluted"
		status.DiagnosticMessage = "config.toml 指向本地端口但 model_provider 配置异常；建议先恢复再重新应用 Codex -> CCX 配置"
	}
}

func (s *Service) applyCodex(port int, accessKey string, mode string) error {
	if mode == "plugin" {
		return s.applyCodexPlugin(port, accessKey)
	}
	return s.applyCodexQuick(port, accessKey)
}

func (s *Service) applyCodexQuick(port int, accessKey string) error {
	configPath := s.codexConfigPath()
	authPath := s.codexAuthPath()
	configContent, configExisted, err := readTextFile(configPath)
	if err != nil {
		return err
	}
	authData, authExisted, err := readJSONMap(authPath)
	if err != nil {
		return err
	}
	modelProvider, mpOK := extractTopLevelTomlString(configContent, "model_provider")
	providerBlock, blockOK := extractNamedTomlBlock(configContent, "model_providers.ccx")
	openaiBaseURL, obOK := extractTopLevelTomlString(configContent, "openai_base_url")
	apiKey, keyOK := authData["OPENAI_API_KEY"].(string)
	state := CodexProxyState{
		Version:               stateVersion,
		ConfigPath:            configPath,
		AuthPath:              authPath,
		ConfigFileExisted:     configExisted,
		AuthFileExisted:       authExisted,
		OriginalModelProvider: optionalString(modelProvider, mpOK),
		OriginalProviderBlock: optionalString(providerBlock, blockOK),
		OriginalOpenAIAPIKey:  optionalString(apiKey, keyOK),
		OriginalOpenAIBaseURL: optionalString(openaiBaseURL, obOK),
		InjectedBaseURL:       codexBaseURL(port),
		InjectedAPIKey:        accessKey,
	}
	if existing, ok := s.readCodexState(); ok {
		state = existing
		state.InjectedBaseURL = codexBaseURL(port)
		state.InjectedAPIKey = accessKey
	}
	if err := writeJSONAtomic(s.codexStatePath(), state); err != nil {
		return err
	}
	// config.toml: model_provider = "openai" + openai_base_url
	updated := upsertTopLevelTomlString(configContent, "model_provider", "openai")
	updated = upsertTopLevelTomlString(updated, "openai_base_url", codexBaseURL(port))
	// 清理插件模式残留
	updated = restoreNamedTomlBlock(updated, "model_providers.ccx", nil)
	if err := writeTextAtomic(configPath, updated); err != nil {
		return err
	}
	// auth.json: OPENAI_API_KEY = accessKey, auth_mode = "apikey"
	authData["OPENAI_API_KEY"] = accessKey
	authData["auth_mode"] = "apikey"
	return writeJSONAtomic(authPath, authData)
}

func (s *Service) applyCodexPlugin(port int, accessKey string) error {
	configPath := s.codexConfigPath()
	authPath := s.codexAuthPath()
	configContent, configExisted, err := readTextFile(configPath)
	if err != nil {
		return err
	}
	authData, authExisted, err := readJSONMap(authPath)
	if err != nil {
		return err
	}
	modelProvider, mpOK := extractTopLevelTomlString(configContent, "model_provider")
	providerBlock, blockOK := extractNamedTomlBlock(configContent, "model_providers.ccx")
	openaiBaseURL, obOK := extractTopLevelTomlString(configContent, "openai_base_url")
	apiKey, keyOK := authData["OPENAI_API_KEY"].(string)
	state := CodexProxyState{
		Version:               stateVersion,
		ConfigPath:            configPath,
		AuthPath:              authPath,
		ConfigFileExisted:     configExisted,
		AuthFileExisted:       authExisted,
		OriginalModelProvider: optionalString(modelProvider, mpOK),
		OriginalProviderBlock: optionalString(providerBlock, blockOK),
		OriginalOpenAIAPIKey:  optionalString(apiKey, keyOK),
		OriginalOpenAIBaseURL: optionalString(openaiBaseURL, obOK),
		InjectedProvider:      ProviderCCX,
		InjectedBaseURL:       codexBaseURL(port),
		InjectedAPIKey:        accessKey,
	}
	if existing, ok := s.readCodexState(); ok {
		state = existing
		state.InjectedProvider = ProviderCCX
		state.InjectedBaseURL = codexBaseURL(port)
		state.InjectedAPIKey = accessKey
	}
	if err := writeJSONAtomic(s.codexStatePath(), state); err != nil {
		return err
	}
	// config.toml: model_provider = "ccx" + [model_providers.ccx] 块 + experimental_bearer_token
	block := fmt.Sprintf(`[model_providers.ccx]
name = "CCX Proxy"
base_url = %q
wire_api = "responses"
requires_openai_auth = true
experimental_bearer_token = %q
`, codexBaseURL(port), accessKey)
	updated := upsertTopLevelTomlString(configContent, "model_provider", "ccx")
	updated = restoreTopLevelTomlString(updated, "openai_base_url", nil)
	updated = restoreNamedTomlBlock(updated, "model_providers.ccx", nil)
	updated = upsertNamedTomlBlock(updated, "model_providers.ccx", block)
	if err := writeTextAtomic(configPath, updated); err != nil {
		return err
	}
	return ensureCodexPluginAuth(authPath, authData, authExisted, accessKey)
}

func ensureCodexPluginAuth(authPath string, authData map[string]any, authExisted bool, accessKey string) error {
	if authExisted {
		return nil
	}
	authData["OPENAI_API_KEY"] = accessKey
	authData["auth_mode"] = "apikey"
	return writeJSONAtomic(authPath, authData)
}

func (s *Service) applyCodexOpenAI(apiKey string) error {
	configPath := s.codexConfigPath()
	authPath := s.codexAuthPath()
	configContent, _, err := readTextFile(configPath)
	if err != nil {
		return err
	}
	authData, _, err := readJSONMap(authPath)
	if err != nil {
		return err
	}
	updated := upsertTopLevelTomlString(configContent, "model_provider", "openai")
	updated = restoreTopLevelTomlString(updated, "openai_base_url", nil) // 清理 CCX proxy 残留
	updated = restoreNamedTomlBlock(updated, "model_providers.ccx", nil)
	// OpenAI 是内置 provider，不需要显式配置块
	updated = restoreNamedTomlBlock(updated, "model_providers.openai", nil)
	if err := writeTextAtomic(configPath, updated); err != nil {
		return err
	}
	key := strings.TrimSpace(apiKey)
	if key != "" {
		// API Key 模式：写入 key + auth_mode = "apikey"
		// OpenAI 直连的 key 直接落在 auth.json，不再单独保存 provider key
		authData["OPENAI_API_KEY"] = key
		authData["auth_mode"] = "apikey"
	} else {
		// OAuth 登录模式：auth_mode = "chatgpt"，OPENAI_API_KEY = null
		authData["auth_mode"] = "chatgpt"
		authData["OPENAI_API_KEY"] = nil
	}
	return writeJSONAtomic(authPath, authData)
}

func codexResponsesBaseURL(provider string) (string, bool) {
	switch provider {
	case ProviderDeepSeek:
		return "https://api.deepseek.com/v1", true
	case ProviderMiMo:
		return "https://api.xiaomimimo.com/v1", true
	case ProviderCompshare:
		return "https://cp.compshare.cn/v1", true
	case ProviderKimi:
		return "https://api.moonshot.cn/v1", true
	case ProviderGLM:
		return "https://open.bigmodel.cn/api/paas/v4", true
	case ProviderMiniMax:
		return "https://api.minimax.chat/v1", true
	case ProviderDashScope:
		return "https://dashscope.aliyuncs.com/compatible-mode/v1", true
	case ProviderRunAPI:
		return runAPIBaseURL, true
	case ProviderUnity2:
		return unity2BaseURL, true
	case ProviderOpenCodeZen:
		return "https://opencode.ai/zen/v1", true
	case ProviderOpenCodeGo:
		return "https://opencode.ai/zen/go/v1", true
	case ProviderXFyun:
		return xfyunCodexBaseURL, true
	case ProviderTencentLkeap:
		return tencentLkeapCodexBaseURL, true
	case ProviderVolcArk:
		return volcArkCodexBaseURL, true
	case ProviderQianfan:
		return qianfanCodexBaseURL, true
	case ProviderModelScope:
		return modelScopeCodexBaseURL, true
	default:
		return "", false
	}
}

func (s *Service) applyCodexThirdParty(provider, baseURL, apiKey string) error {
	configPath := s.codexConfigPath()
	authPath := s.codexAuthPath()
	configContent, configExisted, err := readTextFile(configPath)
	if err != nil {
		return err
	}
	authData, authExisted, err := readJSONMap(authPath)
	if err != nil {
		return err
	}
	modelProvider, mpOK := extractTopLevelTomlString(configContent, "model_provider")
	providerBlock, blockOK := extractNamedTomlBlock(configContent, "model_providers.ccx")
	openaiKey, keyOK := authData["OPENAI_API_KEY"].(string)
	state := CodexProxyState{
		Version:               stateVersion,
		ConfigPath:            configPath,
		AuthPath:              authPath,
		ConfigFileExisted:     configExisted,
		AuthFileExisted:       authExisted,
		OriginalModelProvider: optionalString(modelProvider, mpOK),
		OriginalProviderBlock: optionalString(providerBlock, blockOK),
		OriginalOpenAIAPIKey:  optionalString(openaiKey, keyOK),
		InjectedProvider:      provider,
		InjectedBaseURL:       baseURL,
	}
	if existing, ok := s.readCodexState(); ok {
		state = existing
		state.InjectedProvider = provider
		state.InjectedBaseURL = baseURL
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = s.GetSavedProviderKeys()["codex:"+provider]
	}
	if key == "" {
		return fmt.Errorf("%s API Key 不能为空", provider)
	}
	state.InjectedAPIKey = key
	if err := s.saveProviderKey(PlatformCodex, provider, key); err != nil {
		return err
	}
	if err := writeJSONAtomic(s.codexStatePath(), state); err != nil {
		return err
	}
	block := fmt.Sprintf(`[model_providers.%s]
name = %q
base_url = %q
wire_api = "responses"
requires_openai_auth = true
experimental_bearer_token = %q
`, provider, provider, baseURL, key)
	updated := upsertTopLevelTomlString(configContent, "model_provider", provider)
	updated = restoreTopLevelTomlString(updated, "openai_base_url", nil) // 清理 CCX proxy 残留
	updated = restoreNamedTomlBlock(updated, "model_providers.ccx", nil)
	updated = restoreNamedTomlBlock(updated, "model_providers.openai", nil)
	updated = upsertNamedTomlBlock(updated, "model_providers."+provider, block)
	if err := writeTextAtomic(configPath, updated); err != nil {
		return err
	}
	return ensureCodexPluginAuth(authPath, authData, authExisted, key)
}

// codexThirdPartyQuickBaseURL 通过 openai_base_url 反查已知第三方 provider。

func codexThirdPartyQuickBaseURL(baseURL string) (string, bool) {
	switch {
	case strings.Contains(baseURL, "deepseek.com"):
		return ProviderDeepSeek, true
	case strings.Contains(baseURL, "xiaomimimo.com"):
		return ProviderMiMo, true
	case strings.Contains(baseURL, "cp.compshare.cn"):
		return ProviderCompshare, true
	case strings.Contains(baseURL, "moonshot.cn"):
		return ProviderKimi, true
	case strings.Contains(baseURL, "bigmodel.cn"):
		return ProviderGLM, true
	case strings.Contains(baseURL, "minimax.chat") || strings.Contains(baseURL, "minimaxi.com"):
		return ProviderMiniMax, true
	case strings.Contains(baseURL, "dashscope.aliyuncs.com"):
		return ProviderDashScope, true
	case strings.Contains(baseURL, "runapi.co"):
		return ProviderRunAPI, true
	case strings.Contains(baseURL, "opencode.ai/zen/go"):
		return ProviderOpenCodeGo, true
	case strings.Contains(baseURL, "opencode.ai/zen"):
		return ProviderOpenCodeZen, true
	case strings.Contains(baseURL, "xf-yun.com"):
		return ProviderXFyun, true
	case strings.Contains(baseURL, "lkeap.cloud.tencent.com"):
		return ProviderTencentLkeap, true
	case strings.Contains(baseURL, "volces.com"):
		return ProviderVolcArk, true
	case strings.Contains(baseURL, "baidubce.com"):
		return ProviderQianfan, true
	case strings.Contains(baseURL, "modelscope.cn"):
		return ProviderModelScope, true
	default:
		return "", false
	}
}

// applyCodexThirdPartyQuick 以快捷模式配置第三方 provider。
// 使用 model_provider="openai" + openai_base_url=<第三方 URL>。

func (s *Service) applyCodexThirdPartyQuick(provider, baseURL, apiKey string) error {
	configPath := s.codexConfigPath()
	authPath := s.codexAuthPath()
	configContent, configExisted, err := readTextFile(configPath)
	if err != nil {
		return err
	}
	authData, authExisted, err := readJSONMap(authPath)
	if err != nil {
		return err
	}
	modelProvider, mpOK := extractTopLevelTomlString(configContent, "model_provider")
	providerBlock, blockOK := extractNamedTomlBlock(configContent, "model_providers.ccx")
	openaiBaseURL, obOK := extractTopLevelTomlString(configContent, "openai_base_url")
	openaiKey, keyOK := authData["OPENAI_API_KEY"].(string)
	state := CodexProxyState{
		Version:               stateVersion,
		ConfigPath:            configPath,
		AuthPath:              authPath,
		ConfigFileExisted:     configExisted,
		AuthFileExisted:       authExisted,
		OriginalModelProvider: optionalString(modelProvider, mpOK),
		OriginalProviderBlock: optionalString(providerBlock, blockOK),
		OriginalOpenAIAPIKey:  optionalString(openaiKey, keyOK),
		OriginalOpenAIBaseURL: optionalString(openaiBaseURL, obOK),
		ThirdPartyQuickMode:   true,
		InjectedProvider:      provider,
		InjectedBaseURL:       baseURL,
	}
	if existing, ok := s.readCodexState(); ok {
		state = existing
		state.ThirdPartyQuickMode = true
		state.InjectedProvider = provider
		state.InjectedBaseURL = baseURL
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = s.GetSavedProviderKeys()["codex:"+provider]
	}
	if key == "" {
		return fmt.Errorf("%s API Key 不能为空", provider)
	}
	state.InjectedAPIKey = key
	if err := s.saveProviderKey(PlatformCodex, provider, key); err != nil {
		return err
	}
	if err := writeJSONAtomic(s.codexStatePath(), state); err != nil {
		return err
	}
	// config.toml: model_provider="openai" + openai_base_url=<第三方 URL>
	updated := upsertTopLevelTomlString(configContent, "model_provider", "openai")
	updated = upsertTopLevelTomlString(updated, "openai_base_url", baseURL)
	updated = restoreNamedTomlBlock(updated, "model_providers.ccx", nil)
	// 清理插件模式残留的第三方 provider 块
	if isCodexThirdPartyProvider(provider) {
		updated = restoreNamedTomlBlock(updated, "model_providers."+provider, nil)
	}
	if err := writeTextAtomic(configPath, updated); err != nil {
		return err
	}
	authData["OPENAI_API_KEY"] = key
	authData["auth_mode"] = "apikey"
	return writeJSONAtomic(authPath, authData)
}

func (s *Service) restoreCodex() error {
	var state CodexProxyState
	if err := readJSONFile(s.codexStatePath(), &state); err != nil {
		return err
	}
	if state.ConfigFileExisted {
		content, _, err := readTextFile(state.ConfigPath)
		if err != nil {
			return err
		}
		content = restoreTopLevelTomlString(content, "model_provider", state.OriginalModelProvider)
		content = restoreTopLevelTomlString(content, "openai_base_url", state.OriginalOpenAIBaseURL)
		content = restoreNamedTomlBlock(content, "model_providers.ccx", state.OriginalProviderBlock)
		if state.InjectedProvider != "" && state.InjectedProvider != ProviderCCX && state.InjectedProvider != ProviderOpenAI {
			content = restoreNamedTomlBlock(content, "model_providers."+state.InjectedProvider, nil)
		}
		if err := writeTextAtomic(state.ConfigPath, content); err != nil {
			return err
		}
	} else if err := os.Remove(state.ConfigPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if state.AuthFileExisted {
		authData, _, err := readJSONMap(state.AuthPath)
		if err != nil {
			return err
		}
		restoreStringField(authData, "OPENAI_API_KEY", state.OriginalOpenAIAPIKey)
		if err := writeJSONAtomic(state.AuthPath, authData); err != nil {
			return err
		}
	} else if err := os.Remove(state.AuthPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(s.codexStatePath())
}

func (s *Service) readCodexState() (CodexProxyState, bool) {
	var state CodexProxyState
	if err := readJSONFile(s.codexStatePath(), &state); err != nil {
		return CodexProxyState{}, false
	}
	return state, true
}

func normalizeCodexProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", ProviderOpenAI:
		return ProviderOpenAI
	case ProviderCCX:
		return ProviderCCX
	case ProviderDeepSeek:
		return ProviderDeepSeek
	case ProviderMiMo:
		return ProviderMiMo
	case ProviderCompshare:
		return ProviderCompshare
	case ProviderRunAPI:
		return ProviderRunAPI
	case ProviderUnity2:
		return ProviderUnity2
	case ProviderKimi:
		return ProviderKimi
	case ProviderGLM:
		return ProviderGLM
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
	case ProviderModelScope:
		return ProviderModelScope
	default:
		return ProviderCustom
	}
}

func isCodexThirdPartyProvider(provider string) bool {
	return provider == ProviderDeepSeek || provider == ProviderMiMo || provider == ProviderCompshare || provider == ProviderRunAPI || provider == ProviderKimi || provider == ProviderGLM || provider == ProviderMiniMax || provider == ProviderDashScope || provider == ProviderOpenCodeZen || provider == ProviderOpenCodeGo || provider == ProviderXFyun || provider == ProviderTencentLkeap || provider == ProviderVolcArk || provider == ProviderQianfan || provider == ProviderModelScope
}

func codexBaseURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/v1", port)
}

func codexProviderBlock(baseURL string) string {
	return fmt.Sprintf(`[model_providers.ccx]
name = "CCX Proxy"
base_url = %q
wire_api = "responses"
env_key = "OPENAI_API_KEY"
requires_openai_auth = false
`, baseURL)
}
