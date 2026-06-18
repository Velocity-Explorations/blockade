# blockAIde

Access control priced in physical cost. blockAIde gates a protected resource behind a real Bitcoin payment, so every request carries cryptographic proof that an economic cost was paid. No free probing, no costless iteration.

It ships in two halves that meet at the HTTP 402 boundary: a server-side reverse proxy that enforces payment, and a drop-in browser widget that handles the payment loop for the user. A developer can gate a download with one attribute and one script tag.

> blockAIde builds on the upstream `btc-paywall` reverse proxy by [upstream author], used here under its original license. See `LICENSE`. The proxy, POCs, and `PaymentVerifier` design below are upstream work. The browser widget, cost calculator, and the staked cost-curve roadmap are additions in this fork.

## Why

AI-driven adversaries can now iterate against logical access controls, permissions, identity checks, and rule-based filters, at near-zero marginal cost. The asymmetry is structural. Defense costs continue to rise while attack costs approach zero.

The only constraint that cannot be bypassed through intelligence is physical cost. blockAIde shifts the access control question from "who are you?" to "what did it cost you to be here?", imposing a real, configurable economic cost on every interaction and making large-scale automated probing prohibitively expensive.

## Drop it in

The browser widget (`examples/browser-paywall/l402-paywall.js`) is a single self-contained file. No build step, no framework, no dependencies. Include it and mark a link:

```html
<a href="/protected/report.pdf" data-l402>Download PDF</a>
<script src="l402-paywall.js"></script>
```

The widget intercepts the click, runs the full L402 loop, and releases the download when payment clears. It offers three usage modes over one engine:

1. Declarative. Add `data-l402` to a link or button. No JavaScript written by the developer.
2. Programmatic. `l402Fetch(url, opts)` is a drop-in replacement for `fetch`.
3. Global. `L402.installGlobal()` patches `window.fetch` so existing calls to 402 endpoints are paywalled automatically.

The widget renders whatever price the server names and knows nothing about how that price is chosen. All pricing policy stays server-side, which keeps the widget droppable into any deployment.

## How it works

The two halves communicate over the L402 protocol, an open HTTP-402 authentication scheme originated by Lightning Labs. Payments settle over the Lightning Network. The proxy here is an independent implementation of L402, not a fork of Aperture, so there is no external runtime dependency on a third party.

```
Client                     blockAIde proxy               lnd node           Upstream
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

The modal the user sees is presentation only. Enforcement is the proxy returning 402 and refusing to forward until valid proof arrives. A direct call to the endpoint that bypasses the UI still gets a 402.

### Key design decision: PaymentVerifier

```go
type PaymentVerifier interface {
    IssueChallenge(w http.ResponseWriter, r *http.Request) error
    VerifyProof(token string) (bool, error)
    ExtractToken(authHeader string) (string, bool)
}
```

The proxy (`internal/proxy/proxy.go`) calls only this interface and has no imports from any backend package. `IssueChallenge` writes the 402 with whatever challenge the backend requires. `VerifyProof` validates the credential. `ExtractToken` parses the backend-specific Authorization scheme so the proxy stays scheme-agnostic. The active backend is selected at startup by which config section is present. This is the seam that lets the same proxy enforce Lightning, on-chain, proof-of-work, or a hybrid without touching the proxy core.

## Cost curve

The "Why" claim is that blockAIde makes probing prohibitively expensive at scale. A flat per-request toll does not deliver that on its own. Flat cost is linear in volume, which an adversary budgets in advance, and the marginal cost of the next request never rises. The mechanism that delivers the at-scale claim is cost that escalates with cumulative demand per principal, so the millionth request costs far more than the first while a one-off legitimate user stays at the floor.

The interactive cost calculator (`examples/cost-calculator`) shows this. It plots undefended flat cost against blockAIde's escalating cost, computes the amortized per-request floor an adversary cannot beat even by resetting identities, and reports a deterrence verdict against a chosen attacker value per request.

The current proxy enforces a flat `price_sats` per route, which is the v1 behavior. The staked, per-principal escalating curve is the v2 roadmap below. The README does not claim flat tolls already deliver the at-scale property.

## Status and roadmap

v1, built and tested end to end:

- The reverse proxy, Lightning L402 and on-chain backends, flat per-route pricing, single-use tokens.
- The browser widget, three usage modes, WebLN payment with a manual preimage fallback.
- The interactive cost calculator.

v2, in progress:

- A staked enrollment credential and per-principal escalating cost, replacing the flat toll. The credential is minted by a one-time stake payment, and per-request price rises with the credential's request count. This requires per-principal server state, which is a deliberate trade against L402's stateless verification.
- An NWC (Nostr Wallet Connect) payment client, so a user can pay from their own node without a browser extension. The widget keeps a WebLN-shaped provider interface, so the NWC client drops in unchanged.

Design briefs for both are in `docs/`.

## Non-custodial by design

The payer is the user's own node, holding the user's own keys. blockAIde holds no funds on behalf of anyone and is not a money service. Real-world custody, money-transmission, and KYC questions are out of scope for this proof of concept and are a matter for counsel, not for this code.

## Networks

The stack runs on `regtest` for local development, which starts instantly and needs no chain sync. A public or evaluation demo should run on `signet` or `mutinynet` instead, since regtest is a private local chain. On all of these the sats carry no real value.

---

# Running the demo stack

The repository ships four self-contained server-side POCs. All four run from the same compiled binary; the active backend is selected by config.

| POC | Backend | Upstream | Token scheme | Port |
|-----|---------|----------|--------------|------|
| 1 | Lightning L402 | httpbin | `L402 <macaroon>:<preimage>` | 8080 |
| 2 | Lightning L402 | Keycloak OIDC | `L402 <macaroon>:<preimage>` | 8090 |
| 3 | On-chain Bitcoin | httpbin | `BTC-Onchain <address>` | 8092 |
| 4 | On-chain Bitcoin | Keycloak OIDC | `BTC-Onchain <address>` | 8093 |

## Prerequisites

- Docker and Docker Compose (v2)
- `jq` (used by the setup and e2e scripts)
- Go 1.22+ (only for local development outside Docker)

## POC 1: Lightning L402 paywall

The base POC. Every request must carry a Lightning payment preimage as proof of expenditure. Tokens are single-use; replaying a used token returns 401.

```
make up        # build image, start bitcoind + lnd-server + lnd-client + httpbin + proxy
make setup     # one-time: mine blocks, fund nodes, open Lightning channel
make e2e-test  # 402 -> pay invoice -> retry with token -> assert 200
```

Manual walkthrough (see `docs/e2e-walkthrough.md` for a code-referenced version):

```
# 1. Hit a protected endpoint without a token
curl -v http://localhost:8080/get
#    -> HTTP/1.1 402 Payment Required
#       WWW-Authenticate: L402 macaroon="<base64>", invoice="lnbc..."

# 2. Pay the invoice from the client node
docker compose exec lnd-client lncli --network=regtest payinvoice --force <bolt11-invoice>
#    -> prints a payment_preimage hex string

# 3. Retry with the L402 token
curl -v http://localhost:8080/get -H "Authorization: L402 <macaroon>:<preimage>"
#    -> 200 OK with the httpbin JSON body

# 4. Confirm anti-replay: repeat step 3
#    -> 401 Unauthorized, the token is single-use
```

Tear down: `make down` stops containers and preserves volumes; `make clean` deletes volumes for a full reset.

## POC 2: Keycloak login paywall

Puts the same L402 paywall in front of Keycloak's OIDC token endpoint. The defense case: every credential submission costs sats, including failed ones, so credential stuffing is no longer free even against an attacker who always guesses wrong. Reuses the proxy binary under a Docker Compose `keycloak` profile, leaving the base stack untouched.

```
make up && make setup    # base stack first
make up-keycloak         # adds Keycloak + a second proxy instance on :8090
make e2e-keycloak-test   # pay -> valid creds -> 200+JWT; pay -> wrong creds -> 401; replay -> 401
```

See `examples/keycloak-login/README.md` and `docs/keycloak-poc-plan.md`.

## POC 3: On-chain BTC paywall

Replaces the Lightning backend with direct on-chain payments through bitcoind JSON-RPC. No lnd, no channels, no liquidity. Each request requires a payment to a freshly generated address, granted on 0-conf mempool appearance. This POC is a deliberate contrast: on-chain fees impose a price floor (the dust limit is about 294 sats for P2WPKH), which makes per-request micropayments impractical at small amounts. The same `PaymentVerifier` interface accommodates it with no change to the proxy core.

```
make up-onchain          # bitcoind + httpbin + onchain-paywall (:8092) only
make setup-onchain       # create wallets, mine 101 blocks for test funds (run once)
make e2e-onchain-test    # pay address -> 200; replay spent address -> 401; unpaid -> 401
```

See `examples/onchain-btc/README.md` and `docs/onchain-btc-poc-plan.md`.

## POC 4: On-chain BTC + Keycloak login paywall

The on-chain backend in front of Keycloak. Every credential submission requires a fresh on-chain payment. The attacker must fund on-chain UTXOs and pay miner fees per attempt rather than maintaining a Lightning wallet, so the floor is higher and the friction greater, at the cost of slower confirmation and a simpler token scheme.

```
make up-onchain-keycloak        # bitcoind + Keycloak + on-chain proxy (:8093)
make setup-onchain-keycloak     # create wallets, mine test funds (run once)
make e2e-onchain-keycloak-test  # valid creds, wrong creds, anti-replay
```

See `examples/onchain-keycloak/README.md` and `docs/onchain-keycloak-poc-plan.md`.

## Browser examples

<!--
  DECISION NEEDED: two browser artifacts now exist. Resolve before release.
    examples/browser-paywall/  -> the drop-in component (l402-paywall.js) and its demo page.
                                   This is what a developer adopts. Documented under "Drop it in" above.
    examples/browser-demo/     -> the original guided, step-by-step protocol explainer.
  Either keep both with the labels below, or fold the explainer into the component demo and delete one.
-->

- `examples/browser-paywall/` is the drop-in component and its demo page. Start a POC, serve the page, and exercise the three usage modes. This is the artifact to adopt.
- `examples/browser-demo/` is a guided, step-by-step walkthrough of the raw protocol (402 challenge, payment, retry, anti-replay), useful for understanding the flow rather than for integration.

```
make up && make setup    # start a POC (Lightning)
make up-demo             # serve the guided demo at http://localhost:8084
make down-demo
```

## Configuration

`config.yaml` controls the proxy and is mounted read-only into the container. Exactly one backend section (`lnd` or `bitcoind`) must be present. Each route maps a path prefix to an upstream and a per-request price in sats. Unmatched paths return 404.

Lightning backend (POC 1 and 2):

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

On-chain backend (POC 3):

```yaml
listen_addr: ":8092"
bitcoind:
  host: "bitcoind:18443"
  rpc_user: "bitcoin"
  rpc_password: "bitcoin"
routes:
  - path_prefix: "/get"
    upstream: "http://upstream:80"
    price_sats: 1000     # higher floor due to on-chain dust limits
  - path_prefix: "/post"
    upstream: "http://upstream:80"
    price_sats: 5000
```

`price_sats` is a flat per-route price (v1). The v2 staked cost curve replaces this constant with a per-principal pricing function; see the roadmap and `docs/`.

Optional rate limiting, any backend:

```yaml
rate_limit:
  requests_per_second: 5   # sustained token-bucket refill rate per source IP
  burst: 10                # maximum burst above the sustained rate
```

Rate limiting applies only to unauthenticated requests, the ones that generate a 402. Authenticated requests carrying a valid token are never rate-limited. Omit the section to disable it. Excess requests receive 429. In v2 this limiter is the guard on the anonymous floor tier.

## Local development (without Docker)

```
make build                 # produces bin/proxy
./bin/proxy -config config.yaml
```

For the Lightning backend the proxy needs a reachable lnd node; point it at the Compose `lnd-server` exposed on `localhost:10009`. For the on-chain backend it needs a reachable bitcoind with a loaded wallet on `localhost:18443`. Run `docker volume inspect blockaide_lnd-server-data` to find the lnd volume on your host.

## Infrastructure

The base stack runs five containers, about 1.5 GB RAM total and about 8 GB disk after the first Docker build (the build downloads all of lnd's transitive dependencies). Recommended VM: 2 vCPU, 4 GB RAM minimum (8 GB comfortable), 20 GB disk minimum (40 GB comfortable). Two vCPUs matter during `make setup`, where lnd wallet init on both nodes runs concurrently and briefly saturates a core.

## Makefile targets

Base stack: `make up`, `make setup`, `make e2e-test`, `make down`, `make clean`, `make logs`, `make build`, `make test`, `make deps`.

Profiles, each additive over the base stack: `make up-keycloak` / `make up-onchain` / `make up-onchain-keycloak` / `make up-demo`, with matching `setup-`, `e2e-`, `down-`, and `clean-` targets per profile. See the POC sections above for the relevant commands.
