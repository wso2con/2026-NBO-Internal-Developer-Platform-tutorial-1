// App.tsx — the loan officer console (BUILD-SPEC.md §7.5).
//
// Three screens: applications list, application detail, arrears queue. No routing
// library — the state machine is one `view` value, which is all three screens
// need and keeps the whole app readable on a projector.

import { useCallback, useEffect, useState } from 'react'
import Logo from './Logo'
import {
  getApplication,
  listApplications,
  type Application,
} from './api'
import {
  BANK_ACTION,
  BUCKET_LABEL,
  BUCKET_ORDER,
  bucketRank,
  date,
  isOverdue,
  money,
} from './format'

type View = { screen: 'list' } | { screen: 'arrears' } | { screen: 'detail'; id: string }

export default function App() {
  const [view, setView] = useState<View>({ screen: 'list' })

  return (
    <>
      <header className="masthead">
        <div className="wordmark">
          <Logo size={38} />
          <span className="name">Kifaru Bank</span>
          <span className="sub">Loan Officer Console</span>
        </div>
        <nav>
          <button
            onClick={() => setView({ screen: 'list' })}
            aria-current={view.screen === 'list' ? 'page' : undefined}
          >
            Applications
          </button>
          <button
            onClick={() => setView({ screen: 'arrears' })}
            aria-current={view.screen === 'arrears' ? 'page' : undefined}
          >
            Arrears queue
          </button>
        </nav>
      </header>

      <main>
        {view.screen === 'list' && (
          <ApplicationsList onOpen={(id) => setView({ screen: 'detail', id })} />
        )}
        {view.screen === 'arrears' && (
          <ArrearsQueue onOpen={(id) => setView({ screen: 'detail', id })} />
        )}
        {view.screen === 'detail' && (
          <ApplicationDetail id={view.id} onBack={() => setView({ screen: 'list' })} />
        )}
      </main>
    </>
  )
}

// --- shared data hook --------------------------------------------------------

// Polls every `intervalMs`. §7.5 asks the applications list to auto-refresh every
// 5s so a loan submitted on stage appears without anyone touching the keyboard.
function usePolled<T>(load: (signal: AbortSignal) => Promise<T>, intervalMs: number | null) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null)

  const run = useCallback(
    async (signal: AbortSignal) => {
      try {
        const next = await load(signal)
        if (signal.aborted) return
        setData(next)
        setError(null)
        setUpdatedAt(new Date())
      } catch (e) {
        if (signal.aborted || (e instanceof DOMException && e.name === 'AbortError')) return
        setError(e instanceof Error ? e.message : String(e))
      }
    },
    [load],
  )

  useEffect(() => {
    const controller = new AbortController()
    void run(controller.signal)
    if (intervalMs === null) return () => controller.abort()

    const timer = setInterval(() => void run(controller.signal), intervalMs)
    return () => {
      clearInterval(timer)
      controller.abort()
    }
  }, [run, intervalMs])

  return { data, error, updatedAt }
}

// --- screen 1: applications list ---------------------------------------------

function ApplicationsList({ onOpen }: { onOpen: (id: string) => void }) {
  const load = useCallback((signal: AbortSignal) => listApplications(50, undefined, signal), [])
  const { data, error, updatedAt } = usePolled(load, 5000)

  return (
    <>
      <div className="row">
        <div>
          <h1>Applications</h1>
          <p className="subtitle">Newest first. Refreshes every 5 seconds.</p>
        </div>
        <div className="spacer" />
        {updatedAt && (
          <span className="refresh">Updated {updatedAt.toLocaleTimeString('en-GB')}</span>
        )}
      </div>

      {error && <p className="notice error">Could not reach the API: {error}</p>}
      {!data && !error && <p className="notice">Loading…</p>}

      {data && data.length === 0 && <p className="notice">No applications yet.</p>}

      {data && data.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Application</th>
              <th>Applicant</th>
              <th className="num">Amount (KES)</th>
              <th className="num">Term</th>
              <th className="num">Score</th>
              <th>Decision</th>
              <th>Arrears</th>
              <th>Submitted</th>
            </tr>
          </thead>
          <tbody>
            {data.map((a) => (
              <tr key={a.application_id} className="clickable" onClick={() => onOpen(a.application_id)}>
                <td className="mono">{a.application_id}</td>
                <td>{a.applicant_name}</td>
                <td className="num">{money(a.amount_kes)}</td>
                <td className="num">{a.term_months} mo</td>
                <td className="num">{a.score ?? '—'}</td>
                <td>{a.decision ? <span className={`badge ${a.decision}`}>{a.decision}</span> : '—'}</td>
                <td>
                  {a.arrears_bucket ? (
                    <span className={`badge ${a.arrears_bucket}`}>
                      {BUCKET_LABEL[a.arrears_bucket] ?? a.arrears_bucket}
                    </span>
                  ) : (
                    '—'
                  )}
                </td>
                <td>{date(a.submitted_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  )
}

// --- screen 2: application detail --------------------------------------------

function ApplicationDetail({ id, onBack }: { id: string; onBack: () => void }) {
  const load = useCallback((signal: AbortSignal) => getApplication(id, signal), [id])
  // Polled too, so a disbursement landing a second later appears on its own —
  // the worker acts asynchronously.
  const { data, error } = usePolled<Application>(load, 5000)

  if (error) return <p className="notice error">Could not load {id}: {error}</p>
  if (!data) return <p className="notice">Loading {id}…</p>

  const total = (data.repayments ?? []).reduce((sum, r) => sum + r.amount_due_kes, 0)
  const outstanding = (data.repayments ?? [])
    .filter((r) => !r.paid_date)
    .reduce((sum, r) => sum + r.amount_due_kes, 0)

  return (
    <>
      <button className="link" onClick={onBack}>
        ← Back to applications
      </button>

      <div className="row" style={{ marginTop: '1rem' }}>
        <div>
          <h1>
            {data.applicant_name} <span className="mono" style={{ color: 'var(--muted)' }}>{data.application_id}</span>
          </h1>
          <p className="subtitle">
            {data.decision && <span className={`badge ${data.decision}`}>{data.decision}</span>}
            {data.decision_reason && <> &nbsp;{data.decision_reason}</>}
          </p>
        </div>
      </div>

      <div className="panels">
        <div className="panel">
          <h3>Application</h3>
          <dl>
            <dt>Amount</dt><dd>{money(data.amount_kes)}</dd>
            <dt>Term</dt><dd>{data.term_months} months</dd>
            <dt>Monthly income</dt><dd>{money(data.monthly_income)}</dd>
            <dt>Employment</dt><dd>{data.employment_months} months</dd>
            <dt>KYC verified</dt><dd>{data.kyc_verified ? 'Yes' : 'No'}</dd>
            <dt>Prior defaults</dt><dd>{data.existing_defaults}</dd>
            <dt>Wallet</dt><dd className="mono">{data.wallet_msisdn}</dd>
            <dt>Submitted</dt><dd>{date(data.submitted_at)}</dd>
          </dl>
        </div>

        <div className="panel">
          <h3>Credit score</h3>
          {data.score_factors && data.score_factors.length > 0 ? (
            <>
              <div className="factors">
                {data.score_factors.map((f) => {
                  const width = Math.min(100, (Math.abs(f.points) / 300) * 100)
                  return (
                    <div className="factor" key={f.name}>
                      <span className="factor-name">{f.name}</span>
                      <span className="factor-bar">
                        <div
                          className={f.points < 0 ? 'negative' : undefined}
                          style={{ width: `${width}%` }}
                        />
                      </span>
                      <span className="factor-points">
                        {f.points > 0 ? '+' : ''}
                        {f.points}
                      </span>
                    </div>
                  )
                })}
              </div>
              <div className="score-total">
                <strong>{data.score}</strong>
                <span style={{ color: 'var(--muted)' }}>
                  threshold 650 · {data.decision}
                </span>
              </div>
            </>
          ) : (
            <p style={{ color: 'var(--muted)', margin: 0 }}>
              {data.score !== null
                ? `Score ${data.score}. Breakdown unavailable — the scoring service did not respond.`
                : 'Not yet scored.'}
            </p>
          )}
        </div>

        <div className="panel">
          <h3>Disbursement</h3>
          {data.disbursement ? (
            <dl>
              <dt>Status</dt><dd>{data.disbursement.status}</dd>
              <dt>Provider ref</dt><dd className="mono">{data.disbursement.provider_ref ?? '—'}</dd>
              <dt>Amount</dt><dd>{money(data.disbursement.amount_kes)}</dd>
              <dt>Sent to</dt><dd className="mono">{data.disbursement.wallet_msisdn}</dd>
              <dt>Disbursed</dt><dd>{date(data.disbursement.disbursed_at)}</dd>
            </dl>
          ) : (
            <p style={{ color: 'var(--muted)', margin: 0 }}>
              {data.decision === 'APPROVED'
                ? 'Approved — awaiting disbursement.'
                : 'Not disbursed.'}
            </p>
          )}
        </div>

        <div className="panel">
          <h3>Arrears</h3>
          {data.arrears_bucket ? (
            <dl>
              <dt>Bucket</dt>
              <dd><span className={`badge ${data.arrears_bucket}`}>{BUCKET_LABEL[data.arrears_bucket]}</span></dd>
              <dt>Days past due</dt><dd>{data.days_past_due ?? 0}</dd>
              <dt>Action</dt><dd>{BANK_ACTION[data.arrears_bucket]}</dd>
              <dt>Outstanding</dt><dd>{money(outstanding)}</dd>
            </dl>
          ) : (
            <p style={{ color: 'var(--muted)', margin: 0 }}>
              Not yet classified. The end-of-day job assigns arrears buckets.
            </p>
          )}
        </div>
      </div>

      {data.repayments && data.repayments.length > 0 && (
        <>
          <h2>
            Repayment schedule{' '}
            <span style={{ color: 'var(--muted)', fontWeight: 400, fontSize: '1rem' }}>
              {data.repayments.length} instalments · {money(total)} total · {money(outstanding)} outstanding
            </span>
          </h2>
          <table>
            <thead>
              <tr>
                <th className="num">#</th>
                <th>Due</th>
                <th className="num">Amount (KES)</th>
                <th>Paid</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {data.repayments.map((r) => {
                const overdue = isOverdue(r)
                return (
                  <tr
                    key={r.instalment_no}
                    className={overdue ? 'overdue' : r.paid_date ? 'paid' : undefined}
                  >
                    <td className="num">{r.instalment_no}</td>
                    <td>{date(r.due_date)}</td>
                    <td className="num">{money(r.amount_due_kes)}</td>
                    <td>{date(r.paid_date)}</td>
                    <td>{r.paid_date ? 'Paid' : overdue ? 'OVERDUE' : 'Scheduled'}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </>
      )}
    </>
  )
}

// --- screen 3: arrears queue --------------------------------------------------

function ArrearsQueue({ onOpen }: { onOpen: (id: string) => void }) {
  const load = useCallback((signal: AbortSignal) => listApplications(200, undefined, signal), [])
  const { data, error } = usePolled(load, 5000)

  if (error) return <p className="notice error">Could not reach the API: {error}</p>
  if (!data) return <p className="notice">Loading…</p>

  const classified = data.filter((a) => a.arrears_bucket)
  if (classified.length === 0) {
    return (
      <>
        <h1>Arrears queue</h1>
        <p className="notice">
          No loans classified yet. The end-of-day job populates this view.
        </p>
      </>
    )
  }

  // Grouped by bucket, worst first (§7.5).
  const groups = BUCKET_ORDER.map((bucket) => ({
    bucket,
    loans: classified
      .filter((a) => a.arrears_bucket === bucket)
      .sort((a, b) => bucketRank(a.arrears_bucket) - bucketRank(b.arrears_bucket)),
  })).filter((g) => g.loans.length > 0)

  return (
    <>
      <h1>Arrears queue</h1>
      <p className="subtitle">Grouped by bucket, worst first. Refreshes every 5 seconds.</p>

      {groups.map(({ bucket, loans }) => (
        <section className="bucket-group" key={bucket}>
          <div className="bucket-head">
            <h2>
              <span className={`badge ${bucket}`}>{BUCKET_LABEL[bucket]}</span>
            </h2>
            <span className="action">{BANK_ACTION[bucket]}</span>
            <span className="exposure">
              {loans.length} {loans.length === 1 ? 'loan' : 'loans'}
            </span>
          </div>
          <table>
            <thead>
              <tr>
                <th>Application</th>
                <th>Applicant</th>
                <th className="num">Amount (KES)</th>
                <th className="num">Score</th>
                <th>Submitted</th>
              </tr>
            </thead>
            <tbody>
              {loans.map((a) => (
                <tr key={a.application_id} className="clickable" onClick={() => onOpen(a.application_id)}>
                  <td className="mono">{a.application_id}</td>
                  <td>{a.applicant_name}</td>
                  <td className="num">{money(a.amount_kes)}</td>
                  <td className="num">{a.score ?? '—'}</td>
                  <td>{date(a.submitted_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ))}
    </>
  )
}
