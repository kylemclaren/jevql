import { useEffect, useMemo, useState } from "react"
import { HookSidebar } from "@/components/ui/hook-sidebar"

type Heading = { depth: number; slug: string; text: string }

/* Table of contents drawn with the hook rail; the active row follows scroll. */
export default function DocsToc({ headings }: { headings: Heading[] }) {
  const items = useMemo(() => headings.map((h) => ({ label: (h.depth === 3 ? "· " : "") + h.text, href: "#" + h.slug })), [headings])
  const [active, setActive] = useState(0)

  useEffect(() => {
    const els = headings.map((h) => document.getElementById(h.slug)).filter(Boolean) as HTMLElement[]
    if (!els.length) return
    const onScroll = () => {
      const doc = document.documentElement
      const atBottom = window.scrollY + window.innerHeight >= doc.scrollHeight - 4
      if (atBottom) {
        setActive(els.length - 1)
        return
      }
      // A heading is "current" once it passes a line a quarter of the way down the viewport.
      const line = window.scrollY + Math.min(160, window.innerHeight * 0.25)
      let i = 0
      for (let k = 0; k < els.length; k++) if (els[k].getBoundingClientRect().top + window.scrollY <= line) i = k
      setActive(i)
    }
    onScroll()
    window.addEventListener("scroll", onScroll, { passive: true })
    window.addEventListener("resize", onScroll)
    return () => {
      window.removeEventListener("scroll", onScroll)
      window.removeEventListener("resize", onScroll)
    }
  }, [headings])

  return (
    <HookSidebar
      label="on this page"
      items={items}
      value={active}
      onChange={(i) => {
        const el = document.getElementById(headings[i].slug)
        el?.scrollIntoView({ behavior: "smooth", block: "start" })
        history.replaceState(null, "", "#" + headings[i].slug)
      }}
      color="#ee5ba6"
      className="doc-toc-nav"
    />
  )
}
