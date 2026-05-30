# Plan: On-Chain BTC + Keycloak POC

> Recorded for posterity. This is the as-implemented plan for the fourth
> proof-of-concept, which completes the 2×2 matrix of payment backends
> (Lightning / on-chain) × upstreams (httpbin / Keycloak).

## Context

The first three POCs establish three corners of a matrix:

| | httpbin upstream | Keycloak OIDC upstream |
|---|---|---|
| Lightning L402 | POC 1 ✓ | POC 2 ✓ |
| On-chain Bitcoin | POC 3 ✓ | **POC 4 (this one)** |

POC 4 completes the matrix. It demonstrates that both axes — payment backend and upstream service — are independently configurable via the same binary and the same `PaymentVerifier` interface. No new Go code is needed; the implementation is entirely a config file, a Docker Compose service, and an e2e test.

The security story differs from POC 2 in a meaningful way: the attacker must fund on-chain UTXOs and pay miner fees per credential-stuffing attempt rather than maintaining a Lightning wallet. The economic floor is higher and the overhead is greater, at the cost of slower confirmation (mempool vs. instant) and a simpler token scheme (address vs. cryptographic preimage).

## Decisions

All major decisions were inherited from POC 3 (on-chain) and POC 2 (Keycloak):

- **0-conf mempool detection** — same as POC 3
- **Address-as-token** — `BTC-Onchain <address>`, same as POC 3
- **No lnd in the path** — `make up-onchain-keycloak` starts only `bitcoind + keycloak + onchain-keycloak-paywall`
- **Tester wallet for e2e payments** — reuses the `tester` wallet created by `scripts/setup-onchain.sh`; `make setup-onchain-keycloak` is an alias for that script
- **Keycloak realm reuse** — uses the same `realm-export.json` from POC 2 (`examples/keycloak-login/`); mounted into the Keycloak container via docker-compose
- **Port 8093** — completes the port layout: 8080 (POC 1), 8090 (POC 2), 8092 (POC 3), 8093 (POC 4)

## Files created

| File | Purpose |
|---|---|
| `examples/onchain-keycloak/config.yaml` | Proxy config: `listen_addr: :8093`, bitcoind backend, Keycloak token endpoint |
| `examples/onchain-keycloak/scripts/e2e-onchain-keycloak.sh` | Three-phase e2e test |
| `examples/onchain-keycloak/README.md` | Walkthrough and quick start |
| `docs/onchain-keycloak-poc-plan.md` | This file |

## Files modified

| File | Change |
|---|---|
| `docker-compose.yml` | Added `onchain-keycloak` to `keycloak` service's profiles; added `onchain-keycloak-paywall` service on port 8093 |
| `Makefile` | Added `up-onchain-keycloak`, `setup-onchain-keycloak`, `e2e-onchain-keycloak-test`, `down-onchain-keycloak`, `clean-onchain-keycloak` |
| `README.md` | Updated matrix table to 4 POCs; added POC 4 section and Makefile targets |

## Payment flow

```
Client                  Proxy (:8093)           bitcoind        Keycloak
  │                         │                      │               │
  │── POST /token ───────►  │                      │               │
  │                         │── getnewaddress ───► │               │
  │◄── 402 ─────────────────│                      │               │
  │    BTC-Onchain          │                      │               │
  │    address="bcrt1q..."  │                      │               │
  │    amount_sats="1000"   │                      │               │
  │                         │                      │               │
  │── bitcoin-cli sendtoaddress (tester) ────────► │ (mempool)     │
  │                         │                      │               │
  │── POST /token ───────►  │                      │               │
  │   BTC-Onchain bcrt1q... │── getreceivedbyaddress (minconf=0) ► │
  │   grant_type=password   │◄── 1000 sats ────────│               │
  │   username=alice        │─────────── POST /token ────────────► │
  │                         │◄─────────── 200 + JWT ───────────────│
  │◄── 200 + JWT ───────────│                      │               │
```

The proxy strips the `BTC-Onchain` Authorization header before forwarding to Keycloak, so Keycloak sees a clean credential-only POST.

## Verification

```bash
make up-onchain-keycloak       # starts bitcoind + Keycloak + proxy on :8093
make setup-onchain-keycloak    # creates tester wallet, mines 101 blocks
make e2e-onchain-keycloak-test
```

Three phases:

1. **Happy path** — pays 1000 sats to the challenge address, presents `BTC-Onchain <address>`, Keycloak returns 200 + JWT
2. **Wrong creds** — pays a second address, submits wrong password → 401 from Keycloak (`invalid_grant`); proves the request reached Keycloak and the token was consumed despite failure
3. **Anti-replay** — replays the spent address → 401 from proxy (`invalid or already-used payment token`); body is plain text, not Keycloak JSON, distinguishing the failure mode

## What this POC will and won't prove

**Will prove:**
- The `PaymentVerifier` interface fully decouples payment backend from upstream. Both axes of the matrix are independently configurable with no code changes.
- The credential-stuffing defense from POC 2 holds with an on-chain backend: every attempt — successful or failed — burns a token.
- On-chain Bitcoin can gate a real-world authentication surface without any Lightning infrastructure.

**Won't prove:**
- Anything beyond what POC 2 and POC 3 individually prove. This POC's value is demonstrating the combination works, not introducing new security properties.
- Anything about on-chain confirmation security (0-conf, same as POC 3).
- Anything about wallet management or production deployment.
