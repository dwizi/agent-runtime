# agent-runtime

Security-first, cloud-agnostic, multi-channel agent orchestrator for communities.

`agent-runtime` runs as Docker Compose with:
- `caddy` for TLS, reverse proxy, and admin mTLS.
- `agent-runtime` (Go) for gateway, orchestration, task engine, and markdown indexing triggers.

## What is included in this scaffold

- Go CLI with:
  - `agent-runtime serve` for API + runtime services
  - `agent-runtime tui` for a Charm Bubble Tea admin interface
- SQLite bootstrap and initial schema (`users`, `identities`, `workspaces`, `contexts`, `tasks`, `pairing_requests`)
- Discord gateway connector (`MESSAGE_CREATE` + `INTERACTION_CREATE`) with command routing and DM pairing
- Telegram command gateway with pairing and task/admin commands
- IMAP inbox connector for inbound email ingestion to workspace Markdown
- In-memory task engine with configurable per-workspace concurrency defaults
- Markdown file watcher for `.md` updates under `/data/workspaces`
- qmd-backed retrieval with per-workspace index isolation and debounced re-indexing
- Caddy reverse proxy with separate public/admin hosts and admin mTLS

## Quickstart

1. Copy env:
   - `cp .env.example .env`
2. Install qmd locally only if you run `agent-runtime` outside Docker (`make run`, `make tui`):
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
3. Admin runs `agent-runtime tui`, pastes token, and presses:
   - `[` / `]` to choose role (`overlord`, `admin`, `operator`, `member`, `viewer`)
   - `a` to approve and link identity with selected role
   - `d` to deny

CLI pairing flow for Codex channel:
- one-shot bootstrap admin pairing for the active CLI identity:
  - `agent-runtime chat pairing pair-admin --connector codex --external-id codex-cli --from-user-id codex-cli --role admin`
- manual lifecycle:
  - `agent-runtime chat pairing start --connector codex --external-id codex-cli --from-user-id codex-cli`
  - `agent-runtime chat pairing lookup <token>`
  - `agent-runtime chat pairing approve <token> --approver-user-id <admin-user-id> --role admin`
  - `agent-runtime chat pairing deny <token> --approver-user-id <admin-user-id> --reason "<reason>"`

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
- Natural language command intents are supported, for example:
  - `please create a task to write release notes`
  - `show pending actions`
  - `approve action act_xxx`
  - `yes, i approve it` (admin/overlord; works when exactly one pending action exists in channel)
  - `deny it because unsafe command` (admin/overlord; same single-pending-action rule)
  - `deny pairing token ABCDEF1234 because duplicate`
  - `search for release notes`
  - `open file docs/operations.md`
  - `what is the qmd status?`
  - `set prompt to ...`
  - `enable admin channel`
- `.md` attachments are ingested into workspace storage under `inbox/telegram/<chat-id>/...`
- every inbound/outbound channel message is persisted to Markdown at `logs/chats/telegram/<chat-id>.md`
- LLM replies (GLM Flash 4.7 via z.ai):
  - replies in DM chats
  - replies in group chats only when bot is mentioned (e.g. `@your_bot_username`)
- startup command sync:
  - Agent Runtime calls Telegram `setMyCommands` on connector startup
  - Telegram menu commands are generated from Agent Runtime's shared command catalog
  - command names are normalized to Telegram format (for example `admin-channel` becomes `admin_channel`)
  - advanced text commands remain available even if not present in the Telegram menu

Discord command gateway:
- Listens to Discord Gateway `MESSAGE_CREATE` events (bot token + intents required)
- Listens to Discord Gateway `INTERACTION_CREATE` events for slash-command execution
- Uses the same command set as Telegram from plain message content (`/task`, `/search`, `/open`, `/status`, `/prompt`, `/admin-channel enable`, `/route`, `/approve`, `/deny`, `/pending-actions`, `/approve-action`, `/deny-action`)
- Uses the same natural-language command intents as Telegram (tasking, approvals, qmd search/open/status, prompt/admin controls)
- Supports DM `pair` for one-time token generation
- `.md` attachments are ingested into workspace storage under `inbox/discord/<channel-id>/...`
- every inbound/outbound channel message is persisted to Markdown at `logs/chats/discord/<channel-id>.md`
- LLM replies (GLM Flash 4.7 via z.ai):
  - replies in DMs
  - replies in guild channels only when the bot is tagged/mentioned
- startup command sync:
  - Agent Runtime upserts Discord application commands on connector startup
  - by default, commands are registered globally
  - for immediate availability, set `AGENT_RUNTIME_DISCORD_COMMAND_GUILD_IDS` to target guild IDs
  - `AGENT_RUNTIME_DISCORD_APPLICATION_ID` can be set explicitly; otherwise Agent Runtime resolves it through Discord API
  - advanced text commands remain available even if not present in slash registration

Command surface matrix:

| Command | Telegram menu | Discord slash | Text command parsing |
| --- | --- | --- | --- |
| `task` | yes (`/task`) | yes (`/task`) | yes |
| `search` | yes (`/search`) | yes (`/search`) | yes |
| `open` | yes (`/open`) | yes (`/open`) | yes |
| `status` | yes (`/status`) | yes (`/status`) | yes |
| `monitor` | yes (`/monitor`) | yes (`/monitor`) | yes |
| `admin-channel` | yes (`/admin_channel`) | yes (`/admin-channel`) | yes |
| `prompt` | yes (`/prompt`) | yes (`/prompt`) | yes |
| `approve` | yes (`/approve`) | yes (`/approve`) | yes |
| `deny` | yes (`/deny`) | yes (`/deny`) | yes |
| `pending-actions` | yes (`/pending_actions`) | yes (`/pending-actions`) | yes |
| `approve-action` | yes (`/approve_action`) | yes (`/approve-action`) | yes |
| `deny-action` | yes (`/deny_action`) | yes (`/deny-action`) | yes |
| `pair` | yes (`/pair`, DM flow) | no | yes (DM flow) |
| `route` | yes (`/route`) | yes (`/route`) | yes (admin text command) |

Notes:
- Telegram command names use underscores due to Telegram command naming rules.
- Discord slash registration covers the shared command catalog above.
- Natural-language intents (for example, `please create a task ...`) are text parsing features, not slash/menu commands.

LLM runtime env:
- `AGENT_RUNTIME_LLM_PROVIDER` (default: `openai`; selects the adapter that handles OpenAI-compatible APIs, while `anthropic` routes calls to Claude)
- `AGENT_RUNTIME_LLM_BASE_URL` (default: `https://api.openai.com/v1`; point it at any OpenAI-compatible endpoint or Claude base URL)
- `AGENT_RUNTIME_LLM_API_KEY` (provider key for OpenAI/Z.ai/Claude; leave empty for unauthenticated local endpoints)
- `AGENT_RUNTIME_LLM_MODEL` (default: `gpt-4o`; can override with Z.ai/Claude/local model names such as `glm-4.7-flash`, `claude-3.5-sonic`, or `qwen2.5`)
- `AGENT_RUNTIME_LLM_TIMEOUT_SECONDS` (default: `60`; request timeout per attempt)
- `AGENT_RUNTIME_LLM_GROUNDING_TOP_K` (default: `3`; qmd results pulled when grounding runs)
- `AGENT_RUNTIME_LLM_GROUNDING_MAX_DOC_EXCERPT_BYTES` (default: `1200`; max excerpt size per grounded document)
- `AGENT_RUNTIME_LLM_GROUNDING_MAX_PROMPT_BYTES` (default: `8000`; cap for grounded prompt expansion)
- `AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_LINES` (default: `24`; max chat-tail lines for memory grounding)
- `AGENT_RUNTIME_LLM_GROUNDING_CHAT_TAIL_BYTES` (default: `1800`; byte cap for chat-tail memory grounding)
- `AGENT_RUNTIME_AGENT_GROUNDING_FIRST_STEP` (default: `true`; allows grounding in step 1 of think-act loop)
- `AGENT_RUNTIME_AGENT_GROUNDING_EVERY_STEP` (default: `false`; if true, applies grounding on every loop step)
Notes:
- Setting `AGENT_RUNTIME_LLM_PROVIDER=openai` routes LLM calls through `internal/llm/openai`, which can talk to any OpenAI-compatible endpoint (OpenAI itself, Z.ai, Ollama, vLLM, etc.).
- Run locally against Ollama or vLLM by pointing `AGENT_RUNTIME_LLM_BASE_URL` at your local server (`http://localhost:11434/v1`) and updating `AGENT_RUNTIME_LLM_MODEL` accordingly.
- Use Z.ai by keeping `AGENT_RUNTIME_LLM_PROVIDER=openai`, setting `AGENT_RUNTIME_LLM_BASE_URL=https://api.z.ai/api/paas/v4`, `AGENT_RUNTIME_LLM_MODEL=glm-4.7-flash`, and supplying `AGENT_RUNTIME_LLM_API_KEY`.
- Switch to Claude by setting `AGENT_RUNTIME_LLM_PROVIDER=anthropic`, providing `AGENT_RUNTIME_LLM_API_KEY`, and picking a Claude model name (for example `claude-3.5-sonic`).

Command sync runtime env:
- `AGENT_RUNTIME_COMMAND_SYNC_ENABLED` (default: `true`)
- `AGENT_RUNTIME_DISCORD_APPLICATION_ID` (optional explicit app id for command registration)
- `AGENT_RUNTIME_DISCORD_COMMAND_GUILD_IDS` (optional CSV guild list for fast guild-scoped command propagation)

Codex proactive publish runtime env:
- `AGENT_RUNTIME_CODEX_PUBLISH_URL` (optional HTTP endpoint for outbound Codex notifications)
- `AGENT_RUNTIME_CODEX_PUBLISH_BEARER_TOKEN` (optional bearer token for that endpoint)
- `AGENT_RUNTIME_CODEX_PUBLISH_TIMEOUT_SECONDS` (default: `8`)

IMAP runtime env:
- `AGENT_RUNTIME_IMAP_HOST` (required to enable IMAP connector)
- `AGENT_RUNTIME_IMAP_PORT` (default: `993`)
- `AGENT_RUNTIME_IMAP_USERNAME`
- `AGENT_RUNTIME_IMAP_PASSWORD`
- `AGENT_RUNTIME_IMAP_MAILBOX` (default: `INBOX`)
- `AGENT_RUNTIME_IMAP_POLL_SECONDS` (default: `60`)
- `AGENT_RUNTIME_IMAP_TLS_SKIP_VERIFY` (default: `false`)

IMAP ingestion behavior:
- unread emails are polled from the configured mailbox
- each message is written to workspace Markdown under `inbox/imap/<mailbox>/<uid>-<subject>.md`
- `.md` email attachments are extracted to `inbox/imap/<mailbox>/attachments/`
- dedupe prevents re-ingesting already-processed messages (`uid`/`message-id`)
- each ingested message queues a review task in the orchestrator

Objective scheduler runtime env:
- `AGENT_RUNTIME_OBJECTIVE_POLL_SECONDS` (default: `15`)
- `AGENT_RUNTIME_TASK_RECOVERY_RUNNING_STALE_SECONDS` (default: `600`)

Heartbeat runtime env:
- `AGENT_RUNTIME_HEARTBEAT_ENABLED` (default: `true`)
- `AGENT_RUNTIME_HEARTBEAT_INTERVAL_SECONDS` (default: `30`)
- `AGENT_RUNTIME_HEARTBEAT_STALE_SECONDS` (default: `120`)
- `AGENT_RUNTIME_HEARTBEAT_NOTIFY_ADMIN` (default: `true`)

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
- detailed lifecycle, payloads, and troubleshooting: `docs/objectives-flow.md`
- objectives can be stored as:
  - `trigger_type: "schedule"` with `cron_expr` (and optional `next_run_unix`)
  - `trigger_type: "event"` with `event_key` (currently `markdown.updated`)
- scheduler polls due schedule objectives and enqueues `objective` tasks
- scheduled objective runs are idempotent via a persisted per-run key (`objective:<objective-id>:<scheduled-unix>`)
- markdown file changes can trigger event objectives for the changed workspace
- task recovery on startup:
  - persisted `queued` tasks are re-enqueued into the in-memory worker engine
  - persisted `running` tasks older than `AGENT_RUNTIME_TASK_RECOVERY_RUNNING_STALE_SECONDS` are re-queued and replayed
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
- `AGENT_RUNTIME_SMTP_HOST` (required for SMTP email execution)
- `AGENT_RUNTIME_SMTP_PORT` (default: `587`)
- `AGENT_RUNTIME_SMTP_USERNAME` (optional)
- `AGENT_RUNTIME_SMTP_PASSWORD` (required when username is set)
- `AGENT_RUNTIME_SMTP_FROM` (default sender, can be overridden by approved action payload `from`)

Sandbox command runtime env:
- `AGENT_RUNTIME_SANDBOX_ENABLED` (default: `true`)
- `AGENT_RUNTIME_SANDBOX_ALLOWED_COMMANDS` CSV allowlist (default: `echo,cat,ls,curl,grep,head,tail`)
- `AGENT_RUNTIME_SANDBOX_RUNNER_COMMAND` optional wrapper binary for isolation (e.g. `just-bash`)
- `AGENT_RUNTIME_SANDBOX_RUNNER_ARGS` optional runner arguments (space separated)
- `AGENT_RUNTIME_SANDBOX_TIMEOUT_SECONDS` (default: `20`)

LLM grounding:
- before generating a reply, Agent Runtime runs qmd search in the current workspace and injects top Markdown context snippets/excerpts into the prompt.

Prompt policies and skill templates:
- context prompt policy is stored in `contexts.system_prompt` and can be managed with `/prompt ...`
- default baseline prompts:
  - `AGENT_RUNTIME_LLM_ADMIN_SYSTEM_PROMPT`
  - `AGENT_RUNTIME_LLM_PUBLIC_SYSTEM_PROMPT`
- optional file-based system prompts:
  - `AGENT_RUNTIME_SYSTEM_PROMPT_GLOBAL_FILE`
  - `AGENT_RUNTIME_SYSTEM_PROMPT_WORKSPACE_REL_PATH`
  - `AGENT_RUNTIME_SYSTEM_PROMPT_CONTEXT_REL_PATH`
  - `AGENT_RUNTIME_SKILLS_GLOBAL_ROOT`
- system prompt file precedence:
  - global (`AGENT_RUNTIME_SYSTEM_PROMPT_GLOBAL_FILE`)
  - workspace override (`/data/workspaces/<workspace>/` + `AGENT_RUNTIME_SYSTEM_PROMPT_WORKSPACE_REL_PATH`)
  - context override (`/data/workspaces/<workspace>/` + `AGENT_RUNTIME_SYSTEM_PROMPT_CONTEXT_REL_PATH`, `{context_id}` replaced)
- skill templates are loaded from workspace and global paths:
  - workspace: `skills/contexts/<context-id>/*.md`, `skills/admin/*.md` or `skills/public/*.md`, `skills/common/*.md`
  - global: `<AGENT_RUNTIME_SKILLS_GLOBAL_ROOT>/contexts/<context-id>/*.md`, `<AGENT_RUNTIME_SKILLS_GLOBAL_ROOT>/admin|public/*.md`, `<AGENT_RUNTIME_SKILLS_GLOBAL_ROOT>/common/*.md`
  - workspace templates win over global templates when filenames match
  - starter global templates are included in `context/skills/common` and `context/skills/admin`

LLM safety controls:
- `AGENT_RUNTIME_LLM_ENABLED` toggles all LLM replies
- `AGENT_RUNTIME_LLM_ALLOW_DM` allows/blocks DM LLM replies
- `AGENT_RUNTIME_LLM_REQUIRE_MENTION_IN_GROUPS` requires mention/tag for group/channel replies
- `AGENT_RUNTIME_LLM_ALLOWED_ROLES` optional CSV role allowlist (empty = all roles)
- `AGENT_RUNTIME_LLM_ALLOWED_CONTEXT_IDS` optional CSV context allowlist (empty = all contexts)
- `AGENT_RUNTIME_LLM_RATE_LIMIT_PER_WINDOW` and `AGENT_RUNTIME_LLM_RATE_LIMIT_WINDOW_SECONDS` configure LLM reply limits
- rate limit applies to non-admin users only (`admin`, `overlord` bypass)

LLM action approvals:
- when the LLM includes an action proposal block:
  - ```action {"type":"...","target":"...","summary":"..."} ```
- Agent Runtime stores it as a pending approval in SQLite and posts an approval notice with action id.
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
- `admin-client.p12` (password: `agent-runtime`)

Local helper:
- `make compose-up` or `make sync-env` fills these vars in `.env` only when empty:
  - `AGENT_RUNTIME_ADMIN_TLS_CA_FILE`
  - `AGENT_RUNTIME_ADMIN_TLS_CERT_FILE`
  - `AGENT_RUNTIME_ADMIN_TLS_KEY_FILE`
- before writing, a backup `.env.bak.<timestamp>` is created automatically
- `agent-runtime tui` also runs the same fill-if-empty startup sync automatically (when `ops/caddy/pki` exists locally)

For remote/local TUI usage, point the TUI to the admin endpoint:
- `AGENT_RUNTIME_ADMIN_API_URL=https://admin.<your-domain>`
- Optional client cert auth:
  - `AGENT_RUNTIME_ADMIN_TLS_CA_FILE=...`
  - `AGENT_RUNTIME_ADMIN_TLS_CERT_FILE=...`
  - `AGENT_RUNTIME_ADMIN_TLS_KEY_FILE=...`
  - `AGENT_RUNTIME_ADMIN_TLS_SKIP_VERIFY=false`

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
