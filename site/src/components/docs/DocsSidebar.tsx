import { useEffect, useState, type CSSProperties } from "react"
import { IconSidebarLeftArrow } from "@central-icons-react/round-outlined-radius-2-stroke-2/IconSidebarLeftArrow"
import { IconBook } from "@central-icons-react/round-outlined-radius-2-stroke-2/IconBook"
import { HookSidebar } from "@/components/ui/hook-sidebar"

/* ─────────────────────────────────────────────────────────
 * DOCS SIDEBAR
 * Grouped navigation drawn with the hook-sidebar rail, a
 * search field that filters every group, and a collapse that
 * keeps the icon rail aligned. Adapted from SidebarNav.
 * ───────────────────────────────────────────────────────── */

export type DocsNavItem = { label: string; href: string; keywords?: string }
export type DocsNavGroup = { label: string; items: DocsNavItem[] }

const MOTION = {
  expandedWidth: 240,
  collapsedWidth: 44,
  duration: 280,
  easing: "cubic-bezier(0.16, 1, 0.3, 1)",
}

export default function DocsSidebar({ groups, pathname }: { groups: DocsNavGroup[]; pathname: string }) {
  const [collapsed, setCollapsed] = useState(false)

  useEffect(() => {
    const mobile = window.matchMedia("(max-width: 750px)").matches
    if (mobile) {
      setCollapsed(true) // phones start with the page list folded
      return
    }
    try {
      const saved = localStorage.getItem("jevql-docs-sidebar")
      if (saved === "collapsed") setCollapsed(true)
    } catch {}
  }, [])
  useEffect(() => {
    if (window.matchMedia("(max-width: 750px)").matches) return
    try {
      localStorage.setItem("jevql-docs-sidebar", collapsed ? "collapsed" : "open")
    } catch {}
  }, [collapsed])

  const visible = groups
  const collapse = () => setCollapsed(true)

  return (
    <aside
      data-sidebar-collapsed={collapsed}
      aria-label="Documentation"
      className="docs-sidebar relative flex shrink-0 overflow-hidden transition-[width]"
      style={
        {
          width: collapsed ? MOTION.collapsedWidth : MOTION.expandedWidth,
          transitionDuration: `${MOTION.duration}ms`,
          transitionTimingFunction: MOTION.easing,
          "--sidebar-easing": MOTION.easing,
        } as CSSProperties
      }
    >
      <div className="flex min-h-0 w-[240px] shrink-0 flex-col">
        <div className="relative mb-3 h-9 shrink-0">
          <a
            href="/docs/"
            aria-hidden={collapsed}
            tabIndex={collapsed ? -1 : 0}
            className="sidebar-copy absolute left-1 top-0.5 flex h-8 items-center gap-2 rounded-[6px] px-2 text-[0.72rem] font-black uppercase tracking-[0.06em] text-ink hover:bg-hover-2"
          >
            <IconBook size={16} />
            docs
          </a>
          <button
            type="button"
            aria-label="Collapse sidebar"
            aria-hidden={collapsed}
            tabIndex={collapsed ? -1 : 0}
            onClick={collapse}
            className={`absolute right-1 top-0.5 flex size-8 items-center justify-center rounded-[6px] text-ink-3 transition-[opacity,background-color,color] duration-150 hover:bg-hover-2 hover:text-ink ${collapsed ? "pointer-events-none opacity-0" : "opacity-100"}`}
          >
            <IconSidebarLeftArrow size={18} />
          </button>
          <button
            type="button"
            aria-label="Expand sidebar"
            aria-hidden={!collapsed}
            tabIndex={collapsed ? 0 : -1}
            onClick={() => setCollapsed(false)}
            className={`absolute left-1 top-0.5 flex size-8 items-center justify-center rounded-[6px] text-ink-3 transition-[opacity,background-color,color] duration-150 hover:bg-hover-2 hover:text-ink ${collapsed ? "opacity-100" : "pointer-events-none opacity-0"}`}
          >
            <IconSidebarLeftArrow size={18} className="rotate-180" />
          </button>
        </div>

        <div className="sidebar-copy min-h-0 flex-1 overflow-y-auto px-1 pb-4">
          <div className="flex flex-col gap-5">
            {visible.map((g) => (
              <HookSidebar key={g.label} label={g.label} items={g.items} pathname={pathname} color="#ee5ba6" />
            ))}
          </div>
        </div>

        <div className="sidebar-copy mx-1 mt-2 border-t border-line pt-3">
          <a
            href="/"
            className="flex h-8 items-center justify-center gap-1.5 border-[1.5px] border-ink bg-field text-[0.62rem] font-black uppercase tracking-[0.06em] text-ink shadow-[2px_2px_0_var(--color-ink)] transition-[background-color,transform] duration-150 hover:bg-lime active:translate-[1px] active:shadow-[1px_1px_0_var(--color-ink)]"
          >
            ← back to jevql.dev
          </a>
        </div>
      </div>
    </aside>
  )
}
