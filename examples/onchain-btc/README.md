# On-Chain BTC Paywall POC

This POC demonstrates the same paywall concept as the base Lightning POC but using **on-chain Bitcoin payments** instead of Lightning invoices.

## How it differs from Lightning

| | Lightning (base POC) | On-Chain (this POC) |
|---|---|---|
| Payment | BOLT11 invoice | Bitcoin address |
| Settlement | Off-chain channel balance shift | On-chain transaction (mempool) |
| Confirmation | Instant (preimage reveal) | 0-conf (mempool detection) |
| Fee overhead | ~1 sat routing fee | On-chain miner fee (~140 vbytes) |
| Requires lnd | Yes | No — talks directly to bitcoind |
| Token format | `L402 <macaroon>:<preimage>` | `BTC-Onchain <address>` |

The key point: on-chain fees make per-request micropayments economically absurd at small amounts, which is exactly what this POC is designed to highlight. Lightning wins for micropayments; on-chain makes sense for larger one-time charges.

## Architecture

```
Client                  Proxy (:8092)            bitcoind
  │                         │                       │
  │── GET /get ──────────►  │                       │
  │                         │── getnewaddress ────► │
  │◄── 402 ─────────────────│                       │
  │    WWW-Authenticate:    │                       │
  │    BTC-Onchain          │                       │
  │    address="bcrt1q..."  │                       │
  │    amount_sats="1000"   │                       │
  │                         │                       │
  │── sendtoaddress ────────────────────────────►   │ (mempool)
  │   (bitcoin-cli, tester wallet)                  │
  │                         │                       │
  │── GET /get ──────────►  │                       │
  │   Authorization:        │── getreceivedbyaddress│
  │   BTC-Onchain bcrt1q... │   (minconf=0) ──────► │
  │                         │◄── 1000 sats ─────────│
  │◄── 200 OK ──────────────│                       │
```

The proxy never holds funds. It only calls two bitcoind RPC methods:
- `getnewaddress` — generate a fresh address per request (into the `paywall` wallet)
- `getreceivedbyaddress` — check mempool+confirmed receipts (minconf=0)

## Running the POC

This POC is **fully standalone** — it does not require lnd-server, lnd-client, or the Lightning channel setup (`make setup`). Only bitcoind needs to be running.

```bash
# Start only what's needed: bitcoind + httpbin + onchain-paywall
make up-onchain

# Create wallets and mine 101 regtest blocks for test funds (run once)
make setup-onchain

# Run the end-to-end test
make e2e-onchain-test
```

## Manual walkthrough

```bash
# 1. Request a resource — get a 402 with a payment address
curl -i http://localhost:8092/get
# HTTP/1.1 402 Payment Required
# WWW-Authenticate: BTC-Onchain address="bcrt1q...", amount_sats="1000"

# 2. Pay from the bitcoind "tester" wallet (no lnd needed)
docker compose exec bitcoind \
  bitcoin-cli -regtest -rpcuser=bitcoin -rpcpassword=bitcoin \
  -rpcwallet=tester sendtoaddress bcrt1q... 0.00001000

# 3. Present the address as the token
curl -H "Authorization: BTC-Onchain bcrt1q..." http://localhost:8092/get
# HTTP/1.1 200 OK

# 4. Replay the same address — proxy rejects it
curl -H "Authorization: BTC-Onchain bcrt1q..." http://localhost:8092/get
# HTTP/1.1 401 Unauthorized
```

## Security notes (POC limitations)

- **0-conf**: payments are accepted from the mempool before block confirmation. A double-spend is technically possible but economically irrational for small amounts.
- **In-memory state**: the `pending` map (address → required sats) and `used` set live only in process memory. A restart clears them — addresses issued before a restart cannot be spent after it.
- **No on-chain sweep**: received sats accumulate in bitcoind's `paywall` wallet. In production, use Loop or periodic wallet sweeps to move funds to cold storage.
