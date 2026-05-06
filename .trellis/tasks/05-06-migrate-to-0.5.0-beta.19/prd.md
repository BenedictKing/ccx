# Migration Task: Upgrade to v0.5.0-beta.19

**Created**: 2026-05-06
**From Version**: 0.4.0
**To Version**: 0.5.0-beta.19
**Assignee**: skip

## Status

- [x] Review migration guide
- [x] Update custom files
- [x] Run `trellis update --migrate`
- [x] Test workflows

## Completion Notes

- Migrated local Trellis files to `0.5.0-beta.19`, including `trellis-*` skill/agent naming and removal of the old multi-agent pipeline files.
- Cleaned generated `.new` / `.backup` conflict artifacts after confirming current files already contain the migrated content.
- Added minimal `implement.jsonl` and `check.jsonl` context so the migrated hooks no longer classify this task as missing context.
- Verified `cmd /c trellis update` reports `Already up to date!` on the second run.
- Verified retired command references and bare `implement` / `check` / `research` agent selectors are not present in active Trellis/Codex/Cursor files.
- Verified Python syntax for `.trellis/scripts`, `.codex/hooks`, and `.cursor/hooks`; verified `get_context.py`, `task.py list`, `task.py current --source`, Codex hooks, Cursor hooks, and `git diff --check`.

---

## v0.5.0-beta.0 Migration Guide

## 0.4.x → 0.5.x: What This Release Actually Changes

0.5.0-beta.0 is a **breaking** release. Pre-existing 0.4.x projects need `trellis update --migrate` to sync. The update runs **206 migration entries** (renames + hash-verified safe-file-deletes); the patch is non-destructive but large, so expect a handful of confirm prompts.

### 1. Skills got renamed: `skills/<name>/` → `skills/trellis-<name>/`

All Trellis skill directories gained a `trellis-` prefix. 60+ rename migrations cover every platform (`.claude/`, `.cursor/`, `.agents/`, `.kiro/`, `.qoder/`, etc.).

- **Unmodified skills**: renamed silently.
- **Skills you customized**: confirm prompt. Pressing Enter (default = backup-rename) is always safe — your edits land at the new `trellis-<name>/` path intact.

### 2. Six commands + three sub-agents retired

| Old (removed) | Replacement |
|---|---|
| `/record-session` | merged into `/trellis:finish-work` Step 3 |
| `/check-cross-layer` | merged into `/trellis:check` |
| `/parallel` | use your CLI's native worktree/parallel support |
| `/onboard` | superseded by auto-generated onboarding tasks |
| `/create-command` | low-usage, unshipped |
| `/integrate-skill` | low-usage, unshipped |
| `dispatch` / `debug` / `plan` sub-agents | replaced by skill routing (`trellis-brainstorm`, `trellis-check`, `trellis-break-loop`) |

If any of these you relied on: replace the invocation with the right column. `/record-session` → `/trellis:finish-work` is the most common fix.

### 3. Multi-Agent Pipeline gone

`.trellis/scripts/multi_agent/`, `worktree.yaml`, and the Ralph Loop hook have been removed (138-entry safe-file-delete). Native worktree support in Claude / Cursor / etc. covers this space now. If you built automation around these, you'll need to port it to the platform's native primitives.

### 4. iFlow dropped

The `.iflow/` platform adapter was unmaintained upstream and has been removed. Existing `.iflow/` installs keep working on 0.4.x; switch to a supported platform (`--claude` / `--cursor` / `--codex` / etc.) to stay on 0.5.x.

### 5. task.py schema cleanup

`task.py create` stops writing legacy `current_phase` / `next_action` fields. Existing task.json files keep working (Python readers treat missing fields as null). New tasks from 0.5.x produce the canonical 24-field shape.

### 6. New runtime: per-turn workflow breadcrumb + sub-agent context injection

No user action needed — these are internal flow improvements. But if you hand-edit `.claude/settings.json` / `.codex/hooks.json` / etc., `trellis update` will want to overwrite the hooks block; your custom hooks land on the `Modified by you` prompt so you can review the merge.

## How to upgrade

```bash
trellis update --migrate
```

- `--migrate` is REQUIRED for breaking releases — without it, `trellis update` exits 1 telling you to add it.
- Hash-verified migrations mean your local customizations stay; unmodified Trellis templates get updated silently.
- The confirm prompts inline a `reason` explaining each prompt; Enter (default) is safe.
- Codex users: enable `features.codex_hooks = true` in `~/.codex/config.toml` to activate SessionStart + UserPromptSubmit hook injection (feature-flagged by Codex itself).

### AI Assistant Instructions

When helping with this migration:

When helping a user migrate from 0.4.x to 0.5.x:

1. **Check for retired commands first**. Grep the user's codebase / shell history / aliases for `/record-session`, `/check-cross-layer`, `/parallel`, `/onboard`, `/create-command`, `/integrate-skill`. For each match, help them replace per the table in the guide — most common case is `/record-session` → `/trellis:finish-work`.

2. **Run `trellis update --migrate`**. Walk them through the prompts if any fire — 0.4 didn't hash-track certain SKILL.md files, so pristine copies may show as modified. The per-prompt `reason` explains this; pressing Enter (backup-rename) is always safe.

3. **Don't try to preserve Multi-Agent Pipeline state**. If the user has `.trellis/scripts/multi_agent/`, `worktree.yaml`, or Ralph Loop hooks in local edits, explicitly tell them the feature has no replacement in 0.5 beyond the platform's native worktree support. Help them identify which of their automation still needs porting.

4. **If they're on `.iflow/`**: the platform is gone. Help them pick a supported platform (`--claude` / `--cursor` / `--codex` / etc.) and run `trellis init --<platform>` on the project to add it. `.iflow/` can be manually deleted after.

5. **After migrate**, run `trellis update` a second time and confirm it says "Already up to date!" — any remaining diff indicates an incomplete migration (usually a skill that the user customized and that needs a manual decision).

6. **Skill paths in existing task jsonl files**: if the user has active tasks with `implement.jsonl` / `check.jsonl` referencing old paths (e.g. `.kiro/skills/check/SKILL.md` without `trellis-`), those paths now 404. Re-run `task.py init-context <task-dir> <type> --platform <platform>` to regenerate with correct paths.

7. **Codex feature flag**: remind them to set `features.codex_hooks = true` in `~/.codex/config.toml`, otherwise SessionStart injection is silently skipped and they'll wonder why workflow breadcrumbs don't show up.

---

## v0.5.0-beta.5 Migration Guide

## Sub-Agent Rename: `implement` / `check` / `research` → `trellis-*`

beta.5 renames the three core sub-agents to add a `trellis-` prefix across all 10 agent-capable platforms (Claude, Cursor, OpenCode, Codex, Kiro, Gemini, Qoder, CodeBuddy, Copilot, Droid). The generic names (`implement`, `check`, `research`) were colliding with user-defined agents in some setups.

### What changes

| Old name | New name |
|---|---|
| `implement` | `trellis-implement` |
| `check` | `trellis-check` |
| `research` | `trellis-research` |

Paths per platform:

- **Claude / OpenCode / Cursor / etc.**: `.<platform>/agents/implement.md` → `.<platform>/agents/trellis-implement.md`
- **Codex**: `.codex/agents/implement.toml` → `.codex/agents/trellis-implement.toml`
- **Kiro**: `.kiro/agents/implement.json` → `.kiro/agents/trellis-implement.json`

### Side-effect: removed `model: opus` from agent templates

Agent YAML frontmatter no longer hardcodes a model. Falls back to whatever the platform default is. If you pinned opus explicitly for a reason (budget, performance), re-add `model: opus` to your local agent files after migrate.

### How to upgrade

```bash
trellis update --migrate
```

30 rename entries run. Unmodified agent files are renamed silently; customized ones (e.g. you tweaked the agent prompt) land on the standard `Modified by you` confirm prompt — pressing Enter (backup-rename) is safe.

### What to check after migrate

1. **Any code that spawns agents by name** — shell scripts, CI jobs, or other automation calling `Task(subagent_type="implement")` must update to `Task(subagent_type="trellis-implement")`.
2. **Aliases / hot keys** — if you had a keyboard shortcut or prompt alias that referenced `implement` / `check` / `research`, update it.
3. **Model pin** — if you customized `model: opus` in agent frontmatter, re-add it. Trellis no longer pins a model by default.

### AI Assistant Instructions

When helping with this migration:

When helping a user migrate to 0.5.0-beta.5:

1. **Grep for bare agent names in user code**: look for `Task(subagent_type="implement"|"check"|"research")` in any .md / .ts / .py / shell files. Rename each to the `trellis-` prefixed version.

2. **Check agent customizations**: diff the user's `.<platform>/agents/{implement,check,research}.*` against the new `trellis-*` templates. If they had custom content, help merge it into the new file.

3. **Model pinning**: if their agent frontmatter had `model: opus` and they still want it pinned, re-add it after migrate. beta.5 removes the default pin in favor of platform-default.

4. **Run migrate**: `trellis update --migrate`. Hash-verified renames — pristine files renamed silently, customized files land on the confirm prompt (Enter = backup-rename is safe).

5. **Verify clean second run**: after migrate, running `trellis update` again should report "Already up to date!". Any diff indicates a rename that didn't complete (user chose skip on a modified file).
