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

log "Waiting for bitcoind..."
for i in $(seq 1 20); do
  if $BTC getblockchaininfo >/dev/null 2>&1; then
    log "bitcoind is ready"
    break
  fi
  sleep 1
  [ "$i" = 20 ] && { log "ERROR: bitcoind not ready after 20s"; exit 1; }
done

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
  log "Waiting for tester balance to confirm..."
  until [ "$($BTC -rpcwallet=tester getbalance 2>/dev/null)" != "0.00000000" ]; do sleep 1; done
  log "Tester wallet funded."
else
  log "Tester wallet already has balance ($BALANCE BTC)."
fi

log ""
log "Setup complete. Ready to run: make e2e-onchain-test"
