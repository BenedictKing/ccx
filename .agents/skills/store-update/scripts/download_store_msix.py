#!/usr/bin/env python3
"""Download CCX Desktop Store MSIX files from GitHub Release and preview Store releaseNotes.

No Microsoft Store API interaction. Uses GitHub API to read release info and
`gh release download` to fetch assets (bypasses proxy issues).
"""

from __future__ import annotations

import argparse
import fnmatch
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

GITHUB_API = "https://api.github.com"
DEFAULT_REPO = "BenedictKing/ccx"
DEFAULT_PACKAGE_GLOB = "CCX-Desktop-*-windows-*-store.msix"
REQUIRED_ARCHES = {"amd64", "arm64"}


@dataclass(frozen=True)
class ReleaseAsset:
    name: str
    url: str
    size: int | None


@dataclass(frozen=True)
class PackageAsset:
    arch: str
    name: str
    path: Path
    sha256: str
    sha_status: str


class DownloadError(RuntimeError):
    pass


def log(message: str) -> None:
    print(message, flush=True)


def get_git_proxy() -> str | None:
    """从 git config 读取 http/https 代理设置。"""
    for key in ("https.proxy", "http.proxy"):
        try:
            result = subprocess.run(
                ["git", "config", "--global", key],
                capture_output=True, text=True
            )
            if result.returncode == 0 and result.stdout.strip():
                return result.stdout.strip()
        except FileNotFoundError:
            pass
    return None


# 从 git config 自动检测代理，gh CLI 不读取 git 配置需要显式注入环境变量
_PROXY = get_git_proxy()
if _PROXY:
    log(f"[Proxy] 检测到 git 代理: {_PROXY}")


def gh(args: list[str], *, check: bool = True, capture: bool = True) -> str | None:
    cmd = ["gh"] + args
    log(f"$ {' '.join(cmd)}")
    # gh CLI 使用 Go net/http，不读取 git proxy 配置，需通过环境变量注入
    env = {**os.environ}
    if _PROXY:
        env.setdefault("HTTP_PROXY", _PROXY)
        env.setdefault("HTTPS_PROXY", _PROXY)
    result = subprocess.run(cmd, capture_output=capture, text=True, env=env)
    if check and result.returncode != 0:
        stderr = result.stderr.strip() if result.stderr else ""
        raise DownloadError(f"gh 命令失败: {stderr}")
    return result.stdout.strip() if capture and result.stdout else None


def request_json(url: str) -> Any:
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "ccx-store-download"}
    token = os.environ.get("GITHUB_TOKEN")
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise DownloadError(f"HTTP {exc.code} GET {url}: {detail[:2000]}") from exc
    except urllib.error.URLError as exc:
        raise DownloadError(f"请求失败 GET {url}: {exc}") from exc


def github_release(repo: str, tag: str | None) -> dict[str, Any]:
    endpoint = f"{GITHUB_API}/repos/{repo}/releases/tags/{urllib.parse.quote(tag)}" if tag else f"{GITHUB_API}/repos/{repo}/releases/latest"
    return request_json(endpoint)


def parse_assets(release: dict[str, Any], package_glob: str) -> tuple[list[ReleaseAsset], dict[str, ReleaseAsset]]:
    packages: list[ReleaseAsset] = []
    sha_assets: dict[str, ReleaseAsset] = {}
    for item in release.get("assets", []):
        name = item.get("name")
        url = item.get("browser_download_url")
        if not name or not url:
            continue
        asset = ReleaseAsset(name=name, url=url, size=item.get("size"))
        if fnmatch.fnmatch(name, package_glob):
            packages.append(asset)
        elif name.endswith(".sha256"):
            sha_assets[name.removesuffix(".sha256")] = asset
    return packages, sha_assets


def arch_from_name(name: str) -> str:
    match = re.search(r"windows-(amd64|arm64)-store\.msix$", name)
    if not match:
        raise DownloadError(f"无法从 MSIX 文件名识别架构: {name}")
    return match.group(1)


def parse_sha256(text: str) -> str | None:
    match = re.search(r"\b[a-fA-F0-9]{64}\b", text)
    return match.group(0).lower() if match else None


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def download_packages_gh(
    repo: str,
    tag: str | None,
    packages: list[ReleaseAsset],
    sha_assets: dict[str, ReleaseAsset],
    download_dir: Path,
) -> list[PackageAsset]:
    """Download MSIX and sha256 files using gh release download."""
    if len(packages) != 2:
        names = ", ".join(sorted(a.name for a in packages)) or "无"
        raise DownloadError(f"期望恰好 2 个 Store MSIX，实际 {len(packages)} 个: {names}")

    seen_arches: set[str] = set()
    result: list[PackageAsset] = []

    for asset in sorted(packages, key=lambda item: item.name):
        arch = arch_from_name(asset.name)
        if arch in seen_arches:
            raise DownloadError(f"重复架构 MSIX: {arch}")
        seen_arches.add(arch)

        # Build glob patterns for both MSIX and its sha256 sidecar
        # Release assets are flat (no subdirectories), so use simple wildcard
        msix_pattern = f"*{asset.name}*"
        sha_pattern = f"*{asset.name}*.sha256"

        log(f"下载 MSIX: {asset.name}")
        gh(["release", "download", "--repo", repo,
            "--pattern", msix_pattern,
            "--dir", str(download_dir),
            "--clobber"], check=True, capture=False)

        sha_status = "release 中没有对应 .sha256，已计算本地 sha256"
        sha_asset = sha_assets.get(asset.name)
        if sha_asset:
            sha_pattern = f"*{sha_asset.name}*"
            log(f"下载 sha256: {sha_asset.name}")
            gh(["release", "download", "--repo", repo,
                "--pattern", sha_pattern,
                "--dir", str(download_dir),
                "--clobber"], check=True, capture=False)

            sha_path = download_dir / sha_asset.name
            expected = parse_sha256(sha_path.read_text(encoding="utf-8", errors="replace"))
            if not expected:
                raise DownloadError(f"无法解析 sha256 文件: {sha_asset.name}")

            msix_path = download_dir / asset.name
            digest = file_sha256(msix_path)
            if expected != digest:
                raise DownloadError(f"sha256 不匹配: {asset.name}, expected={expected}, actual={digest}")
            sha_status = "sha256 校验通过"
        else:
            msix_path = download_dir / asset.name
            digest = file_sha256(msix_path)

        result.append(PackageAsset(
            arch=arch,
            name=asset.name,
            path=msix_path,
            sha256=digest,
            sha_status=sha_status,
        ))

    if seen_arches != REQUIRED_ARCHES:
        raise DownloadError(f"MSIX 架构集合错误: actual={sorted(seen_arches)}, expected={sorted(REQUIRED_ARCHES)}")
    return result


def normalize_release_notes(markdown: str, max_chars: int, truncate: bool) -> str:
    text = markdown.replace("\r\n", "\n").replace("\r", "\n").strip()
    if not text:
        return ""

    lines: list[str] = []
    skip_rest = False
    for raw_line in text.split("\n"):
        line = raw_line.strip()
        lower = line.lower()
        if lower.startswith("**full changelog**") or lower.startswith("full changelog"):
            skip_rest = True
        if skip_rest or line == "---":
            continue
        line = re.sub(r"^#{1,6}\s*", "", line)
        line = re.sub(r"\[([^\]]+)\]\(([^)]+)\)", r"\1", line)
        line = re.sub(r"[*_`]+", "", line)
        line = re.sub(r"<[^>]+>", "", line)
        lines.append(line)

    normalized = "\n".join(lines).strip()
    normalized = re.sub(r"\n{3,}", "\n\n", normalized)
    if len(normalized) <= max_chars:
        return normalized
    if not truncate:
        raise DownloadError(
            f"Store releaseNotes 超过 {max_chars} 字符（实际 {len(normalized)}）。"
            "请用 --store-release-notes/--release-notes-file 提供精简内容，或显式加 --truncate-release-notes。"
        )
    suffix = "\n…"
    return normalized[: max_chars - len(suffix)].rstrip() + suffix


def resolve_store_release_notes(args: argparse.Namespace, release: dict[str, Any]) -> tuple[str, str]:
    if args.no_release_notes:
        return "", "disabled"
    if args.store_release_notes is not None:
        source = "--store-release-notes"
        raw = args.store_release_notes
    elif args.release_notes_file:
        source = f"--release-notes-file {args.release_notes_file}"
        raw = args.release_notes_file.read_text(encoding="utf-8")
    else:
        source = "GitHub Release body"
        raw = str(release.get("body") or "")
    notes = normalize_release_notes(raw, args.release_notes_max_chars, args.truncate_release_notes)
    return notes, source


def print_summary(
    release: dict[str, Any],
    packages: list[PackageAsset],
    download_dir: Path,
    release_notes: str,
    release_notes_source: str,
) -> None:
    tag = release.get("tag_name", "unknown")
    url = release.get("html_url", "")

    print("\n📦 CCX Desktop Store MSIX 下载完成")
    print(f"- Release: {tag} ({url})")
    print(f"- 下载目录: {download_dir}")
    print()
    print("- MSIX 文件:")
    for package in sorted(packages, key=lambda item: item.arch):
        print(f"  - {package.arch}: {package.name}")
        print(f"    路径: {package.path}")
        print(f"    sha256: {package.sha256}")
        print(f"    校验: {package.sha_status}")

    print()
    if release_notes:
        preview = release_notes[:500] + ("…" if len(release_notes) > 500 else "")
        print(f"- Store 更新说明（来源: {release_notes_source}，{len(release_notes)} 字符）:")
        for line in preview.splitlines() or [preview]:
            print(f"  {line}")
    else:
        print("- Store 更新说明: 未生成")

    print()
    print("━" * 50)
    print("📋 手动上传指引:")
    print("  1. 打开 https://partner.microsoft.com/dashboard/products/9NR3N6RFNS5M/overview")
    print("  2. Product release → 点击「Packages」行，上传以下两个 MSIX:")
    for package in sorted(packages, key=lambda item: item.arch):
        print(f"       {package.path}")
    print("  3. 点击「Store listings」行 → 选择语言 → 找到")
    print("     「What's new in this version」字段")
    if release_notes:
        print("     将上方 Store 更新说明粘贴进去")
    print("  4. 返回后点击「Submit for certification」")
    print("━" * 50)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Download CCX Desktop Store MSIX files from GitHub Release via gh CLI.")
    parser.add_argument("--repo", default=os.environ.get("MS_STORE_GITHUB_REPO", DEFAULT_REPO), help=f"GitHub repo, default: {DEFAULT_REPO}")
    parser.add_argument("--tag", help="release tag; default uses latest release")
    parser.add_argument("--allow-prerelease", action="store_true", help="allow downloading from a prerelease; default rejects prereleases")
    parser.add_argument("--package-glob", default=os.environ.get("MS_STORE_PACKAGE_GLOB", DEFAULT_PACKAGE_GLOB), help=f"MSIX asset glob, default: {DEFAULT_PACKAGE_GLOB}")
    parser.add_argument("--download-dir", type=Path, help="directory to store downloaded files; default uses a temporary directory")
    parser.add_argument("--store-release-notes", help="override Store listing releaseNotes; default reads GitHub Release body")
    parser.add_argument("--release-notes-file", type=Path, help="read Store listing releaseNotes from a local text/markdown file")
    parser.add_argument("--no-release-notes", action="store_true", help="do not generate Store listing releaseNotes preview")
    parser.add_argument("--release-notes-max-chars", type=int, default=1000, help="maximum Store releaseNotes length; default: 1000")
    parser.add_argument("--truncate-release-notes", action="store_true", help="truncate releaseNotes when over the max length instead of failing")
    return parser.parse_args()


def main() -> int:
    args = parse_args()

    temp_ctx = None
    if args.download_dir:
        download_dir = args.download_dir
        download_dir.mkdir(parents=True, exist_ok=True)
    else:
        temp_ctx = tempfile.TemporaryDirectory(prefix="ccx-store-download-")
        download_dir = Path(temp_ctx.name)

    try:
        log(f"读取 GitHub Release: repo={args.repo}, tag={args.tag or 'latest'}")
        release = github_release(args.repo, args.tag)
        if release.get("draft"):
            raise DownloadError("最新 Release 是 Draft，不参与下载。请先正式发布 Release。")
        if release.get("prerelease") and not args.allow_prerelease:
            raise DownloadError("最新 Release 是 Prerelease，默认不允许下载。如需下载请显式加 --allow-prerelease。")
        release_notes, release_notes_source = resolve_store_release_notes(args, release)

        packages, sha_assets = parse_assets(release, args.package_glob)
        downloaded = download_packages_gh(args.repo, args.tag, packages, sha_assets, download_dir)

        print_summary(
            release=release,
            packages=downloaded,
            download_dir=download_dir,
            release_notes=release_notes,
            release_notes_source=release_notes_source,
        )
        if temp_ctx:
            print("\n提示: 未指定 --download-dir，临时下载目录会在脚本退出后删除。需要保留文件时请使用 --download-dir。")
        return 0
    except DownloadError as exc:
        print(f"\n❌ 下载失败: {exc}", file=sys.stderr)
        return 1
    finally:
        if temp_ctx:
            temp_ctx.cleanup()


if __name__ == "__main__":
    raise SystemExit(main())
