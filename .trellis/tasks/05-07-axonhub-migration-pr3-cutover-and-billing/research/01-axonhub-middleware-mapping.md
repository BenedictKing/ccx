# Research: axonhub middleware → ccx pipeline 9-hook 映射

- **Query**: 如何把 ccx 现有 key 级失败处理（BlacklistKey / MarkKeyAsFailedWithDuration / MatchPauseRule / channel.failoverRule）落到新的 ccx pipeline middleware 接口（PR1 已落地）
- **Scope**: internal（axonhub + ccx）
- **Date**: 2026-05-07

---

## 0. 重要前提（先看再写代码）

**axonhub 没有名为 BlacklistKey / MatchPauseRule / FailoverRule 的代码** —— 这些都是 ccx 自家逻辑（`backend-go/internal/handlers/common/upstream_failover.go` 与 `failover_rules.go` + `config/config.go`）。

axonhub 等价能力分散在：
- `axonhub/internal/server/orchestrator/retry.go`、`channel_queue_error.go`、`channel_request_tracker.go`
- `axonhub/internal/server/biz/channel_llm.go`（pause/disable channel）
- `axonhub/llm/pipeline/middleware.go` 提供了 9 个 hook，由 orchestrator 注入具体 middleware

PR3 的 T5 工作单元**不是搬 axonhub 的 retry orchestrator**，而是把现有 ccx 的 key 级判定逻辑**重新挂到 ccx 自己的 pipeline middleware**（`backend-go/internal/pipeline/middleware.go`），从而让旧 handler 的循环式 failover 退役。

---

## 1. ccx 现有 key 级处理（要被 middleware 化的目标）

### 1.1 入口：`backend-go/internal/handlers/common/upstream_failover.go`

| 行号 | 行为 | 函数 |
|---|---|---|
| 354 | 读 resp.Body → 触发各类 key 处置 | inline |
| 360-374 | `cfgManager.MatchPauseRule(status, body)` 命中 → `MarkKeyAsFailedWithDuration` + `continue`（换 key） | `MatchPauseRule` `MarkKeyAsFailedWithDuration` |
| 376-396 | `matchChannelFailoverRule(upstream, status, body, ...)` 命中 → cooldown 或 blacklist | `matchChannelFailoverRule` |
| 397-401 | route group 无可用模型 → skip 当前 attempt（不冷却不熔断） | `isModelRouteUnavailableError` |
| 403-409 | 默认走 `ShouldRetryWithNextKey` + `ShouldBlacklistKey` | `ShouldRetryWithNextKey` `ShouldBlacklistKey` |
| 411-422 | 命中拉黑 → `cfgManager.BlacklistKey(apiType, channelIndex, key, reason, msg)` | `BlacklistKey` |
| 424-473 | 命中 failover → `MarkKeyAsFailedWithDuration` 或 `MarkKeyAsFailed` + `continue` | `MarkKeyAsFailed*` |
| 510-540 | SSE 流内 `ErrCooldownKey`/`ErrEmptyStreamResponse` → 同样冷却或换 key | 见 `stream.go` |
| 541-572 | SSE 流内 `ErrBlacklistKey` → 拉黑或冷却（rate limit） | 同上 |

### 1.2 函数签名（迁移时不要改语义）

```go
// config/config.go:608
func (cm *ConfigManager) MarkKeyAsFailed(apiKey string, apiType string)

// config/config.go:797
func (cm *ConfigManager) MarkKeyAsFailedWithDuration(apiKey, apiType string, duration time.Duration)

// config/config.go:821
func (cm *ConfigManager) MatchPauseRule(statusCode int, body []byte) *PauseRule

// config/config.go:948
func (cm *ConfigManager) BlacklistKey(apiType string, channelIndex int, apiKey, reason, message string) error

// handlers/common/failover.go:32
func ShouldRetryWithNextKey(statusCode int, bodyBytes []byte, fuzzyMode bool, apiType string) (bool, bool)
//                                                                                            shouldRetry, isQuotaRelated

// handlers/common/failover.go:599
func ShouldBlacklistKey(statusCode int, bodyBytes []byte) BlacklistResult

// handlers/common/failover_rules.go:27
func matchChannelFailoverRule(upstream *config.UpstreamConfig, status int, body []byte,
    errCode, errType, errMessage string) channelFailoverDecision
```

`channelFailoverDecision` 字段：`Matched bool`、`Action string`（cooldown/blacklist）、`Description string`、`Duration time.Duration`、`Reason string`、`Message string`、`IsQuotaRelated bool`。

`BlacklistResult` 字段：`ShouldBlacklist bool`、`Reason`（authentication_error / permission_error / insufficient_balance / rate_limit）、`Message string`。

---

## 2. ccx 新 pipeline 9-hook（PR1 已落地）

来自 `backend-go/internal/pipeline/middleware.go:40-50`：

| Hook | 时机 | 是否能拿到 key/upstream | 用途 |
|---|---|---|---|
| `BeforeRequest(*llm.Request)` | Inbound 转换后 / Outbound 前 | 通过 ctx | 注入 prompt |
| `RawRequest(*http.Request)` | Outbound 后 / Execute 前 | 通过 ctx | header 改写 |
| **`RawResponse(*http.Response)`** | **Execute 返回后 / Outbound.TransformResponse 前** | **通过 ctx** | **本次目标 hook** |
| `RawStream(stream)` | Stream 路径 | 通过 ctx | 包装 SSE |
| **`RawErrorResponse(err)`** | Execute 等阶段返回错误 | 通过 ctx | 网络错误时记录、可触发 cooldown |
| `LlmResponse(*llm.Response)` | Outbound→Inbound 之间 | 通过 ctx | usage 二次解析 |
| `LlmStream(stream)` | Stream llm 包装 | 通过 ctx | usage stream |
| `InboundRawResponse(*http.Response)` | 写客户端前 | 通过 ctx | gzip / 改写 |
| `InboundRawStream(stream)` | Stream 入站包装 | 通过 ctx | 同上 |

注释 `pipeline/middleware.go:21-22` 已显式标注：
> ccx key 级 BlacklistKey / MarkKeyAsFailedWithDuration / MatchPauseRule 在此层（RawResponse）注入。

实现方法：embedding `pipeline.BaseMiddleware`，仅覆盖关心的 hook。

---

## 3. 4 项行为映射到 9-hook（含适配方案）

### 3.1 5xx → BlacklistKey（认证/权限/余额错误）

| 项 | 值 |
|---|---|
| 现状位置 | `upstream_failover.go:411-422`（非流）+ `:541-572`（SSE 流） |
| 目标 hook | `RawResponse` |
| 数据来源 | `*http.Response.StatusCode` + body bytes（先读完后回填给链） |
| 需要的上下文 | apiType、channelIndex、apiKey（**通过 `context.Context` 注入**） |
| 触发 | `result := common.ShouldBlacklistKey(resp.StatusCode, body)`；如 `ShouldBlacklist` → `cfgManager.BlacklistKey(...)` |
| 注意 | RawResponse 钩子本职是返回 `*http.Response`，**body 必须再用 `io.NopCloser(bytes.NewReader(body))` 回写**，不能消费掉 |
| 余额错误开关 | `upstream.IsAutoBlacklistBalanceEnabled()` 仍要检查 |
| 适配难度 | 低 —— 函数签名不动，只是从 handler 移到 middleware |

伪代码：

```go
type KeyFailureMiddleware struct {
    pipeline.BaseMiddleware
    Cfg *config.ConfigManager
}

func (m *KeyFailureMiddleware) RawResponse(ctx context.Context, resp *http.Response) (*http.Response, error) {
    apiType, channelIdx, apiKey, upstream := pipeline.ChannelAttemptFromContext(ctx) // PR1 已埋
    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        return resp, nil
    }

    body, _ := io.ReadAll(resp.Body)
    _ = resp.Body.Close()
    body = utils.DecompressGzipIfNeeded(resp, body)
    resp.Body = io.NopCloser(bytes.NewReader(body)) // 关键：让下游能再读

    // 1. 暂停规则（status + keyword）
    if rule := m.Cfg.MatchPauseRule(resp.StatusCode, body); rule != nil {
        m.Cfg.MarkKeyAsFailedWithDuration(apiKey, apiType, time.Duration(rule.DurationMinutes)*time.Minute)
        return resp, &pipeline.RetryNextKeyError{Reason: "pause_rule"}
    }

    // 2. 渠道 failover 规则
    if dec := matchChannelFailoverRule(upstream, resp.StatusCode, body, "", "", ""); dec.Matched {
        if dec.Action == failoverActionBlacklist {
            _ = m.Cfg.BlacklistKey(apiType, channelIdx, apiKey, dec.Reason, dec.Message)
        } else {
            m.Cfg.MarkKeyAsFailedWithDuration(apiKey, apiType, dec.Duration)
        }
        return resp, &pipeline.RetryNextKeyError{Reason: dec.Reason}
    }

    // 3. 默认 should-blacklist + retry
    if bl := common.ShouldBlacklistKey(resp.StatusCode, body); bl.ShouldBlacklist {
        if bl.Reason != "insufficient_balance" || upstream.IsAutoBlacklistBalanceEnabled() {
            _ = m.Cfg.BlacklistKey(apiType, channelIdx, apiKey, bl.Reason, bl.Message)
        }
    }
    if shouldRetry, _ := common.ShouldRetryWithNextKey(resp.StatusCode, body, m.Cfg.GetFuzzyModeEnabled(), apiType); shouldRetry {
        return resp, &pipeline.RetryNextKeyError{Reason: "retry"}
    }
    return resp, nil // 把 4xx/5xx 透传给 inbound（视为客户端错误）
}
```

### 3.2 429 含 retry-after → MarkKeyAsFailedWithDuration

| 项 | 值 |
|---|---|
| 现状位置 | `upstream_failover.go:432-435`（rule.Action=cooldown）；429 通常由 `matchChannelFailoverRule` 命中或由 `ShouldRetryWithNextKey` 走默认指数退避 |
| 目标 hook | `RawResponse` |
| 数据来源 | `resp.Header.Get("Retry-After")`（HTTP 标准，秒或日期）+ body |
| 适配方案 | 在上面的 `KeyFailureMiddleware.RawResponse` 内，**先**判 `resp.StatusCode == 429`，解析 `Retry-After`：
1. 数字 → `time.Duration(n) * time.Second`
2. HTTP-date → `time.Until(t)`
3. 缺失或 0 → 走 `matchChannelFailoverRule` 的 `Duration` 或默认 60min |
| 适配难度 | 低 —— 当前 ccx 只用规则的 `DurationMinutes`，**没**主动解析 `Retry-After`，迁移时**顺手补上** |
| 注意 | 不要自动拉黑 429（rate_limit），只冷却。SSE 流内 `ErrBlacklistKey{Reason:"rate_limit"}` 走 `MarkKeyAsFailed` 即可（参见 `upstream_failover.go:546-548`）|

### 3.3 channel.failoverRule（regex/keyword 匹配 body）→ retry

| 项 | 值 |
|---|---|
| 现状位置 | `failover_rules.go:27` `matchChannelFailoverRule` |
| 目标 hook | `RawResponse`（同上）+ `RawErrorResponse`（网络错误时也要触发，见下） |
| 数据来源 | `upstream.GetEffectiveFailoverRules()` + status + body + `extractErrorSignalFromBody` |
| 关键差异 | 现有规则是 **关键词包含**（`strings.Contains(searchText, kw)`），**不是 regex**；PRD 描述里写"regex 或 keyword"实际上当前 ccx 只支持 keyword + status_code + error_code。迁移时**保持 keyword 语义**，regex 是后续增强 |
| 行为 | `Action == cooldown` → `MarkKeyAsFailedWithDuration(..., dec.Duration)`；`Action == blacklist` → `BlacklistKey(...)`；返回 `pipeline.RetryNextKeyError` 让 retry 层换 key |
| 适配难度 | 中 —— 主要是把 `errCode/errType/errMessage` 从 stream 阶段也能透传（PR1 是否已经把 stream 阶段错误暴露到 RawErrorResponse 需要 implement 时确认） |

### 3.4 channel.pauseRule（关键词匹配 body）→ pause channel for X min

| 项 | 值 |
|---|---|
| 现状位置 | `config/config.go:821` `MatchPauseRule` + `:797` `MarkKeyAsFailedWithDuration` |
| 关键差异 | **当前 `PauseRule` 暂停的是 KEY（FixedDuration 写到 `failedKeysCache`），不是 channel**。PRD 里说"pause channel"措辞不准，实际 ccx 是 pause-key —— **迁移时保持 pause-key 语义**，否则会和现有测试 `config_pause_rules_test.go` 冲突 |
| 目标 hook | `RawResponse` |
| 字段 | `PauseRule.ErrorCode int`、`PauseRule.Keywords []string`、`PauseRule.DurationMinutes int`、`PauseRule.Description string` |
| 适配难度 | 低 —— 同 3.1 的伪代码第 1 步 |

### 3.5 网络错误 / SSE 流内错误 → RawErrorResponse + InboundRawStream

| 项 | 值 |
|---|---|
| 现状位置 | `upstream_failover.go:340-348`（pre-response 网络错误：直接 `MarkKeyAsFailed` + `continue`）+ `:510-572`（SSE 流内 `ErrCooldownKey` / `ErrBlacklistKey` / `ErrEmptyStreamResponse` / `ErrInvalidResponseBody`） |
| 目标 hook | `RawErrorResponse`（pre-response 错误） + `RawStream` 包装层（SSE 中段错误，由 stream 自己抛出） |
| 注意 | `RawErrorResponse` 只接收 `error`，**没有 resp**。要从 ctx 拿 apiType/key/channelIdx；body 不可得 → 只能调 `MarkKeyAsFailed`（无 duration） |
| SSE 流内错误如何抛出 | ccx 现有 `ErrCooldownKey` / `ErrBlacklistKey`（`handlers/common/stream.go:40`）需要继续保留；建议由 `RawStream` 包装的 stream 在 First-Byte 之后产生时不再触发 retry（已经回了 200 给客户端） |

---

## 4. ctx 注入：必须由 PR1 / PR2 提供的字段

middleware 要"看到"以下数据才能等价替换 handler 循环：

| 字段 | 类型 | 来源 |
|---|---|---|
| apiType | string | `scheduler.ChannelKind` 字符串化（messages/chat/responses/gemini） |
| channelIndex | int | scheduler 选 channel 时设 |
| apiKey | string | scheduler 选 key 时设（已有 `selectedAPIKeyContextKey="selectedAPIKey"`，见 `upstream_failover.go:48`） |
| upstream | `*config.UpstreamConfig` | scheduler 选 channel 时设 |
| requestID | string | 入口生成 |
| baseURL | string | 用于 metrics 标签 |
| isStream | bool | handler 判断后写 |

**在 implement 阶段需要先确认 `pipeline/state.go` 是否已经定义这些 ctx accessor**，没有就加。

---

## 5. retry 决策与 middleware 的边界

`pipeline.RetryNextKeyError`（PR1 应该已有，否则需要在 `pipeline/errors.go` 加）控制流：

```
RawResponse (or RawErrorResponse) returns *RetryNextKeyError
    ↓
pipeline/retry.go or executor.go 捕获 → 询问 scheduler 下一个 key/channel
    ↓
重新进入 RawRequest → Execute
```

middleware **只负责标记和判定**，**不负责换 key**（换 key 是 scheduler 在 retry layer 做的）。这一点不同于 axonhub —— axonhub 是 orchestrator（外层）调度，middleware 是装饰器；ccx 也走同样的拆分。

---

## 6. axonhub 可借鉴的 middleware 实现样本

| 文件 | 借鉴点 |
|---|---|
| `axonhub/llm/pipeline/cc/billing_header.go` | OnInboundRawResponse 在 header 里写 cost；演示如何在 middleware 里访问 ctx 指标 |
| `axonhub/llm/pipeline/maxtoken/max_token.go` | OnInboundLlmRequest 在 llm.Request 上做修改 |
| `axonhub/internal/server/orchestrator/request_execution.go` | 演示 middleware 实例如何被注入 pipeline |

billing_header 不直接搬，但**结构完全可以模仿**：embedding 一个 NoOp middleware + 实现一个钩子。

---

## 7. 风险与开放问题

1. **resp.Body 双读**：所有 RawResponse middleware 都要遵循"读完 body → `io.NopCloser` 回填"的契约，否则下游 outbound transformer 会拿到空 body。**需要在 `pipeline/middleware.go` 注释里明确写**，并写个测试。
2. **flag → 默认**：`upstream.IsAutoBlacklistBalanceEnabled()` 是 channel 级配置，要在 ctx 暴露 upstream 才能调。
3. **rate_limit 不拉黑**：429 + `isInsufficientBalanceMessage` → 拉黑（balance）；429 + 普通 rate_limit → 仅冷却。这条边界已经写在 `upstream_failover.go:546-555`，迁移时**别合并**。
4. **`isModelRouteUnavailableError`** 的 skip-cooldown 路径不能丢（`upstream_failover.go:397-401`），它是"模型路由空"特殊处理。建议作为 `RawResponse` 内独立 early-return。
5. **SSE Header 已发后不能 failover**：`upstream_failover.go:540` 之后的逻辑前提是"Header 还没发"，新 pipeline 也要保持这个语义 —— `InboundRawResponse` 没触发就 OK，`InboundRawStream` 第一个 chunk 之后必须收手。

## Caveats / Not Found

- axonhub 没有名为 BlacklistKey/MatchPauseRule 的对应物，迁移本质是"ccx 原逻辑搬入 ccx 新 middleware"。
- axonhub 的 retry/orchestrator 体量大，**不要照搬**，否则 PR3 范围爆炸。
- 当前 ccx `matchChannelFailoverRule` 不支持 regex，PRD 描述与实际略有出入，按实际为准。
