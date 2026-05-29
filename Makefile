.PHONY: up down setup logs build test e2e-test clean deps \
        up-keycloak down-keycloak setup-keycloak e2e-keycloak-test

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