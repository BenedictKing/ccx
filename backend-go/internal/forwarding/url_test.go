package forwarding

import "testing"

func TestBuildEndpointURLVersionRules(t *testing.T) {
	tests := []struct {
		name          string
		baseURL       string
		versionPrefix string
		endpoint      string
		want          string
	}{
		{
			name:          "adds default version",
			baseURL:       "https://api.example.com",
			versionPrefix: "/v1",
			endpoint:      "/chat/completions",
			want:          "https://api.example.com/v1/chat/completions",
		},
		{
			name:          "existing version is preserved",
			baseURL:       "https://api.example.com/v2",
			versionPrefix: "/v1",
			endpoint:      "/chat/completions",
			want:          "https://api.example.com/v2/chat/completions",
		},
		{
			name:          "hash skips version prefix",
			baseURL:       "https://core.blink.new/api/v1/ai#",
			versionPrefix: "/v1",
			endpoint:      "/chat/completions",
			want:          "https://core.blink.new/api/v1/ai/chat/completions",
		},
		{
			name:          "hash with trailing slash skips version prefix",
			baseURL:       "https://api.example.com/#",
			versionPrefix: "/v1",
			endpoint:      "/messages",
			want:          "https://api.example.com/messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildEndpointURL(tt.baseURL, tt.versionPrefix, tt.endpoint); got != tt.want {
				t.Fatalf("BuildEndpointURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildGeminiNativeURLVersionRules(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		isStream bool
		want     string
	}{
		{
			name:    "adds v1beta",
			baseURL: "https://api.example.com",
			want:    "https://api.example.com/v1beta/models/gemini-2.0-flash:generateContent",
		},
		{
			name:    "existing version is preserved",
			baseURL: "https://api.example.com/v1",
			want:    "https://api.example.com/v1/models/gemini-2.0-flash:generateContent",
		},
		{
			name:    "hash skips v1beta",
			baseURL: "https://api.example.com/gemini#",
			want:    "https://api.example.com/gemini/models/gemini-2.0-flash:generateContent",
		},
		{
			name:     "stream appends sse query",
			baseURL:  "https://api.example.com/v1beta",
			isStream: true,
			want:     "https://api.example.com/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildGeminiNativeURL(tt.baseURL, "gemini-2.0-flash", tt.isStream); got != tt.want {
				t.Fatalf("BuildGeminiNativeURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
