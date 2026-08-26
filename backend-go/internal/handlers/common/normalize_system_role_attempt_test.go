package common

import (
	"net/http"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestShouldNormalizeSystemRoleForAttempt(t *testing.T) {
	cache := config.NewConverterUpstreamCache()
	restore := SwapConverterUpstreamCacheForTest(cache)
	defer restore()

	tests := []struct {
		name         string
		upstream     *config.UpstreamConfig
		attemptModel string
		wantShould   bool
		wantReason   string
	}{
		{
			name:         "nil upstream",
			upstream:     nil,
			attemptModel: "deepseek-v4-flash",
			wantShould:   false,
		},
		{
			name:         "manual switch on",
			upstream:     &config.UpstreamConfig{NormalizeSystemRoleToTopLevel: true},
			attemptModel: "claude-opus-4-8",
			wantShould:   true,
			wantReason:   "manual",
		},
		{
			name:         "claude model without switch",
			upstream:     &config.UpstreamConfig{},
			attemptModel: "claude-opus-4-8",
			wantShould:   false,
		},
		{
			name:         "deepseek model triggers",
			upstream:     &config.UpstreamConfig{},
			attemptModel: "deepseek-v4-flash",
			wantShould:   true,
			wantReason:   "model_family:deepseek",
		},
		{
			name:         "glm model triggers",
			upstream:     &config.UpstreamConfig{},
			attemptModel: "glm-5.2",
			wantShould:   true,
			wantReason:   "model_family:glm",
		},
		{
			name:         "kimi model triggers",
			upstream:     &config.UpstreamConfig{},
			attemptModel: "kimi-k2.6",
			wantShould:   true,
			wantReason:   "model_family:kimi",
		},
		{
			name:         "unknown model stays off",
			upstream:     &config.UpstreamConfig{},
			attemptModel: "some-custom-model",
			wantShould:   false,
		},
		{
			name:         "empty model stays off",
			upstream:     &config.UpstreamConfig{},
			attemptModel: "",
			wantShould:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			should, reason := shouldNormalizeSystemRoleForAttempt(tt.upstream, tt.attemptModel)
			if should != tt.wantShould {
				t.Fatalf("should = %v, want %v (reason=%q)", should, tt.wantShould, reason)
			}
			if tt.wantReason != "" && reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}

	// 指纹学习记忆触发（渠道级，与模型无关）
	cache.Mark("ch-converter", "x-new-api-version")
	should, reason := shouldNormalizeSystemRoleForAttempt(
		&config.UpstreamConfig{ChannelUID: "ch-converter"}, "claude-opus-4-8")
	if !should || reason != "converter_fingerprint" {
		t.Fatalf("fingerprint learned: should=%v reason=%q, want true/converter_fingerprint", should, reason)
	}
	// 其他渠道不受记忆外溢影响
	should, _ = shouldNormalizeSystemRoleForAttempt(
		&config.UpstreamConfig{ChannelUID: "ch-other"}, "claude-opus-4-8")
	if should {
		t.Fatal("指纹记忆不应外溢到其他渠道")
	}
}

func TestDetectConverterFingerprint(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   string
	}{
		{"no fingerprint", http.Header{}, ""},
		{"new-api version", http.Header{"X-New-Api-Version": []string{"v1.0.0-rc.25"}}, "x-new-api-version"},
		{"oneapi request id", http.Header{"X-Oneapi-Request-Id": []string{"abc"}}, "x-oneapi-request-id"},
		{"unrelated headers", http.Header{"Cf-Ray": []string{"x"}, "Server": []string{"cloudflare"}}, ""},
	}
	// Header.Set 走规范化键，与真实响应解析路径一致（小写写入也能命中）
	viaSet := http.Header{}
	viaSet.Set("x-new-api-version", "v1")
	tests = append(tests, struct {
		name   string
		header http.Header
		want   string
	}{"set with lowercase key", viaSet, "x-new-api-version"})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectConverterFingerprint(tt.header); got != tt.want {
				t.Fatalf("detectConverterFingerprint() = %q, want %q", got, tt.want)
			}
		})
	}
}
