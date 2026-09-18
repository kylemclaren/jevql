# jevql (TypeScript SDK)

Client for [jevql](https://github.com/kylemclaren/jevql): semantic SQL on
vanilla Postgres. Write `WHERE jev(people, 'could work from home')`; the
jevql binary runs the SQL on Postgres, judges rows with TypeSafe's Jev model,
and returns a table. The SDK has no runtime dependencies and needs Node 18+
or Bun.

The parsing and two-pass execution live in the Go binary, so install it
first:

```bash
brew install kylemclaren/tap/jevql     # or: curl -fsSL https://<site>/install.sh | sh
npm i jevql
```

## Two transports

**HTTP** talks to a long-running `jevql serve` (one process, one warm cache):

```ts
import { Jevql } from "jevql"

const db = new Jevql({ url: "http://127.0.0.1:7433", token: process.env.JEVQL_TOKEN })

const res = await db.query(
  "SELECT name, jev_prob(people, 'could work from home') AS p FROM people WHERE country = 'PT' ORDER BY p DESC LIMIT 5",
  { threshold: 0.6 },
)
res.columns   // ["name", "p"]
res.rows      // [["Miguel Costa", 0.94], ...]
res.stats     // { judged: 6, requests: 1, cache_hits: 0, input_tokens: 1200, usd: 0.00005, ... }

const rows = await db.queryObjects("SELECT * FROM tickets WHERE jev(tickets, 'is about billing')")
// [{ id: 1, subject: "Charged twice", ... }]
```

Start the server with `jevql serve --listen 127.0.0.1:7433 [--token ...]`.

**CLI** spawns the binary per call. Good for scripts and notebooks; no server
to manage:

```ts
const db = new Jevql({
  cli: {
    databaseUrl: process.env.DATABASE_URL,   // optional; PG* env works too
    apiKey: process.env.TYPESAFE_API_KEY,    // optional; env works too
  },
})
const res = await db.query("SELECT 1")
```

Both transports return the same `QueryResult`:

```ts
interface QueryResult {
  columns: string[]
  rows: unknown[][]
  row_count: number
  tag: string            // "SELECT 5", "INSERT 0 1"
  jev: boolean           // true when jev_* calls were evaluated
  stats: Stats | null    // tokens, cost, cache hits, elapsed
  explain: Explain | null
}
```

## Explain before you pay

```ts
const plan = await db.explain("SELECT * FROM people WHERE jev(people, 'could work from home')")
plan.collect_sql  // what Postgres actually runs
plan.rows         // rows after SQL filters (all of them get judged)
plan.usd          // rough cost, no TypeSafe call made
```

## Errors

Every failure throws `JevqlError` with a `code`:

| code        | meaning                                              |
|-------------|------------------------------------------------------|
| `sql`       | Postgres or jevql rejected the statement             |
| `budget`    | `--max-rows` / `--max-chars` guard stopped it, no HTTP made |
| `api`       | TypeSafe returned an error                            |
| `auth`      | bad or missing server token                           |
| `internal`  | server-side failure                                   |
| `transport` | could not reach the server or run the binary          |

```ts
import { JevqlError } from "jevql"
try {
  await db.query("SELECT * FROM people WHERE jev(people, 'x') OR country = 'PT'")
} catch (e) {
  if (e instanceof JevqlError && e.code === "sql") console.error(e.message) // OR jev(...) is not supported in v1 ...
}
```

`e.status` carries the HTTP status (HTTP transport) or the process exit code
(CLI transport).

## Development

```bash
bun install
bun run typecheck
bun test                              # mocks only
JEVQL_SERVE_URL=http://127.0.0.1:7433 bun test   # also hits a live server
bun run build                         # emits dist/
```
