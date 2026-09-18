#!/usr/bin/env node
// A fake jevql engine for tests. Honours the embedded-engine contract:
// prints one ready line, serves /v1/health and /v1/query, checks the token.
// Env: FAKE_ARGV_FILE (write argv there), FAKE_NEVER_READY=1, FAKE_EXIT_EARLY=1.
import { createServer } from "node:http"
import { writeFileSync } from "node:fs"

const args = process.argv.slice(2)
if (process.env.FAKE_ARGV_FILE) writeFileSync(process.env.FAKE_ARGV_FILE, JSON.stringify({ args, env: { DATABASE_URL: process.env.DATABASE_URL ?? null, EXTRA: process.env.EXTRA ?? null } }))
if (process.env.FAKE_EXIT_EARLY) {
  console.error("boom: could not connect")
  process.exit(1)
}
const token = args[args.indexOf("--token") + 1]
const srv = createServer((req, res) => {
  const auth = req.headers.authorization
  if (auth !== `Bearer ${token}`) {
    res.writeHead(401, { "content-type": "application/json" })
    res.end(JSON.stringify({ error: "missing or invalid bearer token", code: "auth" }))
    return
  }
  if (req.url === "/v1/health") {
    res.writeHead(200, { "content-type": "application/json" })
    res.end(JSON.stringify({ ok: true, version: "fake", model: "jev-test" }))
    return
  }
  let body = ""
  req.on("data", (d) => (body += d))
  req.on("end", () => {
    const q = JSON.parse(body)
    res.writeHead(200, { "content-type": "application/json" })
    res.end(JSON.stringify({ columns: ["sql"], rows: [[q.sql]], row_count: 1, tag: "SELECT 1", jev: false, stats: null, explain: null }))
  })
})
srv.listen(0, "127.0.0.1", () => {
  if (process.env.FAKE_NEVER_READY) {
    console.error("still warming up")
    return
  }
  const { port } = srv.address()
  console.log(JSON.stringify({ ready: true, listen: `127.0.0.1:${port}`, url: `http://127.0.0.1:${port}`, version: "fake", pid: process.pid }))
})
process.on("SIGTERM", () => process.exit(0))
