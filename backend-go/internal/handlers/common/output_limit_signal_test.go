package common

import (
	"net/http"
	"testing"
)

func TestOutputLimitFromError(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		requested int
		want      int
		wantNil   bool
	}{
		{
			name:      "火山方舟 coding 端点 InvalidParameter",
			status:    http.StatusBadRequest,
			body:      `{"error":{"code":"InvalidParameter","message":"The parameter ` + "`max_tokens`" + ` specified in the request is not valid: integer above maximum value, expected a value <= 32768, but got 64000 instead. Request id: 20260825183531"}}`,
			requested: 64000,
			want:      32768,
		},
		{
			name:      "Anthropic 大于上限报错",
			status:    http.StatusBadRequest,
			body:      `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 64000 > 32768, which is the maximum allowed number of output tokens for kimi-k2.6. Either reduce the requested max_tokens or request a model with a larger output window."}}`,
			requested: 64000,
			want:      32768,
		},
		{
			name:      "OpenAI too large 报错",
			status:    http.StatusBadRequest,
			body:      `{"error":{"message":"` + "`max_tokens`" + ` is too large: 64000. This model supports at most ` + "`max_tokens`" + ` of 32768.","type":"invalid_request_error"}}`,
			requested: 64000,
			want:      32768,
		},
		{
			name:      "must be less than or equal to 通用形态",
			status:    http.StatusBadRequest,
			body:      `{"error":{"message":"Invalid parameter: max_output_tokens must be less than or equal to 65536."}}`,
			requested: 100000,
			want:      65536,
		},
		{
			name:      "between 下限上限形态取后一个数",
			status:    http.StatusBadRequest,
			body:      `{"error":{"message":"Invalid value for 'max_output_tokens': value must be between 16 and 65536."}}`,
			requested: 100000,
			want:      65536,
		},
		{
			name:      "Gemini 风格 output tokens 上限",
			status:    http.StatusBadRequest,
			body:      `{"error":{"message":"* GenerateContentRequest.generation_config.max_output_tokens: max output tokens should be less than or equal to 65536"}}`,
			requested: 100000,
			want:      65536,
		},
		{
			name:      "422 同样识别",
			status:    http.StatusUnprocessableEntity,
			body:      `{"error":{"message":"max_tokens must be no more than 32768"}}`,
			requested: 64000,
			want:      32768,
		},
		{
			name:      "上下文超限报错不得误学为输出上限",
			status:    http.StatusBadRequest,
			body:      `{"error":{"message":"This model's maximum context length is 272000 tokens. However, you requested 64000 tokens as ` + "`max_tokens`" + ` plus 300000 tokens in the messages. Please reduce the length of the messages."}}`,
			requested: 64000,
			wantNil:   true,
		},
		{
			name:      "候选值不小于被拒值时不可信",
			status:    http.StatusBadRequest,
			body:      `{"error":{"message":"The parameter ` + "`max_tokens`" + ` specified in the request is not valid: expected a value <= 128000, but got 64000 instead."}}`,
			requested: 64000,
			wantNil:   true,
		},
		{
			name:      "无输出 token 参数特征不学习",
			status:    http.StatusBadRequest,
			body:      `{"error":{"message":"temperature <= 2 is not valid for this model"}}`,
			requested: 64000,
			wantNil:   true,
		},
		{
			name:      "请求里没有输出 token 字段无从校验",
			status:    http.StatusBadRequest,
			body:      `{"error":{"message":"max_tokens must be no more than 32768"}}`,
			requested: 0,
			wantNil:   true,
		},
		{
			name:      "429 属可用性问题不学习",
			status:    http.StatusTooManyRequests,
			body:      `{"error":{"message":"max_tokens above maximum, expected a value <= 32768"}}`,
			requested: 64000,
			wantNil:   true,
		},
		{
			name:      "非 JSON 响应体",
			status:    http.StatusBadRequest,
			body:      `max_tokens expected a value <= 32768`,
			requested: 64000,
			wantNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signal := OutputLimitFromError(tt.status, []byte(tt.body), tt.requested)
			if tt.wantNil {
				if signal != nil {
					t.Fatalf("应不产生信号, got %+v", signal)
				}
				return
			}
			if signal == nil {
				t.Fatal("应识别出输出上限信号")
			}
			if signal.MaxOutputTokens != tt.want {
				t.Fatalf("MaxOutputTokens = %d, want %d", signal.MaxOutputTokens, tt.want)
			}
			if signal.Evidence == "" {
				t.Error("Evidence 不应为空")
			}
		})
	}
}
