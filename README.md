# spinner

Security-first, cloud-agnostic, multi-channel agent orchestrator for communities.

`spinner` runs as Docker Compose with:
- `caddy` for TLS, reverse proxy, and admin mTLS.
- `spinner` (Go) for gateway, orchestration, task engine, and markdown indexing triggers.

## What is included in this scaffold

- Go CLI with:
  - `spinner serve` for API + runtime services
  - `spinner tui` for a Charm Bubble Tea admin interface
- SQLite bootstrap and initial schema (`users`, `identities`, `workspaces`, `contexts`, `tasks`, `pairing_requests`)
- Discord gateway connector (`MESSAGE_CREATE`) with command routing and DM pairing
- Telegram command gateway with pairing and task/admin commands
- IMAP inbox connector for inbound email ingestion to workspace Markdown
- In-memory task engine with configurable per-workspace concurrency defaults
- Markdown file watcher for `.md` updates under `/data/workspaces`
- qmd-backed retrieval with per-workspace index isolation and debounced re-indexing
- Caddy reverse proxy with separate public/admin hosts and admin mTLS

## Quickstart

1. Copy env:
   - `cp .env.example .env`
2. Install qmd locally only if you run `spinner` outside Docker (`make run`, `make tui`):
   - `bun install -g github:tobi/qmd`
   - ensure `qmd` is in `PATH`
3. Run local:
   - `make run`
4. Run TUI:
   - `make tui`
5. Run Docker stack:
   - `docker compose up -d --build`
   - or `make compose-up` (also auto-syncs local mTLS paths into `.env` when empty)
   - Docker image already includes `qmd` for `/search` and `/open`
   - optional tools shell with qmd mounted to workspace data:
     - `docker compose --profile qmd-sidecar up -d qmd-sidecar`

Health endpoints:
- `GET /healthz`
- `GET /readyz`
- `GET /api/v1/heartbeat`
- `GET /api/v1/info`
- `POST /api/v1/tasks`
- `POST /api/v1/pairings/start`
- `GET /api/v1/pairings/lookup?token=<token>`
- `POST /api/v1/pairings/approve`
- `POST /api/v1/pairings/deny`
- `POST /api/v1/objectives`
- `GET /api/v1/objectives?workspace_id=<id>`
- `POST /api/v1/objectives/update`
- `POST /api/v1/objectives/active`
- `POST /api/v1/objectives/delete`

Pairing flow (Telegram DM -> TUI approve):
1. Linked Telegram bot receives `pair` (or `/pair`) in private DM.
2. Connector creates a one-time token and sends it back in DM.
3. Admin runs `spinner tui`, pastes token, and presses:
   - `a` to approve and link identity
   - `d` to deny

Telegram command gateway:
- `/task <prompt>` creates a queued task in the current chat context
- `/search <query>` runs workspace-scoped qmd search
- `/open <path-or-docid>` opens markdown from the current workspace
- `/status` reports qmd index health for the current workspace
- `/prompt show` displays the current context system prompt (admin/overlord only)
- `/prompt set <text>` sets a context-specific system prompt (admin/overlord only)
- `/prompt clear` clears the context-specific system prompt (admin/overlord only)
- `/admin-channel enable` marks the current chat context as admin-only (requires linked `admin` or `overlord`)
- `/route <task-id> <question|issue|task|moderation|noise> [p1|p2|p3] [due-window]` overrides triage routing (admin/overlord)
- `/approve <pairing-token>` approves a pending pairing (requires linked `admin` or `overlord`)
- `/deny <pairing-token> [reason]` denies a pending pairing (requires linked `admin` or `overlord`)
- `/pending-actions` lists pending LLM-proposed actions (admin/overlord)
- `/approve-action <action-id>` approves a pending action (admin/overlord)
- `/deny-action <action-id> [reason]` denies a pending action (admin/overlord)
- Intent fallback: messages like `task write release notes` also create tasks
- `.md` attachments are ingested into workspace storage under `inbox/telegram/<chat-id>/...`
- every inbound/outbound channel message is persisted to Markdown at `logs/chats/telegram/<chat-id>.md`
- LLM replies (GLM Flash 4.7 via z.ai):
  - replies in DM chats
  - replies in group chats only when bot is mentioned (e.g. `@your_bot_username`)

Discord command gateway:
- Listens to Discord Gateway `MESSAGE_CREATE` events (bot token + intents required)
- Uses the same command set as Telegram from plain message content (`/task`, `/search`, `/open`, `/status`, `/prompt`, `/admin-channel enable`, `/route`, `/approve`, `/deny`, `/pending-actions`, `/approve-action`, `/deny-action`)
- Supports DM `pair` for one-time token generation
- `.md` attachments are ingested into workspace storage under `inbox/discord/<channel-id>/...`
- every inbound/outbound channel message is persisted to Markdown at `logs/chats/discord/<channel-id>.md`
- LLM replies (GLM Flash 4.7 via z.ai):
  - replies in DMs
  - replies in guild channels only when the bot is tagged/mentioned

z.ai runtime env:
- `SPINNER_ZAI_API_KEY` (required for channel LLM replies)
- `SPINNER_ZAI_BASE_URL` (default: `https://api.z.ai/api/paas/v4`)
- `SPINNER_ZAI_MODEL` (default: `glm-4.7-flash`)
- `SPINNER_ZAI_TIMEOUT_SECONDS` (default: `45`)

IMAP runtime env:
- `SPINNER_IMAP_HOST` (required to enable IMAP connector)
- `SPINNER_IMAP_PORT` (default: `993`)
- `SPINNER_IMAP_USERNAME`
- `SPINNER_IMAP_PASSWORD`
- `SPINNER_IMAP_MAILBOX` (default: `INBOX`)
- `SPINNER_IMAP_POLL_SECONDS` (default: `60`)
- `SPINNER_IMAP_TLS_SKIP_VERIFY` (default: `false`)

IMAP ingestion behavior:
- unread emails are polled from the configured mailbox
- each message is written to workspace Markdown under `inbox/imap/<mailbox>/<uid>-<subject>.md`
- `.md` email attachments are extracted to `inbox/imap/<mailbox>/attachments/`
- dedupe prevents re-ingesting already-processed messages (`uid`/`message-id`)
- each ingested message queues a review task in the orchestrator

Objective scheduler runtime env:
- `SPINNER_OBJECTIVE_POLL_SECONDS` (default: `15`)

Heartbeat runtime env:
- `SPINNER_HEARTBEAT_ENABLED` (default: `true`)
- `SPINNER_HEARTBEAT_INTERVAL_SECONDS` (default: `30`)
- `SPINNER_HEARTBEAT_STALE_SECONDS` (default: `120`)
- `SPINNER_HEARTBEAT_NOTIFY_ADMIN` (default: `true`)

Heartbeat behavior:
- monitors orchestrator, scheduler, watcher, connectors, and API loop
- exposes current health states at `GET /api/v1/heartbeat`
- emits admin notifications on degraded/recovered state transitions
- appends transition log entries to `/data/workspaces/<workspace-id>/ops/heartbeat.md` for workspaces with admin channels

Message triage behavior:
- non-command inbound messages are auto-classified (`question`, `issue`, `task`, `moderation`, `noise`)
- non-noise classifications auto-create routed tasks with priority and due windows
- routed tasks persist source metadata and assignment lane for audit and operations
- routing decisions are posted to workspace admin channels with `/route ...` override examples

Objectives and proactivity:
- objectives can be stored as:
  - `trigger_type: "schedule"` with `interval_seconds` (and optional `next_run_unix`)
  - `trigger_type: "event"` with `event_key` (currently `markdown.updated`)
- scheduler polls due schedule objectives and enqueues `objective` tasks
- markdown file changes can trigger event objectives for the changed workspace
- create/list objectives via API:
  - `POST /api/v1/objectives`
  - `GET /api/v1/objectives?workspace_id=<workspace-id>&active_only=true`
- manage objectives via API:
  - `POST /api/v1/objectives/update`
  - `POST /api/v1/objectives/active` (pause/resume)
  - `POST /api/v1/objectives/delete`
- TUI objective operations:
  - press `Tab` to switch to Objectives mode
  - type workspace id + `Enter` to load
  - `j/k` select, `p` pause/resume, `x` delete, `r` refresh

SMTP runtime env (for `send_email` approvals):
- `SPINNER_SMTP_HOST` (required for SMTP email execution)
- `SPINNER_SMTP_PORT` (default: `587`)
- `SPINNER_SMTP_USERNAME` (optional)
- `SPINNER_SMTP_PASSWORD` (required when username is set)
- `SPINNER_SMTP_FROM` (default sender, can be overridden by approved action payload `from`)

Sandbox command runtime env:
- `SPINNER_SANDBOX_ENABLED` (default: `true`)
- `SPINNER_SANDBOX_ALLOWED_COMMANDS` CSV allowlist (default: `echo,cat,ls,curl,grep,head,tail`)
- `SPINNER_SANDBOX_RUNNER_COMMAND` optional wrapper binary for isolation (e.g. `just-bash`)
- `SPINNER_SANDBOX_RUNNER_ARGS` optional runner arguments (space separated)
- `SPINNER_SANDBOX_TIMEOUT_SECONDS` (default: `20`)

LLM grounding:
- before generating a reply, Spinner runs qmd search in the current workspace and injects top Markdown context snippets/excerpts into the prompt.

Prompt policies and skill templates:
- context prompt policy is stored in `contexts.system_prompt` and can be managed with `/prompt ...`
- default baseline prompts:
  - `SPINNER_LLM_ADMIN_SYSTEM_PROMPT`
  - `SPINNER_LLM_PUBLIC_SYSTEM_PROMPT`
- optional skill templates are loaded from workspace files:
  - `skills/common/*.md`
  - `skills/admin/*.md` or `skills/public/*.md`
  - `skills/contexts/<context-id>/*.md`

LLM safety controls:
- `SPINNER_LLM_ENABLED` toggles all LLM replies
- `SPINNER_LLM_ALLOW_DM` allows/blocks DM LLM replies
- `SPINNER_LLM_REQUIRE_MENTION_IN_GROUPS` requires mention/tag for group/channel replies
- `SPINNER_LLM_ALLOWED_ROLES` optional CSV role allowlist (empty = all roles)
- `SPINNER_LLM_ALLOWED_CONTEXT_IDS` optional CSV context allowlist (empty = all contexts)
- `SPINNER_LLM_RATE_LIMIT_PER_WINDOW` and `SPINNER_LLM_RATE_LIMIT_WINDOW_SECONDS` configure LLM reply limits
- rate limit applies to non-admin users only (`admin`, `overlord` bypass)

LLM action approvals:
- when the LLM includes an action proposal block:
  - ```action {"type":"...","target":"...","summary":"..."} ```
- Spinner stores it as a pending approval in SQLite and posts an approval notice with action id.
- no external action executes automatically until an admin explicitly approves it.
- `/approve-action <id>` runs the selected plugin and stores execution status (`succeeded`, `failed`, or `skipped`) plus execution message.
- `/deny-action <id> [reason]` keeps the action unexecuted and records an audit reason.

Generic external action plugin (initial):
- `type: "http_request"` or `type: "webhook"` executes an outbound HTTP request.
- `target` (or `payload.url`) is the request URL.
- `payload` supports:
  - `method` (default `POST`)
  - `headers` (object)
  - `json` (object body, auto-encoded)
  - `body` (raw string body)
- `type: "send_email"` or `type: "smtp_email"` sends SMTP email via configured relay.
- `target` or `payload.to` defines recipient(s) (comma-separated or array).
- `payload` for email supports:
  - `subject`
  - `body` / `text`
  - `html`
  - `cc`, `bcc`, `from`
- `type: "run_command"` or `type: "shell_command"` executes a sandboxed command.
- command action payload:
  - `target` or `payload.command` for binary name
  - `payload.args` as array or string
  - optional `payload.cwd` (must stay inside workspace root)
- command execution is allowlist-based and always requires admin approval first.
- commands must be bare executable names (no absolute/relative paths).
- command output is truncated to a safe size before persistence/response.

## mTLS artifacts

On first start, Caddy generates admin mTLS assets in `ops/caddy/pki`:
- `clients-ca.crt`
- `admin-client.crt`
- `admin-client.key`
- `admin-client.p12` (password: `spinner`)

Local helper:
- `make compose-up` or `make sync-env` fills these vars in `.env` only when empty:
  - `SPINNER_ADMIN_TLS_CA_FILE`
  - `SPINNER_ADMIN_TLS_CERT_FILE`
  - `SPINNER_ADMIN_TLS_KEY_FILE`
- before writing, a backup `.env.bak.<timestamp>` is created automatically
- `spinner tui` also runs the same fill-if-empty startup sync automatically (when `ops/caddy/pki` exists locally)

For remote/local TUI usage, point the TUI to the admin endpoint:
- `SPINNER_ADMIN_API_URL=https://admin.<your-domain>`
- Optional client cert auth:
  - `SPINNER_ADMIN_TLS_CA_FILE=...`
  - `SPINNER_ADMIN_TLS_CERT_FILE=...`
  - `SPINNER_ADMIN_TLS_KEY_FILE=...`
  - `SPINNER_ADMIN_TLS_SKIP_VERIFY=false`

Rotate these artifacts before production use.

## Docs

- Overlord/admin index: `docs/README.md`
- Install runbook: `docs/install.md`
- Configuration reference: `docs/configuration.md`
- Operations runbook: `docs/operations.md`
- Production checklist: `docs/production-checklist.md`
- Product requirements: `docs/prd.md`
- Project brief: `docs/brief.md`
- Channel setup guides: `docs/channels/README.md`
- Security checklist: `ops/security.md`
