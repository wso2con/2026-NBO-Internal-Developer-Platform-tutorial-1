import { describe, expect, it } from 'vitest'
import { BUCKET_ORDER, bucketRank, date, isOverdue, money } from './format'

describe('money', () => {
  it('formats to 2dp with thousands separators', () => {
    expect(money(616000.02)).toBe('616,000.02')
    expect(money(136000)).toBe('136,000.00')
    expect(money(269166.65)).toBe('269,166.65')
    expect(money(0)).toBe('0.00')
  })

  it('renders an em dash for missing values rather than NaN', () => {
    expect(money(null)).toBe('—')
    expect(money(undefined)).toBe('—')
  })
})

describe('date', () => {
  it('takes the day part of a Postgres timestamp', () => {
    expect(date('2026-09-12 04:07:21.767213+00')).toBe('2026-09-12')
  })

  it('takes the day part of an ISO string', () => {
    expect(date('2026-09-12T04:07:21Z')).toBe('2026-09-12')
  })

  it('renders an em dash for null', () => {
    expect(date(null)).toBe('—')
  })
})

describe('bucketRank', () => {
  // Worst first — the collections team's priority order (§7.4, §7.5).
  it('ranks NPL worst and CURRENT best', () => {
    expect(bucketRank('NPL_90_PLUS')).toBeLessThan(bucketRank('DPD_61_90'))
    expect(bucketRank('DPD_61_90')).toBeLessThan(bucketRank('DPD_31_60'))
    expect(bucketRank('DPD_31_60')).toBeLessThan(bucketRank('DPD_1_30'))
    expect(bucketRank('DPD_1_30')).toBeLessThan(bucketRank('CURRENT'))
  })

  it('sorts unclassified loans last', () => {
    expect(bucketRank(null)).toBeGreaterThanOrEqual(BUCKET_ORDER.length)
    expect(bucketRank('SOMETHING_ELSE')).toBeGreaterThanOrEqual(BUCKET_ORDER.length)
  })
})

describe('isOverdue', () => {
  const past = '2020-01-01'
  const future = '2099-01-01'

  it('is true for an unpaid instalment whose due date has passed', () => {
    expect(isOverdue({ due_date: past, paid_date: null })).toBe(true)
  })

  it('is false once paid, however late', () => {
    expect(isOverdue({ due_date: past, paid_date: '2020-02-01' })).toBe(false)
  })

  it('is false for an instalment not yet due', () => {
    // The case that makes a loan disbursed today CURRENT rather than overdue.
    expect(isOverdue({ due_date: future, paid_date: null })).toBe(false)
  })
})
