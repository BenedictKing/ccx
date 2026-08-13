package utils

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

// genericHostPrefixes 主机前缀剥离白名单。
// 规则要求只去掉 "www"，其余 hostname label（包括 api 与顶级域名）都保留，
// 使 api.example.com -> api-example-com，www.example.com -> example-com。
var genericHostPrefixes = map[string]struct{}{
	"www": {},
}

const maxChannelNamePrefixLength = 40

var (
	reSlugNonAlphanumeric       = regexp.MustCompile(`[^a-z0-9]+`)
	reSlugLeadingTrailing       = regexp.MustCompile(`^-+|-+$`)
	reSlugRepeatedDash          = regexp.MustCompile(`-{2,}`)
	reChannelNameVersionSegment = regexp.MustCompile(`^v\d+[a-z]*$`)
)

// DeriveChannelNameFromBaseURL 根据首个 baseURL 派生渠道名称。
// 规则：hostname 只去 www 前缀，其余 label（含 api 与顶级域名）全部保留；
// IPv4 点转横线、IPv6 加前缀，附加端口；解析失败或无有效主机时回退 "channel"。
// 与前端 extractChannelNamePrefix 保持一致。
func DeriveChannelNameFromBaseURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "channel"
	}
	hostname := strings.ToLower(strings.Trim(parsed.Hostname(), "[]"))
	if hostname == "" {
		return "channel"
	}

	if ip := net.ParseIP(hostname); ip != nil {
		prefix := strings.ReplaceAll(hostname, ".", "-")
		if ip.To4() == nil {
			prefix = "ipv6-" + hostname
		}
		parts := append([]string{slugifyChannelNamePart(prefix)}, channelNamePathParts(parsed.Path)...)
		parts = fitChannelNamePrefix(parts)
		if len(parts) > 0 {
			return appendChannelNamePort(strings.Join(parts, "-"), parsed.Port())
		}
		return "channel"
	}

	parts := make([]string, 0, 6)
	for _, part := range strings.Split(hostname, ".") {
		if slug := slugifyChannelNamePart(part); slug != "" {
			parts = append(parts, slug)
		}
	}
	if len(parts) == 0 {
		return "channel"
	}
	parts = dropGenericLeadingChannelNameLabels(parts)
	parts = append(parts, channelNamePathParts(parsed.Path)...)
	parts = fitChannelNamePrefix(parts)
	if len(parts) == 0 {
		return "channel"
	}
	return appendChannelNamePort(strings.Join(parts, "-"), parsed.Port())
}

// channelNamePathParts 返回应进入自动名称的非标准路径段。
// 尾部版本段（v1/v1beta 等）不参与命名；路径仅为 api/<版本> 时连 api 一并剥离。
func channelNamePathParts(path string) []string {
	parts := make([]string, 0, 4)
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if slug := slugifyChannelNamePart(part); slug != "" {
			parts = append(parts, slug)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	if reChannelNameVersionSegment.MatchString(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
		if len(parts) == 1 && parts[0] == "api" {
			return nil
		}
	}
	return parts
}

func slugifyChannelNamePart(value string) string {
	slug := reSlugNonAlphanumeric.ReplaceAllString(strings.ToLower(value), "-")
	slug = reSlugLeadingTrailing.ReplaceAllString(slug, "")
	return reSlugRepeatedDash.ReplaceAllString(slug, "-")
}

func appendChannelNamePort(prefix, port string) string {
	if port == "" {
		return prefix
	}
	return prefix + "-" + port
}

func dropGenericLeadingChannelNameLabels(labels []string) []string {
	result := append([]string(nil), labels...)
	for len(result) > 1 {
		if _, ok := genericHostPrefixes[result[0]]; !ok {
			break
		}
		result = result[1:]
	}
	return result
}

func fitChannelNamePrefix(labels []string) []string {
	result := labels
	for len(result) > 1 && len(strings.Join(result, "-")) > maxChannelNamePrefixLength {
		result = result[1:]
	}
	return result
}
