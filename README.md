# btc-paywall

A proof-of-concept HTTP reverse proxy that gates access to protected resources behind a Bitcoin Lightning payment. Every request to a protected endpoint must be accompanied by cryptographic proof that a real economic cost was paid — no free probing, no costless iteration.

## Why

AI-driven adversaries can now iterate against logical access controls (permissions, identity checks, rule-based filters) at near-zero marginal cost. The asymmetry is structural: defense costs continue to rise while attack costs approach zero.

The only constraint that cannot be bypassed through intelligence is physical cost. This proxy shifts the access control question from *"who are you?"* to *"what did it cost you to be here?"* by implementing the [L402 protocol](https://docs.lightning.engineering/the-lightning-network/l402): every request must carry a Lightning payment preimage as proof of expenditure. This imposes a real, configurable economic cost on every interaction, making large-scale automated probing prohibitively expensive.

## How It Works

The proxy sits in front of any HTTP service and enforces the L402 challenge-response flow:

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

## Project Structure

```
btc-paywall/
│
├── cmd/proxy/main.go                   # Entry point — wires config, lnd client,
│                                       # verifier, and proxy together, then serves
│
├── internal/
│   ├── config/
│   │   └── config.go                   # YAML config loader and validation
│   │
│   ├── payment/
│   │   ├── verifier.go                 # PaymentVerifier interface — the only seam
│   │   │                               # between the proxy and any payment backend.
│   │   │                               # Swap this implementation to change from
│   │   │                               # Lightning to PoW, on-chain, or hybrid.
│   │   │
│   │   └── lightning/
│   │       ├── client.go               # lnd gRPC client (AddInvoice, LookupInvoice)
│   │       ├── token.go                # L402 token encode/decode and preimage check
│   │       └── verifier.go             # LightningVerifier: issues 402 challenges
│   │                                   # and validates tokens against lnd
│   │
│   └── proxy/
│       └── proxy.go                    # http.Handler: route matching → verifier →
│                                       # httputil.ReverseProxy forward on success
│
├── docker/
│   └── bitcoin/bitcoin.conf            # bitcoind regtest configuration
│
├── scripts/
│   └── setup-regtest.sh                # One-time regtest initialization: mines
│                                       # blocks, funds nodes, opens a channel
│
├── docs/
│   └── initial-plan.md                 # Architecture decision record for this POC
│
├── config.yaml                         # Active config (pre-wired for Docker Compose)
├── config.example.yaml                 # Annotated reference config
├── docker-compose.yml                  # Full local stack
├── Dockerfile                          # Multi-stage Go build → alpine runtime
└── Makefile                            # Developer workflow targets
```

### Key design decision: `PaymentVerifier`

```go
type PaymentVerifier interface {
    IssueChallenge(w http.ResponseWriter, r *http.Request) error
    VerifyProof(token string) (bool, error)
}
```

The proxy (`internal/proxy/proxy.go`) calls only this interface. The Lightning implementation (`internal/payment/lightning/`) is one backend. Future backends — proof-of-work, on-chain transactions, hybrid — implement the same two methods without touching the proxy core.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and [Docker Compose](https://docs.docker.com/compose/) (v2)
- `jq` (used by the setup script)
- Go 1.22+ (only needed for local development outside Docker)

## Infrastructure Requirements

This stack runs five containers simultaneously. These are the resource requirements for running on a VM or remote machine.

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

Two vCPUs matter during `make setup`: lnd wallet initialization on both nodes runs concurrently and briefly saturates a single core. The 40 GB recommendation applies if you plan to do active development on the VM (Go toolchain, module cache, build artifacts). For a pure runtime target, 20 GB is sufficient.

## Getting Started

### 1. Start the stack

```bash
make up
```

This builds the proxy image and starts five services:

| Service | Role |
|---|---|
| `bitcoind` | Bitcoin Core in regtest mode |
| `lnd-server` | The proxy's Lightning node — receives payments |
| `lnd-client` | Test payer node — used to simulate a paying client |
| `upstream` | [httpbin](https://httpbin.org) — the protected service |
| `proxy` | This project, listening on `localhost:8080` |

The proxy and lnd-server share a Docker volume so the proxy can read lnd's TLS certificate and admin macaroon automatically.

### 2. Initialize regtest (run once)

```bash
make setup
```

This script:
1. Waits for both lnd nodes to be fully started
2. Mines 101 blocks so the server node has spendable coinbase outputs (coinbase maturity rule)
3. Sends 1 BTC to the client node and mines a confirmation block
4. Connects the two peers and opens a 500k-sat channel from client → server
5. Mines 6 blocks to confirm the channel

After setup, the channel is active and the client node can pay the server's invoices.

### 3. Test the paywall

> **Shortcut:** `make e2e-test` runs Steps 1–3 below as a single scripted flow — it requests `/get`, parses the macaroon and invoice from the `WWW-Authenticate` header, pays the invoice from `lnd-client`, retries with the resulting L402 token, and asserts a `200 OK`. Use it as a smoke test after `make up && make setup`. The manual walkthrough below is still useful for understanding what each step does and for exercising Step 4 (anti-replay).

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

Copy the `lnbc...` invoice string from the `WWW-Authenticate` header, then:

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

### 4. Tear down

```bash
make down        # stop containers, preserve data volumes
make clean       # stop containers AND delete volumes (full reset)
```

## Configuration

`config.yaml` controls the proxy. The Docker Compose setup mounts it read-only at `/config/config.yaml` inside the proxy container.

```yaml
listen_addr: ":8080"

lnd:
  host: "lnd-server:10009"          # lnd gRPC endpoint
  tls_cert_path: "/lnd/tls.cert"    # from the shared volume
  macaroon_path: "/lnd/data/chain/bitcoin/regtest/admin.macaroon"

routes:
  - path_prefix: "/get"
    upstream: "http://upstream:80"
    price_sats: 10                   # satoshis required per request

  - path_prefix: "/post"
    upstream: "http://upstream:80"
    price_sats: 50
```

Each route maps a path prefix to an upstream service and a price. Requests to paths not matching any prefix receive `404 Not Found`. Pricing is per-request; time-windowed tokens are a planned extension.

## Local Development (without Docker)

To build and run the proxy binary directly:

```bash
make build                     # produces bin/proxy
./bin/proxy -config config.yaml
```

The proxy still needs a reachable lnd node. You can point it at the Docker Compose lnd-server (exposed on `localhost:10009`) by adjusting `config.yaml` accordingly:

```yaml
lnd:
  host: "localhost:10009"
  tls_cert_path: "/path/to/lnd-server-data/tls.cert"
  macaroon_path: "/path/to/lnd-server-data/data/chain/bitcoin/regtest/admin.macaroon"
```

Run `docker volume inspect btc-paywall_lnd-server-data` to find where Docker stores the volume on your host.

## Makefile Targets

| Target | Description |
|---|---|
| `make up` | Build and start all Docker Compose services |
| `make setup` | Initialize regtest (mine blocks, open channel) |
| `make e2e-test` | Run the full 402 → pay → 200 paywall flow against the running stack |
| `make down` | Stop containers (data volumes preserved) |
| `make clean` | Stop containers and delete all data volumes |
| `make logs` | Tail logs for all services |
| `make build` | Build the proxy binary locally (`bin/proxy`) |
| `make test` | Run Go tests |
| `make deps` | Run `go mod tidy` |
