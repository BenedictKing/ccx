package common

import "encoding/json"

// InjectUpstreamStatusCode 在透传的上游 models JSON 响应体中注入 statusCode 字段，
// 让前端拿到上游真实状态码，而不是硬编码 200。
// body 不是 JSON 对象或重编码失败时原样返回，保证透传行为不退化。
func InjectUpstreamStatusCode(body []byte, statusCode int) []byte {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return body
	}
	obj["statusCode"] = statusCode
	patched, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return patched
}
