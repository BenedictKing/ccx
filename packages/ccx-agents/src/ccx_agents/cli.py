# src/ccx_agents/cli.py
"""``ccx-agents`` CLI — project scaffold, diagnostics, and utilities.

Usage::

    ccx-agents init my-project     # scaffold a new project
    ccx-agents doctor               # check environment
    ccx-agents check                # verify CCX connectivity
    ccx-agents version              # show version
    ccx-agents completion bash      # generate shell completion
"""

from __future__ import annotations

import argparse
import os
import sys
from typing import NoReturn

from . import __version__
from .doctor import check_ccx_connection, diagnose_environment
from .scaffold import scaffold_project


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="ccx-agents",
        description="Route openai-agents-python through CCX — multi-model AI proxy.",
    )
    parser.add_argument(
        "--version",
        "-V",
        action="version",
        version=f"ccx-agents v{__version__}",
    )

    sub = parser.add_subparsers(dest="command")

    # init
    init_p = sub.add_parser("init", help="Scaffold a new ccx-agents project")
    init_p.add_argument("directory", nargs="?", default=".", help="Project directory")
    init_p.add_argument("--name", help="Project name (default: directory basename)")

    # doctor
    sub.add_parser("doctor", help="Run system diagnostics")

    # check
    check_p = sub.add_parser("check", help="Verify CCX connectivity")
    check_p.add_argument("--url", help="CCX base URL (default: env or localhost)")

    # completion
    comp_p = sub.add_parser(
        "completion", help="Generate shell completion script"
    )
    comp_p.add_argument(
        "shell",
        nargs="?",
        default="bash",
        choices=["bash", "zsh", "fish"],
        help="Shell type",
    )

    return parser


# ---------------------------------------------------------------------------
# Shell completion helpers
# ---------------------------------------------------------------------------

_COMPLETION_SCRIPTS: dict[str, str] = {
    "bash": """\
_CCX_AGENTS_COMPLETE=bash_source ccx-agents
""",
    "zsh": """\
_CCX_AGENTS_COMPLETE=zsh_source ccx-agents
""",
    "fish": """\
_CCX_AGENTS_COMPLETE=fish_source ccx-agents
""",
}


def _render_completion(shell: str) -> str:
    # Use argparse's built-in completion if available (Python 3.12+)
    # But for broader compatibility we emit a static script.
    header = f"# ccx-agents shell completion for {shell}\n"
    footer = "# Source this file: source <(ccx-agents completion {shell})\n"
    return (
        header
        + _COMPLETION_SCRIPTS.get(shell, "")
        + footer
    )


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    """CLI entry point.  Returns exit code."""
    parser = _build_parser()
    args = parser.parse_args(argv)

    if not args.command:
        parser.print_help()
        return 0

    # -- init --
    if args.command == "init":
        dest = os.path.abspath(args.directory)
        name = args.name or os.path.basename(dest)
        scaffold_project(dest, name)
        print(f"✅ Created ccx-agents project at {dest}")
        print(f"   cd {dest}")
        print("   pip install -e '.[dev]'")
        print("   python main.py")
        return 0

    # -- doctor --
    if args.command == "doctor":
        issues = diagnose_environment()
        if not issues:
            print("✅ All checks passed")
            return 0
        print("⚠️  Issues found:")
        for issue in issues:
            print(f"   ✗ {issue}")
        return 1

    # -- check --
    if args.command == "check":
        ok, message = check_ccx_connection(args.url)
        if ok:
            print(f"✅ {message}")
            return 0
        print(f"❌ {message}")
        return 1

    # -- completion --
    if args.command == "completion":
        script = _render_completion(args.shell)
        print(script, end="")
        return 0

    return 0


def entry_point() -> NoReturn:
    """Console-script entry point."""
    sys.exit(main())
