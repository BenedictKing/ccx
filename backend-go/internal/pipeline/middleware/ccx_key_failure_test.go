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

// fakeKeyCfg 是 keyFailureConfig 的最小测试桩，记录调用次数与参数。
type fakeKeyCfg struct {
	failedKeyCalls    []failedKeyCall
	blacklistCalls    []blacklistCall
	blacklistErr      error
	fuzzyModeEnabled  bool
}

type failedKeyCall struct {
	apiKey   string
	apiType  string
	duration time.Duration
}

type blacklistCall struct {
	apiType      string
	channelIndex int
	apiKey       string
	reason       string
	message      string
}

func (f *fakeKeyCfg) MarkKeyAsFailedWithDuration(apiKey, apiType string, duration time.Duration) {
	f.failedKeyCalls = append(f.failedKeyCalls, failedKeyCall{apiKey, apiType, duration})
}

func (f *fakeKeyCfg) BlacklistKey(apiType string, channelIndex int, apiKey, reason, message string) error {
	f.blacklistCalls = append(f.blacklistCalls, blacklistCall{apiType, channelIndex, apiKey, reason, message})
	return f.blacklistErr
}

func (f *fakeKeyCfg) GetFuzzyModeEnabled() bool { return f.fuzzyModeEnabled }

// makeResp 构造一个 *http.Response 用于驱动 middleware；body 用 strings.NewReader。
func makeResp(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode:    status,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// readBody 一次性读出 resp.Body 内容（middleware 必须保证 body 可读）。
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	if resp == nil || resp.Body == nil {
		return ""
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(b)
}

func TestCCXKeyFailure_RawResponse_TableDriven(t *testing.T) {
	upstream := &config.UpstreamConfig{
		Name: "test-upstream",
		FailoverRules: []config.FailoverRule{
			{
				Action:          "cooldown",
				StatusCodes:     []int{503},
				Keywords:        []string{"upstream temporarily unavailable"},
				DurationMinutes: 30,
				Description:     "503 cooldown",
			},
			{
				Action:      "blacklist",
				StatusCodes: []int{418},
				Keywords:    []string{"teapot quota"},
				Description: "418 blacklist",
			},
		},
	}

	tests := []struct {
		name              string
		resp              *http.Response
		info              *AttemptInfo
		fuzzy             bool
		blacklistErr      error
		wantErr           error
		wantFailedKeyN    int
		wantBlacklistN    int
		wantFailedKeyDur  time.Duration // 0 = 不校验
		wantBlacklistRsn  string        // "" = 不校验
		wantBodyContains  string
	}{
		{
			name: "200 透传无动作",
			resp: makeResp(200, `{"ok":true}`, nil),
			info: &AttemptInfo{APIType: "messages", ChannelIndex: 0, APIKey: "sk-aaa", Upstream: upstream},
		},
		{
			name: "无 attempt info → 透传",
			resp: makeResp(500, `boom`, nil),
			info: nil,
		},
		{
			name: "429 + Retry-After 秒数",
			resp: makeResp(429, `rate limit`, http.Header{"Retry-After": []string{"15"}}),
			info: &AttemptInfo{APIType: "messages", ChannelIndex: 1, APIKey: "sk-rate", Upstream: upstream},
			wantErr:          ErrRetryNextKey,
			wantFailedKeyN:   1,
			wantFailedKeyDur: 15 * time.Second,
			wantBodyContains: "rate limit",
		},
		{
			name: "429 缺失 Retry-After → 默认 60min",
			resp: makeResp(429, `rate limit`, nil),
			info: &AttemptInfo{APIType: "messages", ChannelIndex: 1, APIKey: "sk-rate", Upstream: upstream},
			wantErr:          ErrRetryNextKey,
			wantFailedKeyN:   1,
			wantFailedKeyDur: 60 * time.Minute,
		},
		{
			name: "channel.failoverRule keyword 命中（cooldown）",
			resp: makeResp(503, `{"error":{"message":"upstream temporarily unavailable"}}`, nil),
			info: &AttemptInfo{APIType: "messages", ChannelIndex: 2, APIKey: "sk-fr", Upstream: upstream},
			wantErr:          ErrRetryNextKey,
			wantFailedKeyN:   1,
			wantFailedKeyDur: 30 * time.Minute,
			wantBodyContains: "upstream temporarily unavailable",
		},
		{
			name: "channel.failoverRule 命中（blacklist）",
			resp: makeResp(418, `{"error":{"message":"teapot quota exceeded"}}`, nil),
			info: &AttemptInfo{APIType: "messages", ChannelIndex: 3, APIKey: "sk-bl", Upstream: upstream},
			wantErr:        ErrRetryNextKey,
			wantBlacklistN: 1,
		},
		{
			name: "401 → ShouldBlacklistKey 命中 → 拉黑 + retry",
			resp: makeResp(401, `{"error":{"type":"authentication_error","message":"invalid api key"}}`, nil),
			info: &AttemptInfo{APIType: "messages", ChannelIndex: 4, APIKey: "sk-401", Upstream: upstream},
			wantErr:          ErrRetryNextKey,
			wantBlacklistN:   1,
			wantBlacklistRsn: "authentication_error",
		},
		{
			name: "400 不符合 retry 条件 → 透传",
			resp: makeResp(400, `{"error":{"message":"bad request"}}`, nil),
			info: &AttemptInfo{APIType: "messages", ChannelIndex: 5, APIKey: "sk-400", Upstream: upstream},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &fakeKeyCfg{fuzzyModeEnabled: tt.fuzzy, blacklistErr: tt.blacklistErr}
			mw := NewCCXKeyFailureMiddleware(cfg)
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
			if got := len(cfg.failedKeyCalls); got != tt.wantFailedKeyN {
				t.Fatalf("failedKey calls = %d, want %d", got, tt.wantFailedKeyN)
			}
			if got := len(cfg.blacklistCalls); got != tt.wantBlacklistN {
				t.Fatalf("blacklist calls = %d, want %d", got, tt.wantBlacklistN)
			}
			if tt.wantFailedKeyDur > 0 && len(cfg.failedKeyCalls) > 0 {
				if cfg.failedKeyCalls[0].duration != tt.wantFailedKeyDur {
					t.Fatalf("failedKey duration = %s, want %s", cfg.failedKeyCalls[0].duration, tt.wantFailedKeyDur)
				}
			}
			if tt.wantBlacklistRsn != "" && len(cfg.blacklistCalls) > 0 {
				if cfg.blacklistCalls[0].reason != tt.wantBlacklistRsn {
					t.Fatalf("blacklist reason = %s, want %s", cfg.blacklistCalls[0].reason, tt.wantBlacklistRsn)
				}
			}
			// body 必须仍然可读（除非 status<300 直接透传）
			if tt.wantBodyContains != "" {
				body := readBody(t, out)
				if !strings.Contains(body, tt.wantBodyContains) {
					t.Fatalf("body = %q, want contains %q", body, tt.wantBodyContains)
				}
			}
		})
	}
}

func TestCCXKeyFailure_RawResponse_BodyRestored(t *testing.T) {
	cfg := &fakeKeyCfg{}
	mw := NewCCXKeyFailureMiddleware(cfg)
	resp := makeResp(401, `{"error":{"type":"authentication_error","message":"x"}}`, nil)
	info := &AttemptInfo{APIType: "messages", ChannelIndex: 0, APIKey: "sk-bd", Upstream: &config.UpstreamConfig{Name: "u"}}
	ctx := WithAttemptInfo(context.Background(), info)

	out, err := mw.RawResponse(ctx, resp)
	if !errors.Is(err, ErrRetryNextKey) {
		t.Fatalf("err = %v, want ErrRetryNextKey", err)
	}
	body, readErr := io.ReadAll(out.Body)
	if readErr != nil {
		t.Fatalf("read restored body: %v", readErr)
	}
	if !bytes.Contains(body, []byte("authentication_error")) {
		t.Fatalf("restored body does not contain expected payload: %q", string(body))
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"-1", 0},
		{"30", 30 * time.Second},
		{"   45   ", 45 * time.Second},
		{"not-a-number", 0},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := parseRetryAfter(tt.in); got != tt.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	future := time.Now().UTC().Add(2 * time.Minute).Format(http.TimeFormat)
	got := parseRetryAfter(future)
	if got <= 0 || got > 3*time.Minute {
		t.Fatalf("parseRetryAfter(future) = %s, want in (0, 3m]", got)
	}

	past := time.Now().UTC().Add(-5 * time.Minute).Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Fatalf("parseRetryAfter(past) = %s, want 0", got)
	}
}
