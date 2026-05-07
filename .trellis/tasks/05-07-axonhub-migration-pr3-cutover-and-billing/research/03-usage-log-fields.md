# Research: usage_log 字段对照与 NDJSON 设计

- **Query**: ccx PR3 T7 工作单元——`UsageStore`/`UsageRecord`/NDJSON 设计，参考 axonhub `usage_log.go` 字段，但**不搬 ent**
- **Scope**: internal（axonhub schema + ccx PR3 PRD 第 87-118 行）
- **Date**: 2026-05-07

---

## 1. axonhub usage_log 字段全表

来源：`axonhub/internal/ent/schema/usage_log.go:43-86`

| ent 字段 | Go 类型 | 说明 |
|---|---|---|
| `request_id` | int | 关联 request 表的 ID（外键，immutable） |
| `api_key_id` | int (optional) | 关联 api_key 表（删除后 nil） |
| `project_id` | int (default 1) | 关联 project，向后兼容用 1 |
| `channel_id` | int (optional) | 渠道 ID，渠道删除后 nil |
| `model_id` | string | 模型标识 |
| `prompt_tokens` | int64 | llm.Usage.PromptTokens |
| `completion_tokens` | int64 | llm.Usage.CompletionTokens |
| `total_tokens` | int64 | 总 tokens |
| `prompt_audio_tokens` | int64 (optional) | 输入音频 token |
| `prompt_cached_tokens` | int64 (optional) | 输入 cache 读 token |
| `prompt_write_cached_tokens` | int64 (optional) | cache 写总数（= 5m + 1h） |
| `prompt_write_cached_tokens_5m` | int64 (optional) | cache 写 5m 变体 |
| `prompt_write_cached_tokens_1h` | int64 (optional) | cache 写 1h 变体 |
| `completion_audio_tokens` | int64 (optional) | 输出音频 |
| `completion_reasoning_tokens` | int64 (optional) | 推理 token（o1 系列） |
| `completion_accepted_prediction_tokens` | int64 (optional) | accepted prediction |
| `completion_rejected_prediction_tokens` | int64 (optional) | rejected prediction |
| `source` | enum (api/playground/test) | 请求来源 |
| `format` | string (default `openai/chat_completions`) | 请求协议格式 |
| `total_cost` | float, nillable, optional | 总成本（axonhub 用 float，ccx 改 decimal） |
| `cost_items` | JSON `[]CostItem` | 成本明细 |
| `cost_price_reference_id` | string optional | 价格表版本 reference |

索引（仅作 NDJSON 查询参考，不搬）：

```
created_at
(model_id, created_at)
(project_id, created_at)
(channel_id, created_at)
(api_key_id, created_at)
```

时间字段由 `TimeMixin` 自动加 `created_at` / `updated_at`。

---

## 2. PR3 PRD 第 87-111 行 UsageRecord vs axonhub 字段对照

PRD 中的 ccx UsageRecord：

```go
type UsageRecord struct {
    RequestID    string
    Timestamp    time.Time
    ChannelID    int
    ChannelName  string
    APIKeyMasked string
    ModelID      string
    Format       string

    InputTokens              int64
    OutputTokens             int64
    CacheReadInputTokens     int64
    CacheCreationInputTokens int64
    TotalTokens              int64

    TotalCost      *decimal.Decimal
    CostItems      []pricing.CostItem
    PriceVersion   string

    DurationMs int64
    Success    bool
    ErrorCode  string
}
```

| ccx 字段 | axonhub 对应 | 状态 |
|---|---|---|
| `RequestID string` | `request_id int` | **类型差异**：ccx 用 string（PR2 已用 trace ID 字符串）；axonhub 是 ent 自增 int。**ccx 保持 string** |
| `Timestamp time.Time` | TimeMixin.created_at | 已对齐（ccx 显式字段更直观） |
| `ChannelID int` | `channel_id int` | 已对齐 |
| `ChannelName string` | （无） | **ccx 新增**：NDJSON 是脱机查询，没法 JOIN，必须冗余 channel name |
| `APIKeyMasked string` | （无 api_key 字段，仅 api_key_id） | **ccx 新增**：避免落明文 key，写脱敏字符串（参考 `utils.MaskAPIKey`） |
| `ModelID string` | `model_id string` | 已对齐 |
| `Format string` | `format string` | 已对齐 |
| `InputTokens` | `prompt_tokens` | 命名不同，语义对齐（建议 NDJSON 用 axonhub 命名 `prompt_tokens` 以利将来同步） |
| `OutputTokens` | `completion_tokens` | 同上 |
| `CacheReadInputTokens` | `prompt_cached_tokens` | 命名不同 |
| `CacheCreationInputTokens` | `prompt_write_cached_tokens` | 命名不同 |
| `TotalTokens` | `total_tokens` | 已对齐 |
| `TotalCost *decimal.Decimal` | `total_cost float nillable` | **类型差异**：ccx 用 decimal（精度），axonhub 用 float（性能）。ccx 保持 decimal |
| `CostItems []pricing.CostItem` | `cost_items JSON` | 已对齐 |
| `PriceVersion string` | `cost_price_reference_id string` | 命名不同 |
| `DurationMs int64` | （无） | **ccx 新增**：实测请求耗时，前端可用 |
| `Success bool` | （无；通过 request 状态推断） | **ccx 新增**：扁平结构需要 |
| `ErrorCode string` | （无） | **ccx 新增**：统计错误分布 |

### 2.1 axonhub 多出的字段（ccx 是否需要？）

| axonhub 字段 | ccx PR3 是否需要 | 建议 |
|---|---|---|
| `prompt_audio_tokens` | 不需要 | 留空，未来语音 PR 再加 |
| `prompt_write_cached_tokens_5m` | **需要** | Anthropic cache 计费精度依赖；落 NDJSON 才能后期复算 |
| `prompt_write_cached_tokens_1h` | **需要** | 同上 |
| `completion_audio_tokens` | 不需要 | 同上 |
| `completion_reasoning_tokens` | **建议加** | o1 系列推理 token 数前端 dashboard 想看 |
| `completion_accepted_prediction_tokens` | 不需要 | gpt-4o speculative decoding，前期不用 |
| `completion_rejected_prediction_tokens` | 不需要 | 同上 |
| `source` enum | 可选 | NDJSON 来源默认 "api"，省略也行 |
| `api_key_id int` | 不需要 | ccx 用 APIKeyMasked，更稳 |
| `project_id` | 不需要 | ccx 单租户，无 project 概念 |

### 2.2 推荐补充字段（仅这 3 个，避免膨胀）

```go
type UsageRecord struct {
    // ... PRD 定义的字段 ...

    CacheCreationInputTokens5m  int64 `json:"prompt_write_cached_tokens_5m,omitempty"`
    CacheCreationInputTokens1h  int64 `json:"prompt_write_cached_tokens_1h,omitempty"`
    CompletionReasoningTokens   int64 `json:"completion_reasoning_tokens,omitempty"`
}
```

---

## 3. NDJSON 落账设计

### 3.1 命名 + 切分

PR3 PRD 第 81、114-116 行已锁定：
- 路径：`logs/usage/usage-2025-05-07.ndjson`（按本地日期 UTC 切分）
- 文件锁定：`sync.Mutex + bufio.Writer`
- flush：每秒一次（可配置）
- 保留期：默认 30 天，启动时清理；可通过 `CCX_USAGE_RETENTION_DAYS` 覆盖
- 当文件不存在时按需创建（`os.OpenFile O_APPEND|O_CREATE|O_WRONLY 0o644`）

### 3.2 切分规则建议

```go
// 当前活跃文件名计算（按 UTC，避免跨时区文件名漂移）
func currentFileName(now time.Time) string {
    return now.UTC().Format("usage-2006-01-02.ndjson")
}
```

**临界处理**：每秒 flush 时检查当前日期，若变化则关旧 fd、开新 fd。**不要靠 cron 切分**，写入时检查最稳。

### 3.3 保留期清理

启动时扫一次 `logs/usage/`：
- 文件名解析出日期
- 当前 UTC 日期 - 文件日期 > N 天 → 删除
- 实现单测覆盖：超期文件、近期文件、非法文件名（不删）

### 3.4 崩溃恢复（PR3 PRD Acceptance 第 167 行明列）

NDJSON 天然附加写 + 单行原子，崩溃时**最后一行**可能残缺。

恢复策略：启动时不修复（NDJSON 的容错就是"读到坏行就跳过"）；读端必须用 `bufio.Scanner` + `json.Unmarshal` 错误时 `continue`。

不要尝试 `fsync` 每行，否则性能崩盘；每秒 buffered flush 即可。

---

## 4. NDJSON 字段顺序与命名约定（重要）

为了**后期能 1:1 映射回 axonhub 的 ent schema**（ccx → axonhub 反向迁移可行），NDJSON key 命名建议**对齐 axonhub 字段名**（snake_case），不用 Go 原生 PascalCase。

```go
type UsageRecord struct {
    RequestID    string    `json:"request_id"`
    Timestamp    time.Time `json:"timestamp"`         // RFC3339 string
    ChannelID    int       `json:"channel_id"`
    ChannelName  string    `json:"channel_name"`
    APIKeyMasked string    `json:"api_key_masked"`
    ModelID      string    `json:"model_id"`
    Format       string    `json:"format"`

    InputTokens              int64 `json:"prompt_tokens"`
    OutputTokens             int64 `json:"completion_tokens"`
    CacheReadInputTokens     int64 `json:"prompt_cached_tokens,omitempty"`
    CacheCreationInputTokens int64 `json:"prompt_write_cached_tokens,omitempty"`
    TotalTokens              int64 `json:"total_tokens"`

    CacheCreationInputTokens5m int64 `json:"prompt_write_cached_tokens_5m,omitempty"`
    CacheCreationInputTokens1h int64 `json:"prompt_write_cached_tokens_1h,omitempty"`
    CompletionReasoningTokens  int64 `json:"completion_reasoning_tokens,omitempty"`

    TotalCost    *decimal.Decimal   `json:"total_cost,omitempty"`     // string 序列化（见 §5）
    CostItems    []pricing.CostItem `json:"cost_items,omitempty"`
    PriceVersion string             `json:"cost_price_reference_id,omitempty"`

    DurationMs int64  `json:"duration_ms"`
    Success    bool   `json:"success"`
    ErrorCode  string `json:"error_code,omitempty"`
}
```

---

## 5. decimal 字段如何序列化进 NDJSON

### 5.1 选择：**string**

```json
{ "total_cost": "0.0123", ... }
```

而不是 `"total_cost": 0.0123`。

### 5.2 理由（必须写到 spec 文档里）

1. **精度无损**：`decimal.Decimal` 内部是定点；JSON number 等价于 IEEE-754 float64，**会丢尾数**。例：`0.1+0.2 == 0.30000000000000004`，但 decimal 是精确 `0.3`。落账目的就是审计/复算，丢精度等于失去意义。
2. **跨语言同构**：Python `decimal.Decimal("0.0123")` / Java `BigDecimal("0.0123")` 都用字符串构造；NDJSON 多语言消费时不会出错。
3. **shopspring/decimal 默认行为**：`MarshalJSON` 已经是 string，**不需要**写自定义；`UnmarshalJSON` 也认 string。**反过来需要**确认：如果 NDJSON 出现 number（被前端误改）需要 fallback —— 但这不是 PR3 范围。
4. **null vs 缺省**：用 `*decimal.Decimal` 指针 + `omitempty`；nil 时不输出 key，避免歧义。

### 5.3 反例（不要做）

```go
// ❌ 不要在结构体里手写 float 转字符串
TotalCost float64 `json:"total_cost"`
// ❌ 不要 json.Number
```

---

## 6. UsageStore 接口建议

```go
package usage

type Store interface {
    // Append 写一条记录。同步入 buffer + 异步 flush。
    Append(ctx context.Context, rec UsageRecord) error

    // Close 关闭文件，flush 残余 buffer。
    Close() error
}

// ndjsonStore 实现
type ndjsonStore struct {
    mu       sync.Mutex
    cfg      Config
    file     *os.File
    writer   *bufio.Writer
    flushDur time.Duration
    stopCh   chan struct{}
}
```

不暴露 query 接口 —— PR3 不做查询（dashboard 走内存 metrics）。NDJSON 仅作审计/复算用，查询是 PR4 的事。

---

## 7. 单测覆盖建议

PR3 PRD 第 167 行明确要求 4 项：并发写、按日切分、保留期清理、崩溃恢复。

| 测试 | 验证点 |
|---|---|
| `TestNDJSONStore_Append` | 单条记录写入文件，可被 scanner 读回 |
| `TestNDJSONStore_ConcurrentAppend` | 100 goroutine × 100 records，无丢失、无串行 |
| `TestNDJSONStore_DailyRotation` | 模拟时间跨 0:00，新文件创建，旧文件不被覆盖 |
| `TestNDJSONStore_RetentionCleanup` | 创建 35 个伪历史文件，启动后只保留 30 |
| `TestNDJSONStore_CrashRecovery` | 在文件中插入"半行"，scanner 仍能读完整行 |
| `TestUsageRecord_DecimalString` | TotalCost=decimal.NewFromFloat(0.1)+0.2 → JSON 出现 `"0.3"` 字符串 |
| `TestUsageRecord_NilDecimal` | TotalCost=nil → JSON 不含 total_cost key |

---

## 8. 与 metrics 的协作

PR3 PRD 第 122-127 行：

```
metrics/channel_metrics.go
  - 新增 AggregatedMetrics.TotalCost decimal.Decimal
  - RecordRequestFinalizeSuccess(usage) 改成同时写 UsageStore
```

实现要点：
- `RecordRequestFinalizeSuccess` 内调 `pricing.ComputeUsageCost(usage, modelPrice)` → 拿到 `total + items`
- 一边 `aggregated.TotalCost = aggregated.TotalCost.Add(total)` 累加内存指标
- 一边 `usageStore.Append(ctx, rec)` 落 NDJSON
- **两步要在同一个 mutex / 单 goroutine 里**，避免双源数据飘移

---

## Caveats / Not Found

- axonhub `request` 表关联的字段（`request_id` 外键）在 ccx 没有对等表，**不要试图复刻 request 表**，PR3 范围是 NDJSON 落账，不引入新数据表
- `source` enum 的 playground/test 值在 ccx 没用到，先不做
- ccx PR2 的 RequestID 是字符串（trace UUID），不是 axonhub 的 int —— NDJSON 字段就用 string，未来真要 sync 给 axonhub 时另写 mapper
- axonhub 没有 `duration_ms` / `success` / `error_code` —— ccx 这 3 个新增字段是必需的，否则 dashboard 没法展示成功率
