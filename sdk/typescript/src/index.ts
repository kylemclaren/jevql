import { Engine } from "./engine.js"
import { HttpTransport } from "./http.js"
import { explainOf, type Transport } from "./transport.js"
import type { EmbeddedOptions, Explain, Health, JevqlOptions, JudgeRequest, JudgeResult, QueryOptions, QueryResult, RemoteOptions } from "./types.js"

export { JevqlError } from "./error.js"
export { engineArgs, platformPackage, resolveEngine, PLATFORM_PACKAGES } from "./engine.js"
export type { EmbeddedOptions, ErrorCode, Explain, Health, HttpOptions, JevqlOptions, JudgeAnswer, JudgeRequest, JudgeResult, QueryOptions, QueryResult, RemoteOptions, Stats } from "./types.js"

/**
 * jevql client.
 *
 * `new Jevql()` runs a private engine (the bundled jevql binary) for the
 * lifetime of this process and talks to it on loopback. Connection and
 * TypeSafe settings come from the options or the environment.
 *
 * `new Jevql({ url, token })` talks to a `jevql serve` you run elsewhere.
 */
export class Jevql {
  private transport: Transport | null = null
  private readonly engine: Engine | null
  private readonly options: JevqlOptions

  constructor(options: JevqlOptions = {}) {
    this.options = options
    if ("url" in options && options.url) {
      this.transport = new HttpTransport(options as RemoteOptions)
      this.engine = null
    } else {
      this.engine = new Engine(options as EmbeddedOptions)
    }
  }

  /** True when this client runs its own engine. */
  get embedded(): boolean {
    return this.engine !== null
  }

  /** Base URL of the engine or server once known. */
  get url(): string | undefined {
    if (this.engine) return this.engine.info?.url
    return (this.options as RemoteOptions).url
  }

  private async ready(): Promise<Transport> {
    if (this.transport) return this.transport
    const info = await this.engine!.start()
    this.transport = new HttpTransport({ url: info.url, token: this.engine!.token, fetch: (this.options as EmbeddedOptions).fetch })
    return this.transport
  }

  /** Start the embedded engine now instead of on first query. No-op in remote mode. */
  async start(): Promise<void> {
    await this.ready()
  }

  /** Run one statement. Plain SQL passes through to Postgres; jev_* calls are judged. */
  async query(sql: string, opts?: QueryOptions): Promise<QueryResult> {
    return (await this.ready()).query(sql, opts)
  }

  /** Like query, but rows as objects keyed by column name. */
  async queryObjects(sql: string, opts?: QueryOptions): Promise<Record<string, unknown>[]> {
    const res = await this.query(sql, opts)
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
    return explainOf(await (await this.ready()).query(sql, { explain: true }))
  }

  /** Judge rows you already hold with one question. No database involved; shares the cache with query. */
  async judge(req: JudgeRequest): Promise<JudgeResult> {
    return (await this.ready()).judge(req)
  }

  /** Engine or server health. */
  async health(): Promise<Health> {
    return (await this.ready()).health()
  }

  /** Stop the embedded engine. Safe to call more than once; remote mode is a no-op. */
  async close(): Promise<void> {
    if (this.engine) {
      await this.engine.stop()
      this.transport = null
    }
  }

  /** `await using db = new Jevql()` */
  async [Symbol.asyncDispose](): Promise<void> {
    await this.close()
  }
}

export default Jevql
