package autopilot

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/BenedictKing/ccx/internal/config"
)

type OptionalFloat64 struct {
	Present bool
	Valid   bool
	Value   float64
}

func (o *OptionalFloat64) UnmarshalJSON(data []byte) error {
	o.Present = true
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		o.Valid = false
		o.Value = 0
		return nil
	}
	var value float64
	if err := jsonUnmarshalFloat64(data, &value); err != nil {
		return err
	}
	o.Valid = true
	o.Value = value
	return nil
}

func (o OptionalFloat64) IsSet() bool {
	return o.Present && o.Valid
}

func (o OptionalFloat64) IsCleared() bool {
	return o.Present && !o.Valid
}

type keyMultiplierPatchRequest struct {
	GroupMultiplier    OptionalFloat64           `json:"groupMultiplier"`
	MaxGroupMultiplier OptionalFloat64           `json:"maxGroupMultiplier"`
	ConsumptionPolicy  OptionalConsumptionPolicy `json:"consumptionPolicy,omitempty"`
}

// OptionalConsumptionPolicy 支持 JSON 三态：缺失 / null / 具体值。
type OptionalConsumptionPolicy struct {
	Present bool
	Valid   bool
	Value   config.KeyConsumptionPolicy
}

func (o *OptionalConsumptionPolicy) UnmarshalJSON(data []byte) error {
	o.Present = true
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		o.Valid = false
		o.Value = ""
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	policy := config.KeyConsumptionPolicy(strings.TrimSpace(value))
	if policy != "" && !config.IsKeyConsumptionPolicyValid(policy) {
		return fmt.Errorf("consumptionPolicy 必须是 normal 或 opportunistic")
	}
	o.Valid = true
	o.Value = policy
	return nil
}

type keyMultiplierResponse struct {
	KeyUID             string                      `json:"keyUid"`
	Group              string                      `json:"group"`
	RemoteMultiplier   *float64                    `json:"remoteMultiplier,omitempty"`
	GroupMultiplier    *float64                    `json:"groupMultiplier,omitempty"`
	MaxMultiplier      *float64                    `json:"maxMultiplier,omitempty"`
	ConsumptionPolicy  config.KeyConsumptionPolicy `json:"consumptionPolicy,omitempty"`
	EffectiveCostClass string                      `json:"effectiveCostClass,omitempty"`
	Status             string                      `json:"status"`
	Reason             string                      `json:"reason"`
	Eligible           bool                        `json:"eligible"`
	UpdatedAt          *time.Time                  `json:"updatedAt,omitempty"`
	ExpiresAt          *time.Time                  `json:"expiresAt,omitempty"`
}

func RegisterKeyMultiplierRoutes(router gin.IRouter, cfgManager *config.ConfigManager) {
	if router == nil || cfgManager == nil {
		return
	}
	router.PATCH("/:kind/channels/:channelUid/keys/:keyUid/multiplier", handlePatchKeyMultiplier(cfgManager))
}

func handlePatchKeyMultiplier(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		kind := strings.TrimSpace(c.Param("kind"))
		channelUID := strings.TrimSpace(c.Param("channelUid"))
		keyUID := strings.TrimSpace(c.Param("keyUid"))
		if kind == "" || channelUID == "" || keyUID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "kind、channelUid、keyUid 不能为空"})
			return
		}
		apiType, err := normalizeChannelKind(kind)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		var req keyMultiplierPatchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}
		if !req.GroupMultiplier.Present && !req.MaxGroupMultiplier.Present && !req.ConsumptionPolicy.Present {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少提供一个字段"})
			return
		}
		if req.GroupMultiplier.IsSet() && !isFiniteNonNegativeValue(req.GroupMultiplier.Value) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "groupMultiplier 必须是有限且非负数"})
			return
		}
		if req.MaxGroupMultiplier.IsSet() && !isFiniteNonNegativeValue(req.MaxGroupMultiplier.Value) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "maxGroupMultiplier 必须是有限且非负数"})
			return
		}
		if req.ConsumptionPolicy.Present && req.ConsumptionPolicy.Valid {
			if !config.IsKeyConsumptionPolicyValid(req.ConsumptionPolicy.Value) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "consumptionPolicy 必须是 normal 或 opportunistic"})
				return
			}
		}

		channelIndex, upstream, err := findUpstreamByChannelUID(cfgManager, apiType, channelUID)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "不存在") {
				status = http.StatusNotFound
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		cfgIndex, current, err := findAPIKeyConfigByKeyUID(*upstream, keyUID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}

		next := cloneAPIKeyConfigForPatch(current)
		isNewAPI := strings.EqualFold(strings.TrimSpace(next.MultiplierSource), "new_api")
		if isNewAPI && req.GroupMultiplier.Present {
			c.JSON(http.StatusConflict, gin.H{"error": "new_api key 的 groupMultiplier 由远端同步，不能手动修改"})
			return
		}

		now := time.Now().UTC()
		if req.GroupMultiplier.Present {
			if req.GroupMultiplier.Valid {
				value := req.GroupMultiplier.Value
				next.GroupMultiplier = &value
			} else {
				next.GroupMultiplier = nil
			}
			next.MultiplierUpdatedAt = &now
		}
		if req.MaxGroupMultiplier.Present {
			if req.MaxGroupMultiplier.Valid {
				value := req.MaxGroupMultiplier.Value
				next.MaxGroupMultiplier = &value
			} else {
				next.MaxGroupMultiplier = nil
			}
			next.MultiplierUpdatedAt = &now
		}
		if req.ConsumptionPolicy.Present {
			if req.ConsumptionPolicy.Valid {
				next.ConsumptionPolicy = config.NormalizeKeyConsumptionPolicy(req.ConsumptionPolicy.Value)
			} else {
				next.ConsumptionPolicy = config.KeyConsumptionNormal
			}
		}

		// 当 GroupMultiplier 被显式设置或当前非 nil 时，必须同时有 MaxGroupMultiplier；否则 400。
		// 注意：0 是合法倍率，不能通过 Value > 0 判断“是否设置”。
		groupWillBeSet := req.GroupMultiplier.IsSet()
		if groupWillBeSet && next.MaxGroupMultiplier == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "设置 groupMultiplier 时必须同时提供 maxGroupMultiplier"})
			return
		}
		// 当只提供 maxGroupMultiplier 而未提供 groupMultiplier 且当前也无 groupMultiplier 时，同样无法构成合法配对。
		// 但允许单独设置 maxGroupMultiplier 以收紧已有配对的上限；此情况 next.GroupMultiplier 非 nil 已在上一条处理。

		if !isNewAPI {
			next.MultiplierSource = "manual"
			if next.GroupMultiplier == nil && next.MaxGroupMultiplier == nil {
				next.MultiplierSyncStatus = ""
				next.MultiplierSyncError = ""
				next.MultiplierExpiresAt = nil
				next.SourceSubscriptionUID = ""
				next.SourceRemoteTokenID = 0
			} else {
				next.MultiplierSyncStatus = "manual"
				next.MultiplierSyncError = ""
				next.MultiplierExpiresAt = nil
			}
		} else if next.MaxGroupMultiplier != nil {
			if next.GroupMultiplier != nil && *next.GroupMultiplier > *next.MaxGroupMultiplier {
				next.MultiplierSyncStatus = "over_limit"
			} else if strings.TrimSpace(next.MultiplierSyncStatus) == "" {
				next.MultiplierSyncStatus = "fresh"
			}
		}

		response := buildKeyMultiplierResponse(keyUID, next, time.Now())
		updates := config.UpstreamUpdate{APIKeyConfigs: patchAPIKeyConfigAt(upstream.APIKeyConfigs, cfgIndex, next)}
		if err := updateUpstreamByType(cfgManager, apiType, channelIndex, updates); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

func buildKeyMultiplierResponse(keyUID string, cfg config.APIKeyConfig, now time.Time) keyMultiplierResponse {
	eligibility := config.EvaluateAPIKeyMultiplierEligibility(cfg, now)
	effectiveCostClass := deriveEffectiveCostClass(cfg, eligibility.Eligible)
	return keyMultiplierResponse{
		KeyUID:             keyUID,
		Group:              strings.TrimSpace(cfg.QuotaGroup),
		RemoteMultiplier:   remoteMultiplierForResponse(cfg),
		GroupMultiplier:    cloneFloat64Ptr(cfg.GroupMultiplier),
		MaxMultiplier:      cloneFloat64Ptr(cfg.MaxGroupMultiplier),
		ConsumptionPolicy:  cfg.ConsumptionPolicy,
		EffectiveCostClass: effectiveCostClass,
		Status:             eligibility.Status,
		Reason:             eligibility.Reason,
		Eligible:           eligibility.Eligible,
		UpdatedAt:          cloneTimePtr(cfg.MultiplierUpdatedAt),
		ExpiresAt:          cloneTimePtr(cfg.MultiplierExpiresAt),
	}
}

func deriveEffectiveCostClass(cfg config.APIKeyConfig, eligible bool) string {
	if !eligible {
		return ""
	}
	if cfg.GroupMultiplier == nil {
		return "list"
	}
	if *cfg.GroupMultiplier == 0 {
		return "zero"
	}
	if *cfg.GroupMultiplier < 1 {
		return "discounted"
	}
	if *cfg.GroupMultiplier == 1 {
		return "standard"
	}
	return "premium"
}

func remoteMultiplierForResponse(cfg config.APIKeyConfig) *float64 {
	if !strings.EqualFold(strings.TrimSpace(cfg.MultiplierSource), "new_api") {
		return nil
	}
	return cloneFloat64Ptr(cfg.GroupMultiplier)
}

func cloneAPIKeyConfigForPatch(cfg config.APIKeyConfig) config.APIKeyConfig {
	cloned := cfg
	if cfg.Enabled != nil {
		v := *cfg.Enabled
		cloned.Enabled = &v
	}
	cloned.GroupMultiplier = cloneFloat64Ptr(cfg.GroupMultiplier)
	cloned.MaxGroupMultiplier = cloneFloat64Ptr(cfg.MaxGroupMultiplier)
	cloned.MultiplierUpdatedAt = cloneTimePtr(cfg.MultiplierUpdatedAt)
	cloned.MultiplierExpiresAt = cloneTimePtr(cfg.MultiplierExpiresAt)
	if cfg.RateLimitAutoFromHeaders != nil {
		v := *cfg.RateLimitAutoFromHeaders
		cloned.RateLimitAutoFromHeaders = &v
	}
	if cfg.Models != nil {
		cloned.Models = append([]string(nil), cfg.Models...)
	}
	return cloned
}

func cloneFloat64Ptr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	copied := *v
	return &copied
}

func cloneTimePtr(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	copied := v.UTC()
	return &copied
}

func patchAPIKeyConfigAt(configs []config.APIKeyConfig, index int, next config.APIKeyConfig) []config.APIKeyConfig {
	patched := append([]config.APIKeyConfig(nil), configs...)
	patched[index] = next
	return patched
}

// findAPIKeyConfigByKeyUID 按 KeyUID 定位 key 配置；托管账号的手工 key 没有 KeyUID
// （KeyUID 仅 new-api 同步路径生成），但其 CredentialUID 在加载期由
// ensureCredentialUIDs 稳定回填，故兜底按 CredentialUID 匹配。
func findAPIKeyConfigByKeyUID(upstream config.UpstreamConfig, keyUID string) (int, config.APIKeyConfig, error) {
	keyUID = strings.TrimSpace(keyUID)
	for i, cfg := range upstream.APIKeyConfigs {
		if keyUID != "" && strings.TrimSpace(cfg.KeyUID) == keyUID {
			return i, cfg, nil
		}
	}
	for i, cfg := range upstream.APIKeyConfigs {
		if cred := strings.TrimSpace(cfg.CredentialUID); keyUID != "" && cred != "" && cred == keyUID {
			return i, cfg, nil
		}
	}
	return -1, config.APIKeyConfig{}, fmt.Errorf("keyUid=%s 不存在", keyUID)
}

func findUpstreamByChannelUID(cfgManager *config.ConfigManager, apiType, channelUID string) (int, *config.UpstreamConfig, error) {
	cfg := cfgManager.GetConfig()
	var upstreams []config.UpstreamConfig
	switch apiType {
	case "messages":
		upstreams = cfg.Upstream
	case "chat":
		upstreams = cfg.ChatUpstream
	case "responses":
		upstreams = cfg.ResponsesUpstream
	case "gemini":
		upstreams = cfg.GeminiUpstream
	case "images":
		upstreams = cfg.ImagesUpstream
	case "vectors":
		upstreams = cfg.VectorsUpstream
	default:
		return -1, nil, fmt.Errorf("不支持的渠道类型: %s", apiType)
	}
	for i := range upstreams {
		if strings.TrimSpace(upstreams[i].ChannelUID) == channelUID {
			copied := upstreams[i]
			return i, &copied, nil
		}
	}
	return -1, nil, fmt.Errorf("channelUid=%s 不存在", channelUID)
}

func updateUpstreamByType(cfgManager *config.ConfigManager, apiType string, index int, updates config.UpstreamUpdate) error {
	switch apiType {
	case "messages":
		_, err := cfgManager.UpdateUpstream(index, updates)
		return err
	case "chat":
		_, err := cfgManager.UpdateChatUpstream(index, updates)
		return err
	case "responses":
		_, err := cfgManager.UpdateResponsesUpstream(index, updates)
		return err
	case "gemini":
		_, err := cfgManager.UpdateGeminiUpstream(index, updates)
		return err
	case "images":
		_, err := cfgManager.UpdateImagesUpstream(index, updates)
		return err
	case "vectors":
		_, err := cfgManager.UpdateVectorsUpstream(index, updates)
		return err
	default:
		return fmt.Errorf("不支持的渠道类型: %s", apiType)
	}
}

func normalizeChannelKind(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "messages", "chat", "responses", "gemini", "images", "vectors":
		return strings.ToLower(strings.TrimSpace(kind)), nil
	default:
		return "", fmt.Errorf("不支持的 kind: %s", kind)
	}
}

func isFiniteNonNegativeValue(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func jsonUnmarshalFloat64(data []byte, target *float64) error {
	return json.Unmarshal(data, target)
}
