# Plan: On-Chain BTC Paywall POC

> Recorded for posterity. This is the as-implemented plan for the third
> proof-of-concept, which replaces the Lightning payment backend with direct
> on-chain Bitcoin payments via bitcoind JSON-RPC.

## Context

The first two POCs both use Lightning (L402) as the payment layer. This third POC demonstrates that the `PaymentVerifier` interface is genuinely backend-agnostic by substituting a completely different payment primitive — on-chain Bitcoin — without touching the proxy core. The trade-off is deliberate: on-chain fees make per-request micropayments economically impractical at small amounts, which is exactly the contrast the POC is designed to expose.

The design question this POC answers: *can the same paywall binary, with only a config change, enforce payment via a fundamentally different mechanism?* Answer: yes.

## Decisions

- **0-conf (mempool detection).** Access is granted as soon as bitcoind sees the transaction in the mempool. No block confirmation is required. This is technically double-spendable but economically irrational at small amounts and appropriate for a POC.
- **Address-as-token.** The token presented in the `Authorization` header is just the Bitcoin address the challenge issued: `BTC-Onchain <address>`. No cryptographic credential is minted — the address itself is the unforgeable reference because only the paywall can look up what was received to it.
- **One payment per request.** Same model as the Lightning POC for direct comparison.
- **Regtest network.** Same local Docker stack. The design is network-agnostic (just change `bitcoind.host` in config).
- **No new Go dependencies.** The bitcoind JSON-RPC client is implemented with `net/http` + `encoding/json`, avoiding version complications from the btcd fork pinned by lnd.
- **Price floor: 1000 sats.** 10 sats (the Lightning POC's price) is below Bitcoin's dust limit for P2WPKH outputs (~294 sats at default fee rates). The on-chain config uses 1000/5000 sats for `/get`/`/post`.
- **Fully standalone — no lnd in the path.** `make up-onchain` starts only `bitcoind + upstream + onchain-paywall`. lnd-server and lnd-client are never started. The e2e test pays via `bitcoin-cli` from a funded regtest wallet (`tester`), not `lncli`. `make setup` (which opens a Lightning channel) is not a prerequisite.

## Interface fix included in this POC

`proxy.go` previously called `lightning.ExtractToken()` directly, violating the CLAUDE.md constraint that the proxy must only talk to the `PaymentVerifier` interface. This POC was the natural forcing function to fix it: `ExtractToken` was added to the interface and the proxy's `lightning` import was removed entirely.

A `WithPrice`/`PriceFromContext` helper was also moved from `internal/payment/lightning/verifier.go` to a new `internal/payment/price.go` so both backends can read the per-route price from context without cross-package coupling.

## Files created

| File | Purpose |
|---|---|
| `internal/payment/price.go` | `WithPrice` / `PriceFromContext` context helpers, now shared by all backends |
| `internal/payment/onchain/client.go` | bitcoind JSON-RPC client (`getnewaddress`, `getreceivedbyaddress`) |
| `internal/payment/onchain/verifier.go` | `OnChainVerifier` implementing `PaymentVerifier` |
| `scripts/setup-onchain.sh` | Creates `paywall` wallet (proxy receiving) and `tester` wallet (e2e test payer); mines 101 blocks to `tester` for coinbase-mature funds |
| `examples/onchain-btc/config.yaml` | Proxy config: `listen_addr: :8092`, bitcoind backend, 1000/5000 sat routes |
| `examples/onchain-btc/scripts/e2e-onchain.sh` | Three-phase end-to-end test |
| `examples/onchain-btc/README.md` | Walkthrough, architecture diagram, security notes |
| `docs/onchain-btc-poc-plan.md` | This file |

## Files modified

| File | Change |
|---|---|
| `internal/payment/verifier.go` | Added `ExtractToken(authHeader string) (string, bool)` to the interface |
| `internal/payment/lightning/verifier.go` | Added `(v *Verifier) ExtractToken` method; replaced local `priceSatsKey` with `payment.PriceFromContext`; removed `WithPrice` / `priceSatsKey` definitions |
| `internal/proxy/proxy.go` | `lightning.ExtractToken` → `h.verifier.ExtractToken`; `lightning.WithPrice` → `payment.WithPrice`; removed `lightning` import entirely |
| `internal/config/config.go` | Changed `Lnd LndConfig` → `Lnd *LndConfig` (pointer, optional); added `Bitcoind *BitcoindConfig`; updated validation to require exactly one backend |
| `cmd/proxy/main.go` | Switch on `cfg.Lnd` vs `cfg.Bitcoind` to instantiate the appropriate verifier |
| `docker-compose.yml` | Added `onchain-paywall` service under `profiles: [onchain]` on port 8092 |
| `Makefile` | Added `up-onchain` (starts only `bitcoind upstream onchain-paywall` — no lnd), `setup-onchain`, `e2e-onchain-test`, `down-onchain`, `clean-onchain` targets |
| `README.md` | Updated project structure, interface docs, added Third POC section, updated Makefile table and config examples |

## Payment flow

```
Client                   Proxy (:8092)              bitcoind
  │                          │                          │
  │── GET /get ───────────►  │                          │
  │                          │── getnewaddress ────────►│
  │                          │◄── bcrt1q... ────────────│
  │◄── 402 ──────────────────│  pending[addr] = 1000   │
  │    BTC-Onchain           │                          │
  │    address="bcrt1q..."   │                          │
  │    amount_sats="1000"    │                          │
  │                          │                          │
  │── bitcoin-cli sendtoaddress (tester wallet) ──────►│ (mempool)
  │                          │                          │
  │── GET /get ───────────►  │                          │
  │   Authorization:         │── getreceivedbyaddress ─►│
  │   BTC-Onchain bcrt1q...  │   (minconf=0)            │
  │                          │◄── 1000 sats ────────────│
  │◄── 200 OK ───────────────│  used[addr] = true       │
```

## Verification

The on-chain POC runs entirely standalone — no Lightning setup required:

```bash
make up-onchain      # starts bitcoind + upstream + onchain-paywall only
make setup-onchain   # creates paywall + tester wallets; mines 101 blocks to tester
make e2e-onchain-test
```

The e2e test runs three phases:

1. **Happy path** — pays 1000 sats to the challenge address from the `tester` wallet via `bitcoin-cli sendtoaddress`, presents the address as the token, asserts 200.
2. **Anti-replay** — presents the same spent address again, asserts 401.
3. **Unpaid address** — gets a fresh challenge address, presents it without paying, asserts 401.

The Lightning POC (`make up && make setup && make e2e-test`) is unaffected — the proxy core is unchanged.

## What this POC will and won't prove

**Will prove:**
- The `PaymentVerifier` interface is genuinely backend-agnostic. Swapping from Lightning to on-chain is a config change plus one new package, with no proxy changes.
- On-chain Bitcoin can enforce the same anti-replay, single-use-token semantics as Lightning.
- The economic reality: on-chain has a practical fee floor (dust limit ~294 sats for P2WPKH) that makes per-request micropayments impractical, unlike Lightning.

**Won't prove:**
- Anything about confirmation security. 0-conf is deliberately chosen for the POC; a production deployment would require 1+ confirmations for meaningful double-spend protection.
- Anything about wallet management. Received sats accumulate in bitcoind's `paywall` wallet. Production would require periodic sweeps via Loop or channel closes.
- Anything about scaling. The `pending` and `used` maps are in-process memory — a restart clears them.
