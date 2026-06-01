#!/usr/bin/env bash
set -euo pipefail

# Sets up a standalone on-chain regtest environment:
#   - Creates the "paywall" wallet (used by the proxy to receive payments)
#   - Creates the "tester" wallet (used by the e2e test to send payments)
#   - Mines 101 blocks to "tester" so it has spendable coinbase outputs
#
# Does NOT require lnd-server or lnd-client to be running.

log() { printf '[setup-onchain] %s\n' "$*"; }

BTC="docker compose exec -T bitcoind bitcoin-cli -regtest -rpcuser=bitcoin -rpcpassword=bitcoin"

# wait_until <description> <attempts> <delay-secs> <log-service> <condition>
# Polls <condition> (re-evaluated each iteration) until it succeeds, up to
# <attempts> times with <delay-secs> between tries. On timeout it dumps the
# recent logs from <log-service> and exits 1, so a stuck dependency fails
# loudly instead of hanging forever. Pass "" for <log-service> to skip the
# log dump.
wait_until() {
  local desc=$1 attempts=$2 delay=$3 svc=$4 cond=$5
  local i=1
  log "Waiting for ${desc}..."
  until eval "$cond" >/dev/null 2>&1; do
    if [ "$i" -ge "$attempts" ]; then
      log "ERROR: timed out after ~$((attempts * delay))s waiting for ${desc}"
      if [ -n "$svc" ]; then
        log "---- recent logs from ${svc} ----"
        docker compose logs --tail=50 "$svc" 2>&1 | sed 's/^/    /' || true
      fi
      exit 1
    fi
    i=$((i + 1))
    sleep "$delay"
  done
  log "${desc} ready"
}

wait_until "bitcoind" 20 1 bitcoind "$BTC getblockchaininfo"

# ---------------------------------------------------------------------------
# Wallet helper: load if exists on disk, create if not, skip if already loaded.
# ---------------------------------------------------------------------------
ensure_wallet() {
  local name="$1"
  if $BTC -rpcwallet="$name" getwalletinfo >/dev/null 2>&1; then
    log "Wallet '$name' already loaded."
    return
  fi
  if $BTC loadwallet "$name" >/dev/null 2>&1; then
    log "Wallet '$name' loaded from disk."
    return
  fi
  log "Creating wallet '$name'..."
  $BTC createwallet "$name" >/dev/null
  log "Wallet '$name' created."
}

ensure_wallet "paywall"
ensure_wallet "tester"

# ---------------------------------------------------------------------------
# Fund the tester wallet if it has no confirmed balance.
# 101 blocks satisfies coinbase maturity (100 confirmations required).
# ---------------------------------------------------------------------------
BALANCE=$($BTC -rpcwallet=tester getbalance 2>/dev/null || echo "0")
if [ "$BALANCE" = "0.00000000" ] || [ "$BALANCE" = "0" ]; then
  log "Mining 101 blocks to 'tester' wallet for test funds..."
  TESTER_ADDR=$($BTC -rpcwallet=tester getnewaddress)
  $BTC generatetoaddress 101 "$TESTER_ADDR" >/dev/null
  wait_until "tester wallet balance to confirm" 30 1 bitcoind \
    "[ \"\$($BTC -rpcwallet=tester getbalance)\" != 0.00000000 ]"
  log "Tester wallet funded."
else
  log "Tester wallet already has balance ($BALANCE BTC)."
fi

log ""
log "Setup complete. Ready to run: make e2e-onchain-test"
