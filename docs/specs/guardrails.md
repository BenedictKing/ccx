# Guardrails 最小集 设计文档

> 范围：内容级防护链（credential-masker 起步），预留 PII 掩码 / prompt-injection / 模态桥扩展点。
> 状态：**已实现**（2026-09-01 落地，对应 omniroute-benchmark-upgrades.md §4 方向 C）。
> 蓝本：OmniRoute `src/lib/guardrails/`（registry.ts 优先级链、credentialMasker.ts）。

## 1. 背景与目标

| 环节 | 现状 | 缺口 |
|---|---|---|
| 结构字段脱敏 | `utils.MaskAPIKey`、`channel_log_helper.go` `KeyMask`/`ProxyKeyMask`（日志字段级掩码） | 键名已知的结构性字段，**内容级无扫描** |
| 错误分类 | `handlers/common/failover.go` moderation 类识别（用于 failover 决策） | 不做内容改写 |
| 红线 | AGENTS.md「记录或展示日志时注意脱敏」是人工纪律 | 无代码兜底 |

真实缺口：上游错误消息回显的 Authorization / 自家网关密钥、body 中粘贴泄漏的 key，目前会原样进日志与 trace 详情页。

**目标**：以最低成本补上内容级凭据掩码的安全底线，同时构建可扩展的 guardrail 链架构。

## 2. 核心设计决策

1. **最小集只做 credential-masker**：preCall 扫描请求体、postCall 扫描错误消息/响应体，命中已知密钥格式（本网关 `PROXY_ACCESS_KEY`/`ADMIN_ACCESS_KEY`/渠道 key 前缀指纹、通用 `sk-`/`Bearer` 形态）即掩码。成本最低，直接兑现日志脱敏红线。
2. **架构即扩展点**：`Guardrail` 接口（`preCall/postCall`，返回 `{block, rewrite}`）+ 优先级注册表 + **fail-open**（guardrail 异常记日志放行）+ 请求头豁免。后续 PII masker、prompt-injection、模态桥都作为新 guardrail 挂链，不动调度内核。
3. **只掩不拦**：credential-masker 永远 `block=false`，rewrite 掩码后放行。本期不做阻断型 guardrail。
4. **性能预算**：单次扫描字节上限 256KB，超出只扫头部。
5. **可审计**：trace / meta 中标注"已掩码"而非静默改写。

## 3. 架构

```text
[入站请求]
     │
     ▼
TryUpstreamWithAllKeys (upstream_failover.go)
     │  ┌─ preCall guardrail 链 ─┐
     ├─►│ credential-masker      │──► 掩码后发往 upstream
     │  └─ 按 priority 排序执行 ─┘    （fail-open：异常放行）
     ...
     ▼
[上游响应]
     │
     │  错误路径：
     │  ┌─ postCall guardrail 链 ─┐
     ├─►│ credential-masker      │──► 掩码后写入 FailoverError.Body
     │  └────────────────────────┘    和 channelErrorInfo
     │
     ▼
[日志写入]
     │  CompleteLog / RecordChannelLog
     │  ┌─ 日志侧防御性掩码 ──┐
     └─►│ MaskErrorInfoForLog │──► 掩码后落库
        └────────────────────┘    （不受豁免头影响，安全底线）
```

### 3.1 Guardrail 接口

```go
type Guardrail interface {
    Name() string              // kebab-case 唯一名称
    Priority() int             // 数值越小越先执行
    Enabled() bool
    PreCall(payload []byte, ctx *Context) (*Result, error)
    PostCall(response []byte, ctx *Context) (*Result, error)
}
```

`Result` 字段：
- `Blocked bool` — 是否阻断（credential-masker 恒为 false）
- `Modified bool` — 是否改写了内容
- `Payload []byte` — PreCall 改写后的请求体
- `Response []byte` — PostCall 改写后的响应体
- `Meta map[string]any` — 可审计元数据（命中类型、数量等）

### 3.2 Registry 注册表

- 并发安全，运行时可 Register/替换
- 按 priority 升序排序执行
- 支持 `x-ccx-disabled-guardrails` 请求头逗号分隔豁免
- **fail-open**：任一 guardrail panic / error 记日志后继续，不阻断请求
- 提供全局 `DefaultRegistry()` 单例

### 3.3 CredentialMasker

**扫描层级**（按优先级从高到低，命中即替换）：

| 层级 | 来源 | 匹配方式 | 替换值 |
|---|---|---|---|
| 1. 网关自身密钥 | `PROXY_ACCESS_KEY` / `ADMIN_ACCESS_KEY` / `EXTRA_PROXY_ACCESS_KEYS` | 精确匹配完整值 | `[MASKED:gateway_key]` |
| 2. 渠道 key 前缀指纹 | 所有 upstream.APIKeys 的前 8 字符 | 前缀 + ≥12 非空白字符 | `[MASKED:channel_key]` |
| 3. 通用密钥模式 | 内置 regex 表 | sk-xxx / Bearer xxx / JWT / AWS / 连接字符串 / private key 等 | `[MASKED:<type>]` |

**约束**：
- 单测扫描字节上限 256KB，超出只扫头部，标记 `trimmed=true`
- 只掩不拦，`Blocked` 恒为 `false`
- 支持配置热重载（渠道 key 前缀随 config change 自动更新）
- 完整密钥值仅在初始化时传入，不写入日志

### 3.4 三个挂载点

| 挂载点 | 位置 | 作用 | 豁免头 |
|---|---|---|---|
| 请求侧 preCall | `upstream_failover.go` `TryUpstreamWithAllKeys` 入口 | 防止请求体中的 key 被发往第三方上游 | 受 `x-ccx-disabled-guardrails` 影响 |
| 响应侧 postCall | `upstream_failover.go` 错误路径 `lastFailoverError` 创建前 | 防止上游错误消息中的 key 泄漏到客户端 | 受 `x-ccx-disabled-guardrails` 影响 |
| 日志侧防御 | `channel_log_helper.go` `CompleteLog` / `RecordChannelLogWithSource` 入口 | 防止 key 进入日志和 trace 详情 | **不受豁免头影响**（安全底线） |

日志侧独立于请求侧豁免的原因：日志脱敏是安全底线，客户端不应有能力关闭服务端日志的防护。

## 4. 配置与运行时

- **全局开关**：credential-masker 默认启用，暂未暴露配置开关（最小集阶段）
- **密钥来源**：
  - 网关密钥：启动时从 `EnvConfig` 读取，运行时不变
  - 渠道 key 前缀：启动时 + 配置热重载时从 `ConfigManager` 提取前 8 字符
- **请求头豁免**：`X-Ccx-Disabled-Guardrails: credential-masker` 可按请求关闭（仅请求/响应侧，日志侧始终生效）

## 5. 边界与保守策略

1. **fail-open 是铁律**：guardrail 链任何异常都不阻断流量，仅 `log.Printf` 告警。
2. **只掩不拦**：本期无阻断型 guardrail；未来加入时须单独评审。
3. **不误杀优先**：通用模式宁可漏判不误判。前缀指纹取 8 字符 + 12 字符后缀阈值（总长 ≥20），降低误杀率。
4. **不做 PII / prompt-injection / 模态桥**：只留接口与注册表扩展点。
5. **不动 scoring / scheduler / autopilot**：guardrail 是内容层，不影响选路逻辑。
6. **无前端改动**：最小集阶段不暴露管理 UI。
7. **日志侧不豁免**：客户端不能通过请求头关闭日志掩码。

## 6. 验证

- **表驱动单测**（`credential_masker_test.go`）：
  - 掩码命中：OpenAI / Anthropic / 网关静态密钥 / 渠道前缀 / Bearer / JWT / AWS 等 7+ 类
  - 不误杀：普通文本、短字符串、URL、base64 图片、版本号、SHA 哈希等 8 类
  - 超限跳过：>maxScanBytes 时仅扫头部 + trimmed 标记
  - fail-open：panic guardrail 不阻断，后续 guardrail 继续执行
  - 优先级排序：priority 升序执行验证
  - 请求头豁免：`x-ccx-disabled-guardrails` 跳过验证
  - PostCall 对称：响应侧掩码与 meta stage 标记
  - 空输入：nil / 空字节返回 nil result
- **集成验证**：`make test` 全量内部包通过（37 packages all ok）
- **编译验证**：`go build ./internal/...` 通过

## 7. 未来扩展

| 扩展方向 | 接入方式 | 优先级参考 |
|---|---|---|
| PII masker | 新 guardrail 注册，postCall 扫描响应体 | 90 |
| Prompt injection 检测 | 新 guardrail 注册，preCall 可 block | 80 |
| 模态桥（图片→描述） | 新 guardrail 注册，preCall 改写请求 | 50 |
| 渠道级开关 | 从配置中读取 per-channel guardrail 白/黑名单 | — |
| 管理端 UI | 暴露注册表列表 + 开关 + 命中统计 | — |

新增 guardrail 的标准姿势：实现 `Guardrail` 接口 → 在 `main.go` 初始化时 `Register` → 补单测。无需修改既有 guardrail 或调度逻辑。

## 8. 文件索引

| 文件 | 职责 |
|---|---|
| `internal/guardrails/guardrails.go` | Guardrail 接口、Result、Context 定义 |
| `internal/guardrails/registry.go` | 优先级注册表、fail-open 执行、请求头豁免 |
| `internal/guardrails/credential_masker.go` | credential-masker 实现（静态/前缀/通用三级匹配） |
| `internal/guardrails/credential_masker_test.go` | 表驱动单测（命中/不误杀/超限/fail-open 等） |
| `internal/guardrails/default.go` | 全局默认注册表单例 |
| `internal/handlers/common/guardrails.go` | 挂载点适配函数（ApplyRequestGuardrails / ApplyResponseGuardrails / MaskErrorInfoForLog） |
| `internal/handlers/common/upstream_failover.go` | 请求侧 preCall + 响应侧 postCall 挂载 |
| `internal/handlers/common/channel_log_helper.go` | 日志侧防御性掩码挂载 |
| `main.go` | 初始化注册 + 配置热重载联动 |
