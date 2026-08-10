package config

// endpoint_capability.go 提供 EndpointCapability 的查询封装与"每周期探测去重台账"。
//
// 能力认知（协议+模型）按 CapabilityUID = (SiteIdentity, GroupIdentity, IdentityBaseURL, Protocol)
// 跨账号共享。本文件把 BuildChannelViews 产出的 map 封装成只读 Registry，并提供：
//   - 按 key 的 endpoint 解析可用模型（供调度/画像消费）；
//   - CapabilityProbeLedger：同一扫描周期内按 CapabilityUID 去重协议/模型探测，
//     使"同站点同分组的不同账号 key"只探一次，其余复用（凭证 auth 仍按 key 各自校验）。

import (
	"sort"
	"strings"
	"sync"
)

// EndpointCapabilityRegistry 是 CapabilityUID -> EndpointCapability 的只读封装。
type EndpointCapabilityRegistry struct {
	byUID map[string]EndpointCapability
}

// NewEndpointCapabilityRegistry 包装 BuildChannelViews 返回的能力表。
func NewEndpointCapabilityRegistry(caps map[string]EndpointCapability) *EndpointCapabilityRegistry {
	if caps == nil {
		caps = map[string]EndpointCapability{}
	}
	return &EndpointCapabilityRegistry{byUID: caps}
}

// Get 按 CapabilityUID 返回能力（副本），不存在时 ok=false。
func (r *EndpointCapabilityRegistry) Get(capUID string) (EndpointCapability, bool) {
	if r == nil {
		return EndpointCapability{}, false
	}
	c, ok := r.byUID[capUID]
	if !ok {
		return EndpointCapability{}, false
	}
	c.Models = append([]string(nil), c.Models...)
	return c, true
}

// Resolve 按 (site, group, identityBaseURL, protocol) 直接解析能力。
func (r *EndpointCapabilityRegistry) Resolve(site, group, identityBaseURL, protocol string) (EndpointCapability, bool) {
	return r.Get(GenerateCapabilityUID(site, group, identityBaseURL, protocol))
}

// ModelsForCapability 返回某能力的模型清单副本；不存在返回 nil。
func (r *EndpointCapabilityRegistry) ModelsForCapability(capUID string) []string {
	c, ok := r.Get(capUID)
	if !ok {
		return nil
	}
	return c.Models
}

// KeyEndpointCapabilityUIDs 返回某 ChannelView 下指定 KeyHash 绑定的全部 CapabilityUID（保序去重）。
func KeyEndpointCapabilityUIDs(view ChannelView, keyHash string) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, k := range view.Keys {
		if k.KeyHash != keyHash {
			continue
		}
		for _, e := range k.Endpoints {
			if _, ok := seen[e.CapabilityUID]; ok {
				continue
			}
			seen[e.CapabilityUID] = struct{}{}
			out = append(out, e.CapabilityUID)
		}
	}
	return out
}

// CapabilityProbeLedger 记录一个扫描周期内已探测的能力，供跨账号去重。
//
// 语义：协议/模型探测是"站点+分组"级事实，同 CapabilityUID 只需首个可用 key 探一次；
// 其余 key（含不同账号）复用结论。凭证 auth 校验（401/403）仍按 key 各自进行，不在此去重。
type CapabilityProbeLedger struct {
	mu     sync.Mutex
	probed map[string]struct{}
}

// NewCapabilityProbeLedger 创建空台账。
func NewCapabilityProbeLedger() *CapabilityProbeLedger {
	return &CapabilityProbeLedger{probed: make(map[string]struct{})}
}

// ClaimProbe 原子地占用某能力的探测权：首次调用返回 true（应探测），
// 之后同一 CapabilityUID 均返回 false（应复用已有结论）。nil 台账恒返回 true（不去重）。
func (l *CapabilityProbeLedger) ClaimProbe(capUID string) bool {
	if l == nil || strings.TrimSpace(capUID) == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.probed[capUID]; ok {
		return false
	}
	l.probed[capUID] = struct{}{}
	return true
}

// Reset 清空台账，供新扫描周期复用同一实例。
func (l *CapabilityProbeLedger) Reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.probed = make(map[string]struct{})
}

// ProbedCount 返回已占用的能力数量（测试/可观测用）。
func (l *CapabilityProbeLedger) ProbedCount() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.probed)
}

// sortedCapabilityUIDs 返回排序后的能力 UID 列表（确定性输出，供诊断）。
func sortedCapabilityUIDs(caps map[string]EndpointCapability) []string {
	out := make([]string, 0, len(caps))
	for uid := range caps {
		out = append(out, uid)
	}
	sort.Strings(out)
	return out
}
