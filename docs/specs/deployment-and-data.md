# 部署、配置、数据与安全

> 状态：现行规格
> 代码基线：`53b79829`

## 1. 目标与范围

CCX 以单个 Go 进程承载嵌入 Web UI、管理 API 和六类代理 API，可运行在二进制、Docker、Desktop 托管或系统服务环境。本文定义运行时路径、环境配置、认证、数据文件、升级和回滚边界。

- 根目录 `VERSION` 是发布版本唯一来源，构建时注入版本、时间和 Git commit；`/health` 用于核对实际实例。
- 环境变量控制进程级行为，`config.json` 控制渠道和运行策略；热重载不能替代 `.env` 修改后的重启。
- 配置、站点账号、SQLite、会话和备份各有明确位置、权限和保留规则，不能用一个“数据目录已备份”概括全部状态。
- 部署或验证不得假定示例端口，不得停止未授权实例；先核对进程 cwd、环境、实际监听和版本。

非目标：不提供多租户用户系统，不承诺精简版直接读取 3.0 权威渠道配置，不在文档任务中自动执行生产迁移或重启。

## 2. 部署与运行时路径

### 启动形态

- 二进制默认从工作目录加载 `.env`，嵌入 `frontend/dist` 并提供单端口服务；`ENABLE_WEB_UI=false` 时只提供 API 和纯 API 根信息。
- Docker 将 `.config` 持久化到 `/app/.config`；宿主机仅本地暴露时使用 `127.0.0.1:hostPort:containerPort`，容器内 `BIND_HOST` 通常保持为空。
- systemd/launchd/NSSM 使用绝对二进制、配置、状态和日志路径；Desktop 的数据目录以 Gateway Monitor 显示为准。
- 开发可由根 Makefile 同时启动前端 Vite 和后端；生产嵌入产物不得手工编辑。

### 路径优先级

| 参数                        | 现行行为                                                                                                             |
| --------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `--config PATH`             | 默认 `.config/config.json`，只改变配置文件位置。                                                                     |
| `--statedir DIR`            | 默认 `.config`；写入 `metrics.db`、`thinking_cache.db`、`conversation_state.json`、`scheduled_recovery_state.json`。 |
| `--logdir DIR`              | 覆盖 `LOG_DIR`；`none`/`null` 禁用日志文件，仅控制台输出。                                                           |
| `--backupdir DIR`           | 默认配置文件同级的 `backups`，只改变配置备份目录。                                                                   |
| `--help/-h`、`--version/-v` | 输出帮助或版本；`ccx version` 等价版本查询。                                                                         |

- `~` 和 `~/...` 按当前用户主目录展开，不支持 `~otheruser`；优先级为 `--logdir` > `LOG_DIR` > 默认 `logs`。
- `site_accounts.json` 始终位于配置文件同目录，不随 `--statedir` 迁移；备份时必须单独包含。
- `.env` 从进程工作目录读取，进程已有环境优先；示例 3688 不是固定默认，未配置 `PORT` 时为 3000。

## 3. 配置、认证与网络安全

### 进程配置默认值

| 配置     | 缺省行为                                                                                                                  |
| -------- | ------------------------------------------------------------------------------------------------------------------------- |
| 服务     | `PORT=3000`，`BIND_HOST` 空表示监听所有网卡；`ENV` 未设置时兼容 NODE_ENV/development，生产部署必须显式 `ENV=production`。 |
| Web/CORS | Web UI 开启，CORS 默认关闭；`APP_UI_LANGUAGE` 支持 zh-CN/en/id 归一化。                                                   |
| 请求     | body 默认 50 MiB；入站读取 60 秒；非流式上游 300000ms，响应头 120 秒，均可按允许范围覆盖。                                |
| 日志     | `LOG_LEVEL=info`，请求和响应日志代码缺省均开启；生产应显式关闭响应日志，轮询日志默认静默。                                |
| 数据     | SQLite 持久化开启，聚合指标保留 366 天，请求历史/Key 操作默认 14 天。                                                     |

### 认证与 TLS

- `PROXY_ACCESS_KEY` 保护代理端点，支持主 Key 和逗号分隔的额外代理 Key；额外 Key 不授予管理权限，也不提供独立用户用量/模型权限。
- `ADMIN_ACCESS_KEY` 保护 `/api/*`、`/admin/*` 和 Web 管理操作。未配置额外代理 Key 时可回退主代理 Key；一旦配置额外 Key，必须设置不同且独立的管理 Key，且各代理 Key 不得重复/使用占位值。
- 生产环境拒绝默认 `your-proxy-access-key`；密钥比较使用常量时间比较。生产不建议 query `key`，上游转发会移除该 CCX 鉴权参数。
- `/health` 无需管理认证，仅暴露健康检查所需信息；管理诊断不返回环境变量和内部路径。
- `ENABLE_HTTPS=true` 时同端口兼容 HTTP/HTTPS；证书和私钥必须成对。未提供证书且 `TLS_AUTO_CERT=true` 时生成仅 localhost/127.0.0.1/::1 的临时自签名证书。
- 客户端严格校验证书时使用 mkcert/企业证书和绝对路径；局域网 IP/主机名必须加入 SAN。
- 出站 URL、代理、重定向和图片下载必须保留当前 SSRF、云元数据、IPv4/IPv6 和大小限制，不因用户配置 BaseURL 而关闭。

## 4. 持久化、备份与升级

| 数据                            | 内容与保留                                                                                        |
| ------------------------------- | ------------------------------------------------------------------------------------------------- |
| `config.json`                   | 六协议渠道、子代理、能力、熔断、fallback 和全局运行配置；写盘 0600，父目录 0700，保存前生成备份。 |
| `backups/config-*.json`         | 每次配置保存前的快照，默认最多 10 份，目录 0700；备份包含敏感 Key，必须同等保护。                 |
| `site_accounts.json`            | 加密站点管理令牌、Key 指纹、接入计划和证据；位于配置目录，原子写入。                              |
| `metrics.db`                    | 聚合指标、熔断、请求历史和 Key 操作；聚合与明细分别按保留期清理。                                 |
| `thinking_cache.db`             | thinking/encrypted reasoning，默认 48 小时，初始化失败降级内存。                                  |
| `conversation_state.json`       | 会话元数据、最近消息/recap 和成功来源；idle 1 小时、expire 24 小时。                              |
| `scheduled_recovery_state.json` | 定时恢复检查点，失败保留旧点用于补跑。                                                            |
| `logs/app.log*`                 | 默认单文件 100MB、10 个备份、30 天、gzip 压缩，可禁用文件输出。                                   |

- 配置加载应用缺省值、UID、serviceType、旧字段和 priority 迁移；保存迁移前先备份。热重载忽略自身写入摘要，外部修改成功后通知运行组件。
- 含 `channelsV3` 或 `channelAuthoritativeVersion` 的 3.0 权威配置直接拒载，保护原文件；当前精简版只接受 2.9 协议渠道格式。
- SQLite 使用增量 schema 迁移和身份键迁移；升级验证使用真实快照副本，不在正式数据库上演练。
- 新字段必须覆盖深拷贝、变更检测、导入/导出、备份恢复和旧配置缺省读取，防止热重载或保存时丢字段。
- 完整迁移包至少包含 config、site_accounts、metrics、thinking、conversation、scheduled recovery 及所需 Agent 状态；只复制 config 不等于完整迁移。

## 5. 状态、异常与回滚边界

- 配置损坏、3.0 权威配置、迁移保存失败或证书配置非法时启动失败并保留原数据，不自动清空重建。
- SQLite 初始化失败可按模块降级内存并记录警告；报表、长期历史和重启恢复能力必须明确降级，不能宣称持久化正常。
- 生产默认密钥、额外代理 Key 缺独立管理 Key 或 Key 重复属于启动配置错误，必须在监听前修正。
- 优雅关闭收到 SIGINT/SIGTERM 后停止 HTTP、排空观测、关闭 Key 操作/路由/指标存储、flush conversation 并停止后台任务； shutdown 有 10 秒上下文和外层 15 秒超时。
- 回滚二进制前核对新配置和数据库是否被旧版本理解；新子代理策略、UID、密文账号和新增持久字段可能需要同步恢复备份。
- 恢复配置备份不自动恢复远端站点已删除 Key、远端分组或客户端 Agent 文件；这些需要各自对账/Restore。
- 日志关闭只影响文件输出，不等于关闭控制台；禁用响应日志也不能追溯删除既有日志，敏感文件轮换和备份仍需访问控制。
- 文档示例中的端口、路径和版本只是示例；升级后必须对目标实例请求 `/health` 并核对实际版本，不能假定新二进制已生效。

## 6. 验收标准

| 场景               | 必须结果                                                                      |
| ------------------ | ----------------------------------------------------------------------------- |
| CLI 路径           | config/statedir/logdir/backupdir 各只影响职责内文件，~展开和 none/null 正确。 |
| 实际端口与健康检查 | 从 cwd/env/监听确认目标实例，/health 版本与新构建一致，不假定示例端口。       |
| 访问密钥矩阵       | 主/额外代理 Key 权限正确，额外 Key 无管理权，缺独立管理 Key 启动失败。        |
| HTTP/HTTPS         | 同端口兼容，证书成对校验，自签名 SAN 正确，非法配置拒绝启动。                 |
| 配置保存与热重载   | 0600/0700、备份上限、原子/副本保存、自写忽略和外部重载正确。                  |
| 2.9 与 3.0 配置    | 2.9 快照迁移保留字段；channelsV3/authoritativeVersion 拒载且原文件不变。      |
| 数据降级与恢复     | SQLite 故障有诊断，内存降级边界明确；各保留期和恢复检查点正确。               |
| 关闭与回滚         | 队列排空、状态 flush、后台停止；旧版本回滚不同时遗留不兼容新配置。            |

部署验收在临时目录和隔离端口执行，使用配置/数据库快照副本；不停止用户实例，不调用生产站点，不修改真实 Agent 配置。

## 7. 实现与测试索引

- 启动、CLI 和关闭：`backend-go/main.go`。
- 环境变量与密钥校验：`config/env.go`及 `middleware/auth_test.go`。
- 配置加载与迁移：`config/config_loader.go`、`config_upgrade_29_test.go`、`config_upgrade_snapshot_test.go`。
- 配置备份：`config/config_loader.go`的 save/backup 实现。
- 指标与历史：`metrics/sqlite_store.go`、`routing_observability.go`及迁移测试。
- Thinking/会话：`thinkingcache/thinking_cache.go`、`conversation/persistence.go`。
- 服务部署：[`docs/service/README.md`](../service/README.md)、根 `Dockerfile`、`docker-compose.yml` 和 `Makefile`。
- 操作配置补充：[`guide/environment.md`](../guide/environment.md)和 [`guide/deployment.md`](../guide/deployment.md)，默认值冲突以代码为准。
