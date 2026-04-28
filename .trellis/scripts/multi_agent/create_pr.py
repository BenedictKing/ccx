#!/usr/bin/env python3
"""
Multi-Agent Pipeline: Create PR.

Usage:
    python3 create_pr.py [task-dir] [--dry-run]

This script:
1. Handles submodule changes (commit, push, PR) if any submodules are configured
2. Stages and commits all main-repo changes (excluding workspace/)
3. Pushes to origin
4. Creates a Draft PR using `gh pr create`
5. Updates task.json with status="completed", pr_url, submodule_prs, and current_phase

Note: This is the only action that performs git commit, as it's the final
step after all implementation and checks are complete.
"""

from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path

import _bootstrap  # noqa: F401 — adds parent scripts/ dir to sys.path

from common.config import get_submodule_packages
from common.git import run_git
from common.io import read_json, write_json
from common.log import Colors
from common.paths import (
    DIR_WORKFLOW,
    FILE_TASK_JSON,
    get_current_task,
    get_repo_root,
)
from common.phase import get_phase_for_action

# Colors, read_json, write_json
# are now imported from common.log and common.io above.


# =============================================================================
# Submodule PR Helpers
# =============================================================================

# Warning message prepended to main PR body when submodule PRs exist
_SUBMODULE_SQUASH_WARNING_MARKER = (
    "Merge submodule PR(s) first. If squash-merged, update submodule ref after merge."
)


def _get_submodule_default_branch(submodule_abs: Path) -> str:
    """Get the default branch of a submodule repository.

    Uses `git symbolic-ref refs/remotes/origin/HEAD` for portability
    (no grep, no English-dependent output).

    Returns:
        Default branch name (e.g. "main"), falls back to "main" on failure.
    """
    ret, out, _ = run_git(
        ["symbolic-ref", "refs/remotes/origin/HEAD"], cwd=submodule_abs
    )
    if ret == 0 and out.strip():
        # Output: "refs/remotes/origin/main" -> "main"
        ref = out.strip()
        prefix = "refs/remotes/origin/"
        if ref.startswith(prefix):
            return ref[len(prefix):]
    return "main"


def _process_submodule_changes(
    repo_root: Path,
    current_branch: str,
    commit_prefix: str,
    scope: str,
    task_name: str,
    task_data: dict,
    task_json: Path,
    dry_run: bool,
) -> tuple[dict[str, str], list[str], bool]:
    """Process submodule changes: commit, push, create PRs.

    Returns:
        Tuple of (submodule_prs dict, changed_submodule_paths list, success bool).
        On failure, submodule_prs contains URLs persisted so far.
    """
    submodule_packages = get_submodule_packages(repo_root)
    if not submodule_packages:
        return {}, [], True

    # Load existing submodule_prs for incremental merge
    raw_prs = task_data.get("submodule_prs")
    submodule_prs: dict[str, str] = dict(raw_prs) if isinstance(raw_prs, dict) else {}

    # Detect which submodules have changes
    changed: list[tuple[str, str]] = []  # (name, path)
    for pkg_name, pkg_path in submodule_packages.items():
        sub_abs = repo_root / pkg_path
        if not sub_abs.is_dir():
            continue

        ret, status_out, _ = run_git(
            ["status", "--porcelain"], cwd=sub_abs
        )
        if ret != 0:
            continue
        if status_out.strip():
            changed.append((pkg_name, pkg_path))

    if not changed:
        return submodule_prs, [], True

    # Determine submodule branch name: <repo-dir-name>/<main-branch>
    repo_dir_name = repo_root.name
    sub_branch = f"{repo_dir_name}/{current_branch}"

    print(f"\n{Colors.BLUE}=== Submodule Changes Detected ==={Colors.NC}")
    for pkg_name, pkg_path in changed:
        print(f"  - {pkg_name} ({pkg_path})")
    print()

    changed_paths: list[str] = []

    for pkg_name, pkg_path in changed:
        sub_abs = repo_root / pkg_path
        sub_base = _get_submodule_default_branch(sub_abs)
        sub_commit_msg = f"{commit_prefix}({scope}): {task_name}"

        print(f"{Colors.YELLOW}Processing submodule: {pkg_name} ({pkg_path}){Colors.NC}")
        print(f"  Submodule base branch: {sub_base}")
        print(f"  Submodule branch: {sub_branch}")

        if dry_run:
            print(f"  [DRY-RUN] Would checkout branch: {sub_branch}")
            print(f"  [DRY-RUN] Would commit: {sub_commit_msg}")
            print(f"  [DRY-RUN] Would push to: origin/{sub_branch}")
            print(f"  [DRY-RUN] Would create PR: {sub_branch} -> {sub_base}")
            submodule_prs[pkg_name] = "https://github.com/example/repo/pull/DRY-RUN"
            changed_paths.append(pkg_path)
            continue

        # --- Checkout or create branch in submodule ---
        ret, _, _ = run_git(
            ["show-ref", "--verify", "--quiet", f"refs/heads/{sub_branch}"],
            cwd=sub_abs,
        )
        if ret == 0:
            # Branch exists, checkout
            ret, _, err = run_git(
                ["checkout", sub_branch], cwd=sub_abs
            )
            if ret != 0:
                print(f"{Colors.RED}Failed to checkout branch in {pkg_name}: {err}{Colors.NC}")
                return submodule_prs, changed_paths, False

            # Check for divergence (reuse risk)
            ret_anc, _, _ = run_git(
                ["merge-base", "--is-ancestor", sub_base, sub_branch],
                cwd=sub_abs,
            )
            if ret_anc != 0:
                print(
                    f"  {Colors.YELLOW}[WARN] submodule branch has diverged history, "
                    f"consider recreating{Colors.NC}"
                )
        else:
            # Create new branch
            ret, _, err = run_git(
                ["checkout", "-b", sub_branch], cwd=sub_abs
            )
            if ret != 0:
                print(f"{Colors.RED}Failed to create branch in {pkg_name}: {err}{Colors.NC}")
                return submodule_prs, changed_paths, False

        # --- Stage and commit ---
        run_git(["add", "-A"], cwd=sub_abs)

        ret, _, _ = run_git(["diff", "--cached", "--quiet"], cwd=sub_abs)
        if ret != 0:
            # Has staged changes
            ret, _, err = run_git(
                ["commit", "-m", sub_commit_msg], cwd=sub_abs
            )
            if ret != 0:
                print(f"{Colors.RED}Failed to commit in {pkg_name}: {err}{Colors.NC}")
                return submodule_prs, changed_paths, False
            print(f"  {Colors.GREEN}Committed in {pkg_name}{Colors.NC}")
        else:
            print(f"  No staged changes in {pkg_name}, skipping commit")

        # --- Push ---
        ret, _, err = run_git(
            ["push", "-u", "origin", sub_branch], cwd=sub_abs
        )
        if ret != 0:
            print(f"{Colors.RED}Failed to push {pkg_name}: {err}{Colors.NC}")
            return submodule_prs, changed_paths, False
        print(f"  {Colors.GREEN}Pushed {pkg_name} to origin/{sub_branch}{Colors.NC}")

        # --- Create or reuse PR ---
        result = subprocess.run(
            [
                "gh", "pr", "list",
                "--head", sub_branch,
                "--base", sub_base,
                "--json", "url",
                "--jq", ".[0].url",
            ],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            cwd=str(sub_abs),
        )
        existing_sub_pr = result.stdout.strip()

        if existing_sub_pr:
            print(f"  {Colors.YELLOW}PR already exists: {existing_sub_pr}{Colors.NC}")
            sub_pr_url = existing_sub_pr
        else:
            result = subprocess.run(
                [
                    "gh", "pr", "create",
                    "--draft",
                    "--base", sub_base,
                    "--title", f"{commit_prefix}({scope}): {task_name} [{pkg_name}]",
                    "--body", f"Submodule changes for {task_name}",
                ],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                cwd=str(sub_abs),
            )
            if result.returncode != 0:
                print(
                    f"{Colors.RED}Failed to create PR for {pkg_name}: "
                    f"{result.stderr}{Colors.NC}"
                )
                return submodule_prs, changed_paths, False

            sub_pr_url = result.stdout.strip()
            print(f"  {Colors.GREEN}PR created for {pkg_name}: {sub_pr_url}{Colors.NC}")

        # Persist immediately (incremental, supports re-entry)
        submodule_prs[pkg_name] = sub_pr_url
        task_data["submodule_prs"] = submodule_prs
        write_json(task_json, task_data)

        changed_paths.append(pkg_path)

    return submodule_prs, changed_paths, True


def _build_submodule_warning(submodule_prs: dict[str, str]) -> str:
    """Build the squash-merge warning block for the main PR body."""
    pr_lines = "\n".join(f"> - {name}: {url}" for name, url in submodule_prs.items())
    return (
        f"> {_SUBMODULE_SQUASH_WARNING_MARKER}\n"
        f">\n"
        f"> Submodule PRs:\n"
        f"{pr_lines}\n"
        f"\n---\n\n"
    )


def _ensure_submodule_warning_on_existing_pr(
    submodule_prs: dict[str, str],
    dry_run: bool,
) -> None:
    """Read-modify-write: add squash warning to existing PR if missing."""
    if dry_run:
        print("[DRY-RUN] Would check/add submodule warning to existing PR")
        return

    # Read current PR body
    result = subprocess.run(
        [
            "gh", "pr", "view",
            "--json", "body",
            "--jq", ".body",
        ],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    if result.returncode != 0:
        return

    current_body = result.stdout.strip()
    if _SUBMODULE_SQUASH_WARNING_MARKER in current_body:
        return  # Warning already present

    # Prepend warning to existing body
    warning = _build_submodule_warning(submodule_prs)
    new_body = warning + current_body

    subprocess.run(
        ["gh", "pr", "edit", "--body", new_body],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    print(f"  {Colors.GREEN}Added submodule merge warning to existing PR{Colors.NC}")


# =============================================================================
# Main
# =============================================================================


def main() -> int:
    """Main entry point."""
    parser = argparse.ArgumentParser(description="Multi-Agent Pipeline: Create PR")
    parser.add_argument("dir", nargs="?", help="Task directory")
    parser.add_argument(
        "--dry-run", action="store_true", help="Show what would be done"
    )

    args = parser.parse_args()
    repo_root = get_repo_root()

    # =============================================================================
    # Get Task Directory
    # =============================================================================
    target_dir = args.dir
    if not target_dir:
        # Try to get from .current-task
        current_task = get_current_task(repo_root)
        if current_task:
            target_dir = current_task

    if not target_dir:
        print(
            f"{Colors.RED}Error: No task directory specified and no current task set{Colors.NC}"
        )
        print("Usage: python3 create_pr.py [task-dir] [--dry-run]")
        return 1

    # Support relative paths
    if not target_dir.startswith("/"):
        target_dir_path = repo_root / target_dir
    else:
        target_dir_path = Path(target_dir)

    task_json = target_dir_path / FILE_TASK_JSON
    if not task_json.is_file():
        print(f"{Colors.RED}Error: task.json not found at {target_dir_path}{Colors.NC}")
        return 1

    # =============================================================================
    # Main
    # =============================================================================
    print(f"{Colors.BLUE}=== Create PR ==={Colors.NC}")
    if args.dry_run:
        print(
            f"{Colors.YELLOW}[DRY-RUN MODE] No actual changes will be made{Colors.NC}"
        )
    print()

    # Read task config
    task_data = read_json(task_json)
    if not task_data:
        print(f"{Colors.RED}Error: Failed to read task.json{Colors.NC}")
        return 1

    task_name = task_data.get("name", "")
    base_branch = task_data.get("base_branch", "main")
    scope = task_data.get("scope", "core")
    dev_type = task_data.get("dev_type", "feature")

    # Map dev_type to commit prefix
    prefix_map = {
        "feature": "feat",
        "frontend": "feat",
        "backend": "feat",
        "fullstack": "feat",
        "bugfix": "fix",
        "fix": "fix",
        "refactor": "refactor",
        "docs": "docs",
        "test": "test",
    }
    commit_prefix = prefix_map.get(dev_type, "feat")

    print(f"Task: {task_name}")
    print(f"Base branch: {base_branch}")
    print(f"Scope: {scope}")
    print(f"Commit prefix: {commit_prefix}")
    print()

    # Get current branch
    _, branch_out, _ = run_git(["branch", "--show-current"])
    current_branch = branch_out.strip()
    print(f"Current branch: {current_branch}")

    # =============================================================================
    # Submodule PR Flow (runs BEFORE main repo staging)
    # =============================================================================
    submodule_prs, changed_submodule_paths, sub_success = _process_submodule_changes(
        repo_root=repo_root,
        current_branch=current_branch,
        commit_prefix=commit_prefix,
        scope=scope,
        task_name=task_name,
        task_data=task_data,
        task_json=task_json,
        dry_run=args.dry_run,
    )

    if not sub_success:
        print(
            f"\n{Colors.RED}Submodule PR flow failed. "
            f"Skipping main repo commit/PR.{Colors.NC}"
        )
        print("Already-created submodule PRs have been saved to task.json.")
        return 1

    # =============================================================================
    # Main Repo: Stage, Commit, Push, PR
    # =============================================================================

    # Check for changes
    print(f"{Colors.YELLOW}Checking for changes...{Colors.NC}")

    # Stage changes
    run_git(["add", "-A"])

    # Exclude workspace and temp files
    run_git(["reset", f"{DIR_WORKFLOW}/workspace/"])
    run_git(["reset", ".agent-log", ".session-id"])

    # If submodules changed, ensure their ref updates are staged
    for sub_path in changed_submodule_paths:
        run_git(["add", sub_path])

    # Check if there are staged changes
    ret, _, _ = run_git(["diff", "--cached", "--quiet"])
    has_staged_changes = ret != 0

    if not has_staged_changes:
        print(f"{Colors.YELLOW}No staged changes to commit{Colors.NC}")

        # Check for unpushed commits
        ret, log_out, _ = run_git(
            ["log", f"origin/{current_branch}..HEAD", "--oneline"]
        )
        unpushed = len([line for line in log_out.splitlines() if line.strip()])

        if unpushed == 0:
            if args.dry_run:
                run_git(["reset", "HEAD"])
            print(f"{Colors.RED}No changes to create PR{Colors.NC}")
            return 1

        print(f"Found {unpushed} unpushed commit(s)")
    else:
        # Commit changes
        print(f"{Colors.YELLOW}Committing changes...{Colors.NC}")
        commit_msg = f"{commit_prefix}({scope}): {task_name}"

        if args.dry_run:
            print(f"[DRY-RUN] Would commit with message: {commit_msg}")
            print("[DRY-RUN] Staged files:")
            _, staged_out, _ = run_git(["diff", "--cached", "--name-only"])
            for line in staged_out.splitlines():
                print(f"  - {line}")
        else:
            run_git(["commit", "-m", commit_msg])
            print(f"{Colors.GREEN}Committed: {commit_msg}{Colors.NC}")

    # Push to remote
    print(f"{Colors.YELLOW}Pushing to remote...{Colors.NC}")
    if args.dry_run:
        print(f"[DRY-RUN] Would push to: origin/{current_branch}")
    else:
        ret, _, err = run_git(["push", "-u", "origin", current_branch])
        if ret != 0:
            print(f"{Colors.RED}Failed to push: {err}{Colors.NC}")
            return 1
        print(f"{Colors.GREEN}Pushed to origin/{current_branch}{Colors.NC}")

    # Create PR
    print(f"{Colors.YELLOW}Creating PR...{Colors.NC}")
    pr_title = f"{commit_prefix}({scope}): {task_name}"
    pr_url = ""

    # Build PR body with optional submodule warning
    has_submodule_prs = bool(submodule_prs)

    if args.dry_run:
        print("[DRY-RUN] Would create PR:")
        print(f"  Title: {pr_title}")
        print(f"  Base:  {base_branch}")
        print(f"  Head:  {current_branch}")
        prd_file = target_dir_path / "prd.md"
        if prd_file.is_file():
            print("  Body:  (from prd.md)")
        if has_submodule_prs:
            print("  Body includes submodule merge warning")
        pr_url = "https://github.com/example/repo/pull/DRY-RUN"
    else:
        # Check if PR already exists
        result = subprocess.run(
            [
                "gh",
                "pr",
                "list",
                "--head",
                current_branch,
                "--base",
                base_branch,
                "--json",
                "url",
                "--jq",
                ".[0].url",
            ],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
        )
        existing_pr = result.stdout.strip()

        if existing_pr:
            print(f"{Colors.YELLOW}PR already exists: {existing_pr}{Colors.NC}")
            pr_url = existing_pr

            # Read-modify-write: add submodule warning if missing
            if has_submodule_prs:
                _ensure_submodule_warning_on_existing_pr(
                    submodule_prs, args.dry_run
                )
        else:
            # Read PRD as PR body
            pr_body = ""
            prd_file = target_dir_path / "prd.md"
            if prd_file.is_file():
                pr_body = prd_file.read_text(encoding="utf-8")

            # Prepend submodule warning if applicable
            if has_submodule_prs:
                pr_body = _build_submodule_warning(submodule_prs) + pr_body

            # Create PR
            result = subprocess.run(
                [
                    "gh",
                    "pr",
                    "create",
                    "--draft",
                    "--base",
                    base_branch,
                    "--title",
                    pr_title,
                    "--body",
                    pr_body,
                ],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
            )

            if result.returncode != 0:
                print(f"{Colors.RED}Failed to create PR: {result.stderr}{Colors.NC}")
                return 1

            pr_url = result.stdout.strip()
            print(f"{Colors.GREEN}PR created: {pr_url}{Colors.NC}")

    # Update task.json
    print(f"{Colors.YELLOW}Updating task status...{Colors.NC}")
    if args.dry_run:
        print("[DRY-RUN] Would update task.json:")
        print("  status: completed")
        print(f"  pr_url: {pr_url}")
        if has_submodule_prs:
            print(f"  submodule_prs: {submodule_prs}")
        print("  current_phase: (set to create-pr phase)")
    else:
        # Get the phase number for create-pr action
        create_pr_phase = get_phase_for_action(task_json, "create-pr")
        if not create_pr_phase:
            create_pr_phase = 4  # Default fallback

        task_data["status"] = "completed"
        task_data["pr_url"] = pr_url
        task_data["current_phase"] = create_pr_phase
        if has_submodule_prs:
            task_data["submodule_prs"] = submodule_prs

        write_json(task_json, task_data)
        print(
            f"{Colors.GREEN}Task status updated to 'completed', phase {create_pr_phase}{Colors.NC}"
        )

    # In dry-run, reset the staging area
    if args.dry_run:
        run_git(["reset", "HEAD"])

    print()
    print(f"{Colors.GREEN}=== PR Created Successfully ==={Colors.NC}")
    print(f"PR URL: {pr_url}")
    if has_submodule_prs:
        print("Submodule PRs:")
        for name, url in submodule_prs.items():
            print(f"  - {name}: {url}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
