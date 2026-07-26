package common

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func newAutopilotProfileTestContext(t *testing.T, path, body string, agentCtx *types.AgentContext) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", path, strings.NewReader(body))
	SetRequestLogContextWithAgent(c, "session-test", 1, agentCtx)
	return c
}

func TestAttachAutopilotRequestProfileExtractsProtocolFeatures(t *testing.T) {
	t.Run("messages tools and subagent", func(t *testing.T) {
		body := `{"model":"glm-5.2","messages":[{"role":"user","content":"hello"}],"tools":[{"name":"search"}]}`
		c := newAutopilotProfileTestContext(t, "/v1/messages", body, &types.AgentContext{
			AgentRole: "subagent",
			AgentType: "claude_code_subagent",
		})

		profile := AttachAutopilotRequestProfile(c, scheduler.ChannelKindMessages, "glm-5.2", "completion", "session-test", []byte(body), 0)
		if !profile.ToolUseNeed {
			t.Fatal("ToolUseNeed = false, want true")
		}
		if profile.AgentRole != "subagent" || profile.AgentType != "claude_code_subagent" {
			t.Fatalf("unexpected agent context: role=%q type=%q", profile.AgentRole, profile.AgentType)
		}
		if profile.EstTokens <= 0 || profile.ContextNeed != profile.EstTokens {
			t.Fatalf("unexpected token estimate: est=%d context=%d", profile.EstTokens, profile.ContextNeed)
		}
		if profile.TaskClass != autopilot.TaskClassWorker {
			t.Fatalf("TaskClass = %q, want worker", profile.TaskClass)
		}
		if profile.PromptHash == "" {
			t.Fatal("PromptHash is empty")
		}
	})

	t.Run("responses reasoning blocks lightweight whitelist", func(t *testing.T) {
		body := `{"model":"claude-sonnet-5","input":"summarize this","reasoning":{"effort":"high"}}`
		c := newAutopilotProfileTestContext(t, "/v1/responses", body, nil)

		profile := AttachAutopilotRequestProfile(c, scheduler.ChannelKindResponses, "claude-sonnet-5", "summarize", "session-test", []byte(body), 0)
		if !profile.ReasoningNeed {
			t.Fatal("ReasoningNeed = false, want true")
		}
		if profile.Operation != "summarize" {
			t.Fatalf("Operation = %q, want summarize", profile.Operation)
		}
		if profile.TaskClass == autopilot.TaskClassLightweight {
			t.Fatal("reasoning request must not be classified as lightweight")
		}
	})

	t.Run("messages MCP tool names require tool support", func(t *testing.T) {
		body := `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"inspect this repository"}],"tools":["Bash","mcp__serena__find_symbol"]}`
		c := newAutopilotProfileTestContext(t, "/v1/messages", body, nil)

		profile := AttachAutopilotRequestProfile(c, scheduler.ChannelKindMessages, "claude-opus-4-8", "completion", "session-test", []byte(body), 0)
		if !profile.ToolUseNeed {
			t.Fatal("MCP 工具列表必须设置 ToolUseNeed=true")
		}
	})

	t.Run("disabled thinking remains false", func(t *testing.T) {
		body := `{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hello"}],"thinking":{"type":"disabled"}}`
		c := newAutopilotProfileTestContext(t, "/v1/messages", body, nil)

		profile := AttachAutopilotRequestProfile(c, scheduler.ChannelKindMessages, "claude-sonnet-5", "completion", "session-test", []byte(body), 0)
		if profile.ReasoningNeed {
			t.Fatal("ReasoningNeed = true for disabled thinking")
		}
	})

	t.Run("zero Gemini thinking budget remains false", func(t *testing.T) {
		body := `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}`
		c := newAutopilotProfileTestContext(t, "/v1beta/models/gemini-3.5-flash:generateContent", body, nil)

		profile := AttachAutopilotRequestProfile(c, scheduler.ChannelKindGemini, "gemini-3.5-flash", "completion", "session-test", []byte(body), 0)
		if profile.ReasoningNeed {
			t.Fatal("ReasoningNeed = true for zero thinking budget")
		}
	})

	t.Run("vectors preserves dimensions", func(t *testing.T) {
		body := `{"model":"text-embedding-3-small","input":"hello","dimensions":1536}`
		c := newAutopilotProfileTestContext(t, "/v1/embeddings", body, nil)

		profile := AttachAutopilotRequestProfile(c, scheduler.ChannelKindVectors, "text-embedding-3-small", "embedding", "session-test", []byte(body), 1536)
		if profile.TaskClass != autopilot.TaskClassEmbedding {
			t.Fatalf("TaskClass = %q, want embedding", profile.TaskClass)
		}
		if profile.EmbeddingDimension != 1536 {
			t.Fatalf("EmbeddingDimension = %d, want 1536", profile.EmbeddingDimension)
		}
	})
}

func TestEnsureAutopilotRequestProfilePreservesAttachedProfile(t *testing.T) {
	body := `{"model":"claude-sonnet-5","input":"summary"}`
	c := newAutopilotProfileTestContext(t, "/v1/responses/compact", body, nil)
	attached := AttachAutopilotRequestProfile(c, scheduler.ChannelKindResponses, "claude-sonnet-5", "summarize", "session-test", []byte(body), 0)

	ensured := EnsureAutopilotRequestProfile(c, scheduler.ChannelKindResponses, "other-model", "completion", "other-session", []byte(body))
	if ensured.Model != attached.Model || ensured.Operation != attached.Operation || ensured.PromptHash != attached.PromptHash {
		t.Fatalf("EnsureAutopilotRequestProfile replaced attached profile: got %+v want %+v", ensured, attached)
	}
}

func TestAttachAutopilotRequestProfileExtractsClientEffort(t *testing.T) {
	tests := []struct {
		name         string
		kind         scheduler.ChannelKind
		path         string
		body         string
		wantExplicit bool
		wantEffort   autopilot.EffortLevel
	}{
		{
			name:         "claude thinking disabled is explicit none",
			kind:         scheduler.ChannelKindMessages,
			path:         "/v1/messages",
			body:         `{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"disabled"}}`,
			wantExplicit: true,
			wantEffort:   autopilot.EffortOff,
		},
		{
			name:         "claude thinking effort is explicit",
			kind:         scheduler.ChannelKindMessages,
			path:         "/v1/messages",
			body:         `{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","effort":"high"}}`,
			wantExplicit: true,
			wantEffort:   autopilot.EffortHigh,
		},
		{
			name:         "chat reasoning_effort is explicit",
			kind:         scheduler.ChannelKindChat,
			path:         "/v1/chat/completions",
			body:         `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"low"}`,
			wantExplicit: true,
			wantEffort:   autopilot.EffortLow,
		},
		{
			name:         "chat reasoning_effort none is explicit off",
			kind:         scheduler.ChannelKindChat,
			path:         "/v1/chat/completions",
			body:         `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"none"}`,
			wantExplicit: true,
			wantEffort:   autopilot.EffortOff,
		},
		{
			name:         "responses reasoning.effort is explicit",
			kind:         scheduler.ChannelKindResponses,
			path:         "/v1/responses",
			body:         `{"model":"claude-sonnet-5","input":"hi","reasoning":{"effort":"medium"}}`,
			wantExplicit: true,
			wantEffort:   autopilot.EffortMedium,
		},
		{
			name:         "gemini thinkingLevel is explicit",
			kind:         scheduler.ChannelKindGemini,
			path:         "/v1beta/models/gemini-3.5-flash:generateContent",
			body:         `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`,
			wantExplicit: true,
			wantEffort:   autopilot.EffortHigh,
		},
		{
			name:         "no effort field is not declared",
			kind:         scheduler.ChannelKindMessages,
			path:         "/v1/messages",
			body:         `{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hi"}]}`,
			wantExplicit: false,
			wantEffort:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newAutopilotProfileTestContext(t, tt.path, tt.body, nil)
			profile := AttachAutopilotRequestProfile(c, tt.kind, "model", "completion", "session-test", []byte(tt.body), 0)
			if profile.ClientEffortExplicit != tt.wantExplicit {
				t.Fatalf("ClientEffortExplicit = %v, want %v", profile.ClientEffortExplicit, tt.wantExplicit)
			}
			if profile.ClientEffort != tt.wantEffort {
				t.Fatalf("ClientEffort = %q, want %q", profile.ClientEffort, tt.wantEffort)
			}
		})
	}
}

func TestAnalyzeAutopilotPromptExtractsEphemeralRoutingSignals(t *testing.T) {
	req := decodeAutopilotRequest([]byte(`{
		"system":"You are a coding assistant",
		"messages":[
			{"role":"user","content":"先检查 scheduler.go"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"定位分布式路由的根因并完成架构重构\ndiff --git a/a.go b/a.go"}
		],
		"tools":[{"name":"Read"},{"function":{"name":"apply_patch"}}]
	}`))

	analysis := analyzeAutopilotPrompt(req, "")
	if analysis.Complexity != autopilot.TaskComplexityComplex {
		t.Fatalf("Complexity = %q, want complex", analysis.Complexity)
	}
	if analysis.DomainHints.SystemPrompt == "" || !analysis.DomainHints.HasDiffContext {
		t.Fatalf("unexpected domain hints: %+v", analysis.DomainHints)
	}
	if len(analysis.DomainHints.ToolNames) != 2 {
		t.Fatalf("ToolNames = %v, want 2 entries", analysis.DomainHints.ToolNames)
	}
	if len(analysis.DomainHints.FileExtensions) == 0 {
		t.Fatalf("FileExtensions = %v, want .go", analysis.DomainHints.FileExtensions)
	}
}

func TestAttachAutopilotRequestProfileIgnoresHarnessOverheadForTaskDifficulty(t *testing.T) {
	tools := make([]map[string]interface{}, 0, 12)
	for i := 0; i < 12; i++ {
		tools = append(tools, map[string]interface{}{
			"name":        "tool_" + strconv.Itoa(i),
			"description": strings.Repeat("tool documentation ", 40),
		})
	}
	bodyValue := map[string]interface{}{
		"model": "claude-sonnet-5",
		"messages": []interface{}{map[string]interface{}{
			"role": "user",
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": "<system-reminder>" + strings.Repeat("generic harness context ", 5_000) + "</system-reminder>",
				},
				map[string]interface{}{"type": "text", "text": "请帮我写一段简短的个人介绍"},
			},
		}},
		"tools":    tools,
		"thinking": map[string]interface{}{"type": "adaptive"},
	}
	bodyBytes, err := json.Marshal(bodyValue)
	if err != nil {
		t.Fatal(err)
	}
	c := newAutopilotProfileTestContext(t, "/v1/messages", string(bodyBytes), &types.AgentContext{AgentRole: "main"})

	profile := AttachAutopilotRequestProfile(
		c, scheduler.ChannelKindMessages, "claude-sonnet-5", "completion", "session-test", bodyBytes, 0)

	if profile.EstTokens < 20_000 {
		t.Fatalf("测试请求未形成足够大的整包上下文: EstTokens=%d", profile.EstTokens)
	}
	if profile.Complexity != autopilot.TaskComplexityRoutine || profile.TaskClass != autopilot.TaskClassWorker {
		t.Fatalf("profile = complexity:%q class:%q, want routine/worker", profile.Complexity, profile.TaskClass)
	}
	if profile.QualityTarget != autopilot.QualityTierHigh {
		t.Fatalf("QualityTarget = %q, want high", profile.QualityTarget)
	}
	if !profile.ToolUseNeed || !profile.ReasoningNeed {
		t.Fatalf("能力下界丢失: tools=%v reasoning=%v", profile.ToolUseNeed, profile.ReasoningNeed)
	}
}

func TestAttachAutopilotRequestProfileRoutesByCurrentTaskDifficulty(t *testing.T) {
	tests := []struct {
		name           string
		prompt         string
		wantComplexity autopilot.TaskComplexity
		wantClass      autopilot.TaskClass
		wantTarget     autopilot.QualityTier
	}{
		{name: "trivial", prompt: "hello", wantComplexity: autopilot.TaskComplexityTrivial, wantClass: autopilot.TaskClassLightweight, wantTarget: autopilot.QualityTierLow},
		{name: "routine", prompt: "实现一个分页查询并补充单元测试", wantComplexity: autopilot.TaskComplexityRoutine, wantClass: autopilot.TaskClassWorker, wantTarget: autopilot.QualityTierNormal},
		{name: "complex", prompt: "定位分布式调度的根因并重构整体架构", wantComplexity: autopilot.TaskComplexityComplex, wantClass: autopilot.TaskClassSupervisor, wantTarget: autopilot.QualityTierPremium},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"model":"claude-opus-4-8","messages":[{"role":"user","content":` + strconv.Quote(tt.prompt) + `}]}`
			c := newAutopilotProfileTestContext(t, "/v1/messages", body, nil)
			profile := AttachAutopilotRequestProfile(c, scheduler.ChannelKindMessages, "claude-opus-4-8", "completion", "session-test", []byte(body), 0)

			if profile.Complexity != tt.wantComplexity || profile.TaskClass != tt.wantClass || profile.QualityTarget != tt.wantTarget {
				t.Fatalf("profile = complexity:%q class:%q target:%q", profile.Complexity, profile.TaskClass, profile.QualityTarget)
			}
		})
	}
}
