# 渠道模型清单一致性与发现链路 设计文档

> 范围:渠道「上游协议与模型」展示、自动发现(AutoDiscovery)、Key 级模型徽章三条链路的口径梳理与一致性改造。
> 状态:P0/P1 已落地(2026-08-28),P2 画像归一随 channel-data-model-v2 推进。
> 证据来源:`backend-go/.config/autopilot.db` 画像实测、`backend-go/logs/app.log` 发现日志、相关代码锚点见 §7。

## 1. 背景与问题现象

2026-08-28 用户反馈:火山方舟(官方渠道)的可用模型展示自相矛盾,同一页面出现三组互不一致的数字。

渠道编辑页「上游协议与模型」区块按协议分区展示:

| 协议分区 | 模型总数 | 发现时间 | 来源标签 | 每 Key 覆盖 |
|---|---|---|---|---|
| Messages(Claude) | 20 个 | 2026-08-28 19:09(当天) | 火山管控面实时清单 / Coding Plan 模型清单 | 14/20、14/20、20/20、20/20 |
| Chat Completions | **24 个** | **2026-07-24 23:43(35 天前)** | 火山管控面实时清单 / Agent Plan 模型清单 | 17/24、17/24、23/24、23/24 |
| Responses(Codex) | 13 个 | 「历史记录未保存,请重新触发发现」 | 来源未知 | 11/13、11/13、12/13、12/13 |

而同页「认证管理」区块 4 个 API Key 的徽章只显示两种数字:`MODELS 200 (14 个)` ×2、`MODELS 200 (20 个)` ×2。

用户的困惑点:「有的 key 在协议那里还显示有 24 个模型,实际上只有 14 或者 20 两种」——即 Chat 分区的 24/17/23 这组数字在当前现实中已不存在,但 UI 没有任何迹象表明它是 35 天前的旧快照。

## 2. 现状数据链路总览

### 2.1 三条独立的「模型数」产生链路

**链路 A:后台自动发现(画像,权威数据源)**

`AutoDiscoveryRunner` 按**单个协议上游渠道**(`channelUID`,非逻辑渠道)逐端点 `(baseURL × key)` 发现:

- 触发方式:手动「重新发现」`POST /api/{kind}/channels/{id}/auto-discover`(幂等,409 表示进行中)、重启续传 `ResumeIncompleteDiscoveries`(checkpoint 跳过已持久化端点)、7 天 TTL 定期刷新 `StartModelRefreshLoop`(每 6h 扫描,`e2883de2`,**2026-08-28 才合入**)。
- 取数:`ProviderID=="volcengine"` 走管控面签名 API `DetectPlan`+`FetchModels`(source=`control_plane`,消息为「火山管控面 XX 模型清单」);其他渠道优先内置清单,否则 `GET /v1/models`(source=`builtin_manifest`/`models_api`/`builtin_fallback`)。
- 跨协议探测(仅 `AutoManaged`):原生协议直接采信 `/models` 清单(`ensureConfiguredProtocolDiscovery`);非原生协议逐模型 POST 真实探测(每协议单轮 ≤30 候选、3 并发、8s 超时,source=`protocol_model_probe`);同 `CapabilityUID` 本周期只探一次,其余 key 复用结论(source=`capability_ledger_reuse`)。

**链路 B:协议视图聚合(`GET /api/accounts`)**

`managedProtocolAvailabilityViews` 对 messages/chat/responses/gemini 四个协议分别聚合本渠道全部画像:`ProtocolModels[协议]` 取**并集**;时间戳取所有画像的**最大值**;来源单一则直取、多来源则 `mixed`;消息取时间戳最新那条。协议条目缺失时的回退:若画像的 `ServiceType` 正好对应该协议,则用 key 级 `AvailableModels`+`ModelsDiscoveredAt` 充当该协议视图(`managedProtocolProfileInventory`)。

**链路 C:Key 徽章(「认证管理」`MODELS 200 (N 个)`)**

前端编辑器对**每个 key 现场**调 `POST /api/{kind}/channels/{id}/models`(后端代理上游 `/v1/models`,多入口渠道按 key 绑定的 baseURL),`N=返回条数`。`200` 是前端**硬编码**(`useTargetModelFetch.ts:164`),并非透传上游状态码;失败时才显示真实 `ApiError.status`。该结果不进画像、无时间戳,与链路 A/B 完全独立。

### 2.2 存储与数据结构

画像落盘于 SQLite `.config/autopilot.db`(实际运行目录 `backend-go/.config/`),表 `autopilot_endpoint_profiles`,粒度为 endpoint:`sha256(channelUID + canonicalBaseURL + keyHash)`。

`KeyEndpointProfile` 与本文相关的字段:

- key 级:`AvailableModels`、`ModelsDiscoveredAt`、`ModelDiscoverySource`、`ModelDiscoveryMessage`
- 协议级(四张平行 map,**各自独立带时间戳与来源**):`ProtocolModels`、`ProtocolDiscoveredAt`、`ProtocolDiscoverySource`、`ProtocolDiscoveryMessage`、`ProtocolDiscoveryError`

写画像 `writeProfileForEndpoint` 每轮**整体覆盖**协议级 map;写前兜底调 `ensureConfiguredProtocolDiscovery` 刷新原生协议条目(时间戳取 key 级 `ModelsDiscoveredAt`)——这意味着一次只更新 key 级清单的发现也会顺带刷新原生协议视图,但**不会**触碰非原生协议的旧数据。

发现任务记录落盘 `autopilot_discovery_tasks`(done/failed 超 24h 被 GC,running 不删),供重启续传 checkpoint 使用。

### 2.3 前端展示映射

`ProtocolModelAvailability.vue` 消费 `ChannelProtocolRoute[]`(由 `buildNativeProtocolModelRoutes` 折叠成上游原生协议维度):

- 「N 个模型」徽章 = `discoveredModels ∪ 各 binding.models` 去重计数
- 「模型清单发现于」= `modelsDiscoveredAt`;缺失/非法 → 「历史记录未保存,请重新触发发现」
- 来源标签:`control_plane/models_api/builtin_manifest/builtin_fallback/protocol_model_probe/mixed`,空 → 「来源未知」
- 分组(仅 bindings ≥2 时):按「各 key 是否可用」的布尔签名归组 → 「N 个 Key 共同可用」「仅以下 N 个 Key 可用」
- 底部每 key `x/y 可用` = `len(binding.models)/len(并集)`

## 3. 火山渠道案例复盘(2026-08-28 实测)

### 3.1 渠道结构

涉事渠道为逻辑渠道 `lc_d4c5365e436379de1e96e89b`(账号 `acct_d112729d98db`,provider `volcengine`),下挂 **3 条独立的协议上游渠道**,各有独立 `channelUID`、独立发现任务、独立画像组:

| 协议 | 上游 channelUID | baseURL | serviceType |
|---|---|---|---|
| messages | `ch_4303c24f77ba` | `https://ark.cn-beijing.volces.com/api/coding` | claude |
| chat | `ch_bf8bd3b2fba1` | `https://ark.cn-beijing.volces.com/api/coding/v3` | openai |
| responses | `ch_88e07eaedbc2` | `https://ark.cn-beijing.volces.com/api/coding/v3` | responses |

4 个推理 Key 通过 `apiKeyConfigs[].baseUrl` 分绑两个 Plan 入口(`BoundBaseURLForKey`):`***42fd`/`***084e` → `/api/coding`(**Coding Plan**),`***c570`/`***d8db` → `/api/plan`(**Agent Plan**)。每个协议上游因此各有 4 个端点画像。

### 3.2 画像实况与三个数字的来源

`autopilot_endpoint_profiles` 实测(2026-08-28 19:49 查询):

| 上游 | key | ModelsDiscoveredAt | key 级模型数 | 来源消息 |
|---|---|---|---|---|
| messages | ***42fd / ***084e | 2026-08-28 19:09 | 14 | 火山管控面 Coding Plan 模型清单 |
| messages | ***c570 / ***d8db | 2026-08-28 19:09 | 20 | 火山管控面 Agent Plan 模型清单 |
| chat | ***42fd / ***084e | **2026-07-24 23:43** | 17 | 火山管控面 Coding Plan 模型清单 |
| chat | ***c570 / ***d8db | **2026-07-24 23:43** | 23 | 火山管控面 Agent Plan 模型清单 |
| responses | 全部 4 key | **空(旧版本画像,字段后加)** | 11/11/12/12 | 空 |

对照界面数字,全部吻合:

- **Messages = 20**:并集 14∪20(Coding 清单是 Agent 清单的子集);per-key 14/20、20/20。与 Key 徽章 14/20 完全一致——这是唯一与现实相符的分区。
- **Chat = 24**:7/24 快照的并集 17∪23。彼时两个套餐合计比现在多 4 个模型(截图可见 `DOUBAO-SEEDANCE-1.5-PRO`、`DOUBAO-SEED-CODE`、`DOUBAO-SEED-2.0-PRO`、`KIMI-K2.6` 等;火山 8/18 下线 kimi-k2.6 在 `auto_discovery.go:176` 注释中有记录)。这 24 个里任何 key 现在都不拥有。
- **Responses = 13**:11∪12,写入版本早于 `ModelsDiscoveredAt`/`ModelDiscoverySource` 字段引入,故前端显示「历史记录未保存/来源未知」。

Key 徽章 14/20 对火山渠道同样走管控面套餐接口(`GetChannelModels` 的火山分支,`handlers/messages/channels.go:459-473`:数据面 `/models` 不反映套餐清单,改走 `FetchVolcenginePlanModelsForChannel`),所以它与 Messages 分区数字一致是**同口径的必然**,只是它实时拉取、不设缓存,而协议分区依赖画像刷新。

### 3.3 时间线还原

- **2026-07-24 23:43**:对 chat 上游 `ch_bf8bd3b2fba1` 的最后一次发现(任务记录已被 24h GC 清除)。此后 35 天无任何机制再触碰它。
- **2026-08-18**:火山下线 kimi-k2.6 等模型(见 `auto_discovery.go:176` 注释),chat 快照从此与现实偏离。
- **2026-08-28 19:09:13**:用户在编辑页点「重新发现」。日志实证:`[AutoManaged-Discover] 重新触发发现: kind=messages id=24 uid=ch_4303c24f77ba`——**只触发了 messages 一条上游**;4/4 端点 28 秒内完成,chat/responses 上游不在任务范围内。
- **2026-08-28 19:17**:7 天 TTL 定期刷新(`e2883de2`)才首次合入;服务 19:43 重启后,6h 扫描的首次 tick 尚未到达,且 dev 环境 `air` 热重载频繁重启,该 loop 实际从未运行过(全部日志无 `[AutoDiscovery-ModelRefresh]` 记录)。

「重新发现」按钮的范围由前端决定:`primaryDiscoveryRoute` 只取 `routes` 中**第一条** `configured !== false && index >= 0` 的路由(即 messages 上游),`autoDiscoverChannel(route.kind, route.channelUid)` 仅对该 `channelUID` 发起(`ProtocolModelAvailability.vue:298-351`)。

## 4. 根因清单

- **R1「重新发现」范围 = 单条协议上游**。按钮只触发第一条已配置路由的 `channelUID`;同一逻辑渠道的 chat/responses/gemini 兄弟上游永等不到手动刷新。这是「Messages 是新的、Chat 是 35 天前的」的直接原因。
- **R2 协议视图时间戳 = max 聚合,无陈旧度标识**。`managedModelAvailabilityDetails` 取所有画像的最大时间戳当视图时间,35 天前的数据与当天的数据在 UI 上无任何区分;超过 TTL 也不提示。
- **R3 旧画像缺时间戳/来源字段,无迁移补写**。字段是后加的,存量画像只能等下一次发现覆盖;而 R1 使非主协议上游永远等不到 → 「历史记录未保存/来源未知」长期存在。
- **R4 自动刷新机制刚刚才存在且难以生效**。7 天 TTL 刷新 2026-08-28 19:17 才合入;6h 定时首 tick + dev 热重载频繁重启,loop 实际从未跑过;`TriggerDiscoveryWithStatus` 返回 `started=false` 时无日志,跳过原因不可观测。在此之前,任何渠道的非主协议清单都**永久滞留**。
- **R5 三种口径并存且无标注**。管控面套餐清单(权益口径)、`/v1/models` 即时拉取(运行时口径,Key 徽章)、逐模型 POST 探测(实测口径)是三个不同语义,数字不同属预期,但 UI 没有任何解释,用户自然认为「很乱」。
- **R6(次要)Key 徽章的 200 是前端硬编码**,不是上游真实状态码,失败时才显示真码,展示口径具有误导性。
- **R7(次要)同一物理端点画像重复且口径不一**。messages 上游做跨协议探测时会写 `ProtocolModels[chat]`(POST 实测口径),chat 上游自身画像是 `/v3/models` 清单口径;两组数据按各自 `channelUID` 隔离,互不知晓,可能互相矛盾。
- **R8 可见模型清单含非对话模型与别名噪音**。火山管控面返回的是套餐**权益清单**(含 embedding/视频/绘图模型与 `-LATEST` 滚动别名),`discoverVolcenginePlanEndpoint` 原样采信(`auto_discovery.go:940`),不经过 `filterExcludedDiscoveryModels`;内置清单里策划好的纯对话模型(Coding 7 个/Agent 11 个)与 `ExcludeModelPatterns` 机制(mimo 已在用)对这条路径均不生效。后果不止展示噪音:非对话模型进入画像后会成为对话协议的调度候选,调用必失败、浪费重试;别名与具体版本并存使计数虚高。

## 5. 改进方案设计

**设计原则:清单新鲜度是系统的责任,不是用户的。** 用户不需要理解「陈旧度」、不需要盯着时间戳、不需要手动补救——过期数据由系统沿两条路径自动收敛;UI 只在数字口径必然不同时提供可解释性(tooltip),不提供任何需要用户决策的告警。

### 5.1 P0:自动保鲜,用户无感

**P0-1 「重新发现」按整组触发(修 R1)**

用户主动点按钮时,语义必须是「刷新这个渠道的全部协议」:

- 后端:`auto-discover` 发现指定 `channelUID` 属于某逻辑渠道时,对兄弟协议上游幂等联动(`TriggerDiscoveryWithStatus` 本身幂等,进行中返回 409 不算错误);或新增聚合端点 `POST /api/logical-channels/{uid}/auto-discover`。
- 前端:`rediscoverAll` 对 `routes` 中所有 `configured !== false` 的上游并发触发并逐个轮询,全部结束后统一 `emit('refreshed')`。

**P0-2 后台自动保鲜(修 R2/R4,主路径)**

让 7 天 TTL 刷新循环真正生效,而不是依赖用户发现异常:

- 首 tick 从「启动后 6 小时」提前到「启动后 ~5 分钟」,之后按 6h 周期;dev/频繁重启环境下也能覆盖。
- `TriggerDiscoveryWithStatus` 返回 `started=false` 或 err 时打日志(含跳过原因),消除不可观测的静默跳过。
- 分层 TTL:廉价清单源(`control_plane`/`models_api`,一次管控面/GET 调用)24h;昂贵实测源(`protocol_model_probe`,逐模型 POST)保持 7 天。火山这类套餐上下线频繁的场景,清单层至多滞后 1 天。

**P0-3 查看时静默自愈(修 R2/R3,兜底路径)**

后台 loop 管不到的(非 AutoManaged 渠道、长期未重启后的首次查看、TTL 边界)由「查看触发」兜底:

- 前端打开渠道编辑页时,逐协议路由检查:超过对应 TTL 或时间戳缺失(旧画像)→ 后台**静默**对该上游 `auto-discover`(复用 409 幂等),完成后就地刷新区块。
- 防探测风暴:每上游 1 小时冷却(前端会话级 + 后端任务幂等双重保障);仅处理当前查看的渠道,不扩散。
- UI 语义:不显示「过期/陈旧」告警,只在刷新进行时在区块右上角显示轻量「模型清单更新中…」;刷新失败**静默保留旧数据**,下次查看再试。
- 「历史记录未保存,请重新触发发现」文案退役:缺时间戳即等于触发静默刷新,刷新期间显示「清单待更新,正在自动更新…」,用户永远不需要「重新触发」。

**P0-4 存量收敛(修 R3)**

无需迁移脚本、无需用户操作:缺时间戳的画像在 `channelModelsStale` 中已计入过期,经 P0-2 自动覆盖;用户查看时经 P0-3 立即收敛。两条路径保证「来源未知/历史未保存」是过渡态而不是永久态。

**P0-5 发现层能力过滤(修 R8,数据修正)**

让进入画像的清单本身就是干净的,展示和调度同时受益:

- 火山管控面路径与 `probeEndpoint` 对齐:写结果前应用 `filterExcludedDiscoveryModels`;火山 manifest 补 `excludeModelPatterns`,剔除明确的非对话模型(`embedding`、`seedance`、`seedream`、`tts`、`asr` 等命名族)。
- 剔除动作记入 `ModelDiscoveryMessage`(如「已过滤 4 个非对话模型」),保持可观测;被滤模型仍可从 `manifest_drift` 事件追溯。
- 不过滤别名:别名是真实可调用的模型 ID,数据层保留,归类是展示层的事(见 P1)。

### 5.2 P1:口径可解释与展示净化(不增加用户负担)

数字差异本身不消除(三种口径各有语义),但要让「为什么不一样」有处可查,且全部是被动式展示,不主动打扰:

- Key 徽章改用上游真实状态码(后端 models 代理接口透传),tooltip 标注「实时拉取上游 /models」。
- 来源标签 tooltip 释义:`control_plane`=套餐权益清单(可能多于端点实时可见)、`models_api`=端点实时清单、`protocol_model_probe`=逐模型实测。
- 别名归类展示(修 R8 的展示侧):识别 `-latest`、`-evolving`、`ark-code-latest` 这类滚动别名,在协议分区中折叠为「别名」徽标附着在目标家族下(如 `KIMI-LATEST → kimi 家族`),计数仍计入总数但视觉上不再与具体版本平铺;旧画像(未经 P0-5 过滤)中残留的非对话模型同样在前端折叠隐藏,作为数据修正生效前的兜底。

### 5.3 P2:结构收敛

**P2-1 同站多协议画像归一(修 R7)**:以 `identityBaseURL + keyHash` 为纽带,消除「messages 上游跨协议探测结果」与「chat 上游自身画像」双口径并存;方向与 `channel-data-model-v2.md` 一致,随 v2 推进落地。

### 5.4 设计权衡与边界

- **探测成本**:清单源(control_plane/models_api)是单次只读调用,24h TTL 代价可忽略;实测源逐模型 POST 会真实消耗上游额度(单模型 max_tokens=64,单轮单协议 ≤30 模型,同 CapabilityUID 去重后每 Plan 只跑一遍),故保持 7 天 TTL,不为「看起来更新鲜」付出额度成本。
- **过滤用排除式而非白名单式**:P0-5 只剔除「确定不是对话模型」的命名族,不做与内置清单的交集——否则 `KIMI-K3` 这类管控面新上线、内置清单尚未收录的对话模型会被误杀。新对话模型的正规回流通道是 `manifest_drift` 事件 → 人工/自动更新内置清单(自动回填目前未实现,见 `README.md` 待排期);过滤后 drift 对比应基于过滤前清单,避免永久性 drift 噪音。
- **覆盖范围**:P0-2 后台 loop 维持只扫 `AutoManaged` 渠道(非托管渠道不做跨协议实测,自动刷新的信息增量小);非托管渠道由 P0-3 查看触发兜底,仅刷清单层,无探测风暴风险。
- **失败语义**:刷新失败一律静默保留旧数据——过期数据优于无数据;失败原因进日志和 `ProtocolDiscoveryError`,不上 UI。
- **桌面端**:`desktop/frontend` 的认证面板是平行实现,P0-3 的查看触发需在桌面端对齐,或后续收敛为后端统一触发点。

## 6. 验收标准

以「用户全程零操作」为衡量基准:

- **自动收敛**:服务连续运行 5 分钟后,存量超过 TTL 的协议清单被后台自动刷新(日志可见 `[AutoDiscovery-ModelRefresh]`,含每个渠道的触发/跳过原因);dev 热重载频繁重启场景下同样成立。
- **查看即新鲜**:打开任一渠道编辑页,若某协议数据超 TTL 或缺时间戳,页面在数秒内自动完成刷新并就地更新,全程无告警、无需点击;1h 内重复打开不重复发起探测。
- **手动语义正确**:点一次「重新发现」,该逻辑渠道下所有协议分区时间戳一致(误差在发现耗时内)。
- **无滞留文案**:「历史记录未保存,请重新触发发现」「来源未知」只可能作为刷新进行中的过渡态短暂出现,不会长期滞留。
- **清单净化**:Messages/Chat/Responses 分区不再出现 embedding、seedance、seedream 等非对话模型(以火山渠道实测验证);滚动别名折叠归类,计数口径 tooltip 可解释;`KIMI-K3` 这类新对话模型不受影响、正常出现。
- **口径可解释**:Key 徽章显示上游真实状态码;徽章与协议分区数字不一致时,tooltip 能说明口径差异。
- **回归**:`cd backend-go && make test`;`cd frontend && bun run build` 及 `ProtocolModelAvailability.test.ts` 同步更新。

## 7. 关键代码锚点

| 主题 | 位置 |
|---|---|
| 发现触发/任务状态 API | `backend-go/internal/autopilot/handlers_auto_managed.go:2885-2980` |
| 发现主流程/续传/7 天 TTL 刷新 | `backend-go/internal/autopilot/auto_discovery.go:140-300,394,447-538` |
| checkpoint 跳过逻辑 | `backend-go/internal/autopilot/auto_discovery.go:673-816` |
| 火山管控面发现(DetectPlan/FetchModels) | `backend-go/internal/autopilot/auto_discovery.go:889-946`、`volcengine_coding_plan.go` |
| 清单排除过滤机制(ExcludeModelPatterns) | `backend-go/internal/config/builtin_models_manifest.go:32-33,286`、`auto_discovery.go:1066-1072,1175-1207` |
| 火山内置策划清单(纯对话模型) | `shared/builtin-models-manifest/builtin-models-manifest.json`(planHint=volcengine_*) |
| 跨协议逐模型探测/ledger 去重 | `backend-go/internal/autopilot/protocol_discovery.go:89-308` |
| 原生协议采信兜底 | `backend-go/internal/autopilot/protocol_discovery.go:337-361`、`auto_discovery.go:1405` |
| 画像写入(整体覆盖协议 map) | `backend-go/internal/autopilot/auto_discovery.go:1297-1472` |
| 协议视图聚合(并集/max 时间戳/mixed) | `backend-go/internal/autopilot/handlers_auto_managed.go:684-893` |
| 协议条目缺失回退 | `backend-go/internal/autopilot/handlers_auto_managed.go:856-888` |
| 定期刷新接线 | `backend-go/main.go:1509-1518` |
| 「重新发现」按钮范围 | `frontend/src/components/edit-channel/ProtocolModelAvailability.vue:298-351` |
| 协议区块渲染/分组/来源标签 | `frontend/src/components/edit-channel/ProtocolModelAvailability.vue:383-517` |
| Key 徽章(硬编码 200) | `frontend/src/composables/useTargetModelFetch.ts:124-173`、`frontend/src/components/edit-channel/ApiKeyManagementSection.vue:262-298` |
| 路由折叠/legacy 兜底 | `frontend/src/utils/channelModelAvailability.ts:115-198` |
| 文案 | `frontend/src/locales/zh-CN.json:776-818,382` |
