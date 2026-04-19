package common

import (
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestMatchChannelFailoverRule_AllConfiguredConditionsUseAND(t *testing.T) {
	upstream := &config.UpstreamConfig{
		ServiceType: "claude",
		FailoverRules: []config.FailoverRule{
			{
				Description: "401 invalid key blacklist",
				Action:      "blacklist",
				StatusCodes: []int{401},
				ErrorCodes:  []string{"authentication_error"},
				Keywords:    []string{"invalid x-api-key"},
			},
		},
	}

	matchedBody := []byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	decision := matchChannelFailoverRule(upstream, 401, matchedBody, "", "", "")
	if !decision.Matched {
		t.Fatal("expected rule to match when status/errorCode/keyword all matched")
	}
	if decision.Action != "blacklist" {
		t.Fatalf("decision.Action = %q, want blacklist", decision.Action)
	}

	missingKeywordBody := []byte(`{"type":"error","error":{"type":"authentication_error","message":"token expired"}}`)
	decision = matchChannelFailoverRule(upstream, 401, missingKeywordBody, "", "", "")
	if decision.Matched {
		t.Fatal("expected no match when keyword condition is not met")
	}

	missingErrorCodeBody := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"invalid x-api-key"}}`)
	decision = matchChannelFailoverRule(upstream, 401, missingErrorCodeBody, "", "", "")
	if decision.Matched {
		t.Fatal("expected no match when error code condition is not met")
	}
}

func TestMatchChannelFailoverRule_ErrorCodesAndKeywordsUseORWithinList(t *testing.T) {
	upstream := &config.UpstreamConfig{
		ServiceType: "claude",
		FailoverRules: []config.FailoverRule{
			{
				Action:      "cooldown",
				StatusCodes: []int{400},
				ErrorCodes:  []string{"authentication_error", "invalid_request_error"},
				Keywords:    []string{"usage limits", "regain access on"},
			},
		},
	}

	body := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"You have reached your specified API usage limits, regain access on 2026-05-01 at 00:00 UTC."}}`)
	decision := matchChannelFailoverRule(upstream, 400, body, "", "", "")
	if !decision.Matched {
		t.Fatal("expected match when one errorCode and one keyword in each list matched")
	}
	if decision.Action != "cooldown" {
		t.Fatalf("decision.Action = %q, want cooldown", decision.Action)
	}
}

func TestMatchChannelFailoverRule_DefaultCooldownDurationAndReason(t *testing.T) {
	upstream := &config.UpstreamConfig{
		ServiceType: "claude",
		FailoverRules: []config.FailoverRule{
			{
				Action:      "cooldown",
				StatusCodes: []int{429},
			},
		},
	}

	decision := matchChannelFailoverRule(upstream, 429, []byte(`{"error":{"message":"rate limit"}}`), "", "", "")
	if !decision.Matched {
		t.Fatal("expected cooldown rule to match")
	}
	if decision.Duration != 60*time.Minute {
		t.Fatalf("decision.Duration = %v, want 60m", decision.Duration)
	}
	if decision.Reason != "rate_limit" {
		t.Fatalf("decision.Reason = %q, want rate_limit", decision.Reason)
	}
	if !decision.IsQuotaRelated {
		t.Fatal("decision.IsQuotaRelated = false, want true for 429 cooldown")
	}
}

func TestMatchChannelFailoverRule_UsesProvidedErrorSignalsAndSkipsInvalidRules(t *testing.T) {
	upstream := &config.UpstreamConfig{
		ServiceType: "claude",
		FailoverRules: []config.FailoverRule{
			{
				Action:      "noop",
				StatusCodes: []int{500},
			},
			{
				Action: "blacklist",
			},
			{
				Action:     "blacklist",
				ErrorCodes: []string{"custom_auth_code"},
			},
		},
	}

	decision := matchChannelFailoverRule(
		upstream,
		500,
		[]byte(`{}`),
		"custom_auth_code",
		"",
		"manual signal",
	)
	if !decision.Matched {
		t.Fatal("expected valid rule to match with provided error code")
	}
	if decision.Action != "blacklist" {
		t.Fatalf("decision.Action = %q, want blacklist", decision.Action)
	}
	if decision.Reason != "authentication_error" {
		t.Fatalf("decision.Reason = %q, want authentication_error", decision.Reason)
	}
}

func TestMatchesErrorCodeRuleAndKeywordRule(t *testing.T) {
	t.Run("error code should match errCode or errType", func(t *testing.T) {
		if !matchesErrorCodeRule([]string{"", "invalid_request_error"}, "invalid_request_error", "") {
			t.Fatal("expected matchesErrorCodeRule to match errCode")
		}
		if !matchesErrorCodeRule([]string{"authentication_error"}, "", "authentication_error") {
			t.Fatal("expected matchesErrorCodeRule to match errType")
		}
		if matchesErrorCodeRule([]string{"permission_error"}, "invalid_request_error", "authentication_error") {
			t.Fatal("expected matchesErrorCodeRule to return false when no candidate matched")
		}
	})

	t.Run("keyword rule should ignore blanks and use contains match", func(t *testing.T) {
		if !matchesKeywordRule([]string{" ", "invalid x-api-key"}, "error: invalid x-api-key") {
			t.Fatal("expected matchesKeywordRule to match non-empty keyword")
		}
		if matchesKeywordRule([]string{"foo", "bar"}, "no related text") {
			t.Fatal("expected matchesKeywordRule to return false when no keyword matched")
		}
	})
}

func TestMatchChannelFailoverRule_NonMatchBranchesAndFallbackFields(t *testing.T) {
	t.Run("nil upstream and empty rules should not match", func(t *testing.T) {
		if got := matchChannelFailoverRule(nil, 500, nil, "", "", ""); got.Matched {
			t.Fatal("expected nil upstream not to match")
		}
		if got := matchChannelFailoverRule(&config.UpstreamConfig{}, 500, nil, "", "", ""); got.Matched {
			t.Fatal("expected upstream with empty rules not to match")
		}
	})

	t.Run("invalid rule definitions should be skipped", func(t *testing.T) {
		upstream := &config.UpstreamConfig{
			FailoverRules: []config.FailoverRule{
				{Action: "noop", StatusCodes: []int{500}},
				{Action: "blacklist"},
			},
		}
		if got := matchChannelFailoverRule(upstream, 500, []byte(`{"error":{"message":"boom"}}`), "", "", ""); got.Matched {
			t.Fatal("expected invalid rules to be skipped")
		}
	})

	t.Run("status/error/keyword mismatch should not match", func(t *testing.T) {
		upstream := &config.UpstreamConfig{
			FailoverRules: []config.FailoverRule{
				{
					Action:      "blacklist",
					StatusCodes: []int{401},
					ErrorCodes:  []string{"authentication_error"},
					Keywords:    []string{"invalid x-api-key"},
				},
			},
		}

		if got := matchChannelFailoverRule(upstream, 500, []byte(`{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`), "", "", ""); got.Matched {
			t.Fatal("expected status mismatch not to match")
		}
		if got := matchChannelFailoverRule(upstream, 401, []byte(`{"error":{"type":"invalid_request_error","message":"invalid x-api-key"}}`), "", "", ""); got.Matched {
			t.Fatal("expected error code mismatch not to match")
		}
		if got := matchChannelFailoverRule(upstream, 401, []byte(`{"error":{"type":"authentication_error","message":"token expired"}}`), "", "", ""); got.Matched {
			t.Fatal("expected keyword mismatch not to match")
		}
	})

	t.Run("fallback description and message should be populated", func(t *testing.T) {
		upstream := &config.UpstreamConfig{
			FailoverRules: []config.FailoverRule{
				{
					Action:      "cooldown",
					StatusCodes: []int{500},
					// intentionally leave Description empty to hit fallback path
					DurationMinutes: 5,
				},
			},
		}

		body := []byte(`{}`)
		decision := matchChannelFailoverRule(upstream, 500, body, "", "", "")
		if !decision.Matched {
			t.Fatal("expected rule to match")
		}
		if decision.Description != "rule[cooldown]" {
			t.Fatalf("decision.Description = %q, want rule[cooldown]", decision.Description)
		}
		if decision.Reason != "temporary_failure" {
			t.Fatalf("decision.Reason = %q, want temporary_failure", decision.Reason)
		}
		if decision.Message != "{}" {
			t.Fatalf("decision.Message = %q, want {}", decision.Message)
		}
	})
}
