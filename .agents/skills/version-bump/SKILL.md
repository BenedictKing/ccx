---
name: version-bump
description: 升级项目版本号并提交git，支持patch/minor/major版本升级或指定具体版本号，自动从git log生成CHANGELOG
version: 1.2.0
author: https://github.com/BenedictKing/ccx/
allowed-tools: Bash, Read, Write, Edit
context: fork
---

# 版本号升级技能

## 指令

当用户输入包含以下关键词时，自动触发版本升级流程：

### 中文触发条件

- "升级版本"、"版本号"、"发布版本"、"更新版本"、"更新版本并提交"、"提交 git" → 执行版本升级
- "bump"、"release" → 执行版本升级

### 参数说明

- 无参数或 `patch`: patch 版本 +1
- `minor`: minor 版本 +1, patch 归零
- `major`: major 版本 +1, minor 和 patch 归零
- 具体版本号 (如 `2.1.0`): 直接使用该版本号

### 发布选项

> ⚠️ **重要**: 默认情况下，版本升级后必须创建 tag 并推送！只有推送 tag 才能触发 GitHub Actions 自动编译发布。除非用户明确说"不要 tag"或"--no-tag"，否则始终创建并推送 tag。

- `--no-tag` 或 "不要 tag": 不创建 git tag（仅提交版本变更）
- `--push` 或 "并推送"、"push": 推送 commit 到远程仓库（默认行为）

## 项目版本文件

- **位置**: `VERSION` (项目根目录)
- **格式**: `v{major}.{minor}.{patch}`
- **示例**: `v2.0.15`

## 执行步骤

### 1. 读取当前版本号

```bash
cat VERSION
```

### 2. 解析并计算新版本号

根据用户指定的升级类型计算：

| 当前版本 | 升级类型     | 新版本  |
| -------- | ------------ | ------- |
| v2.0.14  | patch (默认) | v2.0.15 |
| v2.0.14  | minor        | v2.1.0  |
| v2.0.14  | major        | v3.0.0  |
| v2.0.14  | 2.1.5        | v2.1.5  |

### 3. 更新版本文件

```bash
echo "v{新版本号}" > VERSION
```

### 4. 更新 CHANGELOG.md

**前置检查（必须）：**

1. 读取 CHANGELOG.md，查找 `## [Unreleased]` 区块
2. 检查该区块下是否有实际变更内容（即 `## [Unreleased]` 与下一个 `## [v` 之间是否存在非空行）
3. 根据检查结果决定行为：

| 情况 | 行为 |
|------|------|
| 有 `[Unreleased]` 且有变更内容 | ✅ 正常替换为新版本号和日期 |
| 有 `[Unreleased]` 但下方无变更内容 | 进入 **git log 自动生成** 流程 |
| 无 `[Unreleased]` 区块 | 进入 **git log 自动生成** 流程 |

**git log 自动生成流程：**

当 CHANGELOG 中没有现成的变更内容时，从 git log 自动生成：

1. 获取上一个版本 tag 到 HEAD 的提交记录：
   ```bash
   git log v{上一个版本}..HEAD --pretty=format:"%h %s"
   ```
2. 如果没有提交记录，❌ 中止流程，提示用户没有新的变更
3. 按 Conventional Commits 的 `type` 将提交分组为 CHANGELOG 分类：

   | type | CHANGELOG 分类 |
   |------|---------------|
   | `feat` | 新增 |
   | `fix` | 修复 |
   | `perf` | 优化 |
   | `refactor` | 重构 |
   | `docs` | 文档 |
   | `chore`, `ci`, `build` | 其他 |
   | `revert` | 回滚 |

4. 为每个提交生成简洁的 CHANGELOG 条目，参考已有 CHANGELOG 条目的风格（含加粗标题和详情描述）
5. 在 CHANGELOG.md 顶部插入新版本区块（在第一个 `## [v` 之前）：
   ```markdown
   ## [v{新版本号}] - YYYY-MM-DD

   ### 新增

   - **功能标题** - 简要描述

   ### 修复

   - **修复标题** - 简要描述
   ```
6. 仅保留有内容的分类，跳过空分类

**替换规则（有 Unreleased 时）：**

```markdown
# 替换前

## [Unreleased]

# 替换后

## [v{新版本号}] - YYYY-MM-DD
```

### 5. 验证更新

```bash
cat VERSION
cat CHANGELOG.md | head -20
```

### 6. 查看 git 状态

```bash
git status
git diff --stat
```

### 7. 提交变更

**检查工作区状态：**

1. 如果工作区有未提交的修改（除 VERSION 和 CHANGELOG.md 外），询问用户：
   - "检测到工作区有其他未提交的修改，是否一并提交？(Y/n)"
   - 如果用户选择 Y（默认），使用 `git add -A` 提交所有修改
   - 如果用户选择 N，仅提交 VERSION 和 CHANGELOG.md

2. 提交信息规则：
   - 如果仅提交版本文件：`chore: bump version to v{新版本号}`
   - 如果包含其他修改：让用户提供提交信息，或使用默认格式

```bash
# 包含所有修改
git add -A
git commit -m "{用户确认的提交信息}"

# 或仅提交版本文件
git add VERSION CHANGELOG.md
git commit -m "chore: bump version to v{新版本号}"
```

### 8. 创建 Tag（默认必须执行）

> ⚠️ 除非用户明确说"不要 tag"，否则必须创建 tag！

```bash
git tag v{新版本号}
```

### 9. 推送到远程（默认必须执行）

```bash
# 推送 commit
git push origin main

# 推送 tag（触发 GitHub Actions 自动编译发布）
git push origin v{新版本号}
```

## 示例场景

### 场景 1：默认升级 patch 版本

**用户输入**: "升级版本号并提交" 或 "更新版本并推送"

**自动执行流程**:

1. 读取 VERSION: `v2.0.14`
2. 计算新版本: `v2.0.15`
3. 更新 VERSION 文件
4. 执行 git commit
5. 创建 git tag: `v2.0.15`
6. 推送 commit 和 tag 到远程

### 场景 2：升级 minor 版本

**用户输入**: "升级 minor 版本"

**自动执行流程**:

1. 读取 VERSION: `v2.0.14`
2. 计算新版本: `v2.1.0`
3. 更新 VERSION 文件
4. 执行 git commit

### 场景 3：指定具体版本

**用户输入**: "版本号改为 3.0.0"

**自动执行流程**:

1. 读取 VERSION: `v2.0.14`
2. 使用指定版本: `v3.0.0`
3. 更新 VERSION 文件
4. 执行 git commit

### 场景 4：发布新版本（完整流程）

**用户输入**: "发布新版本并打 tag 推送"

**自动执行流程**:

1. 读取 VERSION: `v2.0.29`
2. 计算新版本: `v2.0.30`
3. 更新 VERSION 文件
4. 执行 git commit
5. 创建 git tag: `v2.0.30`
6. 推送 commit 和 tag 到远程
7. GitHub Actions 自动触发，编译 6 平台版本并发布到 Releases

### 场景 5：仅打 tag（不升级版本）

**用户输入**: "给当前版本打 tag 并推送"

**自动执行流程**:

1. 读取当前 VERSION: `v2.0.29`
2. 创建 git tag: `v2.0.29`
3. 推送 tag 到远程

## 输出格式

### 基础版本升级

```
版本升级完成:
- 原版本: v2.0.14
- 新版本: v2.0.15
- 升级类型: patch

是否提交 git? (Y/n)
```

### 完整发布流程

```
版本升级完成:
- 原版本: v2.0.29
- 新版本: v2.0.30
- 升级类型: patch

✅ Git commit 已创建
✅ Git tag v2.0.30 已创建

是否推送到远程仓库? (Y/n)
  - 推送后将自动触发 GitHub Actions
  - 自动编译 Linux/Windows/macOS 版本
  - 自动发布到 GitHub Releases
```

## GitHub Actions 集成

当推送 `v*` 格式的 tag 时，会自动触发以下 workflow：

| Workflow              | Runner         | 产物                                                               |
| --------------------- | -------------- | ------------------------------------------------------------------ |
| `release-linux.yml`   | ubuntu-latest  | `ccx-linux-amd64`, `ccx-linux-arm64`             |
| `release-macos.yml`   | macos-latest   | `ccx-darwin-amd64`, `ccx-darwin-arm64`           |
| `release-windows.yml` | windows-latest | `ccx-windows-amd64.exe`, `ccx-windows-arm64.exe` |
| `docker-build.yml`    | ubuntu-latest  | Docker 镜像 (阿里云容器镜像服务, linux/amd64 + linux/arm64)        |

### Concurrency 配置

每个 release workflow 使用独立的 concurrency group，确保三个平台**并行编译**：

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: false
```

- `${{ github.workflow }}` 使用 workflow 名称作为前缀，避免不同平台的构建相互阻塞
- `cancel-in-progress: false` 确保发布构建不会被取消

### 发布内容

- 6 个平台的可执行文件（三平台并行构建）
- 自动生成的 Release Notes
- Docker 镜像（推送到阿里云容器镜像服务）
- 发布为 **draft** 模式，需手动确认发布

## 注意事项

- 版本号格式为 `v{x}.{y}.{z}`（无后缀）
- 提交前会显示所有待提交的变更供用户确认
- 如果工作区有其他未提交的修改，会询问用户是否一并提交
- 遵循 Conventional Commits 规范，使用 `chore: bump version` 格式
- 推送 tag 后，GitHub Actions 需要几分钟完成编译和发布
- 可以在 GitHub Actions 页面查看构建进度
