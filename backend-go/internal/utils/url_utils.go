package utils

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var reURLPassword = regexp.MustCompile(`(://[^:@/]+:)[^@]+(@)`)
var reBaseURLVersionSuffix = regexp.MustCompile(`/v\d+[a-z]*$`)

func RedactURLCredentials(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return reURLPassword.ReplaceAllString(rawURL, "${1}***${2}")
	}

	if u.User != nil {
		username := u.User.Username()
		u.User = url.UserPassword(username, "***")
		return u.String()
	}

	return rawURL
}

func ValidateBaseURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("baseURL cannot be empty")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported protocol: %s (only http/https allowed)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL missing hostname")
	}

	if host == "169.254.169.254" {
		return fmt.Errorf("cloud metadata service access is forbidden")
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}

	for _, resolvedIP := range ips {
		if resolvedIP.String() == "169.254.169.254" {
			return fmt.Errorf("domain %s resolves to cloud metadata service", host)
		}
	}

	return nil
}

func DefaultVersionPrefixForService(serviceType string) string {
	if strings.EqualFold(serviceType, "gemini") {
		return "/v1beta"
	}
	return "/v1"
}

func normalizeBaseURL(rawURL string) (normalized string, hasHash bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", false
	}

	hasHash = strings.HasSuffix(trimmed, "#")
	withoutHash := strings.TrimSuffix(trimmed, "#")
	normalized = strings.TrimRight(withoutHash, "/")
	return normalized, hasHash
}

func CanonicalBaseURL(rawURL, serviceType string) string {
	normalized, hasHash := normalizeBaseURL(rawURL)
	if normalized == "" {
		return ""
	}
	if hasHash {
		return normalized + "#"
	}

	versionPrefix := DefaultVersionPrefixForService(serviceType)
	if strings.HasSuffix(normalized, versionPrefix) {
		return strings.TrimSuffix(normalized, versionPrefix)
	}
	return normalized
}

func MetricsIdentityBaseURL(rawURL, serviceType string) string {
	normalized, hasHash := normalizeBaseURL(rawURL)
	if normalized == "" {
		return ""
	}
	if hasHash {
		return normalized + "#"
	}
	if reBaseURLVersionSuffix.MatchString(normalized) {
		return normalized
	}
	return normalized + DefaultVersionPrefixForService(serviceType)
}

func EquivalentBaseURLVariants(rawURL, serviceType string) []string {
	normalized, hasHash := normalizeBaseURL(rawURL)
	if normalized == "" {
		return nil
	}

	variants := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(value string) {
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		variants = append(variants, value)
	}

	if hasHash {
		add(normalized + "#")
		add(normalized + "/#")
		return variants
	}

	versionPrefix := DefaultVersionPrefixForService(serviceType)
	if reBaseURLVersionSuffix.MatchString(normalized) && !strings.HasSuffix(normalized, versionPrefix) {
		add(normalized)
		add(normalized + "/")
		return variants
	}

	canonical := CanonicalBaseURL(rawURL, serviceType)
	identity := MetricsIdentityBaseURL(rawURL, serviceType)
	add(canonical)
	add(canonical + "/")
	add(identity)
	add(identity + "/")
	return variants
}

func isPrivateIP(ip net.IP) bool {
	privateIPv4Blocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"0.0.0.0/8",
		"224.0.0.0/4",
		"240.0.0.0/4",
	}

	privateIPv6Blocks := []string{
		"::1/128",
		"fc00::/7",
		"fe80::/10",
		"ff00::/8",
	}

	blocks := privateIPv4Blocks
	if ip.To4() == nil {
		blocks = append(blocks, privateIPv6Blocks...)
	}

	for _, block := range blocks {
		_, subnet, err := net.ParseCIDR(block)
		if err != nil {
			continue
		}
		if subnet.Contains(ip) {
			return true
		}
	}

	if strings.EqualFold(ip.String(), "localhost") {
		return true
	}

	return false
}
