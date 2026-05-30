# CLAUDE.md

## Project purpose

This is a Bitcoin paywall proxy. The security thesis is that imposing a real economic cost on every HTTP interaction defeats AI-driven adversarial probing at scale — costless iteration cannot bypass physical cost. This is a POC; correctness and architectural clarity matter more than performance optimization.

Four backends are implemented across a 2×2 matrix of payment mechanisms × upstreams:

| | httpbin upstream | Keycloak OIDC upstream |
|---|---|---|
| Lightning L402 | POC 1 (port 8080) | POC 2 (port 8090) |
| On-chain Bitcoin | POC 3 (port 8092) | POC 4 (port 8093) |

### On proof-of-work

Demanding Bitcoin as payment *is* demanding proof-of-work. Every satoshi represents real computational work expended to mine it — this is not a metaphor, it is Bitcoin's consensus mechanism. The paywall's security guarantee is grounded in that physical cost: an attacker cannot iterate cheaply against a gated endpoint because each attempt burns sats that required real energy to produce.

A "proof-of-work backend" in the hashcash sense — where the *client* solves a CPU puzzle directly, without holding Bitcoin — would be a distinct and weaker variant. It imposes cost in compute time rather than money, has no settlement finality, and is more easily parallelized. It is worth noting as an alternative for deployments where requiring the client to hold Bitcoin is undesirable, but it is not more "pure" PoW than Bitcoin payments. The `PaymentVerifier` interface accommodates such a backend, but none is currently implemented.

## Build and test

```bash
make           # print available targets
make build     # compile to bin/proxy
make test      # go test ./...
go vet ./...   # should produce no output
```

See `make help` (or just `make`) for the full list of Docker targets grouped by POC.

## Architecture: the PaymentVerifier seam

The most important design constraint is that `internal/proxy/proxy.go` must only talk to the `payment.PaymentVerifier` interface — never to backend-specific types directly. This is the intentional swap point for switching between Lightning, on-chain, or any future backend.

```
internal/payment/verifier.go              ← interface definition
internal/payment/price.go                 ← shared context helpers (WithPrice / PriceFromContext)
internal/payment/lightning/verifier.go    ← Lightning L402 implementation
internal/payment/onchain/verifier.go      ← on-chain Bitcoin implementation
internal/proxy/proxy.go                   ← calls only the interface
```

When adding features to the proxy layer, keep them backend-agnostic. When adding features specific to a payment backend, keep them inside that backend's package.

The per-request price is passed via context (`payment.WithPrice`) rather than as a function argument, so the interface stays clean across backends that may not use sat-denominated pricing.

## Critical dependency: protobuf replace directive

`go.mod` contains:
```
replace google.golang.org/protobuf => github.com/lightninglabs/protobuf-go-hex-display v1.33.0-hex-display
```

This is required because lnd v0.20.1-beta uses a custom protobuf fork that adds `UseHexForBytes` to `protojson.MarshalOptions`. Do not remove this directive or upgrade `google.golang.org/protobuf` past `v1.33.0` without first verifying lnd's own `go.mod` has moved off the fork. Running `go mod tidy` will preserve it correctly.

## lnd gRPC client (Lightning backend)

`internal/payment/lightning/client.go` wraps two lnd RPC calls:
- `AddInvoice` — creates a new invoice; returns BOLT11 payment request + 32-byte payment hash
- `LookupInvoice` — checks whether an invoice has been settled

Authentication to lnd uses two mechanisms stacked via gRPC options:
1. TLS (self-signed cert from lnd's data directory)
2. Macaroon (`admin.macaroon` as hex in the `macaroon` gRPC metadata key)

In Docker Compose, both credentials come from the `lnd-server-data` volume, mounted read-only into the proxy container at `/lnd`.

## bitcoind JSON-RPC client (on-chain backend)

`internal/payment/onchain/client.go` uses plain `net/http` + `encoding/json` to call two bitcoind RPC methods against the `paywall` named wallet:
- `getnewaddress` — generates a fresh P2WPKH address per request
- `getreceivedbyaddress` — checks total received (minconf=0 for mempool)

No new Go dependencies are introduced; this avoids version complications from the btcd fork pinned by lnd.

The on-chain POCs (3 and 4) do not require lnd. `make up-onchain` and `make up-onchain-keycloak` start only `bitcoind` plus the services each POC needs.

## L402 token format (Lightning backend)

Tokens are `base64(macaroon) + ":" + hex(preimage)`.

The macaroon identifier is `hex(paymentHash)`. Verification steps (all three must pass):
1. `SHA256(preimage) == paymentHash` extracted from the macaroon ID
2. `lnd.LookupInvoice(paymentHash).State == SETTLED`
3. `paymentHash` not in the in-memory `used` map (anti-replay)

The root key used to mint macaroons is generated randomly at startup and held in memory. Tokens issued by one process instance are not valid after a restart. This is intentional for the POC; persistence would be a production concern.

## On-chain token format (on-chain backend)

Tokens are a bare Bitcoin address: `BTC-Onchain <address>`.

The proxy issues one fresh address per request and stores it in an in-memory `pending` map alongside the required amount. Verification steps (all three must pass):
1. Address is in the `pending` map (was issued by this process)
2. `getreceivedbyaddress(address, minconf=0) >= required sats`
3. Address not in the in-memory `used` map (anti-replay)

Same in-memory caveat as Lightning: state is lost on restart.

## Docker Compose topology

Services with no profile are started by `make up`. Profile-gated services are started only by their respective `make up-*` target.

| Service | Profile | Host ports | Notes |
|---|---|---|---|
| `bitcoind` | — | 18443 | regtest; shared by all POCs |
| `lnd-server` | — | 10009, 8081 | Lightning proxy's node |
| `lnd-client` | — | 10010, 8082 | test payer for POCs 1 & 2 |
| `upstream` | — | — | httpbin; used by POCs 1 & 3 |
| `proxy` | — | 8080 | POC 1: Lightning + httpbin |
| `keycloak` | `keycloak`, `onchain-keycloak` | 8091 | shared by POCs 2 & 4 |
| `keycloak-paywall` | `keycloak` | 8090 | POC 2: Lightning + Keycloak |
| `onchain-paywall` | `onchain` | 8092 | POC 3: on-chain + httpbin |
| `onchain-keycloak-paywall` | `onchain-keycloak` | 8093 | POC 4: on-chain + Keycloak |

## Regtest setup scripts

- `scripts/setup-regtest.sh` — mines blocks, funds lnd nodes, opens a Lightning channel. Required for POCs 1 and 2. Not idempotent on channel open; use `make clean && make up && make setup` for a full reset.
- `scripts/setup-onchain.sh` — creates the `paywall` (receiving) and `tester` (e2e test payer) wallets in bitcoind; mines 101 blocks to `tester` for coinbase-mature funds. Required for POCs 3 and 4. Safe to re-run.

Both scripts require `jq` on the host.

## What this project is not

- Not a production system — no persistent token store, no rate limiting, no metrics
- Not a general-purpose API gateway — routing is path-prefix only, no auth passthrough
- Not a wallet — the proxy never holds or moves funds directly; payment logic is delegated to lnd (Lightning backend) or bitcoind (on-chain backend)
