package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// deriveImportChannelName 按渠道首个 baseURL 派生名称（与 ConfigManager.Add 的派生规则一致），
// 用于导入预览/确认阶段预判冲突；无可用地址时回退到原名称或占位符。
func deriveImportChannelName(ch config.UpstreamConfig) string {
	first := ch.BaseURL
	if len(ch.BaseURLs) > 0 {
		first = ch.BaseURLs[0]
	}
	if strings.TrimSpace(first) != "" {
		return utils.DeriveChannelNameFromBaseURL(first)
	}
	if name := strings.TrimSpace(ch.Name); name != "" {
		return name
	}
	return "imported"
}

// ChannelPack 导出的渠道包格式。
// 包含元数据和渠道列表，用于跨实例迁移或模板分享。
type ChannelPack struct {
	Version    int                `json:"version"`
	ExportedAt string             `json:"exportedAt"`
	Channels   []ChannelPackEntry `json:"channels"`
}

// ChannelPackEntry 渠道包中的单条渠道记录。
// ChannelType 标识该渠道属于哪类数组（messages/chat/responses/gemini/images/vectors），
// 导入时据此分发到对应的 Upstream 数组。
type ChannelPackEntry struct {
	ChannelType string                `json:"channelType"`
	Channel     config.UpstreamConfig `json:"channel"`
}

// ImportPreview 导入预览，展示即将发生的变更。
type ImportPreview struct {
	NewChannels []ImportPreviewEntry `json:"newChannels"`
	TotalCount  int                  `json:"totalCount"`
	Warnings    []string             `json:"warnings,omitempty"`
}

// ImportPreviewEntry 导入预览中的单条记录。
type ImportPreviewEntry struct {
	ChannelType string `json:"channelType"`
	Name        string `json:"name"`
	Action      string `json:"action"` // "create" | "name_conflict"
}

// ExportChannelsRequest 导出请求。
type ExportChannelsRequest struct {
	ChannelUIDs  []string `json:"channelUids"`
	ChannelTypes []string `json:"channelTypes,omitempty"` // 可选，按类型过滤
	IncludeKeys  bool     `json:"includeKeys,omitempty"`  // 显式包含明文 APIKeys（需要 admin 鉴权）
}

// ImportChannelsRequest 导入请求。
type ImportChannelsRequest struct {
	Pack ChannelPack `json:"pack"`
}

// ImportConfirmRequest 导入确认请求（二次确认）。
type ImportConfirmRequest struct {
	Pack       ChannelPack `json:"pack"`
	SkipNaming bool        `json:"skipNaming,omitempty"` // 为 true 时自动重命名冲突渠道（加后缀）
}

// ChannelTypeToUpstreamKey 渠道类型到 Config 字段的映射。
var ChannelTypeToUpstreamKey = map[string]string{
	"messages":  "Upstream",
	"chat":      "ChatUpstream",
	"responses": "ResponsesUpstream",
	"gemini":    "GeminiUpstream",
	"images":    "ImagesUpstream",
	"vectors":   "VectorsUpstream",
}

// ValidChannelTypes 所有合法渠道类型。
var ValidChannelTypes = []string{"messages", "chat", "responses", "gemini", "images", "vectors"}

// getChannelsByType 从 Config 中按类型取出渠道切片。
func getChannelsByType(cfg config.Config, channelType string) []config.UpstreamConfig {
	switch channelType {
	case "messages":
		return cfg.Upstream
	case "chat":
		return cfg.ChatUpstream
	case "responses":
		return cfg.ResponsesUpstream
	case "gemini":
		return cfg.GeminiUpstream
	case "images":
		return cfg.ImagesUpstream
	case "vectors":
		return cfg.VectorsUpstream
	default:
		return nil
	}
}

// getAllChannelsFlat 扁平化所有渠道，返回 (channelType, channel, index) 三元组。
func getAllChannelsFlat(cfg config.Config) []struct {
	Type  string
	Index int
	Ch    config.UpstreamConfig
} {
	var result []struct {
		Type  string
		Index int
		Ch    config.UpstreamConfig
	}
	for _, ct := range ValidChannelTypes {
		for i, ch := range getChannelsByType(cfg, ct) {
			result = append(result, struct {
				Type  string
				Index int
				Ch    config.UpstreamConfig
			}{Type: ct, Index: i, Ch: ch})
		}
	}
	return result
}

// sanitizeForExport 深拷贝渠道并根据 includeKeys 决定是否清除 APIKeys。
func sanitizeForExport(ch config.UpstreamConfig, includeKeys bool) config.UpstreamConfig {
	out := ch
	if !includeKeys {
		out.APIKeys = nil
		out.APIKeyConfigs = nil
		out.DisabledAPIKeys = nil
		out.HistoricalAPIKeys = nil
	}
	// ChannelUID 不导出，导入时统一重新生成
	out.ChannelUID = ""
	return out
}

// isValidChannelType 检查渠道类型是否合法。
func isValidChannelType(ct string) bool {
	for _, v := range ValidChannelTypes {
		if v == ct {
			return true
		}
	}
	return false
}

// ExportChannels 导出渠道为渠道包 JSON。
//
// 安全约束：
//   - 默认排除 APIKeys 明文（includeKeys=false）
//   - includeKeys=true 需要 admin 鉴权（WebAuthMiddleware 已对 /api/* 强制 admin 鉴权，
//     此处额外校验请求头中的 admin key 以防止 proxy-only key 越权导出凭证）
func ExportChannels(envCfg *config.EnvConfig, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ExportChannelsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "message": err.Error()})
			return
		}

		// 如果请求包含明文 key，二次确认 admin 鉴权
		if req.IncludeKeys {
			if !requireAdminAuth(c, envCfg) {
				c.JSON(http.StatusForbidden, gin.H{
					"error":   "Forbidden",
					"message": "导出明文 APIKeys 需要管理密钥鉴权",
				})
				return
			}
		}

		cfg := cfgManager.GetConfig()
		allFlat := getAllChannelsFlat(cfg)

		// 按 ChannelUID 或 Name 构建索引，支持按 UID 或 Name 匹配
		uidSet := make(map[string]bool, len(req.ChannelUIDs))
		for _, uid := range req.ChannelUIDs {
			uidSet[uid] = true
		}

		// 支持 channelTypes 过滤
		typeSet := make(map[string]bool)
		if len(req.ChannelTypes) > 0 {
			for _, ct := range req.ChannelTypes {
				if !isValidChannelType(ct) {
					c.JSON(http.StatusBadRequest, gin.H{
						"error":   "Invalid channelType",
						"message": fmt.Sprintf("不支持的渠道类型: %s", ct),
					})
					return
				}
				typeSet[ct] = true
			}
		}

		var entries []ChannelPackEntry
		for _, item := range allFlat {
			// 按类型过滤
			if len(typeSet) > 0 && !typeSet[item.Type] {
				continue
			}
			// 按 UID 或 Name 匹配
			if len(uidSet) > 0 {
				if item.Ch.ChannelUID != "" && uidSet[item.Ch.ChannelUID] {
					// matched by UID
				} else if uidSet[item.Ch.Name] {
					// matched by Name
				} else {
					continue
				}
			}

			sanitized := sanitizeForExport(item.Ch, req.IncludeKeys)
			entries = append(entries, ChannelPackEntry{
				ChannelType: item.Type,
				Channel:     sanitized,
			})
		}

		pack := ChannelPack{
			Version:    1,
			ExportedAt: time.Now().UTC().Format(time.RFC3339),
			Channels:   entries,
		}

		log.Printf("[ChannelBatch-Export] 导出 %d 个渠道 (includeKeys=%v)", len(entries), req.IncludeKeys)

		c.JSON(http.StatusOK, pack)
	}
}

// ExportAllChannels 导出所有渠道（简化接口，支持 query 参数）。
//
//	GET /api/channels/export?includeKeys=true&channelTypes=messages,chat
func ExportAllChannels(envCfg *config.EnvConfig, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		includeKeys := c.Query("includeKeys") == "true"

		if includeKeys {
			if !requireAdminAuth(c, envCfg) {
				c.JSON(http.StatusForbidden, gin.H{
					"error":   "Forbidden",
					"message": "导出明文 APIKeys 需要管理密钥鉴权",
				})
				return
			}
		}

		// 按类型过滤
		var typeSet map[string]bool
		if ctParam := c.Query("channelTypes"); ctParam != "" {
			typeSet = make(map[string]bool)
			for _, ct := range strings.Split(ctParam, ",") {
				ct = strings.TrimSpace(ct)
				if !isValidChannelType(ct) {
					c.JSON(http.StatusBadRequest, gin.H{
						"error":   "Invalid channelType",
						"message": fmt.Sprintf("不支持的渠道类型: %s", ct),
					})
					return
				}
				typeSet[ct] = true
			}
		}

		cfg := cfgManager.GetConfig()
		allFlat := getAllChannelsFlat(cfg)

		var entries []ChannelPackEntry
		for _, item := range allFlat {
			if len(typeSet) > 0 && !typeSet[item.Type] {
				continue
			}
			sanitized := sanitizeForExport(item.Ch, includeKeys)
			entries = append(entries, ChannelPackEntry{
				ChannelType: item.Type,
				Channel:     sanitized,
			})
		}

		pack := ChannelPack{
			Version:    1,
			ExportedAt: time.Now().UTC().Format(time.RFC3339),
			Channels:   entries,
		}

		log.Printf("[ChannelBatch-Export] 导出全部 %d 个渠道 (includeKeys=%v)", len(entries), includeKeys)

		c.JSON(http.StatusOK, pack)
	}
}

// ImportChannels 导入渠道（预览模式，返回 diff 预览供前端展示后确认）。
//
// 安全约束：
// - 不复用原始 ChannelUID，统一重新生成（避免碰撞）
// - 导入前返回 diff 预览，前端展示后用户二次确认才写入
func ImportChannels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ImportChannelsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "message": err.Error()})
			return
		}

		if len(req.Pack.Channels) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "渠道包为空"})
			return
		}

		cfg := cfgManager.GetConfig()

		// 构建现有渠道名称索引（按类型）
		existingNames := make(map[string]map[string]bool)
		for _, ct := range ValidChannelTypes {
			existingNames[ct] = make(map[string]bool)
			for _, ch := range getChannelsByType(cfg, ct) {
				existingNames[ct][ch.Name] = true
			}
		}

		preview := ImportPreview{
			TotalCount: len(req.Pack.Channels),
		}

		for _, entry := range req.Pack.Channels {
			if !isValidChannelType(entry.ChannelType) {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":   "Invalid channelType",
					"message": fmt.Sprintf("渠道类型无效: %s", entry.ChannelType),
				})
				return
			}

			// 名称由首个 baseURL 派生（与 Add 保持一致），用于预览判冲突
			name := deriveImportChannelName(entry.Channel)

			action := "create"
			if existingNames[entry.ChannelType][name] {
				action = "name_conflict"
				preview.Warnings = append(preview.Warnings,
					fmt.Sprintf("渠道 '%s' (类型: %s) 与现有渠道同名", name, entry.ChannelType))
			}

			preview.NewChannels = append(preview.NewChannels, ImportPreviewEntry{
				ChannelType: entry.ChannelType,
				Name:        name,
				Action:      action,
			})
		}

		log.Printf("[ChannelBatch-Import] 生成导入预览: %d 个渠道", len(req.Pack.Channels))

		c.JSON(http.StatusOK, gin.H{
			"preview": preview,
		})
	}
}

// ImportChannelsConfirm 确认导入渠道（实际写入配置）。
//
// 安全约束：
// - 不复用原始 ChannelUID，统一重新生成
// - 复用现有单渠道创建时已有的迁移/校验/backfill 函数
// - 名称冲突时自动重命名（加 "-import" 后缀）
func ImportChannelsConfirm(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ImportConfirmRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "message": err.Error()})
			return
		}

		if len(req.Pack.Channels) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "渠道包为空"})
			return
		}

		cfg := cfgManager.GetConfig()

		// 收集所有现有名称（按类型）
		existingNames := make(map[string]map[string]bool)
		for _, ct := range ValidChannelTypes {
			existingNames[ct] = make(map[string]bool)
			for _, ch := range getChannelsByType(cfg, ct) {
				existingNames[ct][ch.Name] = true
			}
		}

		// 本次导入内部去重
		importedNames := make(map[string]map[string]bool)
		for _, ct := range ValidChannelTypes {
			importedNames[ct] = make(map[string]bool)
		}

		imported := make([]string, 0)
		importErrors := make([]string, 0)

		for i, entry := range req.Pack.Channels {
			if !isValidChannelType(entry.ChannelType) {
				importErrors = append(importErrors, fmt.Sprintf("第 %d 条: 无效渠道类型 %s", i+1, entry.ChannelType))
				continue
			}

			ch := entry.Channel

			// 不复用原始 ChannelUID，强制重新生成
			ch.ChannelUID = ""

			// 名称冲突处理（以首个 baseURL 派生名为准，与 Add 的落库结果一致）
			name := deriveImportChannelName(ch)
			if existingNames[entry.ChannelType][name] || importedNames[entry.ChannelType][name] {
				if req.SkipNaming {
					// 自动重命名：追加 -import 后缀和序号
					baseName := name
					seq := 1
					for {
						name = fmt.Sprintf("%s-import-%d", baseName, seq)
						if !existingNames[entry.ChannelType][name] && !importedNames[entry.ChannelType][name] {
							break
						}
						seq++
						if seq > 100 {
							importErrors = append(importErrors, fmt.Sprintf("第 %d 条: 无法为 '%s' 生成唯一名称", i+1, baseName))
							break
						}
					}
					if seq > 100 {
						continue
					}
				} else {
					importErrors = append(importErrors, fmt.Sprintf("第 %d 条: 渠道 '%s' (类型: %s) 名称冲突，跳过", i+1, name, entry.ChannelType))
					continue
				}
			}

			ch.Name = name

			// 设置默认值（复用现有创建逻辑的 backfill）
			if ch.Status == "" {
				ch.Status = "active"
			}
			if ch.OriginType == "" {
				ch.OriginType = "unknown"
			}
			if ch.OriginTier == "" {
				ch.OriginTier = "unknown"
			}

			// 通过 ConfigManager 的 Add 方法导入，复用现有的迁移/校验/backfill 函数
			var addErr error
			switch entry.ChannelType {
			case "messages":
				addErr = cfgManager.AddUpstream(ch)
			case "chat":
				addErr = cfgManager.AddChatUpstream(ch)
			case "responses":
				addErr = cfgManager.AddResponsesUpstream(ch)
			case "gemini":
				addErr = cfgManager.AddGeminiUpstream(ch)
			case "images":
				addErr = cfgManager.AddImagesUpstream(ch)
			case "vectors":
				addErr = cfgManager.AddVectorsUpstream(ch)
			}

			if addErr != nil {
				importErrors = append(importErrors, fmt.Sprintf("第 %d 条 (%s): %s", i+1, name, addErr.Error()))
				continue
			}

			importedNames[entry.ChannelType][name] = true
			imported = append(imported, fmt.Sprintf("%s (%s)", name, entry.ChannelType))
		}

		log.Printf("[ChannelBatch-Import] 导入完成: 成功 %d, 失败 %d", len(imported), len(importErrors))

		c.JSON(http.StatusOK, gin.H{
			"imported": imported,
			"errors":   importErrors,
			"total":    len(req.Pack.Channels),
		})
	}
}

// GetChannelTemplates 返回内置渠道模板（常见 provider 的预配置模板）。
// 模板以渠道包格式返回，用户可编辑后再导入。
func GetChannelTemplates() gin.HandlerFunc {
	return func(c *gin.Context) {
		templates := getBuiltinTemplates()
		c.JSON(http.StatusOK, gin.H{
			"templates": templates,
		})
	}
}

// GetProviderTemplates 返回内置 provider 模板（已知 provider 的模板化添加配置）。
// 前端据此渲染 provider 选择器：用户选 provider + 输 key，系统自动判别 plan/baseURL 并验证。
func GetProviderTemplates() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"providers": config.ListProviderTemplates(),
		})
	}
}

// getBuiltinTemplates 返回内置渠道模板列表。
func getBuiltinTemplates() []gin.H {
	return []gin.H{
		{
			"name":        "Anthropic Claude (官方)",
			"description": "Anthropic 官方 Claude API",
			"pack": ChannelPack{
				Version: 1,
				Channels: []ChannelPackEntry{
					{
						ChannelType: "messages",
						Channel: config.UpstreamConfig{
							Name:        "Anthropic Claude",
							BaseURL:     "https://api.anthropic.com",
							ServiceType: "claude",
							Status:      "active",
							Tags:        []string{"official", "anthropic"},
						},
					},
				},
			},
		},
		{
			"name":        "OpenAI Chat (官方)",
			"description": "OpenAI 官方 Chat Completions API",
			"pack": ChannelPack{
				Version: 1,
				Channels: []ChannelPackEntry{
					{
						ChannelType: "chat",
						Channel: config.UpstreamConfig{
							Name:        "OpenAI Chat",
							BaseURL:     "https://api.openai.com/v1",
							ServiceType: "openai",
							Status:      "active",
							Tags:        []string{"official", "openai"},
						},
					},
				},
			},
		},
		{
			"name":        "OpenAI Responses (官方)",
			"description": "OpenAI 官方 Responses API（Codex 兼容）",
			"pack": ChannelPack{
				Version: 1,
				Channels: []ChannelPackEntry{
					{
						ChannelType: "responses",
						Channel: config.UpstreamConfig{
							Name:        "OpenAI Responses",
							BaseURL:     "https://api.openai.com/v1",
							ServiceType: "openai",
							Status:      "active",
							Tags:        []string{"official", "openai", "codex"},
						},
					},
				},
			},
		},
		{
			"name":        "Google Gemini (官方)",
			"description": "Google 官方 Gemini API",
			"pack": ChannelPack{
				Version: 1,
				Channels: []ChannelPackEntry{
					{
						ChannelType: "gemini",
						Channel: config.UpstreamConfig{
							Name:        "Google Gemini",
							BaseURL:     "https://generativelanguage.googleapis.com",
							ServiceType: "gemini",
							Status:      "active",
							Tags:        []string{"official", "google", "gemini"},
						},
					},
				},
			},
		},
		{
			"name":        "DeepSeek (国产)",
			"description": "DeepSeek Chat API（OpenAI 兼容协议）",
			"pack": ChannelPack{
				Version: 1,
				Channels: []ChannelPackEntry{
					{
						ChannelType: "chat",
						Channel: config.UpstreamConfig{
							Name:        "DeepSeek",
							BaseURL:     "https://api.deepseek.com",
							ServiceType: "openai",
							Status:      "active",
							Tags:        []string{"official", "deepseek", "domestic"},
						},
					},
				},
			},
		},
	}
}

// requireAdminAuth 二次确认 admin 鉴权（用于 includeKeys=true 的凭证导出场景）。
//
// WebAuthMiddleware 通过 IsValidAdminAccessKey 准入所有 /api/* 请求：
//   - 当 ADMIN_ACCESS_KEY 显式设置时，仅接受 admin key
//   - 当 ADMIN_ACCESS_KEY 未设置且无 EXTRA_PROXY_ACCESS_KEYS 时，回退接受唯一的 ProxyAccessKey
//   - 当 EXTRA_PROXY_ACCESS_KEYS 存在时，拒绝所有非 admin key 的请求（必须显式 ADMIN_ACCESS_KEY）
//
// 此函数在 handler 层再次校验，确保 includeKeys=true 时 proxy-only key 无法越权导出凭证。
// （mem:feedback_extra_proxy_keys_admin 边界契约。）
func requireAdminAuth(c *gin.Context, envCfg *config.EnvConfig) bool {
	providedKey := getAPIKeyFromRequest(c)
	return envCfg.IsValidAdminAccessKey(providedKey)
}

// getAPIKeyFromRequest 从请求中提取 API key（与 middleware/auth.go 逻辑一致）。
func getAPIKeyFromRequest(c *gin.Context) string {
	if key := c.GetHeader("x-api-key"); key != "" {
		return key
	}
	if auth := c.GetHeader("Authorization"); auth != "" {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if key := c.GetHeader("x-goog-api-key"); key != "" {
		return key
	}
	return ""
}
