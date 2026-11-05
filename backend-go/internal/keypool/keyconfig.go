package keypool

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/ratelimit"
)

type Candidate struct {
	APIKey     string
	Config     config.APIKeyConfig
	Index      int
	Scope      string
	QuotaGroup string
}

type Selection struct {
	APIKey         string
	CredentialID   string
	CredentialName string
	QuotaGroup     string
	LimiterScope   string
	Config         config.APIKeyConfig
}

func HasEffectiveConfig(upstream *config.UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	for _, cfg := range upstream.APIKeyConfigs {
		if config.IsAPIKeyConfigEffective(cfg) {
			return true
		}
	}
	return false
}

// CandidatesForModel 返回可用 key 列表，过滤 enabled=false、failedKeys 和模型白名单。
// model 为空时不按模型过滤。
func CandidatesForModel(upstream *config.UpstreamConfig, failedKeys map[string]bool, model string) []Candidate {
	if upstream == nil || len(upstream.APIKeys) == 0 {
		return nil
	}

	configs := config.NormalizeAPIKeyConfigsForView(*upstream)
	byKey := make(map[string]config.APIKeyConfig, len(configs))
	for _, cfg := range configs {
		byKey[cfg.Key] = cfg
	}

	model = strings.TrimSpace(model)
	now := time.Now()
	out := make([]Candidate, 0, len(upstream.APIKeys))
	for i, key := range upstream.APIKeys {
		key = strings.TrimSpace(key)
		if key == "" || failedKeys[key] {
			continue
		}
		cfg := byKey[key]
		if cfg.Key == "" {
			cfg.Key = key
		}
		if cfg.Enabled != nil && !*cfg.Enabled {
			continue
		}
		if model != "" && len(cfg.Models) > 0 && !matchesModel(model, cfg.Models) {
			continue
		}
		// (Key, 模型) 组合级限制：model_not_found 等错误后，该组合在限制期内被跳过，
		// 不影响该 Key 的其他模型，也不阻断 failover 到其他渠道。
		if model != "" && upstream.IsKeyModelDisabledNow(key, model, now) {
			continue
		}
		quotaGroup := strings.TrimSpace(cfg.QuotaGroup)
		scope := "key:" + stableKeyID(key)
		if quotaGroup != "" {
			scope = "quota:" + stableKeyID("quota:"+quotaGroup)
		}
		out = append(out, Candidate{
			APIKey:     key,
			Config:     cfg,
			Index:      i,
			Scope:      scope,
			QuotaGroup: quotaGroup,
		})
	}

	// 按 weight 降序排序，weight 相同时保持原有顺序（稳定排序）
	if len(out) > 1 {
		sort.SliceStable(out, func(i, j int) bool {
			wi, wj := out[i].Config.Weight, out[j].Config.Weight
			if wi == 0 {
				wi = 1
			}
			if wj == 0 {
				wj = 1
			}
			return wi > wj
		})
	}

	return out
}

// matchesModel 检查 model 是否在允许列表中（支持通配符 *）。
// matchesModel 检查 model 是否符合 models 列表中的允许/否定规则。
// 规则：
//   - 空列表 → 默认允许所有
//   - "!prefix" 表示否定模式：匹配则立即排除
//   - "*"/"**" 表示通配所有
//   - "*xxx"/"xxx*"/"*xxx*" 分别为后缀/前缀/包含匹配
//   - 精确匹配优先
//
// 若有任意 include 规则匹配则返回 true；否定优先级最高（任意 !xx 匹配则返回 false）。
// 全部规则均为 include 时，仅当至少一条匹配时返回 true。
func matchesModel(model string, models []string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	matched := false
	hasInclude := false
	for _, raw := range models {
		pattern := strings.ToLower(strings.TrimSpace(raw))
		if pattern == "" {
			continue
		}
		negated := false
		if strings.HasPrefix(pattern, "!") {
			negated = true
			pattern = pattern[1:]
			if pattern == "" {
				continue
			}
		}

		doesMatch := matchSinglePattern(model, pattern)

		if negated {
			if doesMatch {
				return false
			}
			continue
		}

		hasInclude = true
		if doesMatch {
			matched = true
		}
	}
	if !hasInclude {
		// 全部都是否定规则（或为空），无任何排除命中则视为允许
		return true
	}
	return matched
}

// matchSinglePattern 计算单个 pattern 是否匹配 model（pattern 已 trim+lower、已剥离否定前缀）。
func matchSinglePattern(model, pattern string) bool {
	// 通配所有：* 或 **
	if pattern == "*" || pattern == "**" {
		return true
	}
	if pattern == model {
		return true
	}
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		inner := pattern[1 : len(pattern)-1]
		if inner == "" {
			// 兜底：理论上已被 "*"/"**" 分支吞掉
			return true
		}
		return strings.Contains(model, inner)
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(model, pattern[1:])
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(model, pattern[:len(pattern)-1])
	}
	return false
}

func ConfigForCandidate(channel config.UpstreamConfig, cfg config.APIKeyConfig) ratelimit.Config {
	rpm := cfg.RateLimitRPM
	if rpm <= 0 {
		rpm = channel.RateLimitRPM
	}
	windowSeconds := cfg.RateLimitWindowMinutes
	if windowSeconds <= 0 {
		windowSeconds = channel.RateLimitWindowMinutes
	}
	maxConcurrent := cfg.RateLimitMaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = channel.RateLimitMaxConcurrent
	}
	autoFromHeaders := channel.IsRateLimitAutoFromHeadersEnabled()
	if cfg.RateLimitAutoFromHeaders != nil {
		autoFromHeaders = *cfg.RateLimitAutoFromHeaders
	}
	return ratelimit.Config{
		RPM:             rpm,
		WindowSeconds:   config.RateLimitWindowSeconds(windowSeconds),
		MaxConcurrent:   maxConcurrent,
		AutoFromHeaders: autoFromHeaders,
	}
}

func stableKeyID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}
