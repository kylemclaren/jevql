import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react"
import { IconSidebarLeftArrow } from "@central-icons-react/round-outlined-radius-2-stroke-2/IconSidebarLeftArrow"
import { IconBook } from "@central-icons-react/round-outlined-radius-2-stroke-2/IconBook"
import { IconArrowRight } from "@central-icons-react/round-outlined-radius-2-stroke-2/IconArrowRight"

/* ─────────────────────────────────────────────────────────
 * DOCS SIDEBAR
 * Grouped page list with a gliding hover highlight and a
 * square lime block for the current page. Collapses to an
 * icon rail; on phones it folds to a single bar.
 * ───────────────────────────────────────────────────────── */

export type DocsNavItem = { label: string; href: string; keywords?: string }
export type DocsNavGroup = { label: string; items: DocsNavItem[] }

const MOTION = { expandedWidth: 240, collapsedWidth: 44, duration: 280, easing: "cubic-bezier(0.16, 1, 0.3, 1)" }

/* GlideMenu: a highlight that slides between hovered rows. */
function GlideGroup({ children }: { children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const [box, setBox] = useState<{ top: number; height: number } | null>(null)
  const move = (e: React.MouseEvent) => {
    const row = (e.target as HTMLElement).closest<HTMLElement>("[data-row]")
    const root = ref.current
    if (!row || !root) return
    const r = row.getBoundingClientRect(), b = root.getBoundingClientRect()
    setBox({ top: r.top - b.top, height: r.height })
  }
  return (
    <div ref={ref} className="glide group/glide relative flex flex-col" onMouseMove={move} onMouseLeave={() => setBox(null)}>
      <span
        aria-hidden
        className="glide-highlight pointer-events-none absolute inset-x-0 bg-hover-2 transition-[top,height,opacity] duration-150"
        style={{ top: box?.top ?? 0, height: box?.height ?? 0, opacity: box ? 1 : 0 }}
      />
      {children}
    </div>
  )
}

export default function DocsSidebar({ groups, pathname }: { groups: DocsNavGroup[]; pathname: string }) {
  const [collapsed, setCollapsed] = useState(false)
  const norm = (p: string) => p.replace(/\/+$/, "")

  useEffect(() => {
    if (window.matchMedia("(max-width: 750px)").matches) {
      setCollapsed(true)
      return
    }
    try {
      if (localStorage.getItem("jevql-docs-sidebar") === "collapsed") setCollapsed(true)
    } catch {}
  }, [])
  useEffect(() => {
    if (window.matchMedia("(max-width: 750px)").matches) return
    try {
      localStorage.setItem("jevql-docs-sidebar", collapsed ? "collapsed" : "open")
    } catch {}
  }, [collapsed])

  return (
    <aside
      data-sidebar-collapsed={collapsed}
      aria-label="Documentation"
      className="docs-sidebar relative flex shrink-0 overflow-hidden transition-[width]"
      style={{
        width: collapsed ? MOTION.collapsedWidth : MOTION.expandedWidth,
        transitionDuration: `${MOTION.duration}ms`,
        transitionTimingFunction: MOTION.easing,
        "--sidebar-easing": MOTION.easing,
      } as CSSProperties}
    >
      <div className="flex min-h-0 w-[240px] shrink-0 flex-col">
        <div className="relative mb-4 h-9 shrink-0">
          <a
            href="/docs/"
            aria-hidden={collapsed}
            tabIndex={collapsed ? -1 : 0}
            className="sidebar-copy absolute left-1 top-0.5 flex h-8 items-center gap-2 px-2 text-[0.72rem] font-black uppercase tracking-[0.06em] text-ink hover:bg-hover-2"
          >
            <IconBook size={16} />
            docs
          </a>
          <button
            type="button"
            aria-label="Collapse sidebar"
            aria-hidden={collapsed}
            tabIndex={collapsed ? -1 : 0}
            onClick={() => setCollapsed(true)}
            className={`absolute right-1 top-0.5 flex size-8 items-center justify-center text-ink-3 transition-[opacity,background-color,color] duration-150 hover:bg-hover-2 hover:text-ink ${collapsed ? "pointer-events-none opacity-0" : "opacity-100"}`}
          >
            <IconSidebarLeftArrow size={18} />
          </button>
          <button
            type="button"
            aria-label="Expand sidebar"
            aria-hidden={!collapsed}
            tabIndex={collapsed ? 0 : -1}
            onClick={() => setCollapsed(false)}
            className={`absolute left-1 top-0.5 flex size-8 items-center justify-center text-ink-3 transition-[opacity,background-color,color] duration-150 hover:bg-hover-2 hover:text-ink ${collapsed ? "opacity-100" : "pointer-events-none opacity-0"}`}
          >
            <IconSidebarLeftArrow size={18} className="rotate-180" />
          </button>
        </div>

        <div className="sidebar-copy min-h-0 flex-1 overflow-y-auto px-1 pb-4">
          <div className="flex flex-col gap-5">
            {groups.map((g) => (
              <section key={g.label} aria-label={g.label}>
                <p className="mb-1 px-2 text-[0.58rem] font-black uppercase tracking-[0.1em] text-ink-3">{g.label}</p>
                <GlideGroup>
                  {g.items.map((it) => {
                    const active = norm(it.href) === norm(pathname)
                    return (
                      <a
                        key={it.href}
                        data-row
                        href={it.href}
                        aria-current={active ? "page" : undefined}
                        className={`sidebar-row relative z-10 flex h-8 items-center gap-2 px-2 text-[0.78rem] font-bold transition-[background-color,color] duration-150 ${active ? "bg-lime text-[#171715] group-hover/glide:bg-transparent group-hover/glide:text-ink" : "text-ink-2 hover:text-ink"}`}
                      >
                        <span className="min-w-0 flex-1 truncate">{it.label}</span>
                        {active && <IconArrowRight size={14} className="shrink-0 opacity-70" />}
                      </a>
                    )
                  })}
                </GlideGroup>
              </section>
            ))}
          </div>
        </div>

        <div className="sidebar-copy mx-1 mt-2 border-t border-line pt-3">
          <a
            href="/"
            className="flex h-8 items-center justify-center gap-1.5 border-[1.5px] border-ink bg-field text-[0.62rem] font-black uppercase tracking-[0.06em] text-ink shadow-[2px_2px_0_var(--color-ink)] transition-[background-color,transform] duration-150 hover:bg-lime hover:text-[#171715] active:translate-[1px] active:shadow-[1px_1px_0_var(--color-ink)]"
          >
            ← back to jevql.dev
          </a>
        </div>
      </div>
    </aside>
  )
}
