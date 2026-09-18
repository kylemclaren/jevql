#!/usr/bin/env node
// Builds the @jevql/engine-<os>-<arch> npm packages from release tarballs.
//   node scripts/make-platform-packages.mjs --version 0.2.0 --tarballs ./dist-tarballs --out ./platform-packages
// Expects jevql_<version>_<os>_<arch>.tar.gz (the GitHub release assets) containing a `jevql` binary.
import { execFileSync } from "node:child_process"
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync, copyFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

// release (GOOS_GOARCH) -> npm (os, cpu)
export const TARGETS = [
  { release: "darwin_arm64", os: "darwin", cpu: "arm64" },
  { release: "darwin_amd64", os: "darwin", cpu: "x64" },
  { release: "linux_amd64", os: "linux", cpu: "x64" },
  { release: "linux_arm64", os: "linux", cpu: "arm64" },
]

export function packageName(t) {
  return `@jevql/engine-${t.os}-${t.cpu}`
}

export function makePlatformPackages({ version, tarballs, out, targets = TARGETS }) {
  const made = []
  for (const t of targets) {
    const tgz = join(tarballs, `jevql_${version}_${t.release}.tar.gz`)
    if (!existsSync(tgz)) throw new Error(`missing tarball ${tgz}`)
    const tmp = mkdtempSync(join(tmpdir(), "jevql-pkg-"))
    execFileSync("tar", ["-xzf", tgz, "-C", tmp, "jevql"])
    const dir = join(out, `engine-${t.os}-${t.cpu}`)
    mkdirSync(join(dir, "bin"), { recursive: true })
    copyFileSync(join(tmp, "jevql"), join(dir, "bin", "jevql"))
    chmodSync(join(dir, "bin", "jevql"), 0o755)
    rmSync(tmp, { recursive: true, force: true })
    const name = packageName(t)
    const pkg = {
      name,
      version,
      description: `jevql engine binary for ${t.os} ${t.cpu}. Installed automatically by the jevql package.`,
      license: "MIT",
      repository: { type: "git", url: "https://github.com/kylemclaren/jevql", directory: "sdk/typescript" },
      os: [t.os],
      cpu: [t.cpu],
      files: ["bin"],
      preferUnplugged: true,
    }
    writeFileSync(join(dir, "package.json"), JSON.stringify(pkg, null, 2) + "\n")
    writeFileSync(
      join(dir, "README.md"),
      `# ${name}\n\nThe jevql engine binary for ${t.os}/${t.cpu}. You do not install this directly: the \`jevql\` package lists it as an optional dependency and npm picks the one matching your platform.\n\nhttps://github.com/kylemclaren/jevql\n`,
    )
    made.push({ name, dir })
  }
  return made
}

function arg(name) {
  const i = process.argv.indexOf(`--${name}`)
  return i >= 0 ? process.argv[i + 1] : undefined
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const version = arg("version")
  const tarballs = arg("tarballs")
  const out = arg("out")
  if (!version || !tarballs || !out) {
    console.error("usage: make-platform-packages.mjs --version X --tarballs DIR --out DIR")
    process.exit(2)
  }
  for (const m of makePlatformPackages({ version, tarballs, out })) console.log(`${m.name} -> ${m.dir}`)
  void readFileSync
}
