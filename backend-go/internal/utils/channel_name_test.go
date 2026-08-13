package utils

import "testing"

func TestDeriveChannelNameFromBaseURL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"openai 官方", "https://api.openai.com/v1", "api-openai"},
		{"去 www 前缀", "https://www.example.com/v1", "example"},
		{"去 www 保留主体", "https://www.example.com.cn/v1", "example"},
		{"多段公共后缀", "https://api.bigmodel.cn/v1", "api-bigmodel"},
		{"保留 api 前缀", "https://api.xiavier.com/v1", "api-xiavier"},
		{"保留 gateway 前缀", "https://gateway.api.deepseek.com/v1", "gateway-api-deepseek"},
		{"带端口", "https://api.example.com:8443/v1", "api-example-8443"},
		{"IPv4", "http://192.168.1.8:11434/v1", "192-168-1-8-11434"},
		{"IPv6", "http://[::1]:8080/v1", "ipv6-1-8080"},
		{"localhost", "http://localhost:3000/v1", "localhost-3000"},
		{"单标签主机", "https://openai/v1", "openai"},
		{"空串回退", "", "channel"},
		{"无主机回退", "not-a-url", "channel"},
		{"超长主机截断", "https://api.averyveryverylongsubdomainname.anotherlongsegment.com/v1", "anotherlongsegment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveChannelNameFromBaseURL(tc.input); got != tc.want {
				t.Errorf("DeriveChannelNameFromBaseURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
