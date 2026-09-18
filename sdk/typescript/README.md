# jevql (TypeScript SDK)

Semantic SQL on vanilla Postgres from Node or Bun. Write
`WHERE jev(people, 'could work from home')`; the jevql engine runs the SQL
on Postgres, judges rows with TypeSafe's Jev model, and returns a table.

```bash
npm i jevql
```

That is the whole install. The package bundles the jevql engine binary for
your platform (macOS arm64/x64, Linux x64/arm64) as an optional dependency
and runs it privately for the lifetime of your process. Zero runtime
dependencies, Node 18+ or Bun.

## Embedded (default)

```ts
import { Jevql } from "jevql"

const db = new Jevql() // DATABASE_URL and TYPESAFE_API_KEY from the environment

const res = await db.query(
  "SELECT name, jev_prob(people, 'could work from home') AS p FROM people WHERE country = 'PT' ORDER BY p DESC LIMIT 5",
  { threshold: 0.6 },
)
res.columns // ["name", "p"]
res.rows    // [["Miguel Costa", 0.94], ...]
res.stats   // { judged: 6, requests: 1, cache_hits: 0, input_tokens: 1200, usd: 0.00005, ... }

const rows = await db.queryObjects("SELECT * FROM tickets WHERE jev(tickets, 'is about billing')")
// [{ id: 1, subject: "Charged twice", ... }]

const plan = await db.explain("SELECT * FROM people WHERE jev(people, 'x')") // no TypeSafe call

await db.close() // stops the engine; also happens automatically at process exit
```

`await using db = new Jevql()` disposes it at the end of the block.

Explicit options, all optional (environment variables apply otherwise):

| option | flag on the engine |
|---|---|
| `databaseUrl` | connection URL (else `DATABASE_URL` / `PG*`) |
| `apiKey`, `apiUrl`, `model` | `--api-key`, `--api-url`, `--model` |
| `threshold`, `maxRows` | `--threshold`, `--max-rows` |
| `cachePath`, `noCache` | `--cache`, `--no-cache` |
| `enginePath` | which binary to run |
| `env` | extra environment for the engine |
| `startTimeout` | seconds to wait for the engine (default 15) |

The engine is started on the first call (or `await db.start()`), listens on
a random loopback port with a random bearer token, and exits by itself if
your process dies.

## Remote

Point the client at a `jevql serve` you run elsewhere (one shared cache, the
TypeSafe key kept on the server):

```ts
const db = new Jevql({ url: "http://127.0.0.1:7433", token: process.env.JEVQL_TOKEN })
```

Start that server with `jevql serve --listen 0.0.0.0:7433 --token secret`.

## Engine discovery

In order: `enginePath`, the `JEVQL_ENGINE_PATH` environment variable, the
bundled `@jevql/engine-<os>-<arch>` package, then a `jevql` binary on
`PATH`. If none is found the error names the install commands
(`brew install kylemclaren/tap/jevql` or `npm i @jevql/engine-...`).

## Judging rows you already have

```ts
const res = await db.judge({
  question: "is about billing",
  rows: [{ subject: "Charged twice" }, { subject: "API returns 500" }],
})
res.answers // [{ p: 0.93, pass: true, confidence: 0.93 }, { p: 0.04, pass: false, ... }]
```

`kind: "choice"` with `options`, or `kind: "score"` with ordered levels, work the same way; answers carry `choice`/`probabilities` or `score`/`norm`. Identical rows are judged once and answers share the cache with `query`.

## Errors

Every failure is a `JevqlError` with `code` (`sql`, `budget`, `api`, `auth`,
`internal`, `transport`) and, when it came from the engine, `status`:

```ts
import { JevqlError } from "jevql"
try {
  await db.query("SELECT * FROM nope")
} catch (e) {
  if (e instanceof JevqlError && e.code === "sql") console.error(e.message)
}
```

## Types

`QueryResult`, `Stats`, `Explain`, `EmbeddedOptions`, `RemoteOptions` are
exported. `rows` is `unknown[][]`; `queryObjects` returns
`Record<string, unknown>[]`.

## Publishing the platform packages

`scripts/make-platform-packages.mjs --version X --tarballs DIR --out DIR`
turns the GitHub release tarballs into the four `@jevql/engine-*` packages;
the release workflow publishes them alongside this package.
