import { afterAll, beforeAll, describe, expect, test } from "bun:test"
import { Jevql, JevqlError } from "../src/index.js"
import { sample, sampleExplain } from "./helpers.js"

type Seen = { path: string; method: string; auth: string | null; body: unknown }
const seen: Seen[] = []
let server: ReturnType<typeof Bun.serve>
let base = ""

beforeAll(() => {
  server = Bun.serve({
    port: 0,
    async fetch(req) {
      const url = new URL(req.url)
      const body = req.method === "POST" ? await req.json() : null
      seen.push({ path: url.pathname, method: req.method, auth: req.headers.get("authorization"), body })
      if (url.pathname === "/v1/health") return Response.json({ ok: true, version: "0.1.0", model: "jev-latest" })
      if (url.pathname !== "/v1/query") return new Response("nope", { status: 404 })
      const b = body as { sql: string; explain?: boolean }
      const fail = /^FAIL (\d+)/.exec(b.sql)
      if (fail) {
        const status = Number(fail[1])
        return Response.json({ error: `boom ${status}`, code: undefined }, { status })
      }
      if (b.sql === "PLAIN 500") return new Response("<html>gateway</html>", { status: 503 })
      if (b.sql === "BADJSON") return new Response("{not json", { status: 200 })
      if (b.sql === "CODED") return Response.json({ error: "custom", code: "budget" }, { status: 400 })
      return Response.json(b.explain ? sampleExplain : sample)
    },
  })
  base = `http://127.0.0.1:${server.port}`
})
afterAll(() => server.stop(true))

describe("http transport", () => {
  test("query returns the QueryResult and sends the body", async () => {
    seen.length = 0
    const j = new Jevql({ url: base + "/" })
    const res = await j.query("SELECT 1", { threshold: 0.7, maxRows: 5 })
    expect(res).toEqual(sample)
    expect(seen[0]!.path).toBe("/v1/query")
    expect(seen[0]!.body).toEqual({ sql: "SELECT 1", threshold: 0.7, max_rows: 5 })
    expect(seen[0]!.auth).toBeNull()
  })

  test("bearer token is sent", async () => {
    seen.length = 0
    const j = new Jevql({ url: base, token: "s3cret" })
    await j.health()
    expect(seen[0]!.auth).toBe("Bearer s3cret")
  })

  test("explain sets explain: true and returns the explain block", async () => {
    seen.length = 0
    const j = new Jevql({ url: base })
    const ex = await j.explain("SELECT * FROM people WHERE jev(people, 'x')")
    expect(ex.rows).toBe(12)
    expect(seen[0]!.body).toEqual({ sql: "SELECT * FROM people WHERE jev(people, 'x')", explain: true })
  })

  test("queryObjects zips columns and rows", async () => {
    const j = new Jevql({ url: base })
    expect(await j.queryObjects("SELECT 1")).toEqual([
      { name: "Ada", p: 0.93 },
      { name: "Bo", p: 0.12 },
    ])
  })

  test("health", async () => {
    const j = new Jevql({ url: base })
    expect(await j.health()).toEqual({ ok: true, version: "0.1.0", model: "jev-latest" })
  })

  test.each([
    [400, "sql"],
    [401, "auth"],
    [402, "budget"],
    [502, "api"],
    [500, "internal"],
  ])("status %d maps to code %s", async (status, code) => {
    const j = new Jevql({ url: base })
    const err = await j.query(`FAIL ${status}`).catch((e) => e)
    expect(err).toBeInstanceOf(JevqlError)
    expect(err.code).toBe(code)
    expect(err.status).toBe(status)
    expect(err.message).toBe(`boom ${status}`)
  })

  test("server-provided code wins over the status mapping", async () => {
    const err = await new Jevql({ url: base }).query("CODED").catch((e) => e)
    expect(err.code).toBe("budget")
    expect(err.message).toBe("custom")
  })

  test("non-JSON error body still surfaces", async () => {
    const err = await new Jevql({ url: base }).query("PLAIN 500").catch((e) => e)
    expect(err).toBeInstanceOf(JevqlError)
    expect(err.code).toBe("internal")
    expect(err.message).toContain("gateway")
  })

  test("invalid JSON on 200 is a transport error", async () => {
    const err = await new Jevql({ url: base }).query("BADJSON").catch((e) => e)
    expect(err.code).toBe("transport")
  })

  test("unreachable server is a transport error", async () => {
    const err = await new Jevql({ url: "http://127.0.0.1:1" }).query("SELECT 1").catch((e) => e)
    expect(err).toBeInstanceOf(JevqlError)
    expect(err.code).toBe("transport")
  })

  test("custom fetch is used", async () => {
    let called = 0
    const j = new Jevql({
      url: "http://example.invalid",
      fetch: async () => {
        called++
        return Response.json(sample)
      },
    })
    await j.query("SELECT 1")
    expect(called).toBe(1)
  })
})

describe("constructor", () => {
  test("rejects empty options", () => {
    expect(() => new Jevql({} as never)).toThrow(TypeError)
  })
})
