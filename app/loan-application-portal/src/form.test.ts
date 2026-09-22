import { describe, expect, it } from 'vitest'
import { EMPTY_FORM, estimatedInstalment, toPayload, validate, type FormValues } from './form'

// A form filled in the way APP-100244 is — the fixture that scores 712.
const good: FormValues = {
  ...EMPTY_FORM,
  applicant_name: 'Amina W.',
  national_id: '12345678',
  wallet_msisdn: '254712345678',
  amount_kes: '250000',
  term_months: '24',
  monthly_income: '96000',
  employment_months: '30',
  kyc_verified: true,
  existing_defaults: '0',
}

describe('validate', () => {
  it('accepts a complete, valid application', () => {
    expect(validate(good)).toEqual({})
  })

  // Mirrors §7.1: amount 10,000-2,000,000 inclusive.
  it.each([
    ['9999', false],
    ['10000', true],
    ['2000000', true],
    ['2000001', false],
    ['', false],
    ['abc', false],
  ])('amount %s valid=%s', (amount, ok) => {
    const errors = validate({ ...good, amount_kes: amount })
    expect(errors.amount_kes === undefined).toBe(ok)
  })

  // §7.1: ^254[17]\d{8}$ — the third digit must be 1 or 7.
  it.each([
    ['254712345678', true],
    ['254112345678', true],
    ['254812345678', false],
    ['0712345678', false],
    ['+254712345678', false],
    ['25471234567', false],
    ['', false],
  ])('msisdn %s valid=%s', (msisdn, ok) => {
    const errors = validate({ ...good, wallet_msisdn: msisdn })
    expect(errors.wallet_msisdn === undefined).toBe(ok)
  })

  it.each([
    ['6', true],
    ['12', true],
    ['24', true],
    ['36', true],
    ['18', false],
    ['48', false],
  ])('term %s valid=%s', (term, ok) => {
    const errors = validate({ ...good, term_months: term })
    expect(errors.term_months === undefined).toBe(ok)
  })

  it('requires an income above zero', () => {
    expect(validate({ ...good, monthly_income: '0' }).monthly_income).toBeDefined()
    expect(validate({ ...good, monthly_income: '' }).monthly_income).toBeDefined()
  })

  it('rejects negative employment and defaults', () => {
    expect(validate({ ...good, employment_months: '-1' }).employment_months).toBeDefined()
    expect(validate({ ...good, existing_defaults: '-1' }).existing_defaults).toBeDefined()
  })

  it('reports every problem at once, not just the first', () => {
    const errors = validate({
      ...EMPTY_FORM,
      applicant_name: '',
      national_id: '',
      wallet_msisdn: 'nope',
      amount_kes: '1',
      term_months: '7',
      monthly_income: '0',
      employment_months: '-1',
      existing_defaults: '-1',
    })
    expect(Object.keys(errors).length).toBeGreaterThanOrEqual(7)
  })
})

describe('toPayload', () => {
  it('converts strings to the numbers §7.1 expects', () => {
    const body = toPayload(good)
    expect(body).toMatchObject({
      applicant_name: 'Amina W.',
      amount_kes: 250000,
      term_months: 24,
      monthly_income: 96000,
      employment_months: 30,
      kyc_verified: true,
      existing_defaults: 0,
    })
  })

  // §7.1: omitting application_id makes loan-api generate APP-######.
  it('omits application_id entirely when blank', () => {
    expect('application_id' in toPayload({ ...good, application_id: '' })).toBe(false)
    expect('application_id' in toPayload({ ...good, application_id: '   ' })).toBe(false)
  })

  it('sends application_id when the presenter pins one', () => {
    expect(toPayload({ ...good, application_id: 'APP-100244' })).toMatchObject({
      application_id: 'APP-100244',
    })
  })
})

describe('estimatedInstalment', () => {
  // Flat 18% p.a. (§5) — the same figures the seed and golden fixtures use.
  it.each([
    [250000, 24, 14166.67],
    [300000, 24, 17000],
    [120000, 12, 11800],
    [150000, 24, 8500],
  ])('%i over %i months is %f', (amount, term, want) => {
    expect(estimatedInstalment(amount, term)).toBeCloseTo(want, 2)
  })

  it('returns null rather than NaN for incomplete input', () => {
    expect(estimatedInstalment(NaN, 24)).toBeNull()
    expect(estimatedInstalment(250000, 0)).toBeNull()
    expect(estimatedInstalment(0, 24)).toBeNull()
  })
})
