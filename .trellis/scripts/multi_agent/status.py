#!/usr/bin/env python3
"""
Multi-Agent Pipeline: Status Monitor.

Usage:
    python3 status.py                     Show summary of all tasks (default)
    python3 status.py -a <assignee>       Filter tasks by assignee
    python3 status.py --list              List all worktrees and agents
    python3 status.py --detail <task>     Detailed task status
    python3 status.py --watch <task>      Watch agent log in real-time
    python3 status.py --log <task>        Show recent log entries
    python3 status.py --registry          Show agent registry

Entry shim — delegates to status_display and status_monitor.
"""

from __future__ import annotations

import argparse
import sys

import _bootstrap  # noqa: F401 — adds parent scripts/ dir to sys.path

from common.paths import get_repo_root

from .status_display import (
    cmd_detail,
    cmd_help,
    cmd_list,
    cmd_registry,
    cmd_summary,
)
from .status_monitor import cmd_log, cmd_watch


# =============================================================================
# Main
# =============================================================================

def main() -> int:
    """Main entry point."""
    parser = argparse.ArgumentParser(description="Multi-Agent Pipeline: Status Monitor")
    parser.add_argument("-a", "--assignee", help="Filter by assignee")
    parser.add_argument(
        "--list", action="store_true", help="List all worktrees and agents"
    )
    parser.add_argument("--detail", metavar="TASK", help="Detailed task status")
    parser.add_argument("--progress", metavar="TASK", help="Quick progress view")
    parser.add_argument("--watch", metavar="TASK", help="Watch agent log")
    parser.add_argument("--log", metavar="TASK", help="Show recent log entries")
    parser.add_argument("--registry", action="store_true", help="Show agent registry")
    parser.add_argument("target", nargs="?", help="Target task")

    args = parser.parse_args()
    repo_root = get_repo_root()

    if args.list:
        return cmd_list(repo_root)
    elif args.detail:
        return cmd_detail(args.detail, repo_root)
    elif args.progress:
        return cmd_detail(args.progress, repo_root)  # Similar to detail
    elif args.watch:
        return cmd_watch(args.watch, repo_root)
    elif args.log:
        return cmd_log(args.log, repo_root)
    elif args.registry:
        return cmd_registry(repo_root)
    elif args.target:
        return cmd_detail(args.target, repo_root)
    else:
        return cmd_summary(repo_root, args.assignee)


if __name__ == "__main__":
    sys.exit(main())
