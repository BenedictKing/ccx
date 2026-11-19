package scheduler

import (
	"fmt"
	"slices"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
)

// restoreSubagentContinuation 固定成功来源；密文不能通过普通故障转移换上游。
func (s *ChannelScheduler) restoreSubagentContinuation(cfg *config.Config, opts *SelectionOptions) error {
	if opts.Kind != ChannelKindResponses || !opts.ApplySubagentRouting || !opts.PreserveTraceAffinity {
		return nil
	}
	failure := func(reason string) error {
		return fmt.Errorf("子代理加密续聊无法恢复成功来源（%s），请恢复原渠道配置或新建任务", reason)
	}
	if s.conversationTracker == nil {
		return failure("缺少成功记录")
	}
	source, ok := s.conversationTracker.GetSuccessfulChannel("responses", opts.UserID, opts.AgentRole)
	if !ok || source.ChannelUID == "" || source.Model == "" || source.RequestModel == "" {
		return failure("缺少实际成功模型或渠道 UID")
	}
	enabled := false
	if opts.SubagentRouting != nil {
		for _, entry := range opts.SubagentRouting.Channels {
			if entry.ChannelUID == source.ChannelUID {
				enabled = entry.Enabled
				break
			}
		}
	}
	if !enabled {
		return failure("来源已关闭")
	}
	for index := range cfg.ResponsesUpstream {
		upstream := &cfg.ResponsesUpstream[index]
		if upstream.ChannelUID != source.ChannelUID {
			continue
		}
		if opts.FailedChannels[index] || config.GetChannelStatus(upstream) == "suspended" || !s.channelIsRuntimeAvailable(upstream, opts.Kind, index) {
			return failure("来源不可用")
		}
		if opts.ChannelName != "" && opts.ChannelName != upstream.Name {
			return failure("指定渠道与成功来源不一致")
		}
		if source.ServiceType != "" && source.ServiceType != upstream.ServiceType {
			return failure("来源协议已改变")
		}
		if source.BaseURL != "" && !slices.Contains(upstream.GetAllBaseURLs(), source.BaseURL) {
			return failure("来源地址已改变")
		}
		pinned := upstream.Clone()
		if pinned.ModelMapping == nil {
			pinned.ModelMapping = make(map[string]string)
		}
		pinned.ModelMapping[source.RequestModel] = source.Model
		pinned.ModelMapping[source.Model] = source.Model
		if source.BaseURL != "" {
			pinned.BaseURL, pinned.BaseURLs = source.BaseURL, nil
		}
		cfg.ResponsesUpstream = append([]config.UpstreamConfig(nil), cfg.ResponsesUpstream...)
		cfg.ResponsesUpstream[index] = *pinned
		opts.SubagentRouting = &config.SubagentRoutingPolicy{
			Model:    source.RequestModel,
			Channels: []config.SubagentRoutingChannel{{ChannelUID: source.ChannelUID, Enabled: true}},
		}
		return nil
	}
	return failure("来源已删除")
}

func (s *ChannelScheduler) ConfigSnapshot() config.Config {
	return s.configManager.GetConfig()
}

func (s *ChannelScheduler) RecordSuccessfulResponse(userID, model, agentRole string, index int, upstream *config.UpstreamConfig, actualModel, baseURL string) {
	if s.conversationTracker == nil || upstream == nil {
		return
	}
	s.conversationTracker.RecordSuccessfulChannel("responses", userID, agentRole, conversation.SuccessfulChannel{
		ChannelEntry: conversation.ChannelEntry{ChannelIndex: index, ChannelName: upstream.Name},
		ChannelUID:   upstream.ChannelUID, RequestModel: config.RequestModelForAgent(model, upstream, agentRole),
		Model: actualModel, BaseURL: baseURL, ServiceType: upstream.ServiceType,
	})
}

func (s *ChannelScheduler) ResolveSubagentRouting(kind ChannelKind, agentRole, confidence string) (*config.SubagentRoutingPolicy, bool) {
	return config.ResolveSubagentRouting(s.configManager.GetConfig(), string(kind), agentRole, confidence)
}

func (s *ChannelScheduler) HasSubagentRouting(kind ChannelKind, agentRole, confidence string) bool {
	policy, apply := s.ResolveSubagentRouting(kind, agentRole, confidence)
	return policy != nil && apply
}

func (s *ChannelScheduler) ApplySubagentRoutingProfile(requirement *ContextRequirement, policy *config.SubagentRoutingPolicy, apply bool, snapshots ...config.Config) {
	if requirement == nil || !apply || policy == nil || policy.Model == "" {
		return
	}
	var cfg config.Config
	if len(snapshots) > 0 {
		cfg = snapshots[0]
	} else {
		cfg = s.ConfigSnapshot()
	}
	resolved := config.ResolveAgentModelProfile(policy.Model, cfg.AgentModelProfiles)
	if resolved.Known && resolved.MatchedPattern != "*" && resolved.Profile.ContextWindowTokens > 0 {
		requirement.MinimumContextWindowTokens = resolved.Profile.ContextWindowTokens
	}
}
