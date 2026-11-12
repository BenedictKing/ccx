package utils

import (
	"net/url"
	"strings"
)

// ForwardRequestQuery 保留客户端查询参数；上游路由参数优先，网关 query key 不出站。
func ForwardRequestQuery(target, source *url.URL) {
	if target == nil || source == nil || source.RawQuery == "" {
		return
	}
	existing := target.Query()
	parts := []string{}
	if target.RawQuery != "" {
		parts = append(parts, target.RawQuery)
	}
	for _, part := range strings.Split(source.RawQuery, "&") {
		key, _, _ := strings.Cut(part, "=")
		key, err := url.QueryUnescape(key)
		if err != nil || strings.EqualFold(key, "key") || existing.Has(key) {
			continue
		}
		parts = append(parts, part)
	}
	target.RawQuery = strings.Join(parts, "&")
}
