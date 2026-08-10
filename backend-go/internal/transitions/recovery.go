package transitions

import (
	"time"

	"github.com/BenedictKing/ccx/internal/eventbus"
	"github.com/BenedictKing/ccx/internal/metrics"
)

// RestoreAndHalfOpenResult 描述一次恢复编排结果。
type RestoreAndHalfOpenResult struct {
	RestoredKeys     []string
	ActivatedChannel bool
}

// RestoreDisabledKeysAndActivate 收口“恢复 disabled keys + half-open + 激活渠道”的编排。
// publishChannelStatus 可选；当 activatedChannel 为 true 时调用，用于发布 channel_status_changed 事件。
func RestoreDisabledKeysAndActivate(
	restoreDisabledKeys func([]string) ([]string, error),
	moveKeyToHalfOpen func(baseURL, apiKey string),
	setChannelStatus func(string) error,
	shouldActivateChannel func() bool,
	keysToRestore []string,
	publishChannelStatus func(oldStatus, newStatus string),
) (RestoreAndHalfOpenResult, error) {
	_ = (*metrics.MetricsManager)(nil)
	result := RestoreAndHalfOpenResult{}
	if len(keysToRestore) == 0 {
		return result, nil
	}

	restoredKeys, err := restoreDisabledKeys(keysToRestore)
	if err != nil {
		return result, err
	}
	if len(restoredKeys) == 0 {
		return result, nil
	}

	for _, apiKey := range restoredKeys {
		moveKeyToHalfOpen("", apiKey)
	}

	result.RestoredKeys = restoredKeys
	if shouldActivateChannel != nil && shouldActivateChannel() {
		if err := setChannelStatus("active"); err != nil {
			return result, err
		}
		result.ActivatedChannel = true
		if publishChannelStatus != nil {
			publishChannelStatus("suspended", "active")
		}
	}
	return result, nil
}

// PublishChannelStatusEvent 构造一个 channel_status_changed 发布回调。
// bus 为 nil 时返回的回调为空操作；channelUID 为空时也返回空操作。
func PublishChannelStatusEvent(bus *eventbus.Bus, channelUID, channelName, kind string) func(oldStatus, newStatus string) {
	if bus == nil || channelUID == "" {
		return nil
	}
	return func(oldStatus, newStatus string) {
		if oldStatus == "" {
			oldStatus = "active"
		}
		if newStatus == "" {
			newStatus = "active"
		}
		if oldStatus == newStatus {
			return
		}
		now := time.Now().UTC()
		bus.Publish(eventbus.Event{
			UID:         "",
			Type:        eventbus.TypeChannelStatusChanged,
			Scope:       eventbus.ScopeConfig,
			Subject:     channelUID,
			ChannelKind: kind,
			From:        oldStatus,
			To:          newStatus,
			Cause:       "scheduled_recovery",
			Payload: map[string]any{
				"channelUID":  channelUID,
				"channelName": channelName,
				"kind":        kind,
				"oldStatus":   oldStatus,
				"newStatus":   newStatus,
				"reason":      "scheduled_recovery",
				"timestamp":   now.Unix(),
			},
			CreatedAt: now,
		})
	}
}
