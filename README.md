# Kifaru Bank — retail lending demo

*The application behind **Internal Developer Platform (OpenChoreo) — Tutorial 1**,
WSO2CON 2026 Nairobi.*

A fictional Kenyan bank's retail loan system, built as a conference demo. A
customer applies for a loan, the bank scores it, approved loans are disbursed to
a mobile wallet, and a nightly batch job classifies loans falling behind on
repayments.

Built to `BUILD-SPEC.md`, which is the authority for every behaviour and number
here. This file is only how to run it **locally**. Deploying it onto an OpenChoreo
platform is the tutorial:

| Read | For |
|---|---|
| `docs/BUILD-IMAGES.md` | build the six images and push them to your registry |
| `docs/PLATFORM-GUIDE.md` | the platform engineer's half: tenancy, environments, cell design, the bank's component and resource types, authorization |
| `docs/DEVELOPER-GUIDE.md` | the developer's half: project, resources, components, workloads, a source build, promotion, and what the platform refuses |
| `manifests/platform/`, `manifests/app/` | the OpenChoreo artifacts those guides apply, in order |

Run it locally first (below) — the platform deployment uses the same images,
the same environment variable names and the same seed.

> **Status: phases 1–6 of 7 complete.** Everything below works. Phase 7
> (end-to-end tracing in Jaeger, the presenter's runbook, timing) is outstanding.

---

## What you need

**To run the demo you need Docker and `make`. Nothing else.** Every service
builds inside a container — you do not need Ballerina, Go or Node installed.

| Tool | Needed for | Notes |
|---|---|---|
| **Colima** (or Docker Desktop) | running anything | see sizing below |
| **make** | everything | preinstalled on macOS with Xcode CLT |
| `psql` | `make smoke` only | `brew install libpq && brew link --force libpq` |
| `jq` | `make smoke` only | `brew install jq` |
| Ballerina 2201.13.6 | `make test`, editing Ballerina | `bal dist pull 2201.13.6` |
| Go 1.23+ | `make test`, editing Go | `brew install go` |
| Node 20+ | `make test`, editing the web apps | `brew install node` |

### Colima sizing

```bash
colima start --cpu 6 --memory 10 --disk 100 \
             --vm-type vz --vz-rosetta --mount-type virtiofs
```

On a 16 GB machine do **not** give the VM 16 GB — macOS needs several itself.
10 GB is comfortable. `make doctor` reports what the VM actually has and warns
if it is undersized; that is the first thing to run when something behaves oddly,
because a stopped or undersized VM shows up as an unrelated-looking build error.

---

## Quick start

```bash
cd tutorial-1
make doctor     # is the VM up and big enough?
make up         # build and start everything, wait for healthy  (first run: ~10 min)
make seed       # apply migrations and load the four historical loans
make smoke      # run all five demo scenarios
```

`make up` on a cold machine pulls and builds seven images, including a ~1 GB
Ballerina builder. Later runs take seconds.

Then open:

| URL | What |
|---|---|
| **http://localhost:3001** | **Loan application portal** — the customer's form |
| **http://localhost:3000** | **Loan officer console** — applications, detail, arrears queue |
| http://localhost:8080 | `loan-api` (the public API) |
| http://localhost:8081 | `credit-scoring` (internal-only in production) |
| http://localhost:8082 | `payment-rail-stub` (fake mobile money provider) |
| http://localhost:9090/metrics | `disbursement-worker` admin port |

A good first run-through: submit an application on the portal, watch it appear
in the console within 5 seconds, open it and see the disbursement the worker
wrote. Then submit the **same** application again and watch nothing get paid
twice.

---

## The components

| Component | Language | What it does |
|---|---|---|
| `loan-application-portal` | React 18 | Customer's form. Submission only |
| `loan-officer-console` | React 18 | Staff screens: list, detail, arrears queue |
| `loan-api` | Ballerina | Public API: validate, persist, score, publish |
| `credit-scoring` | Ballerina | The rule engine. No database, deterministic |
| `disbursement-worker` | Go | Consumes `loan.approved`, claims, pays, schedules |
| `arrears-eod` | Go | Nightly batch: classifies loans into arrears buckets |
| `payment-rail-stub` | Go | Stands in for a mobile money provider |

Plus postgres, NATS (JetStream) and Valkey.

---

## Make targets

```
make up               build and start everything, wait for healthy
make down             stop and remove volumes
make seed             apply migrations and seed data
make smoke            run all five demo scenarios
make test             every unit test suite
make logs             follow all service logs
make reset            down, up, seed — a clean slate between dry runs
make purge-events     drop accumulated events and Valkey claims, no teardown
make verify-hardened  run every image read-only, non-root, no capabilities
make doctor           check the VM is running and sized adequately
```

---

## Things that will confuse you otherwise

**Run every `make` command from `tutorial-1/`.** That is where the Makefile is.
Anywhere else gives `make: *** No rule to make target` — which means wrong
directory, not broken Makefile.

**`psql` needs the environment loaded first**, once per terminal:

```bash
set -a && . ./.env && set +a
psql -c "SELECT application_id, score, decision FROM loan_applications;"
```

**Re-seed on the day you test.** The seed computes due dates relative to
`CURRENT_DATE` *when it runs*, then those dates are fixed. Seed on Monday and
test on Thursday and the days-past-due read 15/50/81/127 instead of the expected
12/47/78/124. `make seed` re-anchors them.

**A disbursement not appearing usually means a stale Valkey claim.** Claims live
7 days; if you delete a loan's rows without clearing its claim, the worker
suppresses the next legitimate disbursement — which looks exactly like a broken
worker. `make purge-events` clears both events and claims.

**`arrears_bucket` is null until `arrears-eod` runs.** The console's arrears
queue is empty until then. Run it with:

```bash
docker compose run --rm arrears-eod
```

**Services refuse to start without their configuration**, by design. `bal run`
with no environment gives `error: LOG_LEVEL is required but was not set`. That is
the fail-loudly-at-startup rule, not a fault. `make test` supplies placeholders.

---

## Running one service from source

Only needed if you are editing that service. Everything else keeps running in
containers.

```bash
# Ballerina
cd app/credit-scoring
LOG_LEVEL=info bal run

cd app/loan-api
LOG_LEVEL=info \
DATABASE_URL=postgres://kifaru:kifaru_local_only@localhost:5432/kifaru \
NATS_URL=nats://localhost:4222 \
CREDIT_SCORING_URL=http://localhost:8081 bal run

# Go
cd app/disbursement-worker && go test ./...

# Web (proxies /applications to loan-api)
cd app/loan-application-portal && npm install && npm run dev
```

---

## Configuration

`.env` is **gitignored** and created automatically from `.env.example` on first
`make up`. Every value in it is a local development placeholder — there are no
real credentials in this repository, and none belong in it.

The variable names are the contract with OpenChoreo's dependency injection for
the deployment phase, so they are not renamed to suit a language. `.env.example`
documents what each one is for.

---

## Where to look next

| File | What |
|---|---|
| `BUILD-SPEC.md` | The specification. Authoritative for every behaviour and number |
| `demo/CURLS.txt` | Every API call, copy-pasteable |
| `demo/PHASE1-MANUAL-TESTS.txt` | Data layer: schema, seed, constraints |
| `demo/PHASE2-MANUAL-TESTS.txt` | `credit-scoring`, 26 numbered steps |
| `demo/PHASE3-MANUAL-TESTS.txt` | `loan-api` + `payment-rail-stub`, 30 steps |
| `demo/local-smoke.sh` | The five scenarios, asserted |
| `db/migrations/001_init.sql` | Schema |
| `db/seed/seed.sql` | The four historical loans |
