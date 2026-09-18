#!/usr/bin/env python3
"""Build one platform wheel per release tarball.

    python scripts/build_wheels.py --version 0.2.0 --tarballs dist/tarballs --out dist/wheels

For each platform it unpacks `jevql_<version>_<os>_<arch>.tar.gz`, drops the
`jevql` binary into src/jevql/_engine/, builds a wheel, retags it for that
platform and removes the binary again.
"""

from __future__ import annotations

import argparse
import glob
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

PLATFORMS = {
    ("darwin", "arm64"): "macosx_11_0_arm64",
    ("darwin", "amd64"): "macosx_10_15_x86_64",
    ("linux", "amd64"): "manylinux_2_17_x86_64.manylinux2014_x86_64",
    ("linux", "arm64"): "manylinux_2_17_aarch64.manylinux2014_aarch64",
}

ROOT = Path(__file__).resolve().parents[1]
ENGINE_DIR = ROOT / "src" / "jevql" / "_engine"


def ensure_tools() -> None:
    for mod in ("build", "wheel"):
        if subprocess.run([sys.executable, "-c", f"import {mod}"], capture_output=True).returncode != 0:
            subprocess.check_call([sys.executable, "-m", "pip", "install", "--quiet", mod])


def extract_binary(tarball: Path, dest: Path) -> None:
    with tarfile.open(tarball, "r:gz") as tf:
        member = next((m for m in tf.getmembers() if m.name.rstrip("/").split("/")[-1] == "jevql" and m.isfile()), None)
        if member is None:
            raise SystemExit(f"{tarball}: no jevql binary inside")
        src = tf.extractfile(member)
        assert src is not None
        dest.parent.mkdir(parents=True, exist_ok=True)
        with open(dest, "wb") as out:
            shutil.copyfileobj(src, out)
    dest.chmod(0o755)


def build_one(version: str, tarball: Path, tag: str, out: Path) -> Path:
    binary = ENGINE_DIR / "jevql"
    extract_binary(tarball, binary)
    try:
        with tempfile.TemporaryDirectory() as tmp:
            subprocess.check_call([sys.executable, "-m", "build", "--wheel", "--outdir", tmp, str(ROOT)],
                                  stdout=subprocess.DEVNULL)
            built = glob.glob(os.path.join(tmp, "*.whl"))
            if len(built) != 1:
                raise SystemExit(f"expected one wheel, got {built}")
            subprocess.check_call([sys.executable, "-m", "wheel", "tags", "--remove",
                                   "--python-tag", "py3", "--abi-tag", "none", "--platform-tag", tag, built[0]],
                                  stdout=subprocess.DEVNULL)
            tagged = glob.glob(os.path.join(tmp, "*.whl"))
            if len(tagged) != 1:
                raise SystemExit(f"expected one retagged wheel, got {tagged}")
            out.mkdir(parents=True, exist_ok=True)
            target = out / os.path.basename(tagged[0])
            shutil.move(tagged[0], target)
            return target
    finally:
        if binary.exists():
            binary.unlink()


def main(argv: list[str] | None = None) -> list[Path]:
    ap = argparse.ArgumentParser()
    ap.add_argument("--version", required=True)
    ap.add_argument("--tarballs", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--only", nargs="*", help="platform keys like linux_amd64 (default: all present)")
    args = ap.parse_args(argv)
    ensure_tools()
    made: list[Path] = []
    for (osname, arch), tag in PLATFORMS.items():
        key = f"{osname}_{arch}"
        if args.only and key not in args.only:
            continue
        tarball = args.tarballs / f"jevql_{args.version}_{key}.tar.gz"
        if not tarball.exists():
            print(f"skip {key}: {tarball} missing", file=sys.stderr)
            continue
        made.append(build_one(args.version, tarball, tag, args.out))
        print(made[-1])
    if not made:
        raise SystemExit("no wheels built")
    return made


if __name__ == "__main__":
    main()
