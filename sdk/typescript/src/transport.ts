import type { Explain, Health, QueryOptions, QueryResult } from "./types.js"

export interface Transport {
  query(sql: string, opts?: QueryOptions & { explain?: boolean }): Promise<QueryResult>
  health(): Promise<Health>
}

export function explainOf(res: QueryResult): Explain {
  if (!res.explain) {
    throw new Error("jevql: statement has no jev_* calls, nothing to explain")
  }
  return res.explain
}
