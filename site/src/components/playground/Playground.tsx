import { useEffect, useMemo, useRef, useState } from "react"

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

const EXAMPLES: { label: string; sql: string }[] = [
  { label: "urgent tickets that sound angry", sql: `SELECT id, subject, priority
FROM tickets
WHERE status = 'open' AND priority IN ('high', 'urgent')
  AND jev((subject, body), 'the customer sounds angry or is threatening to leave')
ORDER BY created_at DESC
LIMIT 15;` },
  { label: "route open tickets to teams", sql: `SELECT jev_choice((subject, body), 'which team should handle this?',
                  ARRAY['billing', 'technical', 'shipping', 'sales', 'other']) AS team,
       count(*)
FROM tickets
WHERE status = 'open' AND created_at > now() - interval '30 days'
GROUP BY 1
ORDER BY 2 DESC;` },
  { label: "bad reviews that are really about shipping", sql: `SELECT p.name, r.body
FROM reviews r JOIN products p ON p.id = r.product_id
WHERE r.stars <= 2 AND p.category = 'kitchen'
  AND jev((r.title, r.body), 'the complaint is about delivery or packaging, not the product itself')
LIMIT 10;` },
  { label: "how furious are this month's 1-star reviews?", sql: `SELECT left(body, 70) AS review,
       jev_score((title, body), 'how angry is the reviewer?', ARRAY['calm', 'annoyed', 'furious']) AS anger
FROM reviews
WHERE stars = 1 AND created_at > now() - interval '30 days'
ORDER BY anger DESC
LIMIT 10;` },
  { label: "staff who could work from home", sql: `SELECT name, job_title, jev_prob(employees, 'could do this job from home') AS p
FROM employees
WHERE department = 'operations'
ORDER BY p DESC
LIMIT 12;` },
  { label: "products good for a camping trip", sql: `SELECT name, price
FROM products
WHERE in_stock AND jev((name, description), 'useful on a weekend camping trip')
ORDER BY price;` },
  { label: "explain before you spend", sql: `-- press Explain (not Run): rows after filters, batches, tokens, cost. No TypeSafe call.
-- Run would judge every open ticket, which the 300-row guard on this node refuses.
SELECT id, subject FROM tickets WHERE status = 'open' AND jev(tickets, 'mentions a competitor by name');` },
]

const DEFAULT_NODE = "https://jevql-node.fly.dev"

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
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<"run" | "explain" | null>(null)
  const [tables, setTables] = useState<Table[]>([])
  const [detail, setDetail] = useState<TableDetail | null>(null)
  const [health, setHealth] = useState<string>("")
  const abort = useRef<AbortController | null>(null)
  const ta = useRef<HTMLTextAreaElement>(null)

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
    if (!token) { setHealth(""); return }
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
  }

  const fmt = (v: unknown) => v === null ? <i className="null">null</i> : typeof v === "object" ? JSON.stringify(v) : String(v)

  return (
    <div className="pg">
      <aside className="pg-side">
        <section className="pg-conn">
          <p className="eyebrow">node</p>
          <input value={node} onChange={(e) => setNode(e.target.value)} aria-label="Node URL" spellCheck={false} />
          <input value={token} onChange={(e) => setToken(e.target.value)} placeholder="bearer token" aria-label="Bearer token" type="password" />
          <p className={"pg-health " + (health.startsWith("connected") ? "ok" : "")}>{token ? health || "…" : "paste the playground token to connect"}</p>
        </section>
        <section>
          <p className="eyebrow">tables</p>
          <ul className="pg-tables">
            {tables.map((t) => (
              <li key={t.schema + t.name}><button type="button" onClick={() => describe(t.name)} className={detail?.name.endsWith(t.name) ? "on" : ""}>{t.name}<small>{t.kind}</small></button></li>
            ))}
            {tables.length === 0 && <li className="muted">{token ? "no tables yet" : "connect to list tables"}</li>}
          </ul>
          {detail && (
            <ul className="pg-cols">
              {detail.columns.map((c) => <li key={c.name}><b>{c.name}</b> <span>{c.type}</span></li>)}
            </ul>
          )}
        </section>
        <section>
          <p className="eyebrow">examples</p>
          <ul className="pg-examples">
            {EXAMPLES.map((ex) => <li key={ex.label}><button type="button" onClick={() => { setSql(ex.sql); ta.current?.focus() }}>{ex.label}</button></li>)}
          </ul>
        </section>
      </aside>

      <main className="pg-main">
        <div className="pg-editor">
          <textarea ref={ta} value={sql} onChange={(e) => setSql(e.target.value)} onKeyDown={onKey} spellCheck={false} aria-label="SQL" rows={9} />
          <div className="pg-actions">
            <button type="button" onClick={() => run("run")} disabled={!!busy || !token}>{busy === "run" ? "judging…" : "run"} <kbd>⌘↵</kbd></button>
            <button type="button" className="secondary" onClick={() => run("explain")} disabled={!!busy || !token}>{busy === "explain" ? "planning…" : "explain"} <kbd>⇧⌘↵</kbd></button>
            <span className="pg-hint">Read-only demo data. Every row that survives the SQL filters is judged; the node caps a query at 300 rows.</span>
          </div>
        </div>

        {error && <p className="pg-error" role="alert">{error}</p>}

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
    </div>
  )
}
