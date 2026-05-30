.PHONY: up down setup logs build test e2e-test clean deps \
        up-keycloak down-keycloak setup-keycloak e2e-keycloak-test clean-keycloak \
        up-onchain down-onchain setup-onchain e2e-onchain-test clean-onchain \
        up-onchain-keycloak down-onchain-keycloak setup-onchain-keycloak e2e-onchain-keycloak-test clean-onchain-keycloak

## Start all Docker Compose services
up:
	docker compose up -d --build

## Stop and remove containers (data volumes are preserved)
down:
	docker compose down

## Initialize regtest: mine blocks, fund nodes, open Lightning channel
## Run once after `make up`
setup:
	bash scripts/setup-regtest.sh

## Tail logs for all services (Ctrl-C to stop)
logs:
	docker compose logs -f

## Build the proxy binary locally (without Docker)
build:
	go build -o bin/proxy ./cmd/proxy

## Run Go tests
test:
	go test ./...

## Run end-to-end paywall flow against the running stack:
## 402 challenge -> pay invoice from lnd-client -> retry with L402 token -> 200
## Requires `make up && make setup` to have been run.
e2e-test:
	bash scripts/e2e-test.sh

## Remove containers AND data volumes (full reset)
clean:
	docker compose down -v

## Download Go dependencies
deps:
	go mod tidy

## --- Second POC: Keycloak login paywall (examples/keycloak-login/) -----

## Start the existing stack PLUS Keycloak and the keycloak-paywall service.
## Keycloak takes ~30s on first boot to import the realm.
up-keycloak:
	docker compose --profile keycloak up -d --build

## The Keycloak realm is auto-imported by Keycloak's --import-realm flag.
## You still need `make setup` once for the Lightning channel.
setup-keycloak:
	@echo "Keycloak realm is auto-imported on first start; nothing to do here."
	@echo "If you have not yet opened the Lightning channel, run: make setup"

## Run the end-to-end Keycloak paywall flow:
## 402 -> pay invoice from lnd-client -> POST creds -> 200 + JWT
## Plus a phase that proves failed-login attempts also consume the token.
e2e-keycloak-test:
	bash examples/keycloak-login/scripts/e2e-keycloak.sh

## Stop only the Keycloak-profile services (leaves the original POC running).
down-keycloak:
	docker compose --profile keycloak down

clean-keycloak:
	docker compose --profile keycloak down -v

## --- Third POC: on-chain BTC paywall (examples/onchain-btc/) -----------

## Start only the services the on-chain POC needs: bitcoind, httpbin, and
## the onchain-paywall proxy. Does NOT start lnd-server or lnd-client.
up-onchain:
	docker compose --profile onchain up -d --build bitcoind upstream onchain-paywall

## Create the bitcoind "paywall" wallet used by the on-chain verifier.
## Run once after `make up-onchain`. Safe to re-run.
setup-onchain:
	bash scripts/setup-onchain.sh

## Run the end-to-end on-chain paywall flow:
## 402 -> pay via bitcoin-cli (tester wallet) -> present address token -> 200
## Plus anti-replay and unpaid-address probes.
e2e-onchain-test:
	bash examples/onchain-btc/scripts/e2e-onchain.sh

## Stop only the onchain-profile services.
down-onchain:
	docker compose --profile onchain down

clean-onchain:
	docker compose --profile onchain down -v

## --- Fourth POC: on-chain BTC + Keycloak (examples/onchain-keycloak/) ---

## Start bitcoind + Keycloak + onchain-keycloak-paywall. No lnd required.
## Keycloak takes ~30s on first boot to import the realm.
up-onchain-keycloak:
	docker compose --profile onchain-keycloak up -d --build bitcoind keycloak onchain-keycloak-paywall

## Create/fund the bitcoind wallets needed for the e2e test.
## Keycloak realm is auto-imported on first boot; nothing else to do.
setup-onchain-keycloak:
	bash scripts/setup-onchain.sh

## Run the end-to-end on-chain + Keycloak paywall flow:
## 402 -> pay via bitcoin-cli -> POST creds -> 200 + JWT
## Plus failed-login and anti-replay phases.
e2e-onchain-keycloak-test:
	bash examples/onchain-keycloak/scripts/e2e-onchain-keycloak.sh

## Stop only the onchain-keycloak-profile services.
down-onchain-keycloak:
	docker compose --profile onchain-keycloak down

clean-onchain-keycloak:
	docker compose --profile onchain-keycloak down -v