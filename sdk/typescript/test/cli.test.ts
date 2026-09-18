import { afterAll, beforeAll, describe, expect, test } from "bun:test"
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { Jevql, JevqlError } from "../src/index.js"
import { sample, sampleExplain } from "./helpers.js"

let dir = ""
let bin = ""

// A fake jevql: records argv and env to files, then behaves per the SQL text.
beforeAll(() => {
  dir = mkdtempSync(join(tmpdir(), "jevql-sdk-"))
  bin = join(dir, "jevql")
  writeFileSync(
    bin,
    `#!/bin/sh
printf '%s\\n' "$@" > "$FAKE_DIR/argv"
env > "$FAKE_DIR/env"
if [ "$1" = "--version" ]; then echo "jevql 9.9.9"; exit 0; fi
sql=""
explain=0
while [ $# -gt 0 ]; do
  case "$1" in
    -c) sql="$2"; shift ;;
    --explain) explain=1 ;;
  esac
  shift
done
case "$sql" in
  "FAIL1") echo "ERROR:  syntax error at or near \\"FAIL1\\"" >&2; exit 1 ;;
  "FAIL2") echo "ERROR:  typesafe: HTTP 401: bad key" >&2; exit 2 ;;
  "BUDGET") echo "ERROR:  collect returned more than --max-rows (2) rows" >&2; exit 2 ;;
  "MULTI") echo '{"columns":["a"],"rows":[[1]],"row_count":1,"tag":"SELECT 1","jev":false,"stats":null,"explain":null}'; echo '${JSON.stringify(sample)}'; exit 0 ;;
esac
if [ $explain = 1 ]; then echo '${JSON.stringify(sampleExplain)}'; else echo '${JSON.stringify(sample)}'; fi
`,
  )
  chmodSync(bin, 0o755)
})
afterAll(() => rmSync(dir, { recursive: true, force: true }))

const readArgv = () => Bun.file(join(dir, "argv")).text().then((t) => t.trim().split("\n"))
const readEnv = () => Bun.file(join(dir, "env")).text()

describe("cli transport", () => {
  const mk = (extra: Record<string, unknown> = {}) =>
    new Jevql({ cli: { binary: bin, env: { FAKE_DIR: dir }, ...extra } })

  test("query returns the parsed document and passes flags", async () => {
    const j = mk({ databaseUrl: "postgres://u:p@h/db", apiKey: "tsk_x", apiUrl: "http://gw/v1/systemone" })
    const res = await j.query("SELECT 1", { threshold: 0.8, maxRows: 10 })
    expect(res).toEqual(sample)
    expect(await readArgv()).toEqual([
      "--json-table",
      "--threshold",
      "0.8",
      "--max-rows",
      "10",
      "--api-key",
      "tsk_x",
      "-c",
      "SELECT 1",
      "postgres://u:p@h/db",
    ])
    const env = await readEnv()
    expect(env).toContain("TYPESAFE_API_URL=http://gw/v1/systemone")
    expect(env).toContain(`FAKE_DIR=${dir}`)
  })

  test("process env passes through", async () => {
    process.env.JEVQL_SDK_MARKER = "yes"
    await mk().query("SELECT 1")
    expect(await readEnv()).toContain("JEVQL_SDK_MARKER=yes")
  })

  test("explain", async () => {
    const ex = await mk().explain("SELECT 1")
    expect(ex.batches).toBe(1)
    expect(await readArgv()).toContain("--explain")
  })

  test("last JSON line wins for multi-statement -c", async () => {
    expect(await mk().query("MULTI")).toEqual(sample)
  })

  test("queryObjects", async () => {
    expect(await mk().queryObjects("SELECT 1")).toEqual([
      { name: "Ada", p: 0.93 },
      { name: "Bo", p: 0.12 },
    ])
  })

  test("exit 1 maps to sql with the stderr message", async () => {
    const err = await mk().query("FAIL1").catch((e) => e)
    expect(err).toBeInstanceOf(JevqlError)
    expect(err.code).toBe("sql")
    expect(err.status).toBe(1)
    expect(err.message).toBe('syntax error at or near "FAIL1"')
  })

  test("exit 2 maps to api", async () => {
    const err = await mk().query("FAIL2").catch((e) => e)
    expect(err.code).toBe("api")
    expect(err.status).toBe(2)
  })

  test("exit 2 with a max-rows message maps to budget", async () => {
    const err = await mk().query("BUDGET").catch((e) => e)
    expect(err.code).toBe("budget")
  })

  test("health uses --version", async () => {
    expect(await mk().health()).toEqual({ ok: true, version: "9.9.9" })
  })

  test("missing binary is a transport error", async () => {
    const err = await new Jevql({ cli: { binary: join(dir, "nope") } }).query("SELECT 1").catch((e) => e)
    expect(err).toBeInstanceOf(JevqlError)
    expect(err.code).toBe("transport")
  })
})
