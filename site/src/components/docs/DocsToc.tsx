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
      const line = window.scrollY + 120
      let i = 0
      for (let k = 0; k < els.length; k++) if (els[k].offsetTop <= line) i = k
      setActive(i)
    }
    onScroll()
    window.addEventListener("scroll", onScroll, { passive: true })
    return () => window.removeEventListener("scroll", onScroll)
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
