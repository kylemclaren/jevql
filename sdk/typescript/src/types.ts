/** Per-query TypeSafe usage, present when the statement contained jev_* calls. */
export interface Stats {
  collect_rows: number
  judged: number
  requests: number
  cache_hits: number
  input_tokens: number
  output_tokens: number
  usd: number
  elapsed_ms: number
}

/** Output of `explain`: the plan and cost estimate, produced without any TypeSafe call. */
export interface Explain {
  collect_sql: string
  rows: number
  sources: number
  questions: number
  judgements: number
  batches: number
  avg_row_chars: number
  tokens: number
  usd: number
  server_order: boolean
  question_list: string[]
}

/** One statement's result. */
export interface QueryResult {
  columns: string[]
  rows: unknown[][]
  row_count: number
  /** Command tag, e.g. "SELECT 5" or "INSERT 0 1". */
  tag: string
  /** True when jevql evaluated jev_* calls for this statement. */
  jev: boolean
  stats: Stats | null
  explain: Explain | null
}

export type ErrorCode = "sql" | "budget" | "api" | "auth" | "internal" | "transport"

export interface ErrorBody {
  error: string
  code?: ErrorCode
}

export interface QueryOptions {
  /** Default jev() threshold for this query (0..1). */
  threshold?: number
  /** Abort before any TypeSafe call if the collect step returns more rows than this. */
  maxRows?: number
}

/** Talk to a running `jevql serve` somewhere (remote mode). */
export interface RemoteOptions {
  /** Base URL of `jevql serve`, e.g. http://127.0.0.1:7433 */
  url: string
  /** Bearer token if the server was started with --token. */
  token?: string
  /** Custom fetch (tests, polyfills). Defaults to globalThis.fetch. */
  fetch?: (input: string, init?: RequestInit) => Promise<Response>
}

/** Run a private engine in this process's lifetime (embedded mode, the default). */
export interface EmbeddedOptions {
  url?: undefined
  /** Postgres connection URL. Otherwise DATABASE_URL / PG* from the environment apply. */
  databaseUrl?: string
  /** TypeSafe API key. Otherwise TYPESAFE_API_KEY applies. */
  apiKey?: string
  /** TypeSafe endpoint override. Otherwise TYPESAFE_API_URL applies. */
  apiUrl?: string
  /** Model name, default jev-latest. */
  model?: string
  /** Default jev() threshold. */
  threshold?: number
  /** Engine-wide --max-rows guard. */
  maxRows?: number
  /** Answer cache path; default ~/.cache/jevql/cache.db. */
  cachePath?: string
  /** Disable the answer cache. */
  noCache?: boolean
  /** Path to the jevql binary. Otherwise JEVQL_ENGINE_PATH, the bundled platform package, then PATH. */
  enginePath?: string
  /** Extra environment for the engine (merged over process.env). */
  env?: Record<string, string>
  /** Seconds to wait for the engine to report ready. Default 15. */
  startTimeout?: number
  /** Custom fetch (tests, polyfills). */
  fetch?: (input: string, init?: RequestInit) => Promise<Response>
}

/** @deprecated use RemoteOptions */
export type HttpOptions = RemoteOptions

export type JevqlOptions = RemoteOptions | EmbeddedOptions

export interface Health {
  ok: boolean
  version: string
  model?: string
}

/** Body of POST /v1/judge: rows you already hold, one question. */
export interface JudgeRequest {
  question: string
  /** noul (default), choice or score */
  kind?: "noul" | "choice" | "score"
  /** choice options or score levels */
  options?: string[]
  /** noul: pass when p >= threshold */
  threshold?: number
  rows: Record<string, unknown>[]
  /** include the raw TypeSafe answer */
  raw?: boolean
}

/** One row's answer, in input order. */
export interface JudgeAnswer {
  p?: number
  pass?: boolean
  choice?: string
  score?: number
  norm?: number
  confidence?: number
  probabilities?: Record<string, number>
  raw?: unknown
}

export interface JudgeResult {
  answers: JudgeAnswer[]
  stats: Stats | null
}
