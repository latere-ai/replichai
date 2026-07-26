#!/usr/bin/env bash
# Lightweight full-stack smoke test against the real compose stack (Postgres +
# the server), using the free in-process memory provider — no API key, no cost.
# It brings the stack up, submits a paper, asserts the job runs to done, and
# tears the stack down. Exits non-zero on any failure.
set -euo pipefail

cd "$(dirname "$0")/.."
BASE=${BASE:-http://localhost:8080}

cleanup() { docker compose down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> bringing up postgres + server (memory provider)"
docker compose up -d --build >/dev/null

echo -n "==> waiting for health"
for i in $(seq 1 30); do
  if curl -sf "$BASE/healthz" >/dev/null 2>&1; then echo " ok"; break; fi
  echo -n "."; sleep 2
  [ "$i" = 30 ] && { echo " timed out"; docker compose logs server | tail -20; exit 1; }
done

echo "==> submitting a paper"
JOB=$(curl -sf -XPOST "$BASE/papers" -H 'content-type: application/json' \
  -d '{"url":"10.1145/3544548.3580778","project":"smoke"}')
ID=$(printf '%s' "$JOB" | sed -n 's/.*"job_id":"\([^"]*\)".*/\1/p')
[ -n "$ID" ] || { echo "no job id in: $JOB"; exit 1; }
echo "    job_id: $ID"

echo -n "==> waiting for the job to finish"
for i in $(seq 1 30); do
  PHASE=$(curl -sf "$BASE/jobs/$ID" | sed -n 's/.*"phase":"\([^"]*\)".*/\1/p')
  case "$PHASE" in
    done) echo " -> done"; break ;;
    failed) echo " -> FAILED"; curl -s "$BASE/jobs/$ID"; exit 1 ;;
    *) echo -n "."; sleep 1 ;;
  esac
  [ "$i" = 30 ] && { echo " timed out in phase $PHASE"; exit 1; }
done

echo "==> confirming the job persisted in Postgres"
docker compose exec -T postgres psql -U replichai -d replichai -tAc \
  "SELECT phase FROM jobs WHERE id='$ID'" | grep -qx done \
  && echo "    row present, phase=done" \
  || { echo "job not found in Postgres"; exit 1; }

echo "==> SMOKE OK"
