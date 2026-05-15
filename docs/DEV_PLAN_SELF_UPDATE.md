# 开发计划：CCX 二进制自更新

> 关联文档：[docs/SELF_UPDATE.md](SELF_UPDATE.md) · [docs/service/README.md](service/README.md)

## 依赖关系图
```
Step 1 (Windows SCM) ───┐                       ┌──→ Step 3 (updater 包) ──→ Step 4 (API) ──→ Step 5 (前端)
Step 2 (--health-check) ─┘
```

Step 1 和 Step 2 互不依赖可并行，Step 3 依赖二者合入后才有完整的预飞行 + 替换基础设施，Step 4 是 Step 3 的 HTTP 薄封装，Step 5 是 UI 层消费者。

---

## Step 1：Windows SCM 服务集成

**目标**：用 `ccx --install` / `--uninstall` / `--start` / `--stop` 替代 NSSM。

**依赖**：`golang.org/x/sys/windows/svc` + `svc/mgr`（已是间接依赖，版本 v0.41.0，无需 `go get`）。

### 文件

| 文件 | 操作 | 说明 |
|------|------|------|
| `backend-go/service_windows.go` | 新建 | 构建标签 `//go:build windows`，SCM 服务注册/管理/事件循环 |
| `backend-go/service_stub.go` | 新建 | `//go:build !windows`，非 Windows 平台的桩函数 |
| `backend-go/shutdown.go` | 新建 | `shutdownCh` / `shutdownDoneCh` channel 定义 |
| `backend-go/main.go` | 修改 | 在 `os.Args` 分发块增加 4 个 case；入口增加 SCM 检测 |

### `service_windows.go` 接口

```go
func installService(name, displayName, exePath string) error  // 写入 SCM
func removeService(name string) error                         // 从 SCM 删除
func startService(name string) error                          // 启动
func stopService(name string) error                           // 停止
func isWindowsService() bool                                  // 是否由 SCM 启动
func runService(name string) error                            // 事件循环，阻塞
```

SCM 注册配置：
- 启动类型：自动（开机自启）
- 失败重启：3 次 / 30 秒
- 环境变量：不存储，CCX 启动时从 exe 同目录 `.env` 读取（已有 `godotenv.Load()`）

### `main.go` 改动

CLI 分发新增：
```go
case "--install":
    exePath, _ := os.Executable()
    err := installService("ccx", "CCX API Gateway", exePath)
case "--uninstall":
    err := removeService("ccx")
case "--start":
    err := startService("ccx")
case "--stop":
    err := stopService("ccx")
```

`main()` 入口增加 SCM 服务模式检测：

```go
if isWindowsService() {
    runService("ccx")
    return
}
```

### 验证

```
Windows 上 go build → ccx --install → Get-Service ccx
→ ccx --start → http://localhost:3000/health
→ ccx --stop → ccx --uninstall
```

---

## Step 2：`--health-check` 模式

**目标**：新二进制能以最小模式启动，暴露 `/health` 供预飞行验证。

### 文件

| 文件 | 操作 | 说明 |
|------|------|------|
| `backend-go/health_check.go` | 新建 | `runHealthCheck()` 函数：随机 localhost 端口 + 5 秒自动退出 |
| `backend-go/main.go` | 修改 | 在 CLI 分发块增加 `case "--health-check"` |

### 逻辑

```go
case "--health-check":
    runHealthCheck()
```

`runHealthCheck()` 设计：
- 绑定 `127.0.0.1:0`（localhost 随机端口），不暴露给外部网络
- 输出 `READY:<port>` 到 stdout 供调用方解析
- 5 秒后自动 `os.Exit(0)`

### 验证

```
ccx --health-check → 看到 READY:xxxxx → curl http://127.0.0.1:xxxxx/health 返回 version
```

---

## Step 3：`internal/updater/` 包

**目标**：版本检查、下载、预飞行编排、平台自替换的核心逻辑。

### 文件

| 文件 | 操作 | 说明 |
|------|------|------|
| `backend-go/internal/updater/updater.go` | 新建 | 共享逻辑：`CheckLatest`、`DownloadBinary`、`DoUpdate`（编排）、`preFlightCheck` |
| `backend-go/internal/updater/updater_unix.go` | 新建 | `//go:build !windows`。`selfReplace(exePath)`：rename 备份 → rename 替换 → `os.Exit(0)` |
| `backend-go/internal/updater/updater_windows.go` | 新建 | `//go:build windows`。`selfReplace(exePath)`：rename 备份 → rename 替换 → `os.Exit(1)` |
| `backend-go/internal/updater/updater_test.go` | 新建 | 版本比较、资产匹配的单元测试 |

### 核心数据结构与函数

```go
// ReleaseInfo GitHub Release 信息
type ReleaseInfo struct {
    Version     string // "v2.7.0"
    DownloadURL string
    PublishedAt string
    HTMLURL     string
}

// CheckLatest 查询 GitHub 最新正式版（跳过 prerelease）
func CheckLatest(owner, repo string) (*ReleaseInfo, error)

// DownloadBinary 下载二进制到指定路径，流式写入，完成后 chmod 0755
func DownloadBinary(url, destPath string) error

// preFlightCheck 在新端口启动 binaryPath（--health-check 模式），GET /health
// 验证 version 字段，5 秒超时
func preFlightCheck(binaryPath string, expectedVersion string) error

// DoUpdate 完整更新流程，成功时不返回（直接退出）
func DoUpdate(owner, repo, currentVersion string) error
```

### DoUpdate 流程

```
1. CheckLatest(owner, repo) → 获取 ReleaseInfo
2. 版本比较（semver.Compare）→ 如果已是最新，返回 nil
3. 匹配当前 GOOS/GOARCH 资产
4. DownloadBinary(downloadURL, "<exe>.new")
5. preFlightCheck("<exe>.new", latestVersion)
6. selfReplace(os.Executable()) → 不返回
```

### 资产匹配

编译时 `runtime.GOOS`/`GOARCH` 决定：

```go
func AssetName() string {
    switch runtime.GOOS {
    case "linux":
        return "ccx-linux-" + runtime.GOARCH
    case "windows":
        return "ccx-windows-" + runtime.GOARCH + ".exe"
    }
}
```

### 版本比较

使用 `golang.org/x/mod/semver`（已是间接依赖）。开发版本 `v0.0.0-dev` 始终视为可更新。

### 验证

- 单元测试：`AssetName` 返回正确资产名
- 单元测试：版本比较矩阵（包括 `v0.0.0-dev`、`v2.6.89 < v2.7.0`、`v2.7.0 == v2.7.0`）
- 集成测试：`DoUpdate` 端到端（需可控的 GitHub Release 环境或 mock）

---

## Step 4：API Handler

**目标**：`/api/version/check` 和 `/api/version/update` 两个端点。

### 文件

| 文件 | 操作 | 说明 |
|------|------|------|
| `backend-go/internal/handlers/version_handler.go` | 新建 | 两个 handler，使用 `health.go` 中的 `versionString`/`buildTime`/`gitCommit` 变量 |
| `backend-go/main.go` | 修改 | 在 `apiGroup` 中注册路由 |

### Handler 签名

```go
// GET /api/version/check — 返回当前版本 + GitHub 最新版本
func VersionCheckHandler(envCfg *config.EnvConfig) gin.HandlerFunc

// POST /api/version/update — 触发更新流程
// 权限：需要管理密钥认证（ADMIN_ACCESS_KEY）
// 先返回 {"status":"updating"}，再后台执行 DoUpdate
func VersionUpdateHandler(envCfg *config.EnvConfig) gin.HandlerFunc
```

### 响应格式

`GET /api/version/check`：

```json
{
  "current": {"version": "v2.6.89", "buildTime": "...", "gitCommit": "abc1234"},
  "latest":  {"version": "v2.7.0", "publishedAt": "...", "url": "https://..."},
  "hasUpdate": true
}
```

`POST /api/version/update` 成功：

```json
{"status": "updating", "message": "更新已开始，服务即将重启"}
```

`POST /api/version/update` 失败：

```json
{"status": "error", "message": "预飞行验证失败：health check 超时"}
```

`GET /api/version/status`：

```json
{"status": "downloading", "error": "", "progress": 35}
```

`status` 可能值：`idle` | `downloading` | `verifying` | `failed`。`progress` 为 0–100 整数。

### 路由注册

```go
apiGroup.GET("/version/check", handlers.VersionCheckHandler(envCfg))
apiGroup.POST("/version/update", handlers.VersionUpdateHandler(envCfg))
apiGroup.GET("/version/status", handlers.VersionStatusHandler(envCfg))
```

### 验证

```
curl http://localhost:3000/api/version/check
curl -X POST http://localhost:3000/api/version/update -H "x-api-key: <admin-key>"
```

---

## Step 5：前端确认对话框

**目标**：版本徽章点击 → 确认对话框 → 更新进度 → 轮询恢复 → 显示结果。

### 文件

| 文件 | 操作 | 说明 |
|------|------|------|
| `frontend/src/components/VersionUpdateDialog.vue` | 新建 | 确认对话框组件，含状态机 |
| `frontend/src/services/version.ts` | 修改 | 增加 `triggerUpdate()` 函数和 `checkViaBackend()` 方法 |
| `frontend/src/stores/system.ts` | 修改 | 增加 `updateStatus` 状态字段 |
| `frontend/src/App.vue` | 修改 | `handleVersionClick` 改为打开对话框；模板加入 `<VersionUpdateDialog>` |
| `frontend/src/i18n/messages.ts` | 修改 | 新增 13 个 i18n key |

### 组件设计

```
状态机：confirming → downloading → success / error

confirming:
  标题：发现新版本
  内容：v2.6.89 → v2.7.0
        发布日期：2026-05-20
        [查看更新说明]（外链 GitHub Releases）
  按钮：[稍后再说]  [立即更新]

downloading:
  标题：正在更新
  内容：长方形填充进度条（轮询 /api/version/status 获取 progress 0–100） + "正在下载..."
  → "正在验证新版本..."
  → "更新完成，服务即将重启..."
  按钮：无（不可取消）

success:
  标题：更新成功
  内容：已更新至 v2.7.0
        [查看 GitHub Releases]
  按钮：[关闭]

error:
  标题：更新失败
  内容：错误信息（如网络超时、预飞行验证失败）
  按钮：[关闭]
```

### 轮询恢复检测

更新请求发出后，每 2 秒轮询 `/health`（复用已有 `fetchHealth`），检测 `version.version` 字段是否变为新版本号。最多轮询 30 秒。成功后更新 `systemStore.versionInfo` 并关闭对话框。

### i18n 新 key

| Key | 中文 |
|-----|------|
| `app.version.updateAvailable` | 发现新版本 |
| `app.version.currentVersion` | 当前版本 |
| `app.version.latestVersion` | 最新版本 |
| `app.version.publishedAt` | 发布日期 |
| `app.version.releaseNotes` | 查看更新说明 |
| `app.version.updateNow` | 立即更新 |
| `app.version.later` | 稍后再说 |
| `app.version.downloading` | 正在下载新版本... |
| `app.version.verifying` | 正在验证新版本... |
| `app.version.restarting` | 更新完成，服务即将重启... |
| `app.version.updateSuccess` | 更新成功 |
| `app.version.updateFailed` | 更新失败 |
| `app.version.close` | 关闭 |

### 验证

- 单元测试：`VersionUpdateDialog` 各状态渲染正确
- 集成测试：mock `/api/version/check` 返回 `hasUpdate: true` → 确认 UI 显示
- 手动测试：部署旧版本 → 打开界面 → 确认对话框 → 点击"立即更新" → 观察重启 → 确认新版本显示

---

## 版本发布

- **版本号**：`v2.7.0`（MINOR bump，向下兼容的功能新增）
- **CHANGELOG**：`### Added` 下增加自更新功能条目
- **CI 验证**：`make build` + `cd backend-go && make test` + `cd frontend && bun run build`

## 改动范围

| 层 | 新增文件 | 修改文件 | 预估代码量 |
|---|---------|---------|-----------|
| Go 后端 | 7 | 1 | ~500 行 |
| 前端 | 1 | 3 | ~280 行 |
| i18n | 0 | 1 | ~50 行 |

总计 ~830 行新代码，无新增外部依赖。
