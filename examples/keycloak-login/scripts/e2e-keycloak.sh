#!/usr/bin/env bash
set -euo pipefail

PROXY_URL="${PROXY_URL:-http://localhost:8090}"
KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:8091}"
REALM="${REALM:-btc-paywall}"
CLIENT_ID="${CLIENT_ID:-paywall-demo}"
USERNAME="${USERNAME:-alice}"
PASSWORD="${PASSWORD:-correct-horse-battery-staple}"
TOKEN_PATH="/realms/${REALM}/protocol/openid-connect/token"

log()  { printf '[e2e-keycloak] %s\n' "$*"; }
fail() { printf '[e2e-keycloak] FAIL: %s\n' "$*" >&2; exit 1; }

for cmd in curl jq docker; do
  command -v "$cmd" >/dev/null 2>&1 || fail "missing dependency: $cmd"
done

# ---------------------------------------------------------------------------
# Pre-flight: wait for Keycloak realm import to complete (can take 20-30s on
# first boot). We poll Keycloak directly on 8091, not through the paywall.
# ---------------------------------------------------------------------------
log "waiting for Keycloak realm '${REALM}' to be ready..."
for i in $(seq 1 60); do
  if curl -fsS "${KEYCLOAK_URL}/realms/${REALM}/.well-known/openid-configuration" >/dev/null 2>&1; then
    log "Keycloak ready (after ${i}s)"
    break
  fi
  sleep 1
  if [ "$i" = 60 ]; then fail "Keycloak realm did not become ready within 60s"; fi
done

# Helper: extract the L402 macaroon and invoice from a 402 response into
# globals MACAROON and INVOICE. Asserts the response was 402.
parse_challenge() {
  local resp="$1"
  local status
  status=$(printf '%s\n' "$resp" | head -n1 | awk '{print $2}')
  [ "$status" = "402" ] || fail "expected 402, got '$status'"

  local www_auth
  www_auth=$(printf '%s\n' "$resp" | tr -d '\r' \
    | awk -F': ' 'tolower($1)=="www-authenticate"{sub(/^[^:]+: */,""); print; exit}')
  [ -n "$www_auth" ] || fail "no WWW-Authenticate header in 402 response"

  MACAROON=$(printf '%s' "$www_auth" | sed -n 's/.*macaroon="\([^"]*\)".*/\1/p')
  INVOICE=$(printf '%s'  "$www_auth" | sed -n 's/.*invoice="\([^"]*\)".*/\1/p')
  [ -n "$MACAROON" ] || fail "could not parse macaroon from: $www_auth"
  [ -n "$INVOICE"  ] || fail "could not parse invoice from: $www_auth"
}

# Helper: pay an invoice from lnd-client, return the preimage.
pay_invoice() {
  local invoice="$1"
  local out
  out=$(docker compose exec -T lnd-client \
    lncli --network=regtest payinvoice --force --json "$invoice")
  local preimage
  preimage=$(printf '%s\n' "$out" | jq -rs 'last | .payment_preimage // empty')
  [ -n "$preimage" ] || { printf '%s\n' "$out" >&2; fail "no payment_preimage in payinvoice output"; }
  printf '%s' "$preimage"
}

###############################################################################
# Phase 1 — happy path: pay, submit valid credentials, get a JWT.
###############################################################################
log "Phase 1: happy path (valid credentials)"

log "  step 1/3: POST creds without L402 token (expect 402)"
resp=$(curl -sS -i -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=${PASSWORD}")
parse_challenge "$resp"
log "  got 402 (macaroon ${#MACAROON}B, invoice ${INVOICE:0:25}...)"

log "  step 2/3: pay invoice from lnd-client"
PREIMAGE=$(pay_invoice "$INVOICE")
log "  payment settled (preimage ${PREIMAGE:0:16}...)"

log "  step 3/3: re-POST with L402 token (expect 200 + JWT)"
body=$(mktemp); trap 'rm -f "$body"' EXIT
status=$(curl -sS -o "$body" -w '%{http_code}' -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -H "Authorization: L402 ${MACAROON}:${PREIMAGE}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=${PASSWORD}")
[ "$status" = "200" ] || { cat "$body" >&2; fail "expected 200, got '$status'"; }
jq -e '.access_token | type=="string" and length > 100' "$body" >/dev/null \
  || { cat "$body" >&2; fail "response does not contain a plausible access_token"; }
log "  got 200 with a valid-looking access_token"
log "PASS — Keycloak issued a JWT after L402 payment"

###############################################################################
# Phase 2 — failed-login attempt also consumes the token.
###############################################################################
log "Phase 2: failed login (wrong password) STILL consumes the L402 token"

log "  step 1/3: POST WRONG creds without token (expect 402)"
resp=$(curl -sS -i -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=wrong-password-on-purpose")
parse_challenge "$resp"

log "  step 2/3: pay second invoice"
PREIMAGE2=$(pay_invoice "$INVOICE")
MACAROON2="$MACAROON"

log "  step 3/3: submit WRONG creds with valid L402 token (expect 401 from Keycloak)"
status=$(curl -sS -o "$body" -w '%{http_code}' -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -H "Authorization: L402 ${MACAROON2}:${PREIMAGE2}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=wrong-password-on-purpose")
[ "$status" = "401" ] || { cat "$body" >&2; fail "expected 401 from Keycloak, got '$status'"; }
# Body is JSON from Keycloak with "error":"invalid_grant" — proves request
# reached Keycloak, not just stopped at the proxy.
jq -e '.error == "invalid_grant"' "$body" >/dev/null \
  || { cat "$body" >&2; fail "expected Keycloak's invalid_grant body, got something else"; }
log "  got 401 with Keycloak's invalid_grant — credentials reached Keycloak"

###############################################################################
# Phase 3 — same L402 token replayed -> proxy rejects (anti-replay).
###############################################################################
log "Phase 3: replaying the just-spent L402 token (expect 401 from proxy)"

status=$(curl -sS -o "$body" -w '%{http_code}' -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -H "Authorization: L402 ${MACAROON2}:${PREIMAGE2}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=${PASSWORD}")
[ "$status" = "401" ] || { cat "$body" >&2; fail "expected 401 from proxy on replay, got '$status'"; }
# Body is plain text from the proxy, NOT Keycloak JSON — distinguishes the
# failure mode (token rejected) from "credentials rejected".
if jq -e '.error' "$body" >/dev/null 2>&1; then
  cat "$body" >&2
  fail "got Keycloak-style JSON body — token was NOT rejected by the proxy"
fi
grep -q 'invalid or already-used payment token' "$body" \
  || { cat "$body" >&2; fail "expected proxy's anti-replay message, got something else"; }
log "  got 401 from proxy with anti-replay message"

log "PASS — failed-login attempt also consumed the token (anti-replay confirmed)"
log ""
log "ALL PHASES PASSED."
log "  - Phase 1: valid creds + paid token -> 200 + JWT"
log "  - Phase 2: invalid creds + paid token -> 401 from Keycloak (token still spent)"
log "  - Phase 3: replayed token -> 401 from proxy (anti-replay)"
