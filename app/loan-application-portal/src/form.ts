// form.ts — the application form's shape and client-side validation.
//
// These rules MIRROR loan-api's (BUILD-SPEC.md §7.1) so an applicant gets
// immediate feedback instead of a round trip. The server remains authoritative:
// anything that slips through here comes back as a 400 with field-level errors,
// and those are displayed too. Never treat this as the enforcement point.

export const TERMS = [6, 12, 24, 36] as const

export const MIN_AMOUNT = 10_000
export const MAX_AMOUNT = 2_000_000

// §7.1: ^254[17]\d{8}$
export const MSISDN_PATTERN = /^254[17]\d{8}$/

export interface FormValues {
  application_id: string
  applicant_name: string
  national_id: string
  wallet_msisdn: string
  amount_kes: string
  term_months: string
  monthly_income: string
  employment_months: string
  kyc_verified: boolean
  existing_defaults: string
}

export const EMPTY_FORM: FormValues = {
  // Left blank by default: loan-api generates APP-###### when it is omitted
  // (§7.1). The presenter can type APP-100244 to pin the scripted fixture.
  application_id: '',
  applicant_name: '',
  national_id: '',
  wallet_msisdn: '254',
  amount_kes: '',
  term_months: '24',
  monthly_income: '',
  employment_months: '',
  kyc_verified: true,
  existing_defaults: '0',
}

export type Errors = Partial<Record<keyof FormValues, string>>

function asNumber(raw: string): number | null {
  if (raw.trim() === '') return null
  const n = Number(raw)
  return Number.isFinite(n) ? n : null
}

export function validate(v: FormValues): Errors {
  const e: Errors = {}

  if (v.applicant_name.trim() === '') e.applicant_name = 'Enter the applicant name'
  if (v.national_id.trim() === '') e.national_id = 'Enter the national ID'

  if (!MSISDN_PATTERN.test(v.wallet_msisdn)) {
    e.wallet_msisdn = 'Must be 254 followed by 1 or 7 and 8 digits, e.g. 254712345678'
  }

  const amount = asNumber(v.amount_kes)
  if (amount === null) {
    e.amount_kes = 'Enter an amount'
  } else if (amount < MIN_AMOUNT || amount > MAX_AMOUNT) {
    e.amount_kes = `Must be between ${MIN_AMOUNT.toLocaleString()} and ${MAX_AMOUNT.toLocaleString()} KES`
  }

  if (!TERMS.includes(Number(v.term_months) as (typeof TERMS)[number])) {
    e.term_months = 'Choose 6, 12, 24 or 36 months'
  }

  const income = asNumber(v.monthly_income)
  if (income === null || income <= 0) e.monthly_income = 'Enter a monthly income above 0'

  const employment = asNumber(v.employment_months)
  if (employment === null || employment < 0) {
    e.employment_months = 'Enter months employed (0 or more)'
  }

  const defaults = asNumber(v.existing_defaults)
  if (defaults === null || defaults < 0) e.existing_defaults = 'Enter 0 or more'

  return e
}

// Converts the form's strings into the JSON body §7.1 expects. application_id is
// omitted entirely when blank, so the server generates one.
export function toPayload(v: FormValues): Record<string, unknown> {
  const body: Record<string, unknown> = {
    applicant_name: v.applicant_name.trim(),
    national_id: v.national_id.trim(),
    wallet_msisdn: v.wallet_msisdn.trim(),
    amount_kes: Number(v.amount_kes),
    term_months: Number(v.term_months),
    monthly_income: Number(v.monthly_income),
    employment_months: Number(v.employment_months),
    kyc_verified: v.kyc_verified,
    existing_defaults: Number(v.existing_defaults),
  }
  const id = v.application_id.trim()
  if (id !== '') body.application_id = id
  return body
}

// The estimated instalment, shown live beside the amount so an applicant sees
// affordability before submitting. Flat interest at 18% per annum (§5) — the
// same formula credit-scoring uses for its DTI, so the number on screen matches
// the one the decision was based on.
export function estimatedInstalment(amountKes: number, termMonths: number): number | null {
  if (!Number.isFinite(amountKes) || !Number.isFinite(termMonths) || termMonths <= 0) return null
  if (amountKes <= 0) return null
  const total = amountKes * (1 + 0.18 * (termMonths / 12))
  return Math.round((total / termMonths) * 100) / 100
}
