import { spawn, type ChildProcess } from "node:child_process"
import { randomBytes } from "node:crypto"
import { accessSync, constants, existsSync } from "node:fs"
import { createRequire } from "node:module"
import { delimiter, join } from "node:path"
import { JevqlError } from "./error.js"
import type { EmbeddedOptions } from "./types.js"

/* ─────────────────────────────────────────────────────────
 * ENGINE
 * Finds the jevql binary and runs `jevql serve` as a private,
 * token-protected engine on a loopback port for the lifetime
 * of this process. See sdk/PROTOCOL.md, "Embedded engine".
 * ───────────────────────────────────────────────────────── */

export const PLATFORM_PACKAGES = ["darwin-arm64", "darwin-x64", "linux-x64", "linux-arm64"] as const

/** Name of the npm package that carries the binary for this platform, or null when unsupported. */
export function platformPackage(platform: string = process.platform, arch: string = process.arch): string | null {
  const key = `${platform}-${arch}`
  return (PLATFORM_PACKAGES as readonly string[]).includes(key) ? `@jevql/engine-${key}` : null
}

function executable(p: string): boolean {
  try {
    accessSync(p, constants.X_OK)
    return true
  } catch {
    return false
  }
}

export interface ResolveOptions {
  enginePath?: string
  env?: NodeJS.ProcessEnv
  /** Resolver for the bundled package (injectable for tests). */
  resolve?: (specifier: string) => string
  platform?: NodeJS.Platform
  arch?: string
}

/** Locate the jevql binary: enginePath, JEVQL_ENGINE_PATH, bundled platform package, then PATH. */
export function resolveEngine(opts: ResolveOptions = {}): string {
  const env = opts.env ?? process.env
  if (opts.enginePath) {
    if (!existsSync(opts.enginePath)) throw new JevqlError(`jevql: enginePath ${opts.enginePath} does not exist`, "transport")
    return opts.enginePath
  }
  if (env.JEVQL_ENGINE_PATH) {
    if (!existsSync(env.JEVQL_ENGINE_PATH)) throw new JevqlError(`jevql: JEVQL_ENGINE_PATH ${env.JEVQL_ENGINE_PATH} does not exist`, "transport")
    return env.JEVQL_ENGINE_PATH
  }
  const pkg = platformPackage(opts.platform, opts.arch)
  if (pkg) {
    const resolve = opts.resolve ?? createRequire(import.meta.url).resolve
    try {
      const p = resolve(`${pkg}/bin/jevql`)
      if (executable(p)) return p
    } catch {
      /* not installed */
    }
  }
  const exe = (opts.platform ?? process.platform) === "win32" ? "jevql.exe" : "jevql"
  for (const dir of (env.PATH ?? "").split(delimiter)) {
    if (!dir) continue
    const p = join(dir, exe)
    if (executable(p)) return p
  }
  const hint = pkg ? `npm i ${pkg}` : "a platform we do not ship binaries for"
  throw new JevqlError(
    `jevql: engine binary not found. Install it with \`brew install kylemclaren/tap/jevql\`, or \`${hint}\`, or set JEVQL_ENGINE_PATH.`,
    "transport",
  )
}

/** Command-line flags for the engine from explicit options. Exported for tests. */
export function engineArgs(o: EmbeddedOptions, token: string, parentPid = process.pid): string[] {
  const a = ["serve", "--listen", "127.0.0.1:0", "--token", token, "--ready-json", "--parent-pid", String(parentPid)]
  if (o.apiKey) a.push("--api-key", o.apiKey)
  if (o.apiUrl) a.push("--api-url", o.apiUrl)
  if (o.model) a.push("--model", o.model)
  if (o.threshold !== undefined) a.push("--threshold", String(o.threshold))
  if (o.maxRows !== undefined) a.push("--max-rows", String(o.maxRows))
  if (o.cachePath) a.push("--cache", o.cachePath)
  if (o.noCache) a.push("--no-cache")
  if (o.databaseUrl) a.push(o.databaseUrl)
  return a
}

export interface Ready {
  url: string
  version: string
  pid: number
}

const running = new Set<Engine>()
let exitHookInstalled = false
function installExitHook() {
  if (exitHookInstalled) return
  exitHookInstalled = true
  process.on("exit", () => {
    for (const e of running) e.killNow()
  })
}

/** One engine process. */
export class Engine {
  readonly token: string
  private child: ChildProcess | null = null
  private starting: Promise<Ready> | null = null
  private ready: Ready | null = null

  constructor(private readonly opts: EmbeddedOptions) {
    this.token = randomBytes(24).toString("hex")
  }

  /** Bound URL once started. */
  get info(): Ready | null {
    return this.ready
  }

  /** Start (once) and resolve the ready line. Concurrent callers share the same spawn. */
  start(): Promise<Ready> {
    if (this.ready) return Promise.resolve(this.ready)
    if (!this.starting) {
      this.starting = this.spawn().then(
        (r) => {
          this.ready = r
          return r
        },
        (e) => {
          this.starting = null
          throw e
        },
      )
    }
    return this.starting
  }

  private spawn(): Promise<Ready> {
    const binary = resolveEngine({ enginePath: this.opts.enginePath, env: { ...process.env, ...(this.opts.env ?? {}) } })
    const args = engineArgs(this.opts, this.token)
    const env = { ...process.env, ...(this.opts.env ?? {}) }
    const timeoutMs = (this.opts.startTimeout ?? 15) * 1000
    return new Promise<Ready>((resolve, reject) => {
      let child: ChildProcess
      try {
        child = spawn(binary, args, { env, stdio: ["ignore", "pipe", "pipe"] })
      } catch (e) {
        reject(new JevqlError(`jevql: cannot start engine ${binary}: ${(e as Error).message}`, "transport"))
        return
      }
      this.child = child
      running.add(this)
      installExitHook()
      let out = ""
      let err = ""
      let done = false
      const fail = (msg: string) => {
        if (done) return
        done = true
        clearTimeout(timer)
        this.killNow()
        const tail = err.trim().split("\n").slice(-6).join("\n")
        reject(new JevqlError(`jevql: ${msg}${tail ? `\n${tail}` : ""}`, "transport"))
      }
      const timer = setTimeout(() => fail(`engine did not report ready within ${timeoutMs / 1000}s`), timeoutMs)
      child.stdout!.setEncoding("utf8")
      child.stderr!.setEncoding("utf8")
      child.stderr!.on("data", (d: string) => {
        err += d
        if (err.length > 64 * 1024) err = err.slice(-32 * 1024)
      })
      child.stdout!.on("data", (d: string) => {
        if (done) return
        out += d
        const nl = out.indexOf("\n")
        if (nl < 0) return
        const line = out.slice(0, nl).trim()
        try {
          const j = JSON.parse(line) as { ready?: boolean; url?: string; listen?: string; version?: string; pid?: number }
          if (!j.ready || !(j.url || j.listen)) throw new Error("bad ready line")
          done = true
          clearTimeout(timer)
          resolve({ url: j.url ?? `http://${j.listen}`, version: j.version ?? "", pid: j.pid ?? child.pid ?? 0 })
        } catch {
          fail(`engine printed something other than a ready line: ${line.slice(0, 200)}`)
        }
      })
      child.on("error", (e) => fail(`cannot start engine ${binary}: ${e.message}`))
      child.on("exit", (code, signal) => {
        running.delete(this)
        this.child = null
        if (!done) fail(`engine exited before ready (${signal ?? `code ${code}`})`)
      })
    })
  }

  /** SIGKILL immediately (used at process exit). */
  killNow(): void {
    const c = this.child
    if (c && c.exitCode === null && !c.killed) {
      try {
        c.kill("SIGKILL")
      } catch {
        /* already gone */
      }
    }
    running.delete(this)
  }

  /** Graceful stop: SIGTERM, wait up to 2s, then SIGKILL. */
  async stop(): Promise<void> {
    const c = this.child
    this.ready = null
    this.starting = null
    running.delete(this)
    if (!c || c.exitCode !== null) {
      this.child = null
      return
    }
    await new Promise<void>((resolve) => {
      const t = setTimeout(() => {
        try {
          c.kill("SIGKILL")
        } catch {
          /* gone */
        }
      }, 2000)
      c.once("exit", () => {
        clearTimeout(t)
        resolve()
      })
      try {
        c.kill("SIGTERM")
      } catch {
        clearTimeout(t)
        resolve()
      }
    })
    this.child = null
  }
}
