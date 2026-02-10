# Configuration Guide (Overlord/Admin)

This is the operator-focused environment reference.

## Core Runtime

- `SPINNER_ENV` (`production` recommended)
- `SPINNER_IMAGE_PLATFORM` (compose image platform override, default `linux/amd64`)
- `SPINNER_QMD_IMAGE_PLATFORM` (sidecar image platform override, default `linux/amd64`)
- `SPINNER_HTTP_ADDR` (default `:8080`)
- `SPINNER_DATA_DIR`
- `SPINNER_WORKSPACE_ROOT`
- `SPINNER_DB_PATH`
- `SPINNER_DEFAULT_CONCURRENCY`

## Hosts and TLS

- `PUBLIC_HOST`
- `ADMIN_HOST`
- `ACME_EMAIL`
- `SPINNER_ADMIN_API_URL`
- `SPINNER_ADMIN_TLS_SKIP_VERIFY`
- `SPINNER_ADMIN_TLS_CA_FILE`
- `SPINNER_ADMIN_TLS_CERT_FILE`
- `SPINNER_ADMIN_TLS_KEY_FILE`

Notes:
- Admin endpoint is mTLS-protected by Caddy.
- TUI can auto-sync local pki paths if env keys are empty.

## Channel Connectors

### Shared command sync
- `SPINNER_COMMAND_SYNC_ENABLED` (default: `true`)

### Telegram
- `SPINNER_TELEGRAM_TOKEN`
- `SPINNER_TELEGRAM_API_BASE`
- `SPINNER_TELEGRAM_POLL_SECONDS`

Telegram startup behavior:
- connector startup calls Telegram `setMyCommands`
- command names are normalized to Telegram format (for example `admin-channel` becomes `admin_channel`)

### Discord
- `SPINNER_DISCORD_TOKEN`
- `SPINNER_DISCORD_API_BASE`
- `SPINNER_DISCORD_GATEWAY_URL`
- `SPINNER_DISCORD_APPLICATION_ID` (optional)
- `SPINNER_DISCORD_COMMAND_GUILD_IDS` (optional CSV)

Discord startup behavior:
- connector startup upserts Discord application commands
- if `SPINNER_DISCORD_COMMAND_GUILD_IDS` is empty, commands are registered globally
- if `SPINNER_DISCORD_COMMAND_GUILD_IDS` is set, commands are registered per guild for faster visibility
- if `SPINNER_DISCORD_APPLICATION_ID` is empty, Spinner resolves app id using Discord API

## LLM Provider and Policy

- `SPINNER_ZAI_API_KEY`
- `SPINNER_ZAI_BASE_URL`
- `SPINNER_ZAI_MODEL`
- `SPINNER_ZAI_TIMEOUT_SECONDS`
- `SPINNER_LLM_ENABLED`
- `SPINNER_LLM_ALLOW_DM`
- `SPINNER_LLM_REQUIRE_MENTION_IN_GROUPS`
- `SPINNER_LLM_ALLOWED_ROLES`
- `SPINNER_LLM_ALLOWED_CONTEXT_IDS`
- `SPINNER_LLM_RATE_LIMIT_PER_WINDOW`
- `SPINNER_LLM_RATE_LIMIT_WINDOW_SECONDS`
- `SPINNER_LLM_ADMIN_SYSTEM_PROMPT`
- `SPINNER_LLM_PUBLIC_SYSTEM_PROMPT`
- `SPINNER_SOUL_GLOBAL_FILE`
- `SPINNER_SOUL_WORKSPACE_REL_PATH`
- `SPINNER_SOUL_CONTEXT_REL_PATH`
- `SPINNER_SYSTEM_PROMPT_GLOBAL_FILE`
- `SPINNER_SYSTEM_PROMPT_WORKSPACE_REL_PATH`
- `SPINNER_SYSTEM_PROMPT_CONTEXT_REL_PATH`
- `SPINNER_SKILLS_GLOBAL_ROOT`

System prompt file precedence:
1. global file (`SPINNER_SYSTEM_PROMPT_GLOBAL_FILE`)
2. workspace override (`/data/workspaces/<workspace>/` + `SPINNER_SYSTEM_PROMPT_WORKSPACE_REL_PATH`)
3. context/agent override (`/data/workspaces/<workspace>/` + `SPINNER_SYSTEM_PROMPT_CONTEXT_REL_PATH`, where `{context_id}` is replaced)

SOUL precedence:
1. global file (`SPINNER_SOUL_GLOBAL_FILE`)
2. workspace override (`/data/workspaces/<workspace>/` + `SPINNER_SOUL_WORKSPACE_REL_PATH`)
3. context/agent override (`/data/workspaces/<workspace>/` + `SPINNER_SOUL_CONTEXT_REL_PATH`, where `{context_id}` is replaced)

Skill template loading order:
1. workspace context (`/data/workspaces/<workspace>/skills/contexts/<context_id>`)
2. workspace role (`/data/workspaces/<workspace>/skills/admin` or `/data/workspaces/<workspace>/skills/public`)
3. workspace common (`/data/workspaces/<workspace>/skills/common`)
4. global context (`SPINNER_SKILLS_GLOBAL_ROOT/contexts/<context_id>`)
5. global role (`SPINNER_SKILLS_GLOBAL_ROOT/admin` or `SPINNER_SKILLS_GLOBAL_ROOT/public`)
6. global common (`SPINNER_SKILLS_GLOBAL_ROOT/common`)

Notes:
- LLM endpoint is OpenAI-compatible `POST /chat/completions`. You can point it to local Qwen (for example Ollama/vLLM) by setting `SPINNER_ZAI_BASE_URL` to a local URL and `SPINNER_ZAI_MODEL` to your local model name.
- `SPINNER_ZAI_API_KEY` is required for `api.z.ai`; for local endpoints it can be left empty.
- workspace templates override global templates when filenames match.
- templates are summarized into system prompt context; keep each file concise.

## qmd / Markdown Retrieval

- `SPINNER_QMD_BINARY`
- `SPINNER_QMD_SIDECAR_URL`
- `SPINNER_QMD_SIDECAR_ADDR`
- `SPINNER_QMD_INDEX`
- `SPINNER_QMD_COLLECTION`
- `SPINNER_QMD_SHARED_MODELS_DIR`
- `SPINNER_QMD_EMBED_EXCLUDE_GLOBS`
- `SPINNER_QMD_SEARCH_LIMIT`
- `SPINNER_QMD_OPEN_MAX_BYTES`
- `SPINNER_QMD_DEBOUNCE_SECONDS`
- `SPINNER_QMD_INDEX_TIMEOUT_SECONDS`
- `SPINNER_QMD_QUERY_TIMEOUT_SECONDS`
- `SPINNER_QMD_AUTO_EMBED`

Notes:
- `spinner` calls qmd through HTTP sidecar when `SPINNER_QMD_SIDECAR_URL` is set (compose default: `http://spinner-qmd:8091`).
- In compose, `qmd-sidecar` is a standalone container that runs qmd directly.
- `SPINNER_QMD_SIDECAR_ADDR` controls the sidecar bind address.
- For host-native runs without sidecar, install `qmd` manually and keep it on `PATH`.
- `SPINNER_QMD_SHARED_MODELS_DIR` defaults to `/data/qmd-models` so model downloads are reused across all workspaces.
- `SPINNER_QMD_AUTO_EMBED` remains supported; known Bun/NAPI embed crashes are handled as non-fatal so indexing can continue.
- `SPINNER_QMD_EMBED_EXCLUDE_GLOBS` accepts comma-separated path globs (relative to workspace) to prevent those file changes from triggering embed runs (for example: `logs/chats/**`).

## Heartbeat and Supervision

- `SPINNER_HEARTBEAT_ENABLED`
- `SPINNER_HEARTBEAT_INTERVAL_SECONDS`
- `SPINNER_HEARTBEAT_STALE_SECONDS`
- `SPINNER_HEARTBEAT_NOTIFY_ADMIN`
- `SPINNER_TRIAGE_ENABLED`
- `SPINNER_TRIAGE_NOTIFY_ADMIN`

API endpoint:
- `GET /api/v1/heartbeat`

Behavior:
- tracks health state transitions for runtime components
- marks stale components when heartbeat age exceeds `SPINNER_HEARTBEAT_STALE_SECONDS`
- optionally notifies admin channels on degraded/recovered transitions
- writes workspace heartbeat transitions to `/data/workspaces/<workspace-id>/ops/heartbeat.md`
- controls auto triage routing and admin routing notifications for Discord/Telegram messages

## Objectives and Proactivity

- `SPINNER_OBJECTIVE_POLL_SECONDS`
- `SPINNER_TASK_NOTIFY_POLICY` (`both` | `admin` | `origin`)
- `SPINNER_TASK_NOTIFY_SUCCESS_POLICY` (`both` | `admin` | `origin`, optional override)
- `SPINNER_TASK_NOTIFY_FAILURE_POLICY` (`both` | `admin` | `origin`, optional override)

Notification behavior:
- routed chat tasks send natural-language success replies (no task log formatting)
- routed task failures are delivered only to admin-marked channels
- non-admin channels do not receive failure notifications

API endpoints:
- `POST /api/v1/objectives`
- `GET /api/v1/objectives`
- `POST /api/v1/objectives/update`
- `POST /api/v1/objectives/active`
- `POST /api/v1/objectives/delete`

## IMAP / SMTP

### IMAP ingestion
- `SPINNER_IMAP_HOST`
- `SPINNER_IMAP_PORT`
- `SPINNER_IMAP_USERNAME`
- `SPINNER_IMAP_PASSWORD`
- `SPINNER_IMAP_MAILBOX`
- `SPINNER_IMAP_POLL_SECONDS`
- `SPINNER_IMAP_TLS_SKIP_VERIFY`

### SMTP actions
- `SPINNER_SMTP_HOST`
- `SPINNER_SMTP_PORT`
- `SPINNER_SMTP_USERNAME`
- `SPINNER_SMTP_PASSWORD`
- `SPINNER_SMTP_FROM`

## Sandboxed Command Execution

- `SPINNER_SANDBOX_ENABLED`
- `SPINNER_SANDBOX_ALLOWED_COMMANDS`
- `SPINNER_SANDBOX_RUNNER_COMMAND`
- `SPINNER_SANDBOX_RUNNER_ARGS`
- `SPINNER_SANDBOX_TIMEOUT_SECONDS`

Recommended baseline:
- keep allowlist minimal (`curl,rg,cat,ls` unless you need more)
- use a runner wrapper for stronger isolation when available
- review action approvals in admin channels before execution
