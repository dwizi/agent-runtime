# Development Guide

This guide is for contributors developing `agent-runtime` locally.

## Prerequisites

- Go toolchain
- Docker + Docker Compose plugin (optional but recommended)

## Local Setup

```bash
cp .env.example .env
make run
```

Run TUI:

```bash
make tui
```

Run tests:

```bash
make test
```

## Useful Commands

- Build binary: `make build`
- Start compose stack: `make compose-up`
- Stop compose stack: `make compose-down`
- Sync local mTLS env paths: `make sync-env`
- Docs checks: `make docs-check`

## Project Layout

- `cmd/agent-runtime`: CLI entrypoint
- `internal/app`: runtime bootstrap and orchestration wiring
- `internal/httpapi`: HTTP handlers
- `internal/connectors`: Telegram/Discord/IMAP connectors
- `internal/gateway`: message routing, commands, and tools
- `internal/store`: SQLite persistence layer
- `internal/qmd`: markdown retrieval/index integration
- `docs`: user/operator/developer docs

## Testing Strategy

Use package-level tests plus end-to-end smoke checks.

1. `make test`
2. Run runtime (`make run` or `make compose-up`)
3. Exercise one connector path (`/status`, `/task ...`)

## Documentation and Releases

If behavior changes, update:

- `README.md`
- `docs/api.md` (if API changed)
- related docs under `docs/`

Before release, run:

```bash
make test
make docs-check
```
