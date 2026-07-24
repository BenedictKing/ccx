## [Unreleased]

### 新增

- **Autopilot Trace v2 契约与脱敏边界** - 新增 TraceDetailV2/TraceSummary/SchedulerDecisionSummary/EndpointAttemptSummary DTO，ComparisonStatus 三态枚举（matched/mismatched/uncompared）替代裸 bool，SanitizeForPersistence/SanitizeForResponse 双边界脱敏，crypto/rand 碰撞安全 TraceUIDv2，策略指纹（排除比例/seed），稳定哈希分桶，v1→v2 读适配器；新增 40+ L1 表驱动测试
- **SQLite v6 迁移、持久化与清理** - autopilot schema v5→v6 幂等迁移；trace 表新增 schema_version/trace_revision/request_correlation_id/release_id/policy_fingerprint/persistence_class/details_json 列；窗口表重建主键增加 release/policy/cohort 维度和 compared/matched/mismatch/uncompared 计数；safety event 增加 release/policy 字段；in-flight 索引和异常提升落盘；有界异步 writer（512 队列/128 终态预留/64KiB 上限/250ms deadline/1s drain）；7 天 trace + 30 天窗口清理策略
- **Trace 关联接入请求生命周期** - 入口生成 requestCorrelationId 写入 gin context；选择渠道后写入 AutopilotTraceUID；ChannelLog 新增 RequestCorrelationID/AutopilotTraceUID 字段和 ChannelLogOption；BuildPlan 写入 dry-run trace（必落盘）；NormalizeSelectionTrace 将 SelectionTrace 规范化为安全摘要并 AttachSchedulerDecision；AppendEndpointAttempt 有序/容量受限/乱序合并的 attempt 摘要
- **endpoint attempt 摘要接入实际请求链路** - SetAttemptRecorderHook + recordAttemptCompleted hook 模式；upstream_failover 在 CreatePendingLog 后记 "started"，成功/错误时记 "completed"；main.go 注册 hook 连接 TraceStore；fail-open 观测失败不影响代理请求
- **受控发布与自动降级** - active 模式加入配置枚举/归一化/IsAutopilotActive；RolloutPercent/ControlPercent/ReleaseID/RolloutSeed 配置字段和 validateRollout 校验；ReleaseController 集中管理状态迁移（逐级晋升/降级随时允许）、稳定分桶、EvaluateAndApplyRegression 三窗口回归降级、SafetyOverride 安全覆盖；接入 SmartRouter 请求路径（入口冻结 RoutingReleaseSnapshot）；release/policy/cohort 隔离聚合
- **只读 Trace API 与前端详情** - GET /api/autopilot/traces/:traceUid 详情端点（404/503/partial 契约、2s 查询 deadline）；列表 API 改为 TraceSummary 输出，支持 release/cohort/mode 过滤和游标分页；统计新增三态比较计数；前端 AutopilotTraceDetailDialog 决策→Scheduler→尝试→终态时间线；AutopilotTraceTable 适配 TraceSummary；ChannelLogsDialog 跳转入口
- **分层测试（L1-L5）** - L1 纯函数 40+；L2 SmartRouter+TraceStore 集成 8；L3 SQLite 生命周期 12（重启还原/坏 JSON/过期清理/迁移幂等/脱敏）；L4 HTTP 契约 9（列表/详情/统计/404/脱敏/分页）；L5 真实上游 smoke 2（默认 t.Skip）；SSE golden 回归 6（帧顺序/trace 不改输出/取消/脱敏）

## [v3.0.0] - 2026-07-23

### 新增

- **Claude Code System Header 过滤** - OpenAI/Gemini/Responses provider 自动过滤 CC billing header、identity 与 subagent 角色描述，减少上游噪音；StripBillingHeader 扩展为完整移除 billing block
- **分层 system header 过滤接入请求流程** - Messages 入口按 channel-keyHash-model 记忆最优过滤层级，连接错误与 HTTP failover 失败时渐进升级，成功后巩固层级；仅影响 Claude 透传入口，其余 provider 保持原有过滤
- **托管渠道模型清单重新发现** - 编辑渠道对话框为托管渠道提供重新发现按钮，触发 auto-discover 后轮询任务状态并自动刷新模型清单，支持 zh-CN/en/id 三语
- **Kimi Code 套餐用量查询** - Kimi 自动托管凭证支持绑定本地 Web 会话令牌，查询并刷新周额度、短期频限、订阅与赠送余额；绑定、刷新与账号列表接口均不回显令牌
- **DeepSeek 官方账号自动托管** - 快速添加同时创建 Messages、Chat 与 Responses 协议渠道，模型发现统一使用官方 `/models`，编辑渠道时可按凭证查询余额
- **DeepSeek 峰谷成本倍率配置** - Autopilot 成本评分支持按供应商、时区和时间窗口应用倍率；内置规则延后至 2026-07-20 激活，可通过配置调整生效时间
- **Autopilot 规范模型能力基准** - 模型注册表新增独立 benchmark 领域向量，按渠道质量证据向下折算并写入路由 trace；首批覆盖 Claude Opus 4.8、GPT-5.6 Terra 与 GPT-5.6 Sol
- **Autopilot 手动 L3 供应商质量探测** - 新增固定 canary、每日预算与 endpoint×model 画像回写，支持 Messages、Chat、Responses、Gemini 上游协议，响应和持久化均不包含明文 Key 或模型原始输出
- **new-api 多账号管理** - 支持为已有 new-api 订阅添加多个账号（accessToken），后端提供账号 CRUD API，渠道编辑页面可管理账号列表
- **Autopilot 智能路由引擎（Phase 1-4 全部完成）** - SmartRouter 评分引擎（§5.3 九项公式 + §5.5 派系偏好 + §5.6 价格偏向）、确定性 TaskClassifier + 路由配置/kill switch、shadow 注入与 assist/auto 模式语义转正、EndpointAttemptPolicy（§4.6.2）+ TryUpstreamWithAllKeys、CapabilityFloor + MinQualityTier reasons、enforce model routing policy boundaries、接入模型注册表能力下界与上下文窗口硬约束、Phase 1 收尾接线、Phase 2 剩余5项集成、P1.5 迁移与回滚契约
- **Autopilot 画像与健康诊断系统** - ProfileStore（内存缓存 + SQLite 异步持久化）、Profiler L1 被动画像推导、HealthAnalyzer L1 被动健康诊断、后台聚合 worker、健康中心只读 API 与前端视图、LocalModelRuntimeProfile 本地运行时画像与探测、LocalCandidateProvider 进入 endpoint 候选池
- **Autopilot TrustedRoutingAdvisor 与决策追踪** - 五态状态机 + AdvisorHintEffect 路由约束解析、shadow 评估 + 决策记录 + API、画像变更事件推送与前端时间线（Phase 3A）、RoutingDecisionTrace + TraceStore + trace handlers、智能路由诊断面板与自动模型映射诊断预览
- **Autopilot 时序与限流感知** - TimeBucketMetrics + QualityTrend 检测（§3.10）、RateLimitDiscoverer shadow（§4.5）、GroupChangeDetector 分组变更检测（§3.9）、FastDecay shadow 评分 + 批量查询 + RPM 运行态注入、接线限流信号/时序/分组变更到 Manager 与健康中心 API
- **Autopilot L2 探测与模型映射** - L2 轻量探测 worker + 每日预算 + 探测队列、模型自动映射闭环（Phase 3B-2）、StabilityTier 晋降级滞后（Phase 3B-3）、L2 探测自愈闭环（Phase 3B-1）、自动发现完成后一致结果自动写入 SupportedModels
- **Autopilot 订阅与用量画像** - SubscriptionProfile + 驾驶舱聚合 API + 三 store 接线、§3.2.3 订阅级能力共享 + §3.2.4 用量窗口 Phase 1 shadow、订阅中心 provider adapter（余额自动刷新）
- **Autopilot 人工路由意图** - ManualRoutingIntent（Phase 1 shadow）、参与 SmartRouter 路由（§4.6.4 人工意图执行语义）
- **Autopilot 真实请求画像与自适应超时** - 基于真实请求画像、TTFB 自适应响应头超时、采集端点响应头延迟画像、跨重启保留 TTFB 画像
- **Autopilot auto 上线闸门与自动降级** - auto 上线闸门、自动降级、终态渠道降至 assist 队尾
- **Autopilot A/B 测试** - 低比例统计抽样双发、ABTestConfig.Validate() 补齐负值/越界兜底
- **Autopilot SLO regression 自动回滚**
- **Autopilot 本地任务模板系统**
- **Autopilot 用户自定义标签系统** (Phase 4 Item 1)
- **Autopilot 成本报表** - 按用户/模型/渠道/key (Phase 4 Item 2)
- **Autopilot 渠道推荐** (Phase 4 Item 4)
- **Autopilot 批量渠道管理** - 导入/导出/模板 (Phase 4 Item 5)
- **Autopilot new-api 订阅集成** - 后端 adapter + 前端
- **Autopilot 火山方舟套餐用量查询与展示** - 未绑定 Access Key 时回退内置兜底模型清单，兜底模型清单区分 Agent Plan 与 Coding Plan
- **Autopilot MiniMax Token Plan 用量查询** - MiMo 绑定控制台 Cookie 查询 Token Plan
- **渠道自动探测与聚合** - 自动探测并聚合多协议渠道、在编辑页展示协议可用模型、自动识别发现协议类型与展示自动探测协议地址
- **智谱官方渠道自动识别** - 自动识别智谱官方渠道模板，补齐桌面端渠道自动识别模板
- **快速添加渠道 Provider 模板化** - 选 provider + 输 key 即可创建、per-key baseURL 探测与绑定
- **多协议渠道统一展示** - 统一展示多协议渠道、按上游协议展示渠道标签、稳定多协议渠道排序
- **渠道保活验证模块** - 新增渠道保活验证、渠道健康检查配置与持久化
- **(Key,模型) 组合级限制与定时自动恢复** - failover 新增组合级限制，定时自动恢复
- **内置模型清单增强** - /v1/models 补充 max_output_tokens (#266)、同步 LiteLLM 能力与 DeepSWE 证据
- **模型能力基准数据自动更新** - benchmark 自动更新模型能力基准数据
- **优云智算套餐余量查询** - compshare 绑定优云智算套餐余量查询、同步套餐并发限速
- **Kimi Code 模型与按 Key 发现**
- **火山引擎 Agent 与 Coding Plan 自动识别**
- **自动托管账号持久化层级** - 账号级凭证接口、切换凭证差异更新、聚合旧版托管协议渠道
- **new-api 安全分组倍率路由**
- **远程预置更新运行时** - 预置数据集中管理 + 运行时可更新架构（Phase 1）、完成远程预置更新运行时
- **订阅来源等级只读展示** - 来源等级由来源类型系统推导，改为只读展示
- **火山引擎与 BytePlus 赞助**
- **套餐类渠道 Key 行余量摘要补齐多时间窗**
- **桌面端 autopilot 对齐** - 管理面板+健康中心+骨架（第一波）、cockpit概览+new-api订阅+试用意图（第二波）
- **桌面端渠道自动探测与复用同步** - 同步渠道自动探测与复用、托管渠道识别模板与徽章、Codex 生图工具权限探测、TTFB 首字节 P95 列、auto 上线闸门与自动降级到桌面端健康中心
- **Responses 音频 content block 丢弃告警** - converters: Responses 音频 content block 丢弃时输出告警日志
- **前端 Health Center / Subscriptions / Cockpit 视图**
- **渠道卡片健康 badge + 来源标签系统（§8.2）**

### 修复

- **本地部署渠道重新发现后显示"来源未知/历史记录未保存"** - `writeProfiles` 与 `buildEndpointInventory` 统一用 `CanonicalBaseURL` 规范化 baseURL，修复带 `/v1` 后缀时 endpointUID 不一致导致画像写入后无法被 `ListActiveByChannel` 命中的问题
- **套餐型 Provider Key 限额重置后不自动恢复** - Kimi/MiMo/优云智算/火山用量刷新后新增 `TryRestoreDisabledKeysByUsage`，按各 Provider 用量快照的重置时间与剩余额度自动恢复因余额/限额不足被拉黑的 Key；同时修复 `syncManagedAccountsFromChannels` 仅从活跃 Key 重建凭证池、导致被拉黑托管 Key 的 Console token/AccessKey/用量快照在下次配置保存时丢失的问题
- **Provider 多协议账号部分写入** - 首次创建与旧账号补协议改为验证后单次提交，失败不残留半成品渠道，并保留 MiMo 区域及火山套餐端点亲和性
- **快速添加成功后对话框未关闭** - provider 自动托管创建成功后立即结束交互，模型发现继续在后台执行
- **Autopilot 配置迁移与运行态隔离** - 旧配置块解析或升级失败时回退安全默认值，支持结构体指针叠加，并避免环境急停经管理 API 写回配置文件
- **辅助路由协议回归** - `assist` 保留无法评分的原候选供 failover，真实路由 trace 回填实际渠道，修复模型不支持错误重试、普通 Key 的模型限制执行、relay 耗尽误限制及 Messages 响应模型字段丢失
- **new-api 验证失败** - 修复某些 new-api fork（如 Veloera/voapi）要求 `New-API-User` header 导致的 401 错误，新增 VerifyWithFallback 两阶段验证回退逻辑
- **套餐 Key 恢复边界条件收紧** - P1 收紧恢复条件为 AND 语义、P2 修复 Kimi/火山套餐恢复边界条件
- **system header 过滤稳定性修复** - 修复 panic、过期误判与误升级问题、字符串形式 system 未过滤 CC header 的问题
- **Autopilot 替代模型选择优化** - 按任务难度选择替代模型、优先选择更强同档模型、使用渠道模型倍率排序、按能力下限优化排序、避免常规任务过度升级模型、修正多模态模型能力路由
- **Autopilot 模型发现与画像修复** - 记录模型发现来源与时间、自适应模型解析修正、endpoint metrics identity 对齐、profile model support decisions 遵守、当前有效画像清单对账
- **Autopilot 路由约束对齐** - scheduler diagnose 与 request profile 对齐、shadow 与自动路由约束对齐、dry-run 与自动路由决策对齐、诊断画像与路由追踪统一
- **Autopilot 配置迁移与隔离修复** - 自动升级旧版 Autopilot 配置、配置迁移隔离完善、环境急停经管理 API 写回配置文件隔离
- **火山方舟 Agent Plan 修复** - 探针与协议入口区分、Key 端点验证修复、套餐 Key 验证请求特征对齐、Coding Plan 用量刷新修复、套餐模型发现签名修复
- **渠道展示与编辑修复** - 托管渠道真实模型范围展示、额度禁用状态与用量展示修正、渠道行展示与官方渠道命名/官网编辑优化、渠道添加与官方渠道编辑流程简化
- **自动托管渠道编辑精简** - 多轮精简自动托管渠道编辑项、收敛编辑项
- **Autopilot 端到端流程修复** - auto-add/auto-status 端到端流程、辅助路由协议回归、终态渠道降至 assist 队尾
- **MiMo 自动发现修复** - 自动发现状态修复、自动发现失败修复、自动添加多协议渠道
- **桌面端同步修复** - 额度禁用状态与 Key 计数、baseUrl 清理路径、自动渠道命名简化
- **前端图标注册修复** - mdi-check-all、DeepSeek 余额图标、mdi-connection、ApiKeyManagementSection 缺失 mdi 图标、官方渠道标识图标
- **前端 TypeScript 编译错误修复**
- **Compshare system roles 规范化** - messages: normalize Compshare system roles
- **LLM 统一渠道视图置顶/置底排序修复**
- **模型定价同步与未配置成本标记**
- **重新发现轮询识别 failed 状态并展示错误**
- **拉取渠道模型列表时使用 key 级绑定 baseUrl**
- **模型能力基准多来源更新与图表生成修复**
- **熔断引入模型多样性门槛与渠道组合失败熔断**
- **自动托管渠道官网地址持久化**
- **快速添加渠道名称预览修复**
- **最终上游模型日志记录** - logging: record final upstream model
- **内部元数据泄漏到续轮请求阻止** - responses: 阻止内部元数据泄漏到续轮请求
- **快速添加前去重 API Key**
- **渠道自动命名简化与仪表盘轮询合并**
- **容器后端验证的前端嵌入修复**
- **baseUrl 清理剔除 /api-keys 创建页路径**
- **disabled managed credentials 跳过** - routing: skip disabled managed credentials
- **Claude Code system 指纹与客户端探针特征对齐**
- **小屏协议地址布局优化与协议名称换行避免**
- **添加弹窗正文滚动与 usage 控制台路径识别**
- **Autopilot 按真实协议创建自定义渠道**
- **Autopilot DeepSeek 官方账号托管完善**
- **逻辑渠道多协议日志汇总**
- **按客户端协议持久化画像分类与发现完成后立即持久化**
- **绑定模型发现结果保留**
- **官方 provider 凭证池复用与自动托管官方渠道身份标明**
- **按 endpoint binding 隔离模型调度** - routing: 按 endpoint binding 隔离模型调度
- **旧版托管协议渠道聚合**
- **官方渠道改用智能调度**
- **快速添加成功后关闭弹窗**
- **content_part 流式事件识别** - responses: 识别 content_part 流式事件
- **schema 迁移幂等自愈 advisor_decisions.reason 列**
- **scoreEndpointForKey EndpointUID 与 handlers 层不一致修复**
- **RateLimitApplier kill switch 测试闭包捕获值拷贝问题**
- **Responses input item additional_tools 剥离**
- **未知健康状态徽标颜色调整**
- **快速添加上游类型选择移除**
- **黑名单密钥恢复状态提示**

### 重构

- **订阅中心重构为 Provider 卡片式快捷接入面板** - 移除订阅列表表格展示，改为双栏布局：左侧 Provider 卡片（GitHub Copilot/new-api/手动添加），右侧详情表单；订阅中心只负责接入新订阅
- **Agent skills 迁移至 .claude/skills** - 通过 symlink 将 agent skills 迁移到 .claude/skills 目录
- **协议模型可用性 diff 视图重新设计** - frontend: 重新设计协议模型可用性差异视图
- **Kimi 用量展示简化** - frontend: 简化 Kimi 用量展示以匹配官方 CLI

### 优化

- **路由性能优化** - config: clone only selected upstream；routing: cache request channel snapshots、prefilter incapable managed channels、prefilter channels without usable keys

### 文档

- **Autopilot 设计文档** - 新增同订阅多 Key 能力共享/配额负载均衡(§3.2.3)与用量窗口(§3.2.4)设计、任务域优势矩阵(§5.7)与思考等级差异(§5.8)设计、内置模型清单设计(§4.2.1)、new-api 集成与订阅 Provider Adapter 框架、Phase 3A/3B/4 全部完成状态更新
- **桌面端文档** - 补充后端发现与端口附着约定
- **Codex 上游协议变更评估** - 追加 Codex rust-v0.145.0 上游协议变更评估、新增 gpt-5.6 适配与 codex imagegen 上游文生图待办
- **SignPath 代码签名致谢** - 文档站首页与 README 添加 SignPath 代码签名致谢，logo 放大并链接到官网
- **Star History 图表更新** - 调整为 Markdown 图片并更新 token
- **火山引擎推广链接** - README 为火山引擎注册即领 2500 万 Tokens 添加推广链接

### 其他

- **Windows 签名切换至正式证书策略** - ci: 切换 Windows 签名至正式证书策略
- **Serena 项目配置更新**
- **Autopilot 日志降噪** - 降低画像落盘成功日志噪声、移除画像推导冗余日志
- **Codex 0.143.0 upstream check 记录**
- **Apple Container 隔离验证** - 添加 Apple Container 隔离验证环境
- **下载按钮改为两行网格布局** - style: 下载按钮改为两行网格布局

## [v2.9.37] - 2026-07-07

### 新增

- **渠道选择 Trace 全链路诊断** - scheduler: 新增 channel selection trace，覆盖候选过滤、活跃模型过滤、选择失败原因，支持 `/v1/models/diagnose` 干跑视图与流式日志摘要
- **响应续思折叠** - responses: 上游截断的 reasoning continuation 在代理层自动折叠，避免客户端收到不完整思考链
- **渠道配置发现增强** - discovery: 增强渠道配置发现能力，改进推荐算法
- **瞬态渠道发现** - discovery: 新增瞬态渠道发现机制，支持快速探测上游可用性
- **断路器温和策略改为新默认** - circuit-breaker: 调整断路器策略预设，温和策略取代激进策略作为新默认值
- **Agnes 模型描述注册** - registry: 为 Agnes 系列模型补充描述信息
- **store-update 完成后自动打开下载目录** - skill: store-update 下载完成后自动在文件管理器中打开目标目录

### 修复

- **discovery 工具调用探测协议选择错误** - discovery: messages 渠道 serviceType 为 responses/chat/gemini 时，工具调用能力探测始终用 Claude 格式导致假阴性；修复为按 serviceType 自动选择 responses/chat/gemini 对应格式
- **discovery 不再生成 supportedModels 通配白名单** - discovery: 发现流程自动写入 \*alias\* 通配符模式（\*opus\*、\*gpt\* 等）到 supportedModels，实际上渠道无需此限制；删除生成逻辑，apply 时始终清空旧值
- **discovery 协议探测失败时模型映射为空** - discovery: 用户显式选定 channelKind 但该协议探测失败时，buildDiscoveryMappingRecommendation 返回空映射；降级到任意成功协议的模型重新构建别名映射
- **thinking probe 误覆写 passbackReasoningContent** - compat: 历史 thinking block 探测返回 400/422 时错误地将 passbackReasoningContent 置为 false；此标志由独立 thinking 预算探测决定，不受历史块探测影响
- **协议延迟统计含超时模型** - discovery: runDiscoveryProtocolProbe 对所有模型（含超时）的延迟做均值，导致有效延迟数据被拉高；改为仅统计成功模型均值
- **gemini 渠道 reasoningMapping 暂不生成** - discovery: Gemini handler 目前未消费 reasoningMapping，写入配置只产生无效噪声；明确不为 gemini 渠道生成该字段，待 Gemini thinking 路径支持后再启用
- **compat evidence 迭代顺序不稳定** - discovery: map 迭代顺序不确定导致证据列表每次不同；改为先排序 key 再遍历
- **熔断器未将 no_available_account 计为过载** - failover: 上游返回 no_available_account 未触发熔断，新增 FailureClassOverloaded 分类
- **serviceType 派生协议回退优先** - discovery: 当用户未显式指定 channelKind 时，优先使用 serviceType 派生的协议作为回退，而非仅依赖探测成功模型
- **回退成功模型未传播到后续探测** - discovery: 首次协议探测成功得到的模型在后续探测中被忽略，导致其他协议探测缺少候选；改为将回退成功模型注入所有后续探测
- **目标协议失败时未从回退协议构建映射** - discovery: 目标协议探测完全失败时 buildDiscoveryMappingRecommendation 返回空；改为从任意成功协议的模型重新构建别名映射
- **发现流程白名单生成污染 supportedModels** - discovery: 自动写入通配符模式到 supportedModels 实际上限制了渠道可见模型；删除生成逻辑，apply 时始终清空旧值
- **messages 渠道工具调用探测格式错误** - discovery: 工具调用能力探测始终用 Claude 格式；修复为按 serviceType 自动选择 responses/chat/gemini 对应格式
- **显式渠道协议推荐被覆盖** - discovery: 用户明确指定 channelKind 时协议推荐仍被探测结果覆盖；改为优先保留用户选择
- **malformed 响应未触发重试** - failover: 上游返回 malformed 响应体时未触发重试机制；新增重试逻辑
- **Vue SFC 模块类型声明缺失** - desktop: 前端 typecheck 无法识别 .vue 文件模块声明；补充 env.d.ts 中的 SFC 模块声明
- **桌面端开发模式未加载嵌入前端资源** - desktop: Wails dev 模式未正确加载 embed.FS 中的前端资源
- **Wails 开发服务器日志过于冗杂** - desktop: 抑制 Wails dev server 信息级别日志输出
- **.vue 导入 TS2307 类型错误** - frontend: tsconfig 未包含 env.d.ts 导致 .vue 文件导入报 TS2307
- **模型列表未暴露上下文窗口** - models: /v1/models 返回结果未包含 context window 信息
- **模型聚合等待窗口过短** - discovery: 多上游模型聚合的等待窗口不够长，部分慢响应渠道被遗漏
- **discovery 重复创建 channel models fetcher** - discovery: 每次发现流程重新创建 fetcher 实例，改为复用已有实例
- **上游临时过载未触发熔断** - backend: 上游返回 503/429 等临时过载状态码时未正确触发断路器
- **未配置模型时 /v1/models 返回空** - models: 当模型未在上游注册时，回退到渠道配置的模型列表
- **discovery 通道推荐 UI 对齐** - discovery: 通道推荐 UI 展示对齐设计稿
- **上下文能力菜单被遮挡** - frontend: 上下文能力下拉菜单被其他元素遮挡
- **渠道 URL 包含 profile wallet 路径** - frontend: 前端展示渠道 URL 时未剥离 profile wallet 路径前缀
- **渠道 URL 包含 admin 路径** - frontend: 前端展示渠道 URL 时未剥离 /admin 路径前缀
- **桌面端上下文模型下拉层级错误** - desktop: 上下文模型下拉菜单被高级选项遮挡，提升层级

### 优化

- **对齐 model registry map keys 格式** - style: 统一模型注册表 map keys 对齐方式，移除测试文件多余空行

### 文档

- **Channel Autopilot 设计文档** - docs: 新增 Channel Autopilot 自动路由设计文档
- **Channel Autopilot 端点级粒度设计** - docs: 细化 Channel Autopilot 到端点级别的路由粒度设计
- **Channel Autopilot 时间质量感知** - docs: 为 Channel Autopilot 设计补充时间维度质量感知机制
- **Channel Autopilot 手动路由意图** - docs: 新增手动路由意图设计，支持用户显式指定路由偏好
- **Channel Autopilot 成本感知路由** - docs: 新增成本感知路由设计，在质量与成本间自动权衡
- **Channel Autopilot 原生 image/vector 路由** - docs: 将原生 image 与 vector 渠道纳入自动路由范围
- **可信本地路由顾问** - docs: 新增可信本地路由顾问设计，基于历史数据提供路由建议
- **用户价格偏向 (Cost Preference) 设计** - docs: 新增用户价格偏向设计，支持按成本偏好调整路由权重
- **上游供应商质量差异设计 (ProviderQualityScore)** - docs: 补充上游供应商质量差异评分设计
- **模型派系偏好 (Model Family Preference) 设计** - docs: 新增模型派系偏好设计，支持用户指定偏好的模型家族
- **ModelFamily 枚举扩展至 17 个派系** - docs: 模型派系枚举扩展至 17 个，来自模型注册表
- **Channel Autopilot 验收契约** - docs: 新增 Channel Autopilot 验收测试契约文档

### 改进

- **保留用户显式选定的渠道协议推荐** - discovery: 用户明确指定 channelKind 时优先保留别名，不受协议探测结果覆盖

## [v2.9.36] - 2026-07-05

### 新增

- **集成 vectors Embedding 兼容性面板到桌面端渠道编辑流程** - desktop: 集成 vectors Embedding 兼容性面板到渠道编辑流程
- **新增 vectors Embedding 兼容性配置面板组件** - desktop: 新增 vectors Embedding 兼容性配置面板组件
- **移植 embedding 兼容性类型/序列化/i18n 到桌面端** - desktop: 移植 embedding 兼容性类型、序列化与 i18n 到桌面端
- **OpenAI 响应思考标签提取为原生 reasoning 字段** - provider: OpenAI 响应思考标签提取为原生 reasoning 字段

### 修复

- **对齐桌面端 capability 测试反馈** - desktop: 对齐桌面端 capability 测试反馈
- **使用结构化对话轮次** - cockpit: 使用结构化对话轮次替代纯文本
- **embedding 兼容性配置边界问题** - vectors: 修复 embedding 兼容性配置边界问题
- **剥离 embedding 请求体中的 stream 字段** - vectors: 剥离 embedding 请求体中的 stream 字段避免上游 SSE 误触发

### 优化

- **提取 buildEndpointURL 正则为 package-level var** - vectors: 提取 buildEndpointURL 正则为 package-level var 避免重复编译

### 其他

- **补齐 embedding capability 与 handler 边界测试** - test: 补齐 embedding capability 与 handler 边界测试

## [v2.9.35] - 2026-07-04

### 新增

- **展开态当前渠道点击支持固定与取消固定切换** - cockpit: 展开态当前渠道点击支持固定与取消固定切换
- **快捷渠道覆盖支持固定与取消固定** - cockpit: 快捷渠道覆盖支持固定与取消固定
- **发布成功后自动调用 store-update** - github-release: 发布成功后自动调用 store-update

### 修复

- **向量化渠道兼容性配置边界问题** - vectors: 修复桌面端 Embedding 兼容性面板未指定 normalized 时崩溃、Vectors 渠道重名更新、非法 embeddingCapabilities 保存与非法 input 本地校验缺口
- **拦截 Claude Code 模型切换探针避免空响应误判** - messages: 拦截 Claude Code 模型切换探针避免空响应误判
- **修复 copilot 渠道 /v1/models 代理返回 404** - models: 修复 copilot 渠道 /v1/models 代理返回 404
- **修正 gitignore 规则并添加 git 代理自动检测** - store-update: 修正 gitignore 规则并添加 git 代理自动检测

### 其他

- **同步 skill 更新到 .agents/skills/** - chore: 同步 skill 更新到 .agents/skills/

## [v2.9.34] - 2026-07-04

### 新增

- **注册 mdiCash 图标** - ui: 注册 mdiCash 图标，补充前端图标资源
- **5xx + Retry-After 也触发限速 cooldown** - ratelimit: 上游返回 5xx 或 Retry-After 头时同样触发限速 cooldown
- **上游临时过载检测与渠道冷却机制** - failover: 新增上游临时过载检测与渠道冷却机制，提升故障转移鲁棒性

### 修复

- **单模型渠道 CostUSD 不显示** - metrics: 修复单模型渠道 CostUSD 不显示的问题
- **限速窗口单位语义修正** - ratelimit: 修正限速窗口单位语义，字段值直接以秒为单位

### 其他

- **Serena 本地记忆目录加入忽略列表** - chore: 将 Serena 本地记忆目录加入 gitignore

## [v2.9.33] - 2026-07-04

### 修复

- **DeepSeek context_management 不兼容导致 400** - providers: 过滤 DeepSeek Claude 兼容端点不支持的 `context_management.clear_thinking_20251015` 编辑和 `context-management-2025-06-27` beta header，修复 Claude Code 首轮/recap 请求的 thinking 回传 400

## [v2.9.32] - 2026-07-03

### 修复

- **ChannelManager canDeleteChannel 引用错误** - desktop: 修复 ChannelManager canDeleteChannel 引用错误

## [v2.9.31] - 2026-07-03

### 新增

- **DeepSeek tool_use passback 双重降级缓存** - thinking: DeepSeek tool_use passback 双重降级缓存

### 修复

- **opaque encrypted_reasoning 占位保障 DeepSeek thinking passback** - responses: opaque encrypted_reasoning 占位保障 DeepSeek thinking passback

## [v2.9.30] - 2026-07-03

### 新增

- **原生 WebSocket v2 透传** - responses: 支持原生 WebSocket v2 透传
- **流式 session 回写与 incomplete 合成** - responses: 流式路径 session 回写并在 post-commit stall 时合成 incomplete
- **Responses reasoning encrypted_content 缓存** - thinkingcache: 扩展存储 Responses reasoning encrypted_content
- **分层保真 compact** - compact: 增加分层保真 compact 与 function_call_output 摘要

### 修复

- **快速创建渠道数据源提交** - desktop: 统一快速创建渠道时提交数据源
- **视觉回退上下文校验** - failover: 校验 vision fallback context，避免无效视觉回退上下文继续传播
- **agent skills 路径与 Store MSIX tag 下载** - skill: 修复 `.agents/skills` 内 skill 文档误指向 `.claude/skills` 的路径，并确保 store-update 脚本的 `--tag` 会传递给 `gh release download`

### 其他

- **desktop-store-update 技能清理** - skill: 删除 desktop-store-update 技能并更新工具配置
- **agent skills 同步** - skill: 同步 agent skills 文档、脚本和 upstream-check 状态

## [v2.9.29] - 2026-07-03

### 新增

- **token 成本(USD) 统计** - metrics: 新增 token 成本(USD) 统计
- **loadShed reaper 与 cooldown 软跳过改进** - scheduler: loadShed reaper 自动推进到期状态、cooldown 软跳过与 scoped limiter 清理改进
- **preflight 空响应详情诊断与敏感信息脱敏** - stream: preflight 空响应详情诊断与敏感信息脱敏

### 修复

- **MiniMax M2 系列上下文与输出元数据** - model-registry: 修正 MiniMax M2 系列上下文与输出元数据
- **火山方舟 quota exceeded 识别与临时限流误判** - failover: 识别火山方舟 quota exceeded 并修复临时 token-per-minute 限流误判
- **桌面渠道模型映射下拉框显示** - desktop: 修复渠道模型映射下拉框显示并清理调试日志

### 文档

- **Microsoft Store submission API 凭据获取说明** - skill: 补充 Microsoft Store submission API 凭据获取说明

### 其他

- **desktop-store-update 技能** - skill: 新增 desktop-store-update 技能

## [v2.9.28] - 2026-07-02

### 新增

- **BIND_HOST 环境变量支持** - server: 新增 BIND_HOST 环境变量以绑定指定网络接口
- **Claude thinking 回填缓存** - thinkingcache: 新增 Claude thinking 回填缓存模块
- **子代理精确识别** - agent: 通过 X-Claude-Code-Agent-Id 精确识别子代理
- **统一配置管理与 CLI 选项** - thinkingcache: 统一 thinking 缓存配置管理与 CLI 选项
- **DeepSeek Claude thinking 缓存诊断** - compat-diagnose: DeepSeek Claude 渠道启用 thinking 缓存诊断
- **DeepSeek Claude 预设 thinking 缓存模式** - preset: DeepSeek Claude 预设切换到 thinking 缓存模式

### 修复

- **stop_reason 仅事件误判** - stream: stop_reason 仅事件不再误判为语义内容

### 重构

- **override 日志增强** - scheduler: 增强 override 日志输出

## [v2.9.27] - 2026-07-02

### 新增

- **渠道预设 maxTokens 支持** - desktop/preset: 渠道预设新增 maxTokens 字段并支持桌面端配置
- **渠道 preset authHeader 字段** - preset: 渠道 preset 新增 authHeader 字段
- **thinking-only 响应识别** - stream: preflight 支持 thinking-only 响应识别为有效内容
- **订阅 Tab 优化与 preset 清理** - desktop: 订阅 Tab 优化与 preset channel_types 清理
- **LongCat-2.0 与 Step 3.7 Flash 模型注册** - models: 新增 LongCat-2.0 与 Step 3.7 Flash 内置模型注册

### 修复

- **轮询立即触发并统一活跃 job 收集** - capability: 修复轮询调度延迟与活跃 job 收集逻辑
- **兼容上游裸 JSON 行 SSE 流** - claude: 兼容上游裸 JSON 行 SSE 流

### 重构

- **ENV 字段移至服务器配置组** - desktop: 将 ENV 字段移至服务器配置组 PORT 之后
- **SSE 流式规范化重构** - claude: 重构 SSE 流式规范化并补齐响应必需字段
- **openai-code-v2 channel_types 移至测试层** - capability: 将 openai-code-v2 channel_types 移至测试层

### 其他

- **Windows MSIX 打包图标资源** - build: 添加 MSIX 打包所需多尺寸图标资源

## [v2.9.26] - 2026-07-01

### 新增

- **Copilot 代理支持** - copilot: OAuth 流程支持代理地址并优化超时处理，支持通过代理进行 Copilot token 交换，新增 Copilot 渠道默认 BaseURL 自动回填
- **Copilot 订阅中心** - desktop: 新增订阅中心页面，将 Copilot OAuth 从渠道创建移至独立 Tab，支持多账号 key 池管理、多渠道目标与诊断结果展示
- **Copilot 预设与前端适配** - desktop/preset: 新增 GitHub Copilot 预设并支持 proxyUrl 字段；web/frontend: Copilot 渠道支持代理地址输入，channelPayload 支持 Copilot 默认 BaseURL
- **订阅 Tab 账号展示增强** - desktop: 增强订阅 Tab 账号展示与 Copilot 模型获取，支持国际化
- **Claude Sonnet 5 注册** - models: 注册 Claude Sonnet 5 模型
- **模型元数据增强** - models: 丰富模型元数据，支持 Anthropic 家族分类，优化模型发现机制并添加缓存降级与 keypool 集成
- **Claude Desktop 连接探测** - messages: 内置 Claude Desktop 连接探测响应
- **本地 HTTPS 自签名证书** - tls: 支持本地 HTTPS 自签名证书
- **Codex 原生 WebSocket 协议** - responses: 支持 Codex 原生 WebSocket 协议
- **商汤日日新接入** - sensenova: 新增商汤日日新 LLM 平台接入
- **讯飞 Coding Plan 迁移** - preset: 讯飞 Coding Plan 迁移并支持 serviceType 配置
- **MiMo 计划选择器** - ui: 为 Codex 和 OpenCode 添加 MiMo 计划选择器
- **超时控件优化** - ui: 将超时控件暴露为秒级显示
- **macOS 应用内更新** - desktop: 新增 macOS 应用内更新打包支持
- **会话预览** - ui: 添加可展开的会话预览功能
- **渠道 Key 复用显示** - desktop: 渠道 Key 复用时显示当前值
- **image_generation 探测** - diagnose: 探测上游 image_generation tool 支持
- **渠道编辑交互优化** - channel-editor: 优化渠道编辑交互体验

### 修复

- **copilot serviceType 映射** - capability: 修复 copilot serviceType 映射到 responses 协议
- **Copilot 订阅 token 复用** - desktop: 修复 Copilot 订阅保存 token 复用与渠道状态管理
- **copilot BaseURL 清空** - desktop: 修复 copilot 渠道 BaseURL 因空版本前缀被错误清空的问题
- **normalizeMetadataUserId 协议限制** - config: 将 normalizeMetadataUserId 限定为仅 Messages 协议生效
- **TLS 证书路径提示** - tls: 证书路径为相对路径时增加绝对路径提示
- **responses 渠道 normalizeMetadataUserId** - channel-editor: responses 渠道默认关闭 normalizeMetadataUserId
- **Copilot OAuth 超时上下文** - copilot: 补充 OAuth 请求超时与错误上下文
- **模型点击排队** - capability-tests: 修复桌面端模型点击排队问题
- **provider key 资产匹配** - channel: currentAsset 按 planID 精确匹配 provider key 资产
- **keysByProvider 去重** - preset: keysByProvider 去重保留首个 asset
- **plan-scoped key 存储** - configservice: plan-scoped key 存储不覆盖 provider channel key
- **Responses URL 重复拼接** - api: Responses URL 末尾已含 endpoint 时避免重复拼接
- **MiMo reasoning effort** - api: 规范化 MiMo Responses reasoning effort 上限
- **Codex tool_search 剥离** - api: 剥离 Codex tool_search 工具调用
- **compaction 流** - api: 允许 compaction 仅含 usage 的流式响应
- **MiMo token plans** - desktop: 允许 MiMo token plans 用于 responses 协议
- **brew deprecated syntax** - brew: 替换已废弃的 `depends_on` 字符串语法
- **模型能力同步延迟** - ui: 编辑期间延迟模型能力同步
- **thinking reasoning 参数** - api: 规范化 thinking reasoning 参数
- **MiMo thinking style** - channel: MiMo 预设使用 thinking style
- **渠道高级选项布局** - ui: 改善渠道高级选项布局

### 重构

- **ResolveTokenWithProxy** - handlers: 全量替换 Copilot ResolveToken 为 ResolveTokenWithProxy
- **渠道编辑器 composables 拆分** - channel-editor: 接入 useEditChannelModal 主 composable，提取禁用 Key 恢复/模型获取/Payload 构建、表单选项/派生计算/预设/工具函数、UI 辅助 composables
- **应用 composables 提取** - app: 提取 useToasts + useAppController composable，提取 savedProviderKeyForPlan 按 planID 匹配资产 key
- **ChannelEditDialog composables** - desktop: 提取 ChannelEditDialog composables
- **configservice 瘦身** - configservice: service.go 瘦身为纯调度层，提取 Provider Key 管理、预览/试运行、OpenCode 配置、Codex 配置与会话迁移、路径解析与 Claude 配置、file/toml/jsonc 格式辅助函数
- **渠道自定义参数面板分离** - ui: 将渠道自定义参数面板拆分为独立组件

### 文档

- 补充桌面端与环境变量文档
- 清理已完成的上游版本评估条目
- 添加 Claude Code 2.1.196 上游版本审查
- 澄清快速模式使用成本说明

### 测试

- 覆盖 Codex tool_search 剥离测试

## [v2.9.25] - 2026-06-29

### 新增

- **Copilot 授权码一键复制** - channel: 为 Copilot 授权码添加一键复制操作，便于完成 OAuth 配置

### 修复

- **提示浮层不被容器裁剪** - ui: 保持 tooltip 显示在被裁剪容器之上
- **桌面端熔断器弹窗布局** - desktop: 保持熔断器弹窗布局在正常文档流中，避免内容溢出错位
- **移除全局历史图片限制控件** - ui: 移除全局 historical image limit 控件，避免与渠道级配置混淆
- **历史图片限制改为渠道级配置** - config: 将 historical image limit 调整为渠道级配置

### 重构

- **抽取 Copilot OAuth 路径常量** - channel: 提取 Copilot OAuth 路径常量并修复授权码清理逻辑

### 文档

- **澄清渠道级图片限制变更** - changelog: 澄清历史图片限制已改为渠道级配置

## [v2.9.24] - 2026-06-29

### 新增

- **MiMo 预设思考强度配置** - channel: 将 MiMo 预设的思考强度配置学习到一键预设

### 修复

- **Volc Ark coding 端点预设** - desktop: 修正 Volc Ark coding 端点预设配置
- **渠道日志与 Key 错误展示** - desktop: 改善渠道日志和 key 错误信息的显示
- **认证区域置于重定向前** - channel: 将认证配置区域放在重定向区域之前
- **Copilot OAuth 外部链接** - desktop: 修复 Copilot OAuth 链接在桌面端无法打开的问题

### 其他

- **延长桌面端 macOS 公证超时** - release: 延长桌面端 macOS 公证任务超时时间

## [v2.9.23] - 2026-06-29

### 修复

- **剥离不支持的 Claude thinking blocks** - responses: strip unsupported Claude thinking blocks
- **优化 Claude 兼容性诊断** - compat: refine Claude compatibility diagnosis
- **刷新熔断窗口近期历史** - metrics: refresh breaker windows from recent history

### 测试

- **稳定 thinking 兼容性覆盖** - responses: stabilize thinking compatibility coverage

### 其他

- **延长 macOS 公证超时时间** - release: extend macOS notarization timeout

## [v2.9.21] - 2026-06-28

### 新增

- **渠道日志复制操作** - channel: 为渠道日志添加复制操作，便于快速复制日志内容排查问题
- **GPT-5.6 能力探测与渠道选项** - capability: 接入 GPT-5.6 能力探测与渠道配置选项
- **GPT-5.6 Amazon Bedrock 模型定义** - model-registry: 新增 GPT-5.6 Amazon Bedrock 模型定义

### 修复

- **保留 Claude 主 API Key** - desktop: 修复桌面端配置同步时 Claude 主 API key 被覆盖或丢失的问题
- **Base URL 哈希诊断校验** - 修复 base url hash 诊断校验逻辑，提升问题定位准确性
- **桌面端流式超时默认值** - desktop: 对齐并保留 stream timeout 默认值
- **渠道菜单浮层稳定性** - frontend: 稳定渠道操作菜单 overlay 展示，避免交互抖动
- **Copilot OAuth 国际化文案** - desktop: 补齐 Copilot OAuth 桌面端多语言文案
- **输出 token 按模型限制裁剪** - routing: 根据模型能力限制下调超限输出 token，避免上游参数错误
- **能力探测携带必需 thinking 参数** - capability: 强制思考模型探测时自动携带 thinking/reasoning 参数，避免能力误判
- **Kimi K2.7 输出限制修正** - model-registry: 将 Kimi-K2.7-Code 最大输出与推荐输出修正为 32768

## [v2.9.20] - 2026-06-27

### 新增

- **展示 Claude Code recap** - cockpit: 展示 Claude Code recap
- **记录并展示 Claude Code recap** - cockpit: 记录并展示 Claude Code recap

### 其他

- **限制 macOS 公证任务时长** - release: 限制 macOS 公证任务时长
- **并行化 macOS 签名公证流程** - release: 并行化 macOS 签名公证流程

## [v2.9.19] - 2026-06-27

### 新增

- **为对话驾驶舱补充悬浮提示** - ui: 为对话驾驶舱补充悬浮提示

### 修复

- **strip Codex response metadata passthrough** - strip Codex response metadata passthrough
- **处理 Responses 流式错误事件** - responses: 处理 Responses 流式错误事件
- **补齐印尼语缺失翻译** - i18n: 补齐印尼语缺失翻译
- **跳过 Claude 空输出重试标题** - backend: 跳过 Claude 空输出重试标题
- **remove channel logs refresh button** - desktop: remove channel logs refresh button

### 重构

- **抽取 Responses WebSocket 回退处理器** - backend: 抽取 Responses WebSocket 回退处理器

### 其他

- **配置 macOS 签名公证** - desktop: 配置 macOS 签名公证

## [v2.9.18] - 2026-06-26

### 新增

- **捕获 WebView 前端运行时错误** - desktop: 捕获 WebView 前端运行时错误
- **支持前端错误写入桌面日志** - desktop: 支持前端错误写入桌面日志
- **同步渠道预设兼容选项** - channel: 同步渠道预设兼容选项
- **优化兼容诊断默认建议** - channel: 优化兼容诊断默认建议
- **展示主对话最新消息** - frontend: 展示主对话最新消息
- **更新 MiMo V2.5 模型能力** - model-registry: 更新 MiMo V2.5 模型能力
- **添加兼容性诊断入口** - channel: 添加兼容性诊断入口
- **添加兼容性诊断** - channel: 添加兼容性诊断
- **优化驾驶舱展开对话卡片展示** - ui: 优化驾驶舱展开对话卡片展示
- **添加驾驶舱关联对话导航** - ui: 添加驾驶舱关联对话导航
- **GitHub Copilot 共享认证支持与安全加固** - GitHub Copilot 共享认证支持与安全加固
- **支持 Chat/Messages/Gemini 入口转换到 Copilot** - copilot: 支持 Chat/Messages/Gemini 入口转换到 Copilot
- **优化添加渠道引导与文档** - copilot: 优化添加渠道引导与文档
- **优化前端诊断结果展示** - copilot: 优化前端诊断结果展示
- **增加 OAuth 重试/取消与渠道诊断** - copilot: 增加 OAuth 重试/取消与渠道诊断
- **补齐 ping、错误分类与 OAuth 状态展示** - copilot: 补齐 ping、错误分类与 OAuth 状态展示
- **支持 GitHub Copilot OAuth 与 Responses 上游接入** - copilot: 支持 GitHub Copilot OAuth 与 Responses 上游接入
- **ConversationCard 展示子代理列表与状态摘要** - web: ConversationCard 展示子代理列表与状态摘要
- **支持流式会话状态追踪** - conversation: 支持流式会话状态追踪

### 修复

- **区分主对话与子代理覆盖状态** - ui: 区分主对话与子代理覆盖状态
- **支持仅子代理渠道覆盖** - backend: 支持仅子代理渠道覆盖
- **覆盖 Gemini 内联图并加固图片 token 估算** - utils: 覆盖 Gemini 内联图并加固图片 token 估算
- **按真实尺寸估算内联 base64 图片的 token** - utils: 按真实尺寸估算内联 base64 图片的 token
- **跳过空 tool_calls 避免 text block 被拆分** - providers: 跳过空 tool_calls 避免 text block 被拆分
- **移除折叠态子代理摘要** - frontend: 移除折叠态子代理摘要
- **按展开状态置顶对话卡片** - ui: 按展开状态置顶对话卡片
- **修正对话卡片展开展示** - ui: 修正对话卡片展开展示
- **跳过 Claude Command 文档标题回退** - messages: 跳过 Claude Command 文档标题回退
- **优化对话卡片展开展示** - ui: 优化对话卡片展开展示
- **同步单轮主对话预览截断** - desktop: 同步单轮主对话预览截断
- **优化单轮主对话预览截断** - frontend: 优化单轮主对话预览截断
- **拆分驾驶舱主对话轮次展示** - frontend: 拆分驾驶舱主对话轮次展示
- **提供全局提示组件上下文** - desktop: 提供全局提示组件上下文
- **优化驾驶舱对话 ID 展示** - ui: 优化驾驶舱对话 ID 展示
- **优化驾驶舱窄屏工具栏布局** - desktop: 优化驾驶舱窄屏工具栏布局
- **优化渠道搜索框响应式宽度** - channel: 优化渠道搜索框响应式宽度
- **优化驾驶舱窄屏筛选布局** - frontend: 优化驾驶舱窄屏筛选布局
- **添加搜索框清除按钮** - desktop: 添加搜索框清除按钮
- **优化驾驶舱筛选栏布局** - frontend: 优化驾驶舱筛选栏布局
- **优化驾驶舱对话展示** - frontend: 优化驾驶舱对话展示
- **精简驾驶舱统计卡片** - frontend: 精简驾驶舱统计卡片
- **修正子代理总数统计** - frontend: 修正子代理总数统计
- **过滤重复对话卡片** - frontend: 过滤重复对话卡片
- **过滤重复渠道索引** - frontend: 过滤重复渠道索引
- **修正驾驶舱卡片与统计文案** - frontend: 修正驾驶舱卡片与统计文案
- **延迟加载全局统计图** - desktop: 延迟加载全局统计图
- **保留快速输入清理后的 URL** - frontend: 保留快速输入清理后的 URL
- **修正快速输入 URL 清理** - frontend: 修正快速输入 URL 清理
- **新建/编辑渠道默认开启自动学习上游限速** - frontend: 新建/编辑渠道默认开启自动学习上游限速
- **用 unref() 包装 store 响应式值** - frontend: 用 unref() 包装 store 响应式值
- **过滤标题回退中的注入上下文** - backend: 过滤标题回退中的注入上下文
- **修复托盘开机自启切换状态** - desktop: 修复托盘开机自启切换状态
- **调整统一会话 ID 提取优先级** - backend: 调整统一会话 ID 提取优先级
- **更新火山方舟控制台链接** - desktop: 更新火山方舟控制台链接
- **驾驶舱展开卡片排序稳定** - Web 与桌面端会话驾驶舱在存在展开卡片时固定当前卡片顺序，避免轮询刷新或最新消息更新时间导致卡片跳动；全部收起后恢复按最新消息排序，并补充桌面端排序稳定性测试
- **兼容 vLLM 响应中的 reasoning 字段** - providers: 兼容 vLLM 响应中的 reasoning 字段
- **清理会话反馈残留引用** - backend: 清理会话反馈残留引用
- **移除驾驶舱会话反馈输入框** - ui: 移除驾驶舱会话反馈输入框
- **移除驾驶舱卡片展开折叠按钮** - ui: 移除驾驶舱卡片展开折叠按钮
- **调整驾驶舱渠道列表高度** - ui: 调整驾驶舱渠道列表高度
- **修复 customHeaders 覆盖 Copilot token 的安全漏洞** - copilot: 修复 customHeaders 覆盖 Copilot token 的安全漏洞
- **修复响应协议转换与认证头泄露** - copilot: 修复响应协议转换与认证头泄露
- **修复 ConversationChannelSequence Vuetify CSS 颜色变量 alpha 写法** - ui: 修复 ConversationChannelSequence Vuetify CSS 颜色变量 alpha 写法
- **Compshare 月度额度耗尽自动拉黑** - `extractErrorInfo`/`extractErrorCode` 兼容大写字段名 (`RetCode`/`Message`) 和数字类型错误码；`isInsufficientBalanceMessage` 增加 `monthly limit exceeded` 等精确短语，使 429 + 月度额度耗尽错误能正确触发 key 拉黑
- **收敛会话卡片高度避免撑爆页面** - cockpit: 收敛会话卡片高度避免撑爆页面
- **保留原始元数据识别子代理** - conversation: 保留原始元数据识别子代理
- **增强调试日志定位响应协议** - messages: 增强调试日志定位响应协议
- **补齐驾驶舱看板文案** - i18n: 补齐驾驶舱看板文案
- **注册驾驶舱图标** - web: 注册驾驶舱图标
- **恢复驾驶舱顶部入口** - web: 恢复驾驶舱顶部入口

### 优化

- **优化控制台图表长周期采样** - desktop: 优化控制台图表长周期采样
- **优化历史图表长周期采样** - frontend: 优化历史图表长周期采样

### 重构

- **清理会话反馈功能** - 清理会话反馈功能
- **提取 ConversationChannelSequence 组件，消除主对话/子代理渠道列表重复** - ui: 提取 ConversationChannelSequence 组件，消除主对话/子代理渠道列表重复
- **子代理渠道交互改为行列表，与主对话保持一致** - cockpit: 子代理渠道交互改为行列表，与主对话保持一致
- **同步驾驶舱 BoardItem 模型到桌面端** - desktop: 同步驾驶舱 BoardItem 模型到桌面端
- **Dashboard 适配 ConversationBoardItem 结构** - web: Dashboard 适配 ConversationBoardItem 结构
- **重构驾驶舱子代理分组与聚合逻辑** - web: 重构驾驶舱子代理分组与聚合逻辑
- **抽取驾驶舱列分组工具** - web: 抽取驾驶舱列分组工具

### 测试

- **补齐 vLLM reasoning 兼容覆盖** - providers: 补齐 vLLM reasoning 兼容覆盖
- **强化 Gemini 图片估算测试并提速** - utils: 强化 Gemini 图片估算测试并提速
- **补齐 copilot 包 httptest 覆盖** - copilot: 补齐 copilot 包 httptest 覆盖

### 文档

- **记录 Claude Code v2.1.193 变更评估** - todo: 记录 Claude Code v2.1.193 变更评估
- **完善兼容选项提示** - frontend: 完善兼容选项提示
- **记录 Codex 上游变更评估** - todo: 记录 Codex 上游变更评估
- **更新 CHANGELOG 记录 Compshare 月度额度拉黑修复** - 更新 CHANGELOG 记录 Compshare 月度额度拉黑修复

### 其他

- **升级前端依赖** - deps: 升级前端依赖
- **更新 Go 与 Wails 相关依赖** - deps: 更新 Go 与 Wails 相关依赖

## [v2.9.17] - 2026-06-23

### 新增

- **桌面端会话父子关系导航** - 展示会话父子关系导航，便于在关联会话间跳转
- **关联子代理会话记录** - 支持记录与展示子代理关联会话
- **桌面端会话驾驶舱任务看板** - 改造会话驾驶舱任务看板，增强子代理任务概览
- **桌面端独立驾驶舱入口** - 添加独立驾驶舱入口，提升访问效率
- **会话反馈记录** - 支持记录会话反馈信息
- **驾驶舱子代理看板改进** - 改进 cockpit subagent board 展示能力

### 修复

- **统计图表长跨度采样与展示** - 将 Web 与桌面端全局/渠道统计图默认采样点控制在 200 以内，并修复渠道级图表在仅最近几天有数据时选择 1 年导致曲线被全年空白压缩的问题
- **桌面端能力测试渠道复制反馈** - 改进能力测试渠道复制反馈体验

### 重构

- **桌面端侧边栏导航分组拆分** - 拆分侧边栏导航分组，优化导航结构
- **桌面端驾驶舱入口分离** - 将驾驶舱入口从管理控制台分离

### 文档

- **已完成 TODO 清理** - 移除已完成 TODO 项

### 其他

- **统计图表采样展示修复合并** - 合并统计图表采样展示修复
- **桌面端开发模式启动顺序** - 调整开发模式前端启动顺序
- **桌面端开发壳构建任务** - 添加开发壳构建任务

## [v2.9.16] - 2026-06-23

### 修复

- **Docker 共享前端工具嵌入** - Docker 构建包含共享前端工具文件，避免容器构建缺失共享工具

## [v2.9.15] - 2026-06-23

### 新增

- **Kimi K2.7 能力别名** - 添加 Kimi K2.7 模型能力别名，支持默认模型能力识别
- **Qwen3.7 模型阶梯定价** - 添加 Qwen3.7 模型阶梯定价配置
- **Codex tool_search_output 合并** - Responses 支持合并 Codex `tool_search_output` 工具输出
- **桌面端模型下拉自动滚动定位** - 添加模型下拉列表自动滚动定位功能
- **版本升级构建监控** - version-bump 技能新增构建进度监控流程

### 修复

- **Codex Responses 默认模型** - 更新 Codex Responses 共享预设默认模型
- **桌面端渠道中心独立滚动** - 修复渠道中心滚动区域交互
- **能力检测重试一致性** - 修复 capability retry consistency
- **桌面端能力检测提示层裁剪** - 修复能力检测模型提示层被裁剪的问题
- **MiMo Codex 兼容预设** - 开启 MiMo Codex 兼容预设
- **桌面端源模型预设过滤** - 过滤已配置的源模型预设选项
- **桌面端目标模型下拉滚动定位** - 修复目标模型下拉滚动定位
- **桌面端目标模型完全匹配显示** - 修复目标模型完全匹配时不显示窗口的问题
- **桌面端源模型选择展开** - 修复源模型选择后再次点击不展开列表的问题
- **桌面端源模型下拉显示** - 修复源模型下拉列表不显示的问题
- **桌面端模型重定向空胶囊** - 移除模型重定向源模型输入框的空胶囊元素

### 重构

- **渠道协议预设抽取** - 抽取渠道协议预设
- **Codex Responses 共享预设抽取** - 抽取 Codex Responses 共享预设
- **前端模型优先级排序共享** - 共享模型优先级排序逻辑

### 测试

- **用户额度不足黑名单用例** - 补充用户额度不足黑名单用例

## [v2.9.14] - 2026-06-22

### 新增

- **Token 统计时间范围扩展到 1 年** - 新增 90 天、半年、1 年、今年等长时间范围选项
  - 后端数据保留期默认值：30 天 → 366 天，上限：90 天 → 366 天（闰年全年覆盖）
  - API 时间范围上限：30 天 → 366 天
  - 新增时间格式支持：`90d`、`180d`、`365d`、`thisyear`
  - 聚合间隔优化：长时间范围使用 8-12 小时间隔，控制数据点数量
  - Web 端和桌面端所有图表组件同步更新
  - 统一两端时间选项文案：桌面端 `7d`/`30d` → `1周`/`1月`
  - 新增中文、英文、印尼文三语言国际化文案
- **Unity2.ai 赞助商集成** - 新增 Unity2.ai 赞助商渠道预设与国际化文案支持
- **Unity2.ai 国际化文案** - 添加 Unity2.ai 渠道预设翻译
- **自动赞助商集成技能** - 新增 add-sponsor skill，支持自动化赞助商集成流程
- **对齐 Web 与桌面端渠道菜单行为** - 统一两端渠道操作菜单交互逻辑

### 修复

- **Codex Responses 共享预设默认模型** - 将 Kimi 默认模型同步为 `kimi-k2.7` 并补齐能力注册，GLM 默认模型升级为 `glm-5.2`，避免 Web/desktop 一键预设回退到旧模型
- **Unity2.ai 和 RunAPI 响应剥离图片生成工具** - 为 Unity2.ai 和 RunAPI 响应剥离 image generation tool
- **Unity2.ai 使用原生 responses 协议** - Unity2.ai 改用原生 responses 协议
- **Unity2.ai 添加 anthropic plan 支持 Messages 目标** - 为 Unity2.ai 添加 anthropic plan 以支持 Messages target
- **Unity2.ai targets 顺序修正** - 修正 Unity2.ai targets 顺序为标准 Messages → Responses → Chat
- **恢复原生 tool_search 调用** - 恢复 Codex 原生 tool_search calls
- **为 tool_search stream 发送 custom_tool_call_input.done** - 确保 tool_search 流式响应完整性
- **保留 tool_search 历史参数** - 保持 Codex tool_search history arguments
- **恢复 tool_search 自定义调用语义** - 恢复 tool_search custom call semantics
- **统一两端高级选项选择框宽度与布局** - 统一 Web 和桌面端渠道编辑器高级选项布局
- **tool_search 保留原始参数 schema 映射为 function** - converters 中 tool_search 保留原始参数 schema 映射
- **统一渠道菜单文案与 Web 端** - 桌面端渠道菜单文案对齐 Web 端
- **保留搜索结果渠道置顶置底操作** - 桌面端搜索结果中保留渠道排序操作

### 重构

- **预设配置辅助函数抽取** - 使用 helper functions 减少 preset 配置重复代码

### 测试

- **Codex chat 子代理工具调用测试覆盖** - 为 chat sub-agent tool calls 添加测试覆盖

### 文档

- **更新 add-sponsor skill 文档** - 更新 add-sponsor skill 最新使用模式

### 其他

- **移除 add-sponsor.sh 脚本** - 删除旧的 add-sponsor shell 脚本

## [v2.9.13] - 2026-06-21

### 新增

- **子代理感知对话路由与亲和隔离** - 新增 subagent-aware conversation routing 与 affinity isolation，减少不同子代理会话间的路由串扰
- **额外代理访问密钥** - 支持额外代理访问密钥配置，扩展代理入口鉴权能力
- **内置模型预设匹配改用正则表达式** - builtin model registry 的 patterns 从简单通配符（`claude-opus-4-7*`）改为显式正则，精确控制前后边界，避免 `gpt-5.4-mini` 被 `gpt-5.4` 误吃、`glm-5.1` 被 `glm-5` 误吃等歧义；支持厂商前缀（`bedrock/claude-opus-4-7`、`xxx-claude-opus-4-7`）和日期版本号后缀（`-20260101`），同时拒绝 `claude-haiku-4-5-with-claude-opus-4-7-fallback` 这类异常组合名
  - 一个 entry 支持多个 pattern（如 `kimi-for-coding` 别名回退到 `kimi-k2.7-code`），未来 Kimi 升级到 K2.8 只需把别名移到新 entry
  - 前端 `channelPayload.ts`、桌面端 `channel-payload.ts` 匹配函数改用 `new RegExp(pattern, 'i')`
  - 后端 `model_registry.go` 新增 `builtinPatternCache` 预编译缓存，RE2 兼容剥离 `(?=$|@)` lookahead 后手动检查边界；`resolvePatternValueFold` 先正则再通配符
  - generator 加构建时正则校验
  - **用户路由级 supportedModels 配置不受影响**，继续用通配符语义
- **手动暂停与自动熔断区分显示** - UI 区分手动暂停与自动熔断状态展示
- **images 渠道纳入模型聚合** - 模型列表聚合纳入 images 渠道，并支持多渠道并行收集聚合
- **Key 权重与模型选择** - Key 池新增 Weight/Models 选择能力，支持 metadata restore 测试并在前端加载 `apiKeyConfigs`
- **按 Key 凭证池与限速** - 新增 per-key credential pool，支持按 Key 作用域进行限速

### 修复

- **调度器文件拆分冲突修复** - 解决 worktree 合并后的 scheduler file-split 冲突
- **驾驶舱交互与 Key 管理** - 修复桌面端驾驶舱交互与 Key 管理若干问题
- **驾驶舱暂停/熔断状态徽标区分** - 区分暂停与熔断状态 badge 并统一文案
- **驾驶舱渠道选择交互** - 修复驾驶舱渠道选择交互问题
- **渠道统计图表交互对齐** - 对齐渠道统计图表交互逻辑
- **重复 Key 提示** - 添加重复 Key 时给出明确提示
- **桌面 dashboard 控制台交互** - 修复桌面端 dashboard console interactions
- **Responses 转换保持 Codex 工具兼容** - preserve Codex tool compatibility in responses conversion
- **Dashboard 渠道编辑弹窗** - 修复桌面端 dashboard 渠道编辑弹窗问题
- **限速高水位调度优化** - 优化调度器限速高水位策略
- **渠道统计包含黑名单 Key** - channel stats 纳入 blacklisted keys
- **渠道编辑对话框高度** - 优化渠道编辑对话框高度
- **黑名单快照与配置监听** - 加固 blacklist snapshot 与 config watcher
- **Code review 反馈问题修复** - 修复 code review 确认的 11 个 keypool/ratelimit/config 问题
- **ChannelView 返回 apiKeyConfigs** - BuildChannelView 返回 `apiKeyConfigs`
- **模型映射同步后再能力测试** - 前端 capability test 前同步 model mapping

### 重构

- **配置更新逻辑抽取** - 抽取 `applyAPIKeyConfigUpdate` 并改进 watcher 防抖策略
- **前端活动可视化与单测** - 改进 activity visualization 并补充 unit tests
- **ChannelOrchestration 样式拆分** - 从 `ChannelOrchestration.vue` 抽取样式
- **EditChannelModal 样式与优先级逻辑拆分** - 从 `EditChannelModal.vue` 抽取样式和 model-priority 逻辑
- **App 样式与能力测试组合式函数拆分** - 从 `App.vue` 抽取样式和 capability-test composable
- **ChannelOrchestration 活动组合式函数抽取** - 抽取 `useChannelActivity` composable
- **Responses handler 拆分** - 拆分 `handler.go`（2160 行 → 5 个文件）
- **Chat handler 拆分** - 拆分 `handler.go`（2305 行 → 5 个文件）
- **Scheduler 测试拆分** - 拆分 `channel_scheduler_test.go`（1650 行 → 3 个文件）
- **Scheduler 实现拆分** - 拆分 `channel_scheduler.go`（1535 行 → 4 个文件）
- **通用 stream helper 拆分** - 拆分 `handlers/common/stream.go`（2799 行 → 4 个文件）
- **Metrics 实现拆分** - 拆分 `channel_metrics.go`（3731 行 → 5 个文件）

### 文档

- **待办事项更新** - 更新 TODO 文档

### 其他

- **额外代理访问密钥合并** - 合并额外代理访问密钥支持
- **渠道编辑对话框高度修复合并** - 合并渠道编辑对话框高度修复
- **黑名单 Key 统计修复合并** - 合并 blacklisted key stats fix
- **Go 格式化** - 执行 go fmt 格式化

## [v2.9.12] - 2026-06-19

### 修复

- **上下文窗口按输入 token 过滤** - 调度器按输入 token 过滤 context windows，避免选择不满足输入上下文要求的渠道
- **Docker 前端嵌入构建稳健性改进** - 改进 Docker frontend embed build robustness，提升前端产物嵌入构建稳定性

## [v2.9.11] - 2026-06-19

### 修复

- **桌面控制台 fallback 文案本地化** - 桌面端控制台 fallback labels 使用多语言文案，避免未翻译标签展示

## [v2.9.10] - 2026-06-19

### 新增

- **上游认证头覆盖配置** - 后端新增 upstream auth header override，支持按渠道覆盖上游认证头
- **渠道认证头设置入口** - 前端暴露 channel auth header 设置，支持管理界面配置认证头覆盖
- **OpenCode Go 预设 x-api-key** - 桌面端 OpenCode Go 预设自动设置 x-api-key 认证头

### 修复

- **模型能力标签简化** - 简化前端 model capability labels，改善模型能力展示文案

### 其他

- **SignPath artifact 安全暂存** - SignPath artifacts 使用安全名称暂存，避免签名流程中的产物名称冲突

## [v2.9.9] - 2026-06-19

### 其他

- **SignPath 签名步骤唯一 artifact 名称** - 为 SignPath 签名步骤添加唯一 artifact 名称，避免多平台并行构建时名称冲突

## [v2.9.8] - 2026-06-19

### 新增

- **OpenCode Zen/Go 渠道预设更新至 glm-5.2** - 桌面端 OpenCode Zen/Go 预设的 Claude 与 Codex 目标模型映射更新为 glm-5.2 / deepseek-v4-flash，并补齐对应 reasoning 映射

### 修复

- **视觉回退模型上下文能力自动添加** - 前端和桌面端在上下文能力自动添加时纳入视觉回退模型，并按大小写不敏感方式去重，避免回退模型缺失能力配置行
- **前端 lint 存量问题清理** - 修复 Web 前端 ESLint 存量错误与警告，清理编辑渠道弹窗拆分后的未使用旧逻辑并补齐相关类型声明
- **Responses SSE 扫描缓冲区上限提升** - 将 Responses 流式 SSE 单行扫描上限提升至 32MB，避免图片/产物等大事件被截断导致流式失败

### 其他

- **upstream-check seen 标签数量限制** - 限制 upstream-check skill 记录的 seen upstream tags 数量，避免状态无限增长

## [v2.9.7] - 2026-06-19

### 新增

- **模型响应模态信息增强** - 增强 models 接口响应中的 modalities 信息

### 修复

- **前端模型目标建议修复** - 修复前端模型 target suggestions 展示与匹配问题
- **发布暂存目录可见性修复** - 使用可见 release staging directory，修复发布打包暂存路径问题

## [v2.9.6] - 2026-06-19

### 修复

- **移除 agent profile 窗口硬最低限制** - 移除桌面端 agent profile 窗口硬最低限制，新增上下文估算日志
- **简化模型能力定价编辑器** - 简化前端 model capability pricing editor 交互
- **修复 capability test 模型映射复用** - 修复 capability test 模型映射复用问题
- **优化模型能力行布局** - 优化桌面端 model capability row layout
- **改善 capability test 状态处理** - 改善桌面端 capability test status handling
- **同步桌面端模型能力配置** - 同步桌面端 model capabilities 配置

## [v2.9.5] - 2026-06-19

### 新增

- **桌面端模型能力编辑器改进** - 改进桌面端 model capability editor 交互与布局
- **前端模型能力编辑器改进** - 改进前端 model capability editor 交互与布局
- **DeepSeek V4 能力更新** - 更新 model registry 中 DeepSeek V4 模型能力信息

### 其他

- **SignPath 测试签名校验放宽** - 放宽 SignPath test signature validation，提升测试环境签名校验兼容性

## [v2.9.4] - 2026-06-19

### 新增

- **上游模型能力注册表** - 新增 upstream model capability registry，用于集中维护上游模型能力信息
- **上下文感知渠道路由** - 新增 context-aware channel routing，根据请求上下文与模型能力选择更合适的渠道

### 修复

- **桌面端目标模型选项来源修复** - 桌面端使用已拉取的目标模型选项，避免候选模型与上游能力不一致
- **桌面端渠道能力测试流程对齐** - 对齐桌面端渠道 capability test 流程，提升测试结果一致性
- **桌面端渠道重排序类型处理修复** - 修复桌面端 channel reorder 的类型处理问题

### 其他

- **SignPath 签名集成** - CI 集成 SignPath signing，完善发布签名流程

## [v2.9.3] - 2026-06-18

### 新增

- **讯飞星辰渠道预设** - 新增 ProviderXFyun 预设，Anthropic + OpenAI 双入口，支持 Messages/Chat/Responses 三目标，含快速输入 URL 识别与中英文 locale
- **Codex SQLite 迁移修复 visibility 字段** - migrateCodexStateDB 动态检测 threads 列，自动回填 preview/has_user_event/thread_source 等隐藏线程可见性字段
- **中转站错误码 failover 识别增强** - 增强中转站错误码识别与黑名单判定，提升故障转移覆盖
- **渠道运行态冷却与上游账号池降级** - 调度器新增渠道运行态冷却与上游账号池不可用降级策略
- **Compshare glm-5.2 预设与推理映射同步** - 桌面端 ChannelEditDialog 同步 Compshare glm-5.2 预设与 reasoning 映射
- **火山方舟 Compshare 预设更新至 glm-5.2** - 更新桌面端火山方舟 Compshare 渠道预设模型

### 修复

- **渠道日志按稳定渠道名过滤共享 metricsKey** - GetChannelLogs 对共享 metricsKey 优先按创建时渠道名筛选，旧日志回退 channelIndex，避免删除/重排后日志串台
- **Codex SQLite visibility 回填来源保护** - 迁移 threads 时根据 source 列跳过 exec 等后台线程，避免误标记为用户会话
- **trace 亲和性遵守 SupportedModels** - 新增测试验证亲和渠道不支持当前模型时正确跳过并回退优先级选择
- **共享 metricsKey 渠道日志按渠道名归属** - 修复共享 metricsKey 场景下渠道日志归属到稳定渠道名
- **共享 metricsKey 渠道日志按 channelIndex 过滤** - 修复渠道日志在共享 metricsKey 时按 channelIndex 过滤
- **短流 EOF 重试策略** - 在 message_stop 前遇到短流 EOF 时进行重试
- **新建渠道使用生成名称提交** - 修复桌面端新建渠道提交时未使用生成名称的问题

### 文档

- **CHANGELOG 维护更新** - 更新 CHANGELOG，补充讯飞星辰、Codex 迁移修复、日志过滤与调度测试记录

## [v2.9.2] - 2026-06-18

### 新增

- **火山方舟预设模型与推理映射更新** - 更新桌面端火山方舟渠道预设模型与 reasoning 映射
- **渠道卡片缓存命中率展示** - 桌面端渠道卡片新增缓存命中率显示
- **渠道添加偏好卡片式交互** - 渠道添加流程改为卡片式交互，提升偏好选择体验
- **渠道日志 reasoning effort 展示同步** - 桌面端渠道日志同步展示 reasoning effort 信息
- **渠道支持选择队列末尾并记住偏好** - 前端新增渠道支持放到队列末尾，并记住用户偏好
- **CompactionV2 本地 compact 与 SSE 转换** - Responses 实现 CompactionV2 本地压缩并支持 SSE 转换
- **Passthrough converter ResponsesItem 字段映射补全** - passthrough converter 补齐 ResponsesItem 全量字段映射
- **ResponsesItem EncryptedContent 字段** - 类型定义新增 EncryptedContent 字段以支持 compaction
- **视觉回退 reasoning effort 与 output_config.effort 同步** - 视觉回退模型支持 reasoning effort，并同步 output_config.effort
- **渠道日志 reasoning effort chips 展示** - 前端渠道日志弹窗新增 reasoning effort chips
- **视觉回退 reasoning effort 选择器与多语言文案** - 前端新增视觉回退 reasoning effort selector 和 locale keys
- **Claude 渠道 reasoning mapping 支持** - 前端扩展 reasoning mapping 支持到 claude channel type
- **Claude thinking effort 渠道映射注入** - providers 根据渠道 reasoningMapping 注入 Claude thinking effort
- **ChannelLog reasoning effort 字段** - metrics 新增 originalReasoningEffort 与 actualReasoningEffort
- **渠道日志 reasoning effort 提取 helpers** - handlers 新增 reasoning effort 提取辅助函数

### 修复

- **火山方舟 Responses 视觉回退补齐** - 桌面端补齐火山方舟 Responses 视觉回退配置
- **orchestration promotion 标签文案优化** - 优化桌面端 orchestration promotion 标签文案
- **legacy reasoning params 转换顺序修复** - providers 在重写为 block format 前先转换 legacy reasoning params
- **模型映射 noVision 同步修复** - 按目标模型改进 noVision 在 model mappings 间的同步逻辑
- **模型映射 noVision flag 同步修复** - 修复 noVision flag 按 target 在 model mappings 间同步
- **Add/Edit Channel Modal 缺失导入修复** - 补充 AddChannel/EditChannel modals 中 normalizeSelectableString 导入
- **Chat reasoning effort 映射模型来源修复** - Chat 使用实际请求体 model 进行 reasoning effort mapping
- **重复 reasoning effort 标签合并** - 合并 UI 中重复的 reasoning effort 标签显示

### 重构

- **EditChannelModal 请求 URL 构建统一** - 统一 EditChannelModal 使用 buildExpectedRequestUrls
- **AddChannelModal 快速添加职责收敛** - 移除 AddChannelModal 编辑模式，仅保留 quick-add
- **reasoning effort 校验逻辑统一** - config 使用 isValidReasoningEffort 统一 reasoning effort validation

## [v2.9.0] - 2026-06-17

### 新增

- **运行时超时热更新与 responseHeaderTimeout 渠道字段** - 新增 `responseHeaderTimeoutMs` 渠道字段，支持全局运行时超时热更新，httpclient 支持显式 responseHeaderTimeout
- **熔断器/调校台新增超时滑块** - 桌面端和前端熔断器、调校台新增 `requestTimeoutMs` 与 `responseHeaderTimeoutMs` 运行时滑块
- **CompactModel 配置支持** - Responses 渠道新增 `compactModel` 配置字段，支持压缩模型自动选择
- **内联模型映射编辑器** - 所有渠道类型支持内联编辑模型映射，无需弹窗
- **主动限速滑动窗口算法** - 令牌桶改为滑动窗口算法，支持可配置的窗口时长，自动学习上游限速默认开启
- **b64_json 图片响应自动转换** - 支持 `b64_json` 请求的 URL 响应自动转换
- **向下游下发可读的空响应错误文案** - 流式响应场景向客户端下发可读错误信息而非空流
- **桌面端 App 级 polling 调度层** - ConversationDashboard 轮询迁移至 App 级调度，支持 window visibility 事件控制
- **桌面端图表功能对齐 Web 端** - 桌面端控制面板使用统计图表与 Web 端完全对齐
- **渠道活动图表背景** - ChannelCard 新增活动图表背景可视化
- **渠道快速创建卡片上游类型品牌色与图标** - 快速创建渠道卡片的上游类型展示品牌色和对应图标
- **渠道编辑器输出冗长度配置** - 渠道编辑器新增输出冗长度配置选项
- **驾驶舱空闲自动恢复时间持久化配置** - 支持通过环境变量和前端选择器持久化配置 override 有效期
- **Kimi Coding Plan 模型重定向与智能排序** - Kimi 渠道实现 Coding Plan 模型重定向与智能模型排序
- **桌面端模型重定向区域 premium UI 重构** - 目标模型输入改为自定义下拉菜单，支持下拉建议
- **快速模式与历史图片轮次限制配置** - Web 端补全快速模式与历史图片轮次限制配置
- **流式断流超时预设快捷按钮** - 桌面端新增流式断流超时预设快捷按钮
- **Esc 快捷键提示** - Web 和桌面端为对话框关闭按钮添加 Esc 快捷键提示
- **Compatibility 分组高级开关补全** - Web 端和桌面端补全 Compatibility 分组所有缺失的高级开关
- **桌面端 i18n 全面国际化** - 迁移到 vue-i18n + 外部 JSON 语言文件，桌面端渠道编辑、控制台、图表等全面 i18n 化
- **文档: Geo SEO, llms.txt, README 桌面安装说明** - 新增 Geo SEO、llms.txt，改进贡献指南和 README 桌面安装说明

### 修复

- **临时限流不触发 Key 黑名单** - 临时限流场景不再误将 Key 加入黑名单
- **REQUEST_TIMEOUT 默认值从 300s 调整为 120s** - 调整默认请求超时为更合理的 120 秒
- **保留 web_search 工具以兼容 Codex v0.139.0+** - Responses 场景保留 web_search 工具兼容新版 Codex
- **修复 UpstreamUpdate JSON 标签拼写错误** - 修正配置结构体 JSON tag 拼写
- **图表系列修复** - 修复 KeyTrendChart yaxis 绑定、series 复用、forceNiceScale、失败率柱透明度等多项图表问题
- **模型映射编辑修复** - 修复 modelMapping 对象值保存失败、combobox 值规范化、目标模型输入框抖动等问题
- **限速配置修复** - 修复 0 值显示为空、窗口时长清空后无法保存、rateLimitWindowMinutes 字段遗漏等
- **桌面端控制台组件修复** - 修复组件缺失导入、滚动导航、Esc 键 window 全局监听等
- **i18n 修复** - 修复英文模式下统计图表中文、id.json 时长单位错误、useI18n setup 上下文时序等
- **resumeChannel 后自动调用 setStatus('active')** - 恢复渠道后正确更新状态
- **REQUEST_TIMEOUT/RESPONSE_HEADER_TIMEOUT 降级为启动兜底** - 超时配置运行时以调校台为准，环境变量仅作启动兜底

### 重构

- **渠道编辑器全面重构** - 左侧导航+右侧面板布局，拆分快速添加与编辑弹窗，统一内容顺序对齐 WebUI
- **高级选项分组重构** - 创建 Runtime 运行期策略分组，统一布局结构，主动限速拆分为独立 RateLimitGroup
- **模型映射区域深度重构** - 行内可编辑、视觉回退模型行内开关驱动，优化布局与样式
- **快速添加面板重构** - 紧凑两栏布局、卡片式分块布局、上游类型选择器标题行后缀推荐项
- **渠道卡片状态展示重构** - 统一交互逻辑、展开按钮与统计图表折叠优化
- **熔断器/调校台滑块设计重构** - 参考 Web 端样式，增强可见性和对比度
- **桌面端渠道编辑创建流程重构** - 统一使用 t 多语言函数，对齐内容顺序与 WebUI
- **vue-i18n 迁移** - 从自定义 i18n 迁移到 vue-i18n + 外部 JSON 语言文件，统一两端 key 命名空间
- **ChannelManager/ChannelTab 惰性加载** - 非活跃页跳过刷新，提升性能
- **CircuitBreakerDialog 布局重构** - header/footer 固定，body 独立滚动

### 文档

- **新增 RESPONSE_HEADER_TIMEOUT 与运行时超时配置说明**
- **更新 compactModel 说明，明确 Responses 渠道 failover 机制**

## [v2.8.28] - 2026-06-11

### 新增

- **桌面端管理面板集成用量统计图表 (#199)** - 管理面板新增用量统计图表展示
- **补齐渠道中心所有 provider 到 Agent 直连设置** - 完善 Agent 直连配置，覆盖所有渠道中心 provider
- **驾驶舱 Override 有效期选择器补齐** - 驾驶舱 Override 配置界面新增有效期选择器
- **upstream-check 新增功能与体验变更报告输出步骤** - 上游版本检查 skill 新增变更报告输出

### 修复

- **完善驾驶舱 i18n 并移除重复的系统状态显示** - 优化驾驶舱国际化文案，移除重复状态展示
- **补全 tooltip content-class 并加大快捷键提示间距** - 修复 tooltip 样式类缺失，优化快捷键提示视觉间距
- **补全 stream.go 中 message_delta 的 stop_details 字段** - 流式响应 handlers 补充 stop_details 字段处理
- **添加 stop_details 字段到 message_delta** - providers 层新增 stop_details 字段支持
- **过滤 compaction 流式响应中的 reasoning items (#179)** - Responses compaction 场景过滤推理内容，避免重复输出
- **优化弹窗快捷键提示体验** - 改进弹窗快捷键提示交互
- **Override 有效期标签改为用户易懂的「空闲自动恢复」** - 重写 Override 配置文案，提升用户理解
- **有效期选择器默认选中 30min + 补充长时段选项** - 优化有效期默认值与选项范围
- **修复磁贴图标透明背景与黄色光圈，补齐 Windows 磁贴资产** - 修复桌面端 Windows 图标透明度问题，补全磁贴资源
- **千帆 Coding Plan 渠道补充模型映射与特性配置** - 完善千帆编码计划渠道模型映射
- **火山方舟 Coding Plan 渠道补充模型映射与特性配置 (#204)** - 完善火山方舟编码计划渠道模型映射
- **替换已归档官方镜像为社区维护 fork，修复 Docker API 版本兼容性问题** - Watchtower 迁移至社区维护镜像，修复 API 兼容性

### 优化

- **前端构建增加双层缓存，避免重复编译** - 前端构建流程新增缓存机制，加速构建

### 改进

- **驾驶舱 Override 有效期可配置** - 支持环境变量 `OVERRIDE_TTL_MINUTES`（默认 30 分钟）和前端下拉选择器（15min/30min/1h/2h/永不恢复）自定义 override 有效期
- **Override Idle 续期** - 对话活跃时自动续期 override TTL，仅在闲置超过设定时间后才恢复默认调度
- **Override 永不恢复选项** - 支持设置 override 永不过期，手动恢复前不会自动过期

### 文档

- **注释说明极易云 originrouter 刻意不纳入 Agent 直连** - 渠道预设注释补充设计决策说明
- **新增 TODO #199 桌面端用量显示与 #179 compaction v2 think 拆分问题** - 更新待办事项
- **完成 TODO #1 OpenRouter 免费路由工具调用排查，确认为上游限制** - 标记已完成任务
- **清理已完成任务并添加三个新 TODO** - 维护任务列表

### 变更文件

- `backend-go/internal/conversation/override.go` - IsPerpetual、per-call duration、RefreshOverrideForUser、SetDefaultTTL
- `backend-go/internal/conversation/override_test.go` - 新增 8 个测试用例
- `backend-go/internal/config/env.go` - 新增 OVERRIDE_TTL_MINUTES 环境变量
- `backend-go/main.go` - 读取环境变量替换硬编码 30 分钟
- `backend-go/internal/handlers/conversation_handler.go` - API body 新增 duration 参数，响应新增 isPerpetual
- `backend-go/internal/scheduler/channel_scheduler.go` - SelectChannel 成功消费 override 时调用 RefreshOverrideForUser
- `frontend/src/components/ConversationDashboard.vue` - 新增 override 持续时间下拉选择器
- `frontend/src/components/ConversationCard.vue` - 永久 override 展示逻辑
- `frontend/src/services/api-types.ts` - SequenceOverrideInfo 新增 isPerpetual
- `frontend/src/services/api.ts` - setConversationOverride 新增 duration 参数
- `frontend/src/i18n/messages-{zh-cn,en,id}.ts` - 新增 4 个 i18n key

## [v2.8.27] - 2026-06-10

### 新增

- **桌面端 MiniMax M3 渠道预设** - 新增 MiniMax M3 channel presets
- **桌面端讯飞星辰 MAAS 直连支持** - 添加讯飞星辰 MAAS 桌面端 Agent 直连支持
- **弹窗快捷键提示与确认交互** - 所有弹窗添加 ESC 取消与 Cmd/Ctrl+Enter 确认快捷键提示，提升操作效率

### 修复

- **Responses serviceType claude 路径认证修复** - serviceType claude 路径正确使用 x-api-key 认证头
- **桌面端 OpenCode 讯飞星辰直连精确移除** - 仅移除 OpenCode 的讯飞星辰直连，保留 Claude/Codex 路径
- **Fable reasoning 迁移补齐 model mapping** - migrate fable reasoning mapping with model mapping
- **REWRITE_RESPONSE_MODEL 支持 Responses API** - Responses API 场景正确支持 REWRITE_RESPONSE_MODEL 重写

### 重构

- **统一 codex responses 预设 source key** - 统一 codex responses 预设 source key 为 codex/gpt/mini

### 其他

- **Responses provider 认证头 serviceType 分支测试** - 添加 Responses provider 认证头 serviceType 分支测试用例

## [v2.8.26] - 2026-06-10

### 新增

- **Claude Fable 模型支持** - 新增 Claude Fable 系列模型映射与兼容处理，升级时自动补齐 Fable 模型映射配置
- **渠道级主动限速与上游 rate limit header 自动追踪 (#190)** - 新增渠道级令牌桶限速、并发控制与 cooldown 机制，自动解析上游 `X-RateLimit-*` / `Retry-After` 响应头并动态调整限速参数，MiMo 默认 RPM=80
- **历史图片轮次限制与占位符替换** - 新增渠道级历史图片轮次限制配置（`historicalImageTurnLimit`），超过指定轮次的历史对话图片自动替换为 `[Image]` 占位符，避免不必要的 vision 回退模型切换。覆盖 Claude Messages、OpenAI Chat、Responses API、Gemini 四种协议格式，Web/桌面渠道编辑 UI 支持按渠道配置
- **渠道体验增强与 Messages 渠道级 CCH 开关** - Web/桌面渠道列表新增 15 分钟窗口”缓存写偏高”提醒 badge；桌面端 RunAPI messages 预设默认关闭 `normalizeMetadataUserId`；`stripBillingHeader` 从全局设置下沉为 messages 渠道级开关，默认关闭，并同步补齐 Web/桌面渠道编辑表单与老配置迁移逻辑
- **桌面端 OpenRouter 与 ModelScope 渠道预设** - 新增 OpenRouter、ModelScope 渠道中心预设，并将 Kimi Code Plan 合并入 Kimi 预设作为编码计划条目，添加到 Claude/Codex/OpenCode 直连配置列表
- **桌面端渠道预设扩充** - 新增 RunAPI 赞助商预设、Tencent TokenHub 预设与国内 coding-plan 网关预设，统一 RunAPI 与 Token Plan 文案，优化 RunAPI 渠道预设排序
- **桌面端管理面板 Fuzzy 开关与熔断器配置** - 管理面板工具栏新增 Fuzzy 模式开关和熔断器配置按钮，熔断器配置预设值、slider 范围与 WebUI 同步对齐
- **桌面端 Codex/Responses 分层排障诊断** - 在桌面 Agent 页 Codex 卡片新增分层排障，按顺序检查”Codex 配置一致性 → Responses 渠道可用性 → 最近失败请求”，帮助定位 #156 类”本地网关可达但请求失败”的问题。后端扩展 `AgentConfigStatus`（新增 `authMode`/`configConsistent`/`diagnosticCode`/`diagnosticMessage`），在 `getCodexStatus` 中识别 CCS 等工具导致的 `config.toml` 与 `auth.json` 不一致（缺 key、auth_mode 不匹配、插件模式缺 bearer token、旧式配置残缺、配置污染等）；前端新增 `useResponsesDiagnostics` composable，聚合 Codex 配置层、Responses 渠道层（无渠道/全禁用/无 key/熔断/协议错配）与最近失败日志层（401/403、429、5xx、超时）的诊断结论，并在状态页提供最高优先级问题摘要。第一版仅给出诊断与手动修复建议，不做自动修复。涉及 `desktop/internal/configservice/service.go`、`desktop/frontend/src/composables/useResponsesDiagnostics.ts`、`desktop/frontend/src/components/agent/*`、`desktop/frontend/src/components/status/StatusTab.vue`、`desktop/frontend/src/i18n/messages.ts`、`desktop/frontend/src/types/index.ts`
- **桌面端 dashboard 显示运行态当前渠道** - 桌面端 dashboard 新增运行态当前渠道信息展示
- **简化前端添加渠道流程** - 优化前端渠道添加交互流程

### 修复

- **桌面端 Codex 排障忽略 UI 选择的目标模式** - 排障诊断只检查 config.toml / auth.json 文件自洽性，未考虑用户在 UI 上选择的 Codex 模式（快捷/插件），导致选了快捷模式但磁盘仍是插件模式时误报”配置一致”。修复后前端排障会比较 `status.mode`（磁盘实际模式）与 `codexMode`（UI 目标模式），不一致时优先报模式冲突并提示重新应用。涉及 `desktop/frontend/src/composables/useResponsesDiagnostics.ts`、`desktop/frontend/src/i18n/messages.ts`
- **桌面端 Codex 排障按需触发** - Codex 排障诊断改为按需触发，避免不必要的自动诊断开销
- **桌面端渠道介绍 i18n 对齐** - 补齐渠道介绍缺失的 key 与中文产品描述
- **桌面端 Fuzzy 模式 API 路径补全** - Fuzzy 模式 API 路径自动补全 `/api` 前缀并改为 watch 驱动加载
- **桌面端熔断器配置预设值同步** - 同步熔断器配置预设值、slider 范围与 WebUI 一致
- **桌面端 fetch 网络层错误包装** - 包装 fetch 网络层错误，避免管理面板显示原始 `Failed to fetch` 信息
- **桌面端切换渠道目标时清除成功提示条** - 修复切换渠道目标时残留上一目标操作结果的问题
- **桌面端补齐 claudeProviderKeys 缺失键** - 补齐 claudeProviderKeys 缺失的 openrouter/modelscope 键，修复 TS2345 类型错误
- **桌面端当前渠道显示补上渠道名称** - 修复当前渠道显示缺失渠道名称的问题
- **桌面端渠道预设描述统一** - 以平台身份重写渠道预设描述，统一 MiniMax M3 旗舰描述文案
- **桌面端 agent 配置绑定同步** - 同步 agent 配置绑定关系
- **按 content block index 管理 tool_use 缓冲** - 修复重叠 tool_use 导致 `Content block not found` 错误
- **preflight 对未知 SSE 事件类型增加内容兜底识别** - Responses preflight 阶段对未知 SSE 事件类型增加内容兜底识别逻辑
- **识别 messages→responses 桥接下的上游错误** - 修复 messages→responses 桥接场景下上游错误/失败/reasoning 事件未被正确识别的问题
- **调整熔断器预设超时值** - 均衡档改为 60s/60s/180s
- **渠道级流式超时预设与全局调校台对齐** - 渠道级流式超时预设值与全局调校台保持一致
- **补齐渠道调校台参数同步** - 修复渠道级调校台参数未与全局配置同步的问题
- **优化 RunAPI 渠道预设与排序** - 优化 RunAPI 渠道预设配置与列表排序
- **tune 工具调用空闲处理** - 调整流式工具调用空闲超时参数
- **加固本地 compact prompt** - 加固 Responses compact 本地提示词

### 重构

- **合并历史图片轮次限制到熔断器对话框** - 将历史图片轮次限制配置合并到熔断器对话框中，减少工具栏按钮数量
- **从 EnvTab 移除熔断器配置卡片** - 从 EnvTab 移除熔断器配置卡片，统一由管理面板弹窗管理
- **熔断器设置更名为调校台 Tuning Bench** - 将熔断器设置相关 UI 文案统一更名为”调校台 Tuning Bench”

### 文档

- **上游评估 TODO 更新** - 添加上游评估待办事项，记录 #190 新特性条目与上游协议变更
- **upstream-check TODO 格式对齐** - 对齐上游检查脚本 TODO 分组标题层级与任务格式

### 其他

- **将上游检查脚本迁入技能目录** - 将上游版本检查脚本从 tools/ 迁入技能目录，优化技能目录结构

## [v2.8.25] - 2026-06-07

### 新增

- **RunAPI 赞助商与桌面端直连预设** - 新增 RunAPI 赞助商展示、渠道中心预设与 Claude/Codex/OpenCode 直连配置，支持 Messages、Chat、Responses 三类目标
- **流式 post-commit 阶段畸形工具调用截断策略** - 文本已输出后遇到畸形 `tool_use` 时缓冲并截断工具调用，注入 `end_turn` 与 `message_stop` 正常结束，避免客户端进入无效 `tool_result` 重试
- **Responses 添加 tool_search 工具类型识别与兼容处理** - 新增 tool_search 工具类型的识别与兼容处理逻辑
- **新增 stripImageGenerationTool 渠道开关** - 渠道配置新增 stripImageGenerationTool 开关，支持过滤图片生成工具
- **桌面端同步 stripImageGenerationTool 渠道开关** - 桌面端编辑渠道界面同步支持 stripImageGenerationTool 开关
- **上游版本每日自动检查 skill 与脚本** - 添加上游版本每日自动检查 skill 与脚本
- **渠道管理界面增强与国际化完善** - Web UI 渠道管理界面增强与国际化完善
- **新增流式响应观察者模式** - 流式响应新增观察者模式，支持可插拔的流式处理逻辑
- **上游请求日志接入请求上下文标签** - 为上游请求日志接入 session/round 请求上下文标签
- **新增请求日志上下文模块** - 新增请求日志上下文模块支持 session/round 标签
- **流式响应 TPS 进度记录** - 记录流式响应 TPS 进度
- **流式 200 伪成功检测与 failover 机制** - 新增流式 200 伪成功检测与 failover 机制
- **Desktop 前端熔断器配置新增流式超时滑块** - 桌面端熔断器配置界面新增流式超时滑块
- **Web 前端熔断器配置新增流式超时滑块** - Web 前端熔断器配置界面新增流式超时滑块
- **Gemini/Images 流式 preflight 适配** - 完成 Gemini/Images 流式 preflight 适配
- **Gemini handler 初步接入流式 preflight 检测** - Gemini handler 初步接入流式 preflight 检测
- **Responses handler 流式两阶段 preflight 检测** - Responses handler 接入流式两阶段 preflight 检测
- **Chat handler 流式两阶段 preflight 检测** - Chat handler 接入流式两阶段 preflight 检测
- **Minimax 2.7 思考标签处理升级** - 升级 Minimax 2.7 思考标签处理为协议级提取与原生推理字段转换

### 修复

- **记录缓冲工具事件的原始流日志** - 修复 tool_use 缓冲期间原始 SSE 事件未写入响应日志的问题，确保工具开始、参数增量与结束事件可追踪
- **渠道中心预设顺序与 RunAPI Responses 文案优化** - 保持后端预设顺序避免按 Key 分组导致列表跳位，并为 RunAPI Responses 目标显示原生 Responses 方案文案
- **桌面端切换渠道时清除成功提示与错误状态** - 修复切换渠道时旧的成功提示和错误状态残留的问题
- **修复三个疑似 bug (#162 #187 #188)** - 修复三个疑似 bug
- **延长流式空闲超时限制** - 延长 stream idle timeout limits
- **保留流式 preflight 诊断信息** - 保留 stream preflight diagnostics
- **修复 Windows 图标透明度并补全 Codex 预设模板** - 修复桌面端 Windows 图标透明度并补全 Codex 预设模板
- **添加 guide/ 重定向文件修复 GitHub 链接 404** - 修复 docs/guide 重定向导致的 GitHub 链接 404
- **将完整工具调用视为非空流** - 修复完整工具调用被误判为空流的问题
- **修复 JSON 日志截断破坏 Unicode 字符** - 修复 JSON 日志截断时破坏 Unicode 字符的问题
- **修复跨协议能力测试虚拟协议检测** - 修复跨协议能力测试中虚拟协议检测逻辑
- **修正 MSIX 数据目录显示路径** - 修正桌面端 MSIX 数据目录显示路径
- **流式工具调用使用 idle timeout** - 修复流式工具调用使用正确的 idle timeout
- **分离工具调用流式超时** - 将工具调用流式超时与普通流式超时分离
- **修复 compact 流式转换状态 panic** - 修复 Responses compact 流式转换状态 panic
- **捕捉已提交流式响应断流** - 修复已提交流式响应断流未被捕捉的问题
- **移除流式超时 off 档并为渠道新增超时覆盖开关** - 移除流式超时 off 档并为渠道新增超时覆盖开关
- **修复工具调用流式 preflight 误判** - 修复 Chat 工具调用流式 preflight 误判
- **修复 restoreApiKey 接口请求路径** - 修复桌面端 restoreApiKey 接口请求路径
- **修复编辑渠道对话框拉黑密钥恢复按钮不可点击** - 修复桌面端编辑渠道对话框拉黑密钥恢复按钮不可点击
- **修复编辑渠道对话框拉黑密钥恢复按钮不可点击 (Web)** - 修复 Web 端编辑渠道对话框拉黑密钥恢复按钮不可点击
- **规范化 compact reasoning 输出** - 修复 Responses compact reasoning 输出格式
- **修复频道菜单缺失 i18n** - 修复桌面端频道菜单缺失 i18n 翻译
- **对齐管理面板协议显示名** - 修复桌面端管理面板协议显示名不一致
- **修复 Minimax 2.7 模型 think 标签问题** - 修复 minimax 2.7 模型有 `<think>` 标签的问题

### 重构

- **按语言拆分 i18n 消息文件** - 前端 i18n 消息文件按语言拆分
- **拆分 API 服务层类型和工具** - 前端 API 服务层类型和工具拆分
- **移除 Wails v3 系统通知服务** - 桌面端移除 Wails v3 系统通知服务

### 测试

- **补充 RunAPI 桌面端预设与 Agent 配置回归测试** - 覆盖 RunAPI Messages/Chat/Responses 渠道 payload、官网链接、Claude 状态识别与 Key 复用、Codex 快捷/插件模式、OpenCode 直连配置
- **GPT 类渠道探测模型覆盖 codex-auto-review** - GPT 类渠道探测模型新增 codex-auto-review 覆盖
- **覆盖无模型映射的协议转换测试组** - 新增无模型映射的协议转换测试组覆盖
- **拆分 chat_to_responses 测试文件** - 拆分 converters chat_to_responses 测试文件
- **拆分 configservice 测试文件** - 拆分桌面端 configservice 测试文件
- **拆分 failover 测试文件** - 拆分 common failover 测试文件

### 文档

- **更新 TODO 格式规范与疑似 bug 修复记录** - 更新 TODO 格式规范与疑似 bug 修复记录
- **更新 TODO 完成状态与关键提交记录** - 更新 TODO 完成状态与关键提交记录
- **添加 GPT 类渠道模型测试覆盖待办** - 添加 GPT 类渠道模型测试覆盖待办
- **新增桌面端渠道成功提示清理待办** - 新增桌面端渠道成功提示清理待办
- **精简历史版本记录** - 精简 CHANGELOG 历史版本记录

### 其他

- **新增 Serena MCP 配置与项目记忆** - 新增 Serena MCP 配置与项目记忆
- **升级 Vite 和 Vitest** - 前端升级 Vite 和 Vitest 依赖
- **合并 guide/ 重定向文件修复** - 合并 guide/ 重定向文件修复分支

## [v2.8.24] - 2026-06-04

### 修复

- **修复深层嵌套图片检测遗漏导致视觉回退失效** - 修复嵌套结构中图片 URL 未被识别，导致视觉回退策略无法生效的问题

## [v2.8.23] - 2026-06-04

## [v2.8.22] - 2026-06-04

### 修复

- **修复桌面端管理面板 CORS 认证失败** - 修复 `OPTIONS` 预检请求被认证中间件拦截返回 401，导致桌面端 WebView 无法访问管理接口的问题；`ENABLE_CORS=false` 时仍为带 `Origin` 的请求注入 CORS 响应头
- **version-bump 新增 CHANGELOG 覆盖率校验** - 版本升级流程新增 CHANGELOG 覆盖率校验，防止遗漏提交记录

### 文档

- **补全 v2.8.21 CHANGELOG** - 补全 v2.8.21 版本遗漏的 CHANGELOG 条目
- **新增 Windows 桌面图标透明度待办项** - 新增 Windows 桌面图标透明度相关的待办任务

## [v2.8.21] - 2026-06-04

### 新增

- **桌面端原生管理控制台** - 桌面端原生管理控制台替代 iframe WebUI，完整覆盖 i18n，管理入口统一命名为 Dashboard
- **桌面端渠道管理重构** - 添加渠道简化为快速粘贴模式（支持 Cmd/Ctrl+Enter 创建），编辑渠道弹窗对齐 WebUI 核心交互，新增预设模板、模型拉取、高级选项分组、Base URL 预览、服务类型动态排序、模型映射协议预置列表与 filter chips、API Key 必填校验
- **桌面端渠道操作增强** - 渠道卡片支持置顶置底并增加删除守卫，渠道日志弹窗重构为可展开列表并支持自动刷新，删除渠道时显示 loading 遮罩，模型重定向 target 输入框始终提供候选
- **桌面端会话驾驶舱重构** - 会话驾驶舱重构为瀑布流卡片并对齐 WebUI cockpit，能力测试对话框对齐 WebUI，能力测试多 job 并发轮询修复
- **桌面端 Codex 会话迁移** - 添加 Codex 会话迁移支持，快捷/插件模式切换对齐 WebUI
- **CLI --backupdir 选项** - 新增 `--backupdir` 选项指定配置备份目录
- **CLI 自定义运行时路径** - 支持自定义运行时路径
- **LOG_DIR=none 禁用日志文件** - 支持 `LOG_DIR=none` 环境变量禁用日志文件写入
- **渠道级请求超时配置** - 后端渠道配置新增 `requestTimeoutMs`，非流式上游请求优先使用渠道超时，未设置或为 0 时继承全局 `REQUEST_TIMEOUT`

### 修复

- **messages**: system 角色归一化对所有上游生效
- **日志 store 改用 metricsKey 分桶** - 根治删除渠道后日志串桶问题
- **模型过滤拒绝非法字符** - 模型过滤拒绝顿号等非法字符并按分隔符拆分
- **余额不足/套餐过期自动拉黑** - 补全 403 关键词自动拉黑，改为双词组合匹配覆盖更多上游变体
- **下架已停用 GPT 模型** - 从能力测试候选、模型重定向候选和预设中移除 `gpt-5.2` / `gpt-5.2-codex` / `gpt-5.3-codex`，haiku 映射改为 `gpt-5.4-mini`
- **桌面端编辑渠道大量 UI 修复** - 快捷键与底部操作栏、Esc/Enter 快捷键、窄窗口布局、高级选项平铺 Runtime/Transport 分组、Website 移入连接分组、模型卡片布局、noVisionModels 合并行级 toggle、思考强度/Text verbosity 下拉修复、popover 主题变量补全、移除冗余 Latency 列与重复快捷键提示、保存快捷键统一为 Cmd/Ctrl+Enter
- **桌面端快速粘贴优化** - Textarea 高度修正、自动应用识别结果、自动补全 name/serviceType、按钮提示显示平台对应修饰符、移除 Service Type 识别卡片
- **桌面端 Dashboard 修复** - 移除轮询可见性检查修复渠道列表不自动刷新、管理面板 Tabs 配色对齐深色主题、备用池文案对齐 WebUI、驾驶舱命名对齐并移除手动刷新、网关监控按钮改为「打开管理面板」
- **桌面端模型列表修复** - 拉取补传 key 参数修复 'No API key provided' 错误、源模型列表对齐 WebUI allSourceModelOptions、预设数据与按钮分组完全对齐 WebUI
- **桌面端移除一键配置已下线模板** - 移除 gpt-5.3/gpt-5.2 模板，haiku 改映射到 gpt-5.4-mini
- **桌面端渠道中心恢复原有 ChannelTab 界面** - 移除渠道测速按钮，补齐编辑弹窗快捷键
- **桌面端 i18n 补全** - 编辑渠道界面、编辑弹窗新增 i18n keys 等缺失翻译补全

### 重构

- **桌面端管理入口命名统一** - 管理入口统一命名为 Dashboard，隐藏批量测速按钮，移除 Codex 分组冗余开关
- **桌面端编辑渠道布局对齐** - 请求超时从基础信息移入 Runtime 分组，抽取渠道草稿保存逻辑统一提交路径
- **渠道管理清理** - 移除未使用代码（开启 noUnusedLocals）、删除渠道编辑弹窗冗余元素

### 文档

- **Docker 部署示例修正** - 修正 Docker 部署示例与实际 compose 保持一致
- **LOG_DIR=none 补充文档** - 补充 `LOG_DIR=none` 禁用日志文件文档及测试用例
- **新增 TODO 待办任务文档**

## [v2.8.20] - 2026-06-01

### 新增

- **渠道级请求超时配置** - 后端渠道配置新增 `requestTimeoutMs`，非流式上游请求优先使用渠道超时，未设置或为 0 时继承全局 `REQUEST_TIMEOUT`；五类渠道新增/更新路径均校验负数并支持清空回退。
- **Web UI 渠道请求超时表单** - 渠道编辑表单新增“请求超时 (ms)”输入项，支持编辑态清空恢复继承全局，并在复制渠道、payload 序列化、差异检测和多语言文案中同步该字段。

### 新增

- **桌面端 Codex Agent 配置双模式** - Codex 支持两种 CCX 模式：快捷模式（OpenAI provider + CCX 代理，不迁移会话、不支持插件）和插件模式（CCX 原生 provider + `requires_openai_auth = true`，支持插件但切换需会话迁移提示）。UI 下拉框拆分为两个选项，互切时显示会话迁移警告。
- **Codex 配置改用快捷/插件模式开关** - 桌面端 Codex 配置入口统一改为快捷模式/插件模式开关，明确不同接入路径。
- **Codex provider 块统一使用 `env_key`** - 将 `[model_providers.xxx]` 块中的 `temp_env_key` 统一替换为官方文档字段 `env_key`。
- **桌面端 AgentCard 编辑器打开支持多编辑器选择** - 配置文件和认证文件的"用编辑器打开"按钮现在支持多编辑器场景，通过透明 select 覆盖层提供编辑器选择下拉。
- **渠道中心已保存 key 的 preset 优先排列** - 桌面端渠道中心将已保存 API Key 的 preset 优先展示，便于快速选择常用渠道。
- **已配置 key 的渠道在渠道中心优先展示** - Web 前端渠道中心根据已配置 Key 状态优先排列渠道。
- **Codex 渠道模板新增 codex-auto-review 重定向** - 渠道模板新增 codex-auto-review 重定向能力。
- **新增 WebUI 新用户指引** - WebUI 增加新用户引导说明，帮助首次使用者快速理解渠道配置流程。
- **OpenAI 直连增加 API Key 勾选框** - 桌面端 OpenAI 直连新增「我有自己的 API Key」勾选框。
- **新用户指引添加渠道展示模拟表单** - 新用户指引增加渠道展示模拟表单，更直观展示快速模式字段。
- **Codex 第三方 provider 支持快捷/插件模式切换** - 第三方 Codex provider 支持在快捷模式与插件模式之间切换。

### 修复

- **下架已停用 GPT 模型** - 从能力测试候选、模型重定向候选和预设中移除 `gpt-5.2` / `gpt-5.2-codex` / `gpt-5.3-codex`，haiku 层模型统一替换为 `gpt-5.4-mini`，并为桌面端渠道模板补充旧模型回归检查。

- **浏览器直达移除 `ccx_desktop` 参数** - 修复浏览器直达 WebUI 时残留桌面端参数的问题。
- **默认语言改为中文并增加浏览器语言检测** - 修复默认语言选择逻辑，新增浏览器语言检测。
- **新用户指引协议切换说明拆分为三段** - 调整新用户指引中协议切换说明的展示结构。
- **新用户指引简化标签列表** - 将新用户指引标签列表简化为 Claude / Codex / Gemini 等。
- **新用户指引示例渠道改为 gpt-5.5 中转** - 更新新用户指引中的示例渠道文案。
- **新用户指引删除 quickHint** - 删除 quickHint，改为说明 CCX 自动识别。
- **删除新用户指引模拟表单及关联资源** - 删除模拟表单及其关联 i18n/CSS 资源，简化引导内容。
- **新用户指引添加渠道说明拆分为两段** - 调整添加渠道说明，使内容层次更清晰。
- **渠道置顶后保持渠道顺序** - 修复渠道置顶后顺序被打乱的问题。
- **修正 Codex 认证配置模式** - 修复 Codex 认证配置模式写入与展示不一致的问题。
- **渠道排序以 priority 优先，hasKey 仅作兜底** - 修复渠道排序中 hasKey 权重过高导致 priority 失效的问题。
- **消除 Codex 非 OpenAI provider 时悬空分隔符** - 修复 Codex 非 OpenAI provider 表单中多余竖线分隔符。
- **调整 Linux 数据目录到 XDG state** - 修复 Linux 桌面端数据目录位置，改用 XDG state 路径。
- **掩码 Codex 配置预览密钥** - 修复 Codex 配置预览中密钥未正确脱敏的问题。
- **修正 Codex `auth_mode` 为 `apikey`** - 修正 Codex 配置中的认证模式字段值。
- **OpenAI 直连取消勾选 API Key 时清空残留值** - 修复 OpenAI 直连关闭自带 Key 后仍保留旧值的问题。
- **修正 Codex 配置预览 `auth_mode` 被误掩码** - 修复配置预览中普通字段被误判为密钥的问题。
- **对齐 Codex 插件模式认证配置** - 修复 Codex 插件模式下认证配置与预期不一致的问题。
- **JSON 配置 diff 改为字段级掩码** - 修复 JSON 配置 diff 脱敏粒度过粗导致预览不准确的问题。
- **修正短密钥掩码后 JSON diff 漏报变更** - 修复短密钥脱敏后 JSON diff 无法识别真实变更的问题。
- **按字段掩码 Codex 配置 diff** - Codex 配置 diff 改为按字段掩码，避免误掩码普通配置项。
- **OpenAI 直连自带 Key 必须输入** - 修复 OpenAI 直连勾选自带 Key 时允许空值的问题。

### 重构

- **移除左上角 Logo 点击跳转 GitHub 链接** - 移除 UI 左上角 Logo 的 GitHub 跳转行为。
- **配置预览文件标签改用增删行数统计** - 桌面端配置预览文件标签改为展示增删行数统计。

### 文档

- **更新桌面安装文档** - 更新桌面安装文档，补充 Windows Store 已上架和 Homebrew 安装说明。
- **排障指南添加各入口 curl 快速验证命令** - 为排障指南补充各协议入口的 curl 快速验证命令。
- **排障指南首条添加关闭全局代理和 TUN** - 排障指南首条新增关闭全局代理和 TUN 的排查建议。

### 其他

- **修复 go fmt 格式化缩进** - 修正 Go 格式化后的缩进样式。
- **移除 Claude-Reasoning-Debug 调试日志** - 移除 Claude-Reasoning-Debug 调试日志及 summarizeAssistantReasoningState 函数。
- **精简新用户指引内容** - 移除新用户指引中的状态图例、开关和接入客户端三节。
- **补充 desktop type-check 脚本** - 为桌面前端补充 type-check 脚本。
- **固化 Wails3 CLI 安装版本** - CI 中固定 Wails3 CLI 安装版本，提升构建稳定性。

## [v2.8.19] - 2026-05-30

### 修复

- **补全 MiMo target 描述删除遗漏** - 补全桌面端 MiMo target 描述删除时的遗漏处理。

## [v2.8.18] - 2026-05-30

### 新增

- **按 ReasoningParamStyle 转换客户端原始 reasoning 参数** - 新增按 ReasoningParamStyle 配置项转换客户端原始 reasoning 参数的能力。
- **Passthrough 路径 ReasoningMapping 分支补充 reasoning_effort 样式处理** - 在 passthrough 路径的 ReasoningMapping 分支中补充 reasoning_effort 样式的处理逻辑。
- **桌面端 add Homebrew tap 自动化** - 新增 Homebrew tap 自动化发布流程。
- **桌面端点击版本号触发手动更新检查并计入冷却时间** - 点击版本号可手动触发更新检查，并计入冷却时间避免频繁检查。
- **桌面端 Codex CCX 代理切换为 openai_base_url 官方模式** - Codex CCX 代理从自定义模式切换为 openai_base_url 官方模式。

### 修复

- **渠道中心布局断点降低至 md 并修复窄屏 URL 溢出** - 修复渠道中心在窄屏下的布局问题，将断点降低至 md 并修复 URL 文本溢出。
- **Responses API 返回 426 让 Codex WebSocket 回退到 HTTP POST** - 修复 Responses API 返回 426 状态码时 Codex WebSocket 正确回退到 HTTP POST。
- **修复断裂文档链接，统一 PORT=3688，补充源码构建前置依赖** - 修复文档中断裂的链接，统一 PORT 配置为 3688，补充源码构建的前置依赖说明。
- **MiMo messages 目标描述统一为通用表述** - 统一桌面端 MiMo messages 渠道目标描述为通用表述。

### 重构

- **移除熔断器弹窗标题图标** - 移除熔断器弹窗中的标题图标，简化界面。
- **渠道中心添加目标标签改为产品名称** - 将渠道中心的添加目标标签改为显示产品名称。

### 其他

- **UI 熔断器配置改为预设按钮+滑块交互** - 将熔断器配置改为预设按钮+滑块的交互方式，删除重复的 .env 分组。
- **UI 熔断器参数三列并排布局+间隔线** - 调整熔断器参数为三列并排布局并增加间隔线。
- **UI 预设按钮移到滑块下方，参数列增加内边距** - 将预设按钮移到滑块下方，参数列增加内边距优化间距。

## [v2.8.17] - 2026-05-29

### 新增

- **新增 gpt-5.4-mini 到 codex responses 协议源模型列表** - 前端 codex responses 协议源模型列表新增 gpt-5.4-mini 模型。
- **更新 Gemini 源模型列表** - 更新前端 Gemini 源模型列表配置。
- **更新模型列表并同步前后端** - 更新模型列表并保持前后端配置同步。

### 修复

- **WebUI iframe 认证使用 ADMIN_ACCESS_KEY 而非 PROXY_ACCESS_KEY** - 修复桌面端 WebUI iframe 认证使用错误密钥的问题。

### 其他

- **简化 favicon，替换为极简 CX 描边图标** - 简化 favicon 设计，替换为极简 CX 描边图标。

## [v2.8.16] - 2026-05-29

### 新增

- **添加 DashScope Token Plan 预设** - 桌面端添加 DashScope Token Plan 预设。

### 修复

- **修正渠道一键预设显示** - 修正 UI 渠道一键预设显示问题。
- **复用 Windows 安装器旧安装目录** - 桌面端 Windows 安装器复用旧安装目录。
- **修正 Web Logo 主题配色** - 修正 Web Logo 在不同主题下的配色显示。
- **修正暗色模式 Logo 配色** - 修正桌面端暗色模式下 Logo 配色。
- **修正图标亮暗主题显示** - 修正 UI 图标在亮暗主题下的显示。
- **隐藏桌面 WebUI 版本检测** - 隐藏桌面端内嵌 WebUI 版本检测。

### 其他

- **隔离 backend 端口选择测试避免读取真实 .env** - 隔离桌面端 backend 端口选择测试，避免读取真实 `.env`。

## [v2.8.15] - 2026-05-29

### 新增

- **重设计应用图标与品牌 Logo** - 桌面端应用图标与品牌 Logo 全新设计。

### 修复

- **统一 BuildChannelView 高级开关字段输出** - 将 `normalizeSystemRoleToTopLevel`、`stripEmptyTextBlocks`、`passbackReasoningContent`、`passbackThinkingBlocks`、`injectDummyThoughtSignature`、`stripThoughtSignature` 六个高级开关字段统一收归 `BuildChannelView()` 公共函数输出，消除 messages/chat/responses/gemini 四个 handler 中的重复补丁，确保所有渠道类型的 WebUI 编辑回显与保存一致。
- **移除 PreviewAgentConfigDiff 未使用的 status 变量** - 清理桌面端 PreviewAgentConfigDiff 组件中未使用的 status 变量。
- **Agent 配置应用使用最新 .env 端口** - 桌面端 Agent 配置应用时使用最新的 .env 端口值。
- **统一 Web UI Logo 与应用图标** - 统一 Web UI 中的 Logo 和应用图标，保持视觉一致性。
- **补齐渠道一键模板配置** - 前端补齐渠道一键模板配置功能。
- **调整 Opus 4.8 能力探测请求** - 调整 Claude Opus 4.8 模型的能力探测请求参数。
- **修复预设渠道控制台网址和套餐显示** - 修复预设渠道中控制台网址和套餐信息显示不正确的问题。

## [v2.8.14] - 2026-05-29

### 新增

- **新增 normalizeSystemRoleToTopLevel 开关兼容旧 Claude 上游** - 新增系统角色规范化到顶层字段的兼容开关，便于旧版 Claude 上游适配。
- **统一桌面端与 Web UI 的 MiMo/DeepSeek 渠道预设配置** - 统一桌面端与 Web UI 的 MiMo、DeepSeek 渠道预设，保持跨入口配置一致。
- **补全渠道中心全部预设的中英文 i18n** - 为渠道中心全部预设补齐中英文国际化文案。
- **主题切换改为自动/亮色/暗色三按钮，默认跟随系统** - 桌面端主题切换调整为自动、亮色、暗色三按钮，并默认跟随系统主题。

### 修复

- **修复 Messages 嵌套工具结果图片未触发视觉回退** - 识别 `tool_result.content` 等嵌套 content 数组中的图片块，确保 MiMo 等配置 `visionFallbackModel` 的渠道在历史截图请求中正确切换到识图回退模型，并跳过 `noVision` 渠道。
- **调整主题选项排序** - 调整桌面端主题选项展示顺序，使默认与常用选项更符合使用习惯。
- **修复中文模式下渠道说明显示英文** - 修复中文界面中渠道说明仍显示英文的问题。
- **修复渠道 index 为 0 时能力测试无法启动** - 修复前端能力测试在渠道 index 为 0 时被误判而无法启动的问题。
- **修复桌面端内嵌 Web UI 能力测试无响应的问题** - 修复桌面端内嵌 Web UI 发起能力测试后无响应的问题。
- **修复亮色模式下所有状态色文字对比度不足** - 调整亮色模式状态色文本样式，提升可读性。

### 重构

- **移除浏览器模式的 Wails mock 传输层** - 移除桌面端浏览器模式下的 Wails mock 传输层，简化开发模式通信链路。

## [v2.8.13] - 2026-05-29

### 新增

- **添加 claude-opus-4-8 模型到能力探测与优先级列表** - 前端能力探测与模型优先级排序规则新增 claude-opus-4-8 模型支持。
- **桌面端亮暗色主题切换** - 桌面端新增亮色/暗色主题切换功能，支持用户自定义界面外观。

### 其他

- **桌面端 golang.org/x/sys 提升为直接依赖** - 将 `golang.org/x/sys` 从间接依赖提升为直接依赖，确保依赖关系清晰。

## [v2.8.12] - 2026-05-28

### 修复

- **Linux 桌面构建依赖与运行环境更新** - 更新 CI 中 Linux 桌面构建的系统依赖为 gtk4 与 webkitgtk-6.0，构建运行环境切换至 ubuntu-24.04，确保桌面端在最新 Linux 发行版上正常编译与运行。

## [v2.8.11] - 2026-05-28

### 新增

- **桌面端浏览器直连 Vite dev 端口可走通初始化** - 开发模式下支持浏览器直接连接 Vite dev 端口完成初始化流程（mock Wails IPC），无需通过 Wails 桌面窗口即可调试前端。

### 修复

- **WebUI 快速添加渠道不再误解析配置键** - 修正输入解析，避免 `api_key:`、`url:` 等字段名被识别为 API Key；解析逻辑改为保护 URL 中的冒号，并增加常见配置键名白名单过滤。
- **WebUI 快速添加渠道后新渠道始终置顶** - 修正快速添加流程中渠道定位方式，不再依赖 "index 最大" 的启发式；改为按渠道名称精确匹配新渠道，确保新渠道保持在第一位并触发促销期。
- **Windows 首次启动语言检测按平台分支** - 桌面端分平台检测系统语言，修复 Windows 首次启动时语言显示为英文的问题。
- **Linux AppImage 使用 Ubuntu 22.04 构建以兼容 GLIBC 2.35** - CI 构建环境从新版 Ubuntu 降级到 22.04，确保 AppImage 在较旧 Linux 发行版上正常运行。

### 文档

- **推荐下载卡片增加 gh-proxy 加速下载链接** - 为下载卡片添加 gh-proxy 加速镜像链接，提升国内用户下载体验。
- **站点 hero 区域单行标题与 tagline 扩展** - hero 区域改为单行标题、tagline 扩展上游列表与 env 代码块高亮。

## [v2.8.10] - 2026-05-28

### 新增

- **桌面端多语言切换** - 新增轻量 i18n 框架（zh-CN / en），默认英文并自动跟随系统语言，首次启动按系统语言选择、手动切换后持久化到 `ui-preferences.json`；DesktopService 暴露 `GetLanguagePreference` / `SaveLanguagePreference`；侧栏底部守护面板新增语言切换入口；Sidebar、Setup、Status（含 LogViewer、DiagnosticCard）、Agent（含 ProviderForm、ConfigDiffDialog）、Channel、Env、WebUI 等用户可见文案全部迁移到 `t()`，便于 Microsoft Store Windows 截图审核。
- **桌面端 Agent 配置支持 OpenCode** - configservice 新增 `PlatformOpenCode` 平台、`OpenCodeProxyState`、JSONC provider 块解析与 `auth.json` 读写；默认 ccx provider 走 `http://127.0.0.1:<port>/v1`，可选直连 DeepSeek/MiMo/Compshare/Kimi/GLM/MiniMax/DashScope/OpenCode Zen/Go；写入 `~/.config/opencode/opencode.jsonc` 与 `~/.local/share/opencode/auth.json`；前端 AgentTab/AgentCard/ConfigDiffDialog 渲染 OpenCode 卡片；同步补充中英文 OpenCode 客户端 Desktop 自动配置文档。
- **静默版本检查替代 wails updater 自动弹窗** - 关闭 wails updater 30 分钟自动 `CheckAndInstall`（无差别弹窗，失败场景尤为干扰）；新增 `desktopservice.CheckLatestRelease(force)` 直查 GitHub Releases，过滤 prerelease，服务端缓存 4 小时（错误 30 分钟）；侧栏版本号旁追加「新版 vX.X.X」胶囊，仅有更新时显示，点击打开 release 页（Store 分发不显示）；抽出 `useReleaseCheck` composable（启动 8s 首次检查、之后 4 小时一次）；托盘菜单「检查更新…」入口保留，主动点击仍走完整 wails updater 安装流程。
- **OpenCode 直连仅保留官方原生支持的国产渠道** - OpenCode 卡片下拉移除 MiMo/Compshare/DashScope（OpenCode 官方未原生支持），保留 ccx + DeepSeek/Kimi/GLM/MiniMax + Zen/Go；`useAgentConfig` 同步补齐对应 baseURL 分支。
- **渠道中心 preset 文案 i18n 化并区分 plan 渠道名** - 新增 `translateOrFallback` / `tf` helper，preset/plan/target 文案按 key 翻译、缺失 key 回退到 Go preset 原文；新增 `preset-messages.ts` 仅维护 EN，zh-CN 复用后端中文；channelName 默认值在选中非默认 plan 时追加 plan suffix，避免 MiMo/DashScope 多套餐同名覆盖。

### 修复

- **/v1 等裸 API 前缀不再被 SPA 兜底成 WebUI** - `isAPIPath` 仅匹配带尾部斜杠的前缀（"/v1/" 等），导致 `GET /v1`、`GET /api`、`GET /admin` 这类裸前缀落到 NoRoute 的 SPA 兜底分支返回 `index.html`；改为同时匹配 `path == prefix` 与 `prefix+"/"`，并补上之前漏掉的 `/v1beta`（Gemini 原生格式）；新增表驱动测试覆盖裸前缀、子路径以及 `/v1custom` / `/apifoo` 等非 API 路径。
- **状态页 ServiceDetails 去除与 Sidebar 重叠的 PID/Health 信息** - 移除卡片内 PID 与健康状态行（Sidebar 已展示），同时去掉冗余小标题，避免与左侧栏视觉重叠。
- **统一 Agent 卡片 Provider 下拉文案至 i18n** - Codex 卡片 ccx 选项原使用 `agent.localGateway`（"当前 CCX 网关"），与 Claude/OpenCode 卡片的 `agent.provider.localGateway`（"CCX 本地网关"）不一致；OpenCode 卡片其余选项及"访问官方控制台"按钮、API Key placeholder 也是硬编码中文；统一改用 `agent.provider.*` 与 `agent.openConsole` i18n key。
- **注册 mdi-head-snowflake 图标** - AddChannelModal 的"回传 Thinking Blocks"开关使用 `mdi-head-snowflake`，但 `vuetify.ts` 未导入，运行时图标回退到占位文本；补 `@mdi/js` 导入与 `iconMap` 映射。

### 优化

- **收紧 ServiceDetails 卡片上下内边距** - 桌面端状态页 ServiceDetails 卡片上下内边距收紧，提升信息密度。
- **语言切换合入侧栏底部守护面板** - 桌面端语言切换入口整合到侧栏底部守护面板，与现有偏好控件统一布局。

### 文档

- **新增桌面端教程截图** - 为桌面端使用指南补充压缩后的 CCX Desktop 截图，并将端口引用对齐到默认 3688。

## [v2.8.9] - 2026-05-27

### 修复

- **MiMo 计费目标 URL 不更新** - 修复桌面端 MiMo 计费配置切换后目标 URL 未正确更新的问题。
- **Claude 渠道思考块透传缺失** - 修复 Claude 渠道返回 reasoning/thinking blocks 时未按配置回传的问题。
- **Responses 透传渠道预设误展示 gpt-5.x 一键设置** - Responses 协议透传渠道隐藏不适用的 gpt-5.x 一键预设，避免误配置。
- **导引完成后默认页签错误** - 桌面端首次导引完成后默认跳转 Agent 配置，而非渠道中心。
- **Sidebar 导入路径错误** - 修复桌面端 `Sidebar.vue` 绑定导入路径使用 `@/bindings` 导致的问题，改为正确的 `@bindings`。
- **Responses 到 Claude 缺少 max_tokens 默认值** - 修复 Responses → Claude 链路缺少 `max_tokens` 默认值的问题。(#133)

### 其他

- **补充 max_tokens 边界测试** - 为 Responses → Claude 转换链路补充 `max_tokens` 边界测试，覆盖默认值与异常边界。

## [v2.8.8] - 2026-05-27

### 新增

- **DashScope Coding Plan 选项** - 桌面端 DashScope provider 新增 Coding Plan 计费选项，Claude 直连 Agent 配置支持 Coding Plan 选择器。

### 变更

- **桌面端自更新从手写 updater 改为 Wails v3 内置 Updater** - 删除 `desktop/internal/updater/` 包（含 macOS DMG 挂载、Windows NSIS 安装器、Linux AppImage nohup 替换等平台安装器，行为模式易被杀软误杀）；移除 `CheckUpdate`/`DownloadAndInstall`/`CancelUpdate` Go 导出方法和 `UpdateInfo` 结构体；改用 Wails v3 `pkg/updater`（GitHub Releases Provider + SHA256SUMS 校验 + 内置更新 UI 窗口 + 30 分钟定时检查）；删除前端 `useUpdater.ts` composable 和 `UpdateDialog.vue` 组件，Sidebar 版本按钮改为纯展示（更新检查通过托盘菜单触发）；Wails v3 依赖从 alpha.95 升级到 alpha.96
- **DashScope 按量付费计划标签对齐 MiMo 规范** - 统一 DashScope 渠道预设的按量付费计划标签显示，与 MiMo 规范保持一致。

### 修复

- **Chat 渠道选择 Responses 上游时请求格式与端点错误** - 修复 Chat 渠道 `serviceType: "responses"` 时仍以 Chat 格式发送到 `/v1/chat/completions` 的问题；新增 Chat↔Responses 双向协议转换（请求：messages→input, max_tokens→max_output_tokens；响应：output→choices），流式 SSE 事件转换（response.output_text.delta→Chat chunk），前端预期请求 URL 同步修正为 `/v1/responses`。(#130)
- **规范化 role 开关提示不够明确** - 明确 i18n 中规范化 role 开关的提示文案，注明 developer 角色转换与国内模型建议。

### 文档

- **添加英文 CCX Desktop 指南和入口链接** - 新增英文版 CCX 桌面端使用指南与文档入口链接。

## [v2.8.7] - 2026-05-27

### 文档

- **完善桌面端用户教程与客户端接入入口** - 补充桌面端使用教程与客户端接入文档入口说明。

### 重构

- **MiMo 渠道预设移除自定义 base URL，优化 token plan 切换与下拉标签** - MiMo 渠道预设不再设置自定义 base URL，同时优化 token plan 切换逻辑与下拉标签显示。
- **go fmt 格式化 channelpreset 缩进对齐** - 对桌面端 channelpreset 模块执行 go fmt 统一格式化。

### 修复

- **含图请求不再覆盖普通文本 Trace 亲和** - 含图请求 failover 到视觉渠道成功后，不再改写同一用户的普通文本 Trace 亲和，避免 DeepSeek 等文本渠道被长时间绕过；`HasImageContentCached` 只读缓存，不影响已有 Images 渠道行为。
- **恢复 BuildPayload Description 写入，移除 DMG 打包错误静默** - 恢复桌面端 BuildPayload 中 Description 字段的写入，移除 DMG 打包流程中对错误的静默处理以便排查问题，同时格式化相关测试文件。

### 其他

- **补充 NoVisionModels 断言覆盖** - 为桌面端 NoVisionModels 相关逻辑补充断言覆盖，增强测试完整性。

## [v2.8.6] - 2026-05-26

### 新增

- **Images 渠道内置模型列表新增 gpt-image-2** - 前端 Images 渠道的内置模型选项新增 `gpt-image-2`。
- **渠道中心 provider 官方控制台链接** - 桌面端渠道中心为各 provider 添加官方控制台访问链接，方便快速跳转管理；badges 与链接布局调整至标题同行，避免错位。
- **macOS DMG 安装包自定义拖拽背景** - 新增自定义 DMG 背景图与安装布局，Applications 快捷方式与 .app 拖拽区对齐 FlyShadow 风格。

### 变更

- **Compshare 渠道预设调整** - 高级模型重定向（opus/sonnet/gpt-5.4）从 deepseek-v4-pro 改为 glm-5.1；Chat/Responses 渠道移除内置模型重定向（用户自行在客户端指定模型）；视觉策略从全渠道 NoVision 改为仅 deepseek-v4-flash 关闭视觉，并设置 MiniMax-M2.7 为视觉回退模型。

### 修复

- **添加渠道不写入描述，新渠道插入列表首位** - 修复添加渠道时描述字段未写入的问题，新增渠道现在默认插入列表首位而非末尾。
- **渠道预设视觉与重定向策略重构** - 统一桌面端渠道预设的视觉（NoVision）与模型重定向配置，修正多个预设中不一致的策略。

## [v2.8.5] - 2026-05-26

### 新增

- **桌面端 Store MSIX 分发支持** - Windows 发布流程新增 Store MSIX 分发配置与打包脚本，为 Microsoft Store 分发补齐 manifest、package 脚本和 release 产物链路。
- **桌面端 Compshare 推广入口** - 桌面端新增 Compshare 推广入口与外链打开能力，便于用户从侧边栏访问相关服务。
- **Compshare Agent 直连支持** - 桌面端 Agent 配置新增 Compshare provider 选项，并同步支持直连配置状态识别与应用。
- **Compshare 渠道预设** - 渠道中心新增 Compshare 预设，补充对应模型映射、服务类型和预设测试覆盖。

### 文档

- **隐私政策说明** - 新增中英文隐私政策页面，并将其接入文档导航。
- **首页下载推荐展示优化** - 调整 README 首页下载推荐展示，并补充 VitePress 自适应下载推荐组件。
- **首页自适应下载推荐** - 文档首页新增根据平台自适应展示推荐下载项的能力。

### 其他

- **合并 Compshare 桌面渠道支持** - 合并 Compshare 桌面渠道相关功能分支，纳入本次发布范围。

## [v2.8.4] - 2026-05-25

### 修复

- **桌面端自动更新前停止后端进程** - 自动更新下载并校验安装包后，会先停止由桌面壳托管的 Go 后端，再启动平台安装器；如果当前后端由外部进程托管或停止失败，则中止更新并提示用户手动停止后重试，避免 Windows 安装时后端二进制被占用导致只更新 UI。

## [v2.8.3] - 2026-05-25

### 修复

- **桌面端重启 CCX 服务后误报启动失败** - 网关监控重启服务时，停止与启动阶段改用独立超时上下文，避免停止耗时挤占启动健康检查时间；同时健康检查成功后清理过期错误状态，避免日志显示已重启成功但状态卡片仍展示启动失败。
- **静默 health 轮询访问日志** - health 检查轮询现在遵循静默轮询日志配置，避免高频健康检查污染访问日志。
- **桌面端窗口拖动时背景闪烁** - 桌面端窗口背板改用不透明背景，降低拖动窗口时的透明闪烁感。
- **Logo 默认动画导致界面闪烁** - 关闭 Logo 默认动画，避免页面初始渲染和切换时出现不必要的视觉闪烁。
- **Responses 单渠道模式未追踪对话数据** - 修复 Responses 单渠道成功请求未写入对话追踪器的问题，使驾驶舱能够正确展示单渠道对话数据。

### 其他

- **补充 Responses 单渠道对话追踪回归测试** - 为 Responses 单渠道成功请求新增 handler 级测试，验证请求会写入 `ConversationTracker` 并生成驾驶舱对话记录，防止 `/conversations` 再次为空。

## [v2.8.2] - 2026-05-25

### 新增

- **桌面端最小化后隐藏 macOS Dock 图标** - macOS 下窗口最小化后同步隐藏 Dock 图标，减少后台运行时的 Dock 干扰。
- **桌面端路径项支持打开目录/编辑器操作** - Agent 相关路径展示新增纯图标操作按钮，可直接打开目录或用编辑器打开配置文件。

### 修复

- **桌面端渠道预设模型重定向键统一为 `gpt-5`** - MiMo、MiniMax、OpenCode Zen、OpenCode Go 的 Chat / Codex Responses 单模型重定向现在统一使用 `gpt-5` 作为源模型键，匹配 Codex / Chat 客户端实际请求模型；同时集中整理渠道预设的模型映射、reasoning 映射和 Codex 兼容开关配置。
- **桌面端中文系统默认语言仍为英文** - 环境配置表单在 `.env` 缺少 `APP_UI_LANGUAGE` 时会读取 WebView 暴露的系统/浏览器语言，中文系统默认填入 `zh-CN`，非中文系统继续默认 `en`，且不会覆盖用户已有 `.env` 设置。
- **桌面端 MiniMax 渠道预设与 Codex 兼容配置不完整** - MiniMax Responses 预设现在使用 `gpt-5 -> MiniMax-M2.7` 模型映射，并启用非标准 Chat 角色归一化；同步补充渠道预设测试对归一化字段的断言。
- **桌面端 Codex 应用 CCX 渠道时 auth 密钥不一致** - Codex Agent 配置现在优先读取桌面 dataDir、项目根目录和 `backend-go` 下 `.env` 中的 `PROXY_ACCESS_KEY`，再回退到进程环境变量或自动生成值，避免 `~/.codex/auth.json` 写入与实际后端配置不一致的密钥；同时增强 `.env` 解析以兼容 `export PROXY_ACCESS_KEY=...` 和等号周围空格。
- **能力测试复制到其他 Tab 时原生协议类型错误** - 当能力测试中某个原生协议测试成功后复制渠道到其他协议 Tab 时，新增渠道的 `serviceType` 现在按成功协议映射为对应原生上游类型（Claude Messages / OpenAI Chat / Gemini / Responses），不再沿用源渠道的协议类型；转换按钮同时保留目标 Tab 与成功协议两个语义，避免复制到目标 Tab 时写入错误上游类型。
- **桌面端初始配置完成状态误读环境变量** - `IsSetupComplete` 现在只读取桌面 dataDir 下的 `.env`，避免进程环境变量或项目根目录 `.env` 让首次启动引导被错误跳过，并补充对应单元测试。
- **桌面端 Agent 状态路径展示溢出** - Agent 卡片中的当前/目标 URL、配置文件和认证文件路径改为固定标签列 + 可换行内容列，保留编辑器打开按钮且避免长路径挤压布局。
- **桌面端网关监控详情字段未对齐** - 网关监控详情展示字段与后端服务状态字段保持一致，避免状态信息展示不完整或错位。
- **桌面端托盘隐藏 Dock 图标时崩溃** - 修复托盘状态下触发隐藏 Dock 图标可能导致桌面端崩溃的问题。
- **桌面端渠道预设刷新按钮点击报错** - 移除无效的刷新预设按钮，避免点击后触发前端异常。
- **桌面端页面内容区宽度受限** - 移除页面内容区 `max-w-5xl` 限制，使桌面端页面能够自适应占满可用空间。

### 文档

- **补充渠道预设模型重定向说明** - 在变更记录中补充渠道预设模型重定向的说明，明确 Codex / Chat 客户端实际请求模型与预设映射关系。
- **调整赞助商展示样式** - 优化 README 中赞助商展示区域的排版与视觉呈现。
- **添加赞助商注册链接** - 在 README 赞助商信息中补充可直接访问的注册链接。
- **添加优云智算赞助商信息** - README 新增优云智算赞助商介绍与展示图片。

### 其他

- **version-bump 增强 CHANGELOG 完整性校验** - 版本升级技能现在会校验从上一版本 tag 到 HEAD 的提交记录，补全或移动遗漏的 CHANGELOG 条目。
- **桌面端 Wails 绑定产物切换为 TypeScript** - 当前工作区包含 `desktop/frontend/bindings/` 下生成绑定从 `.js` 文件删除到 `.ts` 文件新增的变更，需随本轮未提交改动一起审核。

## [v2.8.1] - 2026-05-25

### 修复

- **桌面 App 内嵌 Web UI 删除渠道不生效** (#115) - Wails 3 的 WKWebView 在 iframe 上下文中对原生 `window.confirm()` 不弹对话框、静默返回 `false`，导致删除请求从未发出；替换为 Vuetify `v-dialog` 实现的通用确认框，兼容桌面 iframe 与浏览器双入口
- **LogViewer autoScroll 开关失效，新日志仍强制滚动** - 移除对 `filteredLogs` 的冗余 watch，改为监听 `searchQuery` 仅在用户改变搜索关键字时强制滚动；新日志增加时改走受 `autoScroll` 控制的 `props.logs.length` watch
- **缩短引导界面生成的 PROXY_ACCESS_KEY 长度** - 随机熵从 24 字节降为 8 字节，密钥从 `ccx-` + 48 字符 hex（52 字符）改为 `ccx-` + 16 字符 hex（20 字符）；64 位熵对本地代理网关已足够安全，显著改善初始引导界面的可读性与可复制性

### 文档

- **修正渠道名称说明，反映同名覆盖的实际行为** - 后端 `createChannel` 在检测到同名渠道时会执行 PUT 覆盖更新，原文案 "如重复，CCX 会拒绝创建" 与实际行为不符，容易造成误导

## [v2.8.0] - 2026-05-25

### 修复

- **TestDetectRootDirFallsBackToCwd 在 macOS `/private/var` 符号链接下的误报** - macOS 的 `/var` 是 `/private/var` 的符号链接，`t.TempDir()` 返回前者但 `os.Getwd()` 解析到后者，导致字符串直接比较失败；改用 `filepath.EvalSymlinks` 对齐期望值

### 其他

- **同步 channelpreset 测试期望与当前 preset 实现** - 修复 9 个因测试期望过时导致的失败用例，实现本身正确
  - 移除对 `SupportedModels` 字段的断言：`preset.go` 从未设置该字段（deepseek_chat 与 mimo 共 4 个用例）
  - `glm_chat` / `glm_responses` BaseURL 改为 coding plan 的实际值（`https://open.bigmodel.cn/api/coding/paas/v4#`），匹配 `bestPlanForTarget` 按 plan 数组顺序选第一个非 anthropic plan 的行为
  - `minimax_responses` 的 `CodexToolCompat` / `StripCodexClientTools` 改为 false：`applyTargetDefaults` 显式 override 为 native passthrough 策略，与 DeepSeek 一致
  - `TestBuildPayloadRejectsUnsupportedTarget` 改用 `invalid-target`：kimi preset 已支持 `NativeMessages`，原 kimi+messages 用例失效

## [v2.7.31] - 2026-05-25

### 破坏性变更

- **桌面端 dataDir 路径稳定化，移除 sha1 hash 子目录** - 历史版本将 dataDir 计算为 `{UserConfigDir}/ccx-desktop/{sha1(rootDir)[:10]}`，rootDir 由 `os.Getwd()` 决定，导致从 Dock、Spotlight、终端 `wails3 dev` 等不同方式启动时产生不同 hash 目录（实测一台机器下出现 3 个互不相通的目录），用户的 PROXY_ACCESS_KEY、渠道配置、Agent 配置快照在不同启动方式之间相互不可见
  - 新版本统一使用 `{UserConfigDir}/ccx-desktop/`，与 `bootstrap.log` 同级，dev 与 prod 共用
  - **升级影响**：从旧版本升级后，原 hash 子目录（如 `ccx-desktop/42099b4af0/`）不会自动迁移，用户首次启动会进入空配置状态，需要重新通过引导页设置或手动从旧 hash 目录复制 `.env`、`agent-config-state/`、`.config/` 到新位置
  - 不做自动迁移的原因：无法可靠判断哪个 hash 目录代表"主用户数据"

## [v2.7.30] - 2026-05-25

### 新增

- **桌面端首次启动引导流程** - 强制要求用户在启动 CCX 服务前完成最小可用配置，避免直接进入主界面后调用失败
  - 新增 SetupView 全屏引导页：检测 `.env` 中无 `PROXY_ACCESS_KEY` 时显示，预填随机生成的密钥（`ccx-<48hex>`），支持显示/隐藏、复制、重新生成
  - 完成后自动合并写入 `.env`（保留已有键值与注释），自动启动 CCX 服务并跳转至【渠道中心】标签页
  - 引导页展示 `.env` 文件路径方便后续调试，提示后续可在【环境参数】页继续修改其他字段
  - 新增 `IsSetupComplete` / `GenerateProxyAccessKey` 两个 Wails 绑定方法，前者只读判断密钥存在性，后者仅生成预览不写入文件
  - App 启动时三态渲染（Loading 占位 / Setup 引导页 / 主界面），避免界面切换闪烁

- **桌面端 bootstrap 启动日志** - 在 backend manager 初始化前写入固定位置的 `bootstrap.log`，覆盖 dataDir 计算阶段的启动诊断盲区
  - 新增 `mustGetwd` / `mustExecutable` 辅助函数，记录关键启动阶段
  - 便于 Windows 双击无反应、dataDir 解析异常等场景的故障排查

### 修复

- **桌面端版本号显示重复 `vv` 前缀** - 修复桌面端左下角及更新弹窗版本号显示为 `vv2.7.29` 的问题
  - VERSION 文件已自带 `v` 前缀，前端模板不应再硬编码 `v`
  - 修正 `Sidebar.vue` 左下角版本徽标，以及 `UpdateDialog.vue` 标题处的当前/最新版本对比

- **detectRootDir 向上遍历边界条件死循环** - 修复 `filepath.Dir` 到达根目录后返回自身导致的潜在死循环
  - 改为显式检测 `parent == dir` 终止
  - 新增测试验证 fallback 到 cwd 的行为

### 其他

- **NSIS 构建添加 `/INPUTCHARSET UTF8`** - 修复 NSIS 在非 ASCII 路径下编译失败的问题，并将 `ccx-go.exe` 构建产物加入 `.gitignore`
- **新增 `make install` 命令** - 一键安装根目录、`backend-go/`、`frontend/`、`desktop/`、`desktop/frontend/` 全部依赖

## [v2.7.29] - 2026-05-25

### 修复

- **批量延迟测试在 Chat 标签页报错 `e.forEach is not a function`** - 修复 Chat 渠道批量延迟测试时，前端 `pingAllChatChannels()` 未解包后端 `{"channels": [...]}` 响应导致 `forEach` 调用失败的问题
  - `api.ts` 中 `pingAllChatChannels()` 现在正确提取 `resp.channels` 并映射字段，与 Images/Gemini 处理方式一致

- **第三方 provider 直连使用 ANTHROPIC_AUTH_TOKEN** - 修复桌面端第三方 provider（DeepSeek、MiMo、Kimi、GLM、MiniMax、DashScope、OpenCode）直连 Claude Code 时，API Key 错误写入 `ANTHROPIC_API_KEY` 的问题，改为正确写入 `ANTHROPIC_AUTH_TOKEN`
  - `resolveClaudeProvider` 统一将第三方 provider 的 key 归入 `authToken` 返回值，与 CCX provider 行为一致
  - apply、restore、preview 三条路径均通过同一函数解析，无需额外修改

- **配置预览 Diff 掩码后丢失敏感字段变更** - 修复 Agent 配置预览弹窗中，敏感字段（如 `ANTHROPIC_AUTH_TOKEN`）变更时因掩码为 `***` 导致 diff 无法识别变更的问题
  - 新增 `computeJSONDiffWithMask` / `computeTextDiffWithMask`：先在原始内容上做 LCS diff 正确识别变更类型，再用掩码后的内容填充展示行
  - 更新全部 6 个 preview 函数（Claude/Codex apply & restore）使用先 diff 再掩码的方式
  - 新增 `extractNestedStringValues` 支持从嵌套 map（如 `env` 子 map）提取敏感字段值
  - 新增 4 个单元测试覆盖掩码 diff 核心场景

## [v2.7.28] - 2026-05-24

### 修复

- **Codex 第三方 provider 状态识别与恢复逻辑修复** - 修复桌面端 Codex 直连模式下第三方 provider 的运行状态识别和恢复逻辑
- **Codex Responses 转 Chat 时空 tool_choice 导致上游 400** - 修复当 Codex 请求仅携带非 function 类型 tools（全被过滤）时，`tool_choice` 和 `parallel_tool_calls` 仍被透传给上游 Chat API 导致拒绝的问题
  - `OpenAIChatConverter` 和 `ConvertResponsesToOpenAIChatRequest` 两条路径均增加 tools 存在性检查
  - 无 tools 时自动剥离 `tool_choice` 和 `parallel_tool_calls`，与透传路径 `stripCodexClientOnlyTools` 行为一致

- **WebUI iframe 切换标签时自动刷新** - 修复桌面端切换标签页后 WebUI iframe 白屏的问题，切回 WebUI 标签时自动刷新内容

- **Windows 桌面端双击无反应** - 修复 Windows 生产构建（`-H windowsgui`）下启动失败时错误静默吞掉的问题
  - 添加 Windows MessageBox 弹窗：WebView2 缺失、ccx-go 找不到等致命错误现在会弹窗提示用户
  - 添加顶层 `recover()` 兜底：未处理的 panic 不再导致进程静默退出
  - 添加单实例互斥锁：重复双击时弹窗提示"已在运行中"，检查系统托盘
  - 添加持久化文件日志：`%APPDATA%\ccx-desktop\<hash>\desktop.log` 记录启动日志便于排查
  - NSIS 安装器 `ccx-go.exe` 从可选改为必需，缺失则安装失败

## [v2.7.27] - 2026-05-24

### 新增

- **Codex 直连新增 DashScope / OpenCode Zen / Go 选项** - 桌面端 Codex 直连配置新增 DashScope、OpenCode Zen 和 OpenCode Go 三个新的 provider 选项
- **DashScope chat/responses 预设扩展** - DashScope 渠道预设增加 GPT 模型重定向和 reasoning mapping 支持

### 修复

- **Codex 直连目标 URL 显示** - 修复 Codex 直连时目标 URL 未正确显示各 provider 的 Responses 端点的问题

## [v2.7.26] - 2026-05-24

### 新增

- **多渠道 Messages 原生透传支持** - Kimi、GLM、MiniMax 新增 Messages 原生透传，DeepSeek codex responses 预设默认开启原生工具透传和 role 规范化
- **渠道预设扩展** - 新增 OpenCode Zen/Go 和阿里云 DashScope 渠道预设与 Agent 直连支持
- **Agent 直连选项** - Agent 配置新增 Kimi / GLM / MiniMax 直连选项
- **渠道中心自动覆盖** - 添加到 CCX 时自动覆盖同名渠道配置
- **Web UI 刷新按钮** - 内嵌 Web UI 页面添加刷新按钮
- **Codex OpenAI 切换** - 支持 Codex 无缝切换回 OpenAI provider

### 修复

- **渠道中心文案与 URL** - 修正描述文案错别字，移除 /v1 结尾 base URL 的 # 后缀
- **渠道中心 target 切换** - 修复切换 target 后被级联重置回 messages 的问题
- **桌面端二进制路径** - 修复 ccx-go 后端二进制未打包到 App Bundle 的路径错误
- **vite 插件路径** - 修复 vite 插件路径为绝对路径并更新前端依赖
- **OpenAI provider 配置** - 修复 OpenAI 模式下目标 URL 显示为空，移除无效配置块写入
- **监控面板初始化** - 初始化时预探测二进制路径，避免监控面板显示"未发现"

### 重构

- **Web UI 刷新按钮位置** - 刷新按钮移至标题栏，WebUITab 通过 defineExpose 暴露刷新方法
- **渠道预设精简** - 移除渠道预设中所有 SupportedModels 显式赋值，MiniMax 预设精简默认值
- **ccx-go 查找路径** - 改为向上遍历最多 6 层目录

### 其他

- **Wails 升级** - Wails v3.0.0-alpha.92 升级到 alpha.95

## [v2.7.25] - 2026-05-23

### 修复

- **渠道预设模型映射更新** - 更新桌面端渠道预设的模型映射配置
- **WebUI 默认语言改为中文** - 前端 WebUI 默认语言从英文改为中文
- **Agent 代理配置应用按钮文案统一** - 统一桌面端 Agent 代理配置的应用按钮文案
- **Checksum 文件路径前缀修正** - 去除 CI Release 流程中 checksum 文件的路径前缀

### 其他

- **桌面应用图标资源更新** - 更新桌面应用图标资源文件

## [v2.7.24] - 2026-05-23

### 修复

- **Release workflow Bad credentials 错误** - 修复 CI Release workflow 中 GitHub token 认证失败的问题

## [v2.7.23] - 2026-05-23

### 修复

- **MiMo 不同计费模式支持独立 API Key** - ProviderKeyAsset 的 Assets map 改为 `provider:planID` 复合 key，同一 provider 不同 plan 不再互相覆盖；前端查找保存的 key 时优先匹配 plan-specific key，回退到 provider-only key 兼容旧数据

## [v2.7.22] - 2026-05-23

### 修复

- **桌面端 CCX Provider 应用按钮状态修复** - CCX provider 的应用按钮不再依赖网关在线状态，避免网关未启动时无法操作
- **Release checksum 路径修复** - 修复 Release checksum 生成路径错误导致三个主平台构建失败的问题

## [v2.7.21] - 2026-05-23

### 修复

- **Release checksum 路径前缀修正** - 修复 CI release 流程中 checksum 文件路径前缀错误并精简 finalize job
- **桌面构建 ccx-go 查找路径** - 修正桌面端构建时 ccx-go 后端二进制的查找路径
- **渠道中心 Base URL 过滤** - 桌面端渠道中心按 target 协议过滤 Base URL 选项，避免显示不匹配的选项

### 其他

- **恢复 Wails 3 TypeScript 绑定文件** - 恢复桌面端 Wails 3 自动生成的 TypeScript 绑定文件
- **version-bump 技能文档更新** - 更新 version-bump 技能的 GitHub Actions 集成说明，新增 `gh run list` 查看构建进度命令

## [v2.7.20] - 2026-05-23

### 修复

- **MiMo 渠道模板 Base URL 修正** - 修正 MiMo 渠道模板 Base URL 为 Anthropic 协议入口，同时调整计费模式表单顺序
- **网关监控默认显示配置端口** - 网关监控打开时默认显示 .env 中配置的端口，启动后端时优先使用用户配置的端口
- **Windows Desktop amd64 安装包缺失** - 修复 CI 中 Windows Desktop amd64 安装包在 Release 中缺失的问题
- **macOS 应用图标重新设计** - 重新设计应用图标以适配 macOS Human Interface Guidelines
- **macOS 托盘图标交互修复** - 修复 macOS 托盘图标点击后窗口置顶和位置偏移的问题

## [v2.7.19] - 2026-05-23

### 修复

- **日志文件混入精简格式重复内容** - 修复 logger 将 stdout 精简格式与原始完整 JSON 双重写入日志文件的问题，确保日志文件仅保留原始完整记录

## [v2.7.18] - 2026-05-23

### 修复

- **DMG 安装体验与图标显示** - 改善 macOS DMG 安装包的安装体验与应用图标显示效果

## [v2.7.17] - 2026-05-23

### 修复

- **Task 依赖链 ARCH 传递遗漏** - 修复 CI 中 Task 依赖链未正确传递 ARCH 环境变量的问题

## [v2.7.16] - 2026-05-23

### 修复

- **桌面构建 ARCH 变量未通过 Task 依赖链传递** - 修复 CI 中桌面端构建时 ARCH 环境变量未正确传递到下游 Task 步骤的问题

### 其他

- **版本升级技能新增本地编译验证** - version-bump 技能在创建 tag 前强制执行本地测试和构建验证，编译失败时中止发布流程

## [v2.7.15] - 2026-05-23

### 变更

- **移除后端 OTA 更新机制** - 删除后端 updater 包及相关 API 接口，桌面端更新改为跳转到 GitHub Releases 下载页面

## [v2.7.14] - 2026-05-23

### 新增

- **桌面端 .env 密码字段复制按钮** - .env 配置中密码类型字段（如 `PROXY_ACCESS_KEY`）添加复制按钮，方便用户快速复制密钥值

### 修复

- **密码字段留空时隐藏复制按钮** - 当密码类型字段值为空时自动隐藏复制按钮，避免无效操作

### 变更

- **移除后端 OTA 自动更新** - 删除 `updater` 包、`/api/update/check` 接口及相关配置，桌面端更新改为跳转到 GitHub Releases 下载页面

### 其他

- **Release workflow 桌面构建前置修复** - CI 桌面端构建步骤前确保 `bin` 目录存在，修复构建流程中目录缺失问题

## [v2.7.13] - 2026-05-23

### 新增
- **Linux ARM64 桌面端支持**：CI 新增 `ubuntu-24.04-arm` runner 构建 arm64 AppImage，updater 移除 arm64 限制。
- **前端测试框架**：引入 vitest + jsdom，新增 `env-file.ts` 20 个单元测试。
- **Bindings 同步 CI 守护**：新增 `check-bindings.yml` workflow，PR 修改 Go 接口时自动验证 TypeScript bindings 同步。
- **桌面端 .env 文件外部编辑器打开**：Env 配置页新增"用编辑器打开"按钮，自动检测系统已安装的编辑器（VS Code、Cursor、Sublime Text、Vim 等），支持 `$EDITOR` / `$VISUAL` 环境变量优先。多个编辑器时提供下拉选择。

- **Desktop 内置 ccx-go 后端**：macOS .app、Windows NSIS、Linux AppImage/deb/rpm 安装包现在内置 ccx-go 后端二进制，用户无需单独下载。Desktop 与 ccx-go 共用版本号。
- **Release Sigstore 签名**：
  - 在 CI 发布流程中集成 Sigstore / cosign keyless signing，使用 GitHub Actions OIDC 颁发短期证书，无需管理密钥。
  - 三个构建平台（macOS / Windows / Linux）各自生成平台级 checksums 并签名，`finalize` job 合并为全平台 `checksums.txt` 后再次签名。
  - 新增 `docs/guide/verification.md` 与 `docs/en/guide/verification.md` 验证文档，说明 cosign 安装与签名验证步骤。
  - 现有 `.sha256` sidecar 文件与 updater 行为不变。

### 变更

- **桌面端 Env 配置访问控制置顶**：`PROXY_ACCESS_KEY` 和 `ADMIN_ACCESS_KEY` 配置组在 Env 配置表单中移到首位，便于首次配置时优先设置。
- **补齐桌面端测试覆盖**：新增 `configservice`（19 个纯函数 + 集成测试）和 `backend/manager`（16 个测试）测试文件。

### 修复
- **MSIX 打包缺少 wails.json**：新建 `desktop/wails.json`，修复 `wails3 tool msix --config` 引用不存在的文件。
- **parseEnvContent 兼容性**：Go 端 `.env` 解析支持 `export` 前缀和 `\r\n` 换行符。
- **桌面端 Agent 配置服务初始化错误日志**：`configservice.New()` 失败时不再静默丢弃错误，改为输出 `[Desktop-Init]` 日志便于排查。
- **渠道对话框下拉菜单位置偏移**：修复 Vuetify v-select/v-combobox 在添加/编辑渠道对话框内首次打开时菜单位置计算错误的问题，通过 eager 预渲染与 resize 触发确保下拉菜单正确定位。
- **DeepSeek `user_id` 限速与隔离字段透传**：
  - 让内部会话标识提取支持 Chat 请求体顶层 `user_id`，用于 Trace 亲和性与调度追踪。
  - 补齐 Chat → Claude、Messages → OpenAI/Responses、Responses → Claude 跨协议转换中的 `user_id` / `metadata.user_id` 映射，避免用户隔离标识在协议转换时丢失。
  - 增加相关单元测试覆盖关键转换路径。
- **Responses 转 Claude 上游 reasoning 回传补齐**：
  - 在 Responses 转 Claude/Messages 风格请求且启用 `PassbackReasoningContent` 时，同步补齐 `reasoning_content`，避免 MiMo 等上游跨渠道历史请求缺少 reasoning 回传字段。

## [v2.7.12] - 2026-05-22

### 修复

- **Windows Release 构建 NSIS makensis 不在 PATH** - 修复 CI 中 Windows Release 构建时 NSIS `makensis` 工具不在 PATH 导致构建失败的问题

## [v2.7.11] - 2026-05-22

### 新增

- **渠道预设 Token Plan 下拉显示实际 Base URL** - 桌面端渠道预设 Token Plan 下拉选项现在会显示实际的 Base URL，便于用户识别和选择

## [v2.7.10] - 2026-05-21

### 修复

- **MiMo Messages 渠道流式响应只输出 thinking 的问题**：
  - 停止为缺少真实思考内容的历史 assistant 消息注入 `"(no prior reasoning recorded)"` 占位 `thinking` 或 `reasoning_content`，避免 MiMo 将正式回答续写进假 thinking block，或继续因假回传返回 `reasoning_content ... must be passed back` 400。
  - 为历史 assistant 消息补齐 MiMo 要求的顶层 `reasoning_content` 字段：保留真实 `thinking` 块原文并同步回传到顶层字段，仅移除历史请求中旧版本注入的占位/空 `thinking` 块；若 assistant 历史没有真实思考内容，则补空字符串 `reasoning_content: ""`（保持顶层字段存在）；若历史 assistant 仅剩占位 thinking，则转为中性非空 text，避免空 content 或轮次丢失触发上游校验问题。
  - `thinking -> reasoning_content` 转换改为“保真搬运”：不再对真实思考文本做 trim/拼接改写；当消息已含顶层 `reasoning_content` 时保持原值，避免内容被改写后触发上游回传一致性校验失败。
  - 修复请求预处理误删真实 thinking 的问题：`thinking` 块里 `signature` 为空/null 时仅移除 `signature` 字段，不再删除整块 `thinking`，避免后续 `reasoning_content` 回传被掏空。
  - 增强 failover 非重试判定：当上游 400 的 `error.param`/`error.message` 命中 `reasoning_content in the thinking mode must be passed back` 时，判定为 schema 参数错误，不再继续 Key/渠道级 failover，避免请求漂移到其他渠道与错误熔断。
  - 流式预检测改为只有正式 text 或工具语义内容才视为有效响应；thinking-only 流会被判定为空响应并触发重试/失败，避免静默返回 200。

## [v2.7.9] - 2026-05-20

### 修复

- **Gemini 渠道流式响应下 tool call ID 生成策略优化**：
  - 在 `backend-go/internal/providers/gemini.go` 中，将 Gemini 流式响应下 functionCall 的 tool ID 生成策略从不稳定的索引拼接形式（如 `toolu_%d`）优化为稳定的、带有 `call_` 前缀的 16 字节随机 hex 字符串（通过 `crypto/rand` 生成），使 ID 在跨多轮对话时更具唯一性且对齐主流（如 OpenAI `call_xxx` 风格），避免在复杂 agent 多轮调用中发生冲突。
  - 在 `backend-go/internal/providers/gemini_stream_test.go` 中添加对应的测试断言，验证 tool call ID 的 `call_` 前缀生成逻辑。
- **修复 Gemini 流式响应中 functionCall 场景的 `stop_reason` 映射**：
  - 在 `backend-go/internal/providers/gemini.go` 中，引入 `hasToolUse` 状态标志。当流式响应中检测到 functionCall 时，将 `message_delta` 的 `stop_reason` 正确映射为 `tool_use`（而不是默认的 `end_turn`），确保下游消费端能够正确感知并启动工具调用执行，完全符合 Claude 规范。
  - 在 `backend-go/internal/providers/gemini_stream_test.go` 中新增 `TestGeminiHandleStreamResponse_FunctionCallMapsStopReasonToToolUse` 单元测试，模拟 functionCall 流式响应并验证 `stop_reason` 映射。

## [v2.7.8] - 2026-05-20

### 重构

- **清理 `providers` 包历史 lint 告警**：
  - `backend-go/internal/providers/openai.go` 中 `ConvertToClaudeResponse` 转换 tool_call 入参时，给 `json.Unmarshal` 补上错误检查；解析失败时降级保留 `toolCall.Function.Arguments` 原始字符串，避免静默丢失参数（errcheck）。
  - `backend-go/internal/providers/openai_stream_usage_test.go` 中 `TestOpenAIProvider_ConvertToClaudeResponse_CacheFieldInJSON` 给 `json.Unmarshal` 补上 `t.Fatalf` 错误检查，防止 unmarshal 失败时后续断言基于空 map 假阳性通过（errcheck）。
  - `backend-go/internal/providers/responses.go` 删除未被任何地方调用的 `formatFunctionCallHistory` 函数（13 行）：与配套 `formatFunctionCallOutputHistory` 不对称——`function_call` 分支始终直接保留原始 item，不需要降级为文本消息，该函数是早期设计残留（unused）。
  - `backend-go/internal/providers/claude.go` 中 `ConvertToProviderRequest` 模型重定向条件 `upstream.ModelMapping != nil && len(upstream.ModelMapping) > 0` 简化为 `len(upstream.ModelMapping) > 0`（Go 对 nil map 的 `len()` 定义为 0，gosimple/S1009）。

## [v2.7.7] - 2026-05-20

### 修复

- **`messages` 走 Gemini 上游时 tool_result 函数名错位导致工具调用沉默丢失**：
  - 修复 `backend-go/internal/providers/gemini.go` 的 `convertMessage` 在转换 Claude `tool_result` 为 Gemini `functionResponse` 时，把 `name` 字段直接填成 `tool_use_id` 的问题。Gemini 协议要求 `functionResponse.name` 必须等于前面对应 `functionCall.name`（函数名），否则上游无法匹配到对应的工具调用，会沉默返回空内容（典型表现：MCP / 多轮工具历史在 Gemini 上游被静默丢弃，下游模型回合显示空白）。
  - 新增 `buildToolUseIDNameMap`，在 `convertMessages` 入口先扫一遍 Claude 历史，构建 `tool_use_id → name` 映射；`convertMessage` 接收该映射并在 tool_result 转换时回查正确函数名。查不到映射的孤立 tool_result 回退使用 `tool_use_id` 兜底，避免完全丢字段。
  - 在 `backend-go/internal/providers/gemini_tool_result_test.go` 中将历史 fixture 升级为含 tool_use 的完整对话，并新增针对 id→name 映射回查的断言用例。

### 新增

- **Chat 渠道透传思考回传支持**：
  - 在 `backend-go/internal/handlers/chat/handler.go` 中引入了与 Messages 渠道一致的预处理逻辑，自动清理空 signature 字段和历史畸形 thinking 内容块，预防上游参数校验 400 错误。
  - 在 `buildProviderRequest` 中，当 `PassbackReasoningContent` 关闭时，在发送给 Claude 协议上游前自动剥离历史 thinking 块，避免跨上游复用签名导致 signature 错误。
  - 在 `backend-go/internal/config/config_chat.go`、`channels.go` 和 `channel_metrics_handler.go` 中同步支持了 `PassbackReasoningContent` 字段的更新与返回。
  - 修改了 `frontend/src/components/AddChannelModal.vue`，使得“回传 Reasoning Content”开关在 Chat 渠道且服务类型为 claude 时也能正常显示和配置。
  - 在 `deepseek_thinking_matrix_test.go` 中新增了 `TestChatHandler_PassbackReasoningContent` 测试用例。

### 修复

- **`messages` 走 Gemini 上游时 MCP / 自定义工具历史丢失导致重复调用**：
  - 修复 `backend-go/internal/converters/responses_to_gemini.go` 中 `responsesItemToGeminiContents` 漏掉 `custom_tool_call` 与 `custom_tool_call_output` 两类 item 的问题。归一化阶段会把 MCP 工具（如 `mcp__serena__*`）落到这两种类型，原实现走 switch 的隐式 default 直接丢弃，导致历史里只剩模型空回合，Gemini 视为"工具调用未返回"并不停重复发起同一工具调用。
  - 现在两类 item 分别转为 `model` 角色的 `FunctionCall`（携带 `DummyThoughtSignature`，与 `function_call` 行为一致）和 `user` 角色的 `FunctionResponse`，并对缺 `call_id`/`name` 的孤立项做兜底丢弃，避免构造出 Name 为空的 part 触发上游 400。
  - 在 `backend-go/internal/converters/gemini_responses_roundtrip_test.go` 新增 `TestResponsesToGeminiRequest_PreservesCustomToolCallHistory` 和 `TestResponsesToGeminiRequest_CustomToolCallOutputWithoutCallIDDropped` 两条回归用例。
- **SUBSCRIPTION_NOT_FOUND 余额不足故障转移**：
  - 将 `SUBSCRIPTION_NOT_FOUND` 错误码和 `"no active subscription found"` 错误信息识别为余额不足（`insufficient_balance`），从而正确触发渠道黑名单和故障转移。
  - 在 `failover_test.go` 和 `stream_test.go` 中补充了对应的普通请求与流式请求测试用例。
- **Gemini 渠道 tool_result 数组解析报错**：
  - 修复了 `messages` 接口转上游 `gemini` 类型渠道时，`tool_result` 包含数组（Content Blocks）导致的反序列化报错问题。
  - 将 `GeminiFunctionResponse.Response` 字段类型从 `map[string]interface{}` 变更为 `interface{}`，提高结构体容错性。
  - 在 `backend-go/internal/providers/gemini.go` 的 `convertMessage` 方法中，对 `tool_result` 的 `content` 进行了智能解析和规范化，确保转换后的 `response` 始终是一个符合 Gemini 官方协议要求的 JSON 对象。
  - 新增了 `gemini_tool_result_test.go` 单元测试，覆盖了 `tool_result` 为数组、字符串、JSON 对象等各种场景，验证其能正确转换为 Gemini 期望的格式。
- **Gemini 渠道空 text part 触发上游 400**：
  - 修复了 `messages` 接口转上游 `gemini` 类型渠道时，Claude assistant 消息中常出现的空 text 块（如带 tool_use 时的前置 padding）被无脑翻译为 `{"text": ""}`，被严格按 Gemini protobuf 校验的上游（如 vip.undyingapi.com）判定为 `contents[X].parts[Y].data: required oneof field 'data' must have one initialized field` 返回 400 的问题。
  - 在 `backend-go/internal/providers/gemini.go` 的 `convertMessage` 中处理 `text` 类型 content 时跳过空字符串，确保不向 Gemini 上游下发无意义的空 Part。
  - 在 `gemini_tool_result_test.go` 中新增 `TestGeminiProvider_ConvertMessage_SkipsEmptyTextBlock` 与 `TestGeminiProvider_ConvertMessage_KeepsNonEmptyTextBlock` 两条用例，覆盖空 text 块被剔除与非空 text 块仍保留两种场景。
- **Claude→Gemini provider 转换遵循 thought_signature 渠道开关**：
  - 修复了 `messages` 接口转上游 `gemini` 类型渠道时，Claude 协议本身不携带 thought_signature 字段，导致严格校验的上游（如 vip.undyingapi.com）返回 `Function call is missing a thought_signature in functionCall parts` 400 的问题。
  - 在 `backend-go/internal/providers/gemini.go` 的 `convertToGeminiRequest` 中按上游 `injectDummyThoughtSignature` / `stripThoughtSignature` 开关注入 part 层级的 `thoughtSignature`，与原生 Gemini 入口（`handlers/gemini/handler.go`）的策略对齐：默认不修改、`injectDummyThoughtSignature` 开启时注入 `DummyThoughtSignature`、`stripThoughtSignature` 优先级更高且在该场景为 no-op（Claude 协议本就无签名）。
  - 在 `gemini_tool_result_test.go` 中新增 `TestGeminiProvider_ConvertToGeminiRequest_InjectDummyThoughtSignature`、`TestGeminiProvider_ConvertToGeminiRequest_DefaultNoSignature`、`TestGeminiProvider_ConvertToGeminiRequest_StripThoughtSignatureNoOp` 三条用例。
- **补齐多渠道 thought_signature 字段链路**：
  - `backend-go/internal/config/config_messages.go` 的 `UpdateUpstream` 补充接收 `StripThoughtSignature`（之前漏了，开启了等于无效）。
  - `backend-go/internal/config/config_chat.go`、`config_responses.go` 的 update 函数补充接收 `InjectDummyThoughtSignature` 和 `StripThoughtSignature`，对齐 [[feedback_channel_config_field]] 的"五类渠道更新函数必须同步应用新字段"规则。
  - `backend-go/internal/handlers/messages/channels.go`、`chat/channels.go`、`responses/channels.go` 的 `GetUpstreams` 把这两个字段透出给前端；`handlers/channel_metrics_handler.go` 的 dashboard 端点同步补充。
  - `frontend/src/components/AddChannelModal.vue` 把 `injectDummyThoughtSignature` 开关的显示条件由仅 `props.channelType === 'gemini'` 改为 `(gemini || messages) && form.serviceType === 'gemini'`；`stripThoughtSignature` 开关的显示条件由仅 gemini 改为 `form.serviceType === 'gemini'` 且 channelType 属于 gemini/messages/chat/responses。chat/responses 渠道当前在 handler/converter 层默认无条件注入 dummy 签名，因此前端不暴露 `injectDummy` 开关以避免误导。
- **`maskKey` 短密钥脱敏过度**：
  - 修复了 `backend-go/main.go` 中启动日志 `maskKey` 对长度 ≤ 4 的密钥直接输出 `****`、完全遮蔽用户能识别的字符的问题。
  - 现在对长度 ≤ 3 的密钥保留首字符（如 `abc` → `a****`），长度 4~8 保留首尾字符，长度 > 8 保留前后各 2 字符。

### 新增

- **CCX 桌面外壳 MVP** - 新增 Wails3 桌面外壳，用于将现有 CCX 后端作为核心服务构件进行启动、停止、重启、托盘驻留和状态监控；外壳通过现有 `/health` 探活并在内置标签页中加载 CCX Web UI，避免改动核心代理、调度和现有 Web 管理界面逻辑。
  - 新增 `desktop/` Wails3 项目，包含后端子进程 supervisor、托盘菜单、状态页、内嵌 Web UI 标签页和前端绑定。
  - 根 `Makefile` 新增 `desktop-dev` / `desktop-build`，复用现有前端 embed 与 Go 后端构建流程。
  - 调整 `.gitignore`，保留 Wails `desktop/build/` 源配置，同时继续忽略桌面构建产物。

### 修复

- **桌面外壳 Wails Runtime 422** - 升级 `wails3` CLI 与 `@wailsio/runtime` 客户端到 alpha.92 / alpha.79，匹配 alpha.79 引入的 binding transport refactor（请求改用 JSON body），消除旧 CLI（alpha.40）读 URL query 时的 `missing object value` 422 错误，桌面事件订阅（`desktop:show-tab` / `desktop:tray-error`）和 typed binding 调用恢复正常；同步升级前端工具链 vite 8 / vue-tsc 3 / typescript 6 / vue 3.5。
- **空/畸形 Tool Call 自动重试** - 在 Fuzzy 模式下将空参数或非法 JSON 的 tool/function call 视为空响应并复用现有 failover，降低上游偶发 `Read({})` 等畸形工具调用对下游客户端的影响
  - 影响模块：Messages/Chat/Responses/Gemini 的非流式空响应判定、Messages/Responses 流式预检、Chat 流式写头前缓冲预检

## [v2.7.4] - 2026-05-18

### 修复

- **Mimo thinking mode 兼容性** - 修复 mimo（小米 MiMo）等 reasoning model 上游在多轮对话切换场景下返回 400 "The reasoning_content in the thinking mode must be passed back to the API" 的问题
  - 之前实现将 thinking 块转为顶层 `reasoning_content` 字段（OpenAI 风格），实测 mimo 的 Anthropic 协议下不认顶层 `reasoning_content`，仍返回 400
  - 新实现保留所有原有 thinking 块，对缺少 thinking 块的 assistant 消息注入占位 thinking 块 `{"type":"thinking","thinking":"(no prior reasoning recorded)"}`，让 mimo 通过 thinking mode 校验
  - 仅在渠道开启 `passbackReasoningContent: true` 时生效，对其他渠道无影响
  - 注意：旧对话切到 mimo 时模型上下文缺少真实推理内容，可能出现幻觉/指令遵循下降（mimo 官方公告明示的代价）
- **OpenAI Provider 缓存 Token 丢失** - 修复 OpenAI Provider 在协议转换（OpenAI/DeepSeek → Claude Messages）时丢弃上游 cache usage 字段的问题（#76）
  - 流式：`HandleStreamResponse` 现在从 terminal usage chunk（`choices: []`）中提取 `prompt_cache_hit_tokens`、`prompt_tokens_details.cached_tokens` 等字段，并注入到 final `message_delta.usage` 中
  - 非流式：`ConvertToClaudeResponse` 新增二次 raw parse，将 DeepSeek/OpenAI 格式的 cache 字段映射到 `CacheReadInputTokens`
  - Metrics：`annotatePromptTokensTotalForProvider` 扩展到 OpenAI Provider，确保缓存命中归一化口径与 Responses Provider 一致

## [v2.7.3] - 2026-05-18

### 优化

- **Docker 构建优化** - 使用 Go 交叉编译替代 QEMU 模拟，优化多架构镜像构建流程并增加层缓存，显著缩短 CI 构建时间

### 修复

- **Mimo reasoning_content 回传修复** - 修复 `convertThinkingToReasoningContent` 在将 thinking 块提取为 `reasoning_content` 字段时未从 content 数组中移除 thinking 块的问题；当从 Claude 原生渠道切换到 mimo 渠道时，残留的 thinking 块导致 mimo 上游返回 400 "The reasoning_content in the thinking mode must be passed back to the API" 错误

## [v2.7.2] - 2026-05-17

### 新增

- **缓存读写总统计** - 全局统计图表和模型统计图表新增缓存 Token 读写统计展示
  - 后端：`ModelHistoryDataPoint` 新增 `cacheCreationTokens`/`cacheReadTokens` 字段；`GetModelStatsHistory` 和全局统计的模型分桶聚合逻辑补充缓存 Token 累加
  - 前端 `GlobalStatsChart`：Summary cards 和 compact summary 新增缓存 R/W 统计（有数据时显示）；Tokens 图表视图动态添加 Cache Read/Write 系列线
  - 前端 `ModelStatsChart`：新增 Cache 视图切换，展示按模型分组的缓存 Token 趋势
  - 前端 `api.ts`：`ModelHistoryDataPoint` 类型补充缓存字段

## [v2.7.1] - 2026-05-17

### 新增

- **渠道统计对齐接入点统计** - 渠道 Key 趋势图新增 7d/30d 时间维度，并展示总请求次数、成功率、输入/输出 Token 汇总卡片，与接入点总览统计对齐 (#72)
  - 后端：`HistoryDataPoint` 补充 Token 字段；`MetricsHistoryResponse` / `ChannelKeyMetricsHistoryResponse` 新增 `summary` 汇总；Key 趋势接口移除 24h 上限，支持 30 天范围 SQLite 聚合
  - 前端：`KeyTrendChart` 新增 7d/30d 按钮、summary cards、长时间范围 x 轴日期格式
  - SQLite：新增 `idx_records_api_type_metrics_key_timestamp` 复合索引优化渠道级长范围查询

### 改进

- **Override 熔断自动清除** - 当驾驶舱设置的 override（next channel）序列中所有渠道均不可用（熔断）时，调度器自动清除该 override 而非仅跳过，避免前端 NEXT 标签长期残留
- **渠道熔断状态下发前端** - `GetConversationChannelsByKind` API 返回的渠道信息新增 `circuitOpen` 字段，前端驾驶舱可实时显示渠道熔断状态（FUSED 标记）

### 修复

- **Claude 协议空 Text Block 兼容开关** - 修复严格校验的第三方 Claude 协议上游因 Claude Code 在 `tool_use` 前插入裸空 `{"type":"text","text":""}` 占位块而返回 400 的兼容性问题；新增 Messages 渠道 `stripEmptyTextBlocks` 开关，在转发前按需移除空 text block，并同步接通前端配置、渠道视图与回归测试。
- **Responses SSE keep-alive** - 为 Responses 流式代理增加 SSE keep-alive 机制，每 15 秒向下游发送 `: keepalive` 注释行，防止 DeepSeek 等慢上游思考期间触发 Codex 客户端 idle timeout 断连 (#67)

## [v2.7.0] - 2026-05-17

### 新增

- **Responses Compact 本地压缩** - 当 responses 渠道上游为非原生 Responses 类型（openai/claude/gemini）时，`/v1/responses/compact` 端点自动切换为本地 compact 模式：将对话历史格式化为 transcript，通过现有 converter 管线发送普通请求让模型生成摘要，再包装为 Responses 格式返回。支持流式/非流式跟随客户端、session 历史读取与 compact 结果写回、大输入截断保护。原生 responses 上游若返回 404/405/501 也会自动回退本地 compact
- **SessionManager 新增只读查询与压缩会话创建** - 新增 `GetSessionByResponseID` 通过 responseID 只读查找 session；新增 `CreateCompactedSession` 创建压缩后的轻量会话并记录映射
- **ResponsesProvider 提取公共请求构建方法** - 新增 `ConvertBodyToProviderRequest` 公共入口，接受 bodyBytes 参数复用现有 URL/转换/认证逻辑，供 compact 等场景调用

### 改进

- **Messages 渠道模型列表兼容国内 Claude 协议入口** - 当 `base_url` 以 `/anthropic`、`/claude`、`/messages` 结尾时（如 `https://api.deepseek.com/anthropic`），模型列表获取自动尝试三段候选 URL：当前路径 → 剔除协议尾段 → 纯域名根路径，解决国内服务商模型接口不在兼容协议子路径下的问题。管理端"获取模型"同步适配。

---

## 历史版本（v2.0.0-go ~ v2.6.99）

> 以下为早期版本的里程碑摘要，`v2.7.0` 及之后保留完整条目。更细粒度的历史变更可通过 `git log` 查看。

### v2.6.x（2026-02 ~ 2026-05）

- **渠道能力测试** - 完成 4 协议互转、Codex 自定义工具兼容、RPM/超时/取消/重定向验证、结果缓存与多协议并发轮询。
- **独立 Images 渠道** - 新增 `/v1/images/generations`、`/v1/images/edits`、`/v1/images/variations`，接入独立 failover、metrics、日志和前端管理。
- **熔断器三态状态机** - 引入 `closed` / `open` / `half_open`、指数退避、单探针恢复、持久化和 Dashboard 状态展示。
- **渠道日志生命周期** - 支持 `pending`、`connecting`、`first_byte`、`streaming`、`completed`、`failed`、`cancelled` 全链路追踪与前端实时刷新。
- **会话调度看板** - 新增 Conversation Dashboard、ConversationTracker、OverrideManager 与按对话覆盖渠道序列能力。
- **Responses Compact 本地压缩** - 非原生 Responses 上游自动通过 converter 管线执行本地 compact，并写回轻量会话。
- **Vision 能力路由** - 新增 `noVision`、`noVisionModels`、`visionFallbackModel`，按图片输入动态跳过或降级模型。
- **Thinking / Reasoning 兼容** - 支持 DeepSeek、MiMo、Gemini 等上游的 `thinking`、`reasoning_content`、`thought_signature` 双向转换和回传。
- **Codex 工具兼容层** - 支持 Codex `apply_patch` 代理工具、字符串工具、native tool passthrough、工具调用回写和 `codexToolCompat` 统一开关。
- **模型与路由能力增强** - 支持模型过滤包含/排除规则、`X-Channel` 指定目标渠道、渠道级代理、自定义请求头、多 BaseURL 等价去重。
- **缓存与指标口径统一** - 归一 Responses / Messages 缓存命中率、Token usage、Key / Model 维度统计和历史分桶。
- **自动恢复机制** - 支持 UTC 0/8/16 时段自动恢复黑名单 key、half-open 探测和 suspended 渠道最小激活。
- **前端体验与性能优化** - 完成多语言管理界面、移动端导航、能力测试弹窗重构、SVG/Tooltip 常驻资源收敛和内存优化。
- **关键修复** - 覆盖 Fuzzy 模式 5xx 误判、空响应 failover、metadata.user_id JSON 对象兼容、401 字符串认证错误拉黑、客户端取消误计失败等问题。

### v2.5.x（2026-01）

- **Failover 逻辑模块化** - 提取通用 failover 分类、重试和黑名单判定逻辑。
- **Gemini 兼容增强** - `thought_signature` 可配置，补齐 Gemini CLI tools schema 与 Dashboard 字段。
- **指标记录架构优化** - 修复二次计数、客户端取消计数、换 key 后历史累计、渠道删除后串桶等问题。
- **渠道管理增强** - 支持渠道置顶/置底、前端模型映射智能选择、隐式缓存读取推断。
- **关键修复** - 包括 Gemini functionDeclaration parameters 类型、Models API 日志标签、响应 model 字段改写可配置化。

### v2.4.x（2025-12 ~ 2026-01）

- **Gemini API 完整实现** - 新增 Gemini API 模块、路由、历史指标和前端管理界面。
- **统计图表增强** - 新增全局流量 / Token 图、Key 趋势图、缓存命中率统计和多模型趋势展示。
- **低质量渠道处理机制** - 支持 `lowQuality` 渠道标记、暂停清理促销期和输出验证日志。
- **配置模块化拆分** - 按渠道类型拆分 config 包，减少单文件配置膨胀。
- **多端点调度优化** - 支持多 BaseURL failover、预热排序、非阻塞动态排序和并发竞争修复。
- **关键修复** - 覆盖流式工具调用分裂、空 signature 导致 Claude API 400、内容审核无限重试、403 预扣费降级、SSE 事件完整性等问题。

### v2.3.x（2025-12）

- **渠道级 API Key 策略与多 BaseURL** - 支持多 key / 多 URL 管理、促销期状态展示和延迟测试优化。
- **SQLite 指标持久化** - 引入本地指标存储，补齐 Responses API Token usage 与 Messages 缓存 TTL 细分统计。
- **快速添加渠道** - 支持协议自动检测、引号 / 等号 / 空格分割、`settings.json` 解析和多 Base URL 输入。
- **Models API 端点** - 新增模型聚合查询能力并完善 API Key / Base URL 去重。
- **关键修复** - 包括滑动窗口重建、Responses usage 缺失、缓存 token 检测、total_tokens 零值补全。

### v2.2.x ~ v2.1.x（2025-12）

- **Handlers 模块重构** - 将 handlers 重构为同级子包结构，完善 Stream 错误处理与 CountTokens 安全加固。
- **指标系统重构** - 指标绑定到 Key 级别，修复熔断器、单渠道路径指标和非 failover 错误计数。
- **错误处理与历史数据** - 新增 Fuzzy Mode 错误处理开关、渠道指标历史数据 API、Key 级使用趋势图和失败率可视化。
- **协议兼容增强** - 支持 TransformerMetadata、CacheControl、BaseURL `#` 结尾语义和 FinishReason 统一映射。
- **稳定性修复** - 覆盖请求体大小限制、goroutine 泄漏、数据竞争、优雅关闭、流式日志合成器类型等问题。

### v2.0.x（2025-10 ~ 2025-12）

- **Go 版本初始发布** - 完成核心功能移植、单文件部署和性能提升。
- **核心组件落地** - 引入 ChannelScheduler、MetricsManager、TraceAffinityManager 等基础模块。
