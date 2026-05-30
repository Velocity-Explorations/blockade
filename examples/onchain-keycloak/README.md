# On-Chain BTC + Keycloak Paywall POC

This POC completes the 2×2 matrix: on-chain Bitcoin payments in front of Keycloak's OIDC token endpoint. Every credential submission — successful or failed — requires a fresh on-chain Bitcoin payment. No lnd node is needed.

## Matrix position

| | httpbin upstream | Keycloak OIDC upstream |
|---|---|---|
| Lightning L402 | POC 1 | POC 2 |
| On-chain Bitcoin | POC 3 | **POC 4 (this one)** |

## How it differs from the other Keycloak POC (POC 2)

| | POC 2: Lightning + Keycloak | POC 4: On-chain + Keycloak |
|---|---|---|
| Payment mechanism | Lightning invoice | Bitcoin address |
| Token scheme | `L402 <macaroon>:<preimage>` | `BTC-Onchain <address>` |
| Requires lnd | Yes | No |
| Attacker overhead | Lightning wallet + channel liquidity | On-chain UTXOs + miner fees |
| Confirmation speed | Instant | Mempool (0-conf) |

## Running the POC

Fully standalone — no lnd required.

```bash
make up-onchain-keycloak        # starts bitcoind + Keycloak + proxy on :8093
make setup-onchain-keycloak     # creates wallets, mines 101 blocks for test funds
make e2e-onchain-keycloak-test  # three-phase end-to-end test
```

## Manual walkthrough

```bash
# 1. Request the Keycloak token endpoint — get a 402 with a Bitcoin address
curl -i -X POST http://localhost:8093/realms/btc-paywall/protocol/openid-connect/token \
  -d grant_type=password -d client_id=paywall-demo \
  -d username=alice -d password=correct-horse-battery-staple
# HTTP/1.1 402 Payment Required
# WWW-Authenticate: BTC-Onchain address="bcrt1q...", amount_sats="1000"

# 2. Pay from the bitcoind "tester" wallet
docker compose exec bitcoind \
  bitcoin-cli -regtest -rpcuser=bitcoin -rpcpassword=bitcoin \
  -rpcwallet=tester sendtoaddress bcrt1q... 0.00001000

# 3. Retry with the address as the token
curl -X POST http://localhost:8093/realms/btc-paywall/protocol/openid-connect/token \
  -H "Authorization: BTC-Onchain bcrt1q..." \
  -d grant_type=password -d client_id=paywall-demo \
  -d username=alice -d password=correct-horse-battery-staple
# HTTP/1.1 200 OK  {"access_token": "..."}

# 4. Replay the same address — proxy rejects it
curl -X POST http://localhost:8093/realms/btc-paywall/protocol/openid-connect/token \
  -H "Authorization: BTC-Onchain bcrt1q..." \
  -d grant_type=password -d client_id=paywall-demo \
  -d username=alice -d password=correct-horse-battery-staple
# HTTP/1.1 401 Unauthorized  invalid or already-used payment token
```

## Tear down

```bash
make down-onchain-keycloak    # stop services only
make clean-onchain-keycloak   # stop + delete volumes
```
