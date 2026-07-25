package autopilot

import (
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func customAutoAddRouteName(baseName, kind string, multiRoute bool) string {
	if !multiRoute {
		return baseName
	}
	return baseName + accountRouteSuffix(kind)
}

// buildCustomManagedProtocolRoute 统一首次 Auto Add、追加协议与重新发现补建的渠道身份字段。
// seed 只承载凭证、端点、模型及其他可继承配置，协议身份由本函数统一覆盖。
func buildCustomManagedProtocolRoute(
	seed config.UpstreamConfig,
	accountUID string,
	baseName string,
	kind string,
	multiRoute bool,
	managedAt time.Time,
) config.UpstreamConfig {
	seed.AccountUID = accountUID
	seed.ChannelUID = config.GenerateChannelUID()
	seed.ProviderID = ""
	seed.ServiceType = kindToDefaultServiceType(kind)
	seed.Name = customAutoAddRouteName(baseName, kind, multiRoute)
	seed.Status = "active"
	seed.AutoManaged = true
	managedAtCopy := managedAt
	seed.AutoManagedAt = &managedAtCopy
	return seed
}
