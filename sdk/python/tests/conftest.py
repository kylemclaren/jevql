import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest

SAMPLE = {
    "columns": ["name", "p"],
    "rows": [["Ada", 0.93], ["Bo", 0.12]],
    "row_count": 2,
    "tag": "SELECT 2",
    "jev": True,
    "stats": {
        "collect_rows": 2,
        "judged": 2,
        "requests": 1,
        "cache_hits": 0,
        "input_tokens": 400,
        "output_tokens": 20,
        "usd": 0.0000168,
        "elapsed_ms": 812.5,
    },
    "explain": None,
}

EXPLAIN = {
    "columns": [],
    "rows": [],
    "row_count": 0,
    "tag": "",
    "jev": True,
    "stats": None,
    "explain": {
        "collect_sql": "SELECT name FROM people",
        "rows": 12,
        "sources": 1,
        "questions": 1,
        "judgements": 12,
        "batches": 1,
        "avg_row_chars": 201.0,
        "tokens": 1100,
        "usd": 0.00005,
        "server_order": False,
        "question_list": ["noul \"could work from home\" on people"],
    },
}


class MockServer:
    """Threaded jevql-serve mock that records requests."""

    def __init__(self):
        self.requests = []
        self.next = ("ok", 200)  # ("ok", 200) | ("error", status, code, message) | ("raw", status, bytes)
        self.token = None
        srv = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _send(self, status, body: bytes, ctype="application/json"):
                self.send_response(status)
                self.send_header("Content-Type", ctype)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self):
                srv.requests.append({"method": "GET", "path": self.path, "headers": dict(self.headers)})
                if srv.token and self.headers.get("Authorization") != f"Bearer {srv.token}":
                    return self._send(401, json.dumps({"error": "unauthorized", "code": "auth"}).encode())
                if self.path == "/v1/health":
                    return self._send(200, json.dumps({"ok": True, "version": "0.1.0", "model": "jev-latest"}).encode())
                self._send(404, json.dumps({"error": "not found", "code": "internal"}).encode())

            def do_POST(self):
                n = int(self.headers.get("Content-Length", 0))
                body = json.loads(self.rfile.read(n) or b"{}")
                srv.requests.append({"method": "POST", "path": self.path, "headers": dict(self.headers), "body": body})
                if srv.token and self.headers.get("Authorization") != f"Bearer {srv.token}":
                    return self._send(401, json.dumps({"error": "unauthorized", "code": "auth"}).encode())
                nxt = srv.next
                if self.path == "/v1/judge" and nxt[0] == "ok":
                    if not body.get("question"):
                        return self._send(400, json.dumps({"error": "question is required", "code": "sql"}).encode())
                    rows = body.get("rows") or []
                    answers = [{"p": 0.9 if r.get("name") == "Ada" else 0.1, "pass": r.get("name") == "Ada", "confidence": 0.9} for r in rows]
                    doc = {"answers": answers, "stats": {"collect_rows": len(rows), "judged": len(rows), "requests": 1, "cache_hits": 0,
                                                         "input_tokens": 100, "output_tokens": 5, "usd": 0.000004, "elapsed_ms": 10}}
                    return self._send(200, json.dumps(doc).encode())
                if nxt[0] == "ok":
                    doc = EXPLAIN if body.get("explain") else SAMPLE
                    return self._send(200, json.dumps(doc).encode())
                if nxt[0] == "error":
                    _, status, code, message = nxt
                    return self._send(status, json.dumps({"error": message, "code": code}).encode())
                _, status, raw = nxt
                self._send(status, raw, ctype="text/plain")

        self.httpd = HTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_port}"
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    def stop(self):
        self.httpd.shutdown()
        self.httpd.server_close()


@pytest.fixture
def server():
    s = MockServer()
    yield s
    s.stop()


FAKE_ENGINE = r'''#!/usr/bin/env python3
"""Fake jevql engine for tests: honours the embedded-engine contract."""
import json, os, sys, time
from http.server import BaseHTTPRequestHandler, HTTPServer

args = sys.argv[1:]
log = os.environ.get("FAKE_ENGINE_LOG")
if log:
    with open(log, "w") as f:
        json.dump({"argv": args, "env_marker": os.environ.get("FAKE_ENGINE_MARK")}, f)
mode = os.environ.get("FAKE_ENGINE_MODE", "ok")
if mode == "never-ready":
    sys.stderr.write("engine: still warming up\n"); sys.stderr.flush()
    time.sleep(60); sys.exit(0)
if mode == "crash":
    sys.stderr.write("ERROR: connection to server failed: boom\n"); sys.stderr.flush()
    sys.exit(3)
if mode == "garbage":
    print("not json at all"); sys.stdout.flush(); time.sleep(60); sys.exit(0)
token = args[args.index("--token") + 1]

class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def _auth(self):
        if self.headers.get("Authorization") != "Bearer " + token:
            self._send(401, {"error": "missing or invalid bearer token", "code": "auth"}); return False
        return True
    def _send(self, status, body):
        data = json.dumps(body).encode()
        self.send_response(status); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data))); self.end_headers(); self.wfile.write(data)
    def do_GET(self):
        if not self._auth(): return
        self._send(200, {"ok": True, "version": "fake", "model": "jev-test"})
    def do_POST(self):
        if not self._auth(): return
        n = int(self.headers.get("Content-Length", 0)); req = json.loads(self.rfile.read(n) or b"{}")
        if req.get("sql") == "SELECT * FROM nope":
            self._send(400, {"error": "relation \"nope\" does not exist", "code": "sql"}); return
        self._send(200, {"columns": ["sql", "threshold"], "rows": [[req.get("sql"), req.get("threshold")]],
                         "row_count": 1, "tag": "SELECT 1", "jev": False, "stats": None, "explain": None})

srv = HTTPServer(("127.0.0.1", 0), H)
print(json.dumps({"ready": True, "listen": "127.0.0.1:%d" % srv.server_port,
                  "url": "http://127.0.0.1:%d" % srv.server_port, "version": "fake", "pid": os.getpid()}))
sys.stdout.flush()
srv.serve_forever()
'''


@pytest.fixture
def fake_engine(tmp_path, monkeypatch):
    """A fake engine binary plus a way to read what it was started with."""
    path = tmp_path / "fake-jevql"
    path.write_text(FAKE_ENGINE)
    path.chmod(0o755)
    log = tmp_path / "engine.json"
    monkeypatch.setenv("FAKE_ENGINE_LOG", str(log))
    monkeypatch.delenv("JEVQL_ENGINE_PATH", raising=False)

    class Fake:
        binary = str(path)

        def started_with(self):
            return json.loads(log.read_text())

    return Fake()
