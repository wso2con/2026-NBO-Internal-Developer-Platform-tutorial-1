// api.ts — the loan-api client (BUILD-SPEC.md §7.1, §7.5).
//
// Only loan-api is called. credit-scoring is internal-only and has no public
// route (§7.2) — on OpenChoreo a call to it from outside the cell is refused,
// which is the Act 2 beat. The scoring factor breakdown reaches us through
// loan-api's detail endpoint instead.

import { apiBaseUrl } from './config'

export interface Factor {
  name: string
  points: number
}

export interface Disbursement {
  disbursement_id: string
  amount_kes: number
  wallet_msisdn: string
  provider_ref: string | null
  status: string
  disbursed_at: string
}

export interface Instalment {
  instalment_no: number
  due_date: string
  amount_due_kes: number
  paid_date: string | null
}

export interface ApplicationSummary {
  application_id: string
  applicant_name: string
  amount_kes: number
  term_months: number
  score: number | null
  decision: string | null
  submitted_at: string
  arrears_bucket: string | null
}

export interface Application extends ApplicationSummary {
  national_id: string
  wallet_msisdn: string
  monthly_income: number
  employment_months: number
  kyc_verified: boolean
  existing_defaults: number
  decision_reason: string | null
  decided_at: string | null
  disbursement: Disbursement | null
  days_past_due: number | null
  repayments?: Instalment[]
  score_factors?: Factor[]
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(`${apiBaseUrl()}${path}`, {
    signal,
    headers: { Accept: 'application/json' },
  })
  if (!res.ok) {
    throw new Error(`${res.status} ${res.statusText} from ${path}`)
  }
  return (await res.json()) as T
}

export function listApplications(
  limit = 50,
  bucket?: string,
  signal?: AbortSignal,
): Promise<ApplicationSummary[]> {
  const params = new URLSearchParams({ limit: String(limit) })
  if (bucket) params.set('bucket', bucket)
  return get<ApplicationSummary[]>(`/applications?${params}`, signal)
}

export function getApplication(id: string, signal?: AbortSignal): Promise<Application> {
  return get<Application>(`/applications/${encodeURIComponent(id)}`, signal)
}
