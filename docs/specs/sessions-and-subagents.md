# 会话续接与子代理路由

> 状态：现行规格
> 代码基线：`53b79829`

## 1. 目标与范围

本文定义 Responses 多轮会话、成功来源恢复、Trace 亲和、thinking/encrypted_content 缓存，以及四类文本协议子代理默认模型和独立渠道序列的现行契约。

- 子代理策略只适用于 `messages`、`responses`、`chat`、`gemini`；Images/Vectors 不参与。
- 只有精确识别的子代理才应用独立策略；启发式角色只用于观测，主代理和未知角色保持普通调度。
- Responses 的 `previous_response_id`、reasoning `encrypted_content` 和 sealed 状态要求来源兼容，不能为追求 failover 而跨上游重放无法解密的历史。
- Web/Desktop 共用同一后端策略；驾驶舱及 `/api/conversations` 人工 override 已退役，旧 Web 地址跳转渠道页。

非目标：不按内容自动识别任务难度，不提供每会话临时模型规则、每渠道子代理例外模型或子代理专用 Key，不保证无角色标记客户端会自动进入子代理池。

## 2. 页面与配置契约

文本协议渠道列表提供“普通请求｜子代理”Tab。子代理页包含：

- 默认模型选择/手工输入，空值表示保持原请求模型；模型选项按需加载，失败时保留输入并显示不可用提示。
- 渠道搜索、顺序、参与开关、映射后模型预览、模型不支持警示及共享暂停状态。
- 未启用任何渠道时显示警示但允许明确保存；无渠道时展示空态；旧 `SubagentModel` 显示迁移提示，多个不同旧模型不得静默合并。
- 首次加载显示骨架；错误可关闭；未保存草稿有标识，保存/取消根据 dirty 和运行状态禁用，保存期间锁定编辑并防止重复提交。
- 搜索时禁止拖拽，避免可见子集覆盖完整顺序；Web/Desktop 提供键盘可操作的开关与移动方式，窄屏不依赖横向表格。
- 后台刷新只更新当前可见渠道的信息，不覆盖用户模型、开关、顺序或保存 revision。

配置形如 `subagentRouting[protocol].model` 与有序 `channels[{channelUid,enabled}]`。策略不存在表示未激活并兼容旧行为；用户明确保存后才激活。

管理接口为 `GET/PUT /api/{type}/channels/subagent-routing`：

- GET 返回策略、可见渠道、映射预览、旧模型和 revision，不返回真实 Key。
- PUT 校验 revision、UID 属于本协议且互不重复；过期 revision 返回 409，不能覆盖他人修改。
- 保存以配置锁内副本完成校验和写盘，成功后返回服务端真实视图；新增渠道默认未参与，删除渠道清理引用。
- 已激活协议拒绝旧客户端对旧 `SubagentModel` 的冲突写入；旧驾驶舱及 override API 不再提供。

## 3. 角色识别与独立调度

### 识别门槛

- Codex Responses：`client_metadata.x-openai-subagent` 或 `x-codex-parent-thread-id` 非空，标记 `subagent/exact`。
- Claude Code Messages：具有 Claude Code session 标记且 `X-Claude-Code-Agent-Id` 非空，标记 `subagent/exact`。
- 仅有工具数量和少量 user 消息的 Claude 请求可标记 `heuristic`，但不得改模型或渠道。
- HTTP 与 Responses WebSocket 使用同一提取逻辑；日志同时记录观测角色、置信度和策略是否实际应用。

### 策略执行

1. 请求入口固定配置快照并解析策略；所有候选、重试和日志使用该快照，不混用保存前后版本。
2. 默认模型非空时，以它替代原模型参与白名单、上下文和能力过滤，再经各渠道 `modelMapping` 得出实际模型。
3. 默认模型为空时保留原请求模型及既有候选行为，但渠道仍限于子代理启用集合。
4. 默认模型非空或加密来源保护启用时，不再回退普通模型候选链；失败只在当前子代理池内转移。
5. 候选依次经过参与集合、前缀/模型/上下文/共享运行限制、合法显式渠道、子代理亲和、子代理序列及降级。
6. 单渠道与多渠道路径都必须应用相同角色和模型规则，不能从当前渠道直连绕过策略。

子代理池全部关闭、暂停、不支持模型或能力不足时明确返回无可用渠道，不回退普通池，也不偷偷改用主代理强模型。普通排序、Promotion 和旧 override 不得越出该集合。

## 4. 会话、亲和与续聊运行逻辑

### Responses 会话

- `SessionManager` 维护 responseID → session 映射及完整 Responses items；`previous_response_id` 未命中且流程必须读取本地历史时返回明确错误，不能伪造空历史。
- 会话默认 24 小时未访问过期，最多 100 条消息和 100k tokens，后台每 5 分钟清理；该 SessionManager 为内存状态，重启后不保证恢复。
- 流式路径收集 input/output items，包括完整 `encrypted_content`；即使客户端中途断连也按实现完成必要会话回写，读取会话返回深拷贝。
- 原生续轮保留 `previous_response_id`；跨协议或本地 compact 才从快照重建/压缩历史，辅助调用关联原请求但不重复计为默认业务请求。

### 成功来源与 Trace 亲和

- 成功来源记录渠道 UID、调用时 index/name、请求模型、实际模型、BaseURL 和 serviceType；旧记录允许缺新增字段。
- `conversation_state.json` 持久保存会话标识、标题/最近用户消息、recap、状态和成功来源；默认闲置 1 小时标记 idle、24 小时过期，停止时 flush。
- 普通 Trace 亲和保存在内存，默认 30 分钟无活动过期；主子角色隔离，上下文桶也隔离，避免大上下文来源污染小请求。
- 加密续轮优先恢复原成功渠道和实际模型。来源被关闭、策略不再允许或兼容性无法证明时明确失败，不跨渠道发送密文、不降为普通文本、不静默改发其他模型。

### Thinking 缓存

- `thinking_cache.db` 保存 Claude thinking 和 Responses reasoning 加密快照，默认 TTL 48 小时，可配置 1–720 小时，当前内存索引上限 512 条。
- SQLite 初始化失败时降级为内存缓存并记录警告，不阻断代理；过期条目清理，关闭时释放数据库。
- 只有符合目标上游及会话条件的请求才回填 thinking；已有 thinking 不重复注入，无缓存时不凭空生成。

## 5. 状态、异常与迁移边界

- 未激活策略读取旧渠道 `SubagentModel` 形成草稿：全部为空则保持原模型；唯一非空值可预填；多个不同值必须展示并让用户选择，不按多数覆盖。
- 首次保存才激活新策略。加载失败不得提交空策略；保存失败保留草稿，过期 revision 要求重新加载并合并。
- 新策略不自动启用新增渠道；普通渠道改名、重排、复制和 Key 变更不得改变子代理 UID 序列。
- 普通 `suspended`、Key 禁用、熔断和限速仍是共享硬限制；子代理参与开关与普通请求开关独立。
- 策略修改后新任务使用新配置；已有加密续轮仍须满足来源兼容。不得通过删除会话、密文或成功来源来“修复”无候选。
- 驾驶舱页面、导航、会话级人工排序及 `/api/conversations` 已删除；保留 conversation tracker 仅用于日志关联、成功来源和加密续聊，不代表人工驾驶舱仍存在。
- `conversation_state.json` 含最近消息/标题等敏感元数据，`thinking_cache.db` 含思考或加密推理快照，文件权限、备份和删除必须与业务数据同级处理。
- 回退到不理解新策略的旧版本前必须恢复对应配置备份，否则旧版本可能忽略策略并重新使用主模型。

## 6. 验收标准

| 场景                      | 必须结果                                                                              |
| ------------------------- | ------------------------------------------------------------------------------------- |
| 主代理、精确/启发式子代理 | 只有精确子代理应用策略；HTTP/WS 一致，日志能区分观测角色与实际应用。                  |
| 默认模型与独立序列        | 每渠道发送映射后的子代理模型；跨渠道重试不改回主模型，不越出启用集合。                |
| 全池关闭或能力不足        | 子代理明确失败，主代理正常；恢复参与后按保存顺序执行。                                |
| 保存与并发                | 刷新读回、过期 revision 409、失败保留草稿、后台刷新不覆盖编辑、UID 不串渠道。         |
| 旧配置迁移                | 未保存不改变流量；混合旧模型不自动合并；旧 override 和驾驶舱不再影响请求。            |
| Responses 加密续聊        | HTTP/WS 两轮均恢复成功来源和实际模型；来源不兼容时不跨渠道发送密文。                  |
| 会话与缓存重启            | conversation 成功来源按保留期恢复；内存 Session 不冒充持久；thinking TTL 和降级正确。 |
| Web/Desktop 展示          | 加载、空态、无启用、旧配置、长名称、搜索、窄屏、键盘和保存锁定均可用。                |

真实 Codex/Claude Code 子任务与生产上游验收必须单独记录；mock 上游通过只证明网关规则，不代表客户端识别或供应商兼容性已全部验证。

## 7. 实现与测试索引

- 角色识别：`backend-go/internal/utils/headers.go`。
- 子代理配置/API：`config/subagent_routing.go`、`handlers/subagent_routing_handler.go`及测试。
- 调度与重试：`scheduler/subagent_routing_test.go`、`common/subagent_routing_integration_test.go`。
- Responses 会话：`session/manager.go`、`responses/stream_session.go`、`subagent_continuation_test.go`、`encrypted_affinity_test.go`。
- 成功来源：`conversation/tracker.go`、`persistence.go`及测试。
- Thinking：`thinkingcache/thinking_cache.go`及收集器测试。
- Web 面板：`SubagentRoutingPanel.vue`及组件测试；Desktop 对应面板位于 `desktop/frontend/src/components/`。
