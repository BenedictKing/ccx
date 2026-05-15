# Windows 服务注册

CCX 内置了 Windows 服务注册命令，无需安装第三方工具。

## 配置运行环境

CCX 通过 exe 同目录下的 `.env` 文件读取运行配置（与 Linux 一致）：

```powershell
# C:\ccx\.env
PROXY_ACCESS_KEY=your-proxy-access-key
PORT=3000
ENABLE_WEB_UI=true
APP_UI_LANGUAGE=zh-CN
ENV=production
LOG_LEVEL=warn
```

## 注册服务

以管理员身份打开 PowerShell：

```powershell
# 注册为 Windows 服务（自动启动 + 崩溃重启）
ccx-windows-amd64.exe --install

# 启动服务
ccx-windows-amd64.exe --start
```

`--install` 会将当前 exe 注册到 Windows 服务控制管理器（SCM），配置：

- 启动类型：自动（开机自启）
- 崩溃重启：连续 3 次失败后停止，间隔 30 秒
- 工作目录：exe 所在目录
- 环境变量：SCM 不存储环境变量，由 CCX 启动时从 `.env` 文件读取

## 常用命令

```powershell
ccx-windows-amd64.exe --start      # 启动服务
ccx-windows-amd64.exe --stop       # 停止服务
ccx-windows-amd64.exe --uninstall  # 移除服务
```

## 查看状态

```powershell
Get-Service ccx
```

或通过 `services.msc` 图形界面查看。

## 升级二进制

```powershell
ccx-windows-amd64.exe --stop
# 替换 exe 文件
ccx-windows-amd64.exe --start
```

或通过 Web 管理界面使用自更新功能（详见 [`docs/SELF_UPDATE.md`](../SELF_UPDATE.md)）。

## 备选方案：NSSM

如果因环境限制无法使用内置服务命令，仍可使用 NSSM：

1. 下载 [NSSM](https://nssm.cc/download)
2. 以管理员身份运行：

```powershell
nssm install ccx C:\ccx\ccx-windows-amd64.exe
nssm set ccx AppDirectory C:\ccx
nssm set ccx Start SERVICE_AUTO_START
nssm start ccx
```