import { describe, expect, test } from "bun:test"
import { Jevql } from "../src/index.js"

const url = process.env.JEVQL_SERVE_URL
const it = url ? test : test.skip

describe("integration (JEVQL_SERVE_URL)", () => {
  it("SELECT 1 against a live jevql serve", async () => {
    const j = new Jevql({ url: url!, token: process.env.JEVQL_TOKEN })
    const h = await j.health()
    expect(h.ok).toBe(true)
    const res = await j.query("SELECT 1 AS one")
    expect(res.columns).toEqual(["one"])
    expect(res.rows).toEqual([[1]])
    expect(res.jev).toBe(false)
  })
})
