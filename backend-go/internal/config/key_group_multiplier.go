package config

import (
	"math"
	"strings"
	"time"
)

const (
	MultiplierEligibilityReasonOK                   = "ok"
	MultiplierEligibilityReasonInvalidMultiplier    = "invalid_multiplier"
	MultiplierEligibilityReasonInvalidMaxMultiplier = "invalid_max_multiplier"
	MultiplierEligibilityReasonOverGroupLimit       = "over_group_limit"
	MultiplierEligibilityReasonMultiplierStale      = "multiplier_stale"
	MultiplierEligibilityReasonSyncError            = "sync_error"
	MultiplierEligibilityReasonRelinkRequired       = "relink_required"
	MultiplierEligibilityReasonUnknownSource        = "unknown_source"
)

type MultiplierEligibility struct {
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason"`
	Status   string `json:"status"`
}

// EvaluateAPIKeyMultiplierEligibility 统一判断 Key 的倍率元数据是否允许参与调度。
func EvaluateAPIKeyMultiplierEligibility(cfg APIKeyConfig, now time.Time) MultiplierEligibility {
	status := normalizeMultiplierSyncStatus(cfg.MultiplierSyncStatus)
	if cfg.GroupMultiplier == nil && cfg.MaxGroupMultiplier == nil {
		return MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK, Status: status}
	}
	if cfg.GroupMultiplier == nil || !isFiniteNonNegative(*cfg.GroupMultiplier) {
		return MultiplierEligibility{Reason: MultiplierEligibilityReasonInvalidMultiplier, Status: status}
	}
	if cfg.MaxGroupMultiplier == nil || !isFiniteNonNegative(*cfg.MaxGroupMultiplier) {
		return MultiplierEligibility{Reason: MultiplierEligibilityReasonInvalidMaxMultiplier, Status: status}
	}
	if *cfg.GroupMultiplier > *cfg.MaxGroupMultiplier {
		return MultiplierEligibility{Reason: MultiplierEligibilityReasonOverGroupLimit, Status: status}
	}

	source := normalizeMultiplierSource(cfg.MultiplierSource)
	switch source {
	case "", "manual", "provider":
		return MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK, Status: status}
	case "new_api":
		if cfg.SourceSubscriptionUID == "" || cfg.SourceRemoteTokenID <= 0 {
			return MultiplierEligibility{Reason: MultiplierEligibilityReasonRelinkRequired, Status: status}
		}
		switch status {
		case "fresh":
			if cfg.MultiplierExpiresAt == nil || !cfg.MultiplierExpiresAt.After(now) {
				return MultiplierEligibility{Reason: MultiplierEligibilityReasonMultiplierStale, Status: status}
			}
			return MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK, Status: status}
		case "over_limit":
			return MultiplierEligibility{Reason: MultiplierEligibilityReasonOverGroupLimit, Status: status}
		case "sync_error":
			return MultiplierEligibility{Reason: MultiplierEligibilityReasonSyncError, Status: status}
		case "relink_required":
			return MultiplierEligibility{Reason: MultiplierEligibilityReasonRelinkRequired, Status: status}
		case "stale", "manual", "":
			return MultiplierEligibility{Reason: MultiplierEligibilityReasonMultiplierStale, Status: status}
		default:
			return MultiplierEligibility{Reason: MultiplierEligibilityReasonUnknownSource, Status: status}
		}
	default:
		return MultiplierEligibility{Reason: MultiplierEligibilityReasonUnknownSource, Status: status}
	}
}

// IsAPIKeyConfigGroupMultiplierAllowed 兼容旧调用方，内部复用统一 evaluator。
func IsAPIKeyConfigGroupMultiplierAllowed(cfg APIKeyConfig) bool {
	return EvaluateAPIKeyMultiplierEligibility(cfg, time.Now()).Eligible
}

// IsAPIKeyGroupMultiplierAllowed 返回渠道中某个 Key 是否满足其成本安全约束。
// 找不到对应配置时按历史手工 Key 处理，保持现有配置的兼容行为。
func (u *UpstreamConfig) IsAPIKeyGroupMultiplierAllowed(apiKey string) bool {
	if u == nil {
		return false
	}
	for _, cfg := range u.APIKeyConfigs {
		if cfg.Key == apiKey {
			return EvaluateAPIKeyMultiplierEligibility(cfg, time.Now()).Eligible
		}
	}
	return true
}

func normalizeMultiplierSource(source string) string {
	return strings.ToLower(strings.TrimSpace(source))
}

func normalizeMultiplierSyncStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func isFiniteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
