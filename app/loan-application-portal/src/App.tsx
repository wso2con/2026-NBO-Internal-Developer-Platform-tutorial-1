// App.tsx — Kifaru Bank loan application portal.
//
// The customer's front door: fill in the form, get a decision. It stands in for
// the mobile app in the demo narrative (§1) — a browser tab on the presenter's
// laptop has fewer ways to fail on stage than an emulator or a phone on a camera.
//
// Submission only. No arrears, no repayment schedules, no lookups of other
// applications: that is the loan officer console's job, and mixing the two
// audiences into one screen would be the wrong shape.

import { useMemo, useState } from 'react'
import Logo from './Logo'
import { submitApplication, type SubmitResult } from './api'
import {
  EMPTY_FORM,
  TERMS,
  estimatedInstalment,
  toPayload,
  validate,
  type Errors,
  type FormValues,
} from './form'

const money = (v: number) =>
  v.toLocaleString('en-KE', { minimumFractionDigits: 2, maximumFractionDigits: 2 })

export default function App() {
  const [values, setValues] = useState<FormValues>(EMPTY_FORM)
  const [errors, setErrors] = useState<Errors>({})
  const [serverErrors, setServerErrors] = useState<Record<string, string>>({})
  const [result, setResult] = useState<SubmitResult | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const instalment = useMemo(
    () => estimatedInstalment(Number(values.amount_kes), Number(values.term_months)),
    [values.amount_kes, values.term_months],
  )

  function set<K extends keyof FormValues>(key: K, value: FormValues[K]) {
    setValues((v) => ({ ...v, [key]: value }))
    setErrors((e) => ({ ...e, [key]: undefined }))
    setServerErrors((e) => ({ ...e, [key]: '' }))
  }

  async function onSubmit(event: React.FormEvent) {
    event.preventDefault()
    const found = validate(values)
    setErrors(found)
    setServerErrors({})
    if (Object.keys(found).length > 0) return

    setSubmitting(true)
    const outcome = await submitApplication(toPayload(values))
    setSubmitting(false)

    if (outcome.kind === 'invalid') {
      // loan-api is authoritative; surface its field errors beside the fields.
      setServerErrors(Object.fromEntries(outcome.errors.map((e) => [e.field, e.message])))
      return
    }
    setResult(outcome)
  }

  function startOver() {
    setResult(null)
    setValues(EMPTY_FORM)
    setErrors({})
    setServerErrors({})
  }

  if (result) {
    return <Outcome result={result} onAnother={startOver} />
  }

  const err = (k: keyof FormValues) => errors[k] ?? serverErrors[k] ?? ''

  return (
    <>
      <header className="masthead">
        <div className="wordmark">
          <Logo size={38} />
          <span className="name">Kifaru Bank</span>
          <span className="sub">Personal Loans</span>
        </div>
      </header>

      <main>
        <h1>Apply for a loan</h1>
        <p className="subtitle">
          A decision in seconds. Approved loans are sent straight to your mobile wallet.
        </p>

        <form onSubmit={onSubmit} noValidate>
          <fieldset>
            <legend>About you</legend>

            <Field label="Full name" error={err('applicant_name')}>
              <input
                value={values.applicant_name}
                onChange={(e) => set('applicant_name', e.target.value)}
                placeholder="Amina W."
                autoComplete="name"
              />
            </Field>

            <Field label="National ID" error={err('national_id')}>
              <input
                value={values.national_id}
                onChange={(e) => set('national_id', e.target.value)}
                placeholder="12345678"
                inputMode="numeric"
              />
            </Field>

            <Field
              label="Mobile wallet"
              hint="Kenyan number, e.g. 254712345678"
              error={err('wallet_msisdn')}
            >
              <input
                value={values.wallet_msisdn}
                onChange={(e) => set('wallet_msisdn', e.target.value)}
                inputMode="numeric"
                maxLength={12}
              />
            </Field>
          </fieldset>

          <fieldset>
            <legend>The loan</legend>

            <Field label="Amount (KES)" hint="10,000 – 2,000,000" error={err('amount_kes')}>
              <input
                value={values.amount_kes}
                onChange={(e) => set('amount_kes', e.target.value)}
                placeholder="250000"
                inputMode="numeric"
              />
            </Field>

            <Field label="Term" error={err('term_months')}>
              <select
                value={values.term_months}
                onChange={(e) => set('term_months', e.target.value)}
              >
                {TERMS.map((t) => (
                  <option key={t} value={t}>
                    {t} months
                  </option>
                ))}
              </select>
            </Field>

            {instalment !== null && (
              <p className="estimate">
                Estimated repayment <strong>KES {money(instalment)}</strong> per month
                <span> · 18% flat per annum</span>
              </p>
            )}
          </fieldset>

          <fieldset>
            <legend>Your circumstances</legend>

            <Field label="Monthly income (KES)" error={err('monthly_income')}>
              <input
                value={values.monthly_income}
                onChange={(e) => set('monthly_income', e.target.value)}
                placeholder="96000"
                inputMode="numeric"
              />
            </Field>

            <Field label="Months in employment" error={err('employment_months')}>
              <input
                value={values.employment_months}
                onChange={(e) => set('employment_months', e.target.value)}
                placeholder="30"
                inputMode="numeric"
              />
            </Field>

            <Field label="Previous defaults" error={err('existing_defaults')}>
              <input
                value={values.existing_defaults}
                onChange={(e) => set('existing_defaults', e.target.value)}
                inputMode="numeric"
              />
            </Field>

            <label className="check">
              <input
                type="checkbox"
                checked={values.kyc_verified}
                onChange={(e) => set('kyc_verified', e.target.checked)}
              />
              <span>My identity has been verified at a branch (KYC)</span>
            </label>
          </fieldset>

          <details className="advanced">
            <summary>Reference number</summary>
            {/* Blank generates APP-###### server-side (§7.1). The presenter types
                APP-100244 here to pin the scripted fixture that scores 712. */}
            <Field
              label="Application reference"
              hint="Leave blank and one will be generated for you"
            >
              <input
                value={values.application_id}
                onChange={(e) => set('application_id', e.target.value)}
                placeholder="APP-100244"
              />
            </Field>
          </details>

          <button className="submit" type="submit" disabled={submitting}>
            {submitting ? 'Checking…' : 'Submit application'}
          </button>
        </form>
      </main>
    </>
  )
}

function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string
  hint?: string
  error?: string
  children: React.ReactNode
}) {
  return (
    <label className={`field${error ? ' invalid' : ''}`}>
      <span className="label">{label}</span>
      {children}
      {error ? <span className="error">{error}</span> : hint ? <span className="hint">{hint}</span> : null}
    </label>
  )
}

function Outcome({ result, onAnother }: { result: SubmitResult; onAnother: () => void }) {
  return (
    <>
      <header className="masthead">
        <div className="wordmark">
          <Logo size={38} />
          <span className="name">Kifaru Bank</span>
          <span className="sub">Personal Loans</span>
        </div>
      </header>

      <main>
        {(result.kind === 'created' || result.kind === 'duplicate') && (
          <DecisionCard result={result} />
        )}

        {result.kind === 'scoring-unavailable' && (
          <div className="outcome pending">
            <h1>We could not complete your application</h1>
            {/* §7.1: a scoring failure is never an approval AND never a decline.
                No decision was made, so we must not imply one. */}
            <p>
              Our credit assessment service is temporarily unavailable, so no decision has
              been made. Your application has been saved — please try again shortly.
            </p>
          </div>
        )}

        {result.kind === 'error' && (
          <div className="outcome error">
            <h1>Something went wrong</h1>
            <p>{result.message}</p>
          </div>
        )}

        <button className="link" onClick={onAnother}>
          ← Submit another application
        </button>
      </main>
    </>
  )
}

function DecisionCard({
  result,
}: {
  result: Extract<SubmitResult, { kind: 'created' | 'duplicate' }>
}) {
  const d = result.decision
  const approved = d.decision === 'APPROVED'

  return (
    <div className={`outcome ${approved ? 'approved' : 'declined'}`}>
      {result.kind === 'duplicate' && (
        // The demo's key beat: submit twice, and the second one changes nothing.
        <p className="duplicate-note">
          You have already submitted this application. Below is the original decision —
          nothing has been charged or paid out twice.
        </p>
      )}

      <h1>{approved ? 'Approved' : 'Not approved'}</h1>

      <p className="reference">
        Reference <strong>{d.application_id}</strong>
      </p>

      {approved ? (
        <p className="lede">
          KES {money(d.amount_kes)} over {d.term_months} months is on its way to your mobile
          wallet.
        </p>
      ) : (
        <p className="lede">
          We are unable to offer this loan at the moment.
        </p>
      )}

      <dl>
        <dt>Credit score</dt>
        <dd>{d.score ?? '—'}</dd>
        {d.decision_reason && (
          <>
            <dt>Assessment</dt>
            <dd>{d.decision_reason}</dd>
          </>
        )}
      </dl>

      {!approved && (
        <p className="footnote">
          Improving your income-to-repayment ratio, or applying for a smaller amount over a
          longer term, may change this outcome.
        </p>
      )}
    </div>
  )
}
