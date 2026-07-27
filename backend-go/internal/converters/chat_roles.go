package converters

// NormalizeNonstandardChatRolesInRequest 将 OpenAI Chat 请求中非标准 role 改写为 user。
// 标准 role 保持为兼容面最广的 system/user/assistant/tool。
func NormalizeNonstandardChatRolesInRequest(reqMap map[string]interface{}) {
	switch messages := reqMap["messages"].(type) {
	case []interface{}:
		for _, msg := range messages {
			m, ok := msg.(map[string]interface{})
			if !ok {
				continue
			}
			normalizeChatMessageRole(m)
		}
	case []map[string]interface{}:
		for _, msg := range messages {
			normalizeChatMessageRole(msg)
		}
	}
}

func normalizeChatMessageRole(msg map[string]interface{}) {
	role, ok := msg["role"].(string)
	if !ok {
		msg["role"] = "user"
		return
	}
	if isStandardChatRole(role) {
		return
	}
	// 非标准 role 若携带 tool_call_id，视为 tool 响应（保留 OpenAI tool_calls 响应链完整性）
	if _, hasID := msg["tool_call_id"]; hasID {
		msg["role"] = "tool"
		return
	}
	msg["role"] = "user"
}

func isStandardChatRole(role string) bool {
	switch role {
	case "system", "user", "assistant", "tool":
		return true
	default:
		return false
	}
}

// DowngradeDeveloperRoleToSystem 仅将 messages 中的 developer role 改写为 system，返回是否发生改写。
//
// 与 NormalizeNonstandardChatRolesInRequest 的区别：后者是用户手工开关的既有契约（把所有非标准
// role 一律降为 user）；本函数是自动学习路径专用的窄改写，只动 developer，其他 role 一概不碰。
//
// 降级目标选 system 而非 user：developer 在 OpenAI 语义中是 system 的后继者，承载的是开发者指令；
// 降为 user 会让这些指令被当作用户输入，语义损失更大。
func DowngradeDeveloperRoleToSystem(reqMap map[string]interface{}) bool {
	changed := false
	switch messages := reqMap["messages"].(type) {
	case []interface{}:
		for _, msg := range messages {
			m, ok := msg.(map[string]interface{})
			if !ok {
				continue
			}
			if downgradeDeveloperRole(m) {
				changed = true
			}
		}
	case []map[string]interface{}:
		for _, msg := range messages {
			if downgradeDeveloperRole(msg) {
				changed = true
			}
		}
	}
	return changed
}

func downgradeDeveloperRole(msg map[string]interface{}) bool {
	if role, ok := msg["role"].(string); ok && role == "developer" {
		msg["role"] = "system"
		return true
	}
	return false
}

// RequestHasDeveloperRole 判断 Chat 请求体的 messages 中是否存在 developer role。
// 供自动学习路径做"改写确实有效"的前置校验：请求里没有 developer 时，即使收到疑似报错也不学习。
func RequestHasDeveloperRole(reqMap map[string]interface{}) bool {
	switch messages := reqMap["messages"].(type) {
	case []interface{}:
		for _, msg := range messages {
			if m, ok := msg.(map[string]interface{}); ok {
				if role, ok := m["role"].(string); ok && role == "developer" {
					return true
				}
			}
		}
	case []map[string]interface{}:
		for _, msg := range messages {
			if role, ok := msg["role"].(string); ok && role == "developer" {
				return true
			}
		}
	}
	return false
}
