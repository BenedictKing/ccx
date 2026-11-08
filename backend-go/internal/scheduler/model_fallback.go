package scheduler

import "github.com/BenedictKing/ccx/internal/config"

func (s *ChannelScheduler) ModelFallbackPolicy(kind ChannelKind, model, routePrefix string) *config.ModelFallbackPolicy {
	if routePrefix != "" {
		return nil
	} // 独立前缀渠道不继承默认 Agent 的路由选择
	return s.configManager.ResolveModelFallbackPolicy(string(kind), model)
}
