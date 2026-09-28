package common

import (
	"errors"
	"fmt"
	"testing"
)

func TestDetectStreamDataError(t *testing.T) {
	tests := []struct {
		name        string
		jsonData    string
		wantIsErr   bool
		wantReason  string
		wantRecover string
	}{
		{
			name:        "OpenAI 认证错误对象",
			jsonData:    `{"error":{"message":"Invalid API key provided","type":"invalid_api_key"}}`,
			wantIsErr:   true,
			wantReason:  "authentication_error",
			wantRecover: "",
		},
		{
			name:        "OpenAI 余额错误带重置时间",
			jsonData:    `{"error":{"type":"insufficient_balance","message":"Insufficient balance, quota resets at 2027-01-02T03:04:05Z"}}`,
			wantIsErr:   true,
			wantReason:  "insufficient_balance",
			wantRecover: "2027-01-02T03:04:05Z",
		},
		{
			name:        "Gemini 过载错误数字码不拉黑",
			jsonData:    `{"error":{"code":503,"message":"The model is overloaded","status":"UNAVAILABLE"}}`,
			wantIsErr:   true,
			wantReason:  "",
			wantRecover: "",
		},
		{
			name:        "Claude 流内错误事件",
			jsonData:    `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			wantIsErr:   true,
			wantReason:  "",
			wantRecover: "",
		},
		{
			name:        "Claude 退化 message 形态",
			jsonData:    `{"type":"error","message":"Invalid API key"}`,
			wantIsErr:   true,
			wantReason:  "authentication_error",
			wantRecover: "",
		},
		{
			name:        "Responses failed 事件",
			jsonData:    `{"type":"response.failed","response":{"id":"resp_1","error":{"code":"invalid_api_key","message":"Incorrect API key provided"}}}`,
			wantIsErr:   true,
			wantReason:  "authentication_error",
			wantRecover: "",
		},
		{
			name:        "error 字符串形态",
			jsonData:    `{"error":"Invalid API key"}`,
			wantIsErr:   true,
			wantReason:  "authentication_error",
			wantRecover: "",
		},
		{
			name:      "无错误键的空对象",
			jsonData:  `{}`,
			wantIsErr: false,
		},
		{
			name:      "正常 OpenAI 内容 chunk",
			jsonData:  `{"id":"c1","choices":[{"delta":{"content":"hi"}}]}`,
			wantIsErr: false,
		},
		{
			name:      "正常 usage chunk",
			jsonData:  `{"id":"c1","choices":[],"usage":{"total_tokens":3}}`,
			wantIsErr: false,
		},
		{
			name:      "Claude 正常 delta",
			jsonData:  `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`,
			wantIsErr: false,
		},
		{
			name:      "DONE 哨兵",
			jsonData:  `[DONE]`,
			wantIsErr: false,
		},
		{
			name:      "顶层 message 非错误形态不误判",
			jsonData:  `{"id":"c1","message":"progress"}`,
			wantIsErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, message, recoverAt, isErr := DetectStreamDataError(tt.jsonData)
			if isErr != tt.wantIsErr {
				t.Fatalf("isErr = %v, want %v (reason=%q message=%q)", isErr, tt.wantIsErr, reason, message)
			}
			if reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q (message=%q)", reason, tt.wantReason, message)
			}
			if tt.wantRecover != "" && recoverAt != tt.wantRecover {
				t.Fatalf("recoverAt = %q, want %q", recoverAt, tt.wantRecover)
			}
		})
	}
}

func TestClassifyPreflightStreamError(t *testing.T) {
	blacklistErr := &ErrBlacklistKey{Reason: "insufficient_balance", Message: "b"}
	tests := []struct {
		name     string
		err      error
		wantWrap bool // 期望被包装为 ErrEmptyStreamResponse
		wantSame bool // 期望原样返回
	}{
		{name: "nil", err: nil, wantSame: true},
		{name: "首字超时哨兵", err: ErrStreamFirstContentTimeout, wantSame: true},
		{name: "断流哨兵", err: ErrStreamStalled, wantSame: true},
		{name: "空响应哨兵", err: ErrEmptyStreamResponse, wantSame: true},
		{name: "无效响应体哨兵", err: ErrInvalidResponseBody, wantSame: true},
		{name: "非流式空响应哨兵", err: ErrEmptyNonStreamResponse, wantSame: true},
		{name: "竞速败出", err: ErrRacingSuperseded, wantSame: true},
		{name: "拉黑错误", err: blacklistErr, wantSame: true},
		{name: "包装过的断流", err: fmt.Errorf("%w: boomed", ErrStreamStalled), wantSame: true},
		{name: "裸上游错误", err: fmt.Errorf("upstream error: map[code:429]"), wantWrap: true},
		{name: "传输层错误", err: errors.New("read: connection reset by peer"), wantWrap: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyPreflightStreamError(tt.err)
			if tt.wantSame {
				if !errors.Is(got, tt.err) && got != tt.err {
					t.Fatalf("expected original error, got %v", got)
				}
			}
			if tt.wantWrap {
				if !errors.Is(got, ErrEmptyStreamResponse) {
					t.Fatalf("expected wrapped ErrEmptyStreamResponse, got %v", got)
				}
			}
		})
	}
}
