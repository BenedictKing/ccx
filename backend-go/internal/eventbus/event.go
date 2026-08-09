// Package eventbus 提供进程内、非阻塞的跨模块事件总线。
//
// 设计目标：让 metrics / config / preset / autopilot 等模块把关键状态变更
// 作为统一 Event 广播出去，供前端 WebSocket 与其他模块订阅，替代分散的回调与轮询。
//
// 该包是叶子包，仅依赖标准库，可被任意 internal 包安全 import（不引入包循环）。
// 总线为通知机制，不是新的真相源：订阅者丢事件不影响正确性，调度仍以配置/指标为准。
package eventbus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// 事件类型常量。新增类型时在此登记，避免各处硬编码字符串漂移。
const (
	TypeCircuitBreakerStateChanged = "circuit_breaker_state_changed"
	TypeKeyBlacklisted             = "key_blacklisted"
	TypeKeyRestored                = "key_restored"
	TypeKeyModelDisabled           = "key_model_disabled"
	TypeKeyModelRestored           = "key_model_restored"
	TypeConfigReloaded             = "config_reloaded"
	TypeUpstreamChanged            = "upstream_changed"
	TypeChannelStatusChanged       = "channel_status_changed"
	TypeLogicalChannelRebuilt      = "logical_channel_rebuilt"
	TypePresetBundleSwapped        = "preset_bundle_swapped"
)

// 事件作用域常量（标识发布来源模块）。
const (
	ScopeMetrics = "metrics"
	ScopeConfig  = "config"
	ScopePreset  = "preset"
)

// Event 是跨模块事件的统一 envelope。只承载已脱敏字段，
// 不包含明文 API Key / Authorization / prompt。
type Event struct {
	UID         string         `json:"uid"`
	Type        string         `json:"type"`
	Scope       string         `json:"scope"`
	Subject     string         `json:"subject,omitempty"`     // channelUID / metricsKey / logicalChannelUid 等
	ChannelKind string         `json:"channelKind,omitempty"` // messages / chat / ...（可选）
	From        string         `json:"from,omitempty"`        // 状态迁移前值
	To          string         `json:"to,omitempty"`          // 状态迁移后值
	Cause       string         `json:"cause,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// EnsureUID 在 UID 为空时按 (type, subject, createdAt) 生成稳定短 ID。
func (e *Event) EnsureUID() {
	if e.UID != "" {
		return
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", e.Type, e.Subject, e.CreatedAt.UnixNano())))
	e.UID = "ev_" + hex.EncodeToString(sum[:8])
}
