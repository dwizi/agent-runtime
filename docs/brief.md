# Project Brief — spinner (Overlord/Admin)

This brief is for the team that installs and operates Spinner.

## What Spinner Is

Spinner is a channel-first orchestration runtime with:
- secure edge (`caddy`)
- admin mTLS access
- chat connectors (Telegram, Discord)
- IMAP ingestion and SMTP action execution
- objective scheduler and proactive task creation
- approval-gated external actions (webhook/email/sandboxed commands)

No web frontend is required; operations happen through chat + TUI + API.

## Overlord/Admin Responsibilities

1. Deploy and harden the stack (Docker Compose, TLS, secrets).
2. Pair and authorize staff identities.
3. Define admin channels and context policies.
4. Control objective schedules and event-triggered automation.
5. Approve or deny external actions.
6. Operate incident response (token/cert rotation, policy lockdown).

## Security Model in Practice

- Zero-trust baseline:
  - admin hostname uses mTLS
  - privileged operations require linked identity + role checks
- Human-in-the-loop:
  - LLM proposals create pending action approvals
  - admins explicitly approve execution
- Sandboxed command actions:
  - command allowlist
  - workspace cwd boundary checks
  - optional isolation runner wrapper

## Current Capability Snapshot

- Connectors: Telegram, Discord, IMAP
- Storage: filesystem + SQLite
- Retrieval: qmd over Markdown
- Scheduling:
  - interval-based objectives
  - markdown update event objectives
- Action plugins:
  - webhook/http
  - smtp email
  - sandbox command

Twitter/X connector is intentionally postponed.

## Suggested Rollout Sequence

1. Deploy stack and verify health.
2. Pair first admin identity in TUI.
3. Enable one admin channel per connector.
4. Validate `/task`, `/pending-actions`, `/approve-action`.
5. Enable IMAP + SMTP for one pilot workspace.
6. Add objectives (one schedule + one markdown event trigger).
7. Tighten sandbox and role allowlists after pilot.

## Primary Docs

- Install: `docs/install.md`
- Configuration: `docs/configuration.md`
- Operations: `docs/operations.md`
- Channel setup: `docs/channels/README.md`
