import { JevqlError, codeForStatus } from "./error.js"
import type { Transport } from "./transport.js"
import type { ErrorBody, Health, HttpOptions, JudgeRequest, JudgeResult, QueryOptions, QueryResult } from "./types.js"

export class HttpTransport implements Transport {
  private readonly base: string
  private readonly token?: string
  private readonly fetchFn: (input: string, init?: RequestInit) => Promise<Response>

  constructor(opts: HttpOptions) {
    this.base = opts.url.replace(/\/+$/, "")
    this.token = opts.token
    const f = opts.fetch ?? globalThis.fetch
    if (!f) throw new JevqlError("jevql: no fetch available; pass options.fetch", "transport")
    this.fetchFn = f
  }

  private headers(): Record<string, string> {
    const h: Record<string, string> = { "content-type": "application/json", accept: "application/json" }
    if (this.token) h.authorization = `Bearer ${this.token}`
    return h
  }

  private async post<T>(path: string, body: unknown): Promise<T> {
    let res: Response
    try {
      res = await this.fetchFn(`${this.base}${path}`, { method: "POST", headers: this.headers(), body: JSON.stringify(body) })
    } catch (e) {
      throw new JevqlError(`jevql: cannot reach ${this.base}: ${(e as Error).message}`, "transport")
    }
    const text = await res.text()
    if (!res.ok) {
      let msg = text || res.statusText
      let code = codeForStatus(res.status)
      try {
        const eb = JSON.parse(text) as ErrorBody
        if (eb.error) msg = eb.error
        if (eb.code) code = eb.code
      } catch {
        /* non-JSON error body */
      }
      throw new JevqlError(msg, code, res.status)
    }
    try {
      return JSON.parse(text) as T
    } catch {
      throw new JevqlError("jevql: server returned invalid JSON", "transport", res.status)
    }
  }

  async query(sql: string, opts: QueryOptions & { explain?: boolean } = {}): Promise<QueryResult> {
    const body: Record<string, unknown> = { sql }
    if (opts.threshold !== undefined) body.threshold = opts.threshold
    if (opts.maxRows !== undefined) body.max_rows = opts.maxRows
    if (opts.explain) body.explain = true
    return this.post<QueryResult>("/v1/query", body)
  }

  async judge(req: JudgeRequest): Promise<JudgeResult> {
    return this.post<JudgeResult>("/v1/judge", req)
  }

  async health(): Promise<Health> {
    let res: Response
    try {
      res = await this.fetchFn(`${this.base}/v1/health`, { headers: this.headers() })
    } catch (e) {
      throw new JevqlError(`jevql: cannot reach ${this.base}: ${(e as Error).message}`, "transport")
    }
    if (!res.ok) throw new JevqlError(`jevql: health check failed (${res.status})`, codeForStatus(res.status), res.status)
    return (await res.json()) as Health
  }
}
