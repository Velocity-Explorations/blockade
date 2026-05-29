# End-to-end walkthrough: what the paywall actually proves

This document walks through what happens on the wire when you run `make e2e-test`, why each step exists, and what it does (and does not) demonstrate as a proof-of-concept for a Bitcoin-based access control layer.

If you have not yet run the test, the entry points are:

```bash
make up && make setup    # one-time stack + regtest init
make e2e-test            # runs the full 402 -> pay -> 200 flow
```

The script lives at `scripts/e2e-test.sh`. The proxy logic it exercises lives in `internal/proxy/proxy.go` and `internal/payment/lightning/verifier.go`.

## The four properties any pay-to-access control needs

The test demonstrates four properties. Each maps to a concrete observable in the request/response flow.

### 1. Access is gated by default

The first `curl http://localhost:8080/get` returns `402 Payment Required` — not 401, not 403. There is no username, no API key, no OAuth token to phish or steal. The proxy doesn't care *who* you are; it cares whether your request carries proof of expenditure. Anonymous identity is preserved as a feature, not a gap.

In code, this branch is `internal/proxy/proxy.go:72-75`: when no `Authorization: L402 ...` header is present, the proxy hands the request straight to `verifier.IssueChallenge`.

### 2. The challenge is cryptographically bound to a real economic event

The 402 response carries a `WWW-Authenticate: L402` header containing two things minted *for this specific request*:

- A Lightning **invoice** (`lnbc...`) — a cryptographic IOU asking for N satoshis, with a 32-byte payment hash baked into it.
- A **macaroon** whose identifier *is* that payment hash. The macaroon is signed with a server-side root key (`internal/payment/lightning/verifier.go:114`), so the client can't forge or mutate it.

The two are cryptographically welded together. You cannot pay one invoice and claim the proof against a different macaroon — the hash inside the macaroon must match the hash of the preimage you reveal. The minting site is `IssueChallenge` at `internal/payment/lightning/verifier.go:48`, which calls `lnd.AddInvoice` and then mints the macaroon with `id = hex(paymentHash)`.

### 3. Payment leaves a verifiable cryptographic trace

Step 2 of the test (`docker compose exec lnd-client lncli payinvoice ...`) forwards the satoshis across the Lightning channel from `lnd-client` -> `lnd-server`. When `lnd-server` accepts the HTLC, it releases the **preimage** — a 32-byte secret that hashes (SHA-256) back to the payment hash in the invoice. Possession of this preimage is *only obtainable by actually paying*. There is no shortcut: you can't guess it (2^256 search space), and the lnd node won't release it until it sees money.

This is the load-bearing primitive of the entire scheme. Lightning was designed around hash-locked payments precisely so that "I have the preimage" is mathematically equivalent to "I paid." Bitcoin paywalls inherit this property for free.

### 4. Three independent checks gate access on retry

When Step 3 sends the token back as `Authorization: L402 <macaroon>:<preimage>`, `VerifyProof` at `internal/payment/lightning/verifier.go:80` runs three checks in sequence. All three must pass before the proxy will forward the request to httpbin:

1. **`SHA256(preimage) == paymentHash`** extracted from the macaroon ID. Proves the preimage matches *this* invoice — you can't recycle a preimage from somewhere else.
2. **`lnd.LookupInvoice(paymentHash).State == SETTLED`** — the proxy independently asks its own lnd node "did money actually arrive for this invoice?" This is the trust anchor: lnd, not the client, is authoritative about whether payment occurred.
3. **`paymentHash` not in the in-memory `used` map** — single-use. Repeating the same valid token returns `401`. Without this check, paying once would buy unlimited access.

The README's "Step 4 — Confirm anti-replay" walkthrough exercises check (3) directly: replay the same token and watch the proxy reject it.

## Why this is a POC for the threat model in CLAUDE.md

The thesis stated in `CLAUDE.md` is that AI-driven adversarial probing relies on costless iteration. A standard login form lets an attacker try a million credential pairs for the price of a million HTTP requests (essentially free). The asymmetry is structural — defense logic gets more expensive while attack iteration approaches zero marginal cost.

Here, every single GET to `/get` costs 10 sats, settled atomically before the response is returned. A million probes costs ten million sats. The dollar amount is incidental and tunable per route (see `config.yaml`); the point is that the cost is *non-zero, real, and physically enforced*. The defender doesn't have to be smarter than the attacker's model — they just have to make iteration arithmetic unfavorable. The test proves the pricing surface works end-to-end: payment is required, payment is verifiable, payment is non-replayable, and the channel between these properties is cryptographic rather than trust-based.

## What the POC deliberately does *not* prove

It is worth being explicit about the boundaries.

- **It is not a login in the session sense.** Each request is paid independently. There is no "I logged in once, now I have a session." Time-windowed tokens (pay-for-window-of-access rather than pay-per-request) are the natural next step and are flagged in the README's Configuration section.
- **The macaroon root key is in-memory only.** A proxy restart invalidates all outstanding tokens. This is intentional for the POC — production would need a persistent keystore, and probably key rotation.
- **There is no rate limiting on the issuance side.** An attacker could request a million 402 challenges (free) without paying any of them. Each challenge costs the proxy lnd resources to mint. A real deployment would need issuance throttling to prevent invoice-spam DoS.
- **No metrics, no audit log, no persistent record of redeemed tokens.** The `used` map lives in process memory.
- **The price is static per route.** Adaptive pricing (charge more under load, less for trusted clients) is unimplemented.

## What "test passes" means

When you see `[e2e] PASS` in the script output, that one line attests to all of the following:

- A payment was *required* (the initial 402).
- A payment was *made* on a real (regtest) Lightning channel.
- The cryptographic proof of that payment was *verified end-to-end* against an independent source of truth (the proxy's own lnd node, via `LookupInvoice`).
- Access was granted *exactly once* — a replay would have failed at the anti-replay check.

That is the minimum evidence required to claim the architecture works. Everything else listed under "what this does not prove" is engineering work, not a question of whether the underlying primitive holds.

## Where to look in the code

| Concern | File |
|---|---|
| Backend-agnostic proxy logic | `internal/proxy/proxy.go` |
| Payment backend interface (the swap seam) | `internal/payment/verifier.go` |
| Lightning-specific challenge issuance | `internal/payment/lightning/verifier.go` (`IssueChallenge`) |
| Lightning-specific verification | `internal/payment/lightning/verifier.go` (`VerifyProof`) |
| L402 token encode/decode | `internal/payment/lightning/token.go` |
| lnd gRPC wrapper | `internal/payment/lightning/client.go` |
| Per-request price plumbing via `context.Context` | `internal/payment/lightning/verifier.go` (`WithPrice`, `priceSatsKey`) |

The most important architectural constraint, repeated from `CLAUDE.md`: `internal/proxy/proxy.go` only depends on the `payment.PaymentVerifier` interface. Replacing Lightning with proof-of-work, on-chain payments, or a hybrid scheme is a matter of writing a new `Verifier` implementation, not touching the proxy.

## Note on the upstream `Authorization` header

After L402 verification succeeds, the proxy now strips the `Authorization` header from the request before forwarding to the upstream (`internal/proxy/proxy.go`). This is correct hygiene — a proxy's own credentials should not leak to the upstream — and is a prerequisite for the second POC, where the upstream (Keycloak) may have its own `Authorization`-based auth scheme that would otherwise collide. A side effect for this walkthrough: the manual `httpbin` demo no longer echoes the L402 token back in its response body, because httpbin only sees the request *after* the header is removed. Status codes are unchanged.

## Extension: from "any HTTP endpoint" to "credential validation"

The companion POC at [`../examples/keycloak-login/`](../examples/keycloak-login/) maps this same primitive onto a concrete defense problem: **credential stuffing**. The proxy is repointed at Keycloak's OIDC token endpoint, and the four properties above acquire a sharper interpretation:

- *Access is gated by default* → no login attempt can be made without an L402 token.
- *Cryptographically bound to a real economic event* → the macaroon issued for this 402 is one-shot and tied to a specific paid invoice.
- *Verifiable cryptographic trace* → unchanged; the lnd settlement check is the same.
- *Token consumed before forwarding* → this is the load-bearing property for credential stuffing. The token is spent the moment the request passes verification, **before** Keycloak sees the credentials. So whether the password is right or wrong, the sats are gone. There is no refund-on-failure path that would re-introduce the cost asymmetry attackers rely on.

The Keycloak POC's `e2e-keycloak.sh` script demonstrates this in three phases (happy path, failed-login-still-costs, anti-replay) and distinguishes "Keycloak rejected the credentials" from "the proxy rejected the token" by inspecting response bodies — making the "failed logins also cost" claim concretely testable, not just asserted.
