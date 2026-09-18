import type { ErrorCode } from "./types.js"

/** Raised for SQL, budget, API, auth and transport failures. `code` says which. */
export class JevqlError extends Error {
  readonly code: ErrorCode
  /** HTTP status when the error came from the engine or server. */
  readonly status?: number

  constructor(message: string, code: ErrorCode, status?: number) {
    super(message)
    this.name = "JevqlError"
    this.code = code
    this.status = status
  }
}

export function codeForStatus(status: number): ErrorCode {
  switch (status) {
    case 400:
      return "sql"
    case 401:
      return "auth"
    case 402:
      return "budget"
    case 502:
      return "api"
    default:
      return status >= 500 ? "internal" : "transport"
  }
}
