// A small SQL tokenizer for the playground editor overlay. Not a parser:
// it colours keywords, strings, numbers, comments and jev_* calls.
const KEYWORDS = new Set("select from where and or not in is null as order by group having limit offset join left right inner outer on distinct case when then else end like ilike between exists union all with values insert update delete set into asc desc interval now count sum avg min max array true false coalesce cast".split(" "))
const JEV = /^jev(_prob|_choice|_score|_score_norm|_confidence|_eval)?$/i

const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")

export function highlightSQL(src: string): string {
  let out = ""
  let i = 0
  while (i < src.length) {
    const ch = src[i]
    // line comment
    if (ch === "-" && src[i + 1] === "-") {
      let j = src.indexOf("\n", i)
      if (j < 0) j = src.length
      out += `<span class="tk-c">${esc(src.slice(i, j))}</span>`
      i = j
      continue
    }
    // string
    if (ch === "'") {
      let j = i + 1
      while (j < src.length) {
        if (src[j] === "'" && src[j + 1] === "'") { j += 2; continue }
        if (src[j] === "'") { j++; break }
        j++
      }
      out += `<span class="tk-s">${esc(src.slice(i, j))}</span>`
      i = j
      continue
    }
    // number
    if (/[0-9]/.test(ch) && !/[A-Za-z_]/.test(src[i - 1] ?? "")) {
      let j = i
      while (j < src.length && /[0-9.]/.test(src[j])) j++
      out += `<span class="tk-n">${esc(src.slice(i, j))}</span>`
      i = j
      continue
    }
    // identifier / keyword
    if (/[A-Za-z_]/.test(ch)) {
      let j = i
      while (j < src.length && /[A-Za-z0-9_]/.test(src[j])) j++
      const word = src.slice(i, j)
      if (JEV.test(word)) out += `<span class="tk-j">${esc(word)}</span>`
      else if (KEYWORDS.has(word.toLowerCase())) out += `<span class="tk-k">${esc(word)}</span>`
      else out += esc(word)
      i = j
      continue
    }
    out += esc(ch)
    i++
  }
  // trailing newline needs a visible line for the overlay to match textarea height
  return out + (src.endsWith("\n") ? " " : "")
}
