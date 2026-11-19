package config

import (
	"fmt"
	"strings"
)

// ModelFallbackPolicy 是用户显式选定的模型顺序；不按请求内容猜测替代模型。
type ModelFallbackPolicy struct {
	Agent          string   `json:"agent,omitempty"`
	Protocol       string   `json:"protocol"`
	RequestModel   string   `json:"requestModel"`
	Models         []string `json:"models"`
	MaxAttempts    int      `json:"maxAttempts"`
	MaxWaitSeconds int      `json:"maxWaitSeconds"`
}

// ReplaceAgentModelFallbackPolicies 原子替换该 Agent 的别名规则，移除已撤销的覆盖。
func (cm *ConfigManager) ReplaceAgentModelFallbackPolicies(agent, protocol string, policies []ModelFallbackPolicy) error {
	for _, p := range policies {
		if p.Protocol != protocol || p.RequestModel == "" || len(p.Models) == 0 || p.MaxAttempts < 1 || p.MaxAttempts > 30 || p.MaxWaitSeconds < 1 || p.MaxWaitSeconds > 300 {
			return fmt.Errorf("模型路由或重试限制无效")
		}
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	before := cm.config.deepCopy()
	if cm.config.ModelFallbackPolicies == nil {
		cm.config.ModelFallbackPolicies = map[string]ModelFallbackPolicy{}
	}
	for key, p := range cm.config.ModelFallbackPolicies {
		if p.Agent == agent && p.Protocol == protocol {
			delete(cm.config.ModelFallbackPolicies, key)
		}
	}
	for _, p := range policies {
		p.Agent = agent
		p.Models = append([]string(nil), p.Models...)
		cm.config.ModelFallbackPolicies[protocol+":"+p.RequestModel] = p
	}
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.config = before
		return err
	}
	return nil
}

func (cm *ConfigManager) ResolveModelFallbackPolicy(protocol, model string) *ModelFallbackPolicy {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return ResolveModelFallbackPolicy(cm.config, protocol, model)
}

func ResolveModelFallbackPolicy(cfg Config, protocol, model string) *ModelFallbackPolicy {
	var found *ModelFallbackPolicy
	for _, policy := range cfg.ModelFallbackPolicies {
		if policy.Protocol != protocol || policy.RequestModel == "" || !strings.Contains(model, policy.RequestModel) || len(policy.Models) == 0 {
			continue
		}
		if found == nil || len(policy.RequestModel) > len(found.RequestModel) || (len(policy.RequestModel) == len(found.RequestModel) && policy.RequestModel < found.RequestModel) {
			copy := policy
			copy.Models = append([]string(nil), policy.Models...)
			found = &copy
		}
	}
	return found
}
