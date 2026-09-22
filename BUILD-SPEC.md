# BUILD-SPEC.md — Kifaru Bank retail lending demo

**Read this file fully before writing any code.** It is the complete specification for a
conference demo application. Build it in the phase order given in §12, and stop at each
checkpoint so the work can be verified.

---

## 1. Mission

Build five services plus local infrastructure for a fictional Kenyan bank's retail loan
system. This runs as a live demo at a conference in front of a banking audience. It will
later be deployed onto **OpenChoreo**, an internal developer platform running on
Kubernetes, but **Phase 1 (this work) targets local Docker Compose only**.

The demo narrative is: a customer applies for a loan → the bank scores it → approved loans
are disbursed to a mobile wallet → a nightly batch job classifies loans that are falling
behind on repayments.

### What success looks like

A single command brings up everything locally. A second command runs four scripted
scenarios end to end and prints pass/fail for each. Those four scenarios are the demo, and
they are specified exactly in §10.

---

## 2. Non-negotiable constraints

These come from the target platform. Violating any of them breaks the demo later, in ways
that are expensive to fix on the day.

1. **All configuration comes from environment variables.** No config files, no flags, no
   defaults that point at `localhost` in production code paths. OpenChoreo injects
   connection details as env vars at pod start; anything else is unreachable. For the
   Ballerina services this means `os:getEnv()` and no `Config.toml` — see §3.2.

2. **No hardcoded hostnames or ports for dependencies, ever.** `credit-scoring`'s address
   arrives in an env var. The database URL arrives in an env var. If you find yourself
   typing `http://credit-scoring:8081` outside of a compose file or a test fixture, stop.

3. **Containers must run as a non-root user with a read-only root filesystem.** This is not
   optional and it is not a Phase 2 concern. Act 4 of the demo adds `runAsNonRoot`,
   `readOnlyRootFilesystem` and `drop: [ALL]` capabilities to the platform template, live
   on stage, and every service must keep running. Build the images this way from the first
   commit and verify it (§11). Write nothing to disk at runtime except `/tmp`, and mount
   `/tmp` as emptyDir-equivalent (`tmpfs` in compose).

4. **Listen on `0.0.0.0`, on the port given by `$PORT`.** Default `8080` if unset.

5. **Structured JSON logs to stdout only.** No log files. Every log line that relates to a
   loan must carry an `application_id` field — the demo filters portal logs by it.

6. **Propagate W3C trace context.** Accept `traceparent` on inbound HTTP, forward it on
   outbound HTTP, and carry it in NATS message headers. The demo shows a distributed trace
   crossing gateway → api → scoring → broker → worker. Without propagation that trace is
   four disconnected fragments.

7. **No secrets in images or in Git.** Credentials arrive as env vars.

8. **Graceful shutdown on SIGTERM**, 10-second drain. Kubernetes will send it constantly
   during the promotion demo.

---

## 3. Technology

### 3.1 Language per service — this split is deliberate

| Service | Language | Why |
|---|---|---|
| `loan-api` | **Ballerina** | Request/response integration service: HTTP in, HTTP out to scoring, SQL, event publish. Ballerina's service syntax reads well on a projector, and this is the one built live on stage |
| `credit-scoring` | **Ballerina** | Pure HTTP, no dependencies. Small enough that the presenter can show the whole service on one screen |
| `disbursement-worker` | **Go** | Long-running JetStream consumer with Valkey and Postgres. Mature clients, low memory, fast restarts |
| `arrears-eod` | **Go** | Batch job that must start fast and exit. JVM startup is dead weight for a CronJob |
| `payment-rail-stub` | **Go** | Not a demo component. Trivial, keep it that way |
| `loan-officer-console` | **React 18 + Vite + TypeScript** | |

The rule, if anyone asks on stage: **Ballerina for the customer-facing integration services,
Go for the background runtime.** That split is also what makes the platform's build story
real — two languages, two different build workflows, and the ComponentType's
`allowedWorkflows` deciding which is legal for which component type. A single-language demo
quietly undersells that.

### 3.2 Ballerina services

| Concern | Choice |
|---|---|
| Runtime | Ballerina Swan Lake, current stable |
| HTTP | `ballerina/http` |
| Postgres | `ballerinax/postgresql` + `ballerina/sql` |
| NATS | `ballerinax/nats` |
| Logging | `ballerina/log` configured for JSON output |
| Observability | Ballerina's built-in OpenTelemetry tracing and Prometheus metrics — enable via the observability flags, do not add libraries |
| Tests | `bal test` with the golden fixtures from §6 |

**Configuration — env vars only, no `Config.toml`. This is decided, not open.**

Read every setting with `os:getEnv()`, using the exact variable names given in §7. Do not
use `configurable` variables and do not commit a `Config.toml`. The reason is the promotion
demo: one immutable image is promoted through three environments, each with different
addresses, so configuration cannot live in a file inside the image.

Give each Ballerina service a small `config.bal` that reads and validates everything **once
at startup**, before the listener starts:

- Required variable missing or empty → log a single clear line naming the variable, then
  exit non-zero. Fail loudly at startup, never silently at first request.
- Optional variables get a default in code (`SCORING_TIMEOUT_MS` → 2000).
- Numeric and boolean values are parsed and range-checked here, not at the point of use.
- Expose the resolved config (with secrets redacted) in the startup log line, so the
  presenter can prove on stage which database the pod is actually pointing at.

**Do not rename an environment variable to suit the language.** Those names are the contract
with the platform's dependency injection — the platform picks them, the service adapts.

### 3.3 Go services

| Concern | Choice |
|---|---|
| Version | Go 1.23+ |
| HTTP | stdlib `net/http` with `http.ServeMux`, no framework |
| Postgres | `jackc/pgx/v5` (`pgxpool`) |
| NATS | `nats-io/nats.go`, JetStream for durable consumption |
| Valkey | `redis/go-redis/v9` — Valkey is Redis-protocol compatible |
| Tracing | `go.opentelemetry.io/otel` + OTLP HTTP exporter, gated on `OTEL_EXPORTER_OTLP_ENDPOINT` being set, no-op when unset |
| Metrics | `prometheus/client_golang` |
| Tests | stdlib `testing`, plus `testcontainers-go` where a real Postgres is needed |

### 3.4 Shared

Migrations are plain `.sql` files applied by a compose service. Do not pull in a migration
framework. Both languages must produce the same JSON log shape (§2.5) and honour the same
`traceparent` propagation (§2.6) — a trace that breaks at the language boundary is worse
than no trace, because the presenter will be standing in front of it.

---

## 4. Repository layout

```
kifaru-lending-demo/
├── BUILD-SPEC.md              # this file
├── README.md                  # written last, see §13
├── Makefile
├── docker-compose.yml
├── .env.example
├── app/
│   ├── loan-api/              # Ballerina package
│   ├── credit-scoring/        # Ballerina package
│   ├── disbursement-worker/   # Go module
│   ├── arrears-eod/           # Go module
│   ├── payment-rail-stub/     # Go module — fake external payment provider
│   └── loan-officer-console/  # React + Vite
├── db/
│   ├── migrations/001_init.sql
│   └── seed/seed.sql
├── demo/
│   ├── local-smoke.sh         # runs all four scenarios, §10
│   ├── payloads/
│   │   ├── APP-100244.json
│   │   └── APP-100245.json
│   └── RUNBOOK.md             # written last, see §13
└── platform/                  # EMPTY in Phase 1. Do not create OpenChoreo YAML.
```

Each service is independently buildable: its own `go.mod` or `Ballerina.toml`, its own
`Dockerfile`, no build step that reaches outside its own directory. **Do not create a shared
library module in either language.** A small amount of duplication across five services is
fine, and it is how the platform will build them — one component, one repository path, one
workflow.

---

## 5. Data model

`db/migrations/001_init.sql`:

```sql
CREATE TABLE loan_applications (
    application_id   TEXT PRIMARY KEY,
    applicant_name   TEXT        NOT NULL,
    national_id      TEXT        NOT NULL,
    wallet_msisdn    TEXT        NOT NULL,
    amount_kes       NUMERIC(12,2) NOT NULL,
    term_months      INT         NOT NULL,
    monthly_income   NUMERIC(12,2) NOT NULL,
    employment_months INT        NOT NULL,
    kyc_verified     BOOLEAN     NOT NULL DEFAULT FALSE,
    existing_defaults INT        NOT NULL DEFAULT 0,
    score            INT,
    decision         TEXT,          -- PENDING | APPROVED | DECLINED
    decision_reason  TEXT,
    submitted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at       TIMESTAMPTZ
);

CREATE TABLE disbursements (
    disbursement_id  TEXT PRIMARY KEY,
    application_id   TEXT NOT NULL REFERENCES loan_applications(application_id),
    amount_kes       NUMERIC(12,2) NOT NULL,
    wallet_msisdn    TEXT NOT NULL,
    provider_ref     TEXT,
    status           TEXT NOT NULL,  -- SENT | FAILED
    disbursed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE repayments (
    repayment_id     BIGSERIAL PRIMARY KEY,
    application_id   TEXT NOT NULL REFERENCES loan_applications(application_id),
    instalment_no    INT NOT NULL,
    due_date         DATE NOT NULL,
    amount_due_kes   NUMERIC(12,2) NOT NULL,
    paid_date        DATE,
    UNIQUE (application_id, instalment_no)
);

CREATE TABLE arrears_classification (
    application_id   TEXT PRIMARY KEY REFERENCES loan_applications(application_id),
    days_past_due    INT  NOT NULL,
    bucket           TEXT NOT NULL,   -- CURRENT | DPD_1_30 | DPD_31_60 | DPD_61_90 | NPL_90_PLUS
    outstanding_kes  NUMERIC(12,2) NOT NULL,
    classified_at    TIMESTAMPTZ NOT NULL
);
```

### Repayment schedule

Flat interest at **18% per annum**. Total repayable = `amount × (1 + 0.18 × term_months/12)`,
divided evenly across `term_months` instalments, first due one month after disbursement.
Round to 2dp; put any rounding remainder on the final instalment. Generate the schedule when
a disbursement succeeds.

### Seed data — `db/seed/seed.sql`

Four historical loans, already approved, already disbursed, with partial repayment history.
These are the collections queue; `APP-100244` and `APP-100245` are **not** seeded — they are
created at runtime by scenarios 1 and 3.

| Application | Applicant | Amount | Term | Target DPD | Expected bucket |
|---|---|---|---|---|---|
| `APP-100301` | Joseph M. | 150,000 | 24 | 12 | `DPD_1_30` |
| `APP-100302` | Grace N. | 250,000 | 24 | 47 | `DPD_31_60` |
| `APP-100303` | Peter O. | 120,000 | 12 | 78 | `DPD_61_90` |
| `APP-100304` | Halima S. | 480,000 | 36 | 124 | `NPL_90_PLUS` |

**Due dates must be relative to seed time, never absolute literals.** Compute them in SQL as
`CURRENT_DATE - INTERVAL 'N days'` so the classification comes out identical whichever day
the demo runs. A hardcoded `'2026-09-11'` in the seed file produces a table that is correct
today, wrong at the conference, and wrong in a way nobody notices until it is on a projector.

For each seeded loan: generate the full schedule per the rule above, mark instalments paid up
to the point that leaves the **oldest unpaid instalment** exactly the target DPD before
today, and leave the rest unpaid. Outstanding = sum of unpaid instalments.

This gives one loan in each of the four overdue buckets, and `APP-100244` — created during
the run — supplies `CURRENT`. Five loans, five buckets, no date drift.

---

## 6. Scoring rules

`credit-scoring` is deterministic and stateless. No database, no randomness, no clock
dependence. Same input always gives the same score.

```
score = 300 (base)
      + incomePoints        = min(200, floor(monthly_income / 500))
      + employmentPoints    = min(60,  employment_months)
      + affordabilityPoints  (see below)
      + kycPoints           = +40 if kyc_verified else -80
      - 150 × existing_defaults

clamp to [300, 850]
```

Affordability is based on debt-to-income: `monthly_instalment / monthly_income`, where the
instalment uses the same flat-interest formula as §5.

| DTI | Points |
|---|---|
| < 0.20 | +150 |
| 0.20 – 0.35 | +100 |
| 0.35 – 0.50 | +40 |
| ≥ 0.50 | −100 |

**Decision threshold: score ≥ 650 → APPROVED, otherwise DECLINED.**

### Golden test — this must pass exactly

The demo narrative depends on these two numbers. Write a unit test asserting them, and if
your implementation produces anything else, fix the implementation rather than the test.

| Fixture | Inputs | Expected |
|---|---|---|
| **APP-100244** | income 96,000 · employment 30mo · amount 250,000 · term 24 · KYC true · defaults 0 | instalment 14,166.67 · DTI 0.148 · **score 712 · APPROVED** |
| **APP-100245** | income 38,000 · employment 24mo · amount 300,000 · term 24 · KYC true · defaults 0 | instalment 17,000.00 · DTI 0.447 · **score 480 · DECLINED** |

Working: 300 + 192 + 30 + 150 + 40 = **712** · 300 + 76 + 24 + 40 + 40 = **480**.

---

## 7. Service specifications

### 7.1 `loan-api` — the front door · **Ballerina**

Public HTTP service. The only component built from source during the live demo, via the
platform's Ballerina build workflow.

Keep the service definition compact and readable — it goes on a projector at T+0:35 and the
presenter reads what is *absent* from it. Resist the urge to spread it across many files:
one service file, one client module for scoring, one for persistence, one for events.

**Environment variables**

| Var | Example | Meaning |
|---|---|---|
| `PORT` | `8080` | |
| `DATABASE_URL` | `postgres://...` | Injected from the platform's Postgres resource |
| `NATS_URL` | `nats://...` | Injected from the NATS resource |
| `CREDIT_SCORING_URL` | `http://credit-scoring:8081` | Injected from the endpoint dependency |
| `SCORING_TIMEOUT_MS` | `2000` | |
| `LOG_LEVEL` | `info` | |

**Endpoints**

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/applications` | Submit an application |
| `GET` | `/applications/{id}` | Fetch one, with score, decision, disbursement and arrears bucket |
| `GET` | `/applications` | List, newest first, `?limit=50`, `?bucket=` filter |
| `GET` | `/healthz` | Liveness — process is up. No dependency checks |
| `GET` | `/readyz` | Readiness — DB and NATS reachable |
| `GET` | `/metrics` | Prometheus |

**`POST /applications`**

Request:
```json
{
  "application_id": "APP-100244",
  "applicant_name": "Amina W.",
  "national_id": "12345678",
  "wallet_msisdn": "254712345678",
  "amount_kes": 250000,
  "term_months": 24,
  "monthly_income": 96000,
  "employment_months": 30,
  "kyc_verified": true,
  "existing_defaults": 0
}
```

`application_id` is supplied by the caller. This is deliberate — it makes the demo
repeatable and it is how the idempotency story hangs together. If omitted, generate
`APP-` + 6 digits.

Flow: validate → insert as `PENDING` → call `credit-scoring` → persist score and decision →
if `APPROVED`, publish `loan.approved` to NATS → return 201 with the full record.

If the same `application_id` is submitted again, return **200** with the existing record and
do not re-score, do not re-publish. Log `duplicate submission ignored`.

If `credit-scoring` is unreachable or times out, return **503**, leave the record `PENDING`,
and publish nothing. Never approve on a scoring failure — say this in a code comment,
because a banking audience will ask.

**Validation:** amount 10,000–2,000,000 KES; term ∈ {6,12,24,36}; income > 0; msisdn matches
`^254[17]\d{8}$`. Return 400 with a field-level error list.

---

### 7.2 `credit-scoring` — the risk brain · **Ballerina**

Internal-only HTTP service. **It has no public route in production and never will.** Do not
add auth, a UI, or anything that implies external access.

**Env:** `PORT` (default `8081`), `LOG_LEVEL`.
No database. No outbound calls. Stateless.

**`POST /score`**

Request: `{ "application_id", "amount_kes", "term_months", "monthly_income", "employment_months", "kyc_verified", "existing_defaults" }`

Response `200`:
```json
{
  "application_id": "APP-100244",
  "score": 712,
  "decision": "APPROVED",
  "reason": "Affordability strong (DTI 0.15); employment history 30 months; KYC verified",
  "factors": [
    { "name": "base",          "points": 300 },
    { "name": "income",        "points": 192 },
    { "name": "employment",    "points": 30  },
    { "name": "affordability", "points": 150 },
    { "name": "kyc",           "points": 40  }
  ]
}
```

The `factors` breakdown matters: it lets the presenter explain *why* 480 was declined
without opening the code. Also serve `GET /rules` returning the current thresholds as JSON —
a nice "the rules are inspectable" beat if there's time.

Endpoints: `/score`, `/rules`, `/healthz`, `/readyz`, `/metrics`.

---

### 7.3 `disbursement-worker` — the money mover · **Go**

**No HTTP server on the main port.** It is a consumer. Expose `/healthz`, `/readyz` and
`/metrics` on a separate admin port (`ADMIN_PORT`, default `9090`) — the platform needs
probes, but the component declares no service endpoint.

**Environment variables**

| Var | Meaning |
|---|---|
| `NATS_URL` | Injected |
| `VALKEY_URL` | Injected |
| `DATABASE_URL` | Injected |
| `PAYMENT_RAIL_URL` | The external provider. Leaves the cell through the egress gateway |
| `PAYMENT_RAIL_API_KEY` | Injected from a secret |
| `IDEMPOTENCY_TTL_HOURS` | Default `168` (7 days) |
| `ADMIN_PORT` | Default `9090` |

**Flow, in this exact order:**

1. Consume `loan.approved` from NATS (JetStream, durable consumer named `disbursement`).
2. `SET key=disb:{application_id} value={worker_instance} NX EX={ttl}` in Valkey.
   - **If the key already exists → log `duplicate disbursement suppressed`, increment
     `disbursements_suppressed_total`, ack the message, stop.** This is the single most
     important behaviour in the entire demo. Do not reorder it, do not make it best-effort,
     do not put it after the payment call.
3. `POST` to the payment rail with an `Idempotency-Key` header set to the application id.
4. Insert a row into `disbursements`; generate the repayment schedule (§5).
5. Publish `loan.disbursed`.
6. Ack.

If the payment call fails: delete the Valkey key, nak the message with a 30-second backoff,
and let it retry. Give up after 5 attempts, record `status=FAILED`, and publish
`loan.disbursement_failed`.

**Log these three lines verbatim-ish, because the presenter will point at them:**
```
disbursement claimed      application_id=APP-100244
duplicate disbursement suppressed  application_id=APP-100244 held_by=worker-1
payment sent              application_id=APP-100244 provider_ref=PR-... amount_kes=250000
```

---

### 7.4 `arrears-eod` — the night shift · **Go**

A batch job. Runs to completion and exits. Exit code 0 on success, non-zero on failure.
**It must not loop or sleep waiting for work** — the platform schedules it as a CronJob.

**Env:** `DATABASE_URL`, `AS_OF_DATE` (optional `YYYY-MM-DD`, defaults to today), `DRY_RUN`
(default false). `AS_OF_DATE` exists for two reasons: reproducing a past EOD run when
investigating, and letting the presenter fast-forward the clock on stage to show a loan
moving between buckets. The smoke test leaves it unset — see §10.6.

**Logic:** for each loan with `decision = 'APPROVED'` and at least one disbursement, find the
oldest unpaid instalment with `due_date < AS_OF_DATE`. Days past due = `AS_OF_DATE − due_date`,
or 0 if nothing is overdue. Classify:

| DPD | Bucket | Bank action |
|---|---|---|
| 0 | `CURRENT` | none |
| 1–30 | `DPD_1_30` | SMS reminder |
| 31–60 | `DPD_31_60` | collections call |
| 61–90 | `DPD_61_90` | demand letter |
| 91+ | `NPL_90_PLUS` | non-performing, provision and report |

Upsert into `arrears_classification`. Then print a summary table to stdout — the presenter
shows this on screen, so make it readable. **Ordered worst bucket first**, because that is
the collections team's priority order:

```
=== Kifaru Bank — End of Day Arrears Classification ===
As of: 2026-09-11        Loans assessed: 5

APPLICATION    APPLICANT        OUTSTANDING     DPD   BUCKET
APP-100304     Halima S.           616,000.02    124   NPL_90_PLUS
APP-100303     Peter O.            106,200.00     78   DPD_61_90
APP-100302     Grace N.            269,166.65     47   DPD_31_60
APP-100301     Joseph M.           136,000.00     12   DPD_1_30
APP-100244     Amina W.            340,000.00      0   CURRENT

Non-performing exposure: KES 616,000.02 (1 loan)
Completed in 340ms
```

The outstanding figures above are what the §5 seed and the flat-interest rule
actually produce — each is the sum of that loan's unpaid instalments, so it is
necessarily a whole number of instalments:

| Loan | Instalment | Unpaid | Outstanding |
|---|---|---|---|
| `APP-100301` | 8,500.00 | 16 | 136,000.00 |
| `APP-100302` | 14,166.67 | 19 | 269,166.65 |
| `APP-100303` | 11,800.00 | 9 | 106,200.00 |
| `APP-100304` | 20,533.33 | 30 | 616,000.02 |
| `APP-100244` | 14,166.67 | 24 | 340,000.00 |

`APP-100302` and `APP-100304` end in odd cents because their totals do not divide
evenly by the term: 340,000 / 24 and 739,200 / 36 both recur, and §5 puts the
rounding remainder on the final instalment. That is correct behaviour, not drift.
If round figures on the projector matter more than the amounts in §5's seed table,
changing those two loans to 252,000 and 486,000 makes every instalment exact.

**Why APP-100244 is CURRENT and not overdue.** Scenario 1 creates and disburses it during
the run, and §5 puts the first instalment one month after disbursement, so it cannot be past
due on the day. That is correct behaviour and it reads better on stage than a fudge: the
loan the presenter created twenty minutes ago is current, and the four seeded loans are the
collections queue. One loan per bucket, five rows, the whole classification scheme visible
at once.

Log each classification as JSON to stdout as well, so the portal can show it — the table is
a human-readable extra, not a replacement.

---

### 7.5 `loan-officer-console` — the staff screen · **React**

React + Vite SPA. Three screens, no routing library needed if you keep it simple:

- **Applications list** — table from `GET /applications`, with score, decision and bucket.
  Auto-refresh every 5s.
- **Application detail** — the record, the scoring factor breakdown, the disbursement, and
  the repayment schedule with overdue instalments highlighted.
- **Arrears queue** — grouped by bucket, worst first. This is the collections team's view.

**Config at runtime, not build time.** The API base URL must come from a `/config.js` file
served by the container and populated from `$API_BASE_URL` at startup, because the same
image gets promoted to three environments with three different URLs. Do not bake
`VITE_API_URL` into the bundle.

Serve with nginx-unprivileged or a small Go static file server — it must run as non-root
with a read-only root filesystem, which rules out stock nginx without work.

Visual style: restrained, bank-ish. Navy and white, no gradients, generous whitespace,
a "Kifaru Bank" wordmark top left. It appears on a projector in a large room, so default
font sizes need bumping.

---

### 7.6 `payment-rail-stub` — the fake external provider · **Go**

Not a demo component; it stands in for a mobile money API. Keep it trivial.

`POST /v1/payments` with `Idempotency-Key` header → `201 { "provider_ref": "PR-<uuid>", "status": "SENT" }`.
Same idempotency key twice → `200` with the *same* `provider_ref` and a `"duplicate": true`
flag. Add `FAIL_RATE` (default 0) and `LATENCY_MS` (default 150) env vars so failure paths
can be exercised.

---

## 8. Events

Subject prefix `loan.`, JSON payloads. Every event carries `traceparent` in the NATS header.

| Subject | Published by | Payload |
|---|---|---|
| `loan.submitted` | loan-api | `{ application_id, amount_kes, term_months, submitted_at }` |
| `loan.approved` | loan-api | `{ application_id, applicant_name, amount_kes, term_months, wallet_msisdn, score, approved_at }` |
| `loan.disbursed` | worker | `{ application_id, disbursement_id, provider_ref, amount_kes, disbursed_at }` |
| `loan.disbursement_failed` | worker | `{ application_id, reason, attempts, failed_at }` |

Use a JetStream stream named `LOANS` over `loan.>`, file storage, 24-hour retention. The
worker uses a durable pull consumer so a restart doesn't lose messages — which is the point
of using a broker at all, and worth a code comment.

---

## 9. Local environment

### 9.1 Runtime: Colima, not Docker Desktop

The development machine runs **Colima** on macOS, almost certainly Apple Silicon, and the
same Colima VM will host the local OpenChoreo k3d cluster in Phase 2. Assume nothing about
Docker Desktop.

**VM sizing.** Phase 1 needs roughly 4 CPU / 8GB. Phase 2 adds an OpenChoreo control plane,
data plane, workflow plane, observability plane and a Backstage portal on top, so size the
VM once for the larger case rather than recreating it later:

```bash
colima start \
  --cpu 6 --memory 16 --disk 100 \
  --vm-type vz --vz-rosetta \
  --mount-type virtiofs
```

`--vz-rosetta` gives fast amd64 emulation for any image that turns out to be x86-only.
`virtiofs` makes bind mounts substantially faster, which matters for the Vite dev server and
the Ballerina build cache.

**Architecture.** Everything builds and runs as `linux/arm64` locally. Postgres, NATS,
Valkey and Temurin are all multi-arch and fine. **Verify the Ballerina base image before
Phase 2 begins:**

```bash
docker manifest inspect ballerina/ballerina:<tag> | grep -i arm64
```

The Ballerina base Dockerfile branches on architecture and pulls an aarch64 JDK, so arm64
support is expected — but confirm it rather than assume. If the published manifest lacks
arm64, the builder stage runs under Rosetta: it works, it is slow, and it needs to be known
on day one, not mid-build.

**Multi-arch output is still required.** The conference cluster may be x86. Do not rely on
whatever the laptop happens to produce:

```bash
docker buildx build --platform linux/amd64,linux/arm64 --push ...
```

Add `make images-multiarch` for this. Local development can build single-arch for speed;
anything pushed to a registry must carry both.

**Practical Colima notes.** Published ports reach `127.0.0.1` on the host as expected.
`host.docker.internal` resolves on current Colima versions, but do not depend on it — the
compose network is enough for everything in this spec. If `docker` commands fail after a
reboot, the VM is stopped, not broken.

### 9.2 Compose

`docker-compose.yml` brings up: `postgres:16`, `nats:2.10` (with `-js`), `valkey/valkey:8`,
`payment-rail-stub`, then the five services. Use healthchecks and `depends_on:
condition: service_healthy` — do not use `sleep` to sequence startup.

Use `docker compose` (v2, plugin syntax) throughout. No `docker-compose` v1, no `version:`
key at the top of the file.

Ports on the host: loan-api `8080`, credit-scoring `8081`, console `3000`, payment rail
`8082`, worker admin `9090`, postgres `5432`, nats `4222`, valkey `6379`.

Include an optional `observability` compose profile with OTLP collector + Jaeger, off by
default, so traces can be checked locally without slowing down the normal loop.

### 9.3 Makefile

These targets are the interface. Keep them working.

```
make up          # build and start everything, wait for healthy
make down        # stop and remove volumes
make seed        # apply migrations and seed data
make smoke       # run demo/local-smoke.sh
make test        # bal test + go test ./... in every module + vitest
make logs        # follow all service logs
make reset       # down, up, seed — a clean slate between dry runs
make verify-hardened   # §11: run every image read-only, non-root, no capabilities
make images-multiarch  # buildx both platforms and push
make doctor      # check Colima is running and has enough CPU/memory/disk
```

`make doctor` is worth the ten lines: the most common failure on this setup is a VM that is
stopped or undersized, and the symptom is an unrelated-looking build error. Have it print
the VM's CPU, memory and free disk, and warn below 4 CPU / 8GB / 20GB free.

`make reset && make smoke` must go from nothing to all-green in under two minutes. Time it
and record the number in the README — if it creeps past five minutes you will stop running
dry runs, which is how demos break.

---

## 10. The four scenarios

These are the demo. **Write two separate things** — they have different audiences and
trying to make one script serve both produces something that is neither.

| | `demo/local-smoke.sh` | `demo/RUNBOOK.md` |
|---|---|---|
| For | you, during development | the presenters, on stage |
| Shape | bash, asserts everything, exits non-zero on any failure | plain commands a human types, in order, with expected output beside each |
| Style | poll loops, `jq` pipelines, terse PASS/FAIL lines | one readable `curl` per beat, no loops, no pipelines |
| Written in | Phase 3 onward, grown each phase | Phase 7 (§13) |

Build the smoke script now. The runbook comes last, once the behaviour is settled.

### 10.1 Tooling

`bash` + `curl` + `jq` + `psql`, plus one `docker compose run` for the batch job. No test
framework, no Python. Keep it to tools that are already on the presenter's laptop.

### 10.2 The polling helper — required

Disbursement is asynchronous. `loan-api` returns 201 immediately and the worker acts a
moment later. A script that checks the database right after the `curl` will pass while the
work is still in flight, which makes the most important assertion in the demo unreliable in
exactly the direction that hides a bug.

So: **no bare `sleep` calls anywhere in the script.** Write two helpers and use them for
every database assertion.

```bash
# wait_for_count <sql> <expected> <timeout_seconds>
#   Polls every 250ms until the query returns <expected>, or fails at timeout.
#   Use for POSITIVE assertions — a row that should appear.

# assert_count_stays <sql> <expected> <seconds>
#   Asserts the query returns <expected> continuously for the whole window.
#   Use for NEGATIVE assertions — a row that must never appear.
```

The distinction matters. `wait_for_count` failing means the system is broken or slow.
`assert_count_stays` failing means the system did something it shouldn't have — and it
catches the case where a duplicate disbursement arrives a second after you checked. A
negative assertion that passes because the event hadn't arrived yet is worse than no test
at all.

### 10.3 Scenario 1 — approval and disbursement

Submit `APP-100244`. Assert from the HTTP response: **201**, score **712**, decision
`APPROVED`.

Then `wait_for_count` (timeout 10s) for: exactly one row in `disbursements` for that
application with a non-null `provider_ref`, and exactly 24 rows in `repayments`.

### 10.4 Scenario 2 — duplicate submission · the critical one

Submit `APP-100244` again, byte-identical. Assert from the response: **200**, score still
**712**, and no new `decided_at` timestamp.

Then the assertion the whole demo rests on: `assert_count_stays` for **5 seconds** that
`disbursements` for `APP-100244` remains at **exactly 1**. A 5-second window rather than a
snapshot, because a broken worker disburses late, not instantly.

Also assert one of: the worker logged `duplicate disbursement suppressed`, or
`disbursements_suppressed_total` on the worker's admin port incremented by 1. Prefer the
metric — log-scraping is brittle.

### 10.5 Scenario 3 — decline

Submit `APP-100245`. Assert: **201**, score **480**, decision `DECLINED`.

Then `assert_count_stays` for **3 seconds** that `disbursements` for `APP-100245` is
**0**. Also assert no `loan.approved` event exists for it — either by subscribing before
submitting, or by checking the JetStream stream, whichever is simpler to do reliably.

### 10.6 Scenario 4 — arrears classification

Run `arrears-eod` with `AS_OF_DATE` unset, so it uses today — which is the same day the
seed ran, and §5's relative due dates make that deterministic. Do **not** pin an absolute
date here; pinning only works if the seed dates are absolute too, and they are not.

Assert: exit code 0, five rows in `arrears_classification`, and specifically —

| Application | Expected bucket |
|---|---|
| `APP-100301` | `DPD_1_30` |
| `APP-100302` | `DPD_31_60` |
| `APP-100303` | `DPD_61_90` |
| `APP-100304` | `NPL_90_PLUS` |
| `APP-100244` | `CURRENT` — created and disbursed during this run, so not yet due |

Also assert exactly one loan in `NPL_90_PLUS` and that the non-performing exposure total is
printed. Print the formatted table.

This scenario depends on scenario 1 having run first, since `APP-100244` does not exist
otherwise. Assert it is present rather than silently accepting four rows.

The job is synchronous — it runs to completion — so no polling is needed.

### 10.7 Scenario 5 — isolation check

Can't be fully proven locally, but script it now so it's ready for the cluster. Assert
`credit-scoring` responds when called from inside the compose network. Add a comment
recording that on OpenChoreo the equivalent call from outside the cell must be **refused**,
and that this is the Act 2 beat at T+0:49.

### 10.8 Output format

One line per scenario, so a failure is obvious at a glance:

```
[1/5] approval and disbursement ......... PASS   (score 712, 1 disbursement, 24 instalments)
[2/5] duplicate submission .............. PASS   (still 1 disbursement after 5s)
[3/5] decline ........................... PASS   (score 480, 0 disbursements after 3s)
[4/5] arrears classification ............ PASS   (5 loans, 1 NPL)
[5/5] isolation check ................... PASS   (scoring reachable in-network)
```

On failure, print the actual versus expected value and the failing query. Do not stop at
the first failure — run all five, then exit non-zero. Knowing that three of five broke is
more useful than knowing the first one did.

---

## 11. Container requirements

Every Dockerfile is multi-stage. Applies to both runtimes:

- `USER 10001:10001` — a numeric UID, not a name. Kubernetes `runAsNonRoot` cannot verify a
  username.
- No `chown` at runtime, no writes outside `/tmp`.
- `HEALTHCHECK` in the Dockerfile for compose.
- Build for `linux/amd64` **and** `linux/arm64` — the laptop is arm64, the conference
  cluster may not be. See §9.1 for the buildx invocation.

**Go services:** distroless or `scratch` final stage, `alpine` only if you genuinely need a
shell. Under 40MB.

**Ballerina services:** build the jar in a Ballerina builder stage, then run it on a slim JRE
base — do not ship the full Ballerina distribution in the runtime image. Expect roughly
200–300MB; that is acceptable and not worth fighting. Two things need care under a read-only
root filesystem: the JVM wants a writable temp directory, so point `java.io.tmpdir` at
`/tmp` and mount it as tmpfs, and set `-XX:+UseContainerSupport` with a sensible
`-XX:MaxRAMPercentage` so the JVM sizes itself to the container limit rather than the node.
Get this right now — a JVM that only misbehaves under a memory limit will misbehave for the
first time on the conference cluster.

Because the Ballerina images are larger and slower to pull, **pre-pull them onto the k3d node
before the session.** The `loan-api` build is already on the critical path at T+0:26; a
cold 250MB pull on top of it is how that beat runs long.

**Verification step, required before you declare Phase 3 done.** For each service, run:

```bash
docker run --rm --read-only --tmpfs /tmp --user 10001:10001 --cap-drop ALL <image>
```

If any service fails to start under that, fix the service. Add this as a Make target
`make verify-hardened` that loops over all five images. This directly rehearses the Act 4
live edit — if this target is green, that part of the demo cannot fail.

---

## 12. Build phases — stop at each checkpoint

**Phase 1 — skeleton.** Repo layout, one `go.mod` or `Ballerina.toml` per service, Makefile,
compose with only postgres/nats/valkey, migrations and seed SQL. Do the two §9.1 prechecks
first: Colima sized correctly, and `docker manifest inspect` confirming the Ballerina base
image has an arm64 variant. Checkpoint: `make doctor` clean, `make up && make seed` works,
`psql` shows the seeded rows.

**Phase 2 — credit-scoring (Ballerina).** The full rule engine with the golden test from §6
passing. This is the smallest service, it pins down the numbers everything else depends on,
and it is the right place to settle the Ballerina questions — the env var mechanism from
§3.2, the JSON log shape, and the hardened container from §11 — before a second Ballerina
service inherits whatever you decide. Checkpoint: `bal test` green, `curl` returns 712 and
480 for the two fixtures, `make verify-hardened` passes for this one image, and the service
**exits non-zero with a named-variable error** when a required env var is unset. Test that
last one by actually unsetting it.

**Phase 3 — loan-api (Ballerina) and payment-rail-stub (Go).** Full HTTP contract, DB
persistence, scoring call, NATS publish. Checkpoint: Scenarios 1 and 3 pass (disbursement
assertions will fail — expected, no worker yet).

**Phase 4 — disbursement-worker.** Checkpoint: Scenarios 1, 2 and 3 all pass.

**Phase 5 — arrears-eod.** Checkpoint: Scenario 4 passes with the formatted table.

**Phase 6 — loan-officer-console.** Checkpoint: all three screens work against the running
API; runtime config verified by changing `API_BASE_URL` and restarting the container only.

**Phase 7 — hardening and polish.** `make verify-hardened` green across all six images,
tracing verified end to end in Jaeger **including the Ballerina → Go hop at the broker**,
README and RUNBOOK written, `make reset && make smoke` timed.

---

## 13. Documentation to produce (Phase 7, not before)

**`README.md`** — what this is, one-command quick start, the Make targets, how to run one
scenario in isolation, and a short "how this maps to OpenChoreo" section listing which env
vars will be injected by the platform rather than set by compose. That last table is what
makes Phase 2 of the project straightforward.

**`demo/RUNBOOK.md`** — every command the presenters will type on stage, in order, with the
expected output beside each. Two people driving a 68-minute demo need a shared script, not a
shared memory. Include the fallback commands for when a live build fails.

---

## 14. Out of scope — do not build these

Authentication or authorisation of any kind. A real amortisation engine (flat interest is
fine and is stated as such). Real mobile money integration. Kubernetes manifests, Helm
charts, or **any OpenChoreo YAML** — `platform/` stays empty in this phase. A shared library
module in either language. A third language. `Config.toml` files or any other in-image
configuration. Retry/circuit-breaker frameworks. A database ORM. Multi-currency. Any
feature not named in this document.

If something in this spec is ambiguous or looks wrong, stop and ask rather than inventing a
resolution. The demo narrative depends on specific numbers and specific behaviours, and a
reasonable-looking improvisation is more expensive to unpick later than a question now.
