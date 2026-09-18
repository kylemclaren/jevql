import { useEffect, useMemo, useRef, useState } from "react"
import { IconMagnifyingGlass } from "@central-icons-react/round-outlined-radius-2-stroke-2/IconMagnifyingGlass"

export type SearchPage = { title: string; href: string; section: string; description: string; headings: string[]; body: string }

/* ⌘K palette over the docs pages: title, description and headings. */
export default function DocsSearch({ pages }: { pages: SearchPage[] }) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState("")
  const [cursor, setCursor] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const isMac = typeof navigator !== "undefined" && /Mac/i.test(navigator.platform)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        setOpen((o) => !o)
      }
      if (e.key === "Escape") setOpen(false)
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [])
  useEffect(() => {
    if (open) {
      setQ("")
      setCursor(0)
      setTimeout(() => input.current?.focus(), 10)
      document.body.style.overflow = "hidden"
    } else document.body.style.overflow = ""
  }, [open])

  const needle = q.trim().toLowerCase()
  const results = useMemo(() => {
    const scored = pages.map((p) => {
      if (!needle) return { p, score: 1, hit: "" }
      const t = p.title.toLowerCase(), d = p.description.toLowerCase()
      let score = 0, hit = ""
      if (t.includes(needle)) score += 10
      if (d.includes(needle)) score += 4
      const h = p.headings.find((x) => x.toLowerCase().includes(needle))
      if (h) { score += 6; hit = h }
      if (p.body.includes(needle)) {
        score += 2
        if (!hit) {
          const at = p.body.indexOf(needle)
          hit = "…" + p.body.slice(Math.max(0, at - 30), at + needle.length + 40).trim() + "…"
        }
      }
      return { p, score, hit }
    })
    return scored.filter((r) => r.score > 0).sort((a, b) => b.score - a.score).slice(0, 12)
  }, [pages, needle])

  useEffect(() => setCursor(0), [needle])

  const go = (i: number) => {
    const r = results[i]
    if (!r) return
    let href = r.p.href
    if (r.hit && !r.hit.startsWith("…")) href += "#" + r.hit.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "")
    location.href = href
  }

  return (
    <>
      <button type="button" className="repo-link docs-search-btn" onClick={() => setOpen(true)} aria-label="Search docs">
        <IconMagnifyingGlass size={14} />
        <span>search</span>
        <kbd>{isMac ? "⌘" : "ctrl"} K</kbd>
      </button>
      {open && (
        <div className="palette-backdrop" onMouseDown={(e) => { if (e.target === e.currentTarget) setOpen(false) }}>
          <div className="palette" role="dialog" aria-label="Search documentation">
            <div className="palette-input">
              <IconMagnifyingGlass size={16} />
              <input
                ref={input}
                value={q}
                onChange={(e) => setQ(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "ArrowDown") { e.preventDefault(); setCursor((c) => Math.min(c + 1, results.length - 1)) }
                  if (e.key === "ArrowUp") { e.preventDefault(); setCursor((c) => Math.max(c - 1, 0)) }
                  if (e.key === "Enter") go(cursor)
                }}
                placeholder="Search the docs"
                aria-label="Search the docs"
              />
              <kbd>esc</kbd>
            </div>
            <ul className="palette-list" role="listbox">
              {results.map((r, i) => (
                <li key={r.p.href} role="option" aria-selected={i === cursor} className={i === cursor ? "active" : ""} onMouseEnter={() => setCursor(i)} onMouseDown={() => go(i)}>
                  <small>{r.p.section}{r.hit ? " · " + r.hit : ""}</small>
                  <strong>{r.p.title}</strong>
                  <span>{r.p.description}</span>
                </li>
              ))}
              {results.length === 0 && <li className="empty">No pages match “{q}”</li>}
            </ul>
          </div>
        </div>
      )}
    </>
  )
}
