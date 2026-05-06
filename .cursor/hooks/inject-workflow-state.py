#!/usr/bin/env python3
"""Trellis UserPromptSubmit hook: inject per-turn workflow breadcrumb.

Runs on every user prompt. Resolves the active task through Trellis'
session-aware active task resolver and emits a short <workflow-state> block
reminding the main AI what task is active and its expected flow. Breadcrumb text is pulled from
workflow.md [workflow-state:STATUS] tag blocks (single source of truth
for users who fork the Trellis workflow), with hardcoded fallbacks so
the hook never breaks when workflow.md is missing or malformed.

Shared across all hook-capable platforms (Claude, Cursor, Codex, Qoder,
CodeBuddy, Droid, Gemini, Copilot). Kiro is not wired (no per-turn
hook entry point). Written to each platform's hooks directory via
writeSharedHooks() at init time.

Silent exit 0 cases (no output):
  - No .trellis/ directory found (not a Trellis project)
  - task.json malformed or missing status

Unknown status (no tag + no hardcoded fallback) emits a generic
breadcrumb rather than silent-exiting, so custom statuses surface in
the UI instead of appearing as "randomly broken".
"""
from __future__ import annotations

import json
import os
import re
import sys
from pathlib import Path
from typing import Optional


# ---------------------------------------------------------------------------
# CWD-robust Trellis root discovery (fixes hook-path-robustness for this hook)
# ---------------------------------------------------------------------------

def find_trellis_root(start: Path) -> Optional[Path]:
    """Walk up from start to find directory containing .trellis/.

    Handles CWD drift: subdirectory launches, monorepo packages, etc.
    Returns None if no .trellis/ found (silent no-op).
    """
    cur = start.resolve()
    while cur != cur.parent:
        if (cur / ".trellis").is_dir():
            return cur
        cur = cur.parent
    return None


# ---------------------------------------------------------------------------
# Active task discovery
# ---------------------------------------------------------------------------

def _detect_platform(input_data: dict) -> str | None:
    if isinstance(input_data.get("cursor_version"), str):
        return "cursor"
    env_map = {
        "CLAUDE_PROJECT_DIR": "claude",
        "CURSOR_PROJECT_DIR": "cursor",
        "CODEBUDDY_PROJECT_DIR": "codebuddy",
        "FACTORY_PROJECT_DIR": "droid",
        "GEMINI_PROJECT_DIR": "gemini",
        "QODER_PROJECT_DIR": "qoder",
        "KIRO_PROJECT_DIR": "kiro",
        "COPILOT_PROJECT_DIR": "copilot",
    }
    for env_name, platform in env_map.items():
        if os.environ.get(env_name):
            return platform
    script_parts = set(Path(sys.argv[0]).parts)
    if ".claude" in script_parts:
        return "claude"
    if ".cursor" in script_parts:
        return "cursor"
    if ".codex" in script_parts:
        return "codex"
    if ".gemini" in script_parts:
        return "gemini"
    if ".qoder" in script_parts:
        return "qoder"
    if ".codebuddy" in script_parts:
        return "codebuddy"
    if ".factory" in script_parts:
        return "droid"
    if ".kiro" in script_parts:
        return "kiro"
    return None


def _resolve_active_task(root: Path, input_data: dict):
    scripts_dir = root / ".trellis" / "scripts"
    if str(scripts_dir) not in sys.path:
        sys.path.insert(0, str(scripts_dir))
    from common.active_task import resolve_active_task  # type: ignore[import-not-found]

    return resolve_active_task(root, input_data, platform=_detect_platform(input_data))


def get_active_task(root: Path, input_data: dict) -> Optional[tuple[str, str, str]]:
    """Return (task_id, status, source) from the current active task."""
    active = _resolve_active_task(root, input_data)
    if not active.task_path:
        return None

    task_dir = Path(active.task_path)
    if not task_dir.is_absolute():
        task_dir = root / task_dir
    if active.stale:
        return task_dir.name, f"stale_{active.source_type}", active.source

    task_json = task_dir / "task.json"
    if not task_json.is_file():
        return None
    try:
        data = json.loads(task_json.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return None

    task_id = data.get("id") or task_dir.name
    status = data.get("status", "")
    if not isinstance(status, str) or not status:
        return None
    return task_id, status, active.source


# ---------------------------------------------------------------------------
# Breadcrumb loading: parse workflow.md, fall back to hardcoded defaults
# ---------------------------------------------------------------------------

# Supports STATUS values with letters, digits, underscores, hyphens
# (so "in-review" / "blocked-by-team" work alongside "in_progress").
_TAG_RE = re.compile(
    r"\[workflow-state:([A-Za-z0-9_-]+)\]\s*\n(.*?)\n\s*\[/workflow-state:\1\]",
    re.DOTALL,
)

# Hardcoded defaults for built-in Trellis statuses. Used when workflow.md is
# missing, malformed, or lacks the tag for this status.
#
# `no_task` is a pseudo-status emitted when no session active task exists — it keeps
# the Next-Action reminder flowing per-turn even without an active task.
_FALLBACK_BREADCRUMBS = {
    "no_task": (
        "No active task.\n"
        "Trigger words in the user message that suggest creating a task: "
        "重构 / 抽成 / 独立 / 分发 / 拆出来 / 搞一个 / 做成 / 接入 / 集成 / "
        "refactor / rewrite / extract / productize / publish / build X / design Y.\n"
        "Task is NOT required if ALL three hold: (a) zero file writes this turn, "
        "(b) answer fits in one reply with no multi-round plan, (c) no research "
        "beyond reading 1-2 repo files.\n"
        "When in doubt and no override below applies: prefer creating a task — "
        "over-tasking is cheap; under-tasking leaks plans and research into "
        "main context.\n"
        "Flow: load `trellis-brainstorm` skill → it creates the task via "
        "`python3 ./.trellis/scripts/task.py create` and drives requirements Q&A. "
        "For research-heavy work (tool comparison, docs, cross-platform survey), "
        "spawn `trellis-research` sub-agents via Task tool — NEVER do 3+ inline "
        "WebFetch/WebSearch/`gh api` calls in the main conversation.\n"
        "User override (per-turn escape hatch): if the user's CURRENT message "
        "contains an explicit opt-out phrase (\"跳过 trellis\" / \"别走流程\" / "
        "\"小修一下\" / \"直接改\" / \"先别建任务\" / \"skip trellis\" / "
        "\"no task\" / \"just do it\" / \"don't create a task\"), honor it for "
        "this turn — briefly acknowledge (\"好，本轮跳过 trellis 流程\") and "
        "proceed without creating a task. Per-turn only; does not carry forward; "
        "do NOT invent an override the user did not say."
    ),
    "planning": (
        "Complete prd.md via trellis-brainstorm skill; then run task.py start.\n"
        "Research belongs in `{task_dir}/research/*.md`, written by "
        "`trellis-research` sub-agents. Do NOT inline WebFetch/WebSearch in "
        "main session — PRD only links to research files."
    ),
    "in_progress": (
        "Flow: trellis-implement → trellis-check → trellis-update-spec → finish\n"
        "Next required action: inspect conversation history + git status, then "
        "execute the next uncompleted step in that sequence.\n"
        "For agent-capable platforms, the default is to dispatch "
        "`trellis-implement` for implementation and `trellis-check` before "
        "reporting completion — do not edit code in the main session by default.\n"
        "Use the exact Trellis agent type names when spawning sub-agents: "
        "`trellis-implement`, `trellis-check`, or `trellis-research`. "
        "Generic/default/generalPurpose sub-agents do not receive "
        "`implement.jsonl` / `check.jsonl` injection.\n"
        "User override (per-turn escape hatch): if the user's CURRENT message "
        "explicitly tells the main session to handle it directly (\"你直接改\" / "
        "\"别派 sub-agent\" / \"main session 写就行\" / \"do it inline\" / "
        "\"不用 sub-agent\"), honor it for this turn and edit code directly. "
        "Per-turn only; does not carry forward; do NOT invent an override the "
        "user did not say."
    ),
    "completed": (
        "Code committed via Phase 3.4; run `/trellis:finish-work` to wrap up "
        "(archive task + record session).\n"
        "If you reach this state with uncommitted code, return to Phase 3.4 "
        "first — `/finish-work` refuses to run on a dirty working tree.\n"
        "`task.py archive` deletes runtime session files that point at the "
        "archived task."
    ),
}


def load_breadcrumbs(root: Path) -> dict[str, str]:
    """Parse workflow.md for [workflow-state:STATUS] blocks.

    Returns {status: body_text}. Missing tags fall back to hardcoded
    defaults so the hook always has something to say for built-in
    statuses. Custom statuses without tags fall to generic breadcrumb
    downstream (see build_breadcrumb).
    """
    result = dict(_FALLBACK_BREADCRUMBS)

    workflow = root / ".trellis" / "workflow.md"
    if not workflow.is_file():
        return result
    try:
        content = workflow.read_text(encoding="utf-8")
    except OSError:
        return result

    for match in _TAG_RE.finditer(content):
        status = match.group(1)
        body = match.group(2).strip()
        if body:
            result[status] = body
    return result


def build_breadcrumb(
    task_id: Optional[str],
    status: str,
    templates: dict[str, str],
    source: str | None = None,
) -> str:
    """Build the <workflow-state>...</workflow-state> block.

    - Known status (in templates or fallback) → detailed template body
    - Unknown status (no tag + no fallback) → generic "refer to workflow.md"
    - `no_task` pseudo-status (task_id is None) → header omits task info
    """
    body = templates.get(status)
    if body is None:
        body = "Refer to workflow.md for current step."
    header = f"Status: {status}" if task_id is None else f"Task: {task_id} ({status})"
    if source:
        header = f"{header}\nSource: {source}"
    return f"<workflow-state>\n{header}\n{body}\n</workflow-state>"


# ---------------------------------------------------------------------------
# Entry
# ---------------------------------------------------------------------------

def main() -> int:
    try:
        data = json.load(sys.stdin)
    except (json.JSONDecodeError, ValueError):
        data = {}

    cwd_str = data.get("cwd") or os.getcwd()
    cwd = Path(cwd_str)

    root = find_trellis_root(cwd)
    if root is None:
        return 0  # not a Trellis project

    templates = load_breadcrumbs(root)
    task = get_active_task(root, data)
    if task is None:
        # No active task — still emit a breadcrumb nudging AI toward
        # trellis-brainstorm + task.py create when user describes real work.
        breadcrumb = build_breadcrumb(None, "no_task", templates)
    else:
        task_id, status, source = task
        breadcrumb = build_breadcrumb(task_id, status, templates, source)

    output = {
        "hookSpecificOutput": {
            "hookEventName": "UserPromptSubmit",
            "additionalContext": breadcrumb,
        }
    }
    print(json.dumps(output))
    return 0


if __name__ == "__main__":
    sys.exit(main())
