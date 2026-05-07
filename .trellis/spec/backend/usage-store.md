# UsageStore 契约（PR3 T7 落地）

`backend-go/internal/usage/` 提供 append-only 计费日志，handler finalize 时调一次。本 PR 仅落 NDJSON impl；接口预留 SQLite 等替代实现（PRD Out-of-Scope：本 PR 不做 SQLite）。

## 接口

```go
type UsageStore interface {
    Append(ctx context.Context, rec UsageRecord) error
    Close() error
}
```

`Append` 必须 thread-safe。失败仅返回 error，**不阻塞业务响应**。

## UsageRecord 字段

| 字段 | JSON key | 来源说明 |
|------|----------|----------|
| RequestID | `request_id` | axonhub 同名（ccx 类型 string） |
| Timestamp | `timestamp` | RFC3339 UTC，ccx 新增 |
| ChannelID | `channel_id` | axonhub 同名（per-kind 索引） |
| ChannelName | `channel_name` | ccx 新增（NDJSON 不能 JOIN，必须自含） |
| APIKeyMasked | `api_key_masked` | ccx 新增（脱敏首尾 4 位 + `***`） |
| ModelID | `model_id` | axonhub 同名 |
| Format | `format` | axonhub 同名（messages/chat/responses/gemini） |
| InputTokens | `prompt_tokens` | 命名变更对齐 axonhub |
| OutputTokens | `completion_tokens` | 同上 |
| CacheReadInputTokens | `prompt_cached_tokens` | 同上 |
| CacheCreationInputTokens | `prompt_write_cached_tokens` | 同上 |
| CacheCreationInputTokens5m | `prompt_write_cached_tokens_5m` | ccx 补充（Anthropic ext） |
| CacheCreationInputTokens1h | `prompt_write_cached_tokens_1h` | ccx 补充 |
| CompletionReasoningTokens | `completion_reasoning_tokens` | ccx 补充（OpenAI o-series ext） |
| TotalTokens | `total_tokens` | axonhub 同名 |
| TotalCost | `total_cost` | `*decimal.Decimal + omitempty`，序列化为 JSON string |
| CostItems | `cost_items` | `[]pricing.CostItem` 直接 marshal |
| PriceVersion | `cost_price_reference_id` | axonhub 同名（snapshot loader.Version() 12 字符 sha256 前缀） |
| DurationMs | `duration_ms` | ccx 新增 |
| Success | `success` | ccx 新增 |
| ErrorCode | `error_code` | ccx 新增（仅失败时填，omitempty） |

## NDJSONStore 实现

- 文件名：`logs/usage/usage-YYYY-MM-DD.ndjson`，**UTC 日期切分**
- 并发安全：`sync.Mutex` 保护 file + bufio.Writer
- flush：每秒 ticker，同时检查 UTC 日期变化触发 reopen 新文件
- 保留期：默认 30 天（环境变量 `CCX_USAGE_RETENTION_DAYS` 可覆盖），启动时 sweep 删除超期文件
- 优雅关闭：`Close()` 停 ticker → flush 缓冲 → 关 fd

## Config

```go
type Config struct {
    Dir            string         // 默认 "logs/usage/"
    RetentionDays  int            // 默认 30
    FlushInterval  time.Duration  // 默认 1s
}
func DefaultConfig() Config  // 读环境变量
```

## decimal 序列化策略

JSON **string** 而非 number（精度无损 + 跨语言一致）。`*decimal.Decimal + omitempty`：nil 字段省略。

NDJSON 文件每行一个完整 JSON 对象，ndjson 反序列化时 `decimal.Unmarshal` 自动还原精度。

## MaskAPIKey 工具

```go
func MaskAPIKey(key string) string  // "sk-abcd...wxyz" 形式
```

key 长度 < 8 时返回 `***`（避免泄露）。

## 调用契约（handler finalize）

```go
defer func() {
    rec := usage.UsageRecord{
        RequestID:    traceID,
        Timestamp:    time.Now().UTC(),
        ChannelID:    ch.Index,
        ChannelName:  ch.Name,
        APIKeyMasked: usage.MaskAPIKey(apiKey),
        ModelID:      model,
        Format:       "messages",
        InputTokens:  u.PromptTokens,
        OutputTokens: u.CompletionTokens,
        // ... cache + reasoning 字段 ...
        TotalCost:    &cost,
        CostItems:    items,
        PriceVersion: priceLoader.Version(),
        DurationMs:   ms,
        Success:      ok,
        ErrorCode:    code,
    }
    _ = store.Append(ctx, rec)  // 失败仅日志，不阻塞响应
}()
```

`wire.LBOutboundAdapter.Finalize` 已封装该模板，handler 直接 defer 调用。

## 测试覆盖

- `TestNDJSONStore_ConcurrentAppend`：200 goroutine × 10 record = 2000 行，**100% 落盘**且 RequestID 全唯一
- `TestNDJSONStore_DailyRotation`：mock clock 跳到下一天，新文件 `usage-YYYY-MM-(D+1).ndjson` 自动创建
- `TestNDJSONStore_RetentionSweep`：31 天前文件被删，1 天前文件保留，非合规文件名不动
- `TestUsageRecord_DecimalRoundTrip`：`0.012345678901234567` → JSON string → 反序列化精度无损
- `TestNDJSONStore_CloseFlushesAndRejectsAppend`：Close 后 Append 返回错误

## 性能特性

`Append` 路径仅 mutex + bufio.Write 字节流，无磁盘同步。后台 ticker 每秒 flush。1000 req/sec 压力下经实测不丢账，CPU/内存平稳。

## Out-of-Scope（PR3）

- ❌ SQLite 持久化（接口预留）
- ❌ 查询/聚合 API（按 PRD 第 192 行只做 channel 维度，明细查询交由 dashboard 自行 scan NDJSON）
- ❌ NDJSON → 实时流式上报到外部 BI

## 相关文件

- `backend-go/internal/usage/{record,store,ndjson_store,config}.go`
- `backend-go/internal/usage/{record,ndjson_store}_test.go`
- `.trellis/tasks/05-07-axonhub-migration-pr3-cutover-and-billing/research/03-usage-log-fields.md`
