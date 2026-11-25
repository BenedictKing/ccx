# 原始 ccx 逐提交移植评审（2026-11-11）

对 `main`（原始 ccx，v3.0.0）自 `v2.9.37` 以来的全部提交逐条判定「是否适合放进当前 lean 项目」，产出可执行的移植清单。

## 一、评审口径

| 项 | 值 |
| --- | --- |
| 源 | `main` = `20d04de2`（v3.0.0，2026-11-04），主工作树 `/Users/petaflops/works/api-gateways/llm-routing/ccx` |
| 范围 | `v2.9.37..main`，共 **1268** 个提交（2026-07-07 ~ 2026-11-04） |
| 目标 | 当前 worktree 分支 `codex/v3-lean-from-2.9.37`（v2.9.37 + 53 个独有提交） |
| 判定依据 | `docs/superpowers/plans/2026-11-05-v3-port-inventory.md`、`2026-11-05-lean-v3-rebuild.md`、`AGENTS.md` |
| 判定类别 | 建议移植 / 条件移植 / 已覆盖 / 不移植 |
| 覆盖方式 | 374 条进入候选域，逐个 `git show --stat` + 重点 diff + lean 现状 Grep 核验；894 条按主题规则批量判定，每条记录理由 |
| 优先级 | P0 协议/数据正确性 > P1 稳定性/failover/配置兼容 > P2 配置与表单契约 > P3 UI/工具链 |

**完整逐条清单见同目录 `2026-11-11-main-commit-port-audit.tsv`**（1268 行，含每条的理由、改动域、评审来源）。

## 二、结论

| 判定 | 条数 | 占比 | 说明 |
| --- | --- | --- | --- |
| 不移植 | 1162 | 91.6% | 3.0 大一统体系、评测/调度、纯文档与生成产物 |
| 条件移植 | 42 | 3.3% | 行为有价值，但需抽函数/重写接入点，不能整笔搬 |
| 已覆盖 | 40 | 3.2% | 9 条 subject 精确对应的移植 + 31 条 lean 已行为等价实现 |
| 建议移植 | 24 | 1.9% | 独立、无 3.0 架构依赖、lean 确认仍是修复前状态 |

三条核心结论：

1. **绝大多数提交本来就不该搬。** 这 4 个月 main 主要在建 3.0 大一统体系：autopilot 289 条、benchmark 84 条、racing 22 条、frontend/ui 151 条（多为统一渠道卡、订阅中心、健康中心/驾驶舱），加上 ChannelsV3 权威持久化、逻辑渠道聚合、SmartRouter 自动选模、配额真相与兼容性自学习。这与 lean「六类协议独立渠道 + 用户显式模型映射」的方向相反，且大量目标文件在 lean 根本不存在。
2. **真正值得搬的是 66 条**（24 建议 + 42 条件），其中 P0 共 19 条，集中在五组：流式语义检测/SSE 尾行、协议转换正确性、配置升级兼容、网关响应头与限流、模型数据。lean 已按移植清单完成 P0 代理正确性与 P4 故障转移的大部分行为，缺口是「后续 8–11 月的同类修复」。
3. **最大的单点缺口是数据而不是提交**：模型注册表 main 有 112 条 `upstreamCapabilities` / 140 个 pattern，lean 只有 62 / 78；62 条共有条目里 42 条的能力位、价格或上下文窗口有差异，含明确数据错误。这部分不能靠 cherry-pick 解决，必须按条目合并后重生成产物。

## 三、第一梯队：建议移植（24 条）

### P0：协议与数据正确性（9 条）

| 组 | 提交 | 修复内容 | 落地方式 |
| --- | --- | --- | --- |
| 流式语义检测 | `d23fbf92` `14d36ae7` `a3090203` | `stream_processor.go` 补 `content_part.*`、`refusal`、`delta.audio` 语义检测，避免正常流被判空响应触发误 failover | 手工套 diff，与 reasoning 归一化组的 knownTypes 改动合并去重 |
| SSE 尾行 / reader 生命周期 | `6d4324b5` `d4064035` `e2de1217` `bd449ddb` | 重复写 remainder、EOF 重发尾行正文、SSE 尾帧丢失、预检早退泄漏 goroutine | **严格按此顺序**；`bd449ddb` 给 `StartBodyChunkReader` 加第 4 参，须同步 chat/gemini/images 全部调用点；忽略 `6d4324b5` 的文件 mode 变更 |
| 配置升级 | `15189bc0` | 旧配置中 priority=0 的渠道在升级后插到显式排序之前 | 只抽 `normalizeChannelPriorities()`，挂在 lean `loadConfig` 六协议数组定型之后（去掉 3.0 的托管账户合并前置依赖），补旧配置加载→保存→重启回归 |
| 模型输出 | `e0c41e75` | `/v1/models` 不输出 `max_output_tokens` | 只取 `models.go` 3 处回填 + 等价测试；内置清单的 `ModelIDs` 是 main 时代口径，若取需按 lean 已注册模型重写并保留 `DisableProbe=false` |

配套回归：`d22b17b2`（flushRemainder 不重复写）、`dc64fc91`（reasoning 归一化/preflight 行为锁定）列在条件移植，建议随组带上。

### P1：稳定性与网关正确性（7 条）

| 提交 | 内容 | 备注 |
| --- | --- | --- |
| `7f5bcaf4` | 删除 `isRetryableResponsesError` 白名单，非黑名单上游错误统一触发预检 failover | lean `fold.go:392`、`stream.go:215` 仍是旧白名单；与 `00396a44` 请求侧终态化不冲突 |
| `4b292c56` | config watcher 跳过自身保存触发的重载（sha256 摘要比对） | 与 lean 的 2.9 迁移链直接相关，避免迁移写盘后热重载二次改写运行时状态 |
| `7b8f39aa` | 非流式响应写回不再透传上游 `Content-Type` | 抽 `ForwardContentType` + skip 项，并走查 lean 全部 `ForwardResponseHeaders` 调用点，对纯字节透传路径补回 |
| `6cda4596` + `d300b2bb` | 带 `#` 的完整 Base URL 路径不被误清理/不被域名根去重 | lean `02e0222c` 已用 `#` 标记精确路径，两处语义直接冲突，**应优先修**；`d300b2bb` 为配套测试 |
| `0a26f665` `10aba5ca` `181e1b6f` | 渠道名称预览卡暗色白底、能力测试 tooltip 暗色覆盖、渠道卡 hover 抖动（删 `transform`） | 纯 CSS；`181e1b6f` 只取 CSS 部分，`providerDisplay.ts` 的 tokenrhythm 品牌名不适用 |

### P2 / P3：前端契约与通用缺陷（8 条）

- `be6ca396` `e9f0aa79`：`dashboardPathPrefixes` 补 `/usage`、`/api-keys`、`/apikeys`，与 `02e0222c` 同文件不同段，可合并为一次改动。
- `9051451c`：删空转路由守卫（两分支一律 `next()`，鉴权实际在 App.vue），零行为风险。
- `eee81e0b`：编辑渠道时按 Key 数量预加载模型列表，恢复 Key 行模型数展示。
- `b8af42d0`：添加渠道弹窗正文可滚动；`4b803e93`：日志复制按钮改逻辑方向定位；`0a5badd4`：音频 content block 丢弃时输出告警日志（**唯一可近似整笔 cherry-pick 的提交**）。

## 四、第二梯队：条件移植（42 条，按主题）

| 主题 | 提交 | 要抽什么 | 主要风险 |
| --- | --- | --- | --- |
| OpenRouter reasoning 归一化（P0） | `b0e89a4d` `b480629e` `e49746c4`（+`dc64fc91` 测试） | 新增纯函数文件，把非标准 `reasoning_text` 归一为 OpenAI `reasoning_summary_*` | lean `fold.go`/`stream.go` 已分叉，接入点须重写；顺序不可颠倒 |
| Codex additional_tools（P0） | `a003d616` `38f2aafc` `80880bac` | `codex_tools.go` 纯函数增强 + 转换路径/流式 remap | 首笔近 500 行改 9 文件，与 3.0 handler 强耦合；按「建能力→补转换→补流式」分组重写 |
| token 估算（P0） | `5c2e468a` `d1156294` | Responses 估算精度、音频按媒体类型而非 base64 字符数 | 剥离 autopilot `RequestProfile` 依赖；图片部分 lean 已覆盖需跳过 |
| 响应透传与统计（P0） | `9d894c47` `a6ad832f` | `ClaudeResponse.ExtraFields` 通用透传；旁路请求记录带 model，消除图表 unknown 归桶 | `a6ad832f` 改公共 `Record*` 签名、波及 14 文件，且 lean `delegates.go`/`channel_metrics_recording.go` 已改过，建议单独批次 |
| providers 兼容性（P1/P2） | `88a6132e` `ee506886` `1c63bbb2` `c71158d8` `c55f9b4e` `170a0325` `905a4192` | CC 注入 system 头/tokens-left 块过滤、cache_control 块边界、缺失 ID 合成 `toolu_`、预算提醒剔除、Compshare 角色归一 | 只抽谓词与调用点，不建 `system_filter.go`/`system_probe.go` 等 3.0 文件；CC 版本口径不同（lean 2.1.209 / main 评估 2.1.251），先复现再合入 |
| 限流与密文参数（P1） | `2a1c065e` `ec6ef1bd` | 多族 reset 头鲁棒解析 + 1 小时上限截断；`encrypted_function_args` 透传、空 arguments 补 `{}`、token 计数 | lean 现有 `cooldownUntil` 无上限，异常 `Retry-After: 86400` 会冻结渠道一整天 |
| 配置/表单契约（P1/P2） | `8f7a7884` `51640b12` `5fd95338` `bd84ef1e` `c2653e61` `c8d834e1` `4a7bd1df` `505e5a36` | key 级 baseUrl、第三方域名剔除 CCH billing header、旧 grok 映射清理、多级路径/端口渠道名、桌面端 baseUrl 前缀、Key payload 不丢未知字段 | lean `APIKeyConfig` 无 `baseUrl`/`keyUid` 等字段；`505e5a36` 与 `606c641b` 是同日互逆的一对，不可只取其一；先确认 lean 是否有 grok 遗留映射，否则收益为零 |
| 状态一致性与诊断（P1/P2） | `10a155c0` `81ec221a` `eb7772a8` `f8a23220` | 恢复渠道时清除 Key 暂停、熔断无日志时给成因、发现候选保序并过滤非 chat 模型、流式无限重复检测 | 后端强耦合 3.0 metrics/SQLite/`model_circuit`；`f8a23220` 只抽检测算法，熔断改接 lean 渠道级开关 |
| 去重与工具链（P2/P3） | `3a08db97` 及 10 条 P3 | 重复 system 块去重、性能优化、图标注册、能力测试 schema 单一真相源、备注 tooltip、协议预期地址 | 多为可选增强；`b47fc0f3` 需按 lean 模型清单重建 schema，`5d4016ba` 需确认 lean 用 `description` 而非 `remark` |

## 五、模型注册表专项（最大单点缺口）

两边真相源同为 `shared/model-registry/ccx_model_registry.json`；lean 的生成产物与源 json 完全一致，**差异来自数据本身，不是产物过期**。

| 项 | main | lean |
| --- | --- | --- |
| `upstreamCapabilities` 条目 | 112 | 62 |
| 展开 pattern | 140 | 78 |
| `/v1/models` `max_output_tokens` | 有 | 缺 |
| 内置清单回退 | 有 | 缺 |

- **完全缺失的模型族**：Gemini 3 全系（7）、Grok 4.x（4）、图像/视频生成 11 个、GPT-5.x 官方变体与日期后缀、Claude `*-5.5` 点版本、`gpt-6.1-terra` / `gpt-6` / `astra` 条目、Kimi `k3`/`k3[1m]`/`k3.1-pro-highspeed`、豆包 seed 2.x、glm-5.3 变体、MiMo V2.6、muse-spark 等。注意 lean 的 `astra/sol/terra/luna` 只是 `a81cba7f` 的 codex 重定向源别名，**不等于注册表模型条目**。
- **隐性漏匹配比缺条目更危险**：lean 用 `claude-opus-5-\d{1,2}`，main 用 `claude-*-5[.-]5`；用户传 `claude-opus-5.5`、`claude-haiku-5.5` 时 lean 漏匹配，回落到 200000 默认窗口。
- **明确数据错误**：`gpt-5.5` 窗口 lean 272000 / main 1050000；`gpt-5.4` 1000000 / 1050000；`kimi-k3` maxOutput 262144 / 131072；`qwen3.8-max` 991808 / 1000000；多个 GPT-5.x 的 displayName 在 lean 还是 `"Codex 272K models"` 占位。62 条共有条目中 42 条缺能力位或 pricing。
- **正确做法**：不 cherry-pick main 的 `chore(data)` 快照提交（它们同时带 benchmark/图表产物，属明确不移植）。把源 json 的 `upstreamCapabilities` 按模型族合并进 lean 源 json，点版本统一 `[.-]` 写法并去重，再跑 `scripts/generate-model-registry.mjs` 重生成 `generated_model_registry.go` 与 `model-registry.ts`，补 `model_registry_test.go` 回归。`benchmarkProfiles`(79) / `imageArenaProfiles`(5) 不搬。

## 六、建议执行顺序

| 批次 | 内容 | 验收 |
| --- | --- | --- |
| 1（P0，先做） | 流式语义检测 3 条 + SSE 尾行组 4 条（按序）+ `7f5bcaf4` + 配套回归用例 | 三协议流式、工具调用、空流/EOF/拒答/音频流、预检错误；SSE 尾帧不重复不截断 |
| 2（P0/P1） | `15189bc0` → `4b292c56` → `7b8f39aa` → `2a1c065e` → `6cda4596`/`d300b2bb` | 2.9 旧配置加载→保存→重启；`go test ./internal/config/... ./internal/utils/... ./internal/ratelimit/...`；Web `bun run type-check` |
| 3（数据） | 模型注册表按条目合并 + `e0c41e75` 的 `max_output_tokens` | 重生成产物一致、点版本/别名匹配回归、`/v1/models` 输出校验 |
| 4（P0 条件组，按组独立） | reasoning 归一化 → additional_tools → token 估算 → `a6ad832f` | 每组先写失败用例，再抽最小行为；OpenRouter/Codex 工具调用实测 |
| 5（P1/P2/P3） | providers 兼容组、配置契约组、通用 UI 小修 | 按 lean 现状重写接入点；Web/Desktop 双端构建 |

**纪律**：不整笔 cherry-pick（`v3-port-inventory.md` 执行纪律第 59 行），按行为抽取 + 补回归；每个批次跑 `cd backend-go && make test`、`cd frontend && bun run build`、Desktop 类型检查；实施在独立 worktree 与隔离配置目录，不要影响 3688 端口运行实例。

## 七、风险与边界

1. **成组顺序不可打乱**：SSE 尾行组、reasoning 归一化组、additional_tools 组均有严格依赖；`bd449ddb` 是函数签名变更，漏改调用点即编译失败。
2. **`6cda4596` 与 `02e0222c` 语义冲突**：都改 `stripDashboardPathFromBaseUrl` 对 `#` 的处理，落地后必须跑 base URL 与渠道创建/编辑回归。
3. **模型数据合并键冲突**：两边对同一模型 pattern 写法不同，直接合并会改变 `resolvePatternValue` 的最长优先匹配，需逐族去重并保留 lean 的 `[1m]`/`-highspeed` 兼容写法。
4. **P3 硬约束**：余额探测只作状态展示（`98c6f8ba` 已由 lean `833cfc13` 覆盖）；不得引入把余额写入 quota、参与调度评分、或据探针失败禁用 Key 的实现。
5. **四个已知 issue 已由 lean 自修**：#300 对比度 `de34ee35`、#236 菜单定位 `b608ff9d`、#265 baseURL `02e0222c`、#302 能力测试假阴性 `36223fe9`；#270 自定义定价 `5944824d`。本清单没有对应的 3.0 提交，不需要重复移植。
6. **lean 缺失模块是硬约束**：`guardrails/`、`metrics/model_circuit.go`、`channel_discovery_fast.go`、`config/channel_authoritative.go`、Desktop `channelApiKeys.ts`、`managedChannelPatch.ts` 等在 lean 不存在，相关提交判不移植是结论而非遗漏。
7. **2026-11「恢复显式模型重定向」整组**（`1350b517` `2b5018a0` `8c3483ba` `1ac12cb8` `537e50ae` `ff767fa1` `20d04de2`）：lean 从基线从未删除显式映射，`ff767fa1` 候选已由 `2801934f` 同步，`20d04de2` 属 ChannelsV3 单卡 patch。整组天然已覆盖 / 不适用，组件接口完全不同，不可 cherry-pick。

## 八、完整逐条清单

`2026-11-11-main-commit-port-audit.tsv`（1268 条，Tab 分隔，UTF-8）字段：

`hash | 日期 | 提交标题 | 判定 | 优先级 | 改动域 | 文件数 | 理由 | 评审来源`

其中「评审来源」为 `ccx_audit_A/B1/B2/C/D.md` 的 374 条是逐条 diff 核验结果；标为「规则分类」的 894 条是按主题/路径规则判定，每条都带具体理由（如 `scope=autopilot：v3 大特性/评估体系，port-inventory 明确暂缓`）。按「判定」列筛选即可直接得到待办清单。
