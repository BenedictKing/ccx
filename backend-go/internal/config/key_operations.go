package config

import (
	"slices"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// KeyOperationEvent 仅保留身份指纹、状态和固定原因，不保存明文 Key 或上游错误正文。
type KeyOperationEvent struct {
	Sequence   int64     `json:"sequence"`
	OccurredAt time.Time `json:"occurredAt"`
	APIType    string    `json:"apiType"`
	ChannelUID string    `json:"channelUid"`
	KeyID      string    `json:"keyId"`
	KeyMask    string    `json:"keyMask"`
	Action     string    `json:"action"`
	Source     string    `json:"source"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Outcome    string    `json:"outcome"`
	Reason     string    `json:"reason,omitempty"`
}

// SetKeyOperationObserver 回调在配置锁内执行，必须非阻塞且不得回调 ConfigManager。
func (cm *ConfigManager) SetKeyOperationObserver(observer func(KeyOperationEvent)) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.keyOperationObserver = observer
}

func keyOperationState(upstream *UpstreamConfig, key string) string {
	for _, disabled := range upstream.DisabledAPIKeys {
		if disabled.Key == key {
			return "blacklisted"
		}
	}
	if slices.Contains(upstream.APIKeys, key) {
		for _, cfg := range upstream.APIKeyConfigs {
			if cfg.Key == key && cfg.Enabled != nil && !*cfg.Enabled {
				return "paused"
			}
		}
		return "active"
	}
	return "missing"
}

func KeyState(upstream *UpstreamConfig, key string) string {
	state := keyOperationState(upstream, key)
	if state == "missing" && slices.Contains(upstream.HistoricalAPIKeys, key) {
		return "historical"
	}
	return state
}

func disabledKeys(keys []DisabledKeyInfo) []string {
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key.Key)
	}
	return result
}

func (cm *ConfigManager) observeKeyOperationLocked(apiType string, index int, key, action, source, reason string) func(*error) {
	observer := cm.keyOperationObserver
	channels := cm.getUpstreamSliceLocked(apiType)
	if observer == nil || channels == nil || index < 0 || index >= len(*channels) || key == "" {
		return func(*error) {}
	}
	channel := &(*channels)[index]
	uid, before := channel.ChannelUID, keyOperationState(channel, key)
	return func(result *error) {
		after := keyOperationState(channel, key)
		if uid == "" {
			uid = channel.ChannelUID
		}
		outcome := "success"
		if *result != nil {
			outcome = "failed"
		} else if before == after {
			if source == "automatic" {
				return
			}
			outcome = "unchanged"
		}
		observer(KeyOperationEvent{OccurredAt: time.Now(), APIType: strings.ToLower(apiType), ChannelUID: uid,
			KeyID: utils.APIKeyID(key), KeyMask: utils.MaskAPIKey(key), Action: action, Source: source,
			From: before, To: after, Outcome: outcome, Reason: keyOperationReason(reason)})
	}
}

func keyOperationReason(reason string) string {
	switch reason {
	case "insufficient_balance", "unauthorized", "invalid", "unavailable", "rate_limited", "authentication_error", "permission_error", "remote_token_deleted":
		return reason
	case "":
		return ""
	default:
		return "other"
	}
}
