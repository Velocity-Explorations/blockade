# Plan: Keycloak login paywall POC

> Recorded for posterity. This is the as-approved plan for the second proof-of-concept,
> which puts the L402 Lightning paywall in front of Keycloak's OIDC token endpoint.
> The decision on the one open question (single vs. dual binary) is captured below
> under "Decisions".

## Context

The existing `btc-paywall` proxy demonstrates that gating an HTTP endpoint behind L402 Lightning payments raises the cost of automated probing from "free" to "real." That POC uses `httpbin` as a generic upstream, which is illustrative but doesn't show the most directly compelling use case: **defending a credential-validation surface against credential stuffing**.

This second POC moves the same paywall in front of Keycloak's OIDC Direct Access Grants endpoint. The behavior we want to demonstrate:

1. A client `POST`s `username` + `password` to Keycloak through the proxy.
2. The request is rejected with `402 Payment Required` and an L402 challenge unless it carries a fresh L402 token.
3. The client pays the invoice, retrieves the preimage, and retries with `Authorization: L402 <macaroon>:<preimage>`.
4. The proxy verifies the token, **consumes it** (anti-replay marks it used immediately), and forwards the credentials to Keycloak.
5. Keycloak returns either `200 OK` with JWTs (if creds are valid) or `401 Unauthorized` (if not). Either way the L402 token is spent.
6. Replay of the same token, regardless of credential validity, returns `401` from the proxy.

Net effect: every login attempt — successful or failed — burns sats. A 1M-credential stuffing run costs 100M sats (configurable), even if every guess is wrong.

## Decisions

- **One binary, two configs.** The new Compose service runs the existing `proxy` image with `-config /config/keycloak.yaml`. No `cmd/keycloak-paywall/` directory or second Dockerfile is created. A divergent binary is only justified if a future requirement demands Keycloak-aware behavior in Go code (e.g. JWT post-validation, response shaping). The whole POC is therefore a config + Compose addition plus the one cross-cutting change to `internal/proxy/proxy.go`.

## High-level design

The existing payment machinery is fully reusable. The new POC is essentially a *configuration* of the existing proxy, plus a new container topology that adds Keycloak + Postgres alongside the current `bitcoind`/`lnd-server`/`lnd-client` services.

The only material code change is to `internal/proxy/proxy.go`: it must strip the `Authorization: L402 ...` header from the outbound request before forwarding. This is correct behavior in general (a proxy's own credentials should not leak to the upstream) and resolves the latent collision with Keycloak's HTTP Basic auth scheme on the token endpoint.

The new POC uses Keycloak's Direct Access Grants flow with a **public** client (no `client_secret`), which sidesteps the need for HTTP Basic auth entirely and is the simplest configuration to demo with `curl` — matching the existing POC's `make e2e-test` style.

## Files to create / modify

### Modify

| File | Change |
|---|---|
| `internal/proxy/proxy.go` | Strip `Authorization` header from forwarded request after L402 verification succeeds. ~3 lines. |
| `docker-compose.yml` | Add `keycloak` and `keycloak-db` services and a new `keycloak-paywall` service under a Compose `profiles: [keycloak]` so the existing `make up` is unaffected. |
| `Makefile` | Add `up-keycloak`, `setup-keycloak`, `e2e-keycloak-test`, `down-keycloak` targets. |
| `README.md` | Add a short "Second POC: Keycloak login paywall" section pointing to `examples/keycloak-login/README.md`. |
| `docs/e2e-walkthrough.md` | Add a paragraph at the bottom referencing the second POC and how the threat model maps onto credential stuffing. |

### Create

| File | Purpose |
|---|---|
| `examples/keycloak-login/config.yaml` | Proxy config: `listen_addr: :8090`, lnd config identical to the existing one, single route gating `/realms/btc-paywall/protocol/openid-connect/token` at e.g. 100 sats. |
| `examples/keycloak-login/realm-export.json` | Keycloak realm import with one realm (`btc-paywall`), one public client (`paywall-demo`) with Direct Access Grants enabled, and one test user (`alice` / `correct-horse-battery-staple`). |
| `examples/keycloak-login/scripts/e2e-keycloak.sh` | End-to-end test: 402 → pay → submit valid creds → assert 200 with JWT in body; bonus second run with wrong password → assert 401 from Keycloak (proving credentials reach Keycloak, not just the proxy). |
| `examples/keycloak-login/scripts/e2e-keycloak-replay.sh` | Anti-replay: pay once, submit valid creds twice with the same L402 token → assert 200 then 401 from the proxy. |
| `examples/keycloak-login/README.md` | Quickstart, the three-step manual walkthrough, the `make e2e-keycloak-test` shortcut, what the test proves vs. what it doesn't. Same shape as `docs/e2e-walkthrough.md`. |

Note: `cmd/keycloak-paywall/main.go` is **not** created — see Decisions above.

## Detailed changes

### `internal/proxy/proxy.go`

After `valid` is confirmed true, before `rt.rp.ServeHTTP(w, r)`, delete the `Authorization` header from `r.Header`. This is the only cross-cutting change. The existing `make e2e-test` against httpbin will continue to pass — it asserts on status code, not on the echoed `Authorization` header. (The header's presence in the demo body output disappears, which we'll note in `docs/e2e-walkthrough.md`.)

### `docker-compose.yml`

Add three services, all gated behind `profiles: [keycloak]`:

- `keycloak-db` — `postgres:16` with a named volume.
- `keycloak` — `quay.io/keycloak/keycloak:26.0` running `start-dev --import-realm`. Mounts `./examples/keycloak-login/realm-export.json` read-only. Container port 8080 → host 8091. `depends_on: keycloak-db: service_healthy`.
- `keycloak-paywall` — reuses the existing proxy image, started with a different `-config` path. Mounts `./examples/keycloak-login/config.yaml` read-only and the existing `lnd-server-data` volume. Listens on 8090.

Port summary for the new services:

| Service | Container | Host |
|---|---|---|
| `keycloak-db` | 5432 | — (internal only) |
| `keycloak` | 8080 | 8091 |
| `keycloak-paywall` | 8090 | 8090 |

Existing 8080 (proxy) and 8081 (lnd-server REST) are unaffected because the new services are only started under the `keycloak` profile.

### `Makefile`

```make
up-keycloak:
	docker compose --profile keycloak up -d --build

setup-keycloak:
	@echo "Keycloak realm is auto-imported on first start; no extra setup needed."
	@echo "Run 'make setup' first if the Lightning channel is not yet open."

e2e-keycloak-test:
	bash examples/keycloak-login/scripts/e2e-keycloak.sh

down-keycloak:
	docker compose --profile keycloak down
```

`.PHONY` updated accordingly.

### `examples/keycloak-login/scripts/e2e-keycloak.sh`

Mirror of `scripts/e2e-test.sh` with Keycloak-specific assertions:

1. `POST` to `http://localhost:8090/realms/btc-paywall/protocol/openid-connect/token` with `grant_type=password&client_id=paywall-demo&username=alice&password=...` and **no** L402 header → expect `402` with `WWW-Authenticate: L402` header.
2. Parse macaroon + invoice, pay invoice via `docker compose exec lnd-client lncli payinvoice --json --force`, extract preimage.
3. Re-`POST` with `Authorization: L402 <macaroon>:<preimage>` → expect `200` and a body containing `"access_token"` (parse with `jq -e '.access_token | length > 100'`).
4. Bonus: pay a second invoice, submit with **wrong password** → assert `401` from Keycloak (`error: invalid_grant`) AND that the L402 token is now spent (third request with same token → `401` from the proxy).

Step 4 is the crucial demonstration that **failed login attempts still consume payment**.

## Verification

### Sanity checks (no Lightning involved)

```bash
go vet ./...                                       # PATH="$HOME/.asdf/shims:$PATH" if running via the harness
PATH="$HOME/.asdf/shims:$PATH" make build          # binary still compiles
```

### End-to-end with the existing POC

```bash
make up && make setup && make e2e-test
```

Must still pass after the `Authorization`-header-strip change. (The body output in the script will no longer contain the echoed L402 header; this is expected.)

### End-to-end with the new POC

```bash
make up-keycloak                                # brings up everything including Keycloak
# wait ~30s for Keycloak first-boot realm import
make e2e-keycloak-test                          # runs the full 402 → pay → JWT assertion
```

Expected output: `[e2e-keycloak] PASS — Keycloak issued JWT after L402 payment` and a follow-up `[e2e-keycloak] PASS — failed-login attempt also consumed the token` line.

### Manual probe (matches the README walkthrough style)

```bash
# 1. Get the 402
curl -i -X POST http://localhost:8090/realms/btc-paywall/protocol/openid-connect/token \
  -d grant_type=password -d client_id=paywall-demo \
  -d username=alice -d password=correct-horse-battery-staple

# 2. Pay (same as the existing POC)
docker compose exec lnd-client lncli --network=regtest payinvoice --force <bolt11>

# 3. Retry — expect 200 + JWT
curl -X POST http://localhost:8090/realms/btc-paywall/protocol/openid-connect/token \
  -H "Authorization: L402 <macaroon>:<preimage>" \
  -d grant_type=password -d client_id=paywall-demo \
  -d username=alice -d password=correct-horse-battery-staple

# 4. Validate the JWT works against Keycloak directly
curl http://localhost:8091/realms/btc-paywall/protocol/openid-connect/userinfo \
  -H "Authorization: Bearer <access_token from step 3>"
```

## What this POC will and won't prove

**Will prove:**
- Credential stuffing is no longer free against any IAM that exposes a token endpoint.
- The same Lightning payment primitive that gates a generic HTTP endpoint also gates a real-world authentication surface — no new cryptographic machinery needed.
- The architectural seam works: swapping `httpbin` for `Keycloak` was a config change, not a code rewrite.

**Won't prove:**
- Anything about the *login UX* for a real human user (browser flow, WebLN integration, password manager interplay) — that's the natural follow-up POC.
- Anything about token persistence across proxy restarts, rate limiting on `IssueChallenge`, or pricing schemes more sophisticated than flat per-attempt — same limitations carry over from the existing POC.
- Anything about the `client_credentials` flow, refresh tokens, or session management post-login. Scope is bounded at "first credential submission."

## Critical files (for the implementer)

- `internal/proxy/proxy.go` — the one-line change site (header strip after verification).
- `cmd/proxy/main.go` — verify it cleanly accepts the new config without modification (no edit expected).
- `docker-compose.yml` — three new services under `profiles: [keycloak]`.
- `examples/keycloak-login/realm-export.json` — Keycloak realm fixture with public client + Direct Access Grants enabled.
- `examples/keycloak-login/scripts/e2e-keycloak.sh` — proves all four properties end-to-end.
