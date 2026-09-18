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

/** One statement's result. Both transports return this exact shape. */
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

/** Talk to a running `jevql serve` over HTTP. */
export interface HttpOptions {
  /** Base URL of `jevql serve`, e.g. http://127.0.0.1:7433 */
  url: string
  /** Bearer token if the server was started with --token. */
  token?: string
  /** Custom fetch (tests, polyfills). Defaults to globalThis.fetch. */
  fetch?: (input: string, init?: RequestInit) => Promise<Response>
}

/** Spawn the jevql binary per call. */
export interface CliOptions {
  cli: {
    /** Path or name of the binary. Default "jevql". */
    binary?: string
    /** Passed as the positional connection URL; otherwise DATABASE_URL / PG* env apply. */
    databaseUrl?: string
    /** Passed as --api-key; otherwise TYPESAFE_API_KEY from the environment applies. */
    apiKey?: string
    /** Passed as TYPESAFE_API_URL in the child environment. */
    apiUrl?: string
    /** Extra environment for the child (merged over process.env). */
    env?: Record<string, string>
    cwd?: string
  }
}

export type JevqlOptions = HttpOptions | CliOptions

export interface Health {
  ok: boolean
  version: string
  model?: string
}
