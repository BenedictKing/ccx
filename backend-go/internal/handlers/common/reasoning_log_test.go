package common

import "testing"

func TestExtractReasoningEffortForLog(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "chat reasoning_effort",
			body: `{"model":"gpt-5","reasoning_effort":"high"}`,
			want: "high",
		},
		{
			name: "responses reasoning object",
			body: `{"model":"gpt-5","reasoning":{"effort":"medium"}}`,
			want: "medium",
		},
		{
			name: "claude thinking budget",
			body: `{"model":"claude","thinking":{"type":"enabled","budget_tokens":8192}}`,
			want: "budget=8192",
		},
		{
			name: "claude thinking effort",
			body: `{"model":"claude","thinking":{"type":"enabled","effort":"max"}}`,
			want: "max",
		},
		{
			name: "claude output config effort",
			body: `{"model":"glm-5.2","output_config":{"effort":"max"}}`,
			want: "max",
		},
		{
			name: "claude disabled thinking wins over stale effort",
			body: `{"model":"claude","thinking":{"type":"disabled","effort":"max"}}`,
			want: "none",
		},
		{
			name: "gemini thinking level",
			body: `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH"}}}`,
			want: "HIGH",
		},
		{
			name: "gemini include thoughts",
			body: `{"generationConfig":{"thinkingConfig":{"includeThoughts":true}}}`,
			want: "enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractReasoningEffortForLog([]byte(tt.body)); got != tt.want {
				t.Fatalf("extractReasoningEffortForLog() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractClientEffortExplicit_Gemini(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantRaw      string
		wantExplicit bool
	}{
		{
			name:         "generationConfig thinkingLevel present",
			body:         `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH"}}}`,
			wantRaw:      "HIGH",
			wantExplicit: true,
		},
		{
			name:         "bare thinkingConfig thinkingLevel fallback",
			body:         `{"thinkingConfig":{"thinkingLevel":"low"}}`,
			wantRaw:      "low",
			wantExplicit: true,
		},
		{
			name:         "thinkingBudget zero without thinkingLevel means disabled",
			body:         `{"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}`,
			wantRaw:      "none",
			wantExplicit: true,
		},
		{
			name:         "thinkingBudget nonzero without thinkingLevel is not pinned",
			body:         `{"generationConfig":{"thinkingConfig":{"thinkingBudget":1024}}}`,
			wantRaw:      "",
			wantExplicit: false,
		},
		{
			name:         "thinkingConfig present but empty is not pinned",
			body:         `{"generationConfig":{"thinkingConfig":{}}}`,
			wantRaw:      "",
			wantExplicit: false,
		},
		{
			name:         "thinkingConfig absent",
			body:         `{"generationConfig":{}}`,
			wantRaw:      "",
			wantExplicit: false,
		},
		{
			name:         "includeThoughts alone does not pin a level",
			body:         `{"generationConfig":{"thinkingConfig":{"includeThoughts":true}}}`,
			wantRaw:      "",
			wantExplicit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRaw, gotExplicit := ExtractClientEffortExplicit([]byte(tt.body), "gemini")
			if gotRaw != tt.wantRaw || gotExplicit != tt.wantExplicit {
				t.Fatalf("ExtractClientEffortExplicit() = (%q, %v), want (%q, %v)",
					gotRaw, gotExplicit, tt.wantRaw, tt.wantExplicit)
			}
		})
	}
}
