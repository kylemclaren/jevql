"""Embedded engine: spawn, ready line, token, flags, discovery, shutdown."""

import os
import shutil
import stat
import sys
import time

import pytest

from jevql import Engine, Jevql, JevqlError, resolve_engine
from jevql import engine as engine_mod


def test_query_starts_engine_and_uses_token(fake_engine):
    db = Jevql(engine_path=fake_engine.binary, no_cache=True)
    assert db.embedded
    res = db.query("SELECT 1 AS one", threshold=0.7)
    assert res.rows == [["SELECT 1 AS one", 0.7]]
    assert db.health()["ok"] is True
    started = fake_engine.started_with()
    argv = started["argv"]
    assert argv[:3] == ["serve", "--listen", "127.0.0.1:0"]
    assert "--ready-json" in argv and "--no-cache" in argv
    assert argv[argv.index("--parent-pid") + 1] == str(os.getpid())
    token = argv[argv.index("--token") + 1]
    assert len(token) >= 24 and token == db.transport.engine.token
    db.close()


def test_engine_started_once(fake_engine):
    db = Jevql(engine_path=fake_engine.binary)
    db.query("SELECT 1")
    pid = db.transport.engine.process.pid
    db.query("SELECT 2")
    assert db.transport.engine.process.pid == pid
    db.close()


def test_flags_and_env_passthrough(fake_engine, monkeypatch):
    monkeypatch.setenv("FAKE_ENGINE_MARK", "inherited")
    db = Jevql(
        engine_path=fake_engine.binary,
        database_url="postgres://u:p@h/db",
        api_key="tsk_x",
        api_url="https://gw.example/v1/systemone",
        model="jev-preview",
        threshold=0.8,
        max_rows=50,
        cache_path="/tmp/c.db",
    )
    db.health()
    started = fake_engine.started_with()
    argv = started["argv"]
    assert argv[-1] == "postgres://u:p@h/db"
    for flag, val in [("--api-key", "tsk_x"), ("--api-url", "https://gw.example/v1/systemone"), ("--model", "jev-preview"),
                      ("--threshold", "0.8"), ("--max-rows", "50"), ("--cache", "/tmp/c.db")]:
        assert argv[argv.index(flag) + 1] == val
    assert "--no-cache" not in argv
    assert started["env_marker"] == "inherited"
    db.close()


def test_errors_map_codes(fake_engine):
    with Jevql(engine_path=fake_engine.binary) as db:
        with pytest.raises(JevqlError) as ei:
            db.query("SELECT * FROM nope")
        assert ei.value.code == "sql" and ei.value.status == 400


def test_close_terminates_engine(fake_engine):
    db = Jevql(engine_path=fake_engine.binary)
    db.query("SELECT 1")
    proc = db.transport.engine.process
    assert proc.poll() is None
    db.close()
    assert proc.poll() is not None
    assert db.transport.engine.process is None
    # usable again after close: a fresh engine is spawned
    assert db.query("SELECT 3").rows[0][0] == "SELECT 3"
    db.close()


def test_context_manager_closes(fake_engine):
    with Jevql(engine_path=fake_engine.binary) as db:
        db.query("SELECT 1")
        proc = db.transport.engine.process
    assert proc.poll() is not None


def test_never_ready_times_out(fake_engine, monkeypatch):
    monkeypatch.setenv("FAKE_ENGINE_MODE", "never-ready")
    eng = Engine(engine_path=fake_engine.binary, ready_timeout=1.0)
    t0 = time.monotonic()
    with pytest.raises(JevqlError) as ei:
        eng.start()
    assert time.monotonic() - t0 < 5
    assert ei.value.code == "transport"
    assert "did not start" in str(ei.value) and "warming up" in str(ei.value)
    assert eng.process is None


def test_crash_before_ready_reports_stderr(fake_engine, monkeypatch):
    monkeypatch.setenv("FAKE_ENGINE_MODE", "crash")
    with pytest.raises(JevqlError) as ei:
        Jevql(engine_path=fake_engine.binary).query("SELECT 1")
    assert ei.value.code == "transport"
    assert "status 3" in str(ei.value) and "boom" in str(ei.value)


def test_garbage_output(fake_engine, monkeypatch):
    monkeypatch.setenv("FAKE_ENGINE_MODE", "garbage")
    with pytest.raises(JevqlError) as ei:
        Engine(engine_path=fake_engine.binary, ready_timeout=2).start()
    assert "unexpected engine output" in str(ei.value)


# --- discovery -------------------------------------------------------------


def _exe(path):
    path.write_text("#!/bin/sh\nexit 0\n")
    path.chmod(0o755)
    return str(path)


def test_discovery_order(tmp_path, monkeypatch):
    explicit = _exe(tmp_path / "explicit")
    env = _exe(tmp_path / "from-env")
    bundled = tmp_path / "bundled" / "jevql"
    bundled.parent.mkdir()
    _exe(bundled)
    pathdir = tmp_path / "bin"
    pathdir.mkdir()
    on_path = _exe(pathdir / "jevql")

    monkeypatch.setenv("PATH", str(pathdir))
    monkeypatch.setenv("JEVQL_ENGINE_PATH", env)
    monkeypatch.setattr(engine_mod, "bundled_engine_path", lambda: bundled)

    assert resolve_engine(explicit) == explicit
    assert resolve_engine() == env
    monkeypatch.delenv("JEVQL_ENGINE_PATH")
    assert resolve_engine() == str(bundled)
    monkeypatch.setattr(engine_mod, "bundled_engine_path", lambda: tmp_path / "missing" / "jevql")
    assert resolve_engine() == on_path


def test_missing_engine_message(tmp_path, monkeypatch):
    monkeypatch.setenv("PATH", str(tmp_path))
    monkeypatch.delenv("JEVQL_ENGINE_PATH", raising=False)
    monkeypatch.setattr(engine_mod, "bundled_engine_path", lambda: tmp_path / "nope" / "jevql")
    with pytest.raises(JevqlError) as ei:
        Jevql().query("SELECT 1")
    assert ei.value.code == "transport"
    msg = str(ei.value)
    assert "pip install jevql" in msg and "brew install kylemclaren/tap/jevql" in msg and "JEVQL_ENGINE_PATH" in msg


def test_non_executable_engine_is_rejected(tmp_path, monkeypatch):
    bad = tmp_path / "jevql"
    bad.write_text("nope")
    bad.chmod(stat.S_IRUSR)
    monkeypatch.setenv("PATH", str(tmp_path))
    with pytest.raises(JevqlError) as ei:
        resolve_engine(str(bad))
    assert "not an executable" in str(ei.value)


def test_bundled_path_is_inside_package():
    p = engine_mod.bundled_engine_path()
    assert p.parent.name == "_engine" and p.parent.parent.name == "jevql"


@pytest.mark.skipif(not os.environ.get("JEVQL_TEST_DATABASE_URL"), reason="JEVQL_TEST_DATABASE_URL not set")
def test_real_engine_roundtrip():
    """Real jevql binary on PATH against a real database."""
    assert shutil.which("jevql"), "jevql must be on PATH"
    with Jevql(database_url=os.environ["JEVQL_TEST_DATABASE_URL"], no_cache=True) as db:
        assert db.health()["ok"] is True
        res = db.query("SELECT 1 AS one, 'x' AS s")
        assert res.columns == ["one", "s"] and res.rows == [[1, "x"]]
        assert res.jev is False
        assert db.judge("is empty", []).answers == []
        with pytest.raises(JevqlError):
            db.judge("", [{"a": 1}])
        proc = db.transport.engine.process
        assert proc.poll() is None
    assert proc.poll() is not None
