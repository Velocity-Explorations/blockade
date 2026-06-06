#!/usr/bin/env bash
set -euo pipefail

PROXY_URL="${PROXY_URL:-http://localhost:8092}"

log()  { printf '[e2e-onchain] %s\n' "$*"; }
fail() { printf '[e2e-onchain] FAIL: %s\n' "$*" >&2; exit 1; }

for cmd in curl jq docker; do
  command -v "$cmd" >/dev/null 2>&1 || fail "missing dependency: $cmd"
done

# ---------------------------------------------------------------------------
# Helper: parse address and amount_sats from a 402 WWW-Authenticate header.
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
# Helper: send on-chain BTC from the bitcoind "tester" wallet to an address.
# Uses bitcoin-cli directly — no lnd required.
# ---------------------------------------------------------------------------
send_onchain() {
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

###############################################################################
# Phase 1 — happy path: pay address, present it as token, get 200.
###############################################################################
log "Phase 1: happy path (valid on-chain payment)"

log "  step 1/3: GET /get without token (expect 402)"
resp=$(curl -sS -i "${PROXY_URL}/get")
parse_challenge "$resp"
log "  got 402 (address ${ADDRESS}, amount ${AMOUNT_SATS} sats)"

log "  step 2/3: send ${AMOUNT_SATS} sats to ${ADDRESS} from tester wallet"
send_onchain "$ADDRESS" "$AMOUNT_SATS"
log "  payment broadcast to mempool"

log "  step 3/3: GET /get with BTC-Onchain token (expect 200)"
status=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: BTC-Onchain ${ADDRESS}" \
  "${PROXY_URL}/get")
[ "$status" = "200" ] || fail "expected 200, got '$status'"
log "  got 200 — upstream responded"
log "PASS — on-chain payment accepted"

###############################################################################
# Phase 2 — anti-replay: presenting the same address again must fail.
###############################################################################
log "Phase 2: anti-replay (re-using a spent address)"

status=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: BTC-Onchain ${ADDRESS}" \
  "${PROXY_URL}/get")
[ "$status" = "401" ] || fail "expected 401 on replay, got '$status'"
log "  got 401 — address already spent"
log "PASS — anti-replay enforced"

###############################################################################
# Phase 3 — unpaid address: a fresh address with no payment must fail.
###############################################################################
log "Phase 3: unpaid address (no payment sent)"

log "  step 1/2: GET /get to obtain a fresh challenge address"
resp=$(curl -sS -i "${PROXY_URL}/get")
parse_challenge "$resp"
log "  got 402 (unpaid address ${ADDRESS})"

log "  step 2/2: present address without paying (expect 401)"
status=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: BTC-Onchain ${ADDRESS}" \
  "${PROXY_URL}/get")
[ "$status" = "401" ] || fail "expected 401 for unpaid address, got '$status'"
log "  got 401 — no payment found"
log "PASS — unpaid address rejected"

###############################################################################
# Phase 4 — restart persistence: pending address survives proxy restart.
###############################################################################
log "Phase 4: restart persistence (SQLite pending store)"

log "  step 1/4: GET /get to obtain a fresh challenge address"
resp=$(curl -sS -i "${PROXY_URL}/get")
parse_challenge "$resp"
log "  got 402 (address ${ADDRESS}, amount ${AMOUNT_SATS} sats)"

log "  step 2/4: restart the proxy container"
docker compose restart onchain-paywall >/dev/null 2>&1
# Wait for the proxy to accept connections again (up to 30s).
for i in $(seq 1 30); do
  code=$(curl -sS -o /dev/null -w '%{http_code}' "${PROXY_URL}/get" 2>/dev/null || true)
  [ "$code" = "402" ] && break
  sleep 1
done
[ "$code" = "402" ] || fail "proxy did not recover after restart"
log "  proxy back up after restart"

log "  step 3/4: send ${AMOUNT_SATS} sats to ${ADDRESS} from tester wallet"
send_onchain "$ADDRESS" "$AMOUNT_SATS"
log "  payment broadcast to mempool"

log "  step 4/4: GET /get with pre-restart address token (expect 200)"
status=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: BTC-Onchain ${ADDRESS}" \
  "${PROXY_URL}/get")
[ "$status" = "200" ] || fail "expected 200 after restart, got '$status' — pending entry lost"
log "  got 200 — pending entry survived restart"
log "PASS — SQLite persistence confirmed"

log ""
log "ALL PHASES PASSED."
log "  - Phase 1: paid address token -> 200"
log "  - Phase 2: replay of spent address -> 401"
log "  - Phase 3: unpaid address token -> 401"
log "  - Phase 4: pending address survives proxy restart -> 200"
