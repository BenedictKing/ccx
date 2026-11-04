package config

import (
	"sort"
	"strings"
)

// RedirectModel 按渠道配置返回实际发送给上游的模型名。
func RedirectModel(model string, upstream *UpstreamConfig) string {
	redirected, _ := RedirectModelWithMatch(model, upstream)
	return redirected
}

// RedirectModelWithMatch 返回模型重定向结果，并标记是否命中 ModelMapping。
func RedirectModelWithMatch(model string, upstream *UpstreamConfig) (string, bool) {
	if upstream == nil || len(upstream.ModelMapping) == 0 {
		return model, false
	}
	if mapped, ok := upstream.ModelMapping[model]; ok {
		return mapped, true
	}

	type mapping struct {
		source string
		target string
	}
	mappings := make([]mapping, 0, len(upstream.ModelMapping))
	for source, target := range upstream.ModelMapping {
		mappings = append(mappings, mapping{source: source, target: target})
	}
	sort.Slice(mappings, func(i, j int) bool {
		if len(mappings[i].source) != len(mappings[j].source) {
			return len(mappings[i].source) > len(mappings[j].source)
		}
		return mappings[i].source < mappings[j].source
	})
	for _, candidate := range mappings {
		if strings.Contains(model, candidate.source) {
			return candidate.target, true
		}
	}
	return model, false
}
