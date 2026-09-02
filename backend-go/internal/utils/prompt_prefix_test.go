package utils

import (
	"strings"
	"testing"
)

// restorePromptAffinityFallback 保证用例间的开关状态互不影响。
func restorePromptAffinityFallback(t *testing.T, enabled bool) {
	t.Helper()
	SetPromptAffinityFallback(enabled)
}

func TestDerivePromptPrefixID_StableAcrossTurns(t *testing.T) {
	restorePromptAffinityFallback(t, true)

	// 同一会话：system 与首条 user 不变，后续轮次（追加重述历史）指纹必须一致
	turn1 := map[string]interface{}{
		"model": "gpt-5",
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "You are helpful."},
			map[string]interface{}{"role": "user", "content": "帮我审阅这段代码"},
		},
	}
	turn2 := map[string]interface{}{
		"model": "gpt-5",
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "You are helpful."},
			map[string]interface{}{"role": "user", "content": "帮我审阅这段代码"},
			map[string]interface{}{"role": "assistant", "content": "好的，请贴出代码"},
			map[string]interface{}{"role": "user", "content": "func main() {}"},
		},
	}

	id1 := DerivePromptPrefixID(turn1)
	id2 := DerivePromptPrefixID(turn2)
	if id1 == "" {
		t.Fatal("DerivePromptPrefixID(turn1) 为空")
	}
	if !strings.HasPrefix(id1, PromptPrefixIDPrefix) {
		t.Fatalf("指纹缺少 %s 前缀: %q", PromptPrefixIDPrefix, id1)
	}
	if id1 != id2 {
		t.Fatalf("同会话两轮指纹不一致: %q vs %q", id1, id2)
	}
}

func TestDerivePromptPrefixID_DifferentConversationsDiffer(t *testing.T) {
	restorePromptAffinityFallback(t, true)

	a := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "第一条会话的开场"},
		},
	}
	b := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "另一条会话的开场"},
		},
	}
	if DerivePromptPrefixID(a) == DerivePromptPrefixID(b) {
		t.Fatal("不同会话指纹不应相同")
	}
}

func TestDerivePromptPrefixID_ProtocolShapes(t *testing.T) {
	restorePromptAffinityFallback(t, true)

	tests := []struct {
		name string
		body map[string]interface{}
		want string // "" 表示不应产生指纹
	}{
		{
			name: "chat content blocks",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": []interface{}{
						map[string]interface{}{"type": "text", "text": "块状内容"},
					}},
				},
			},
			want: PromptPrefixIDPrefix,
		},
		{
			name: "anthropic top-level system",
			body: map[string]interface{}{
				"system":   "全局系统提示",
				"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
			},
			want: PromptPrefixIDPrefix,
		},
		{
			name: "responses instructions plus input string",
			body: map[string]interface{}{
				"instructions": "be brief",
				"input":        "直接字符串输入",
			},
			want: PromptPrefixIDPrefix,
		},
		{
			name: "responses input item array",
			body: map[string]interface{}{
				"input": []interface{}{
					map[string]interface{}{"type": "message", "role": "user", "content": "数组输入"},
				},
			},
			want: PromptPrefixIDPrefix,
		},
		{
			name: "gemini systemInstruction and contents",
			body: map[string]interface{}{
				"systemInstruction": map[string]interface{}{"parts": []interface{}{map[string]interface{}{"text": "sys"}}},
				"contents":          []interface{}{map[string]interface{}{"role": "user", "parts": []interface{}{map[string]interface{}{"text": "你好"}}}},
			},
			want: PromptPrefixIDPrefix,
		},
		{
			name: "gemini snake system_instruction",
			body: map[string]interface{}{
				"system_instruction": map[string]interface{}{"parts": []interface{}{map[string]interface{}{"text": "sys"}}},
				"contents":           []interface{}{map[string]interface{}{"parts": []interface{}{map[string]interface{}{"text": "snake 形状"}}}},
			},
			want: PromptPrefixIDPrefix,
		},
		{
			name: "images prompt",
			body: map[string]interface{}{"prompt": "a cat"},
			want: PromptPrefixIDPrefix,
		},
		{
			name: "vectors input array",
			body: map[string]interface{}{"input": []interface{}{"第一段文本", "第二段文本"}},
			want: PromptPrefixIDPrefix,
		},
		{
			name: "tool_result-only user turns skipped until real text",
			body: map[string]interface{}{
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": []interface{}{
						map[string]interface{}{"type": "tool_result", "tool_use_id": "t1"},
					}},
					map[string]interface{}{"role": "assistant", "content": "done"},
					map[string]interface{}{"role": "user", "content": "真正的首条文本"},
				},
			},
			want: PromptPrefixIDPrefix,
		},
		{
			name: "no conversation shape",
			body: map[string]interface{}{"model": "gpt-5"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DerivePromptPrefixID(tt.body)
			switch tt.want {
			case "":
				if got != "" {
					t.Fatalf("不应产生指纹，得到 %q", got)
				}
			case PromptPrefixIDPrefix:
				if !strings.HasPrefix(got, PromptPrefixIDPrefix) || len(got) != len(PromptPrefixIDPrefix)+16 {
					t.Fatalf("指纹格式异常: %q", got)
				}
			}
		})
	}
}

func TestDerivePromptPrefixID_SkipsToolResultOnlyTurn(t *testing.T) {
	restorePromptAffinityFallback(t, true)

	// tool_result-only 的 user 轮不定义指纹；首个含文本 user 轮才是会话锚点
	toolOnly := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": []interface{}{
				map[string]interface{}{"type": "tool_result", "tool_use_id": "t1"},
			}},
			map[string]interface{}{"role": "user", "content": "锚点消息"},
		},
	}
	anchorOnly := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "锚点消息"},
		},
	}
	if DerivePromptPrefixID(toolOnly) != DerivePromptPrefixID(anchorOnly) {
		t.Fatal("tool_result-only 轮被跳过后，两请求指纹应一致")
	}
}

func TestDerivePromptPrefixID_BudgetBound(t *testing.T) {
	restorePromptAffinityFallback(t, true)

	huge := strings.Repeat("长", promptPrefixRoleBytes/3+100) // 超出单角色预算
	a := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": huge},
		},
	}
	b := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": huge + "预算外的尾部差异"},
		},
	}
	if DerivePromptPrefixID(a) != DerivePromptPrefixID(b) {
		t.Fatal("超出预算的尾部差异不应影响指纹（4KB 截断生效）")
	}
}

func TestDerivePromptPrefixID_DisabledSwitch(t *testing.T) {
	defer restorePromptAffinityFallback(t, true)
	SetPromptAffinityFallback(false)

	body := map[string]interface{}{
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hello"}},
	}
	if got := DerivePromptPrefixID(body); got != "" {
		t.Fatalf("关闭开关后不应产生指纹，得到 %q", got)
	}
}

func TestExtractUnifiedSessionID_PromptPrefixFallback(t *testing.T) {
	restorePromptAffinityFallback(t, true)

	// 完全匿名的 chat 请求 → 内容指纹兜底；显式标识优先级不变
	anonBody := []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"anonymous hello"}]}`)
	got := ExtractUnifiedSessionID(nil, anonBody)
	if !strings.HasPrefix(got, PromptPrefixIDPrefix) {
		t.Fatalf("匿名请求应回退到 pp: 指纹，得到 %q", got)
	}

	// 同会话第二轮（历史重述）得到相同指纹
	anonTurn2 := []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"anonymous hello"},{"role":"assistant","content":"hi"}]}`)
	if got2 := ExtractUnifiedSessionID(nil, anonTurn2); got2 != got {
		t.Fatalf("同会话指纹应一致: %q vs %q", got, got2)
	}

	// 关闭开关后回到旧行为（空串）
	SetPromptAffinityFallback(false)
	defer SetPromptAffinityFallback(true)
	if got3 := ExtractUnifiedSessionID(nil, anonBody); got3 != "" {
		t.Fatalf("关闭开关后应返回空串，得到 %q", got3)
	}
}
