#!/usr/bin/env bash
#
# Post-deploy release smoke for replichai.latere.ai.
#
# replichai's gate is fail-closed and its OIDC login front door is not wired
# yet, so the operator surface admits nobody in production. What a release must
# still prove is liveness: the pod is up, migrations applied at startup, and the
# open /healthz endpoint answers. That is exactly what this checks. It grows an
# authenticated check once the login lands.
#
# Required tools: curl.
#
# Env (pipeline contract):
#   BASE_URL    default https://replichai.latere.ai
#   TAG         release tag, for evidence output
#   COMMIT      release commit sha, for evidence output
#   DEPLOY_URL  deploy workflow URL, for evidence output
#   OUTPUT_MD   optional path for markdown evidence
# EXPECTED_ASSET / SERVICE_TOKEN are accepted but unused (dockerfile mode, unauthed).

set -euo pipefail

BASE_URL="${BASE_URL:-https://replichai.latere.ai}"
BASE_URL="${BASE_URL%/}"
TAG="${TAG:-unknown}"
COMMIT="${COMMIT:-unknown}"
DEPLOY_URL="${DEPLOY_URL:-}"
OUTPUT_MD="${OUTPUT_MD:-}"

pass() { printf 'OK %s\n' "$*"; }
fail() { printf 'FAIL %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null || fail "curl is required"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# --retry rides out the transient 5xx the LB emits during a rollout cutover.
code=$(curl -sS -L --retry 5 --retry-delay 2 --retry-max-time 30 \
  -o "$tmp/health" -w '%{http_code}' "${BASE_URL}/healthz") || fail "healthz: curl failed"
[ "$code" = "200" ] || fail "healthz: expected 200, got $code (body: $(head -c 200 "$tmp/health" 2>/dev/null || true))"
grep -q '"status":"ok"' "$tmp/health" || fail "healthz: body is not ok ($(cat "$tmp/health"))"
pass "healthz ($code) -> $(cat "$tmp/health")"

# The frontend shell is served (public), so a researcher gets the app to sign
# into. A "not built" notice would mean the image shipped without the bundle.
code=$(curl -sS -L --retry 3 --retry-delay 2 -o "$tmp/root" -w '%{http_code}' "${BASE_URL}/") || fail "app: curl failed"
[ "$code" = "200" ] || fail "app: GET / returned $code, want 200"
grep -q 'not built into this binary' "$tmp/root" && fail "app: the frontend bundle is missing from the image"
grep -qi 'assets/index' "$tmp/root" || fail "app: GET / did not serve the built app shell"
pass "app shell served ($code)"

# The gate must be fail-closed: an unauthenticated operator route is refused,
# not served. This proves the production build is not running the dev bypass.
#
# The probe must be a route that still EXISTS. It used to be /reproductions,
# which 006f removed — and because the SPA catch-all serves the app shell for any
# unmatched GET, the check then read 200 and reported an open gate on a release
# whose gate was fine. So probe /projects (a live operator route) and require the
# JSON refusal, not merely a status: an HTML shell with a 403 would be a
# different bug, and a future removal must fail loudly rather than pass quietly.
code=$(curl -sS -L --retry 3 --retry-delay 2 -o "$tmp/gated" -w '%{http_code}' "${BASE_URL}/projects") || fail "gate: curl failed"
case "$code" in
  401|403) : ;;
  *) fail "gate: /projects returned $code, expected 401/403 — the dev bypass may be enabled in prod" ;;
esac
grep -q '"error"' "$tmp/gated" || fail "gate: /projects refused with $code but did not return the JSON error — is the route still registered?"
pass "gate is fail-closed on /projects ($code)"

if [ -n "$OUTPUT_MD" ]; then
  {
    echo "## Release smoke — replichai ${TAG}"
    echo
    echo "- commit: \`${COMMIT}\`"
    [ -n "$DEPLOY_URL" ] && echo "- deploy: ${DEPLOY_URL}"
    echo "- \`GET ${BASE_URL}/healthz\` → 200 \`$(cat "$tmp/health")\`"
    echo "- \`GET ${BASE_URL}/projects\` → ${code} (fail-closed gate, no dev bypass)"
  } > "$OUTPUT_MD"
fi

pass "release smoke complete"
