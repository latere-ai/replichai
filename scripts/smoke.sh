#!/usr/bin/env bash
# Lightweight full-stack smoke test against the real compose stack (Postgres +
# the server), using the free in-process memory provider — no API key, no cost.
# It brings the stack up, submits a paper, asserts the job runs to done, and
# tears the stack down. Exits non-zero on any failure.
set -euo pipefail

cd "$(dirname "$0")/.."
BASE=${BASE:-http://localhost:8080}

# Work with either Docker or podman (the compose subcommand or the standalone).
detect_compose() {
  if [ -n "${COMPOSE:-}" ]; then echo "$COMPOSE"; return; fi
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then echo "docker compose"; return; fi
  if command -v docker-compose >/dev/null 2>&1; then echo "docker-compose"; return; fi
  if command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then echo "podman compose"; return; fi
  if command -v podman-compose >/dev/null 2>&1; then echo "podman-compose"; return; fi
  echo ""
}
COMPOSE=$(detect_compose)
[ -n "$COMPOSE" ] || { echo "no docker/podman compose found on PATH"; exit 1; }
echo "==> using: $COMPOSE"

cleanup() { $COMPOSE down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> bringing up postgres + server (memory provider)"
$COMPOSE up -d --build >/dev/null

echo -n "==> waiting for health"
for i in $(seq 1 30); do
  if curl -sf "$BASE/healthz" >/dev/null 2>&1; then echo " ok"; break; fi
  echo -n "."; sleep 2
  [ "$i" = 30 ] && { echo " timed out"; docker compose logs server | tail -20; exit 1; }
done

# Two acts since 012: a project records the papers it is about, and a session
# runs one of them. The create spends nothing, which is why it works here with
# no credits configured at all.
echo "==> opening a project with one paper"
CREATED=$(curl -sf -XPOST "$BASE/projects" -H 'content-type: application/json' \
  -d '{"title":"smoke","papers":["10.1145/3544548.3580778"]}')
PROJECT=$(printf '%s' "$CREATED" | sed -n 's/.*"project_id":"\([^"]*\)".*/\1/p')
KEY=$(printf '%s' "$CREATED" | sed -n 's/.*"key":"\([^"]*\)".*/\1/p')
[ -n "$PROJECT" ] && [ -n "$KEY" ] || { echo "no project or paper key in: $CREATED"; exit 1; }
echo "    project: $PROJECT, paper: $KEY"

echo "==> starting a session for it"
JOB=$(curl -sf -XPOST "$BASE/projects/$PROJECT/sessions" -H 'content-type: application/json' \
  -d "{\"paper_key\":\"$KEY\"}")
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
$COMPOSE exec -T postgres psql -U replichai -d replichai -tAc \
  "SELECT phase FROM jobs WHERE id='$ID'" | grep -qx done \
  && echo "    row present, phase=done" \
  || { echo "job not found in Postgres"; exit 1; }

echo "==> SMOKE OK"
