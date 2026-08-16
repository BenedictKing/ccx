package autopilot

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/BenedictKing/ccx/internal/scheduler"
)

// ── Trace 生命周期回填（设计 §3.4/§3.5）──
//
// 顺序：请求画像 -> RoutingDecisionTrace（Autopilot 计划）
//   -> SelectionTrace（调度裁决）-> ChannelLog（每次真实尝试）-> 请求终态。
// 关联而非复制。

// NormalizeSelectionTrace 将 scheduler.SelectionTrace 规范化为 SchedulerDecisionSummary。
// 只保留阶段计数、跳过原因代码和最终选择，不复制整个运行时对象。
func NormalizeSelectionTrace(trace *scheduler.SelectionTrace) *SchedulerDecisionSummary {
	if trace == nil {
		return nil
	}

	summary := &SchedulerDecisionSummary{}

	// 阶段计数
	for _, stage := range trace.Stages {
		summary.Stages = append(summary.Stages, SchedulerStageSummary{
			Name:  stage.Name,
			Count: stage.Count,
		})
	}

	// 跳过原因代码（去重，只保留安全代码）与被滤渠道明细
	reasonSet := make(map[string]bool)
	for _, cand := range trace.Candidates {
		if cand.Reason == "" || !isSafeSkipReason(cand.Reason) {
			continue
		}
		reasonSet[cand.Reason] = true
		summary.SkippedCandidates = append(summary.SkippedCandidates, SkippedCandidateSummary{
			ChannelIndex: cand.ChannelIndex,
			ChannelName:  cand.ChannelName,
			Stage:        cand.Stage,
			Reason:       cand.Reason,
			Details:      cand.Details,
		})
	}
	for reason := range reasonSet {
		summary.SkipReasons = append(summary.SkipReasons, reason)
	}

	// 最终选择
	if trace.Selected != nil {
		summary.SelectedUID = trace.Selected.Route.ChannelUID
		summary.SelectedName = trace.Selected.ChannelName
		summary.SelectionCode = trace.Selected.Reason
	}

	return summary
}

// AttachSchedulerDecision 按 TraceUID 将规范化的 Scheduler 裁决附加到对应 trace。
// 无论选择成功还是失败，均保留已知的硬约束阶段与原因。
// 已落盘的记录同步 UPDATE details_json；未落盘（抽样中）的只更新内存与 in-flight，
// 终态落盘时自然携带该字段，不破坏 1/10 抽样。
func (s *TraceStore) AttachSchedulerDecision(traceUID string, decision *SchedulerDecisionSummary) {
	if s == nil || traceUID == "" || decision == nil {
		return
	}
	var updated *RoutingDecisionTrace
	s.mu.Lock()
	for i := len(s.records) - 1; i >= 0; i-- {
		if s.records[i].TraceUID == traceUID {
			s.records[i].SchedulerDecision = decision
			cp := *s.records[i]
			updated = &cp
			break
		}
	}
	s.mu.Unlock()

	// 同步到 in-flight 索引
	s.inflightMu.Lock()
	if trace, ok := s.inflight[traceUID]; ok {
		trace.SchedulerDecision = decision
	}
	s.inflightMu.Unlock()

	// 锁外更新已落盘行的 details_json；未落盘的 trace 影响 0 行，天然跳过
	if updated == nil || s.db == nil {
		return
	}
	detail := updated.ToTraceDetailV2(nil, updated.TraceRevision, PersistenceSampled)
	SanitizeForPersistence(detail)
	detailsJSON, err := json.Marshal(detail)
	if err != nil {
		return
	}
	if _, err := s.db.Exec(`
UPDATE autopilot_routing_traces
SET details_json = ?
WHERE trace_uid = ?
`, string(detailsJSON), traceUID); err != nil {
		log.Printf("[TraceStore-AttachScheduler] 警告: 更新 details_json 失败 uid=%s: %v", traceUID, err)
	}
}

// attemptSeqCounters 为每个 trace 维护单调递增的 attempt 序号。
var attemptSeqCounters sync.Map // key=traceUID, value=*int

// AppendEndpointAttempt 向指定 trace 追加一条有序、容量受限的安全尝试摘要。
// attempt 完成可乱序，由受锁状态机按 attemptUid 合并。
func (s *TraceStore) AppendEndpointAttempt(traceUID string, attempt EndpointAttemptSummary) {
	if s == nil || traceUID == "" {
		return
	}

	// 分配单调递增序号
	if attempt.AttemptSeq == 0 {
		counter, _ := attemptSeqCounters.LoadOrStore(traceUID, new(int))
		seqPtr := counter.(*int)
		*seqPtr++
		attempt.AttemptSeq = *seqPtr
	}
	if attempt.EndpointLabel == "" {
		attempt.EndpointLabel = DeriveEndpointLabel(attempt.ChannelUID, attempt.AttemptSeq)
	}

	appendFn := func(trace *RoutingDecisionTrace) {
		// 查找是否已存在相同 attemptUid（乱序合并）
		for i := range trace.EndpointAttempts {
			if trace.EndpointAttempts[i].AttemptUID == attempt.AttemptUID && attempt.AttemptUID != "" {
				// 合并：更新已有摘要
				trace.EndpointAttempts[i] = attempt
				return
			}
		}
		// 容量限制：超过 MaxAttemptsPerTrace 时截断
		if len(trace.EndpointAttempts) >= MaxAttemptsPerTrace {
			trace.AttemptsTruncated = true
			trace.AttemptsTotal = len(trace.EndpointAttempts) + 1
			if trace.AttemptsByResult == nil {
				trace.AttemptsByResult = make(map[string]int)
			}
			trace.AttemptsByResult[attempt.Result]++
			return
		}
		trace.EndpointAttempts = append(trace.EndpointAttempts, attempt)
	}

	s.mu.Lock()
	for i := len(s.records) - 1; i >= 0; i-- {
		if s.records[i].TraceUID == traceUID {
			appendFn(s.records[i])
			break
		}
	}
	s.mu.Unlock()

	s.inflightMu.Lock()
	if trace, ok := s.inflight[traceUID]; ok {
		appendFn(trace)
	}
	s.inflightMu.Unlock()
}

// clearAttemptCounter 清除某 trace 的 attempt 序号计数器（终态后调用，避免内存泄漏）。
func clearAttemptCounter(traceUID string) {
	attemptSeqCounters.Delete(traceUID)
}
