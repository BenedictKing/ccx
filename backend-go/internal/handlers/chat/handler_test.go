package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestBuildProviderRequest_KeepsRequestModelPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

	bodyBytes := []byte(`{"model":"gpt-5.1-codex","messages":[{"role":"user","content":"hi"}]}`)
	upstream := &config.UpstreamConfig{
		ServiceType:   "openai",
		TextVerbosity: "low",
		FastMode:      true,
	}

	req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-test", bodyBytes, "gpt-5.1-codex", false)
	if err != nil {
		t.Fatalf("buildProviderRequest() err = %v", err)
	}

	var got map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}

	// 显式 ModelMapping 已退役：请求模型透传，不再改写为映射目标。
	if got["model"] != "gpt-5.1-codex" {
		t.Fatalf("model = %v, want gpt-5.1-codex (passthrough)", got["model"])
	}

	text, ok := got["text"].(map[string]interface{})
	if !ok || text["verbosity"] != "low" {
		t.Fatalf("text = %#v, want verbosity=low", got["text"])
	}

	if got["service_tier"] != "priority" {
		t.Fatalf("service_tier = %v, want priority", got["service_tier"])
	}
}

func TestStripImageGenerationFromChatTools(t *testing.T) {
	t.Run("剥离 image_generation 保留其他工具", func(t *testing.T) {
		reqMap := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "image_generation"},
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
			},
			"tool_choice": "auto",
		}
		stripImageGenerationFromChatTools(reqMap)

		tools, ok := reqMap["tools"].([]interface{})
		if !ok || len(tools) != 1 {
			t.Fatalf("tools = %#v, want 1 entry", reqMap["tools"])
		}
		if reqMap["tool_choice"] != "auto" {
			t.Fatalf("tool_choice 不应被删除")
		}
	})

	t.Run("全部剥离后清理 tools/tool_choice", func(t *testing.T) {
		reqMap := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "image_generation"},
			},
			"tool_choice":         "auto",
			"parallel_tool_calls": true,
		}
		stripImageGenerationFromChatTools(reqMap)

		if _, ok := reqMap["tools"]; ok {
			t.Fatal("tools 应被删除")
		}
		if _, ok := reqMap["tool_choice"]; ok {
			t.Fatal("tool_choice 应被删除")
		}
		if _, ok := reqMap["parallel_tool_calls"]; ok {
			t.Fatal("parallel_tool_calls 应被删除")
		}
	})

	t.Run("剥离 Codex image_gen namespace 与扁平 function", func(t *testing.T) {
		reqMap := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "namespace", "name": "image_gen"},
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "image_gen__imagegen"}},
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
			},
		}
		stripImageGenerationFromChatTools(reqMap)

		tools, ok := reqMap["tools"].([]interface{})
		if !ok || len(tools) != 1 {
			t.Fatalf("tools = %#v, want 1 entry", reqMap["tools"])
		}
	})

	t.Run("无 image_generation 不修改", func(t *testing.T) {
		reqMap := map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": "lookup_user"}},
			},
		}
		stripImageGenerationFromChatTools(reqMap)
		tools, ok := reqMap["tools"].([]interface{})
		if !ok || len(tools) != 1 {
			t.Fatalf("tools 不应被修改")
		}
	})
}

func TestBuildProviderRequest_StripsImageGenerationTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

	bodyBytes := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"image_generation","output_format":"png"},{"type":"function","function":{"name":"lookup_user"}}]}`)
	upstream := &config.UpstreamConfig{
		ServiceType: "openai",
	}
	upstream.SetLearnedCompatTrait(config.TraitStripImageGenTool, true)

	req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-test", bodyBytes, "gpt-5.5", false)
	if err != nil {
		t.Fatalf("buildProviderRequest() err = %v", err)
	}

	var got map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}

	tools, ok := got["tools"].([]interface{})
	if !ok {
		t.Fatalf("tools 缺失或类型错误: %#v", got["tools"])
	}
	if len(tools) != 1 {
		t.Fatalf("tools 长度=%d，期望 1（image_generation 应被剥离）", len(tools))
	}
	tool := tools[0].(map[string]interface{})
	if tool["type"] != "function" {
		t.Fatalf("剩余工具应为 function，实际 %v", tool["type"])
	}
}

func TestConvertChatToClaudeRequest_MapsUserIDToMetadata(t *testing.T) {
	bodyBytes := []byte(`{"model":"deepseek-v4-pro","user_id":"deepseek_user_123","messages":[{"role":"user","content":"hi"}]}`)

	got, err := convertChatToClaudeRequest(bodyBytes, "claude-3-5-sonnet", false)
	if err != nil {
		t.Fatalf("convertChatToClaudeRequest() err = %v", err)
	}

	metadata, ok := got["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata missing or invalid: %#v", got["metadata"])
	}
	if metadata["user_id"] != "deepseek_user_123" {
		t.Fatalf("metadata.user_id = %v, want deepseek_user_123", metadata["user_id"])
	}
}

func TestBuildProviderRequest_KeepsRequestReasoningParamsPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

	// 显式 ReasoningMapping 已退役：请求路径不再注入/改写思考参数，
	// 客户端自带的 reasoning 形态按原样透传（风格改写仅存在于 autopilot 决档注入路径）。
	bodyBytes := []byte(`{"model":"gpt-5.1-codex","messages":[{"role":"user","content":"hi"}],"reasoning":{"effort":"high"},"reasoning_effort":"high"}`)
	upstream := &config.UpstreamConfig{
		ServiceType:         "openai",
		ReasoningParamStyle: "thinking",
	}

	req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-test", bodyBytes, "gpt-5.1-codex", false)
	if err != nil {
		t.Fatalf("buildProviderRequest() err = %v", err)
	}

	var got map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}

	if _, ok := got["thinking"]; ok {
		t.Fatalf("thinking 不应被注入: %#v", got)
	}
	reasoning, ok := got["reasoning"].(map[string]interface{})
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning 应原样透传: %#v", got["reasoning"])
	}
	if gotEffort, _ := got["reasoning_effort"].(string); gotEffort != "high" {
		t.Fatalf("reasoning_effort 应原样透传: %#v", got["reasoning_effort"])
	}
}

func TestBuildProviderRequest_NormalizeNonstandardChatRolesDefaultOff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

	bodyBytes := []byte(`{"model":"gpt-5","messages":[{"role":"developer","content":"dev"},{"role":"user","content":"hi"}]}`)
	upstream := &config.UpstreamConfig{ServiceType: "openai"}

	req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-test", bodyBytes, "gpt-5", false)
	if err != nil {
		t.Fatalf("buildProviderRequest() err = %v", err)
	}

	var got map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}

	messages := got["messages"].([]interface{})
	first := messages[0].(map[string]interface{})
	if first["role"] != "developer" {
		t.Fatalf("role = %v, want developer when switch is off", first["role"])
	}
}

func TestBuildProviderRequest_NormalizeNonstandardChatRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		serviceType string
	}{
		{name: "openai", serviceType: "openai"},
		{name: "gemini_without_model_redirect", serviceType: "gemini"},
	}

	bodyBytes := []byte(`{"model":"gpt-5","messages":[{"role":"system","content":"sys"},{"role":"developer","content":"dev"},{"role":"user","content":"hi"},{"role":"assistant","content":"ok"},{"role":"tool","tool_call_id":"call_1","content":"{}"},{"role":"function","content":"legacy"},{"content":"missing"},{"role":123,"content":"number"}]}`)
	wantRoles := []string{"system", "user", "user", "assistant", "tool", "user", "user", "user"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

			upstream := &config.UpstreamConfig{
				ServiceType: tt.serviceType,
			}
			upstream.SetLearnedCompatTrait(config.TraitNormalizeNonstandardChatRoles, true)
			req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-test", bodyBytes, "gpt-5", false)
			if err != nil {
				t.Fatalf("buildProviderRequest() err = %v", err)
			}

			var got map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
				t.Fatalf("decode request body: %v", err)
			}

			messages, ok := got["messages"].([]interface{})
			if !ok || len(messages) != len(wantRoles) {
				t.Fatalf("messages = %#v, want %d items", got["messages"], len(wantRoles))
			}

			for i, want := range wantRoles {
				msg, ok := messages[i].(map[string]interface{})
				if !ok {
					t.Fatalf("message[%d] = %#v, want object", i, messages[i])
				}
				if gotRole := msg["role"]; gotRole != want {
					t.Fatalf("message[%d].role = %v, want %s", i, gotRole, want)
				}
			}
		})
	}
}

func TestBuildProviderRequest_FunctionWithToolCallIDMapsToTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

	bodyBytes := []byte(`{"model":"gpt-5","messages":[{"role":"assistant","content":"ok","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"function","name":"f","content":"result","tool_call_id":"call_1"}]}`)
	upstream := &config.UpstreamConfig{
		ServiceType: "openai",
	}
	upstream.SetLearnedCompatTrait(config.TraitNormalizeNonstandardChatRoles, true)
	req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-test", bodyBytes, "gpt-5", false)
	if err != nil {
		t.Fatalf("buildProviderRequest() err = %v", err)
	}

	var got map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}

	messages := got["messages"].([]interface{})
	if len(messages) != 2 {
		t.Fatalf("messages length = %d, want 2", len(messages))
	}
	second := messages[1].(map[string]interface{})
	if second["role"] != "tool" {
		t.Fatalf("function message with tool_call_id role = %v, want tool", second["role"])
	}
}

func TestBuildProviderRequest_PreservesMultimodalContentArray(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		serviceType string
		upstream    *config.UpstreamConfig
		model       string
		wantModel   string
	}{
		{
			name:        "openai_passthrough_keeps_image_url",
			serviceType: "openai",
			upstream: &config.UpstreamConfig{
				ServiceType: "openai",
			},
			model:     "gpt-4o-image",
			wantModel: "gpt-4o-image",
		},
		{
			name:        "responses_converts_image_to_input_image",
			serviceType: "responses",
			upstream: &config.UpstreamConfig{
				ServiceType: "responses",
			},
			model:     "gpt-4o-image",
			wantModel: "gpt-4o-image",
		},
		{
			name:        "gemini_passthrough_keeps_image_url_without_remarshal",
			serviceType: "gemini",
			upstream: &config.UpstreamConfig{
				ServiceType: "gemini",
			},
			model:     "gpt-4o-image",
			wantModel: "gpt-4o-image",
		},
		{
			name:        "gemini_passthrough_keeps_image_url_with_remarshal",
			serviceType: "gemini",
			upstream: &config.UpstreamConfig{
				ServiceType: "gemini",
			},
			model:     "gemini-2.5-flash-image-preview",
			wantModel: "gemini-2.5-flash-image-preview",
		},
	}

	for _, tt := range tests {
		// 显式 ModelMapping 已退役：请求体 model 直接使用各 case 的请求模型。
		bodyBytes := []byte(`{"model":"` + tt.model + `","messages":[{"role":"user","content":[{"type":"text","text":"修改这个图片"},{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}]}`)
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

			req, err := buildProviderRequest(c, tt.upstream, "https://api.example.com", "sk-test", bodyBytes, tt.model, false)
			if err != nil {
				t.Fatalf("buildProviderRequest() err = %v", err)
			}

			var got map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
				t.Fatalf("decode request body: %v", err)
			}

			if got["model"] != tt.wantModel {
				t.Fatalf("model = %v, want %v", got["model"], tt.wantModel)
			}

			if tt.serviceType == "responses" {
				// responses 上游：请求体已转换为 Responses 格式，使用 input 而非 messages
				input, ok := got["input"].([]interface{})
				if !ok || len(input) == 0 {
					t.Fatalf("input = %#v, want non-empty array", got["input"])
				}
				firstItem, ok := input[0].(map[string]interface{})
				if !ok {
					t.Fatalf("input[0] = %#v, want object", input[0])
				}
				itemContent, ok := firstItem["content"].([]interface{})
				if !ok || len(itemContent) < 2 {
					t.Fatalf("input[0].content = %#v, want >= 2 items", firstItem["content"])
				}
				foundImage := false
				for _, c := range itemContent {
					block, ok := c.(map[string]interface{})
					if ok && block["type"] == "input_image" {
						foundImage = true
						break
					}
				}
				if !foundImage {
					t.Fatalf("input[0].content does not contain input_image block")
				}
				return
			}

			messages, ok := got["messages"].([]interface{})
			if !ok || len(messages) != 1 {
				t.Fatalf("messages = %#v, want single message", got["messages"])
			}

			msg, ok := messages[0].(map[string]interface{})
			if !ok {
				t.Fatalf("message[0] = %#v, want object", messages[0])
			}

			content, ok := msg["content"].([]interface{})
			if !ok || len(content) != 2 {
				t.Fatalf("content = %#v, want 2-part array", msg["content"])
			}

			textPart, ok := content[0].(map[string]interface{})
			if !ok || textPart["type"] != "text" || textPart["text"] != "修改这个图片" {
				t.Fatalf("text part = %#v, want text block", content[0])
			}

			imagePart, ok := content[1].(map[string]interface{})
			if !ok || imagePart["type"] != "image_url" {
				t.Fatalf("image part = %#v, want image_url block", content[1])
			}

			imageURL, ok := imagePart["image_url"].(map[string]interface{})
			if !ok || imageURL["url"] != "https://example.com/image.png" {
				t.Fatalf("image_url = %#v, want original url", imagePart["image_url"])
			}
		})
	}
}

func TestBuildProviderRequest_ClaudePassbackThinkingBlocksKeepsThinking(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

	bodyBytes := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","reasoning_content":"previous reasoning","content":"previous text"}
		]
	}`)

	upstream := &config.UpstreamConfig{
		ServiceType: "claude",
	}
	upstream.SetLearnedCompatTrait(config.TraitPassbackThinkingBlocks, true)

	req, err := buildProviderRequest(c, upstream, "https://api.example.com/anthropic", "sk-test", bodyBytes, "deepseek-v4-pro", false)
	if err != nil {
		t.Fatalf("buildProviderRequest() err = %v", err)
	}

	var got map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}

	messages, ok := got["messages"].([]interface{})
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v, want 2", got["messages"])
	}

	assistant, ok := messages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("assistant type = %T, want map[string]interface{}", messages[1])
	}
	content, ok := assistant["content"].([]interface{})
	if !ok || len(content) < 1 {
		t.Fatalf("assistant.content = %#v, want non-empty array", assistant["content"])
	}
	first, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatalf("assistant.content[0] type = %T, want map[string]interface{}", content[0])
	}
	if first["type"] != "thinking" {
		t.Fatalf("assistant.content[0].type = %v, want thinking", first["type"])
	}
	if first["thinking"] != "previous reasoning" {
		t.Fatalf("assistant.content[0].thinking = %v, want previous reasoning", first["thinking"])
	}
}

func TestInjectGeminiThoughtSignatures(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantSig  bool
		wantKeep string // 如果非空，期望保留原始 signature
	}{
		{
			name: "注入 dummy signature 到缺失的 tool_calls",
			input: `{"model":"gemini-3-pro","messages":[
				{"role":"user","content":"hi"},
				{"role":"assistant","tool_calls":[
					{"id":"call_1","type":"function","function":{"name":"test","arguments":"{}"}}
				]},
				{"role":"tool","tool_call_id":"call_1","content":"ok"}
			]}`,
			wantSig: true,
		},
		{
			name: "保留已有的 thought_signature",
			input: `{"model":"gemini-3-pro","messages":[
				{"role":"user","content":"hi"},
				{"role":"assistant","tool_calls":[
					{"id":"call_1","type":"function","function":{"name":"test","arguments":"{}"},
					 "extra_content":{"google":{"thought_signature":"real_sig_abc"}}}
				]},
				{"role":"tool","tool_call_id":"call_1","content":"ok"}
			]}`,
			wantSig:  true,
			wantKeep: "real_sig_abc",
		},
		{
			name:    "无 tool_calls 不修改",
			input:   `{"model":"gemini-3-pro","messages":[{"role":"user","content":"hi"}]}`,
			wantSig: false,
		},
		{
			name: "保留已有 extra_content 中的其他字段",
			input: `{"model":"gemini-3-pro","messages":[
				{"role":"user","content":"hi"},
				{"role":"assistant","tool_calls":[
					{"id":"call_1","type":"function","function":{"name":"test","arguments":"{}"},
					 "extra_content":{"custom_key":"custom_value","google":{"other_field":"keep_me"}}}
				]},
				{"role":"tool","tool_call_id":"call_1","content":"ok"}
			]}`,
			wantSig: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := injectGeminiThoughtSignatures([]byte(tt.input))

			var reqMap map[string]interface{}
			if err := json.Unmarshal(result, &reqMap); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}

			messages := reqMap["messages"].([]interface{})
			for _, msg := range messages {
				msgMap := msg.(map[string]interface{})
				if msgMap["role"] != "assistant" {
					continue
				}
				toolCalls, ok := msgMap["tool_calls"].([]interface{})
				if !ok || len(toolCalls) == 0 {
					continue
				}

				firstTC := toolCalls[0].(map[string]interface{})
				extraContent, hasEC := firstTC["extra_content"].(map[string]interface{})

				if tt.wantSig && !hasEC {
					t.Fatal("expected extra_content but not found")
				}
				if !tt.wantSig {
					return
				}

				google := extraContent["google"].(map[string]interface{})
				sig := google["thought_signature"].(string)

				if tt.wantKeep != "" {
					if sig != tt.wantKeep {
						t.Fatalf("expected signature %q, got %q", tt.wantKeep, sig)
					}
				} else {
					if sig == "" {
						t.Fatal("expected non-empty signature")
					}
				}

				// 验证 merge 行为：已有的 extra_content 字段应被保留
				if tt.name == "保留已有 extra_content 中的其他字段" {
					if _, ok := extraContent["custom_key"]; !ok {
						t.Fatal("extra_content.custom_key was lost during merge")
					}
					if otherField, ok := google["other_field"].(string); !ok || otherField != "keep_me" {
						t.Fatal("extra_content.google.other_field was lost during merge")
					}
				}
			}
		})
	}
}
