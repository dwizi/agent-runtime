# Production Checklist (Overlord/Admin)

Use this as a go-live gate for Spinner deployments.

## A. Preflight

- [ ] Deployment host has Docker + Docker Compose plugin installed.
- [ ] DNS for `PUBLIC_HOST` and `ADMIN_HOST` resolves to target host.
- [ ] `.env` exists and is not committed to git.
- [ ] `SPINNER_ZAI_API_KEY` set and validated.
- [ ] At least one channel token configured (`SPINNER_TELEGRAM_TOKEN` and/or `SPINNER_DISCORD_TOKEN`).
- [ ] `SPINNER_IMAP_*` and `SPINNER_SMTP_*` set only if email workflows are enabled.
- [ ] `SPINNER_SANDBOX_ALLOWED_COMMANDS` reduced to minimum required commands.

## B. Security Baseline

- [ ] Admin endpoint configured with mTLS (`ADMIN_HOST` + Caddy client cert trust).
- [ ] `SPINNER_ADMIN_TLS_SKIP_VERIFY=false` for operator clients.
- [ ] Connector tokens stored in secret manager (not plain shared files).
- [ ] Least-privilege bot permissions validated in Telegram/Discord.
- [ ] Initial admin identities paired via one-time token flow.
- [ ] At least one admin channel enabled (`/admin-channel enable`).

## C. Data and Recovery

- [ ] Backups configured for:
  - [ ] `/data/spinner/meta.sqlite`
  - [ ] `/data/workspaces/`
  - [ ] `.env` in secure secret storage
  - [ ] `ops/caddy/pki` (if preserving cert continuity)
- [ ] Restore test performed once in staging.

## D. Deployment

- [ ] Run: `make compose-up`
- [ ] Health checks pass:
  - [ ] `curl -fsS http://localhost/healthz`
  - [ ] `curl -fsS http://localhost/readyz`
  - [ ] `curl -fsS http://localhost/api/v1/info`
- [ ] TUI access verified: `make tui`
- [ ] First `/task` command succeeds in each enabled connector.

## E. Policy and Automation

- [ ] LLM safety settings reviewed (`SPINNER_LLM_*`).
- [ ] Objective polling configured (`SPINNER_OBJECTIVE_POLL_SECONDS`).
- [ ] At least one objective tested:
  - [ ] schedule-triggered
  - [ ] markdown-update-triggered
- [ ] Action approval flow tested:
  - [ ] `/pending-actions`
  - [ ] `/approve-action <id>`
  - [ ] `/deny-action <id>`

## F. Observability and Ops Readiness

- [ ] Log access/retention policy defined.
- [ ] On-call escalation path documented.
- [ ] Token/cert rotation cadence defined.
- [ ] Incident response steps reviewed with operators.

## G. Rollback Plan

- [ ] Previous known-good image/tag available.
- [ ] Rollback command documented for your environment.
- [ ] Data rollback policy defined (DB restore point + workspace snapshot).
- [ ] Verified ability to disable risky execution quickly:
  - [ ] `SPINNER_SANDBOX_ENABLED=false`
  - [ ] connector token revoke/regenerate procedure

## H. Sign-off

- [ ] Overlord sign-off
- [ ] Security/admin sign-off
- [ ] Operations sign-off
- [ ] Go-live timestamp recorded
