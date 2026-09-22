#!/usr/bin/env bash
# D3 — load the Kifaru schema and seed into the cell's Postgres.
#
# Run from tutorial-1/:   cd tutorial-1 && ./scripts/load-seed.sh
#
# Nothing here is hard-coded, because none of the names are predictable:
#   * the CELL namespace is generated   dp-<ns>-<project>-<env>-<hash>
#   * the postgres Service is named after the RESOURCE RELEASE, not the Resource:
#       r-loans-db-development-<hash>        NOT  loans-db
#   * its secret is <that name>-creds       NOT  loans-db-creds
#   * the username is <resource>-user       ->   loans-db-user
#
# Usage:  ./scripts/load-seed.sh [ENVIRONMENT]                         default: development
#         ./scripts/load-seed.sh development --reseed                  drop and reload
#
# Environment:
#   NS            OpenChoreo namespace (default kifaru-bank); PROJECT (default lending)
#   KUBE_CONTEXT  kubectl context of the DATA PLANE, where the cells live. Default:
#                 the current context (right for a single-cluster install such as
#                 k3d). On a multi-cluster install set it to the data-plane context
#                 — that is NOT the cluster occ talks to.
#   Needs: occ (logged in), kubectl, psql (brew install libpq && brew link --force libpq).
#
# 001_init.sql has no IF NOT EXISTS and the seed has no ON CONFLICT, so a second
# run fails on "relation already exists". --reseed drops the four tables first;
# that is also how you re-anchor the due dates on the day you demo.

set -euo pipefail

ENVIRONMENT="${1:-development}"
RESEED="${2:-}"
OC_NS="${NS:-kifaru-bank}"
PROJECT="${PROJECT:-lending}"
LOCAL_PORT=15432
KUBE_CONTEXT="${KUBE_CONTEXT:-}"
KUBECTL=(kubectl)
if [ -n "$KUBE_CONTEXT" ]; then KUBECTL=(kubectl --context "$KUBE_CONTEXT"); fi

command -v psql >/dev/null || { echo "psql not found: brew install libpq && brew link --force libpq" >&2; exit 1; }
[ -f db/migrations/001_init.sql ] || { echo "run this from tutorial-1/ (db/migrations not found here)" >&2; exit 1; }
echo "==> namespace=${OC_NS}  project=${PROJECT}  env=${ENVIRONMENT}  kubectl=${KUBECTL[*]}"

echo "==> finding the ${ENVIRONMENT} cell"
# status.namespace is the LAST 'namespace:' in the YAML; metadata.namespace is the
# first. Taking the first is the bug that sent kubectl looking for a Service named
# after the cell.
CELL=$(occ projectreleasebinding get "${PROJECT}-${ENVIRONMENT}" -n "$OC_NS" \
       | awk '/^  namespace:/{v=$2} END{print v}')
[ -n "$CELL" ] && [[ "$CELL" == dp-* ]] || {
  echo "ERROR: could not resolve the cell namespace (got '${CELL}')." >&2
  echo "       is ${PROJECT}-${ENVIRONMENT} Ready?  occ projectreleasebinding list -n $OC_NS -p $PROJECT" >&2
  exit 1; }
echo "    $CELL"

echo "==> finding the postgres service"
SVC=$("${KUBECTL[@]}" get svc -n "$CELL" -o name | grep loans-db | head -1 | cut -d/ -f2)
[ -n "$SVC" ] || {
  echo "ERROR: no loans-db service in $CELL." >&2
  echo "       is the resource bound?  occ resourcereleasebinding list -n $OC_NS" >&2
  exit 1; }
echo "    $SVC"

DB=$("${KUBECTL[@]}" get cm "${SVC}-config" -n "$CELL" -o jsonpath='{.data.database}')
USER=$("${KUBECTL[@]}" get cm "${SVC}-config" -n "$CELL" -o jsonpath='{.data.username}')
PASS=$("${KUBECTL[@]}" get secret "${SVC}-creds" -n "$CELL" -o jsonpath='{.data.password}' | base64 -d)
echo "    database=$DB  user=$USER"

echo "==> port-forwarding ${LOCAL_PORT} -> ${SVC}:5432"
"${KUBECTL[@]}" port-forward -n "$CELL" "svc/${SVC}" "${LOCAL_PORT}:5432" >/dev/null 2>&1 &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null || true' EXIT

for i in $(seq 1 30); do
  PGPASSWORD="$PASS" psql -h localhost -p "$LOCAL_PORT" -U "$USER" -d "$DB" -c 'SELECT 1' >/dev/null 2>&1 && break
  [ "$i" = 30 ] && { echo "ERROR: port-forward never became usable" >&2; exit 1; }
  sleep 1
done

if [ "$RESEED" = "--reseed" ]; then
  echo "==> dropping existing tables (--reseed)"
  PGPASSWORD="$PASS" psql -h localhost -p "$LOCAL_PORT" -U "$USER" -d "$DB" -v ON_ERROR_STOP=1 -c "
    DROP TABLE IF EXISTS arrears_classification, repayments, disbursements, loan_applications CASCADE;"
fi

echo "==> applying schema and seed"
PGPASSWORD="$PASS" psql -h localhost -p "$LOCAL_PORT" -U "$USER" -d "$DB" \
  -v ON_ERROR_STOP=1 \
  -f db/migrations/001_init.sql \
  -f db/seed/seed.sql

echo "==> verifying (expect dpd 124 / 78 / 47 / 12)"
# Same query as demo/PHASE1-MANUAL-TESTS.txt A.4 — unpaid is paid_date IS NULL,
# and the name column is applicant_name.
PGPASSWORD="$PASS" psql -h localhost -p "$LOCAL_PORT" -U "$USER" -d "$DB" -c "
  SELECT l.application_id, l.applicant_name,
         (CURRENT_DATE - MIN(r.due_date) FILTER (WHERE r.paid_date IS NULL)) AS dpd,
         CASE
           WHEN (CURRENT_DATE - MIN(r.due_date) FILTER (WHERE r.paid_date IS NULL)) <= 0  THEN 'CURRENT'
           WHEN (CURRENT_DATE - MIN(r.due_date) FILTER (WHERE r.paid_date IS NULL)) <= 30 THEN 'DPD_1_30'
           WHEN (CURRENT_DATE - MIN(r.due_date) FILTER (WHERE r.paid_date IS NULL)) <= 60 THEN 'DPD_31_60'
           WHEN (CURRENT_DATE - MIN(r.due_date) FILTER (WHERE r.paid_date IS NULL)) <= 90 THEN 'DPD_61_90'
           ELSE 'NPL_90_PLUS'
         END AS bucket,
         SUM(r.amount_due_kes) FILTER (WHERE r.paid_date IS NULL) AS outstanding
    FROM loan_applications l JOIN repayments r ON r.application_id = l.application_id
   GROUP BY 1,2 ORDER BY dpd DESC;"

echo
echo "Done. Re-run on the day you demo — due dates anchor to CURRENT_DATE at seed time."
