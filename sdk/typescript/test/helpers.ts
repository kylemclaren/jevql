import type { QueryResult } from "../src/types.js"

export const sample: QueryResult = {
  columns: ["name", "p"],
  rows: [
    ["Ada", 0.93],
    ["Bo", 0.12],
  ],
  row_count: 2,
  tag: "SELECT 2",
  jev: true,
  stats: { collect_rows: 2, judged: 2, requests: 1, cache_hits: 0, input_tokens: 300, output_tokens: 10, usd: 0.0000126, elapsed_ms: 812 },
  explain: null,
}

export const sampleExplain: QueryResult = {
  ...sample,
  rows: [],
  row_count: 0,
  stats: null,
  explain: {
    collect_sql: "SELECT people.name AS __jev_s0_0, name FROM people",
    rows: 12,
    sources: 1,
    questions: 1,
    judgements: 12,
    batches: 1,
    avg_row_chars: 201,
    tokens: 1100,
    usd: 0.0000462,
    server_order: false,
    question_list: ['noul "x" on people'],
  },
}
