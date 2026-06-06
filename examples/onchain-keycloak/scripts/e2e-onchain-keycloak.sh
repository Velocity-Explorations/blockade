#!/usr/bin/env bash
set -euo pipefail

PROXY_URL="${PROXY_URL:-http://localhost:8093}"
KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:8091}"
REALM="${REALM:-btc-paywall}"
CLIENT_ID="${CLIENT_ID:-paywall-demo}"
USERNAME="${USERNAME:-alice}"
PASSWORD="${PASSWORD:-correct-horse-battery-staple}"
TOKEN_PATH="/realms/${REALM}/protocol/openid-connect/token"

log()  { printf '[e2e-onchain-keycloak] %s\n' "$*"; }
fail() { printf '[e2e-onchain-keycloak] FAIL: %s\n' "$*" >&2; exit 1; }

for cmd in curl jq docker awk; do
  command -v "$cmd" >/dev/null 2>&1 || fail "missing dependency: $cmd"
done

# ---------------------------------------------------------------------------
# Pre-flight: wait for Keycloak realm import (can take 20-30s on first boot).
# ---------------------------------------------------------------------------
log "waiting for Keycloak realm '${REALM}' to be ready..."
for i in $(seq 1 60); do
  if curl -fsS "${KEYCLOAK_URL}/realms/${REALM}/.well-known/openid-configuration" >/dev/null 2>&1; then
    log "Keycloak ready (after ${i}s)"
    break
  fi
  sleep 1
  [ "$i" = 60 ] && fail "Keycloak realm did not become ready within 60s"
done

# ---------------------------------------------------------------------------
# Helper: parse BTC-Onchain address and amount_sats from a 402 response.
# Populates globals ADDRESS and AMOUNT_SATS.
# ---------------------------------------------------------------------------
parse_challenge() {
  local resp="$1"
  local status
  status=$(printf '%s\n' "$resp" | head -n1 | awk '{print $2}')
  [ "$status" = "402" ] || fail "expected 402, got '$status'"

  local www_auth
  www_auth=$(printf '%s\n' "$resp" | tr -d '\r' \
    | awk -F': ' 'tolower($1)=="www-authenticate"{sub(/^[^:]+: */,""); print; exit}')
  [ -n "$www_auth" ] || fail "no WWW-Authenticate header in 402 response"

  ADDRESS=$(printf '%s' "$www_auth" | sed -n 's/.*address="\([^"]*\)".*/\1/p')
  AMOUNT_SATS=$(printf '%s' "$www_auth" | sed -n 's/.*amount_sats="\([^"]*\)".*/\1/p')
  [ -n "$ADDRESS" ]     || fail "could not parse address from: $www_auth"
  [ -n "$AMOUNT_SATS" ] || fail "could not parse amount_sats from: $www_auth"
}

# ---------------------------------------------------------------------------
# Helper: pay an address from the bitcoind "tester" wallet. No lnd required.
# ---------------------------------------------------------------------------
pay_address() {
  local addr="$1"
  local sats="$2"
  local btc_amount
  btc_amount=$(awk "BEGIN { printf \"%.8f\", $sats / 100000000 }")
  docker compose exec -T bitcoind \
    bitcoin-cli -regtest -rpcuser=bitcoin -rpcpassword=bitcoin \
    -rpcwallet=tester \
    sendtoaddress "$addr" "$btc_amount" \
    >/dev/null 2>&1 \
    || fail "sendtoaddress $addr $btc_amount BTC failed"
}

body=$(mktemp); trap 'rm -f "$body"' EXIT

###############################################################################
# Phase 1 — happy path: pay address, submit valid credentials, get a JWT.
###############################################################################
log "Phase 1: happy path (valid credentials)"

log "  step 1/3: POST creds without token (expect 402)"
resp=$(curl -sS -i -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=${PASSWORD}")
parse_challenge "$resp"
log "  got 402 (address ${ADDRESS}, amount ${AMOUNT_SATS} sats)"

log "  step 2/3: pay ${AMOUNT_SATS} sats to ${ADDRESS} from tester wallet"
pay_address "$ADDRESS" "$AMOUNT_SATS"
log "  payment broadcast to mempool"

log "  step 3/3: re-POST with BTC-Onchain token (expect 200 + JWT)"
status=$(curl -sS -o "$body" -w '%{http_code}' -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -H "Authorization: BTC-Onchain ${ADDRESS}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=${PASSWORD}")
[ "$status" = "200" ] || { cat "$body" >&2; fail "expected 200, got '$status'"; }
jq -e '.access_token | type=="string" and length > 100' "$body" >/dev/null \
  || { cat "$body" >&2; fail "response does not contain a plausible access_token"; }
log "  got 200 with a valid-looking access_token"
log "PASS — Keycloak issued a JWT after on-chain payment"

###############################################################################
# Phase 2 — failed-login attempt also consumes the token.
###############################################################################
log "Phase 2: failed login (wrong password) STILL consumes the on-chain token"

log "  step 1/3: POST WRONG creds without token (expect 402)"
resp=$(curl -sS -i -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=wrong-password-on-purpose")
parse_challenge "$resp"
ADDRESS2="$ADDRESS"

log "  step 2/3: pay second address"
pay_address "$ADDRESS2" "$AMOUNT_SATS"
log "  payment broadcast to mempool"

log "  step 3/3: submit WRONG creds with valid token (expect 401 from Keycloak)"
status=$(curl -sS -o "$body" -w '%{http_code}' -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -H "Authorization: BTC-Onchain ${ADDRESS2}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=wrong-password-on-purpose")
[ "$status" = "401" ] || { cat "$body" >&2; fail "expected 401 from Keycloak, got '$status'"; }
jq -e '.error == "invalid_grant"' "$body" >/dev/null \
  || { cat "$body" >&2; fail "expected Keycloak's invalid_grant body"; }
log "  got 401 with Keycloak's invalid_grant — credentials reached Keycloak"

###############################################################################
# Phase 3 — replaying the spent address is rejected by the proxy.
###############################################################################
log "Phase 3: replaying the just-spent address (expect 401 from proxy)"

status=$(curl -sS -o "$body" -w '%{http_code}' -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -H "Authorization: BTC-Onchain ${ADDRESS2}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=${PASSWORD}")
[ "$status" = "401" ] || { cat "$body" >&2; fail "expected 401 from proxy on replay, got '$status'"; }
if jq -e '.error' "$body" >/dev/null 2>&1; then
  cat "$body" >&2
  fail "got Keycloak-style JSON — address was NOT rejected by the proxy"
fi
grep -q 'invalid or already-used payment token' "$body" \
  || { cat "$body" >&2; fail "expected proxy's anti-replay message"; }
log "  got 401 from proxy — address already spent"

log "PASS — failed-login attempt also consumed the token (anti-replay confirmed)"

###############################################################################
# Phase 4 — restart persistence: pending address survives proxy restart.
###############################################################################
log "Phase 4: restart persistence (SQLite pending store)"

log "  step 1/4: POST creds without token (expect 402)"
resp=$(curl -sS -i -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=${PASSWORD}")
parse_challenge "$resp"
log "  got 402 (address ${ADDRESS}, amount ${AMOUNT_SATS} sats)"

log "  step 2/4: restart the proxy container"
docker compose restart onchain-keycloak-paywall >/dev/null 2>&1
for i in $(seq 1 30); do
  code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${PROXY_URL}${TOKEN_PATH}" \
    -d "grant_type=password" -d "client_id=${CLIENT_ID}" \
    -d "username=${USERNAME}" -d "password=${PASSWORD}" 2>/dev/null || true)
  [ "$code" = "402" ] && break
  sleep 1
done
[ "$code" = "402" ] || fail "proxy did not recover after restart"
log "  proxy back up after restart"

log "  step 3/4: pay ${AMOUNT_SATS} sats to ${ADDRESS} from tester wallet"
pay_address "$ADDRESS" "$AMOUNT_SATS"
log "  payment broadcast to mempool"

log "  step 4/4: re-POST with pre-restart address token (expect 200 + JWT)"
status=$(curl -sS -o "$body" -w '%{http_code}' -X POST "${PROXY_URL}${TOKEN_PATH}" \
  -H "Authorization: BTC-Onchain ${ADDRESS}" \
  -d "grant_type=password" \
  -d "client_id=${CLIENT_ID}" \
  -d "username=${USERNAME}" \
  -d "password=${PASSWORD}")
[ "$status" = "200" ] || { cat "$body" >&2; fail "expected 200 after restart, got '$status' — pending entry lost"; }
jq -e '.access_token | type=="string" and length > 100' "$body" >/dev/null \
  || { cat "$body" >&2; fail "response does not contain a plausible access_token"; }
log "  got 200 with valid access_token — pending entry survived restart"
log "PASS — SQLite persistence confirmed"

log ""
log "ALL PHASES PASSED."
log "  - Phase 1: valid creds + paid address -> 200 + JWT"
log "  - Phase 2: invalid creds + paid address -> 401 from Keycloak (token still spent)"
log "  - Phase 3: replayed address -> 401 from proxy (anti-replay)"
log "  - Phase 4: pending address survives proxy restart -> 200 + JWT"
