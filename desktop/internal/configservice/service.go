package configservice

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BenedictKing/ccx/desktop/internal/appdirs"
)

const (
	PlatformClaude   = "claude"
	PlatformCodex    = "codex"
	PlatformOpenCode = "opencode"

	ProviderCCX          = "ccx"
	ProviderDeepSeek     = "deepseek"
	ProviderMiMo         = "mimo"
	ProviderKimi         = "kimi"
	ProviderGLM          = "glm"
	ProviderSenseNova    = "sensenova"
	ProviderMiniMax      = "minimax"
	ProviderDashScope    = "dashscope"
	ProviderOpenCodeZen  = "opencode-zen"
	ProviderOpenCodeGo   = "opencode-go"
	ProviderCustom       = "custom"
	ProviderOpenAI       = "openai"
	ProviderXFyun        = "xfyun"
	ProviderTencentLkeap = "tencent-lkeap"
	ProviderVolcArk      = "volc-ark"
	ProviderQianfan      = "qianfan"
	ProviderModelScope   = "modelscope"

	deepSeekClaudeBaseURL            = "https://api.deepseek.com/anthropic"
	defaultMiMoBaseURL               = "https://api.xiaomimimo.com/anthropic"
	kimiClaudeBaseURL                = "https://api.moonshot.cn/anthropic"
	glmClaudeBaseURL                 = "https://open.bigmodel.cn/api/anthropic"
	sensenovaClaudeBaseURL           = "https://token.sensenova.cn"
	sensenovaChatBaseURL             = "https://token.sensenova.cn/v1"
	miniMaxClaudeBaseURL             = "https://api.minimaxi.com/anthropic"
	dashScopeClaudeBaseURL           = "https://dashscope.aliyuncs.com/apps/anthropic"
	dashScopeCodingPlanClaudeBaseURL = "https://coding.dashscope.aliyuncs.com/apps/anthropic"
	openCodeZenClaudeBaseURL         = "https://opencode.ai/zen"
	openCodeGoClaudeBaseURL          = "https://opencode.ai/zen/go"
	xfyunClaudeBaseURL               = "https://maas-coding-api.cn-huabei-1.xf-yun.com/anthropic"
	xfyunCodexBaseURL                = "https://maas-coding-api.cn-huabei-1.xf-yun.com/v2"
	tencentLkeapClaudeBaseURL        = "https://api.lkeap.cloud.tencent.com/plan/anthropic"
	volcArkClaudeBaseURL             = "https://ark.cn-beijing.volces.com/api/coding"
	qianfanClaudeBaseURL             = "https://qianfan.baidubce.com/anthropic/coding"
	tencentLkeapCodexBaseURL         = "https://api.lkeap.cloud.tencent.com/plan/v3"
	volcArkCodexBaseURL              = "https://ark.cn-beijing.volces.com/api/coding/v3"
	qianfanCodexBaseURL              = "https://qianfan.baidubce.com/v2/coding"
	modelScopeCodexBaseURL           = "https://api-inference.modelscope.cn/v1"
	stateVersion                     = 1

	openCodeZenBaseURL = "https://opencode.ai/zen/v1"
	openCodeGoBaseURL  = "https://opencode.ai/zen/go/v1"
)

type AgentConfigStatus struct {
	Platform           string `json:"platform"`
	Provider           string `json:"provider,omitempty"`
	Mode               string `json:"mode,omitempty"` // "quick" | "plugin"
	TargetProvider     string `json:"targetProvider,omitempty"`
	Configured         bool   `json:"configured"`
	MatchesCurrentPort bool   `json:"matchesCurrentPort"`
	NeedsUpdate        bool   `json:"needsUpdate"`
	CurrentBaseURL     string `json:"currentBaseUrl"`
	TargetBaseURL      string `json:"targetBaseUrl"`
	ConfigPath         string `json:"configPath"`
	AuthPath           string `json:"authPath,omitempty"`
	HasState           bool   `json:"hasState"`
	LastError          string `json:"lastError,omitempty"`

	// Codex 专属：config.toml 与 auth.json 一致性诊断。
	// 用于识别 CCS 等工具或手工编辑导致的配置污染，避免用户只看到上游 503。
	AuthMode          string `json:"authMode,omitempty"`          // auth.json 的 auth_mode："apikey" | "chatgpt"
	ConfigConsistent  bool   `json:"configConsistent"`            // config.toml 与 auth.json 语义是否一致
	DiagnosticCode    string `json:"diagnosticCode,omitempty"`    // 不一致时的诊断码，如 codex.missing_api_key
	DiagnosticMessage string `json:"diagnosticMessage,omitempty"` // 面向用户的诊断说明
}

type ApplyAgentConfigRequest struct {
	Platform string `json:"platform"`
	Provider string `json:"provider,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
	BaseURL  string `json:"baseUrl,omitempty"`
	Mode     string `json:"mode,omitempty"` // "quick" | "plugin"
}

type MigrateCodexSessionsRequest struct {
	Provider string `json:"provider,omitempty"`
	Mode     string `json:"mode,omitempty"` // "quick" | "plugin"
}

type MigrateCodexSessionsResult struct {
	TargetProvider    string `json:"targetProvider"`
	TotalFiles        int    `json:"totalFiles"`
	MigratedFiles     int    `json:"migratedFiles"`
	SkippedFiles      int    `json:"skippedFiles"`
	FailedFiles       int    `json:"failedFiles"`
	SQLiteRowsUpdated int64  `json:"sqliteRowsUpdated"`
	SQLiteSkipped     bool   `json:"sqliteSkipped"`
	SQLiteError       string `json:"sqliteError,omitempty"`
}

type Service struct {
	homeDir  string
	stateDir string
}

type codexThreadsColumns struct {
	modelProvider    bool
	preview          bool
	firstUserMessage bool
	hasUserEvent     bool
	threadSource     bool
	source           bool
}

type ClaudeProxyState struct {
	Version           int     `json:"version"`
	TargetPath        string  `json:"targetPath"`
	FileExisted       bool    `json:"fileExisted"`
	EnvExisted        bool    `json:"envExisted"`
	OriginalBaseURL   *string `json:"originalBaseUrl,omitempty"`
	OriginalAuthToken *string `json:"originalAuthToken,omitempty"`
	OriginalAPIKey    *string `json:"originalApiKey,omitempty"`
	OriginalModel     *string `json:"originalModel,omitempty"`
	OriginalSmallFast *string `json:"originalSmallFast,omitempty"`
	InjectedProvider  string  `json:"injectedProvider"`
	InjectedBaseURL   string  `json:"injectedBaseUrl"`
	InjectedAuthToken string  `json:"injectedAuthToken,omitempty"`
	InjectedAPIKey    string  `json:"injectedApiKey,omitempty"`
}

type CodexProxyState struct {
	Version               int     `json:"version"`
	ConfigPath            string  `json:"configPath"`
	AuthPath              string  `json:"authPath"`
	ConfigFileExisted     bool    `json:"configFileExisted"`
	AuthFileExisted       bool    `json:"authFileExisted"`
	OriginalModelProvider *string `json:"originalModelProvider,omitempty"`
	OriginalProviderBlock *string `json:"originalProviderBlock,omitempty"`
	OriginalOpenAIAPIKey  *string `json:"originalOpenaiApiKey,omitempty"`
	OriginalOpenAIBaseURL *string `json:"originalOpenaiBaseUrl,omitempty"`
	InjectedProvider      string  `json:"injectedProvider,omitempty"`
	InjectedBaseURL       string  `json:"injectedBaseUrl"`
	InjectedAPIKey        string  `json:"injectedApiKey"`
	ThirdPartyQuickMode   bool    `json:"thirdPartyQuickMode,omitempty"`
}

type DiffLine struct {
	Type    string `json:"type"` // "context" | "added" | "removed"
	Content string `json:"content"`
}

type FileDiff struct {
	Path   string     `json:"path"`
	Action string     `json:"action"` // "modify" | "create" | "delete"
	Lines  []DiffLine `json:"lines"`
}

type ConfigDiffResult struct {
	Files []FileDiff `json:"files"`
}

type ProviderKeyStore struct {
	Version int                         `json:"version"`
	Keys    map[string]string           `json:"keys"`
	Assets  map[string]ProviderKeyAsset `json:"assets,omitempty"`
}

type ProviderKeyAsset struct {
	Provider string   `json:"provider"`
	APIKey   string   `json:"apiKey"`
	BaseURL  string   `json:"baseUrl,omitempty"`
	PlanID   string   `json:"planId,omitempty"`
	ProxyURL string   `json:"proxyUrl,omitempty"`
	Usages   []string `json:"usages,omitempty"`
}

func New(dataDir string) (*Service, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return nil, fmt.Errorf("无法定位用户主目录")
	}
	if dataDir == "" {
		dataDir = appdirs.DataDirForHome(homeDir)
	}
	stateDir := filepath.Join(dataDir, "agent-config-state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, err
	}
	return &Service{homeDir: homeDir, stateDir: stateDir}, nil
}

func (s *Service) GetStatus(platform string, port int) (AgentConfigStatus, error) {
	switch platform {
	case PlatformClaude:
		return s.getClaudeStatus(port)
	case PlatformCodex:
		return s.getCodexStatus(port)
	case PlatformOpenCode:
		return s.getOpenCodeStatus(port)
	default:
		return AgentConfigStatus{}, fmt.Errorf("不支持的 agent 平台: %s", platform)
	}
}

func (s *Service) Apply(req ApplyAgentConfigRequest, port int, accessKey string) error {
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		return fmt.Errorf("agent 平台不能为空")
	}
	switch platform {
	case PlatformClaude:
		return s.applyClaude(req, port, accessKey)
	case PlatformCodex:
		provider := strings.TrimSpace(req.Provider)
		if provider == ProviderOpenAI {
			return s.applyCodexOpenAI(req.APIKey)
		}
		if responsesURL, ok := codexResponsesBaseURL(provider); ok {
			if baseURL := strings.TrimSpace(req.BaseURL); baseURL != "" {
				responsesURL = baseURL
			}
			if req.Mode == "quick" {
				return s.applyCodexThirdPartyQuick(provider, responsesURL, req.APIKey)
			}
			return s.applyCodexThirdParty(provider, responsesURL, req.APIKey)
		}
		if port == 0 {
			return fmt.Errorf("CCX 端口未设置")
		}
		if accessKey == "" {
			return fmt.Errorf("PROXY_ACCESS_KEY 为空")
		}
		return s.applyCodex(port, accessKey, req.Mode)
	case PlatformOpenCode:
		return s.applyOpenCode(req, port, accessKey)
	default:
		return fmt.Errorf("不支持的 agent 平台: %s", platform)
	}
}

func (s *Service) Restore(platform string) error {
	switch platform {
	case PlatformClaude:
		return s.restoreClaude()
	case PlatformCodex:
		return s.restoreCodex()
	case PlatformOpenCode:
		return s.restoreOpenCode()
	default:
		return fmt.Errorf("不支持的 agent 平台: %s", platform)
	}
}

func (s *Service) PreviewApply(req ApplyAgentConfigRequest, port int, accessKey string) (ConfigDiffResult, error) {
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		return ConfigDiffResult{}, fmt.Errorf("agent 平台不能为空")
	}
	switch platform {
	case PlatformClaude:
		return s.previewApplyClaude(req, port, accessKey)
	case PlatformCodex:
		provider := strings.TrimSpace(req.Provider)
		if provider == ProviderOpenAI {
			return s.previewApplyCodexOpenAI(req.APIKey)
		}
		if responsesURL, ok := codexResponsesBaseURL(provider); ok {
			if baseURL := strings.TrimSpace(req.BaseURL); baseURL != "" {
				responsesURL = baseURL
			}
			if req.Mode == "quick" {
				return s.previewApplyCodexThirdPartyQuick(provider, responsesURL, req.APIKey)
			}
			return s.previewApplyCodexThirdParty(provider, responsesURL, req.APIKey)
		}
		return s.previewApplyCodex(port, accessKey, req.Mode)
	case PlatformOpenCode:
		return s.previewApplyOpenCode(req, port, accessKey)
	default:
		return ConfigDiffResult{}, fmt.Errorf("不支持的 agent 平台: %s", platform)
	}
}

// PreviewRestore 预览 Restore 操作的变更，不实际写入文件。

func (s *Service) PreviewRestore(platform string) (ConfigDiffResult, error) {
	switch platform {
	case PlatformClaude:
		return s.previewRestoreClaude()
	case PlatformCodex:
		return s.previewRestoreCodex()
	case PlatformOpenCode:
		return s.previewRestoreOpenCode()
	default:
		return ConfigDiffResult{}, fmt.Errorf("不支持的 agent 平台: %s", platform)
	}
}
