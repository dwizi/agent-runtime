GO ?= go

.PHONY: build test run tui compose-up compose-up-qmd compose-down compose-down-qmd sync-env

build:
	$(GO) build ./cmd/agent-runtime

test:
	$(GO) test ./...

run:
	$(GO) run ./cmd/agent-runtime serve

tui:
	$(GO) run ./cmd/agent-runtime tui

up: 
	make compose-up compose-up-qmd

compose-up:
	docker compose up -d --build --remove-orphans
	sh ./scripts/local-sync-pki-env.sh

compose-up-qmd:
	docker compose --profile qmd-sidecar up -d --build
	sh ./scripts/local-sync-pki-env.sh

compose-down:
	docker compose down

compose-down-qmd:
	docker compose --profile qmd-sidecar down

sync-env:
	sh ./scripts/local-sync-pki-env.sh
