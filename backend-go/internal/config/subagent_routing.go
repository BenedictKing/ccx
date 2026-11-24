package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrSubagentRoutingRevisionConflict = errors.New("子代理配置已变化，请重新加载后合并修改")
	ErrInvalidSubagentRouting          = errors.New("无效的子代理配置")
	ErrSubagentLegacyModelConflict     = errors.New("该协议已启用独立子代理配置，请在渠道列表的子代理标签页修改默认模型")
)

type SubagentRoutingChannel struct {
	ChannelUID string `json:"channelUid"`
	Enabled    bool   `json:"enabled"`
}

type SubagentRoutingPolicy struct {
	Model    string                   `json:"model"`
	Channels []SubagentRoutingChannel `json:"channels"`
}

type SubagentRoutingLegacyModel struct {
	ChannelUID  string `json:"channelUid"`
	ChannelName string `json:"channelName"`
	Model       string `json:"model"`
}

type SubagentRoutingChannelView struct {
	ChannelUID     string            `json:"channelUid"`
	ChannelIndex   int               `json:"channelIndex"`
	ChannelName    string            `json:"channelName"`
	ServiceType    string            `json:"serviceType"`
	Status         string            `json:"status"`
	EffectiveState string            `json:"effectiveState"`
	Enabled        bool              `json:"enabled"`
	RequestModel   string            `json:"requestModel"`
	ResolvedModel  string            `json:"resolvedModel"`
	ModelSupported bool              `json:"modelSupported"`
	ModelMapping   map[string]string `json:"modelMapping,omitempty"`
}

type SubagentRoutingView struct {
	Protocol     string                       `json:"protocol"`
	Activated    bool                         `json:"activated"`
	Revision     string                       `json:"revision"`
	Policy       SubagentRoutingPolicy        `json:"policy"`
	Channels     []SubagentRoutingChannelView `json:"channels"`
	LegacyModels []SubagentRoutingLegacyModel `json:"legacyModels,omitempty"`
	LegacyMixed  bool                         `json:"legacyMixed"`
}

func NormalizeSubagentProtocol(protocol string) (string, bool) {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	switch protocol {
	case "messages", "responses", "chat", "gemini":
		return protocol, true
	default:
		return "", false
	}
}

func UpstreamsForProtocol(cfg Config, protocol string) []UpstreamConfig {
	switch protocol {
	case "messages":
		return cfg.Upstream
	case "responses":
		return cfg.ResponsesUpstream
	case "chat":
		return cfg.ChatUpstream
	case "gemini":
		return cfg.GeminiUpstream
	case "images":
		return cfg.ImagesUpstream
	case "vectors":
		return cfg.VectorsUpstream
	default:
		return nil
	}
}

func SubagentRoutingForConfig(cfg Config, protocol string) (*SubagentRoutingPolicy, bool) {
	policy, ok := cfg.SubagentRouting[protocol]
	if !ok {
		return nil, false
	}
	policy.Model = strings.TrimSpace(policy.Model)
	policy.Channels = append([]SubagentRoutingChannel(nil), policy.Channels...)
	return &policy, true
}

// appendMissingSubagentChannels 让新增渠道默认进入子代理选择空间；备用池渠道始终不补入。
func appendMissingSubagentChannels(cfg Config, protocol string, policy *SubagentRoutingPolicy) {
	if policy == nil {
		return
	}
	existing := make(map[string]struct{}, len(policy.Channels))
	for _, channel := range policy.Channels {
		existing[channel.ChannelUID] = struct{}{}
	}
	for _, upstream := range UpstreamsForProtocol(cfg, protocol) {
		if GetChannelStatus(&upstream) == "disabled" {
			continue
		}
		if _, ok := existing[upstream.ChannelUID]; ok {
			continue
		}
		policy.Channels = append(policy.Channels, SubagentRoutingChannel{ChannelUID: upstream.ChannelUID, Enabled: true})
		existing[upstream.ChannelUID] = struct{}{}
	}
}

func ResolveSubagentRouting(cfg Config, protocol, agentRole, confidence string) (*SubagentRoutingPolicy, bool) {
	policy, activated := SubagentRoutingForConfig(cfg, protocol)
	if !activated {
		return nil, false
	}
	appendMissingSubagentChannels(cfg, protocol, policy)
	return policy, agentRole == "subagent" && confidence == "exact"
}

func SubagentRoutingRevision(cfg Config, protocol string) string {
	policy, activated := SubagentRoutingForConfig(cfg, protocol)
	data, _ := json.Marshal(struct {
		Upstreams []UpstreamConfig       `json:"upstreams"`
		Policy    *SubagentRoutingPolicy `json:"policy"`
		Activated bool                   `json:"activated"`
	}{UpstreamsForProtocol(cfg, protocol), policy, activated})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func BuildSubagentRoutingView(cfg Config, protocol string) (SubagentRoutingView, error) {
	protocol, ok := NormalizeSubagentProtocol(protocol)
	if !ok {
		return SubagentRoutingView{}, fmt.Errorf("%w: 不支持的协议 %s", ErrInvalidSubagentRouting, protocol)
	}
	upstreams := UpstreamsForProtocol(cfg, protocol)
	policy, activated := SubagentRoutingForConfig(cfg, protocol)
	legacyModels := make([]SubagentRoutingLegacyModel, 0)
	uniqueModels := map[string]struct{}{}
	for _, upstream := range upstreams {
		if GetChannelStatus(&upstream) == "disabled" {
			continue
		}
		model := strings.TrimSpace(upstream.SubagentModel)
		if model == "" {
			continue
		}
		legacyModels = append(legacyModels, SubagentRoutingLegacyModel{ChannelUID: upstream.ChannelUID, ChannelName: upstream.Name, Model: model})
		uniqueModels[model] = struct{}{}
	}
	if !activated {
		policy = &SubagentRoutingPolicy{Channels: make([]SubagentRoutingChannel, 0, len(upstreams))}
		if len(uniqueModels) == 1 {
			for model := range uniqueModels {
				policy.Model = model
			}
		}
		type indexedUpstream struct {
			upstream UpstreamConfig
			index    int
		}
		ordered := make([]indexedUpstream, 0, len(upstreams))
		for i, upstream := range upstreams {
			if GetChannelStatus(&upstream) == "disabled" {
				continue
			}
			ordered = append(ordered, indexedUpstream{upstream: upstream, index: i})
		}
		sort.SliceStable(ordered, func(i, j int) bool {
			return GetChannelPriority(&ordered[i].upstream, ordered[i].index) < GetChannelPriority(&ordered[j].upstream, ordered[j].index)
		})
		for _, item := range ordered {
			policy.Channels = append(policy.Channels, SubagentRoutingChannel{ChannelUID: item.upstream.ChannelUID, Enabled: true})
		}
	}
	appendMissingSubagentChannels(cfg, protocol, policy)

	visibleChannels := make([]SubagentRoutingChannel, 0, len(policy.Channels))
	views := make([]SubagentRoutingChannelView, 0, len(policy.Channels))
	for _, entry := range policy.Channels {
		for index, upstream := range upstreams {
			if upstream.ChannelUID != entry.ChannelUID {
				continue
			}
			if GetChannelStatus(&upstream) == "disabled" {
				break
			}
			visibleChannels = append(visibleChannels, entry)
			requestModel := policy.Model
			resolvedModel := ""
			supported := true
			if requestModel != "" {
				resolvedModel = RedirectModel(requestModel, &upstream)
				supported, _ = upstream.ExplainModelSupport(requestModel)
			}
			views = append(views, SubagentRoutingChannelView{
				ChannelUID: upstream.ChannelUID, ChannelIndex: index, ChannelName: upstream.Name,
				ServiceType: upstream.ServiceType, Status: GetChannelStatus(&upstream), EffectiveState: GetChannelEffectiveState(&upstream),
				Enabled: entry.Enabled, RequestModel: requestModel, ResolvedModel: resolvedModel, ModelSupported: supported, ModelMapping: upstream.ModelMapping,
			})
			break
		}
	}
	policy.Channels = visibleChannels
	return SubagentRoutingView{
		Protocol: protocol, Activated: activated, Revision: SubagentRoutingRevision(cfg, protocol), Policy: *policy,
		Channels: views, LegacyModels: legacyModels, LegacyMixed: len(uniqueModels) > 1,
	}, nil
}

func (cm *ConfigManager) GetSubagentRouting(protocol string) (SubagentRoutingView, error) {
	cm.mu.RLock()
	cfg := cm.config.deepCopy()
	cm.mu.RUnlock()
	return BuildSubagentRoutingView(cfg, protocol)
}

func (cm *ConfigManager) UpdateSubagentRouting(protocol, revision string, policy SubagentRoutingPolicy) (SubagentRoutingView, error) {
	protocol, ok := NormalizeSubagentProtocol(protocol)
	if !ok {
		return SubagentRoutingView{}, fmt.Errorf("%w: 不支持的协议 %s", ErrInvalidSubagentRouting, protocol)
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if SubagentRoutingRevision(cm.config, protocol) != revision {
		return SubagentRoutingView{}, ErrSubagentRoutingRevisionConflict
	}
	upstreams := UpstreamsForProtocol(cm.config, protocol)
	validUIDs := make(map[string]struct{}, len(upstreams))
	for _, upstream := range upstreams {
		validUIDs[upstream.ChannelUID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(policy.Channels))
	for i := range policy.Channels {
		uid := strings.TrimSpace(policy.Channels[i].ChannelUID)
		if uid == "" {
			return SubagentRoutingView{}, fmt.Errorf("%w: channelUid 不能为空", ErrInvalidSubagentRouting)
		}
		if _, exists := validUIDs[uid]; !exists {
			return SubagentRoutingView{}, fmt.Errorf("%w: 渠道 UID 不属于 %s: %s", ErrInvalidSubagentRouting, protocol, uid)
		}
		if _, exists := seen[uid]; exists {
			return SubagentRoutingView{}, fmt.Errorf("%w: 渠道 UID 重复: %s", ErrInvalidSubagentRouting, uid)
		}
		seen[uid] = struct{}{}
		policy.Channels[i].ChannelUID = uid
	}
	policy.Model = strings.TrimSpace(policy.Model)
	policy.Channels = append([]SubagentRoutingChannel(nil), policy.Channels...)
	next := cm.config.deepCopy()
	if next.SubagentRouting == nil {
		next.SubagentRouting = make(map[string]SubagentRoutingPolicy)
	}
	next.SubagentRouting[protocol] = policy
	if err := cm.saveConfigLocked(next); err != nil {
		return SubagentRoutingView{}, err
	}
	return BuildSubagentRoutingView(cm.config, protocol)
}

func appendSubagentChannelLocked(cfg *Config, protocol, uid string) {
	policy, activated := SubagentRoutingForConfig(*cfg, protocol)
	if !activated {
		return
	}
	policy.Channels = append(policy.Channels, SubagentRoutingChannel{ChannelUID: uid, Enabled: true})
	cfg.SubagentRouting[protocol] = *policy
}

func removeSubagentChannelLocked(cfg *Config, protocol, uid string) {
	policy, activated := SubagentRoutingForConfig(*cfg, protocol)
	if !activated {
		return
	}
	channels := policy.Channels[:0]
	for _, channel := range policy.Channels {
		if channel.ChannelUID != uid {
			channels = append(channels, channel)
		}
	}
	policy.Channels = channels
	cfg.SubagentRouting[protocol] = *policy
}

func rejectLegacySubagentModelUpdate(cfg Config, protocol string, updates UpstreamUpdate) error {
	if updates.SubagentModel == nil {
		return nil
	}
	if _, activated := SubagentRoutingForConfig(cfg, protocol); activated {
		return ErrSubagentLegacyModelConflict
	}
	return nil
}

func WithSubagentRouting(upstream *UpstreamConfig, policy *SubagentRoutingPolicy, apply bool) *UpstreamConfig {
	if upstream == nil || policy == nil {
		return upstream
	}
	clone := upstream.Clone()
	clone.SubagentModel = ""
	if apply {
		clone.SubagentModel = strings.TrimSpace(policy.Model)
		if clone.SubagentModel != "" {
			if clone.ModelMapping == nil {
				clone.ModelMapping = make(map[string]string)
			}
			// 新策略沿用完整模型映射规则；旧渠道字段仍保持原兼容语义。
			clone.ModelMapping[clone.SubagentModel] = RedirectModel(clone.SubagentModel, upstream)
		}
	}
	return clone
}
