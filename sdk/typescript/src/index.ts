import { CliTransport } from "./cli.js"
import { HttpTransport } from "./http.js"
import { explainOf, type Transport } from "./transport.js"
import type { Explain, Health, JevqlOptions, QueryOptions, QueryResult } from "./types.js"

export { JevqlError } from "./error.js"
export type { CliOptions, ErrorCode, Explain, Health, HttpOptions, JevqlOptions, QueryOptions, QueryResult, Stats } from "./types.js"

/**
 * jevql client. Pass `{ url }` to talk to a running `jevql serve`, or
 * `{ cli: {...} }` to spawn the jevql binary for each call.
 */
export class Jevql {
  private readonly transport: Transport

  constructor(options: JevqlOptions) {
    if ("cli" in options) {
      this.transport = new CliTransport(options)
    } else if ("url" in options && options.url) {
      this.transport = new HttpTransport(options)
    } else {
      throw new TypeError("jevql: pass { url } or { cli }")
    }
  }

  /** Run one statement. Plain SQL passes through to Postgres; jev_* calls are judged. */
  query(sql: string, opts?: QueryOptions): Promise<QueryResult> {
    return this.transport.query(sql, opts)
  }

  /** Like query, but rows as objects keyed by column name. */
  async queryObjects(sql: string, opts?: QueryOptions): Promise<Record<string, unknown>[]> {
    const res = await this.transport.query(sql, opts)
    return res.rows.map((row) => {
      const o: Record<string, unknown> = {}
      res.columns.forEach((c, i) => {
        o[c] = row[i]
      })
      return o
    })
  }

  /** Plan and cost estimate for a jev query. Makes no TypeSafe call. */
  async explain(sql: string): Promise<Explain> {
    return explainOf(await this.transport.query(sql, { explain: true }))
  }

  /** Server health (HTTP) or the CLI version (CLI transport). */
  health(): Promise<Health> {
    return this.transport.health()
  }
}

export default Jevql
