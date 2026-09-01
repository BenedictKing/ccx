# 对标 OmniRoute 网关增强规划 设计文档

> 范围：与 OmniRoute（本地快照 `/Users/petaflops/works/unsorted/OmniRoute`，v3.8.51）全面对比后沉淀的增强方向——Tier-1 四项详细设计（配额真相分级调度、请求侧工具输出压缩、guardrails 最小集、路由预演升级）、Tier-2 backlog、不跟进决策记录。
> 状态：**部分实现**（2026-09-01 定稿；§3 请求侧压缩与 §4 Guardrails 最小集已落地，拆独立 spec [guardrails.md](./guardrails.md) 与 [request-compression.md](./request-compression.md)；其余项规划中）。各项落地后应拆出独立 spec 并回填状态。
> 锚点约定：CCX 锚点为仓库相对路径；OmniRoute 锚点均为上述快照仓库内相对路径。

## 1. 背景与对比结论

OmniRoute 是 TS/Next.js 单体消费级网关（352 provider、19 种路由策略、免费额度目录、五套扩展系统）；CCX 是订阅型个人/小团队网关（订阅账号体系、场景化智能路由、能力自学习）。对比结论：**CCX 在调度智能（场景预设 + effort 分级 + benchmark 实测画像）、渠道实例级能力自学习（compat-cache）、协议联邦（跨协议同账号 failover）上占优，不必对标；真正的提升空间集中在配额驱动调度、网关侧 token 压缩、内容面防护、路由决策可解释性、管理面 MCP 化五块。**

| 方向 | 一句话 | 优先级 | 本文 |
|---|---|---|---|
| A. 配额真相分级 + 按余量调度 | "这个账号本窗口还剩多少"参与选路 | Tier-1 | §2 |
| B. 请求侧工具输出压缩 | 请求发出前压 tool_result 历史（RTK 模式） | Tier-1 | §3 |
| C. Guardrails 最小集 | 内容级凭据掩码起步，预留扩展链 | Tier-1 | §4 |
| D. 路由预演升级 | 请求体直喂的零上游请求路由预演 | Tier-1 | §5 |
| E~H. MCP 化 / 韧性补课 / context-relay / doctor CLI | — | Tier-2 | §6 |
| 测试矩阵 / 免费目录 / TLS 隐身等 | — | Tier-3 或不跟进 | §7 |

## 2. 方向 A：配额真相分级与按余量调度

OmniRoute 蓝本：`src/lib/quota/`（`providerQuotaTelemetry.ts` 来源优先级、`providerQuotaState.ts` token 账本、`accountBuckets.ts` 懒重置饱和桶）、`open-sse/services/combo/quotaShareStrategy.ts`（DRR + P2C + 降级不剔除）、`docs/routing/QUOTA_SHARE.md`。

### 2.1 现状盘点

| 环节 | 现状 | 缺口 |
|---|---|---|
| 用量/余额采集 | `SubscriptionBalanceFetcher` 接口（`autopilot/subscription_balance_fetcher.go`）+ 厂商 fetcher（`kimi_console.go`、`volcengine_coding_plan.go`、`minimax_token_plan.go` 等，各带 TTL 缓存；new-api 走 Verify 不接此接口） | 各适配器口径独立，无统一"可信度"语义 |
| 评分消费 | `autopilot/scoring.go` `ApplyCostPreference` + `CostTierFree`（`model_profile.go`） | 只看单价与成本档，不看该账号当前窗口剩余额度 |
| 底层 key 选择 | `keypool/keyconfig.go` Weight 排序 + `scheduler/select.go` 限速负载卸载水位线（high 0.50 / low 0.30 / vision 0.80） | 只有 RPM/并发维度卸载，无 token 配额维度 |
| 速率学习 | `ratelimit/rate_limit_discovery.go`（header/429/TTFB 学 RPM、MaxConcurrent） | 学速率限制，不学额度窗口重置 |
| 时效偏好 | TTFB 拥挤学习（`autopilot.md` §5.1，`observeStreamingLatency`） | 管"快慢"，不管"还能用多久" |

### 2.2 核心设计决策

1. **配额真相 5 级 + 来源优先级**：`healthy / approaching_limit / exhausted / unavailable / unknown`，硬原则 **unknown ≠ exhausted，unknown 不禁用渠道**（fail-open）。来源优先级 `provider_api > response_headers > configured > estimated > unknown`，按维度逐项取最优；响应头只经显式 per-provider 映射解析（如 Anthropic `anthropic-ratelimit-input-tokens-*`、OpenAI `x-ratelimit-*-tokens`），绝不猜测通用头名。CCX 的 console 适配器归入 `provider_api`，newapi 倍率同步归入 `configured`。
2. **懒重置饱和桶，无后台 cron**：每 `(账号UID, 窗口类型)` 一个桶（如 5h / 月度），读取时判断 `now >= resetsAtMs` 即清零。与现有定时恢复循环（`scheduler/recovery.go`）解耦，桶本身零调度成本。
3. **两层接线，只降级不剔除**：
   - SmartRouter 层：`ScoreCandidate` 增加 `quotaHeadroom` 因子（归一化剩余额度，unknown 给中性分 0.5 不惩罚——对齐冷候选中性分纪律），进 `DefaultTaskWeights` 加权体系；与 TTFB 拥挤度合成"时效 × 余量"双维度。
   - 底层选择层：`scheduler/select.go` 沿用限速卸载的水位线模式，饱和账号**沉底排序而非过滤**；全员饱和时全体回候选（fail-open）。
4. **DRR 权重公平为可选二期**：先落地降级排序；若后续需要"长期按 key Weight 比例精确收敛"，再引入 Deficit Round Robin（每轮加 `weight/totalWeight` 量子，选 deficit 最大者），与现有 Weight 排序兼容。

### 2.3 实现要点

- 新增 `backend-go/internal/quota/` 包：`Truth`（5 级枚举 + 来源）、`Buckets`（懒重置饱和桶，内存态 + 可选落盘 metrics.db）、`Headers`（per-provider 响应头映射解析，接 `ratelimit/rate_limit_applier.go` 同一挂点）。
- SmartRouter 接线：`buildChannelEntry` 填充 `QuotaHeadroom`，`scoring.go` 加因子与权重（默认权重从既有因子匀出，保持和为 1）。
- 前端：订阅中心 `UsageQuotaRows.vue` 增加"真相等级"列（healthy/unknown 可视区分）。

### 2.4 边界与保守策略

1. 一切门控 fail-open：配额包崩溃/无数据不影响现有选路。
2. 单机内存 + SQLite，不引入 Redis。
3. 不推翻 newapi `MultiplierSource` 状态机（`config.go`），配额分级只消费其产出。
4. 与 TTFB 拥挤度方案（kiro.rs 蓝本四层）合流时共用同一采集管道，避免双份观测开销。

## 3. 方向 B：请求侧工具输出压缩（RTK 模式） ✅ 已实现

> 独立 spec：[request-compression.md](./request-compression.md)；本节约束与设计决策同步保留，作为上下文参考。

OmniRoute 蓝本：`open-sse/services/compression/`（`engines/rtk/` 命令输出 filter、`fidelityGate.ts` 保真门、`pipelineGuards.ts` 膨胀回退）、`docs/compression/RTK_COMPRESSION.md`。

### 3.1 现状盘点

| 环节 | 现状 | 缺口 |
|---|---|---|
| 会话压缩 | `handlers/responses/compact.go`、`compact_v2.go`、`compact_local.go`；`session/manager.go` `CreateCompactedSession` | 客户端主动调用的事后会话压缩，非网关带内 |
| 请求前处理 | `handlers/common/body_clamp.go` 等请求体处理链 | 无内容压缩钩子 |
| 遥测 | `metrics/sqlite_store.go` request_records token 四类计数 | 无压缩节省维度 |

### 3.2 核心设计决策

1. **作用域收窄**：只压 messages **历史中的 tool_result / 工具输出内容**；不碰 system、不碰最后一条 user 消息、不碰响应体。CC 代理流量工具输出占大头，收益集中在这一块。
2. **分类器 + 结构化 filter 起步**：命令输出先分类（git / test / build / package / docker / 通用堆栈等 ~10 类起步，OmniRoute 49 类不必照搬），每类 filter 保错误/警告/摘要/变更文件/tail 上下文，剥进度条/重复行/ANSI 噪声。
3. **保真门是红线**：压缩后校验受保护内容存活——JSON key 完整、数字字面量完整、diff hunk 完整、受保护 token 存活率阈值（≥95% 起步）；任一不达标**回退原文**。压缩后体积反而变大也整体回退（膨胀回退）。
4. **fail-open**：压缩器 panic/超 CPU 预算时按原文放行，绝不因压缩失败拒绝请求。
5. **开关层级**：全局默认关 → 渠道级开启 → 请求头 opt-out（`x-ccx-compression: off`）；场景预设联动（`batch_cheap` 等价格敏感预设建议默认开）。
6. **遥测闭环**：每次压缩记录原始/压缩后 token 估算与回退原因，进 metrics（request_records 扩列或独立表），成本报表（`CostReportView.vue`）展示节省。

### 3.3 实现要点

- 新增 `backend-go/internal/compression/`：`Classifier`（命令输出分类）、`Filters`（表驱动 filter 集）、`FidelityGate`（保真校验）、`Plan`（开关层级解析）。
- 接线点：协议转换完成后、上游请求发出前，挂 `handlers/common` 请求处理链（与 `body_clamp.go` 同位置语义）；四类入站协议各自的 messages 归一形态上操作，避免逐协议重复实现。
- Go 侧无需 worker 池，但对超长历史设处理预算（条数/字节上限），超出部分跳过压缩。

### 3.4 边界与保守策略

1. 工具调用参数（tool_use block）永不压缩——只压工具结果。
2. 流式响应不压缩（响应侧不动，与 OmniRoute 同口径）。
3. images/vectors 入口不适用。

## 4. 方向 C：Guardrails 最小集 ✅ 已实现

> 独立 spec：[guardrails.md](./guardrails.md)；本节约束与设计决策同步保留，作为上下文参考。

OmniRoute 蓝本：`src/lib/guardrails/`（`registry.ts` 优先级链、`BaseGuardrail` preCall/postCall 契约、fail-open、`credential-masker.ts` priority 95、请求头 `x-omniroute-disabled-guardrails` 豁免）。

### 4.1 现状盘点

| 环节 | 现状 | 缺口 |
|---|---|---|
| 结构字段脱敏 | `utils.MaskAPIKey`、`channel_log_helper.go` `KeyMask`/`ProxyKeyMask`（日志字段级掩码） | 键名已知的结构性字段，**内容级无扫描** |
| 错误分类 | `handlers/common/failover.go` moderation 类识别（用于 failover 决策） | 不做内容改写 |
| 红线 | AGENTS.md「记录或展示日志时注意脱敏」是人工纪律 | 无代码兜底 |

真实缺口：上游错误消息回显的 Authorization / 自家网关密钥、body 中粘贴泄漏的 key，目前会原样进日志与 trace 详情页。

### 4.2 核心设计决策

1. **最小集只做 credential-masker**：preCall 扫描请求体、postCall 扫描错误消息/响应体，命中已知密钥格式（本网关 `PROXY_ACCESS_KEY`/`ADMIN_ACCESS_KEY`/渠道 key 的前缀指纹、通用 `sk-`/`Bearer` 形态）即掩码。成本最低，直接兑现日志脱敏红线。
2. **架构即扩展点**：`Guardrail` 接口（`preCall/postCall`，返回 `{block, rewrite}`）+ 优先级注册表 + **fail-open**（guardrail 异常记日志放行）+ 请求头豁免。后续 PII masker、prompt-injection、模态桥（"图片请求发给非视觉模型 → 自动转描述"）都作为新 guardrail 挂链，不动调度内核。
3. **挂载点**：请求侧挂 `handlers/common` 处理链头部；响应侧挂 `upstream_failover.go` 错误路径与 `channel_log_helper.go` 写日志路径（写前扫）。

### 4.3 边界与保守策略

1. 只掩不拦：credential-masker 永远 `block=false`，rewrite 掩码后放行。
2. 性能预算：单次扫描字节上限（如 256KB），超出只扫头部。
3. 掩码产物需可审计（trace 中标注"已掩码"而非静默改写）。

## 5. 方向 D：路由预演升级（请求体直喂 route preview）

OmniRoute 蓝本：`POST /api/omniroute/route/preview`（零上游请求确定性预演）+ `decisionTrace.ts`。

### 5.1 现状盘点

| 环节 | 现状 | 缺口 |
|---|---|---|
| SmartRouter dry-run | `POST /api/smart-routing/diagnose`（`autopilot/handlers_dryrun.go`，兼容 `/route-dryrun`）已零上游请求 | `DryRunRequest` 需调用方**手工填** capability 布尔、`estTokens` 等特征，特征提取责任在调用方 |
| 请求画像 | `handlers/common/autopilot_request_profile.go` 已有真实请求的特征提取 | 未与 dry-run 打通 |
| 底层调度诊断 | `POST /api/{type}/channels/scheduler/diagnose`（main.go:1280 等） | 与 SmartRouter dry-run 两张面，入口分离 |
| 决策原因 | trace stages（protocol_federation / smart_filter / model_circuit_filter…）事后可查 | 预演时无逐阶段淘汰解释 |

### 5.2 核心设计决策

1. **请求体直喂**：新端点接受"原始请求体 + 入站协议"→ 复用 `autopilot_request_profile.go` 自动提特征 → 进现有 dry-run 管线 → 返回 `RoutingPlan` + **逐阶段淘汰原因列表**（每个被过滤候选给出 stage 与 reason）。不改 dry-run 内核，只加入口层。
2. **两面对齐**：预演响应附带底层 scheduler 视角（复用 `DiagnoseSchedulerSelection` 逻辑），一次调用看全两层。
3. **UI**：AutopilotView `DiagnosePanel` 升级——粘贴真实请求体即可预演，替代手工填布尔。对调试场景预设 / effort 口径（`X-Routing-Scenario` 头生效链）直接省时间。

### 5.3 边界与保守策略

1. 预演端点走管理鉴权，永不发上游请求（与 dry-run 同保证）。
2. 请求体仅内存态用于特征提取，不落 trace（避免大 body 持久化）。

## 6. Tier-2 backlog（待排期，仅记方向）

- **管理面 MCP 化**：REST 管理面已全，加一层 MCP server（渠道/配额/trace/健康查询 + 受控写操作），scope 鉴权 + 调用审计（蓝本 `open-sse/mcp-server/`）。价值：用 Claude Code 直接运维 CCX。
- **韧性错误分类补课**：错误先分类再决定作用域——401/403/429 属 key/模型级，**永不 trip 渠道级熔断**（核对 `failover.go` 与 `channel_metrics_circuit.go` 的现行映射）；模型级熔断引入"成功减半衰减"；客户端 abort 等本地流错误不计熔断（蓝本 `src/lib/resilience/failureClassification.ts`、`docs/architecture/RESILIENCE_GUIDE.md`）。
- **context-relay 会话接力**：换渠道/上下文近满时生成结构化交接摘要（summary + keyDecisions + taskProgress）落库、下一跳注入，解决会话亲和失效时"粘不住"的连续性（蓝本 `open-sse/services/contextHandoff.ts`；CCX 锚点 `session/trace_affinity.go`、`conversation/tracker.go`）。
- **doctor CLI**：薄 CLI 复用现有 handler——`doctor`（config/db/端口/运行时分级检查，`--json`，非零退出）、`channels test-all`（蓝本 `bin/cli/commands/doctor.mjs`）。

## 7. Tier-3 与不跟进决策记录

| 项 | 决策 | 理由 |
|---|---|---|
| 路由决策矩阵测试 | **跟进**（低优先） | 调度器决策做成确定性矩阵回归（蓝本 `test:combo:matrix`），CCX 场景预设×effort 组合适合 |
| 免费额度目录 | **不跟进** | CCX 是付费订阅场景；其"研究产物代码化 + CI 锁口径"做法已由 benchmark 注册表承担 |
| TLS 指纹伪装 / CLI 指纹仿真 / WAF 对抗 | **不跟进** | ToS 灰色地带；CCX 走正规订阅 + newapi 通道，引入只增加封号与维护风险 |
| 352 provider 广度 | **不对标** | provider 模板 + newapi 代理通道 + compat-cache 自学习已覆盖长尾 |
| Electron 桌面端 | **不跟进** | Wails + MSIX 方案更轻，已上线 |
| 多语言 UI / 单体 Dashboard 架构 | **不跟进** | 自用场景无诉求；Go 前后端分离 + 嵌入交付更干净 |
| 多租户 / 团队 fair-share | **暂缓** | 单管理员口令模型（`middleware/auth.go`）够用；若开放团队共享再评估 |

## 8. 与现有设计的关系

- **TTFB 拥挤度（`autopilot.md` §5.1）**：方向 A 的 quotaHeadroom 因子与拥挤度共用采集管道、各管一个维度（快慢 vs 余量），评分处合流。
- **public-key-routing（`public-key-routing.md`）**：Key 级零成本机会性消耗是配额调度的特例（CostTierFree 档），方向 A 落地后应吸收其语义而非并存两套。
- **compat-cache（`tool-call-capability.md`）**：方向 C 的 guardrail 链与 compat 学习互不干扰——前者改写内容，后者影响选路。
- **场景预设（`autopilot/scenario_preset.go`）**：方向 B 的开关层级以场景预设为一档。

## 9. 落地顺序与验证要求

| 序 | 项 | 理由 | 验证基线 |
|---|---|---|---|
| 1 | C. credential-masker ✅ | 成本最低，兑现脱敏红线（已落地） | 表驱动单测（掩码命中/不误杀/超限跳过）+ trace 抽查 |
| 2 | D. route preview | 纯增量入口层，调试效率立现 | 预演结果与真实路由一致性对拍（dry-run vs trace） |
| 3 | A. 配额分级调度 | 调度体验最大增量，与拥挤度合流 | 单测（桶懒重置/真相分级/降级排序）+ 饱和场景集成测试 |
| 4 | B. RTK 压缩 ✅ | 收益直观但改动面大，放后（已落地） | 保真门单测 + 真实 CC 会话压缩率抽样 |
| 5 | Tier-2 各项 | 按痛感排期 | 各自拆 spec 时定 |

统一要求：各项实现时 `cd backend-go && make test` + `go build ./...`；涉及前端时 `cd frontend && bun run build`。
