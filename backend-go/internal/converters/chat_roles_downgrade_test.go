package converters

import "testing"

func TestDowngradeDeveloperRoleToSystem(t *testing.T) {
	reqMap := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "sys"},
			map[string]interface{}{"role": "developer", "content": "dev"},
			map[string]interface{}{"role": "user", "content": "hi"},
			map[string]interface{}{"role": "assistant", "content": "ok"},
			map[string]interface{}{"role": "tool", "tool_call_id": "call_1", "content": "{}"},
			// 其他非标准 role 必须保持原样：本函数只处理 developer
			map[string]interface{}{"role": "function", "content": "legacy"},
		},
	}

	if !DowngradeDeveloperRoleToSystem(reqMap) {
		t.Fatal("存在 developer role 时应返回已改写")
	}

	want := []string{"system", "system", "user", "assistant", "tool", "function"}
	messages := reqMap["messages"].([]interface{})
	for i, w := range want {
		got := messages[i].(map[string]interface{})["role"]
		if got != w {
			t.Errorf("messages[%d].role = %v, want %v", i, got, w)
		}
	}
	// tool_call_id 不应被破坏
	if messages[4].(map[string]interface{})["tool_call_id"] != "call_1" {
		t.Error("tool_call_id 不应被改动")
	}
}

func TestDowngradeDeveloperRoleToSystemNoop(t *testing.T) {
	reqMap := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	if DowngradeDeveloperRoleToSystem(reqMap) {
		t.Error("无 developer role 时不应报告改写")
	}
}

func TestRequestHasDeveloperRole(t *testing.T) {
	with := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "developer", "content": "dev"},
		},
	}
	if !RequestHasDeveloperRole(with) {
		t.Error("应检出 developer role")
	}

	without := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	if RequestHasDeveloperRole(without) {
		t.Error("不应误报 developer role")
	}

	// 缺失 messages 不应 panic
	if RequestHasDeveloperRole(map[string]interface{}{}) {
		t.Error("空请求不应检出")
	}
}
