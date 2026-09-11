#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: AGPL-3.0-or-later

#
# Post-deploy release smoke for replichai.latere.ai.
#
# replichai's gate is fail-closed: the operator surface admits nobody without a
# session from the OIDC login at /login and /callback. This script holds no
# credential, so it never signs in, and it checks what a release must prove
# without one: /healthz answers, which shows the pod is up and migrations
# applied; the app shell is served; an operator route refuses an anonymous
# request with the JSON error, which shows the dev bypass did not reach
# production; and the addresses a machine reads, namely robots.txt, the sitemap,
# a 404 and a published page, answer as themselves. An authenticated check would
# need a service credential the pipeline does not hold.
#
# Required tools: curl.
#
# Env (pipeline contract):
#   BASE_URL    default https://replichai.latere.ai
#   TAG         release tag, for evidence output
#   COMMIT      release commit sha, for evidence output
#   DEPLOY_URL  deploy workflow URL, for evidence output
#   OUTPUT_MD   optional path for markdown evidence
# EXPECTED_ASSET and SERVICE_TOKEN are accepted but unused: the build mode is
# dockerfile and the checks are unauthenticated.

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

# --retry covers the transient 5xx the load balancer returns during a rollout cutover.
code=$(curl -sS -L --retry 5 --retry-delay 2 --retry-max-time 30 \
  -o "$tmp/health" -w '%{http_code}' "${BASE_URL}/healthz") || fail "healthz: curl failed"
[ "$code" = "200" ] || fail "healthz: expected 200, got $code (body: $(head -c 200 "$tmp/health" 2>/dev/null || true))"
grep -q '"status":"ok"' "$tmp/health" || fail "healthz: body is not ok ($(cat "$tmp/health"))"
pass "healthz ($code) -> $(cat "$tmp/health")"

# The frontend shell is public, so a researcher gets the app to sign into. A
# "not built" notice would mean the image shipped without the bundle.
code=$(curl -sS -L --retry 3 --retry-delay 2 -o "$tmp/root" -w '%{http_code}' "${BASE_URL}/") || fail "app: curl failed"
[ "$code" = "200" ] || fail "app: GET / returned $code, want 200"
grep -q 'not built into this binary' "$tmp/root" && fail "app: the frontend bundle is missing from the image"
grep -qi 'assets/index' "$tmp/root" || fail "app: GET / did not serve the built app shell"
pass "app shell served ($code)"

# The gate must be fail-closed: an unauthenticated operator route is refused,
# not served. This proves the production build is not running the dev bypass.
#
# The probe must be a route that still exists. It used to be /reproductions,
# which 006f removed. The SPA catch-all serves the app shell for any unmatched
# GET, so the check then read 200 and reported an open gate on a release whose
# gate was fine. The probe is now /projects, a live operator route, and it
# requires the JSON refusal rather than only a status: an HTML shell with a 403
# would be a different bug, and a future removal must fail loudly rather than
# pass quietly.
code=$(curl -sS -L --retry 3 --retry-delay 2 -o "$tmp/gated" -w '%{http_code}' "${BASE_URL}/projects") || fail "gate: curl failed"
case "$code" in
  401|403) : ;;
  *) fail "gate: /projects returned $code, expected 401/403 — the dev bypass may be enabled in prod" ;;
esac
grep -q '"error"' "$tmp/gated" || fail "gate: /projects refused with $code but did not return the JSON error — is the route still registered?"
pass "gate is fail-closed on /projects ($code)"

# The addresses a non-browser client reads (042). Each of these was broken
# before that spec: robots.txt and sitemap.xml answered with the app, a shared
# link to a published reproduction previewed as the homepage, and every mistyped
# address answered 200 with the dashboard.
code=$(curl -sS -L --retry 3 --retry-delay 2 -o "$tmp/robots" -w '%{http_code}' "${BASE_URL}/robots.txt") || fail "robots: curl failed"
[ "$code" = "200" ] || fail "robots.txt returned $code, want 200"
grep -q '<!doctype' "$tmp/robots" && fail "robots.txt is being served the app shell"
grep -q "^Sitemap: ${BASE_URL}/sitemap.xml$" "$tmp/robots" || fail "robots.txt does not name the sitemap"
pass "robots.txt served ($code)"

code=$(curl -sS -L --retry 3 --retry-delay 2 -o "$tmp/sitemap" -w '%{http_code}' "${BASE_URL}/sitemap.xml") || fail "sitemap: curl failed"
[ "$code" = "200" ] || fail "sitemap.xml returned $code, want 200"
grep -q '<urlset' "$tmp/sitemap" || fail "sitemap.xml is not a urlset (is the SPA answering it?)"
grep -q "<loc>${BASE_URL}/registry</loc>" "$tmp/sitemap" || fail "sitemap.xml does not list the registry"
pass "sitemap served ($code, $(grep -c '<loc>' "$tmp/sitemap") urls)"

# An unknown address answers 404 rather than 200 with the dashboard.
code=$(curl -sS -o "$tmp/miss" -w '%{http_code}' "${BASE_URL}/this-page-does-not-exist-$$") || fail "404: curl failed"
[ "$code" = "404" ] || fail "an unknown address returned $code, want 404"
pass "an unknown address is 404"

# A published project page carries its own head. The id comes from the sitemap,
# so this checks whatever is published rather than a fixture that can go stale.
# A deployment with nothing public yet skips it.
public_url=$(grep -o "<loc>${BASE_URL}/p/[^<]*</loc>" "$tmp/sitemap" | head -1 | sed 's/<[^>]*>//g' || true)
if [ -n "$public_url" ]; then
  curl -sS -L --retry 3 --retry-delay 2 -o "$tmp/page" "$public_url" || fail "public page: curl failed"
  grep -q "<link rel=\"canonical\" href=\"${public_url}\"" "$tmp/page" || fail "$public_url does not carry its own canonical"
  grep -q 'does the paper actually reproduce?' "$tmp/page" && fail "$public_url is still served the default head"
  grep -q 'application/ld+json' "$tmp/page" || fail "$public_url carries no structured data"
  pass "a published page describes itself ($public_url)"
else
  pass "no public project to check (nothing published yet)"
fi

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
