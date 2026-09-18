import { useEffect, useMemo, useRef, useState } from "react"
import { HookSidebar } from "@/components/ui/hook-sidebar"

type Heading = { depth: number; slug: string; text: string }

/* Table of contents drawn with the hook rail.
 * The active row is the section under the reading line (a quarter down the
 * viewport). At the very bottom of the page the last heading wins, because
 * short final sections can never reach the line. A click pins its row until
 * the reader scrolls by hand, so the smooth scroll never "corrects" it. */
export default function DocsToc({ headings }: { headings: Heading[] }) {
  const items = useMemo(() => headings.map((h) => ({ label: (h.depth === 3 ? "· " : "") + h.text, href: "#" + h.slug })), [headings])
  const [active, setActive] = useState(0)
  const pinned = useRef<number | null>(null)

  useEffect(() => {
    const els = headings.map((h) => document.getElementById(h.slug)).filter(Boolean) as HTMLElement[]
    if (!els.length) return
    const compute = () => {
      if (pinned.current !== null) return
      const doc = document.documentElement
      const y = window.scrollY
      const atBottom = y + window.innerHeight >= doc.scrollHeight - 2
      if (atBottom) {
        setActive(els.length - 1)
        return
      }
      const line = y + Math.min(160, window.innerHeight * 0.25)
      let i = 0
      for (let k = 0; k < els.length; k++) {
        if (els[k].getBoundingClientRect().top + y <= line) i = k
      }
      setActive(i)
    }
    const unpin = () => {
      pinned.current = null
      compute()
    }
    compute()
    if (location.hash) {
      const i = headings.findIndex((h) => "#" + h.slug === location.hash)
      if (i >= 0) {
        pinned.current = i
        setActive(i)
      }
    }
    window.addEventListener("scroll", compute, { passive: true })
    window.addEventListener("resize", compute)
    for (const ev of ["wheel", "touchmove", "keydown", "mousedown"] as const) window.addEventListener(ev, unpin, { passive: true })
    return () => {
      window.removeEventListener("scroll", compute)
      window.removeEventListener("resize", compute)
      for (const ev of ["wheel", "touchmove", "keydown", "mousedown"] as const) window.removeEventListener(ev, unpin)
    }
  }, [headings])

  return (
    <HookSidebar
      label="on this page"
      items={items}
      value={active}
      onChange={(i) => {
        pinned.current = i
        setActive(i)
        const el = document.getElementById(headings[i].slug)
        el?.scrollIntoView({ behavior: "smooth", block: "start" })
        history.replaceState(null, "", "#" + headings[i].slug)
      }}
      color="#ee5ba6"
      className="doc-toc-nav"
    />
  )
}
