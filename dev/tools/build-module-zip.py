#!/usr/bin/env python3
from __future__ import annotations

import shutil
import sys
import time
import zipfile
import re
from pathlib import Path


PACKAGE_ROOTS = (
    "module.prop",
    "service.sh",
    "customize.sh",
    "action.sh",
    "uninstall.sh",
    "skip_mount",
    "sepolicy.rule",
    "webroot",
    "KsuProxy",
)

REQUIRED_ENTRIES = (
    "module.prop",
    "service.sh",
    "customize.sh",
    "action.sh",
    "uninstall.sh",
    "skip_mount",
    "webroot/index.html",
    "KsuProxy/scripts/ksu-proxy.sh",
    "KsuProxy/config/whitelist/packages.json",
    "KsuProxy/bin/arm64-v8a/sing-box",
    "KsuProxy/bin/arm64-v8a/x-tunnel",
)


def is_excluded(rel: Path) -> bool:
    rel_posix = rel.as_posix()
    if rel.name in {".DS_Store", "Thumbs.db"}:
        return True
    if rel_posix.startswith("KsuProxy/config/sing-box/") and re.fullmatch(r"box \(\d+\)\.json", rel.name):
        return True
    if rel.name.endswith(".bak"):
        return True
    return False


def is_executable(rel: Path) -> bool:
    rel_posix = rel.as_posix()
    if rel.suffix == ".sh":
        return True
    return rel_posix.startswith("KsuProxy/bin/")


def zip_timestamp(path: Path) -> tuple[int, int, int, int, int, int]:
    return time.localtime(path.stat().st_mtime)[:6]


def add_dir(zf: zipfile.ZipFile, path: Path, rel: Path) -> None:
    arcname = rel.as_posix().rstrip("/") + "/"
    info = zipfile.ZipInfo(arcname, zip_timestamp(path))
    info.external_attr = (0o755 << 16) | 0x10
    zf.writestr(info, b"")


def add_file(zf: zipfile.ZipFile, path: Path, rel: Path) -> None:
    arcname = rel.as_posix()
    mode = 0o755 if is_executable(rel) else 0o644
    info = zipfile.ZipInfo(arcname, zip_timestamp(path))
    info.compress_type = zipfile.ZIP_DEFLATED
    info.external_attr = mode << 16
    with path.open("rb") as src, zf.open(info, "w", force_zip64=True) as dst:
        shutil.copyfileobj(src, dst, length=1024 * 1024)


def iter_entries(root: Path):
    for package_root in PACKAGE_ROOTS:
        path = root / package_root
        if not path.exists():
            raise FileNotFoundError(f"missing package entry: {package_root}")
        rel = Path(package_root)
        if path.is_dir():
            yield path, rel
            for child in sorted(path.rglob("*")):
                child_rel = child.relative_to(root)
                if is_excluded(child_rel):
                    continue
                yield child, child_rel
        else:
            if is_excluded(rel):
                continue
            yield path, rel


def build_zip(root: Path, output: Path) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    if output.exists():
        output.unlink()
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
        for path, rel in iter_entries(root):
            if path.is_dir():
                add_dir(zf, path, rel)
            else:
                add_file(zf, path, rel)


def validate_zip(output: Path) -> None:
    with zipfile.ZipFile(output) as zf:
        bad = zf.testzip()
        if bad:
            raise zipfile.BadZipFile(f"CRC check failed for {bad}")
        names = set(zf.namelist())
    backslash = [name for name in names if "\\" in name]
    if backslash:
        raise zipfile.BadZipFile(f"Windows-style zip entries found: {backslash[:3]}")
    missing = [name for name in REQUIRED_ENTRIES if name not in names]
    if missing:
        raise zipfile.BadZipFile(f"required module entries missing: {missing}")
    wrapped_roots = [name for name in names if name.startswith("ksu-proxy/")]
    if wrapped_roots:
        raise zipfile.BadZipFile("module files are wrapped in an extra ksu-proxy/ directory")


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        print("usage: build-module-zip.py <module_root> <output_zip>", file=sys.stderr)
        return 2
    root = Path(argv[1]).resolve()
    output = Path(argv[2]).resolve()
    try:
        build_zip(root, output)
        validate_zip(output)
    except Exception as exc:
        if output.exists():
            output.unlink()
        print(f"build-module-zip: {exc}", file=sys.stderr)
        return 1
    print(f"Validated {output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
