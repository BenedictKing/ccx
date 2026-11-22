# 非 Docker 自启动

本目录提供 CCX 直接运行可执行文件时的自启动示例。Docker 部署推荐使用根目录的 `docker-compose.yml` 和 `docker-compose.watchtower.yml`。

## Linux systemd

适合 Linux 服务器长期运行。

### 1. 准备文件

从 Release 下载 Linux 可执行文件，并放到 `/opt/ccx`：

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ccx
sudo mkdir -p /opt/ccx
sudo cp ccx-linux-amd64 /opt/ccx/
sudo chmod +x /opt/ccx/ccx-linux-amd64
```

在 `/opt/ccx/.env` 写入运行配置：

```bash
PROXY_ACCESS_KEY=your-proxy-access-key
PORT=3688
ENABLE_WEB_UI=true
APP_UI_LANGUAGE=zh-CN
ENV=production
LOG_LEVEL=warn
```

如果希望按 XDG 习惯拆分配置与日志，可以在 service 的 `ExecStart` 中追加命令行参数，例如：

```ini
ExecStart=/opt/ccx/ccx-linux-amd64 --config /etc/ccx/config.json --statedir /var/lib/ccx --logdir /var/log/ccx
# 或使用 --logdir none 禁用日志文件写入，完全交由 journald 管理日志
# ExecStart=/opt/ccx/ccx-linux-amd64 --config /etc/ccx/config.json --statedir /var/lib/ccx --logdir none
```

普通用户服务也可以使用用户目录，例如 `--config ~/.config/ccx/config.json --statedir ~/.local/state/ccx --logdir ~/.local/state/ccx/logs`。`--config` 只改变配置文件位置；`--statedir` 统一指定 `metrics.db`、`thinking_cache.db`、`conversation_state.json`、`scheduled_recovery_state.json` 的目录，未指定时保持默认 `.config`；`--logdir` 只影响应用日志目录；`--backupdir` 可单独指定配置备份目录。

对于 `ccx@.service` 这类用户级模板服务，可以把实例名用于区分配置目录：

```ini
ExecStart=/usr/local/bin/ccx --config %h/.config/ccx/%I/config.json --statedir %h/.local/state/ccx/%I --logdir %h/.local/state/ccx/%I/logs
WorkingDirectory=%h/.local/state/ccx/%I/
```

设置目录权限：

```bash
sudo chown -R ccx:ccx /opt/ccx
```

### 2. 安装服务

```bash
sudo cp docs/service/ccx.service /etc/systemd/system/ccx.service
sudo systemctl daemon-reload
sudo systemctl enable --now ccx
```

### 3. 查看状态和日志

```bash
sudo systemctl status ccx
journalctl -u ccx -f
```

### 4. 更新二进制

```bash
sudo systemctl stop ccx
sudo cp ccx-linux-amd64 /opt/ccx/ccx-linux-amd64
sudo chmod +x /opt/ccx/ccx-linux-amd64
sudo chown ccx:ccx /opt/ccx/ccx-linux-amd64
sudo systemctl start ccx
```

## macOS launchd

适合 macOS 本机后台运行。

### 1. 准备文件

从 Release 下载 macOS 可执行文件，并放到用户目录，例如：

```bash
mkdir -p ~/ccx/logs
cp ccx-darwin-arm64 ~/ccx/
chmod +x ~/ccx/ccx-darwin-arm64
```

编辑 `docs/service/com.ccx.gateway.plist`，将所有 `/Users/your-user/ccx` 替换为实际路径。

### 2. 安装 LaunchAgent

```bash
cp docs/service/com.ccx.gateway.plist ~/Library/LaunchAgents/com.ccx.gateway.plist
launchctl unload ~/Library/LaunchAgents/com.ccx.gateway.plist 2>/dev/null || true
launchctl load ~/Library/LaunchAgents/com.ccx.gateway.plist
launchctl start com.ccx.gateway
```

### 3. 查看和停止

```bash
launchctl list | grep com.ccx.gateway
launchctl stop com.ccx.gateway
launchctl unload ~/Library/LaunchAgents/com.ccx.gateway.plist
```

日志默认写入：

```text
~/ccx/logs/stdout.log
~/ccx/logs/stderr.log
```

## Windows NSSM

适合 Windows 服务器或桌面环境后台运行。详见 `docs/service/windows-nssm.md`。

基本流程：

```powershell
nssm install ccx C:\ccx\ccx-windows-amd64.exe
nssm set ccx AppDirectory C:\ccx
nssm set ccx AppEnvironmentExtra PROXY_ACCESS_KEY=your-proxy-access-key PORT=3688 ENABLE_WEB_UI=true APP_UI_LANGUAGE=zh-CN
nssm set ccx Start SERVICE_AUTO_START
nssm start ccx
```

## 自动更新建议

非 Docker 部署暂不建议程序内自更新。更稳妥的方式是手动替换二进制并重启服务：

- Linux: `systemctl stop ccx` 后替换 `/opt/ccx/ccx-linux-amd64`，再 `systemctl start ccx`
- macOS: `launchctl stop com.ccx.gateway` 后替换可执行文件，再 `launchctl start com.ccx.gateway`
- Windows: `nssm stop ccx` 后替换 exe，再 `nssm start ccx`

如需要自动更新，优先使用 Docker + Watchtower。
