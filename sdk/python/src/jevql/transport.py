from __future__ import annotations

import json
import os
import shutil
import subprocess
import urllib.error
import urllib.request
from abc import ABC, abstractmethod
from typing import Any, Dict, List, Mapping, Optional

from .errors import EXIT_TO_CODE, STATUS_TO_CODE, JevqlError


class Transport(ABC):
    """Something that can run a statement and return the QueryResult document."""

    @abstractmethod
    def run(
        self,
        sql: str,
        *,
        threshold: Optional[float] = None,
        max_rows: Optional[int] = None,
        explain: bool = False,
    ) -> Dict[str, Any]:
        """Return the raw QueryResult dict for one statement."""

    def health(self) -> Dict[str, Any]:
        """Return a health document; transports without one report ok."""
        return {"ok": True}

    def close(self) -> None:  # pragma: no cover - default no-op
        return None


class HttpTransport(Transport):
    """Talks to a running ``jevql serve``."""

    def __init__(self, url: str, token: Optional[str] = None, timeout: float = 60.0):
        self.url = url.rstrip("/")
        self.token = token
        self.timeout = timeout

    def _headers(self) -> Dict[str, str]:
        h = {"Content-Type": "application/json", "Accept": "application/json"}
        if self.token:
            h["Authorization"] = f"Bearer {self.token}"
        return h

    def _request(self, method: str, path: str, body: Optional[Mapping[str, Any]] = None) -> Dict[str, Any]:
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.url + path, data=data, method=method, headers=self._headers())
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read()
                status = resp.status
        except urllib.error.HTTPError as e:
            raw = e.read()
            status = e.code
            payload = _decode(raw)
            code = payload.get("code") if isinstance(payload, dict) else None
            message = payload.get("error") if isinstance(payload, dict) else None
            raise JevqlError(
                str(message or f"HTTP {status}"),
                code=str(code or STATUS_TO_CODE.get(status, "internal")),
                status=status,
            ) from None
        except urllib.error.URLError as e:
            raise JevqlError(f"cannot reach jevql serve at {self.url}: {e.reason}", code="transport") from None
        payload = _decode(raw)
        if not isinstance(payload, dict):
            raise JevqlError("malformed response from jevql serve", code="transport", status=status)
        return payload

    def run(self, sql, *, threshold=None, max_rows=None, explain=False):
        body: Dict[str, Any] = {"sql": sql}
        if threshold is not None:
            body["threshold"] = float(threshold)
        if max_rows is not None:
            body["max_rows"] = int(max_rows)
        if explain:
            body["explain"] = True
        return self._request("POST", "/v1/query", body)

    def health(self) -> Dict[str, Any]:
        return self._request("GET", "/v1/health")


class CliTransport(Transport):
    """Runs the ``jevql`` binary with ``--json-table`` for every call."""

    def __init__(
        self,
        binary: str = "jevql",
        database_url: Optional[str] = None,
        api_key: Optional[str] = None,
        api_url: Optional[str] = None,
        env: Optional[Mapping[str, str]] = None,
        cwd: Optional[str] = None,
        timeout: Optional[float] = None,
    ):
        self.binary = binary
        self.database_url = database_url
        self.api_key = api_key
        self.api_url = api_url
        self.env = dict(env) if env else None
        self.cwd = cwd
        self.timeout = timeout

    def argv(self, sql: str, *, threshold=None, max_rows=None, explain=False) -> List[str]:
        args = [self.binary, "--json-table"]
        if threshold is not None:
            args += ["--threshold", str(float(threshold))]
        if max_rows is not None:
            args += ["--max-rows", str(int(max_rows))]
        if explain:
            args.append("--explain")
        if self.api_key:
            args += ["--api-key", self.api_key]
        if self.api_url:
            args += ["--api-url", self.api_url]
        args += ["-c", sql]
        if self.database_url:
            args.append(self.database_url)
        return args

    def run(self, sql, *, threshold=None, max_rows=None, explain=False):
        argv = self.argv(sql, threshold=threshold, max_rows=max_rows, explain=explain)
        env = os.environ.copy()
        if self.env:
            env.update(self.env)
        if shutil.which(self.binary) is None and not os.path.exists(self.binary):
            raise JevqlError(
                f"jevql binary not found ({self.binary!r}); install it with: brew install kylemclaren/tap/jevql",
                code="transport",
            )
        try:
            proc = subprocess.run(
                argv,
                capture_output=True,
                text=True,
                env=env,
                cwd=self.cwd,
                timeout=self.timeout,
            )
        except (OSError, subprocess.SubprocessError) as e:
            raise JevqlError(f"failed to run {self.binary}: {e}", code="transport") from None
        if proc.returncode != 0:
            message = (proc.stderr or proc.stdout or "").strip() or f"jevql exited with status {proc.returncode}"
            raise JevqlError(message, code=EXIT_TO_CODE.get(proc.returncode, "internal"), status=proc.returncode)
        docs = [line for line in proc.stdout.splitlines() if line.strip()]
        if not docs:
            raise JevqlError("jevql produced no output", code="transport")
        try:
            payload = json.loads(docs[-1])
        except ValueError:
            raise JevqlError("malformed --json-table output from jevql", code="transport") from None
        if not isinstance(payload, dict) or "columns" not in payload:
            if isinstance(payload, dict) and payload.get("error"):
                raise JevqlError(str(payload["error"]), code=str(payload.get("code", "internal")))
            raise JevqlError("malformed --json-table output from jevql", code="transport")
        return payload

    def health(self) -> Dict[str, Any]:
        path = shutil.which(self.binary) or (self.binary if os.path.exists(self.binary) else None)
        if not path:
            return {"ok": False, "error": f"{self.binary} not found"}
        try:
            proc = subprocess.run([self.binary, "--version"], capture_output=True, text=True, timeout=30)
        except (OSError, subprocess.SubprocessError) as e:
            return {"ok": False, "error": str(e)}
        version = proc.stdout.strip().split()[-1] if proc.stdout.strip() else ""
        return {"ok": proc.returncode == 0, "version": version, "binary": path}


def _decode(raw: bytes) -> Any:
    if not raw:
        return {}
    try:
        return json.loads(raw.decode())
    except (UnicodeDecodeError, ValueError):
        return {"error": raw.decode(errors="replace").strip()}
