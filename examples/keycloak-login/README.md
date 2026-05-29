# Keycloak login paywall (second POC)

> **TL;DR** — Same L402 Lightning paywall as the parent project, this time gating
> Keycloak's OIDC token endpoint. **Every login attempt costs sats, including
> failed ones.** Credential stuffing is no longer free, even if every guess is wrong.

This POC reuses the existing `btc-paywall` proxy unchanged (one binary, two configs) and adds Keycloak + a second proxy instance under a Docker Compose `keycloak` profile, so it does not interfere with the original `make up` / `make e2e-test` flow.

## Why this POC exists

The original POC demonstrated that gating an arbitrary HTTP endpoint behind L402 raises the cost of probing from "free" to "real." This second POC moves the same paywall in front of an actual identity provider's credential-validation surface — the most directly compelling defense use case.

The key property: the L402 token is consumed when the request passes the proxy's verification, **before** the credentials reach Keycloak. So whether the password is right or wrong, the sats are already spent. There is no refund-on-failure path that would re-introduce the cost-asymmetry attackers rely on.

For the deeper mechanism (preimage proof, anti-replay, the three-check verification sequence) see [`../../docs/e2e-walkthrough.md`](../../docs/e2e-walkthrough.md). That document covers how the L402 primitive works; this README focuses on what the Keycloak wrapping adds.

## Quick start

From the repo root:

```bash
make up                  # original stack: bitcoind, lnd-server, lnd-client, httpbin, proxy
make setup               # one-time: mine blocks, fund nodes, open Lightning channel
make up-keycloak         # adds keycloak + keycloak-paywall services
make e2e-keycloak-test   # runs the full three-phase end-to-end test
```

`make up-keycloak` is additive — it brings up the original stack too if it is not already running. First Keycloak boot takes ~30 seconds for the realm import; the e2e script waits for readiness automatically.

## What the e2e script proves

The script (`scripts/e2e-keycloak.sh`) exercises three phases. All three must pass:

| Phase | Action | Asserts |
|---|---|---|
| 1. Happy path | Pay invoice, submit valid creds with token | `200 OK` + JWT in body — proves the proxy actually forwards to Keycloak and Keycloak issues real tokens |
| 2. Failed login | Pay a second invoice, submit **wrong** password with token | `401` with Keycloak's `{"error":"invalid_grant"}` body — proves credentials reach Keycloak AND the token is consumed even though login failed |
| 3. Replay | Submit valid creds again with the token from phase 2 | `401` with proxy's plain-text `invalid or already-used payment token` body — proves anti-replay |

Distinguishing the two `401` failure modes (Keycloak rejecting credentials vs. proxy rejecting the token) by body content is the load-bearing assertion in phases 2 and 3 — it is what makes "failed logins also cost" demonstrable rather than just claimed.

## Manual walkthrough

For when you want to see the bytes on the wire.

### Step 1 — Hit the token endpoint without an L402 token

```bash
curl -i -X POST http://localhost:8090/realms/btc-paywall/protocol/openid-connect/token \
  -d grant_type=password \
  -d client_id=paywall-demo \
  -d username=alice \
  -d password=correct-horse-battery-staple
```

Expected response:

```
HTTP/1.1 402 Payment Required
WWW-Authenticate: L402 macaroon="<base64>", invoice="lnbcrt..."
```

### Step 2 — Pay the invoice from `lnd-client`

```bash
docker compose exec lnd-client lncli --network=regtest payinvoice --force <bolt11-invoice>
```

Note the `payment_preimage` hex string in the output.

### Step 3 — Retry with the L402 token

```bash
curl -X POST http://localhost:8090/realms/btc-paywall/protocol/openid-connect/token \
  -H "Authorization: L402 <macaroon>:<preimage>" \
  -d grant_type=password \
  -d client_id=paywall-demo \
  -d username=alice \
  -d password=correct-horse-battery-staple
```

Expected response: `200 OK` with a JSON body containing `access_token`, `refresh_token`, `expires_in`, etc. — a normal Keycloak token response.

### Step 4 — Validate the JWT against Keycloak directly

This bypasses the paywall entirely, hitting Keycloak on its own port (`8091`):

```bash
curl http://localhost:8091/realms/btc-paywall/protocol/openid-connect/userinfo \
  -H "Authorization: Bearer <access_token from step 3>"
```

Expected: a JSON body with `sub`, `email`, `preferred_username` — proving the token Keycloak issued is real and verifiable independently.

### Step 5 — Try the same L402 token again (anti-replay)

Repeat step 3. Expected response: `401 Unauthorized` with body `invalid or already-used payment token`. This is the proxy rejecting the request before it reaches Keycloak.

### Step 6 — Try a wrong password with a fresh token

Repeat steps 1-3 with `password=wrong-on-purpose`. Expected response: `401` with a Keycloak JSON body like `{"error":"invalid_grant","error_description":"Invalid user credentials"}`. The L402 token is now spent — verify by replaying it (expect step 5's behavior).

## Topology

| Service | Container port | Host port | Purpose |
|---|---|---|---|
| `keycloak` | 8080 | **8091** | OIDC IdP, accessed directly for `/userinfo`, `/.well-known/...` |
| `keycloak-paywall` | 8090 | **8090** | The L402 proxy fronting Keycloak's token endpoint |
| `keycloak` (internal) | 8080 | — | Reached as `http://keycloak:8080` from `keycloak-paywall` over the `paywall` Docker network |

The `keycloak-paywall` service reuses the same proxy binary as the original POC — it is just started with `-config /config/keycloak.yaml` instead of `-config /config/config.yaml`. See [`config.yaml`](config.yaml) — it has exactly one route, gating the OIDC token path at 100 sats.

The Keycloak container runs in `start-dev` mode with `--import-realm`. The realm definition in [`realm-export.json`](realm-export.json) is intentionally minimal: one realm (`btc-paywall`), one **public** OIDC client (`paywall-demo`) with Direct Access Grants enabled, one user (`alice`). Public client = no `client_secret`, so HTTP Basic auth never enters the picture and the proxy's L402 `Authorization` header has no scheme to collide with.

## What this POC will and won't prove

**Will prove:**

- Credential stuffing is no longer free against any IAM that exposes a token endpoint — the cost asymmetry that AI-driven probing relies on is broken.
- The same Lightning payment primitive that gates `httpbin` also gates Keycloak — no new cryptographic machinery needed.
- The architectural seam in `internal/proxy/proxy.go` works: swapping the upstream from `httpbin` to `Keycloak` was a config change plus a one-line cross-cutting fix (strip `Authorization` before forwarding), not a code rewrite.

**Won't prove:**

- Anything about the *login UX* for a real human user: browser flow, WebLN/Alby integration, password-manager interplay. That is the natural follow-up POC.
- Token persistence across proxy restarts (root macaroon key is in-memory).
- Anything about rate limiting on `IssueChallenge` — an attacker can still ask for unlimited 402 challenges without paying any of them. Real deployments need issuance throttling on top of payment requirements.
- The `client_credentials` flow, refresh tokens, or session management. Scope is bounded at "first credential submission."

## Tear down

```bash
make down-keycloak       # stops only the keycloak-profile services
make down                # stops the original stack (preserves volumes)
make clean               # full reset: stops everything + deletes all volumes
```
