# Phase 3c 实施交接文档

> 目标：把本会话（2026-08-10~11）Phase 3c 已完成的工作、暴露的问题、剩余待办、约束与陷阱打包，供另一个 agent 在新会话里继续推进。阅读后应能：
> 1. 在不重新考古代码的前提下理解 ChannelsV3 权威化主线与当前真实状态；
> 2. 知道哪些提交已落地、哪些可参考、哪些必须先解决才能继续；
> 3. 避免重蹈本会话踩过的坑（false-revert、错误镜像、双 agent 冲突、陈旧快照乒乓）。

---

## 1. 项目与模块背景

- **仓库根**：`/Users/petaflops/works/api-gateways/llm-routing/ccx`
- **后端模块**：`backend-go/`，Go 1.25 + Gin
- **后端模块文档**：`backend-go/CLAUDE.md`、`backend-go/README.md`
- **项目根文档**：`CLAUDE.md`、`docs/specs/channel-data-model-v2.md`（v2 主线 spec，所有阶段定义在此）
- **常用命令**（根 Makefile）：
  - `make dev` — 同时启动 frontend dev server + backend 热重载（用户已有此流程）
  - `make generate-preset-manifest` — 同步 `shared/` 预置到 `backend-go/internal/presetstore/embedded/`
  - `cd backend-go && go build ./... && go test ./internal/config/... -count=1` — 本会话标准验证
  - `cd backend-go && go test -race ./internal/autopilot/...` — 锁/并发相关包必跑

## 2. 核心概念与术语

- **ChannelsV3**：渠道多协议聚合的**无损权威形态**。`Config.ChannelsV3 []ChannelV3`，每个 ChannelV3 聚合 1 个物理渠道（可能跨多协议），成员 `ChannelProtocolMember{ Kind, Index, Upstream }` 携带完整 `UpstreamConfig`。`ChannelV3.ChannelUID` 跨协议稳定。
- **六数组**：`Config.Upstream / ChatUpstream / ResponsesUpstream / GeminiUpstream / ImagesUpstream / VectorsUpstream`，每协议一个 `[]UpstreamConfig` 切片，下标 `Index` 是历史 index 语义。
- **运行时权威**：Phase 3c 目标 = 持久化以 `ChannelsV3` 为准（脱敏），运行时把六数组作为 `ChannelsV3` 的内存投影供 index-based API 使用。删除数组字段仅指"不再持久化"，内存投影必须保留（因为 `UpstreamConfig` 是 provider 接口的输入，无法消除）。
- **ManagedAccounts.Credentials**：托管账号的凭证表，`ManagedAccountCredential{ CredentialUID, APIKey }`。`hydrateManagedAccountCredentials` 按 `AccountUID+CredentialUID` 把 Key 注入运行时六数组——这是 Key 闭环的关键。
- **关键函数**：
  - `BuildAuthoritativeChannels(cfg)` — 六数组→ChannelsV3（`channel_authoritative.go:67`）
  - `ApplyAuthoritativeChannels(channels)` / `ApplyAuthoritativeChannelsAsStruct` — ChannelsV3→六数组，按 `Index` 保序
  - `applyAuthoritativeChannelsAsLoadSource(cfg)` — 加载时翻转入口（`channel_authoritative.go:182`）
  - `compareAuthoritativeRoundTrip` — 对账（忽略易变字段：AutoManagedAt / AutoManagedKind / AccountUID / CredentialUID / LogicalChannelUID / LogicalName）
  - `stripManagedChannelSecrets` — 落盘脱敏（清空托管渠道 `APIKeys`、`APIKeyConfigs[].Key`）
  - `syncManagedAccountCredentialsFromChannels` — **本会话新增**（`config_loader.go:881`），落盘前同步 Key 到 ManagedAccounts

## 3. 数据模型与权威结构

### 持久化形态（落盘 JSON）
```text
config.json
├── managedAccounts:  ManagedAccountConfig[]    # Key 持有者
├── channelsV3:       ChannelV3[]               # 权威（脱敏后）
├── channelAuthoritativeVersion: int           # = ChannelV3SchemaVersion
├── (历史) upstream/chatUpstream/.../vectorsUpstream: UpstreamConfig[]  # 旧配置仍写
└── (其他字段)
```

**ChannelsV3 是脱敏形态**：保存时由 `BuildAuthoritativeChannels(&persisted)` 合成，persisted 已 `stripManagedChannelSecrets`，故 ChannelsV3 内不含托管明文 Key。**Key 持有在 `managedAccounts.credentials[].apiKey`**，靠 `CredentialUID` 与渠道的 `APIKeyConfigs[].CredentialUID` 关联。

### 运行时形态（cm.config）
```text
Config
├── managedAccounts:  []                        # 运行时一致
├── channelsV3:       []                        # 权威来源
├── upstream/chatUpstream/.../vectorsUpstream: []  # 内存投影（运行索引 API 用）
└── (其他字段)
```

加载后流程（目标形态，波 1 重做后）：
1. `json.Unmarshal` → 磁盘六数组进入 `cm.config.Upstream` 等
2. 迁移集执行（`ensureChannelUIDs/ensureAccountUIDs/...`）— 在 `cm.config` 上原地改写
3. **`applyAuthoritativeChannelsAsLoadSource`**：用 `ChannelsV3` 重建运行时六数组（覆盖磁盘值），开启翻转
4. **`syncManagedAccountCredentialsFromChannels`** 已在 `saveConfigLocked` 早期同步 Key 到 `ManagedAccounts`；加载时 `hydrateManagedAccountCredentials` 从 `ManagedAccounts` 补 Key
5. 关键 invariant：`cm.config` 运行时六数组 = `ApplyAuthoritativeChannels(cm.config.ChannelsV3)`，经 hydrate 后 Key 也完整

### 严格 / 非严格模式
- `CCX_CHANNEL_AUTHORITATIVE_STRICT=true`：对账失败（ChannelsV3 与磁盘六数组差异超出允许）拒绝启动
- 默认（非严格）：记录诊断后**以 ChannelsV3 为权威覆盖**六数组

## 4. 已落地提交清单（HEAD 之前）

按时间倒序，从最近到本会话起点。**HEAD 之前最后一个相关提交**：`b101275f`（K3-256K 注册，与 Phase 3c 无关但属于本会话产出）。

| 提交 | 内容 | 与 Phase 3c 关系 |
|---|---|---|
| `b101275f` | K3-256K 模型注册（shared + embedded + docs 同步 + Go 端 3 文件） | 无直接关系，可作 K3-256K 注册 PR 提交 |
| `421ae7ac` | `syncManagedAccountCredentialsFromChannels` helper + `saveConfigLocked` 早期两次调用 | **波 1 必备前置**（修 Key 丢） |
| `4998bbb5` | upstreamprobe 火山套餐探针流式 | 无直接关系，独立修复 |
| `2e3f271d` | Revert `ee362ceb`（波 1 运行时权威反转） | 撤销波 1，等方案 1 修复后重做 |
| `ee362ceb` | **波 1 尝试**（已 revert）：移除 `CCX_CHANNEL_AUTHORITATIVE_LOAD` 门控 + 非严格覆盖行为 + hydrate 调用 + 3 测试断言更新 | 参考实现，本次重做时**先 cherry-pick 或参考其 diff** |
| `15e43d3e` | 阶段 1 critical 修复：`compareAuthoritativeRoundTrip` 忽略易变字段 | **必须保留**，波 1 依赖 |
| `2c7fd797` | 修复 new-api 闭包结构 | 无关系 |
| `3db01e0e` | new-api provision per-UID 锁 + AccountUID 收敛 | 无关系 |
| `2ee9187e` | Phase 3c 阶段 1 加载翻转 + 重建不变量（特征开关门控） | **阶段 1 提交**，作为对照 |
| `1ad98c01` | wv0kemhd3 多模块（probe-ledger / status-event / 文档 / link 前端） | 无关系 |
| `e4125cbd` | chore(model-registry) 刷新基准 | 本会话起点 |

**重要**：本会话"先做再发现 bug 再回退再修前置再重试"的循环留下了三个相互纠缠的提交（`ee362ceb` / `2e3f271d` / `421ae7ac`）。接手 agent 不要被回退误导——`421ae7ac` 方案 1 是正确的、可重用的前置工作。

## 5. 当前工作树与未提交状态

`git status --short`（提交交接文档时快照）：
```
 M backend-go/internal/presetstore/embedded/index.json
 M backend-go/internal/presetstore/embedded/index.json.sha256
 M docs/public/presets/index.json
 M docs/public/presets/index.json.sha256
```

这 4 个文件是**纯日期戳 diff**（`dataVersion: v3.0.0+20260810 → v3.0.0+20260811`），是 K3-256K agent 在生成 embedded 时刷的，与 K3-256K 注册无关（实际条目 diff 在 `865619e0 → b696b51d` 那次）。可以 checkout 掉保持工作区干净，或随下次 `make generate-preset-manifest` 自然覆盖。

**无其他未提交改动**——所有 Phase 3c 相关工作都已 commit。

## 6. 已完成 / 进行中 / 待办总表

| 编号 | 任务 | 状态 | 备注 |
|---|---|---|---|
| 1 | 阶段 1 加载翻转 + 重建不变量（CCX_CHANNEL_AUTHORITATIVE_LOAD 开关） | ✅ 完成（`2ee9187e`） | |
| 2 | 阶段 1 critical 修复（误报回退 → 忽略易变字段） | ✅ 完成（`15e43d3e`） | |
| 3 | 方案 1 修 Key 同步（`syncManagedAccountCredentialsFromChannels`） | ✅ 完成（`421ae7ac`） | **波 1 前置** |
| 4 | **波 1 运行时权威反转** | ⏳ 待重做 | 已尝试并 revert（`ee362ceb` / `2e3f271d`），方案 1 已修前置，**可重做** |
| 5 | **波 2 消费者切换**（handlers / scheduler / metrics / healthcheck / autopilot） | ❓ 待评估 | 见 §8 |
| 6 | **波 3 删六数组字段** | ❓ 待评估 | 见 §9 |
| 7 | #8 熔断层 model-circuit 跨协议共享（metrics 改键） | ⏳ 未做 | `model_circuit` 键已改为 `(channelUID, keyHash, model)`，但 app 验证未做 |
| 8 | CapabilityProbeLedger 接入 healthcheck L1/L2 | ⏳ 未做 | autopilot 侧已接，healthcheck 侧 `checkKeyL1/L2` 内部未调 `ClaimProbe` |
| 9 | 新环境变量 `CCX_CHANNEL_AUTHORITATIVE_LOAD/STRICT` 登记到 `.env.example` 与 `docs/guide/environment.md` | ⏳ 未做 | 小任务 |

**待办优先级**：
1. **波 1 重做**（详见 §7）—— 必经前置
2. **波 2 评估**（详见 §8）—— 决定是否要逐文件改
3. **波 3 评估**（详见 §9）—— 与 index 语义绑定，最复杂
4. 其余小项可在波 1/2 完成后顺手清理

## 7. 波 1（运行时权威反转）—— 待重做

### 目标
让 `Config.ChannelsV3` 成为运行时唯一权威，**移除 `CCX_CHANNEL_AUTHORITATIVE_LOAD` 开关门控**，加载后始终从 ChannelsV3 重建运行时六数组。ChannelsV3 与磁盘六数组偏差时：
- 严格模式：拒绝启动
- 非严格模式：以 ChannelsV3 覆盖

### 已具备的前置条件
- `421ae7ac` 方案 1：落盘前同步 Key 到 ManagedAccounts（保证加载 hydrate 有源）
- `15e43d3e` 阶段 1 critical：`compareAuthoritativeRoundTrip` 忽略易变字段
- `2ee9187e` 阶段 1：基础设施（`BuildAuthoritativeChannels` / `ApplyAuthoritativeChannels` / `applyAuthoritativeChannelsAsLoadSource` / 严格/非严格开关）

### 重做步骤（建议路径）
1. **读 `ee362ceb` 提交 diff**：`git show ee362ceb` —— 3 文件、44+/20-，参考其改动
2. **重做方式 A（推荐）—— 直接 cherry-pick**：
   ```
   git revert 2e3f271d   # 撤销 Revert，恢复 ee362ceb
   cd backend-go && go build ./... && go test ./internal/config/...
   ```
   若有方案 1 后续微调需要，cherry-pick 后再做。
3. **重做方式 B —— 手工重做**：照 `ee362ceb` 改 3 文件：
   - `backend-go/internal/config/channel_authoritative.go`：移除 `channelAuthoritativeLoadEnabled()` 门控；非严格模式从"回退磁盘"改为"ChannelsV3 覆盖"
   - `backend-go/internal/config/config_loader.go`：翻转后再调一次 `hydrateManagedAccountCredentials`
   - `backend-go/internal/config/channel_authoritative_load_test.go`：3 个测试断言更新（OrderRestored / DivergenceNonStrictMode / SwitchDisabled）

### 关键代码点（精确行号 / 函数）
- `applyAuthoritativeChannelsAsLoadSource` — `backend-go/internal/config/channel_authoritative.go:182`（移除 186 行 `channelAuthoritativeLoadEnabled()` 检查；非严格从 `return false, nil` 改为继续执行覆盖）
- `loadConfig` 翻转后 hydrate — `backend-go/internal/config/config_loader.go:203` 之后追加 `cm.config.hydrateManagedAccountCredentials()`
- 测试断言更新位置：`channel_authoritative_load_test.go` 中 `TestAuthoritativeLoad_OrderRestored` / `TestAuthoritativeLoad_DivergenceNonStrictMode` / `TestAuthoritativeLoad_SwitchDisabled`（`ee362ceb` 改法可参考）

### 验收标准
- `cd backend-go && go build ./...` 通过
- `go test ./internal/config/ -count=1` 全过（含 `TestAuthoritativeLoad_MigratedChannelNoFalseRollback` 不再误报）
- 端到端：加一个含迁移历史（AutoManagedKind 空）的渠道 → `SaveConfig` → 重载 → 加载日志含"已从 ChannelsV3 重建六数组"+ 渠道字段（含 `AutoManagedKind="new_api"`）正确恢复
- 端到端：加一个带 Key 的托管渠道（AccountUID + ProviderID）→ save → 重载 → 加载后 `cm.GetConfig().Upstream[0].APIKeys` 非空（验证方案 1 + 波 1 联动）

### 关键陷阱
- **不要试图让 ChannelsV3 不脱敏**：违背安全设计意图
- **不要回退 `15e43d3e`**：它是对账逻辑，移除会引发误报
- **不要修改 `421ae7ac` 的 sync 顺序**：必须在 `stripManagedChannelSecrets` 之前调，否则 ManagedAccounts 仍空

## 8. 波 2（消费者切换）—— 评估

**预期**：38 文件 / ~280 处 `cm.config.XXXUpstream` / `cfg.XXXUpstream` 改为读"ChannelsV3 投影"。

**评估结果：波 2 可能不需要改任何文件。**

理由（基于波 1 已完成的前提）：
1. `cm.GetConfig()` 返回 `cm.config` 的深拷贝（`config.go:1330`）
2. 波 1 后 `cm.config.Upstream` 等六数组字段**已是 ChannelsV3 的运行时投影**（通过 `applyAuthoritativeChannelsAsLoadSource` 写入）
3. 所有消费点（`scheduler/select.go` / `handlers/messages/handler.go` / ...）读的就是这 6 个字段，**读取语义不变**

**结论**：波 2 不需要大规模重构，只需**验证**（可能需跑 `make dev` + 实测，观察路由/调度/健康检查是否正常）。

**若要真正切换**（让消费点显式读投影而非隐式共享），最小改动方案：
- 新增 `ConfigManager.UpstreamsForKind(kind string) []UpstreamConfig` 访问器
- 逐包替换 38 个消费点（纯机械，但工作量大、收益有限）

**建议**：先做波 1，验证 `make dev` 下系统正常，再决定波 2 是否需要——**先求对，再求显式**。

## 9. 波 3（删六数组字段）—— 评估

**期望终态**：`Config.Upstream / ChatUpstream / ... / VectorsUpstream` 字段不再持久化（`json:"-"`），内存投影仍存在供 index-based API 使用。

**最大障碍：index 语义贯穿一切。**

- 管理 API（`/api/messages/channels/:id` 等）用 `:id` 数字（数组下标）寻址
- 60+ 个写方法（AddUpstream / UpdateUpstream / RemoveUpstream / Reorder / SetStatus / APIKey 管理）全部按 index 寻址
- `scheduler/select.go:1383` 等用 `cfg.Upstream[id]` 直接取渠道

**真正"删字段"意味着 index 语义彻底改变**——要么：
- 改 API 为 ChannelUID-based（破坏前端契约）
- 保留内存六数组作投影（则 `json:"-"` 是唯一可做的"删"）

**实际可做范围**：
- 把 `Config` 上 6 个数组字段加 `json:"-"` tag（停落盘）
- 写方法逻辑不变（仍改内存数组，由 save 时通过 `BuildAuthoritativeChannels` 投影到 ChannelsV3 落盘）
- 旧配置（无 ChannelsV3）通过现有迁移路径处理（`applyAuthoritativeChannelsAsLoadSource` 在 ChannelsV3 为空时跳过，磁盘六数组仍读入内存作投影）

**评估结论**：
- 严格"删数组"在保留 index API 下做不到（只能 `json:"-"`，不能删字段）
- 推荐波 3 形态：六数组字段改 `json:"-"` + ChannelsV3 唯一持久化
- 工作量：1-2 个文件小改 + 旧配置加载路径验证

**建议**：波 3 与波 1 合并实施（同一个提交，因为 `json:"-"` + 加载时投影是反转的自然延伸）。

## 10. 约束、陷阱与已学到的教训

### 通用工程约束（CLAUDE.md 兜底）
- `git push` 任何远端、reset --hard / rebase / amend / cherry-pick / 删除分支、rm -rf、改 env/权限、调生产 API、global install、数据库删除——**必须用户显式确认**
- git commit 默认自动；commit 用 `git commit -m "..."`，多行用多个 `-m` 拼接
- 直接传参 `git commit -m`，不要写 `.git/COMMIT_EDITMSG` 再 `-F`
- 大文档分步写入：先骨架再 Edit 逐章填充，单次 Edit 不超过 50 行

### Phase 3c 特有陷阱（**必读**）
1. **ChannelsV3 是脱敏形态**：加载时 Key 必丢，必须靠 `ManagedAccounts.Credentials` 补。`AddUpstream` 等不自动写 `ManagedAccounts`，**必须靠 `421ae7ac` 的 `syncManagedAccountCredentialsFromChannels` 兜底**。
2. **`applyAuthoritativeChannelsAsLoadSource` 必须后于 `hydrateManagedAccountCredentials`**：若翻转在 hydrate 之前，hydrate 注入的 Key 会被 ChannelsV3 覆盖掉。波 1 实施时**翻转后必须再调一次 hydrate**（或把 hydrate 移到翻转之后）。
3. **ChannelsV3 ↔ 六数组聚合键不对称**：`BuildAuthoritativeChannels` 用 `(LogicalChannelUID, AccountUID, SiteIdentity)` 聚合，`ApplyAuthoritativeChannels` 按 `Index` 排序——3 个独立渠道可能因 site 解析同 key 被聚到同一 V3（OrderRestored 测试已暴露）。**这是预存行为**，不影响功能但影响测试断言。
4. **不要回退 `15e43d3e` 的 strip 易变字段**：移除会导致迁移历史渠道误报回退。
5. **ChannelsV3 的 `AccountUID` 在加载时也是易变的**（随机生成），已在 strip 列表中——`mimo_console_test.go` 之前因未 strip 而失败，已修。
6. **不要轻易相信"被回滚"告警**：本会话因 agent 反复基于陈旧快照重写导致多次误报"被回滚"，最终查明是自我循环（见 memory `feedback_parallel_agent_self_redo_vs_external_revert`）。收到告警先 `git status` / `git diff` / `git reflog` 取证再行动。

### 已学到的教训（已写入 memory）
- `feedback_parallel_agent_self_redo_vs_external_revert`：并行 agent 改同文件被覆写的真凶常是 agent 自身反复重做，**必须先取证磁盘/git 真相再判断**
- `project_autopilot_phase1_done`：本会话 Phase 1~4 全部完成清单 + healthcheck ledger ClaimProbe 接入遗留

### 索引与本会话其他 memory
- 渠道粒度重构 v2、autopilot Phase 1 done、new-api 集成设计、ManualRoutingIntent API 契约、new-api 后端 API 契约、上下文硬约束用映射后模型、渠道级上下文上限自学习、Fuzzy 模式移除、Kimi K3 参数约束、熔断依据与日志生命周期
- 关键 memory 路径：`/Users/petaflops/.claude/projects/-Users-petaflops-works-api-gateways-llm-routing-ccx/memory/`

## 11. 验证清单（每步必须做）

### 波 1 重做后必跑
```bash
cd /Users/petaflops/works/api-gateways/llm-routing/ccx/backend-go
go build ./...
go test ./internal/config/ -count=1
go test -race ./internal/config/ -count=1
```

预期：全部 PASS。

### 端到端：迁移历史渠道翻转
```bash
cat > /tmp/e2e.go <<'EOF'
// 复用 channel-data-model-v2.md §7 中的 TestAuthoritativeLoad_MigratedChannelNoFalseRollback
// 模式:AutoManagedKind 留空、AutoManaged=true、OriginType=relay,
// 期望加载日志含"已从 ChannelsV3 重建六数组"+ 重载后 AutoManagedKind="new_api"
EOF
go test -run TestAuthoritativeLoad_MigratedChannelNoFalseRollback ./internal/config/ -v
```

### 端到端：托管 Key 保留
```bash
# 在 zz_e2e_test.go 形态(见 421ae7ac 提交):
# 1. 加一个 AccountUID=acct_e, ProviderID=kimi, APIKeyConfigs[0].Key=sk-e 的渠道
# 2. SaveConfig() → 落盘
# 3. 新 ConfigManager 加载 → GetConfig()
# 4. 断言 Upstream[0].APIKeys == ["sk-e"]
```

### 端到端：make dev
- 用户 `make dev` 重启服务
- 浏览器打开 Admin 界面，验证渠道列表正常
- 试一次"添加渠道"+"重载"
- 看管理 API（`/api/messages/channels`）返回是否正常

### 重要：不要做的事
- ❌ 不要 `git push`（本会话默认不推送，提交后由用户决定）
- ❌ 不要 `git reset --hard` / `rebase` / `amend`——会改写历史
- ❌ 不要 `rm -rf` 或删除文件
- ❌ 不要在没有用户确认时改 env/权限
- ❌ 不要用 `git commit -F` + COMMIT_EDITMSG（应用 `git commit -m` 多 `-m`）

## 12. 关联文档与文件索引

<!-- 待补充 -->

- 必读 spec:
  - `docs/specs/channel-data-model-v2.md` — 渠道粒度重构 v2 主线 spec(Phase 3a/b/c 阶段定义在 §6;当前实现状态在 §7)
  - `docs/specs/logical-channel.md` — 逻辑渠道 spec(Phase 3c 涉及 LogicalChannelUID 字段)
  - `docs/specs/new-api-integration.md` — new-api 集成 spec(AccountUID 收敛相关)
  - `docs/specs/cross-module-integration.md` — 跨模块集成 spec(eventbus 事件、ChannelsV3 与其他模块的关系)
  - `docs/specs/healthcheck.md` — 健康检查 spec(与 CapabilityProbeLedger 关联)
  - `docs/specs/autopilot.md` — autopilot 架构 spec
  - `docs/specs/web-ui-pages.md` / `web-ui-dialogs.md` — 前端 spec(v2 渠道粒度前端展示)

- 关键代码文件:
  - `backend-go/internal/config/channel_authoritative.go` — ChannelsV3 构建/投影/对账
  - `backend-go/internal/config/channel_view.go` — ChannelView 读模型(已用作 /api/channels)
  - `backend-go/internal/config/endpoint_capability.go` — CapabilityProbeLedger(需接入 healthcheck L1/L2)
  - `backend-go/internal/config/config_loader.go` — loadConfig / saveConfigLocked / 各种迁移 / syncManagedAccountCredentialsFromChannels(421ae7ac)
  - `backend-go/internal/config/config.go` — Config 字段定义、GetConfig、所有 Add*Upstream/Update*Upstream/Remove*Upstream/Set*Status 等写方法
  - `backend-go/internal/config/config_chat.go` / config_gemini.go / config_images.go / config_messages.go / config_responses.go / config_vectors.go — 各协议专属的 Add*Upstream 等
  - `backend-go/internal/scheduler/select.go` — 调度候选选择(消费六数组的典型代表)
  - `backend-go/internal/handlers/messages/channels.go` — 管理 API(用 :id 数字寻址)
  - `backend-go/internal/handlers/messages/handler.go` — 代理入口
  - `backend-go/internal/handlers/common/multi_channel_failover.go` — 多渠道故障转移
  - `backend-go/internal/autopilot/handlers_newapi.go` — new-api provision(per-UID 锁、AccountUID 写入)
  - `backend-go/internal/autopilot/newapi_subscription_sync_service.go` — 周期同步(WithSubscriptionLock / LockForUID 导出)
  - `backend-go/internal/metrics/model_circuit.go` — model circuit breaker(键已改为 (channelUID, keyHash, model))
  - `backend-go/internal/healthcheck/manager.go` / check.go / l2.go — 健康检查(L1/L2 未接 ClaimProbe)

- CLAUDE.md 索引:
  - 根:/Users/petaflops/works/api-gateways/llm-routing/ccx/CLAUDE.md
  - 后端:/Users/petaflops/works/api-gateways/llm-routing/ccx/backend-go/CLAUDE.md
  - 前端:/Users/petaflops/works/api-gateways/llm-routing/ccx/frontend/CLAUDE.md

- memory:
  - /Users/petaflops/.claude/projects/-Users-petaflops-works-api-gateways-llm-routing-ccx/memory/MEMORY.md — 索引
  - feedback_parallel_agent_self_redo_vs_external_revert — 必读
  - project_autopilot_phase1_done — 已完成清单
  - project-channel-data-model-v2 — v2 主线项目状态
