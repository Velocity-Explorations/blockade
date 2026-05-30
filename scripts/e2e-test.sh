#!/usr/bin/env bash
set -euo pipefail

PROXY_URL="${PROXY_URL:-http://localhost:8080}"
TEST_PATH="${TEST_PATH:-/get}"

log() { printf '[e2e] %s\n' "$*"; }
fail() { printf '[e2e] FAIL: %s\n' "$*" >&2; exit 1; }

for cmd in curl jq docker; do
  command -v "$cmd" >/dev/null 2>&1 || fail "missing dependency: $cmd"
done

log "Step 1/3: GET ${PROXY_URL}${TEST_PATH} (expect 402)"
resp=$(curl -sS -i "${PROXY_URL}${TEST_PATH}")
status=$(printf '%s\n' "$resp" | head -n1 | awk '{print $2}')
[ "$status" = "402" ] || fail "expected 402, got '$status'"

www_auth=$(printf '%s\n' "$resp" | tr -d '\r' \
  | awk -F': ' 'tolower($1)=="www-authenticate"{sub(/^[^:]+: */,""); print; exit}')
[ -n "$www_auth" ] || fail "no WWW-Authenticate header in 402 response"

macaroon=$(printf '%s' "$www_auth" | sed -n 's/.*macaroon="\([^"]*\)".*/\1/p')
invoice=$(printf '%s' "$www_auth"  | sed -n 's/.*invoice="\([^"]*\)".*/\1/p')
[ -n "$macaroon" ] || fail "could not parse macaroon from: $www_auth"
[ -n "$invoice"  ] || fail "could not parse invoice from: $www_auth"
log "got challenge (macaroon ${#macaroon}B, invoice ${invoice:0:25}...)"

log "Step 2/3: paying invoice from lnd-client"
# Retry up to 5 times — the channel may be active before the routing graph
# has fully propagated, causing the first payment attempt to fail.
pay_out=""
for attempt in 1 2 3 4 5; do
  if pay_out=$(docker compose exec -T lnd-client \
      lncli --network=regtest payinvoice --force --json "$invoice" 2>&1); then
    break
  fi
  [ "$attempt" = "5" ] && { printf '%s\n' "$pay_out" >&2; fail "payinvoice failed after 5 attempts"; }
  log "  attempt $attempt failed, retrying in 5s..."
  sleep 5
done
preimage=$(printf '%s\n' "$pay_out" | jq -rs 'last | .payment_preimage // empty')
[ -n "$preimage" ] || { printf '%s\n' "$pay_out" >&2; fail "no payment_preimage in payinvoice output"; }
log "payment settled (preimage ${preimage:0:16}...)"

log "Step 3/3: GET ${PROXY_URL}${TEST_PATH} with L402 token (expect 200)"
body=$(mktemp)
trap 'rm -f "$body"' EXIT
final=$(curl -sS -o "$body" -w '%{http_code}' \
  -H "Authorization: L402 ${macaroon}:${preimage}" \
  "${PROXY_URL}${TEST_PATH}")
[ "$final" = "200" ] || { cat "$body" >&2; fail "expected 200, got '$final'"; }

log "PASS — proxy returned 200 after L402 payment"
log "first 200 bytes of upstream response:"
head -c 200 "$body"; echo