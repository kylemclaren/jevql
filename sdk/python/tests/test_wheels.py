"""The platform wheel builder embeds the engine binary and retags the wheel."""

import io
import subprocess
import sys
import tarfile
import zipfile
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
import build_wheels  # noqa: E402


def _fake_tarball(dirpath: Path, version: str, key: str) -> Path:
    tb = dirpath / f"jevql_{version}_{key}.tar.gz"
    with tarfile.open(tb, "w:gz") as tf:
        script = b"#!/bin/sh\necho jevql fake\n"
        info = tarfile.TarInfo("jevql")
        info.size = len(script)
        info.mode = 0o755
        tf.addfile(info, io.BytesIO(script))
        readme = b"readme"
        i2 = tarfile.TarInfo("README.md")
        i2.size = len(readme)
        tf.addfile(i2, io.BytesIO(readme))
    return tb


@pytest.mark.parametrize("key,tag", [
    ("linux_amd64", "manylinux_2_17_x86_64.manylinux2014_x86_64"),
    ("darwin_arm64", "macosx_11_0_arm64"),
])
def test_build_wheel_for_platform(tmp_path, key, tag):
    tarballs = tmp_path / "tarballs"
    tarballs.mkdir()
    _fake_tarball(tarballs, "9.9.9", key)
    out = tmp_path / "wheels"
    made = build_wheels.main(["--version", "9.9.9", "--tarballs", str(tarballs), "--out", str(out), "--only", key])
    assert len(made) == 1
    whl = made[0]
    assert whl.name.startswith("jevql-0.2.0-py3-none-") and whl.name.endswith(".whl")
    # compressed tag sets are order-insensitive
    assert set(whl.name[len("jevql-0.2.0-py3-none-"):-len(".whl")].split(".")) == set(tag.split("."))
    with zipfile.ZipFile(whl) as z:
        names = z.namelist()
        assert "jevql/_engine/jevql" in names
        assert z.read("jevql/_engine/jevql").startswith(b"#!/bin/sh")
        info = z.getinfo("jevql/_engine/jevql")
        assert (info.external_attr >> 16) & 0o111, "binary must be executable inside the wheel"
        wheel_meta = [n for n in names if n.endswith("WHEEL")][0]
        tags = {ln.split(b": ", 1)[1].decode() for ln in z.read(wheel_meta).splitlines() if ln.startswith(b"Tag: ")}
        assert tags == {f"py3-none-{t}" for t in tag.split(".")}
    # the working tree is left clean
    assert not (ROOT / "src" / "jevql" / "_engine" / "jevql").exists()


def test_missing_tarballs_fail(tmp_path):
    with pytest.raises(SystemExit):
        build_wheels.main(["--version", "0.0.0", "--tarballs", str(tmp_path), "--out", str(tmp_path / "o")])


def test_source_install_without_binary_imports():
    out = subprocess.run([sys.executable, "-c", "import jevql; print(jevql.__version__)"],
                         capture_output=True, text=True, cwd=ROOT, env={"PYTHONPATH": str(ROOT / "src"), "PATH": ""})
    assert out.returncode == 0 and out.stdout.strip() == "0.2.0"
