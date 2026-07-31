package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"
	"sync"
	"time"
)

// 渠道-模型级运行时熔断。
//
// 与 Key 级熔断（channel_metrics_circuit.go）的分工：Key 级熔断在失败能明确归因到
// 单一模型时会主动豁免（isSingleModelFailureLocked），避免单个模型故障拖垮同 Key 下
// 其他健康模型。但豁免之后该模型本身也就不再受任何约束，导致"某渠道的 sonnet-5 持续
// 403、opus-5 正常"时调度器仍反复首选该组合。本文件补上被豁免掉的那一半惩罚：
// 以 (channelUID, keyHash, model) 为粒度记账，只隔离出问题的模型。
//
// 状态是纯内存的，进程重启即清空。403/5xx 多为临时故障，不值得固化到 config.json；
// 结构化的 model_not_found 仍由 ConfigManager.DisableKeyModel 走持久化黑名单。
const (
	// 双阈值：任一命中即熔断。
	// 快速通道容忍度低——密集失败是明确的持续故障信号，早断早省一次无效请求。
	modelCircuitFastWindow   = 5 * time.Minute
	modelCircuitFastFailures = 2
	// 慢速通道要求更多证据，避免相隔较久的偶发抖动被误判为持续故障。
	modelCircuitSlowWindow   = 30 * time.Minute
	modelCircuitSlowFailures = 3

	// 失败序列容量：等于慢速阈值，多余的旧记录对判定无贡献。
	modelCircuitMaxFailures = modelCircuitSlowFailures

	// 退避参数独立于 Key 级熔断（后者 30s~10min）：模型级隔离范围更窄，
	// 起步可以更短，但抖动组合要能被拉到更长的隔离期。
	modelCircuitBackoffBase = 60 * time.Second
	modelCircuitBackoffMax  = 30 * time.Minute

	// 条目在无任何活动后的保留时长，超过则被后台清理回收。
	modelCircuitStaleAfter = 6 * time.Hour

	modelCircuitErrorMaxLen = 200
)

// 编译期断言：条目回收阈值必须明显大于最大退避。
//
// 只读判定不刷新 lastActivity，所以隔离生效期间该组合很可能完全没有活动——
// 若 staleAfter 不够大，"因为隔离生效所以没人再请求它"会被误判成"长期无活动
// 可以回收"，把学到的 backoffLevel 一并丢掉，反复抖动的组合每次都从 60s 重新
// 起步，隔离再也升不上去。数组长度为负则编译失败。
var _ [modelCircuitStaleAfter - 4*modelCircuitBackoffMax]struct{}

type modelCircuitKey struct {
	channelUID string
	keyHash    string
	model      string
}

type modelCircuitState struct {
	// failures 为升序的失败时间戳，最多 modelCircuitMaxFailures 条；
	// 写入前会剔除超出慢速窗口的过期记录，一次成功则整体清空。
	failures     []time.Time
	backoffLevel int
	openUntil    time.Time
	lastError    string
	lastActivity time.Time
}

// ModelCircuitTracker 维护渠道-模型级熔断状态。
//
// 使用独立的锁而非复用 MetricsManager.mu：后者覆盖 requestHistory 等热路径写入，
// 把模型级判定塞进同一临界区会无谓地扩大竞争范围。
type ModelCircuitTracker struct {
	mu     sync.Mutex
	states map[modelCircuitKey]*modelCircuitState
	// logPrefix 用于日志标签，取自 MetricsManager.apiType（如 "messages"）。
	logPrefix string
}

// NewModelCircuitTracker 创建模型级熔断追踪器。apiType 仅用于日志标签。
func NewModelCircuitTracker(apiType string) *ModelCircuitTracker {
	prefix := strings.TrimSpace(apiType)
	if prefix == "" {
		prefix = "Metrics"
	}
	return &ModelCircuitTracker{
		states:    make(map[modelCircuitKey]*modelCircuitState),
		logPrefix: prefix,
	}
}

// shouldOpenModelCircuit 判断失败序列是否已构成熔断条件。
//
// 两条判据并行，任一满足即熔断。判定只看窗口内"最近连续的 N 次"：failures 已按升序
// 排列，取倒数第 N 条的时间戳与当前时刻比较，即可确认这 N 次是否落在窗口内。
func shouldOpenModelCircuit(failures []time.Time, now time.Time) (bool, time.Duration, int) {
	n := len(failures)
	// 快速通道：最近 2 次落在 5 分钟内。
	if n >= modelCircuitFastFailures &&
		now.Sub(failures[n-modelCircuitFastFailures]) <= modelCircuitFastWindow {
		return true, modelCircuitFastWindow, modelCircuitFastFailures
	}
	// 慢速通道：最近 3 次落在 30 分钟内。
	if n >= modelCircuitSlowFailures &&
		now.Sub(failures[n-modelCircuitSlowFailures]) <= modelCircuitSlowWindow {
		return true, modelCircuitSlowWindow, modelCircuitSlowFailures
	}
	return false, 0, 0
}

// modelCircuitBackoff 按退避级别计算隔离时长（60s 起，每级翻倍，上限 30min）。
func modelCircuitBackoff(level int) time.Duration {
	if level < 0 {
		level = 0
	}
	delay := modelCircuitBackoffBase
	for i := 0; i < level; i++ {
		delay *= 2
		if delay >= modelCircuitBackoffMax {
			return modelCircuitBackoffMax
		}
	}
	return delay
}

// pruneExpiredFailures 剔除超出慢速窗口的失败记录。
//
// 这是"长时间窗口"语义的落地点：超过 30 分钟的旧失败不再作为证据，故障必须在窗口内
// 重现才会累积，否则相隔数小时的独立故障会被错误地攒成一次熔断。
func pruneExpiredFailures(failures []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-modelCircuitSlowWindow)
	idx := 0
	for idx < len(failures) && failures[idx].Before(cutoff) {
		idx++
	}
	if idx == 0 {
		return failures
	}
	return failures[idx:]
}

// ModelCircuitKeyHash 由 apiKey 计算熔断表使用的 Key 指纹。
//
// 与 autopilot.KeyHashFromAPIKey 算法一致（sha256 前 16 个 hex 字符）。定义在这里是
// 为了让 scheduler 也能构造键——scheduler 不能 import autopilot（autopilot 已依赖
// scheduler，反向 import 会形成循环），而 metrics 是两者共同的下层依赖。
func ModelCircuitKeyHash(apiKey string) string {
	h := sha256.New()
	h.Write([]byte(apiKey))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// truncateModelCircuitError 按 rune 截断，避免把中文错误信息切成半个字符。
func truncateModelCircuitError(msg string) string {
	msg = strings.TrimSpace(msg)
	runes := []rune(msg)
	if len(runes) <= modelCircuitErrorMaxLen {
		return msg
	}
	return string(runes[:modelCircuitErrorMaxLen]) + "..."
}

// RecordModelFailure 记录一次渠道-模型级失败，返回本次是否触发熔断及隔离到期时间。
//
// 调用方只在"该失败确实反映渠道-模型健康度"时调用：配额/账号限流（已有 cooldown
// 机制）、内容审核（换渠道不改变请求内容）、客户端取消、以及被忽略的中间态重试都
// 不应计入，否则会把与模型可用性无关的问题记到模型头上。
func (t *ModelCircuitTracker) RecordModelFailure(channelUID, keyHash, model, errSummary string) (bool, time.Time) {
	return t.recordModelFailureAt(channelUID, keyHash, model, errSummary, time.Now())
}

func (t *ModelCircuitTracker) recordModelFailureAt(channelUID, keyHash, model, errSummary string, now time.Time) (bool, time.Time) {
	if t == nil || channelUID == "" || model == "" {
		return false, time.Time{}
	}

	k := modelCircuitKey{channelUID: channelUID, keyHash: keyHash, model: model}

	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.states[k]
	if st == nil {
		st = &modelCircuitState{}
		t.states[k] = st
	}
	st.lastActivity = now
	st.lastError = truncateModelCircuitError(errSummary)

	// 隔离期刚过后的首次失败：直接按退避级别递增重新熔断，不再累积阈值证据。
	// 上一轮隔离已经证明该组合有问题，到期放行后立刻又失败即证明故障仍在，
	// 没有理由再等第二次失败。openUntil 非零表示曾经熔断过（成功会清零）。
	if !st.openUntil.IsZero() && !now.Before(st.openUntil) {
		delay := modelCircuitBackoff(st.backoffLevel)
		st.backoffLevel++
		st.openUntil = now.Add(delay)
		st.failures = nil
		log.Printf("[%s-ModelCircuit] 渠道 %s 模型 %s 恢复后再次失败，熔断 %s（最近错误: %s）",
			t.logPrefix, channelUID, model, delay, st.lastError)
		return true, st.openUntil
	}

	st.failures = pruneExpiredFailures(st.failures, now)
	st.failures = append(st.failures, now)
	if len(st.failures) > modelCircuitMaxFailures {
		st.failures = st.failures[len(st.failures)-modelCircuitMaxFailures:]
	}

	opened, window, threshold := shouldOpenModelCircuit(st.failures, now)
	if !opened {
		return false, time.Time{}
	}

	delay := modelCircuitBackoff(st.backoffLevel)
	st.backoffLevel++
	st.openUntil = now.Add(delay)
	st.failures = nil
	log.Printf("[%s-ModelCircuit] 渠道 %s 模型 %s %s 内失败 %d 次，熔断 %s（最近错误: %s）",
		t.logPrefix, channelUID, model, window, threshold, delay, st.lastError)
	return true, st.openUntil
}

// RecordModelSuccess 记录一次成功，清空失败序列。
func (t *ModelCircuitTracker) RecordModelSuccess(channelUID, keyHash, model string) {
	t.recordModelSuccessAt(channelUID, keyHash, model, time.Now())
}

func (t *ModelCircuitTracker) recordModelSuccessAt(channelUID, keyHash, model string, now time.Time) {
	if t == nil || channelUID == "" || model == "" {
		return
	}

	k := modelCircuitKey{channelUID: channelUID, keyHash: keyHash, model: model}

	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.states[k]
	if st == nil {
		return
	}
	st.lastActivity = now
	st.failures = nil

	// 隔离期尚未到期时不得解除隔离。这类成功来自熔断前就已发出、此刻才返回的
	// 请求（长流式尤其常见），它证明的是"那一次调用没问题"，不是"故障已恢复"。
	// 若据此清零 openUntil，剩余隔离时间会被腰斩，流量立刻涌回未验证的组合。
	if !st.openUntil.IsZero() && now.Before(st.openUntil) {
		return
	}

	// 隔离已到期且这次成功证明组合可用：完全恢复并回收条目。
	// 退避级别随条目一并清除，符合"确认恢复才重置"的意图。
	delete(t.states, k)
	if !st.openUntil.IsZero() {
		log.Printf("[%s-ModelCircuit] 渠道 %s 模型 %s 恢复后请求成功，解除熔断",
			t.logPrefix, channelUID, model)
	}
}

// IsModelCircuitOpen 判断该渠道-模型-Key 组合当前是否处于熔断隔离期。
//
// 纯只读：隔离到期即对所有请求放行，不做单探针门控。
//
// 为什么不做单探针门控：门控需要在"资格发放"与"结果裁决"之间维护一段跨请求状态，
// 而本机制的查询点在候选枚举阶段（keypool 会遍历所有候选 Key），发放资格的 Key
// 未必就是最终发出请求的那把；同一组合的完成回调也无法区分是探针还是熔断前就已
// 发出、此刻才返回的旧请求。要正确关联需引入 lease token 并在每条退出路径显式
// 释放（既有 Key 级熔断的 AcquireProbe/ReleaseProbe 就是这么做的）。
//
// 这里选择不付这个复杂度：到期后若上游仍坏，第一个失败会立即按递增退避重新熔断
// （见 recordModelFailureAt 的"恢复后再次失败"分支），窗口内最多漏进几个请求，
// 而它们本来也会 failover 到其他渠道，不构成实质伤害。
func (t *ModelCircuitTracker) IsModelCircuitOpen(channelUID, keyHash, model string) bool {
	return t.isModelCircuitOpenAt(channelUID, keyHash, model, time.Now())
}

func (t *ModelCircuitTracker) isModelCircuitOpenAt(channelUID, keyHash, model string, now time.Time) bool {
	if t == nil || channelUID == "" || model == "" {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.states[modelCircuitKey{channelUID: channelUID, keyHash: keyHash, model: model}]
	if st == nil || st.openUntil.IsZero() {
		return false
	}
	return now.Before(st.openUntil)
}

// ChannelModelCircuitOpen 判断某渠道下该模型是否已在所有候选 Key 上熔断。
//
// 只有全部 Key 都处于隔离期才返回 true（升级为渠道级排除）；任一 Key 仍可用时返回
// false，让请求走 Key 级过滤去命中那把健康的 Key。keyHashes 为空时返回 false
// （fail-open：无从判断的情况不阻断调度）。
//
// 注意这里不发放探针资格：渠道级判定在调度选渠道阶段被调用，可能对同一组合查询多次，
// 若在此消耗探针会让真正发起请求的 Key 级检查拿不到资格。探针只由
// IsModelCircuitOpen 发放。
func (t *ModelCircuitTracker) ChannelModelCircuitOpen(channelUID string, keyHashes []string, model string) bool {
	return t.channelModelCircuitOpenAt(channelUID, keyHashes, model, time.Now())
}

func (t *ModelCircuitTracker) channelModelCircuitOpenAt(channelUID string, keyHashes []string, model string, now time.Time) bool {
	if t == nil || channelUID == "" || model == "" || len(keyHashes) == 0 {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, keyHash := range keyHashes {
		st := t.states[modelCircuitKey{channelUID: channelUID, keyHash: keyHash, model: model}]
		if st == nil || st.openUntil.IsZero() {
			return false
		}
		// 已到期 → 该 Key 即将被放行，渠道整体不算熔断。
		// 口径与 isModelCircuitOpenAt 完全一致，避免"渠道级说熔断、Key 级已放行"的矛盾。
		if !now.Before(st.openUntil) {
			return false
		}
	}
	return true
}

// Cleanup 回收长期无活动的条目，避免 Key 轮换或渠道删除后条目无限累积。
func (t *ModelCircuitTracker) Cleanup() {
	t.cleanupAt(time.Now())
}

func (t *ModelCircuitTracker) cleanupAt(now time.Time) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for k, st := range t.states {
		if st.lastActivity.IsZero() || now.Sub(st.lastActivity) > modelCircuitStaleAfter {
			delete(t.states, k)
		}
	}
}

// TrackedCount 返回当前追踪的组合数量（供测试与诊断使用）。
func (t *ModelCircuitTracker) TrackedCount() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.states)
}
