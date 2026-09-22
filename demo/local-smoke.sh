#!/usr/bin/env bash
# local-smoke.sh — the four demo scenarios, asserted end to end (BUILD-SPEC.md §10).
#
# For developers, during development. The presenter's on-stage script is
# demo/RUNBOOK.md, written in phase 7 (§13) — do not merge the two.
#
# Tooling is bash + curl + jq + psql only (§10.1). No test framework, no Python.
#
# NO BARE SLEEPS (§10.2). Disbursement is asynchronous: loan-api returns 201 and
# the worker acts a moment later, so a script that reads the database immediately
# after the curl passes while the work is still in flight — failing in exactly the
# direction that hides a bug. Every database assertion goes through one of the two
# polling helpers below.
#
# Grown each phase (§10). Scenarios needing a component that does not exist yet
# report PEND rather than a misleading PASS or FAIL.

set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

API="${API:-http://localhost:8080}"
SCORING="${SCORING:-http://localhost:8081}"
ADMIN="${ADMIN:-http://localhost:9090}"          # disbursement-worker admin port (§7.3)
COMPOSE_NETWORK="${COMPOSE_NETWORK:-kifaru_default}"

# psql settings come from .env (§10.1).
if [[ -f .env ]]; then
    set -a
    # shellcheck disable=SC1091
    . ./.env
    set +a
fi

PASSES=0
FAILURES=0
PENDING=0
LAST_ACTUAL=""
FIRST_DECIDED_AT=""
declare -a NOTES=()

# --- helpers ----------------------------------------------------------------

q() { psql -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }

# wait_for_count <sql> <expected> <timeout_seconds>
#   Polls every 250ms until the query returns <expected>, or fails at timeout.
#   POSITIVE assertions — a row that should appear. Failing means the system is
#   broken or slow.
wait_for_count() {
    local sql="$1" expected="$2" timeout="$3"
    local deadline=$((SECONDS + timeout))
    local actual=""
    while ((SECONDS < deadline)); do
        actual="$(q "$sql")"
        [[ "$actual" == "$expected" ]] && return 0
        sleep 0.25
    done
    LAST_ACTUAL="$actual"
    return 1
}

# assert_count_stays <sql> <expected> <seconds>
#   Asserts the query returns <expected> CONTINUOUSLY for the whole window.
#   NEGATIVE assertions — a row that must never appear. Failing means the system
#   did something it shouldn't have. A snapshot would pass simply because the
#   event had not arrived yet, which is worse than no test at all.
assert_count_stays() {
    local sql="$1" expected="$2" seconds="$3"
    local deadline=$((SECONDS + seconds))
    local actual=""
    while ((SECONDS < deadline)); do
        actual="$(q "$sql")"
        if [[ "$actual" != "$expected" ]]; then
            LAST_ACTUAL="$actual"
            return 1
        fi
        sleep 0.25
    done
    return 0
}

submit() {
    curl -s -o /tmp/kifaru-smoke-body -w '%{http_code}' \
        -X POST "$API/applications" \
        -H 'Content-Type: application/json' \
        -d "@$1"
}

# The worker's suppression counter, from its admin port (§7.3). §10.4 prefers this
# over scraping logs, which is brittle.
suppressed_total() {
    curl -s "$ADMIN/metrics" 2>/dev/null \
        | awk '/^disbursements_suppressed_total /{print $2}' \
        | head -1
}

# Publishes a second, identical loan.approved so the Valkey claim is actually
# exercised — see the note in scenario 2.
publish_duplicate_approved() {
    docker run --rm --network "$COMPOSE_NETWORK" natsio/nats-box:latest \
        nats --server nats:4222 pub loan.approved \
        '{"application_id":"APP-100244","applicant_name":"Amina W.","amount_kes":250000,"term_months":24,"wallet_msisdn":"254712345678","score":712,"approved_at":"2026-09-11T00:00:00Z"}' \
        >/dev/null 2>&1
}

body() { jq -r "$1" /tmp/kifaru-smoke-body 2>/dev/null; }

# §10.8: one line per scenario, so a failure is obvious at a glance.
report() {
    local n="$1" name="$2" verdict="$3" detail="$4"
    local dots
    dots="$(printf '%.0s.' $(seq 1 $((34 - ${#name}))))"
    printf '[%d/5] %s %s %-5s  (%s)\n' "$n" "$name" "$dots" "$verdict" "$detail"
    case "$verdict" in
        PASS) ((PASSES++)) ;;
        FAIL) ((FAILURES++)) ;;
        PEND) ((PENDING++)) ;;
    esac
}

fail_detail() {
    NOTES+=("$1")
}

# --- preflight --------------------------------------------------------------

if ! curl -sf "$API/healthz" >/dev/null 2>&1; then
    echo "loan-api is not answering on $API — run 'make up' first." >&2
    exit 1
fi
if ! q "SELECT 1" | grep -q 1; then
    echo "cannot reach postgres with the settings in .env — run 'make up' first." >&2
    exit 1
fi

# A clean slate: the runtime-created applications only. Seeded loans stay.
psql -q -c "DELETE FROM arrears_classification WHERE application_id IN ('APP-100244','APP-100245');
            DELETE FROM repayments            WHERE application_id IN ('APP-100244','APP-100245');
            DELETE FROM disbursements         WHERE application_id IN ('APP-100244','APP-100245');
            DELETE FROM loan_applications     WHERE application_id IN ('APP-100244','APP-100245');" \
    >/dev/null 2>&1

# ...AND the Valkey claims for those loans. The claim TTL is 7 days by default
# (§7.3), so a claim from an earlier run long outlives the database rows deleted
# above. Leaving it behind makes the SECOND run of this script suppress its own
# legitimate disbursement and report a false failure — which looks exactly like a
# broken worker. Clearing both is what makes `make reset && make smoke` repeatable.
docker compose exec -T valkey \
    valkey-cli DEL "disb:APP-100244" "disb:APP-100245" >/dev/null 2>&1

echo
echo "Kifaru Bank — local smoke"
echo

# --- scenario 1 — approval and disbursement (§10.3) -------------------------

s1_detail=""
s1_verdict="PASS"

status="$(submit demo/payloads/APP-100244.json)"
score="$(body '.score')"
decision="$(body '.decision')"

if [[ "$status" != "201" || "$score" != "712" || "$decision" != "APPROVED" ]]; then
    s1_verdict="FAIL"
    s1_detail="expected 201/712/APPROVED, got $status/$score/$decision"
    fail_detail "scenario 1: HTTP response was $status, score $score, decision $decision"
else
    s1_detail="score 712"
    FIRST_DECIDED_AT="$(body '.decided_at')"
    # Written by the disbursement-worker (§7.3 step 4) — absent until phase 4.
    if wait_for_count \
        "SELECT count(*) FROM disbursements WHERE application_id='APP-100244' AND provider_ref IS NOT NULL" \
        1 10; then
        s1_detail="$s1_detail, 1 disbursement"
        if wait_for_count \
            "SELECT count(*) FROM repayments WHERE application_id='APP-100244'" 24 10; then
            s1_detail="$s1_detail, 24 instalments"
        else
            s1_verdict="FAIL"
            s1_detail="$s1_detail, expected 24 instalments got ${LAST_ACTUAL:-0}"
            fail_detail "scenario 1: repayments for APP-100244 = ${LAST_ACTUAL:-0}, expected 24
    query: SELECT count(*) FROM repayments WHERE application_id='APP-100244'"
        fi
    else
        s1_verdict="FAIL"
        s1_detail="$s1_detail, expected 1 disbursement got ${LAST_ACTUAL:-0}"
        fail_detail "scenario 1: disbursements for APP-100244 = ${LAST_ACTUAL:-0}, expected 1
    query: SELECT count(*) FROM disbursements WHERE application_id='APP-100244' AND provider_ref IS NOT NULL
"
    fi
fi
report 1 "approval and disbursement" "$s1_verdict" "$s1_detail"

# --- scenario 2 — duplicate submission (§10.4) ------------------------------
# The one the whole demo rests on.
#
# Protection is layered, and this asserts BOTH layers:
#
#   1. loan-api returns 200 on a resubmit and does not re-score or re-publish
#      (§7.1), so the worker never even sees a second event.
#   2. If a duplicate loan.approved DOES reach the broker anyway — a redelivery,
#      a double publish, two workers racing — the worker's Valkey claim suppresses
#      it (§7.3 step 2).
#
# Layer 1 alone would make `assert_count_stays` pass trivially and leave
# disbursements_suppressed_total at zero, which is why the duplicate event is
# published directly here. Without that, the most important assertion in the demo
# would be green while the guarantee it names was never exercised.

s2_detail=""
s2_verdict="PASS"

status2="$(submit demo/payloads/APP-100244.json)"
score2="$(body '.score')"
decided2="$(body '.decided_at')"

if [[ "$status2" != "200" || "$score2" != "712" ]]; then
    s2_verdict="FAIL"
    s2_detail="expected 200/712 on resubmit, got $status2/$score2"
    fail_detail "scenario 2: resubmitting APP-100244 returned $status2 with score $score2, expected 200/712"
elif [[ "$decided2" != "$FIRST_DECIDED_AT" ]]; then
    s2_verdict="FAIL"
    s2_detail="decided_at changed — it was re-scored"
    fail_detail "scenario 2: decided_at changed from '$FIRST_DECIDED_AT' to '$decided2' — the duplicate was re-scored"
else
    suppressed_before="$(suppressed_total)"

    # Layer 2: force a duplicate event onto the broker.
    publish_duplicate_approved

    # Negative assertion over a window, not a snapshot: a broken worker disburses
    # late, not instantly (§10.4).
    if assert_count_stays \
        "SELECT count(*) FROM disbursements WHERE application_id='APP-100244'" 1 5; then
        suppressed_after="$(suppressed_total)"
        if (( $(printf '%.0f' "${suppressed_after:-0}") > $(printf '%.0f' "${suppressed_before:-0}") )); then
            s2_detail="still 1 disbursement after 5s, suppressed_total ${suppressed_before}->${suppressed_after}"
        else
            s2_verdict="FAIL"
            s2_detail="1 disbursement held, but disbursements_suppressed_total did not increment"
            fail_detail "scenario 2: disbursements_suppressed_total stayed at ${suppressed_before}
    the duplicate event was not suppressed by the Valkey claim — check the worker consumed it"
        fi
    else
        s2_verdict="FAIL"
        s2_detail="DOUBLE DISBURSEMENT — count went to ${LAST_ACTUAL}"
        fail_detail "scenario 2: APP-100244 has ${LAST_ACTUAL} disbursements, expected exactly 1
    query: SELECT count(*) FROM disbursements WHERE application_id='APP-100244'
    THIS IS THE FAILURE THE DEMO EXISTS TO RULE OUT."
    fi
fi
report 2 "duplicate submission" "$s2_verdict" "$s2_detail"

# --- scenario 3 — decline (§10.5) -------------------------------------------

s3_detail=""
s3_verdict="PASS"

status3="$(submit demo/payloads/APP-100245.json)"
score3="$(body '.score')"
decision3="$(body '.decision')"

if [[ "$status3" != "201" || "$score3" != "480" || "$decision3" != "DECLINED" ]]; then
    s3_verdict="FAIL"
    s3_detail="expected 201/480/DECLINED, got $status3/$score3/$decision3"
    fail_detail "scenario 3: HTTP response was $status3, score $score3, decision $decision3"
else
    # A declined loan must never be disbursed. Negative assertion, so the count is
    # held for a window rather than sampled once.
    if assert_count_stays \
        "SELECT count(*) FROM disbursements WHERE application_id='APP-100245'" 0 3; then
        s3_detail="score 480, 0 disbursements after 3s"
    else
        s3_verdict="FAIL"
        s3_detail="a declined loan was disbursed (${LAST_ACTUAL})"
        fail_detail "scenario 3: APP-100245 is DECLINED but has ${LAST_ACTUAL} disbursement(s)
    query: SELECT count(*) FROM disbursements WHERE application_id='APP-100245'"
    fi
fi
report 3 "decline" "$s3_verdict" "$s3_detail"

# --- scenario 4 — arrears classification (§10.6) ----------------------------
#
# AS_OF_DATE is left UNSET so the job uses today — the same day the seed ran,
# which §5's relative due dates make deterministic. Pinning an absolute date here
# would only work if the seed dates were absolute too, and they are not.
#
# Synchronous: the job runs to completion, so no polling helper is needed.
#
# Depends on scenario 1 having run, since APP-100244 supplies the CURRENT bucket
# and does not exist otherwise — asserted explicitly rather than silently
# accepting four rows.

s4_detail=""
s4_verdict="PASS"

eod_output="$(docker compose run --rm arrears-eod 2>&1)"
eod_exit=$?

if ((eod_exit != 0)); then
    s4_verdict="FAIL"
    s4_detail="arrears-eod exited $eod_exit"
    fail_detail "scenario 4: arrears-eod exited with code $eod_exit
$(echo "$eod_output" | tail -5)"
else
    # every expected bucket, per §10.6's table
    mismatches=""
    for pair in \
        "APP-100301:DPD_1_30" \
        "APP-100302:DPD_31_60" \
        "APP-100303:DPD_61_90" \
        "APP-100304:NPL_90_PLUS" \
        "APP-100244:CURRENT"
    do
        app="${pair%%:*}"; want="${pair##*:}"
        got="$(q "SELECT bucket FROM arrears_classification WHERE application_id='$app'")"
        [[ "$got" == "$want" ]] || mismatches="$mismatches $app(want $want, got ${got:-none})"
    done

    total="$(q 'SELECT count(*) FROM arrears_classification')"
    npl="$(q "SELECT count(*) FROM arrears_classification WHERE bucket='NPL_90_PLUS'")"

    if [[ -n "$mismatches" ]]; then
        s4_verdict="FAIL"
        s4_detail="bucket mismatch:$mismatches"
        fail_detail "scenario 4: wrong buckets —$mismatches
    query: SELECT application_id, bucket FROM arrears_classification ORDER BY 1"
    elif [[ "$total" != "5" ]]; then
        s4_verdict="FAIL"
        s4_detail="expected 5 classified loans, got $total"
        fail_detail "scenario 4: arrears_classification has $total rows, expected 5"
    elif [[ "$npl" != "1" ]]; then
        s4_verdict="FAIL"
        s4_detail="expected exactly 1 NPL_90_PLUS, got $npl"
        fail_detail "scenario 4: $npl loans in NPL_90_PLUS, expected exactly 1"
    elif ! grep -q "Non-performing exposure: KES" <<<"$eod_output"; then
        s4_verdict="FAIL"
        s4_detail="the non-performing exposure line was not printed"
        fail_detail "scenario 4: arrears-eod did not print the exposure total (§7.4)"
    else
        s4_detail="5 loans, 1 NPL"
    fi

    # The table is what goes on the projector, so show it.
    printf '%s\n' "$eod_output" | sed -n '/^=== Kifaru Bank/,/^Completed in/p' > /tmp/kifaru-arrears-table
fi
report 4 "arrears classification" "$s4_verdict" "$s4_detail"

# --- scenario 5 — isolation check (§10.7) -----------------------------------
# Cannot be fully proven locally. Here we only assert credit-scoring answers from
# INSIDE the compose network.
#
# On OpenChoreo the equivalent call from OUTSIDE the cell must be REFUSED — that
# is the Act 2 beat at T+0:49, and it is what makes credit-scoring an internal-only
# component rather than merely an undocumented one.

if docker compose exec -T loan-api \
        wget -q -O- "http://credit-scoring:8081/healthz" >/dev/null 2>&1; then
    report 5 "isolation check" "PASS" "scoring reachable in-network"
else
    report 5 "isolation check" "FAIL" "credit-scoring unreachable inside the compose network"
    fail_detail "scenario 5: credit-scoring did not answer on the compose network"
fi

# --- summary ----------------------------------------------------------------
# §10.8: run every scenario, then exit non-zero. Knowing three of five broke is
# more useful than knowing the first one did.

echo
# §10.6 asks for the formatted table to be printed — it is the artefact the
# presenter puts on screen.
if [[ -s /tmp/kifaru-arrears-table ]]; then
    cat /tmp/kifaru-arrears-table
    echo
fi

if ((${#NOTES[@]} > 0)); then
    echo "Failures:"
    for n in "${NOTES[@]}"; do
        echo "  - $n"
    done
    echo
fi
printf '%d passed, %d failed, %d pending\n' "$PASSES" "$FAILURES" "$PENDING"

((FAILURES == 0)) || exit 1
exit 0
