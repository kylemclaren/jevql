import type { Explain, Health, JudgeRequest, JudgeResult, QueryOptions, QueryResult } from "./types.js"

export interface Transport {
  query(sql: string, opts?: QueryOptions & { explain?: boolean }): Promise<QueryResult>
  health(): Promise<Health>
  judge(req: JudgeRequest): Promise<JudgeResult>
}

export function explainOf(res: QueryResult): Explain {
  if (!res.explain) {
    throw new Error("jevql: statement has no jev_* calls, nothing to explain")
  }
  return res.explain
}
