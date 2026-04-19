package common

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

const (
	failoverActionCooldown  = "cooldown"
	failoverActionBlacklist = "blacklist"
)

type channelFailoverDecision struct {
	Matched        bool
	Action         string
	Description    string
	Duration       time.Duration
	Reason         string
	Message        string
	IsQuotaRelated bool
}

func matchChannelFailoverRule(
	upstream *config.UpstreamConfig,
	statusCode int,
	body []byte,
	errCode string,
	errType string,
	errMessage string,
) channelFailoverDecision {
	if upstream == nil {
		return channelFailoverDecision{}
	}

	rules := upstream.GetEffectiveFailoverRules()
	if len(rules) == 0 {
		return channelFailoverDecision{}
	}

	bodyText := string(body)
	extractedCode, extractedType, extractedMessage := extractErrorSignalFromBody(body)
	if errCode == "" {
		errCode = extractedCode
	}
	if errType == "" {
		errType = extractedType
	}
	if errMessage == "" {
		errMessage = extractedMessage
	}

	searchText := strings.ToLower(strings.Join([]string{bodyText, errMessage, errCode, errType}, "\n"))

	for _, rule := range rules {
		action := strings.ToLower(strings.TrimSpace(rule.Action))
		if action != failoverActionCooldown && action != failoverActionBlacklist {
			continue
		}

		hasCondition := len(rule.StatusCodes) > 0 || len(rule.ErrorCodes) > 0 || len(rule.Keywords) > 0
		if !hasCondition {
			continue
		}

		if len(rule.StatusCodes) > 0 && !containsInt(rule.StatusCodes, statusCode) {
			continue
		}

		if len(rule.ErrorCodes) > 0 && !matchesErrorCodeRule(rule.ErrorCodes, errCode, errType) {
			continue
		}

		if len(rule.Keywords) > 0 && !matchesKeywordRule(rule.Keywords, searchText) {
			continue
		}

		desc := strings.TrimSpace(rule.Description)
		if desc == "" {
			desc = fmt.Sprintf("rule[%s]", action)
		}

		duration := time.Duration(rule.DurationMinutes) * time.Minute
		if action == failoverActionCooldown && duration <= 0 {
			duration = 60 * time.Minute
		}

		reason := deriveRuleReason(action, statusCode, errCode, errType, errMessage)
		message := truncateMessage(strings.TrimSpace(errMessage))
		if message == "" {
			message = truncateMessage(strings.TrimSpace(bodyText))
		}

		return channelFailoverDecision{
			Matched:        true,
			Action:         action,
			Description:    desc,
			Duration:       duration,
			Reason:         reason,
			Message:        message,
			IsQuotaRelated: reason == "insufficient_balance" || statusCode == 429,
		}
	}

	return channelFailoverDecision{}
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func matchesErrorCodeRule(ruleCodes []string, errCode string, errType string) bool {
	errCodeLower := strings.ToLower(strings.TrimSpace(errCode))
	errTypeLower := strings.ToLower(strings.TrimSpace(errType))

	for _, ruleCode := range ruleCodes {
		code := strings.ToLower(strings.TrimSpace(ruleCode))
		if code == "" {
			continue
		}
		if code == errCodeLower || code == errTypeLower {
			return true
		}
	}
	return false
}

func matchesKeywordRule(keywords []string, searchText string) bool {
	for _, keyword := range keywords {
		kw := strings.ToLower(strings.TrimSpace(keyword))
		if kw == "" {
			continue
		}
		if strings.Contains(searchText, kw) {
			return true
		}
	}
	return false
}

func extractErrorSignalFromBody(body []byte) (errCode string, errType string, errMessage string) {
	if len(body) == 0 {
		return "", "", ""
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", "", ""
	}

	readCode := func(v interface{}) string {
		switch value := v.(type) {
		case string:
			return value
		case float64:
			return fmt.Sprintf("%.0f", value)
		default:
			return ""
		}
	}

	if errObj, ok := resp["error"].(map[string]interface{}); ok {
		errCode = readCode(errObj["code"])
		if value, ok := errObj["type"].(string); ok {
			errType = value
		}
		if value, ok := errObj["message"].(string); ok {
			errMessage = value
		}
		return errCode, errType, errMessage
	}

	errCode = readCode(resp["code"])
	if value, ok := resp["type"].(string); ok {
		errType = value
	}
	if value, ok := resp["message"].(string); ok {
		errMessage = value
	}

	return errCode, errType, errMessage
}

func deriveRuleReason(action string, statusCode int, errCode string, errType string, errMessage string) string {
	if action == failoverActionCooldown {
		if statusCode == 429 || strings.Contains(strings.ToLower(errType), "rate_limit") {
			return "rate_limit"
		}
		return "temporary_failure"
	}

	typeLower := strings.ToLower(errType)
	codeLower := strings.ToLower(errCode)
	msgLower := strings.ToLower(errMessage)

	if statusCode == 401 || strings.Contains(typeLower, "auth") || strings.Contains(codeLower, "auth") || isAuthenticationMessage(msgLower) {
		return "authentication_error"
	}
	if strings.Contains(typeLower, "permission") || strings.Contains(codeLower, "permission") || isPermissionMessage(msgLower) {
		return "permission_error"
	}
	if strings.Contains(typeLower, "insufficient") || strings.Contains(codeLower, "insufficient") || isInsufficientBalanceMessage(msgLower) {
		return "insufficient_balance"
	}
	if statusCode == 400 {
		return "invalid"
	}
	return "unavailable"
}
