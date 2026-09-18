import { describe, expect, test } from "bun:test"
import { Jevql } from "../src/index.js"

// Remote mode against a running `jevql serve`.
const url = process.env.JEVQL_SERVE_URL
const remote = url ? test : test.skip

// Embedded mode with the real engine on PATH against a real database.
const dbUrl = process.env.JEVQL_TEST_DATABASE_URL
const embedded = dbUrl ? test : test.skip

describe("integration", () => {
  remote("remote: SELECT 1 against JEVQL_SERVE_URL", async () => {
    const j = new Jevql({ url: url!, token: process.env.JEVQL_TOKEN })
    expect((await j.health()).ok).toBe(true)
    const res = await j.query("SELECT 1 AS one")
    expect(res.rows).toEqual([[1]])
    expect(res.jev).toBe(false)
  })

  embedded("embedded: real engine, SELECT 1 against JEVQL_TEST_DATABASE_URL", async () => {
    const j = new Jevql({ databaseUrl: dbUrl!, noCache: true })
    try {
      const h = await j.health()
      expect(h.ok).toBe(true)
      expect(j.url).toMatch(/^http:\/\/127\.0\.0\.1:\d+$/)
      const res = await j.query("SELECT 1 AS one, 'x' AS t")
      expect(res.columns).toEqual(["one", "t"])
      expect(res.rows).toEqual([[1, "x"]])
      await expect(j.query("SELECT * FROM nope_table")).rejects.toMatchObject({ code: "sql", status: 400 })
    } finally {
      await j.close()
    }
  })
})
