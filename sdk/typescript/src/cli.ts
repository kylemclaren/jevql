import { execFile } from "node:child_process"
import { JevqlError, codeForExit } from "./error.js"
import type { Transport } from "./transport.js"
import type { CliOptions, Health, QueryOptions, QueryResult } from "./types.js"

interface Run {
  stdout: string
  stderr: string
  code: number | null
}

function run(binary: string, args: string[], env: NodeJS.ProcessEnv, cwd?: string): Promise<Run> {
  return new Promise((resolve, reject) => {
    execFile(binary, args, { env, cwd, maxBuffer: 256 * 1024 * 1024 }, (err, stdout, stderr) => {
      const e = err as (Error & { code?: number | string }) | null
      if (e && typeof e.code === "string") {
        // ENOENT etc: the binary could not be started at all.
        reject(new JevqlError(`jevql: cannot run ${binary}: ${e.message}`, "transport"))
        return
      }
      resolve({ stdout: String(stdout), stderr: String(stderr), code: e ? (e.code as number) ?? 1 : 0 })
    })
  })
}

export class CliTransport implements Transport {
  private readonly binary: string
  private readonly opts: CliOptions["cli"]

  constructor(opts: CliOptions) {
    this.opts = opts.cli
    this.binary = opts.cli.binary ?? "jevql"
  }

  /** Arguments for one statement; exported for tests. */
  args(sql: string, opts: QueryOptions & { explain?: boolean } = {}): string[] {
    const a = ["--json-table"]
    if (opts.threshold !== undefined) a.push("--threshold", String(opts.threshold))
    if (opts.maxRows !== undefined) a.push("--max-rows", String(opts.maxRows))
    if (opts.explain) a.push("--explain")
    if (this.opts.apiKey) a.push("--api-key", this.opts.apiKey)
    a.push("-c", sql)
    if (this.opts.databaseUrl) a.push(this.opts.databaseUrl)
    return a
  }

  private env(): NodeJS.ProcessEnv {
    const env: NodeJS.ProcessEnv = { ...process.env, ...(this.opts.env ?? {}) }
    if (this.opts.apiUrl) env.TYPESAFE_API_URL = this.opts.apiUrl
    return env
  }

  async query(sql: string, opts: QueryOptions & { explain?: boolean } = {}): Promise<QueryResult> {
    const r = await run(this.binary, this.args(sql, opts), this.env(), this.opts.cwd)
    if (r.code !== 0) {
      const msg = r.stderr.trim().replace(/^ERROR:\s*/, "") || `jevql exited with code ${r.code}`
      throw new JevqlError(msg, codeForExit(r.code, r.stderr), r.code ?? undefined)
    }
    const lines = r.stdout.split("\n").map((l) => l.trim()).filter(Boolean)
    if (lines.length === 0) throw new JevqlError("jevql: no result on stdout", "transport")
    try {
      // -c may hold several statements; the last document is the result of the last one.
      return JSON.parse(lines[lines.length - 1]!) as QueryResult
    } catch {
      throw new JevqlError("jevql: invalid JSON on stdout", "transport")
    }
  }

  async health(): Promise<Health> {
    const r = await run(this.binary, ["--version"], this.env(), this.opts.cwd)
    if (r.code !== 0) throw new JevqlError(r.stderr.trim() || "jevql --version failed", "transport", r.code ?? undefined)
    const m = /jevql\s+(\S+)/.exec(r.stdout)
    return { ok: true, version: m?.[1] ?? r.stdout.trim() }
  }
}
