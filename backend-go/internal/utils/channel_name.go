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
	reSlugNonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)
	reSlugLeadingTrailing = regexp.MustCompile(`^-+|-+$`)
	reSlugRepeatedDash    = regexp.MustCompile(`-{2,}`)
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
		if ip.To4() != nil {
			return appendChannelNamePort(strings.ReplaceAll(hostname, ".", "-"), parsed.Port())
		}
		if slug := slugifyChannelNamePart(appendChannelNamePort("ipv6-"+hostname, parsed.Port())); slug != "" {
			return slug
		}
		return "ipv6"
	}

	labels := make([]string, 0, 4)
	for _, part := range strings.Split(hostname, ".") {
		if slug := slugifyChannelNamePart(part); slug != "" {
			labels = append(labels, slug)
		}
	}
	if len(labels) == 0 {
		return "channel"
	}
	if len(labels) == 1 {
		return appendChannelNamePort(labels[0], parsed.Port())
	}

	meaningful := fitChannelNamePrefix(dropGenericLeadingChannelNameLabels(labels))
	if len(meaningful) == 0 {
		return "channel"
	}
	return appendChannelNamePort(strings.Join(meaningful, "-"), parsed.Port())
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
