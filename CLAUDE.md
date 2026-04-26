# CLAUDE.md

## Project purpose

This is a Bitcoin Lightning paywall proxy implementing the L402 protocol. The security thesis is that imposing a real economic cost (via Lightning micropayments) on every HTTP interaction defeats AI-driven adversarial probing at scale — costless iteration cannot bypass physical cost. This is a POC; correctness and architectural clarity matter more than performance optimization.

## Build and test

```bash
make build        # compile to bin/proxy
make test         # go test ./...
go vet ./...      # should produce no output
```

The full runtime environment requires Docker:
```bash
make up           # build image + start bitcoind, lnd-server, lnd-client, httpbin, proxy
make setup        # one-time regtest init: mine blocks, fund nodes, open channel
make down         # stop (volumes preserved)
make clean        # stop + delete volumes (full reset)
```

## Architecture: the PaymentVerifier seam

The most important design constraint is that `internal/proxy/proxy.go` must only talk to the `payment.PaymentVerifier` interface — never to Lightning-specific types directly. This is the intentional swap point for future backends (proof-of-work, on-chain, hybrid).

```
internal/payment/verifier.go          ← interface definition (do not add Lightning imports here)
internal/payment/lightning/verifier.go ← first implementation
internal/proxy/proxy.go               ← calls only the interface
```

When adding features to the proxy layer, keep them backend-agnostic. When adding features to Lightning-specific behavior, keep them inside `internal/payment/lightning/`.

The price for a request is passed via context (`lightning.WithPrice`) rather than as a function argument, so the interface stays clean when other backends don't use sat-denominated pricing.

## Critical dependency: protobuf replace directive

`go.mod` contains:
```
replace google.golang.org/protobuf => github.com/lightninglabs/protobuf-go-hex-display v1.33.0-hex-display
```

This is required because lnd v0.20.1-beta uses a custom protobuf fork that adds `UseHexForBytes` to `protojson.MarshalOptions`. Do not remove this directive or upgrade `google.golang.org/protobuf` past `v1.33.0` without first verifying lnd's own `go.mod` has moved off the fork. Running `go mod tidy` will preserve it correctly.

## lnd gRPC client

`internal/payment/lightning/client.go` wraps two lnd RPC calls:
- `AddInvoice` — creates a new invoice; returns BOLT11 payment request + 32-byte payment hash
- `LookupInvoice` — checks whether an invoice has been settled

Authentication to lnd uses two mechanisms stacked via gRPC options:
1. TLS (self-signed cert from lnd's data directory)
2. Macaroon (`admin.macaroon` as hex in the `macaroon` gRPC metadata key)

In Docker Compose, both credentials come from the `lnd-server-data` volume, which is mounted read-only into the proxy container at `/lnd`.

## L402 token format

Tokens are `base64(macaroon) + ":" + hex(preimage)`.

The macaroon identifier is `hex(paymentHash)`. Verification steps (all three must pass):
1. `SHA256(preimage) == paymentHash` extracted from the macaroon ID
2. `lnd.LookupInvoice(paymentHash).State == SETTLED`
3. `paymentHash` not in the in-memory `used` map (anti-replay)

The root key used to mint macaroons is generated randomly at startup and held in memory. Tokens issued by one process instance are not valid after a restart. This is intentional for the POC; persistence would be a production concern.

## Docker Compose topology

| Service | Internal ports | Host ports | Notes |
|---|---|---|---|
| `bitcoind` | 18443 (RPC), 28332/28333 (ZMQ) | 18443 | regtest |
| `lnd-server` | 10009 (gRPC), 8080 (REST), 9735 (P2P) | 10009, 8081 | proxy's node |
| `lnd-client` | 10009 (gRPC), 8080 (REST), 9736 (P2P) | 10010, 8082 | test payer |
| `upstream` | 80 | — | httpbin, not exposed to host |
| `proxy` | 8080 | 8080 | our binary |

`lnd-server` and `proxy` share the `lnd-server-data` named volume. The proxy mounts it read-only at `/lnd` to read `tls.cert` and `admin.macaroon`.

## Regtest setup script

`scripts/setup-regtest.sh` uses `docker compose exec -T` to run `bitcoin-cli` and `lncli` commands inside containers. It is safe to re-run but is not fully idempotent — running it a second time will attempt to open a second channel. Use `make clean && make up && make setup` for a full reset.

The script requires `jq` on the host.

## What this project is not

- Not a production system — no persistent token store, no rate limiting, no metrics
- Not a general-purpose API gateway — routing is path-prefix only, no auth passthrough
- Not a wallet — the proxy never holds or moves funds directly; all payment logic is delegated to lnd
