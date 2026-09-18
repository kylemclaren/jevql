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


@pytest.fixture
def fake_jevql(tmp_path):
    """A fake jevql binary that records argv/env and replays canned behaviour.

    Control file: <tmp>/behaviour.json {"exit": int, "stdout": str, "stderr": str}
    Recording:    <tmp>/argv.json, <tmp>/env.json
    """
    control = tmp_path / "behaviour.json"
    control.write_text(json.dumps({"exit": 0, "stdout": json.dumps(SAMPLE) + "\n", "stderr": ""}))
    script = tmp_path / "jevql"
    script.write_text(
        "#!/usr/bin/env python3\n"
        "import json, os, sys\n"
        f"d = {str(tmp_path)!r}\n"
        "if sys.argv[1:] == ['--version']:\n"
        "    print('jevql 0.1.0'); sys.exit(0)\n"
        "json.dump(sys.argv[1:], open(os.path.join(d, 'argv.json'), 'w'))\n"
        "json.dump({k: v for k, v in os.environ.items() if k in ('DATABASE_URL', 'TYPESAFE_API_KEY', 'TYPESAFE_API_URL', 'JEVQL_TEST')}, open(os.path.join(d, 'env.json'), 'w'))\n"
        "b = json.load(open(os.path.join(d, 'behaviour.json')))\n"
        "sys.stdout.write(b['stdout']); sys.stderr.write(b['stderr'])\n"
        "sys.exit(b['exit'])\n"
    )
    script.chmod(0o755)

    class Fake:
        path = str(script)
        dir = tmp_path

        def set(self, exit=0, stdout="", stderr=""):
            control.write_text(json.dumps({"exit": exit, "stdout": stdout, "stderr": stderr}))

        def argv(self):
            return json.loads((tmp_path / "argv.json").read_text())

        def env(self):
            return json.loads((tmp_path / "env.json").read_text())

    return Fake()
