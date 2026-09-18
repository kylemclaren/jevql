import { afterEach, describe, expect, test } from "bun:test"
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import { Jevql, JevqlError, engineArgs, platformPackage, resolveEngine } from "../src/index.js"

const here = dirname(fileURLToPath(import.meta.url))
const fake = join(here, "fake-engine.mjs")

/** A tiny executable that runs the fake engine, with optional env baked in. */
function fakeBinary(dir: string, extraEnv: Record<string, string> = {}): string {
  const p = join(dir, "jevql")
  const env = Object.entries(extraEnv)
    .map(([k, v]) => `${k}=${JSON.stringify(v)}`)
    .join(" ")
  writeFileSync(p, `#!/bin/sh\n${env} exec "${process.execPath}" "${fake}" "$@"\n`)
  chmodSync(p, 0o755)
  return p
}

const dirs: string[] = []
function tmp(): string {
  const d = mkdtempSync(join(tmpdir(), "jevql-ts-"))
  dirs.push(d)
  return d
}
afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true })
})

describe("engine discovery", () => {
  test("platform package names", () => {
    expect(platformPackage("darwin", "arm64")).toBe("@jevql/engine-darwin-arm64")
    expect(platformPackage("linux", "x64")).toBe("@jevql/engine-linux-x64")
    expect(platformPackage("win32", "x64")).toBeNull()
  })

  test("order: enginePath > JEVQL_ENGINE_PATH > bundled > PATH", () => {
    const a = fakeBinary(tmp())
    const b = fakeBinary(tmp())
    const c = fakeBinary(tmp())
    const pathDir = tmp()
    const d = fakeBinary(pathDir)
    const resolve = (spec: string) => {
      if (spec === "@jevql/engine-linux-x64/bin/jevql") return c
      throw new Error("not found")
    }
    const base = { platform: "linux" as const, arch: "x64", resolve }
    expect(resolveEngine({ ...base, enginePath: a, env: { JEVQL_ENGINE_PATH: b, PATH: pathDir } })).toBe(a)
    expect(resolveEngine({ ...base, env: { JEVQL_ENGINE_PATH: b, PATH: pathDir } })).toBe(b)
    expect(resolveEngine({ ...base, env: { PATH: pathDir } })).toBe(c)
    expect(resolveEngine({ ...base, resolve: () => { throw new Error("no") }, env: { PATH: pathDir } })).toBe(d)
  })

  test("missing engine names the install commands", () => {
    const empty = tmp()
    let err: unknown
    try {
      resolveEngine({ platform: "darwin", arch: "arm64", resolve: () => { throw new Error("no") }, env: { PATH: empty } })
    } catch (e) {
      err = e
    }
    expect(err).toBeInstanceOf(JevqlError)
    const m = (err as JevqlError).message
    expect((err as JevqlError).code).toBe("transport")
    expect(m).toContain("brew install kylemclaren/tap/jevql")
    expect(m).toContain("npm i @jevql/engine-darwin-arm64")
    expect(m).toContain("JEVQL_ENGINE_PATH")
  })

  test("bad explicit path errors", () => {
    expect(() => resolveEngine({ enginePath: "/nope/jevql" })).toThrow(/does not exist/)
  })
})

describe("engine args", () => {
  test("only explicit options become flags", () => {
    const a = engineArgs({}, "tok", 42)
    expect(a).toEqual(["serve", "--listen", "127.0.0.1:0", "--token", "tok", "--ready-json", "--parent-pid", "42"])
    const b = engineArgs(
      { databaseUrl: "postgres://x", apiKey: "k", apiUrl: "http://u", model: "m", threshold: 0.7, maxRows: 5, cachePath: "/c.db", noCache: true },
      "tok",
      1,
    )
    expect(b.slice(8)).toEqual(["--api-key", "k", "--api-url", "http://u", "--model", "m", "--threshold", "0.7", "--max-rows", "5", "--cache", "/c.db", "--no-cache", "postgres://x"])
  })
})

describe("embedded engine (fake)", () => {
  test("starts, authenticates, queries, stops", async () => {
    const dir = tmp()
    const argvFile = join(dir, "argv.json")
    const bin = fakeBinary(dir, { FAKE_ARGV_FILE: argvFile })
    const db = new Jevql({ enginePath: bin, databaseUrl: "postgres://fake/db", threshold: 0.9, env: { EXTRA: "yes" } })
    expect(db.embedded).toBe(true)
    expect(db.url).toBeUndefined()
    // concurrent first calls share one spawn
    const [h, r] = await Promise.all([db.health(), db.query("SELECT 1")])
    expect(h.ok).toBe(true)
    expect(r.rows).toEqual([["SELECT 1"]])
    expect(db.url).toMatch(/^http:\/\/127\.0\.0\.1:\d+$/)
    const argv = JSON.parse(readFileSync(argvFile, "utf8")) as { args: string[]; env: Record<string, string | null> }
    expect(argv.args[0]).toBe("serve")
    expect(argv.args).toContain("--ready-json")
    expect(argv.args[argv.args.indexOf("--parent-pid") + 1]).toBe(String(process.pid))
    expect(argv.args[argv.args.indexOf("--threshold") + 1]).toBe("0.9")
    expect(argv.args[argv.args.length - 1]).toBe("postgres://fake/db")
    expect(argv.env.EXTRA).toBe("yes")
    const objs = await db.queryObjects("SELECT 2")
    expect(objs).toEqual([{ sql: "SELECT 2" }])
    // the token is enforced: a client with the wrong token is rejected
    const wrong = new Jevql({ url: db.url!, token: "nope" })
    await expect(wrong.query("x")).rejects.toMatchObject({ code: "auth", status: 401 })
    await db.close()
    await db.close() // idempotent
    // after close the old engine is gone
    const gone = new Jevql({ url: db.url ?? "http://127.0.0.1:1", token: "x" })
    await expect(gone.query("x")).rejects.toMatchObject({ code: "transport" })
  })

  test("times out when the engine never reports ready", async () => {
    const bin = fakeBinary(tmp(), { FAKE_NEVER_READY: "1" })
    const db = new Jevql({ enginePath: bin, startTimeout: 1 })
    let err: unknown
    try {
      await db.query("SELECT 1")
    } catch (e) {
      err = e
    }
    expect(err).toBeInstanceOf(JevqlError)
    expect((err as JevqlError).code).toBe("transport")
    expect((err as JevqlError).message).toContain("did not report ready")
    expect((err as JevqlError).message).toContain("still warming up") // stderr tail is included
    await db.close()
  })

  test("engine exiting early surfaces stderr", async () => {
    const bin = fakeBinary(tmp(), { FAKE_EXIT_EARLY: "1" })
    const db = new Jevql({ enginePath: bin })
    await expect(db.query("SELECT 1")).rejects.toMatchObject({ code: "transport", message: expect.stringContaining("could not connect") })
    // a failed start can be retried
    await expect(db.query("SELECT 1")).rejects.toMatchObject({ code: "transport" })
  })

  test("await using disposes the engine", async () => {
    const bin = fakeBinary(tmp())
    let url: string | undefined
    {
      await using db = new Jevql({ enginePath: bin })
      await db.start()
      url = db.url
      expect(url).toBeDefined()
    }
    const gone = new Jevql({ url: url!, token: "x" })
    await expect(gone.health()).rejects.toMatchObject({ code: "transport" })
  })
})

describe("platform packages", () => {
  test("generator builds one package per target from tarballs", async () => {
    const { makePlatformPackages, TARGETS } = await import("../scripts/make-platform-packages.mjs")
    const tarballs = tmp()
    const out = tmp()
    const src = tmp()
    writeFileSync(join(src, "jevql"), "#!/bin/sh\necho jevql 9.9.9\n")
    chmodSync(join(src, "jevql"), 0o755)
    const { execFileSync } = await import("node:child_process")
    for (const t of TARGETS) execFileSync("tar", ["-czf", join(tarballs, `jevql_9.9.9_${t.release}.tar.gz`), "-C", src, "jevql"])
    const made = makePlatformPackages({ version: "9.9.9", tarballs, out })
    expect(made.map((m: { name: string }) => m.name)).toEqual([
      "@jevql/engine-darwin-arm64",
      "@jevql/engine-darwin-x64",
      "@jevql/engine-linux-x64",
      "@jevql/engine-linux-arm64",
    ])
    const pkg = JSON.parse(readFileSync(join(out, "engine-darwin-x64", "package.json"), "utf8"))
    expect(pkg).toMatchObject({ name: "@jevql/engine-darwin-x64", version: "9.9.9", os: ["darwin"], cpu: ["x64"], license: "MIT", files: ["bin"] })
    expect(existsSync(join(out, "engine-linux-arm64", "bin", "jevql"))).toBe(true)
    expect(existsSync(join(out, "engine-linux-arm64", "README.md"))).toBe(true)
    // missing tarball is an error
    expect(() => makePlatformPackages({ version: "0.0.0", tarballs, out })).toThrow(/missing tarball/)
    mkdirSync(join(out, "x"), { recursive: true })
  })
})
