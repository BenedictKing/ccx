package scheduler

import (
	"github.com/BenedictKing/ccx/internal/config"
)

func (s *ChannelScheduler) ResolveSubagentRouting(kind ChannelKind, agentRole, confidence string) (*config.SubagentRoutingPolicy, bool) {
	return config.ResolveSubagentRouting(s.configManager.GetConfig(), string(kind), agentRole, confidence)
}

func (s *ChannelScheduler) HasSubagentRouting(kind ChannelKind, agentRole, confidence string) bool {
	policy, apply := s.ResolveSubagentRouting(kind, agentRole, confidence)
	return policy != nil && apply
}

func (s *ChannelScheduler) ApplySubagentRoutingProfile(requirement *ContextRequirement, policy *config.SubagentRoutingPolicy, apply bool) {
	if requirement == nil || !apply || policy == nil || policy.Model == "" {
		return
	}
	resolved := config.ResolveAgentModelProfile(policy.Model, s.configManager.GetConfig().AgentModelProfiles)
	if resolved.Known && resolved.MatchedPattern != "*" && resolved.Profile.ContextWindowTokens > 0 {
		requirement.MinimumContextWindowTokens = resolved.Profile.ContextWindowTokens
	}
}
