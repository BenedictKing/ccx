package common

import (
	"bytes"
	"fmt"
	"strings"
)

// 无限重复循环检测：模型偶发卡在同一短语/同一段话上无限复述，持续烧 token
// 直到客户端断开。检测器在流式转发路径上增量扫描尾部字节窗口，命中后由
// 各协议处理器中止当前流（客户端收到错误/不完整收尾）并返回
// ErrStreamRepetitionLoop，failover 执行器据此按「一次确证即隔离」强制熔断
// 渠道×Key×模型（见 ModelCircuitTracker.RecordModelForcedFailure）。
const (
	// repetitionWindowBytes 尾部窗口大小。要容纳「段落级」重复单元（数 KB）
	// 的多份副本：1.5KB 段落 ×3 份 ≈ 4.5KB，10KB 单元 ×3 份 = 30KB。
	repetitionWindowBytes = 32768
	// repetitionMinPhraseBytes 重复单元的字节下限。低于该长度的周期性
	// （换行符、列表符号、短分隔线、JSON 骨架等结构性重复）即使高频出现
	// 也是正常输出的形状，不作为证据。
	repetitionMinPhraseBytes = 48
	// repetitionMinTotalBytes 重复总量的字节下限。命中前烧掉的输出量约等于
	// 该值，是止损点与误杀风险的平衡：正常 Markdown 表格/代码不会有
	// 「完全字节级相同的连续复现」攒到 3KB。
	repetitionMinTotalBytes = 3072
	// repetitionScanIntervalBytes 每累计多少新增字节做一次窗口扫描，
	// 摊薄全量周期枚举的成本（单次扫描亚毫秒级）。
	repetitionScanIntervalBytes = 512
)

// repetitionMinRepeats 按重复单元长度返回所需的连续重复次数门槛。
// 单元越长，「字节级完全复现」的证据越强，所需次数越少：整段话原样复述
// 3 次在正常输出中几乎不可能；短单元则要多积证据，防表格行等结构性重复。
func repetitionMinRepeats(phraseLen int) int {
	switch {
	case phraseLen >= 1024:
		return 3
	case phraseLen >= 256:
		return 4
	default:
		return 8
	}
}

// RepetitionLoopScanner 流式文本增量的无限重复扫描器。
// 逐 delta 喂入，维护尾部字节窗口，窗口尾部出现「同一 ≥48B 单元按分档
// 次数连续复现且总量 ≥3KB」时命中；幂等：命中后停止扫描，仅记忆结论。
type RepetitionLoopScanner struct {
	buf     []byte
	pending int
	fired   bool
	// phraseLen / repeats 命中时的重复单元字节数与连续次数，供日志展示。
	phraseLen int
	repeats   int
}

// Feed 送入一段新增文本，返回自本次调用起是否已检测到无限重复。
func (s *RepetitionLoopScanner) Feed(delta string) bool {
	if s == nil || s.fired {
		return s != nil && s.fired
	}
	if delta == "" {
		return false
	}
	s.buf = append(s.buf, delta...)
	if excess := len(s.buf) - repetitionWindowBytes; excess > 0 {
		s.buf = append(s.buf[:0], s.buf[excess:]...)
	}
	s.pending += len(delta)
	if s.pending < repetitionScanIntervalBytes {
		return false
	}
	s.pending = 0
	phraseLen, repeats := detectRepetitionLoop(s.buf)
	if phraseLen > 0 {
		s.fired = true
		s.phraseLen = phraseLen
		s.repeats = repeats
		s.buf = nil
		return true
	}
	return false
}

// Found 返回是否已命中无限重复。
func (s *RepetitionLoopScanner) Found() bool {
	return s != nil && s.fired
}

// Detail 返回命中结论的人类可读描述（未命中返回空串），用于中止日志。
func (s *RepetitionLoopScanner) Detail() string {
	if s == nil || !s.fired {
		return ""
	}
	return fmt.Sprintf("重复单元 %dB × %d 次", s.phraseLen, s.repeats)
}

// detectRepetitionLoop 在窗口字节上做尾部周期检测。
// 枚举周期 p（重复单元长度），先比较最后两块（不匹配即早退换下一个候选），
// 匹配则从尾部向前数连续相同块数；小周期先于其倍数被枚举到，返回的必是
// 最小重复单元。返回 (0,0) 表示未命中。
func detectRepetitionLoop(buf []byte) (phraseLen, repeats int) {
	n := len(buf)
	if n < repetitionMinTotalBytes {
		return 0, 0
	}
	// 可检测的重复单元上限 = 窗口/最低次数档（段落级 ≥1KB 需连续 3 次）。
	maxPhrase := n / 3
	for p := repetitionMinPhraseBytes; p <= maxPhrase; p++ {
		tail := buf[n-p:]
		if !bytes.Equal(tail, buf[n-2*p:n-p]) {
			continue
		}
		maxBlocks := n / p
		m := 1
		for m < maxBlocks && bytes.Equal(buf[n-(m+1)*p:n-m*p], tail) {
			m++
		}
		if m >= repetitionMinRepeats(p) && m*p >= repetitionMinTotalBytes {
			return p, m
		}
	}
	return 0, 0
}

// repetitionLoopDetailSuffix 统一拼装中止日志/错误摘要的重复描述后缀。
func repetitionLoopDetailSuffix(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return ""
	}
	return " (" + detail + ")"
}
