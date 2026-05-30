# btc-paywall

A proof-of-concept HTTP reverse proxy that gates access to protected resources behind a real Bitcoin payment. Every request to a protected endpoint must be accompanied by cryptographic proof that a real economic cost was paid — no free probing, no costless iteration.

Three payment backends are implemented in this repository, each as a self-contained POC:

| POC | Backend | Upstream | Token scheme | Port |
|---|---|---|---|---|
| 1 | Lightning L402 | httpbin | `L402 <macaroon>:<preimage>` | 8080 |
| 2 | Lightning L402 | Keycloak OIDC | `L402 <macaroon>:<preimage>` | 8090 |
| 3 | On-chain Bitcoin | httpbin | `BTC-Onchain <address>` | 8092 |
| 4 | On-chain Bitcoin | Keycloak OIDC | `BTC-Onchain <address>` | 8093 |

All four run from the same compiled binary; the active backend is selected by config.

## Why

AI-driven adversaries can now iterate against logical access controls (permissions, identity checks, rule-based filters) at near-zero marginal cost. The asymmetry is structural: defense costs continue to rise while attack costs approach zero.

The only constraint that cannot be bypassed through intelligence is physical cost. This proxy shifts the access control question from *"who are you?"* to *"what did it cost you to be here?"* — imposing a real, configurable economic cost on every interaction, making large-scale automated probing prohibitively expensive.

## Project Structure

```
btc-paywall/
│
├── cmd/proxy/main.go                   # Entry point — wires config, verifier,
│                                       # and proxy together, then serves
│
├── internal/
│   ├── config/
│   │   └── config.go                   # YAML config loader and validation
│   │                                   # Supports both lnd and bitcoind backends
│   │
│   ├── payment/
│   │   ├── verifier.go                 # PaymentVerifier interface — the only seam
│   │   │                               # between the proxy and any payment backend.
│   │   │                               # Swap this to change from Lightning to
│   │   │                               # on-chain, PoW, or hybrid.
│   │   ├── price.go                    # Context helpers shared by all backends
│   │   │                               # (WithPrice / PriceFromContext)
│   │   │
│   │   ├── lightning/
│   │   │   ├── client.go               # lnd gRPC client (AddInvoice, LookupInvoice)
│   │   │   ├── token.go                # L402 token encode/decode and preimage check
│   │   │   └── verifier.go             # LightningVerifier: issues 402 challenges
│   │   │                               # and validates tokens against lnd
│   │   │
│   │   └── onchain/
│   │       ├── client.go               # bitcoind JSON-RPC client (getnewaddress,
│   │       │                           # getreceivedbyaddress) — no new dependencies
│   │       └── verifier.go             # OnChainVerifier: issues 402 challenges with
│   │                                   # a Bitcoin address; validates 0-conf mempool
│   │
│   └── proxy/
│       └── proxy.go                    # http.Handler: route matching → verifier →
│                                       # httputil.ReverseProxy forward on success
│
├── docker/
│   └── bitcoin/bitcoin.conf            # bitcoind regtest configuration
│
├── scripts/
│   ├── setup-regtest.sh                # One-time regtest initialization: mines
│   │                                   # blocks, funds nodes, opens a channel
│   └── setup-onchain.sh                # Creates the bitcoind "paywall" wallet
│                                       # required by the on-chain POC
│
├── examples/
│   ├── keycloak-login/                 # POC 2: L402 in front of Keycloak OIDC
│   ├── onchain-btc/                    # POC 3: on-chain Bitcoin, httpbin upstream
│   └── onchain-keycloak/               # POC 4: on-chain Bitcoin, Keycloak upstream
│
├── docs/
│   ├── e2e-walkthrough.md              # In-depth walkthrough of the POC 1 e2e test
│   ├── keycloak-poc-plan.md            # Design rationale for POC 2
│   ├── onchain-btc-poc-plan.md         # Design rationale for POC 3
│   └── onchain-keycloak-poc-plan.md    # Design rationale for POC 4
│
├── config.yaml                         # POC 1 config (pre-wired for Docker Compose)
├── docker-compose.yml                  # Full local stack (all four POCs)
├── Dockerfile                          # Multi-stage Go build → alpine runtime
└── Makefile                            # Developer workflow targets
```

### Key design decision: `PaymentVerifier`

```go
type PaymentVerifier interface {
    IssueChallenge(w http.ResponseWriter, r *http.Request) error
    VerifyProof(token string) (bool, error)
    ExtractToken(authHeader string) (string, bool)
}
```

The proxy (`internal/proxy/proxy.go`) calls only this interface — it has no imports from any backend package. `IssueChallenge` writes the 402 response with whatever challenge the backend requires. `VerifyProof` validates the credential. `ExtractToken` parses the backend-specific `Authorization` scheme so the proxy stays scheme-agnostic.

The active backend is selected at startup based on which config section (`lnd` or `bitcoind`) is present in the config file.

---

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and [Docker Compose](https://docs.docker.com/compose/) (v2)
- `jq` (used by the setup and e2e scripts)
- Go 1.22+ (only needed for local development outside Docker)

## Infrastructure Requirements

The base stack runs five containers simultaneously.

**Per-service RAM profile:**

| Service | RAM | Notes |
|---|---|---|
| `bitcoind` | ~300 MB | regtest — no chain to sync, starts immediately |
| `lnd-server` | ~300 MB | briefly CPU-intensive at wallet init |
| `lnd-client` | ~300 MB | same |
| `httpbin` | ~150 MB | Python + Gunicorn |
| `proxy` | ~75 MB | lightweight Go binary |
| Docker daemon + OS | ~400 MB | baseline |
| **Total** | **~1.5 GB** | |

**Disk:**

`docker-compose.yml` uses `build: .`, so `make up` runs a full Go build inside Docker on first run. The builder downloads all of lnd's transitive dependencies before compiling.

| Item | Size |
|---|---|
| Docker images (all services) | ~1 GB |
| Docker build cache (Go deps + compilation) | ~3–4 GB |
| lnd / bitcoind volume data (regtest) | < 100 MB |
| OS + Docker install | ~3 GB |
| **Total** | **~8 GB used** |

**Recommended VM sizing:**

| Resource | Minimum | Comfortable |
|---|---|---|
| vCPU | 2 | 2 |
| RAM | 4 GB | 8 GB |
| Disk | 20 GB | 40 GB |

Two vCPUs matter during `make setup`: lnd wallet initialization on both nodes runs concurrently and briefly saturates a single core.

---

## POC 1: Lightning L402 Paywall

The base POC. The proxy enforces the [L402 protocol](https://docs.lightning.engineering/the-lightning-network/l402): every request must carry a Lightning payment preimage as proof of expenditure.

```
Client                     btc-paywall proxy              lnd node           Upstream
  |                               |                           |                   |
  |-- GET /protected ------------>|                           |                   |
  |                               |-- AddInvoice(N sats) ---->|                   |
  |                               |<-- paymentHash, bolt11 ---|                   |
  |<-- 402 Payment Required ------|                           |                   |
  |    WWW-Authenticate: L402     |                           |                   |
  |    macaroon="...",            |                           |                   |
  |    invoice="lnbc..."          |                           |                   |
  |                               |                           |                   |
  |  [client pays invoice]        |                           |                   |
  |  [receives preimage]          |                           |                   |
  |                               |                           |                   |
  |-- GET /protected ------------>|                           |                   |
  |   Authorization: L402         |                           |                   |
  |   <macaroon>:<preimage>       |                           |                   |
  |                               |  SHA256(preimage)==hash?  |                   |
  |                               |-- LookupInvoice --------->|                   |
  |                               |<-- settled: true  --------|                   |
  |                               |-------------------------------- GET /protected>|
  |<-- 200 OK --------------------|<----------------------------------------------|
```

Tokens are single-use. Replaying a used token returns `401 Unauthorized`.

### Quick start

```bash
make up        # build image, start bitcoind + lnd-server + lnd-client + httpbin + proxy
make setup     # one-time: mine blocks, fund nodes, open Lightning channel
make e2e-test  # 402 → pay invoice → retry with token → assert 200
```

### Manual walkthrough

> For a deeper, code-referenced explanation of what each step proves, see [`docs/e2e-walkthrough.md`](docs/e2e-walkthrough.md).

**Step 1 — Hit a protected endpoint without a token:**

```bash
curl -v http://localhost:8080/get
```

Expected response:
```
HTTP/1.1 402 Payment Required
WWW-Authenticate: L402 macaroon="<base64>", invoice="lnbc..."
```

**Step 2 — Pay the invoice from the client node:**

```bash
docker compose exec lnd-client lncli --network=regtest payinvoice --force <bolt11-invoice>
```

The command prints a `payment_preimage` hex string on success.

**Step 3 — Retry with the L402 token:**

```bash
curl -v http://localhost:8080/get \
  -H "Authorization: L402 <macaroon>:<preimage>"
```

Expected response: `200 OK` with the httpbin JSON body.

**Step 4 — Confirm anti-replay:**

Repeat the same curl from Step 3. Expected response: `401 Unauthorized` — the token is single-use.

### Tear down

```bash
make down    # stop containers, preserve data volumes
make clean   # stop containers AND delete volumes (full reset)
```

---

## POC 2: Keycloak Login Paywall

Puts the same L402 paywall in front of Keycloak's OIDC token endpoint. The compelling defense case: **every credential submission costs sats, including failed ones**. Credential stuffing is no longer free even against an attacker who guesses wrong every time.

This POC reuses the existing proxy binary (one binary, two configs) and runs under a Docker Compose `keycloak` profile, leaving the base stack untouched. `make up && make setup` must be run first.

See [`examples/keycloak-login/README.md`](examples/keycloak-login/README.md) for the full walkthrough and [`docs/keycloak-poc-plan.md`](docs/keycloak-poc-plan.md) for the design rationale.

### Quick start

```bash
make up && make setup    # base stack (if not already running)
make up-keycloak         # adds Keycloak + a second proxy instance on :8090
make e2e-keycloak-test   # three-phase test: pay → valid creds → 200+JWT;
                         #   pay → wrong creds → 401 from Keycloak;
                         #   replay same token → 401 from proxy
```

### Tear down

```bash
make down-keycloak    # stop Keycloak profile services only
make clean-keycloak   # stop + delete Keycloak volumes
```

---

## POC 3: On-Chain BTC Paywall

Replaces the Lightning backend with direct on-chain Bitcoin payments. The proxy talks to bitcoind via JSON-RPC — no lnd, no channels, no liquidity management. Each request requires a payment to a freshly generated address; access is granted as soon as the transaction appears in the mempool (0-conf).

This POC is deliberately designed to highlight the *contrast* with Lightning: on-chain fees impose a practical price floor (the Bitcoin dust limit is ~294 sats for P2WPKH outputs), making per-request micropayments economically impractical at small amounts. The same `PaymentVerifier` interface accommodates it with no changes to the proxy core — only a new package and a config file.

**This POC runs standalone — it does not require lnd-server, lnd-client, or `make setup`.** The only container it needs is bitcoind. Test payments are sent via `bitcoin-cli` from a funded regtest wallet that `make setup-onchain` creates.

See [`examples/onchain-btc/README.md`](examples/onchain-btc/README.md) for the full walkthrough and [`docs/onchain-btc-poc-plan.md`](docs/onchain-btc-poc-plan.md) for the design rationale.

### Quick start

```bash
make up-onchain          # starts bitcoind + httpbin + onchain-paywall (:8092) only
make setup-onchain       # creates wallets, mines 101 blocks for test funds (run once)
make e2e-onchain-test    # three-phase test: pay address → 200;
                         #   replay spent address → 401;
                         #   unpaid address → 401
```

### Tear down

```bash
make down-onchain    # stop on-chain profile services only
make clean-onchain   # stop + delete on-chain volumes
```

---

## POC 4: On-Chain BTC + Keycloak Login Paywall

Completes the matrix: the on-chain Bitcoin backend in front of Keycloak's OIDC token endpoint. Every credential submission — successful or failed — requires a fresh on-chain Bitcoin payment. No lnd node is needed on the defender's side.

The economic character differs from POC 2 in a meaningful way: the attacker must fund on-chain UTXOs and pay miner fees per attempt rather than maintaining a Lightning wallet. The floor is higher and the friction is greater, at the cost of slower confirmation and a simpler (less cryptographic) token scheme.

See [`examples/onchain-keycloak/README.md`](examples/onchain-keycloak/README.md) for the full walkthrough and [`docs/onchain-keycloak-poc-plan.md`](docs/onchain-keycloak-poc-plan.md) for the design rationale.

### Quick start

```bash
make up-onchain-keycloak        # starts bitcoind + Keycloak + onchain-keycloak-paywall (:8093)
make setup-onchain-keycloak     # creates wallets, mines test funds (run once)
make e2e-onchain-keycloak-test  # three-phase test: valid creds, wrong creds, anti-replay
```

### Tear down

```bash
make down-onchain-keycloak    # stop on-chain+Keycloak profile services only
make clean-onchain-keycloak   # stop + delete volumes
```

---

## Configuration

`config.yaml` controls the proxy. The Docker Compose setup mounts it read-only inside the container. Exactly one backend section (`lnd` or `bitcoind`) must be present.

**Lightning backend** (POC 1 and POC 2):
```yaml
listen_addr: ":8080"

lnd:
  host: "lnd-server:10009"          # lnd gRPC endpoint
  tls_cert_path: "/lnd/tls.cert"    # from the shared lnd-server-data volume
  macaroon_path: "/lnd/data/chain/bitcoin/regtest/admin.macaroon"

routes:
  - path_prefix: "/get"
    upstream: "http://upstream:80"
    price_sats: 10
  - path_prefix: "/post"
    upstream: "http://upstream:80"
    price_sats: 50
```

**On-chain Bitcoin backend** (POC 3):
```yaml
listen_addr: ":8092"

bitcoind:
  host: "bitcoind:18443"            # bitcoind JSON-RPC endpoint
  rpc_user: "bitcoin"
  rpc_password: "bitcoin"

routes:
  - path_prefix: "/get"
    upstream: "http://upstream:80"
    price_sats: 1000                 # higher floor due to on-chain dust limits
  - path_prefix: "/post"
    upstream: "http://upstream:80"
    price_sats: 5000
```

Each route maps a path prefix to an upstream and a per-request price in satoshis. Unmatched paths return `404 Not Found`.

**Rate limiting (optional, any backend):**
```yaml
rate_limit:
  requests_per_second: 5   # sustained token-bucket refill rate per source IP
  burst: 10                # maximum burst above the sustained rate
```

Rate limiting applies only to unauthenticated requests — the ones that generate a 402 challenge. Authenticated requests (those carrying a valid payment token) are never rate-limited. Omit the `rate_limit` section entirely to disable it. Excess requests receive `429 Too Many Requests`.

## Local Development (without Docker)

```bash
make build                     # produces bin/proxy
./bin/proxy -config config.yaml
```

For the Lightning backend, the proxy needs a reachable lnd node. Point it at the Docker Compose `lnd-server` (exposed on `localhost:10009`):

```yaml
lnd:
  host: "localhost:10009"
  tls_cert_path: "/path/to/lnd-server-data/tls.cert"
  macaroon_path: "/path/to/lnd-server-data/data/chain/bitcoin/regtest/admin.macaroon"
```

For the on-chain backend, the proxy needs a reachable bitcoind node with a loaded wallet. Point it at the Docker Compose `bitcoind` (exposed on `localhost:18443`):

```yaml
bitcoind:
  host: "localhost:18443"
  rpc_user: "bitcoin"
  rpc_password: "bitcoin"
```

Run `docker volume inspect btc-paywall_lnd-server-data` to find where Docker stores the lnd volume on your host.

## Makefile Targets

**Base stack:**

| Target | Description |
|---|---|
| `make up` | Build and start the base stack (bitcoind, lnd-server, lnd-client, httpbin, proxy) |
| `make setup` | One-time regtest init: mine blocks, fund nodes, open Lightning channel |
| `make e2e-test` | POC 1 end-to-end: 402 → pay invoice → retry with L402 token → 200 |
| `make down` | Stop all containers (data volumes preserved) |
| `make clean` | Stop all containers and delete all data volumes (full reset) |
| `make logs` | Tail logs for all services |
| `make build` | Build the proxy binary locally (`bin/proxy`) |
| `make test` | Run Go tests |
| `make deps` | Run `go mod tidy` |

**POC 2 — Keycloak login paywall:**

| Target | Description |
|---|---|
| `make up-keycloak` | Start Keycloak + second proxy instance on :8090 (additive over `make up`) |
| `make e2e-keycloak-test` | Three-phase Keycloak e2e: valid creds, wrong creds, anti-replay |
| `make down-keycloak` | Stop Keycloak profile services only |
| `make clean-keycloak` | Stop Keycloak profile services and delete their volumes |

**POC 3 — On-chain BTC paywall:**

| Target | Description |
|---|---|
| `make up-onchain` | Start on-chain proxy on :8092 (bitcoind + httpbin only, no lnd) |
| `make setup-onchain` | Create bitcoind wallets and mine test funds (run once) |
| `make e2e-onchain-test` | Three-phase on-chain e2e: happy path, anti-replay, unpaid address |
| `make down-onchain` | Stop on-chain profile services only |
| `make clean-onchain` | Stop on-chain profile services and delete their volumes |

**POC 4 — On-chain BTC + Keycloak login paywall:**

| Target | Description |
|---|---|
| `make up-onchain-keycloak` | Start bitcoind + Keycloak + on-chain proxy on :8093 (no lnd) |
| `make setup-onchain-keycloak` | Create bitcoind wallets and mine test funds (run once) |
| `make e2e-onchain-keycloak-test` | Three-phase e2e: valid creds, wrong creds, anti-replay |
| `make down-onchain-keycloak` | Stop on-chain+Keycloak profile services only |
| `make clean-onchain-keycloak` | Stop on-chain+Keycloak profile services and delete their volumes |
