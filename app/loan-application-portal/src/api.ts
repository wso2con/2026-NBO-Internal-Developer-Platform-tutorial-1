// api.ts — the loan-api client (BUILD-SPEC.md §7.1).
//
// One call: POST /applications. This portal is the customer's front door and
// deliberately reads nothing back — no arrears, no schedules, no other people's
// applications. That is the loan officer console's job, and a customer-facing
// app that could read collections data would be the wrong shape entirely.

import { apiBaseUrl } from './config'

export interface Factor {
  name: string
  points: number
}

export interface Decision {
  application_id: string
  score: number | null
  decision: string | null
  decision_reason: string | null
  amount_kes: number
  term_months: number
  score_factors?: Factor[]
}

export interface FieldError {
  field: string
  message: string
}

// Everything the UI needs to know about the outcome, including WHICH kind of
// success it was: 201 is a new application, 200 means loan-api recognised a
// duplicate and returned the existing decision without re-scoring (§7.1).
export type SubmitResult =
  | { kind: 'created'; decision: Decision }
  | { kind: 'duplicate'; decision: Decision }
  | { kind: 'invalid'; errors: FieldError[] }
  | { kind: 'scoring-unavailable'; message: string }
  | { kind: 'error'; message: string }

export async function submitApplication(body: Record<string, unknown>): Promise<SubmitResult> {
  let res: Response
  try {
    res = await fetch(`${apiBaseUrl()}/applications`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(body),
    })
  } catch (e) {
    return {
      kind: 'error',
      message: e instanceof Error ? e.message : 'Could not reach the service',
    }
  }

  if (res.status === 201 || res.status === 200) {
    const decision = (await res.json()) as Decision
    return { kind: res.status === 201 ? 'created' : 'duplicate', decision }
  }

  if (res.status === 400) {
    const body = (await res.json()) as { errors?: FieldError[] }
    return { kind: 'invalid', errors: body.errors ?? [] }
  }

  // §7.1: scoring unreachable returns 503 and leaves the application PENDING.
  // Never presented as a decline — no decision was made at all.
  if (res.status === 503) {
    const body = (await res.json().catch(() => ({}))) as { message?: string }
    return {
      kind: 'scoring-unavailable',
      message: body.message ?? 'The scoring service is unavailable',
    }
  }

  return { kind: 'error', message: `Unexpected response ${res.status} from the service` }
}
