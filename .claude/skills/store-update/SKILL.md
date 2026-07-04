---
name: store-update
description: 在 CCX Desktop 发布后下载 Store MSIX 并生成发布公告。用户提到 Store 上架、MSIX、从 GitHub Release 下载 store.msix、发布后同步 Windows Store、从 release 填写商店更新内容时必须使用此技能。该技能会下载最新 GitHub Release 的 amd64/arm64 MSIX，校验 sha256，从 Release body 生成 Store listing releaseNotes 预览，并输出手动上传指引。
version: 2.0.0
author: https://github.com/BenedictKing/ccx/
allowed-tools: Bash, Read
context: fork
---

# CCX Desktop Store MSIX 下载与发布公告技能

## 适用场景

当用户在 GitHub Release 发布完成后，需要下载 Store MSIX 包、校验完整性、生成 Store 更新说明时使用本技能。典型输入：

- "release 后下载两个 msix"
- "帮我准备 Store 的 msix 和更新说明"
- "下载 store msix 并生成发布内容"
- "跑一下 desktop store update"

## 执行流程

### 1. 下载 MSIX 并校验

```bash
python3 .claude/skills/store-update/scripts/download_store_msix.py --download-dir ./store-msix
```

脚本会：

1. 通过 GitHub API 读取最新 release 信息。
2. 从 Release body 生成 Store listing `releaseNotes` 纯文本预览。
3. 用 `gh release download` 下载两个 MSIX（amd64 / arm64）和对应 `.sha256` 到指定目录（`gh` 会自动走已登录的 auth，绕过代理）。
4. 校验架构集合必须为 `amd64` + `arm64`。
5. 校验 sha256（如果 release 中存在 `.sha256`）。
6. 输出下载结果摘要、文件路径和手动操作指引。

### 2. 输出手动操作指引

下载完成后，向用户输出以下信息：

- 两个 MSIX 文件的本地路径。
- SHA256 校验结果。
- 从 Release body 生成的 Store 更新说明预览。
- 手动操作指引：
  - 打开 [Partner Center](https://partner.microsoft.com/dashboard/products/9NR3N6RFNS5M/overview)
  - 进入 CCX Desktop → Product release → 点击「Packages」行上传两个 MSIX
  - 点击「Store listings」行 → 选择语言 → 在「What's new in this version」粘贴更新说明
  - 返回后点击「Submit for certification」

## 默认产物匹配

当前 release workflow 生成两个 Store MSIX：

- `CCX-Desktop-${VERSION}-windows-amd64-store.msix`
- `CCX-Desktop-${VERSION}-windows-arm64-store.msix`

脚本要求最新 GitHub Release 中恰好匹配 amd64 与 arm64 两个 `.msix`，并优先读取同名 `.sha256` 进行校验。

## 常用参数

```bash
python3 .claude/skills/store-update/scripts/download_store_msix.py --help
```

常用选项：

- `--tag <tag>`：指定 release tag，不使用 latest。
- `--allow-prerelease`：允许 prerelease Release，默认拒绝。
- `--repo owner/name`：指定 GitHub 仓库。
- `--download-dir <dir>`：保留下载文件，便于手动上传。默认使用临时目录。
- `--store-release-notes "..."`：手动覆盖 Store listing 更新内容。
- `--release-notes-file <file>`：从本地文件读取 Store listing 更新内容。
- `--no-release-notes`：不生成 Store listing releaseNotes 预览。
- `--release-notes-max-chars 1000`：Store 更新内容最大长度，默认 1000。
- `--truncate-release-notes`：超过长度时显式截断；默认超过长度会失败，避免静默丢内容。

## 输出要求

完成后输出中文摘要，至少包含：

- GitHub release tag 和 URL。
- Store 更新内容来源、长度和预览。
- 两个 MSIX 文件名、架构、本地路径和 sha256 校验状态。
- 手动上传指引（Partner Center 入口和操作步骤）。
- 如果失败，保留错误摘要。
