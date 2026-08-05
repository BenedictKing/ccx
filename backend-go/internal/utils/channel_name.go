package utils

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

// multiPartPublicSuffixes 常见的多段公共后缀，用于从 hostname 中提取有辨识度的主名
var multiPartPublicSuffixes = map[string]struct{}{
	"ac.cn": {}, "com.cn": {}, "edu.cn": {}, "gov.cn": {}, "net.cn": {}, "org.cn": {},
	"co.uk": {}, "org.uk": {}, "ac.uk": {}, "gov.uk": {},
	"com.au": {}, "net.au": {}, "org.au": {},
	"co.jp": {}, "ne.jp": {}, "or.jp": {},
	"co.kr": {}, "or.kr": {},
	"com.br": {}, "com.mx": {}, "com.sg": {}, "com.hk": {}, "com.tw": {}, "com.vn": {},
	"co.id": {}, "co.in": {}, "co.nz": {},
	"github.io": {}, "pages.dev": {}, "workers.dev": {}, "vercel.app": {},
	"netlify.app": {}, "onrender.com": {}, "railway.app": {},
}

// genericHostPrefixes 通用主机前缀，派生渠道名时剥离以获得更简洁的标识
var genericHostPrefixes = map[string]struct{}{
	"www": {}, "api": {}, "apis": {}, "openapi": {}, "gateway": {}, "proxy": {},
}

const maxChannelNamePrefixLength = 40

var (
	reSlugNonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)
	reSlugLeadingTrailing = regexp.MustCompile(`^-+|-+$`)
	reSlugRepeatedDash    = regexp.MustCompile(`-{2,}`)
)

// DeriveChannelNameFromBaseURL 根据首个 baseURL 派生渠道名称。
// 规则：hostname 去 www/通用前缀、剥离公共后缀、IPv4 点转横线、IPv6 加前缀，附加端口；
// 解析失败或无有效主机时回退 "channel"。与前端 extractChannelNamePrefix 保持一致。
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

	suffixCount := channelNamePublicSuffixLabelCount(labels)
	stemEnd := len(labels) - suffixCount
	if stemEnd < 1 {
		stemEnd = 1
	}
	meaningful := fitChannelNamePrefix(dropGenericLeadingChannelNameLabels(labels[:stemEnd]))
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

func channelNamePublicSuffixLabelCount(labels []string) int {
	maxCount := len(labels) - 1
	if maxCount > 3 {
		maxCount = 3
	}
	for count := maxCount; count >= 2; count-- {
		suffix := strings.Join(labels[len(labels)-count:], ".")
		if _, ok := multiPartPublicSuffixes[suffix]; ok {
			return count
		}
	}
	return 1
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
