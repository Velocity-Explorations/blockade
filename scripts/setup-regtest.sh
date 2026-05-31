#!/usr/bin/env bash
# Initializes a fresh regtest environment:
#   - mines 101 blocks so both nodes have spendable coinbase outputs
#   - funds lnd-client via on-chain send
#   - connects the two lnd peers
#   - opens a channel from lnd-client → lnd-server
#   - mines 6 blocks to confirm the channel
#
# Run once after `make up`. Safe to re-run (idempotent checks included).
set -euo pipefail

BTC_CLI="docker compose exec -T bitcoind bitcoin-cli -regtest -rpcuser=bitcoin -rpcpassword=bitcoin"
LND_SERVER="docker compose exec -T lnd-server lncli --network=regtest"
LND_CLIENT="docker compose exec -T lnd-client lncli --network=regtest"

log() { echo "[setup] $*"; }

# ---------------------------------------------------------------------------
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

wait_for_lnd() {
  local svc=$1
  wait_until "$svc to be ready" 40 3 "$svc" \
    "docker compose exec -T $svc lncli --network=regtest getinfo"
}

wait_for_chain_sync() {
  local svc=$1
  wait_until "$svc to sync to chain tip" 60 2 "$svc" \
    "[ \"\$(docker compose exec -T $svc lncli --network=regtest getinfo 2>/dev/null | jq -r .synced_to_chain)\" = true ]"
}

# ---------------------------------------------------------------------------
wait_until "bitcoind" 20 3 bitcoind "$BTC_CLI getblockchaininfo"

wait_for_lnd lnd-server
wait_for_lnd lnd-client

# ---------------------------------------------------------------------------
log "Getting lnd-server mining address..."
SERVER_ADDR=$($LND_SERVER newaddress p2wkh | jq -r '.address')
log "lnd-server address: $SERVER_ADDR"

log "Mining 101 blocks to lnd-server (coinbase maturity)..."
$BTC_CLI generatetoaddress 101 "$SERVER_ADDR" > /dev/null

wait_for_chain_sync lnd-server

wait_until "lnd-server on-chain balance" 30 2 lnd-server \
  "[ \"\$($LND_SERVER walletbalance | jq -r .confirmed_balance)\" -gt 0 ]"

# ---------------------------------------------------------------------------
log "Getting lnd-client address..."
CLIENT_ADDR=$($LND_CLIENT newaddress p2wkh | jq -r '.address')
log "lnd-client address: $CLIENT_ADDR"

log "Sending 1 BTC to lnd-client..."
$LND_SERVER sendcoins --addr="$CLIENT_ADDR" --amt=100000000 > /dev/null

log "Mining 1 block to confirm..."
$BTC_CLI generatetoaddress 1 "$SERVER_ADDR" > /dev/null

wait_for_chain_sync lnd-server
wait_for_chain_sync lnd-client

wait_until "lnd-client on-chain balance" 30 2 lnd-client \
  "[ \"\$($LND_CLIENT walletbalance | jq -r .confirmed_balance)\" -gt 0 ]"

# ---------------------------------------------------------------------------
log "Connecting lnd-client → lnd-server..."
SERVER_PUBKEY=$($LND_SERVER getinfo | jq -r '.identity_pubkey')
# lnd-server is reachable at its service name on P2P port 9735
$LND_CLIENT connect "${SERVER_PUBKEY}@lnd-server:9735" 2>/dev/null || true
log "lnd-server pubkey: $SERVER_PUBKEY"

# ---------------------------------------------------------------------------
log "Opening channel: lnd-client → lnd-server (500k sats capacity)..."
$LND_CLIENT openchannel \
  --node_key="$SERVER_PUBKEY" \
  --local_amt=500000 > /dev/null

log "Mining 6 blocks to confirm channel..."
$BTC_CLI generatetoaddress 6 "$SERVER_ADDR" > /dev/null

wait_until "Lightning channel to become active" 40 3 lnd-client \
  "[ \"\$($LND_CLIENT listchannels | jq '[.channels[] | select(.active == true)] | length')\" -gt 0 ]"

# ---------------------------------------------------------------------------
log ""
log "=============================="
log " Regtest setup complete!"
log "=============================="
log ""
log "lnd-server pubkey : $SERVER_PUBKEY"
log "Channel capacity  : 500,000 sats"
log ""
log "To test the paywall:"
log "  1. curl -v http://localhost:8080/get"
log "     → 402 + WWW-Authenticate: L402 macaroon=\"...\", invoice=\"lnbc...\""
log ""
log "  2. Pay the invoice from lnd-client:"
log "     docker compose exec lnd-client lncli --network=regtest payinvoice <bolt11>"
log "     → note the payment_preimage"
log ""
log "  3. Retry with token:"
log "     curl -H 'Authorization: L402 <macaroon>:<preimage>' http://localhost:8080/get"
log "     → 200 OK"
