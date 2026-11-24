# 渠道配置与模型能力

> 状态：现行规格
> 代码基线：`53b79829`

## 1. 目标与范围

渠道是某一协议入口下的上游连接及调度单元。本文定义渠道配置、模型映射、能力元数据、瞬态渠道发现和模型连通性测试的现行契约。

- 六类渠道分别保存在 `upstream`、`chatUpstream`、`responsesUpstream`、`geminiUpstream`、`imagesUpstream`、`vectorsUpstream`；管理路径统一为 `/api/{type}/channels/*`。
- `channelUid` 是历史观测和子代理策略使用的稳定身份；数组 `index` 只用于当前管理操作，重排、改名和换 Key 不得改变 UID。
- `serviceType` 描述上游协议，`channelKind` 描述 CCX 对外入口，两者可以不同；不得仅凭模型名称猜测协议。
- 渠道发现使用表单草稿和临时配置，不直接保存渠道；连通性测试使用已保存渠道，结果不构成对所有上游能力的永久保证。

非目标：不自动对未知上游启用智能评分，不为 Images/Vectors 提供文本能力测试，不用模型注册表代替真实请求验收。

## 2. 配置与界面契约

Web/Desktop 的渠道列表按协议分组；新增、复制和编辑使用同一配置语义。核心字段如下：

| 字段组     | 契约                                                                                                                     |
| ---------- | ------------------------------------------------------------------------------------------------------------------------ |
| 身份与连接 | `channelUid` 稳定；`name` 可改；`baseUrl`/`baseUrls` 支持单地址与故障转移地址；`apiKeys` 支持多 Key 轮换。               |
| 协议与认证 | `serviceType` 必须与上游匹配；`authHeader` 支持自动、Bearer、`x-api-key`；上游认证与 CCX 管理/代理密钥分离。             |
| 网络       | `proxyUrl` 支持当前 HTTP/HTTPS/SOCKS5 客户端；`insecureSkipVerify` 仅显式开启；`customHeaders` 最后应用到上游请求。      |
| 模型       | `supportedModels` 过滤客户端请求模型；`modelMapping` 改写出站模型；`reasoningMapping` 控制推理档位；能力按实际模型解析。 |
| 调度与保护 | `priority`、`status`、`promotionUntil`、限速、超时、熔断开关和 Key 配置见调度规格。                                      |
| 兼容选项   | Claude/Gemini/Responses/Chat/Images 各自开关默认关闭，只有明确需要时启用；不得由 UI 静默全量开启。                       |

- 表单中的 Base URL、Key、模型和兼容开关先形成草稿，点击保存后才进入 `config.json`；发现配置的“应用推荐”也只修改草稿。
- 新增或复制渠道生成新 UID；普通列表拖拽修改顺序，不改变 UID。删除渠道同时清理其子代理引用，历史观测仍保留调用时名称。
- Web/Desktop 必须共享后端字段校验和 API 类型；前端预览模型时复用精确优先、最长包含、同长度字典序的规则，不另造匹配语义。
- 管理接口返回渠道视图时不得暴露完整上游 Key；真实 Key 只在新增/编辑所需的最小范围内处理。
- Vectors 的 `serviceType` 固定为 OpenAI 兼容；没有默认模型候选，模型可拉取或手工输入。Embedding 配置键匹配映射后的实际模型。
- 编辑渠道的 GLM 一键模型重定向模板按入口分层：Messages 入口将 `fable`/`opus` 映射到 `glm-5.4`，将 `haiku`/`sonnet` 映射到 `glm-5.3-flash`，推理档位分别为 `max`/`max`/`high`/`max`；Responses 入口将 `astra`/`codex-auto-review`/`luna`/`sol`/`terra` 映射到 `glm-5.4`。应用模板只更新当前表单草稿，不改写已保存渠道，其他厂商模板保持原映射。
- Messages 的 DeepSeek 模板将四个源模型映射到 `deepseek-flash`，并将 `haiku` 推理档位设为 `high`、其余源模型设为 `max`；Kimi 模板将 `haiku` 映射到 `kimi-for-coding`，其余源模型映射到 `k3.1-pro`，其中 `haiku`/`sonnet` 推理档位为 `high`，`fable`/`opus` 为 `max`。Responses 模板保持原映射与推理配置。

## 3. 模型解析与能力判定

### 映射与白名单

- `modelMapping` 精确匹配优先；否则按源字符串 UTF-8 字节长度降序执行包含匹配，同长度按字典序升序。单次映射不递归，未命中保持原名。
- 命中但目标与原名相同记为 `matched_unchanged`，不得误报为改名；观测同时保留请求模型、本轮有效模型、映射模型和最终出站模型。
- `supportedModels` 为空表示全部允许；支持精确、`prefix*`、`*suffix`、`*contains*` 和 `!` 排除规则。排除优先；只有排除规则时其余模型允许。
- 白名单匹配客户端/有效请求模型，能力和 Embedding 元数据主要匹配映射后的实际模型。UI 展示必须区分这两层。

### 能力来源与路由

实际模型能力按以下顺序解析：渠道 `modelCapabilities` → 全局 `upstreamModelCapabilities` → 内置注册表 → 渠道 `defaultCapability` → unknown。渠道/全局先匹配实际模型，也保留对原始模型的兼容匹配；内置注册表支持当前的大小写折叠解析。

- 上下文路由启用时，按请求内容、输出预算及下游 Agent profile 计算最低窗口；估算值和 Agent 最低要求取较大者。
- 显式输出上限超过实际模型 `maxOutputTokens` 时过滤渠道；`compaction_trigger` 可跳过原始窗口校验，但仍校验输出上限。
- 未知模型默认只承载不超过 `unknownSafeWindowTokens` 的请求；`allowUnknownContext=true` 才允许大上下文落到未知渠道。
- 图片请求按 `noVision`、`noVisionModels` 和 `visionFallbackModel` 处理；能力过滤只决定候选资格，不改变用户配置的普通顺序。
- 候选中存在 `embeddingCapabilities` 时启用严格 Embedding 过滤：`embeddingSpaceId`（缺省取实际模型名）、有效维度及 `normalized` 语义必须兼容。全部旧配置无元数据时保持原 fallback。

所有解析都使用请求级配置副本；不得把角色模型、映射结果或视觉回退写回持久配置。

GLM-5.4 的内置能力先验覆盖 `glm-5.4`、日期后缀及 `[1m]` 长上下文形式：上下文窗口为 1048576 tokens，支持视觉输入和 `low`/`high`/`max` 推理档位。最大输出暂沿用 GLM-5.3 的 131072 tokens，待官方模型页确认；标准 API 定价尚未发布，不得伪造 `pricing`。该条目描述 Coding Plan 能力，不代表已完成标准 API 或真实上游验收。

## 4. 渠道发现与连通性测试

### 瞬态渠道发现

`POST /api/channel-discovery` 仅支持 `messages`、`responses`、`chat`、`gemini`。请求至少包含合法 serviceType、一个 Base URL 和一个 Key，可选 `channelKind`、`targetClients`、代理、TLS、自定义头及当前映射。

运行顺序为：读取 `/models`（失败时使用内置探测候选并给 warning）→ 选择少量 strong/primary/fast 候选 → 实测协议 → 推荐目标渠道 → 生成映射和 reasoning 建议 → 兼容、工具、视觉及 thinking 探测 → 返回证据。

- 推荐只使用实测成功模型。Responses/Chat 当前稳定源别名为 `astra`、`sol`、`terra`、`luna`、`codex-auto-review`；Messages 使用 `opus`、`sonnet`、`haiku`、`fable`。
- 发现结果不生成 `supportedModels` 白名单，避免把有限探测结果变成长期流量限制；应用时清除旧推荐白名单的当前行为必须保持。
- 结果区位于新增/编辑表单的模型重定向区域，展示推荐入口、模型数、成功协议、映射、reasoning、兼容项、能力证据和 warning。
- 编辑已保存渠道时仍使用当前草稿，不回读旧 channelId 配置。应用推荐不自动保存，也不注册调度器或改变熔断状态。
- Copilot 使用模型级端点路由做文本探测；未实际测试的工具、视觉、thinking 保持未知，不自动开启兼容改写。

### 模型连通性测试

- 管理端点为 `capability-snapshot`、`capability-test`、任务查询/取消和单模型重试；只适用于四类文本渠道。
- 可测目标协议、自定义模型、超时和 RPM；默认单模型超时 30 秒，默认 RPM 30、上限 60。未指定协议时覆盖四文本协议。
- 任务状态为 queued/running/completed/failed/cancelled；模型另有 idle/queued/running/success/failed/skipped，整体 outcome 区分 success/partial/failed/cancelled。
- 支持缓存命中、复用运行中任务、恢复已取消任务和复用上次成功结果；`force` 跳过缓存。测试记录实际映射模型、延迟、流式支持和真实错误。
- Job 与快照是进程内状态，重启后不得宣称仍保留完整任务；测试日志标记 `capability_test`，不进入默认业务模型分布。

## 5. 状态、异常与兼容边界

- 渠道保存必须先完成协议、URL、Key、模型规则、能力、超时和限速校验；写盘失败不得发布半更新的内存配置。
- `/models` 不可用时，发现可以降级到内置候选，但必须显示来源和 warning；协议全部失败时不得生成可应用的自动推荐。
- 鉴权或额度失败应停止不必要的大量探测；部分兼容测试超时保留其他结果并标记 inconclusive/未知。
- 无 Key 的连通性测试返回明确失败任务，不伪造兼容协议；取消只影响当前任务，运行中任务不得被重复执行。
- 模型注册表只提供能力先验和默认候选，真实可用性由请求验证；注册表源 JSON 变更后通过生成脚本同步 Go、Web、Desktop，不手改生成物。
- 旧 `stripCodexClientTools`、旧 `SubagentModel` 等字段仅按迁移兼容逻辑读取；新协议策略激活后不得再被旧写入静默覆盖。
- `rateLimitWindowMinutes` 是保留的 JSON 字段名，当前值按秒解释；`rateLimitBurst` 已废弃，不再作为现行突发配置契约。
- Images/Vectors 没有 capability-test/snapshot 路由；Embedding 兼容性必须通过元数据和真实向量调用验证，不能用文本测试推断。

## 6. 验收标准

| 场景                         | 必须结果                                                                     |
| ---------------------------- | ---------------------------------------------------------------------------- |
| 精确、包含、同名和未命中映射 | 精确优先；最长包含及字典序稳定；单次不递归，结果与实际出站模型一致。         |
| 白名单包含/排除              | 空列表放行，排除优先，分隔输入和通配符按校验规则执行。                       |
| 能力覆盖与未知模型           | 渠道/全局/内置/default/unknown 顺序正确；输出上限和上下文不足时无候选。      |
| 渠道 UID 生命周期            | 新建/复制分配新 UID；改名、重排、换 Key 不变；删除后历史身份仍可读。         |
| 发现不持久化                 | `/models` 失败可降级；推荐只用成功模型；不写配置、不生成白名单、不改变熔断。 |
| 表单应用推荐                 | 新建和编辑都使用当前草稿；应用后只更新草稿，保存失败不发布部分配置。         |
| 连通性任务                   | 自定义/配置模型、取消、重试、缓存和复用状态正确；日志与默认业务统计隔离。    |
| Images/Vectors 边界          | 不出现文本能力测试入口；Embedding 元数据非法时拒绝保存。                     |

Web/Desktop 还需验证长模型名、加载、空态、部分失败、无 Key、保存冲突和窄屏操作； mock 探测通过不等于真实上游验收，真实账号结果应单独记录。

## 7. 实现与测试索引

- 配置字段：`backend-go/internal/config/config.go`。
- 映射与白名单：`config_utils.go`及对应测试。
- 能力与 Embedding：`model_registry.go`、`embedding_capability.go`。
- 渠道发现：`channel_discovery.go`、`channel_discovery_test.go`。
- 连通性测试：`capability_test_handler.go`、`capability_test_jobs.go`、`capability_test_http_test.go`。
- Web 表单与结果：`EditChannelModal.vue`、`CapabilityTestDialog.vue`及组件测试。
- Desktop 对应实现：`desktop/frontend/src/components/`中的渠道编辑、发现和连通性组件。
- 一键模板源：`shared/channel-presets/claude-messages.json`、`shared/channel-presets/codex-responses.json`；生成物由 `scripts/generate-channel-presets.mjs` 同步到 Web、Desktop 与 Desktop Go。
- 模板回归测试：`useEditChannelPresets.test.ts`、`useChannelEditPresets.test.ts`、`desktop/internal/channelpreset/preset_test.go`、`desktop/internal/channelpreset/glm_compat_test.go`；本次仅做本地生成与自动化检查，未执行 DeepSeek、Kimi、GLM 真实上游请求验收。
