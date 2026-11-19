package scheduler

import "github.com/BenedictKing/ccx/internal/config"

func (s *ChannelScheduler) ModelFallbackPolicy(kind ChannelKind, model, routePrefix string, snapshots ...config.Config) *config.ModelFallbackPolicy {
	if routePrefix != "" {
		return nil
	} // 独立前缀渠道不继承默认 Agent 的路由选择
	if len(snapshots) > 0 {
		return config.ResolveModelFallbackPolicy(snapshots[0], string(kind), model)
	}
	return s.configManager.ResolveModelFallbackPolicy(string(kind), model)
}
