---
name: version-bump
description: 升级项目版本号并提交git，支持patch/minor/major版本升级或指定具体版本号，自动从git log生成CHANGELOG
version: 1.5.0
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

1. 读取 CHANGELOG.md，查找 `## [Unreleased]` 区块（若不存在则创建）
2. 获取上一个版本 tag 到 HEAD 的全部提交记录，用于后续校验补全：
   ```bash
   git log v{上一个版本}..HEAD --pretty=format:"%h %s"
   ```
3. 如果没有提交记录，❌ 中止流程，提示用户没有新的变更

**git log 校验与补全（始终执行）：**

无论 `[Unreleased]` 是否已有内容，**都必须**用 git log 逐条校验，确保每个提交都已在 CHANGELOG 中有对应条目：

1. 解析 `[Unreleased]` 区块中已有的条目（按标题摘要匹配）
2. 将 git log 中的每个 commit subject 与已有条目逐一比对
3. 找出所有**尚未记录**的提交，分为两类：
   - **根本不存在**：CHANGELOG 中完全没有该提交的条目 → 直接生成新条目追加到 `[Unreleased]`
   - **错位到前一个版本下**：在 `## [v{上一个版本}]` 区块中找到了该提交的条目 → **必须将条目从旧版本区块中剪切，移动到 `[Unreleased]` 区块**（保留分类分组，删除原位置）
4. 追加/移动完毕后，若全部已有记录则无需改动

> ⚠️ **错位处理必须优先于补全**：先扫描 `[v{上一个版本}]` 区块是否有属于本次 v{新版本} 的提交，有则剪切移动，再对剩余遗漏的提交生成新条目。避免同一个提交在 CHANGELOG 中出现两次。

**覆盖率校验（必须通过）：**

校验补全完成后，必须执行以下命令确认无遗漏：

```bash
# 获取本版本区间所有有效 commit 数量（排除 Merge 和 bump version）
TOTAL=$(git log v{上一个版本}..HEAD --pretty=%s | grep -cvE "^(Merge |chore: bump version)")
# 统计 CHANGELOG 中本版本的条目数量（按 "- **" 开头的行计数）
RECORDED=$(sed -n '/^## \[v{新版本号}\]/,/^## \[/p' CHANGELOG.md | grep -c "^- \*\*")
echo "有效提交: ${TOTAL} 条, 已记录: ${RECORDED} 条"
```

- 如果 `已记录 < 有效提交`：**必须回溯补全后才能继续**，不允许跳过
- 允许 `已记录 >= 有效提交`（某些提交可能被合并为一条，或多条关联 commit 归为一个条目）

**按 type 分组规则：**

| type | CHANGELOG 分类 |
|------|---------------|
| `feat` | 新增 |
| `fix` | 修复 |
| `perf` | 优化 |
| `refactor` | 重构 |
| `docs` | 文档 |
| `chore`, `ci`, `build` | 其他 |
| `revert` | 回滚 |

**替换为新版本号：**

校验补全完成后，将 `## [Unreleased]` 替换为：

```markdown
## [v{新版本号}] - YYYY-MM-DD
```

保留下方所有条目内容不变（已包含全部提交）。

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

### 8. 本地编译与打包验证（必须通过）

> ⚠️ **重要**: 提交后、创建 tag 前，必须通过本地编译验证。编译失败则中止流程，不允许推送。

**执行步骤：**

1. **运行后端测试：**
   ```bash
   cd backend-go && make test
   ```
   - 测试失败 → ❌ 中止流程，提示用户修复测试后重试

2. **执行完整构建（前端 + 后端）：**
   ```bash
   make build
   ```
   - 构建失败 → ❌ 中止流程，提示用户修复编译错误后重试
   - 构建成功 → ✅ 继续后续 tag 和推送流程

3. **验证构建产物：**
   ```bash
   ls -lh dist/ccx-go
   ```
   - 确认产物存在且大小合理

**中止时的处理：**

如果编译验证失败：
- 不创建 tag
- 不推送
- 已提交的 commit 保留在本地
- 输出错误信息，提示用户修复后重新执行

### 9. 创建 Tag（默认必须执行）

> ⚠️ 除非用户明确说"不要 tag"，否则必须创建 tag！

```bash
git tag v{新版本号}
```

### 10. 推送到远程（默认必须执行）

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
3. **从 git log 校验并补全 CHANGELOG** — 将 `v2.0.14..HEAD` 的每个 commit 逐一与 `[Unreleased]` 已有条目比对，遗漏的追加进去
4. 将 `## [Unreleased]` 替换为 `## [v2.0.15] - YYYY-MM-DD`
5. 更新 VERSION 文件
6. 执行 git commit
7. 本地编译验证（`make test` + `make build`）
8. 创建 git tag: `v2.0.15`
9. 推送 commit 和 tag 到远程

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
5. 本地编译验证（`make test` + `make build`）
6. 创建 git tag: `v2.0.30`
7. 推送 commit 和 tag 到远程
8. GitHub Actions 自动触发，编译 6 平台版本并发布到 Releases

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
✅ 本地编译验证通过（测试 + 构建）
✅ Git tag v2.0.30 已创建

是否推送到远程仓库? (Y/n)
  - 推送后将自动触发 GitHub Actions
  - 自动编译 Linux/Windows/macOS 版本
  - 自动发布到 GitHub Releases
```

## GitHub Actions 集成

当推送 `v*` 格式的 tag 时，会自动触发 `release.yml` workflow，在三平台并行编译：

| Job               | Runner              | 产物                                                      |
| ----------------- | ------------------- | --------------------------------------------------------- |
| `build-macos`     | macos-latest        | `ccx-darwin-arm64`, `ccx-darwin-amd64`, DMG 安装包        |
| `build-windows`   | windows-latest      | `ccx-windows-amd64.exe`, `ccx-windows-arm64.exe`, NSIS 安装包 |
| `build-linux`     | ubuntu-latest       | `ccx-linux-amd64`, `ccx-linux-arm64`, AppImage            |
| `build-linux-arm64-desktop` | ubuntu-24.04-arm | `CCX-Desktop-linux-arm64.AppImage`              |
| `docker-build`    | ubuntu-latest       | Docker 镜像 (阿里云容器镜像服务, linux/amd64 + linux/arm64) |

### Concurrency 配置

构建 job 使用独立的 concurrency group，确保三平台**并行编译**：

```yaml
concurrency:
  group: release-${{ github.ref }}
  cancel-in-progress: false
```

- `cancel-in-progress: false` 确保发布构建不会被取消

### 发布内容

- 6 个平台的可执行文件 + 安装包（三平台并行构建）
- 各平台 checksum + cosign 签名文件
- Docker 镜像（推送到阿里云容器镜像服务）
- 发布为 **draft** 模式，需在 GitHub Releases 页面手动确认发布

## 注意事项

- 版本号格式为 `v{x}.{y}.{z}`（无后缀）
- 提交前会显示所有待提交的变更供用户确认
- 如果工作区有其他未提交的修改，会询问用户是否一并提交
- 遵循 Conventional Commits 规范，使用 `chore: bump version` 格式
- **编译验证是强制步骤**：commit 后必须通过 `make test` 和 `make build`，否则不允许创建 tag 和推送
- 编译验证失败时，已提交的 commit 保留在本地，用户修复后可手动推送
- 推送 tag 后，GitHub Actions 需要几分钟完成编译
- 查看构建进度：`gh run list --limit 5`
- 所有构建完成后，draft release 会包含全部平台产物，需手动确认发布

## 构建监控

推送 tag 后，**自动启动构建进度监控**：

```bash
# 每 5 分钟自动检查一次构建状态
gh run list --limit 5
```

**监控行为：**

1. **启动监控**：推送 tag 后，立即设置定时任务，每 5 分钟检查一次
2. **进度汇报**：构建进行中时，简要汇报运行时长和状态
3. **完成通知**：构建完成后，详细汇报：
   - ✅/❌ 各 workflow 执行结果
   - 📦 Release draft 产物清单
   - 🐳 Docker 镜像推送状态
4. **自动停止**：所有构建完成后，自动取消监控任务

**手动查询**：用户随时可以问"构建进度如何"主动获取最新状态
