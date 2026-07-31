package scheduler

import (
	"errors"
	"fmt"
	"strings"
)

// SelectionTrace 记录一次渠道选择的关键阶段与候选跳过原因。
//
// 它只描述调度器已经做出的判断，不参与选择决策；调用方可用于日志、
// 诊断接口或测试断言。
type SelectionTrace struct {
	Kind        ChannelKind               `json:"kind"`
	Model       string                    `json:"model,omitempty"`
	RoutePrefix string                    `json:"routePrefix,omitempty"`
	ChannelName string                    `json:"channelName,omitempty"`
	AgentRole   string                    `json:"agentRole,omitempty"`
	Stages      []SelectionTraceStage     `json:"stages,omitempty"`
	Candidates  []SelectionTraceCandidate `json:"candidates,omitempty"`
	Selected    *SelectionTraceSelection  `json:"selected,omitempty"`
}

// SelectionTraceStage 记录某个过滤阶段后的候选数量。
type SelectionTraceStage struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// SelectionTraceCandidate 记录单个候选渠道在某阶段被跳过的原因。
type SelectionTraceCandidate struct {
	Route        ChannelRouteRef `json:"route"`
	ChannelIndex int             `json:"channelIndex"`
	ChannelName  string          `json:"channelName"`
	Stage        string          `json:"stage"`
	Reason       string          `json:"reason"`
	Details      string          `json:"details,omitempty"`
}

// SelectionTraceSelection 记录最终选中的渠道。
type SelectionTraceSelection struct {
	Route        ChannelRouteRef `json:"route"`
	ChannelIndex int             `json:"channelIndex"`
	ChannelName  string          `json:"channelName"`
	Reason       string          `json:"reason"`
}

// SelectionTraceError 在选择失败时保留已经执行的调度 trace。
type SelectionTraceError struct {
	Err   error
	Trace *SelectionTrace
}

func (e *SelectionTraceError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *SelectionTraceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newSelectionTraceError(err error, trace *SelectionTrace) error {
	if err == nil {
		return nil
	}
	return &SelectionTraceError{Err: err, Trace: trace}
}

// SelectionTraceFromError 提取失败选择过程中已经生成的调度 trace。
func SelectionTraceFromError(err error) (*SelectionTrace, bool) {
	var traceErr *SelectionTraceError
	if errors.As(err, &traceErr) && traceErr.Trace != nil {
		return traceErr.Trace, true
	}
	return nil, false
}

func newSelectionTrace(opts SelectionOptions) *SelectionTrace {
	return &SelectionTrace{
		Kind:        opts.Kind,
		Model:       opts.Model,
		RoutePrefix: opts.RoutePrefix,
		ChannelName: opts.ChannelName,
		AgentRole:   opts.AgentRole,
	}
}

func (t *SelectionTrace) setStage(name string, count int) {
	if t == nil {
		return
	}
	t.Stages = append(t.Stages, SelectionTraceStage{Name: name, Count: count})
}

func (t *SelectionTrace) skipChannel(ch ChannelInfo, stage, reason, details string) {
	if t == nil {
		return
	}
	t.Candidates = append(t.Candidates, SelectionTraceCandidate{
		Route:        ch.Route,
		ChannelIndex: ch.Index,
		ChannelName:  ch.Name,
		Stage:        stage,
		Reason:       reason,
		Details:      details,
	})
}

func (t *SelectionTrace) selectChannel(route ChannelRouteRef, channelName, reason string) {
	if t == nil {
		return
	}
	t.Selected = &SelectionTraceSelection{
		Route:        route,
		ChannelIndex: route.Index,
		ChannelName:  channelName,
		Reason:       reason,
	}
}

// FormatSelectionTraceSummary 生成适合请求日志的一行调度摘要。
// maxSkips 控制最多展示多少个跳过候选；小于等于 0 时只展示阶段和最终选择。
func FormatSelectionTraceSummary(trace *SelectionTrace, maxSkips int) string {
	if trace == nil {
		return ""
	}

	parts := make([]string, 0, 3)
	if len(trace.Stages) > 0 {
		stages := make([]string, 0, len(trace.Stages))
		for _, stage := range trace.Stages {
			stages = append(stages, fmt.Sprintf("%s:%d", stage.Name, stage.Count))
		}
		parts = append(parts, "stages="+strings.Join(stages, ","))
	}
	if maxSkips > 0 && len(trace.Candidates) > 0 {
		limit := maxSkips
		if limit > len(trace.Candidates) {
			limit = len(trace.Candidates)
		}
		skips := make([]string, 0, limit+1)
		for _, candidate := range trace.Candidates[:limit] {
			name := candidate.ChannelName
			if name == "" {
				name = "unknown"
			}
			skips = append(skips, fmt.Sprintf("%d:%s@%s/%s", candidate.ChannelIndex, name, candidate.Stage, candidate.Reason))
		}
		if len(trace.Candidates) > limit {
			skips = append(skips, fmt.Sprintf("+%d", len(trace.Candidates)-limit))
		}
		parts = append(parts, "skipped="+strings.Join(skips, ","))
	}
	if trace.Selected != nil {
		name := trace.Selected.ChannelName
		if name == "" {
			name = "unknown"
		}
		parts = append(parts, fmt.Sprintf("selected=%d:%s/%s", trace.Selected.ChannelIndex, name, trace.Selected.Reason))
	}

	return strings.Join(parts, " ")
}
