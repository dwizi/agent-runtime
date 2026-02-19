# Configuration Guide (Overlord/Admin)

This is the operator-focused environment reference.

## Core Runtime

- `AGENT_RUNTIME_ENV` (`production` recommended)
- `AGENT_RUNTIME_IMAGE_PLATFORM` (compose image platform override, default `linux/amd64`)
- `AGENT_RUNTIME_QMD_IMAGE_PLATFORM` (sidecar image platform override, default `linux/amd64`)
- `AGENT_RUNTIME_HTTP_ADDR` (default `:8080`)
- `AGENT_RUNTIME_DATA_DIR`
- `AGENT_RUNTIME_WORKSPACE_ROOT`
- `AGENT_RUNTIME_DB_PATH`
- `AGENT_RUNTIME_DEFAULT_CONCURRENCY`
- `AGENT_RUNTIME_EXT_PLUGINS_CONFIG` (default: `ext/plugins/plugins.json`)
- `AGENT_RUNTIME_EXT_PLUGIN_CACHE_DIR` (default: `${AGENT_RUNTIME_DATA_DIR}/agent-runtime/ext-plugin-cache`)
- `AGENT_RUNTIME_EXT_PLUGIN_WARM_ON_BOOTSTRAP` (default: `true`)
- `AGENT_RUNTIME_MCP_CONFIG` (default: `ext/mcp/servers.json`)
- `AGENT_RUNTIME_MCP_WORKSPACE_CONFIG_REL_PATH` (default: `context/mcp/servers.json`)
- `AGENT_RUNTIME_MCP_REFRESH_SECONDS` (default: `120`)
- `AGENT_RUNTIME_MCP_HTTP_TIMEOUT_SECONDS` (default: `30`)

## Hosts and TLS

- `PUBLIC_HOST`
- `ADMIN_HOST`
- `ACME_EMAIL`
- `AGENT_RUNTIME_ADMIN_API_URL`
- `AGENT_RUNTIME_ADMIN_TLS_SKIP_VERIFY`
- `AGENT_RUNTIME_ADMIN_TLS_CA_FILE`
- `AGENT_RUNTIME_ADMIN_TLS_CERT_FILE`
- `AGENT_RUNTIME_ADMIN_TLS_KEY_FILE`

Notes:
- Admin endpoint is mTLS-protected by Caddy.
- TUI can auto-sync local pki paths if env keys are empty.

## Channel Connectors

### Shared command sync
- `AGENT_RUNTIME_COMMAND_SYNC_ENABLED` (default: `true`)

### Telegram
- `AGENT_RUNTIME_TELEGRAM_TOKEN`
- `AGENT_RUNTIME_TELEGRAM_API_BASE`
- `AGENT_RUNTIME_TELEGRAM_POLL_SECONDS`

Telegram startup behavior:
- connector startup calls Telegram `setMyCommands`
- command names are normalized to Telegram format (for example `admin-channel` becomes `admin_channel`)

### Discord
- `AGENT_RUNTIME_DISCORD_TOKEN`
- `AGENT_RUNTIME_DISCORD_API_BASE`
- `AGENT_RUNTIME_DISCORD_GATEWAY_URL`
- `AGENT_RUNTIME_DISCORD_APPLICATION_ID` (optional)
- `AGENT_RUNTIME_DISCORD_COMMAND_GUILD_IDS` (optional CSV)

Discord startup behavior:
- connector startup upserts Discord application commands
- if `AGENT_RUNTIME_DISCORD_COMMAND_GUILD_IDS` is empty, commands are registered globally
- if `AGENT_RUNTIME_DISCORD_COMMAND_GUILD_IDS` is set, commands are registered per guild for faster visibility
- if `AGENT_RUNTIME_DISCORD_APPLICATION_ID` is empty, Agent Runtime resolves app id using Discord API

### Codex (optional proactive callback)
- `AGENT_RUNTIME_CODEX_PUBLISH_URL`
- `AGENT_RUNTIME_CODEX_PUBLISH_BEARER_TOKEN` (optional)
- `AGENT_RUNTIME_CODEX_PUBLISH_TIMEOUT_SECONDS` (default: `8`)

Codex behavior:
- when publish URL is set, outbound notifications for Codex contexts are POSTed as JSON:
  - `{"connector":"codex","external_id":"<session-id>","text":"<message>"}`
- when publish URL is empty, Codex publish remains log-only (no outbound callback request)

## LLM Provider and Policy

- `AGENT_RUNTIME_LLM_PROVIDER` (default: `openai`)
  - `openai`: Use for OpenAI, Z.ai, local Ollama/vLLM, or any OpenAI-compatible API.
  - `anthropic`: Use for Claude.
- `AGENT_RUNTIME_LLM_BASE_URL` (default: `https://api.openai.com/v1`)
- `AGENT_RUNTIME_LLM_API_KEY`
- `AGENT_RUNTIME_LLM_MODEL` (default: `gpt-4o`)
- `AGENT_RUNTIME_LLM_TIMEOUT_SECONDS` (default: `60`)
- `AGENT_RUNTIME_LLM_ENABLED`
- `AGENT_RUNTIME_LLM_ALLOW_DM`
- `AGENT_RUNTIME_LLM_REQUIRE_MENTION_IN_GROUPS`
- `AGENT_RUNTIME_LLM_ALLOWED_ROLES`
- `AGENT_RUNTIME_LLM_ALLOWED_CONTEXT_IDS`
- `AGENT_RUNTIME_LLM_RATE_LIMIT_PER_WINDOW`
- `AGENT_RUNTIME_LLM_RATE_LIMIT_WINDOW_SECONDS`
- `AGENT_RUNTIME_LLM_GROUNDING_TOP_K`
- `AGENT_RUNTIME_LLM_GROUNDING_MAX_DOC_EXCERPT_BYTES`
- `AGENT_RUNTIME_LLM_GROUNDING_MAX_PROMPT_BYTES`
- `AGENT_RUNTIME_LLM_GROUNDING_MAX_PROMPT_TOKENS`
- `AGENT_RUNTIME_LLM_GROUNDING_USER_MAX_TOKENS`
- `AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_MAX_TOKENS`
- `AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_MAX_TOKENS`
- `AGENT_RUNTIME_LLM_GROUNDING_QMD_MAX_TOKENS`
- `AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_LINES`
- `AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_BYTES`
- `AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_REFRESH_TURNS`
- `AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_MAX_ITEMS`
- `AGENT_RUNTIME_LLM_GROUNDING_MEMORY_SUMMARY_SOURCE_MAX_LINES`
- `AGENT_RUNTIME_LLM_ADMIN_SYSTEM_PROMPT`
- `AGENT_RUNTIME_LLM_PUBLIC_SYSTEM_PROMPT`
- `AGENT_RUNTIME_REASONING_PROMPT_FILE` (default: `/context/REASONING.md`)
- `AGENT_RUNTIME_SOUL_GLOBAL_FILE`
- `AGENT_RUNTIME_SOUL_WORKSPACE_REL_PATH`
- `AGENT_RUNTIME_SOUL_CONTEXT_REL_PATH`
- `AGENT_RUNTIME_SYSTEM_PROMPT_GLOBAL_FILE`
- `AGENT_RUNTIME_SYSTEM_PROMPT_WORKSPACE_REL_PATH`
- `AGENT_RUNTIME_SYSTEM_PROMPT_CONTEXT_REL_PATH`
- `AGENT_RUNTIME_SKILLS_GLOBAL_ROOT` (default: `/data/.agents/skills`)

### Provider Configuration Examples

**OpenAI (Default):**
```bash
AGENT_RUNTIME_LLM_PROVIDER=openai
AGENT_RUNTIME_LLM_BASE_URL=https://api.openai.com/v1
AGENT_RUNTIME_LLM_API_KEY=sk-...
AGENT_RUNTIME_LLM_MODEL=gpt-4o
```

**Z.ai:**
```bash
AGENT_RUNTIME_LLM_PROVIDER=openai
AGENT_RUNTIME_LLM_BASE_URL=https://api.z.ai/api/paas/v4
AGENT_RUNTIME_LLM_API_KEY=z-...
AGENT_RUNTIME_LLM_MODEL=glm-4.7-flash
```

**Local (Ollama/vLLM):**
```bash
AGENT_RUNTIME_LLM_PROVIDER=openai
AGENT_RUNTIME_LLM_BASE_URL=http://host.docker.internal:11434/v1
AGENT_RUNTIME_LLM_API_KEY=
AGENT_RUNTIME_LLM_MODEL=qwen2.5:7b
```

**Anthropic (Claude):**
```bash
AGENT_RUNTIME_LLM_PROVIDER=anthropic
AGENT_RUNTIME_LLM_BASE_URL=https://api.anthropic.com/v1
AGENT_RUNTIME_LLM_API_KEY=sk-ant-...
AGENT_RUNTIME_LLM_MODEL=claude-3-5-sonnet-latest
```

System prompt file precedence:
1. global file (`AGENT_RUNTIME_SYSTEM_PROMPT_GLOBAL_FILE`)
2. workspace override (`/data/workspaces/<workspace>/` + `AGENT_RUNTIME_SYSTEM_PROMPT_WORKSPACE_REL_PATH`)
3. context/agent override (`/data/workspaces/<workspace>/` + `AGENT_RUNTIME_SYSTEM_PROMPT_CONTEXT_REL_PATH`, where `{context_id}` is replaced)

SOUL precedence:
1. global file (`AGENT_RUNTIME_SOUL_GLOBAL_FILE`)
2. workspace override (`/data/workspaces/<workspace>/` + `AGENT_RUNTIME_SOUL_WORKSPACE_REL_PATH`)
3. context/agent override (`/data/workspaces/<workspace>/` + `AGENT_RUNTIME_SOUL_CONTEXT_REL_PATH`, where `{context_id}` is replaced)

Skill template loading order:
1. workspace context (`/data/workspaces/<workspace>/skills/contexts/<context_id>`)
2. workspace role (`/data/workspaces/<workspace>/skills/admin` or `/data/workspaces/<workspace>/skills/public`)
3. workspace common (`/data/workspaces/<workspace>/skills/common`)
4. global context (`AGENT_RUNTIME_SKILLS_GLOBAL_ROOT/contexts/<context_id>`)
5. global role (`AGENT_RUNTIME_SKILLS_GLOBAL_ROOT/admin` or `AGENT_RUNTIME_SKILLS_GLOBAL_ROOT/public`)
6. global common (`AGENT_RUNTIME_SKILLS_GLOBAL_ROOT/common`)

Notes:
- LLM endpoint is OpenAI-compatible `POST /chat/completions`. Update `AGENT_RUNTIME_LLM_BASE_URL` to point to your local server (for example Ollama/vLLM) or hosted provider, and use `AGENT_RUNTIME_LLM_MODEL` to pick the desired model.
- `AGENT_RUNTIME_LLM_API_KEY` is required for remote providers that enforce auth (OpenAI, Z.ai, Claude) but may stay empty for local endpoints configured without a key.
- workspace templates override global templates when filenames match.
- templates are summarized into system prompt context; keep each file concise.

## qmd / Markdown Retrieval

- `AGENT_RUNTIME_QMD_BINARY`
- `AGENT_RUNTIME_QMD_SIDECAR_URL`
- `AGENT_RUNTIME_QMD_SIDECAR_ADDR`
- `AGENT_RUNTIME_QMD_INDEX`
- `AGENT_RUNTIME_QMD_COLLECTION`
- `AGENT_RUNTIME_QMD_SHARED_MODELS_DIR`
- `AGENT_RUNTIME_QMD_EMBED_EXCLUDE_GLOBS`
- `AGENT_RUNTIME_QMD_SEARCH_LIMIT`
- `AGENT_RUNTIME_QMD_OPEN_MAX_BYTES`
- `AGENT_RUNTIME_QMD_DEBOUNCE_SECONDS`
- `AGENT_RUNTIME_QMD_INDEX_TIMEOUT_SECONDS`
- `AGENT_RUNTIME_QMD_QUERY_TIMEOUT_SECONDS`
- `AGENT_RUNTIME_QMD_AUTO_EMBED`

Notes:
- `agent-runtime` calls qmd through HTTP sidecar when `AGENT_RUNTIME_QMD_SIDECAR_URL` is set (compose default: `http://agent-runtime-qmd:8091`).
- In compose, `qmd-sidecar` is a standalone container that runs qmd directly.
- `AGENT_RUNTIME_QMD_SIDECAR_ADDR` controls the sidecar bind address.
- For host-native runs without sidecar, install `qmd` manually and keep it on `PATH`.
- `AGENT_RUNTIME_QMD_SHARED_MODELS_DIR` defaults to `/data/qmd-models` so model downloads are reused across all workspaces.
- `AGENT_RUNTIME_QMD_AUTO_EMBED` remains supported; known Bun/NAPI embed crashes are handled as non-fatal so indexing can continue.
- `AGENT_RUNTIME_QMD_EMBED_EXCLUDE_GLOBS` accepts comma-separated path globs (relative to workspace) to prevent those file changes from triggering embed runs (for example: `logs/chats/**`).

## Heartbeat and Supervision

- `AGENT_RUNTIME_HEARTBEAT_ENABLED`
- `AGENT_RUNTIME_HEARTBEAT_INTERVAL_SECONDS`
- `AGENT_RUNTIME_HEARTBEAT_STALE_SECONDS`
- `AGENT_RUNTIME_HEARTBEAT_NOTIFY_ADMIN`
- `AGENT_RUNTIME_TRIAGE_ENABLED`
- `AGENT_RUNTIME_TRIAGE_NOTIFY_ADMIN`

API endpoint:
- `GET /api/v1/heartbeat`

Behavior:
- tracks health state transitions for runtime components
- marks stale components when heartbeat age exceeds `AGENT_RUNTIME_HEARTBEAT_STALE_SECONDS`
- optionally notifies admin channels on degraded/recovered transitions
- writes workspace heartbeat transitions to `/data/workspaces/<workspace-id>/ops/heartbeat.md`
- controls auto triage routing and admin routing notifications for Discord/Telegram messages

## Objectives and Proactivity

- `AGENT_RUNTIME_OBJECTIVE_POLL_SECONDS`
- `AGENT_RUNTIME_TASK_NOTIFY_POLICY` (`both` | `admin` | `origin`)
- `AGENT_RUNTIME_TASK_NOTIFY_SUCCESS_POLICY` (`both` | `admin` | `origin`, optional override)
- `AGENT_RUNTIME_TASK_NOTIFY_FAILURE_POLICY` (`both` | `admin` | `origin`, optional override)
- `AGENT_RUNTIME_AGENT_SENSITIVE_APPROVAL_TTL_SECONDS` (default `600`)
- detailed flow and API payload examples: `docs/objectives-flow.md`

Notification behavior:
- routed chat tasks send natural-language success replies (no task log formatting)
- routed task failures are delivered only to admin-marked channels
- non-admin channels do not receive failure notifications
- sensitive-agent approvals from `/approve-action` are valid for one follow-up agent turn and expire after the configured TTL

API endpoints:
- `POST /api/v1/objectives`
- `GET /api/v1/objectives`
- `POST /api/v1/objectives/update`
- `POST /api/v1/objectives/active`
- `POST /api/v1/objectives/delete`

## IMAP / SMTP

### IMAP ingestion
- `AGENT_RUNTIME_IMAP_HOST`
- `AGENT_RUNTIME_IMAP_PORT`
- `AGENT_RUNTIME_IMAP_USERNAME`
- `AGENT_RUNTIME_IMAP_PASSWORD`
- `AGENT_RUNTIME_IMAP_MAILBOX`
- `AGENT_RUNTIME_IMAP_POLL_SECONDS`
- `AGENT_RUNTIME_IMAP_TLS_SKIP_VERIFY`

### SMTP actions
- `AGENT_RUNTIME_SMTP_HOST`
- `AGENT_RUNTIME_SMTP_PORT`
- `AGENT_RUNTIME_SMTP_USERNAME`
- `AGENT_RUNTIME_SMTP_PASSWORD`
- `AGENT_RUNTIME_SMTP_FROM`

## Sandboxed Command Execution

- `AGENT_RUNTIME_SANDBOX_ENABLED`
- `AGENT_RUNTIME_SANDBOX_ALLOWED_COMMANDS`
- `AGENT_RUNTIME_SANDBOX_RUNNER_COMMAND`
- `AGENT_RUNTIME_SANDBOX_RUNNER_ARGS`
- `AGENT_RUNTIME_SANDBOX_TIMEOUT_SECONDS`

Recommended baseline:
- keep allowlist minimal (`curl,rg,cat,ls` unless you need more)
- use a runner wrapper for stronger isolation when available
- for Vercel `skills` installs, include `node,npm,npx` (and optionally `bun,bunx`) in `AGENT_RUNTIME_SANDBOX_ALLOWED_COMMANDS`

## External Plugins

- `AGENT_RUNTIME_EXT_PLUGINS_CONFIG` (default: `ext/plugins/plugins.json`)
- `AGENT_RUNTIME_EXT_PLUGIN_CACHE_DIR` (default: `/data/agent-runtime/ext-plugin-cache`)
- `AGENT_RUNTIME_EXT_PLUGIN_WARM_ON_BOOTSTRAP` (default: `true`)
- `AGENT_RUNTIME_TINYFISH_API_KEY` (used by `ext/plugins/tinyfish`)
- `AGENT_RUNTIME_TINYFISH_BASE_URL` (optional override, default `https://agent.tinyfish.ai`)
- `AGENT_RUNTIME_RESEND_API_KEY` (used by `ext/plugins/resend`)
- `AGENT_RUNTIME_RESEND_FROM` (default sender for `resend_email`)
- `AGENT_RUNTIME_RESEND_API_BASE` (optional override, default `https://api.resend.com`)

Notes:
- Third-party plugins under `ext/plugins/` are disabled unless explicitly enabled in the plugin config file.
- Default config file: `ext/plugins/plugins.json`.
- Generic executable plugins are enabled via `external_plugins[]` entries that point to a `plugin.json` manifest.
- Manifest `runtime.command` is executed with `runtime.args`; stdin/stdout use JSON contract documented in `ext/plugins/README.md`.
- For `runtime.isolation.mode=uv`, runtime warms and caches plugin envs under `AGENT_RUNTIME_EXT_PLUGIN_CACHE_DIR`.
- External plugin execution reuses sandbox runner settings (`AGENT_RUNTIME_SANDBOX_RUNNER_COMMAND`, `AGENT_RUNTIME_SANDBOX_RUNNER_ARGS`) when set.
- Warmup failures are non-fatal at startup; execution path attempts lazy uv sync before failing the action.
- `ext/plugins/` is reserved for external plugin assets/manifests, not runtime app code.
- review action approvals in admin channels before execution

## MCP Servers

- `AGENT_RUNTIME_MCP_CONFIG` (default: `ext/mcp/servers.json`)
- `AGENT_RUNTIME_MCP_WORKSPACE_CONFIG_REL_PATH` (default: `context/mcp/servers.json`)
- `AGENT_RUNTIME_MCP_REFRESH_SECONDS` (default: `120`)
- `AGENT_RUNTIME_MCP_HTTP_TIMEOUT_SECONDS` (default: `30`)

Notes:
- MCP servers are loaded from `ext/mcp/servers.json`.
- Supported transport types: `streamable_http`, `sse`.
- Header values support env templates such as `${AGENT_RUNTIME_MY_TOKEN}`.
- Missing env vars for templated values invalidate that server config.
- Workspace overrides are read from `/data/workspaces/<workspace-id>/context/mcp/servers.json`.
- Workspace overrides are override-only for existing global server IDs; unknown IDs are ignored with warning logs.
- MCP tool names are registered with prefix format `mcp_<server_id>__<tool_name>`.
- Startup does not fail when a server is unreachable; status is degraded and retried on refresh.
