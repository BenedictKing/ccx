package siteaccess

import (
	"net/url"
	"strings"
)

// NormalizeSiteURL 把用户粘贴的中转站地址归一为站点根：
// 剥离面板页路径（/keys、/console/token 等）、端点后缀（/v1）与尾斜杠。
// 识别不出合法 URL 时回退为去空格的原始输入，由后续验证给出错误。
func NormalizeSiteURL(input string) string {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	u.RawQuery = ""
	u.Fragment = ""
	path := strings.TrimRight(u.Path, "/")
	// 面板页路径：首段命中面板页名即回到站点根（这些页面都不是 API 根）
	panelSegments := map[string]bool{
		"keys": true, "key": true, "tokens": true, "token": true,
		"console": true, "panel": true, "settings": true, "profile": true,
		"dashboard": true, "billing": true, "usage": true, "models": true,
	}
	if segments := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2); len(segments) > 0 && panelSegments[segments[0]] {
		path = ""
	}
	if idx := strings.Index(path, "/v1/"); idx >= 0 {
		// API 端点路径（/v1/chat/completions 等）：截到 /v1 之前，保留反代子路径
		path = path[:idx]
	}
	path = strings.TrimSuffix(path, "/v1")
	u.Path = strings.TrimRight(path, "/")
	return u.String()
}
