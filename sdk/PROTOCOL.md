# jevql machine protocol

Every SDK talks to the Go core through one of two transports that share the
same JSON document shapes.

## Transport A: `jevql serve` (HTTP, localhost)

```
jevql serve [--listen 127.0.0.1:7433] [--token SECRET]
```

- `GET /v1/health` → `200 {"ok": true, "version": "0.1.0", "model": "jev-latest"}`
- `POST /v1/query` with body `QueryRequest` → `200 QueryResult` or an `ErrorBody`
  with status `400` (SQL / usage error, `code: "sql"`), `402` (budget guard,
  `code: "budget"`), `502` (TypeSafe API error, `code: "api"`), `401` (bad or
  missing bearer token, `code: "auth"`), `500` (`code: "internal"`).
- If the server was started with `--token` (or `JEVQL_TOKEN`), every request
  needs `Authorization: Bearer <token>`.
- Content type is `application/json` both ways.

## Transport B: the CLI

```
jevql --json-table -c "<sql>"
```

Prints one `QueryResult` JSON document per statement, one per line, to stdout.
Errors go to stderr as text; the exit code is `1` for SQL/usage errors and `2`
for API/budget errors. With `--explain`, the document carries `explain`.

## Shapes

```jsonc
// QueryRequest
{
  "sql": "SELECT name FROM people WHERE jev(people, 'could work from home')",
  "threshold": 0.7,     // optional, overrides the server default for this query
  "explain": false,     // optional, plan and cost only, no TypeSafe calls
  "max_rows": 500       // optional, overrides --max-rows for this query
}

// QueryResult
{
  "columns": ["name", "p"],           // output column names in order; [] for row-less statements
  "rows": [["Ada", 0.93], ["Bo", 0.4]], // JSON values: string, number, boolean, null, object, array
  "row_count": 2,
  "tag": "SELECT 2",                  // Postgres-style command tag ("INSERT 0 1", "CREATE TABLE", ...)
  "jev": true,                        // whether jev_* functions were evaluated
  "stats": {                          // null unless jev is true
    "collect_rows": 12, "judged": 12, "requests": 1, "cache_hits": 0,
    "input_tokens": 2100, "output_tokens": 40, "usd": 0.0001, "elapsed_ms": 1350
  },
  "explain": null                     // Explain object when explain was requested
}

// Explain
{
  "collect_sql": "SELECT ... FROM people",
  "rows": 12, "sources": 1, "questions": 1, "judgements": 12, "batches": 1,
  "avg_row_chars": 201, "tokens": 1100, "usd": 0.00005, "server_order": false,
  "question_list": ["noul \"could work from home\" on people"]
}

// ErrorBody
{ "error": "human readable message", "code": "sql" | "budget" | "api" | "auth" | "internal" }
```

Value encoding follows the CLI's canonical JSON: timestamps are RFC 3339
strings in UTC, `numeric` is a JSON number, `bytea` is base64, arrays and
json/jsonb columns are nested JSON.

## Embedded engine (how the SDKs start `jevql serve` themselves)

The TypeScript and Python packages bundle the jevql binary for the current
platform and run it as a private engine. The contract:

```
jevql serve --listen 127.0.0.1:0 --token <random> --ready-json --parent-pid <host pid> [--api-key K] [--api-url U] [--model M] [--threshold T] [--max-rows N] [--cache P | --no-cache] [postgres://...]
```

- `--listen 127.0.0.1:0` binds a free port on loopback.
- `--ready-json` prints exactly one JSON line to stdout once listening:
  `{"ready": true, "listen": "127.0.0.1:41661", "url": "http://127.0.0.1:41661", "version": "0.2.0", "pid": 55851}`.
  Nothing else is written to stdout. Diagnostics go to stderr.
- `--parent-pid` makes the engine exit within about two seconds of that
  process disappearing, so a crashed host never leaves an orphan.
- The SDK generates the token, passes it with `--token`, and sends it as a
  bearer token on every request. Environment variables (`DATABASE_URL`,
  `PG*`, `TYPESAFE_API_KEY`, `TYPESAFE_API_URL`, `JEV_THRESHOLD`) are inherited
  by the engine, so the SDK only passes flags for options given explicitly.
- Stopping: send SIGTERM (or close stdin and wait); the engine shuts down
  gracefully. The SDK should do this on `close()` and at host exit.

Engine discovery order: an explicit `enginePath` option, then the
`JEVQL_ENGINE_PATH` environment variable, then the bundled platform package,
then a `jevql` binary on `PATH`. If none exists the SDK raises a `transport`
error that names the install command.
