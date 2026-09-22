# Testing and demoing Kifaru Bank

A runbook for anyone picking this up cold. Follow it top to bottom the first time;
after that, **§3 Automated checks** and **§5 The demo** are the two sections you will
reuse.

Everything in §1–§7 runs locally in Docker and touches no cluster. Deploying to
OpenChoreo is a separate pair of documents: `docs/PLATFORM-GUIDE.md`
(platform engineer) and `docs/DEVELOPER-GUIDE.md` (developer).

*Kifaru* is Swahili for rhinoceros — hence the logo.

---

## 1. Before you start

**You need:** Docker (Colima or Desktop) with ~6 GB free and about 4 GB of disk.
Ballerina, Go and Node are **not** required — every service builds inside a
container. They are only needed for `make test`, which runs unit tests on the host.

**Check Docker is alive:**

```bash
docker ps
```

If that errors, start it. On Colima: `colima start --cpu 6 --memory 10 --disk 100
--vm-type vz --vz-rosetta --mount-type virtiofs`.

On a 16 GB machine do **not** give Colima 16 GB — macOS needs several itself.

**Two extra tools, for §3 only:**

```bash
brew install jq
brew install libpq && brew link --force libpq   # gives you psql
```

**Ports used.** Eight are published:

| Port | Service |
|---|---|
| 3001 | loan-application-portal — the customer's form |
| 3000 | loan-officer-console — the staff screens |
| 8080 | loan-api |
| 8081 | credit-scoring |
| 8082 | payment-rail-stub |
| 9090 | disbursement-worker admin (metrics only) |
| 5432 / 4222 / 6379 | postgres / NATS / Valkey |

> **8080 collides with a k3d cluster** if you also run OpenChoreo locally. Only one
> stack at a time — see §7.

Check a port before blaming the stack: `lsof -nP -iTCP:8080 -sTCP:LISTEN`.

The Compose project name is pinned to `kifaru`, so containers are always `kifaru-*`
regardless of the checkout directory name.

**Run every `make` command from this directory.** Anywhere else gives
`make: *** No rule to make target`, which means wrong directory, not broken Makefile.

---

## 2. First run

```bash
make doctor     # is the VM up and big enough?
make up         # build and start everything    (first run ~10 min)
make seed       # schema + four historical loans
```

`make up` builds seven images including a ~1 GB Ballerina builder. Later runs take
seconds. It uses `--wait`, so it does not return until every container is healthy.

### What "working" looks like

```bash
curl -s localhost:8080/healthz     # {"status":"ok"}
curl -s localhost:8081/healthz     # {"status":"ok"}
curl -s localhost:9090/healthz     # {"status":"ok"}
```

`make seed` ends with a four-row table: Halima S. at 124 days past due, Peter O. at
78, Grace N. at 47, Joseph M. at 12. **Those four numbers are the check** — if they
are 125/79/48/13 you seeded yesterday, so re-run `make seed`. See §7.

Then open **http://localhost:3001** (customer portal) and **http://localhost:3000**
(staff console).

---

## 3. Automated checks

### 3a. The five demo scenarios — ~30s

```bash
make smoke
```

```
[1/5] approval and disbursement ......... PASS   (score 712, 1 disbursement, 24 instalments)
[2/5] duplicate submission .............. PASS   (still 1 disbursement after 5s, suppressed_total 3->4)
[3/5] decline ........................... PASS   (score 480, 0 disbursements after 3s)
[4/5] arrears classification ............ PASS   (5 loans, 1 NPL)
[5/5] isolation check ................... PASS   (scoring reachable in-network)

5 passed, 0 failed, 0 pending
```

It prints the arrears table afterwards. Exits non-zero if anything fails, and runs
all five rather than stopping at the first.

**Scenario 2 is the one that matters.** It submits the same application twice *and*
publishes a duplicate event straight to the broker, then asserts only one
disbursement exists after a 5-second window and that the worker's suppression
counter went up. If that ever goes red, stop and investigate before demoing.

### 3b. Unit tests — needs the toolchains on the host

```bash
make test
```

Expect 19 (credit-scoring) + 11 (loan-api) + three Go modules + 10 (console) + 31
(portal), all passing.

### 3c. Container hardening

```bash
make verify-hardened
```

All seven images must start read-only, as non-root uid 10001, with every capability
dropped. `arrears-eod` reports "batch job, exited 0" — that is a pass, not a
failure; it is a CronJob and exiting is what it is supposed to do.

---

## 4. Manual spot checks

```bash
cd tutorial-1
set -a && . ./.env && set +a     # once per terminal, needed for every psql below
```

```bash
# submit an application -> 201, score 712, APPROVED
curl -s -X POST localhost:8080/applications \
     -H 'Content-Type: application/json' \
     -d @demo/payloads/APP-100244.json | jq .

# the worker disburses a moment later - give it 3s, then look
curl -s localhost:8080/applications/APP-100244 \
  | jq -c '{decision, provider_ref:.disbursement.provider_ref, instalments:(.repayments|length)}'
# -> APPROVED, PR-..., 24

# send it again - 200, not 201, and still exactly ONE disbursement
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:8080/applications \
     -H 'Content-Type: application/json' -d @demo/payloads/APP-100244.json
psql -tAc "SELECT count(*) FROM disbursements WHERE application_id='APP-100244';"

# what the worker logged
docker compose logs disbursement-worker | grep -E 'claimed|payment sent' | tail -2

# the arrears batch job
docker compose run --rm arrears-eod
```

`demo/CURLS.txt` has the full set, including the scoring rules and the event stream.

---

## 5. The demo

### Before presenting

```bash
make reset        # ~10s: destroys volumes, restarts, re-seeds
make purge-events # clears accumulated events and Valkey claims
make smoke        # confirm 5/5 before anyone is watching
```

Then open both browser tabs and leave them on screen.

### The beats

**1. A customer applies.** On **localhost:3001**, fill in the form. Amount and term
show a live monthly repayment as you type. Submit — a decision comes back in about a
second with the score and the reason.

To pin the scripted fixture that scores exactly **712**, expand *Reference number*
and enter `APP-100244`. Leave it blank and the platform generates an id, which is
better for an audience suggestion.

**2. The bank sees it.** Switch to **localhost:3000**. The application appears within
5 seconds — the list auto-refreshes. Click it: the score breakdown, the disbursement
the worker wrote, and the 24-instalment repayment schedule.

**3. Submit the same application again.** Back on the portal, submit identical
details. The screen says *"You have already submitted this application… nothing has
been charged or paid out twice."* Show the console: **still one disbursement**.

That is the whole point of the demo. Two independent defences — `loan-api` returns
200 without re-scoring, and if a duplicate event reaches the broker anyway the
worker's Valkey claim suppresses it:

```bash
curl -s localhost:9090/metrics | grep disbursements_suppressed_total
```

**4. A loan that fails.** Submit with a low income or a large amount — DECLINED,
with a reason explaining why. No disbursement, ever.

**5. The night shift.**

```bash
docker compose run --rm arrears-eod
```

Prints the collections table, worst bucket first, ending with the non-performing
exposure. Then show the console's **Arrears queue** tab — the same data, grouped.

**6. Fast-forward the clock.** The line that lands with a banking audience:

```bash
docker compose run --rm -e AS_OF_DATE=2026-12-01 -e DRY_RUN=true arrears-eod
```

Every loan climbs a bucket and the non-performing exposure grows. `DRY_RUN=true`
means nothing is written — **use it**, or the fast-forwarded buckets become the
stored truth and you must re-run without it to restore.

---

## 6. The screens, tab by tab

**Portal (3001)** — one screen, submission only. Deliberately reads nothing back: no
arrears, no schedules, no other customers' applications. A customer-facing app that
could read collections data would be the wrong shape.

**Console (3000)**

- *Applications* — every application, newest first, refreshing every 5s. Score,
  decision, arrears bucket.
- *Application detail* (click a row) — the record, the scoring factor breakdown, the
  disbursement, and the repayment schedule with overdue instalments highlighted.
- *Arrears queue* — grouped by bucket, worst first, with the collections action for
  each. This is the collections team's view.

---

## 7. Things that look broken and are not

**`arrears_bucket` is empty everywhere, and the Arrears queue is blank.** Correct
until `arrears-eod` has run. `docker compose run --rm arrears-eod`.

**Days-past-due read 125/79/48/13 instead of 124/78/47/12.** The seed anchors due
dates to the day it runs, then they are fixed — so they drift one day per day.
`make seed` re-anchors. This is why §5 says re-seed before presenting.

**A disbursement never appears.** Almost always a stale Valkey claim. Claims live 7
days, so deleting a loan's rows without clearing its claim makes the worker suppress
the next legitimate disbursement — which looks exactly like a broken worker.
`make purge-events` clears both events and claims.

**`bal run` says `LOG_LEVEL is required but was not set`.** By design: services fail
loudly at startup rather than silently defaulting. Use `LOG_LEVEL=info bal run`.
`make test` supplies placeholders for you.

**`make verify-hardened` says `arrears-eod ... exited 0`.** A pass. It is a batch job;
running to completion and exiting is the requirement.

**`localhost:8080/metrics` and `:8081/metrics` return 404.** Known deviation: those
two are Ballerina services and their built-in Prometheus endpoint is served on the
runtime's own observability port, not the service port. The worker's metrics on
:9090 do work.

**`jq` prints the schedule total as `339999.99999999994`.** Binary floating point in
`jq`, not bad data. The database holds `340000.00` exactly — check with psql.

**k3d will not start: "port is already allocated".** `loan-api` holds 8080. Stop this
stack first (`docker compose stop`), then start the cluster. Only one at a time.

---

## 8. Troubleshooting

**Nothing responds after a reboot.** The Colima VM is stopped, not broken.
`colima start`, then `make up`. `make doctor` tells you which.

**`make: *** No rule to make target`.** Wrong directory. `pwd` must end in
`tutorial-1`.

**`psql: authentication failed`.** You skipped `set -a && . ./.env && set +a`.

**A container is unhealthy.** `docker compose ps` to find it, then
`docker compose logs <service> | tail -40`. All services log JSON to stdout with the
same shape, and every loan-related line carries `application_id`.

**Everything is sluggish.** Check host memory, not the VM. Colima's allocation is
committed whether or not it is busy, so on a 16 GB machine other Docker projects and
browser tabs matter more than anything inside the VM.

**Start completely clean.**

```bash
make down     # removes volumes: database, event stream, Valkey claims
make up && make seed
```

---

## 9. Where to look next

| File | What |
|---|---|
| `README.md` | Quick start and the component list |
| `BUILD-SPEC.md` | The specification — authoritative for every behaviour and number |
| `docs/BUILD-IMAGES.md` | Building and pushing the six images for the platform |
| `docs/PLATFORM-GUIDE.md` | Deploying to OpenChoreo: the platform-engineer half |
| `docs/DEVELOPER-GUIDE.md` | Deploying to OpenChoreo: the developer half |
| `demo/CURLS.txt` | Every API call, copy-pasteable |
| `demo/local-smoke.sh` | The five scenarios, asserted |
| `demo/PHASE1-MANUAL-TESTS.txt` | Data layer: schema, seed, constraints |
| `demo/PHASE2-MANUAL-TESTS.txt` | credit-scoring, 26 numbered steps |
| `demo/PHASE3-MANUAL-TESTS.txt` | loan-api + payment-rail-stub, 30 steps |

**Status:** phases 1–6 of 7 are complete and everything above works. Phase 7 —
end-to-end tracing in Jaeger, the on-stage presenter script, and timing the
reset-to-smoke loop — is outstanding.
