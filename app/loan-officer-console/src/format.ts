// format.ts — display helpers. Pure, so they are unit-testable.

// Worst first — the collections team's priority order (§7.4, §7.5).
export const BUCKET_ORDER = [
  'NPL_90_PLUS',
  'DPD_61_90',
  'DPD_31_60',
  'DPD_1_30',
  'CURRENT',
] as const

export const BUCKET_LABEL: Record<string, string> = {
  NPL_90_PLUS: 'Non-performing (90+)',
  DPD_61_90: '61–90 days',
  DPD_31_60: '31–60 days',
  DPD_1_30: '1–30 days',
  CURRENT: 'Current',
}

export const BANK_ACTION: Record<string, string> = {
  NPL_90_PLUS: 'Provision and report',
  DPD_61_90: 'Demand letter',
  DPD_31_60: 'Collections call',
  DPD_1_30: 'SMS reminder',
  CURRENT: 'None',
}

export function money(v: number | null | undefined): string {
  if (v === null || v === undefined) return '—'
  return v.toLocaleString('en-KE', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })
}

export function date(v: string | null | undefined): string {
  if (!v) return '—'
  // Dates arrive as ISO strings or Postgres timestamps; take the day part.
  return v.slice(0, 10)
}

export function bucketRank(bucket: string | null | undefined): number {
  if (!bucket) return BUCKET_ORDER.length
  const i = BUCKET_ORDER.indexOf(bucket as (typeof BUCKET_ORDER)[number])
  return i === -1 ? BUCKET_ORDER.length : i
}

// An instalment is overdue when it is unpaid and its due date has passed.
export function isOverdue(instalment: { due_date: string; paid_date: string | null }): boolean {
  if (instalment.paid_date) return false
  return new Date(instalment.due_date) < new Date(new Date().toDateString())
}
