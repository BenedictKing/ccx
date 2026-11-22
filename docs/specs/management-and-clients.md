# Web、Desktop 与客户端接入

> 状态：现行规格
> 代码基线：`53b79829`

## 1. 目标与范围

Web 与 Desktop 共享 CCX 管理 API，提供渠道、站点、观测和运行配置入口；Desktop 额外管理本地后端进程、环境文件和 Claude/Codex/OpenCode 配置。

- Web 是嵌入 Go 二进制的 SPA，Desktop 复用同一后端契约但不隐式复制一套调度规则。
- Desktop 的 Agent 写入必须可预览、可诊断、可恢复，并区分 CCX 代理密钥、上游 Key 和客户端已有凭证。
- 客户端通过六类公开协议接入，Base URL、路由前缀、认证头和本地 HTTPS 以实际运行实例为准。
- 页面契约覆盖正常、加载、空态、错误、未保存、冲突、离线和窄屏状态，不以成功 toast 代替服务端读回。

非目标：不在用户未确认时改写 Agent 文件，不自动重启客户端，不保证第三方客户端后续版本配置 schema 永远兼容，不把 Desktop 变成第二套独立网关。

## 2. 导航与页面契约

### Web

- `/` 重定向 `/channels/messages`；六协议使用 `/channels/{messages|responses|chat|images|vectors|gemini}`，站点接入为 `/site-onboarding`。
- 桌面顶栏平铺协议和站点入口，窄屏使用菜单；认证后提供运行诊断、语言和明暗主题控制。
- `/conversations` 仅保留到 `/channels/messages` 的兼容跳转，不再展示驾驶舱。
- 协议页顺序为全局统计/模型去向、渠道概况、普通或子代理编排；渠道编辑、日志、能力测试、Key 运维和诊断使用对应对话框/侧栏。
- 未认证只显示认证入口；管理请求 401 不清空现有草稿。切换协议应取消或忽略旧请求，避免旧响应覆盖新页面。

### Desktop

侧边栏包含状态、Agent、渠道、站点/订阅、控制台和环境页；Tab 使用 `v-show` 保留内部状态和滚动位置。状态页提供服务状态、端口、运行时长、日志、Web 地址和数据目录；控制台只在可见且后端运行时轮询。

- 首次设置引导生成/保存代理密钥，完成后才能按向导进入服务与 Agent 配置。
- 渠道和站点操作使用管理密钥；打开 Web UI 或创建预设渠道需要服务时，先按 Desktop 管理器启动并报告真实错误。
- 环境页编辑的是 Desktop 数据目录下实际生效的 `.env`，不是源码 `backend-go/.env`；保存后提示重启。
- 关闭主窗口按当前托盘行为保活，退出/Shutdown 才执行管理器停止；自启动、语言和更新入口按分发版本显示。
- GitHub 安装包支持应用内更新，Store/MSIX 由 Store 更新；更新必须校验 SHA256，macOS 打开 DMG、Windows 启动安装器、Linux AppImage 按当前 updater 流程处理。

## 3. Desktop 服务与 Agent 配置

### 本地服务生命周期

- Start/Stop/Restart 调用 Desktop backend Manager 并带上下文超时；Restart 先停后启，任何阶段失败原样返回，不显示假运行状态。
- 状态来自 Manager 与实际后端，而非按钮乐观更新；日志可从本地缓冲读取，Web URL 使用实际协议和端口。
- Desktop 调本机自签名 HTTPS 管理 API 时可使用专用本机 TLS 客户端配置，该例外不得扩散到远端上游。

### Agent 配置闭环

支持 `claude`、`codex`、`opencode`。状态包含 configured、当前/目标 BaseURL、端口匹配、需更新、配置路径、备份状态，以及 Codex config.toml/auth.json 一致性诊断。

1. 读取现有配置和用户选择的 provider/mode；CCX 模式使用实际端口与代理访问密钥，第三方模式使用该上游 Key。
2. 生成 JSON/TOML 文件级 diff，敏感字段脱敏；Codex 的 provider block、BaseURL、API Key 和 auth_mode 变化必须可见。
3. 用户确认后 Apply，保存原始值到 `agent-config-state`；Restore 预览并恢复该状态，不覆盖无关配置块。
4. 应用后重新读取状态并提示重启对应客户端；端口变化标记 needsUpdate，不能仍显示当前配置匹配。
5. Codex 会话迁移是独立显式操作，分别报告文件迁移/跳过/失败及 SQLite 更新，不与普通 Apply 隐式捆绑。

- Claude CCX 配置写入 BaseURL、代理凭证和必要模型/onboarding 字段；Codex quick/plugin 使用 Responses 协议；OpenCode 使用 Chat 入口。
- Provider 预设创建渠道可复用本地保存的上游 Key，保存资产按 provider/plan/usage 区分；管理 API 不返回这些明文给其他页面。

## 4. 客户端接入运行逻辑

| 客户端             | CCX 接入契约                                                                                                       |
| ------------------ | ------------------------------------------------------------------------------------------------------------------ |
| Claude Code        | Base URL 指向实际 CCX 地址，使用 `/v1/messages`；客户端 Key 是 `PROXY_ACCESS_KEY`，模型经 Messages 渠道映射。      |
| Codex CLI/App      | 使用 Responses provider 和 `/v1/responses`；WebSocket/compact/续聊由对应渠道能力决定，不把 Chat 地址当 Responses。 |
| OpenCode           | 使用 OpenAI 兼容 `/v1/chat/completions`；模型和 provider 配置与 Chat 渠道一致。                                    |
| 其他 OpenAI 客户端 | 按能力选择 Chat、Images 或 Embeddings；生成/编辑/变体是不同 Images 路径。                                          |
| Gemini 客户端      | 使用 `/v1beta/models/{model}:action` 和当前支持的代理鉴权头，模型可经 Gemini 渠道映射。                            |

- 路由前缀插在协议路径前，例如 `/{routePrefix}/v1/responses`；客户端配置必须保留完整前缀。
- 本机服务不可达时先检查实际监听、BIND_HOST、端口、HTTP/HTTPS 和证书，再检查 Key；Windows/WSL/Docker 的 localhost 可能需要宿主机地址。
- 模型显示、上下文用量或 token 价格是客户端与网关两侧信息，CCX 的渠道能力只影响选路，不会自动改写客户端本地预算。
- 客户端切换模型或发送探测请求时，网关按专门探针规则处理，不能因空响应误判整个渠道不可用。
- 上游 401/403 与 CCX 401 必须分开排查：前者是渠道 Key，后者是代理/管理访问密钥。

## 5. 状态、异常与兼容边界

- Web/Desktop 对同一配置保存必须处理 401、409、400、网络失败和响应乱序；保存失败保留草稿，过期响应不得覆盖新协议或新编辑。
- 页面后台轮询不得覆盖 dirty 表单；全局统计收起、Tab 不可见或服务停止时停止对应刷新。
- 本地二进制缺失、端口占用、启动超时和进程退出应显示 Manager 错误与日志入口，不能仅无限 loading。
- Agent 配置 JSON/TOML 损坏、路径不可写或备份失败时停止写入并报告文件；部分多文件写入不得伪装全部恢复成功。
- Codex 配置与 auth_mode 不一致时显示 diagnosticCode/message，不通过反复 Apply 掩盖第三方工具或手工修改造成的冲突。
- 自签名证书只用于本机明确场景；Store 与 GitHub 更新渠道不同，不能向 Store 用户展示 GitHub 自动替换流程。
- 桌面和 Web 的协议字段、状态文案、i18n、图标注册同步；长名称、200% 缩放、窄屏和键盘路径不能阻断保存、排序或关闭对话框。
- 真实客户端、原生 Wails 和签名安装包验收分别记录；浏览器组件测试不证明文件写入、托盘、更新和客户端重启已验证。

## 6. 验收标准

| 场景                        | 必须结果                                                                            |
| --------------------------- | ----------------------------------------------------------------------------------- |
| Web 导航与认证              | 六协议、站点、诊断可用；旧 conversations 跳转；401 不清草稿，窄屏菜单可操作。       |
| 配置并发与轮询              | 慢保存、失败、过期响应和后台刷新不覆盖 dirty 草稿或新协议。                         |
| Desktop 服务生命周期        | 启动/停止/重启状态真实，端口和日志正确，错误不无限 loading。                        |
| Agent preview/apply/restore | diff 脱敏且包含 auth_mode/provider 变化，状态读回准确，原配置可恢复。               |
| Codex 一致性与迁移          | config/auth 冲突可见，文件和 SQLite 结果分别报告，不隐式迁移。                      |
| 客户端端到端                | Claude/Codex/OpenCode 分别到达 Messages/Responses/Chat，密钥、模型和 BaseURL 正确。 |
| 更新与分发                  | GitHub/Store 入口不混用，哈希校验失败停止，签名安装包另行验收。                     |
| 可访问性                    | 长名称、空态、窄屏、200% 缩放和键盘可完成核心流程。                                 |

组件测试之外，至少在隔离数据目录完成一次真实管理闭环和本地假客户端请求；涉及用户 Agent 文件的验收只使用临时 HOME/配置副本。

## 7. 实现与测试索引

- Web 导航与控制器：`frontend/src/App.vue`、`router/index.ts`、`composables/useAppController.ts`。
- Web 页面：`ChannelsView.vue`、`SiteOnboardingView.vue`及组件测试。
- Desktop 页面：`desktop/frontend/src/App.vue`、`components/layout/Sidebar.vue`。
- Desktop 服务桥：`desktop/app.go`、`internal/backend/` 的 Manager 实现。
- Agent 配置：`configservice/service.go`、`preview.go`、`claude.go`、`codex.go`、`opencode.go`及测试。
- Provider 预设：`desktop/internal/channelpreset/`和 `desktop/app.go` 的创建流程。
- 客户端接入详细步骤：[`guide/clients/`](../guide/clients/)与 [`guide/desktop/`](../guide/desktop/)，行为冲突以本规格和代码为准。
