# Install and Bootstrap (Overlord/Admin)

This runbook covers first-time installation and secure bootstrap.

## 1. Prerequisites

- Docker + Docker Compose plugin
- Go toolchain (for local `make run` / `make tui`)
- Domain/DNS ready if deploying publicly

Notes:
- Docker runtime image already includes `qmd`.
- Install local `qmd` only when running Agent Runtime directly on host (`make run`, `make tui`).

## 2. Clone and Prepare

```bash
git clone <repo-url> agent-runtime
cd agent-runtime
cp .env.example .env
```

If you need to force architecture (for example, run x86_64 images on arm64 hosts), set:
- `AGENT_RUNTIME_IMAGE_PLATFORM=linux/amd64`
- `AGENT_RUNTIME_QMD_IMAGE_PLATFORM=linux/amd64`

## 3. Set Required Secrets in `.env`

Minimum for production bootstrap:

- `AGENT_RUNTIME_LLM_PROVIDER` (default `openai`; set to `anthropic` for Claude, and configure `AGENT_RUNTIME_LLM_BASE_URL`, `AGENT_RUNTIME_LLM_MODEL`, and `AGENT_RUNTIME_LLM_API_KEY` as needed)
- `AGENT_RUNTIME_DISCORD_TOKEN` (if enabling Discord)
- `AGENT_RUNTIME_TELEGRAM_TOKEN` (if enabling Telegram)
- `PUBLIC_HOST`
- `ADMIN_HOST`
- `ACME_EMAIL`

`AGENT_RUNTIME_LLM_API_KEY` is required for authenticated remote providers (OpenAI, Z.ai, Claude) but may stay empty when pointing `AGENT_RUNTIME_LLM_BASE_URL` at a local Ollama/vLLM endpoint.

Optional but recommended for connector command UX:
- `AGENT_RUNTIME_COMMAND_SYNC_ENABLED=true`
- `AGENT_RUNTIME_DISCORD_COMMAND_GUILD_IDS=<guild-id-csv>` (faster slash-command visibility)
- `AGENT_RUNTIME_DISCORD_APPLICATION_ID=<app-id>` (when automatic app id lookup is restricted)

Then set admin API target for local TUI:

- `AGENT_RUNTIME_ADMIN_API_URL=https://<ADMIN_HOST>`

## 4. Start the Stack

```bash
make compose-up
```

What this does:
- starts `agent-runtime` and `caddy`
- provisions local admin mTLS material under `ops/caddy/pki`
- syncs missing mTLS env paths into `.env`
- bind-mounts host paths for live editing:
  - `./data` -> `/data` (workspaces, sqlite, task outputs)
  - `./context` -> `/context` (global SOUL, system prompt, and skills files)
- bootstraps connector command sync (Telegram menu commands + Discord slash command upsert) if enabled

Optional qmd tooling sidecar:

```bash
docker compose --profile qmd-sidecar up -d qmd-sidecar
```

## 5. Verify Runtime Health

```bash
curl -fsS http://localhost/healthz
curl -fsS http://localhost/readyz
curl -fsS http://localhost/api/v1/heartbeat
curl -fsS http://localhost/api/v1/info
```

Expected:
- `healthz` = `{"status":"ok"}`
- `readyz` = `{"status":"ready"}`
- `heartbeat` includes component states and `overall`

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

- Set `AGENT_RUNTIME_ADMIN_TLS_SKIP_VERIFY=false` for operator clients.
- Rotate connector tokens after initial validation.
- Set sandbox policy:
  - `AGENT_RUNTIME_SANDBOX_ALLOWED_COMMANDS`
  - `AGENT_RUNTIME_SANDBOX_RUNNER_COMMAND` (if using isolation wrapper)
- Set objective polling and IMAP only when needed.

## 9. Local Dev Mode (optional)

If you run without Docker:

```bash
make run
make tui
```

Use this for iterative development, not final production hardening.
