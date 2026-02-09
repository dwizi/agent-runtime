# Install and Bootstrap (Overlord/Admin)

This runbook covers first-time installation and secure bootstrap.

## 1. Prerequisites

- Docker + Docker Compose plugin
- Go toolchain (for local `make run` / `make tui`)
- `qmd` installed and available in `PATH`
- Domain/DNS ready if deploying publicly

## 2. Clone and Prepare

```bash
git clone <repo-url> spinner
cd spinner
cp .env.example .env
```

## 3. Set Required Secrets in `.env`

Minimum for production bootstrap:

- `SPINNER_ZAI_API_KEY`
- `SPINNER_DISCORD_TOKEN` (if enabling Discord)
- `SPINNER_TELEGRAM_TOKEN` (if enabling Telegram)
- `PUBLIC_HOST`
- `ADMIN_HOST`
- `ACME_EMAIL`

Then set admin API target for local TUI:

- `SPINNER_ADMIN_API_URL=https://<ADMIN_HOST>`

## 4. Start the Stack

```bash
make compose-up
```

What this does:
- starts `spinner` and `caddy`
- provisions local admin mTLS material under `ops/caddy/pki`
- syncs missing mTLS env paths into `.env`
- bind-mounts host paths for live editing:
  - `./data` -> `/data` (workspaces, sqlite, task outputs)
  - `./context` -> `/context` (global SOUL files)

## 5. Verify Runtime Health

```bash
curl -fsS http://localhost/healthz
curl -fsS http://localhost/readyz
curl -fsS http://localhost/api/v1/info
```

Expected:
- `healthz` = `{"status":"ok"}`
- `readyz` = `{"status":"ready"}`

## 6. Bootstrap First Admin Identity

1. DM the Telegram or Discord bot with `pair`.
2. Copy the one-time token.
3. Open TUI:
   ```bash
   make tui
   ```
4. Paste token and approve (`a`).

Result: connector identity is linked and can issue admin commands.

## 7. Set First Admin Channel

In your admin chat/channel:

```text
/admin-channel enable
```

This marks the context as admin scope for stricter policy use.

## 8. Recommended Immediate Hardening

- Set `SPINNER_ADMIN_TLS_SKIP_VERIFY=false` for operator clients.
- Rotate connector tokens after initial validation.
- Set sandbox policy:
  - `SPINNER_SANDBOX_ALLOWED_COMMANDS`
  - `SPINNER_SANDBOX_RUNNER_COMMAND` (if using isolation wrapper)
- Set objective polling and IMAP only when needed.

## 9. Local Dev Mode (optional)

If you run without Docker:

```bash
make run
make tui
```

Use this for iterative development, not final production hardening.
