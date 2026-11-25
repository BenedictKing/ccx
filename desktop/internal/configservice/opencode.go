package configservice

import (
	"fmt"
	"os"
	"strings"
)

type OpenCodeProxyState struct {
	Version              int     `json:"version"`
	ProviderID           string  `json:"providerId"`
	ConfigPath           string  `json:"configPath"`
	AuthPath             string  `json:"authPath"`
	ConfigFileExisted    bool    `json:"configFileExisted"`
	AuthFileExisted      bool    `json:"authFileExisted"`
	OriginalProviderJSON *string `json:"originalProviderJson,omitempty"`
	OriginalAuthType     *string `json:"originalAuthType,omitempty"`
	OriginalAuthKey      *string `json:"originalAuthKey,omitempty"`
	InjectedBaseURL      string  `json:"injectedBaseUrl"`
	InjectedAPIKey       string  `json:"injectedApiKey"`
}

func (s *Service) readOpenCodeState() (OpenCodeProxyState, bool) {
	var state OpenCodeProxyState
	if err := readJSONFile(s.openCodeStatePath(), &state); err != nil {
		return OpenCodeProxyState{}, false
	}
	return state, true
}

func (s *Service) writeOpenCodeState(state OpenCodeProxyState) error {
	return writeJSONAtomic(s.openCodeStatePath(), state)
}

func openCodeAuthKeyFromMap(authData map[string]any, provider string) (string, string) {
	obj, _ := authData[provider].(map[string]any)
	if obj == nil {
		return "", ""
	}
	authType, _ := obj["type"].(string)
	key, _ := obj["key"].(string)
	return authType, key
}

func upsertOpenCodeAuthKey(authData map[string]any, provider string, key string) map[string]any {
	if authData == nil {
		authData = map[string]any{}
	}
	existing, _ := authData[provider].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	existing["type"] = "api"
	existing["key"] = key
	authData[provider] = existing
	return authData
}

func restoreOpenCodeAuthKey(authData map[string]any, provider string, origType *string, origKey *string) map[string]any {
	if authData == nil {
		authData = map[string]any{}
	}
	if origType == nil && origKey == nil {
		delete(authData, provider)
		return authData
	}
	existing, _ := authData[provider].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	if origType != nil {
		existing["type"] = *origType
	} else {
		delete(existing, "type")
	}
	if origKey != nil {
		existing["key"] = *origKey
	} else {
		delete(existing, "key")
	}
	authData[provider] = existing
	return authData
}

func normalizeOpenCodeProvider(provider string) string {
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
	case ProviderXFyun:
		return ProviderXFyun
	case ProviderOpenCodeZen:
		return ProviderOpenCodeZen
	case ProviderOpenCodeGo:
		return ProviderOpenCodeGo
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

func isOpenCodeDirectProvider(provider string) bool {
	switch provider {
	case ProviderDeepSeek, ProviderMiMo, ProviderKimi, ProviderGLM, ProviderSenseNova, ProviderMiniMax, ProviderDashScope, ProviderXFyun, ProviderOpenCodeZen, ProviderOpenCodeGo, ProviderTencentLkeap, ProviderVolcArk, ProviderQianfan:
		return true
	default:
		return false
	}
}

func openCodeDirectBaseURL(provider string) (string, bool) {
	switch provider {
	case ProviderDeepSeek:
		return "https://api.deepseek.com/v1", true
	case ProviderMiMo:
		return "https://api.xiaomimimo.com/v1", true
	case ProviderKimi:
		return "https://api.moonshot.cn/v1", true
	case ProviderGLM:
		return "https://open.bigmodel.cn/api/paas/v4", true
	case ProviderSenseNova:
		return sensenovaChatBaseURL, true
	case ProviderMiniMax:
		return "https://api.minimax.chat/v1", true
	case ProviderDashScope:
		return "https://dashscope.aliyuncs.com/compatible-mode/v1", true
	case ProviderXFyun:
		return xfyunCodexBaseURL, true
	case ProviderOpenCodeZen:
		return openCodeZenBaseURL, true
	case ProviderOpenCodeGo:
		return openCodeGoBaseURL, true
	case ProviderTencentLkeap:
		return tencentLkeapCodexBaseURL, true
	case ProviderVolcArk:
		return volcArkCodexBaseURL, true
	case ProviderQianfan:
		return qianfanCodexBaseURL, true
	default:
		return "", false
	}
}

func openCodeDirectLabel(provider string) string {
	switch provider {
	case ProviderDeepSeek:
		return "DeepSeek"
	case ProviderMiMo:
		return "MiMo"
	case ProviderKimi:
		return "Kimi"
	case ProviderGLM:
		return "GLM"
	case ProviderSenseNova:
		return "SenseNova"
	case ProviderMiniMax:
		return "MiniMax"
	case ProviderDashScope:
		return "DashScope"
	case ProviderXFyun:
		return "讯飞星辰"
	case ProviderOpenCodeZen:
		return "OpenCode Zen"
	case ProviderOpenCodeGo:
		return "OpenCode Go"
	case ProviderTencentLkeap:
		return "腾讯云 TokenHub"
	case ProviderVolcArk:
		return "火山方舟"
	case ProviderQianfan:
		return "百度千帆"
	default:
		return provider
	}
}

func openCodeProviderBlockJSON(providerID string, label string, baseURL string) string {
	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString(fmt.Sprintf("      \"npm\": \"@ai-sdk/openai-compatible\",\n"))
	b.WriteString(fmt.Sprintf("      \"name\": %q,\n", label))
	b.WriteString("      \"options\": {\n")
	b.WriteString(fmt.Sprintf("        \"baseURL\": %q\n", baseURL))
	b.WriteString("      },\n")
	b.WriteString("      \"models\": {}\n")
	b.WriteString("    }")
	return b.String()
}

func detectOpenCodeProvider(configContent string, providerID string) (string, string) {
	if strings.TrimSpace(configContent) == "" || strings.TrimSpace(providerID) == "" {
		return "", ""
	}
	block, ok := extractJSONObjectString(configContent, providerID)
	if !ok {
		return "", ""
	}
	baseURL, _ := findJSONCStringValue(block, "baseURL")
	return baseURL, providerID
}

func resolveOpenCodeProvider(req ApplyAgentConfigRequest, port int, accessKey string) (string, string, string, string, string, error) {
	provider := normalizeOpenCodeProvider(req.Provider)
	switch provider {
	case ProviderCCX:
		if port == 0 {
			return "", "", "", "", "", fmt.Errorf("CCX 端口未设置")
		}
		if accessKey == "" {
			return "", "", "", "", "", fmt.Errorf("PROXY_ACCESS_KEY 为空")
		}
		return ProviderCCX, ProviderCCX, codexBaseURL(port), accessKey, ProviderCCX, nil
	default:
		if !isOpenCodeDirectProvider(provider) {
			return "", "", "", "", "", fmt.Errorf("不支持的 OpenCode provider: %s", provider)
		}
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			return "", "", "", "", "", fmt.Errorf("%s API Key 不能为空", provider)
		}
		baseURL, ok := openCodeDirectBaseURL(provider)
		if !ok {
			return "", "", "", "", "", fmt.Errorf("%s 缺少 OpenCode Base URL", provider)
		}
		if requestedBaseURL := strings.TrimSpace(req.BaseURL); requestedBaseURL != "" {
			baseURL = requestedBaseURL
		}
		return provider, provider, baseURL, apiKey, provider, nil
	}
}

func (s *Service) getOpenCodeStatus(port int) (AgentConfigStatus, error) {
	configPath := s.openCodeConfigPath()
	authPath := s.openCodeAuthPath()
	target := codexBaseURL(port)
	status := AgentConfigStatus{
		Platform:       PlatformOpenCode,
		Provider:       ProviderCCX,
		TargetProvider: ProviderCCX,
		TargetBaseURL:  target,
		ConfigPath:     configPath,
		AuthPath:       authPath,
		HasState:       fileExists(s.openCodeStatePath()),
	}
	if existing, ok := s.readOpenCodeState(); ok {
		status.Provider = existing.ProviderID
		if existing.ProviderID != ProviderCCX {
			status.TargetProvider = existing.ProviderID
		}
	}
	configContent, configExists, err := readTextFile(configPath)
	if err != nil {
		status.LastError = err.Error()
		return status, nil
	}
	authData, _, err := readJSONMap(authPath)
	if err != nil {
		status.LastError = err.Error()
		return status, nil
	}
	providerID := status.Provider
	if providerID == "" {
		providerID = ProviderCCX
	}
	baseURL, _ := detectOpenCodeProvider(configContent, providerID)
	_, authKey := openCodeAuthKeyFromMap(authData, providerID)
	status.CurrentBaseURL = baseURL
	if providerID == ProviderCCX {
		status.TargetBaseURL = target
	} else if wantURL, ok := openCodeDirectBaseURL(providerID); ok {
		status.TargetBaseURL = wantURL
	} else {
		status.TargetBaseURL = ""
	}
	envAccessKey := strings.TrimSpace(os.Getenv("PROXY_ACCESS_KEY"))
	if providerID == ProviderCCX {
		status.Configured = configExists && baseURL == target && strings.TrimSpace(authKey) != "" && envAccessKey != "" && strings.TrimSpace(authKey) == envAccessKey
		status.MatchesCurrentPort = status.Configured
		status.NeedsUpdate = configExists && (isLocalBaseURL(baseURL) || strings.TrimSpace(authKey) != "") && !status.MatchesCurrentPort
	} else {
		status.Configured = configExists && baseURL != "" && strings.TrimSpace(authKey) != ""
		status.MatchesCurrentPort = status.Configured
		status.NeedsUpdate = configExists && (isLocalBaseURL(baseURL) || strings.TrimSpace(authKey) != "") && !status.MatchesCurrentPort
	}
	return status, nil
}

func (s *Service) applyOpenCode(req ApplyAgentConfigRequest, port int, accessKey string) error {
	providerID, providerLabel, targetURL, apiKey, storedProvider, err := resolveOpenCodeProvider(req, port, accessKey)
	if err != nil {
		return err
	}
	configPath := s.openCodeConfigPath()
	authPath := s.openCodeAuthPath()
	configContent, configExisted, err := readTextFile(configPath)
	if err != nil {
		return err
	}
	authData, authExisted, err := readJSONMap(authPath)
	if err != nil {
		return err
	}
	origProviderJSON, _ := extractJSONObjectString(configContent, providerID)
	origAuthType, origAuthKey := openCodeAuthKeyFromMap(authData, providerID)
	state := OpenCodeProxyState{
		Version:              stateVersion,
		ProviderID:           storedProvider,
		ConfigPath:           configPath,
		AuthPath:             authPath,
		ConfigFileExisted:    configExisted,
		AuthFileExisted:      authExisted,
		OriginalProviderJSON: optionalString(origProviderJSON, origProviderJSON != ""),
		OriginalAuthType:     optionalString(origAuthType, origAuthType != ""),
		OriginalAuthKey:      optionalString(origAuthKey, origAuthKey != ""),
		InjectedBaseURL:      targetURL,
		InjectedAPIKey:       apiKey,
	}
	if existing, ok := s.readOpenCodeState(); ok {
		state.ConfigFileExisted = existing.ConfigFileExisted
		state.AuthFileExisted = existing.AuthFileExisted
		if existing.OriginalProviderJSON != nil {
			state.OriginalProviderJSON = existing.OriginalProviderJSON
		}
		if existing.OriginalAuthType != nil {
			state.OriginalAuthType = existing.OriginalAuthType
		}
		if existing.OriginalAuthKey != nil {
			state.OriginalAuthKey = existing.OriginalAuthKey
		}
	}
	if err := s.writeOpenCodeState(state); err != nil {
		return err
	}
	providerJSON := openCodeProviderBlockJSON(providerID, providerLabel, targetURL)
	updatedConfig := patchOpenCodeProviderJSONC(configContent, providerID, providerJSON)
	if err := writeTextAtomic(configPath, updatedConfig); err != nil {
		return err
	}
	authData = upsertOpenCodeAuthKey(authData, providerID, apiKey)
	return writeJSONAtomic(authPath, authData)
}

func (s *Service) restoreOpenCode() error {
	var state OpenCodeProxyState
	if err := readJSONFile(s.openCodeStatePath(), &state); err != nil {
		return err
	}
	if state.ConfigFileExisted {
		content, _, err := readTextFile(state.ConfigPath)
		if err != nil {
			return err
		}
		if state.OriginalProviderJSON != nil {
			content = patchOpenCodeProviderJSONC(content, state.ProviderID, *state.OriginalProviderJSON)
		} else {
			content = removeJSONCObjectKey(content, state.ProviderID)
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
		authData = restoreOpenCodeAuthKey(authData, state.ProviderID, state.OriginalAuthType, state.OriginalAuthKey)
		if err := writeJSONAtomic(state.AuthPath, authData); err != nil {
			return err
		}
	} else if err := os.Remove(state.AuthPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(s.openCodeStatePath())
}
