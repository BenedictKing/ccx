package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// fakePauseCfg 是 pauseRuleConfig 的最小测试桩。
type fakePauseCfg struct {
	pauseRule      *config.PauseRule
	failedKeyCalls []failedKeyCall
}

func (f *fakePauseCfg) MatchPauseRule(statusCode int, body []byte) *config.PauseRule {
	return f.pauseRule
}

func (f *fakePauseCfg) MarkKeyAsFailedWithDuration(apiKey, apiType string, duration time.Duration) {
	f.failedKeyCalls = append(f.failedKeyCalls, failedKeyCall{apiKey, apiType, duration})
}

func TestCCXPauseRule_RawResponse_TableDriven(t *testing.T) {
	tests := []struct {
		name             string
		resp             *http.Response
		info             *AttemptInfo
		rule             *config.PauseRule
		wantErr          error
		wantPauseN       int
		wantPauseDur     time.Duration
		wantBodyContains string
	}{
		{
			name: "200 直接透传无动作",
			resp: makeResp(200, `{"ok":true}`, nil),
			info: &AttemptInfo{APIType: "messages", APIKey: "sk-x"},
			rule: &config.PauseRule{ErrorCode: 200, DurationMinutes: 5},
		},
		{
			name: "无 attempt info → 透传",
			resp: makeResp(500, `payload`, nil),
			info: nil,
			rule: &config.PauseRule{ErrorCode: 500, DurationMinutes: 5},
		},
		{
			name: "未匹配规则 → 透传",
			resp: makeResp(500, `payload`, nil),
			info: &AttemptInfo{APIType: "messages", APIKey: "sk-no-rule"},
			rule: nil,
		},
		{
			name:             "命中暂停规则 → MarkKeyAsFailedWithDuration + retry",
			resp:             makeResp(503, `service unavailable`, nil),
			info:             &AttemptInfo{APIType: "messages", ChannelIndex: 7, APIKey: "sk-pr"},
			rule:             &config.PauseRule{ErrorCode: 503, DurationMinutes: 12, Description: "vendor outage"},
			wantErr:          ErrRetryNextKey,
			wantPauseN:       1,
			wantPauseDur:     12 * time.Minute,
			wantBodyContains: "service unavailable",
		},
		{
			name:         "命中规则但 DurationMinutes=0 → fallback 60min",
			resp:         makeResp(503, `oops`, nil),
			info:         &AttemptInfo{APIType: "messages", ChannelIndex: 8, APIKey: "sk-fallback"},
			rule:         &config.PauseRule{ErrorCode: 503, DurationMinutes: 0, Description: "no duration"},
			wantErr:      ErrRetryNextKey,
			wantPauseN:   1,
			wantPauseDur: 60 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &fakePauseCfg{pauseRule: tt.rule}
			mw := NewCCXPauseRuleMiddleware(cfg)
			ctx := context.Background()
			if tt.info != nil {
				ctx = WithAttemptInfo(ctx, tt.info)
			}
			out, err := mw.RawResponse(ctx, tt.resp)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if got := len(cfg.failedKeyCalls); got != tt.wantPauseN {
				t.Fatalf("MarkKeyAsFailedWithDuration calls = %d, want %d", got, tt.wantPauseN)
			}
			if tt.wantPauseDur > 0 && len(cfg.failedKeyCalls) > 0 {
				if cfg.failedKeyCalls[0].duration != tt.wantPauseDur {
					t.Fatalf("duration = %s, want %s", cfg.failedKeyCalls[0].duration, tt.wantPauseDur)
				}
			}
			if tt.wantBodyContains != "" {
				body := readBody(t, out)
				if !strings.Contains(body, tt.wantBodyContains) {
					t.Fatalf("body = %q, want contains %q", body, tt.wantBodyContains)
				}
			}
		})
	}
}

func TestCCXPauseRule_RawResponse_BodyRestored(t *testing.T) {
	cfg := &fakePauseCfg{pauseRule: &config.PauseRule{ErrorCode: 503, DurationMinutes: 5}}
	mw := NewCCXPauseRuleMiddleware(cfg)
	resp := makeResp(503, `service unavailable`, nil)
	info := &AttemptInfo{APIType: "messages", ChannelIndex: 0, APIKey: "sk-pause"}
	ctx := WithAttemptInfo(context.Background(), info)

	out, err := mw.RawResponse(ctx, resp)
	if !errors.Is(err, ErrRetryNextKey) {
		t.Fatalf("err = %v, want ErrRetryNextKey", err)
	}
	body, readErr := io.ReadAll(out.Body)
	if readErr != nil {
		t.Fatalf("read restored body: %v", readErr)
	}
	if !bytes.Contains(body, []byte("service unavailable")) {
		t.Fatalf("restored body does not contain expected payload: %q", string(body))
	}
}
