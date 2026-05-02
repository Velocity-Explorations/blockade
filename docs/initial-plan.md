# BTC Paywall — Implementation Plan

## Context

The goal is a Bitcoin-anchored access control mechanism that imposes real economic cost on
every interaction with a protected resource. This defeats AI-driven adversarial probing at
scale by replacing costless logical verification with verifiable payment. The L402 protocol
(HTTP 402 + Lightning invoices + macaroon tokens) is the most mature implementation of this
model. We are building a standalone reverse proxy that speaks L402, with a clean
`PaymentVerifier` abstraction so the payment backend can be swapped without touching the
proxy core.

POC scope: local regtest environment (bitcoind + two lnd nodes), protecting httpbin as a
stand-in upstream.

---

## Tech Stack

| Layer | Choice | Reason |
|---|---|---|
| Language | Go | lnd is Go-native; stdlib has production-grade reverse proxy; single binary deploy |
| Lightning node | lnd (lightninglabs/lnd) | Dominant implementation; gRPC client is first-class Go |
| Node transport | gRPC (`github.com/lightningnetwork/lnd/lnrpc`) | Type-safe; maintained by lnd team |
| Macaroon library | `gopkg.in/macaroon.v2` | What lnd uses internally; no extra abstraction layer |
| Local infra | Docker Compose | bitcoind (regtest) + lnd-server + lnd-client + upstream + proxy |
| Upstream (POC) | kennethreitz/httpbin | Simple HTTP testing service; easy to verify forwarded requests |
| Config | YAML + `gopkg.in/yaml.v3` | Minimal dependencies; human-readable |

---

## Project Structure

```
btc-paywall/
├── cmd/
│   └── proxy/
│       └── main.go                  # wires config → verifier → proxy → ListenAndServe
├── internal/
│   ├── config/
│   │   └── config.go                # YAML loader; Config, RouteConfig, LndConfig structs
│   ├── payment/
│   │   ├── verifier.go              # PaymentVerifier interface (IssueChallenge, VerifyProof)
│   │   └── lightning/
│   │       ├── client.go            # lnd gRPC connection (TLS + macaroon auth)
│   │       ├── verifier.go          # LightningVerifier: AddInvoice → 402; LookupInvoice → verify
│   │       └── token.go             # L402 token: encode/decode <base64(macaroon)>:<hex(preimage)>
│   └── proxy/
│       └── proxy.go                 # http.Handler: extract token → call verifier → httputil.ReverseProxy
├── docker/
│   ├── bitcoin/
│   │   └── bitcoin.conf             # regtest config
│   └── lnd/
│       └── lnd.conf                 # shared lnd config template
├── scripts/
│   └── setup-regtest.sh             # mine blocks, fund nodes, open channel, wait for ready
├── config.example.yaml              # annotated example config
├── docker-compose.yml
├── Makefile                         # up, down, setup, logs, clean targets
├── go.mod
└── go.sum
```

---

## Core Abstraction

```go
// internal/payment/verifier.go
type PaymentVerifier interface {
    // IssueChallenge writes a 402 response with WWW-Authenticate header.
    IssueChallenge(w http.ResponseWriter, r *http.Request) error
    // VerifyProof validates an Authorization: L402 <token> value.
    VerifyProof(token string) (bool, error)
}
```

The proxy calls only this interface. `LightningVerifier` is the first implementation.
Future backends (PoW, on-chain) implement the same two methods.

---

## L402 Flow

```
Client                       Proxy (Go)                    lnd-server           Upstream
  |                              |                              |                    |
  |-- GET /protected ----------->|                              |                    |
  |                              |-- AddInvoice(priceSats) ---->|                    |
  |                              |<-- paymentHash, payReq ------|                    |
  |                              |  mint macaroon(paymentHash)  |                    |
  |<-- 402 + WWW-Authenticate ---|                              |                    |
  |    L402 macaroon=".."        |                              |                    |
  |         invoice="lnbc.."    |                              |                    |
  |                              |                              |                    |
  |  [client pays invoice]       |                              |                    |
  |  [gets preimage back]        |                              |                    |
  |                              |                              |                    |
  |-- GET /protected ----------->|                              |                    |
  |   Authorization: L402        |                              |                    |
  |   <macaroon>:<preimage>      |                              |                    |
  |                              |  decode token                |                    |
  |                              |  SHA256(preimage)==hash? ✓   |                    |
  |                              |-- LookupInvoice(hash) ------>|                    |
  |                              |<-- settled=true -------------|                    |
  |                              |--------------------------------- GET /get -------->|
  |<-- 200 ----------------------|<----------------------------------------------- |
```

**Token format**: `base64(macaroon) + ":" + hex(preimage)` — no database required.
Verification is purely: preimage hashes to the payment hash in the macaroon identifier,
and lnd confirms the invoice is settled.

---

## Docker Compose Services

```yaml
services:
  bitcoind:     # regtest Bitcoin node
  lnd-server:   # proxy's Lightning node (Alice) — issues invoices, receives payments
  lnd-client:   # test payer node (Bob) — used in setup script and manual testing
  upstream:     # kennethreitz/httpbin — the "protected" service
  proxy:        # our Go binary
```

`lnd-client` is a test fixture. In production, the client is external (a wallet or agent).

---

## Config Shape (`config.example.yaml`)

```yaml
listen_addr: ":8080"

lnd:
  host: "lnd-server:10009"
  tls_cert_path: "/lnd/tls.cert"
  macaroon_path: "/lnd/data/chain/bitcoin/regtest/admin.macaroon"

routes:
  - path_prefix: "/get"
    upstream: "http://upstream:80"
    price_sats: 10
  - path_prefix: "/post"
    upstream: "http://upstream:80"
    price_sats: 50
```

Price is per-request for the POC. Time-windowed tokens are a natural follow-on.

---

## Implementation Sequence

1. **Go module + skeleton** — `go mod init`, create all directories and empty files
2. **Config** — `internal/config/config.go`: load YAML, validate required fields
3. **lnd gRPC client** — `internal/payment/lightning/client.go`: TLS dial, macaroon
   credential, expose `AddInvoice` and `LookupInvoice` wrappers
4. **L402 token** — `internal/payment/lightning/token.go`: encode/decode, preimage
   verification (`sha256(preimage) == paymentHash`)
5. **LightningVerifier** — `internal/payment/lightning/verifier.go`: implement
   `IssueChallenge` (create invoice → mint macaroon → write 402 headers) and
   `VerifyProof` (decode token → check hash → lookup invoice settled)
6. **Proxy handler** — `internal/proxy/proxy.go`: extract `Authorization: L402` header,
   call verifier, forward via `httputil.ReverseProxy` on success
7. **main.go** — load config, connect to lnd, wire verifier into proxy, serve
8. **Docker Compose + configs** — bitcoind.conf (regtest), lnd.conf, Compose file
9. **Setup script** — `scripts/setup-regtest.sh`: wait for nodes, create wallets, mine
   101 blocks, connect peers, open channel, mine 6 confirmation blocks
10. **Makefile** — `make up`, `make setup`, `make down`, `make logs`

---

## Key Go Dependencies

```
github.com/lightningnetwork/lnd/lnrpc   # generated gRPC client (invoices, lookup)
google.golang.org/grpc                   # gRPC transport
gopkg.in/macaroon.v2                     # macaroon construction/verification
gopkg.in/yaml.v3                         # config loading
```

No dependency on the full lnd binary — only the generated protobuf/gRPC types.

---

## Verification (End-to-End)

```bash
# 1. Start all services
make up

# 2. Initialize regtest (mine blocks, open channel) — run once
make setup

# 3. Hit the protected endpoint — expect 402
curl -v http://localhost:8080/get
# → HTTP/1.1 402 Payment Required
# → WWW-Authenticate: L402 macaroon="...", invoice="lnbc..."

# 4. Pay the invoice from the client node
docker exec btc-paywall-lnd-client-1 lncli --network=regtest payinvoice <bolt11>
# → payment_preimage: <hex>

# 5. Retry with the L402 token
curl -H "Authorization: L402 <macaroon>:<preimage>" http://localhost:8080/get
# → 200 OK from httpbin

# 6. Replay the same token — should be rejected (invoice already consumed)
curl -H "Authorization: L402 <macaroon>:<preimage>" http://localhost:8080/get
# → 401 Unauthorized
```

Step 6 validates that token reuse is blocked — a critical security property.
