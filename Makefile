.PHONY: up down setup logs build test e2e-test clean deps

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
