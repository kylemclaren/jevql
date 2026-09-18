"""Embedded engine: a private ``jevql serve`` process owned by this client.

The binary is discovered in this order: an explicit path, ``JEVQL_ENGINE_PATH``,
the copy bundled inside this package (platform wheels), then ``jevql`` on PATH.
"""

from __future__ import annotations

import atexit
import json
import os
import secrets
import shutil
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any, Dict, List, Optional

from .errors import JevqlError

READY_TIMEOUT = 15.0
STOP_GRACE = 2.0

INSTALL_HINT = (
    "jevql engine binary not found. `pip install jevql` should have included one for this "
    "platform; if you installed from source, set JEVQL_ENGINE_PATH or install the CLI with "
    "`brew install kylemclaren/tap/jevql`."
)


def bundled_engine_path() -> Path:
    name = "jevql.exe" if sys.platform == "win32" else "jevql"
    return Path(__file__).resolve().parent / "_engine" / name


def resolve_engine(engine_path: Optional[str] = None) -> str:
    """Return the path of the engine binary, or raise a ``transport`` error."""
    candidates: List[str] = []
    if engine_path:
        candidates.append(engine_path)
    env = os.environ.get("JEVQL_ENGINE_PATH")
    if env:
        candidates.append(env)
    bundled = bundled_engine_path()
    if bundled.is_file():
        candidates.append(str(bundled))
    on_path = shutil.which("jevql")
    if on_path:
        candidates.append(on_path)
    for c in candidates:
        if os.path.isfile(c) and os.access(c, os.X_OK):
            return c
    if candidates:
        raise JevqlError(f"jevql engine not usable at {candidates[0]!r}: not an executable file", code="transport")
    raise JevqlError(INSTALL_HINT, code="transport")


class Engine:
    """Spawns and owns one ``jevql serve`` process."""

    def __init__(
        self,
        *,
        engine_path: Optional[str] = None,
        database_url: Optional[str] = None,
        api_key: Optional[str] = None,
        api_url: Optional[str] = None,
        model: Optional[str] = None,
        threshold: Optional[float] = None,
        max_rows: Optional[int] = None,
        cache_path: Optional[str] = None,
        no_cache: bool = False,
        ready_timeout: float = READY_TIMEOUT,
    ):
        self.engine_path = engine_path
        self.database_url = database_url
        self.api_key = api_key
        self.api_url = api_url
        self.model = model
        self.threshold = threshold
        self.max_rows = max_rows
        self.cache_path = cache_path
        self.no_cache = no_cache
        self.ready_timeout = ready_timeout
        self.token = secrets.token_urlsafe(24)
        self.url: Optional[str] = None
        self.version: Optional[str] = None
        self.process: Optional[subprocess.Popen] = None
        self._lock = threading.Lock()
        self._registered = False

    def argv(self) -> List[str]:
        binary = resolve_engine(self.engine_path)
        args = [
            binary,
            "serve",
            "--listen",
            "127.0.0.1:0",
            "--token",
            self.token,
            "--ready-json",
            "--parent-pid",
            str(os.getpid()),
        ]
        if self.api_key:
            args += ["--api-key", self.api_key]
        if self.api_url:
            args += ["--api-url", self.api_url]
        if self.model:
            args += ["--model", self.model]
        if self.threshold is not None:
            args += ["--threshold", str(float(self.threshold))]
        if self.max_rows is not None:
            args += ["--max-rows", str(int(self.max_rows))]
        if self.cache_path:
            args += ["--cache", self.cache_path]
        if self.no_cache:
            args.append("--no-cache")
        if self.database_url:
            args.append(self.database_url)
        return args

    @property
    def running(self) -> bool:
        return self.process is not None and self.process.poll() is None

    def start(self) -> str:
        """Start the engine if needed and return its base URL."""
        with self._lock:
            if self.running and self.url:
                return self.url
            argv = self.argv()
            try:
                proc = subprocess.Popen(
                    argv,
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    env=os.environ.copy(),
                    text=True,
                )
            except OSError as e:
                raise JevqlError(f"failed to start jevql engine ({argv[0]}): {e}", code="transport") from None
            self.process = proc
            if not self._registered:
                atexit.register(self.stop)
                self._registered = True
            ready = self._read_ready(proc)
            self.url = str(ready.get("url") or f"http://{ready.get('listen')}")
            self.version = str(ready.get("version") or "")
            return self.url

    def _read_ready(self, proc: subprocess.Popen) -> Dict[str, Any]:
        result: Dict[str, Any] = {}
        err: List[str] = []

        def reader() -> None:
            assert proc.stdout is not None
            line = proc.stdout.readline()
            if line:
                try:
                    payload = json.loads(line)
                except ValueError:
                    err.append(f"unexpected engine output: {line.strip()!r}")
                    return
                if isinstance(payload, dict) and payload.get("ready"):
                    result.update(payload)
                else:
                    err.append(f"unexpected engine output: {line.strip()!r}")

        t = threading.Thread(target=reader, daemon=True)
        t.start()
        deadline = time.monotonic() + self.ready_timeout
        while t.is_alive() and time.monotonic() < deadline:
            if proc.poll() is not None:
                break
            t.join(0.05)
        if result:
            return result
        exited = proc.poll() is not None
        status = proc.returncode
        tail = self._stderr_tail(proc)
        self.stop()
        if exited:
            raise JevqlError(f"jevql engine exited with status {status} before it was ready: {tail}", code="transport")
        reason = err[0] if err else f"no ready line within {self.ready_timeout:.0f}s"
        raise JevqlError(f"jevql engine did not start: {reason}. {tail}".strip(), code="transport")

    @staticmethod
    def _stderr_tail(proc: subprocess.Popen) -> str:
        if proc.stderr is None:
            return ""
        try:
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=STOP_GRACE)
            data = proc.stderr.read() or ""
        except Exception:  # pragma: no cover - best effort
            return ""
        lines = [ln for ln in data.strip().splitlines() if ln.strip()]
        return " | ".join(lines[-5:])

    def stop(self) -> None:
        proc = self.process
        if proc is None:
            return
        self.process = None
        self.url = None
        if proc.poll() is None:
            try:
                proc.terminate()
                proc.wait(timeout=STOP_GRACE)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=STOP_GRACE)
            except OSError:
                pass
        for stream in (proc.stdout, proc.stderr):
            try:
                if stream:
                    stream.close()
            except OSError:  # pragma: no cover
                pass
