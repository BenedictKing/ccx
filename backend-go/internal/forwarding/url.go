package forwarding

import (
	"fmt"
	"regexp"
	"strings"
)

var versionSuffixPattern = regexp.MustCompile(`/v\d+[a-z]*$`)

func BuildEndpointURL(baseURL, versionPrefix, endpoint string) string {
	base := strings.TrimSpace(baseURL)
	skipVersionPrefix := strings.HasSuffix(base, "#")
	if skipVersionPrefix {
		base = strings.TrimSuffix(base, "#")
	}
	base = strings.TrimRight(base, "/")

	endpoint = "/" + strings.TrimLeft(endpoint, "/")
	versionPrefix = strings.Trim(versionPrefix, "/")
	if versionPrefix != "" {
		versionPrefix = "/" + versionPrefix
	}

	if skipVersionPrefix || versionPrefix == "" || versionSuffixPattern.MatchString(base) {
		return base + endpoint
	}
	return base + versionPrefix + endpoint
}

func BuildGeminiNativeURL(baseURL, model string, isStream bool) string {
	action := "generateContent"
	if isStream {
		action = "streamGenerateContent"
	}

	url := BuildEndpointURL(baseURL, "/v1beta", fmt.Sprintf("/models/%s:%s", model, action))
	if isStream {
		url += "?alt=sse"
	}
	return url
}
