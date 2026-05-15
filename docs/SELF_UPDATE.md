# CCX 二进制自更新

CCX 通过 Web 管理界面提供二进制自更新功能。支持一键更新，无需手动下载替换。

## 使用方式

1. 打开 CCX Web 管理界面
2. 顶栏右侧的版本徽章会自动检测更新：
   - 显示当前版本（如 `v2.6.89`）— 已是最新
   - 显示 `v2.6.89 → v2.7.0` — 有新版本可用
   - 显示时钟图标 — 正在检查
3. 点击版本徽章：
   - 有新版本时弹出确认对话框，显示新版本号、发布日期和更新说明链接
   - 点击"立即更新"执行更新
4. 更新过程：点击"立即更新" → 下载新版本（进度条逐步填充） → 预飞行验证 → 替换二进制 → 服务重启（约 10–30 秒）
5. 重启后界面自动刷新，版本徽章显示新版本号，点击可跳转 GitHub Releases 查看详情

## 版本检测机制

- 版本徽章在页面加载时自动检查一次
- 检测结果缓存 30 分钟，避免频繁请求 GitHub API
- 检测来源：`GET /api/version/check`（后端调用 GitHub API）
- 仅匹配正式版本，跳过 `-rc`、`-beta` 等预发布版本

## 更新执行流程

```
用户点击"立即更新"
  → 后端下载新二进制到 <binary>.new（同目录，同文件系统）
  → 校验下载完整性（文件大小 > 1MB）
  → 【预飞行验证】在新端口启动新二进制，GET /health 验证正常
  → 验证通过：将当前二进制重命名为临时 .old 文件，释放文件名
  → 将 <binary>.new rename 为 <binary>（原子替换）
  → 删除临时 .old 文件，清理完成
  → 后端进程退出
  → 服务管理器自动拉起新版本
  → 前端轮询 /health 检测到版本号变化，显示更新成功
```

### 预飞行验证

预飞行验证是整个更新流程的核心安全保障。在替换当前运行的程序之前，CCX 会先对新二进制进行隔离测试：

1. 以 `--health-check` 模式启动新二进制（绑定随机 localhost 端口）
2. 解析 `READY:<port>` 信号，向 `/health` 发送 GET 请求，超时 5 秒
3. 验证响应中 `version` 字段与预期一致
4. 验证通过后杀掉临时进程，继续替换流程

预飞行验证通过即证明新二进制可正常启动并响应请求，因此替换后直接删除旧版本。如果验证失败（启动崩溃、端口未监听、版本不匹配），新二进制被删除，当前版本不受影响，前端显示错误提示。

### Linux（systemd）— 全自动

```
下载 → 预飞行验证 → rename 替换 → 删除旧版本 → os.Exit(0)
→ systemd Restart=always 在 5 秒后自动拉起（进程以 os.Exit(0) 正常退出）
```

**为什么可以原子替换**：Linux 内核中文件由 inode 标识。`rename` 覆盖运行中的二进制时，内核将目录项指向新 inode，旧 inode 仍被运行中的进程持有，进程继续正常执行至退出。停服窗口约 5 秒。

### Windows（SCM 服务）— 全自动

```
下载 → 预飞行验证 → rename 当前 exe 为 .old（释放文件名） → rename .new 为 exe → 删除 .old → os.Exit(1)
→ SCM 检测到非零退出码，触发失败重启动作，以新 exe 启动
```

**技术说明**：Windows 不允许删除或覆盖正在运行的 exe，但**允许重命名**。因此流程是先将当前 exe 重命名为临时 `.old` 文件释放原路径，再将 `.new` 重命名为 exe，删除临时文件，最后以非零退出码通知 SCM 执行失败重启动作。两个平台的核心替换逻辑一致，仅在退出码上不同。

停服窗口约 2–5 秒。

> **注意**：Windows 下替换完成后 `.old` 备份文件可能无法立即删除（运行中的进程持有文件句柄）。
> 此为 Windows 文件系统行为，不影响功能，`.old` 文件可在下次重启后手动清理。

## 后端 API

| 端点 | 方法 | 认证 | 说明 |
|------|------|------|------|
| `/api/version/check` | `GET` | 无 | 返回当前版本信息及 GitHub 最新版本 |
| `/api/version/update` | `POST` | 管理密钥 | 触发更新（下载 → 预飞行验证 → 替换 → 退出/重启服务） |
| `/api/version/status` | `GET` | 无 | 返回更新进度状态（供前端轮询进度条） |

`/api/version/check` 响应示例：

```json
{
  "current": {
    "version": "v2.6.89",
    "buildTime": "2026-05-13_08:00:00_CST",
    "gitCommit": "abc1234"
  },
  "latest": {
    "version": "v2.7.0",
    "publishedAt": "2026-05-20T10:00:00Z",
    "url": "https://github.com/BenedictKing/ccx/releases/tag/v2.7.0"
  },
  "hasUpdate": true
}
```

`/api/version/update` 成功响应：

```json
{
  "status": "updating",
  "message": "更新已开始，服务即将重启"
}
```

预飞行验证失败响应：

```json
{
  "status": "error",
  "message": "新版本启动验证失败：health check 超时。当前版本未受影响。"
}
```

`/api/version/status` 响应示例：

```json
{
  "status": "downloading",
  "error": "",
  "progress": 35
}
```

`status` 可能值：`idle` | `downloading` | `verifying` | `failed`。`progress` 为 0–100 的整数，前端据此渲染长方形填充进度条。

## systemd 重启限制配置

CCX 的 systemd 服务单元包含重启限制，作为最后的兜底保护：

```ini
# docs/service/ccx.service（关键配置）
Restart=always
RestartSec=5
StartLimitBurst=3
StartLimitInterval=30s
```

含义：30 秒内最多允许 3 次重启。超过限制后 systemd 停止尝试，服务进入 failed 状态。

## 边界情况

| 场景 | 行为 |
|------|------|
| 已是最新版本 | 版本徽章正常显示，点击跳转 GitHub Releases |
| GitHub API 不可达 | 版本徽章显示当前版本，错误状态缓存 5 分钟 |
| 当前架构无匹配二进制 | 更新请求返回错误，前端显示提示 |
| 开发版本（`v0.0.0-dev`） | 视为可更新，允许覆盖 |
| 下载中断/磁盘满 | 清理临时文件，前端显示错误提示 |
| 预飞行验证失败 | 删除新二进制，保留当前版本，前端显示错误 |
| 二进制目录无写入权限 | 后端返回 500，前端显示权限错误 |
| 新版本启动崩溃（绕过预飞行） | systemd/SCM 重试 3 次后停止，需手动干预恢复 |
| 目标版本不支持 `--health-check`（首次自更新前的旧版本） | 跳过健康验证，仅依赖下载大小校验，替换后继续 |
| 预飞行检查 stdout 读取超时 | 子进程被杀，新二进制被删除，当前版本不受影响 |

## 安全保障层次

| 层次 | 机制 | 作用 |
|------|------|------|
| 下载校验 | 文件大小 > 1MB | 防止下载错误页面 |
| 预飞行验证 | `--health-check` + `/health` | 确保新版本能正常启动 |
| SCM/systemd 限制 | 30s 内最多 3 次重启 | 防止无限崩溃循环 |
| 并发控制 | `atomic.Bool` | 防止重复触发更新 |
| 原子替换 | `os.Rename` 同文件系统 | 不会出现"半个二进制" |

## 服务注册

自更新依赖服务管理器实现自动重启。使用前请确保 CCX 已注册为系统服务：

**Linux**：

```bash
sudo cp docs/service/ccx.service /etc/systemd/system/ccx.service
sudo systemctl daemon-reload
sudo systemctl enable --now ccx
```

**Windows**（以管理员身份）：

```powershell
ccx-windows-amd64.exe --install
ccx-windows-amd64.exe --start
```

详见 [`docs/service/README.md`](service/README.md)。

## 网络要求

更新功能需要服务器能够访问：

- `https://api.github.com` — 查询 Release 信息
- GitHub Release 资产下载地址（由上述 API 返回）

如果服务器无法直连 GitHub，可设置 HTTP 代理环境变量后重启 CCX。

## 与 Docker 的关系

Docker 部署的用户**不应使用**此功能。容器内替换二进制后，重启容器时旧镜像会覆盖替换结果。

Docker 用户请使用 Watchtower 自动更新：

```bash
docker compose -f docker-compose.yml -f docker-compose.watchtower.yml up -d
```
