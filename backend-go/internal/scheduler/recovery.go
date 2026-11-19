package scheduler

import (
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/transitions"
)

type ScheduledRecoveryResult struct {
	Kind              ChannelKind
	ChannelIndex      int
	ChannelName       string
	RestoredKeys      []string
	RestoredKeyModels []string // 恢复的 (Key,模型) 组合描述（"maskedKey|model"）
	ActivatedChannel  bool
}

// SelectionResult 渠道选择结果
type SelectionResult struct {
	Upstream     *config.UpstreamConfig
	ChannelIndex int
	Reason       string // 选择原因（用于日志）
	Trace        *SelectionTrace
}

// ContextRequirement 描述当前请求的输入上下文与输出预算。
type ContextRequirement struct {
	InputTokens                int
	OutputTokens               int
	RequiredTokens             int
	MinimumContextWindowTokens int
	ExplicitOutputMax          bool
	SkipWindowValidation       bool
}

func (r *ContextRequirement) effectiveWindowTokens() int {
	if r == nil {
		return 0
	}
	// ContextWindowTokens 在能力数据层统一为可承载输入窗口；调度层不再叠加输出预留，避免对 GPT 等纯输入窗口模型重复扣减。
	return r.InputTokens
}

func (r *ContextRequirement) needsOutputValidation() bool {
	return r != nil && r.ExplicitOutputMax && r.OutputTokens > 0
}

type CandidateFilterFunc func(
	channels []ChannelInfo,
	upstreamFor func(ChannelInfo) *config.UpstreamConfig,
	candidateAvailable func(ChannelInfo, *config.UpstreamConfig) bool,
) ([]ChannelInfo, error)

// SelectionOptions 描述一次渠道选择所需的上下文。
type SelectionOptions struct {
	UserID                string
	FailedChannels        map[int]bool
	Kind                  ChannelKind
	Model                 string
	RoutePrefix           string
	ChannelName           string
	ContextRequirement    *ContextRequirement
	HasImageContent       bool
	PreserveTraceAffinity bool   // 加密续轮不因自动优先级切换渠道；显式覆盖与可用性检查仍生效
	AgentRole             string // "main" | "subagent" — 角色感知 override 查找与亲和隔离
	AgentConfidence       string
	SubagentRouting       *config.SubagentRoutingPolicy
	ApplySubagentRouting  bool
	ConfigSnapshot        *config.Config // 同一请求的候选和重试共用，不按旧索引读取新配置
	CandidateFilter       CandidateFilterFunc
	DryRun                bool // 只诊断选择结果，不更新 lastSelectedChannel 或 override TTL
}

func (s *ChannelScheduler) selectionResult(kind ChannelKind, upstream *config.UpstreamConfig, channelIndex int, reason string) *SelectionResult {
	return s.selectionResultWithRecord(kind, upstream, channelIndex, reason, true)
}

func (s *ChannelScheduler) selectionResultWithRecord(kind ChannelKind, upstream *config.UpstreamConfig, channelIndex int, reason string, record bool) *SelectionResult {
	if record {
		s.recordLastSelectedChannel(kind, channelIndex)
	}
	return &SelectionResult{
		Upstream:     upstream,
		ChannelIndex: channelIndex,
		Reason:       reason,
	}
}

// NextScheduledRecoveryTimeUTC 返回下一个 UTC 0/8/16 点后 1 秒的恢复时刻。
func NextScheduledRecoveryTimeUTC(now time.Time) time.Time {
	now = now.UTC()
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 1, 0, time.UTC)
	for _, hour := range []int{0, 8, 16} {
		candidate := time.Date(base.Year(), base.Month(), base.Day(), hour, 0, 1, 0, time.UTC)
		if now.Before(candidate) {
			return candidate
		}
	}
	return base.Add(24 * time.Hour)
}

// LastScheduledRecoveryTimeUTC 返回当前时刻之前最近一个 UTC 0/8/16 点后 1 秒的恢复时刻。
func LastScheduledRecoveryTimeUTC(now time.Time) time.Time {
	now = now.UTC()
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 1, 0, time.UTC)
	for i := len([]int{0, 8, 16}) - 1; i >= 0; i-- {
		hour := []int{0, 8, 16}[i]
		candidate := time.Date(base.Year(), base.Month(), base.Day(), hour, 0, 1, 0, time.UTC)
		if !now.Before(candidate) {
			return candidate
		}
	}
	return base.Add(-8 * time.Hour)
}

// MissedScheduledRecoveryTimeUTC 返回 (lastChecked, now] 区间内最近错过的恢复槽位。
func MissedScheduledRecoveryTimeUTC(lastChecked, now time.Time) (time.Time, bool) {
	lastChecked = lastChecked.UTC()
	now = now.UTC()
	if !now.After(lastChecked) {
		return time.Time{}, false
	}
	candidate := LastScheduledRecoveryTimeUTC(now)
	if candidate.After(lastChecked) {
		return candidate, true
	}
	return time.Time{}, false
}

func shouldSkipScheduledRecovery(disabledAt, recoverAt string, now time.Time) bool {
	if recoverAt != "" {
		parsed, err := time.Parse(time.RFC3339, recoverAt)
		if err == nil {
			return now.Before(parsed.UTC())
		}
	}
	if disabledAt == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, disabledAt)
	if err != nil {
		return false
	}
	return now.Sub(parsed.UTC()) < time.Hour
}

func kindAPIType(kind ChannelKind) string {
	switch kind {
	case ChannelKindResponses:
		return "Responses"
	case ChannelKindGemini:
		return "Gemini"
	case ChannelKindChat:
		return "Chat"
	case ChannelKindImages:
		return "Images"
	case ChannelKindVectors:
		return "Vectors"
	default:
		return "Messages"
	}
}

func (s *ChannelScheduler) scheduledRecoveryKinds() []ChannelKind {
	return []ChannelKind{ChannelKindMessages, ChannelKindResponses, ChannelKindGemini, ChannelKindChat, ChannelKindImages, ChannelKindVectors}
}

func (s *ChannelScheduler) restoreScheduledKeysForKind(kind ChannelKind, now time.Time) ([]ScheduledRecoveryResult, error) {
	cfg := s.configManager.GetConfig()
	var upstreams []config.UpstreamConfig
	switch kind {
	case ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	case ChannelKindVectors:
		upstreams = cfg.VectorsUpstream
	default:
		upstreams = cfg.Upstream
	}

	metricsManager := s.getMetricsManager(kind)
	apiType := kindAPIType(kind)
	results := make([]ScheduledRecoveryResult, 0)

	for idx, upstream := range upstreams {
		if upstream.Status == "disabled" {
			continue
		}

		// (Key,模型) 组合级限制：到期后清理，与整 Key 拉黑恢复相互独立。
		restoredKeyModels, err := s.configManager.RestoreExpiredKeyModels(apiType, idx, now)
		if err != nil {
			return nil, err
		}

		keysToRestore := make([]string, 0)
		for _, dk := range upstream.DisabledAPIKeys {
			if !config.IsAutoRecoverableDisabledReason(dk.Reason) {
				continue
			}
			if shouldSkipScheduledRecovery(dk.DisabledAt, dk.RecoverAt, now) {
				continue
			}
			keysToRestore = append(keysToRestore, dk.Key)
		}
		if len(keysToRestore) == 0 {
			// 无整 Key 恢复，但可能有 (Key,模型) 组合被恢复，需单独上报。
			if len(restoredKeyModels) > 0 {
				name := upstream.Name
				if updated := s.getUpstreamByIndex(idx, kind); updated != nil {
					name = updated.Name
				}
				results = append(results, ScheduledRecoveryResult{
					Kind:              kind,
					ChannelIndex:      idx,
					ChannelName:       name,
					RestoredKeyModels: restoredKeyModels,
				})
			}
			continue
		}

		restoreResult, err := transitions.RestoreDisabledKeysAndActivate(
			func(keys []string) ([]string, error) {
				return s.configManager.RestoreDisabledKeys(apiType, idx, keys)
			},
			func(_ string, apiKey string) {
				for _, baseURL := range upstream.GetAllBaseURLs() {
					metricsManager.MoveKeyToHalfOpen(baseURL, apiKey, NormalizedMetricsServiceType(kind, upstream.ServiceType))
				}
			},
			func(status string) error {
				return s.setChannelStatusByKind(idx, kind, status)
			},
			func() bool {
				latest := s.getUpstreamByIndex(idx, kind)
				return latest != nil && upstream.Status == "suspended" && len(upstream.APIKeys) == 0 && latest.Status == "suspended"
			},
			keysToRestore,
		)
		if err != nil {
			return nil, err
		}
		if len(restoreResult.RestoredKeys) == 0 && len(restoredKeyModels) == 0 {
			continue
		}

		updatedUpstream := s.getUpstreamByIndex(idx, kind)
		if updatedUpstream == nil {
			continue
		}

		results = append(results, ScheduledRecoveryResult{
			Kind:              kind,
			ChannelIndex:      idx,
			ChannelName:       updatedUpstream.Name,
			RestoredKeys:      restoreResult.RestoredKeys,
			RestoredKeyModels: restoredKeyModels,
			ActivatedChannel:  restoreResult.ActivatedChannel,
		})
	}

	return results, nil
}

// RunScheduledRecoveries 执行一次自动恢复扫描。
func (s *ChannelScheduler) RunScheduledRecoveries(now time.Time) ([]ScheduledRecoveryResult, error) {
	results := make([]ScheduledRecoveryResult, 0)
	for _, kind := range s.scheduledRecoveryKinds() {
		kindResults, err := s.restoreScheduledKeysForKind(kind, now.UTC())
		if err != nil {
			return nil, err
		}
		results = append(results, kindResults...)
	}
	return results, nil
}
