package common

import (
	"encoding/json"
	"testing"
)

func TestInjectUpstreamStatusCode(t *testing.T) {
	t.Run("JSON 对象注入真实状态码", func(t *testing.T) {
		patched := InjectUpstreamStatusCode([]byte(`{"object":"list","data":[{"id":"m1"}]}`), 200)
		var obj map[string]any
		if err := json.Unmarshal(patched, &obj); err != nil {
			t.Fatalf("注入后响应应为合法 JSON: %v", err)
		}
		if obj["statusCode"] != float64(200) {
			t.Fatalf("statusCode = %v, want 200", obj["statusCode"])
		}
		if obj["object"] != "list" {
			t.Fatalf("原有字段应保留: %v", obj)
		}
	})

	t.Run("已有 statusCode 被覆盖为真实值", func(t *testing.T) {
		patched := InjectUpstreamStatusCode([]byte(`{"statusCode":999,"data":[]}`), 200)
		var obj map[string]any
		if err := json.Unmarshal(patched, &obj); err != nil {
			t.Fatal(err)
		}
		if obj["statusCode"] != float64(200) {
			t.Fatalf("statusCode = %v, want 200", obj["statusCode"])
		}
	})

	t.Run("非 JSON 原样透传", func(t *testing.T) {
		raw := []byte("upstream raw error page")
		if got := InjectUpstreamStatusCode(raw, 200); string(got) != string(raw) {
			t.Fatalf("非 JSON 响应不应被修改: %q", got)
		}
	})

	t.Run("JSON 数组与 null 原样透传", func(t *testing.T) {
		for _, body := range []string{`[{"id":"m1"}]`, `null`, `"text"`} {
			if got := InjectUpstreamStatusCode([]byte(body), 200); got != nil && string(got) != body {
				t.Fatalf("非对象 JSON %s 不应被修改, got %q", body, got)
			}
		}
	})
}
