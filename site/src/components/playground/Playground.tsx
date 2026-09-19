import { useEffect, useMemo, useRef, useState } from "react"
import { Kbd, KbdGroup } from "@/components/ui/kbd"
import { Toaster, toast } from "@/components/ui/toast"
import { highlightSQL } from "./highlight"

/* ─────────────────────────────────────────────────────────
 * PLAYGROUND
 * A browser client for a jevql node: SQL editor, run / explain,
 * results table with the jev footer, schema browser, examples.
 * The node URL and token live in localStorage only.
 * ───────────────────────────────────────────────────────── */

type Stats = { collect_rows: number; judged: number; requests: number; cache_hits: number; input_tokens: number; usd: number; elapsed_ms: number }
type Explain = { collect_sql: string; rows: number; questions: number; judgements: number; batches: number; tokens: number; usd: number; server_order: boolean; question_list: string[] }
type Result = { columns: string[]; rows: unknown[][]; row_count: number; tag: string; jev: boolean; stats: Stats | null; explain: Explain | null }
type Table = { schema: string; name: string; kind: string }
type TableDetail = { name: string; columns: { name: string; type: string }[] }

// Ordered from plain SQL to multi-table judgements. level drives the swatch colour in the sidebar.
const LEVELS = ["plain SQL", "one jev() filter", "rank and scale", "classify and group", "several tables", "explain the bill"]
const EXAMPLES: { label: string; level: number; sql: string }[] = [
  { label: "what is in here?", level: 1, sql: `-- plain SQL passes straight through to Postgres. 365k Amazon listings, 1.6M shopper questions, 3.5M answers (Amazon-PQA).
SELECT category, count(*) AS listings
FROM listings
GROUP BY 1
ORDER BY 2 DESC;` },
  { label: "the most-asked-about backpack brands", level: 1, sql: `-- pick a brand from here, then use it in the WHERE of any example below
SELECT l.brand, count(*) AS questions
FROM questions q JOIN listings l ON l.asin = q.asin
WHERE l.category = 'backpacks' AND l.brand IS NOT NULL
GROUP BY 1
ORDER BY 2 DESC
LIMIT 15;` },
  { label: "is this drone safe for a kid?", level: 2, sql: `-- one jev() predicate. Postgres runs the brand filter; Jev judges the 295 rows that survive it.
SELECT left(l.title, 40) AS drone, q.text
FROM questions q JOIN listings l ON l.asin = q.asin
WHERE l.brand = 'Sky Viper'
  AND jev((l.title, q.text), 'asks whether it is suitable or safe for a child')
LIMIT 15;` },
  { label: "Versace sunglasses: questions not in English", level: 2, sql: `SELECT left(l.title, 30) AS listing, q.text
FROM questions q JOIN listings l ON l.asin = q.asin
WHERE l.brand = 'Versace'
  AND jev(q, 'is not written in English')
LIMIT 15;` },
  { label: "Polk speakers: battery and charging questions", level: 2, sql: `SELECT left(l.title, 40) AS speaker, q.text
FROM questions q JOIN listings l ON l.asin = q.asin
WHERE l.brand = 'Polk Audio'
  AND jev(q, 'asks about battery life or charging')
LIMIT 15;` },
  { label: "pest strip: who is asking about pets and kids?", level: 3, sql: `-- jev_prob ranks instead of filtering; the dataset's own yes/no verdict rides along
SELECT left(q.text, 70) AS question, q.verdict,
       jev_prob(q, 'asks whether it is safe around pets or children') AS p
FROM questions q JOIN listings l ON l.asin = q.asin
WHERE l.title ILIKE '%No-Pest Strip%' AND q.kind = 'yes-no'
ORDER BY p DESC
LIMIT 10;` },
  { label: "how frustrated are Sims 3 buyers?", level: 3, sql: `-- jev_score maps an ordered scale to a number you can sort by
SELECT left(q.text, 70) AS question,
       jev_score((q.text, l.title), 'How frustrated does the asker sound?',
                 ARRAY['neutral', 'mildly annoyed', 'frustrated', 'furious']) AS mood
FROM questions q JOIN listings l ON l.asin = q.asin
WHERE l.title ILIKE '%Sims 3 Starter%' AND q.kind = 'open-ended'
ORDER BY mood DESC
LIMIT 10;` },
  { label: "which fit questions is the model surest about?", level: 3, sql: `-- jev_confidence next to a jev() filter on the same question: each row is judged once, then sorted by how sure the model was
SELECT left(q.text, 70) AS question,
       jev_confidence(q, 'asks whether it fits a particular gun or holster') AS confidence
FROM questions q JOIN listings l ON l.asin = q.asin
WHERE l.brand = 'Streamlight' AND l.title ILIKE '%TLR-7%'
  AND jev(q, 'asks whether it fits a particular gun or holster')
ORDER BY confidence DESC
LIMIT 10;` },
  { label: "what do LifeStraw shoppers ask about?", level: 4, sql: `-- jev_choice picks one option per row, and the column groups like any other
SELECT jev_choice((q.text, l.title), 'What is this shopper asking about?',
                  ARRAY['filtering and safety', 'capacity or size', 'durability',
                        'cleaning and maintenance', 'price or shipping', 'something else']) AS topic,
       count(*)
FROM questions q JOIN listings l ON l.asin = q.asin
WHERE l.brand = 'LifeStraw' AND q.kind = 'open-ended'
GROUP BY 1
ORDER BY 2 DESC;` },
  { label: "what are these rugs made of?", level: 4, sql: `-- judge the listings themselves, not the questions
SELECT jev_choice((title, bullets), 'What is the rug made of?',
                  ARRAY['polypropylene or synthetic', 'wool', 'cotton', 'jute or natural fibre', 'not stated']) AS material,
       count(*)
FROM listings
WHERE category = 'area rugs' AND brand = 'Sweet Home Stores'
GROUP BY 1
ORDER BY 2 DESC;` },
  { label: "grade the model against real yes/no verdicts", level: 4, sql: `-- verdict is the dataset's label for what the answers say overall. How often does the model read each answer the same way?
SELECT q.verdict AS label,
       jev_choice((q.text, a.text), 'Does this answer say yes or no to the question?',
                  ARRAY['yes', 'no', 'neutral']) AS judged,
       count(*)
FROM answers a
JOIN questions q ON q.id = a.question_id
JOIN listings l ON l.asin = q.asin
WHERE l.brand = 'Bose' AND l.category = 'sunglasses' AND q.kind = 'yes-no'
GROUP BY 1, 2
ORDER BY 1, 3 DESC;` },
  { label: "questions the bullet points already answer", level: 5, sql: `-- two tables in one judgement: the bullet points from listings, the question from questions
SELECT left(q.text, 70) AS question,
       jev_prob((l.bullets, q.text), 'the bullet points already answer this question') AS covered
FROM questions q JOIN listings l ON l.asin = q.asin
WHERE l.title = 'Levi''s Men''s 501 Original-Fit Jean'
ORDER BY covered DESC
LIMIT 12;` },
  { label: "answers the bullet points could have given", level: 5, sql: `-- three tables: the answer, its question, and the listing's bullet points. A FAQ the seller already wrote.
SELECT left(q.text, 45) AS question, left(a.text, 60) AS answer
FROM answers a
JOIN questions q ON q.id = a.question_id
JOIN listings l ON l.asin = q.asin
WHERE l.title = 'Levi''s Men''s 501 Original-Fit Jean' AND q.kind = 'yes-no'
  AND jev((l.bullets, q.text, a.text), 'the answer could have been found in the bullet points')
LIMIT 10;` },
  { label: "answers that admit they don't know", level: 5, sql: `SELECT left(q.text, 50) AS question, left(a.text, 60) AS answer
FROM answers a
JOIN questions q ON q.id = a.question_id
JOIN listings l ON l.asin = q.asin
WHERE l.brand = 'Bose' AND l.category = 'sunglasses' AND q.kind = 'yes-no'
  AND jev((q.text, a.text), 'the person answering admits they do not know')
LIMIT 10;` },
  { label: "explain before you spend", level: 6, sql: `-- press Explain (not Run): rows after filters, batches, tokens, cost. No TypeSafe call.
-- This would judge every question about every Bluetooth speaker: 189,068 rows, about $0.70.
-- Run refuses it on this node (300-row cap). Add a brand filter and it fits.
SELECT q.text
FROM questions q
WHERE q.category = 'portable bluetooth speakers'
  AND jev(q, 'asks whether it is waterproof');` },
]

// The demo node also carries a small synthetic store; the playground features the Amazon data only.
const HIDDEN_ON_DEMO = new Set(["customers", "employees", "order_items", "orders", "products", "reviews", "tickets"])

const DEFAULT_NODE = "https://jevql-node.fly.dev"
// The demo node is open: read-only data, a 300-row cap per query and a rate limit.
// Point the playground at your own node from the settings panel (with a token if it has one).

function useLocal(key: string, initial: string) {
  const [v, setV] = useState(initial)
  useEffect(() => { try { const s = localStorage.getItem(key); if (s !== null) setV(s) } catch {} }, [key])
  const set = (n: string) => { setV(n); try { localStorage.setItem(key, n) } catch {} }
  return [v, set] as const
}

export default function Playground() {
  const [node, setNode] = useLocal("jevql-play-node", DEFAULT_NODE)
  const [token, setToken] = useLocal("jevql-play-token", "")
  const [sql, setSql] = useLocal("jevql-play-sql", EXAMPLES[0].sql)
  const [result, setResult] = useState<Result | null>(null)
  const setError = (msg: string | null) => { if (msg) toast.add({ type: "error", title: msg.includes(":") ? msg.slice(0, msg.indexOf(":")) : "error", description: msg.includes(":") ? msg.slice(msg.indexOf(":") + 1).trim() : msg, timeout: 8000 }) }
  const [busy, setBusy] = useState<"run" | "explain" | null>(null)
  const [tables, setTables] = useState<Table[]>([])
  const [detail, setDetail] = useState<TableDetail | null>(null)
  const [health, setHealth] = useState<string>("")
  const [settings, setSettings] = useState(false)
  const abort = useRef<AbortController | null>(null)
  const ta = useRef<HTMLTextAreaElement>(null)
  const hl = useRef<HTMLPreElement>(null)

  const headers = useMemo(() => ({ "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}) }), [token])
  const base = node.replace(/\/+$/, "")

  async function call<T>(path: string, init?: RequestInit): Promise<T> {
    const r = await fetch(base + path, { ...init, headers: { ...headers, ...(init?.headers as Record<string, string> | undefined) } })
    const text = await r.text()
    let body: any = null
    try { body = JSON.parse(text) } catch {}
    if (!r.ok) throw new Error(body?.error ? `${body.code ?? "error"}: ${body.error}` : `${r.status} ${r.statusText}`)
    return body as T
  }

  useEffect(() => {
    let dead = false
    call<{ ok: boolean; version: string; model: string }>("/v1/health").then((h) => !dead && setHealth(`connected · ${h.version} · ${h.model}`)).catch((e) => !dead && setHealth("cannot reach node: " + e.message))
    call<Table[]>("/v1/schema/tables").then((t) => !dead && setTables(t)).catch(() => {})
    return () => { dead = true }
  }, [base, token])

  async function run(mode: "run" | "explain") {
    abort.current?.abort()
    const ctl = new AbortController()
    abort.current = ctl
    setBusy(mode); setError(null)
    try {
      const body = JSON.stringify({ sql, explain: mode === "explain" })
      const res = await call<Result>("/v1/query", { method: "POST", body, signal: ctl.signal })
      setResult(res)
    } catch (e) {
      if ((e as Error).name !== "AbortError") setError((e as Error).message)
    } finally { setBusy(null) }
  }

  async function describe(name: string) {
    try { setDetail(await call<TableDetail>(`/v1/schema/tables/${encodeURIComponent(name)}`)) } catch (e) { setError((e as Error).message) }
  }

  function onKey(e: React.KeyboardEvent) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") { e.preventDefault(); run(e.shiftKey ? "explain" : "run") }
    if (e.key === "Tab") { e.preventDefault(); document.execCommand("insertText", false, "  ") }
  }
  const syncScroll = () => { if (hl.current && ta.current) { hl.current.scrollTop = ta.current.scrollTop; hl.current.scrollLeft = ta.current.scrollLeft } }
  const connected = health.startsWith("connected")

  const fmt = (v: unknown) => v === null ? <i className="null">null</i> : typeof v === "object" ? JSON.stringify(v) : String(v)

  return (
    <div className="pg">
      <Toaster />
      <aside className="pg-side">
        <section className="pg-conn">
          <p className="eyebrow">node</p>
          <button type="button" className={"pg-node " + (connected ? "ok" : "")} onClick={() => setSettings(true)} title="Change node">
            <span className="pg-node-url">{base.replace(/^https?:\/\//, "")}</span>
            <span className="pg-node-state">{connected ? "connected" : health ? "unreachable" : "…"}</span>
          </button>
        </section>
        <section>
          <p className="eyebrow">tables</p>
          <ul className="pg-tables">
            {tables.filter((t) => node !== DEFAULT_NODE || !HIDDEN_ON_DEMO.has(t.name)).map((t) => (
              <li key={t.schema + t.name}><button type="button" onClick={() => describe(t.name)} className={detail?.name.endsWith(t.name) ? "on" : ""}>{t.name}<small>{t.kind}</small></button></li>
            ))}
            {tables.length === 0 && <li className="muted">{connected ? "no tables" : "connect a node to list tables"}</li>}
          </ul>
          {detail && (
            <ul className="pg-cols">
              {detail.columns.map((c) => <li key={c.name}><b>{c.name}</b> <span>{c.type}</span></li>)}
            </ul>
          )}
        </section>
        <section>
          <p className="eyebrow">examples</p>
          <p className="pg-scale" aria-hidden="true"><span>simple</span><i /><span>complex</span></p>
          <ul className="pg-examples">
            {EXAMPLES.map((ex) => <li key={ex.label}><button type="button" data-level={ex.level} title={LEVELS[ex.level - 1]} onClick={() => { setSql(ex.sql); ta.current?.focus() }}>{ex.label}</button></li>)}
          </ul>
        </section>
      </aside>

      <main className="pg-main">
        <div className="pg-editor">
          <div className="pg-code">
            <pre ref={hl} className="pg-hl" aria-hidden="true" dangerouslySetInnerHTML={{ __html: highlightSQL(sql) }} />
            <textarea ref={ta} value={sql} onChange={(e) => setSql(e.target.value)} onKeyDown={onKey} onScroll={syncScroll} spellCheck={false} aria-label="SQL" rows={9} />
          </div>
          <div className="pg-actions">
            <button type="button" onClick={() => run("run")} disabled={!!busy}>{busy === "run" ? "judging…" : "run"} <KbdGroup><Kbd>⌘</Kbd><Kbd>↵</Kbd></KbdGroup></button>
            <button type="button" className="secondary" onClick={() => run("explain")} disabled={!!busy}>{busy === "explain" ? "planning…" : "explain"} <KbdGroup><Kbd>⇧</Kbd><Kbd>⌘</Kbd><Kbd>↵</Kbd></KbdGroup></button>
            <span className="pg-hint">Read-only demo data. Every row that survives the SQL filters is judged; this node caps a query at 300 rows.</span>
          </div>
          <div className={"pg-progress" + (busy ? " on" : "")} role="progressbar" aria-busy={!!busy} aria-label={busy === "explain" ? "planning" : "judging"}><i /></div>
        </div>


        {result?.explain && (
          <div className="pg-explain">
            <p className="eyebrow">plan · no TypeSafe call was made</p>
            <pre>{result.explain.collect_sql}</pre>
            <dl>
              <div><dt>rows after filters</dt><dd>{result.explain.rows}</dd></div>
              <div><dt>questions</dt><dd>{result.explain.questions}</dd></div>
              <div><dt>judgements</dt><dd>{result.explain.judgements}</dd></div>
              <div><dt>batches</dt><dd>{result.explain.batches}</dd></div>
              <div><dt>est. tokens</dt><dd>{result.explain.tokens.toLocaleString()}</dd></div>
              <div><dt>est. cost</dt><dd>${result.explain.usd.toFixed(4)}</dd></div>
            </dl>
          </div>
        )}

        {result && !result.explain && (
          <div className="pg-result">
            {result.columns.length > 0 ? (
              <div className="pg-scroll">
                <table>
                  <thead><tr>{result.columns.map((c) => <th key={c}>{c}</th>)}</tr></thead>
                  <tbody>{result.rows.map((r, i) => <tr key={i}>{r.map((v, j) => <td key={j} className={typeof v === "number" ? "num" : ""}>{fmt(v)}</td>)}</tr>)}</tbody>
                </table>
              </div>
            ) : <p className="muted">{result.tag}</p>}
            <p className="pg-footer">
              ({result.row_count} row{result.row_count === 1 ? "" : "s"})
              {result.stats && <> · jev: {result.stats.judged} judged, {result.stats.requests} req, {result.stats.cache_hits} cache hits, {result.stats.input_tokens.toLocaleString()} tokens, ${result.stats.usd.toFixed(4)}, {result.stats.elapsed_ms} ms</>}
            </p>
          </div>
        )}
      </main>

      <div className={"pg-drawer-backdrop " + (settings ? "open" : "")} onClick={() => setSettings(false)} aria-hidden={!settings} />
      <aside className={"pg-drawer " + (settings ? "open" : "")} role="dialog" aria-label="Node settings" aria-hidden={!settings}>
        <div className="pg-drawer-head">
          <div><p className="eyebrow">settings</p><h2>Which node?</h2></div>
          <button type="button" className="secondary small" onClick={() => setSettings(false)} aria-label="Close">✕</button>
        </div>
        <label>Node URL<input value={node} onChange={(e) => setNode(e.target.value)} spellCheck={false} placeholder={DEFAULT_NODE} /></label>
        <label>Bearer token <small>only if your node has one</small><input value={token} onChange={(e) => setToken(e.target.value)} type="password" placeholder="leave empty for the demo node" /></label>
        <p className={"pg-health " + (connected ? "ok" : "")}>{health || "…"}</p>
        <p className="fine">The demo node at <code>{DEFAULT_NODE.replace(/^https?:\/\//, "")}</code> is open and read-only. Run your own with <code>jevql serve</code> (or <a href="/docs/deploy/">deploy a node</a>), start it with <code>--cors {typeof location !== "undefined" ? location.origin : "https://jevql.fly.dev"}</code>, and point this page at it.</p>
        <button type="button" className="secondary" onClick={() => { setNode(DEFAULT_NODE); setToken("") }}>back to the demo node</button>
      </aside>
    </div>
  )
}
